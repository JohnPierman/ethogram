package cooccurrence

import (
	"math"
	"slices"

	"github.com/JohnPierman/ethogram/domain/calibration"
)

// BlockID names one block of the offline partition z of §8.2.
type BlockID string

// BlockPair addresses the inter-block weight m_rs. Canonical: R ≤ S, so (r, s) and
// (s, r) address one entry.
type BlockPair struct {
	R, S BlockID
}

// NewBlockPair returns the canonical pair for two blocks given in either order.
func NewBlockPair(a, b BlockID) BlockPair {
	if b < a {
		return BlockPair{R: b, S: a}
	}
	return BlockPair{R: a, S: b}
}

// Partition is the persisted offline Leiden result (§8.2): computed by a scheduled
// batch, never in the scoring path — that separation is what preserves R4, since
// community detection is stochastic and the scoring path may not be. It carries
// its own provenance (seed, graph checksum, resolution), which the evidence
// reports so a verdict can be traced to the exact partition that priced it.
type Partition struct {
	Seed          int64
	GraphChecksum string
	Resolution    float64

	// Blocks is the assignment z(i).
	Blocks map[NodeID]BlockID

	// DegreeSums holds D_r = Σ_{i: z(i)=r} k_i, the block degree sums of (14). The
	// paper writes D_r, not κ_r, deliberately: κ is reserved for the von Mises
	// concentration of §7.2.
	DegreeSums map[BlockID]float64

	// BlockWeights holds m_rs, keyed canonically, under the Karrer–Newman
	// convention: weight INTERNAL to a block is counted from both endpoints, so
	// m_rr is TWICE the internal weight. Equation (15)'s collapse depends on this;
	// see Lambda.
	BlockWeights map[BlockPair]float64

	// TotalDegree is Σ_r D_r = 2m of the graph this partition was computed on.
	//
	// It exists because the partition is frozen at the burn-in boundary while the
	// degrees fed to Lambda are read from the live decayed graph, and equation (14) is
	// a maximum-likelihood estimate over ONE graph. Without the snapshot's own total,
	// the block statistics and the node degrees are measured on two different graphs
	// and λ carries the ratio between them; with it, the block term reduces to a
	// dimensionless affinity that the live graph's scale cannot distort. A partition
	// that does not declare it cannot be used, and Lambda falls back to (15).
	TotalDegree float64
}

// SumDegreeSums totals block degree sums in a canonical block order, so the result does
// not depend on map iteration order (R4). Callers building a Partition use it to fill
// TotalDegree.
func SumDegreeSums(sums map[BlockID]float64) float64 {
	ids := make([]BlockID, 0, len(sums))
	for id := range sums {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	total := 0.0
	for _, id := range ids {
		total += sums[id]
	}
	return total
}

// Lambda evaluates the maximum-likelihood Poisson rate of equation (14),
//
//	λ_ij = k_i·k_j·m_rs / (D_r·D_s),   r = z(i), s = z(j)
//
// falling back to the single-block collapse of equation (15) when the partition is
// nil, when either node is unassigned, or when either block's degree sum is zero.
// The fallback is reported through usedFallback so the caller can record it in
// evidence — §8.4: the detector "falls back to (15) and reports having done so".
//
// # The collapse identity (review item B1)
//
// With one block, D_1 = 2m and m_11 = 2m under the Karrer–Newman convention that
// counts weight internal to a block from both endpoints, so (14) gives
//
//	λ_ij = k_i·k_j·2m / (2m·2m) = k_i·k_j / 2m
//
// EXACTLY: the configuration-model expectation, which is what (15) computes
// directly. An earlier draft of the paper had m_11 = m, which yields k_i·k_j/4m —
// wrong by a factor of two. The one-block collapse test is the regression guard
// for that fix.
func Lambda(p *Partition, a, b NodeID, ka, kb, m float64) (lambda float64, usedFallback bool) {
	if p == nil {
		return configurationLambda(ka, kb, m), true
	}
	r, okR := p.Blocks[a]
	s, okS := p.Blocks[b]
	if !okR || !okS {
		return configurationLambda(ka, kb, m), true
	}
	dr, ds := p.DegreeSums[r], p.DegreeSums[s]
	if dr <= 0 || ds <= 0 || p.TotalDegree <= 0 {
		return configurationLambda(ka, kb, m), true
	}
	return configurationLambda(ka, kb, m) * p.affinity(r, s, dr, ds), false
}

// affinity is the dimensionless block term ω_rs = m_rs·2m / (D_r·D_s): the weight
// observed between blocks r and s over the weight the configuration model expects
// between them, both measured on the snapshot the partition was computed from.
//
// Factoring (14) as λ_ij = (k_i·k_j / 2m_live)·ω_rs is an identity when every quantity
// comes from one graph — substitute 2m for TotalDegree and the ω cancels back to
// m_rs/(D_r·D_s) — and it is the only form that survives the two graphs this detector
// actually has: node degrees from the live decayed graph, block structure from the
// frozen snapshot. ω is a ratio measured entirely within the snapshot, so the snapshot's
// scale cancels out of it, and the live scale enters exactly once, through (15).
//
// The single-block collapse is preserved exactly: with one block D_1 = m_11 = 2m and
// TotalDegree = D_1, so ω_11 = 2m·2m/(2m·2m) = 1 and λ = k_i·k_j/2m.
func (p *Partition) affinity(r, s BlockID, dr, ds float64) float64 {
	return p.BlockWeights[NewBlockPair(r, s)] * p.TotalDegree / (dr * ds)
}

// configurationLambda is equation (15): the single-block degeneration of (14),
// λ = k_i·k_j/2m. m ≤ 0 yields λ = 0, and hence P = 1 through PoissonLowerTail: an
// empty graph asserts nothing.
func configurationLambda(ka, kb, m float64) float64 {
	if m <= 0 {
		return 0
	}
	return ka * kb / (2 * m)
}

// PoissonLowerTail returns Pr(Poisson(λ) ≤ w), the lower tail the nulls of (14)
// and (15) are tested against: small exactly when co-occurrence ought to have been
// observed and was not. For a first co-occurrence (w = 0) it is e^(−λ).
//
// The tail is evaluated through the existing chi-square machinery by the identity
//
//	Pr(Poisson(λ) ≤ n) = Q(n+1, λ) = ChiSquareSurvivalNonIntegral(2λ, 2(n+1))
//
// with n = ⌊w⌋, since a decayed weight is generally non-integral while the null
// counts events. λ ≤ 0 returns 1: a graph expecting nothing is surprised by
// nothing. The result is guarded into (0, 1]: a huge λ with a small n underflows
// e^(−λ) to zero, which would poison the logarithm of equation (18), so the tail
// is floored at the smallest positive float64 instead.
// PoissonLowerTailLog returns ln Pr(Poisson(λ) ≤ w), the quantity [PoissonLowerTail]
// can only floor.
//
// The linear tail is e^(−λ) for a first co-occurrence, and λ here is not small: the
// graph's degrees are event counts, so a busy account paired with a moderately busy
// host gives λ in the thousands. e^(−4000) is zero in float64, and PoissonLowerTail
// returns the smallest positive float64 instead — correct as a guard, fatal as a score,
// because every such pair then reports the same number. Measured on LANL days 7–8, 33 of
// 262 labelled events came back at exactly the floor, and the min-p arm retained 400
// alerts tied to the last bit.
//
// λ ≤ 0 returns 0 = ln 1: a graph expecting nothing is surprised by nothing.
func PoissonLowerTailLog(lambda, w float64) float64 {
	if lambda <= 0 {
		return 0
	}
	n := math.Floor(w)
	if n < 0 {
		n = 0
	}
	logP := calibration.ChiSquareLogSurvivalNonIntegral(2*lambda, 2*(n+1))
	if logP > 0 {
		return 0
	}
	return logP
}

func PoissonLowerTail(lambda, w float64) float64 {
	if lambda <= 0 {
		return 1
	}
	n := math.Floor(w)
	if n < 0 {
		n = 0 // a decayed weight cannot be negative; keep the identity's domain
	}
	p := calibration.ChiSquareSurvivalNonIntegral(2*lambda, 2*(n+1))
	if p <= 0 {
		return math.SmallestNonzeroFloat64
	}
	if p > 1 {
		return 1
	}
	return p
}
