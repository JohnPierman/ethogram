// Package allocation divides a fixed alert budget across detectors of unequal quality.
//
// # The problem
//
// Several detectors each rank the same stream, and only a fixed number of alerts a day can
// be investigated. The obvious constructions all fail, and they fail in different ways.
//
// Combining the detectors' p-values into one statistic — Fisher's sum, or the corrected
// minimum — asks whether the evidence is jointly unusual. Where most detectors are
// uninformative about the alternative, that averages an informative one away. Taking the
// union of the detectors' own rankings avoids the averaging but replaces it with an equal
// quota: with J detectors and a budget of B, each reads about B/J of the depth it had
// alone, so adding a detector that finds nothing costs the budget a share of B/J. And
// thresholding the detectors' raw p-values at one cut compares numbers that share no
// scale, handing the whole queue to whichever detector's tail is numerically smallest
// rather than to whichever is most informative.
//
// What none of them contains is a statement of how much of the budget each detector has
// EARNED. This package is that statement.
//
// # The construction
//
// Two quantities per detector, both fitted on a labelled window disjoint from the window
// being scored, and both frozen before scoring begins:
//
//   - [Tail] — the detector's own null over its log p-value, so an alert can be placed on a
//     common scale without comparing p-values across detectors. A rank in the fitting
//     sample would do, except that it cannot fall below 1/(n+1) and the alerts worth having
//     are past that floor; [Tail] extends the tail below it instead of flooring at it.
//   - [Weight] — how sharply that detector's labelled events separate from its own null,
//     as the single parameter of a Beta(a, 1) density over those null quantiles.
//
// An alert's score is then the log-likelihood ratio of its being labelled against its being
// background, [Weight.LogLikelihoodRatio]. A detector whose labelled events sat where any
// event sits fits a = 1, scores 0 on everything it holds, and enters a queue only when the
// informative detectors have failed to fill it. A detector whose labelled events sat far
// into its own tail scores highly there and nowhere else. The budget divides itself: there
// is no share parameter, because a share is what a common scale plus a fitted weight
// already implies.
//
// # Why the score is per-alert
//
// Deliberately, and it is the constraint that shaped everything above. A score that reads
// an alert's rank among the day's events cannot be computed until the day is over, so it
// cannot be deployed however well it evaluates: an operator at 14:00 does not know what
// arrives by 23:59. Every quantity here is either a property of the single alert or a
// property of frozen state, so the same score that ranks a batch thresholds a stream.
//
// # What this package is not
//
// Not a p-value. No null distributes a likelihood ratio built from a fitted weight, so
// this reports a selection and its evidence, not a calibrated tail probability. Each alert
// still carries the p-value of the detector that raised it.
//
// Nothing here names a detector, a field, a corpus or an entity: it is arithmetic over log
// p-values and a labelled sample, so the same construction applies to any set of detectors
// on any stream.
package allocation

import (
	"errors"
	"fmt"
	"math"
	"sort"
)

// Uninformative is the weight of a detector whose labelled events are distributed exactly
// as its background is: it scores every alert 0 and so earns no share of the budget.
const Uninformative = 1.0

// Weight is one detector's fitted quality: the exponent a of the density
//
//	f(q) = a·q^(a−1)    on (0, 1]
//
// over the null quantiles q that detector assigned to labelled events.
//
// A value object, validated on construction and compared by value. a = 1 is the uniform
// density, meaning the detector's labelled events sit where any event sits.
//
// a is bounded above by 1 rather than merely reported. An a above 1 says the labelled
// events concentrate at the detector's LEAST extreme end, which would make the likelihood
// ratio increasing in q and have a selection prefer that detector's dullest alerts. On the
// sample sizes this is fitted from — tens of labelled events across several detectors —
// that is a sampling artefact rather than a detector to be run backwards, so it is
// admitted as [Uninformative] and the fit records that it was clamped.
type Weight struct {
	a float64
}

// NewWeight validates and returns a weight. a must lie in (0, 1]; values above 1 are
// refused here rather than clamped, because a caller constructing one directly is stating
// a belief and an out-of-range belief is a mistake. [Fit] clamps, and says so.
func NewWeight(a float64) (Weight, error) {
	if math.IsNaN(a) || math.IsInf(a, 0) {
		return Weight{}, fmt.Errorf("allocation: weight %v is not finite", a)
	}
	if a <= 0 || a > 1 {
		return Weight{}, fmt.Errorf("allocation: weight %v is outside (0, 1]", a)
	}
	return Weight{a: a}, nil
}

// UninformativeWeight is the weight of a detector no evidence supports.
func UninformativeWeight() Weight { return Weight{a: Uninformative} }

// A is the fitted exponent.
func (w Weight) A() float64 { return w.a }

// IsInformative reports whether any share of a budget is owed to this detector.
func (w Weight) IsInformative() bool { return w.a < Uninformative }

// LogLikelihoodRatio scores one alert, given the log of its null quantile under the
// detector that raised it.
//
//	ln f(q) = ln a + (a − 1)·ln q
//
// Because a null quantile is uniform on (0, 1] by construction, that density is the
// likelihood ratio of the alert being labelled against its being background — so alerts
// from different detectors are comparable on it, which is the whole point.
//
// The argument is ln q rather than q because q reaches 1e-1400 on real telemetry, which is
// not a representable number. The expression is linear in ln q, so there is nothing to
// underflow. An [Uninformative] weight returns exactly 0 for every argument.
func (w Weight) LogLikelihoodRatio(logQ float64) float64 {
	if w.a >= Uninformative {
		return 0
	}
	if logQ > 0 {
		logQ = 0 // a quantile cannot exceed 1; clamp rather than return a positive score
	}
	return math.Log(w.a) + (w.a-1)*logQ
}

// FitReport records what a fit was given and what it did, so a weight in a result can be
// read back to the evidence behind it.
type FitReport struct {
	// Observed is the number of labelled events the detector surfaced inside the
	// retained depth, and Censored the number it evaluated but did not surface. Both
	// belong in the record: a weight fitted from three observations and forty censored
	// events is a different claim from one fitted from forty-three observations.
	Observed int
	Censored int
	// Clamped is true when the unconstrained maximum lay above 1 and the fit returned
	// [Uninformative] instead. Reported rather than silent, because it is the difference
	// between "no evidence" and "evidence pointing the wrong way".
	Clamped bool
	// LogLikelihood is the maximised value, for comparing fits of the same sample.
	LogLikelihood float64
	// Deviance is 2(ln L(â) − ln L(1)): twice the log-likelihood ratio against the
	// hypothesis that this detector is uninformative.
	Deviance float64
	// Significant is whether Deviance exceeded [DevianceThreshold], and therefore whether
	// the fitted weight was kept at all.
	Significant bool
}

// DevianceThreshold is how much better than uninformative a fit must be before its weight
// is used: twice the log-likelihood ratio against a = 1 must exceed it.
//
// The test is needed because the estimator is consistent but not free. Two hundred labelled
// events drawn from exactly the uniform null fit a ≈ 1 ± 0.07, and the likelihood ratio at
// a = 0.93 is not neutral — it scores an alert at ln q = −4000 some 248 log units above
// zero, so a weight that is pure sampling noise buys a detector that found nothing a large
// share of a budget. Noise in the weight becomes cost in the queue, and the whole claim of
// this package is that it does not.
//
// 2.706 is the 90th percentile of χ²(1), which is the 5% one-sided critical value for a
// parameter tested at the boundary of its range: under a = 1 the deviance is distributed as
// an equal mixture of a point mass at zero and χ²(1) (Chernoff's boundary result), so the
// upper 5% point of the mixture is the upper 10% point of χ²(1).
const DevianceThreshold = 2.706

// fitIterations is the number of golden-section steps taken over a in (0, 1].
//
// The likelihood is smooth and unimodal in a on this interval, and each step shrinks the
// bracket by a factor of about 1.5, so this reduces a bracket of width 1 to far below the
// precision any downstream comparison can use. It is a fixed count rather than a tolerance
// so that the result depends on the sample alone and not on how the arithmetic rounded
// (R4): a convergence test would let two runs take different numbers of steps.
const fitIterations = 200

// Fit is the maximum-likelihood weight for one detector, from the null quantiles it
// assigned to labelled events in the fitting window.
//
// Both samples are logs of quantiles. observedLogQ holds one entry per labelled event the
// detector surfaced; censoredLogQ holds, for each labelled event the detector evaluated but
// did not surface, the log quantile of the shallowest alert retained for it — the event's
// own quantile is somewhere below that and is not known more precisely.
//
// The censored term is not a refinement. Without it the likelihood is maximised by treating
// a detector that surfaced two of forty-nine labelled events, at its two most extreme
// ranks, as the sharpest detector in the set; measured on such a sample the difference is
// a = 0.38 with censoring against a = 0.07 without. A detector is not rewarded for the
// events it missed being invisible.
//
// A detector that ABSTAINED on a labelled event contributes neither an observation nor a
// censoring point, and the caller is responsible for that exclusion: abstention is the
// absence of an opinion rather than a weak one, and scoring it as a miss would penalise a
// detector for declining to guess.
//
// Neither input is modified.
func Fit(observedLogQ, censoredLogQ []float64) (Weight, FitReport, error) {
	observed, err := finiteNonPositive(observedLogQ, "observed")
	if err != nil {
		return Weight{}, FitReport{}, err
	}
	censored, err := finiteNonPositive(censoredLogQ, "censored")
	if err != nil {
		return Weight{}, FitReport{}, err
	}

	report := FitReport{Observed: len(observed), Censored: len(censored)}
	if len(observed) == 0 {
		// Nothing was surfaced. The likelihood has no interior maximum and the honest
		// answer is that this detector has earned nothing, not that it is perfect.
		report.Clamped = true
		return UninformativeWeight(), report, nil
	}

	// Summed in sorted order so the total is bit-identical across runs whatever order the
	// caller collected the sample in (R4).
	sorted := make([]float64, len(observed))
	copy(sorted, observed)
	sort.Float64s(sorted)
	var sumLogQ float64
	for _, v := range sorted {
		sumLogQ += v
	}
	censoredSorted := make([]float64, len(censored))
	copy(censoredSorted, censored)
	sort.Float64s(censoredSorted)

	logLik := func(a float64) float64 {
		total := float64(len(sorted))*math.Log(a) + (a-1)*sumLogQ
		for _, logQ := range censoredSorted {
			total += logSurvival(a * logQ)
		}
		return total
	}

	const lower = 1e-6
	lo, hi := lower, Uninformative
	for range fitIterations {
		m1 := lo + (hi-lo)/3
		m2 := hi - (hi-lo)/3
		if logLik(m1) < logLik(m2) {
			lo = m1
		} else {
			hi = m2
		}
	}
	a := (lo + hi) / 2

	// The bracket is closed at 1, so a maximum at the boundary is indistinguishable from
	// one beyond it. Both mean the same thing here and both are recorded as clamped.
	if a >= Uninformative-1e-9 {
		report.Clamped = true
		report.LogLikelihood = logLik(Uninformative)
		return UninformativeWeight(), report, nil
	}
	if a < lower {
		a = lower
	}
	report.LogLikelihood = logLik(a)
	report.Deviance = 2 * (report.LogLikelihood - logLik(Uninformative))
	report.Significant = report.Deviance > DevianceThreshold
	if !report.Significant {
		// The sample is consistent with this detector being uninformative, so it is
		// treated as uninformative. See [DevianceThreshold] for why a weight that merely
		// happens to fall below 1 is not good enough to spend a budget on.
		return UninformativeWeight(), report, nil
	}
	w, err := NewWeight(a)
	if err != nil {
		return Weight{}, report, err
	}
	return w, report, nil
}

// logSurvival is ln(1 − e^x) for x ≤ 0: the log probability that a censored quantile lies
// below the point it was censored at.
//
// Computed through [math.Expm1] rather than as math.Log(1-math.Exp(x)) because the two
// disagree exactly where it matters. For x near 0 — a detector that barely failed to
// surface an event — 1-math.Exp(x) cancels to zero and the logarithm returns −Inf, which
// would discard the whole fit rather than the one term.
func logSurvival(x float64) float64 {
	switch {
	case x >= 0:
		// q^a = 1: this detector places the event at the very top of its own null, so
		// the probability of it being below that is zero. Represented as a large finite
		// penalty rather than −Inf so that one such term cannot make every candidate a
		// equally impossible and leave the maximiser choosing on nothing.
		return -math.MaxFloat64 / 4
	case x < -700:
		return 0 // e^x is zero to within float64; ln(1−0) = 0
	default:
		return math.Log(-math.Expm1(x))
	}
}

// finiteNonPositive validates a log-quantile sample: every entry must be a finite log of a
// quantile in (0, 1], so at most 0.
func finiteNonPositive(vs []float64, name string) ([]float64, error) {
	if vs == nil {
		return nil, nil
	}
	out := make([]float64, 0, len(vs))
	for i, v := range vs {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, fmt.Errorf("allocation: %s log quantile %d is %v, not finite",
				name, i, v)
		}
		if v > 0 {
			return nil, fmt.Errorf(
				"allocation: %s log quantile %d is %v; a quantile cannot exceed 1",
				name, i, v)
		}
		out = append(out, v)
	}
	return out, nil
}

// ErrEmptyTail is returned when a tail is asked to be fitted from no observations.
var ErrEmptyTail = errors.New("allocation: a tail needs at least two observations")
