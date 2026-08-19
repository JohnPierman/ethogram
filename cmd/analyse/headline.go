package main

import (
	"fmt"
	"math"
	"sort"

	"github.com/JohnPierman/ethogram/domain/statistics"
)

// headToHead compares the framework against each §12.4 baseline at matched alert
// budget: the comparison E1 and E2 exist to make.
//
// The comparison is expressed in PERCENTAGE POINTS of recall, not as a percentage
// improvement. Where a baseline detects nothing, a relative improvement is a division
// by zero and any figure printed for it would be an artefact of the formula rather
// than a measurement; the ratio is therefore reported only when the baseline's own
// count is positive, and the point difference always. This is the same discipline
// §12.4 applies to the budget itself: state the comparison in units that remain
// meaningful at the boundary.
//
// Both arms are measured over the same red-team events, so the paired tests apply:
// McNemar on the discordant pairs, and a paired bootstrap on the difference in
// detections.
type headToHead struct {
	Budget int `json:"budget_per_day"`

	FrameworkDetected int                 `json:"framework_detected"`
	FrameworkRecall   statistics.Interval `json:"framework_recall"`

	BaselineName     string              `json:"baseline"`
	BaselineDetected int                 `json:"baseline_detected"`
	BaselineRecall   statistics.Interval `json:"baseline_recall"`

	RedTeamEvents int `json:"red_team_events"`

	// CommonDays is the window both arms scored, to which the comparison is confined.
	// It is reported because it is the population the numbers above range over, and a
	// reader comparing two rows needs to know they cover the same days.
	CommonDays []int64 `json:"common_days"`

	// DeltaPoints is the framework's recall minus the baseline's, in percentage
	// points: the figure a reader should quote.
	DeltaPoints float64 `json:"delta_percentage_points"`

	// TimesBetter is the ratio of detections, present only when the baseline
	// detected something. Absent is the honest rendering of a zero denominator.
	TimesBetter    *float64 `json:"times_better,omitempty"`
	RatioUndefined string   `json:"ratio_undefined,omitempty"`

	McNemar   statistics.McNemarResult  `json:"mcnemar"`
	Bootstrap statistics.BootstrapDelta `json:"bootstrap_delta"`

	// Caveats travel with the comparison, because they qualify it.
	Caveats []string `json:"caveats"`
}

// baselineArm is a baseline's detections keyed by budget, read from the sidecar
// result. The sidecar records which red-team rows each model alerted on.
type baselineArm struct {
	name string
	// detectedAt[budget] is the set of red-team event keys the model alerted on.
	detectedAt map[int]map[eventKey]bool
	// countAt[budget] is the detection count the sidecar recorded, which is the only
	// figure available when it did not name the rows.
	countAt map[int]int
	// days is the set of corpus days the model actually scored.
	days map[int64]bool
	// total is the number of red-team rows the sidecar scored.
	total int
}

// coveredDays returns the corpus days an arm scored, taken from its retained per-day
// alert lists.
func (a arm) coveredDays() map[int64]bool {
	out := make(map[int64]bool, len(a.perDay))
	for d := range a.perDay {
		out[d] = true
	}
	return out
}

// restrictToCommonDays narrows a labelled population to the days both arms scored.
//
// The two arms need not have been run over the same window — the baselines were scored
// over a longer span than a given replay may cover — and a comparison drawn across the
// union would measure each arm on a different denominator while presenting the two
// numbers side by side. Intersecting the days first is what makes the pairing real: on
// every remaining event, both arms had the opportunity to alert.
func restrictToCommonDays(pop redTeamPopulation, a, b map[int64]bool) (redTeamPopulation, []int64) {
	common := map[int64]bool{}
	for d := range a {
		if b[d] {
			common[d] = true
		}
	}
	out := redTeamPopulation{categories: pop.categories, source: pop.source}
	for _, k := range pop.keys {
		if common[k.t/86400] {
			out.keys = append(out.keys, k)
		}
	}
	days := make([]int64, 0, len(common))
	for d := range common {
		days = append(days, d)
	}
	sort.Slice(days, func(i, j int) bool { return days[i] < days[j] })
	return out, days
}

// detectionsFor returns the model's detections at a budget and whether they are known
// event by event.
//
// A recorded count of zero names its set exactly — the empty set — so a model that
// detected nothing is fully paired despite naming no rows, and needs no caveat. A
// positive count without named rows is a different matter: the events exist but cannot
// be identified, so no paired test and no per-category attribution is possible, and the
// caller must say so rather than assume which events they were.
func (a baselineArm) detectionsFor(budget int) (set map[eventKey]bool, named bool) {
	set = a.detectedAt[budget]
	if set == nil {
		set = map[eventKey]bool{}
	}
	if len(set) > 0 {
		return set, true
	}
	return set, a.countAt[budget] == 0
}

// loadBaselineArms reads the sidecar result into per-model arms.
//
// The sidecar records detections as counts per budget plus the red-team rows it
// scored. Where it records the alerted rows themselves, the pairing is exact; where it
// records only counts, the comparison falls back to unpaired proportions and says so,
// because a paired test cannot be invented from a count.
func loadBaselineArms(data map[string]any) ([]baselineArm, []string) {
	var arms []baselineArm
	var notes []string

	results, ok := data["results"].(map[string]any)
	if !ok {
		return nil, []string{"the baselines result carries no results block"}
	}

	names := make([]string, 0, len(results))
	for name := range results {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		block, ok := results[name].(map[string]any)
		if !ok {
			continue
		}
		det, ok := block["detections_at_budget"].(map[string]any)
		if !ok {
			continue
		}
		arm := baselineArm{
			name:       name,
			detectedAt: map[int]map[eventKey]bool{},
			countAt:    map[int]int{},
			days:       map[int64]bool{},
		}

		for key, v := range det {
			b := budgetFromKey(key)
			if b == 0 {
				continue
			}
			entry, ok := v.(map[string]any)
			if !ok {
				continue
			}
			if total, ok := entry["red_team_total"].(float64); ok && arm.total == 0 {
				arm.total = int(total)
			}
			// The days the model actually scored, so the comparison can be confined
			// to the window both arms saw.
			if perDay, ok := entry["per_day_detections"].(map[string]any); ok {
				for dayName := range perDay {
					var d int64
					if _, err := fmt.Sscanf(dayName, "%d", &d); err == nil {
						arm.days[d] = true
					}
				}
			}
			set := map[eventKey]bool{}
			// Preferred: the alerted rows themselves.
			if rows, ok := entry["detected_events"].([]any); ok {
				for _, rv := range rows {
					rm, ok := rv.(map[string]any)
					if !ok {
						continue
					}
					t, _ := rm["t"].(float64)
					entity, _ := rm["entity"].(string)
					set[eventKey{int64(t), entity}] = true
				}
			}
			arm.detectedAt[b] = set
			if n, ok := entry["detections"].(float64); ok {
				arm.countAt[b] = int(n)
				// A positive count with no named rows is the only case that costs us
				// the pairing; a count of zero names the empty set exactly.
				if n > 0 && len(set) == 0 {
					notes = append(notes, fmt.Sprintf(
						"%s at budget %d recorded %.0f detections as a count without naming "+
							"the events, so neither the paired test nor the per-category "+
							"attribution is available for it",
						name, b, n))
				}
			}
		}
		arms = append(arms, arm)
	}
	return arms, notes
}

func budgetFromKey(key string) int {
	// Keys are of the form budget_<n>_per_day.
	var n int
	if _, err := fmt.Sscanf(key, "budget_%d_per_day", &n); err != nil {
		return 0
	}
	return n
}

// compareToBaselines builds the head-to-head rows for every baseline and budget.
func compareToBaselines(primary arm, pop redTeamPopulation, arms []baselineArm,
	budgets []int, bootstraps int, seed uint64, sharedCaveats []string,
) []headToHead {
	out := make([]headToHead, 0, len(arms)*len(budgets))
	frameworkDays := primary.coveredDays()

	for _, b := range budgets {
		frameworkHit := primary.redTeamDetectedAt(b)

		for _, ba := range arms {
			// Each baseline may have been run over a different window, so the
			// population is intersected per arm rather than once for all of them.
			// The population is the complete labelled set within that window, not the
			// labelled events that reached a retained alert list: a recall whose
			// denominator excludes the events we scored unremarkably is not a recall.
			restricted, commonDays := restrictToCommonDays(pop, frameworkDays, ba.days)
			events := restricted.keys

			fa := make([]bool, len(events))
			frameworkCount := 0
			for i, k := range events {
				fa[i] = frameworkHit[k]
				if fa[i] {
					frameworkCount++
				}
			}

			baselineHit, _ := ba.detectionsFor(b)
			bb := make([]bool, len(events))
			baselineCount := 0
			for i, k := range events {
				bb[i] = baselineHit[k]
				if bb[i] {
					baselineCount++
				}
			}

			n := len(events)
			row := headToHead{
				CommonDays:        commonDays,
				Budget:            b,
				FrameworkDetected: frameworkCount,
				FrameworkRecall:   statistics.WilsonInterval(frameworkCount, n),
				BaselineName:      ba.name,
				BaselineDetected:  baselineCount,
				BaselineRecall:    statistics.WilsonInterval(baselineCount, n),
				RedTeamEvents:     n,
				McNemar:           statistics.McNemar(fa, bb),
				Bootstrap:         statistics.PairedBootstrapDelta(fa, bb, bootstraps, seed),
				Caveats:           sharedCaveats,
			}
			row.DeltaPoints = 100 * (row.FrameworkRecall.Point - row.BaselineRecall.Point)
			if baselineCount > 0 {
				ratio := float64(frameworkCount) / float64(baselineCount)
				if !math.IsInf(ratio, 0) && !math.IsNaN(ratio) {
					row.TimesBetter = &ratio
				}
			} else {
				row.RatioUndefined = "the baseline detected nothing at this budget, so a " +
					"relative improvement is a division by zero; the difference is reported " +
					"in percentage points of recall"
			}
			out = append(out, row)
		}
	}
	return out
}

// headlineSentence renders the one-line claim for the strongest budget where the
// framework leads, in the form a reader should quote. It returns the empty string when
// no comparison supports a claim, rather than manufacturing one.
func headlineSentence(rows []headToHead) string {
	best := -1
	for i, r := range rows {
		if r.FrameworkDetected <= r.BaselineDetected {
			continue
		}
		if best < 0 || r.DeltaPoints > rows[best].DeltaPoints {
			best = i
		}
	}
	if best < 0 {
		return ""
	}
	r := rows[best]

	sig := fmt.Sprintf("McNemar %s p = %.3g", testKind(r.McNemar), r.McNemar.PValue)
	if r.McNemar.PValue < 1e-12 {
		sig = fmt.Sprintf("McNemar %s p < 1e-12", testKind(r.McNemar))
	}

	return fmt.Sprintf(
		"At a matched budget of %d alerts per analyst-day the framework detected %d of %d "+
			"red-team events (%.1f%%, 95%% CI %.1f–%.1f) against %d for %s (%.1f%%, 95%% CI "+
			"%.1f–%.1f), a difference of %.1f percentage points of recall (%s; paired "+
			"bootstrap 95%% CI [%.0f, %.0f] events).",
		r.Budget, r.FrameworkDetected, r.RedTeamEvents,
		100*r.FrameworkRecall.Point, 100*r.FrameworkRecall.Low, 100*r.FrameworkRecall.High,
		r.BaselineDetected, r.BaselineName,
		100*r.BaselineRecall.Point, 100*r.BaselineRecall.Low, 100*r.BaselineRecall.High,
		r.DeltaPoints, sig, r.Bootstrap.Low, r.Bootstrap.High)
}

func testKind(m statistics.McNemarResult) string {
	if m.Exact {
		return "exact"
	}
	return "χ²"
}
