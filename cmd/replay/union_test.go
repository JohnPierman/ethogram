package main

import (
	"reflect"
	"testing"

	"github.com/JohnPierman/ethogram/domain/cooccurrence"
	"github.com/JohnPierman/ethogram/domain/detector"
	"github.com/JohnPierman/ethogram/domain/marginal"
	"github.com/JohnPierman/ethogram/domain/novelty"
	"github.com/JohnPierman/ethogram/domain/objective"
	"github.com/JohnPierman/ethogram/domain/pairing"
	"github.com/JohnPierman/ethogram/domain/volume"
)

// mkAlert builds an alert with a distinct identity per t.
func mkAlert(t int64, entity string, logP float64, red bool) alert {
	return alert{
		P: 0, LogP: logP, ModelLogP: logP, TSeconds: t,
		Entity: entity, SrcComp: "C1", DstComp: "C2", IsRedTeam: red,
	}
}

func mkDay(als ...alert) *dayAlerts { return &dayAlerts{alerts: als} }

// The whole point of the arm: an event two arms both rank is ONE alert, credited to both,
// at the better of the two ranks. The arms' sets are not disjoint and a union that
// double-counted would inflate every queue size it reports.
func TestFuseDayDeduplicatesAcrossArms(t *testing.T) {
	shared := mkAlert(100, "U1", -20, true)
	byArm := map[detector.ID]*dayAlerts{
		novelty.DetectorID: mkDay(mkAlert(50, "U9", -30, false), shared),
		pairing.DetectorID: mkDay(shared),
	}
	ids := []detector.ID{novelty.DetectorID, pairing.DetectorID}

	got := fuseDay(byArm, ids, 10)

	if len(got) != 2 {
		t.Fatalf("union size = %d, want 2 distinct events", len(got))
	}
	var found *fusedAlert
	for i := range got {
		if got[i].al.TSeconds == 100 {
			found = &got[i]
		}
	}
	if found == nil {
		t.Fatal("the shared event is missing from the union")
	}
	if found.rank != 1 {
		t.Errorf("shared event rank = %d, want 1: pairing ranked it first", found.rank)
	}
	if len(found.carriers) != 2 {
		t.Errorf("carriers = %v, want both arms credited", found.carriers)
	}
}

// A rank collision must resolve on the event identity, never on the p-value. Every arm has
// a rank 1, so collisions are structural rather than incidental; resolving them on log p
// would hand each one to whichever detector's p-values are numerically smallest, which is
// the bias the arm exists to remove.
func TestFusedLessIgnoresPValueOnRankTies(t *testing.T) {
	// Earlier timestamp, far LESS extreme p-value.
	early := fusedAlert{al: mkAlert(100, "U1", -1, false), rank: 1}
	// Later timestamp, far MORE extreme p-value.
	late := fusedAlert{al: mkAlert(200, "U1", -300, false), rank: 1}

	if !fusedLess(early, late) {
		t.Error("a rank tie resolved toward the smaller p-value; it must resolve on identity")
	}
	if fusedLess(late, early) {
		t.Error("fusedLess is not antisymmetric on a rank tie")
	}

	better := fusedAlert{al: mkAlert(900, "U9", -1, false), rank: 1}
	worse := fusedAlert{al: mkAlert(100, "U1", -300, false), rank: 2}
	if !fusedLess(better, worse) {
		t.Error("rank must dominate the identity tie-break")
	}
}

// Determinism (R4): the union is assembled through maps, so its order must come from the
// comparator and not from map iteration. A single pass cannot show this; repetition can.
func TestFuseDayIsDeterministic(t *testing.T) {
	byArm := map[detector.ID]*dayAlerts{
		novelty.DetectorID: mkDay(mkAlert(10, "U1", -9, false), mkAlert(20, "U2", -8, true)),
		pairing.DetectorID: mkDay(mkAlert(20, "U2", -7, true), mkAlert(30, "U3", -6, false)),
		volume.DetectorID:  mkDay(mkAlert(30, "U3", -5, false), mkAlert(10, "U1", -4, false)),
	}
	ids := []detector.ID{novelty.DetectorID, pairing.DetectorID, volume.DetectorID}

	first := fuseDay(byArm, ids, 10)
	for i := 0; i < 64; i++ {
		again := fuseDay(byArm, ids, 10)
		if !reflect.DeepEqual(identities(first), identities(again)) {
			t.Fatalf("union order differs between runs:\n%v\n%v",
				identities(first), identities(again))
		}
	}
}

func identities(fs []fusedAlert) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, alertIdentity(f.al))
	}
	return out
}

// depth bounds what each arm contributes, so a deeper union is a superset of a shallower
// one and can only find more.
func TestFuseDayDepthIsMonotone(t *testing.T) {
	byArm := map[detector.ID]*dayAlerts{
		novelty.DetectorID: mkDay(mkAlert(1, "U1", -9, false), mkAlert(2, "U2", -8, false)),
		pairing.DetectorID: mkDay(mkAlert(3, "U3", -7, false), mkAlert(4, "U4", -6, false)),
	}
	ids := []detector.ID{novelty.DetectorID, pairing.DetectorID}

	shallow := fuseDay(byArm, ids, 1)
	deep := fuseDay(byArm, ids, 2)
	if len(shallow) != 2 || len(deep) != 4 {
		t.Fatalf("depth 1 gave %d and depth 2 gave %d, want 2 and 4",
			len(shallow), len(deep))
	}
	have := map[string]bool{}
	for _, k := range identities(deep) {
		have[k] = true
	}
	for _, k := range identities(shallow) {
		if !have[k] {
			t.Errorf("%s is in the shallow union but not the deep one", k)
		}
	}
}

// The union can only find at least what its best single arm finds at the same depth. This
// is the property that makes the arm worth measuring, and it is cheap to assert.
func TestUnionFindsAtLeastItsBestArm(t *testing.T) {
	byArm := map[detector.ID]*dayAlerts{
		novelty.DetectorID: mkDay(mkAlert(1, "U1", -9, true), mkAlert(2, "U2", -8, false)),
		pairing.DetectorID: mkDay(mkAlert(3, "U3", -7, true), mkAlert(4, "U4", -6, true)),
	}
	ids := []detector.ID{novelty.DetectorID, pairing.DetectorID}

	c := newUnionCounts()
	c.tally(fuseDay(byArm, ids, 2), 0)

	if c.truePos != 3 {
		t.Errorf("union true positives = %d, want 3 (1 + 2, none shared)", c.truePos)
	}
	if c.alerts != 4 {
		t.Errorf("union alerts = %d, want 4", c.alerts)
	}
}

// A limit is the equal-cost accounting: the union is charged the same alerts per day as
// every other arm. A limit of 0 is the equal-depth accounting and truncates nothing.
func TestTallyLimitTruncates(t *testing.T) {
	fused := []fusedAlert{
		{al: mkAlert(1, "U1", -9, true), rank: 1, carriers: []detector.ID{novelty.DetectorID}},
		{al: mkAlert(2, "U2", -8, true), rank: 2, carriers: []detector.ID{pairing.DetectorID}},
		{al: mkAlert(3, "U3", -7, false), rank: 3, carriers: []detector.ID{volume.DetectorID}},
	}

	capped := newUnionCounts()
	capped.tally(fused, 2)
	if capped.alerts != 2 || capped.truePos != 2 {
		t.Errorf("capped = %d alerts / %d tp, want 2 / 2", capped.alerts, capped.truePos)
	}

	whole := newUnionCounts()
	whole.tally(fused, 0)
	if whole.alerts != 3 || whole.truePos != 2 {
		t.Errorf("whole = %d alerts / %d tp, want 3 / 2", whole.alerts, whole.truePos)
	}
}

// exclusive_true_positives answers "what would dropping this arm cost", so it must count
// only the labelled alerts no other arm carried.
func TestExclusiveTruePositives(t *testing.T) {
	shared := mkAlert(1, "U1", -9, true)
	fused := []fusedAlert{
		{al: shared, rank: 1,
			carriers: []detector.ID{novelty.DetectorID, pairing.DetectorID}},
		{al: mkAlert(2, "U2", -8, true), rank: 2,
			carriers: []detector.ID{pairing.DetectorID}},
		{al: mkAlert(3, "U3", -7, false), rank: 3,
			carriers: []detector.ID{volume.DetectorID}},
	}

	c := newUnionCounts()
	c.tally(fused, 0)

	if got := c.exclusive[pairing.DetectorID]; got != 1 {
		t.Errorf("pairing exclusive = %d, want 1", got)
	}
	if got := c.exclusive[novelty.DetectorID]; got != 0 {
		t.Errorf("novelty exclusive = %d, want 0: it shared its only labelled alert", got)
	}
	if got := c.exclusive[volume.DetectorID]; got != 0 {
		t.Errorf("volume exclusive = %d, want 0: its exclusive alert is not labelled", got)
	}
}

// The entity-scope grouping is chosen on the design's argument and not on the labels, so
// it must be exactly the arms that are not population-scope.
func TestUnionArmIDsExcludesPopulationScope(t *testing.T) {
	a := newAccumulator(nil, 10, objective.Budgets{}, false, weightingNone)
	for _, id := range []detector.ID{
		novelty.DetectorID, pairing.DetectorID, marginal.DetectorID,
		cooccurrence.DetectorID,
	} {
		a.detectorPerDay[id] = map[int64]*dayAlerts{7: mkDay()}
	}

	all := a.unionArmIDs(false)
	if len(all) != 4 {
		t.Fatalf("all arms = %v, want 4", all)
	}

	entity := a.unionArmIDs(true)
	want := []detector.ID{novelty.DetectorID, pairing.DetectorID}
	if !reflect.DeepEqual(entity, want) {
		t.Errorf("entity-scope arms = %v, want %v", entity, want)
	}
}

// The two accountings must differ in exactly the documented way: equal cost never exceeds
// the budget, equal depth never falls below it while the arms have alerts to give.
func TestUnionGroupingAccountings(t *testing.T) {
	a := newAccumulator(nil, 10, objective.Budgets{}, false, weightingNone)
	a.detectorPerDay[novelty.DetectorID] = map[int64]*dayAlerts{
		7: mkDay(mkAlert(1, "U1", -9, true), mkAlert(2, "U2", -8, false)),
	}
	a.detectorPerDay[pairing.DetectorID] = map[int64]*dayAlerts{
		7: mkDay(mkAlert(3, "U3", -7, true), mkAlert(4, "U4", -6, false)),
	}

	got := a.unionGrouping(a.unionArmIDs(false), []int{2})
	cost := got["at_equal_cost"].(map[string]any)["budget_2_per_day"].(map[string]any)
	depth := got["at_equal_depth"].(map[string]any)["budget_2_per_day"].(map[string]any)

	if cost["alerts"].(int) != 2 {
		t.Errorf("equal-cost alerts = %v, want 2 (the budget)", cost["alerts"])
	}
	if depth["alerts"].(int) != 4 {
		t.Errorf("equal-depth alerts = %v, want 4 (both arms' top 2, disjoint)",
			depth["alerts"])
	}
	if m := depth["budget_multiple"].(float64); m != 2 {
		t.Errorf("budget_multiple = %v, want 2: the union cost twice the budget", m)
	}
	if depth["true_positives"].(int) != 2 {
		t.Errorf("equal-depth tp = %v, want 2", depth["true_positives"])
	}
}

// The union must name the labelled events it caught, not only count them: a fused rank is
// not a p-value, so nothing downstream can reconstruct this queue the way it reconstructs a
// per-detector arm's. Without the keys every per-attack-type row for this arm is blank.
func TestUnionNamesTheLabelledEventsItCaught(t *testing.T) {
	fused := []fusedAlert{
		{al: mkAlert(500, "U1", -9, true), rank: 1,
			carriers: []detector.ID{novelty.DetectorID}},
		{al: mkAlert(600, "U2", -8, false), rank: 2,
			carriers: []detector.ID{pairing.DetectorID}},
		{al: mkAlert(700, "U3", -7, true), rank: 3,
			carriers: []detector.ID{pairing.DetectorID}},
	}

	c := newUnionCounts()
	c.tally(fused, 0)

	want := []string{"500|U1", "700|U3"}
	if got := c.caughtKeys(); !reflect.DeepEqual(got, want) {
		t.Errorf("caught keys = %v, want %v", got, want)
	}
	if len(c.caught) != 2 {
		t.Errorf("distinct caught = %d, want 2", len(c.caught))
	}
}

// One labelled (t, entity) can alert on several component pairs. Those are several alerts
// and one labelled event, and a per-type table read against a labelled-event denominator
// must use the distinct count or it can report catching more events than exist.
func TestDistinctTruePositivesDeduplicatesComponentPairs(t *testing.T) {
	a := mkAlert(800, "U7", -9, true)
	b := mkAlert(800, "U7", -8, true)
	b.DstComp = "C999" // same labelled event, different pair, so a second alert
	fused := []fusedAlert{
		{al: a, rank: 1, carriers: []detector.ID{novelty.DetectorID}},
		{al: b, rank: 2, carriers: []detector.ID{novelty.DetectorID}},
	}

	c := newUnionCounts()
	c.tally(fused, 0)

	if c.truePos != 2 {
		t.Errorf("alert-level true positives = %d, want 2", c.truePos)
	}
	if len(c.caught) != 1 {
		t.Errorf("distinct labelled events = %d, want 1", len(c.caught))
	}
	rep := c.report(10, 0)
	if rep["distinct_true_positives"].(int) != 1 {
		t.Errorf("reported distinct = %v, want 1", rep["distinct_true_positives"])
	}
}

// An arm with no alerts on a day must not break the union or be miscounted as present.
func TestFuseDayToleratesAbsentArms(t *testing.T) {
	byArm := map[detector.ID]*dayAlerts{
		novelty.DetectorID: mkDay(mkAlert(1, "U1", -9, true)),
		pairing.DetectorID: nil,
	}
	ids := []detector.ID{novelty.DetectorID, pairing.DetectorID, volume.DetectorID}

	got := fuseDay(byArm, ids, 5)
	if len(got) != 1 {
		t.Fatalf("union size = %d, want 1", len(got))
	}
	if !reflect.DeepEqual(got[0].carriers, []detector.ID{novelty.DetectorID}) {
		t.Errorf("carriers = %v, want novelty alone", got[0].carriers)
	}
}
