package main

import (
	"math"
	"testing"

	"github.com/JohnPierman/ethogram/domain/allocation"
	"github.com/JohnPierman/ethogram/domain/detector"
	"github.com/JohnPierman/ethogram/domain/novelty"
	"github.com/JohnPierman/ethogram/domain/objective"
	"github.com/JohnPierman/ethogram/domain/volume"
)

// mkWeightedAcc builds an accumulator with a burn-in mirror for two detectors: one whose
// labelled burn-in events sit deep in its own tail, one whose sit where anything sits.
func mkWeightedAcc(t *testing.T, labelled bool) *accumulator {
	t.Helper()
	acc := newAccumulator(&redTeamLabels{
		keys: map[string]struct{}{}, users: map[string]struct{}{},
		days: map[int64]bool{1: true},
	}, 50, objective.Budgets{10}, true, weightingNone, onlineNone, "", mustRouter(t))

	// A burn-in null for each detector: 400 observations spread over several decades, and
	// 40,000 evaluations, so a retained observation is genuinely in the tail.
	for _, id := range []detector.ID{novelty.DetectorID, volume.DetectorID} {
		acc.burnInPerDay[id] = map[int64]*dayAlerts{}
		acc.burnInScored[id] = map[int64]int64{1: 40000}
		als := make([]alert, 0, 400)
		for i := range 400 {
			als = append(als, mkAlert(int64(200000+i), "U9", -float64(400-i)/8, false))
		}
		acc.burnInPerDay[id][1] = &dayAlerts{alerts: als}
	}

	if !labelled {
		return acc
	}

	// Six labelled burn-in events. The novelty arm surfaces all of them at its most
	// extreme ranks; the volume arm evaluates them and surfaces none.
	for i := range 6 {
		ts := int64(100000 + i)
		acc.burnInLabelled = append(acc.burnInLabelled, ledgerLabelled{
			Key: "k", TSeconds: ts, Entity: "U1", Day: 1,
			LogDetector: map[string]float64{
				string(novelty.DetectorID): -60,
				string(volume.DetectorID):  -0.2,
			},
		})
		red := mkAlert(ts, "U1", -60, true)
		nov := acc.burnInPerDay[novelty.DetectorID][1]
		nov.alerts = append([]alert{red}, nov.alerts...)
	}
	return acc
}

// TestFitWeightedArmSeparatesAnInformativeArmFromAUselessOne is the property the arm exists
// for. Both detectors here have identically shaped nulls; only where their labelled burn-in
// events sat differs, and that alone must decide which one earns a share of the budget.
func TestFitWeightedArmSeparatesAnInformativeArmFromAUselessOne(t *testing.T) {
	acc := mkWeightedAcc(t, true)

	w := acc.fitWeightedArm()
	if w == nil {
		t.Fatal("no arm was fitted from six labelled burn-in events")
	}

	nov, ok := w.weights[novelty.DetectorID]
	if !ok {
		t.Fatalf("the novelty arm was skipped: %v", w.skipped)
	}
	vol, ok := w.weights[volume.DetectorID]
	if !ok {
		t.Fatalf("the volume arm was skipped: %v", w.skipped)
	}

	if !nov.IsInformative() {
		t.Errorf("the arm that surfaced every labelled event fitted a = %v, uninformative",
			nov.A())
	}
	if vol.IsInformative() {
		t.Errorf("the arm that surfaced none fitted a = %v, informative", vol.A())
	}

	// The useless arm's evidence must be recorded as censored misses rather than as an
	// absence, or the fit would have nothing to penalise it with.
	if r := w.reports[volume.DetectorID]; r.Censored != 6 || r.Observed != 0 {
		t.Errorf("volume report = %+v, want 6 censored and 0 observed", r)
	}
	if r := w.reports[novelty.DetectorID]; r.Observed != 6 {
		t.Errorf("novelty report = %+v, want 6 observed", r)
	}
}

// TestFitWeightedArmDeclinesWithNoBurnInLabels pins the honest-unmeasured path. A corpus
// whose labels all fall after the boundary supports no fitted weight, and an arm reporting
// every weight as uninformative would be an equal quota under another name.
func TestFitWeightedArmDeclinesWithNoBurnInLabels(t *testing.T) {
	acc := mkWeightedAcc(t, false)
	if w := acc.fitWeightedArm(); w != nil {
		t.Fatal("an arm was fitted with no labelled burn-in event to fit it on")
	}
	got := acc.weightedResults([]int{10})
	if measured, _ := got["measured"].(bool); measured {
		t.Error("the arm reports itself measured with nothing to fit on")
	}
	if _, ok := got["reason"].(string); !ok {
		t.Error("an unmeasured arm gives no reason")
	}
}

// TestScoreDayLetsTheInformativeArmTakeTheQueue is the allocation claim: a useless arm's
// most extreme alert must not displace an informative arm's ordinary one.
func TestScoreDayLetsTheInformativeArmTakeTheQueue(t *testing.T) {
	acc := mkWeightedAcc(t, true)
	w := acc.fitWeightedArm()
	if w == nil {
		t.Fatal("no arm fitted")
	}

	ids := []detector.ID{novelty.DetectorID, volume.DetectorID}
	byArm := map[detector.ID]*dayAlerts{
		// One moderately extreme alert from the informative arm.
		novelty.DetectorID: mkDay(mkAlert(500, "U1", -45, true)),
		// Three of the most extreme alerts imaginable from the useless one.
		volume.DetectorID: mkDay(
			mkAlert(600, "U2", -4000, false),
			mkAlert(601, "U3", -3000, false),
			mkAlert(602, "U4", -2000, false),
		),
	}

	got := w.scoreDay(byArm, ids, 10)
	if len(got) != 4 {
		t.Fatalf("scored %d alerts, want 4", len(got))
	}
	if got[0].arm != novelty.DetectorID {
		t.Errorf("the queue is led by %s; the useless arm displaced the informative one",
			got[0].arm)
	}
	if got[0].score <= 0 {
		t.Errorf("the informative arm's alert scored %v, not above zero", got[0].score)
	}
	for _, s := range got[1:] {
		if s.score != 0 {
			t.Errorf("a useless arm's alert scored %v, want exactly 0", s.score)
		}
	}
}

// TestScoreDayDeduplicatesAndKeepsTheBetterScore: the arms' alert sets are not disjoint, and
// an event two detectors raised is one alert credited at the higher of the two scores.
func TestScoreDayDeduplicatesAndKeepsTheBetterScore(t *testing.T) {
	acc := mkWeightedAcc(t, true)
	w := acc.fitWeightedArm()
	if w == nil {
		t.Fatal("no arm fitted")
	}

	shared := mkAlert(700, "U7", -50, true)
	byArm := map[detector.ID]*dayAlerts{
		novelty.DetectorID: mkDay(shared),
		volume.DetectorID:  mkDay(shared),
	}
	got := w.scoreDay(byArm, []detector.ID{novelty.DetectorID, volume.DetectorID}, 10)
	if len(got) != 1 {
		t.Fatalf("scored %d alerts for one event, want 1", len(got))
	}
	if got[0].arm != novelty.DetectorID {
		t.Errorf("the shared alert is credited to %s, not the arm that scored it higher",
			got[0].arm)
	}
}

// TestWeightedLessBreaksTiesOnIdentity: an uninformative arm scores every alert it holds at
// exactly zero, so ties are structural rather than rare. Breaking them on the p-value would
// hand every collision to whichever detector's numbers happen to be smallest, which is the
// bias this whole construction exists to remove.
func TestWeightedLessBreaksTiesOnIdentity(t *testing.T) {
	early := scoredAlert{al: mkAlert(100, "U2", -4000, false), score: 0}
	late := scoredAlert{al: mkAlert(200, "U1", -1, false), score: 0}

	if !weightedLess(early, late) {
		t.Error("a tie did not resolve to the earlier event identity")
	}
	if weightedLess(late, early) {
		t.Error("the tie-break is not antisymmetric")
	}

	// And a real score difference must still dominate the identity.
	better := scoredAlert{al: mkAlert(999, "U9", -1, false), score: 5}
	if !weightedLess(better, early) {
		t.Error("the identity tie-break overrode a genuine score difference")
	}
}

// TestBurnInFitSampleIgnoresAbstention: a detector that never evaluated a labelled event
// contributes neither an observation nor a censoring point (R3). Counting an abstention as a
// miss would penalise a detector for declining to guess.
func TestBurnInFitSampleIgnoresAbstention(t *testing.T) {
	acc := mkWeightedAcc(t, true)
	// A seventh labelled event that only the novelty arm evaluated.
	acc.burnInLabelled = append(acc.burnInLabelled, ledgerLabelled{
		Key: "k7", TSeconds: 100099, Entity: "U8", Day: 1,
		LogDetector: map[string]float64{string(novelty.DetectorID): -0.5},
	})

	sample, total := acc.burnInTailSample(volume.DetectorID)
	tail, err := allocation.NewTail(sample, total, 0)
	if err != nil {
		t.Fatal(err)
	}
	observed, censored := acc.burnInFitSample(volume.DetectorID, tail)
	if len(observed) != 0 {
		t.Errorf("observed = %v, want none: the volume arm surfaced nothing", observed)
	}
	if len(censored) != 6 {
		t.Errorf("censored = %d, want 6: the seventh event it never evaluated", len(censored))
	}
	for _, q := range censored {
		if q > 0 || math.IsInf(q, 0) || math.IsNaN(q) {
			t.Errorf("censoring point %v is not a finite log quantile", q)
		}
	}
}
