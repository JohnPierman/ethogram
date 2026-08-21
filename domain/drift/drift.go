// Package drift tests whether an entity's event rate has undergone a sustained shift,
// which is the alternative the volume predictive of equation (11) is built to tolerate.
//
// # Why a second volume statistic
//
// Equation (11)'s null is negative binomial and structurally over-dispersed:
// Var[K]/E[K] = (b+ρ)/b > 1. That is correct for the question it asks — is *this period's*
// count surprising for this entity — and it is why the arm does not fire on an account whose
// habitual variation is wide. It also means a modest shift sustained over many periods is
// inside the null in every single period, so no accumulation of evidence ever occurs. On the
// planted corpus the volume arm's median p-value on low-and-slow attacks is 0.72 against 0.29
// on the other mechanisms: its response to the one mechanism it exists to catch is inverted.
//
// The defect is not in the fit. A marginal test of one period cannot see a drift that is
// small in every period, however well calibrated it is, because the evidence is in the
// sequence and a marginal test discards the sequence.
//
// # The statistic
//
// Page's one-sided cumulative sum over per-period counts. With the entity's own baseline rate
// λ₀ from equation (10)'s posterior, and a shift ρ > 1 the test is to be sensitive to, the
// reference value is
//
//	k = λ₀(ρ − 1)/ln ρ,
//
// the count at which the Poisson likelihoods of λ₀ and ρλ₀ are equal, and the statistic is
//
//	S_t = max(0, S_{t−1} + k_t − k).
//
// Under no drift the increments have negative mean and S stays near zero; under a sustained
// shift they have positive mean and S grows linearly in the number of periods while the
// spread of its null grows as the square root. That gap is the whole mechanism, and it is why
// this reaches an alternative equation (11) cannot: the two statistics differ in what they
// accumulate, not in how well they are calibrated.
//
// # The null, and why it is the entity's own
//
// S has no closed-form null worth using at this scale, and a nominal threshold on it would be
// a threshold on an average-run-length calculation that assumes a Poisson stream the corpus
// does not supply. So the reported p-value is the upper tail of S standardised by the mean and
// spread of the S values *this entity's own history* has produced — the construction §6.2
// adopts for the timing detector, for the same reason: it removes a scale that is not
// comparable across entities, and it has no floor.
//
// [MinWeight] is where the detector abstains instead. An entity with too few completed periods
// has no null to standardise against, and one whose rate never varies has no spread; both are
// abstentions under R3 rather than a p-value computed against noise.
//
// # What this does not do
//
// The null lags the data it is fitted on, so an entity that has been drifting for longer than
// the discount half-life will have inflated its own null and will score less extremely than a
// newly drifting one. That is a property of any self-referential null and is the reason the
// arm is a complement to equation (11) rather than a replacement: the two make different
// mistakes.
package drift

import (
	"errors"
	"fmt"
	"math"
)

// ErrShift reports a shift factor that does not describe an increase.
var ErrShift = errors.New("drift: the shift factor must be finite and greater than one")

// ErrBaseline reports a baseline rate a reference value cannot be formed from.
var ErrBaseline = errors.New("drift: the baseline rate must be finite and positive")

// DefaultShift is the multiplicative shift the reference value is tuned for.
//
// 1.3. The planted low-and-slow mechanism is "a modest sustained increase" using only values
// the account already uses, and a CUSUM tuned for a shift it is not seeing loses power in the
// usual way: too small a target and the statistic drifts up on ordinary variation, too large
// and it accumulates nothing until the shift is already obvious. This is a stated parameter,
// not a fitted one -- fitting it on the labels the arm is scored against would make its
// sensitivity a restatement of the corpus.
const DefaultShift = 1.3

// MinWeight is the least discounted period weight at which the standardised statistic is
// formed. Below it the entity has too few of its own S values for a mean and a spread to mean
// anything, and the detector abstains.
//
// 8. One period cannot produce a spread and a handful produce one dominated by whichever
// period happened to be busiest; eight is where the estimate stops being a restatement of a
// single observation, and matches the order of the minimum history the novelty-rate arm
// requires before it will hold an opinion.
//
// It interacts with the discount, and the interaction binds. Discounted weight saturates at
// 1/(1−δ) however long the entity is observed, so a half-life short enough to put that
// ceiling below MinWeight makes the arm abstain forever rather than warm up slowly. See
// [MaxDiscount].
const MinWeight = 8

// MaxDiscount is the heaviest per-period discount at which the null can ever reach
// [MinWeight]: below it, 1/(1−δ) < MinWeight and the arm abstains on every entity for all
// time.
//
// At daily periods this requires a half-life of at least about 5.2 days. The framework's
// seven-day half-life gives δ = 0.906 and a saturating weight of 10.6, which clears it —
// but not by much, and a run that shortens the half-life below five days disables this arm
// silently unless it checks. That is why the bound is a named constant rather than a remark.
const MaxDiscount = 1 - 1.0/MinWeight

// ReachesMinWeight reports whether a per-period discount permits the null ever to be formed.
// A caller configuring a half-life should consult it rather than discover the abstention in a
// result file.
func ReachesMinWeight(delta float64) bool {
	return delta >= MaxDiscount && delta < 1 || delta == 1
}

// Reference returns Page's reference value for detecting a multiplicative shift of the given
// factor away from a baseline rate.
//
// It lies strictly between λ₀ and ρλ₀: it is the count at which the two Poisson likelihoods
// are equal, so counts above it are evidence for the shift and counts below it against.
func Reference(baseline, shift float64) (float64, error) {
	if math.IsNaN(baseline) || math.IsInf(baseline, 0) || baseline <= 0 {
		return 0, fmt.Errorf("%w, got %v", ErrBaseline, baseline)
	}
	if math.IsNaN(shift) || math.IsInf(shift, 0) || shift <= 1 {
		return 0, fmt.Errorf("%w, got %v", ErrShift, shift)
	}
	return baseline * (shift - 1) / math.Log(shift), nil
}

// Next returns the cumulative sum after observing count in a period whose reference value is
// reference, given the sum accumulated so far.
//
// The floor at zero is what makes this a test for a shift rather than a random walk: quiet
// periods reset the accumulated evidence instead of banking credit against a later burst,
// so the statistic answers "is the rate elevated now" and not "has this entity ever been
// busy".
func Next(current, count, reference float64) float64 {
	if s := current + count - reference; s > 0 {
		return s
	}
	return 0
}

// Null is the per-entity distribution of the realised cumulative sum: discounted first and
// second moments over the S values the entity's own periods have produced.
//
// A value object in everything but the discounting, which is in-place because it is the one
// operation a caller performs per period and copying the state per event is the cost this
// avoids. Held by the caller's state row and persisted with it.
type Null struct {
	Sum   float64
	SumSq float64
	W     float64
}

// Observe folds one realised cumulative sum into the null, discounting what is already there
// by delta first.
//
// delta is the same power discount the rest of the framework uses, 2^(−Δt/T½), so an entity's
// null follows its own changing behaviour rather than averaging over a year of it.
func (n *Null) Observe(s, delta float64) {
	n.Sum = delta*n.Sum + s
	n.SumSq = delta*n.SumSq + s*s
	n.W = delta*n.W + 1
}

// Moments reports the null's mean and standard deviation, and whether they rest on enough
// weight to standardise against.
func (n Null) Moments() (mean, sd float64, ok bool) {
	if n.W < MinWeight {
		return 0, 0, false
	}
	mean = n.Sum / n.W
	variance := n.SumSq/n.W - mean*mean
	if variance <= 0 {
		// An entity whose rate never varies produces the same S every period, so there is
		// no spread to standardise against and no opinion to be had on this scale.
		// Reported as an abstention rather than repaired with a floor on the spread, which
		// would manufacture extreme scores out of a rounding difference.
		return mean, 0, false
	}
	return mean, math.Sqrt(variance), true
}

// Standardise returns the number of the entity's own standard deviations by which the given
// cumulative sum exceeds its own mean, and whether the null supports the statement.
func (n Null) Standardise(s float64) (z float64, ok bool) {
	mean, sd, ok := n.Moments()
	if !ok {
		return 0, false
	}
	return (s - mean) / sd, true
}

// UpperTail returns the probability that a standard normal deviate is at least z.
//
// The upper tail, because a sustained increase is what the arm is for: S is floored below and
// grows without bound above, so the evidence is entirely in the upper tail and a two-sided
// test here would spend half its significance on an event that cannot occur.
func UpperTail(z float64) float64 {
	if math.IsNaN(z) {
		return 1
	}
	p := 0.5 * math.Erfc(z/math.Sqrt2)
	switch {
	case p <= 0:
		// The standardised statistic has no floor, so a long sustained drift can underflow
		// the tail. Reported at the smallest representable positive value rather than zero,
		// which no p-value may be.
		return math.SmallestNonzeroFloat64
	case p > 1:
		return 1
	default:
		return p
	}
}
