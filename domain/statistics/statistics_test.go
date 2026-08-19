package statistics_test

import (
	"math"
	"testing"

	"github.com/JohnPierman/ethogram/domain/statistics"
)

// The expectations below are reference values from scipy: Clopper–Pearson and Wilson
// from scipy.stats.binomtest(...).proportion_ci, the exact McNemar p-value from
// scipy.stats.binomtest(a, a+b, 0.5), and the corrected χ² tail from
// scipy.stats.chi2.sf. Checking against an independent implementation is what makes
// these estimators defensible in a report: a referee can regenerate the table.

func closeRel(t *testing.T, name string, got, want, tol float64) {
	t.Helper()
	if want == 0 {
		if math.Abs(got) > tol {
			t.Errorf("%s = %v, want 0", name, got)
		}
		return
	}
	if rel := math.Abs(got-want) / math.Abs(want); rel > tol {
		t.Errorf("%s = %.17g, want %.17g (relative error %.2e)", name, got, want, rel)
	}
}

func TestClopperPearsonAgainstScipy(t *testing.T) {
	for _, tc := range []struct {
		k, n            int
		wantLow, wantHi float64
	}{
		{0, 10, 0.0, 0.3084971078187607},
		{1, 10, 0.0025285785444617865, 0.4450161170281954},
		{5, 10, 0.18708602844739855, 0.8129139715526015},
		{9, 10, 0.5549838829718047, 0.9974714214555382},
		{10, 10, 0.6915028921812393, 1.0},
		{3, 100, 0.006229971538306397, 0.08517605297428},
		{50, 1000, 0.03733539760466177, 0.06539048791549365},
		// The shape of a real E1 row: 579 of 700 red-team events detected.
		{579, 700, 0.7970544133983395, 0.854441621597948},
	} {
		iv := statistics.ClopperPearsonInterval(tc.k, tc.n)
		closeRel(t, "CP low", iv.Low, tc.wantLow, 1e-9)
		closeRel(t, "CP high", iv.High, tc.wantHi, 1e-9)
		if iv.N != tc.n {
			t.Errorf("n = %d, want %d", iv.N, tc.n)
		}
		if iv.Point != float64(tc.k)/float64(tc.n) {
			t.Errorf("point = %v", iv.Point)
		}
		// The exact interval must contain the point estimate.
		if iv.Point < iv.Low-1e-12 || iv.Point > iv.High+1e-12 {
			t.Errorf("point %v outside [%v, %v]", iv.Point, iv.Low, iv.High)
		}
	}
}

func TestWilsonAgainstScipy(t *testing.T) {
	for _, tc := range []struct {
		k, n            int
		wantLow, wantHi float64
	}{
		{0, 10, 0.0, 0.27753279986288926},
		{1, 10, 0.017876213095072896, 0.4041500267952385},
		{5, 10, 0.236593090512564, 0.7634069094874361},
		{9, 10, 0.5958499732047615, 0.9821237869049271},
		{10, 10, 0.7224672001371109, 1.0},
		{3, 100, 0.010254524024038925, 0.08451936429052762},
		{50, 1000, 0.03813026239274881, 0.06531382024425081},
		{579, 700, 0.7973656206898444, 0.8533491024345813},
	} {
		iv := statistics.WilsonInterval(tc.k, tc.n)
		closeRel(t, "Wilson low", iv.Low, tc.wantLow, 1e-9)
		closeRel(t, "Wilson high", iv.High, tc.wantHi, 1e-9)
	}
}

// TestWilsonIsNarrowerThanClopperPearson pins the relationship that justifies
// reporting both: the exact interval never has coverage below nominal and is
// therefore wider; Wilson is tighter and is the one to read when width matters.
func TestWilsonIsNarrowerThanClopperPearson(t *testing.T) {
	for _, tc := range []struct{ k, n int }{{1, 10}, {5, 10}, {3, 100}, {50, 1000}} {
		w := statistics.WilsonInterval(tc.k, tc.n)
		cp := statistics.ClopperPearsonInterval(tc.k, tc.n)
		if (w.High - w.Low) > (cp.High - cp.Low) {
			t.Errorf("k=%d n=%d: Wilson width %v exceeds Clopper-Pearson %v",
				tc.k, tc.n, w.High-w.Low, cp.High-cp.Low)
		}
	}
}

func TestIntervalsHandleDegenerateInput(t *testing.T) {
	for _, iv := range []statistics.Interval{
		statistics.WilsonInterval(0, 0),
		statistics.ClopperPearsonInterval(0, 0),
		statistics.WilsonInterval(5, -1),
	} {
		if iv.Low != 0 || iv.High != 0 || iv.Point != 0 {
			t.Errorf("degenerate interval should be zeroed, got %+v", iv)
		}
	}
}

// ---------------------------------------------------------------------------
// McNemar
// ---------------------------------------------------------------------------

// pairs builds aligned outcome slices with the given discordant and concordant
// counts, which is the only structure the test needs.
func pairs(bothDetected, onlyA, onlyB, neither int) (a, b []bool) {
	for range bothDetected {
		a, b = append(a, true), append(b, true)
	}
	for range onlyA {
		a, b = append(a, true), append(b, false)
	}
	for range onlyB {
		a, b = append(a, false), append(b, true)
	}
	for range neither {
		a, b = append(a, false), append(b, false)
	}
	return a, b
}

func TestMcNemarExactAgainstScipy(t *testing.T) {
	for _, tc := range []struct {
		onlyA, onlyB int
		wantP        float64
	}{
		{10, 2, 0.03857421875},
		{3, 1, 0.625},
		{12, 5, 0.143463134765625},
		{1, 0, 1.0},
		{7, 7, 1.0},
	} {
		a, b := pairs(50, tc.onlyA, tc.onlyB, 30)
		got := statistics.McNemar(a, b)
		if !got.Exact {
			t.Errorf("onlyA=%d onlyB=%d: expected the exact test below 25 discordant pairs",
				tc.onlyA, tc.onlyB)
		}
		closeRel(t, "exact p", got.PValue, tc.wantP, 1e-9)
		if got.OnlyA != tc.onlyA || got.OnlyB != tc.onlyB {
			t.Errorf("discordant counts = (%d, %d), want (%d, %d)",
				got.OnlyA, got.OnlyB, tc.onlyA, tc.onlyB)
		}
		if got.Delta != tc.onlyA-tc.onlyB {
			t.Errorf("delta = %d", got.Delta)
		}
		// Concordant pairs must not enter the test.
		if got.BothDetected != 50 || got.NeitherDetected != 30 {
			t.Errorf("concordant counts wrong: %+v", got)
		}
	}
}

func TestMcNemarChiSquareAgainstScipy(t *testing.T) {
	for _, tc := range []struct {
		onlyA, onlyB    int
		wantChi2, wantP float64
	}{
		{30, 15, 4.355555555555555, 0.036888425707049914},
		{100, 60, 9.50625, 0.002047732153668759},
		{40, 20, 6.016666666666667, 0.014171388254012323},
	} {
		a, b := pairs(100, tc.onlyA, tc.onlyB, 100)
		got := statistics.McNemar(a, b)
		if got.Exact {
			t.Errorf("onlyA=%d onlyB=%d: expected the χ² form above 25 discordant pairs",
				tc.onlyA, tc.onlyB)
		}
		closeRel(t, "chi2", got.Statistic, tc.wantChi2, 1e-12)
		closeRel(t, "chi2 p", got.PValue, tc.wantP, 1e-9)
	}
}

// TestMcNemarIgnoresConcordantPairs is the property that makes the test the right one
// for E4 and E9: events both arms agree on carry no information about which arm is
// better, so adding any number of them must not move the p-value.
func TestMcNemarIgnoresConcordantPairs(t *testing.T) {
	a1, b1 := pairs(0, 30, 15, 0)
	a2, b2 := pairs(100_000, 30, 15, 50_000)
	r1, r2 := statistics.McNemar(a1, b1), statistics.McNemar(a2, b2)
	if r1.PValue != r2.PValue || r1.Statistic != r2.Statistic {
		t.Fatalf("concordant pairs changed the test: %v vs %v", r1.PValue, r2.PValue)
	}
}

// TestMcNemarPerfectAgreementAssertsNothing: identical arms give no evidence of a
// difference, and the result must say so rather than report a significant zero.
func TestMcNemarPerfectAgreement(t *testing.T) {
	a, b := pairs(500, 0, 0, 500)
	got := statistics.McNemar(a, b)
	if got.PValue != 1 {
		t.Errorf("p = %v for identical arms, want 1", got.PValue)
	}
	if got.Delta != 0 || got.Statistic != 0 {
		t.Errorf("identical arms should show no difference: %+v", got)
	}
}

// ---------------------------------------------------------------------------
// Paired bootstrap
// ---------------------------------------------------------------------------

func TestPairedBootstrapRecoversTheObservedDelta(t *testing.T) {
	a, b := pairs(200, 60, 20, 300)
	got := statistics.PairedBootstrapDelta(a, b, 500, 12345)
	if got.Observed != 40 {
		t.Fatalf("observed delta = %v, want 40", got.Observed)
	}
	if got.Low > got.Observed || got.High < got.Observed {
		t.Errorf("the interval [%v, %v] excludes the observed delta %v",
			got.Low, got.High, got.Observed)
	}
	if !got.ExcludesZero {
		t.Errorf("a 60-against-20 advantage over 580 events should exclude zero: [%v, %v]",
			got.Low, got.High)
	}
}

// TestPairedBootstrapIsDeterministic: the interval must be reproducible from the seed
// recorded in the result file, or a reader cannot check it.
func TestPairedBootstrapIsDeterministic(t *testing.T) {
	a, b := pairs(120, 30, 25, 200)
	first := statistics.PairedBootstrapDelta(a, b, 400, 999)
	for range 8 {
		got := statistics.PairedBootstrapDelta(a, b, 400, 999)
		if got != first {
			t.Fatalf("bootstrap not reproducible: %+v vs %+v", got, first)
		}
	}
	// A different seed may move the interval; that is expected, and is why the seed
	// is recorded.
	other := statistics.PairedBootstrapDelta(a, b, 400, 1000)
	if other.Observed != first.Observed {
		t.Errorf("the observed delta must not depend on the seed: %v vs %v",
			other.Observed, first.Observed)
	}
}

// TestPairedBootstrapIncludesZeroForEqualArms: arms that differ only by noise must
// produce an interval containing zero, or the method would manufacture significance.
func TestPairedBootstrapIncludesZeroForEqualArms(t *testing.T) {
	a, b := pairs(100, 20, 20, 300)
	got := statistics.PairedBootstrapDelta(a, b, 600, 7)
	if got.ExcludesZero {
		t.Errorf("equal arms produced a delta interval excluding zero: [%v, %v]",
			got.Low, got.High)
	}
}

func TestPairedBootstrapDegenerateInput(t *testing.T) {
	if got := statistics.PairedBootstrapDelta(nil, nil, 100, 1); got.Observed != 0 {
		t.Errorf("empty input should give a zero delta, got %+v", got)
	}
	a, b := pairs(10, 1, 1, 10)
	if got := statistics.PairedBootstrapDelta(a, b, 0, 1); got.Resamples != 0 {
		t.Errorf("zero resamples should produce no interval, got %+v", got)
	}
}
