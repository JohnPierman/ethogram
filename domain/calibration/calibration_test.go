package calibration_test

import (
	"errors"
	"math"
	"testing"

	"github.com/JohnPierman/ethogram/domain/calibration"
)

// assertRelative fails unless got is within relTol of want in relative terms.
func assertRelative(t *testing.T, name string, got, want, relTol float64) {
	t.Helper()
	if math.Abs(got-want) > relTol*math.Abs(want) {
		t.Errorf("%s: got %.17g, want %.17g (relative error %.3g)",
			name, got, want, math.Abs(got-want)/math.Abs(want))
	}
}

// chiSquareFixtures holds reference values of Pr(X ≥ x) for X ~ χ²(k), generated
// with scipy.stats.chi2.sf(x, k). The last two rows probe the extreme tail, where a
// naive 1 − CDF evaluation would return zero.
var chiSquareFixtures = []struct {
	x    float64
	k    int
	want float64
}{
	{0.5, 1, 0.47950012218695337},
	{1, 1, 0.31731050786291115},
	{5, 1, 0.025347318677468325},
	{2, 2, 0.36787944117144245},
	{10, 2, 0.006737946999085469},
	{4.605170185988091, 2, 0.10000000000000003},
	{3, 4, 0.5578254003710748},
	{7.779, 4, 0.10001751571024528},
	{11.345, 6, 0.07828135508618646},
	{2, 7, 0.9598403687301016},
	{20, 7, 0.005569683072945574},
	{13.4, 11, 0.26798720496961276},
	{3.14159, 3, 0.37030575573213165},
	{0.1, 10, 0.9999999975020487},
	{25, 10, 0.005345505487134069},
	{50, 20, 0.0002214766382487835},
	{100, 40, 4.791357300338064e-07},
	{1e-08, 2, 0.999999995},
	{200, 10, 1.6139305336977317e-37},
	{700, 100, 8.658230861323775e-91},
}

func TestChiSquareSurvivalMatchesReferenceValues(t *testing.T) {
	const (
		relTol   = 1e-12
		absFloor = 1e-300
	)
	worst := 0.0
	for _, fixture := range chiSquareFixtures {
		got := calibration.ChiSquareSurvival(fixture.x, fixture.k)
		diff := math.Abs(got - fixture.want)
		if diff <= absFloor {
			continue
		}
		relErr := diff / fixture.want
		worst = math.Max(worst, relErr)
		if relErr > relTol {
			t.Errorf("ChiSquareSurvival(%v, %d): got %.17g, want %.17g (relative error %.3g)",
				fixture.x, fixture.k, got, fixture.want, relErr)
		}
	}
	t.Logf("worst relative error against the fixture table: %.3g", worst)
}

func TestChiSquareSurvivalDegenerateInputs(t *testing.T) {
	if got := calibration.ChiSquareSurvival(-1, 5); got != 1 {
		t.Errorf("x < 0: got %v, want 1", got)
	}
	if got := calibration.ChiSquareSurvival(0, 5); got != 1 {
		t.Errorf("x = 0: got %v, want 1", got)
	}
	if got := calibration.ChiSquareSurvival(3.2, 0); got != 1 {
		t.Errorf("k = 0: got %v, want 1", got)
	}
	if got := calibration.ChiSquareSurvival(3.2, -2); got != 1 {
		t.Errorf("k < 0: got %v, want 1", got)
	}
	if got := calibration.ChiSquareSurvivalNonIntegral(5, 0); got != 1 {
		t.Errorf("df = 0: got %v, want 1", got)
	}
}

// TestChiSquareSurvivalNonIntegralInterpolates checks that a fractional df lands
// strictly between the two integer survivals bracketing it: the survival function is
// strictly increasing in df at fixed x.
func TestChiSquareSurvivalNonIntegralInterpolates(t *testing.T) {
	lower := calibration.ChiSquareSurvival(5, 2)
	mid := calibration.ChiSquareSurvivalNonIntegral(5, 2.5)
	upper := calibration.ChiSquareSurvival(5, 3)
	if !(lower < mid && mid < upper) {
		t.Errorf("Q at df 2.5 should lie strictly between df 2 and df 3: %v, %v, %v",
			lower, mid, upper)
	}
}

// Fisher fixtures generated with scipy: X² = -2*sum(log(p)) accumulated in slice
// order, df = 2J, tail = chi2.sf(X², 2J).
func TestFisherMatchesReferenceValues(t *testing.T) {
	fixtures := []struct {
		p        []float64
		wantX2   float64
		wantDF   int
		wantTail float64
	}{
		{[]float64{0.01, 0.20, 0.70}, 13.142566084721848, 6, 0.04082702883527948},
		{[]float64{0.5, 0.5}, 2.772588722239781, 4, 0.5965735902799727},
		{[]float64{1e-6, 0.03, 0.9, 0.44}, 36.49681904602382, 8, 1.4238784664802136e-05},
	}
	for _, fixture := range fixtures {
		x2, df, tail, err := calibration.Fisher(fixture.p)
		if err != nil {
			t.Fatalf("Fisher(%v): unexpected error: %v", fixture.p, err)
		}
		assertRelative(t, "X²", x2, fixture.wantX2, 1e-14)
		if df != fixture.wantDF {
			t.Errorf("Fisher(%v): df = %d, want %d", fixture.p, df, fixture.wantDF)
		}
		assertRelative(t, "tail", tail, fixture.wantTail, 1e-12)
	}
}

func TestFisherRejectsInvalidInputs(t *testing.T) {
	_, _, _, err := calibration.Fisher([]float64{})
	if !errors.Is(err, calibration.ErrNoEvaluatedVerdicts) {
		t.Errorf("empty input: got %v, want ErrNoEvaluatedVerdicts", err)
	}
	_, _, _, err = calibration.Fisher(nil)
	if !errors.Is(err, calibration.ErrNoEvaluatedVerdicts) {
		t.Errorf("nil input: got %v, want ErrNoEvaluatedVerdicts", err)
	}

	// R3: an abstention encoded as any placeholder p-value must be rejected, never
	// folded into the statistic.
	for _, bad := range [][]float64{
		{0.2, 0, 0.4},
		{-0.1, 0.5},
		{0.5, 1.0000000001},
		{0.3, math.NaN()},
	} {
		if _, _, _, err := calibration.Fisher(bad); err == nil {
			t.Errorf("Fisher(%v): expected an error, got nil", bad)
		}
	}

	// p = 1 exactly is a legitimate boundary value, not a rejection.
	if _, _, _, err := calibration.Fisher([]float64{1, 0.5}); err != nil {
		t.Errorf("Fisher with p = 1: unexpected error: %v", err)
	}
}

// TestFisherSummationOrderIsDocumentedNotHidden pins the documented contract: the
// statistic is the float sum in the slice order given, so a permuted input may
// legitimately differ in the last bit — Fisher does not sort to hide that — while
// the same order twice is bit-identical (R4 determinism, E8 replays).
func TestFisherSummationOrderIsDocumentedNotHidden(t *testing.T) {
	original := []float64{0.037, 0.51, 0.0042, 0.77, 0.11}
	permuted := []float64{0.77, 0.0042, 0.11, 0.037, 0.51}

	x2First, _, tailFirst, err := calibration.Fisher(original)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	x2Permuted, _, _, err := calibration.Fisher(permuted)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	x2Again, _, tailAgain, err := calibration.Fisher(original)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The permutation changes only rounding, never the value beyond it.
	assertRelative(t, "permuted X²", x2Permuted, x2First, 1e-12)
	if math.Float64bits(x2Permuted) != math.Float64bits(x2First) {
		t.Logf("permuted input differs in the last bit, as documented: %x vs %x",
			math.Float64bits(x2Permuted), math.Float64bits(x2First))
	}

	// The same order must be bit-identical every time.
	if math.Float64bits(x2First) != math.Float64bits(x2Again) {
		t.Errorf("same input twice gave different X² bits: %x vs %x",
			math.Float64bits(x2First), math.Float64bits(x2Again))
	}
	if math.Float64bits(tailFirst) != math.Float64bits(tailAgain) {
		t.Errorf("same input twice gave different tail bits: %x vs %x",
			math.Float64bits(tailFirst), math.Float64bits(tailAgain))
	}
}

// TestBrownWithoutCovarianceDegradesToFisher asserts the §10.2 degradation: with a
// nil (or all-zero) covariance the correction reduces to Fisher exactly — c = 1 and
// f = 2J in exact float arithmetic, and a bit-identical tail.
func TestBrownWithoutCovarianceDegradesToFisher(t *testing.T) {
	p := []float64{0.04, 0.2, 0.6}
	x2Fisher, df, tailFisher, err := calibration.Fisher(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	allZero := [][]float64{
		{0, 0, 0},
		{0, 0, 0},
		{0, 0, 0},
	}
	for name, covariance := range map[string][][]float64{"nil": nil, "all-zero": allZero} {
		x2, c, f, tail, err := calibration.Brown(p, covariance)
		if err != nil {
			t.Fatalf("%s covariance: unexpected error: %v", name, err)
		}
		if math.Float64bits(x2) != math.Float64bits(x2Fisher) {
			t.Errorf("%s covariance: X² bits differ from Fisher: %x vs %x",
				name, math.Float64bits(x2), math.Float64bits(x2Fisher))
		}
		if c != 1 {
			t.Errorf("%s covariance: c = %.17g, want exactly 1", name, c)
		}
		if f != float64(df) {
			t.Errorf("%s covariance: f = %.17g, want exactly %d", name, f, df)
		}
		if math.Float64bits(tail) != math.Float64bits(tailFisher) {
			t.Errorf("%s covariance: tail bits differ from Fisher: %x vs %x",
				name, math.Float64bits(tail), math.Float64bits(tailFisher))
		}
	}
}

// TestBrownWithPositiveCovarianceWeakensSignificance asserts the direction of the
// correction: positively correlated detectors carry less independent evidence than
// Fisher assumes, so c > 1, f < 2J, and the corrected tail must be LARGER than
// Fisher's on the same inputs. Ignoring the correction would be anti-conservative;
// applying it must weaken significance.
func TestBrownWithPositiveCovarianceWeakensSignificance(t *testing.T) {
	p := []float64{0.01, 0.02, 0.03}
	cov := calibration.KostMcDermott(0.5)
	if cov <= 0 {
		t.Fatalf("KostMcDermott(0.5) = %v, expected a positive covariance", cov)
	}
	covariance := [][]float64{
		{0, cov, cov},
		{cov, 0, cov},
		{cov, cov, 0},
	}

	_, df, tailFisher, err := calibration.Fisher(p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, c, f, tailBrown, err := calibration.Brown(p, covariance)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if c <= 1 {
		t.Errorf("c = %v, want > 1 under positive covariance", c)
	}
	if f >= float64(df) {
		t.Errorf("f = %v, want < 2J = %d under positive covariance", f, df)
	}
	if tailBrown <= tailFisher {
		t.Errorf("Brown tail %v is not larger than Fisher tail %v; "+
			"the correction must weaken significance", tailBrown, tailFisher)
	}
}

func TestKostMcDermottClampsCorrelation(t *testing.T) {
	// 3.263·1 + 0.710·1 + 0.027·1 = 4.000 at the upper clamp.
	atOne := calibration.KostMcDermott(1)
	if got := calibration.KostMcDermott(1.7); got != atOne {
		t.Errorf("rho above 1 must clamp: got %v, want %v", got, atOne)
	}
	atMinusOne := calibration.KostMcDermott(-1)
	if got := calibration.KostMcDermott(-2.5); got != atMinusOne {
		t.Errorf("rho below -1 must clamp: got %v, want %v", got, atMinusOne)
	}
	assertRelative(t, "KostMcDermott(0.5)",
		calibration.KostMcDermott(0.5), 3.263*0.5+0.710*0.25+0.027*0.125, 1e-15)
}

func TestSidak(t *testing.T) {
	// Exact small case: 1 − (1 − 0.1)³ = 1 − 0.729 = 0.271.
	assertRelative(t, "Sidak(0.1, 3)", calibration.Sidak(0.1, 3), 0.271, 1e-15)

	// Tiny minP: the expm1/log1p form must keep the full tail. The naive
	// 1 − (1 − minP)^T computes 1 − minP = 1 exactly here and returns 0; the true
	// value is T·minP to within relative T·minP, so comparing against 10·1e−12 at
	// relative 1e−6 both proves the value and proves no cancellation happened.
	assertRelative(t, "Sidak(1e-12, 10)", calibration.Sidak(1e-12, 10), 10*1e-12, 1e-6)

	// T = 1 is no multiplicity: minP passes through unchanged, bit for bit.
	if got := calibration.Sidak(0.37, 1); math.Float64bits(got) != math.Float64bits(0.37) {
		t.Errorf("Sidak(0.37, 1) = %v, want 0.37 unchanged", got)
	}
	if got := calibration.Sidak(0.37, 0); math.Float64bits(got) != math.Float64bits(0.37) {
		t.Errorf("Sidak(0.37, 0) = %v, want 0.37 unchanged", got)
	}

	// minP ≥ 1 saturates at exactly 1.
	if got := calibration.Sidak(1, 5); got != 1 {
		t.Errorf("Sidak(1, 5) = %v, want 1", got)
	}
}

// fdrFixturePValues is the classic 25-test teaching example used with the
// Benjamini–Hochberg procedure, already in ascending order (the procedure itself
// must not rely on that).
var fdrFixturePValues = []float64{
	0.001, 0.008, 0.039, 0.041, 0.042, 0.06, 0.074, 0.205, 0.212, 0.216, 0.222,
	0.251, 0.269, 0.275, 0.34, 0.341, 0.384, 0.569, 0.594, 0.696, 0.762, 0.94,
	0.942, 0.975, 1.0,
}

// TestBenjaminiHochbergHandFixture walks the fixture at q = 0.25 by hand. The
// thresholds are (i/25)·0.25 = i·0.01:
//
//	i=1: 0.001 ≤ 0.01 ✓   i=2: 0.008 ≤ 0.02 ✓   i=3: 0.039 > 0.03 ✗
//	i=4: 0.041 > 0.04 ✗   i=5: 0.042 ≤ 0.05 ✓   i=6: 0.06  ≤ 0.06 ✓
//	i=7: 0.074 > 0.07 ✗   i≥8: every p_(i) exceeds its threshold (max 0.25 at i=25)
//
// The largest passing rank is therefore i = 6, on an exact boundary: p_(6) = 0.06
// equals (6/25)·0.25. The equality survives float64 exactly — 0.06 and 0.24 share
// the real significand 1.92, which rounds identically at both exponents, and the
// ·0.25 rescaling is exact — so the ≤ criterion of [71] admits rank 6, and the six
// smallest p-values are discoveries. (Derivations of this example that stop at five
// discoveries use a strict < criterion; the step-up procedure of [71] is defined
// with ≤, which this test pins deliberately.)
func TestBenjaminiHochbergHandFixture(t *testing.T) {
	discoveries := calibration.BenjaminiHochberg(fdrFixturePValues, 0.25)
	if len(discoveries) != 6 {
		t.Fatalf("got %d discoveries, want 6: %v", len(discoveries), discoveries)
	}
	for rank, d := range discoveries {
		if d.Index != rank {
			t.Errorf("discovery %d: index %d, want %d", rank, d.Index, rank)
		}
		if d.PValue != fdrFixturePValues[rank] {
			t.Errorf("discovery %d: p-value %v, want %v", rank, d.PValue, fdrFixturePValues[rank])
		}
	}
}

// TestBenjaminiYekutieliShrinksThreshold reruns the fixture under arbitrary
// dependence. H_25 = Σ_{i=1..25} 1/i ≈ 3.8159581777535068, so the corrected rate is
// q/H_25 ≈ 0.0655145 and the thresholds are (i/25)·0.0655145 ≈ i·0.00262058:
//
//	i=1: 0.001 ≤ 0.00262 ✓   i=2: 0.008 > 0.00524 ✗   i=3: 0.039 > 0.00786 ✗
//
// and every later rank fails by a growing margin — the thresholds climb by only
// 0.00262 per rank to 0.0655 at i=25 while the sorted p-values reach 0.074 by rank
// 7 — so the arithmetic gives exactly one discovery, the p-value 0.001 at index 0.
func TestBenjaminiYekutieliShrinksThreshold(t *testing.T) {
	byDiscoveries := calibration.BenjaminiYekutieli(fdrFixturePValues, 0.25)
	bhDiscoveries := calibration.BenjaminiHochberg(fdrFixturePValues, 0.25)

	if len(byDiscoveries) > len(bhDiscoveries) {
		t.Errorf("BY made %d discoveries, more than BH's %d; the BY threshold is strictly smaller",
			len(byDiscoveries), len(bhDiscoveries))
	}
	if len(byDiscoveries) != 1 {
		t.Fatalf("got %d discoveries, want exactly 1: %v", len(byDiscoveries), byDiscoveries)
	}
	if byDiscoveries[0].Index != 0 || byDiscoveries[0].PValue != 0.001 {
		t.Errorf("got %+v, want {Index: 0, PValue: 0.001}", byDiscoveries[0])
	}
}

// TestBenjaminiHochbergDeterministicWithTies runs the procedure twice on an input
// containing equal p-values: the index tiebreak is a total order, so the result —
// including which of the tied values sits at which rank — must be identical run to
// run (R4).
func TestBenjaminiHochbergDeterministicWithTies(t *testing.T) {
	p := []float64{0.02, 0.01, 0.02, 0.001, 0.9}
	first := calibration.BenjaminiHochberg(p, 0.25)
	second := calibration.BenjaminiHochberg(p, 0.25)

	// Hand derivation: sorted with index tiebreak the ranks are 0.001(idx 3),
	// 0.01(idx 1), 0.02(idx 0), 0.02(idx 2), 0.9(idx 4); thresholds i·0.05. Rank 4:
	// 0.02 ≤ 0.2 ✓, rank 5: 0.9 > 0.25 ✗, so the four smallest are discovered.
	wantIndices := []int{0, 1, 2, 3}
	if len(first) != len(wantIndices) {
		t.Fatalf("got %d discoveries, want %d: %v", len(first), len(wantIndices), first)
	}
	for i, want := range wantIndices {
		if first[i].Index != want {
			t.Errorf("discovery %d: index %d, want %d", i, first[i].Index, want)
		}
	}

	if len(second) != len(first) {
		t.Fatalf("second run returned %d discoveries, first returned %d", len(second), len(first))
	}
	for i := range first {
		if first[i].Index != second[i].Index ||
			math.Float64bits(first[i].PValue) != math.Float64bits(second[i].PValue) {
			t.Errorf("run-to-run mismatch at %d: %+v vs %+v", i, first[i], second[i])
		}
	}
}

func TestStepUpDegenerateInputs(t *testing.T) {
	for name, got := range map[string][]calibration.Discovery{
		"BH empty": calibration.BenjaminiHochberg([]float64{}, 0.25),
		"BH nil":   calibration.BenjaminiHochberg(nil, 0.25),
		"BH q=0":   calibration.BenjaminiHochberg([]float64{0.001}, 0),
		"BH q<0":   calibration.BenjaminiHochberg([]float64{0.001}, -1),
		"BY empty": calibration.BenjaminiYekutieli(nil, 0.25),
		"BY q=0":   calibration.BenjaminiYekutieli([]float64{0.001}, 0),
	} {
		if got == nil {
			t.Errorf("%s: got nil, want an empty non-nil slice", name)
		}
		if len(got) != 0 {
			t.Errorf("%s: got %v, want no discoveries", name, got)
		}
	}
}

// TestGammaLowerTailOverTheBurstArmsRegion covers the shape region the fitted inter-arrival null
// opened up (#53) and the exponential one never reached.
//
// With a per-entity gap dispersion kappa the tail is evaluated at a = (k-1)*kappa, and kappa runs
// down to 0.01 on clustered traffic, so a can be as small as 0.02 where before it was an integer
// of at least two. Both branches of the implementation are exercised — the series below a+1 and the
// continued fraction above it — and the region is where the branch boundary itself sits.
//
// Three properties, each of which a scoring path depends on: the result is a log-probability, it
// increases with the span, and it is always representable. A non-finite value here is refused by
// the verdict constructor and stops a replay that has already run for an hour.
func TestGammaLowerTailOverTheBurstArmsRegion(t *testing.T) {
	shapes := []float64{0.02, 0.09, 0.31, 0.9, 1, 2.79, 6, 31}
	scaled := []float64{1e-9, 1e-6, 1e-3, 0.1, 0.5, 0.99, 1, 1.01, 2, 10, 100, 1e4}

	for _, a := range shapes {
		previous := math.Inf(-1)
		for _, x := range scaled {
			got := calibration.GammaLowerTailLog(a, x)
			switch {
			case math.IsNaN(got):
				t.Errorf("a = %g, x = %g: NaN", a, x)
			case math.IsInf(got, 1):
				t.Errorf("a = %g, x = %g: +Inf, which is a probability above one", a, x)
			case got > 0:
				t.Errorf("a = %g, x = %g: ln P = %g, above zero", a, x, got)
			case math.IsInf(got, -1):
				t.Errorf("a = %g, x = %g: -Inf. A positive span has positive probability of "+
					"being at least this short, and the verdict constructor refuses a "+
					"non-finite p-value", a, x)
			case got < previous:
				t.Errorf("a = %g: ln P fell from %g to %g as x rose to %g; the lower tail is "+
					"increasing in the span, and a scan that ranked a LONGER span as more "+
					"surprising would invert the arm", a, previous, got, x)
			}
			previous = got
		}
		// At a large multiple of the mean the lower tail is essentially one.
		if far := calibration.GammaLowerTailLog(a, 200*(a+1)); far < math.Log(0.99) {
			t.Errorf("a = %g: ln P = %g far out in the upper tail, want essentially zero",
				a, far)
		}
	}

	// The two branches must agree across the boundary they share, or the statistic has a step
	// in it that no data caused.
	for _, a := range shapes {
		below := calibration.GammaLowerTailLog(a, math.Nextafter(a+1, 0))
		above := calibration.GammaLowerTailLog(a, math.Nextafter(a+1, 2*(a+1)))
		if math.Abs(below-above) > 1e-9*math.Abs(below) {
			t.Errorf("a = %g: the series gives ln P = %.12g just below x = a+1 and the "+
				"continued fraction %.12g just above; the branches disagree by %.3g",
				a, below, above, above-below)
		}
	}

	// A shape of zero has no distribution, and a non-positive span is not a duration.
	if got := calibration.GammaLowerTailLog(0, 1); got != 0 {
		t.Errorf("a = 0 gave ln P = %g, want 0", got)
	}
	if got := calibration.GammaLowerTailLog(1, 0); !math.IsInf(got, -1) {
		t.Errorf("x = 0 gave ln P = %g, want -Inf: a zero-length span has probability zero "+
			"under a continuous null, which is why the burst arm never passes one", got)
	}
}
