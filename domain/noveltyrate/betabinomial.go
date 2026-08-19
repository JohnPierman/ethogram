package noveltyrate

import "math"

// LogUpperTail returns ln P(K ≥ k) for K ~ BetaBinomial(m, a, b).
//
// # Why a Beta-binomial and not a binomial
//
// The rate θ at which an entity produces first-ever values is estimated from its own
// history, and for a quiet account that estimate is worth very little. A binomial tail
// would treat θ̂ as known and call a second novel value from an account with twenty
// events of history overwhelming. Carrying the Beta posterior over θ instead spreads the
// predictive, so an account is judged extreme only in proportion to how well its own rate
// is actually pinned down.
//
// # Why log space
//
// This is the fourth place in this codebase where a tail underflows float64 — the
// combination met it at X² ≈ 1450, volume in the negative-binomial tail, Detector III at
// the verdict boundary. Forty first-ever values in an hour from an account that produces
// one a week lands far below the least positive float64, and every event past that point
// would tie with every other. Ties cannot be undone downstream: conformal calibration
// maps ties to ties and a minimum over tied values is a tie. So the logarithm is the
// value here, and any p-value is a reading of it.
//
// The sum is taken over whichever tail is shorter. For the extreme case that matters —
// k near m — the upper sum is a handful of terms. For k below the predictive mean the
// upper tail is near one and is computed as the complement of the short lower sum, which
// costs k terms rather than m − k.
func LogUpperTail(k, m int, a, b float64) float64 {
	switch {
	case k <= 0:
		return 0 // P(K ≥ 0) = 1
	case k > m:
		return math.Inf(-1)
	case m == 0:
		return 0
	}

	// The predictive mean of K is m·a/(a+b). Above it the upper tail is the short sum;
	// at or below it the lower tail is, and the complement is numerically the safer of
	// the two because the answer there is O(1) rather than a small number.
	if float64(k) <= float64(m)*a/(a+b) {
		lower := logSumRange(0, k-1, m, a, b)
		if lower >= 0 {
			// Rounding put the lower tail at or above one; the upper tail is then
			// below anything float64 distinguishes from zero, but it is not
			// impossible, so it is floored rather than reported as −∞.
			return math.Log(math.SmallestNonzeroFloat64)
		}
		return math.Log1p(-math.Exp(lower))
	}
	return logSumRange(k, m, m, a, b)
}

// logSumRange returns ln Σ_{i=lo..hi} P(K = i), by log-sum-exp so that no individual
// term has to be representable.
func logSumRange(lo, hi, m int, a, b float64) float64 {
	if lo > hi {
		return math.Inf(-1)
	}
	maxLog := math.Inf(-1)
	terms := make([]float64, 0, hi-lo+1)
	for i := lo; i <= hi; i++ {
		t := logPMF(i, m, a, b)
		terms = append(terms, t)
		if t > maxLog {
			maxLog = t
		}
	}
	if math.IsInf(maxLog, -1) {
		return math.Inf(-1)
	}
	var sum float64
	for _, t := range terms {
		sum += math.Exp(t - maxLog)
	}
	return maxLog + math.Log(sum)
}

// logPMF returns ln P(K = k) for K ~ BetaBinomial(m, a, b):
//
//	ln C(m,k) + ln B(a+k, b+m−k) − ln B(a, b)
func logPMF(k, m int, a, b float64) float64 {
	if k < 0 || k > m {
		return math.Inf(-1)
	}
	return logChoose(m, k) + logBeta(a+float64(k), b+float64(m-k)) - logBeta(a, b)
}

func logChoose(n, k int) float64 {
	ln, _ := math.Lgamma(float64(n) + 1)
	lk, _ := math.Lgamma(float64(k) + 1)
	lnk, _ := math.Lgamma(float64(n-k) + 1)
	return ln - lk - lnk
}

func logBeta(a, b float64) float64 {
	la, _ := math.Lgamma(a)
	lb, _ := math.Lgamma(b)
	lab, _ := math.Lgamma(a + b)
	return la + lb - lab
}
