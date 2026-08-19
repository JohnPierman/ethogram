package main

import (
	"math"
	"testing"

	"github.com/JohnPierman/ethogram/domain/objective"
)

// The per-category tables are the form the headline claim takes, so the arithmetic
// behind them is fixed here against hand-worked fixtures. Two properties matter most
// and are tested directly: that the recall denominator is the complete labelled
// population rather than the events that happened to be alerted, and that a comparison
// is confined to the days both arms actually scored.

const day = int64(86400)

// buildArm makes an arm whose day d holds the given rows, ordered as the analysis
// expects (ascending p).
func buildArm(name string, perDay map[int64][]alertRow) arm {
	return arm{name: name, perDay: perDay}
}

func TestRedTeamPopulationIsTheCompleteLabelledSetNotTheAlertedOne(t *testing.T) {
	// Three labelled events were scored; only one of them reached the retained alert
	// list. A denominator taken from the alert list would report 1/1 = 100% recall for
	// a run that missed two of the three.
	primary := buildArm("framework", map[int64][]alertRow{
		7: {{P: 1e-9, TSeconds: 7 * day, Entity: "U1", IsRedTeam: true}},
	})
	results := map[string]any{
		"red_team_scored": []any{
			map[string]any{"t": float64(7 * day), "entity": "U1"},
			map[string]any{"t": float64(7*day + 100), "entity": "U2"},
			map[string]any{"t": float64(7*day + 200), "entity": "U3"},
		},
	}

	pop := loadRedTeamPopulation(results, primary)
	if pop.size() != 3 {
		t.Fatalf("population = %d, want 3 (the complete labelled set)", pop.size())
	}

	rows := detectionTable(primary, pop, objective.Budgets{10}, nil)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].TruePositives != 1 {
		t.Errorf("true positives = %d, want 1", rows[0].TruePositives)
	}
	if rows[0].RedTeamScored != 3 {
		t.Errorf("denominator = %d, want 3", rows[0].RedTeamScored)
	}
	if rows[0].FalseNegatives != 2 {
		t.Errorf("false negatives = %d, want 2", rows[0].FalseNegatives)
	}
	if got := rows[0].Recall.Point; math.Abs(got-1.0/3.0) > 1e-12 {
		t.Errorf("recall = %v, want 1/3", got)
	}
}

func TestRedTeamPopulationFallbackDeclaresItselfAnUpperBound(t *testing.T) {
	// A run predating the complete list must not silently produce a flattering
	// denominator; it must say in the record that the figure is an upper bound.
	primary := buildArm("framework", map[int64][]alertRow{
		7: {{P: 1e-9, TSeconds: 7 * day, Entity: "U1", IsRedTeam: true}},
	})
	pop := loadRedTeamPopulation(map[string]any{}, primary)
	if pop.size() != 1 {
		t.Fatalf("fallback population = %d, want 1", pop.size())
	}
	if pop.source == "" {
		t.Fatal("fallback recorded no source")
	}
	for _, want := range []string{"upper bound", "retained"} {
		if !contains(pop.source, want) {
			t.Errorf("fallback source does not mention %q: %s", want, pop.source)
		}
	}
}

func TestRestrictToCommonDaysIntersects(t *testing.T) {
	// The framework ran days 7–9; the baseline ran days 7–30. Only the overlap may be
	// compared, or each arm is measured on a different denominator.
	pop := redTeamPopulation{
		keys: []eventKey{
			{7 * day, "U1"},
			{9 * day, "U2"},
			{20 * day, "U3"},
		},
		categories: map[eventKey][]string{},
	}
	framework := map[int64]bool{7: true, 8: true, 9: true}
	baseline := map[int64]bool{7: true, 9: true, 20: true, 30: true}

	got, days := restrictToCommonDays(pop, framework, baseline)
	if got.size() != 2 {
		t.Fatalf("restricted population = %d, want 2 (days 7 and 9)", got.size())
	}
	if len(days) != 2 || days[0] != 7 || days[1] != 9 {
		t.Errorf("common days = %v, want [7 9]", days)
	}
	for _, k := range got.keys {
		if k.t/day == 20 {
			t.Error("day 20 survived the intersection but the framework never scored it")
		}
	}
}

func TestDetectionsForTreatsZeroAsAnExactEmptySet(t *testing.T) {
	// A recorded zero names its set precisely, so the pairing is exact and no caveat
	// is owed. A positive count with no named rows cannot be attributed at all.
	a := baselineArm{
		name:       "iforest",
		detectedAt: map[int]map[eventKey]bool{10: {}, 25: {}},
		countAt:    map[int]int{10: 0, 25: 4},
	}
	if set, named := a.detectionsFor(10); !named || len(set) != 0 {
		t.Errorf("zero count: named=%v size=%d, want named with an empty set", named, len(set))
	}
	if _, named := a.detectionsFor(25); named {
		t.Error("a positive count with no named rows reported itself as paired")
	}
}

func TestCategoryComparisonCountsOnlyEventsInTheCategory(t *testing.T) {
	// Four labelled events on day 7. Two are novel_value, one of which the framework
	// alerts on; one is population_rare, which the framework misses. The baseline
	// detects nothing. The novel_value row must therefore read 1 of 2 against 0 of 2,
	// and the population_rare row 0 of 1 against 0 of 1 — the category where a
	// marginal detector should be competitive.
	primary := buildArm("framework", map[int64][]alertRow{
		7: {{P: 1e-9, TSeconds: 7 * day, Entity: "U1", IsRedTeam: true}},
	})
	pop := redTeamPopulation{
		keys: []eventKey{
			{7 * day, "U1"},
			{7*day + 1, "U2"},
			{7*day + 2, "U3"},
			{7*day + 3, "U4"},
		},
		categories: map[eventKey][]string{
			{7 * day, "U1"}:   {"novel_value"},
			{7*day + 1, "U2"}: {"novel_value"},
			{7*day + 2, "U3"}: {"population_rare"},
			{7*day + 3, "U4"}: {"off_hours"},
		},
	}
	arms := []baselineArm{{
		name:       "iforest",
		detectedAt: map[int]map[eventKey]bool{10: {}},
		countAt:    map[int]int{10: 0},
		days:       map[int64]bool{7: true},
	}}

	rows := categoryComparison(primary, pop, arms, []int{10}, 64, 1)

	byID := map[string]categoryRow{}
	for _, r := range rows {
		byID[r.Category.ID] = r
	}

	novel := byID["novel_value"]
	if novel.RedTeamEvents != 2 {
		t.Errorf("novel_value denominator = %d, want 2", novel.RedTeamEvents)
	}
	if novel.FrameworkDetected != 1 {
		t.Errorf("novel_value framework = %d, want 1", novel.FrameworkDetected)
	}
	if novel.BaselineDetected != 0 {
		t.Errorf("novel_value baseline = %d, want 0", novel.BaselineDetected)
	}
	if math.Abs(novel.DeltaPoints-50) > 1e-9 {
		t.Errorf("novel_value delta = %v points, want 50", novel.DeltaPoints)
	}
	if novel.TimesBetter != nil {
		t.Error("a ratio was reported against a zero denominator")
	}
	if novel.RatioUndefined == "" {
		t.Error("the undefined ratio was not explained")
	}

	rare := byID["population_rare"]
	if rare.RedTeamEvents != 1 || rare.FrameworkDetected != 0 || rare.BaselineDetected != 0 {
		t.Errorf("population_rare = %d of %d against %d, want 0 of 1 against 0",
			rare.FrameworkDetected, rare.RedTeamEvents, rare.BaselineDetected)
	}
	if math.Abs(rare.DeltaPoints) > 1e-9 {
		t.Errorf("population_rare delta = %v, want 0", rare.DeltaPoints)
	}
}

func TestCategoryComparisonReportsEmptyCategoriesAsUnmeasurable(t *testing.T) {
	// A category no labelled event exhibits earns neither credit nor blame, and must
	// say so rather than render as a zero that looks like a failure to detect.
	primary := buildArm("framework", map[int64][]alertRow{7: {}})
	pop := redTeamPopulation{
		keys:       []eventKey{{7 * day, "U1"}},
		categories: map[eventKey][]string{{7 * day, "U1"}: {"novel_value"}},
	}
	arms := []baselineArm{{
		name:       "iforest",
		detectedAt: map[int]map[eventKey]bool{10: {}},
		countAt:    map[int]int{10: 0},
		days:       map[int64]bool{7: true},
	}}

	rows := categoryComparison(primary, pop, arms, []int{10}, 16, 1)
	for _, r := range rows {
		if r.Category.ID == "novel_pair" && r.Unmeasurable == "" {
			t.Error("an empty category rendered as a measured zero")
		}
		if r.Category.ID == "novel_value" && r.Unmeasurable != "" {
			t.Errorf("a populated category was marked unmeasurable: %s", r.Unmeasurable)
		}
	}
}

func TestCategoryComparisonRefusesAttributionWithoutNamedEvents(t *testing.T) {
	// A baseline that reports a positive count without naming rows cannot have its
	// detections attributed to a category; inventing an attribution would be the one
	// thing the provenance rule forbids.
	primary := buildArm("framework", map[int64][]alertRow{7: {}})
	pop := redTeamPopulation{
		keys:       []eventKey{{7 * day, "U1"}},
		categories: map[eventKey][]string{{7 * day, "U1"}: {"novel_value"}},
	}
	arms := []baselineArm{{
		name:       "iforest",
		detectedAt: map[int]map[eventKey]bool{10: {}},
		countAt:    map[int]int{10: 3},
		days:       map[int64]bool{7: true},
	}}

	rows := categoryComparison(primary, pop, arms, []int{10}, 16, 1)
	for _, r := range rows {
		if r.Category.ID != "novel_value" {
			continue
		}
		if r.Unmeasurable == "" {
			t.Fatal("attribution was reported for a baseline that named no events")
		}
		if r.FrameworkDetected != 0 || r.BaselineDetected != 0 {
			t.Error("counts were filled in on an unmeasurable row")
		}
	}
}

func TestCategoryCensusSharesAreAgainstTheLabelledTotal(t *testing.T) {
	pop := redTeamPopulation{
		keys: []eventKey{
			{7 * day, "U1"}, {7*day + 1, "U2"}, {7*day + 2, "U3"}, {7*day + 3, "U4"},
		},
		categories: map[eventKey][]string{
			{7 * day, "U1"}:   {"novel_value", "off_hours"},
			{7*day + 1, "U2"}: {"novel_value"},
			{7*day + 2, "U3"}: {"off_hours"},
			// U4 exhibits none, which the census must report so the overlapping rows
			// are not mistaken for a partition.
		},
	}
	census := categoryCensus(pop)
	for _, row := range census {
		meta, _ := row["category"].(categoryMeta)
		n, _ := row["red_team_events"].(int)
		switch meta.ID {
		case "novel_value":
			if n != 2 {
				t.Errorf("novel_value = %d, want 2", n)
			}
			if share, _ := row["share_of_labelled"].(float64); math.Abs(share-0.5) > 1e-12 {
				t.Errorf("novel_value share = %v, want 0.5", share)
			}
		case "off_hours":
			if n != 2 {
				t.Errorf("off_hours = %d, want 2", n)
			}
		case "novel_pair", "volume_burst", "population_rare":
			if n != 0 {
				t.Errorf("%s = %d, want 0", meta.ID, n)
			}
		}
		if u, _ := row["labelled_uncategorised"].(int); u != 1 {
			t.Errorf("uncategorised = %d, want 1", u)
		}
	}
}

func TestHeadlineSentenceIsEmptyWhenNothingLeads(t *testing.T) {
	// No claim is better than a manufactured one.
	rows := []headToHead{{
		Budget: 10, FrameworkDetected: 0, BaselineDetected: 0, RedTeamEvents: 10,
	}}
	if got := headlineSentence(rows); got != "" {
		t.Errorf("headline = %q, want empty when the framework does not lead", got)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
