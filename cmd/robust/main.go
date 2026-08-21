// Command robust solves the allocation of a fixed alert budget across detectors as a
// two-person zero-sum game, from a recorded per-mechanism matrix.
//
// It reads the matrix `cmd/methodtable -matrix` writes and needs no corpus: every quantity
// below is arithmetic over counts a replay already recorded. That is deliberate. The
// allocation question is about how to spend a budget over detectors whose performance has
// been measured, so re-scoring four million events to answer it would be re-measuring the
// inputs rather than answering the question.
//
// The four reported objectives answer four different questions and disagree, which is the
// point:
//
//   - the best single arm, which is what maximises expected detections under any prior;
//   - the maximin allocation, which is degenerate whenever a mechanism is uncovered;
//   - the competitive-ratio allocation, which is the well-posed robust objective;
//   - the randomised allocation, which is what a mixed strategy actually is and is a
//     different object from dividing the budget.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/JohnPierman/ethogram/domain/robust"
)

func main() {
	log.SetFlags(0)
	log.SetPrefix("robust: ")

	var (
		matrixPath = flag.String("matrix", "", "method-matrix JSON from `methodtable -matrix` (required)")
		budget     = flag.Int("budget", 1000, "per-day budget to analyse; must be one the matrix records")
		admit      = flag.String("admit", "", "comma-separated extra rows to admit into the strategy set, for instance \"lof\". Baseline rows are measured on a different and easier problem, so admitting one is a deliberate act and is recorded as a caveat rather than assumed")
		priorSpec  = flag.String("prior", "", "comma-separated mechanism=weight pairs stating how common each mechanism is believed to be. Normalised. Used only to price robustness, never to choose an allocation")
		costSpec   = flag.String("attacker-cost", "", "comma-separated mechanism=cost pairs, the adversary's cost of mounting each mechanism in units of the cheapest")
		lambdaSpec = flag.String("lambdas", "0,0.005,0.01,0.02,0.05", "comma-separated exchange rates between the adversary's cost and its detection risk")
		outPath    = flag.String("out", "", "write the result JSON here")
		mdPath     = flag.String("md", "", "write Markdown tables here (default stdout)")
		runID      = flag.String("run-id", "", "identifier to record for this analysis (required with -out)")
	)
	flag.Parse()

	if *matrixPath == "" {
		log.Fatal("-matrix is required")
	}
	if *outPath != "" && *runID == "" {
		log.Fatal("-run-id is required when writing a result file: an unnamed result cannot be cited")
	}

	doc, err := readMatrix(*matrixPath)
	if err != nil {
		log.Fatal(err)
	}
	rect, ok := doc.at(*budget)
	if !ok {
		log.Fatalf("the matrix records no rectangle at %d alerts/day; it has %v",
			*budget, doc.budgets())
	}

	analysis, err := analyse(doc, rect, splitList(*admit), *priorSpec, *costSpec, *lambdaSpec)
	if err != nil {
		log.Fatal(err)
	}

	md := analysis.markdown()
	if *mdPath == "" {
		fmt.Print(md)
	} else if err := os.WriteFile(*mdPath, []byte(md), 0o644); err != nil {
		log.Fatalf("writing %s: %v", *mdPath, err)
	}

	if *outPath != "" {
		analysis.Run["run_id"] = *runID
		blob, err := json.MarshalIndent(analysis, "", "  ")
		if err != nil {
			log.Fatalf("encoding the result: %v", err)
		}
		if err := os.WriteFile(*outPath, append(blob, '\n'), 0o644); err != nil {
			log.Fatalf("writing %s: %v", *outPath, err)
		}
		log.Printf("wrote %s", *outPath)
	}
}

// ---------------------------------------------------------------------------
// reading the recorded matrix
// ---------------------------------------------------------------------------

type matrixRow struct {
	Name      string         `json:"name"`
	Group     string         `json:"group"`
	Caught    map[string]int `json:"caught"`
	Alerts    int            `json:"alerts"`
	Permitted int            `json:"permitted"`
	Measured  bool           `json:"measured"`
	Note      string         `json:"note,omitempty"`
}

type rectangle struct {
	Budget     int            `json:"budget"`
	Permitted  int            `json:"permitted"`
	Mechanisms []string       `json:"mechanisms"`
	Planted    map[string]int `json:"planted"`
	Rows       []matrixRow    `json:"rows"`
}

type matrixDoc struct {
	Kind    string         `json:"kind"`
	Run     map[string]any `json:"run"`
	Budgets []rectangle    `json:"budgets"`
}

func readMatrix(path string) (matrixDoc, error) {
	blob, err := os.ReadFile(path)
	if err != nil {
		return matrixDoc{}, fmt.Errorf("reading %s: %w", path, err)
	}
	var doc matrixDoc
	if err := json.Unmarshal(blob, &doc); err != nil {
		return matrixDoc{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	if doc.Kind != "method-matrix" {
		return matrixDoc{}, fmt.Errorf("%s is a %q document, want a method-matrix", path, doc.Kind)
	}
	if len(doc.Budgets) == 0 {
		return matrixDoc{}, fmt.Errorf("%s records no budgets", path)
	}
	return doc, nil
}

func (d matrixDoc) at(budget int) (rectangle, bool) {
	for _, r := range d.Budgets {
		if r.Budget == budget {
			return r, true
		}
	}
	return rectangle{}, false
}

func (d matrixDoc) budgets() []int {
	out := make([]int, 0, len(d.Budgets))
	for _, r := range d.Budgets {
		out = append(out, r.Budget)
	}
	sort.Ints(out)
	return out
}

// ---------------------------------------------------------------------------
// the analysis
// ---------------------------------------------------------------------------

// Analysis is the emitted result. Every field is derived from the recorded matrix named in
// Run, so a reader can reproduce it without the corpus.
type Analysis struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind"`
	// Run carries this analysis's own identifier and the matrix it was derived from. The
	// results directory refuses a file without it, and rightly: a number whose provenance
	// is not in the file is a number nobody can trace.
	Run        map[string]any `json:"run"`
	Budget     int            `json:"budget"`
	Arms       []string       `json:"arms"`
	Mechanisms []string       `json:"mechanisms"`
	Caveats    []string       `json:"caveats"`

	Rate    map[string]map[string]float64 `json:"detection_rate"`
	Planted map[string]int                `json:"planted"`

	Unreachable []string `json:"unreachable"`

	Maximin          Solved   `json:"maximin"`
	Competitive      Solved   `json:"competitive_ratio"`
	Price            *Priced  `json:"price_of_robustness,omitempty"`
	AttackerCost     []Costed `json:"attacker_cost,omitempty"`
	Shadow           []Shadow `json:"shadow_prices"`
	RandomisedVsRule []Rule   `json:"randomised_against_the_recorded_rules"`
}

// Solved is one solved objective.
type Solved struct {
	Value         float64            `json:"value"`
	Mix           map[string]float64 `json:"mix"`
	Response      map[string]float64 `json:"adversary_reply"`
	Support       []string           `json:"support"`
	BestPure      string             `json:"best_pure_arm"`
	BestPureValue float64            `json:"best_pure_value"`
	Gain          float64            `json:"gain_from_mixing"`
	Retained      map[string]float64 `json:"retained_fraction,omitempty"`
	Dropped       []string           `json:"dropped_mechanisms,omitempty"`
}

// Priced is the exchange rate between expected and worst-case detection.
type Priced struct {
	Prior           map[string]float64 `json:"prior"`
	BayesArm        string             `json:"bayes_arm"`
	BayesExpected   float64            `json:"bayes_expected_rate"`
	BayesWorstCase  float64            `json:"bayes_worst_case_rate"`
	RobustExpected  float64            `json:"robust_expected_rate"`
	RobustWorstCase float64            `json:"robust_worst_case_rate"`
	ExpectedGivenUp float64            `json:"expected_rate_given_up"`
	WorstCaseBought float64            `json:"worst_case_rate_bought"`
	FractionGivenUp float64            `json:"fraction_of_expected_given_up"`
}

// Costed is the equilibrium at one exchange rate between adversary cost and detection risk.
type Costed struct {
	Lambda   float64            `json:"lambda"`
	Value    float64            `json:"value"`
	Mix      map[string]float64 `json:"mix"`
	Response map[string]float64 `json:"adversary_reply"`
}

// Shadow is where the next unit of detector work is worth spending.
type Shadow struct {
	Arm       string  `json:"arm"`
	Mechanism string  `json:"mechanism"`
	Current   float64 `json:"current_rate"`
	Gain      float64 `json:"gain_in_guarantee"`
}

// Rule compares an allocation on the two axes it must be judged on at once: how much it
// finds in total, and how little it finds against the mechanism it covers worst.
type Rule struct {
	Name      string  `json:"name"`
	Detected  float64 `json:"detected_total"`
	Alerts    int     `json:"alerts"`
	WorstCase float64 `json:"worst_case_retained_fraction"`
	Note      string  `json:"note,omitempty"`
}

func analyse(doc matrixDoc, rect rectangle, admit []string,
	priorSpec, costSpec, lambdaSpec string) (*Analysis, error) {

	detectors, extras, unmatched := partition(rect, admit)
	arms := append(append([]string(nil), detectors...), extras...)
	if len(arms) == 0 {
		return nil, fmt.Errorf("no detector rows in the matrix at %d alerts/day", rect.Budget)
	}
	mechs := rect.Mechanisms

	byName := map[string]matrixRow{}
	for _, r := range rect.Rows {
		byName[clean(r.Name)] = r
	}

	rate := make([][]float64, len(arms))
	named := map[string]map[string]float64{}
	for i, arm := range arms {
		rate[i] = make([]float64, len(mechs))
		named[arm] = map[string]float64{}
		for j, mech := range mechs {
			planted := rect.Planted[mech]
			if planted <= 0 {
				continue
			}
			rate[i][j] = float64(byName[arm].Caught[mech]) / float64(planted)
			named[arm][mech] = rate[i][j]
		}
	}

	m, err := robust.NewMatrix(arms, mechs, rate)
	if err != nil {
		return nil, err
	}

	out := &Analysis{
		SchemaVersion: 1,
		Kind:          "robust-allocation",
		Run:           map[string]any{"source_matrix": doc.Run},
		Budget:        rect.Budget,
		Arms:          arms,
		Mechanisms:    mechs,
		Rate:          named,
		Planted:       rect.Planted,
		Unreachable:   m.Unreachable(),
		Caveats: []string{
			"detection rates are event-level; each mechanism has eight victims and a " +
				"deterministic choice of planted values, so the effective sample size is " +
				"nearer eight than the event count and every value here is a coarse rate",
			"the adversary is assumed to choose the mechanism knowing the allocation but " +
				"not the realised draw, which is what makes a mixed strategy meaningful",
		},
	}
	for _, name := range unmatched {
		out.Caveats = append(out.Caveats, fmt.Sprintf(
			"%q is admitted into the strategy set but was measured on a 1-in-100 event "+
				"sample rather than the full corpus, a hundredfold easier problem; its row "+
				"is an existence proof that the mechanism is reachable, not a matched "+
				"comparison", name))
	}

	maximin, err := m.Maximin()
	if err != nil {
		return nil, err
	}
	out.Maximin = solved(maximin, nil, nil)

	comp, dropped, err := m.CompetitiveRatio()
	if err != nil {
		return nil, err
	}
	retained, err := m.Retained(comp.Mix)
	if err != nil {
		return nil, err
	}
	out.Competitive = solved(comp, retained, dropped)

	if priorSpec != "" {
		prior, parseErr := parsePairs(priorSpec)
		if parseErr != nil {
			return nil, fmt.Errorf("-prior: %w", parseErr)
		}
		p, priceErr := m.PriceOfRobustness(comp.Mix, prior)
		if priceErr != nil {
			return nil, priceErr
		}
		fraction := 0.0
		if p.BayesExpected > 0 {
			fraction = p.ExpectedGivenUp() / p.BayesExpected
		}
		out.Price = &Priced{
			Prior: p.Prior, BayesArm: p.BayesArm,
			BayesExpected: p.BayesExpected, BayesWorstCase: p.BayesWorstCase,
			RobustExpected: p.RobustExpected, RobustWorstCase: p.RobustWorstCase,
			ExpectedGivenUp: p.ExpectedGivenUp(), WorstCaseBought: p.WorstCaseBought(),
			FractionGivenUp: fraction,
		}
	}

	if costSpec != "" {
		cost, parseErr := parsePairs(costSpec)
		if parseErr != nil {
			return nil, fmt.Errorf("-attacker-cost: %w", parseErr)
		}
		lambdas, lambdaErr := parseFloats(lambdaSpec)
		if lambdaErr != nil {
			return nil, fmt.Errorf("-lambdas: %w", lambdaErr)
		}
		for _, lambda := range lambdas {
			priced, costErr := m.WithAttackerCost(cost, lambda)
			if costErr != nil {
				return nil, costErr
			}
			a, solveErr := priced.Maximin()
			if solveErr != nil {
				return nil, solveErr
			}
			out.AttackerCost = append(out.AttackerCost, Costed{
				Lambda: lambda, Value: a.Value,
				Mix: trim(a.Mix), Response: trim(a.Response),
			})
		}
	}

	prices, err := m.ShadowPrices(0.10, 1.0)
	if err != nil {
		return nil, err
	}
	for _, s := range prices {
		out.Shadow = append(out.Shadow, Shadow{
			Arm: s.Arm, Mechanism: s.Mechanism, Current: s.Current, Gain: s.Gain,
		})
	}

	out.RandomisedVsRule, err = compareRules(m, rect, byName, arms, comp.Mix)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// partition splits the matrix's rows into the framework's detectors, which form the strategy
// set, and any rows the caller has explicitly admitted alongside them.
func partition(rect rectangle, admit []string) (detectors, extras, unmatched []string) {
	want := map[string]bool{}
	for _, name := range admit {
		want[name] = true
	}
	for _, r := range rect.Rows {
		name := clean(r.Name)
		switch {
		case strings.HasPrefix(r.Group, "per-entity") && !strings.Contains(r.Group, "baseline"),
			strings.HasPrefix(r.Group, "population") && !strings.Contains(r.Group, "baseline"):
			detectors = append(detectors, name)
		case want[name]:
			extras = append(extras, name)
			unmatched = append(unmatched, name)
		}
	}
	return detectors, extras, unmatched
}

// compareRules puts the randomised allocation beside the combination rules the run already
// recorded, on both axes at once. Total detections is what the paper reports; the worst-case
// retained fraction is what a total conceals, and the two orderings are not the same.
func compareRules(m robust.Matrix, rect rectangle, byName map[string]matrixRow,
	arms []string, mix map[string]float64) ([]Rule, error) {

	total := func(caught map[string]int) float64 {
		sum := 0.0
		for _, n := range caught {
			sum += float64(n)
		}
		return sum
	}
	worst := func(weights map[string]float64) (float64, error) {
		retained, err := m.Retained(weights)
		if err != nil {
			return 0, err
		}
		lo := 1.0
		for _, v := range retained {
			if v < lo {
				lo = v
			}
		}
		return lo, nil
	}

	var out []Rule

	// The best single arm, which is the allocation in use.
	bestArm, bestTotal := "", -1.0
	for _, arm := range arms {
		if t := total(byName[arm].Caught); t > bestTotal {
			bestArm, bestTotal = arm, t
		}
	}
	if w, err := worst(map[string]float64{bestArm: 1}); err == nil {
		out = append(out, Rule{
			Name: "best single arm (" + bestArm + ")", Detected: bestTotal,
			Alerts: byName[bestArm].Alerts, WorstCase: w,
		})
	} else {
		return nil, err
	}

	// Every combination rule the run recorded. A rule is not a mixture over the arms, so
	// its retained fraction is computed from its own counts against the same per-mechanism
	// ceiling the mixtures are measured against, which is what makes the column comparable.
	for _, r := range rect.Rows {
		if !strings.Contains(r.Group, "combination") {
			continue
		}
		note := ""
		if r.Alerts > r.Permitted && r.Permitted > 0 {
			note = fmt.Sprintf("spends %.1fx the permitted alerts",
				float64(r.Alerts)/float64(r.Permitted))
		}
		out = append(out, Rule{
			Name: clean(r.Name), Detected: total(r.Caught), Alerts: r.Alerts, Note: note,
			WorstCase: retainedFromCounts(m, rect, r.Caught),
		})
	}

	// The randomised allocation at the competitive-ratio mixture, and the two-arm
	// randomisation over the best arm and the arm most complementary to it.
	expected := 0.0
	for arm, w := range mix {
		expected += w * total(byName[arm].Caught)
	}
	w, err := worst(mix)
	if err != nil {
		return nil, err
	}
	out = append(out, Rule{
		Name: "randomised, competitive-ratio mixture", Detected: expected,
		Alerts:    rect.Permitted,
		WorstCase: w,
		Note:      "one arm at full depth, chosen by lottery; not a budget split",
	})

	// The ceiling on per-entity routing, which is the one rule that can exceed the best
	// single arm at equal cost, because it is the only one that exploits arms reaching
	// different events rather than averaging over them.
	//
	// Two bounds, both oracles. The lower one routes each mechanism to the arm that reaches
	// it most often, which any per-entity policy can match by construction since a mechanism's
	// victims all share a mechanism. The upper one is the union of every arm at full depth:
	// no routing can find an event that no arm finds. The gap between them is the headroom a
	// router has to be measured inside, and the distance from the best single arm is what
	// makes it worth building.
	perMechanism := 0.0
	for _, mech := range m.Mechanisms() {
		best := 0
		for _, arm := range arms {
			if n := byName[arm].Caught[mech]; n > best {
				best = n
			}
		}
		perMechanism += float64(best)
	}
	ceiling := 0.0
	for _, r := range rect.Rows {
		if strings.Contains(r.Name, "all arms") && strings.Contains(r.Name, "equal depth") {
			ceiling = total(r.Caught)
		}
	}
	out = append(out, Rule{
		Name:     "per-entity routing, oracle floor",
		Detected: perMechanism, Alerts: rect.Permitted,
		Note: "each mechanism to the arm that reaches it most; any per-entity policy " +
			"matches this by construction",
	})
	if ceiling > 0 {
		out = append(out, Rule{
			Name:     "per-entity routing, oracle ceiling",
			Detected: ceiling, Alerts: rect.Permitted,
			Note: "no routing can reach an event no arm reaches; the alert cost of " +
				"attaining it is not accounted for here",
		})
	}

	// An even randomisation over the two highest-yielding arms. This is the row that
	// answers whether randomising beats dividing at equal cost, and it needs no judgement
	// about which pair is interesting: total detections is linear in the weights, so the
	// best two-arm mixture by that measure is simply over the two best arms.
	partner, partnerTotal := "", -1.0
	for _, arm := range arms {
		if arm == bestArm {
			continue
		}
		if t := total(byName[arm].Caught); t > partnerTotal {
			partner, partnerTotal = arm, t
		}
	}
	if partner != "" {
		half := map[string]float64{bestArm: 0.5, partner: 0.5}
		w, err := worst(half)
		if err != nil {
			return nil, err
		}
		out = append(out, Rule{
			Name:     fmt.Sprintf("randomised, even over %s and %s", bestArm, partner),
			Detected: 0.5*bestTotal + 0.5*partnerTotal,
			Alerts:   rect.Permitted, WorstCase: w,
			Note: "the two highest-yielding arms, each at its own full depth",
		})
	}
	return out, nil
}

// retainedFromCounts is the worst-case retained fraction of a rule that is not a mixture
// over the arms: its own detection rate per mechanism, against the best rate any single arm
// reaches there. Mechanisms no arm reaches are skipped, since every rule ties at zero on them
// and including them would report every rule as guaranteeing nothing.
func retainedFromCounts(m robust.Matrix, rect rectangle, caught map[string]int) float64 {
	unreachable := map[string]bool{}
	for _, mech := range m.Unreachable() {
		unreachable[mech] = true
	}
	ceiling := map[string]float64{}
	for _, arm := range m.Arms() {
		blend, err := m.Blend(map[string]float64{arm: 1})
		if err != nil {
			return 0
		}
		for mech, r := range blend {
			if r > ceiling[mech] {
				ceiling[mech] = r
			}
		}
	}

	lo := 1.0
	for _, mech := range m.Mechanisms() {
		if unreachable[mech] || ceiling[mech] <= 0 {
			continue
		}
		planted := rect.Planted[mech]
		if planted <= 0 {
			continue
		}
		got := float64(caught[mech]) / float64(planted) / ceiling[mech]
		if got < lo {
			lo = got
		}
	}
	return lo
}

func solved(a robust.Allocation, retained map[string]float64, dropped []string) Solved {
	return Solved{
		Value: a.Value, Mix: trim(a.Mix), Response: trim(a.Response),
		Support: a.Support(1e-9), BestPure: a.BestPure, BestPureValue: a.BestPureValue,
		Gain: a.GainFromMixing(), Retained: retained, Dropped: dropped,
	}
}

// trim drops the zero weights. An equilibrium on a sparse matrix rests on two or three arms
// and printing the zeros buries which.
func trim(m map[string]float64) map[string]float64 {
	out := map[string]float64{}
	for k, v := range m {
		if v > 1e-9 {
			out[k] = v
		}
	}
	return out
}

func clean(name string) string { return strings.Trim(name, "`") }

func splitList(spec string) []string {
	if strings.TrimSpace(spec) == "" {
		return nil
	}
	parts := strings.Split(spec, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func parsePairs(spec string) (map[string]float64, error) {
	out := map[string]float64{}
	for _, pair := range splitList(spec) {
		key, value, ok := strings.Cut(pair, "=")
		if !ok {
			return nil, fmt.Errorf("%q is not a name=value pair", pair)
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil {
			return nil, fmt.Errorf("%q: %w", pair, err)
		}
		out[strings.TrimSpace(key)] = v
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no pairs in %q", spec)
	}
	return out, nil
}

func parseFloats(spec string) ([]float64, error) {
	var out []float64
	for _, raw := range splitList(spec) {
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, fmt.Errorf("%q: %w", raw, err)
		}
		out = append(out, v)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no values in %q", spec)
	}
	return out, nil
}
