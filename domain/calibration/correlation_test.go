package calibration_test

import (
	"math"
	"testing"

	"github.com/JohnPierman/ethogram/domain/calibration"
)

// The covariance estimated here is the input that decides whether the combination
// treats dependent detectors as independent. An error would not fail loudly; it would
// quietly restore the anti-conservatism the correction exists to remove. The estimates
// are therefore checked against hand-computable fixtures.

func TestCovarianceOfPerfectlyDependentDetectors(t *testing.T) {
	// Two detectors reporting the identical statistic on every event. The sample
	// covariance must equal the sample variance, and the correlation must be 1.
	c := calibration.NewCorrelations(2)
	xs := []float64{1, 2, 3, 4, 5, 6, 7, 8}
	for _, x := range xs {
		c.Observe([]string{"a", "b"}, []float64{x, x})
	}
	est := c.Freeze().Estimates()
	if len(est) != 1 {
		t.Fatalf("got %d pair estimates, want 1", len(est))
	}
	// Variance of 1..8 with the n−1 denominator is 6.
	if math.Abs(est[0].Covariance-6) > 1e-12 {
		t.Errorf("covariance = %v, want 6", est[0].Covariance)
	}
	if math.Abs(est[0].Correlation-1) > 1e-12 {
		t.Errorf("correlation = %v, want 1", est[0].Correlation)
	}
	if !est[0].Used {
		t.Error("a fully supported pair was not used")
	}
}

func TestCovarianceOfIndependentDetectorsIsNearZero(t *testing.T) {
	// A fixture whose deviation products cancel exactly. With x = 1,2,3,4 the deviations
	// about x̄ = 2.5 are −1.5, −0.5, 0.5, 1.5; with y = 1,3,3,1 the deviations about
	// ȳ = 2 are −1, 1, 1, −1. The products are 1.5, −0.5, 0.5, −1.5 and sum to zero, so
	// the covariance is exactly zero rather than merely small.
	c := calibration.NewCorrelations(2)
	pairs := [][2]float64{{1, 1}, {2, 3}, {3, 3}, {4, 1}}
	for _, p := range pairs {
		c.Observe([]string{"a", "b"}, []float64{p[0], p[1]})
	}
	est := c.Freeze().Estimates()
	if math.Abs(est[0].Covariance) > 1e-12 {
		t.Errorf("covariance = %v, want 0 for an uncorrelated fixture", est[0].Covariance)
	}
	if math.Abs(est[0].Correlation) > 1e-12 {
		t.Errorf("correlation = %v, want 0", est[0].Correlation)
	}
}

func TestPairsBelowSupportAreRecordedButNotUsed(t *testing.T) {
	// An estimate from too few points is not evidence. It must be visible in the
	// record and excluded from the matrix, degrading that pair to independence rather
	// than asserting a covariance drawn from three events.
	c := calibration.NewCorrelations(50)
	for i := range 3 {
		c.Observe([]string{"a", "b"}, []float64{float64(i), float64(i)})
	}
	m := c.Freeze()
	est := m.Estimates()
	if len(est) != 1 {
		t.Fatalf("got %d estimates, want 1", len(est))
	}
	if est[0].Used {
		t.Error("a pair with 3 observations was used against a threshold of 50")
	}
	if est[0].N != 3 {
		t.Errorf("N = %d, want 3 recorded even though unused", est[0].N)
	}
	if got := m.Matrix([]string{"a", "b"})[0][1]; got != 0 {
		t.Errorf("unsupported pair contributed %v to the matrix, want 0", got)
	}
}

func TestAbstentionsDoNotContribute(t *testing.T) {
	// Only detectors that both evaluated on the same event may inform their pair. An
	// event where one abstained carries no information about how the two move together.
	c := calibration.NewCorrelations(2)
	c.Observe([]string{"a", "b"}, []float64{1, 1})
	c.Observe([]string{"a"}, []float64{99}) // b abstained
	c.Observe([]string{"b"}, []float64{99}) // a abstained
	c.Observe([]string{"a", "b"}, []float64{3, 3})

	est := c.Freeze().Estimates()
	if len(est) != 1 {
		t.Fatalf("got %d estimates, want 1", len(est))
	}
	if est[0].N != 2 {
		t.Errorf("N = %d, want 2 (only the co-evaluated events)", est[0].N)
	}
}

func TestMatrixIsSymmetricWithZeroDiagonal(t *testing.T) {
	// Equation (19) reads the strict upper triangle, but a matrix that disagreed with
	// itself across the diagonal would be a latent trap for any other reader.
	c := calibration.NewCorrelations(2)
	for i := range 10 {
		x := float64(i)
		c.Observe([]string{"a", "b", "c"}, []float64{x, 2 * x, -x})
	}
	m := c.Freeze()
	ids := []string{"a", "b", "c"}
	mat := m.Matrix(ids)
	for i := range ids {
		if mat[i][i] != 0 {
			t.Errorf("diagonal [%d] = %v, want 0", i, mat[i][i])
		}
		for j := range ids {
			if mat[i][j] != mat[j][i] {
				t.Errorf("matrix is not symmetric at (%d,%d): %v vs %v", i, j, mat[i][j], mat[j][i])
			}
		}
	}
	// a and c move exactly opposite, so their covariance must be negative.
	if mat[0][2] >= 0 {
		t.Errorf("cov(a,c) = %v, want negative for anti-correlated inputs", mat[0][2])
	}
}

func TestMatrixOrderFollowsTheIdsGiven(t *testing.T) {
	// The matrix must line up with the p-value vector equation (19) is handed, whatever
	// order that arrives in.
	c := calibration.NewCorrelations(2)
	for i := range 10 {
		x := float64(i)
		c.Observe([]string{"a", "b"}, []float64{x, x})
	}
	m := c.Freeze()
	forward := m.Matrix([]string{"a", "b"})
	reverse := m.Matrix([]string{"b", "a"})
	if forward[0][1] != reverse[0][1] {
		t.Errorf("pair covariance changed with argument order: %v vs %v",
			forward[0][1], reverse[0][1])
	}
}

func TestUnknownDetectorContributesZero(t *testing.T) {
	// A detector added to the composition after the estimate was frozen must not
	// prevent the correction from using what was measured for the others.
	c := calibration.NewCorrelations(2)
	for i := range 10 {
		x := float64(i)
		c.Observe([]string{"a", "b"}, []float64{x, x})
	}
	m := c.Freeze()
	mat := m.Matrix([]string{"a", "b", "unseen"})
	if mat[0][1] == 0 {
		t.Error("the measured pair lost its covariance when an unknown detector joined")
	}
	if mat[0][2] != 0 || mat[1][2] != 0 {
		t.Error("an unseen detector contributed a covariance it cannot have")
	}
}

func TestEstimateIsDeterministic(t *testing.T) {
	// R4: the same stream must give a bit-identical estimate.
	build := func() []calibration.PairEstimate {
		c := calibration.NewCorrelations(2)
		for i := range 200 {
			x := float64(i%7) * 1.5
			y := float64(i%11) * 0.25
			c.Observe([]string{"a", "b", "c"}, []float64{x, y, x + y})
		}
		return c.Freeze().Estimates()
	}
	first, second := build(), build()
	if len(first) != len(second) {
		t.Fatalf("estimate counts differ: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if math.Float64bits(first[i].Covariance) != math.Float64bits(second[i].Covariance) {
			t.Errorf("pair %s/%s covariance differs between runs: %v vs %v",
				first[i].A, first[i].B, first[i].Covariance, second[i].Covariance)
		}
	}
}

func TestBrownWithMeasuredCovarianceIsMoreConservativeThanFisher(t *testing.T) {
	// The point of the correction, stated as a test. Positively dependent detectors
	// must yield a LARGER combined p-value under Brown than under Fisher, because
	// Fisher counts dependent evidence more than once.
	c := calibration.NewCorrelations(2)
	for i := range 500 {
		x := 2 + float64(i%13)*0.5
		c.Observe([]string{"a", "b"}, []float64{x, x}) // perfectly dependent
	}
	m := c.Freeze()

	ps := []float64{1e-6, 1e-6}
	_, _, fisherTail, err := calibration.Fisher(ps)
	if err != nil {
		t.Fatal(err)
	}
	_, cScale, f, brownTail, err := calibration.Brown(ps, m.Matrix([]string{"a", "b"}))
	if err != nil {
		t.Fatal(err)
	}
	if !(brownTail > fisherTail) {
		t.Errorf("Brown tail %v is not above Fisher tail %v; the correction did not "+
			"discount the dependence", brownTail, fisherTail)
	}
	if cScale <= 1 {
		t.Errorf("scale c = %v, want above 1 for positively dependent detectors", cScale)
	}
	if f >= 4 {
		t.Errorf("effective degrees of freedom f = %v, want below 2J = 4", f)
	}
}

func TestZeroCovarianceReducesToFisher(t *testing.T) {
	// §10.2: the correction must degrade to Fisher rather than to failure when no
	// dependence was measured.
	c := calibration.NewCorrelations(2)
	m := c.Freeze() // nothing observed at all
	ps := []float64{0.01, 0.2, 0.5}
	_, _, fisherTail, err := calibration.Fisher(ps)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, brownTail, err := calibration.Brown(ps, m.Matrix([]string{"a", "b", "c"}))
	if err != nil {
		t.Fatal(err)
	}
	if math.Float64bits(brownTail) != math.Float64bits(fisherTail) {
		t.Errorf("with no measured dependence Brown gave %v and Fisher %v; the two "+
			"must agree exactly", brownTail, fisherTail)
	}
}
