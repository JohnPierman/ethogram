package main

import (
	"math"
	"testing"

	"github.com/JohnPierman/ethogram/domain/objective"
)

// feed builds an accumulator's entity-day state directly, which is what observe() folds
// into once an event has been scored. Working at this level keeps the test about the
// aggregation rather than about assembling scored events.
func feed(a *accumulator, entity string, day int64, logPs []float64, redTeam int) {
	for i, lp := range logPs {
		a.observeEntityDay(entity, day, lp, i < redTeam)
	}
}

// testBudgets is the accumulator's configured budget set. These tests call
// entityDayResults with their own budgets, so this only has to be a valid set.
var testBudgets = objective.Budgets{10, 25, 50, 100}

func TestEntityDayAccumulatesTheEvidenceEventRankingDiscards(t *testing.T) {
	a := newAccumulator(&redTeamLabels{keys: map[string]struct{}{}}, 200, testBudgets, false, weightingNone, onlineNone, "")

	// The shape LANL day 8 actually has: one account with many moderately unusual
	// events, against a background account with a single more extreme one.
	feed(a, "U66@DOM1", 8, []float64{-40, -41, -39, -42, -38, -40, -41}, 7)
	feed(a, "U9999@DOM1", 8, []float64{-55}, 0)

	if got := len(a.entityDays); got != 2 {
		t.Fatalf("entity-days = %d, want 2", got)
	}

	campaign := a.entityDays[entityDayKey{entity: "U66@DOM1", day: 8}]
	if campaign.Events != 7 {
		t.Errorf("events = %d, want 7", campaign.Events)
	}
	if campaign.RedTeamEvents != 7 {
		t.Errorf("labelled = %d, want 7", campaign.RedTeamEvents)
	}
	if campaign.MinLogP != -42 {
		t.Errorf("min log p = %v, want Ã¢Ë†â€™42", campaign.MinLogP)
	}
	// Fisher's statistic is ÃŽÂ£ Ã¢Ë†â€™2 ln P; the seven sum to 281Ã‚Â·2 = 562.
	if want := 2.0 * (40 + 41 + 39 + 42 + 38 + 40 + 41); campaign.SumX2 != want {
		t.Errorf("sum XÃ‚Â² = %v, want %v", campaign.SumX2, want)
	}

	single := a.entityDays[entityDayKey{entity: "U9999@DOM1", day: 8}]
	if single.MinLogP != -55 {
		t.Errorf("background min log p = %v, want Ã¢Ë†â€™55", single.MinLogP)
	}

	// The two rankings disagree, which is the entire reason both are recorded: the
	// background account wins on its single most extreme event, the campaign wins on
	// accumulated evidence.
	if single.MinLogP >= campaign.MinLogP {
		t.Error("expected the background account to hold the more extreme single event")
	}
	if single.SumX2 >= campaign.SumX2 {
		t.Error("expected the campaign to hold the larger accumulated statistic")
	}
}

// TestEntityDayCorrectionPenalisesManyChances: an entity with more events has more
// chances at an extreme one, and the corrected minimum must charge it for them.
func TestEntityDayCorrectionPenalisesManyChances(t *testing.T) {
	a := newAccumulator(&redTeamLabels{keys: map[string]struct{}{}}, 200, testBudgets, false, weightingNone, onlineNone, "")
	feed(a, "quiet@DOM1", 3, []float64{-30}, 0)
	noisy := make([]float64, 1000)
	for i := range noisy {
		noisy[i] = -10
	}
	noisy[0] = -30
	feed(a, "noisy@DOM1", 3, noisy, 0)

	res := a.entityDayResults([]int{10})
	corrected, ok := res["corrected_minimum"].(map[string]any)
	if !ok {
		t.Fatal("expected a corrected_minimum block")
	}
	top, ok := corrected["top"].(map[string]any)
	if !ok {
		t.Fatal("expected a top block")
	}
	day3, ok := top["day_03"].([]entityDayRow)
	if !ok {
		t.Fatalf("expected day_03 rows, got %T", top["day_03"])
	}
	if len(day3) != 2 {
		t.Fatalf("expected two entity-days, got %d", len(day3))
	}
	// Both have the same best event; the quiet account had one chance at it and must
	// therefore rank ahead.
	if day3[0].Entity != "quiet@DOM1" {
		t.Errorf("ranked %q first; the account with one chance at Ã¢Ë†â€™30 must outrank the "+
			"one with a thousand", day3[0].Entity)
	}
	if want := -30 + math.Log(1000); math.Abs(day3[1].CorrectedLogP-want) > 1e-12 {
		t.Errorf("corrected log p = %v, want %v = Ã¢Ë†â€™30 + ln(1000)",
			day3[1].CorrectedLogP, want)
	}
}

// TestEntityDayDetectionCountsLabelledDays: an entity-day is a true positive when any of
// its events was labelled, which is the entity-scope analogue of the event-level table.
func TestEntityDayDetectionCountsLabelledDays(t *testing.T) {
	a := newAccumulator(&redTeamLabels{keys: map[string]struct{}{}}, 200, testBudgets, false, weightingNone, onlineNone, "")
	// One labelled entity-day that is genuinely extreme, and three benign ones that are
	// not, so a budget of one must find it.
	feed(a, "attacker@DOM1", 5, []float64{-90, -88}, 2)
	feed(a, "benign1@DOM1", 5, []float64{-10}, 0)
	feed(a, "benign2@DOM1", 5, []float64{-11}, 0)
	feed(a, "benign3@DOM1", 5, []float64{-12}, 0)

	res := a.entityDayResults([]int{1})
	if got := res["labelled_entity_days"]; got != 1 {
		t.Errorf("labelled entity-days = %v, want 1", got)
	}
	if got := res["total_entity_days"]; got != 4 {
		t.Errorf("total entity-days = %v, want 4", got)
	}

	for _, ranking := range []string{"corrected_minimum", "fisher_over_the_day"} {
		block := res[ranking].(map[string]any)
		det := block["detections_at_budget"].(map[string]any)
		b1 := det["budget_1_per_day"].(map[string]any)
		if b1["true_positives"] != 1 {
			t.Errorf("%s at budget 1: true positives = %v, want 1",
				ranking, b1["true_positives"])
		}
		if b1["entity_days_alerted"] != 1 {
			t.Errorf("%s at budget 1: alerted = %v, want 1",
				ranking, b1["entity_days_alerted"])
		}
	}
}

// TestEntityDayRankingIsDeterministic: the same events folded in the same order must
// rank identically, and ties must break on a stated key rather than on map order (R4).
func TestEntityDayRankingIsDeterministic(t *testing.T) {
	build := func() []entityDayRow {
		a := newAccumulator(&redTeamLabels{keys: map[string]struct{}{}}, 200, testBudgets, false, weightingNone, onlineNone, "")
		for i := range 50 {
			// Deliberate ties: every entity has the identical single event.
			feed(a, string(rune('a'+i%26))+"@DOM1", 1, []float64{-20}, 0)
		}
		res := a.entityDayResults([]int{5})
		block := res["corrected_minimum"].(map[string]any)
		top := block["top"].(map[string]any)
		return top["day_01"].([]entityDayRow)
	}
	first, second := build(), build()
	if len(first) != len(second) {
		t.Fatalf("lengths differ: %d then %d", len(first), len(second))
	}
	for i := range first {
		if first[i].Entity != second[i].Entity {
			t.Fatalf("position %d: %q then %q; tied entity-days must break on a stated key",
				i, first[i].Entity, second[i].Entity)
		}
	}
}
