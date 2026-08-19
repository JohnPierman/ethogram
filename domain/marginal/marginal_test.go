package marginal_test

import (
	"math"
	"math/rand/v2"
	"testing"

	"github.com/JohnPierman/ethogram/domain/marginal"
	"github.com/JohnPierman/ethogram/domain/novelty"
)

// ---------------------------------------------------------------------------
// The hand-computed fixture for equations (4) and (5) at population scope.
//
// Population history A:4, B:2, C:1, D:1 — total N = 8, K = 4, α = 1, so the
// denominator of (4) is N + α(K+1) = 8 + 1·5 = 13 and
//
//	p̂(A) = 5/13   p̂(B) = 3/13   p̂(C) = p̂(D) = 2/13   p̂(∅) = 1/13
//
// which sum to (5+3+2+2+1)/13 = 13/13 = 1 exactly. The (5) tail masses:
//
//	P(A) = 1                       A is the mode: every mass qualifies
//	P(B) = (1+2+2+3)/13 = 8/13     ∅ + C + D + B
//	P(C) = (1+2+2)/13   = 5/13     ∅ + C + D; D ties C, and ties are included
//	P(unseen) = 1/13               reduces to p̂(∅), per §6.1
// ---------------------------------------------------------------------------

func populationFixture() []marginal.ValueCount {
	return []marginal.ValueCount{
		{Value: "A", Count: 4},
		{Value: "B", Count: 2},
		{Value: "C", Count: 1},
		{Value: "D", Count: 1},
	}
}

func closeTo(t *testing.T, name string, got, want float64) {
	t.Helper()
	const tol = 1e-12
	if math.Abs(got-want) > tol {
		t.Errorf("%s = %.18f, want %.18f (difference %.3e)", name, got, want, got-want)
	}
}

// TestEstimateCategoricalAgainstHandComputedFixture checks equations (4) and (5)
// against the tabulated derivation above. The numbers are Detector I's arithmetic on
// population-scope rows, which is the whole design (§9): scope differs, estimator
// does not.
func TestEstimateCategoricalAgainstHandComputedFixture(t *testing.T) {
	est := marginal.Estimator{Alpha: 1}
	history := populationFixture()

	t.Run("equation (4): the predictive masses", func(t *testing.T) {
		for _, tc := range []struct {
			value string
			want  float64
		}{
			{"A", 5.0 / 13.0},
			{"B", 3.0 / 13.0},
			{"C", 2.0 / 13.0},
			{"D", 2.0 / 13.0},
		} {
			got := est.EstimateCategorical(history, tc.value)
			closeTo(t, "p_hat("+tc.value+")", got.PHatObserved, tc.want)
			closeTo(t, "p_hat(nil)", got.PHatUnseen, 1.0/13.0)
			closeTo(t, "N", got.Total, 8)
			if got.Distinct != 4 {
				t.Errorf("K = %d, want 4", got.Distinct)
			}
			if got.Abstained {
				t.Errorf("%q abstained with no floor set", tc.value)
			}
		}
	})

	t.Run("the masses sum to one", func(t *testing.T) {
		sum := est.EstimateCategorical(history, "A").PHatUnseen
		for _, vc := range history {
			sum += est.EstimateCategorical(history, vc.Value).PHatObserved
		}
		closeTo(t, "total mass", sum, 1.0)
	})

	t.Run("equation (5): the tail masses", func(t *testing.T) {
		for _, tc := range []struct {
			value string
			want  float64
			why   string
		}{
			{"A", 1.0, "A is the mode: every mass qualifies"},
			{"B", 8.0 / 13.0, "nil + C + D + B"},
			{"C", 5.0 / 13.0, "nil + C + D"},
			{"D", 5.0 / 13.0, "nil + C + D"},
		} {
			got := est.EstimateCategorical(history, tc.value)
			closeTo(t, "P("+tc.value+")", got.TailMass, tc.want)
		}
	})

	t.Run("an unseen value reduces to the reserved mass", func(t *testing.T) {
		got := est.EstimateCategorical(history, "Z")
		if !got.IsUnseen {
			t.Fatal("a value absent from the marginal must report as unseen")
		}
		closeTo(t, "P(unseen)", got.TailMass, 1.0/13.0)
	})

	t.Run("the estimator is Detector I's, bit for bit", func(t *testing.T) {
		// §9: the two detectors differ in scope, not in estimator. Feed the same
		// counts to novelty's estimator directly and require bit identity.
		reference := novelty.Estimator{Alpha: 1}
		for _, value := range []string{"A", "B", "C", "D", "Z"} {
			ours := est.EstimateCategorical(history, value)
			theirs := reference.Estimate([]novelty.ValueCount{
				{Value: "A", Count: 4}, {Value: "B", Count: 2},
				{Value: "C", Count: 1}, {Value: "D", Count: 1},
			}, value)
			if math.Float64bits(ours.TailMass) != math.Float64bits(theirs.TailMass) {
				t.Errorf("P(%s) diverged from Detector I's arithmetic: %#x vs %#x", value,
					math.Float64bits(ours.TailMass), math.Float64bits(theirs.TailMass))
			}
		}
	})
}

// TestEstimateCategoricalAbstainsBelowMinimumObservations covers the §9 floor: a
// population marginal totalling 3 is not evidence of what the population does, and the
// estimate must say so rather than score.
func TestEstimateCategoricalAbstainsBelowMinimumObservations(t *testing.T) {
	est := marginal.Estimator{Alpha: 1, MinObservations: 50}

	thin := est.EstimateCategorical([]marginal.ValueCount{
		{Value: "A", Count: 2},
		{Value: "B", Count: 1},
	}, "A")
	if !thin.Abstained {
		t.Fatal("a marginal of total 3 against a floor of 50 must abstain")
	}
	if thin.AbstainReason == "" {
		t.Error("an abstention must carry a reason")
	}
	closeTo(t, "N", thin.Total, 3)

	// Enough evidence, and the same estimator scores.
	rich := est.EstimateCategorical([]marginal.ValueCount{
		{Value: "A", Count: 40},
		{Value: "B", Count: 20},
	}, "A")
	if rich.Abstained {
		t.Errorf("a marginal of total 60 against a floor of 50 must score, got abstention %q",
			rich.AbstainReason)
	}
}

// ---------------------------------------------------------------------------
// The quantile sketch
// ---------------------------------------------------------------------------

func sketchOf(n, maxCentroids int) *marginal.Sketch {
	s := marginal.NewSketch(maxCentroids)
	for i := 1; i <= n; i++ {
		s.Add(float64(i), 1)
	}
	return s
}

// TestSketchQuantilesOnUniformInput: 1..1000 in order. Uniform input has a linear
// CDF, and centroid means sit at the centre of their spans, so the piecewise-linear
// interpolation should land near the exact answers with room to spare.
func TestSketchQuantilesOnUniformInput(t *testing.T) {
	s := sketchOf(1000, 128)

	if got := s.Weight(); got != 1000 {
		t.Errorf("Weight() = %v, want exactly 1000", got)
	}
	if got, want := s.Quantile(0.5), 500.5; math.Abs(got-want) > 0.02*want {
		t.Errorf("Quantile(0.5) = %v, want within 2%% of %v", got, want)
	}
	if got := s.CDF(500.5); math.Abs(got-0.5) > 0.02 {
		t.Errorf("CDF(500.5) = %v, want within 0.02 of 0.5", got)
	}
}

// TestSketchStaysBounded: the merge rule must hold the centroid count at the bound
// however many observations arrive, which is what makes the state finite (§13.3).
func TestSketchStaysBounded(t *testing.T) {
	const bound = 64
	s := marginal.NewSketch(bound)
	for i := range 10000 {
		s.Add(float64(i%977)*1.5, 1)
	}
	if got := s.Centroids(); got > bound {
		t.Errorf("Centroids() = %d after 10000 adds, want <= %d", got, bound)
	}
	if got := s.Weight(); got != 10000 {
		t.Errorf("Weight() = %v, want exactly 10000", got)
	}
}

// TestSketchIsDeterministic: two sketches fed the identical sequence must agree to
// the bit (R4). The merge rule holds no randomness and no wall clock, so this is a
// property of the code's shape; the assertion is on the float bits, not a tolerance.
func TestSketchIsDeterministic(t *testing.T) {
	feed := func() *marginal.Sketch {
		s := marginal.NewSketch(96)
		rng := rand.New(rand.NewPCG(11, 13)) // test-only; identical stream for both
		for range 5000 {
			s.Add(rng.NormFloat64()*100+500, 1)
		}
		return s
	}
	a, b := feed(), feed()
	for _, q := range []float64{0.1, 0.5, 0.9} {
		if math.Float64bits(a.Quantile(q)) != math.Float64bits(b.Quantile(q)) {
			t.Errorf("Quantile(%v) differs between identically fed sketches: %#x vs %#x",
				q, math.Float64bits(a.Quantile(q)), math.Float64bits(b.Quantile(q)))
		}
	}
}

// TestEstimateNumericTwoSidedTail covers the §9 numeric tail: central values score
// near one, both extremes score small and roughly equal (extremity has no preferred
// direction), and the floor keeps P inside (0, 1] everywhere, since equation (18)
// takes its logarithm.
func TestEstimateNumericTwoSidedTail(t *testing.T) {
	est := marginal.Estimator{MinObservations: 50}
	s := sketchOf(1000, 128)

	middle := est.EstimateNumeric(s, 500)
	if middle.Abstained || middle.TailMass < 0.9 {
		t.Errorf("a central value scored P = %v (abstained %v), want near 1",
			middle.TailMass, middle.Abstained)
	}

	low := est.EstimateNumeric(s, 1)
	high := est.EstimateNumeric(s, 1000)
	if low.TailMass > 0.05 || high.TailMass > 0.05 {
		t.Errorf("extreme values scored P = %v and %v, want small", low.TailMass, high.TailMass)
	}
	if math.Abs(low.TailMass-high.TailMass) > 0.01 {
		t.Errorf("the tail is two-sided and the input symmetric: P(1) = %v, P(1000) = %v",
			low.TailMass, high.TailMass)
	}

	for _, x := range []float64{-1e12, 0, 1, 250, 500.5, 750, 1000, 1e12} {
		got := est.EstimateNumeric(s, x)
		if got.TailMass <= 0 || got.TailMass > 1 {
			t.Errorf("P(%v) = %v escaped (0, 1]", x, got.TailMass)
		}
	}

	// Below the floor: a nil sketch (cold start) and a thin one both abstain.
	for name, sk := range map[string]*marginal.Sketch{"nil": nil, "thin": sketchOf(10, 128)} {
		got := est.EstimateNumeric(sk, 5)
		if !got.Abstained || got.AbstainReason == "" {
			t.Errorf("%s sketch: want an abstention with a reason, got P = %v", name, got.TailMass)
		}
	}
}

// TestEstimateCategoricalIsInvariantToInputOrder is trap 2 at population scope.
// Floating-point addition is not associative and the rows may arrive from a query
// without a total order, so the estimator sorts before accumulating. Shuffling the
// input must not move a single bit.
func TestEstimateCategoricalIsInvariantToInputOrder(t *testing.T) {
	est := marginal.Estimator{Alpha: 1}
	base := est.EstimateCategorical(populationFixture(), "C")

	rng := rand.New(rand.NewPCG(1, 2)) // test-only; the scoring path holds no randomness
	for range 256 {
		shuffled := populationFixture()
		rng.Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})
		got := est.EstimateCategorical(shuffled, "C")
		if math.Float64bits(got.TailMass) != math.Float64bits(base.TailMass) {
			t.Fatalf("tail mass changed with input order: %#x vs %#x",
				math.Float64bits(got.TailMass), math.Float64bits(base.TailMass))
		}
	}
}
