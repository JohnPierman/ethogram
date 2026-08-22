package calibration_test

import (
	"math"
	"math/rand/v2"
	"testing"

	"github.com/JohnPierman/ethogram/domain/calibration"
)

// The diagnostic is itself an instrument, so it gets the same treatment every arm in this
// repository gets: it is checked against constructed data whose answer is known before it is
// pointed at a corpus. A diagnostic that reports "the correction is off by a factor of nine"
// without having been shown to report "the correction is fine" when it is would be worthless.

const brownDay = 86400

// fisherOf returns Fisher's statistic for one draw of J independent uniforms.
func fisherOf(rng *rand.Rand, j int) float64 {
	x2 := 0.0
	for i := 0; i < j; i++ {
		x2 += -2 * math.Log(rng.Float64())
	}
	return x2
}

// TestIndependentUniformsAreCalledCorrect is the null case. With independent uniform p-values
// Fisher is exactly right: E[X²] = 2J and Var[X²] = 4J, which is what a zero covariance predicts
// (c = 1, f = 2J). Both ratios must come back near one and the diagnostic must say so.
func TestIndependentUniformsAreCalledCorrect(t *testing.T) {
	rng := rand.New(rand.NewPCG(55, 55))
	const j = 6
	d := calibration.NewBrownDiagnostic(brownDay)
	for i := 0; i < 200_000; i++ {
		// c = 1, f = 2J is Brown degrading to Fisher, which §10.2 requires.
		d.Observe("scored", 0, j, fisherOf(rng, j), 1, 2*float64(j))
	}

	cells := d.Cells()
	if len(cells) != 1 {
		t.Fatalf("got %d cells, want 1", len(cells))
	}
	c := cells[0]
	t.Logf("mean %.3f (want %.0f, ratio %.4f); variance %.3f (want %.0f, ratio %.4f); "+
		"f %.2f against empirical %.2f", c.MeanX2, c.ExpectedMean, c.MeanRatio,
		c.VarianceX2, c.PredictedVariance, c.VarianceRatio, c.BrownF, c.EmpiricalF)

	if math.Abs(c.MeanRatio-1) > 0.02 {
		t.Errorf("mean ratio %.4f on independent uniforms: Fisher's mean is exactly 2J here",
			c.MeanRatio)
	}
	if math.Abs(c.VarianceRatio-1) > 0.03 {
		t.Errorf("variance ratio %.4f on independent uniforms: Fisher's variance is exactly "+
			"4J here", c.VarianceRatio)
	}
	if math.Abs(c.EmpiricalF-c.BrownF) > 0.05*c.BrownF {
		t.Errorf("empirical f %.2f against Brown's %.2f", c.EmpiricalF, c.BrownF)
	}
	if got := d.Summarise().Finding; got !=
		"both moments are within tolerance of the assumed distribution" {
		t.Errorf("the diagnostic reported a defect on data with none: %q", got)
	}
}

// TestPerfectDependenceIsCaughtAndItsDirectionIsRight is the defect #55 is about. When every
// detector returns the SAME p-value, X² = J·(−2 ln p), so E = 2J still — dependence does not move
// the mean, which is the whole reason Brown corrects only the variance — but Var = 4J² rather
// than 4J.
//
// An uncorrected Fisher therefore under-states the dispersion by a factor of J, refers the
// statistic to a chi-square that is too narrow, and returns a tail that is too small. The
// diagnostic must report the factor and must call the direction anti-conservative, because that
// is the direction that manufactures discoveries a nominal level did not license.
func TestPerfectDependenceIsCaughtAndItsDirectionIsRight(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 55))
	const j = 6
	d := calibration.NewBrownDiagnostic(brownDay)
	for i := 0; i < 200_000; i++ {
		x2 := float64(j) * -2 * math.Log(rng.Float64())
		d.Observe("scored", 0, j, x2, 1, 2*float64(j)) // uncorrected: c = 1, f = 2J
	}

	c := d.Cells()[0]
	t.Logf("mean ratio %.4f; variance %.1f against a predicted %.1f (ratio %.2f); "+
		"f %.2f against empirical %.2f", c.MeanRatio, c.VarianceX2, c.PredictedVariance,
		c.VarianceRatio, c.BrownF, c.EmpiricalF)

	if math.Abs(c.MeanRatio-1) > 0.02 {
		t.Errorf("mean ratio %.4f: dependence must not move the mean", c.MeanRatio)
	}
	// Var = 4J² against a predicted 4J, so the ratio is J.
	if math.Abs(c.VarianceRatio-float64(j)) > 0.15*float64(j) {
		t.Errorf("variance ratio %.3f, want about %d: perfectly dependent p-values make "+
			"Fisher's variance J times its independent value", c.VarianceRatio, j)
	}
	// The matched chi-square has f = 2E²/Var = 2·(2J)²/(4J²) = 2. One effective test, which
	// is the truth: J copies of the same p-value carry one test's worth of evidence.
	if math.Abs(c.EmpiricalF-2) > 0.4 {
		t.Errorf("empirical f %.3f, want about 2: J copies of one p-value are one test",
			c.EmpiricalF)
	}
	if want := "the dependence, anti-conservatively"; len(c.Direction) < len(want) ||
		c.Direction[:len(want)] != want {
		t.Errorf("direction %q, want it to open with %q", c.Direction, want)
	}
}

// TestBrownGetsCreditWhenItsCovarianceIsRight is the other side: the same perfectly dependent
// statistic, with the covariance that actually describes it. Cov(−2 ln p, −2 ln p) = Var(−2 ln p)
// = 4 for each of the J(J−1)/2 pairs, so Var = 4J + 2·4·J(J−1)/2 = 4J². Brown then predicts the
// dispersion exactly and the diagnostic must not accuse a correction that is right.
func TestBrownGetsCreditWhenItsCovarianceIsRight(t *testing.T) {
	rng := rand.New(rand.NewPCG(2, 55))
	const j = 6
	// The moments Brown would compute from a correct covariance.
	expectation := 2 * float64(j)
	variance := 4*float64(j) + 2*4*float64(j*(j-1)/2)
	brownC := variance / (2 * expectation)
	brownF := 2 * expectation * expectation / variance

	d := calibration.NewBrownDiagnostic(brownDay)
	for i := 0; i < 200_000; i++ {
		x2 := float64(j) * -2 * math.Log(rng.Float64())
		d.Observe("scored", 0, j, x2, brownC, brownF)
	}

	c := d.Cells()[0]
	t.Logf("with the correct covariance: c %.2f, f %.2f; variance ratio %.4f",
		c.BrownC, c.BrownF, c.VarianceRatio)
	if math.Abs(c.VarianceRatio-1) > 0.05 {
		t.Errorf("variance ratio %.4f with a covariance that exactly describes the "+
			"dependence: Brown is right here and must not be accused", c.VarianceRatio)
	}
	if got := d.Summarise().Finding; got !=
		"both moments are within tolerance of the assumed distribution" {
		t.Errorf("reported %q for a correct correction", got)
	}
}

// TestANonUniformMarginalIsBlamedOnTheMarginals is the distinction the diagnostic exists to draw.
// Here the p-values are independent — the dependence is perfect — but each is stochastically too
// small, which is what an anti-conservative detector null produces. The mean must move, and the
// diagnostic must say the inputs rather than the dependence, because no covariance estimate
// reaches a mean.
func TestANonUniformMarginalIsBlamedOnTheMarginals(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 55))
	const j = 6
	d := calibration.NewBrownDiagnostic(brownDay)
	for i := 0; i < 200_000; i++ {
		// p = u², which is stochastically smaller than uniform, so −2 ln p is twice as
		// large in expectation and the mean lands near 4J rather than 2J.
		x2 := 0.0
		for k := 0; k < j; k++ {
			u := rng.Float64()
			x2 += -2 * math.Log(u*u)
		}
		d.Observe("scored", 0, j, x2, 1, 2*float64(j))
	}

	c := d.Cells()[0]
	t.Logf("mean ratio %.3f, variance ratio %.3f; direction: %s",
		c.MeanRatio, c.VarianceRatio, c.Direction)
	if c.MeanRatio < 1.5 {
		t.Errorf("mean ratio %.3f: p = u^2 doubles E[-2 ln p], so the mean must be near 4J",
			c.MeanRatio)
	}
	if want := "both halves fail"; c.Direction[:len(want)] == want {
		// Var[-2 ln u^2] = 16 against the 4 a uniform gives, so the variance is off too and
		// naming both is correct. What must NOT happen is the dependence being blamed alone.
		return
	}
	if want := "the marginals, not the dependence"; len(c.Direction) < len(want) ||
		c.Direction[:len(want)] != want {
		t.Errorf("direction %q: a mean away from 2J is a defect in the inputs, and Brown "+
			"matches the mean by construction so it cannot be the dependence alone",
			c.Direction)
	}
}

// TestPhaseAndDayAreSeparated pins the bucketing, which is what lets in-sample be distinguished
// from out-of-sample and a correction that drifted from one that was never right.
func TestPhaseAndDayAreSeparated(t *testing.T) {
	d := calibration.NewBrownDiagnostic(brownDay)
	for i := 0; i < calibration.MinimumCount+5; i++ {
		d.Observe("burn_in", 0, 4, 8, 1, 8)
		d.Observe("burn_in", 2*brownDay, 4, 8, 1, 8)
		d.Observe("scored", 9*brownDay, 4, 8, 1, 8)
		d.Observe("scored", 9*brownDay, 3, 6, 1, 6) // same day, different J
	}

	cells := d.Cells()
	if len(cells) != 4 {
		t.Fatalf("got %d cells, want 4 (two burn-in days, then one scored day at two J)",
			len(cells))
	}
	// Ordered by phase, then day, then J — deterministic output (R4).
	want := []struct {
		phase string
		day   int64
		j     int
	}{
		{"burn_in", 0, 4}, {"burn_in", 2, 4}, {"scored", 9, 3}, {"scored", 9, 4},
	}
	for i, w := range want {
		got := cells[i]
		if got.Phase != w.phase || got.Day != w.day || got.J != w.j {
			t.Errorf("cell %d is (%s, day %d, J %d), want (%s, day %d, J %d)",
				i, got.Phase, got.Day, got.J, w.phase, w.day, w.j)
		}
	}
}

// TestASmallCellReportsItsCountRatherThanAVariance covers the honesty requirement: a variance
// from a handful of draws could manufacture the very factor-of-nine claim this diagnostic exists
// to make, so a thin cell must decline to make it while still being visible.
func TestASmallCellReportsItsCountRatherThanAVariance(t *testing.T) {
	d := calibration.NewBrownDiagnostic(brownDay)
	for i := 0; i < 5; i++ {
		d.Observe("scored", 0, 4, float64(i), 1, 8)
	}
	c := d.Cells()[0]
	if c.Count != 5 {
		t.Errorf("count %d, want 5", c.Count)
	}
	if c.VarianceRatio != 0 {
		t.Errorf("a five-event cell reported a variance ratio of %g", c.VarianceRatio)
	}
	if c.Direction != "too few events in this cell to report a variance" {
		t.Errorf("direction %q", c.Direction)
	}
	if s := d.Summarise(); s.Reported != 0 ||
		s.Finding != "no cell carried enough events to report a variance" {
		t.Errorf("summary reported %d cells and said %q", s.Reported, s.Finding)
	}
}

// TestBadNumbersAreDroppedNotPropagated: one NaN would destroy every moment in its cell, and a
// diagnostic reporting NaN says strictly less than one reporting a smaller count.
func TestBadNumbersAreDroppedNotPropagated(t *testing.T) {
	d := calibration.NewBrownDiagnostic(brownDay)
	for i := 0; i < calibration.MinimumCount; i++ {
		d.Observe("scored", 0, 4, 8, 1, 8)
	}
	d.Observe("scored", 0, 4, math.NaN(), 1, 8)
	d.Observe("scored", 0, 4, math.Inf(1), 1, 8)
	d.Observe("scored", 0, 4, 8, math.NaN(), 8)
	d.Observe("scored", 0, 4, 8, 0, 8) // a non-positive scale is not a scale
	d.Observe("scored", 0, 0, 8, 1, 8) // J = 0 induces no statistic

	c := d.Cells()[0]
	if c.Count != calibration.MinimumCount {
		t.Errorf("count %d, want %d: bad inputs must not be counted",
			c.Count, calibration.MinimumCount)
	}
	for name, v := range map[string]float64{
		"mean": c.MeanX2, "variance": c.VarianceX2, "mean ratio": c.MeanRatio,
		"variance ratio": c.VarianceRatio, "empirical f": c.EmpiricalF,
	} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			t.Errorf("%s is %g", name, v)
		}
	}
}

// TestTheMomentsSurviveALargeOffset is why Welford is used rather than the sum-of-squares form.
// On a model tail X² reaches the thousands, and Σx² − (Σx)²/n then differences two large nearly
// equal numbers and can even return a negative variance.
func TestTheMomentsSurviveALargeOffset(t *testing.T) {
	rng := rand.New(rand.NewPCG(9, 55))
	d := calibration.NewBrownDiagnostic(brownDay)
	const offset = 4000.0
	var naiveSum, naiveSumSq float64
	const n = 50_000
	for i := 0; i < n; i++ {
		x2 := offset + rng.NormFloat64() // unit variance about a large mean
		d.Observe("scored", 0, 4, x2, 1, 8)
		naiveSum += x2
		naiveSumSq += x2 * x2
	}
	got := d.Cells()[0].VarianceX2
	naive := (naiveSumSq - naiveSum*naiveSum/n) / (n - 1)
	t.Logf("Welford %.6f, naive sum-of-squares %.6f, truth 1.0", got, naive)
	if math.Abs(got-1) > 0.05 {
		t.Errorf("variance %.6f, want 1.0 to within 5%%", got)
	}
}

// TestNilIsSafe: the diagnostic is optional, so a run without it must not need a nil check at
// every call site.
func TestNilIsSafe(t *testing.T) {
	var d *calibration.BrownDiagnostic
	d.Observe("scored", 0, 4, 8, 1, 8)
	if cells := d.Cells(); cells != nil {
		t.Errorf("a nil diagnostic returned %d cells", len(cells))
	}
}
