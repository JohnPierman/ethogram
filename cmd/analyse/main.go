// Command analyse derives evaluation results from a replay run's result JSON. It
// reads results, never the corpus, and every file it writes cites the parent run, so
// the provenance chain from figure to corpus bytes stays unbroken.
//
// Three things are derived:
//
// E3 (§12.3): Benjamini–Hochberg at each nominal q over each scored day's combined
// p-values, with realised FDR against nominal. §10.2 predicts conservatism (realised
// below nominal) because the discrete statistics of §6 and §8 are super-uniform; the
// magnitude is the result, not a failure. §10.3's dependence caveat is honoured by
// reporting Benjamini–Yekutieli alongside, which is valid under arbitrary dependence.
//
// E4 and E9 (§12.3): the ablation arms. Both are measured on the same events as the
// primary arm, so they are paired data, and the comparison is McNemar's test on the
// discordant pairs plus a paired bootstrap on the difference in detections. An
// unpaired comparison here would use the wrong variance and answer a different
// question.
//
// E1 and E2 (§12.4): detection at matched alert budget, every proportion carrying n
// with Wilson and Clopper–Pearson intervals.
//
// Two limits are recorded rather than smoothed over. The replay retains only the top
// K alerts per day, so a step-up threshold falling beyond K is reported as a
// saturated day rather than silently truncated. And LANL's red team is the positive
// class while unlabelled events are treated as negatives, so an alerted unlabelled
// event counts as a false discovery even though the corpus cannot establish that it
// is benign; §12.5 lists the absence of a true negative class as a threat, and every
// realised-FDR figure here inherits it.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/JohnPierman/ethogram/domain/objective"
	"github.com/JohnPierman/ethogram/domain/statistics"
)

func main() {
	var (
		runPath    = flag.String("run", "", "replay result JSON (required)")
		outPath    = flag.String("out", "", "derived result JSON path (required)")
		runID      = flag.String("run-id", "", "identifier for this analysis (required)")
		bootstraps = flag.Int("bootstraps", 2000, "paired bootstrap resamples")
		seed       = flag.Uint64("seed", 20260814, "bootstrap seed, recorded in the output")
		baselines  = flag.String("baselines", "", "baselines result JSON (§12.4); enables the head-to-head and per-category comparisons")
		budgetSpec = flag.String("budgets", "10,25,50,100", "comma-separated per-day alert budgets to evaluate, ascending order not required")
		valueRatio = flag.Float64("value-ratio", 0, "value of one true positive in units of the cost of one false positive (v/c) for the objective U = v*TP - FP. Zero leaves the objective unscored and reports only the parameter-free break-even ratio, which is the honest default: a v/c chosen after seeing a result is not a threshold")
	)
	flag.Parse()
	if *runPath == "" || *outPath == "" || *runID == "" {
		log.Fatal("-run, -out and -run-id are required")
	}

	// Both new parameters are validated here, at the boundary, so a malformed exchange
	// rate or budget list fails before the run's results are read rather than part way
	// through writing a derived result.
	budgets, err := objective.ParseBudgets(*budgetSpec)
	if err != nil {
		log.Fatal(err)
	}
	cfg := analysisConfig{
		runPath: *runPath, outPath: *outPath, runID: *runID,
		bootstraps: *bootstraps, seed: *seed, baselinesPath: *baselines,
		budgets: budgets,
	}
	// The objective is scored only when the operator states an exchange rate.
	if *valueRatio > 0 {
		u, uErr := objective.NewUtility(*valueRatio)
		if uErr != nil {
			log.Fatal(uErr)
		}
		cfg.utility = &u
	}

	if err := analyse(cfg); err != nil {
		log.Fatal(err)
	}
}

// analysisConfig is everything one analysis run needs. A struct rather than a parameter
// list: the objective added two more, and the call was already at six.
type analysisConfig struct {
	runPath       string
	outPath       string
	runID         string
	bootstraps    int
	seed          uint64
	baselinesPath string

	// budgets are the per-day alert budgets to evaluate, ascending.
	budgets objective.Budgets

	// utility is the objective, or nil when no exchange rate was supplied, in which case
	// only the parameter-free break-even ratio is reported.
	utility *objective.Utility
}

// alertRow is one retained alert.
type alertRow struct {
	P float64 `json:"p"`
	// LogP is ln P. It is what rows are ordered and thresholded on, because P
	// underflows to zero across the whole region alerts are drawn from and ties every
	// event there to every other.
	LogP      float64 `json:"log_p"`
	TSeconds  int64   `json:"t"`
	Entity    string  `json:"entity"`
	IsRedTeam bool    `json:"is_red_team"`
}

// eventKey identifies a scored event for pairing across arms.
type eventKey struct {
	t      int64
	entity string
}

// arm is one set of per-day alerts, from the primary run or from an ablation.
type arm struct {
	name   string
	perDay map[int64][]alertRow
}

// redTeamDetectedAt returns the set of red-team events this arm alerts on at the
// given per-day budget: the day's b smallest combined p-values.
func (a arm) redTeamDetectedAt(budget int) map[eventKey]bool {
	out := map[eventKey]bool{}
	for _, rows := range a.perDay {
		n := budget
		if n > len(rows) {
			n = len(rows)
		}
		for _, r := range rows[:n] {
			if r.IsRedTeam {
				out[eventKey{r.TSeconds, r.Entity}] = true
			}
		}
	}
	return out
}

// redTeamScored returns every red-team event this arm scored, in a deterministic
// order, which is the population the paired tests range over.
func (a arm) redTeamScored() []eventKey {
	seen := map[eventKey]bool{}
	for _, rows := range a.perDay {
		for _, r := range rows {
			if r.IsRedTeam {
				seen[eventKey{r.TSeconds, r.Entity}] = true
			}
		}
	}
	out := make([]eventKey, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].t != out[j].t {
			return out[i].t < out[j].t
		}
		return out[i].entity < out[j].entity
	})
	return out
}

func analyse(cfg analysisConfig) error {
	runPath, outPath, runID := cfg.runPath, cfg.outPath, cfg.runID
	bootstraps, seed, baselinesPath := cfg.bootstraps, cfg.seed, cfg.baselinesPath
	budgets, utility := cfg.budgets, cfg.utility

	started := time.Now().UTC()

	raw, err := os.ReadFile(runPath) //nolint:gosec // the result file the flag names
	if err != nil {
		return err
	}
	var run map[string]any
	if unmarshalErr := json.Unmarshal(raw, &run); unmarshalErr != nil {
		return unmarshalErr
	}
	parentRun, _ := run["run"].(map[string]any)
	parentID, _ := parentRun["run_id"].(string)
	if parentID == "" {
		return fmt.Errorf("%s carries no run_id; refusing to derive results from an unattributed run", runPath)
	}
	results, _ := run["results"].(map[string]any)

	primary, err := loadArm(results, "alerts_per_day", "framework")
	if err != nil {
		return err
	}
	if len(primary.perDay) == 0 {
		return fmt.Errorf("%s has no alerts_per_day; nothing to analyse", runPath)
	}

	// Exact per-day test counts when the run recorded them; otherwise the run-wide
	// mean, with the substitution recorded so no reader mistakes it for exact.
	perDayM, mSource := loadPerDayM(run, results, primary)

	days := make([]int64, 0, len(primary.perDay))
	for d := range primary.perDay {
		days = append(days, d)
	}
	sort.Slice(days, func(i, j int) bool { return days[i] < days[j] })

	topK := 200
	if params, ok := run["parameters"].(map[string]any); ok {
		if v, ok := params["top_k_per_day"].(float64); ok {
			topK = int(v)
		}
	}

	calibrationBH := calibrate(primary, days, perDayM, topK, false)
	calibrationBY := calibrate(primary, days, perDayM, topK, true)

	// The complete labelled population, which is the denominator every recall below is
	// measured against, and the carrier of each event's structural categories.
	pop := loadRedTeamPopulation(results, primary)

	// The run retained only its top-k alerts per day, so a budget above k cannot be
	// answered: the day's list runs out and the shortfall reads as a queue that found
	// nothing rather than as a question this run cannot answer.
	if budgets.Max() > topK {
		return fmt.Errorf("analyse: budget %d exceeds the run's retained %d alerts per day; "+
			"re-run the replay with -topk at least %d", budgets.Max(), topK, budgets.Max())
	}

	detection := detectionTable(primary, pop, budgets, utility)

	// How much of each budget is actually worth emitting. A budget is a ceiling; with only
	// a few hundred labelled events in the corpus, most of what a large one permits is
	// cost without return, and the objective can say how much.
	cutoffArms := []namedArm{{
		name:        "composite",
		combination: "Fisher (18) with Brown's correction (19)",
		arm:         primary,
	}}
	// The Sidak-over-minimum arm records its own alert list, so it can be cut off too.
	// It is the arm that actually detects on this corpus, which makes it the one whose
	// truncation is worth reading.
	if block, ok := results["min_p_arm"].(map[string]any); ok {
		if minP, armErr := loadArm(block, "alerts_per_day", "min-p"); armErr == nil && len(minP.perDay) > 0 {
			combination, _ := block["combination"].(string)
			cutoffArms = append(cutoffArms, namedArm{
				name: "min-p", combination: combination, arm: minP,
			})
		}
	}
	cutoffs := cutoffTable(cutoffArms, pop, budgets, utility)

	// The head-to-head against the §12.4 baselines, in aggregate and per category of
	// anomaly: what E1 and E2 exist to answer.
	var (
		headToHeadRows []headToHead
		categoryRows   []categoryRow
		headline       string
		baselineMeta   map[string]any
	)
	if baselinesPath != "" {
		braw, berr := os.ReadFile(baselinesPath) //nolint:gosec // the result file the flag names
		if berr != nil {
			return fmt.Errorf("read baselines: %w", berr)
		}
		var bdata map[string]any
		if uerr := json.Unmarshal(braw, &bdata); uerr != nil {
			return fmt.Errorf("parse baselines: %w", uerr)
		}
		arms, notes := loadBaselineArms(bdata)

		// The asymmetry between the two arms' thresholds is a property of the
		// comparison, not a detail. The framework's budget is the exact per-day top-b
		// over the full scored population; each baseline's threshold is a quantile
		// estimated from the sidecar's uniform sample. Both estimate the same operating
		// point, only one does so exactly, and a reader deciding how much to credit the
		// gap needs to know which.
		caveats := append([]string{
			"the framework's per-day budget is exact (the day's b smallest combined " +
				"p-values over the full scored population); each baseline's threshold is a " +
				"quantile estimated from a deterministic 1-in-100 sample, so the two arms " +
				"estimate the same operating point with different precision",
			"the baseline feature encoding is the conventional one for these detectors " +
				"(hashed categoricals, hour and weekday fractions) and was not tuned in " +
				"their favour; a different encoding would move their results",
			"Detector IV inside the framework is itself a marginal outlier detector over " +
				"the population, so the population_rare category is in part the framework " +
				"measured against one of its own components (§12.4)",
			"the categories are structural properties of each event relative to its " +
				"history, assigned from evidence and not from which detector scored it " +
				"lowest; an event may belong to more than one category, so the rows are " +
				"overlapping subsets of the labelled population and do not sum to it",
		}, notes...)

		headToHeadRows = compareToBaselines(primary, pop, arms, budgets, bootstraps, seed, caveats)
		categoryRows = categoryComparison(primary, pop, arms, budgets, bootstraps, seed)
		headline = headlineSentence(headToHeadRows)

		bid, _ := strAtLocal(bdata, "run", "run_id")
		baselineMeta = map[string]any{
			"source_file": baselinesPath,
			"run_id":      bid,
			"models":      baselineNames(arms),
			"caveats":     caveats,
		}
	}

	// The ablation arms, when the run carried them.
	ablations := map[string]any{}
	for _, ab := range []struct {
		key, block, name, hypothesis string
	}{
		{"e4", "e4_partitioned_arm", "cooccurrence-partitioned (14)", "E4"},
		{"e9", "e9_cell_arm", "timing-cells-168 (§7.1)", "E9"},
	} {
		block, ok := results[ab.block].(map[string]any)
		if !ok {
			continue
		}
		armRows, armErr := loadArm(block, "alerts_per_day", ab.name)
		if armErr != nil || len(armRows.perDay) == 0 {
			// The arm recorded detections but not its per-day alert sets; the paired
			// test needs the alert sets, so it is reported as unavailable rather than
			// approximated from the counts.
			ablations[ab.key] = map[string]any{
				"hypothesis": ab.hypothesis,
				"arm":        ab.name,
				"paired_tests": "unavailable: the arm's per-day alert sets are not in the " +
					"result file, and a paired test cannot be reconstructed from counts alone",
				"detections_at_budget": block["detections_at_budget"],
			}
			continue
		}
		ablations[ab.key] = pairedComparison(primary, armRows, budgets, bootstraps, seed, ab)
	}

	out := map[string]any{
		"schema_version": "1",
		"kind":           "analysis",
		"hypothesis":     hypothesesFor(ablations),
		"paper_refs": map[string]any{
			"sections":  []string{"§10.2", "§10.3", "§12.3", "§12.4", "§12.5"},
			"equations": []int{18},
		},
		"run": map[string]any{
			"run_id":      runID,
			"parent_run":  parentID,
			"parent_file": runPath,
			"started_at":  started.Format(time.RFC3339),
			"finished_at": time.Now().UTC().Format(time.RFC3339),
			"go_version":  runtime.Version(),
		},
		"corpus": run["corpus"],
		"parameters": map[string]any{
			"q_grid":              []float64{0.001, 0.005, 0.01, 0.05, 0.10, 0.25},
			"budgets":             []int(budgets),
			"objective":           objectiveProvenance(utility),
			"bh_denominator":      mSource,
			"top_k_per_day":       topK,
			"bootstrap_resamples": bootstraps,
			"bootstrap_seed":      seed,
			"ground_truth_caveat": "unlabelled events count as negatives; the corpus has no " +
				"true negative class (§12.5), so realised FDR is an upper bound",
		},
		"results": map[string]any{
			"calibration_bh": calibrationBH,
			"calibration_by": calibrationBY,
			"detection":      detection,
			"utility_cutoff": cutoffs,
			"gap":            gapTable(primary, pop, budgets),
			"red_team_population": map[string]any{
				"n":      pop.size(),
				"source": pop.source,
			},
			"category_census":     categoryCensus(pop),
			"category_comparison": categoryRows,
			"head_to_head":        headToHeadRows,
			"headline":            headline,
			"baseline_source":     baselineMeta,
			"ablations":           ablations,
			"prediction": "§10.2 predicts realised FDR below nominal q (super-uniform discrete " +
				"statistics under Fisher); the conservatism ratio quantifies it",
		},
		"provenance_complete": run["provenance_complete"],
	}

	if err := writeJSON(outPath, out); err != nil {
		return err
	}
	if headline != "" {
		log.Printf("HEADLINE: %s", headline)
	}
	for _, p := range calibrationBH {
		log.Printf("BH q=%.3f: discoveries=%d tp=%d realised FDR=%.4f [%.4f, %.4f] "+
			"conservatism=%.2f saturated_days=%d",
			p.NominalQ, p.Discoveries, p.TruePositives, p.RealisedFDR.Point,
			p.RealisedFDR.Low, p.RealisedFDR.High, p.Conservatism, p.SaturatedDays)
	}
	return nil
}

// loadArm reads a per-day alert map from a results block.
func loadArm(block map[string]any, key, name string) (arm, error) {
	a := arm{name: name, perDay: map[int64][]alertRow{}}
	rawMap, ok := block[key].(map[string]any)
	if !ok {
		return a, nil
	}
	for dayName, v := range rawMap {
		b, err := json.Marshal(v)
		if err != nil {
			return a, err
		}
		var rows []alertRow
		if unmarshalErr := json.Unmarshal(b, &rows); unmarshalErr != nil {
			return a, unmarshalErr
		}
		day, err := strconv.ParseInt(strings.TrimPrefix(dayName, "day_"), 10, 64)
		if err != nil {
			return a, fmt.Errorf("unparseable day key %q: %w", dayName, err)
		}
		// The replay writes them ascending in p; sorting here makes the analysis
		// independent of that guarantee.
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].LogP != rows[j].LogP {
				return rows[i].LogP < rows[j].LogP
			}
			return rows[i].TSeconds < rows[j].TSeconds
		})
		a.perDay[day] = rows
	}
	return a, nil
}

// loadPerDayM returns the BH denominator per day and a description of where it came
// from.
func loadPerDayM(run, results map[string]any, primary arm) (map[int64]float64, string) {
	m := map[int64]float64{}
	if exact, ok := results["scored_per_day"].(map[string]any); ok && len(exact) > 0 {
		for dayName, v := range exact {
			day, err := strconv.ParseInt(strings.TrimPrefix(dayName, "day_"), 10, 64)
			if err != nil {
				continue
			}
			if n, ok := v.(float64); ok {
				m[day] = n
			}
		}
		if len(m) > 0 {
			return m, "exact per-day scored-event counts recorded by the run"
		}
	}
	// Fallback: the run predates per-day counts. The run-wide mean is used and the
	// substitution is recorded, because it shifts every realised-FDR figure derived
	// from it.
	scored, _ := run["corpus"].(map[string]any)["events_scored"].(float64)
	days := float64(len(primary.perDay))
	mean := 0.0
	if days > 0 {
		mean = scored / days
	}
	for d := range primary.perDay {
		m[d] = mean
	}
	return m, fmt.Sprintf("APPROXIMATE: run-wide mean of %.0f scored events per day; the "+
		"run did not record exact per-day counts", mean)
}

type calibrationPoint struct {
	NominalQ      float64             `json:"nominal_q"`
	Procedure     string              `json:"procedure"`
	Discoveries   int                 `json:"discoveries"`
	TruePositives int                 `json:"true_positives"`
	RealisedFDR   statistics.Interval `json:"realised_fdr"`
	Conservatism  float64             `json:"conservatism_ratio"`
	SaturatedDays int                 `json:"saturated_days"`
}

// calibrate applies the step-up procedure per day and aggregates realised FDR.
func calibrate(a arm, days []int64, perDayM map[int64]float64, topK int, harmonic bool) []calibrationPoint {
	procedure := "benjamini-hochberg"
	if harmonic {
		procedure = "benjamini-yekutieli"
	}
	qGrid := []float64{0.001, 0.005, 0.01, 0.05, 0.10, 0.25}
	points := make([]calibrationPoint, 0, len(qGrid))

	for _, q := range qGrid {
		totalDisc, totalTP, saturated := 0, 0, 0
		for _, d := range days {
			rows := a.perDay[d]
			m := perDayM[d]
			if m <= 0 {
				continue
			}
			effQ := q
			if harmonic {
				// BY divides by the harmonic number over the day's test count.
				effQ = q / harmonicNumber(int(m))
			}
			// The step-up condition in log space. Comparing p directly would compare
			// zeros: past roughly X² = 1450 at two degrees of freedom the combined
			// tail underflows, and every alert in the retained set reports exactly
			// zero, so every one of them satisfies any threshold and the cut lands on
			// the last row regardless of q.
			logEffQ := math.Log(effQ)
			cut := -1
			for i, r := range rows {
				if r.LogP <= math.Log(float64(i+1)/m)+logEffQ {
					cut = i
				}
			}
			if cut == len(rows)-1 && len(rows) >= topK {
				// The step-up reached the last retained alert: the true threshold may
				// lie beyond K, so this day's discovery count is a lower bound.
				saturated++
			}
			for i := 0; i <= cut; i++ {
				totalDisc++
				if rows[i].IsRedTeam {
					totalTP++
				}
			}
		}
		fp := totalDisc - totalTP
		iv := statistics.WilsonInterval(fp, totalDisc)
		cons := 0.0
		if q > 0 {
			cons = iv.Point / q
		}
		points = append(points, calibrationPoint{
			NominalQ: q, Procedure: procedure,
			Discoveries: totalDisc, TruePositives: totalTP,
			RealisedFDR: iv, Conservatism: cons, SaturatedDays: saturated,
		})
	}
	return points
}

// harmonicNumber returns H_m = Σ_{i=1..m} 1/i, accumulated ascending.
//
// For the corpus-scale m here the series is evaluated by its asymptotic form beyond a
// threshold, which is accurate to better than 1e-12 and avoids millions of additions
// per q per day.
func harmonicNumber(m int) float64 {
	if m <= 0 {
		return 1
	}
	if m <= 1000 {
		h := 0.0
		for i := 1; i <= m; i++ {
			h += 1 / float64(i)
		}
		return h
	}
	// H_m = ln m + γ + 1/(2m) - 1/(12m²) + …
	const euler = 0.5772156649015329
	fm := float64(m)
	return math.Log(fm) + euler + 1/(2*fm) - 1/(12*fm*fm)
}

type detectionRow struct {
	Budget         int                 `json:"budget_per_day"`
	Alerts         int                 `json:"alerts"`
	TruePositives  int                 `json:"true_positives"`
	FalseNegatives int                 `json:"false_negatives"`
	RedTeamScored  int                 `json:"red_team_scored"`
	Recall         statistics.Interval `json:"recall_wilson"`
	RecallExact    statistics.Interval `json:"recall_clopper_pearson"`
	Precision      statistics.Interval `json:"precision_wilson"`

	// FalsePositives is the queue an analyst reads and does not act on. Recorded
	// explicitly rather than left as Alerts - TruePositives, because it is the cost side
	// of the objective and a reader should not have to derive it.
	FalsePositives int `json:"false_positives"`

	// TrueFalseRatio is TP/FP, omitted when there are no false positives because the
	// ratio is unbounded there. Reported for continuity with how the operating points
	// were discussed, not as the objective: it is precision under a monotone transform,
	// so maximising it maximises precision and selects the smallest queue holding a
	// true positive. Objective is what ranks the operating points.
	TrueFalseRatio *float64 `json:"true_false_ratio,omitempty"`

	// BreakEvenValueRatio is the exchange rate at which this operating point starts to
	// pay: FP/TP. Omitted when nothing true was found, because then no exchange rate
	// makes the queue worth reading. It takes no parameter, which is why it is recorded
	// unconditionally while Objective is not.
	BreakEvenValueRatio *float64 `json:"break_even_value_ratio,omitempty"`

	// Objective is U = v*TP - FP in units of one false positive, present only when
	// -value-ratio was supplied. IsWorthwhile reports whether it beats alerting on
	// nothing, which is the comparison a positive U encodes.
	Objective      *float64 `json:"objective_utility,omitempty"`
	IsWorthwhile   *bool    `json:"is_worthwhile,omitempty"`
	IsObjectiveTop *bool    `json:"is_objective_maximum,omitempty"`
}

// objectiveProvenance records the objective and its parameter, so a recorded utility can
// be reproduced and an unscored run is visibly unscored rather than silently absent.
//
// The objective's form is recorded even when no exchange rate was supplied, because the
// break-even ratio in every detection row is a statement about that form.
func objectiveProvenance(utility *objective.Utility) map[string]any {
	out := map[string]any{
		"form":  "U = v*TP - c*FP, scored in units of c",
		"units": "one false positive",
	}
	if utility == nil {
		out["value_ratio"] = nil
		out["scored"] = false
		out["note"] = "no -value-ratio was supplied, so the objective is not scored; each " +
			"detection row still carries the parameter-free break_even_value_ratio, the " +
			"exchange rate at which that operating point begins to pay"
		return out
	}
	out["value_ratio"] = utility.ValueRatio()
	out["scored"] = true
	out["minimum_precision"] = utility.MinimumPrecision()
	return out
}

// detectionTable is E1 and E2's headline: detections at each matched budget with n
// and intervals on every proportion.
func detectionTable(a arm, pop redTeamPopulation, budgets objective.Budgets,
	utility *objective.Utility) []detectionRow {
	// The denominator is every labelled event the run scored, not merely those that
	// reached a retained per-day alert list. The difference is exactly the labelled
	// events scored unremarkably, and those are the ones a recall figure exists to
	// count against us.
	scored := pop.size()
	rows := make([]detectionRow, 0, len(budgets))
	// Candidate operating points for the objective, with the row each came from, so the
	// selected budget can be marked without recomputing anything.
	outcomes := make([]objective.Outcome, 0, len(budgets))
	outcomeAt := make([]int, 0, len(budgets))
	for _, b := range budgets {
		alerts := 0
		for _, day := range a.perDay {
			n := b
			if n > len(day) {
				n = len(day)
			}
			alerts += n
		}
		tp := len(a.redTeamDetectedAt(b))
		row := detectionRow{
			Budget: b, Alerts: alerts, TruePositives: tp,
			FalseNegatives: scored - tp, RedTeamScored: scored,
			FalsePositives: alerts - tp,
			Recall:         statistics.WilsonInterval(tp, scored),
			RecallExact:    statistics.ClopperPearsonInterval(tp, scored),
			Precision:      statistics.WilsonInterval(tp, alerts),
		}

		// The objective's own view of this operating point. A malformed outcome is a
		// defect in the counting above rather than a condition to report, so the
		// objective fields are simply absent if the outcome will not construct.
		if outcome, oErr := objective.NewOutcome(tp, alerts-tp, scored); oErr == nil {
			if ratio, ok := outcome.Ratio(); ok {
				row.TrueFalseRatio = &ratio
			}
			if breakEven, ok := outcome.BreakEvenValueRatio(); ok {
				row.BreakEvenValueRatio = &breakEven
			}
			if utility != nil {
				score := utility.Score(outcome)
				worthwhile := utility.IsWorthwhile(outcome)
				row.Objective, row.IsWorthwhile = &score, &worthwhile
			}
			outcomes = append(outcomes, outcome)
			outcomeAt = append(outcomeAt, len(rows))
		}
		rows = append(rows, row)
	}

	// Which budget the objective actually selects, marked on the row rather than
	// asserted in prose. Ties go to the smaller queue, which is why the candidates are
	// offered in the ascending order Budgets guarantees.
	if utility != nil && len(outcomes) > 0 {
		if best, ok := utility.Best(outcomes); ok {
			top := true
			rows[outcomeAt[best]].IsObjectiveTop = &top
		}
	}
	return rows
}

// pairedComparison runs McNemar and the paired bootstrap between the primary arm and
// an ablation arm, at each budget, over the red-team events both arms scored.
func pairedComparison(primary, other arm, budgets []int, bootstraps int, seed uint64,
	meta struct{ key, block, name, hypothesis string },
) map[string]any {
	// The paired population: red-team events either arm retained. An event missing
	// from an arm's retained alerts was not alerted by that arm at any budget within
	// K, which is a legitimate "not detected".
	population := map[eventKey]bool{}
	for _, k := range primary.redTeamScored() {
		population[k] = true
	}
	for _, k := range other.redTeamScored() {
		population[k] = true
	}
	events := make([]eventKey, 0, len(population))
	for k := range population {
		events = append(events, k)
	}
	sort.Slice(events, func(i, j int) bool {
		if events[i].t != events[j].t {
			return events[i].t < events[j].t
		}
		return events[i].entity < events[j].entity
	})

	perBudget := make([]map[string]any, 0, len(budgets))
	for _, b := range budgets {
		primaryHit := primary.redTeamDetectedAt(b)
		otherHit := other.redTeamDetectedAt(b)

		pa := make([]bool, len(events))
		pb := make([]bool, len(events))
		for i, k := range events {
			pa[i] = primaryHit[k]
			pb[i] = otherHit[k]
		}

		mc := statistics.McNemar(pa, pb)
		bs := statistics.PairedBootstrapDelta(pa, pb, bootstraps, seed)
		perBudget = append(perBudget, map[string]any{
			"budget_per_day":     b,
			"paired_events":      len(events),
			"framework_detected": countTrue(pa),
			"arm_detected":       countTrue(pb),
			"mcnemar":            mc,
			"bootstrap_delta":    bs,
		})
	}

	return map[string]any{
		"hypothesis": meta.hypothesis,
		"arm":        meta.name,
		"design": "paired: both arms scored the same events, so only discordant pairs " +
			"carry evidence (McNemar); the bootstrap resamples events with both arms' " +
			"outcomes travelling together",
		"per_budget": perBudget,
	}
}

func countTrue(b []bool) int {
	n := 0
	for _, v := range b {
		if v {
			n++
		}
	}
	return n
}

func hypothesesFor(ablations map[string]any) []string {
	hs := []string{"E1", "E2", "E3"}
	if _, ok := ablations["e4"]; ok {
		hs = append(hs, "E4")
	}
	if _, ok := ablations["e9"]; ok {
		hs = append(hs, "E9")
	}
	return hs
}

// strAtLocal reads a nested string from the baselines document.
func strAtLocal(m map[string]any, path ...string) (string, bool) {
	cur := m
	for i, k := range path {
		if i == len(path)-1 {
			v, ok := cur[k].(string)
			return v, ok
		}
		next, ok := cur[k].(map[string]any)
		if !ok {
			return "", false
		}
		cur = next
	}
	return "", false
}

func baselineNames(arms []baselineArm) []string {
	out := make([]string, 0, len(arms))
	for _, a := range arms {
		out = append(out, a.name)
	}
	return out
}

func writeJSON(path string, v any) error {
	dir := "."
	if idx := strings.LastIndexAny(path, `/\`); idx >= 0 {
		dir = path[:idx]
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.Create(path) //nolint:gosec // the output the flag names
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", " ")
	if err := enc.Encode(v); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
