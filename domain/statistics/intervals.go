// Package statistics provides the interval estimators and paired tests the evaluation
// tables require.
//
// §12.4 constrains how comparisons are reported: detection is compared at matched
// alert budget, and where a published baseline reports at a single operating point
// this work reports its own at that point. The evaluation tables add two further
// requirements of their own. Every reported proportion carries n and an interval,
// never a bare point estimate; and every comparison between two arms measured on the
// same events is tested as paired data, because treating paired measurements as
// independent samples overstates the uncertainty of their difference and understates
// the evidence for it.
//
// Everything here is deterministic. The bootstrap draws from an explicit seed
// recorded in the result file, so a rerun reproduces the interval exactly; R4
// concerns the scoring path, but a result a reader cannot reproduce is not evidence
// either.
package statistics

import "math"

// Interval is a two-sided confidence interval for a proportion.
type Interval struct {
	Point float64 `json:"point"`
	Low   float64 `json:"low"`
	High  float64 `json:"high"`
	N     int     `json:"n"`
	Level float64 `json:"level"`
	Kind  string  `json:"kind"`
}

// z975 is the standard normal 97.5th percentile, for two-sided 95% intervals.
const z975 = 1.959963984540054

// WilsonInterval returns the Wilson score interval for k successes in n trials.
//
// Wilson rather than the normal approximation because detection counts are small and
// often near the boundary: the normal interval can extend below zero or above one and
// has poor coverage exactly where these measurements live. Wilson stays inside [0,1]
// and keeps nominal coverage at small n.
func WilsonInterval(k, n int) Interval {
	iv := Interval{N: n, Level: 0.95, Kind: "wilson"}
	if n <= 0 {
		return iv
	}
	phat := float64(k) / float64(n)
	iv.Point = phat

	fn := float64(n)
	denom := 1 + z975*z975/fn
	centre := phat + z975*z975/(2*fn)
	margin := z975 * math.Sqrt(phat*(1-phat)/fn+z975*z975/(4*fn*fn))
	iv.Low = math.Max(0, (centre-margin)/denom)
	iv.High = math.Min(1, (centre+margin)/denom)
	return iv
}

// ClopperPearsonInterval returns the exact binomial interval for k successes in n
// trials, inverting the binomial test through the beta quantile.
//
// §12.4's comparisons are at matched budget, so a detection count is a binomial
// observation with a known denominator, and the exact interval is available. It is
// reported alongside Wilson where the guarantee matters more than the width: exact
// coverage is never below nominal, at the cost of being conservative.
func ClopperPearsonInterval(k, n int) Interval {
	iv := Interval{N: n, Level: 0.95, Kind: "clopper-pearson"}
	if n <= 0 {
		return iv
	}
	iv.Point = float64(k) / float64(n)
	const alpha = 0.05
	if k > 0 {
		iv.Low = betaQuantile(alpha/2, float64(k), float64(n-k+1))
	}
	if k < n {
		iv.High = betaQuantile(1-alpha/2, float64(k+1), float64(n-k))
	} else {
		iv.High = 1
	}
	return iv
}

// betaQuantile inverts the regularised incomplete beta function by bisection.
//
// Bisection rather than Newton: the domain is [0,1], sixty halvings reach 1e-18, and
// the result is deterministic and monotone in the inputs, which matters more here
// than speed. These intervals are computed once per table row.
func betaQuantile(p, a, b float64) float64 {
	if p <= 0 {
		return 0
	}
	if p >= 1 {
		return 1
	}
	lo, hi := 0.0, 1.0
	for range 200 {
		mid := (lo + hi) / 2
		if RegularisedIncompleteBeta(mid, a, b) < p {
			lo = mid
		} else {
			hi = mid
		}
	}
	return (lo + hi) / 2
}

// RegularisedIncompleteBeta returns I_x(a,b) via the continued fraction of
// Lentz, with the standard symmetry reflection for numerical stability.
//
// It is exported because the scoring path needs it as well as the evaluation tables do:
// the negative binomial upper tail is I_q(k, r), and evaluating that tail by summing
// its terms is unusable when the dispersion parameter r is small — the term ratio
// approaches one and convergence takes hundreds of thousands of iterations per event,
// which is a stalled run rather than a slow one. This continued fraction is bounded at
// 300 iterations. Being a pure deterministic function of its arguments, it satisfies R4
// wherever it is called.
func RegularisedIncompleteBeta(x, a, b float64) float64 {
	if x <= 0 {
		return 0
	}
	if x >= 1 {
		return 1
	}
	lbeta, _ := math.Lgamma(a + b)
	la, _ := math.Lgamma(a)
	lb, _ := math.Lgamma(b)
	front := math.Exp(lbeta - la - lb + a*math.Log(x) + b*math.Log(1-x))

	// The continued fraction converges quickly for x < (a+1)/(a+b+2); otherwise use
	// the reflection I_x(a,b) = 1 - I_{1-x}(b,a).
	if x > (a+1)/(a+b+2) {
		return 1 - RegularisedIncompleteBeta(1-x, b, a)
	}

	const tiny = 1e-300
	f, c, d := 1.0, 1.0, 0.0
	for i := 0; i <= 300; i++ {
		m := i / 2
		var numerator float64
		switch {
		case i == 0:
			numerator = 1
		case i%2 == 0:
			fm := float64(m)
			numerator = (fm * (b - fm) * x) / ((a + 2*fm - 1) * (a + 2*fm))
		default:
			fm := float64(m)
			numerator = -((a + fm) * (a + b + fm) * x) / ((a + 2*fm) * (a + 2*fm + 1))
		}
		d = 1 + numerator*d
		if math.Abs(d) < tiny {
			d = tiny
		}
		d = 1 / d
		c = 1 + numerator/c
		if math.Abs(c) < tiny {
			c = tiny
		}
		delta := c * d
		f *= delta
		if math.Abs(1-delta) < 1e-15 {
			break
		}
	}
	return front * (f - 1) / a
}
