package drift_test

import (
	"math"
	"sort"
	"testing"

	"github.com/JohnPierman/ethogram/domain/drift"
	"github.com/JohnPierman/ethogram/domain/volume"
)

// The two synthetic streams. Deterministic rather than sampled: the property under test is
// which statistic separates a sustained shift from ordinary variation, and a fixed sequence
// with realistic dispersion tests that without making the result depend on a seed.
//
// The stationary cycle has mean 10 and a spread an ordinary account would show. The drifted
// cycle is the same shape scaled by 1.3 -- the mechanism the planted corpus calls low-and-slow,
// "familiar values only, a modest sustained increase" -- so the dispersion is preserved and the
// only difference between the streams is the level.
var (
	stationaryCycle = []float64{8, 12, 10, 9, 11, 10, 13, 7, 10, 10}
	driftedCycle    = []float64{10, 16, 13, 12, 14, 13, 17, 9, 13, 13}
)

const (
	burnInPeriods = 40
	scoredPeriods = 40
)

// stream returns burn-in periods drawn from the stationary cycle followed by scored periods
// from the given cycle, so both arms are fitted on identical history and differ only in what
// they are then asked to score.
func stream(scored []float64) []float64 {
	out := make([]float64, 0, burnInPeriods+scoredPeriods)
	for i := 0; i < burnInPeriods; i++ {
		out = append(out, stationaryCycle[i%len(stationaryCycle)])
	}
	for i := 0; i < scoredPeriods; i++ {
		out = append(out, scored[i%len(scored)])
	}
	return out
}

// replay scores a stream with both statistics and returns the p-values each assigned to the
// scored periods. Every quantity is formed from state that precedes the period being scored,
// so neither arm sees the count it is scoring.
func replay(t *testing.T, counts []float64) (driftP, volumeP []float64) {
	t.Helper()

	var posterior volume.GammaPosterior
	var null drift.Null
	sum := 0.0
	cusum := 0.0

	for i, k := range counts {
		scoring := i >= burnInPeriods

		// Both arms read the posterior as it stands before this period.
		baseline := posterior.Mean()
		if baseline > 0 {
			reference, err := drift.Reference(baseline, drift.DefaultShift)
			if err != nil {
				t.Fatalf("Reference(%v): %v", baseline, err)
			}
			cusum = drift.Next(cusum, k, reference)
			if z, ok := null.Standardise(cusum); ok && scoring {
				driftP = append(driftP, drift.UpperTail(z))
			}
			if scoring && posterior.A > 0 {
				// The volume arm's own null: this period's count against the entity's
				// over-dispersed predictive, at one whole period of exposure.
				phi := volume.Dispersion(sum, float64(i))
				volumeP = append(volumeP,
					volume.UpperTailDispersed(baseline, phi, int(k)))
			}
			null.Observe(cusum, 1)
		}
		posterior.Observe(k, 1)
		sum += k
	}
	return driftP, volumeP
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return math.NaN()
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	return s[len(s)/2]
}

// TestDriftSeparatesASustainedShiftWhereTheVolumePredictiveDoesNot. The measurement A1 turns
// on, as a test rather than an argument.
//
// Both arms are fitted on the same forty stationary periods and then score forty more. The
// cumulative sum must find the shifted stream far more surprising than the stationary one;
// the over-dispersed predictive of equation (11) must not, because a shift that is modest in
// every single period is inside a widened marginal null in every single period. That is the
// inverted response the paper records on the planted corpus, reproduced here on a stream
// whose construction is known.
func TestDriftSeparatesASustainedShiftWhereTheVolumePredictiveDoesNot(t *testing.T) {
	driftFlat, volumeFlat := replay(t, stream(stationaryCycle))
	driftShift, volumeShift := replay(t, stream(driftedCycle))

	if len(driftFlat) == 0 || len(driftShift) == 0 {
		t.Fatal("the cumulative sum produced no scored p-values")
	}
	if len(volumeFlat) == 0 || len(volumeShift) == 0 {
		t.Fatal("the volume predictive produced no scored p-values")
	}

	dFlat, dShift := median(driftFlat), median(driftShift)
	vFlat, vShift := median(volumeFlat), median(volumeShift)
	t.Logf("median p — drift: stationary %.4g, shifted %.4g (ratio %.1fx)",
		dFlat, dShift, dFlat/dShift)
	t.Logf("median p — volume: stationary %.4g, shifted %.4g (ratio %.1fx)",
		vFlat, vShift, vFlat/vShift)

	if dShift >= dFlat {
		t.Errorf("the cumulative sum scores the shifted stream %.4g and the stationary one"+
			" %.4g; the shifted stream must be the more surprising", dShift, dFlat)
	}
	// The separation must be worth having, not merely signed correctly.
	if dFlat/dShift < 10 {
		t.Errorf("the cumulative sum separates the streams by only %.1fx, want at least 10x",
			dFlat/dShift)
	}
	// And it must be a real improvement on the arm already in the framework.
	if dFlat/dShift <= vFlat/vShift {
		t.Errorf("the cumulative sum separates by %.1fx and the volume predictive by %.1fx;"+
			" the new statistic must do better on the mechanism it is for",
			dFlat/dShift, vFlat/vShift)
	}
}

// TestReferenceLiesBetweenTheTwoRates. Page's reference value is the count at which the
// in-control and out-of-control Poisson likelihoods are equal, so it must lie strictly between
// them. A reference outside that interval makes the statistic drift in one direction whatever
// the data does.
func TestReferenceLiesBetweenTheTwoRates(t *testing.T) {
	for _, baseline := range []float64{0.5, 1, 10, 1000} {
		for _, shift := range []float64{1.05, 1.3, 2, 10} {
			k, err := drift.Reference(baseline, shift)
			if err != nil {
				t.Fatalf("Reference(%v, %v): %v", baseline, shift, err)
			}
			if k <= baseline || k >= baseline*shift {
				t.Errorf("Reference(%v, %v) = %v, want strictly between %v and %v",
					baseline, shift, k, baseline, baseline*shift)
			}
		}
	}
}

// TestReferenceRejectsAShiftThatIsNotAnIncrease. The arm is one-sided by construction, so a
// factor at or below one describes no alternative and a non-finite one describes nothing.
func TestReferenceRejectsAShiftThatIsNotAnIncrease(t *testing.T) {
	for name, tc := range map[string]struct{ baseline, shift float64 }{
		"shift of one":       {10, 1},
		"shift below one":    {10, 0.8},
		"negative shift":     {10, -2},
		"shift not a number": {10, math.NaN()},
		"infinite shift":     {10, math.Inf(1)},
		"zero baseline":      {0, 1.3},
		"negative baseline":  {-1, 1.3},
		"infinite baseline":  {math.Inf(1), 1.3},
	} {
		if _, err := drift.Reference(tc.baseline, tc.shift); err == nil {
			t.Errorf("%s: accepted, want an error", name)
		}
	}
}

// TestNextFloorsAtZeroSoQuietPeriodsDoNotBankCredit. The floor is what makes this a test for a
// present elevation rather than a random walk over the entity's whole history. Without it a
// long quiet stretch would accumulate negative drift that a later burst had to overcome, and
// the arm would answer "has this entity ever been busy" instead.
func TestNextFloorsAtZeroSoQuietPeriodsDoNotBankCredit(t *testing.T) {
	const reference = 11.0
	s := 0.0
	for i := 0; i < 50; i++ {
		s = drift.Next(s, 2, reference) // fifty very quiet periods
	}
	if s != 0 {
		t.Fatalf("after fifty quiet periods the sum is %v, want 0", s)
	}
	// One busy period must move it immediately, not have to climb out of a deficit.
	if got := drift.Next(s, 20, reference); got != 9 {
		t.Errorf("after a busy period the sum is %v, want 9", got)
	}
}

// TestSustainedExcessGrowsTheSumWithoutBound. The mechanism the package rests on: a shift
// accumulates linearly in the number of periods, which is what a marginal test of one period
// cannot see however well it is calibrated.
func TestSustainedExcessGrowsTheSumWithoutBound(t *testing.T) {
	const reference = 11.0
	s, previous := 0.0, 0.0
	for i := 0; i < 30; i++ {
		s = drift.Next(s, 13, reference)
		if i > 0 && s <= previous {
			t.Fatalf("period %d did not increase the sum: %v then %v", i, previous, s)
		}
		previous = s
	}
	if s < 30 {
		t.Errorf("thirty periods of a 2-count excess reached %v, want about 60", s)
	}
}

// TestNullAbstainsBelowItsMinimumWeight. R3: a detector without its inputs abstains rather
// than returning a neutral score. Standardising against a handful of observations would
// produce a p-value whose extremity reflected the sample size.
func TestNullAbstainsBelowItsMinimumWeight(t *testing.T) {
	var n drift.Null
	for i := 0; i < drift.MinWeight-1; i++ {
		n.Observe(float64(i), 1)
		if _, ok := n.Standardise(5); ok {
			t.Fatalf("standardised on %d observations, below the minimum of %d",
				i+1, drift.MinWeight)
		}
	}
	n.Observe(99, 1)
	if _, ok := n.Standardise(5); !ok {
		t.Errorf("did not standardise once the minimum weight was reached")
	}
}

// TestNullAbstainsWithoutSpread. A perfectly regular account produces the same sum every
// period. There is no spread to standardise against, and inventing one would turn a rounding
// difference into an extreme score.
func TestNullAbstainsWithoutSpread(t *testing.T) {
	var n drift.Null
	for i := 0; i < 4*drift.MinWeight; i++ {
		n.Observe(7, 1)
	}
	mean, sd, ok := n.Moments()
	if ok {
		t.Errorf("standardised against a null with no spread (mean %v, sd %v)", mean, sd)
	}
	if math.Abs(mean-7) > 1e-9 {
		t.Errorf("mean = %v, want 7", mean)
	}
}

// TestNullDiscountsOlderPeriods. The null must follow the entity's own changing behaviour
// rather than average over all of it, or an account whose workload changed a year ago is
// scored against a level it no longer has.
// The discount is the framework's own: a seven-day half-life read at daily periods, so
// 2^(-1/7). A heavier discount than [drift.MaxDiscount] cannot reach the minimum weight at
// all, which TestNullCannotReachItsMinimumWeightUnderTooHeavyADiscount covers.
func TestNullDiscountsOlderPeriods(t *testing.T) {
	const daily = 0.9057236643191192 // 2^(-1/7)

	var discounted, flat drift.Null
	for i := 0; i < 60; i++ {
		discounted.Observe(1, daily)
		flat.Observe(1, 1)
	}
	for i := 0; i < 20; i++ {
		discounted.Observe(50, daily)
		flat.Observe(50, 1)
	}
	dMean, _, dOK := discounted.Moments()
	fMean, _, fOK := flat.Moments()
	if !dOK || !fOK {
		t.Fatalf("expected both nulls to be usable, got %v and %v", dOK, fOK)
	}
	if dMean <= fMean {
		t.Errorf("the discounted mean %v is not above the undiscounted %v; recent periods"+
			" must weigh more", dMean, fMean)
	}
}

// TestNullCannotReachItsMinimumWeightUnderTooHeavyADiscount. A constraint that binds silently
// and so is asserted rather than trusted: discounted weight saturates at 1/(1-delta), so a
// half-life short enough to put that ceiling below the minimum weight makes the arm abstain on
// every entity for all time. A run that shortened the half-life would disable the detector and
// record only a column of abstentions.
func TestNullCannotReachItsMinimumWeightUnderTooHeavyADiscount(t *testing.T) {
	tooHeavy := drift.MaxDiscount - 0.05
	if drift.ReachesMinWeight(tooHeavy) {
		t.Errorf("ReachesMinWeight(%v) = true, want false", tooHeavy)
	}
	var n drift.Null
	for i := 0; i < 10_000; i++ {
		n.Observe(float64(i%7), tooHeavy)
	}
	if _, ok := n.Standardise(5); ok {
		t.Errorf("formed a null under a discount whose saturating weight is %.2f, below the"+
			" minimum of %d", 1/(1-tooHeavy), drift.MinWeight)
	}

	// The framework's own seven-day half-life at daily periods must clear the bound.
	const daily = 0.9057236643191192
	if !drift.ReachesMinWeight(daily) {
		t.Errorf("ReachesMinWeight(%v) = false; the framework's own discount must be usable",
			daily)
	}
	var usable drift.Null
	for i := 0; i < 10_000; i++ {
		usable.Observe(float64(i%7), daily)
	}
	if _, ok := usable.Standardise(5); !ok {
		t.Error("the framework's own discount never forms a null")
	}
}

// TestUpperTailIsAProperTailAndNeverZero. A p-value of exactly zero cannot be reported: it
// would make any downstream combination or log transform undefined. The statistic has no
// floor, so a long drift can underflow the tail and this is where that is handled.
func TestUpperTailIsAProperTailAndNeverZero(t *testing.T) {
	if got := drift.UpperTail(0); math.Abs(got-0.5) > 1e-12 {
		t.Errorf("UpperTail(0) = %v, want 0.5", got)
	}
	if drift.UpperTail(3) >= drift.UpperTail(1) {
		t.Error("the tail is not decreasing in z")
	}
	for _, z := range []float64{40, 100, math.Inf(1)} {
		if p := drift.UpperTail(z); p <= 0 {
			t.Errorf("UpperTail(%v) = %v, want a positive value", z, p)
		}
	}
	for _, z := range []float64{-40, -100, math.Inf(-1)} {
		if p := drift.UpperTail(z); p > 1 {
			t.Errorf("UpperTail(%v) = %v, want at most 1", z, p)
		}
	}
	if p := drift.UpperTail(math.NaN()); p != 1 {
		t.Errorf("UpperTail(NaN) = %v, want 1: no evidence is not extreme evidence", p)
	}
}
