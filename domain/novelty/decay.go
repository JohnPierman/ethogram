// Package novelty implements Detector I, per-entity categorical novelty (§6).
//
// The null H₀ is that e(f) is drawn from the entity's historical distribution over
// D_f, estimated from decayed counts. The estimator is the posterior predictive of a
// symmetric Dirichlet–multinomial with concentration α, one category of which is
// reserved for the unseen (equation (4)), and the p-value is the discrete tail mass
// (equation (5)).
//
// The reserved category is what discharges R2 here: the estimator is well defined
// without enumerating D_f, so a field whose value set was never declared is admitted.
//
// # Plug-in, not exact
//
// §6.1 is explicit that the distribution in (4) is estimated from the entity's own
// history, so (5) is a plug-in tail mass rather than an exact frequentist p-value: its
// validity is the validity of the model that produced it. This is a deliberate trade
// for evidence that an analyst can recompute by hand (§6.4, R5). Where a detector's
// model-based calibration proves inadequate, the conformal construction of §10.1
// applies to it exactly as it applies to a heuristic scorer.
package novelty

import (
	"math"

	"github.com/JohnPierman/ethogram/domain/event"
)

// HalfLife is the decay half-life T½ of §6.2, in the same units as [event.Timestamp].
//
// The symbol is T½ rather than H throughout, per the notation of §5.4: H is reserved
// for the harmonic truncation order of §7.2.
type HalfLife event.Timestamp

// DecayFactor returns δ = 2^(−Δt/T½), the discount applied to a count observed Δt ago
// (§6.2).
//
// Both timestamps are event time. No wall clock is consulted, which is what makes a
// replay reproduce a run exactly (R4); an architecture test rejects time.Now from this
// layer outright.
//
// Δt ≤ 0 yields 1: an event at or before a row's last-observed timestamp applies no
// discount. Out-of-order arrival is therefore absorbed rather than inflating a count,
// which matters because a corpus replayed by timestamp can still carry ties, as LANL's
// one-second resolution guarantees it will.
func DecayFactor(from, to event.Timestamp, halfLife HalfLife) float64 {
	if halfLife <= 0 {
		return 1
	}
	dt := to - from
	if dt <= 0 {
		return 1
	}
	return math.Exp2(-float64(dt) / float64(halfLife))
}

// Decay discounts a count from its own last-observed timestamp to the scoring time.
//
// §6.2 requires the discount be applied lazily per row from that row's own timestamp,
// which is what makes a sweep job unnecessary: a row carries (count, last_seen) and is
// brought up to date only when it is read.
func Decay(count float64, lastSeen, at event.Timestamp, halfLife HalfLife) float64 {
	return count * DecayFactor(lastSeen, at, halfLife)
}

// Accumulate folds one fresh observation into a decayed count, returning the updated
// count for storage alongside the new timestamp.
//
// Decay is linear, so a total maintained by this same rule stays exactly equal to the
// sum of the counts it totals:
//
//	N(t) = Σ_v n_v(t)
//
// holds by induction, since discounting every term by the same factor and adding one
// to a single term is what both the parts and the total do. That is why N can be
// carried as a single decayed scalar rather than re-summed from every value row, which
// would otherwise make each score cost a full scan of the value set.
func Accumulate(count float64, lastSeen, at event.Timestamp, halfLife HalfLife) float64 {
	return Decay(count, lastSeen, at, halfLife) + 1
}

// EffectiveSampleSize is 1/(1−δ), the number of observations informing a posterior
// under geometric weights (§7.5).
//
// It gives operators an interpretable statement of how much history stands behind a
// verdict, and §13.2 notes that it also bounds the drift the framework can perceive: a
// change unfolding over a timescale substantially exceeding T½ is absorbed into the
// baseline as it occurs and is never scored.
//
// step is the spacing at which the discount is applied, typically the corpus's time
// resolution.
func EffectiveSampleSize(step event.Timestamp, halfLife HalfLife) float64 {
	delta := DecayFactor(0, step, halfLife)
	if delta >= 1 {
		return math.Inf(1)
	}
	return 1 / (1 - delta)
}
