package allocation_test

import (
	"math"
	"math/rand"
	"testing"

	"github.com/JohnPierman/ethogram/domain/allocation"
)

// ---------------------------------------------------------------------------
// Weight
// ---------------------------------------------------------------------------

func TestNewWeightRefusesWhatIsNotAWeight(t *testing.T) {
	for _, a := range []float64{0, -1, 1.0001, math.NaN(),
		math.Inf(1), math.Inf(-1)} {
		if _, err := allocation.NewWeight(a); err == nil {
			t.Errorf("NewWeight(%v) was accepted; (0, 1] is the whole admissible range", a)
		}
	}
	for _, a := range []float64{1e-9, 0.5, 1} {
		if _, err := allocation.NewWeight(a); err != nil {
			t.Errorf("NewWeight(%v) = %v, want accepted", a, err)
		}
	}
}

// TestUninformativeWeightScoresEverythingZero is the property that stops a detector which
// found nothing from costing a share of the budget. It is the whole reason the rule beats
// an equal quota, so it is pinned rather than assumed.
func TestUninformativeWeightScoresEverythingZero(t *testing.T) {
	w := allocation.UninformativeWeight()
	if w.IsInformative() {
		t.Error("the uninformative weight reports itself informative")
	}
	for _, logQ := range []float64{0, -1, -50, -4000, -1e6} {
		if got := w.LogLikelihoodRatio(logQ); got != 0 {
			t.Errorf("LogLikelihoodRatio(%v) = %v, want exactly 0", logQ, got)
		}
	}
}

// TestLogLikelihoodRatioRewardsExtremityInProportionToTheWeight pins the ordering the whole
// construction rests on: within one detector a more extreme alert scores higher, and
// between detectors the sharper detector scores higher at equal extremity.
func TestLogLikelihoodRatioRewardsExtremityInProportionToTheWeight(t *testing.T) {
	sharp, err := allocation.NewWeight(0.05)
	if err != nil {
		t.Fatal(err)
	}
	blunt, err := allocation.NewWeight(0.9)
	if err != nil {
		t.Fatal(err)
	}

	// Monotone in extremity, within an arm.
	prev := math.Inf(-1)
	for _, logQ := range []float64{-1, -5, -20, -100, -1000, -100000} {
		got := sharp.LogLikelihoodRatio(logQ)
		if got <= prev {
			t.Errorf("score at ln q = %v is %v, not above the previous %v", logQ, got, prev)
		}
		prev = got
	}

	// The sharper arm wins at equal extremity, once past the point where its own
	// normalisation is paid off.
	for _, logQ := range []float64{-20, -100, -1000} {
		if sharp.LogLikelihoodRatio(logQ) <= blunt.LogLikelihoodRatio(logQ) {
			t.Errorf("at ln q = %v the blunt arm scores at least as high as the sharp one",
				logQ)
		}
	}
}

// TestLogLikelihoodRatioIsFiniteAtRealExtremity guards the reason this takes a log at all.
// A detector's tail reaches ln p = -4000 on this corpus; a score that overflows there
// ties every alert past it.
func TestLogLikelihoodRatioIsFiniteAtRealExtremity(t *testing.T) {
	w, err := allocation.NewWeight(0.01)
	if err != nil {
		t.Fatal(err)
	}
	for _, logQ := range []float64{-4000, -1e5, -1e12} {
		if got := w.LogLikelihoodRatio(logQ); math.IsInf(got, 0) || math.IsNaN(got) {
			t.Errorf("LogLikelihoodRatio(%v) = %v, want finite", logQ, got)
		}
	}
}

// ---------------------------------------------------------------------------
// Fit
// ---------------------------------------------------------------------------

func TestFitRecoversAConcentratedSampleAndRejectsAUniformOne(t *testing.T) {
	rng := rand.New(rand.NewSource(1))

	concentrated := make([]float64, 40)
	for i := range concentrated {
		concentrated[i] = math.Log(rng.Float64() * 1e-6)
	}
	w, report, err := allocation.Fit(concentrated, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !w.IsInformative() || w.A() > 0.2 {
		t.Errorf("a = %v on a sample concentrated at 1e-6; want a small informative weight",
			w.A())
	}
	if report.Observed != 40 || report.Censored != 0 {
		t.Errorf("report = %+v, want 40 observed and 0 censored", report)
	}

	uniform := make([]float64, 200)
	for i := range uniform {
		uniform[i] = math.Log(rng.Float64())
	}
	w, report, err = allocation.Fit(uniform, nil)
	if err != nil {
		t.Fatal(err)
	}
	if w.IsInformative() {
		t.Errorf("a = %v on a uniform sample; a detector whose labelled events sit where "+
			"any event sits has earned nothing", w.A())
	}
	// The point estimate need not land exactly at 1 -- with 200 draws it lands within
	// about 0.07 of it -- so what must be true is that the fit was not SIGNIFICANT. That
	// is the guard that stops sampling noise from buying a budget.
	if report.Significant {
		t.Errorf("a uniform sample was called significant: deviance %v against a threshold "+
			"of %v", report.Deviance, allocation.DevianceThreshold)
	}
}

// TestFitRejectsWeightsThatAreOnlySamplingNoise is the guard [allocation.DevianceThreshold]
// exists for, measured over many replicates rather than one.
//
// A weight is not free. At a = 0.93 -- an entirely ordinary draw from the uniform null on a
// sample of this size -- the likelihood ratio scores an alert at ln q = -4000 some 248 log
// units above zero, so a detector that found nothing would take a large share of the queue.
// The false-positive rate of the significance test is what bounds how often that happens.
func TestFitRejectsWeightsThatAreOnlySamplingNoise(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	const replicates = 400
	informative := 0
	for range replicates {
		sample := make([]float64, 50)
		for i := range sample {
			sample[i] = math.Log(rng.Float64())
		}
		w, _, err := allocation.Fit(sample, nil)
		if err != nil {
			t.Fatal(err)
		}
		if w.IsInformative() {
			informative++
		}
	}
	// The test is nominally 5%; allow generous Monte-Carlo room but catch a rate that
	// says the guard is absent.
	if rate := float64(informative) / replicates; rate > 0.12 {
		t.Errorf("%d of %d uniform samples were called informative (%.1f%%); the "+
			"significance guard is not holding", informative, replicates, 100*rate)
	}
}

// TestFitKeepsRealSignal is the other half: the guard must not be so strict that a genuine
// effect is discarded. A detector whose labelled events sit two decades into its own tail
// must survive it on a sample as small as ten.
func TestFitKeepsRealSignal(t *testing.T) {
	rng := rand.New(rand.NewSource(13))
	const replicates = 200
	kept := 0
	for range replicates {
		sample := make([]float64, 10)
		for i := range sample {
			sample[i] = math.Log(rng.Float64() * 1e-2)
		}
		w, _, err := allocation.Fit(sample, nil)
		if err != nil {
			t.Fatal(err)
		}
		if w.IsInformative() {
			kept++
		}
	}
	if rate := float64(kept) / replicates; rate < 0.9 {
		t.Errorf("only %d of %d genuinely concentrated samples survived the guard (%.1f%%)",
			kept, replicates, 100*rate)
	}
}

// TestCensoringPreventsALuckyArmFromTakingTheBudget is the property the censored term
// exists for, and the one that would be silently wrong if the term were dropped.
//
// The sample is a detector that surfaced two labelled events, both extremely, and failed to
// surface forty-seven others. Read on its two hits alone it is the sharpest detector in the
// framework; read honestly it is mediocre.
func TestCensoringPreventsALuckyArmFromTakingTheBudget(t *testing.T) {
	lucky := []float64{math.Log(1e-7), math.Log(2e-7)}
	censored := make([]float64, 47)
	for i := range censored {
		censored[i] = math.Log(1e-3)
	}

	withCensoring, report, err := allocation.Fit(lucky, censored)
	if err != nil {
		t.Fatal(err)
	}
	withoutCensoring, _, err := allocation.Fit(lucky, nil)
	if err != nil {
		t.Fatal(err)
	}

	if withCensoring.A() <= withoutCensoring.A() {
		t.Errorf("censoring did not penalise the misses: a = %v with, %v without",
			withCensoring.A(), withoutCensoring.A())
	}
	if report.Censored != 47 || report.Observed != 2 {
		t.Errorf("report = %+v, want 2 observed and 47 censored", report)
	}
	// The gap should be large, not marginal: this is the difference between allocating
	// most of a budget to this detector and allocating a slice.
	if withCensoring.A() < 2*withoutCensoring.A() {
		t.Errorf("censoring moved a only from %v to %v; too little to change an allocation",
			withoutCensoring.A(), withCensoring.A())
	}
}

func TestFitWithNoObservationsEarnsNothing(t *testing.T) {
	w, report, err := allocation.Fit(nil, []float64{math.Log(1e-3), math.Log(1e-3)})
	if err != nil {
		t.Fatal(err)
	}
	if w.IsInformative() {
		t.Errorf("a = %v from no observations; an unsurfaced detector has earned nothing",
			w.A())
	}
	if !report.Clamped || report.Observed != 0 || report.Censored != 2 {
		t.Errorf("report = %+v, want clamped with 0 observed and 2 censored", report)
	}
}

func TestFitRefusesQuantilesAboveOne(t *testing.T) {
	if _, _, err := allocation.Fit([]float64{0.5}, nil); err == nil {
		t.Error("a positive log quantile was accepted; a quantile cannot exceed 1")
	}
	if _, _, err := allocation.Fit([]float64{math.NaN()}, nil); err == nil {
		t.Error("NaN was accepted as a log quantile")
	}
	if _, _, err := allocation.Fit(nil, []float64{math.Inf(-1)}); err == nil {
		t.Error("-Inf was accepted as a censoring point")
	}
}

// TestFitIsDeterministicUnderReordering is R4 at the level of this estimator: the same
// sample in a different order is the same sample, and must give a bit-identical weight.
func TestFitIsDeterministicUnderReordering(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	obs := make([]float64, 60)
	for i := range obs {
		obs[i] = math.Log(rng.Float64() * 1e-3)
	}
	cen := make([]float64, 30)
	for i := range cen {
		cen[i] = math.Log(rng.Float64() * 1e-2)
	}

	want, _, err := allocation.Fit(obs, cen)
	if err != nil {
		t.Fatal(err)
	}
	for trial := range 20 {
		shuffled := make([]float64, len(obs))
		copy(shuffled, obs)
		rng.Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})
		got, _, err := allocation.Fit(shuffled, cen)
		if err != nil {
			t.Fatal(err)
		}
		if got.A() != want.A() {
			t.Fatalf("trial %d: a = %v under reordering, want exactly %v",
				trial, got.A(), want.A())
		}
	}
}

func TestFitDoesNotModifyItsInputs(t *testing.T) {
	obs := []float64{math.Log(0.5), math.Log(1e-6), math.Log(1e-3)}
	cen := []float64{math.Log(0.2), math.Log(0.01)}
	obsCopy := append([]float64(nil), obs...)
	cenCopy := append([]float64(nil), cen...)

	if _, _, err := allocation.Fit(obs, cen); err != nil {
		t.Fatal(err)
	}
	for i := range obs {
		if obs[i] != obsCopy[i] {
			t.Fatalf("Fit sorted its observed input in place: %v", obs)
		}
	}
	for i := range cen {
		if cen[i] != cenCopy[i] {
			t.Fatalf("Fit sorted its censored input in place: %v", cen)
		}
	}
}

// ---------------------------------------------------------------------------
// Tail
// ---------------------------------------------------------------------------

func TestNewTailRefusesAnUnusableSample(t *testing.T) {
	if _, err := allocation.NewTail([]float64{-1}, 100, 0); err == nil {
		t.Error("a one-observation tail was accepted")
	}
	if _, err := allocation.NewTail([]float64{-1, -2}, 1, 0); err == nil {
		t.Error("total below the sample size was accepted; the quantiles would be too large")
	}
	if _, err := allocation.NewTail([]float64{-1, 0.5}, 100, 0); err == nil {
		t.Error("a positive log p-value was accepted")
	}
	if _, err := allocation.NewTail([]float64{-1, math.Inf(-1)}, 100, 0); err == nil {
		t.Error("-Inf was accepted as an observation")
	}
}

// TestTailIsStrictlyDecreasingPastTheThreshold is the property the extension exists for:
// without it every alert more extreme than the fitting sample ties at the floor, and a tie
// at the head of the queue is decided by arrival order rather than by evidence.
func TestTailIsStrictlyDecreasingPastTheThreshold(t *testing.T) {
	// A plausible burn-in tail: 1000 observations spread over several decades of log p.
	sample := make([]float64, 1000)
	for i := range sample {
		sample[i] = -float64(i) / 20
	}
	tail, err := allocation.NewTail(sample, 600000, 200)
	if err != nil {
		t.Fatal(err)
	}
	if tail.Scale() <= 0 {
		t.Fatalf("no exponential extension was fitted: scale = %v", tail.Scale())
	}

	prev := tail.LogQuantile(tail.Threshold())
	for _, logP := range []float64{-60, -100, -500, -1000, -4000} {
		got := tail.LogQuantile(logP)
		if got >= prev {
			t.Errorf("LogQuantile(%v) = %v, not strictly below the previous %v",
				logP, got, prev)
		}
		if math.IsInf(got, 0) || math.IsNaN(got) {
			t.Errorf("LogQuantile(%v) = %v, want finite", logP, got)
		}
		prev = got
	}
}

// TestTailIsMonotoneEverywhere covers the empirical piece and the join, not only the
// extension: a non-monotone join would reorder alerts either side of the threshold.
func TestTailIsMonotoneEverywhere(t *testing.T) {
	sample := make([]float64, 500)
	for i := range sample {
		sample[i] = -math.Sqrt(float64(i))
	}
	tail, err := allocation.NewTail(sample, 100000, 100)
	if err != nil {
		t.Fatal(err)
	}
	prev := math.Inf(1)
	for x := 0.0; x > -200; x -= 0.05 {
		got := tail.LogQuantile(x)
		if got > prev+1e-12 {
			t.Fatalf("LogQuantile rose from %v to %v at logP = %v", prev, got, x)
		}
		prev = got
	}
}

// TestTailNeverExceedsCertainty pins that a quantile is a probability: ln q <= 0.
func TestTailNeverExceedsCertainty(t *testing.T) {
	sample := []float64{-0.1, -0.2, -0.3, -0.4}
	tail, err := allocation.NewTail(sample, 10, 2)
	if err != nil {
		t.Fatal(err)
	}
	for x := 0.0; x > -50; x -= 0.5 {
		if got := tail.LogQuantile(x); got > 0 {
			t.Fatalf("LogQuantile(%v) = %v, above ln 1", x, got)
		}
	}
}

// TestTailQuantileMatchesTheEmpiricalCountAboveTheThreshold checks the piece that is not
// modelled at all: above the threshold the answer should simply be the observed proportion.
func TestTailQuantileMatchesTheEmpiricalCountAboveTheThreshold(t *testing.T) {
	// Ten observations, evenly spaced, out of a thousand evaluations. The excess fit is
	// confined to the two most extreme, so everything above them is empirical.
	sample := []float64{-10, -9, -8, -7, -6, -5, -4, -3, -2, -1}
	tail, err := allocation.NewTail(sample, 1000, 2)
	if err != nil {
		t.Fatal(err)
	}
	// -5 is the sixth most extreme, so six of a thousand are at or below it.
	want := math.Log(6.0 / 1000.0)
	if got := tail.LogQuantile(-5); math.Abs(got-want) > 1e-12 {
		t.Errorf("LogQuantile(-5) = %v, want %v (6 of 1000)", got, want)
	}
	// Less extreme than every observation: all ten are at or below it, so ten of a
	// thousand. A quantile counts what is MORE extreme, so a shallow cut admits the whole
	// retained sample rather than none of it.
	if got, want := tail.LogQuantile(-0.5), math.Log(10.0/1000.0); math.Abs(got-want) > 1e-12 {
		t.Errorf("LogQuantile(-0.5) = %v, want %v (10 of 1000)", got, want)
	}
}

func TestNewTailDoesNotModifyItsSample(t *testing.T) {
	sample := []float64{-1, -9, -3, -7, -5}
	before := append([]float64(nil), sample...)
	if _, err := allocation.NewTail(sample, 100, 2); err != nil {
		t.Fatal(err)
	}
	for i := range sample {
		if sample[i] != before[i] {
			t.Fatalf("NewTail sorted its sample in place: %v", sample)
		}
	}
}

// TestTailAndWeightComposeIntoAUsableScore is the integration the package exists for: two
// detectors, one informative and one not, and the informative one's extreme alert must
// outrank the other's however extreme that other's own p-value is.
func TestTailAndWeightComposeIntoAUsableScore(t *testing.T) {
	// Both detectors have the same shaped null; only their fitted weights differ.
	sample := make([]float64, 400)
	for i := range sample {
		sample[i] = -float64(i) / 10
	}
	tail, err := allocation.NewTail(sample, 500000, 200)
	if err != nil {
		t.Fatal(err)
	}

	informative, err := allocation.NewWeight(0.05)
	if err != nil {
		t.Fatal(err)
	}
	useless := allocation.UninformativeWeight()

	// The useless detector's most extreme alert imaginable, against a merely notable
	// alert from the informative one.
	uselessScore := useless.LogLikelihoodRatio(tail.LogQuantile(-4000))
	informativeScore := informative.LogLikelihoodRatio(tail.LogQuantile(-45))
	if informativeScore <= uselessScore {
		t.Errorf("a useless detector at ln p = -4000 scores %v, at or above an informative "+
			"detector's %v; the weight is not doing its job",
			uselessScore, informativeScore)
	}
}

// TestEdgeCasesTheCorpusWillEventuallyReach covers the branches a fitting window can
// produce but a well-behaved one does not: a censoring point at the very top of a
// detector's null, a quantile handed in above 1, a sample with no spread to fit an
// extension from, and the accessors a run record reads.
func TestEdgeCasesTheCorpusWillEventuallyReach(t *testing.T) {
	// A censoring point of ln 1 = 0 says the detector places the labelled event at the
	// least extreme end of its own null. That contributes no information in favour of any
	// a, and must not make every candidate equally impossible.
	w, report, err := allocation.Fit([]float64{math.Log(1e-8), math.Log(1e-9)},
		[]float64{0, 0, 0})
	if err != nil {
		t.Fatal(err)
	}
	if math.IsNaN(w.A()) || math.IsInf(report.LogLikelihood, 0) {
		t.Errorf("a censoring point at ln q = 0 broke the fit: a = %v, ln L = %v",
			w.A(), report.LogLikelihood)
	}

	// A quantile above 1 is not representable as a probability; the score clamps rather
	// than returning something positive that would outrank a real detection.
	sharp, err := allocation.NewWeight(0.1)
	if err != nil {
		t.Fatal(err)
	}
	if got := sharp.LogLikelihoodRatio(5); got != sharp.LogLikelihoodRatio(0) {
		t.Errorf("LogLikelihoodRatio(5) = %v, want the value at ln q = 0, %v",
			got, sharp.LogLikelihoodRatio(0))
	}

	// A sample with no spread fits no extension, and the tail must still be usable.
	flat, err := allocation.NewTail([]float64{-3, -3, -3, -3}, 900, 2)
	if err != nil {
		t.Fatal(err)
	}
	if flat.Scale() != 0 {
		t.Errorf("Scale = %v on a sample with no spread, want 0", flat.Scale())
	}
	// More extreme than every observation and with no extension to fall back on: one
	// evaluation's worth is the tightest statement the sample supports.
	if got := flat.LogQuantile(-50); got != -math.Log(900) {
		t.Errorf("LogQuantile(-50) = %v past every observation, want %v",
			got, -math.Log(900))
	}
	// Less extreme than every observation: all four are more extreme, so all four count.
	if got := flat.LogQuantile(-1); got != math.Log(4.0/900.0) {
		t.Errorf("LogQuantile(-1) = %v, want the empirical %v",
			got, math.Log(4.0/900.0))
	}
	if flat.Observations() != 4 || flat.Evaluations() != 900 {
		t.Errorf("Observations/Evaluations = %d/%d, want 4/900",
			flat.Observations(), flat.Evaluations())
	}
}
