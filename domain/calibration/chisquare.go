// Package calibration implements calibration and combination (§10).
//
// The per-detector tail masses arrive here as p-values. §10.2 combines them into one
// score per event by Fisher's method, equation (18), corrected for inter-detector
// correlation by Brown's method, equation (19), with the covariance terms of Kost &
// McDermott [31]; the Šidák correction of equation (16) accounts for a minimum taken
// over multiple tests; and §10.3 controls the false discovery rate across entities by
// the step-up procedures of Benjamini–Hochberg [71] and Benjamini–Yekutieli [72].
//
// Every function here is pure and deterministic (R4): fixed summation orders, fixed
// iteration caps, no clock and no randomness. Abstaining detectors never appear in
// the inputs — R3 requires the caller to drop them, shrinking the degrees of freedom,
// rather than encode them as any placeholder score.
package calibration

import "math"

// Iteration policy for the incomplete gamma expansions. The cap is fixed so that both
// routines terminate unconditionally: should convergence not be reached, the current
// best estimate is returned rather than looping forever, keeping evaluation
// deterministic in time as well as in value (R4). At these settings both expansions
// converge well inside the cap over the whole domain the survival functions use.
const (
	gammaMaxIterations     = 500
	gammaRelativeTolerance = 1e-15
)

// ChiSquareSurvival returns Pr(X ≥ x) for X ~ χ²(k) with k ≥ 1 degrees of freedom:
// the survival function that turns the statistic of equation (18) into a combined
// p-value.
//
// The survival function is the regularised upper incomplete gamma Q(k/2, x/2).
// x ≤ 0 returns 1, since a χ² variate is non-negative. k ≤ 0 is degenerate — no
// degrees of freedom means no test was performed — and also returns 1, never a
// fabricated score.
func ChiSquareSurvival(x float64, k int) float64 {
	if k <= 0 {
		return 1
	}
	return ChiSquareSurvivalNonIntegral(x, float64(k))
}

// ChiSquareSurvivalNonIntegral is ChiSquareSurvival for a real-valued number of
// degrees of freedom, evaluated as Q(df/2, x/2) directly — the gamma machinery below
// is defined for real a throughout. Brown's method needs it: the effective degrees of
// freedom f of equation (19) is generally non-integral, and rounding it would discard
// exactly the correction the method exists to apply.
//
// x ≤ 0 or df ≤ 0 return 1, as for ChiSquareSurvival.
func ChiSquareSurvivalNonIntegral(x float64, df float64) float64 {
	if df <= 0 || x <= 0 {
		return 1
	}
	return regularisedUpperGamma(df/2, x/2)
}

// regularisedUpperGamma evaluates Q(a, x) = Γ(a, x)/Γ(a) for a > 0, x > 0 by the
// standard split at x = a + 1: below it, the power series for the lower P(a, x)
// converges fastest and Q = 1 − P; at or above it, the continued fraction for Q
// itself does. The shared prefactor x^a e^{−x}/Γ(a) is assembled in log space via
// math.Lgamma, so deep tails come out as accurate small numbers rather than 0/∞ —
// at (a, x) = (50, 350), Γ(a) alone overflows float64 while Q ≈ 8.7e−91 is
// perfectly representable.
func regularisedUpperGamma(a, x float64) float64 {
	if x < a+1 {
		return 1 - lowerGammaSeries(a, x)
	}
	return upperGammaContinuedFraction(a, x)
}

// lowerGammaSeries evaluates the regularised lower incomplete gamma
//
//	P(a, x) = x^a e^{−x}/Γ(a) · Σ_{n≥0} xⁿ / (a(a+1)⋯(a+n))
//
// for x < a + 1, where successive terms shrink geometrically. Iteration stops when a
// term falls below gammaRelativeTolerance of the running sum, or at the fixed cap,
// whichever comes first; the partial sum at that point is the returned best estimate.
func lowerGammaSeries(a, x float64) float64 {
	logGamma, _ := math.Lgamma(a)
	term := 1 / a
	sum := term
	factor := a
	for range gammaMaxIterations {
		factor++
		term *= x / factor
		sum += term
		if math.Abs(term) < math.Abs(sum)*gammaRelativeTolerance {
			break
		}
	}
	return sum * math.Exp(a*math.Log(x)-x-logGamma)
}

// upperGammaContinuedFraction evaluates Q(a, x) for x ≥ a + 1 by the continued
// fraction
//
//	Q(a, x) = x^a e^{−x}/Γ(a) · 1/(x+1−a − 1·(1−a)/(x+3−a − 2·(2−a)/(x+5−a − ⋯)))
//
// with the modified Lentz algorithm. Denominators that would vanish are floored at a
// tiny positive value, the standard Lentz guard; iteration stops when a step of the
// running product is within gammaRelativeTolerance of one, or at the fixed cap, the
// value so far being the returned best estimate.
func upperGammaContinuedFraction(a, x float64) float64 {
	logGamma, _ := math.Lgamma(a)
	return upperGammaContinuedFractionFactor(a, x) * math.Exp(a*math.Log(x)-x-logGamma)
}

// upperGammaContinuedFractionFactor is the continued fraction itself, without the
// x^a e^{−x}/Γ(a) prefactor.
//
// It is separated so that the log-space survival function can add the prefactor's
// logarithm instead of multiplying by its value. The factor is of order 1/x, so it
// never underflows; the prefactor is what collapses to zero in the deep tail, and in
// log space it is simply a sum.
func upperGammaContinuedFractionFactor(a, x float64) float64 {
	const lentzFloor = 1e-300
	b := x + 1 - a
	c := 1 / lentzFloor
	d := 1 / b
	h := d
	for i := 1; i <= gammaMaxIterations; i++ {
		coefficient := -float64(i) * (float64(i) - a)
		b += 2
		d = coefficient*d + b
		if math.Abs(d) < lentzFloor {
			d = lentzFloor
		}
		c = b + coefficient/c
		if math.Abs(c) < lentzFloor {
			c = lentzFloor
		}
		d = 1 / d
		step := d * c
		h *= step
		if math.Abs(step-1) < gammaRelativeTolerance {
			break
		}
	}
	return h
}

// ChiSquareLogSurvival is the natural logarithm of ChiSquareSurvival.
//
// It exists because the survival function underflows. Ranking events by how extreme
// they are means comparing tails, and past roughly X² = 1450 at 2 degrees of freedom
// the tail is smaller than the least positive float64 and every event from there
// downwards reports exactly zero. Those events are then indistinguishable: on a corpus
// of tens of millions, the most extreme tens of thousands all collapse to the same
// value and any ordering among them is decided by whatever the tie-break happens to be,
// not by evidence. Measured on LANL days 7–13, every one of the 1,400 retained alerts
// had a combined p-value of exactly zero, while labelled attack events sat at p = 1e−274
// — representable, vastly less extreme, and unable to compete.
//
// The logarithm has no such limit. ln Q reaches −745 where Q reaches the smallest
// normal float, and continues smoothly to −1e5 and beyond, so the ordering survives as
// far as the arithmetic that produced X² is meaningful.
//
// Returns 0 (that is, ln 1) for x ≤ 0 or k ≤ 0, matching ChiSquareSurvival's 1.
func ChiSquareLogSurvival(x float64, k int) float64 {
	if k <= 0 {
		return 0
	}
	return ChiSquareLogSurvivalNonIntegral(x, float64(k))
}

// ChiSquareLogSurvivalNonIntegral is ChiSquareLogSurvival for a real-valued number of
// degrees of freedom, which Brown's correction requires.
func ChiSquareLogSurvivalNonIntegral(x float64, df float64) float64 {
	if df <= 0 || x <= 0 {
		return 0
	}
	return regularisedUpperGammaLog(df/2, x/2)
}

// regularisedUpperGammaLog is ln Q(a, x).
//
// Below the split the upper tail is not small — Q ≥ Q(a, a+1), which stays well clear
// of underflow — so its logarithm is taken directly from the same series the linear
// path uses, and the two agree exactly. At or above the split the prefactor is added in
// log space rather than exponentiated, which is where the range is won.
func regularisedUpperGammaLog(a, x float64) float64 {
	if x < a+1 {
		q := 1 - lowerGammaSeries(a, x)
		if q <= 0 {
			// The series has lost all significance against 1. Fall through to the
			// continued fraction, which is accurate exactly where this is not.
			return upperGammaContinuedFractionLog(a, x)
		}
		return math.Log(q)
	}
	return upperGammaContinuedFractionLog(a, x)
}

func upperGammaContinuedFractionLog(a, x float64) float64 {
	logGamma, _ := math.Lgamma(a)
	return math.Log(upperGammaContinuedFractionFactor(a, x)) + a*math.Log(x) - x - logGamma
}
