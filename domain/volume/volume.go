// Package volume implements the volume half of Detector II (§7.4).
//
// Timing answers when; volume answers how much. The entity's total event rate μ
// carries a Gamma(a, b) posterior updated by the same power discounting as §7.2
// (equation (10)), estimated from all of the entity's events rather than the small
// fraction falling in one cell. For a window Ω with expected activity fraction
// ρ(Ω) = ∫_Ω f̂(φ) dφ, the null H₀ is that the count observed in Ω is drawn from the
// entity's own predictive: K | μ ~ Poisson(μ·ρ) with μ ~ Gamma(a, b), and
// marginalising μ gives the negative binomial of equation (11).
//
// Structural overdispersion is preserved: Var[K]/E[K] = (b+ρ)/b > 1 for all
// b, ρ > 0, so the model remains necessarily overdispersed relative to Poisson,
// which matters because empirical activity counts exhibit variance exceeding the
// mean and a Poisson null would over-reject (§7.4).
package volume

import (
	"math"

	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/statistics"
)

// GammaPosterior is the rate posterior of equation (10): shape a and rate b, updated
// per period by a ← δ·a + k, b ← δ·b + 1 with δ = 2^(−Δt/T½).
//
// a is a shape parameter and generally non-integral, which is why equation (11) is
// evaluated through the log-gamma function rather than factorials.
type GammaPosterior struct {
	A float64
	B float64
}

// Observe folds one period's count k into the posterior with discount delta.
//
// The caller computes delta from event time only (the period's distance from the
// previous observation, over T½); this package consults no clock (R4). Called from
// the Observe half of §5.2, strictly after scoring.
func (g *GammaPosterior) Observe(k, delta float64) {
	g.A = delta*g.A + k
	g.B = delta*g.B + 1
}

// Mean is the posterior mean rate a/b, part of the §7.7 evidence.
func (g GammaPosterior) Mean() float64 {
	if g.B <= 0 {
		return 0
	}
	return g.A / g.B
}

// HalfLife re-exports the decay unit for callers wiring (10) to a corpus timescale.
type HalfLife = event.Timestamp

// UpperTail evaluates the p-value of §7.4: Pr(K ≥ kObs) under the negative binomial
// predictive of equation (11),
//
//	Pr(K = k) = Γ(k+a) / (k! Γ(a)) · (b/(b+ρ))^a · (ρ/(b+ρ))^k
//
// with kObs the running count including the event being scored.
//
// The tail is summed upward from kObs directly, rather than as 1 − lower tail, so a
// small tail is not lost to cancellation; the first term is computed in log space
// through math.Lgamma, since a is generally non-integral, and subsequent terms follow
// the exact ratio Pr(k+1)/Pr(k) = (k+a)/(k+1) · ρ/(b+ρ). Summation runs in a fixed
// ascending order and stops when a term no longer changes the accumulator, so the
// result is deterministic (R4).
//
// Degenerate inputs take their limiting values: kObs ≤ 0 gives 1 (every count is at
// least zero); ρ ≤ 0 gives 1 for any kObs ≤ 0 and, for kObs > 0, 1 as well, because a
// window expected to contain none of the entity's activity offers no grounds to call
// any observed count surprising under the entity's own predictive — the detector
// abstains from windows with no expected mass rather than fabricating certainty, per
// R3. Invalid posteriors (a ≤ 0 or b ≤ 0, the no-history state) give 1: with no
// evidence, no count is anomalous, matching the cold-start convention of §6.2 and
// §7.5.
func UpperTail(a, b, rho float64, kObs int) float64 {
	if kObs <= 0 || a <= 0 || b <= 0 || rho <= 0 {
		return 1
	}

	logRatioBase := math.Log(rho / (b + rho))

	// log Pr(K = kObs), via log-gamma.
	k0 := float64(kObs)
	lg1, _ := math.Lgamma(k0 + a)
	lg2, _ := math.Lgamma(k0 + 1)
	lg3, _ := math.Lgamma(a)
	logTerm := lg1 - lg2 - lg3 + a*math.Log(b/(b+rho)) + k0*logRatioBase

	term := math.Exp(logTerm)
	sum := term

	// Ascending k, exact term ratio, stop at convergence. The ratio tends to
	// ρ/(b+ρ) < 1, so convergence is geometric.
	ratioBase := rho / (b + rho)
	for k := k0; ; k++ {
		term *= (k + a) / (k + 1) * ratioBase
		next := sum + term
		if next == sum {
			break
		}
		sum = next
	}

	if sum > 1 {
		return 1
	}
	if sum <= 0 {
		// The true tail is positive but below float resolution; report the smallest
		// positive normal rather than zero, which (18) could not take the log of.
		return math.SmallestNonzeroFloat64
	}
	return sum
}

// MinDispersionWindows is the number of completed windows below which a measured
// dispersion is not trusted. Five is the smallest sample from which a variance-to-mean
// ratio says anything at all; below it the estimate is dominated by whichever window
// happened to be first.
//
// Below it the detector ABSTAINS. It previously fell back to equation (11) exactly, which
// was the wrong direction to fail in: (11) un-widened is the narrowest null the arm has, so
// an entity whose dispersion could not be measured was scored against the null least able
// to tolerate its ordinary variation. See [DispersionReachable] for who that hit.
const MinDispersionWindows = 5

// DispersionReachable reports whether an entity whose active windows are gapHours apart can
// ever accumulate [MinDispersionWindows] of discounted weight, given the window half-life.
//
// It can not, past a certain sparsity, and that was the defect behind the sub-1e-12 pile.
// The accumulator is discounted by elapsed calendar hours, so its weight saturates at
// 1/(1-delta) with delta = 2^(-gapHours/halfLifeHours) -- and for gaps of about three days
// or more at a seven-day half-life the ceiling falls below five. Such an entity could never
// measure its own dispersion however long it was observed, was therefore scored under the
// un-widened null forever, and a single ordinary burst then scored many orders of magnitude
// into the tail. Measured on synthetic benign accounts: a burst every four days put 31.6% of
// its own events below 1e-12, and every seven days 41.7%, with p reaching 1e-45.
//
// This is not a tuning constant to be lowered until the symptom goes. A minimum above the
// reachable ceiling is unsatisfiable by construction, so the arm must either abstain -- which
// is what it now does -- or estimate the dispersion on a timescale the entity actually acts
// on. The second is the better repair and is not attempted here.
func DispersionReachable(gapHours, halfLifeHours float64) bool {
	if gapHours <= 0 || halfLifeHours <= 0 {
		return true
	}
	delta := math.Exp2(-gapHours / halfLifeHours)
	if delta >= 1 {
		return true
	}
	return 1/(1-delta) >= MinDispersionWindows
}

// DispersionMeasurable reports whether the accumulated window weight supports an estimate.
// The detector consults it to decide between scoring and abstaining.
func DispersionMeasurable(windows float64) bool { return windows >= MinDispersionWindows }

// Dispersion returns the entity's measured Pearson dispersion φ̂, the discounted mean
// of (k − m)² / m over completed windows, where m is the count equation (11) expected
// of each window at the time it opened.
//
// # Why this exists
//
// Equation (11)'s overdispersion, Var/E = (b+ρ)/b, expresses uncertainty about the rate
// μ, and that uncertainty shrinks as history accumulates: with T½ = 7 days the
// discounted period count settles at b ≈ 10.6, so Var/E ≤ 1.09 and the predictive is
// Poisson in all but name. Real telemetry is not Poisson — events arrive in sessions —
// so the null is misspecified in the one direction §7.4 explicitly set out to avoid,
// and the detector rejects entities for their own habitual behaviour.
//
// φ̂ measures the misspecification from the entity's own history instead of assuming it
// away. It is a Pearson statistic: under a correctly specified null its expectation is
// 1, and its excess over 1 is the factor by which real window counts scatter more than
// the model predicts. It is floored at 1 so the correction can only ever widen the null,
// never sharpen it — a detector must not become anti-conservative through a repair.
//
// Only windows that contained at least one event contribute, because a window with no
// events is never scored and so has no recorded expectation. Conditioning on activity
// biases φ̂ upward, which is conservative and is the direction to err in.
func Dispersion(sum, windows float64) float64 {
	if windows < MinDispersionWindows || sum <= 0 {
		return 1
	}
	if phi := sum / windows; phi > 1 {
		return phi
	}
	return 1
}

// UpperTailDispersed evaluates Pr(K ≥ kObs) under the negative binomial with the mean
// equation (11) prescribes and variance φ·mean, the dispersion measured from the
// entity's own completed windows.
//
// The negative binomial is closed under this reparameterisation: Var = m(1 + m/r) = φm
// gives r = m/(φ−1), so the predictive is NB(r, m).
//
// # Why this is not evaluated by UpperTail's summation
//
// It could be — NB(r, m) is what UpperTail computes with a = r, b = r and ρ = m — and
// doing so stalls a run rather than slowing it. UpperTail sums terms whose ratio tends
// to ρ/(b+ρ), which for equation (11) proper is at most about 0.09 because b ≈ 10.6 and
// ρ ≤ 1, so a handful of terms suffice. Here the ratio is m/(r+m), and a genuinely
// bursty entity has a large φ, hence a small r, hence a ratio approaching one: at
// m = 1000 and φ = 1000 the ratio is 0.999 and convergence needs of the order of 40,000
// terms, for every event. A replay measured this directly — throughput fell from
// ~200,000 rows a minute to ~600.
//
// The closed form has no such regime. For the negative binomial,
//
//	Pr(K ≥ k) = I_q(k, r),   q = m/(r+m)
//
// which follows from Pr(K ≤ k−1) = I_{1−q}(r, k) and I_x(a,b) = 1 − I_{1−x}(b,a). The
// continued fraction evaluating it is bounded at 300 iterations regardless of r.
//
// The result is guarded into (0, 1] on the same reasoning as UpperTail: a tail below
// float resolution is reported as the smallest positive normal rather than zero, which
// equation (18) could not take the logarithm of.
func UpperTailDispersed(mean, phi float64, kObs int) float64 {
	if kObs <= 0 || mean <= 0 || phi <= 1 {
		return 1
	}
	r := mean / (phi - 1)
	p := statistics.RegularisedIncompleteBeta(mean/(r+mean), float64(kObs), r)
	if p <= 0 {
		return math.SmallestNonzeroFloat64
	}
	if p > 1 {
		return 1
	}
	return p
}

// PredictiveMoments returns E[K] and Var[K] under equation (11), for evidence and for
// the overdispersion property Var/E = (b+ρ)/b.
func PredictiveMoments(a, b, rho float64) (mean, variance float64) {
	if a <= 0 || b <= 0 || rho <= 0 {
		return 0, 0
	}
	mean = a * rho / b
	variance = mean * (b + rho) / b
	return mean, variance
}
