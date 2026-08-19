package main

import (
	"math"
	"testing"

	"github.com/JohnPierman/ethogram/domain/objective"
)

// cutoffFixture builds one day of ten ranked alerts in which the labelled events are
// concentrated at the top: ranks 1, 2, 3 and 6 are labelled, the rest are not.
//
// So a budget of 10 emits 10 alerts for 4 true positives, while stopping at 3 emits 3 for
// 3. Which is better is entirely a question of what a true positive is worth, which is the
// point of the cutoff.
func cutoffFixture() (arm, redTeamPopulation) {
	rows := []alertRow{
		{LogP: -30, TSeconds: 7 * day, Entity: "U1", IsRedTeam: true},
		{LogP: -29, TSeconds: 7*day + 1, Entity: "U2", IsRedTeam: true},
		{LogP: -28, TSeconds: 7*day + 2, Entity: "U3", IsRedTeam: true},
		{LogP: -27, TSeconds: 7*day + 3, Entity: "U4"},
		{LogP: -26, TSeconds: 7*day + 4, Entity: "U5"},
		{LogP: -25, TSeconds: 7*day + 5, Entity: "U6", IsRedTeam: true},
		{LogP: -24, TSeconds: 7*day + 6, Entity: "U7"},
		{LogP: -23, TSeconds: 7*day + 7, Entity: "U8"},
		{LogP: -22, TSeconds: 7*day + 8, Entity: "U9"},
		{LogP: -21, TSeconds: 7*day + 9, Entity: "U10"},
	}
	a := buildArm("framework", map[int64][]alertRow{7: rows})
	pop := redTeamPopulation{
		keys: []eventKey{
			{t: 7 * day, entity: "U1"}, {t: 7*day + 1, entity: "U2"},
			{t: 7*day + 2, entity: "U3"}, {t: 7*day + 5, entity: "U6"},
		},
		categories: map[eventKey][]string{},
	}
	return a, pop
}

// TestCutoffStopsShortOfTheBudgetWhenMarginalAlertsDoNotPay is the property the whole
// thing exists for: a budget is a ceiling, not a quota.
//
// At v/c = 2 the fourth true positive at rank 6 costs two false positives to reach and
// four more sit behind it, so the utility-maximising queue stops at rank 3 and emits three
// alerts rather than the ten the budget permits.
func TestCutoffStopsShortOfTheBudgetWhenMarginalAlertsDoNotPay(t *testing.T) {
	a, pop := cutoffFixture()
	u, err := objective.NewUtility(2)
	if err != nil {
		t.Fatal(err)
	}

	got, ok := cutoffAt(a, pop, 10, u)
	if !ok {
		t.Fatal("no cutoff computed")
	}
	if got.AlertsAtBudget != 10 {
		t.Errorf("alerts at budget = %d, want 10", got.AlertsAtBudget)
	}
	if got.TruePositivesAtBudget != 4 {
		t.Errorf("true positives at budget = %d, want 4", got.TruePositivesAtBudget)
	}
	if got.OptimalAlerts != 3 {
		t.Errorf("optimal alerts = %d, want 3 — the budget is a ceiling, not a quota",
			got.OptimalAlerts)
	}
	if got.TruePositivesAtOptimal != 3 {
		t.Errorf("true positives at optimum = %d, want 3", got.TruePositivesAtOptimal)
	}
	// U(3) = 2*3 - 0 = 6; U(10) = 2*4 - 6 = 2.
	if math.Abs(got.ObjectiveAtOptimal-6) > 1e-12 {
		t.Errorf("objective at optimum = %v, want 6", got.ObjectiveAtOptimal)
	}
	if math.Abs(got.ObjectiveAtBudget-2) > 1e-12 {
		t.Errorf("objective at budget = %v, want 2", got.ObjectiveAtBudget)
	}
}

// TestCutoffReachesADeeperTruePositiveWhenItIsWorthIt: the cutoff is not biased toward
// truncating, it follows the exchange rate. At v/c = 20 the fourth true positive at rank 6
// is worth the two false ones needed to reach it, so the queue extends from 3 to 6 - but
// never to 10, because ranks 7 to 10 hold nothing true and every one of them is pure cost.
func TestCutoffReachesADeeperTruePositiveWhenItIsWorthIt(t *testing.T) {
	a, pop := cutoffFixture()
	u, err := objective.NewUtility(20)
	if err != nil {
		t.Fatal(err)
	}

	got, ok := cutoffAt(a, pop, 10, u)
	if !ok {
		t.Fatal("no cutoff computed")
	}
	// U(3) = 60; U(6) = 20*4 - 2 = 78; U(10) = 20*4 - 6 = 74.
	if got.OptimalAlerts != 6 {
		t.Errorf("optimal alerts = %d, want 6 - deep enough for the fourth true positive, "+
			"no deeper", got.OptimalAlerts)
	}
	if got.TruePositivesAtOptimal != 4 {
		t.Errorf("true positives at optimum = %d, want 4", got.TruePositivesAtOptimal)
	}
	if math.Abs(got.ObjectiveAtOptimal-78) > 1e-12 {
		t.Errorf("objective at optimum = %v, want 78", got.ObjectiveAtOptimal)
	}
	if got.AlertsSuppressed != 4 || got.TruePositivesForgone != 0 {
		t.Errorf("suppressed %d alerts costing %d true positives, want 4 and 0",
			got.AlertsSuppressed, got.TruePositivesForgone)
	}
}

// TestCutoffTakesTheWholeBudgetWhenTheLastAlertPays is the guard against a truncation
// bias: where the deepest alert is itself a true positive, the whole budget is optimal and
// the cutoff must say so.
func TestCutoffTakesTheWholeBudgetWhenTheLastAlertPays(t *testing.T) {
	rows := []alertRow{
		{LogP: -30, TSeconds: 7 * day, Entity: "U1", IsRedTeam: true},
		{LogP: -29, TSeconds: 7*day + 1, Entity: "U2"},
		{LogP: -28, TSeconds: 7*day + 2, Entity: "U3", IsRedTeam: true},
	}
	a := buildArm("framework", map[int64][]alertRow{7: rows})
	pop := redTeamPopulation{
		keys:       []eventKey{{t: 7 * day, entity: "U1"}, {t: 7*day + 2, entity: "U3"}},
		categories: map[eventKey][]string{},
	}
	u, err := objective.NewUtility(5)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := cutoffAt(a, pop, 3, u)
	if !ok {
		t.Fatal("no cutoff computed")
	}
	if got.OptimalAlerts != 3 {
		t.Errorf("optimal alerts = %d, want the full 3", got.OptimalAlerts)
	}
	if got.AlertsSuppressed != 0 {
		t.Errorf("suppressed %d, want 0", got.AlertsSuppressed)
	}
}

// TestCutoffEmitsNothingWhenNothingPays. If no prefix beats an empty queue the honest
// answer is to emit nothing, and the report must be able to say so rather than returning
// the least bad queue as though it were worthwhile.
func TestCutoffEmitsNothingWhenNothingPays(t *testing.T) {
	rows := make([]alertRow, 10)
	for i := range rows {
		rows[i] = alertRow{LogP: float64(-30 + i), TSeconds: 7*day + int64(i), Entity: "U"}
	}
	a := buildArm("framework", map[int64][]alertRow{7: rows})
	pop := redTeamPopulation{keys: []eventKey{{t: 1, entity: "X"}}, categories: map[eventKey][]string{}}

	u, err := objective.NewUtility(5)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := cutoffAt(a, pop, 10, u)
	if !ok {
		t.Fatal("no cutoff computed")
	}
	if got.OptimalAlerts != 0 {
		t.Errorf("optimal alerts = %d, want 0 — no prefix beats an empty queue", got.OptimalAlerts)
	}
	if got.ObjectiveAtOptimal != 0 {
		t.Errorf("objective at optimum = %v, want exactly 0", got.ObjectiveAtOptimal)
	}
	if got.IsWorthwhileAtOptimal {
		t.Error("an empty queue must not be reported as worthwhile")
	}
}

// TestCutoffIsBoundedByTheBudget: the ceiling is real. Even where rank 11 would pay, a
// budget of 5 cannot emit it.
func TestCutoffIsBoundedByTheBudget(t *testing.T) {
	a, pop := cutoffFixture()
	u, err := objective.NewUtility(20)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := cutoffAt(a, pop, 5, u)
	if !ok {
		t.Fatal("no cutoff computed")
	}
	if got.AlertsAtBudget != 5 {
		t.Errorf("alerts at budget = %d, want 5", got.AlertsAtBudget)
	}
	if got.OptimalAlerts > 5 {
		t.Errorf("optimal alerts = %d, must not exceed the budget", got.OptimalAlerts)
	}
}

// TestCutoffPoolsAcrossDays: the budget is per day, so a two-day window at a budget of two
// offers four alerts, and the cutoff ranks them against each other rather than per day.
func TestCutoffPoolsAcrossDays(t *testing.T) {
	a := buildArm("framework", map[int64][]alertRow{
		7: {
			{LogP: -30, TSeconds: 7 * day, Entity: "U1", IsRedTeam: true},
			{LogP: -10, TSeconds: 7*day + 1, Entity: "U2"},
		},
		8: {
			{LogP: -29, TSeconds: 8 * day, Entity: "U3", IsRedTeam: true},
			{LogP: -11, TSeconds: 8*day + 1, Entity: "U4"},
		},
	})
	pop := redTeamPopulation{
		keys:       []eventKey{{t: 7 * day, entity: "U1"}, {t: 8 * day, entity: "U3"}},
		categories: map[eventKey][]string{},
	}
	u, err := objective.NewUtility(3)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := cutoffAt(a, pop, 2, u)
	if !ok {
		t.Fatal("no cutoff computed")
	}
	if got.AlertsAtBudget != 4 {
		t.Errorf("alerts at budget = %d, want 4 across two days", got.AlertsAtBudget)
	}
	// Pooled order is -30, -29, -11, -10: both labelled events come first, so the
	// optimum is two alerts.
	if got.OptimalAlerts != 2 || got.TruePositivesAtOptimal != 2 {
		t.Errorf("optimum = %d alerts / %d true positives, want 2 and 2",
			got.OptimalAlerts, got.TruePositivesAtOptimal)
	}
}
