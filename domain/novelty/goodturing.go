package novelty

// UnseenMass estimates the probability that the next observation of a field takes a value
// never yet seen for this entity, from the shape of the observed distribution rather than
// from its size alone.
//
// # Why the Dirichlet form is not enough
//
// Equation (4) reserves α / (n + α(K+1)) for the unseen. That depends only on the total
// count and the number of distinct values, never on how the mass is spread across them,
// and for a closed vocabulary — an operating-system field with ten values — it is
// perfectly adequate. For an open one it is wrong by orders of magnitude, and open
// vocabularies are what a real corpus is made of: addresses, hostnames, user agents,
// paths.
//
//	an account with 3 values, 1000 observations each   true P(new) ≈ 0    (4) gives 0.00033
//	an account with 500 values, one observation each   true P(new) ≈ 1    (4) gives 0.001
//
// Two histories that differ by everything receive nearly the same answer, and the second
// is what a compromised account looks like.
//
// # Good–Turing
//
// The singleton rate carries the information the counts alone do not. If a great many of
// the values seen so far were seen exactly once, the vocabulary is still opening and the
// next observation is likely to be new; if none were, it has closed and it is not:
//
//	P̂(new) = N₁ / n
//
// with N₁ the number of values observed exactly once and n the total. It reads the shape
// directly, needs only the count-of-counts, adapts to open and closed vocabularies without
// being told which it faces, and names no field — which R2 requires of everything here.
//
// # Where it is not used
//
// Below minSingletonSupport observations the count-of-counts is too thin to carry an
// estimate: with five observations, N₁/n takes one of six values and none of them means
// anything. The Dirichlet reserve is returned instead, exactly as the volume detector
// falls back to equation (11) below its dispersion floor. The estimate is also clamped
// away from 0 and 1: a null that asserts the next value is certainly new, or certainly
// not, is asserting more than the evidence supports, and a zero would poison the
// logarithm the combination takes.
//
// Decayed counts are non-integral, so "seen exactly once" is read as a count within
// singletonTolerance of one unit of weight. That is the same compromise the rest of the
// system makes to keep decay lazy, and it degrades smoothly: a value decayed to 0.9 is
// still nearly a singleton and is treated as one.
func UnseenMass(history []ValueCount, alpha float64) (mass float64, usedGoodTuring bool) {
	return UnseenMassWithTail(history, alpha, 0)
}

// UnseenMassWithTail is [UnseenMass] with singleton weight belonging to values the caller is no
// longer holding (§13.3, issue #3).
//
// A bounded store evicts the tail one value at a time, and the singleton rate the estimate reads
// lives there. Passing the evicted singleton weight back in is what lets a bounded store answer
// the same question an unbounded one does: without it the estimator sees a closed vocabulary
// where the truth is open, which took novelty's detections from 864 to 0 on the open-vocabulary
// corpus before this parameter existed.
//
// tailSingletons is added to the singleton weight and *not* to the total, because the total
// already counts every observation the evicted values contributed — eviction moves weight rather
// than discarding it, so counting them again would understate the rate.
func UnseenMassWithTail(history []ValueCount, alpha, tailSingletons float64) (
	mass float64, usedGoodTuring bool) {

	var (
		total      float64
		distinct   int
		singletons float64
	)
	for _, vc := range history {
		if vc.Count <= 0 {
			continue
		}
		total += vc.Count
		distinct++
		if vc.Count >= 1-singletonTolerance && vc.Count <= 1+singletonTolerance {
			singletons++
		}
	}

	dirichlet := alpha / (total + alpha*(float64(distinct)+1))
	if total < minSingletonSupport {
		return dirichlet, false
	}

	if tailSingletons > 0 {
		singletons += tailSingletons
	}
	gt := singletons / total
	switch {
	case gt <= 0:
		// Nothing was seen exactly once, so the vocabulary looks closed. That is
		// evidence of a small unseen mass, not of none at all, so the Dirichlet reserve
		// is kept as the floor rather than returning zero.
		return dirichlet, false
	case gt >= maxUnseenMass:
		return maxUnseenMass, true
	}
	// Good–Turing can fall below the Dirichlet reserve for a very settled vocabulary.
	// Taking the larger keeps the estimate conservative: reserving too little mass for
	// the unseen is what makes a first-ever value look impossible rather than merely
	// surprising.
	if gt < dirichlet {
		return dirichlet, false
	}
	return gt, true
}

const (
	// minSingletonSupport is the total observed weight below which the count-of-counts
	// cannot support an estimate and the Dirichlet reserve is used instead.
	minSingletonSupport = 30

	// singletonTolerance admits a decayed count as a singleton. Counts decay lazily and
	// are therefore non-integral; a value at 0.95 units of weight was seen once and has
	// aged slightly, and reading it as a singleton is closer to the truth than refusing
	// to.
	singletonTolerance = 0.25

	// maxUnseenMass caps the estimate. A null asserting that the next value is certainly
	// new leaves no mass for the values already observed, and equation (5)'s tail would
	// then be built on a distribution that does not sum to one.
	maxUnseenMass = 0.99
)
