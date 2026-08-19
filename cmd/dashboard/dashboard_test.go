package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// extractRunT is extractRun for the cases that carry no synthetic ground truth, which is
// every run over a corpus nothing was injected into.
func extractRunT(file string, data map[string]any, budgets []int) Run {
	return extractRun(file, data, budgets, nil)
}

// write puts a result file in a temporary results directory.
func write(t *testing.T, dir, name string, document map[string]any) {
	t.Helper()
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("marshal %s: %v", name, err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), raw, 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// replayDocument is a minimal well-formed replay result the tests vary from.
func replayDocument() map[string]any {
	return map[string]any{
		"schema_version": "1",
		"kind":           "replay",
		"hypothesis":     []any{"E1"},
		"run": map[string]any{
			"run_id": "test-001", "git_sha": "abcdef0123456789",
			"started_at": "2026-01-01T00:00:00Z", "finished_at": "2026-01-01T01:00:00Z",
		},
		"corpus": map[string]any{"events_scored": 1000.0, "events_skipped": 0.0},
		"results": map[string]any{
			"detections_at_budget": map[string]any{
				"budget_100_per_day": map[string]any{
					"true_positives": 3.0, "red_team_total": 10.0, "alerts": 200.0,
				},
			},
		},
	}
}

// taxonomyDocument is a minimal cmd/inject taxonomy.
func taxonomyDocument() map[string]any {
	return map[string]any{
		"schema_version": 1.0,
		"kind":           "attack-taxonomy",
		"run":            map[string]any{"run_id": "inject-001"},
		"victim_type": map[string]any{
			"U500@DOM1": "credential_spray",
			"U600@DOM1": "low_and_slow",
		},
		"premise": map[string]any{
			"credential_spray": "many destinations never used by this account",
			"low_and_slow":     "familiar values only, a modest sustained increase",
		},
		"per_type": map[string]any{
			"credential_spray": map[string]any{"events": 40.0},
			"low_and_slow":     map[string]any{"events": 36.0},
		},
	}
}

// labelledWithVictims puts three labelled events on a replay result: two synthetic of
// different kinds, one from the real campaign.
func labelledWithVictims(document map[string]any) {
	results := document["results"].(map[string]any)
	results["red_team_scored"] = []any{
		map[string]any{"t": 700000.0, "entity": "U500@DOM1",
			"categories": []any{"novel_value"},
			"detectors":  map[string]any{"novelty": 1e-11}},
		map[string]any{"t": 700001.0, "entity": "U600@DOM1",
			"categories": []any{"volume_burst"},
			"detectors":  map[string]any{"novelty": 1e-9}},
		map[string]any{"t": 700002.0, "entity": "U66@DOM1",
			"categories": []any{"novel_pair"},
			"detectors":  map[string]any{"novelty": 1e-7}},
	}
	results["detector_arms"] = map[string]any{"arms": map[string]any{
		"novelty": map[string]any{"detections_at_budget": map[string]any{
			"budget_100_per_day": map[string]any{
				"true_positives": 2.0, "red_team_total": 3.0, "alerts": 200.0,
			},
		}},
	}}
}

// TestAttackTypeIsAPartitionUnlikeAStructuralCategory pins the difference that makes the
// per-type table readable. An event can be odd in several structural ways at once, so those
// columns overlap and must not be summed; a planted event is of exactly ONE kind, or it is
// real. Blurring the two would let a reader add columns and get a number above the total.
func TestAttackTypeIsAPartitionUnlikeAStructuralCategory(t *testing.T) {
	types := map[string]string{"U500@DOM1": "credential_spray"}
	synthetic := labelledEvent{key: "1|U500@DOM1", attackType: types["U500@DOM1"],
		categories: []string{"novel_value", "off_hours", "volume_burst"}}
	campaign := labelledEvent{key: "2|U66@DOM1", categories: []string{"novel_pair"}}

	if got := attackTypeOf(synthetic); len(got) != 1 || got[0] != "credential_spray" {
		t.Errorf("attackTypeOf(synthetic) = %v, want exactly one kind", got)
	}
	if got := attackTypeOf(campaign); len(got) != 1 || got[0] != realCampaign {
		t.Errorf("attackTypeOf(campaign) = %v, want exactly %q", got, realCampaign)
	}
	// The same event lands in three structural columns and one type column.
	if got := structuralCategories(synthetic); len(got) != 3 {
		t.Errorf("structuralCategories = %v, want three overlapping columns", got)
	}
}

// TestPerTypeIsBuiltWhateverOrderTheDirectoryIsWalkedIn guards the ordering hazard the
// two-pass build exists for. results/ is walked in name order, and "injection-*.json" sorts
// after "baselines-*.json" but before "lanl-*.json" — so a single pass would attribute some
// files and not others depending on nothing but their names.
func TestPerTypeIsBuiltWhateverOrderTheDirectoryIsWalkedIn(t *testing.T) {
	for _, name := range []string{"zz-taxonomy.json", "aa-taxonomy.json"} {
		dir := t.TempDir()
		document := replayDocument()
		labelledWithVictims(document)
		write(t, dir, "mm-replay.json", document)
		write(t, dir, name, taxonomyDocument())

		index, err := build(dir)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if index.AttackTypes == nil {
			t.Fatalf("%s: the taxonomy was not read", name)
		}
		var novelty *Arm
		for i, a := range index.Runs[0].Arms {
			if a.Name == "novelty" {
				novelty = &index.Runs[0].Arms[i]
			}
		}
		if novelty == nil || novelty.PerType == nil {
			t.Fatalf("%s: the novelty arm carries no per-type tally, so the taxonomy was "+
				"read after the run that needed it", name)
		}
		// The two smallest p-values are the two synthetic events, one of each kind.
		got := novelty.PerType["100"]
		if got["credential_spray"] != 1 || got["low_and_slow"] != 1 {
			t.Errorf("%s: per-type = %v, want one of each planted kind", name, got)
		}
		if got[realCampaign] != 0 {
			t.Errorf("%s: credited %d real-campaign detections; the third event ranks "+
				"last and is outside the two it caught", name, got[realCampaign])
		}
		if got[""] != 2 {
			t.Errorf("%s: total = %d, want 2", name, got[""])
		}
	}
}

func TestWithoutATaxonomyNoPerTypeTallyIsInvented(t *testing.T) {
	// An empty per-type panel reads as "every model found nothing", which is a claim. Absent
	// ground truth must produce no tally at all so the page omits the panel instead.
	dir := t.TempDir()
	document := replayDocument()
	labelledWithVictims(document)
	write(t, dir, "replay.json", document)

	index, err := build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if index.AttackTypes != nil {
		t.Error("an attack-types block appeared with no taxonomy document present")
	}
	for _, a := range index.Runs[0].Arms {
		if a.PerType != nil {
			t.Errorf("arm %q carries a per-type tally with no ground truth to build it from",
				a.Name)
		}
	}
}

// TestATaxonomyWithNoVictimMapIsRejected: without the account-to-type map nothing can be
// attributed, and a panel of empty columns is worse than no panel because it looks answered.
// TestTheHeadlineIsDerivedRatherThanAsserted guards a claim the page carried while its own
// tables contradicted it. The headline read "No arm detects a labelled event at any alert
// budget" — true when only the composite had been measured, and still on the page after the
// per-detector arms were added and novelty reached 60 of 549. A hardcoded summary goes stale
// silently; a derived one cannot.
func TestTheHeadlineIsDerivedRatherThanAsserted(t *testing.T) {
	page := dashboardTemplate
	if strings.Contains(page, "No arm detects a labelled event at any alert budget\n") {
		t.Error("the headline still asserts that no arm detects anything as static prose")
	}
	for _, needed := range []string{
		"function headlinePanel(",
		`$("headline").innerHTML = headlinePanel(run)`,
	} {
		if !strings.Contains(page, needed) {
			t.Errorf("the headline is not computed from the selected run: %q is absent", needed)
		}
	}
	// The claim that combining loses must be conditional on the measurement, not asserted.
	if !strings.Contains(page, "compositeTP < bestTP") {
		t.Error("the page states that combining destroys the signal without checking whether " +
			"this run's composite is actually below its best component")
	}
}

// TestThePageRefusesToDivideOneRunsCountsByAnothersDenominator guards the defect a rendering
// pass exposed: entity_ewma showed "1 (0.18%)" in the attack-type table — one detection out of
// the 262 labelled events its own run scored, presented as a rate over the 549 this run
// scored. The page must withhold the baseline rows on a mismatched population and say so.
func TestThePageRefusesToDivideOneRunsCountsByAnothersDenominator(t *testing.T) {
	page := dashboardTemplate
	for _, needed := range []string{
		"run.labelled_scored === base.redteam",
		"baselines withheld from this table",
	} {
		if !strings.Contains(page, needed) {
			t.Errorf("the attack-type panel does not guard mismatched denominators: %q is "+
				"absent, so a baseline's count could be divided by another run's total",
				needed)
		}
	}
	// The withheld case must state it is a missing comparison, not a zero — the same
	// convention the rest of the page uses for a measurement that does not exist.
	if !strings.Contains(page, "missing comparison, not a zero") {
		t.Error("the withheld-baselines notice does not distinguish itself from a zero")
	}
}

// TestTheDesignedTypeOrderSurvivesAndUnknownKindsAreNotDropped: the types read as a
// progression from multi-signal through the single-signal kinds that isolate one null to the
// upper bound, and alphabetical ordering scatters that.
func TestTheDesignedTypeOrderSurvivesAndUnknownKindsAreNotDropped(t *testing.T) {
	document := taxonomyDocument()
	document["order"] = []any{"low_and_slow", "credential_spray"}
	document["per_type"].(map[string]any)["account_takeover"] = map[string]any{"events": 15.0}

	got := extractAttackTypes("t.json", document)
	if got == nil {
		t.Fatal("the taxonomy was rejected")
	}
	if len(got.Order) != 3 || got.Order[0] != "low_and_slow" || got.Order[1] != "credential_spray" {
		t.Errorf("order = %v, want the recorded order first", got.Order)
	}
	if got.Order[2] != "account_takeover" {
		t.Errorf("order = %v; a planted kind the recorded order omits must be appended, not "+
			"dropped, or a taxonomy from an older injector loses a column", got.Order)
	}
}

func TestATaxonomyWithNoVictimMapIsRejected(t *testing.T) {
	document := taxonomyDocument()
	delete(document, "victim_type")
	if got := extractAttackTypes("t.json", document); got != nil {
		t.Errorf("a taxonomy with no victim map yielded %+v, want nil", got)
	}
}

func TestBaselinePerTypeComesFromTheEventsTheModelNamed(t *testing.T) {
	types := map[string]string{"U500@DOM1": "credential_spray", "U600@DOM1": "low_and_slow"}
	document := map[string]any{
		"schema_version": 1.0,
		"kind":           "baselines",
		"run":            map[string]any{"run_id": "base-001"},
		"input":          map[string]any{"rows_total": 100.0, "rows_redteam": 3.0},
		"parameters":     map[string]any{"days_from": 7.0, "days_to": 9.0},
		"results": map[string]any{
			"iforest": map[string]any{
				"scope": "population",
				"detections_at_budget": map[string]any{
					"budget_100_per_day": map[string]any{
						"detections": 2.0, "red_team_total": 3.0,
						"detected_events": []any{
							map[string]any{"t": 700000.0, "entity": "U500@DOM1"},
							map[string]any{"t": 700002.0, "entity": "U66@DOM1"},
						},
					},
				},
			},
		},
	}

	baseline := extractBaseline("b.json", document, types)
	got := baseline.Models[0].PerType["100"]
	if got["credential_spray"] != 1 {
		t.Errorf("per-type = %v, want one credential_spray", got)
	}
	if got[realCampaign] != 1 {
		t.Errorf("per-type = %v, want the unplanted account counted as the real campaign", got)
	}
	if got["low_and_slow"] != 0 {
		t.Errorf("per-type = %v, want no low_and_slow", got)
	}
	if got[""] != 2 {
		t.Errorf("total = %d, want 2", got[""])
	}
}

func TestBuildRefusesAResultWithoutProvenance(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "bad.json", map[string]any{"kind": "replay"})

	if _, err := build(dir); err == nil {
		t.Fatal("build accepted a result file with no schema_version or run block; " +
			"the provenance gate is what keeps invented numbers off the page")
	}
}

func TestDetectionsFromReadsBothRecordedShapes(t *testing.T) {
	// The framework writes true_positives/alerts; the baseline sidecar writes
	// detections. A dashboard that understood only one would silently show zero for the
	// other, which is exactly the failure the NOT RUN convention exists to prevent.
	framework := detectionsFrom(map[string]any{
		"budget_50_per_day": map[string]any{
			"true_positives": 7.0, "red_team_total": 262.0, "alerts": 100.0,
		},
	})
	if got := framework["50"]; got.TruePositives != 7 || got.Total != 262 || got.Alerts != 100 {
		t.Errorf("framework shape = %+v, want {7 262 100}", got)
	}

	sidecar := detectionsFrom(map[string]any{
		"budget_10_per_day": map[string]any{
			"detections": 2.0, "red_team_total": 653.0,
		},
	})
	if got := sidecar["10"]; got.TruePositives != 2 || got.Total != 653 {
		t.Errorf("sidecar shape = %+v, want tp 2 of 653", got)
	}

	entityDay := detectionsFrom(map[string]any{
		"budget_25_per_day": map[string]any{
			"true_positives": 4.0, "labelled_entity_days": 46.0,
			"entity_days_alerted": 50.0,
		},
	})
	if got := entityDay["25"]; got.Total != 46 || got.Alerts != 50 {
		t.Errorf("entity-day shape = %+v, want 46 total / 50 alerted", got)
	}
}

// TestNoBackgroundPopulationIsReportedAsCritical is the regression guard for the defect
// that reached a committed result: a run whose entity sample was applied twice scored
// only labelled entities, wrote a well-formed result with plausible numbers, and was read
// as a held-out replication for a day. The page must say so on its face.
func TestNoBackgroundPopulationIsReportedAsCritical(t *testing.T) {
	document := replayDocument()
	results := document["results"].(map[string]any)
	results["entity_days"] = map[string]any{
		"rows": []any{
			map[string]any{"entity": "U1@D", "red_team_events": 3.0},
			map[string]any{"entity": "U2@D", "red_team_events": 1.0},
		},
	}
	document["corpus"].(map[string]any)["events_skipped"] = 3000000.0

	run := extractRunT("r.json", document, []int{100})
	if run.EntitiesScored != 2 || run.LabelledEntities != 2 {
		t.Fatalf("entity census = %d scored / %d labelled, want 2/2",
			run.EntitiesScored, run.LabelledEntities)
	}

	var critical *Warning
	for i, w := range run.Warnings {
		if w.Severity == "critical" {
			critical = &run.Warnings[i]
		}
	}
	if critical == nil {
		t.Fatal("a run in which every scored entity is a labelled account produced no " +
			"critical warning; there is no background population, so no detection " +
			"figure from such a run is a valid measurement")
	}
	if !strings.Contains(critical.Text, "background") {
		t.Errorf("critical warning does not mention the missing background: %q",
			critical.Text)
	}
}

func TestAHealthyPopulationRaisesNoCriticalWarning(t *testing.T) {
	document := replayDocument()
	rows := make([]any, 0, 100)
	for i := range 100 {
		labelled := 0.0
		if i < 3 {
			labelled = 1.0
		}
		rows = append(rows, map[string]any{
			"entity":          "U" + string(rune('A'+i%26)) + string(rune('0'+i/26)),
			"red_team_events": labelled,
		})
	}
	document["results"].(map[string]any)["entity_days"] = map[string]any{"rows": rows}

	for _, w := range extractRunT("r.json", document, []int{100}).Warnings {
		if w.Severity == "critical" {
			t.Fatalf("healthy run raised a critical warning: %q", w.Text)
		}
	}
}

func TestPerCategoryCountsTheTopBudgetPerDay(t *testing.T) {
	// The retained alert list holds the day's top K in rank order, so a budget of B per
	// day is its first B entries. Counting the whole list at every budget would report
	// the same number for 10/day and 100/day.
	alert := func(red bool, categories ...string) map[string]any {
		list := make([]any, 0, len(categories))
		for _, c := range categories {
			list = append(list, c)
		}
		return map[string]any{"is_red_team": red, "categories": list}
	}
	days := []dayAlerts{{day: 7, alerts: []map[string]any{
		alert(true, "novel_value"),
		alert(false, "off_hours"),
		alert(true, "novel_value", "off_hours"),
	}}}

	counts, note := perCategoryFromAlerts(days, []int{1, 3})
	if note != "" {
		t.Fatalf("unexpected truncation note: %q", note)
	}
	if got := counts["1"]["novel_value"]; got != 1 {
		t.Errorf("at budget 1, novel_value = %d, want 1", got)
	}
	if got := counts["1"]["off_hours"]; got != 0 {
		t.Errorf("at budget 1, off_hours = %d, want 0 (that alert ranks third)", got)
	}
	if got := counts["3"]["novel_value"]; got != 2 {
		t.Errorf("at budget 3, novel_value = %d, want 2", got)
	}
	if got := counts["3"][""]; got != 2 {
		t.Errorf("at budget 3, total labelled detected = %d, want 2 (the benign alert "+
			"must not be counted)", got)
	}
}

func TestPerCategoryReportsTruncationRatherThanUndercounting(t *testing.T) {
	days := []dayAlerts{{day: 7, alerts: []map[string]any{
		{"is_red_team": true, "categories": []any{"novel_value"}},
	}}}
	counts, note := perCategoryFromAlerts(days, []int{1, 100})
	if _, present := counts["100"]; present {
		t.Error("a budget beyond the retained list was reported rather than omitted; " +
			"that under-counts detections and reads as a worse result than was measured")
	}
	if note == "" {
		t.Error("omitting a budget must be stated, or the row silently disappears")
	}
}

// TestPerCategoryFromRankRecoversExactlyTheCaughtEvents pins the reconstruction that makes
// the per-category table computable for our own arms.
//
// A per-detector arm alerts on its most extreme events, so the labelled events it caught are
// the N with the smallest p-value for that detector. Getting the ordering or the cut wrong
// would silently attribute detections to the wrong attack type, which is worse than an empty
// table because it looks like an answer.
func TestPerCategoryFromRankRecoversExactlyTheCaughtEvents(t *testing.T) {
	events := []labelledEvent{
		{key: "1|a", categories: []string{"novel_value"}, scores: map[string]float64{"novelty": 1e-9}},
		{key: "2|b", categories: []string{"novel_value", "off_hours"}, scores: map[string]float64{"novelty": 1e-7}},
		{key: "3|c", categories: []string{"off_hours"}, scores: map[string]float64{"novelty": 0.4}},
		{key: "4|d", categories: []string{"volume_burst"}, scores: map[string]float64{"novelty": 0.9}},
		// Abstained: no p-value for this detector, so it can never be one of its alerts.
		{key: "5|e", categories: []string{"novel_value"}, scores: map[string]float64{}},
	}

	// The two most extreme are 1|a and 2|b.
	got := perCategoryFromRank(events, "novelty", 2)
	if got[""] != 2 {
		t.Errorf("total = %d, want 2", got[""])
	}
	if got["novel_value"] != 2 {
		t.Errorf("novel_value = %d, want 2 (both of the top two carry it)", got["novel_value"])
	}
	if got["off_hours"] != 1 {
		t.Errorf("off_hours = %d, want 1", got["off_hours"])
	}
	if got["volume_burst"] != 0 {
		t.Errorf("volume_burst = %d, want 0: that event is the least extreme", got["volume_burst"])
	}

	// An arm that caught nothing attributes nothing.
	if len(perCategoryFromRank(events, "novelty", 0)) != 0 {
		t.Error("an arm with no detections produced category counts")
	}

	// A detector that abstained on an event must never be credited with catching it, even
	// when the requested count exceeds the number it actually scored.
	all := perCategoryFromRank(events, "novelty", 99)
	if all[""] != 4 {
		t.Errorf("total = %d, want 4: the abstained event is not a candidate", all[""])
	}
}

func TestAnArmThatNamesNoEventsSaysWhyRatherThanShowingZero(t *testing.T) {
	counts, note := perCategoryFromAlerts(nil, []int{100})
	if counts != nil {
		t.Error("per-category counts were produced for an arm that kept no alert list")
	}
	if !strings.Contains(note, "without naming") {
		t.Errorf("reason = %q, want it to state that the arm recorded counts only", note)
	}
}

func TestDetectorScopeIsCarriedOntoEveryDetector(t *testing.T) {
	document := replayDocument()
	document["results"].(map[string]any)["p_histograms"] = map[string]any{
		"novelty":      map[string]any{"under_1e_12": 0.0},
		"cooccurrence": map[string]any{"under_1e_12": 267013.0},
		"combined":     map[string]any{"under_1e_12": 5.0},
	}
	document["results"].(map[string]any)["status_counts"] = map[string]any{
		"novelty":      map[string]any{"evaluated": 1451839.0, "abstained_unusable": 10.0},
		"cooccurrence": map[string]any{"evaluated": 1451839.0},
	}

	detectors := extractRunT("r.json", document, []int{100}).Detectors
	if len(detectors) != 2 {
		t.Fatalf("got %d detectors, want 2 (combined is the composite, not a detector)",
			len(detectors))
	}
	byName := map[string]DetectorStat{}
	for _, d := range detectors {
		byName[d.Name] = d
	}
	if byName["novelty"].Scope != scopePerEntity {
		t.Errorf("novelty scope = %q, want %q", byName["novelty"].Scope, scopePerEntity)
	}
	if byName["cooccurrence"].Scope != scopePopulation {
		t.Errorf("cooccurrence scope = %q, want %q",
			byName["cooccurrence"].Scope, scopePopulation)
	}
	if share := byName["cooccurrence"].Share; share < 0.18 || share > 0.19 {
		t.Errorf("cooccurrence share below 1e-12 = %v, want about 0.184", share)
	}
	if byName["novelty"].Abstained != 10 {
		t.Errorf("novelty abstained = %d, want 10", byName["novelty"].Abstained)
	}
}

func TestBudgetsComeFromTheRunsRatherThanAHardCodedList(t *testing.T) {
	budgets := discoverBudgets([]map[string]any{{
		"results": map[string]any{
			"detections_at_budget": map[string]any{"budget_10_per_day": map[string]any{}},
			"min_p_arm": map[string]any{
				"detections_at_budget": map[string]any{"budget_250_per_day": map[string]any{}},
			},
		},
	}})
	want := []int{10, 250}
	if len(budgets) != len(want) || budgets[0] != want[0] || budgets[1] != want[1] {
		t.Errorf("budgets = %v, want %v; the page must offer the budgets that were "+
			"measured, not a list that can disagree with the runs", budgets, want)
	}
}

func TestEveryHypothesisAppearsWhetherOrNotItRan(t *testing.T) {
	board := scoreboard([]map[string]any{{
		"run":        map[string]any{"run_id": "test-001"},
		"hypothesis": []any{"E1", "E8"},
	}})
	if len(board) != len(hypotheses) {
		t.Fatalf("scoreboard has %d rows, want %d: a hypothesis with no run must render "+
			"NOT RUN rather than be omitted", len(board), len(hypotheses))
	}
	byID := map[string][]string{}
	for _, h := range board {
		byID[h.ID] = h.Runs
	}
	if len(byID["E1"]) != 1 || byID["E1"][0] != "test-001" {
		t.Errorf("E1 runs = %v, want [test-001]", byID["E1"])
	}
	if len(byID["E5"]) != 0 {
		t.Errorf("E5 runs = %v, want none", byID["E5"])
	}
}

func TestRenderIsDeterministic(t *testing.T) {
	// The page carries no build timestamp on purpose: regenerating from unchanged
	// results must produce an unchanged file, or CI cannot tell a real change from a
	// rebuild and the committed artefact churns on every run.
	dir := t.TempDir()
	write(t, dir, "a.json", replayDocument())

	index, err := build(dir)
	if err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(dir, "one.html")
	second := filepath.Join(dir, "two.html")
	if err := render(index, first); err != nil {
		t.Fatal(err)
	}
	if err := render(index, second); err != nil {
		t.Fatal(err)
	}
	a, _ := os.ReadFile(first)
	b, _ := os.ReadFile(second)
	if string(a) != string(b) {
		t.Error("two renders of the same index differ; the page is not reproducible")
	}
}

func TestRenderCannotBeBrokenByAScriptTerminatorInTheData(t *testing.T) {
	// A recorded note is free text from a run. If one ever contains "</script>" the
	// embedded payload would end the block early and the page would render blank.
	dir := t.TempDir()
	document := replayDocument()
	document["results"].(map[string]any)["entity_days"] = map[string]any{
		"rows": []any{map[string]any{
			"entity": "</script><h1>injected</h1>", "red_team_events": 0.0,
		}},
	}
	write(t, dir, "a.json", document)

	index, err := build(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "page.html")
	if err := render(index, out); err != nil {
		t.Fatalf("render refused a document it should have escaped: %v", err)
	}
	page, _ := os.ReadFile(out)
	if strings.Contains(string(page), "</script><h1>injected") {
		t.Error("an entity name terminated the script block: the payload is not escaped")
	}
}

func TestTheControlCategoryIsMarkedAsSuch(t *testing.T) {
	categories := fallbackTaxonomy([]Run{{
		Categories: map[string]int{"novel_value": 215, controlCategory: 0},
	}})
	var control *Category
	for i, c := range categories {
		if c.ID == controlCategory {
			control = &categories[i]
		}
	}
	if control == nil || !control.IsControl {
		t.Fatalf("%s is not marked as the control; it is the row where the framework "+
			"should show NO advantage, and unmarked it reads as a defeat", controlCategory)
	}
}
