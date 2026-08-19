package main

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// This file distils results/*.json into the compact index the page embeds. The rule it
// exists to keep is the same one cmd/report keeps: a number reaches the page only by
// coming out of a result file, and an absent measurement renders as NOT RUN rather than
// as zero. Nothing here computes a statistic that a run did not record; where a run
// recorded a count without naming the events behind it, the index says so and the page
// prints the reason instead of a number.

// detectorScope maps each framework detector to the scope of the question it asks.
//
// This is the project's central finding made visible: per-entity detectors carried the
// signal and population-scope ones did not, so a reader comparing arms has to be able to
// see which is which without consulting a second document.
var detectorScope = map[string]string{
	"novelty": scopePerEntity,
	"timing":  scopePerEntity,
	"volume":  scopePerEntity,
	// The two forms of Detector III. They test the same signal — a combination of values
	// scarcely seen together — at scopes the framework treats very differently, and a
	// reader comparing runs has to see which one a result used.
	"cooccurrence": scopePopulation,
	"pairing":      scopePerEntity,
	"marginal":     scopePopulation,
	// Detector V asks whether an entity's rate of first-ever values has risen against
	// its own history, so it is per-entity twice over: the comparison is against the
	// same account, and it never consults another account's volume.
	"noveltyrate": scopePerEntity,
}

const (
	scopePerEntity  = "per-entity"
	scopePopulation = "population"
	scopeMixed      = "mixed"
)

// Index is the whole embedded payload.
type Index struct {
	Runs        []Run        `json:"runs"`
	Baselines   []Baseline   `json:"baselines"`
	Budgets     []int        `json:"budgets"`
	Categories  []Category   `json:"categories"`
	Hypotheses  []Hypothesis `json:"hypotheses"`
	AttackTypes *AttackTypes `json:"attack_types,omitempty"`
}

// AttackTypes is the synthetic ground truth: which kind of attack was planted on which
// account, and what question each kind was built to ask.
//
// It answers a question the real campaign cannot. The real labels come as one uneven mix, so a
// detector that misses a kind could be missing it because the kind is hard or because the
// corpus holds almost none of it. Planted attacks separate those, and because each generator
// exercises one premise, the per-type table reads as a coverage map.
//
// A detection here means a detector responds to a mechanism BY CONSTRUCTION. It is a weaker
// claim than finding a real intrusion, and the page must never add the two together.
type AttackTypes struct {
	RunID string `json:"run_id"`
	File  string `json:"file"`
	// VictimType maps an account to the one attack type planted on it. Victims are disjoint
	// from every account the real labels name, so the account alone classifies a label.
	VictimType map[string]string `json:"victim_type"`
	// Premise says, per type, which single question the generator asks. Without it a reader
	// concludes a detector is bad at an attack that was built to carry a signal it cannot
	// express — low_and_slow is deliberately the case the dispersion widening tolerates.
	Premise map[string]string `json:"premise"`
	Planted map[string]int    `json:"planted"`
	// Order is the sequence the types were designed in, which reads as a progression from
	// multi-signal through the single-signal kinds that isolate one null to the upper bound.
	// Alphabetical ordering scatters that, so the intended order travels with the document.
	Order []string `json:"order,omitempty"`
	Note  string   `json:"note"`
}

// Hypothesis is one row of the §12.3 scoreboard, present whether or not it has a result.
type Hypothesis struct {
	ID    string   `json:"id"`
	Title string   `json:"title"`
	Runs  []string `json:"runs"`
}

// Category carries the taxonomy with each row, so the page needs no second document to
// explain why a population model cannot express a category.
type Category struct {
	ID        string `json:"id"`
	Test      string `json:"test"`
	Contrast  string `json:"contrast"`
	IsControl bool   `json:"is_control"`
}

// Detection is one arm's result at one budget.
type Detection struct {
	TruePositives int `json:"tp"`
	Total         int `json:"total"`
	Alerts        int `json:"alerts"`
}

// Arm is a ranked alert list: the composite, one detector on its own, min-p, or an
// entity-day ranking.
type Arm struct {
	Name  string `json:"name"`
	Group string `json:"group"`
	Scope string `json:"scope"`
	Note  string `json:"note,omitempty"`
	// Unit is what the budget is spent on, because an entity-day arm and an event arm
	// are not comparable and a shared table would imply they were.
	Unit       string               `json:"unit"`
	Detections map[string]Detection `json:"detections"`
	// PerCategory is populated only where the arm named the events it alerted on.
	PerCategory map[string]map[string]int `json:"per_category,omitempty"`
	// PerType is the same tally over synthetic attack types instead of structural
	// categories, present only when a run carries planted ground truth.
	PerType map[string]map[string]int `json:"per_type,omitempty"`
	// NotAttributable states why a per-category breakdown is absent, when it is.
	NotAttributable string `json:"not_attributable,omitempty"`
}

// DetectorStat is a detector's calibration summary over all scored events.
type DetectorStat struct {
	Name      string  `json:"name"`
	Scope     string  `json:"scope"`
	Under1e12 int     `json:"under_1e_12"`
	Share     float64 `json:"share"`
	Evaluated int     `json:"evaluated"`
	Abstained int     `json:"abstained"`
}

// Warning is an integrity finding the page raises about a run, computed from the run's
// own recorded numbers rather than asserted.
type Warning struct {
	Severity string `json:"severity"`
	Text     string `json:"text"`
}

// Run is one replay result.
type Run struct {
	RunID    string `json:"run_id"`
	File     string `json:"file"`
	Kind     string `json:"kind"`
	GitSHA   string `json:"git_sha"`
	Dirty    bool   `json:"dirty"`
	Started  string `json:"started"`
	Finished string `json:"finished"`
	Coverage string `json:"coverage"`
	Days     []int  `json:"days"`

	EventsScored   int `json:"events_scored"`
	EventsSkipped  int `json:"events_skipped"`
	LabelledScored int `json:"labelled_scored"`

	Sampled    bool   `json:"sampled"`
	SampleNote string `json:"sample_note,omitempty"`
	Conformal  bool   `json:"conformal"`
	OpenVocab  bool   `json:"open_vocabulary"`

	// CorpusFiles names what the run read. A replay that applied no entity sample can
	// still have read a corpus that is itself a subset, and the result records only the
	// former — so the file names travel to the page rather than being summarised into a
	// "full population" claim the result does not support.
	CorpusFiles []string `json:"corpus_files,omitempty"`

	EntitiesScored   int `json:"entities_scored"`
	LabelledEntities int `json:"labelled_entities"`

	Arms       []Arm          `json:"arms"`
	Detectors  []DetectorStat `json:"detectors"`
	Categories map[string]int `json:"categories"`
	Warnings   []Warning      `json:"warnings"`
	Hypotheses []string       `json:"hypotheses"`

	// Labelled maps each labelled event's key to the categories it exhibits. It is what
	// lets a baseline's named detections be attributed to a category: the baseline knows
	// which events it caught, this run knows what kind of anomaly each event is, and
	// neither can produce the per-category comparison alone.
	Labelled map[string][]string `json:"labelled"`

	// CategoryScopes carries any category whose structural test depended on the run's
	// composition, so a column is never silently filled by two different questions.
	// novel_pair is currently the only one: the population form asks whether the
	// population graph ever carried the pair, the per-entity form whether this entity
	// ever did.
	CategoryScopes map[string]string `json:"category_scopes,omitempty"`
}

// BaselineModel is one §12.4 comparison model.
type BaselineModel struct {
	Name       string               `json:"name"`
	Scope      string               `json:"scope"`
	Family     string               `json:"family"`
	Detections map[string]Detection `json:"detections"`
	// Detected names the labelled events caught at each budget, which is what makes the
	// per-category comparison computable at all.
	Detected map[string][]string `json:"detected,omitempty"`
	// PerType tallies those detections by synthetic attack type, present only when a run
	// carries planted ground truth.
	PerType map[string]map[string]int `json:"per_type,omitempty"`
}

// Baseline is one baselines result file.
type Baseline struct {
	RunID         string          `json:"run_id"`
	File          string          `json:"file"`
	Days          []int           `json:"days"`
	Models        []BaselineModel `json:"models"`
	Rows          int             `json:"rows"`
	Redteam       int             `json:"redteam"`
	Entities      int             `json:"entities"`
	HistoryIntact bool            `json:"history_intact"`
	Caveats       []string        `json:"caveats"`
	Warnings      []Warning       `json:"warnings"`
}

// ---------------------------------------------------------------------------
// extraction helpers
// ---------------------------------------------------------------------------

func mapOf(node any, key string) map[string]any {
	m, ok := node.(map[string]any)
	if !ok {
		return nil
	}
	out, _ := m[key].(map[string]any)
	return out
}

func listOf(node any, key string) []any {
	m, ok := node.(map[string]any)
	if !ok {
		return nil
	}
	out, _ := m[key].([]any)
	return out
}

func intOf(node any, key string) int {
	m, ok := node.(map[string]any)
	if !ok {
		return 0
	}
	f, _ := m[key].(float64)
	return int(f)
}

func strOf(node any, key string) string {
	m, ok := node.(map[string]any)
	if !ok {
		return ""
	}
	s, _ := m[key].(string)
	return s
}

func boolOf(node any, key string) bool {
	m, ok := node.(map[string]any)
	if !ok {
		return false
	}
	b, _ := m[key].(bool)
	return b
}

// budgetOf reads the per-day budget out of a "budget_100_per_day" key.
func budgetOf(key string) (int, bool) {
	if !strings.HasPrefix(key, "budget_") || !strings.HasSuffix(key, "_per_day") {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(key, "budget_"), "_per_day"))
	return n, err == nil
}

// eventKey is the (t, entity) identity an alert and a labelled event are matched on.
func eventKey(t int, entity string) string {
	return strconv.Itoa(t) + "|" + entity
}

// realCampaign labels a labelled event that no synthetic attack accounts for, so a per-type
// table always states what it found in the actual intrusion beside what it found by
// construction — and never sums the two.
const realCampaign = "real campaign"

// labelledEvent is one labelled event with what each detector scored it, which structural
// categories it exhibits, and which synthetic attack type it belongs to if any.
type labelledEvent struct {
	key        string
	categories []string
	attackType string // empty for an event of the real campaign
	scores     map[string]float64
}

// caughtFromRank recovers exactly which labelled events a per-detector arm caught.
//
// A per-detector arm ranks on that detector's own p-value and alerts on the day's most
// extreme events, so the labelled events it catches are precisely the `count` labelled
// events with the smallest p-value for that detector. The run records the count; the
// ranking recovers the identities.
//
// This is a reconstruction, not an estimate: no threshold is re-derived and no histogram
// interpolation is involved. It exists because the arms record how many labelled events they
// caught without recording which — the same gap the baselines had before they began naming
// their detections — and without it the per-category and per-type tables, which are the point
// of having a taxonomy at all, stay empty for every arm the project cares most about.
func caughtFromRank(events []labelledEvent, detector string, count int) []labelledEvent {
	if count <= 0 {
		return nil
	}
	scored := make([]labelledEvent, 0, len(events))
	for _, e := range events {
		if _, ok := e.scores[detector]; ok {
			scored = append(scored, e)
		}
	}
	// Ascending p, then by key so ties resolve reproducibly (R4).
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].scores[detector] != scored[j].scores[detector] {
			return scored[i].scores[detector] < scored[j].scores[detector]
		}
		return scored[i].key < scored[j].key
	})
	if count > len(scored) {
		count = len(scored)
	}
	return scored[:count]
}

// tally counts a caught set into buckets, with "" carrying the arm's total so a reader can
// see at a glance that the buckets do not have to sum to it — an event can be odd in several
// ways at once, and a per-category row that summed would be the wrong shape.
func tally(caught []labelledEvent, bucketsOf func(labelledEvent) []string) map[string]int {
	if len(caught) == 0 {
		return map[string]int{}
	}
	counts := map[string]int{"": len(caught)}
	for _, e := range caught {
		for _, bucket := range bucketsOf(e) {
			counts[bucket]++
		}
	}
	return counts
}

func structuralCategories(e labelledEvent) []string { return e.categories }

// attackTypeOf returns the one bucket a labelled event belongs to. Unlike a structural
// category this is a partition: an event is synthetic of exactly one kind, or it is real.
func attackTypeOf(e labelledEvent) []string {
	if e.attackType == "" {
		return []string{realCampaign}
	}
	return []string{e.attackType}
}

func perCategoryFromRank(events []labelledEvent, detector string, count int) map[string]int {
	return tally(caughtFromRank(events, detector, count), structuralCategories)
}

func perTypeFromRank(events []labelledEvent, detector string, count int) map[string]int {
	return tally(caughtFromRank(events, detector, count), attackTypeOf)
}

// detectionsFrom reads a detections_at_budget block in either of the two shapes the
// project writes: the framework's true_positives/red_team_total, and the sidecar's
// detections/red_team_total.
func detectionsFrom(block map[string]any) map[string]Detection {
	if block == nil {
		return nil
	}
	out := map[string]Detection{}
	for key, raw := range block {
		budget, ok := budgetOf(key)
		if !ok {
			continue
		}
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		tp := intOf(entry, "true_positives")
		if _, present := entry["detections"]; present {
			tp = intOf(entry, "detections")
		}
		total := intOf(entry, "red_team_total")
		if total == 0 {
			total = intOf(entry, "labelled_entity_days")
		}
		alerts := intOf(entry, "alerts")
		if alerts == 0 {
			alerts = intOf(entry, "entity_days_alerted")
		}
		out[strconv.Itoa(budget)] = Detection{TruePositives: tp, Total: total, Alerts: alerts}
	}
	return out
}

// alertList flattens results.alerts_per_day into per-day ranked slices.
//
// The retained list holds the day's top-K by construction, so a budget of B per day is
// the first B entries of each day. Reading a budget off a longer list is the only way to
// attribute a detection to a category without re-running, and it is exact as long as
// B <= K; beyond that the index reports the truncation rather than silently under-counting.
type dayAlerts struct {
	day    int
	alerts []map[string]any
}

func alertsPerDay(results map[string]any, key string) []dayAlerts {
	node := mapOf(results, key)
	if node == nil {
		return nil
	}
	var out []dayAlerts
	for name, raw := range node {
		list, ok := raw.([]any)
		if !ok {
			continue
		}
		day, err := strconv.Atoi(strings.TrimPrefix(name, "day_"))
		if err != nil {
			continue
		}
		entries := make([]map[string]any, 0, len(list))
		for _, item := range list {
			if entry, ok := item.(map[string]any); ok {
				entries = append(entries, entry)
			}
		}
		// The list is written in rank order; sorting on log_p would re-derive it and
		// risk disagreeing with the run over ties, so the file's order is trusted.
		out = append(out, dayAlerts{day: day, alerts: entries})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].day < out[j].day })
	return out
}

// bucketsFromAlerts counts, per budget and bucket, the labelled events an arm alerted on.
// Returns nil when the arm kept no alert list.
//
// The retained list holds the day's top K in rank order, so a budget of B a day is its first
// B entries. Budgets past K are omitted with a note rather than counted against a short list,
// because an under-count reads as a weak arm rather than as a missing measurement.
func bucketsFromAlerts(days []dayAlerts, budgets []int,
	bucketsOf func(map[string]any) []string) (map[string]map[string]int, string) {
	if len(days) == 0 {
		return nil, "this arm recorded detection counts without naming the events behind " +
			"them, so they cannot be attributed"
	}
	retained := 0
	for _, d := range days {
		if len(d.alerts) > retained {
			retained = len(d.alerts)
		}
	}
	out := map[string]map[string]int{}
	truncated := false
	for _, budget := range budgets {
		if budget > retained {
			truncated = true
			continue
		}
		counts := map[string]int{}
		for _, d := range days {
			limit := min(budget, len(d.alerts))
			for _, alert := range d.alerts[:limit] {
				if !boolOf(alert, "is_red_team") {
					continue
				}
				for _, bucket := range bucketsOf(alert) {
					counts[bucket]++
				}
				counts[""]++ // the arm's total detections at this budget
			}
		}
		out[strconv.Itoa(budget)] = counts
	}
	note := ""
	if truncated {
		note = fmt.Sprintf("attribution is available only up to %d alerts a day, the number "+
			"the run retained; larger budgets are omitted rather than under-counted", retained)
	}
	return out, note
}

func alertCategories(alert map[string]any) []string {
	var out []string
	for _, raw := range listOf(alert, "categories") {
		if name, ok := raw.(string); ok {
			out = append(out, name)
		}
	}
	return out
}

// alertAttackType classifies one alert by the ground truth it belongs to. Victims are disjoint
// from every account the real labels name, so the account alone decides.
func alertAttackType(types map[string]string) func(map[string]any) []string {
	return func(alert map[string]any) []string {
		if kind, ok := types[strOf(alert, "entity")]; ok {
			return []string{kind}
		}
		return []string{realCampaign}
	}
}

func perCategoryFromAlerts(days []dayAlerts, budgets []int) (map[string]map[string]int, string) {
	return bucketsFromAlerts(days, budgets, alertCategories)
}

// ---------------------------------------------------------------------------
// runs
// ---------------------------------------------------------------------------

// extractRun distils one replay result. types maps a victim account to the synthetic attack
// planted on it, and is empty for a run over a corpus with no injection.
func extractRun(file string, data map[string]any, budgets []int, types map[string]string) Run {
	results := mapOf(data, "results")
	corpus := mapOf(data, "corpus")
	runBlock := mapOf(data, "run")
	params := mapOf(data, "parameters")

	run := Run{
		File:          file,
		RunID:         strOf(runBlock, "run_id"),
		Kind:          strOf(data, "kind"),
		GitSHA:        shortSHA(strOf(runBlock, "git_sha")),
		Dirty:         boolOf(runBlock, "git_dirty"),
		Started:       strOf(runBlock, "started_at"),
		Finished:      strOf(runBlock, "finished_at"),
		EventsScored:  intOf(corpus, "events_scored"),
		EventsSkipped: intOf(corpus, "events_skipped"),
		Conformal:     boolOf(mapOf(data, "conformal"), "applied"),
		OpenVocab:     boolOf(params, "open_vocabulary"),
		Categories:    map[string]int{},
	}
	if cov := mapOf(corpus, "coverage"); cov != nil {
		run.Coverage = strOf(cov, "statement")
	}
	for _, raw := range listOf(corpus, "files") {
		path := strOf(raw, "path")
		if path == "" {
			continue
		}
		if slash := strings.LastIndexAny(path, `/\`); slash >= 0 {
			path = path[slash+1:]
		}
		run.CorpusFiles = append(run.CorpusFiles, path)
	}
	if sample := mapOf(params, "entity_sample"); sample != nil && boolOf(sample, "applied") {
		run.Sampled = true
		run.SampleNote = fmt.Sprintf("1 entity in %d, labelled entities exempt",
			intOf(sample, "keep_one_in_n"))
	}
	for _, raw := range listOf(data, "hypothesis") {
		if id, ok := raw.(string); ok {
			run.Hypotheses = append(run.Hypotheses, id)
		}
	}

	labelled := listOf(results, "red_team_scored")
	run.LabelledScored = len(labelled)
	run.Days = daysOf(results)
	run.Labelled = make(map[string][]string, len(labelled))
	events := make([]labelledEvent, 0, len(labelled))
	for _, raw := range labelled {
		entity := strOf(raw, "entity")
		key := eventKey(intOf(raw, "t"), entity)
		categories := []string{}
		for _, item := range listOf(raw, "categories") {
			if name, ok := item.(string); ok {
				categories = append(categories, name)
			}
		}
		sort.Strings(categories)
		run.Labelled[key] = categories

		scores := map[string]float64{}
		for name, value := range mapOf(raw, "detectors") {
			if p, ok := value.(float64); ok {
				scores[name] = p
			}
		}
		events = append(events, labelledEvent{key: key, categories: categories,
			attackType: types[entity], scores: scores})
	}

	// Categories: the census the run recorded, which is the denominator every
	// per-category row is read against.
	categories := mapOf(results, "anomaly_categories")
	if counts := mapOf(categories, "counts"); counts != nil {
		for name, raw := range counts {
			run.Categories[name] = intOf(raw, "red_team_events")
		}
	}
	if scope := mapOf(categories, "novel_pair_scope"); scope != nil {
		run.CategoryScopes = map[string]string{
			"novel_pair": strOf(scope, "scope") + " — " + strOf(scope, "test"),
		}
	}

	run.Arms = extractArms(results, budgets, events, types)
	run.Detectors = extractDetectors(results)
	run.EntitiesScored, run.LabelledEntities = entityCensus(results)
	run.Warnings = runWarnings(run)
	return run
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func daysOf(results map[string]any) []int {
	seen := map[int]bool{}
	for _, key := range []string{"scored_per_day", "alerts_per_day"} {
		for name := range mapOf(results, key) {
			if day, err := strconv.Atoi(strings.TrimPrefix(name, "day_")); err == nil {
				seen[day] = true
			}
		}
	}
	out := make([]int, 0, len(seen))
	for day := range seen {
		out = append(out, day)
	}
	sort.Ints(out)
	return out
}

// entityCensus reports how many distinct entities a run actually scored and how many of
// them produced a labelled event.
//
// This exists because a run can silently score almost nothing: applying an entity sample
// to a corpus that is already an entity sample can leave only the labelled entities,
// which are exempt from sampling. The run still writes a well-formed result with
// plausible numbers. Counting the entities is what makes that visible.
func entityCensus(results map[string]any) (total, labelled int) {
	rows := listOf(mapOf(results, "entity_days"), "rows")
	if len(rows) == 0 {
		return -1, -1
	}
	entities := map[string]bool{}
	withLabel := map[string]bool{}
	for _, raw := range rows {
		name := strOf(raw, "entity")
		if name == "" {
			continue
		}
		entities[name] = true
		if intOf(raw, "red_team_events") > 0 {
			withLabel[name] = true
		}
	}
	return len(entities), len(withLabel)
}

func extractArms(results map[string]any, budgets []int, events []labelledEvent,
	types map[string]string) []Arm {
	var arms []Arm

	// The composite and min-p arms name the events they alerted on, so both tallies come
	// straight off their alert lists rather than from a rank reconstruction.
	byType := alertAttackType(types)
	attribute := func(arm *Arm, days []dayAlerts) {
		arm.PerCategory, arm.NotAttributable = bucketsFromAlerts(days, budgets, alertCategories)
		if len(types) > 0 {
			arm.PerType, _ = bucketsFromAlerts(days, budgets, byType)
		}
	}

	composite := Arm{
		Name: "composite", Group: "composite", Scope: scopeMixed, Unit: "event",
		Note:       "Fisher over every evaluated detector, with the dependence correction",
		Detections: detectionsFrom(mapOf(results, "detections_at_budget")),
	}
	attribute(&composite, alertsPerDay(results, "alerts_per_day"))
	if len(composite.Detections) > 0 {
		arms = append(arms, composite)
	}

	if minp := mapOf(results, "min_p_arm"); minp != nil {
		arm := Arm{
			Name: "min-p", Group: "combination", Scope: scopeMixed, Unit: "event",
			Note:       strOf(minp, "combination"),
			Detections: detectionsFrom(mapOf(minp, "detections_at_budget")),
		}
		attribute(&arm, alertsPerDay(minp, "alerts_per_day"))
		arms = append(arms, arm)
	}

	if detectorArms := mapOf(mapOf(results, "detector_arms"), "arms"); detectorArms != nil {
		names := make([]string, 0, len(detectorArms))
		for name := range detectorArms {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			scope, known := detectorScope[name]
			if !known {
				scope = scopeMixed
			}
			arm := Arm{
				Name: name, Group: "detector", Scope: scope, Unit: "event",
				Detections: detectionsFrom(mapOf(detectorArms[name], "detections_at_budget")),
			}
			// The arm records how many labelled events it caught but not which; its own
			// ranking recovers them exactly. See caughtFromRank.
			if len(events) > 0 {
				arm.PerCategory = map[string]map[string]int{}
				if len(types) > 0 {
					arm.PerType = map[string]map[string]int{}
				}
				for budget, detection := range arm.Detections {
					arm.PerCategory[budget] = perCategoryFromRank(events, name, detection.TruePositives)
					if arm.PerType != nil {
						arm.PerType[budget] = perTypeFromRank(events, name, detection.TruePositives)
					}
				}
			} else {
				arm.NotAttributable = "this run recorded no per-event detector p-values, so " +
					"the arm's detections cannot be attributed to a category"
			}
			arms = append(arms, arm)
		}
	}

	arms = append(arms, unionArms(mapOf(results, "union_arm"), events, len(types) > 0)...)

	// Entity-day rankings spend their budget on entity-days rather than events, so they
	// carry a different unit and the page never puts them in one table with the rest.
	entityDays := mapOf(results, "entity_days")
	for _, key := range []string{"corrected_minimum", "fisher_over_the_day", "standardised"} {
		block := mapOf(entityDays, key)
		if block == nil {
			continue
		}
		detections := detectionsFrom(mapOf(block, "detections_at_budget"))
		if len(detections) == 0 {
			continue
		}
		arms = append(arms, Arm{
			Name:  "entity-day: " + strings.ReplaceAll(key, "_", " "),
			Group: "entity-day", Scope: scopePerEntity, Unit: "entity-day",
			Detections: detections,
			NotAttributable: "an entity-day ranking alerts on an account's whole day, so " +
				"a per-event category does not apply to it",
		})
	}
	return arms
}

// unionGroupings are the union arm's two arm sets, and the accountings each is reported
// under. The two accountings are not alternative presentations of one number: at equal cost
// the union is charged the same alerts per day as every other arm, and at equal depth it is
// charged whatever the deduplicated union of the arms' own top lists costs, which is more.
// Showing only one of them would either flatter the union or understate what it finds.
var unionGroupings = []struct{ field, label, scope string }{
	{"entity_scope_arms", "union: per-entity arms", scopePerEntity},
	{"all_arms", "union: all arms", scopeMixed},
}

var unionAccountings = []struct{ field, label string }{
	{"at_equal_cost", "equal cost"},
	{"at_equal_depth", "equal depth"},
}

// unionArms reads the union arm, which differs from every other arm here in one way that
// matters: it NAMES the labelled events it caught instead of leaving them to be recovered.
//
// caughtFromRank recovers a per-detector arm's catch by re-ranking the labelled events on
// that detector's p-value, which reproduces the arm's own order. A union ranks on a fused
// rank, which is not a p-value and which no labelled event carries, so there is nothing to
// re-rank on. The run records the keys for exactly this reason.
func unionArms(union map[string]any, events []labelledEvent, hasTypes bool) []Arm {
	if union == nil {
		return nil
	}
	byKey := make(map[string][]string, len(events))
	for _, e := range events {
		byKey[e.key] = e.categories
	}
	kindOf := make(map[string]string, len(events))
	for _, e := range events {
		kindOf[e.key] = e.attackType
	}

	out := []Arm{}
	for _, g := range unionGroupings {
		grp := mapOf(union, g.field)
		if grp == nil {
			continue
		}
		for _, acc := range unionAccountings {
			at := mapOf(grp, acc.field)
			detections := detectionsFrom(at)
			if len(detections) == 0 {
				continue
			}
			arm := Arm{
				Name: g.label + " (" + acc.label + ")", Group: "combination",
				Scope: g.scope, Unit: "event", Detections: detections,
				Note: unionNote(acc.field),
			}
			arm.PerCategory, arm.PerType = unionTallies(grp, acc.field, byKey, kindOf, hasTypes)
			out = append(out, arm)
		}
	}
	return out
}

func unionNote(accounting string) string {
	if accounting == "at_equal_depth" {
		return "every arm keeps its own top B and the deduplicated union is emitted " +
			"whole, so this costs more than B alerts a day"
	}
	return "truncated to the same alerts a day every other arm is allowed"
}

// unionTallies buckets the union's named catch per budget, by structural category and by
// attack type.
func unionTallies(grp map[string]any, accounting string, byKey map[string][]string,
	kindOf map[string]string, hasTypes bool) (perCat, perType map[string]map[string]int) {
	perCat = map[string]map[string]int{}
	if hasTypes {
		perType = map[string]map[string]int{}
	}
	for key, raw := range mapOf(grp, accounting) {
		perDay, ok := budgetOf(key)
		if !ok {
			continue
		}
		budget := strconv.Itoa(perDay)
		cats := map[string]int{}
		types := map[string]int{}
		total := 0
		for _, item := range listOf(raw, "caught_red_team") {
			eventKey, ok := item.(string)
			if !ok {
				continue
			}
			total++
			for _, c := range byKey[eventKey] {
				cats[c]++
			}
			if kind := kindOf[eventKey]; kind != "" {
				types[kind]++
			} else {
				types[realCampaign]++
			}
		}
		if total == 0 {
			continue
		}
		cats[""] = total
		perCat[budget] = cats
		if perType != nil {
			types[""] = total
			perType[budget] = types
		}
	}
	return perCat, perType
}

func extractDetectors(results map[string]any) []DetectorStat {
	histograms := mapOf(results, "p_histograms")
	statuses := mapOf(results, "status_counts")
	names := make([]string, 0, len(histograms))
	for name := range histograms {
		if name == "combined" {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)

	scored := 0
	for _, name := range names {
		if evaluated := intOf(mapOf(statuses, name), "evaluated"); evaluated > scored {
			scored = evaluated
		}
	}

	out := make([]DetectorStat, 0, len(names))
	for _, name := range names {
		scope, known := detectorScope[name]
		if !known {
			scope = scopeMixed
		}
		status := mapOf(statuses, name)
		evaluated := intOf(status, "evaluated")
		abstained := 0
		for key, raw := range status {
			if strings.HasPrefix(key, "abstained") {
				if value, ok := raw.(float64); ok {
					abstained += int(value)
				}
			}
		}
		under := intOf(mapOf(histograms, name), "under_1e_12")
		share := 0.0
		if evaluated > 0 {
			share = float64(under) / float64(evaluated)
		}
		out = append(out, DetectorStat{
			Name: name, Scope: scope, Under1e12: under,
			Share: round(share, 6), Evaluated: evaluated, Abstained: abstained,
		})
	}
	return out
}

func round(value float64, places int) float64 {
	scale := math.Pow(10, float64(places))
	return math.Round(value*scale) / scale
}

// runWarnings derives integrity findings from what the run itself recorded.
//
// Every check here corresponds to a defect that reached a committed result and was not
// visible on any page at the time. The point of computing them is that the next
// occurrence announces itself instead of having to be noticed.
func runWarnings(run Run) []Warning {
	var out []Warning

	if run.EntitiesScored == 0 {
		out = append(out, Warning{"critical", "the run scored no entities at all"})
	}
	if run.EntitiesScored > 0 && run.LabelledEntities == run.EntitiesScored {
		out = append(out, Warning{"critical", fmt.Sprintf(
			"every one of the %d entities this run scored produced a labelled event: "+
				"there is NO background population, so the alert budget was spent "+
				"competing labelled traffic against itself and no detection figure "+
				"from this run is a valid measurement. The usual cause is entity "+
				"sampling applied twice — once building the corpus subset and again in "+
				"the replay, with disjoint selectors",
			run.EntitiesScored)})
	} else if run.EntitiesScored > 0 && run.LabelledEntities*4 > run.EntitiesScored {
		out = append(out, Warning{"warning", fmt.Sprintf(
			"%d of the %d entities scored produced a labelled event; the labelled share "+
				"of the population is far above a deployment's, so recall measured here "+
				"overstates what the same configuration would achieve",
			run.LabelledEntities, run.EntitiesScored)})
	}
	if run.EventsSkipped > 0 && run.EventsScored > 0 &&
		run.EventsSkipped > run.EventsScored {
		out = append(out, Warning{"warning", fmt.Sprintf(
			"%d events were skipped against %d scored: the sampler dropped more of the "+
				"corpus than it kept",
			run.EventsSkipped, run.EventsScored)})
	}
	if run.Sampled {
		out = append(out, Warning{"note",
			"entity-sampled run: labelled entities are exempt from the sample, so the " +
				"labelled share is inflated and a detection rate measured here is NOT " +
				"comparable to a full-population one. Population-scope quantities — the " +
				"population_rare census, the co-occurrence graph — are computed against " +
				"the retained entities only"})
	}
	if run.Dirty {
		out = append(out, Warning{"warning",
			"the working tree was dirty when this run was recorded, so its git sha does " +
				"not fully describe the code that produced it"})
	}
	return out
}

// ---------------------------------------------------------------------------
// baselines
// ---------------------------------------------------------------------------

func extractBaseline(file string, data map[string]any, types map[string]string) Baseline {
	input := mapOf(data, "input")
	params := mapOf(data, "parameters")

	baseline := Baseline{
		File:          file,
		RunID:         strOf(mapOf(data, "run"), "run_id"),
		Rows:          intOf(input, "rows_total"),
		Redteam:       intOf(input, "rows_redteam"),
		Entities:      intOf(input, "entities"),
		HistoryIntact: boolOf(input, "entity_history_intact"),
	}
	for day := intOf(params, "days_from"); day < intOf(params, "days_to"); day++ {
		baseline.Days = append(baseline.Days, day)
	}
	for _, raw := range listOf(data, "caveats") {
		if text, ok := raw.(string); ok {
			baseline.Caveats = append(baseline.Caveats, text)
		}
	}

	models := mapOf(data, "results")
	names := make([]string, 0, len(models))
	for name := range models {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		block := mapOf(models, name)
		model := BaselineModel{
			Name:       name,
			Scope:      strOf(block, "scope"),
			Family:     strOf(block, "family"),
			Detections: detectionsFrom(mapOf(block, "detections_at_budget")),
			Detected:   map[string][]string{},
		}
		if model.Scope == "" {
			model.Scope = scopePopulation
		}
		for key, raw := range mapOf(block, "detections_at_budget") {
			budget, ok := budgetOf(key)
			if !ok {
				continue
			}
			var keys []string
			perType := map[string]int{}
			for _, item := range listOf(raw, "detected_events") {
				entity := strOf(item, "entity")
				keys = append(keys, eventKey(intOf(item, "t"), entity))
				if kind, ok := types[entity]; ok {
					perType[kind]++
				} else {
					perType[realCampaign]++
				}
			}
			sort.Strings(keys)
			if len(keys) > 0 {
				model.Detected[strconv.Itoa(budget)] = keys
				perType[""] = len(keys) // the model's total at this budget
			}
			// A model that caught nothing gets an empty tally rather than none, so the page
			// distinguishes "measured, found nothing" from "not measured".
			if len(types) > 0 {
				if model.PerType == nil {
					model.PerType = map[string]map[string]int{}
				}
				model.PerType[strconv.Itoa(budget)] = perType
			}
		}
		baseline.Models = append(baseline.Models, model)
	}

	if !baseline.HistoryIntact {
		baseline.Warnings = append(baseline.Warnings, Warning{"warning",
			"this export is a uniform sample over EVENTS, so every entity's history is " +
				"decimated. The population models are unaffected; the per-entity model " +
				"is handicapped, and a poor showing from it here is not evidence " +
				"against the per-entity framing"})
	}
	return baseline
}
