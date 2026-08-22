package statistics

import "math"

// The framework decays every per-entity accumulator by the same power discount,
// delta = 2^(-elapsed/halfLife), so an estimate follows the entity's recent behaviour rather
// than averaging over all of it. This file is about a consequence of that which cost three
// separate defects before it was named.
//
// # The trap
//
// A discounted count does not grow without bound. Folding one observation every `gap` of
// elapsed time gives, in the limit,
//
//	W = 1 + delta + delta^2 + ... = 1/(1 - delta),   delta = 2^(-gap/halfLife)
//
// so W SATURATES. An arm that requires a minimum discounted weight before it will form an
// estimate is therefore imposing a condition that, past a certain sparsity, is unsatisfiable
// however long the entity is observed. The arm does not warm up slowly; it never warms up.
//
// Worse, the failure is silent and its direction is usually wrong. Each of the three arms that
// hit this fell back to something narrower or emptier than the estimate it could not form:
//
//   - `volume` fell back to equation (11) un-widened -- the narrowest null it has -- for exactly
//     the entities whose variation it had no evidence about, and a wholly benign account
//     bursting every four days put 31.6% of its own events below 1e-12 (#33, #42).
//   - `timing` abstains on any entity whose events average more than about 12.4 hours apart at
//     the seven-day half-life, because its weight saturates at 10.6 against a minimum of 20 --
//     so the standardised statistic is unavailable to sparse accounts permanently, not merely
//     until they accrue history (#37).
//   - `drift` would have abstained forever below a five-day half-life, which is why its own
//     bound is asserted rather than remarked on.
//
// The point of naming it here is that the arithmetic is identical in all three and the
// consequence is not obvious from any one call site. A minimum weight is not a warm-up
// parameter; it is a claim about which entities the arm can ever serve, and
// [MinimumWeightReachable] is how that claim gets checked.

// SaturatingWeight is the discounted observation count an entity converges to when it produces
// one observation every gap of elapsed time, under the given half-life.
//
// Both arguments are in the same unit, whatever that is -- microseconds, hours, periods -- and
// only their ratio matters. A non-positive gap or half-life means no discounting applies within
// the window in question, so the weight is unbounded and the function reports that.
func SaturatingWeight(gap, halfLife float64) float64 {
	if gap <= 0 || halfLife <= 0 || math.IsNaN(gap) || math.IsNaN(halfLife) {
		return math.Inf(1)
	}
	delta := math.Exp2(-gap / halfLife)
	if delta >= 1 {
		return math.Inf(1)
	}
	return 1 / (1 - delta)
}

// MinimumWeightReachable reports whether an entity producing one observation every gap can ever
// accumulate the required discounted weight.
//
// False means the requirement is unsatisfiable for that entity by construction: no amount of
// further observation will help, and whatever the arm does in place of the estimate is what it
// will do forever. That is a statement about the arm's coverage rather than about the entity,
// and it should be recorded as such rather than discovered in a result file.
func MinimumWeightReachable(minimum, gap, halfLife float64) bool {
	return SaturatingWeight(gap, halfLife) >= minimum
}

// MaximumGapForWeight is the sparsest an entity's observations may be and still reach the
// required discounted weight: the gap at which [SaturatingWeight] equals minimum exactly.
//
// Inverting W = 1/(1 - 2^(-gap/T)) gives gap = T * log2(1 / (1 - 1/W)).
//
// It returns zero where the minimum is unreachable at any spacing -- a minimum at or below one
// is met by a single observation, and this is the degenerate direction of that. Use it to state
// an arm's coverage in the units an operator thinks in: "the standardised timing statistic is
// available to accounts active more often than every 12.4 hours" is a sentence a reader can
// check against their own telemetry, where "the minimum weight is 20" is not.
func MaximumGapForWeight(minimum, halfLife float64) float64 {
	if minimum <= 1 || halfLife <= 0 || math.IsNaN(minimum) || math.IsNaN(halfLife) {
		return 0
	}
	if math.IsInf(minimum, 1) {
		return 0
	}
	return halfLife * math.Log2(1/(1-1/minimum))
}
