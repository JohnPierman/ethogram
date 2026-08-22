package novelty

import (
	"cmp"
	"slices"
)

// ValueCount is one value's decayed count, already discounted to the scoring time.
type ValueCount struct {
	Value string
	Count float64
}

// Estimate is the result of evaluating equations (4) and (5) for one observed value,
// together with the sufficient statistics that produced it.
//
// Every field here is carried into the verdict's evidence, because R5 requires the
// verdict carry enough for an analyst to recompute it by hand, and E7 measures whether
// they actually can.
type Estimate struct {
	// PHatObserved is p̂(v) from equation (4) for the value that was observed.
	PHatObserved float64

	// PHatUnseen is p̂(∅), the mass reserved for the unseen. For a value not in the
	// entity's history, p̂(v) = p̂(∅) and the tail mass reduces to it (§6.1).
	PHatUnseen float64

	// TailMass is P from equation (5): the total probability of values no more
	// probable than the observed one.
	TailMass float64

	// NObserved is n_v, the decayed count of the observed value, zero if unseen.
	NObserved float64

	// Total is N = Σ n_v.
	Total float64

	// Distinct is K = |{v : n_v > 0}|.
	Distinct int

	// Alpha is the Dirichlet concentration α.
	Alpha float64

	// IsUnseen reports whether the observed value was absent from the history, the
	// case §6.2 discusses as maximally informative.
	IsUnseen bool
}

// Estimator evaluates equations (4) and (5).
type Estimator struct {
	// Alpha is the symmetric Dirichlet concentration α of equation (4).
	Alpha float64

	// OpenVocabulary switches the reserved unseen mass from equation (4)'s fixed-α
	// reserve to the Good–Turing estimate of [UnseenMass], with the observed values
	// renormalised to what is left.
	//
	// It is a deviation from (4) and is off by default, because it changes every
	// novelty score and must therefore be measured rather than assumed. The case for
	// turning it on is that (4)'s reserve reads only the totals, so it cannot tell an
	// account with three long-standing addresses from one with five hundred seen once
	// each — and the second is what a compromised account looks like. See [UnseenMass].
	OpenVocabulary bool
}

// Estimate evaluates the smoothed predictive (4) and the discrete tail mass (5) for
// an observed value against a history of decayed counts.
//
//	p̂(v) = (n_v + α) / (N + α(K+1)),   p̂(∅) = α / (N + α(K+1))            (4)
//	P     = Σ_u p̂(u) · 𝟙[p̂(u) ≤ p̂(v)]                                    (5)
//
// The sum in (5) ranges over the K observed values *and* the reserved category ∅,
// which together carry unit mass. For a value not in the history p̂(v) = p̂(∅), which
// is the minimum, so P reduces to p̂(∅) exactly as §6.1 states.
//
// # Determinism
//
// history is sorted by value before anything is accumulated, so the floating-point
// additions occur in a fixed order. This is not defensive tidiness: floating-point
// addition is not associative, the caller's slice may well have arrived from a map or
// from a query without a total order, and E8 asserts scores are byte-identical. The
// sort is by value rather than by count so that ties in the decayed counts, which
// LANL's one-second resolution makes common, cannot reorder the sum either.
//
// history is not mutated; the caller's slice order is preserved.
func (e Estimator) Estimate(history []ValueCount, observed string) Estimate {
	return e.EstimateWithTail(history, observed, 0)
}

// EstimateWithTail is [Estimator.Estimate] with singleton weight belonging to values the caller
// no longer holds, which a bounded store reports through [TailReporter].
//
// It changes nothing unless OpenVocabulary is on and the weight is positive: equation (4) reads
// the totals, and the totals a bounded store reports are exact. Only the Good-Turing reserve
// reads the shape, and the shape is what eviction damages.
func (e Estimator) EstimateWithTail(history []ValueCount, observed string,
	tailSingletons float64) Estimate {
	sorted := slices.Clone(history)
	slices.SortFunc(sorted, func(a, b ValueCount) int { return cmp.Compare(a.Value, b.Value) })

	var total float64
	distinct := 0
	for _, vc := range sorted {
		if vc.Count <= 0 {
			continue
		}
		total += vc.Count
		distinct++
	}

	denominator := total + e.Alpha*(float64(distinct)+1)

	// With no history at all, N = 0 and K = 0, so the denominator is α and (4) places
	// unit mass on ∅. Equation (5) then returns exactly 1: a first observation is
	// never anomalous, which §6.2 states is the correct verdict and not a special
	// case. Computing it through the general path rather than branching is what makes
	// the exactness fall out rather than being asserted.
	pHatUnseen := e.Alpha / denominator
	// Good–Turing reads the shape of the distribution rather than its size. The observed
	// values are then renormalised to the mass it leaves, so the categories still carry
	// unit mass exactly: p̂(∅) + Σ_v p̂(v) = p̂(∅) + (1 − p̂(∅))·Σ_v n_v/N = 1.
	observedScale := 0.0
	if e.OpenVocabulary && total > 0 {
		if mass, used := UnseenMassWithTail(sorted, e.Alpha, tailSingletons); used {
			pHatUnseen = mass
			observedScale = (1 - mass) / total
		}
	}

	var (
		nObserved float64
		isUnseen  = true
	)
	for _, vc := range sorted {
		if vc.Count > 0 && vc.Value == observed {
			nObserved = vc.Count
			isUnseen = false
			break
		}
	}

	pHatObserved := pHatUnseen
	if !isUnseen {
		pHatObserved = (nObserved + e.Alpha) / denominator
		if observedScale > 0 {
			pHatObserved = nObserved * observedScale
		}
	}

	// Equation (5). The reserved category is folded in first, then the observed values
	// in sorted order, giving one fixed summation order for every call.
	tail := 0.0
	if pHatUnseen <= pHatObserved {
		tail += pHatUnseen
	}
	for _, vc := range sorted {
		if vc.Count <= 0 {
			continue
		}
		pu := (vc.Count + e.Alpha) / denominator
		if observedScale > 0 {
			pu = vc.Count * observedScale
		}
		if pu <= pHatObserved {
			tail += pu
		}
	}

	// The masses sum to one exactly in exact arithmetic, but each term is a rounded
	// quotient, so their sum can exceed one in the last bit. Clamping keeps the value
	// a probability; equation (18) takes its logarithm, and ln of a value above one is
	// negative, which would silently subtract from the combined statistic.
	if tail > 1 {
		tail = 1
	}

	return Estimate{
		PHatObserved: pHatObserved,
		PHatUnseen:   pHatUnseen,
		TailMass:     tail,
		NObserved:    nObserved,
		Total:        total,
		Distinct:     distinct,
		Alpha:        e.Alpha,
		IsUnseen:     isUnseen,
	}
}

// NoveltyPValue returns the p-value for a first-ever value at the given history size,
// which §6.2 uses to state a monotonicity property:
//
//	P = α / (N + α(K+1))
//
// At fixed K this is strictly decreasing in N, so the same novel observation is more
// surprising for an entity with richer history, novelty being informative in
// proportion to the evidence it contradicts. K itself grows as history accumulates, so
// the two move together; for an attribute with a settled value set K grows far more
// slowly than N and the ordering survives.
func (e Estimator) NoveltyPValue(total float64, distinct int) float64 {
	return e.Alpha / (total + e.Alpha*(float64(distinct)+1))
}
