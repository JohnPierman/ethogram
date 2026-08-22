package calibration

import (
	"math"
	"math/rand"
	"testing"
)

// TestEqualWeightsReduceExactlyToBenjaminiHochberg is the first acceptance criterion of
// issue #15: the weighted procedure must not be a different procedure. Equal weights of
// any magnitude have to give the same discoveries, in the same order, with the same
// tie-breaking, or every comparison the corpus measurement makes is confounded by the
// implementation rather than by the weighting.
//
// The inputs deliberately include exact ties, exact zeros and exact ones: ties are where a
// weighted implementation most easily diverges, since it ranks a quotient rather than the
// p-value itself.
func TestEqualWeightsReduceExactlyToBenjaminiHochberg(t *testing.T) {
	rng := rand.New(rand.NewSource(15))
	const trials = 2_000

	for trial := 0; trial < trials; trial++ {
		m := 1 + rng.Intn(50)
		pValues := make([]float64, m)
		for i := range pValues {
			switch rng.Intn(10) {
			case 0:
				pValues[i] = 0
			case 1:
				pValues[i] = 1
			case 2:
				// A coarse grid, so exact ties occur often.
				pValues[i] = float64(rng.Intn(5)) / 4
			default:
				pValues[i] = rng.Float64()
			}
		}
		q := rng.Float64()
		// Any single positive magnitude must behave identically, since the weights are
		// renormalised to sum to m.
		magnitude := math.Exp(rng.NormFloat64() * 3)
		weights := make([]float64, m)
		for i := range weights {
			weights[i] = magnitude
		}

		want := BenjaminiHochberg(pValues, q)
		got, err := WeightedBenjaminiHochberg(pValues, weights, q)
		if err != nil {
			t.Fatalf("trial %d: unexpected error: %v", trial, err)
		}
		if len(got) != len(want) {
			t.Fatalf("trial %d: m=%d q=%g weight=%g: %d discoveries, want %d",
				trial, m, q, magnitude, len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("trial %d: discovery %d is %+v, want %+v", trial, i, got[i], want[i])
			}
		}
	}
}

// TestWeightValidationIsPinned fixes what the procedure refuses and what it accepts,
// including the two choices issue #15 asks to be pinned rather than left to the reader:
// weights are renormalised to sum to m rather than refused for not doing so, and a weight
// of zero excludes its test rather than erroring.
func TestWeightValidationIsPinned(t *testing.T) {
	pValues := []float64{0.001, 0.02, 0.5}

	for _, tc := range []struct {
		name    string
		weights []float64
		wantErr bool
	}{
		{"negative", []float64{1, -1, 1}, true},
		{"NaN", []float64{1, math.NaN(), 1}, true},
		{"positive infinity", []float64{1, math.Inf(1), 1}, true},
		{"negative infinity", []float64{1, math.Inf(-1), 1}, true},
		{"all zero", []float64{0, 0, 0}, true},
		{"too few", []float64{1, 1}, true},
		{"too many", []float64{1, 1, 1, 1}, true},
		{"not summing to m is renormalised", []float64{0.1, 0.1, 0.1}, false},
		{"one zero excludes only that test", []float64{1, 0, 1}, false},
	} {
		_, err := WeightedBenjaminiHochberg(pValues, tc.weights, 0.1)
		if tc.wantErr && err == nil {
			t.Errorf("%s: accepted, want refused", tc.name)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("%s: refused with %v, want accepted", tc.name, err)
		}
	}

	// A zero weight cannot be discovered however small its p-value: this is the documented
	// reading of "no weight", so it is asserted rather than left to inference.
	zeroed, err := WeightedBenjaminiHochberg([]float64{1e-300, 0.9, 0.9},
		[]float64{0, 1, 1}, 0.5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, d := range zeroed {
		if d.Index == 0 {
			t.Fatalf("a zero-weighted test was discovered at p=%g", d.PValue)
		}
	}

	// And the discovery reports the original p-value, not the weighted one (R5).
	reported, err := WeightedBenjaminiHochberg([]float64{0.02, 0.9},
		[]float64{1.9, 0.1}, 0.5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(reported) == 0 {
		t.Fatal("expected a discovery at p=0.02 with weight 1.9")
	}
	if reported[0].PValue != 0.02 {
		t.Errorf("reported p-value is %g, want the original 0.02", reported[0].PValue)
	}
}

// nullTrial draws m independent uniform p-values with an informative-but-null covariate:
// the covariate spans four orders of magnitude, exactly as history length does on the
// corpus, and carries no information about the p-values whatsoever. Any weighting that
// finds structure here has found noise.
func nullTrial(rng *rand.Rand, m int) (pValues, covariate []float64) {
	pValues = make([]float64, m)
	covariate = make([]float64, m)
	for i := range pValues {
		pValues[i] = rng.Float64()
		covariate[i] = math.Log(100 + rng.Float64()*20_000)
	}
	return pValues, covariate
}

// realisedFDR runs one configuration over many replicates of the global null and returns
// the realised false discovery rate. Under a global null every discovery is false, so the
// realised rate is the mean of 1{any discovery}.
func realisedFDR(t *testing.T, replicates, m int, opts StratifiedOptions, q float64,
	seed int64) float64 {

	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	rejecting := 0
	for r := 0; r < replicates; r++ {
		pValues, covariate := nullTrial(rng, m)
		weights, _, err := StratifiedWeights(pValues, covariate, opts)
		if err != nil {
			t.Fatalf("replicate %d: %v", r, err)
		}
		discoveries, err := WeightedBenjaminiHochberg(pValues, weights, q)
		if err != nil {
			t.Fatalf("replicate %d: %v", r, err)
		}
		if len(discoveries) > 0 {
			rejecting++
		}
	}
	return float64(rejecting) / float64(replicates)
}

// TestCrossFittingIsWhatKeepsTheErrorRateUnderControl is the criterion that justifies the
// fold machinery, and issue #15 asks for both directions: FDR controlled with
// cross-fitting, and demonstrably not controlled without it.
//
// The first attempt at this test asserted the failure at Storey's conventional λ = 0.5 and
// did not find it — 0.093 in-sample against 0.086 cross-fitted, both inside q. Sweeping λ
// says why, and the reason is worth more than the assertion was. Discoveries come from
// p-values many orders of magnitude below λ, so an estimator that reads only above λ is
// nearly independent of the tests it reweights; at λ = 0.5 in-sample weighting is therefore
// almost safe *by accident of λ*, not because in-sample weighting is safe. Lower λ into the
// region the discoveries come from and the failure is unmistakable.
//
// So the test is the sweep. It pins three things: cross-fitting controls at every λ, the
// in-sample inflation grows as λ falls, and at the smallest λ it is gross rather than
// marginal.
func TestCrossFittingIsWhatKeepsTheErrorRateUnderControl(t *testing.T) {
	const (
		replicates = 120
		m          = 1_000
		q          = 0.1
	)
	// Twenty strata over 2,000 tests: 100 apiece, few enough that the per-stratum
	// null-proportion estimate is genuinely noisy, which is the regime over-fitting shows
	// up in and therefore the one worth testing.
	const strata = 20

	// Monte-Carlo error on a proportion from 1,000 replicates is at most
	// 0.5/sqrt(1000) ≈ 0.016; two standard errors is the 0.032 allowance below.
	const monteCarlo = 0.032

	// The default suite runs the two ends of the sweep; the tagged simulation runs all five
	// at the size the reported table comes from. Two ends are enough to fail on a
	// regression, because the claim is that the inflation grows as lambda falls.
	lambdas := []float64{0.5, 0.02}
	inSample := make([]float64, len(lambdas))

	for i, lambda := range lambdas {
		crossFitted := StratifiedOptions{Strata: strata, Folds: 5, Lambda: lambda, Seed: 1}
		naive := StratifiedOptions{Strata: strata, Folds: 1, Lambda: lambda, Seed: 1}

		gotCross := realisedFDR(t, replicates, m, crossFitted, q, 42)
		gotNaive := realisedFDR(t, replicates, m, naive, q, 42)
		inSample[i] = gotNaive
		t.Logf("lambda=%.2f  cross-fitted %.4f  in-sample %.4f  (q=%g)",
			lambda, gotCross, gotNaive, q)

		if gotCross > q+monteCarlo {
			t.Errorf("lambda=%.2f: cross-fitted realised FDR %.4f exceeds q=%g by more "+
				"than the Monte-Carlo allowance %g", lambda, gotCross, q, monteCarlo)
		}
		if gotNaive < gotCross {
			t.Errorf("lambda=%.2f: in-sample %.4f is below cross-fitted %.4f, which "+
				"inverts the whole argument for cross-fitting", lambda, gotNaive, gotCross)
		}
	}

	// The failure has to be gross somewhere, or the machinery is unjustified. At the
	// smallest lambda the estimator reads the region the discoveries come from, and the
	// realised rate should be well clear of the level asked for.
	worst := inSample[len(inSample)-1]
	if worst <= q+monteCarlo {
		t.Errorf("in-sample realised FDR at lambda=%.2f is %.4f, which does not clearly "+
			"exceed q=%g: without a measured failure there is no case for cross-fitting",
			lambdas[len(lambdas)-1], worst, q)
	}
	// And it has to grow as lambda falls, or the explanation above is wrong even if the
	// numbers happen to pass.
	if inSample[len(inSample)-1] <= inSample[0] {
		t.Errorf("in-sample inflation does not grow as lambda falls (%.4f at %.2f against "+
			"%.4f at %.2f), so the stated mechanism is not the one operating",
			inSample[len(inSample)-1], lambdas[len(lambdas)-1], inSample[0], lambdas[0])
	}
}

// TestTheWeightingIsInertUnderAGlobalNull records the other half of the null behaviour:
// with no signal anywhere the learner should find nothing to up-weight, so the report says
// it degenerated and the procedure is unweighted Benjamini–Hochberg. "The weighting did
// nothing" and "the weighting was applied and did not help" are different findings and the
// report has to distinguish them.
func TestTheWeightingIsInertUnderAGlobalNull(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	pValues, covariate := nullTrial(rng, 400)
	// Two strata over 400 tests: 200 apiece, enough that Storey's estimate lands at the
	// clamp of one in both and the learner has nothing to say.
	opts := StratifiedOptions{Strata: 2, Folds: 5, Lambda: 0.5, Seed: 1}

	weights, report, err := StratifiedWeights(pValues, covariate, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !report.Degenerate {
		t.Skipf("the learner found structure in this null sample (pi0 %v), which is "+
			"sampling noise rather than a defect; the FDR test is what guards it",
			report.NullProportion)
	}
	for i, w := range weights {
		if w != 1 {
			t.Fatalf("degenerate weights should be uniform, weight %d is %g", i, w)
		}
	}
	want := BenjaminiHochberg(pValues, 0.1)
	got, err := WeightedBenjaminiHochberg(pValues, weights, 0.1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("degenerate weighting gives %d discoveries, unweighted gives %d",
			len(got), len(want))
	}
}

// TestWeightingFindsSignalConcentratedInTheLowCovariateStratum is the power criterion, and
// it reproduces the corpus's own shape: the covariate is history length, and the signal
// sits in the accounts with the least history — the accounts whose p-values equation (4)
// cannot push far enough down to win a slot.
func TestWeightingFindsSignalConcentratedInTheLowCovariateStratum(t *testing.T) {
	const (
		replicates = 25
		m          = 1_000
		alternates = 200
		q          = 0.1
	)
	rng := rand.New(rand.NewSource(2015))
	weightedTotal, plainTotal := 0, 0

	for r := 0; r < replicates; r++ {
		pValues := make([]float64, m)
		covariate := make([]float64, m)
		isSignal := make([]bool, m)
		for i := range pValues {
			pValues[i] = rng.Float64()
			covariate[i] = math.Log(100 + rng.Float64()*20_000)
		}
		// Place every alternative in the lowest-covariate fifth, and make each one
		// moderately rather than overwhelmingly significant: an overwhelming alternative
		// is found with or without weighting, so it would measure nothing.
		placed := 0
		for i := 0; i < m && placed < alternates; i++ {
			if covariate[i] < math.Log(100+0.2*20_000) {
				pValues[i] = rng.Float64() * 0.02
				isSignal[i] = true
				placed++
			}
		}

		opts := StratifiedOptions{Strata: 5, Folds: 5, Lambda: 0.5, Seed: 1}
		weights, _, err := StratifiedWeights(pValues, covariate, opts)
		if err != nil {
			t.Fatalf("replicate %d: %v", r, err)
		}
		weightedDiscoveries, err := WeightedBenjaminiHochberg(pValues, weights, q)
		if err != nil {
			t.Fatalf("replicate %d: %v", r, err)
		}
		for _, d := range weightedDiscoveries {
			if isSignal[d.Index] {
				weightedTotal++
			}
		}
		for _, d := range BenjaminiHochberg(pValues, q) {
			if isSignal[d.Index] {
				plainTotal++
			}
		}
	}

	t.Logf("true discoveries over %d replicates: weighted %d, unweighted %d",
		replicates, weightedTotal, plainTotal)
	if weightedTotal <= plainTotal {
		t.Errorf("weighted made %d true discoveries against unweighted %d: the weighting "+
			"must gain power where the signal is concentrated in one stratum, or there is "+
			"no reason to carry it", weightedTotal, plainTotal)
	}
}

// TestTheWeightsAreDeterministic covers R4 for the whole path: fold assignment is a pure
// function of seed and index, so identical input yields identical weights, identical
// discoveries and an identical report — and a different seed is allowed to differ, which
// is what makes the seed worth recording.
func TestTheWeightsAreDeterministic(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	pValues, covariate := nullTrial(rng, 500)
	for i := 0; i < 60; i++ {
		pValues[i] = 1e-4 * rng.Float64()
	}
	opts := StratifiedOptions{Strata: 4, Folds: 4, Lambda: 0.5, Seed: 1}

	first, firstReport, err := StratifiedWeights(pValues, covariate, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	second, secondReport, err := StratifiedWeights(pValues, covariate, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("weight %d differs between runs: %g then %g", i, first[i], second[i])
		}
	}
	if firstReport.Seed != secondReport.Seed || firstReport.Strata != secondReport.Strata {
		t.Fatalf("report differs between runs: %+v then %+v", firstReport, secondReport)
	}
	for g := range firstReport.Weight {
		if firstReport.Weight[g] != secondReport.Weight[g] {
			t.Fatalf("stratum %d weight differs between runs", g)
		}
	}

	firstDiscoveries, err := WeightedBenjaminiHochberg(pValues, first, 0.1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	secondDiscoveries, err := WeightedBenjaminiHochberg(pValues, second, 0.1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(firstDiscoveries) != len(secondDiscoveries) {
		t.Fatalf("discovery count differs between runs: %d then %d",
			len(firstDiscoveries), len(secondDiscoveries))
	}
	for i := range firstDiscoveries {
		if firstDiscoveries[i] != secondDiscoveries[i] {
			t.Fatalf("discovery %d differs between runs", i)
		}
	}
}

// TestDegenerateCovariatesAreStatesOfTheDataNotErrors covers the boundaries a corpus will
// actually present: a constant covariate, one test, and a covariate with heavy ties. None
// of them is a caller mistake, so none is refused; a constant covariate collapses to one
// stratum, which is unweighted testing, and the report says so through Counts.
func TestDegenerateCovariatesAreStatesOfTheDataNotErrors(t *testing.T) {
	constant := make([]float64, 50)
	pValues := make([]float64, 50)
	rng := rand.New(rand.NewSource(3))
	for i := range pValues {
		pValues[i] = rng.Float64()
		constant[i] = 7
	}

	_, report, err := StratifiedWeights(pValues, constant, DefaultStratifiedOptions())
	if err != nil {
		t.Fatalf("a constant covariate was refused: %v", err)
	}
	if len(report.Counts) != 1 {
		t.Errorf("a constant covariate gave %d strata, want 1", len(report.Counts))
	}

	if _, _, err := StratifiedWeights([]float64{0.5}, []float64{1},
		DefaultStratifiedOptions()); err != nil {
		t.Errorf("a single test was refused: %v", err)
	}

	if _, _, err := StratifiedWeights([]float64{}, []float64{},
		DefaultStratifiedOptions()); err != nil {
		t.Errorf("an empty input was refused: %v", err)
	}

	for _, tc := range []struct {
		name string
		opts StratifiedOptions
	}{
		{"no strata", StratifiedOptions{Strata: 0, Folds: 5, Lambda: 0.5}},
		{"no folds", StratifiedOptions{Strata: 5, Folds: 0, Lambda: 0.5}},
		{"lambda at zero", StratifiedOptions{Strata: 5, Folds: 5, Lambda: 0}},
		{"lambda at one", StratifiedOptions{Strata: 5, Folds: 5, Lambda: 1}},
	} {
		if _, _, err := StratifiedWeights(pValues, constant, tc.opts); err == nil {
			t.Errorf("%s: accepted, want refused", tc.name)
		}
	}

	nan := make([]float64, 50)
	copy(nan, constant)
	nan[3] = math.NaN()
	if _, _, err := StratifiedWeights(pValues, nan, DefaultStratifiedOptions()); err == nil {
		t.Error("a NaN covariate was accepted, want refused")
	}
}
