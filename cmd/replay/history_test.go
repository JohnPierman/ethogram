package main

import (
	"math"
	"testing"

	"github.com/JohnPierman/ethogram/domain/novelty"
	"github.com/JohnPierman/ethogram/domain/volume"
)

// TestParseWeightingPinsWhatIsSupported fixes the flag's contract, including that `asset`
// is refused rather than quietly treated as something else. A run whose provenance block
// says one thing and whose ranking did another is worse than a run that did not start.
func TestParseWeightingPinsWhatIsSupported(t *testing.T) {
	for _, tc := range []struct {
		in      string
		want    weightingMode
		wantErr bool
	}{
		{"none", weightingNone, false},
		{"history", weightingHistory, false},
		{"asset", "", true},
		{"", "", true},
		{"History", "", true},
		{"ln_n", "", true},
	} {
		got, err := parseWeighting(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("%q: accepted as %q, want refused", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: refused with %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestNoneWeightingTouchesNothing is the criterion that every earlier run must remain
// reproducible: with the default mode the covariate is not even tracked and the ranking key
// is the arm's own log p-value, unchanged.
func TestNoneWeightingTouchesNothing(t *testing.T) {
	h := newHistoryWeighting(weightingNone)
	if h.on() {
		t.Fatal("the none mode reports itself as on")
	}
	for _, logP := range []float64{0, -1, -700, -4000} {
		if got := h.adjust(novelty.DetectorID, "u1", logP); got != logP {
			t.Errorf("adjust(%g) = %g, want the input unchanged", logP, got)
		}
	}
	h.seen("u1")
	h.seen("u1")
	if len(h.entityEvents) != 0 {
		t.Errorf("the none mode tracked %d entities; it should track none",
			len(h.entityEvents))
	}
	if err := h.freeze(); err != nil {
		t.Errorf("freezing an unweighted run failed: %v", err)
	}
	rec := h.record()
	if rec["mode"] != string(weightingNone) {
		t.Errorf("recorded mode is %v, want none", rec["mode"])
	}
}

// TestTheCovariateIsHistoryStrictlyBeforeTheEvent pins the ordering that makes the
// covariate deployable: an event is counted against its entity only after it has been
// weighted, so nothing is ever weighted by a history that includes itself.
func TestTheCovariateIsHistoryStrictlyBeforeTheEvent(t *testing.T) {
	h := newHistoryWeighting(weightingHistory)
	if got := h.covariateFor("u1"); got != 0 {
		t.Errorf("a first-ever event has covariate %g, want 0 — ln(1+0)", got)
	}
	h.seen("u1")
	if got, want := h.covariateFor("u1"), math.Log(2); got != want {
		t.Errorf("after one event the covariate is %g, want %g", got, want)
	}
	for i := 0; i < 98; i++ {
		h.seen("u1")
	}
	if got, want := h.covariateFor("u1"), math.Log(100); math.Abs(got-want) > 1e-12 {
		t.Errorf("after 99 events the covariate is %g, want %g", got, want)
	}
	if got := h.covariateFor("u2"); got != 0 {
		t.Errorf("an unseen entity has covariate %g, want 0", got)
	}
}

// TestTheBurnInSampleCoversTheWholeWindow covers the decimating sampler. A sampler that
// filled up and then stopped would fit the weights on the earliest burn-in days alone,
// which are exactly the days on which every history is shortest — the covariate would be
// measured over a range the scoring window does not have.
func TestTheBurnInSampleCoversTheWholeWindow(t *testing.T) {
	s := &covariateSample{}
	const total = historyWeightingCap * 5
	for i := 0; i < total; i++ {
		s.add(float64(-i), float64(i))
	}
	if len(s.logP) > historyWeightingCap {
		t.Fatalf("the sample holds %d observations, above the cap of %d",
			len(s.logP), historyWeightingCap)
	}
	if len(s.logP) < historyWeightingCap/2 {
		t.Errorf("the sample holds only %d observations of a cap of %d, so it is "+
			"discarding more than halving requires", len(s.logP), historyWeightingCap)
	}
	// The last retained observation must come from near the end of the stream, not from
	// the prefix: that is the property a fill-and-stop sampler fails.
	last := s.covariate[len(s.covariate)-1]
	if last < float64(total)*0.5 {
		t.Errorf("the newest retained observation is at %g of %d, so the sample is "+
			"weighted towards the start of the burn-in window", last, total)
	}
	// And it must be a deterministic function of arrival order alone (R4).
	again := &covariateSample{}
	for i := 0; i < total; i++ {
		again.add(float64(-i), float64(i))
	}
	if len(again.logP) != len(s.logP) {
		t.Fatalf("two identical streams gave samples of %d and %d", len(s.logP), len(again.logP))
	}
	for i := range s.logP {
		if s.logP[i] != again.logP[i] || s.covariate[i] != again.covariate[i] {
			t.Fatalf("sample entry %d differs between two identical streams", i)
		}
	}
}

// TestAZeroWeightRanksLastRatherThanDroppingTheEvent is the reason [zeroWeightFloor]
// exists, and it is worth a test of its own because the first implementation did drop the
// events: on the first measured run `marginal`'s busiest stratum was weighted to zero and
// 231,060 scored events disappeared from that arm.
//
// A threshold procedure may exclude a stratum. A top-B selection may not: the budget is a
// capacity to fill, and an empty analyst slot is not an improvement on a weakly-ranked
// alert.
func TestAZeroWeightRanksLastRatherThanDroppingTheEvent(t *testing.T) {
	if got := zeroWeightFloor(0); got <= 0 {
		t.Errorf("the floor for an empty stratum is %g, want positive", got)
	}
	for _, observations := range []int{1, 100, 40_000} {
		got := zeroWeightFloor(observations)
		if got <= 0 || math.IsInf(got, 0) || math.IsNaN(got) {
			t.Errorf("the floor for %d observations is %g, want a positive finite weight",
				observations, got)
		}
	}
	// More evidence that a stratum is null means a lower floor: the estimator's resolution
	// is what sets it.
	if zeroWeightFloor(40_000) >= zeroWeightFloor(100) {
		t.Error("the floor does not fall as the stratum grows, so it is not the " +
			"estimator's resolution limit")
	}

	// A floored stratum must still rank, and must rank behind an ordinarily weighted one.
	table := weightTable{
		bounds:    []float64{5},
		logWeight: []float64{math.Log(2), math.Log(zeroWeightFloor(1000))},
		floored:   []bool{false, true},
	}
	head := -10 - table.logWeightFor(1)
	tail := -10 - table.logWeightFor(9)
	if !(head < tail) {
		t.Errorf("the up-weighted stratum ranks at %g and the floored one at %g; the "+
			"floored stratum must sort behind, not ahead", head, tail)
	}
	if math.IsInf(tail, 0) || math.IsNaN(tail) {
		t.Errorf("the floored stratum's ranking key is %g, which no JSON result can "+
			"carry and no budget can fill", tail)
	}
}

// TestAnArmWithNoBurnInEvidenceRunsUnweighted covers the case the corpus actually
// presents: `volume` abstains through a short burn-in window, so it contributes no
// observation to fit a weight from. The arm must then rank on its own p-value rather than
// on a weight invented for it.
func TestAnArmWithNoBurnInEvidenceRunsUnweighted(t *testing.T) {
	h := newHistoryWeighting(weightingHistory)
	h.observeBurnIn(novelty.DetectorID, -3, 1)
	if err := h.freeze(); err != nil {
		t.Fatalf("freeze: %v", err)
	}
	if _, ok := h.table[volume.DetectorID]; ok {
		t.Fatal("an arm that contributed no burn-in observation was given a weight table")
	}
	if got := h.adjust(volume.DetectorID, "u1", -12); got != -12 {
		t.Errorf("an unfitted arm's score was adjusted to %g, want -12 unchanged", got)
	}
	rec := h.record()
	arms, _ := rec["arms"].(map[string]any)
	if _, present := arms[string(volume.DetectorID)]; present {
		t.Error("the record claims a table for an arm that has none")
	}
}

// TestFreezeHappensOnceAndOnly pins that the fit cannot be reopened. A weight refitted
// after scoring began would have read the p-values it ranks, which is the failure the
// burn-in boundary exists to prevent.
func TestFreezeHappensOnceAndOnly(t *testing.T) {
	h := newHistoryWeighting(weightingHistory)
	for i := 0; i < 400; i++ {
		h.observeBurnIn(novelty.DetectorID, -float64(i%7)-0.5, float64(i%5))
	}
	if err := h.freeze(); err != nil {
		t.Fatalf("freeze: %v", err)
	}
	before := h.table[novelty.DetectorID]

	// Observations after the boundary are ignored, and a second freeze changes nothing.
	for i := 0; i < 400; i++ {
		h.observeBurnIn(novelty.DetectorID, -50, 9)
	}
	if err := h.freeze(); err != nil {
		t.Fatalf("second freeze: %v", err)
	}
	after := h.table[novelty.DetectorID]
	if len(before.logWeight) != len(after.logWeight) {
		t.Fatalf("the table changed shape after freezing: %d strata then %d",
			len(before.logWeight), len(after.logWeight))
	}
	for g := range before.logWeight {
		if before.logWeight[g] != after.logWeight[g] {
			t.Errorf("stratum %d's weight changed after freezing", g)
		}
	}
}

// TestHistoryOfLabelledReportsOnlyRepresentableProducts covers the p x n statistic. An
// underflowed p-value is not a p x n of zero, and reporting it as one would read as a
// removed dependence rather than an unrepresentable number.
func TestHistoryOfLabelledReportsOnlyRepresentableProducts(t *testing.T) {
	none := historyOfLabelled([]redTeamScore{
		{P: 0, HistoryN: 500},
		{P: 1e-9, HistoryN: 0},
	})
	if none["recorded"] != false {
		t.Errorf("p x n was recorded from unrepresentable inputs: %v", none)
	}

	got := historyOfLabelled([]redTeamScore{
		{P: 0.002, HistoryN: 500}, // 1.0
		{P: 0.004, HistoryN: 500}, // 2.0
		{P: 0.006, HistoryN: 500}, // 3.0
		{P: 0, HistoryN: 500},     // dropped: underflowed
		{P: 0.5, HistoryN: 0},     // dropped: no history recorded
	})
	if got["recorded"] != true {
		t.Fatalf("p x n was not recorded from usable inputs: %v", got)
	}
	if got["n"] != 3 {
		t.Errorf("p x n used %v events, want the 3 with representable products", got["n"])
	}
	if median, ok := got["median"].(float64); !ok || math.Abs(median-2) > 1e-9 {
		t.Errorf("median p x n is %v, want 2", got["median"])
	}
}

// TestQuantileOfHandlesTheEnds covers the small-sample edges the corpus will present when
// a category has one or two labelled events.
func TestQuantileOfHandlesTheEnds(t *testing.T) {
	if got := quantileOf(nil, 0.5); got != 0 {
		t.Errorf("the median of nothing is %g, want 0", got)
	}
	if got := quantileOf([]float64{7}, 0.5); got != 7 {
		t.Errorf("the median of one value is %g, want 7", got)
	}
	four := []float64{1, 2, 3, 4}
	for _, tc := range []struct{ q, want float64 }{
		{0, 1}, {0.5, 2.5}, {1, 4},
	} {
		if got := quantileOf(four, tc.q); math.Abs(got-tc.want) > 1e-12 {
			t.Errorf("quantile %g is %g, want %g", tc.q, got, tc.want)
		}
	}
}
