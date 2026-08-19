package main

import (
	"math"
	"testing"

	"github.com/JohnPierman/ethogram/domain/objective"
)

// objectiveFixture builds an arm and its labelled population such that the two operating
// points are the shape the recorded runs actually have: a tiny queue that is pure, and a
// larger one that finds more while being mostly wrong.
//
// Day 7 holds four events in ascending p. The first is labelled, the next two are not, and
// the fourth is. So a budget of 1 gives 1 true positive and 0 false, and a budget of 4
// gives 2 true and 2 false.
func objectiveFixture() (arm, redTeamPopulation) {
	primary := buildArm("framework", map[int64][]alertRow{
		7: {
			{P: 1e-12, LogP: -27.6, TSeconds: 7 * day, Entity: "U1", IsRedTeam: true},
			{P: 1e-11, LogP: -25.3, TSeconds: 7*day + 1, Entity: "U2"},
			{P: 1e-10, LogP: -23.0, TSeconds: 7*day + 2, Entity: "U3"},
			{P: 1e-9, LogP: -20.7, TSeconds: 7*day + 3, Entity: "U4", IsRedTeam: true},
		},
	})
	results := map[string]any{
		"red_team_scored": []any{
			map[string]any{"t": float64(7 * day), "entity": "U1"},
			map[string]any{"t": float64(7*day + 3), "entity": "U4"},
		},
	}
	return primary, loadRedTeamPopulation(results, primary)
}

// TestBreakEvenIsReportedWithoutAnyChosenConstant. The break-even ratio is the default
// report precisely because it commits nobody to a cost model: it must appear with no
// -value-ratio supplied, and the objective must not.
func TestBreakEvenIsReportedWithoutAnyChosenConstant(t *testing.T) {
	primary, pop := objectiveFixture()

	rows := detectionTable(primary, pop, objective.Budgets{1, 4}, nil)
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}

	// Budget 4: 2 true, 2 false, so an exchange rate of 1 is where it starts to pay.
	four := rows[1]
	if four.FalsePositives != 2 {
		t.Errorf("false positives = %d, want 2", four.FalsePositives)
	}
	if four.BreakEvenValueRatio == nil {
		t.Fatal("break-even must be reported with no value ratio supplied")
	}
	if got := *four.BreakEvenValueRatio; math.Abs(got-1.0) > 1e-12 {
		t.Errorf("break-even = %v, want 1", got)
	}
	if four.Objective != nil || four.IsWorthwhile != nil {
		t.Error("the objective must be unscored until an exchange rate is supplied")
	}
	if four.IsObjectiveTop != nil {
		t.Error("no operating point may be marked as the maximum of an unscored objective")
	}

	// Budget 1: 1 true, 0 false. The ratio is unbounded there and must be omitted
	// rather than serialised as an infinity.
	if rows[0].TrueFalseRatio != nil {
		t.Error("TP/FP must be omitted when there are no false positives")
	}
	if rows[0].BreakEvenValueRatio == nil || *rows[0].BreakEvenValueRatio != 0 {
		t.Error("a queue with no false positives breaks even at an exchange rate of 0")
	}
}

// TestObjectiveSelectsTheOperatingPointRatherThanThePurestQueue is the point of the
// change, expressed as a test.
//
// Ranked on TP/FP the budget of 1 wins outright: it is unbounded, being perfectly pure.
// The objective must not choose it at an exchange rate where finding the second true
// positive is worth the two false ones it costs.
func TestObjectiveSelectsTheOperatingPointRatherThanThePurestQueue(t *testing.T) {
	primary, pop := objectiveFixture()

	// v/c = 3: budget 1 scores 3·1 − 0 = 3; budget 4 scores 3·2 − 2 = 4.
	u, err := objective.NewUtility(3)
	if err != nil {
		t.Fatal(err)
	}
	rows := detectionTable(primary, pop, objective.Budgets{1, 4}, &u)

	if rows[0].Objective == nil || rows[1].Objective == nil {
		t.Fatal("both rows must carry a scored objective")
	}
	if got := *rows[0].Objective; math.Abs(got-3) > 1e-12 {
		t.Errorf("budget 1 scored %v, want 3", got)
	}
	if got := *rows[1].Objective; math.Abs(got-4) > 1e-12 {
		t.Errorf("budget 4 scored %v, want 4", got)
	}
	if rows[0].IsObjectiveTop != nil {
		t.Error("the pure queue must not be marked the maximum at this exchange rate")
	}
	if rows[1].IsObjectiveTop == nil || !*rows[1].IsObjectiveTop {
		t.Error("the larger queue is the objective's maximum at this exchange rate and " +
			"must be marked as such")
	}
}

// TestObjectiveFollowsTheOperatorNotTheImplementation: at a low enough valuation the small
// clean queue genuinely is the right answer, and the objective must say so. This is the
// guard against the selection being hard-wired in either direction.
func TestObjectiveFollowsTheOperatorNotTheImplementation(t *testing.T) {
	primary, pop := objectiveFixture()

	// v/c = 1.5: budget 1 scores 1.5; budget 4 scores 3 − 2 = 1.
	u, err := objective.NewUtility(1.5)
	if err != nil {
		t.Fatal(err)
	}
	rows := detectionTable(primary, pop, objective.Budgets{1, 4}, &u)

	if rows[0].IsObjectiveTop == nil || !*rows[0].IsObjectiveTop {
		t.Error("at this exchange rate the small queue is the maximum")
	}
	if rows[1].IsObjectiveTop != nil {
		t.Error("only one operating point may be marked the maximum")
	}
}

// TestObjectiveProvenanceRecordsWhetherItWasScored. A result that carries no objective
// must say why, or a reader cannot tell an unscored run from one whose objective was zero.
func TestObjectiveProvenanceRecordsWhetherItWasScored(t *testing.T) {
	unscored := objectiveProvenance(nil)
	if unscored["scored"] != false {
		t.Error("an unscored run must record scored = false")
	}
	if unscored["value_ratio"] != nil {
		t.Error("an unscored run must record a nil value ratio, not a default")
	}
	if _, ok := unscored["note"]; !ok {
		t.Error("an unscored run must say what is reported instead")
	}

	u, err := objective.NewUtility(7)
	if err != nil {
		t.Fatal(err)
	}
	scored := objectiveProvenance(&u)
	if scored["scored"] != true {
		t.Error("a scored run must record scored = true")
	}
	if got := scored["value_ratio"]; got != 7.0 {
		t.Errorf("value_ratio = %v, want 7", got)
	}
	// 1/(1+7): the precision a queue must beat to be worth reading at this rate.
	if got, ok := scored["minimum_precision"].(float64); !ok || math.Abs(got-0.125) > 1e-12 {
		t.Errorf("minimum_precision = %v, want 0.125", scored["minimum_precision"])
	}
}
