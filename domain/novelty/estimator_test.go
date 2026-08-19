package novelty_test

import (
	"math"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/novelty"
)

// ---------------------------------------------------------------------------
// The hand-computed fixture for equations (4) and (5).
//
// Five values, α = 1, half-life T½ = 1 day, scored at day 4. Observation times are
// whole days, so every decay factor is an exact power of two and every decayed count
// is an exact dyadic rational, representable in float64 without rounding. The
// quotients are not, so the expectations below are the exact rationals and the
// comparisons carry a tolerance; the inputs, however, are exact, so a discrepancy
// cannot be blamed on the fixture.
//
// Derivation, applying the lazy rule of §6.2, n ← n·2^(−Δt/T½) + 1 stored against the
// row's own timestamp:
//
//	value  observed at (days)  stored count @ last_seen   decayed to day 4
//	  A         0, 1, 2              7/4 @ 2                7/16 = 0.4375
//	  B         3                      1 @ 3                 1/2 = 0.5
//	  C         0                      1 @ 0                1/16 = 0.0625
//	  D         2, 2                    2 @ 2                 1/2 = 0.5
//	  E         1                      1 @ 1                 1/8 = 0.125
//
//	N = 13/8 = 1.625,  K = 5,  α = 1,  N + α(K+1) = 61/8 = 7.625
//
//	p̂(∅) = 8/61     p̂(A) = 23/122   p̂(B) = 12/61
//	p̂(C) = 17/122   p̂(D) = 12/61    p̂(E) = 9/61
//
// Those six masses sum to exactly 1, which is the check that the reserved category is
// carrying the right share.
// ---------------------------------------------------------------------------

const (
	day       = event.Day
	halfLife  = novelty.HalfLife(event.Day)
	fixAlpha  = 1.0
	scoreTime = 4 * day
)

// fixtureHistory returns the decayed counts at day 4, exactly as tabulated above.
func fixtureHistory() []novelty.ValueCount {
	return []novelty.ValueCount{
		{Value: "A", Count: 0.4375},
		{Value: "B", Count: 0.5},
		{Value: "C", Count: 0.0625},
		{Value: "D", Count: 0.5},
		{Value: "E", Count: 0.125},
	}
}

func closeTo(t *testing.T, name string, got, want float64) {
	t.Helper()
	const tol = 1e-12
	if math.Abs(got-want) > tol {
		t.Errorf("%s = %.18f, want %.18f (difference %.3e)", name, got, want, got-want)
	}
}

// TestEstimatorAgainstHandComputedFixture checks equations (4) and (5) against the
// tabulated derivation above.
func TestEstimatorAgainstHandComputedFixture(t *testing.T) {
	est := novelty.Estimator{Alpha: fixAlpha}
	history := fixtureHistory()

	t.Run("equation (4): the predictive masses", func(t *testing.T) {
		for _, tc := range []struct {
			value string
			want  float64
		}{
			{"A", 23.0 / 122.0},
			{"B", 12.0 / 61.0},
			{"C", 17.0 / 122.0},
			{"D", 12.0 / 61.0},
			{"E", 9.0 / 61.0},
		} {
			got := est.Estimate(history, tc.value)
			closeTo(t, "p_hat("+tc.value+")", got.PHatObserved, tc.want)
			closeTo(t, "p_hat(nil)", got.PHatUnseen, 8.0/61.0)
			closeTo(t, "N", got.Total, 1.625)
			if got.Distinct != 5 {
				t.Errorf("K = %d, want 5", got.Distinct)
			}
			if got.IsUnseen {
				t.Errorf("%q is in the history and must not report as unseen", tc.value)
			}
		}
	})

	t.Run("the masses sum to one", func(t *testing.T) {
		// The reserved category plus the five observed values carry unit mass. This is
		// what makes (5) a tail mass of a genuine distribution rather than of an
		// unnormalised score.
		sum := est.Estimate(history, "A").PHatUnseen
		for _, vc := range history {
			sum += est.Estimate(history, vc.Value).PHatObserved
		}
		closeTo(t, "total mass", sum, 1.0)
	})

	t.Run("equation (5): the tail masses", func(t *testing.T) {
		for _, tc := range []struct {
			value string
			want  float64
			why   string
		}{
			{"A", 37.0 / 61.0, "nil + C + E + A"},
			{"B", 1.0, "every value is at most as probable as the joint mode"},
			{"C", 33.0 / 122.0, "nil + C"},
			{"D", 1.0, "D ties B as the joint mode"},
			{"E", 51.0 / 122.0, "nil + C + E"},
		} {
			got := est.Estimate(history, tc.value)
			closeTo(t, "P("+tc.value+")", got.TailMass, tc.want)
			if got.TailMass > 1 {
				t.Errorf("P(%s) = %v exceeds one; equation (18) would take a negative logarithm",
					tc.value, got.TailMass)
			}
		}
	})

	t.Run("an unseen value reduces to the reserved mass", func(t *testing.T) {
		// §6.1: "For an unseen value it reduces to p̂(∅)."
		got := est.Estimate(history, "Z")
		if !got.IsUnseen {
			t.Fatal("a value absent from the history must report as unseen")
		}
		closeTo(t, "P(unseen)", got.TailMass, 8.0/61.0)
		closeTo(t, "p_hat(unseen)", got.PHatObserved, got.PHatUnseen)
		if got.NObserved != 0 {
			t.Errorf("n_v = %v for an unseen value, want 0", got.NObserved)
		}
	})
}

// TestColdStartReturnsExactlyOne covers §6.2. With no history N = 0 and K = 0, so (4)
// places unit mass on ∅ and (5) returns P = 1. The assertion is on exact equality, not
// a tolerance: α/α is 1 in floating point without rounding, and the paper is explicit
// that this is the correct verdict rather than a special case, so it should fall out of
// the general path rather than be branched to.
func TestColdStartReturnsExactlyOne(t *testing.T) {
	for _, alpha := range []float64{0.1, 0.5, 1.0, 2.0, 7.5} {
		est := novelty.Estimator{Alpha: alpha}
		got := est.Estimate(nil, "first-ever-value")

		if got.TailMass != 1.0 {
			t.Errorf("alpha = %v: P = %v (bits %#x), want exactly 1",
				alpha, got.TailMass, math.Float64bits(got.TailMass))
		}
		if got.PHatUnseen != 1.0 {
			t.Errorf("alpha = %v: p_hat(nil) = %v, want exactly 1", alpha, got.PHatUnseen)
		}
		if got.Total != 0 || got.Distinct != 0 {
			t.Errorf("alpha = %v: N = %v, K = %d, want 0 and 0", alpha, got.Total, got.Distinct)
		}
		if !got.IsUnseen {
			t.Errorf("alpha = %v: the first observation must report as unseen", alpha)
		}
	}
}

// TestNoveltyIsMoreSurprisingWithRicherHistory covers the monotonicity property of
// §6.2: at fixed K, P = α/(N + α(K+1)) is strictly decreasing in N, because novelty is
// informative in proportion to the evidence it contradicts.
func TestNoveltyIsMoreSurprisingWithRicherHistory(t *testing.T) {
	est := novelty.Estimator{Alpha: 1}

	previous := math.Inf(1)
	for _, total := range []float64{0, 1, 1.625, 10, 100, 1000} {
		got := est.NoveltyPValue(total, 5)
		if got >= previous {
			t.Errorf("N = %v: P = %v did not decrease from %v", total, got, previous)
		}
		previous = got
	}

	// The property is stated at fixed K, and the paper notes K grows with history too.
	// Confirm the qualifier is real: letting K grow alongside N does not reverse the
	// ordering for an attribute whose value set settles.
	settled := est.NoveltyPValue(1000, 8)
	thin := est.NoveltyPValue(10, 5)
	if settled >= thin {
		t.Errorf("richer history with a settled value set should still be more surprising: "+
			"%v vs %v", settled, thin)
	}
}

// TestEstimateIsInvariantToInputOrder is the (5) half of trap 2. Floating-point
// addition is not associative, and the history may arrive from a map or from a query
// without a total order, so the estimator sorts before accumulating. Shuffling the
// input must not move a single bit.
func TestEstimateIsInvariantToInputOrder(t *testing.T) {
	est := novelty.Estimator{Alpha: 1}
	base := est.Estimate(fixtureHistory(), "E")

	rng := rand.New(rand.NewPCG(1, 2)) // test-only; the scoring path holds no randomness
	for range 256 {
		shuffled := fixtureHistory()
		rng.Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})
		got := est.Estimate(shuffled, "E")
		if math.Float64bits(got.TailMass) != math.Float64bits(base.TailMass) {
			t.Fatalf("tail mass changed with input order: %#x vs %#x",
				math.Float64bits(got.TailMass), math.Float64bits(base.TailMass))
		}
	}
}

// TestEstimateDoesNotMutateCallerSlice: the estimator sorts internally, and a caller
// reusing its history slice must not find it reordered.
func TestEstimateDoesNotMutateCallerSlice(t *testing.T) {
	est := novelty.Estimator{Alpha: 1}
	history := []novelty.ValueCount{
		{Value: "E", Count: 0.125},
		{Value: "A", Count: 0.4375},
		{Value: "C", Count: 0.0625},
	}
	before := slices.Clone(history)
	est.Estimate(history, "A")
	for i := range history {
		if history[i] != before[i] {
			t.Fatalf("caller slice was mutated at %d: %v vs %v", i, history[i], before[i])
		}
	}
}

// TestZeroCountsAreNotDistinct: K counts values with n_v > 0, so a row decayed to zero
// or pruned must not inflate K, which sits in the denominator of (4).
func TestZeroCountsAreNotDistinct(t *testing.T) {
	est := novelty.Estimator{Alpha: 1}
	got := est.Estimate([]novelty.ValueCount{
		{Value: "A", Count: 1},
		{Value: "B", Count: 0},
		{Value: "C", Count: 0},
	}, "A")

	if got.Distinct != 1 {
		t.Errorf("K = %d, want 1", got.Distinct)
	}
	closeTo(t, "N", got.Total, 1)
	// N + α(K+1) = 1 + 2 = 3, p̂(A) = 2/3, p̂(∅) = 1/3, and both are ≤ p̂(A) so P = 1.
	closeTo(t, "p_hat(A)", got.PHatObserved, 2.0/3.0)
	closeTo(t, "P(A)", got.TailMass, 1.0)
}

// ---------------------------------------------------------------------------
// Decay, §6.2
// ---------------------------------------------------------------------------

func TestDecayFactor(t *testing.T) {
	for _, tc := range []struct {
		name     string
		from, to event.Timestamp
		want     float64
	}{
		{"no elapsed time", 0, 0, 1},
		{"one half-life", 0, day, 0.5},
		{"two half-lives", 0, 2 * day, 0.25},
		{"four half-lives", 0, 4 * day, 0.0625},
		{"half a half-life", 0, day / 2, math.Sqrt2 / 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := novelty.DecayFactor(tc.from, tc.to, halfLife)
			closeTo(t, "delta", got, tc.want)
		})
	}
}

// TestDecayAbsorbsOutOfOrderArrival: a corpus replayed by timestamp still carries ties
// and, at LANL's one-second resolution, events that arrive at or before a row's own
// last-observed time. A negative elapsed interval must not amplify a count.
func TestDecayAbsorbsOutOfOrderArrival(t *testing.T) {
	if got := novelty.DecayFactor(4*day, 2*day, halfLife); got != 1 {
		t.Errorf("a backwards interval gave delta = %v, want 1", got)
	}
	if got := novelty.Decay(3, 4*day, 4*day, halfLife); got != 3 {
		t.Errorf("a zero interval changed the count: %v, want 3", got)
	}
	if got := novelty.Decay(3, 4*day, 2*day, halfLife); got != 3 {
		t.Errorf("a backwards interval changed the count: %v, want 3", got)
	}
}

// TestLazyAccumulationReproducesTheFixture walks the observation schedule in the table
// above and requires the stored counts and their decayed values to match, which is
// what ties the estimator fixture to the §6.2 update rule rather than to a table of
// numbers someone typed in.
func TestLazyAccumulationReproducesTheFixture(t *testing.T) {
	for _, tc := range []struct {
		value       string
		observedAt  []event.Timestamp
		wantStored  float64
		wantDecayed float64
	}{
		{"A", []event.Timestamp{0, day, 2 * day}, 1.75, 0.4375},
		{"B", []event.Timestamp{3 * day}, 1, 0.5},
		{"C", []event.Timestamp{0}, 1, 0.0625},
		{"D", []event.Timestamp{2 * day, 2 * day}, 2, 0.5},
		{"E", []event.Timestamp{day}, 1, 0.125},
	} {
		t.Run(tc.value, func(t *testing.T) {
			var (
				count    float64
				lastSeen event.Timestamp
			)
			for i, at := range tc.observedAt {
				if i == 0 {
					count, lastSeen = 1, at
					continue
				}
				count = novelty.Accumulate(count, lastSeen, at, halfLife)
				lastSeen = at
			}
			if count != tc.wantStored {
				t.Errorf("stored count = %v, want %v", count, tc.wantStored)
			}
			if got := novelty.Decay(count, lastSeen, scoreTime, halfLife); got != tc.wantDecayed {
				t.Errorf("decayed to day 4 = %v, want %v", got, tc.wantDecayed)
			}
		})
	}
}

// TestTotalEqualsSumOfParts is the identity that lets N be carried as one decayed
// scalar instead of re-summed from every value row on each score. Decay is linear, so
// maintaining N by the same lazy rule keeps it exactly equal to Σ n_v.
func TestTotalEqualsSumOfParts(t *testing.T) {
	type row struct {
		count    float64
		lastSeen event.Timestamp
	}
	rows := map[string]*row{}
	var (
		total         float64
		totalLastSeen event.Timestamp
	)

	schedule := []struct {
		value string
		at    event.Timestamp
	}{
		{"A", 0}, {"C", 0}, {"E", day}, {"A", day}, {"A", 2 * day},
		{"D", 2 * day}, {"D", 2 * day}, {"B", 3 * day},
	}
	for _, s := range schedule {
		r, ok := rows[s.value]
		if !ok {
			r = &row{}
			rows[s.value] = r
		}
		r.count = novelty.Accumulate(r.count, r.lastSeen, s.at, halfLife)
		r.lastSeen = s.at

		total = novelty.Accumulate(total, totalLastSeen, s.at, halfLife)
		totalLastSeen = s.at
	}

	// Bring every row and the total to the same instant and compare.
	var sum float64
	for _, v := range slices.Sorted(mapKeys(rows)) {
		sum += novelty.Decay(rows[v].count, rows[v].lastSeen, scoreTime, halfLife)
	}
	got := novelty.Decay(total, totalLastSeen, scoreTime, halfLife)

	closeTo(t, "N maintained lazily", got, sum)
	closeTo(t, "N", got, 1.625)
}

func mapKeys[V any](m map[string]V) func(yield func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

// TestEffectiveSampleSize covers §7.5: the geometric weights sum to 1/(1−δ), which is
// the interpretable statement of how much history informs a verdict and, per §13.2,
// bounds the drift the framework can perceive.
func TestEffectiveSampleSize(t *testing.T) {
	// At a step of one half-life, δ = 1/2 and the effective sample size is 2.
	closeTo(t, "ESS at one half-life", novelty.EffectiveSampleSize(day, halfLife), 2)

	// A finer step retains more history.
	fine := novelty.EffectiveSampleSize(day/8, halfLife)
	if fine <= 2 {
		t.Errorf("a finer step should retain more history, got %v", fine)
	}

	// A non-positive half-life means no decay, hence unbounded history.
	if got := novelty.EffectiveSampleSize(day, 0); !math.IsInf(got, 1) {
		t.Errorf("no decay should give an infinite effective sample size, got %v", got)
	}
}

// TestSuperUniformityOnADiscreteNull checks the direction the paper predicts rather
// than a magnitude. §10.2's remark states that the discrete statistics of §6 and §8 are
// super-uniform, so Pr(P ≤ t) ≤ t, and the combined test is therefore conservative;
// E3 measures how conservative. A test asserting exactness here would be asserting the
// opposite of what the paper claims.
//
// The null is deliberately non-degenerate. With equal counts every value ties as the
// mode, every draw scores P = 1, and the assertion passes vacuously; the counts here
// are dyadic and unequal, so the p-values take several distinct levels and the
// assertion has content at every threshold. Draws are taken from the model's own
// predictive, reserved category included, which is exactly the distribution the tail
// mass in (5) is computed against.
func TestSuperUniformityOnADiscreteNull(t *testing.T) {
	est := novelty.Estimator{Alpha: 1}

	// Unequal dyadic counts: N = 63.5, K = 8. Exact in float64.
	counts := []float64{32, 16, 8, 4, 2, 1, 0.25, 0.25}
	history := make([]novelty.ValueCount, 0, len(counts))
	for i, c := range counts {
		history = append(history, novelty.ValueCount{Value: string(rune('a' + i)), Count: c})
	}

	// The predictive masses, from the estimator itself: p̂(v) for each value and p̂(∅)
	// for the reserved category, in a fixed order for the inverse-CDF draw below.
	type mass struct {
		value string
		p     float64
	}
	masses := make([]mass, 0, len(history)+1)
	for _, vc := range history {
		masses = append(masses, mass{vc.Value, est.Estimate(history, vc.Value).PHatObserved})
	}
	masses = append(masses, mass{"", est.Estimate(history, "a").PHatUnseen}) // ∅

	rng := rand.New(rand.NewPCG(42, 7)) // test-only; the scoring path holds no randomness
	const draws = 40000
	pvalues := make([]float64, 0, draws)
	fresh := 0
	for range draws {
		u := rng.Float64()
		var observed string
		acc := 0.0
		for _, m := range masses {
			acc += m.p
			if u < acc {
				observed = m.value
				break
			}
		}
		if observed == "" {
			// The reserved category: a genuinely fresh value each time.
			fresh++
			observed = "fresh-" + string(rune('0'+fresh%10)) + "-" + string(rune('a'+(fresh/10)%26))
		}
		pvalues = append(pvalues, est.Estimate(history, observed).TailMass)
	}

	sawBelowOne := false
	for _, p := range pvalues {
		if p < 1 {
			sawBelowOne = true
			break
		}
	}
	if !sawBelowOne {
		t.Fatal("every draw scored P = 1; the null is degenerate and the test is vacuous")
	}

	for _, threshold := range []float64{0.01, 0.05, 0.1, 0.25, 0.5} {
		hits := 0
		for _, p := range pvalues {
			if p <= threshold {
				hits++
			}
		}
		realised := float64(hits) / float64(len(pvalues))
		// Monte Carlo slack: four binomial standard deviations at the nominal level.
		slack := 4 * math.Sqrt(threshold*(1-threshold)/float64(draws))
		if realised > threshold+slack {
			t.Errorf("Pr(P <= %v) = %v exceeds the nominal level beyond sampling noise; "+
				"the discrete null must be super-uniform, not anti-conservative",
				threshold, realised)
		}
		t.Logf("t = %-5v realised Pr(P <= t) = %.4f  (conservatism %+.4f)",
			threshold, realised, threshold-realised)
	}
}
