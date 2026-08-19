// Package marginal implements Detector IV, the population-scope marginal outlier (§9).
//
// The null H₀ is that the observed value is drawn from the population marginal for its
// field. §9 confines the detector's role to the question isolation-based methods answer
// well — whether a value is extreme against that marginal — and keeps it precisely so
// that the framework's other detectors can be credited only with what they add beyond
// it: this is the category a conventional isolation forest over a pooled feature cloud
// catches.
//
// # Scope, not estimator, is what differs from §6
//
// For categorical and boolean fields the estimator is the same smoothed Dirichlet
// predictive and discrete tail mass as equations (4) and (5). Detector I applies them
// per entity — "this entity has not done this" (§6) — where this detector applies them
// per (source, field) across every entity: "this value is rare in the population".
// Reusing the identical arithmetic is what makes the comparison between the two
// meaningful, because a divergence in their verdicts on the same event is then
// attributable to scope alone.
//
// # Numeric fields
//
// For unfamiliar numeric fields §9 admits a nonparametric marginal from a streaming
// quantile digest, assuming no distributional form and abstaining below a minimum
// observation count. The digest here is [Sketch], a simplified bounded sketch in the
// spirit of the t-digest cited at [44], simplified because the scoring path must be
// deterministic (R4) and exact quantiles are not required for a two-sided tail.
package marginal

import (
	"cmp"
	"context"
	"math"
	"slices"

	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/novelty"
)

// HalfLife is the decay half-life T½ of §6.2. It is an alias of [novelty.HalfLife],
// not a defined type: the categorical marginal decays by exactly the rule Detector I
// uses, and an alias makes accidental divergence between the two inexpressible.
type HalfLife = novelty.HalfLife

// abstainReasonThinMarginal is the §9 abstention wording, shared by the categorical
// and numeric estimators so that one condition renders as one reason.
const abstainReasonThinMarginal = "population marginal below the minimum observation count (§9)"

// ValueCount is one value's decayed count in the population marginal, already
// discounted to the scoring time. It is the same shape as [novelty.ValueCount] because
// the estimator is the same; only the scope of the rows behind it differs.
type ValueCount struct {
	Value string
	Count float64
}

// Estimate is the result of evaluating one observed value against the population
// marginal, together with the sufficient statistics that produced it (R5).
//
// It mirrors [novelty.Estimate] at population scope and adds the abstention pair:
// §9 abstains below a minimum observation count rather than scoring against a marginal
// too thin to say what the population does.
type Estimate struct {
	// PHatObserved is p̂(v) from equation (4) for the value that was observed.
	PHatObserved float64

	// PHatUnseen is p̂(∅), the mass reserved for the unseen (§6.1).
	PHatUnseen float64

	// TailMass is P from equation (5) for a categorical estimate, or the two-sided
	// tail probability for a numeric one.
	TailMass float64

	// NObserved is n_v, the decayed count of the observed value, zero if unseen.
	NObserved float64

	// Total is N = Σ n_v for a categorical estimate, or the sketch weight for a
	// numeric one. It is what the §9 minimum-observation floor is compared against.
	Total float64

	// Distinct is K = |{v : n_v > 0}|. Zero for a numeric estimate.
	Distinct int

	// Alpha is the Dirichlet concentration α. Zero for a numeric estimate, which has
	// no smoothing parameter.
	Alpha float64

	// IsUnseen reports whether the observed value was absent from the marginal.
	// Always false for a numeric estimate, whose support is the whole line.
	IsUnseen bool

	// Abstained reports that the marginal held too little evidence to score against
	// (§9). The verdict must then be abstained_unusable, never a p-value.
	Abstained bool

	// AbstainReason is the human-readable reason, empty unless Abstained.
	AbstainReason string
}

// Estimator evaluates the population marginal: equations (4) and (5) for categorical
// and boolean fields, and a two-sided tail from the quantile sketch for numeric ones.
type Estimator struct {
	// Alpha is the symmetric Dirichlet concentration α of equation (4).
	Alpha float64

	// MinObservations is the evidence — total decayed count for a categorical
	// marginal, sketch weight for a numeric one — below which the estimator abstains
	// rather than scores (§9). Zero disables the floor.
	MinObservations float64
}

// EstimateCategorical evaluates the smoothed predictive (4) and the discrete tail
// mass (5) for an observed value against the population's decayed counts.
//
// The arithmetic is [novelty.Estimator.Estimate]'s, delegated rather than restated:
// Detector IV differs from Detector I in scope, not in estimator, and running the
// identical code is what makes the comparison between them meaningful. The delegate
// sorts the history by value before accumulating anything, so the floating-point
// summation order is fixed (R4) however the rows arrived.
//
// Below MinObservations of total decayed count the estimate abstains: a population
// marginal built from a handful of events is not evidence of what the population does
// (§9). The sufficient statistics are still returned, so the abstention's evidence can
// show how far short the marginal fell.
func (e Estimator) EstimateCategorical(history []ValueCount, observed string) Estimate {
	converted := make([]novelty.ValueCount, len(history))
	for i, vc := range history {
		converted[i] = novelty.ValueCount(vc)
	}
	base := novelty.Estimator{Alpha: e.Alpha}.Estimate(converted, observed)

	est := Estimate{
		PHatObserved: base.PHatObserved,
		PHatUnseen:   base.PHatUnseen,
		TailMass:     base.TailMass,
		NObserved:    base.NObserved,
		Total:        base.Total,
		Distinct:     base.Distinct,
		Alpha:        base.Alpha,
		IsUnseen:     base.IsUnseen,
	}
	if base.Total < e.MinObservations {
		est.Abstained = true
		est.AbstainReason = abstainReasonThinMarginal
	}
	return est
}

// EstimateNumeric evaluates a two-sided tail for x against the population sketch:
//
//	P = 2 · min(CDF(x), 1 − CDF(x)),   clamped to (0, 1]
//
// Two-sided, because §9's question is whether the value is extreme against the
// marginal and extremity has no preferred direction for an unfamiliar field. P is
// floored at 1/(weight+1) so it is never exactly zero — equation (18) takes its
// logarithm — the floor being the mass one further observation would carry.
//
// Below MinObservations of sketch weight the estimate abstains with the same reason
// as the categorical case (§9). A nil sketch is the cold start: weight zero, not an
// error.
func (e Estimator) EstimateNumeric(s *Sketch, x float64) Estimate {
	var weight float64
	if s != nil {
		weight = s.Weight()
	}
	if s == nil || weight < e.MinObservations {
		return Estimate{
			Total:         weight,
			Abstained:     true,
			AbstainReason: abstainReasonThinMarginal,
		}
	}

	cdf := s.CDF(x)
	p := 2 * min(cdf, 1-cdf)
	if floor := 1 / (weight + 1); p < floor {
		p = floor
	}
	if p > 1 {
		p = 1
	}
	return Estimate{TailMass: p, Total: weight}
}

// DefaultMaxCentroids is the centroid bound used unless a caller chooses another. At
// this bound a sketch occupies about two kilobytes per (source, field) — the figure
// table T5 reports — and resolves the two-sided tail to well under a percentile,
// finer than the verdicts of §9 require.
const DefaultMaxCentroids = 128

// centroid is one (mean, weight) pair. The slice holding them is kept in ascending
// mean order, which is the sketch's single canonical traversal.
type centroid struct {
	mean   float64
	weight float64
}

// Sketch is a deterministic bounded quantile sketch over a numeric field's population
// marginal.
//
// It is a simplified bounded sketch in the spirit of the t-digest cited at [44]:
// sorted centroids of (mean, weight), compressed when their count exceeds the bound.
// It departs from [44] in the compression rule, which is chosen for determinism
// rather than tail resolution: the scoring path admits no randomness and no wall
// clock (R4), and exact quantiles are not required for a two-sided tail. When the
// count exceeds the bound, the adjacent pair with the smallest combined weight is
// merged, ties broken by the lower index — a rule that is a pure function of the
// insertion sequence, so two sketches fed the same values in the same order are
// bit-identical.
type Sketch struct {
	maxCentroids int
	centroids    []centroid // ascending mean
	total        float64
}

// NewSketch returns an empty sketch bounded at maxCentroids. A bound below two cannot
// support interpolation and is treated as [DefaultMaxCentroids].
func NewSketch(maxCentroids int) *Sketch {
	if maxCentroids < 2 {
		maxCentroids = DefaultMaxCentroids
	}
	return &Sketch{maxCentroids: maxCentroids}
}

// Add folds one observation into the sketch: a sorted insertion followed by at most
// one merge, with no randomness and no wall clock (R4).
//
// Non-finite values and non-positive weights are ignored. A centroid mean must be
// finite for the order the sketch depends on to exist; the detector abstains on such
// input before it reaches the sketch, so the guard here defends the invariant rather
// than handling an error.
func (s *Sketch) Add(x, weight float64) {
	if weight <= 0 || math.IsInf(x, 0) || math.IsNaN(x) {
		return
	}
	idx, found := slices.BinarySearchFunc(s.centroids, x, func(c centroid, target float64) int {
		return cmp.Compare(c.mean, target)
	})
	if found {
		s.centroids[idx].weight += weight
	} else {
		s.centroids = slices.Insert(s.centroids, idx, centroid{mean: x, weight: weight})
	}
	s.total += weight
	s.compress()
}

// compress restores the centroid bound by merging the adjacent pair with the smallest
// combined weight, ties broken by the lower index. The weighted mean of a pair lies
// between its parents' means, so merging preserves the sorted order.
func (s *Sketch) compress() {
	for len(s.centroids) > s.maxCentroids {
		best, bestWeight := 0, math.Inf(1)
		for i := 0; i+1 < len(s.centroids); i++ {
			if combined := s.centroids[i].weight + s.centroids[i+1].weight; combined < bestWeight {
				best, bestWeight = i, combined
			}
		}
		a, b := s.centroids[best], s.centroids[best+1]
		s.centroids[best] = centroid{
			mean:   (a.mean*a.weight + b.mean*b.weight) / bestWeight,
			weight: bestWeight,
		}
		s.centroids = slices.Delete(s.centroids, best+1, best+2)
	}
}

// Quantile returns the value at cumulative probability q, interpolated linearly
// between centroid means with each centroid's weight centred on its mean. q outside
// [0, 1] clamps to the extreme centroids. An empty sketch returns 0; callers gate on
// [Sketch.Weight], and the estimator abstains long before a sketch is empty.
func (s *Sketch) Quantile(q float64) float64 {
	n := len(s.centroids)
	if n == 0 {
		return 0
	}
	target := q * s.total
	cumBefore := 0.0
	for i := range n {
		c := s.centroids[i]
		mid := cumBefore + c.weight/2
		if target < mid {
			if i == 0 {
				return c.mean
			}
			prev := s.centroids[i-1]
			prevMid := cumBefore - prev.weight/2
			t := (target - prevMid) / (mid - prevMid)
			return prev.mean + t*(c.mean-prev.mean)
		}
		cumBefore += c.weight
	}
	return s.centroids[n-1].mean
}

// CDF returns the fraction of the sketch's weight at or below x, under the same
// piecewise-linear interpolation as [Sketch.Quantile], so the two are consistent
// inverses up to interpolation. Below the first centroid it is 0 and above the last
// it is 1; [Estimator.EstimateNumeric] floors the resulting tail, so a p-value of
// exactly zero cannot occur (equation (18) takes its logarithm). An empty sketch
// returns 0.
func (s *Sketch) CDF(x float64) float64 {
	n := len(s.centroids)
	if n == 0 {
		return 0
	}
	if x < s.centroids[0].mean {
		return 0
	}
	if x > s.centroids[n-1].mean {
		return 1
	}
	cumBefore := 0.0
	for i := range n {
		c := s.centroids[i]
		here := (cumBefore + c.weight/2) / s.total
		if x == c.mean {
			return here
		}
		if x < c.mean {
			prev := s.centroids[i-1]
			before := (cumBefore - prev.weight/2) / s.total
			t := (x - prev.mean) / (c.mean - prev.mean)
			return before + t*(here-before)
		}
		cumBefore += c.weight
	}
	return 1
}

// Weight returns the total weight held, the evidence the §9 minimum-observation floor
// gates on.
func (s *Sketch) Weight() float64 { return s.total }

// Centroids returns the number of centroids held, bounded by construction. It appears
// in verdict evidence (R5) and in table T5's state measurements.
func (s *Sketch) Centroids() int { return len(s.centroids) }

// Clone returns an independent copy. Repositories hand copies to Score so that the
// scoring path, holding the sketch, cannot advance state — the same isolation a
// database round-trip provides, which is what keeps Score writeless (§5.2).
func (s *Sketch) Clone() *Sketch {
	return &Sketch{
		maxCentroids: s.maxCentroids,
		centroids:    slices.Clone(s.centroids),
		total:        s.total,
	}
}

// Repository persists population-scope marginals per (source, field). No entity
// appears in any key: that absence is the definition of the detector (§9), and the
// contrast with the per-entity keys of [novelty.ValueCountRepository] is the whole
// point of keeping both.
//
// Categorical counts decay lazily per §6.2 exactly as Detector I's do: a row stores
// (count, last_seen), the discount 2^(−Δt/T½) is applied on read from the row's own
// timestamp via [novelty.Decay], and updates fold in via [novelty.Accumulate], so no
// sweep job exists.
//
// # The numeric sketch does not decay, deliberately
//
// This is a recorded deviation from §6.2's lazy-decay rule, not an oversight, and the
// reason is that decaying it could not change a single p-value.
//
// A quantile sketch carries centroids, and a centroid carries no timestamp — it is
// already an aggregate of observations from across the history. The only decay a
// t-digest admits without being rebuilt is a uniform scaling of every centroid weight
// by 2^(−Δt/T½), and a quantile is a ratio of weights: scaling all of them leaves every
// quantile, and therefore every numeric p-value, exactly where it was. The one thing
// uniform scaling would alter is the total weight that gates the §9 abstention, so the
// whole observable effect of "implementing decay" here would be to make the detector
// abstain more often, dressed up as recency.
//
// Genuine recency would need per-observation ages the structure does not keep: a
// sliding window of digests, or an exponentially weighted sketch rebuilt on a schedule.
// Either is a real design with real cost, and neither should be adopted on the strength
// of a consistency argument alone.
//
// The consequence, which any corpus with numeric fields makes live: Detector IV's two
// halves then answer on different timescales — the categorical marginal over a 7-day
// half-life, the numeric marginal over the whole history. On LANL auth, which has no
// numeric fields, nothing exercises this path at all.
type Repository interface {
	// FindCategorical returns the population's decayed value counts for
	// (source, field) at the given instant, in ascending Value order: row order feeds
	// the float accumulation of equation (5), and an unordered result would make the
	// sum depend on storage internals (trap 5: Postgres row order is undefined
	// without ORDER BY). A missing marginal returns an empty slice: cold start is
	// N = 0, not an error.
	FindCategorical(ctx context.Context, source event.SourceID, field event.FieldPath, at event.Timestamp) ([]ValueCount, error)

	// Cardinality returns the number of distinct values recorded for
	// (source, field), without materialising them.
	//
	// It exists so the detector can decline a field before paying for its history.
	// Equation (5)'s tail is a sum over the whole distribution, so FindCategorical is
	// linear in the distinct value count; at population scope that count is the
	// source's, not one entity's, and for a field such as a destination host it runs
	// to tens of thousands. Asking the size first turns a question the detector would
	// answer with an abstention into one it can decline in constant time.
	Cardinality(ctx context.Context, source event.SourceID, field event.FieldPath) (int, error)

	// FindNumeric returns the quantile sketch for (source, field) and whether one
	// exists. Implementations must return an isolated copy, so that a caller holding
	// the sketch cannot advance state (§5.2). at is accepted so that an
	// implementation ageing its sketch can honour it; the reference implementations
	// do not decay the sketch.
	FindNumeric(ctx context.Context, source event.SourceID, field event.FieldPath, at event.Timestamp) (*Sketch, bool, error)

	// SaveCategorical folds one observation into the population row for
	// (source, field, value) at the event time: count ← count·2^(−Δt/T½) + 1,
	// last_seen ← at, per §6.2. Creates the row if absent.
	SaveCategorical(ctx context.Context, source event.SourceID, field event.FieldPath, value string, at event.Timestamp) error

	// SaveNumeric folds one numeric observation of unit weight into the sketch for
	// (source, field), creating the sketch if absent.
	SaveNumeric(ctx context.Context, source event.SourceID, field event.FieldPath, x float64, at event.Timestamp) error
}
