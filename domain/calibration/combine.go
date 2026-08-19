package calibration

import (
	"errors"
	"fmt"
	"math"
)

// ErrNoEvaluatedVerdicts is returned when combination is asked for zero p-values.
// Under R3 an event on which every detector abstained has no score at all: the
// system reports no opinion; it does not report a null score.
var ErrNoEvaluatedVerdicts = errors.New(
	"the system reports no opinion; it does not report a null score")

// Fisher combines p-values by Fisher's method, equation (18):
//
//	X² = −2 Σ_{i=1..J} ln P_i  ~  χ²(2J) under H₀,   J = len(pValues)
//
// returning the statistic, its degrees of freedom 2J, and the combined tail
// Pr(χ²(2J) ≥ X²).
//
// # Summation order
//
// Floating-point addition is not associative, so the summation order is part of the
// answer. Fisher sums in the slice order given and does not sort: callers pass
// canonically-ordered inputs, and E8's byte-identical replays hold because the same
// canonical order arrives on every evaluation. Sorting here would hide the caller's
// ordering obligation rather than discharge it.
//
// # Abstention (R3)
//
// Every p-value must lie in (0, 1]. Zero, negative values, values above one and NaN
// are rejected with an error: an abstaining detector must be dropped by the caller
// before this call — reducing J, and with it the degrees of freedom — never encoded
// as a placeholder value. An empty slice returns ErrNoEvaluatedVerdicts.
func Fisher(pValues []float64) (x2 float64, degreesOfFreedom int, tail float64, err error) {
	if len(pValues) == 0 {
		return 0, 0, 0, ErrNoEvaluatedVerdicts
	}
	logSum := 0.0
	for i, p := range pValues {
		if math.IsNaN(p) || p <= 0 || p > 1 {
			return 0, 0, 0, fmt.Errorf(
				"combining p-values: pValues[%d] = %v is outside (0, 1]; "+
					"abstentions must be dropped by the caller, not encoded (R3)", i, p)
		}
		logSum += math.Log(p)
	}
	x2 = -2 * logSum
	degreesOfFreedom = 2 * len(pValues)
	return x2, degreesOfFreedom, ChiSquareSurvival(x2, degreesOfFreedom), nil
}

// Brown applies Brown's correction, equation (19), to Fisher's statistic for
// correlated detectors. With X² as in equation (18) and J = len(pValues):
//
//	E[X²]   = 2J
//	Var[X²] = 4J + 2 Σ_{i<j} cov(−2 ln P_i, −2 ln P_j)
//	c = Var/(2E),   f = 2E²/Var
//
// and the combined tail is Pr(χ²(f) ≥ X²/c), evaluated for the generally
// non-integral f by ChiSquareSurvivalNonIntegral.
//
// covariance supplies the pairwise cov(−2 ln P_i, −2 ln P_j) terms, typically from
// KostMcDermott. Only the strict upper triangle i < j is read, in fixed row-major
// order, so the accumulation is one fixed float sum (R4). When supplied, the matrix
// must be J×J; anything else is rejected with an error.
//
// A nil covariance, or an all-zero one, reduces exactly to Fisher: the moment sums
// give c = 1 and f = 2J exactly in float arithmetic, and the tail is bit-identical
// to Fisher's — §10.2: the correction degrades to Fisher rather than to failure.
// The reduction runs through the general path, not a special case, so the exactness
// falls out of the arithmetic rather than being asserted.
//
// A covariance so negative that Var[X²] ≤ 0 cannot arise from any real joint
// distribution of the statistics and is rejected with an error.
func Brown(pValues []float64, covariance [][]float64) (x2 float64, c float64, f float64, tail float64, err error) {
	var degreesOfFreedom int
	x2, degreesOfFreedom, _, err = Fisher(pValues)
	if err != nil {
		return 0, 0, 0, 0, err
	}

	j := len(pValues)
	covarianceSum := 0.0
	if covariance != nil {
		if len(covariance) != j {
			return 0, 0, 0, 0, fmt.Errorf(
				"brown correction: covariance has %d rows, want %d", len(covariance), j)
		}
		for row := 0; row < j; row++ {
			if len(covariance[row]) != j {
				return 0, 0, 0, 0, fmt.Errorf(
					"brown correction: covariance row %d has %d columns, want %d",
					row, len(covariance[row]), j)
			}
			for col := row + 1; col < j; col++ {
				covarianceSum += covariance[row][col]
			}
		}
	}

	expected := float64(degreesOfFreedom) // E[X²] = 2J
	variance := 2*expected + 2*covarianceSum
	if variance <= 0 {
		return 0, 0, 0, 0, fmt.Errorf(
			"brown correction: Var[X²] = %v is not positive; "+
				"the supplied covariance is not realisable", variance)
	}

	c = variance / (2 * expected)
	f = 2 * expected * expected / variance
	return x2, c, f, ChiSquareSurvivalNonIntegral(x2/c, f), nil
}

// KostMcDermott returns the polynomial approximation of Kost & McDermott (2002), as
// cited at [31], for the covariance term Brown's correction needs:
//
//	cov(−2 ln P_i, −2 ln P_j) ≈ 3.263ρ + 0.710ρ² + 0.027ρ³
//
// where rho is the correlation of the underlying statistics. rho is clamped to
// [−1, 1] first: a correlation cannot lie outside that interval, and the polynomial
// was fitted only within it.
func KostMcDermott(rho float64) float64 {
	clamped := math.Min(math.Max(rho, -1), 1)
	return 3.263*clamped + 0.710*clamped*clamped + 0.027*clamped*clamped*clamped
}

// FisherLog is [Fisher] taking ln P directly: X² = −2 Σ ln P_i, with the same fixed
// summation order and the same reference χ²(2J).
//
// It exists because taking the logarithm inside Fisher throws away everything a detector
// computed below the least positive float64. Detector III's tail reaches ln P ≈ −4000 on
// this corpus; passed as a linear p it arrives floored at 5e−324, and Fisher reads it as
// ln P = −744. The statistic is then wrong by thousands, identically for every event past
// the floor, which is the tie that ranking cannot survive.
//
// Every logP must be ≤ 0 and finite. An abstaining detector must be dropped by the caller
// before this call, never encoded (R3); an empty slice returns ErrNoEvaluatedVerdicts.
func FisherLog(logPValues []float64) (x2 float64, degreesOfFreedom int, logTail float64, err error) {
	if len(logPValues) == 0 {
		return 0, 0, 0, ErrNoEvaluatedVerdicts
	}
	logSum := 0.0
	for i, lp := range logPValues {
		if math.IsNaN(lp) || math.IsInf(lp, 0) || lp > 0 {
			return 0, 0, 0, fmt.Errorf(
				"combining p-values: logPValues[%d] = %v is not a finite ln p in (-inf, 0]; "+
					"abstentions must be dropped by the caller, not encoded (R3)", i, lp)
		}
		logSum += lp
	}
	x2 = -2 * logSum
	degreesOfFreedom = 2 * len(logPValues)
	return x2, degreesOfFreedom, ChiSquareLogSurvival(x2, degreesOfFreedom), nil
}

// BrownFromStatistic applies equation (19) to an already-computed X² and J, so that a
// caller holding log p-values can correct them without round-tripping through a linear
// p that would floor. The covariance handling, the c and f moments and the degradation
// to Fisher when the covariance is nil or zero are exactly [Brown]'s.
func BrownFromStatistic(x2 float64, j int, covariance [][]float64) (c float64, f float64, logTail float64, err error) {
	if j <= 0 {
		return 0, 0, 0, ErrNoEvaluatedVerdicts
	}
	covarianceSum := 0.0
	if covariance != nil {
		if len(covariance) != j {
			return 0, 0, 0, fmt.Errorf(
				"brown correction: covariance has %d rows, want %d", len(covariance), j)
		}
		for row := range j {
			if len(covariance[row]) != j {
				return 0, 0, 0, fmt.Errorf(
					"brown correction: covariance row %d has %d columns, want %d",
					row, len(covariance[row]), j)
			}
			for col := row + 1; col < j; col++ {
				covarianceSum += covariance[row][col]
			}
		}
	}
	expectation := 2 * float64(j)
	variance := 4*float64(j) + 2*covarianceSum
	if variance <= 0 {
		return 0, 0, 0, fmt.Errorf(
			"brown correction: Var[X2] = %v is not positive; no joint distribution of the "+
				"statistics can produce it", variance)
	}
	c = variance / (2 * expectation)
	f = 2 * expectation * expectation / variance
	return c, f, ChiSquareLogSurvivalNonIntegral(x2/c, f), nil
}

// SidakLog is [Sidak] in log space: ln(1 − (1 − minP)^T) from ln(minP).
//
// It exists because Sidak's result underflows over exactly the range worth correcting.
// A minimum of 1e−300 corrected across five tests is 5e−300, which float64 can hold, but
// one of 1e−320 is not, and Sidak floors it at the smallest positive float64 — turning
// every extreme event into the same number, which is the tie that ranking cannot survive.
// The combination of §10.2 has already been moved to log space for that reason; this is
// the same move for the multiplicity correction.
//
// The exact form is used wherever it is usable, which is further than it first appears:
// log1p and expm1 are built for arguments near zero, so 1 − (1 − minP)^T stays accurate
// down to a minimum around 1e−320. What fails first is not the correction but exp itself,
// which underflows below ln p ≈ −745 and would hand the exact form a zero. Below that
// threshold the correction is ln T + ln minP to within O(minP) — which is also the
// readable statement of what Šidák does to a rank, charging the logarithm of the number
// of chances taken.
//
// tests ≤ 1 returns ln minP unchanged, and a minimum at or above one returns 0 = ln 1.
func SidakLog(logMinP float64, tests int) float64 {
	if tests <= 1 {
		return logMinP
	}
	if logMinP >= 0 {
		return 0
	}
	if logMinP > -700 {
		minP := math.Exp(logMinP)
		return math.Log(-math.Expm1(float64(tests) * math.Log1p(-minP)))
	}
	return math.Log(float64(tests)) + logMinP
}

// Sidak applies the Šidák correction of equation (16) to the smallest of T
// p-values: P = 1 − (1 − minP)^T. It is evaluated as −expm1(T·log1p(−minP)), which
// keeps full relative precision when minP is tiny — the direct form rounds 1 − minP
// to 1 and returns 0, destroying exactly the small p-values worth correcting.
//
// tests ≤ 1 means no multiplicity, so minP is returned unchanged; minP ≥ 1 returns
// exactly 1. The result is clamped into (0, 1]: a probability cannot exceed one,
// and a zero would poison equation (18), whose logarithm it feeds, so a
// non-positive result is floored at the smallest positive float64 rather than
// returned as an impossible zero.
func Sidak(minP float64, tests int) float64 {
	if tests <= 1 {
		return minP
	}
	if minP >= 1 {
		return 1
	}
	corrected := -math.Expm1(float64(tests) * math.Log1p(-minP))
	if corrected > 1 {
		return 1
	}
	if corrected <= 0 {
		return math.SmallestNonzeroFloat64
	}
	return corrected
}
