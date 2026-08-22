package calibration

import (
	"errors"
	"fmt"
	"math"
	"slices"
)

// Higher Criticism at entity scope (§10.3, issue #17).
//
// # The statistic this replaces
//
// The framework's premise is that the unit of analysis is the individual, but the budget is
// spent on events. Aggregating an entity's day is the natural repair, and the first
// aggregate tried was Fisher's statistic over the entity's own events, Σ −2 ln P. It failed
// for a diagnosable reason: it grows with the number of events, so busy entities win
// whatever they did — 71 of 172 real entity-days and only 1 of 8 planted ones. That is the
// same disease as the history-length dependence one level up, a statistic whose scale is
// set by traffic volume rather than by evidence.
//
// Higher Criticism (Donoho and Jin 2004) is built for the opposite regime: a sparse signal among many tests,
// normalised so that the statistic does not inflate with the number of tests, and adaptively
// optimal without the sparsity having to be specified. A campaign is a sparse cluster of
// moderate anomalies inside an otherwise ordinary day, which is exactly the alternative
// Fisher's sum dilutes and this one is optimal against.
//
// The statistic is
//
//	HC*_n = max_{1 ≤ i ≤ α₀n}  √n · (i/n − p_(i)) / √(p_(i)(1 − p_(i)))
//
// over the ascending order statistics of the entity's p-values.
//
// # Why the input is log p-values
//
// Because p-values here reach ln P = −4000, which is zero as a float64, and a statistic
// computed from zeros is computed from nothing. This is the fifth site in this codebase to
// meet that: the χ² tail met it first, then volume's negative binomial, then the verdict
// boundary, then the arm ranking. Taking the log as the primary representation means a day
// whose most extreme event is at ln P = −4000 is separable from one at −2000, where in
// p-space both are exactly zero and therefore tied.
//
// The consequence is that the statistic itself overflows long before its logarithm does: at
// p = e^−4000 the term is of order e^2000 against a float64 ceiling near e^709. So the value
// is carried as its logarithm and [MoreExtreme] is the comparison to rank by; Statistic is
// the reading of it, and is +Inf where the reading is unavailable.
//
// # Determinism (R4)
//
// One fixed sort with an index tie-break, one fixed scan order, no clock and no randomness.

// ErrNoTests reports an attempt to compute the statistic over nothing.
var ErrNoTests = errors.New("calibration: higher criticism needs at least one p-value")

// DefaultAlpha0 is the fraction of the ranks the maximum is taken over.
//
// Donoho and Jin take α₀ = 0.1 and note the statistic is insensitive to it, since under the
// sparse alternative the maximum is attained at small i. Kept as the default and left
// configurable so the insensitivity can be checked rather than trusted.
const DefaultAlpha0 = 0.1

// HigherCriticismResult is the statistic with everything needed to recompute it by hand
// (R5).
type HigherCriticismResult struct {
	// LogStatistic is ln HC* where the statistic is positive, and −Inf where it is not.
	// This is the field to rank on, through [MoreExtreme]: it is finite over the whole
	// range of inputs this corpus produces, where Statistic is not.
	LogStatistic float64
	// Statistic is HC* itself, for reading. It is +Inf when the value overflows float64,
	// which happens whenever LogStatistic exceeds about 709.
	Statistic float64
	// Positive records whether any rank produced a positive term. A day where every
	// p-value sits above its own rank fraction has no positive term, which is a
	// well-defined and entirely ordinary outcome — the day was quieter than uniform — and
	// is not the same as a day with no evidence.
	Positive bool
	// Rank is the i attaining the maximum, and PValueLog is that rank's log p-value.
	Rank      int
	PValueLog float64
	// N is the number of tests the day comprised, and Considered is how many ranks fell
	// inside the α₀ cap.
	N          int
	Considered int
	// Truncated records that fewer p-values were supplied than the α₀ cap would examine,
	// so the maximum was taken over a prefix. See [HigherCriticism] on when that is
	// faithful and when it is not.
	Truncated bool
}

// MoreExtreme reports whether a is the more extreme of two results, and is the total order
// to rank entity-days by.
//
// It compares the logarithm where both statistics are positive, because that is the field
// that survives the inputs; where only one is positive that one wins; and where neither is,
// the larger raw value wins. Ranking on Statistic alone would tie every day whose value
// overflowed, which on this corpus is most of the interesting ones.
func MoreExtreme(a, b HigherCriticismResult) bool {
	if a.Positive != b.Positive {
		return a.Positive
	}
	if a.Positive {
		return a.LogStatistic > b.LogStatistic
	}
	return a.Statistic > b.Statistic
}

// HigherCriticism computes HC* from log p-values.
//
// logP need not be sorted and is not modified. n is the number of tests the day comprised,
// which may exceed len(logP): passing only the smallest k log p-values with the true n
// computes the top-k statistic, which is the bounded-storage form issue #17 asks for. n
// below len(logP) is a caller error and is refused, since the rank fraction i/n would then
// exceed one.
//
// # When the top-k form is faithful, and when it is not
//
// Under the sparse alternative the maximum is attained at small i, so a modest k reproduces
// the full-data statistic exactly. The failure case is a day whose maximum falls beyond rank
// k, which happens when the signal is dense rather than sparse — and then Truncated is set,
// so a truncated maximum is never silently reported as a complete one. What k suffices is a
// measurement on real data, not a choice; see the table in cmd/replay.
//
// # What is refused
//
// A log p-value above zero, which is a p-value above one; a NaN; an empty set; a
// non-positive n; an n below len(logP); and an α₀ outside (0,1]. A log p-value of −Inf is
// accepted: it is a p-value that underflowed, the statistic for it is genuinely infinite,
// and refusing it would reject exactly the events the framework exists to surface.
func HigherCriticism(logP []float64, n int, alpha0 float64) (HigherCriticismResult, error) {
	if len(logP) == 0 {
		return HigherCriticismResult{}, ErrNoTests
	}
	if n <= 0 {
		return HigherCriticismResult{}, fmt.Errorf(
			"calibration: higher criticism needs a positive test count, got %d", n)
	}
	if n < len(logP) {
		return HigherCriticismResult{}, fmt.Errorf(
			"calibration: %d p-values supplied against a test count of %d, so a rank "+
				"fraction would exceed one", len(logP), n)
	}
	if alpha0 <= 0 || alpha0 > 1 {
		return HigherCriticismResult{}, fmt.Errorf(
			"calibration: alpha0 must lie in (0,1], got %g", alpha0)
	}
	for i, lp := range logP {
		if math.IsNaN(lp) {
			return HigherCriticismResult{}, fmt.Errorf(
				"calibration: log p-value %d is NaN", i)
		}
		if lp > 0 {
			return HigherCriticismResult{}, fmt.Errorf(
				"calibration: log p-value %d is %g, above zero, so the p-value exceeds one",
				i, lp)
		}
	}

	sorted := make([]float64, len(logP))
	copy(sorted, logP)
	slices.Sort(sorted)

	// The α₀ cap, at least one rank: a day of one event still has a statistic.
	considered := int(math.Floor(alpha0 * float64(n)))
	if considered < 1 {
		considered = 1
	}
	// Fewer p-values than the cap would examine means the maximum is taken over a prefix,
	// which is only possible when the caller supplied a subset.
	truncated := considered > len(sorted)
	if truncated {
		considered = len(sorted)
	}

	result := HigherCriticismResult{
		LogStatistic: math.Inf(-1),
		Statistic:    math.Inf(-1),
		N:            n,
		Considered:   considered,
		Truncated:    truncated,
	}

	for i := 1; i <= considered; i++ {
		lp := sorted[i-1]
		fraction := float64(i) / float64(n)
		p := math.Exp(lp)
		gap := fraction - p
		if gap <= 0 {
			// This rank's p-value sits at or above its own rank fraction, so the term is
			// not positive. Evaluated directly: it cannot overflow, since the numerator is
			// bounded by one and the denominator is bounded below by the p-value's own
			// spread at a p-value large enough to have produced this branch.
			term := math.Sqrt(float64(n)) * gap / math.Sqrt(p*(1-p))
			if !result.Positive && term > result.Statistic {
				result.Statistic = term
				result.Rank = i
				result.PValueLog = lp
			}
			continue
		}

		// ln of the term, which is what survives the inputs:
		//
		//	ln HC_i = ½ln n + ln(i/n − p) − ½(ln p + ln(1−p))
		//
		// ln p is the input itself rather than the log of a reconstructed p, so a p-value
		// that underflowed to zero contributes its true magnitude here. log1p(−p) is exact
		// at p = 0, which is the common case in the deep tail.
		logTerm := 0.5*math.Log(float64(n)) + math.Log(gap) - 0.5*(lp+math.Log1p(-p))
		if !result.Positive || logTerm > result.LogStatistic {
			result.Positive = true
			result.LogStatistic = logTerm
			result.Statistic = math.Exp(logTerm)
			result.Rank = i
			result.PValueLog = lp
		}
	}

	return result, nil
}

// NullScale is √(2 ln ln n), the order of HC* under the global null.
//
// It exists so a measured null can be read against the theory rather than against a
// remembered number: the statistic has no fixed critical value, it grows very slowly with n,
// and a reader comparing two days of different sizes needs to know by how much.
func NullScale(n int) float64 {
	if n < 3 {
		return 0
	}
	return math.Sqrt(2 * math.Log(math.Log(float64(n))))
}
