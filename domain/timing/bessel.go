// Package timing implements Detector II, periodic timing (§7.2).
//
// The detector estimates an entity's time-of-day density on the 24-hour circle by von
// Mises kernel density estimation, maintained as a truncated Fourier series. The
// factors r_h = I_h(κ)/I_0(κ) of equation (7), where I_h is the modified Bessel
// function of the first kind, are the von Mises kernel's own Fourier coefficients: the
// smoothing they apply to each harmonic is derived from the kernel itself rather than
// chosen ad hoc, and the concentration κ is the single bandwidth parameter, set from
// an interpretable bandwidth in hours via equation (8).
//
// The ratio is computed directly, by backward recurrence on the consecutive ratio
// ρ_h = I_h(κ)/I_{h−1}(κ), rather than by evaluating I_h(κ) and I_0(κ) separately:
// I_h(κ) overflows float64 for κ ≥ ~713 while the ratio remains within (0, 1), and the
// estimator only ever needs the ratio.
package timing

import "math"

// KernelCoefficients returns [r_1, …, r_H], where r_h = I_h(κ)/I_0(κ) is the h-th
// Fourier coefficient of a von Mises kernel of concentration kappa (equation (7)).
//
// The consecutive ratio ρ_h = I_h(κ)/I_{h−1}(κ) satisfies ρ_h = 1/(2h/κ + ρ_{h+1}), so
// the recurrence is seeded with ρ = 0 at order H + 40 + ⌊2κ⌋ — well beyond the turning
// point h ≈ κ, past which the true ratios decay so quickly that the seed's error is
// extinguished before the recurrence reaches the retained orders — and run downwards
// to h = 1. The coefficient is then the running product r_h = Π_{j≤h} ρ_j. Every
// intermediate value stays within (0, 1), which is what makes the large-κ regime safe.
//
// kappa ≤ 0 is the uniform kernel, which has no harmonic content, so every coefficient
// is zero. H ≤ 0 yields an empty slice.
func KernelCoefficients(kappa float64, H int) []float64 {
	coefficients := make([]float64, max(H, 0))
	if kappa <= 0 || H <= 0 {
		return coefficients
	}

	seedOrder := H + 40 + int(2*kappa)
	ratios := make([]float64, H+1)
	rho := 0.0
	for h := seedOrder; h >= 1; h-- {
		rho = 1 / (2*float64(h)/kappa + rho)
		if h <= H {
			ratios[h] = rho
		}
	}

	product := 1.0
	for h := 1; h <= H; h++ {
		product *= ratios[h]
		coefficients[h-1] = product
	}
	return coefficients
}

// KappaForBandwidthHours converts an interpretable bandwidth σ, in hours, to the von
// Mises concentration κ. Equation (8) gives σ ≈ (24/2π)·κ^(−1/2), hence
// κ = ((24/(2π))/σ)². A bandwidth of zero or below carries no concentration and yields
// 0, the uniform kernel.
func KappaForBandwidthHours(sigmaHours float64) float64 {
	if sigmaHours <= 0 {
		return 0
	}
	root := (24 / (2 * math.Pi)) / sigmaHours
	return root * root
}

// HarmonicOrder returns the truncation order H for a given concentration. §7.2 states
// that H ≈ 3√(2κ) suffices for the truncation error to be negligible; the result is
// rounded up and is never below 1. kappa ≤ 0 has no harmonic content beyond the mean,
// so the minimum order of 1 is returned.
func HarmonicOrder(kappa float64) int {
	if kappa <= 0 {
		return 1
	}
	return max(int(math.Ceil(3*math.Sqrt(2*kappa))), 1)
}
