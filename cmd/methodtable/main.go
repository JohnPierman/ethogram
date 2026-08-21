// Command methodtable builds the paper's headline comparison: one row per method, one
// column per attack type.
//
// The paper had a results table per question -- arms by budget, then types by arm -- and a
// reader wanting to know which method to use had to hold three tables at once. This emits
// the single table that answers it: every method the project measures, against every kind
// of attack it has ground truth for.
//
// Numbers are derived here rather than typed into the Markdown. The table is large enough
// that keeping it correct by hand across a re-run is not a reasonable expectation.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"

	"github.com/JohnPierman/ethogram/domain/objective"
)

func main() {
	log.SetFlags(0)
	log.SetPrefix("methodtable: ")

	var (
		runPath       = flag.String("run", "", "replay result JSON over the injected corpus (required)")
		baselinePath  = flag.String("baselines", "", "baselines result JSON for the same corpus")
		taxPath       = flag.String("taxonomy", "", "injection taxonomy JSON (required)")
		budget        = flag.Int("budget", 1000, "per-day alert budget to report the framework at")
		budgetSpec    = flag.String("budgets", "", "comma-separated per-day budgets to emit one table each for, ascending, for instance \"10,100,1000\". Supersedes -budget. Every budget must be one the run recorded: a budget the run never measured is not a table of zeros, and this refuses rather than rendering one")
		onlyBaselines = flag.Bool("only-baselines", false, "emit only the reference-implementation rows, in one table at their own measured budget. Their budgets stop short of the framework's, so carrying them inside a framework table at a budget they were not measured at forces every cell to be a dash -- which is honest and unreadable")
		noBaselines   = flag.Bool("no-baselines", false, "omit the reference-implementation rows")
		outPath       = flag.String("out", "", "write Markdown here (default stdout)")
		matrixPath    = flag.String("matrix", "", "also write the table as JSON here, one rectangle per budget, for the robust-allocation analysis to read. Emitted from the same rows the Markdown renders, so the two cannot disagree about a count")
	)
	flag.Parse()

	if *runPath == "" || *taxPath == "" {
		log.Fatal("-run and -taxonomy are required")
	}

	// One table reads at one budget, and §3.1's own closing paragraph says the table
	// "moves a great deal with it": at 1000 alerts a day five of six planted types are
	// reached, at 100 exactly one is. Emitting the set in one pass is what lets the paper
	// show that rather than assert it, and it keeps the three tables provably from the
	// same run -- three separate invocations could silently read three different files.
	budgets := objective.Budgets{*budget}
	if *budgetSpec != "" {
		parsed, err := objective.ParseBudgets(*budgetSpec)
		if err != nil {
			log.Fatal(err)
		}
		budgets = parsed
	}

	run, err := readJSON(*runPath)
	if err != nil {
		log.Fatalf("reading run: %v", err)
	}
	tax, err := readJSON(*taxPath)
	if err != nil {
		log.Fatalf("reading taxonomy: %v", err)
	}

	var bl map[string]any
	if *baselinePath != "" {
		bl, err = readJSON(*baselinePath)
		if err != nil {
			log.Fatalf("reading baselines: %v", err)
		}
	}

	var md strings.Builder
	var matrices []matrixBudget
	rows := 0
	for i, b := range budgets {
		t := newTable(run, tax, b)
		if !t.measuredAtBudget() {
			log.Fatalf("the run records no per-detector arm at %d alerts/day; "+
				"re-run the replay with that budget rather than publishing a table "+
				"of zeros for it", b)
		}
		if bl != nil && !*noBaselines {
			t.addBaselines(bl)
		}
		if *onlyBaselines {
			t.keepOnlyBaselines()
		}
		if i > 0 {
			md.WriteString("\n")
		}
		if len(budgets) > 1 {
			// The budget is stated on the table rather than only in the prose above
			// it. Three tables of the same shape are trivially confusable, and a
			// reader who has scrolled to the third one has lost the sentence that
			// said which budget it was.
			fmt.Fprintf(&md, "**At %d alerts a day.**\n\n", b)
		}
		md.WriteString(t.render(i == len(budgets)-1))
		matrices = append(matrices, t.matrix())
		rows += len(t.rows)
	}

	if *matrixPath != "" {
		corpus := str(mapOf(run, "parameters"), "corpus_subset")
		if corpus == "" {
			corpus = str(mapOf(mapOf(run, "parameters"), "corpus_subset"), "source")
		}
		runID := str(mapOf(run, "run"), "run_id")
		if err := writeMatrix(*matrixPath, runID, corpus, matrices); err != nil {
			log.Fatalf("writing the matrix: %v", err)
		}
		log.Printf("wrote %s: %d matrices", *matrixPath, len(matrices))
	}

	if *outPath == "" {
		fmt.Print(md.String())
		return
	}
	if err := os.WriteFile(*outPath, []byte(md.String()), 0o644); err != nil {
		log.Fatalf("writing %s: %v", *outPath, err)
	}
	log.Printf("wrote %s: %d tables, %d method rows", *outPath, len(budgets), rows)
}

func readJSON(path string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// realCampaign names the labelled events no synthetic attack accounts for. A per-type table
// always states what a method found in the actual intrusion beside what it found by
// construction, and never sums the two.
const realCampaign = "real campaign"

// labelled is one labelled event: what each method scored it, and which ground truth it
// belongs to.
type labelled struct {
	key    string
	kind   string
	scores map[string]float64
	minP   float64
	hasMin bool
	comb   float64
}

// row is one method's detections, by attack type.
type row struct {
	name string
	// group orders the table: detectors, then combinations, then baselines.
	group string
	// note carries a qualification that belongs beside the row rather than in a
	// footnote nobody reads -- an unmeasured budget, or a different corpus sample.
	note   string
	caught map[string]int
	total  int
	alerts int
	// permitted is the alert count the budget allows over the whole window. A row
	// spending more than this is buying its detections rather than ranking better.
	permitted int
	// measured is false when no run covers this method at this budget, which is a
	// different statement from detecting nothing and must not be rendered as a zero.
	measured bool
}

type table struct {
	budget int
	// permitted is budget x days, read off a per-detector arm rather than assumed: an
	// arm is charged exactly the budget every day it produced alerts on.
	permitted int
	types     []string
	planted   map[string]int
	events    []labelled
	rows      []row
	runID     string
}

func newTable(run, tax map[string]any, budget int) *table {
	t := &table{budget: budget, planted: map[string]int{}}
	t.runID = str(mapOf(run, "run"), "run_id")

	victim := map[string]string{}
	for entity, raw := range mapOf(tax, "victim_type") {
		if kind, ok := raw.(string); ok {
			victim[entity] = kind
		}
	}
	for _, raw := range listOf(tax, "order") {
		if kind, ok := raw.(string); ok {
			t.types = append(t.types, kind)
		}
	}
	if len(t.types) == 0 {
		t.types = sortedKeys(mapOf(tax, "per_type"))
	}
	t.types = append(t.types, realCampaign)

	results := mapOf(run, "results")
	t.loadLabelled(results, victim)
	t.countPlanted()
	t.addFrameworkRows(results)
	return t
}

// loadLabelled reads every labelled event the run scored, with each detector's p-value for
// it, the composite's, and the corrected minimum's.
func (t *table) loadLabelled(results map[string]any, victim map[string]string) {
	byKey := map[string]*labelled{}
	order := []string{}
	for _, raw := range listOf(results, "red_team_scored") {
		rec := asMap(raw)
		entity := str(rec, "entity")
		key := fmt.Sprintf("%d|%s", intOf(rec, "t"), entity)
		e, seen := byKey[key]
		if !seen {
			e = &labelled{key: key, kind: kindOf(victim, entity),
				scores: map[string]float64{}, comb: num(rec, "p")}
			byKey[key] = e
			order = append(order, key)
		}
		for name, v := range mapOf(rec, "detectors") {
			if p, ok := v.(float64); ok {
				if prev, had := e.scores[name]; !had || p < prev {
					e.scores[name] = p
				}
			}
		}
	}

	// The corrected minimum reports its labelled events separately, under a key that
	// also carries the component pair. Reduce it to the same (t, entity) identity.
	for _, raw := range listOf(mapOf(results, "min_p_arm"), "red_team_scored") {
		rec := asMap(raw)
		key := fmt.Sprintf("%d|%s", intOf(rec, "t"), str(rec, "entity"))
		e, ok := byKey[key]
		if !ok {
			continue
		}
		p := num(rec, "p")
		if !e.hasMin || p < e.minP {
			e.minP, e.hasMin = p, true
		}
	}

	for _, k := range order {
		t.events = append(t.events, *byKey[k])
	}
}

func kindOf(victim map[string]string, entity string) string {
	if kind, ok := victim[entity]; ok {
		return kind
	}
	return realCampaign
}

func (t *table) countPlanted() {
	for _, e := range t.events {
		t.planted[e.kind]++
	}
}

// caughtByScore reconstructs which labelled events a method caught, by ranking them on that
// method's own score and taking as many as the run recorded it catching.
//
// This is a reconstruction and not an estimate: a method that ranks on one score alerts on
// the most extreme events, so the labelled events it caught are exactly the n most extreme
// labelled ones. No threshold is re-derived.
func caughtByScore(events []labelled, score func(labelled) (float64, bool), n int) []labelled {
	if n <= 0 {
		return nil
	}
	have := make([]labelled, 0, len(events))
	for _, e := range events {
		if _, ok := score(e); ok {
			have = append(have, e)
		}
	}
	sort.Slice(have, func(i, j int) bool {
		pi, _ := score(have[i])
		pj, _ := score(have[j])
		if pi != pj {
			return pi < pj
		}
		return have[i].key < have[j].key
	})
	if n > len(have) {
		n = len(have)
	}
	return have[:n]
}

func tally(caught []labelled) map[string]int {
	out := map[string]int{}
	for _, e := range caught {
		out[e.kind]++
	}
	return out
}

func (t *table) budgetKey() string { return fmt.Sprintf("budget_%d_per_day", t.budget) }

// measuredAtBudget reports whether the run actually recorded anything at this budget.
//
// A budget the run never measured produces a table every cell of which reads "--", which
// is honest but useless, and the failure mode it guards against is worse than useless: a
// reader seeing a full page of dashes concludes the methods detect nothing, which is
// exactly the confusion `cell` exists to prevent one cell at a time. Refusing is better
// than rendering it, and the message names the fix.
func (t *table) measuredAtBudget() bool {
	for _, r := range t.rows {
		if r.measured {
			return true
		}
	}
	return false
}

// addFrameworkRows adds every arm the framework recorded: the per-detector arms, the two
// p-value combinations, and the union arm's groupings.
func (t *table) addFrameworkRows(results map[string]any) {
	arms := mapOf(mapOf(results, "detector_arms"), "arms")
	for _, name := range sortedKeys(arms) {
		at := mapOf(mapOf(mapOf(arms, name), "detections_at_budget"), t.budgetKey())
		if n := intOf(at, "alerts"); n > t.permitted {
			t.permitted = n
		}
	}
	for _, name := range sortedKeys(arms) {
		det := mapOf(arms, name)
		at := mapOf(mapOf(det, "detections_at_budget"), t.budgetKey())
		n, alerts := intOf(at, "true_positives"), intOf(at, "alerts")
		id := name
		t.rows = append(t.rows, row{
			name: "`" + name + "`", group: groupOf(name), measured: at != nil,
			alerts: alerts,
			caught: tally(caughtByScore(t.events, func(e labelled) (float64, bool) {
				p, ok := e.scores[id]
				return p, ok
			}, n)),
			total: n,
		})
	}

	comb := mapOf(mapOf(results, "detections_at_budget"), t.budgetKey())
	nc := intOf(comb, "true_positives")
	t.rows = append(t.rows, row{
		name: "composite (Fisher + Brown)", group: "combination", measured: comb != nil,
		alerts: intOf(comb, "alerts"),
		caught: tally(caughtByScore(t.events, func(e labelled) (float64, bool) {
			return e.comb, true
		}, nc)),
		total: nc,
	})

	mp := mapOf(mapOf(mapOf(results, "min_p_arm"), "detections_at_budget"), t.budgetKey())
	nm := intOf(mp, "true_positives")
	t.rows = append(t.rows, row{
		name: "corrected minimum (Šidák)", group: "combination", measured: mp != nil,
		alerts: intOf(mp, "alerts"),
		caught: tally(caughtByScore(t.events, func(e labelled) (float64, bool) {
			return e.minP, e.hasMin
		}, nm)),
		total: nm,
	})

	t.addUnionRows(mapOf(results, "union_arm"))
}

// addUnionRows reads the union arm, which names the labelled events it caught rather than
// leaving them to be reconstructed: a fused rank is not a score any labelled event carries.
//
// Both accountings are emitted. They are different measurements and not two renderings of
// one: at equal cost the union is charged the same alerts a day as every other row, and at
// equal depth it is charged whatever the deduplicated union of the arms' own top lists
// costs, which is several times more. Showing only the first understates what the union
// finds and only the second hides what it spends, which is why the table carries an alerts
// column.
func (t *table) addUnionRows(union map[string]any) {
	groupings := []struct{ field, label string }{
		{"entity_scope_arms", "union, per-entity arms"},
		{"all_arms", "union, all arms"},
	}
	accountings := []struct{ field, label string }{
		{"at_equal_cost", "equal cost"},
		{"at_equal_depth", "equal depth"},
	}
	byKey := map[string]string{}
	for _, e := range t.events {
		byKey[e.key] = e.kind
	}

	for _, g := range groupings {
		grp := mapOf(union, g.field)
		for _, acc := range accountings {
			label := g.label + " (" + acc.label + ")"
			if grp == nil {
				t.rows = append(t.rows, row{name: label, group: "combination",
					measured: false, note: "no recorded run includes it"})
				continue
			}
			at := mapOf(mapOf(grp, acc.field), t.budgetKey())
			caught := map[string]int{}
			n := 0
			for _, raw := range listOf(at, "caught_red_team") {
				key, ok := raw.(string)
				if !ok {
					continue
				}
				kind, known := byKey[key]
				if !known {
					kind = realCampaign
				}
				caught[kind]++
				n++
			}
			t.rows = append(t.rows, row{name: label, group: "combination",
				measured: at != nil, alerts: intOf(at, "alerts"),
				caught: caught, total: n})
		}
	}
}

// keepOnlyBaselines drops the framework's own rows, leaving the reference implementations
// to be tabulated on their own.
//
// They belong in a separate table because their budgets are not the framework's. Carrying
// them inside a table at 1000 alerts a day, when they were measured at 100, makes every one
// of their cells a dash -- correct, and a page of dashes that a reader takes for a row of
// zeros, which is exactly what `cell` exists to prevent one cell at a time.
func (t *table) keepOnlyBaselines() {
	kept := make([]row, 0, len(t.rows))
	for _, r := range t.rows {
		if strings.HasPrefix(r.group, "baseline") {
			kept = append(kept, r)
		}
	}
	t.rows = kept
}

func groupOf(name string) string {
	switch name {
	case "marginal", "cooccurrence":
		return "population detector"
	default:
		return "per-entity detector"
	}
}

// addBaselines adds the reference implementations. Their budgets stop short of the
// framework's, and they read a sampled feature table rather than the whole corpus, so each
// row carries what it was actually measured at.
func (t *table) addBaselines(bl map[string]any) {
	results := mapOf(bl, "results")
	byKey := map[string]string{}
	for _, e := range t.events {
		byKey[e.key] = e.kind
	}

	for _, name := range sortedKeys(results) {
		model := mapOf(results, name)
		at, used := baselineBudget(mapOf(model, "detections_at_budget"), t.budget)
		r := row{name: "`" + name + "`", group: "baseline (" + str(model, "scope") + ")",
			measured: at != nil, caught: map[string]int{}}
		if used != t.budget {
			r.note = fmt.Sprintf("measured at %d/day, not %d", used, t.budget)
		}
		for _, raw := range listOf(at, "detected_events") {
			kind, known := byKey[normaliseKey(raw)]
			if !known {
				kind = realCampaign
			}
			r.caught[kind]++
			r.total++
		}
		if n := intOf(at, "detections"); n > r.total && len(listOf(at, "detected_events")) == 0 {
			// The model recorded a count without naming the events; report the
			// count and leave the columns empty rather than inventing a split.
			r.total = n
			r.note = strings.TrimSpace(r.note + " detections not named per event")
		}
		t.rows = append(t.rows, r)
	}
}

// baselineBudget returns the requested budget, or the largest one below it that the model
// actually ran, so a row states what it was measured at instead of silently reporting a
// different budget as if it were the one asked for.
func baselineBudget(all map[string]any, want int) (map[string]any, int) {
	if at := mapOf(all, fmt.Sprintf("budget_%d_per_day", want)); at != nil {
		return at, want
	}
	best, bestAt := -1, map[string]any(nil)
	for key := range all {
		var b int
		if _, err := fmt.Sscanf(key, "budget_%d_per_day", &b); err != nil {
			continue
		}
		if b <= want && b > best {
			best, bestAt = b, mapOf(all, key)
		}
	}
	return bestAt, best
}

// normaliseKey reduces a baseline's named detection to the (t, entity) identity the rest of
// this table uses. The baselines write either that identity or a record carrying it.
func normaliseKey(raw any) string {
	if s, ok := raw.(string); ok {
		parts := strings.Split(s, "|")
		if len(parts) >= 2 {
			return parts[0] + "|" + parts[1]
		}
		return s
	}
	rec := asMap(raw)
	return fmt.Sprintf("%d|%s", intOf(rec, "t"), str(rec, "entity"))
}

// groupOrder is the reading order of the table: the framework's own thesis first, then what
// it is compared against. Sorting rows by name alone interleaves the baselines with the
// arms and the table stops being readable.
var groupOrder = []string{
	"per-entity detector",
	"population detector",
	"combination",
	"baseline (per-entity)",
	"baseline (population)",
}

func groupRank(group string) int {
	for i, g := range groupOrder {
		if g == group {
			return i
		}
	}
	return len(groupOrder)
}

// sortRows puts the rows in group order, keeping each group's own order as added: the
// detectors alphabetically, the combinations weakest-argument-first, the baselines
// alphabetically.
func (t *table) sortRows() {
	sort.SliceStable(t.rows, func(i, j int) bool {
		return groupRank(t.rows[i].group) < groupRank(t.rows[j].group)
	})
}

// render writes the table. showPlanted controls the trailing census of planted events,
// which is a property of the corpus and not of the budget: repeating it under each of
// three tables says three times over that nothing about the ground truth changed.
func (t *table) render(showPlanted bool) string {
	t.sortRows()
	var b strings.Builder

	head := []string{"Method"}
	for _, kind := range t.types {
		head = append(head, shortType(kind))
	}
	// Alerts, beside the detections. A recall figure without the cost that bought it is
	// the one number in this table a reader could be actively misled by: the union's
	// equal-depth rows find the most and spend several times the budget to do it.
	//
	// There is deliberately no total column. Planted attacks test whether a detector
	// responds to a MECHANISM and the real campaign tests whether it detects an intrusion;
	// the corpus supplying the planted labels states in its own manifest that the two must
	// not be combined into one headline, and a column summing them is exactly that
	// combination. The two ground truths sit side by side and are never added.
	head = append(head, "alerts")
	b.WriteString("| " + strings.Join(head, " | ") + " |\n")
	b.WriteString("|" + strings.Repeat("---|", len(head)) + "\n")

	group := ""
	for i := range t.rows {
		if t.rows[i].permitted == 0 {
			t.rows[i].permitted = t.permitted
		}
	}
	for _, r := range t.rows {
		if r.group != group {
			group = r.group
			b.WriteString("| *" + group + "* |" +
				strings.Repeat(" |", len(t.types)+1) + "\n")
		}
		name := r.name
		if r.note != "" {
			// The qualification rides on the row's own name rather than sitting in a
			// footnote, so a reader cannot take the cells at face value without it.
			name += " *(" + r.note + ")*"
		}
		b.WriteString("| " + name)
		for _, kind := range t.types {
			b.WriteString(" | " + cell(r, kind, t.planted[kind]))
		}
		b.WriteString(" | " + alertCell(r) + " |\n")
	}

	if !showPlanted {
		return b.String()
	}
	b.WriteString("\nPlanted: ")
	parts := []string{}
	for _, kind := range t.types {
		parts = append(parts, fmt.Sprintf("%s %d", shortType(kind), t.planted[kind]))
	}
	b.WriteString(strings.Join(parts, ", ") + ".\n")
	return b.String()
}

func cell(r row, kind string, planted int) string {
	if !r.measured {
		return "--"
	}
	n := r.caught[kind]
	if n == 0 {
		return "0"
	}
	if n == planted && planted > 0 {
		return fmt.Sprintf("**%d/%d**", n, planted)
	}
	return fmt.Sprintf("%d", n)
}

// alertCell states what the row spent. A row charged more than the budget carries the
// multiple, since that is the comparison a reader would otherwise have to do by hand and
// the one that decides whether the extra detections were worth buying.
func alertCell(r row) string {
	if !r.measured || r.alerts == 0 {
		return "--"
	}
	if r.permitted > 0 && r.alerts > r.permitted {
		return fmt.Sprintf("%d **(×%.1f)**", r.alerts,
			float64(r.alerts)/float64(r.permitted))
	}
	return fmt.Sprintf("%d", r.alerts)
}

func shortType(kind string) string {
	switch kind {
	case "account_takeover":
		return "takeover"
	case "credential_spray":
		return "spray"
	case "lateral_chain":
		return "lateral"
	case "low_and_slow":
		return "low+slow"
	case "off_hours":
		return "off-hrs"
	case "privilege_escalation":
		return "priv-esc"
	case realCampaign:
		return "real"
	default:
		return kind
	}
}

// --- JSON helpers. The result schema is read positionally rather than into structs
// because these documents carry many fields this command has no opinion about.

func asMap(raw any) map[string]any {
	m, _ := raw.(map[string]any)
	return m
}

func mapOf(m map[string]any, key string) map[string]any {
	if m == nil {
		return nil
	}
	return asMap(m[key])
}

func listOf(m map[string]any, key string) []any {
	if m == nil {
		return nil
	}
	l, _ := m[key].([]any)
	return l
}

func str(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	s, _ := m[key].(string)
	return s
}

func num(m map[string]any, key string) float64 {
	if m == nil {
		return 0
	}
	f, _ := m[key].(float64)
	return f
}

func intOf(m map[string]any, key string) int { return int(num(m, key)) }

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
