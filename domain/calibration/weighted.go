package calibration

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
)

// Covariate-weighted multiple testing (§10.3, issue #15).
//
// # The problem this addresses
//
// The novelty estimate of equation (4) reduces, for a first-ever value, to the reserved
// mass α/(n + α(K+1)), which for α = 1 and n ≫ K is about 1/n. Measured across 32 planted
// victims spanning 263 to 20,666 events of history, p × n has median 1.15 and lies within
// [0.50, 7.22]. So among novel events the ranking is close to sorting accounts by event
// count: clearing the realised cut of 8.52e−06 needs roughly 117,000 events of history and
// the busiest planted victim had 20,666. No attack on an ordinary account can win a slot
// however it behaves.
//
// History length is therefore informative about the p-value's scale while being no part of
// the question being asked, which is the setting covariate-weighted multiple testing was
// built for [73][74].
//
// # Why this operates on the selection rather than inside the null
//
// The previous attempt fixed it inside the null — Good–Turing — and lost: novelty 60 → 46
// detections, pairing 59 → 36, and in the top decile it raised 43 of 54 p-values by a
// median factor of 109. Large histories carry many singletons, so Good–Turing judges a
// first-ever value unremarkable for precisely the accounts equation (4) ranked highest.
//
// The lesson is not that reweighting is wrong but that the working configuration works for
// a reason the model does not state, and any reweighting that neutralises n risks
// destroying that accidental correlation. So the weights here are learned from the data
// rather than imposed, and whether they help is a measurement, not an assumption.
//
// # Determinism (R4)
//
// Every routine here is pure. Stratum boundaries come from a sorted copy of the covariate,
// fold assignment is a fixed hash of seed and index with no clock and no randomness source,
// and every sum is accumulated in one fixed order.

// ErrWeightLength reports a weight vector whose length does not match the p-values it is
// meant to weight.
var ErrWeightLength = errors.New("calibration: weights and p-values differ in length")

// WeightedBenjaminiHochberg is the weighted step-up procedure of Genovese, Roeder and
// Wasserman [73]: Benjamini–Hochberg applied to the weighted p-values p_i/w_i,
//
//	p_(i)/w_(i) ≤ (i/m)·q,   Σ w_i = m,
//
// reporting every test of rank at or below the largest passing rank.
//
// # What the guarantee requires
//
// FDR control holds for **any** weights that are fixed independently of the p-values they
// are applied to. That independence is the whole burden of using this correctly, and it is
// not something this function can check: a weight vector computed from the same p-values it
// weights will over-reject, and the arithmetic here will look exactly the same while it
// happens. [StratifiedWeights] exists to discharge that burden by cross-fitting, and its
// documentation states the limits of what cross-fitting buys.
//
// # Weight handling, pinned rather than left implicit
//
//   - Weights are renormalised to sum to m. Passing all ones and all twos therefore give
//     identical results, and equal weights of any magnitude reduce exactly to
//     [BenjaminiHochberg] — same discoveries, same order, same tie-breaking.
//   - A weight of zero excludes its test: its weighted p-value is +Inf, so it can never be
//     discovered, and it still counts towards m. This is the intended reading of "no
//     weight", not a degenerate case.
//   - Negative, NaN or infinite weights are refused, as is a weight vector whose length
//     does not match the p-values.
//   - Weights summing to zero are refused rather than silently renormalised, since there is
//     no scale that makes them sum to m.
//
// Ties in the weighted p-value are broken by original index, a total order, so equal values
// cannot reorder between runs (R4). Discoveries are returned sorted by Index ascending and
// carry the *original* p-value, not the weighted one, so the reported evidence is the
// evidence the detector produced (R5).
func WeightedBenjaminiHochberg(pValues, weights []float64, q float64) ([]Discovery, error) {
	m := len(pValues)
	if m != len(weights) {
		return nil, fmt.Errorf("%w: %d p-values against %d weights", ErrWeightLength,
			m, len(weights))
	}
	if m == 0 {
		return []Discovery{}, nil
	}

	total := 0.0
	for i, w := range weights {
		switch {
		case math.IsNaN(w):
			return nil, fmt.Errorf("calibration: weight %d is NaN", i)
		case math.IsInf(w, 0):
			return nil, fmt.Errorf("calibration: weight %d is infinite", i)
		case w < 0:
			return nil, fmt.Errorf("calibration: weight %d is negative: %g", i, w)
		}
		total += w
	}
	if total <= 0 {
		return nil, errors.New("calibration: weights sum to zero, so no renormalisation " +
			"makes them sum to m")
	}

	// Renormalise to Σw = m, then divide. Scaling by m/total rather than dividing each
	// weight in place keeps one fixed multiplication per test and no accumulated drift.
	scale := float64(m) / total
	ranked := make([]weightedTest, m)
	for i, p := range pValues {
		w := weights[i] * scale
		weighted := math.Inf(1)
		if w > 0 {
			weighted = p / w
		}
		ranked[i] = weightedTest{Index: i, PValue: p, Weighted: weighted}
	}

	slices.SortFunc(ranked, func(a, b weightedTest) int {
		if byValue := cmp.Compare(a.Weighted, b.Weighted); byValue != 0 {
			return byValue
		}
		return cmp.Compare(a.Index, b.Index)
	})

	discoveries := []Discovery{}
	if q <= 0 {
		return discoveries, nil
	}

	largestPassing := 0
	for rank := m; rank >= 1; rank-- {
		if ranked[rank-1].Weighted <= float64(rank)/float64(m)*q {
			largestPassing = rank
			break
		}
	}
	for _, t := range ranked[:largestPassing] {
		discoveries = append(discoveries, Discovery{Index: t.Index, PValue: t.PValue})
	}
	slices.SortFunc(discoveries, func(a, b Discovery) int {
		return cmp.Compare(a.Index, b.Index)
	})
	return discoveries, nil
}

// weightedTest carries the weighted p-value the ranking is performed on alongside the
// original, so the returned discovery reports what the detector produced.
type weightedTest struct {
	Index    int
	PValue   float64
	Weighted float64
}

// StratifiedOptions configures the weight learner. The zero value is not usable; use
// [DefaultStratifiedOptions] and override.
type StratifiedOptions struct {
	// Strata is how many covariate bins to learn a weight for. More strata resolve the
	// covariate more finely and estimate each weight from less data; the failure mode is
	// over-fitting, which Folds is what defends against.
	Strata int
	// Folds is the number of cross-fitting folds. Each fold's weights are learned from the
	// other folds only. Folds = 1 means in-sample weighting, which breaks FDR control by
	// an amount that depends on Lambda; it is available so that the failure can be
	// measured rather than asserted, and the measurement is in this package's tests.
	Folds int
	// Lambda is Storey's tuning constant: the null proportion in a stratum is estimated
	// from the p-values above Lambda, where under any reasonable alternative almost
	// nothing but nulls lives.
	//
	// It turns out to matter for a second reason that is worth knowing before changing it.
	// Discoveries come from p-values far below any sensible Lambda, so an estimator reading
	// only above Lambda is nearly independent of the tests it reweights — which is why
	// in-sample weighting at Lambda = 0.5 is very nearly safe by accident, and why it stops
	// being safe as Lambda falls into the region the discoveries come from. Measured under
	// a global null at q = 0.1 with twenty strata over two thousand tests:
	//
	//	Lambda   in-sample   cross-fitted
	//	0.50       0.093        0.086
	//	0.25       0.126        0.087
	//	0.10       0.131        0.083
	//	0.05       0.157        0.084
	//	0.02       0.206        0.086
	//
	// Cross-fitting is flat across the whole range; in-sample is not, and at Lambda = 0.02
	// it realises twice the level it was asked for.
	Lambda float64
	// Seed fixes the fold assignment. It is recorded in the report so a run can be
	// reproduced exactly (R4).
	Seed uint64
}

// DefaultStratifiedOptions is the configuration the corpus measurement uses: five strata,
// five folds, Storey's conventional λ = 0.5, seed 1.
//
// Five strata rather than more because the covariate here is history length, which the
// measurement shows acts almost monotonically — five bins already separate the 263-event
// accounts from the 20,666-event ones, and finer bins would estimate each weight from
// fewer events for no additional resolution of a monotone effect.
func DefaultStratifiedOptions() StratifiedOptions {
	return StratifiedOptions{Strata: 5, Folds: 5, Lambda: 0.5, Seed: 1}
}

// StratifiedReport records how the weights were arrived at, so the ranking they produce can
// be recomputed by hand from the recorded numbers (R5).
type StratifiedReport struct {
	// Strata, Folds, Lambda and Seed echo the options actually used after defaulting.
	Strata int
	Folds  int
	Lambda float64
	Seed   uint64
	// Bounds are the Strata−1 covariate cut points, ascending.
	Bounds []float64
	// Counts is how many tests fell in each stratum.
	Counts []int
	// NullProportion is the pooled Storey estimate per stratum over all folds, reported
	// for interpretation only: the weights actually applied are the per-fold estimates,
	// which by construction differ from this.
	NullProportion []float64
	// Weight is the pooled weight per stratum on the same all-folds basis, likewise for
	// interpretation.
	Weight []float64
	// Degenerate records that every learned weight vanished — every stratum looked
	// entirely null — so the weights fell back to uniform and the procedure reduces to
	// unweighted Benjamini–Hochberg. This is the expected outcome under a global null and
	// is reported rather than hidden, because "the weighting did nothing" and "the
	// weighting was applied and did not help" are different findings.
	Degenerate bool
	// CrossFitted is false when Folds = 1, in which case the weights are not independent
	// of the p-values they are applied to and the FDR guarantee does not hold.
	CrossFitted bool
}

// StratifiedWeights learns one weight per covariate stratum and returns a per-test weight
// vector suitable for [WeightedBenjaminiHochberg].
//
// # The construction
//
// Bin the covariate into equal-count strata. Within a stratum, estimate the proportion of
// true nulls by Storey's estimator [75]
//
//	π̂₀ = #{p_i > λ} / ((1−λ)·n_g),
//
// clamped to (0, 1], and set the stratum's raw weight to (1−π̂₀)/π̂₀ — the grouped
// Benjamini–Hochberg weight of Hu, Zhao and Zhou [76]. A stratum that looks entirely null
// gets weight zero and a stratum rich in signal gets a large one; the vector is then
// renormalised by [WeightedBenjaminiHochberg] to sum to m.
//
// If every stratum looks entirely null every raw weight is zero, there is no scale that
// makes them sum to m, and the weights fall back to uniform. That is not a workaround: with
// no evidence that the covariate separates anything, the procedure should be the unweighted
// one, and the report says so through Degenerate.
//
// # Cross-fitting, and exactly what it does and does not buy
//
// The weights applied to a fold are estimated from the other folds only. Without this, the
// weights are a function of the very p-values they rank: a stratum is up-weighted precisely
// because it happened to contain small p-values, which is over-fitting in the ordinary
// sense and it inflates the realised FDR. The test suite measures both directions rather
// than asserting them, because the failing direction is what justifies the complexity.
//
// How much this matters was measured rather than assumed, and the answer is not uniform:
// see [StratifiedOptions.Lambda] for the table. At Storey's conventional λ = 0.5 in-sample
// weighting is very nearly safe, because the estimator reads only the p-values above λ while
// the discoveries come from far below it — an accident of λ, not a property of in-sample
// weighting. At λ = 0.02 the same code realises twice the false discovery rate it was asked
// for. Cross-fitting is flat across the whole range, which is the case for carrying it.
//
// What cross-fitting does not buy is a finite-sample proof. The Genovese–Roeder–Wasserman
// guarantee is for weights fixed independently of *all* the p-values, and here the weight
// vector as a whole still depends on the whole sample even though each fold's weights avoid
// its own. This is the position of Ignatiadis and Huber [74], whose guarantee is asymptotic
// and whose finite-sample evidence is simulation; the simulation in this package's tests is
// the same kind of evidence and is reported as such.
//
// # Ties in the covariate
//
// Equal-count binning cannot split a tie, so a covariate with heavy ties yields strata of
// unequal size, and a covariate that is constant yields one stratum — in which case every
// weight is equal and the procedure reduces to unweighted Benjamini–Hochberg. Reported
// through Counts rather than treated as an error, since a constant covariate is a real
// state of the data and not a caller mistake.
func StratifiedWeights(pValues, covariate []float64, opts StratifiedOptions) (
	[]float64, StratifiedReport, error) {

	m := len(pValues)
	if m != len(covariate) {
		return nil, StratifiedReport{}, fmt.Errorf(
			"%w: %d p-values against %d covariate values", ErrWeightLength, m, len(covariate))
	}
	if opts.Strata < 1 {
		return nil, StratifiedReport{}, fmt.Errorf(
			"calibration: need at least one stratum, got %d", opts.Strata)
	}
	if opts.Folds < 1 {
		return nil, StratifiedReport{}, fmt.Errorf(
			"calibration: need at least one fold, got %d", opts.Folds)
	}
	if opts.Lambda <= 0 || opts.Lambda >= 1 {
		return nil, StratifiedReport{}, fmt.Errorf(
			"calibration: Storey's lambda must lie in (0,1), got %g", opts.Lambda)
	}
	for i, c := range covariate {
		if math.IsNaN(c) || math.IsInf(c, 0) {
			return nil, StratifiedReport{}, fmt.Errorf(
				"calibration: covariate %d is not finite: %g", i, c)
		}
	}

	report := StratifiedReport{
		Strata: opts.Strata, Folds: opts.Folds, Lambda: opts.Lambda, Seed: opts.Seed,
		CrossFitted: opts.Folds > 1,
	}
	if m == 0 {
		return []float64{}, report, nil
	}

	bounds := equalCountBounds(covariate, opts.Strata)
	stratumOf := make([]int, m)
	counts := make([]int, len(bounds)+1)
	for i, c := range covariate {
		g := sort.SearchFloat64s(bounds, c)
		// SearchFloat64s returns the first index with bounds[idx] >= c, which places a
		// value exactly on a boundary in the lower stratum. Either side is defensible; the
		// choice is fixed here so it cannot vary between runs.
		stratumOf[i] = g
		counts[g]++
	}
	report.Bounds = bounds
	report.Counts = counts
	report.Strata = len(counts)

	// Folds is validated at or above one above, and i is a slice index, so neither
	// conversion can wrap.
	folds := uint64(opts.Folds) //nolint:gosec // validated positive above
	fold := make([]int, m)
	for i := range fold {
		fold[i] = int(splitMix64(opts.Seed+uint64(i)) % folds) //nolint:gosec // i is an index
	}

	// Pooled estimates, for the report only.
	report.NullProportion, report.Weight = stratumWeights(
		pValues, stratumOf, fold, -1, len(counts), opts.Lambda)

	weights := make([]float64, m)
	nonZero := false
	if opts.Folds == 1 {
		_, w := stratumWeights(pValues, stratumOf, fold, -1, len(counts), opts.Lambda)
		for i := range weights {
			weights[i] = w[stratumOf[i]]
			if weights[i] > 0 {
				nonZero = true
			}
		}
	} else {
		for f := 0; f < opts.Folds; f++ {
			_, w := stratumWeights(pValues, stratumOf, fold, f, len(counts), opts.Lambda)
			for i := range weights {
				if fold[i] != f {
					continue
				}
				weights[i] = w[stratumOf[i]]
				if weights[i] > 0 {
					nonZero = true
				}
			}
		}
	}

	if !nonZero {
		report.Degenerate = true
		for i := range weights {
			weights[i] = 1
		}
	}
	return weights, report, nil
}

// stratumWeights estimates the null proportion and the grouped-BH weight per stratum.
//
// excludeFold is the fold whose p-values must not inform the estimate; pass −1 to use every
// test, which is the pooled estimate the report carries and the in-sample estimate
// Folds = 1 deliberately uses.
func stratumWeights(pValues []float64, stratumOf, fold []int, excludeFold, strata int,
	lambda float64) (nullProportion, weight []float64) {

	above := make([]int, strata)
	total := make([]int, strata)
	for i, p := range pValues {
		if fold[i] == excludeFold {
			continue
		}
		g := stratumOf[i]
		total[g]++
		if p > lambda {
			above[g]++
		}
	}

	nullProportion = make([]float64, strata)
	weight = make([]float64, strata)
	for g := 0; g < strata; g++ {
		if total[g] == 0 {
			// No training evidence about this stratum. A weight of zero would exclude it
			// on no evidence, so it takes the neutral weight of one: unweighted treatment
			// is the honest default for a stratum nothing is known about.
			nullProportion[g] = 1
			weight[g] = 1
			continue
		}
		pi0 := float64(above[g]) / ((1 - lambda) * float64(total[g]))
		// Clamp to (0, 1]: an estimate above one means fewer large p-values than the null
		// predicts, which is evidence of nothing; an estimate of zero would divide by it.
		pi0 = math.Min(pi0, 1)
		pi0 = math.Max(pi0, 1/float64(total[g]))
		nullProportion[g] = pi0
		weight[g] = (1 - pi0) / pi0
	}
	return nullProportion, weight
}

// equalCountBounds returns strata−1 ascending cut points splitting the covariate into
// equal-count bins, deduplicated so that a tied covariate produces fewer, larger strata
// rather than empty ones.
func equalCountBounds(covariate []float64, strata int) []float64 {
	if strata <= 1 || len(covariate) == 0 {
		return []float64{}
	}
	sorted := make([]float64, len(covariate))
	copy(sorted, covariate)
	slices.Sort(sorted)

	largest := sorted[len(sorted)-1]
	bounds := make([]float64, 0, strata-1)
	for k := 1; k < strata; k++ {
		idx := k * len(sorted) / strata
		if idx >= len(sorted) {
			idx = len(sorted) - 1
		}
		b := sorted[idx]
		// A cut point at or above the largest value has nothing above it, so it would
		// create an empty stratum rather than split anything; a repeat of the previous cut
		// point likewise. Both happen whenever the covariate is tied, and a constant
		// covariate is the limiting case that must yield exactly one stratum.
		if b >= largest {
			continue
		}
		if len(bounds) > 0 && bounds[len(bounds)-1] == b {
			continue
		}
		bounds = append(bounds, b)
	}
	return bounds
}

// splitMix64 is the SplitMix64 finaliser, used to assign folds from an index without a
// randomness source: the assignment is a pure function of seed and index, so it is
// identical on every run and every platform (R4), and it is independent of the p-values,
// which is what cross-fitting requires.
func splitMix64(x uint64) uint64 {
	x += 0x9E3779B97F4A7C15
	x = (x ^ (x >> 30)) * 0xBF58476D1CE4E5B9
	x = (x ^ (x >> 27)) * 0x94D049BB133111EB
	return x ^ (x >> 31)
}
