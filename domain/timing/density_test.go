package timing_test

import (
	"math"
	"testing"

	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/timing"
)

func closeTo(t *testing.T, name string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s = %.18f, want %.18f (difference %.3e)", name, got, want, got-want)
	}
}

// phase converts a clock time to the daily angle.
func phase(hours float64) float64 { return 2 * math.Pi * hours / 24 }

// ---------------------------------------------------------------------------
// The hand-computed moment fixture for equation (6).
//
// Three events, T½ = 1 day, so the discount between consecutive observations is
// exactly δ = 1/2:
//
//	day 0 at 06:00  →  φ = π/2
//	day 1 at 12:00  →  φ = π
//	day 2 at 18:00  →  φ = 3π/2
//
// Applying C_h ← δ·C_h + cos(hφ), S_h ← δ·S_h + sin(hφ), W ← δ·W + 1 by hand:
//
//	h = 1: C₁ = ½(½·0 + cos π/2 ... ) stepwise:
//	  after 06:00: C₁ = cos(π/2) = 0        S₁ = sin(π/2) = 1        W = 1
//	  after 12:00: C₁ = 0/2 + cos(π) = −1   S₁ = 1/2 + sin(π) = 1/2  W = 3/2
//	  after 18:00: C₁ = −1/2 + cos(3π/2) = −1/2
//	               S₁ = 1/4 + sin(3π/2) = −3/4                       W = 7/4
//
//	h = 2 (angles double: π, 2π, 3π):
//	  after 06:00: C₂ = cos(π) = −1          S₂ = 0
//	  after 12:00: C₂ = −1/2 + cos(2π) = 1/2 S₂ = 0
//	  after 18:00: C₂ = 1/4 + cos(3π) = −3/4 S₂ = 0
//
// The trigonometric values at these angles are 0 and ±1, so every expected moment is
// an exact dyadic rational; the only imprecision is that float64 π/2 is not exactly
// π/2, contributing ~1e-16 per step, hence the 1e-12 tolerance.
// ---------------------------------------------------------------------------

func fixtureMoments(H int) *timing.Moments {
	m := timing.NewMoments(H)
	m.Observe(phase(6), 1)    // first observation: no prior state to discount
	m.Observe(phase(12), 0.5) // one day elapsed at T½ = 1 day
	m.Observe(phase(18), 0.5)
	return m
}

func TestMomentsAgainstHandComputedFixture(t *testing.T) {
	m := fixtureMoments(2)

	closeTo(t, "C_1", m.C[0], -0.5, 1e-12)
	closeTo(t, "S_1", m.S[0], -0.75, 1e-12)
	closeTo(t, "C_2", m.C[1], -0.75, 1e-12)
	closeTo(t, "S_2", m.S[1], 0.0, 1e-12)
	closeTo(t, "W", m.W, 1.75, 1e-12)

	if got := m.Size(); got != 5 {
		t.Errorf("Size() = %d, want 2H+1 = 5", got)
	}
}

// TestStateSizeIsFixed pins the §7.2 claim that state is 2H + 1 numbers per entity
// regardless of event count, and the paper's worked example: κ ≈ 6.5, H = 11,
// twenty-three stored numbers.
func TestStateSizeIsFixed(t *testing.T) {
	kappa := timing.KappaForBandwidthHours(1.5)
	H := timing.HarmonicOrder(kappa)
	if H != 11 {
		t.Fatalf("H = %d for a 1.5-hour bandwidth, want 11", H)
	}
	m := timing.NewMoments(H)
	if m.Size() != 23 {
		t.Fatalf("stored numbers = %d, want 23", m.Size())
	}
	for i := range 10_000 {
		m.Observe(phase(float64(i%24)), 0.999)
	}
	if m.Size() != 23 {
		t.Fatalf("state grew with events: %d numbers", m.Size())
	}
}

// TestPhaseOfTimestamp: the mapping is φ(t) = 2π(t mod 24h)/24h, and adding whole days
// changes nothing, which is the periodicity the representation is built on.
func TestPhaseOfTimestamp(t *testing.T) {
	closeTo(t, "midnight", timing.PhaseOfTimestamp(0), 0, 1e-15)
	closeTo(t, "06:00", timing.PhaseOfTimestamp(6*event.Hour), math.Pi/2, 1e-12)
	closeTo(t, "18:00", timing.PhaseOfTimestamp(18*event.Hour), 3*math.Pi/2, 1e-12)
	closeTo(t, "same time next day",
		timing.PhaseOfTimestamp(6*event.Hour+3*event.Day),
		timing.PhaseOfTimestamp(6*event.Hour), 1e-12)
}

// ---------------------------------------------------------------------------
// Equation (7): structural properties of the density
// ---------------------------------------------------------------------------

// singleEventDensity: one observation at 06:00 with weight 1 makes (7) the truncated
// von Mises kernel centred at π/2, giving analytically known structure: the mode at
// the observation, symmetry about it, and monotone decay to the antipode.
func singleEventDensity(kappa float64) *timing.Density {
	H := timing.HarmonicOrder(kappa)
	m := timing.NewMoments(H)
	m.Observe(phase(6), 1)
	return timing.NewDensity(m, timing.KernelCoefficients(kappa, H))
}

func TestDensityStructure(t *testing.T) {
	kappa := timing.KappaForBandwidthHours(1.5)
	d := singleEventDensity(kappa)

	t.Run("integrates to one", func(t *testing.T) {
		// The constant term of (7) is 1/2π and every harmonic integrates to zero, so
		// the integral is exactly 1 up to the zero-clamp. Trapezoidal on the grid.
		const g = 4096
		sum := 0.0
		for i := range g {
			sum += d.Evaluate(2 * math.Pi * float64(i) / g)
		}
		// The zero-clamp can only add mass: where the truncated series ripples
		// negative near the antipode, clamping raises it to zero, contributing a few
		// parts in 1e5 at these values of kappa and H. The tolerance covers that
		// clamped ripple, not arithmetic error; the un-clamped series integrates to 1
		// exactly, since truncation removes only zero-mean harmonics.
		closeTo(t, "integral", sum*2*math.Pi/g, 1.0, 1e-4)
	})

	t.Run("mode at the observation", func(t *testing.T) {
		mode := d.Evaluate(phase(6))
		for _, h := range []float64{0, 3, 9, 12, 15, 21} {
			if d.Evaluate(phase(h)) >= mode {
				t.Errorf("density at %02.0f:00 is not below the mode at 06:00", h)
			}
		}
	})

	t.Run("symmetry about the mode", func(t *testing.T) {
		// 06:00 ± the same offset must have equal density: the kernel is even.
		for _, off := range []float64{1, 2, 3, 5.5, 9} {
			a := d.Evaluate(phase(6 + off))
			b := d.Evaluate(phase(6 - off))
			closeTo(t, "symmetry at ±"+formatHours(off), a, b, 1e-12)
		}
	})

	t.Run("monotone decay from mode to antipode", func(t *testing.T) {
		prev := d.Evaluate(phase(6))
		for _, h := range []float64{7, 8, 9, 10, 12, 14, 18} {
			cur := d.Evaluate(phase(h))
			if cur > prev+1e-12 {
				t.Errorf("density rose between offsets at %v:00: %v > %v", h, cur, prev)
			}
			prev = cur
		}
	})

	t.Run("cold start is uniform", func(t *testing.T) {
		empty := timing.NewDensity(timing.NewMoments(11), timing.KernelCoefficients(kappa, 11))
		for _, h := range []float64{0, 6, 12, 18} {
			closeTo(t, "uniform", empty.Evaluate(phase(h)), 1/(2*math.Pi), 1e-15)
		}
	})
}

func formatHours(h float64) string {
	return string(rune('0'+int(h))) + "h"
}

// ---------------------------------------------------------------------------
// Equation (9): the level-set tail mass
// ---------------------------------------------------------------------------

func TestLevelSetTailMass(t *testing.T) {
	kappa := timing.KappaForBandwidthHours(1.5)
	d := singleEventDensity(kappa)
	ix := timing.NewLevelIndex(d, timing.GridSize)

	pAt := func(hours float64) float64 { return ix.TailMass(d.Evaluate(phase(hours))) }

	t.Run("mode has mass one", func(t *testing.T) {
		// Every grid point's density is ≤ the mode's, so the whole distribution is in
		// the level set: P = 1 after normalisation.
		closeTo(t, "P(mode)", pAt(6), 1.0, 1e-12)
	})

	t.Run("cold start returns one everywhere", func(t *testing.T) {
		// §7.5: with no observations (9) returns P = 1 for every time.
		empty := timing.NewDensity(timing.NewMoments(11), timing.KernelCoefficients(kappa, 11))
		emptyIx := timing.NewLevelIndex(empty, timing.GridSize)
		for _, h := range []float64{0, 3, 12, 23} {
			closeTo(t, "P(cold)", emptyIx.TailMass(empty.Evaluate(phase(h))), 1.0, 1e-12)
		}
	})

	t.Run("monotone in the density", func(t *testing.T) {
		// Level-set mass is monotone in the level by construction; times further from
		// the mode must never score larger mass.
		prev := pAt(6)
		for _, h := range []float64{7, 8, 9, 10, 12, 16, 18} {
			cur := pAt(h)
			if cur > prev+1e-12 {
				t.Errorf("tail mass rose moving away from the mode at %v:00: %v > %v", h, cur, prev)
			}
			prev = cur
		}
	})

	t.Run("symmetric times score alike up to grid quantisation", func(t *testing.T) {
		// The densities at 03:00 and 09:00 agree to ~1e-16 (the symmetry test above),
		// but the lookup quantises mass to grid cells: two levels differing in the
		// last bit can straddle one sorted grid value and so differ by that cell's
		// mass. The §7.2 construction accepts this in exchange for a deterministic
		// binary-search lookup, so the correct expectation is agreement within a few
		// cells' mass at that level, not bit equality.
		a, b := pAt(3), pAt(9)
		if math.Abs(a-b) > 0.005 {
			t.Errorf("P(03:00) = %v vs P(09:00) = %v; differ beyond grid quantisation", a, b)
		}
	})

	t.Run("antipode is floored, never zero", func(t *testing.T) {
		// The clamped density at the antipode is 0; the lookup reports the grid's
		// resolution limit rather than a zero that (18) could not take the log of.
		got := pAt(18)
		if got <= 0 {
			t.Fatalf("P(18:00) = %v; must be strictly positive", got)
		}
		if got > ix.Floor() {
			t.Errorf("P(18:00) = %v, want the floor %v", got, ix.Floor())
		}
	})

	t.Run("deterministic rebuild", func(t *testing.T) {
		again := timing.NewLevelIndex(d, timing.GridSize)
		for _, h := range []float64{0, 5.99, 6, 6.01, 12, 17.5, 23.99} {
			a := ix.TailMass(d.Evaluate(phase(h)))
			b := again.TailMass(d.Evaluate(phase(h)))
			if math.Float64bits(a) != math.Float64bits(b) {
				t.Fatalf("rebuild changed P at %vh: %x vs %x", h,
					math.Float64bits(a), math.Float64bits(b))
			}
		}
	})
}

// ---------------------------------------------------------------------------
// §12.5: the wraparound control
// ---------------------------------------------------------------------------

// TestControlWraparound is the wraparound control of §12.5, verbatim:
//
//	"A wraparound control constructs a synthetic entity active exclusively between
//	 23:00 and 01:00 and asserts that both 23:30 and 00:30 are scored as unremarkable,
//	 and that a 12:00 event is not. The 168-cell representation fails this control by
//	 construction, since it places the entity's two activity modes at opposite extremes
//	 of the index; it is included precisely because passing it is the minimum evidence
//	 that §7.2 works."
//
// This is the test that proves the whole §7.2 argument: cos and sin do not know where
// the day begins, so activity straddling midnight is one mode, not two.
func TestControlWraparound(t *testing.T) {
	kappa := timing.KappaForBandwidthHours(1.5)
	H := timing.HarmonicOrder(kappa)

	// Fourteen nights of activity, four events per night, spread across 23:00–01:00
	// deterministically. δ between events within a night ≈ 1 at T½ = 7 days; the
	// discount is applied per observation from the true schedule.
	const halfLifeDays = 7.0
	m := timing.NewMoments(H)
	last := -1.0 // days
	for night := range 14 {
		for _, offset := range []float64{23.0, 23.5, 0.25, 0.75} {
			day := float64(night)
			if offset < 12 {
				day++ // the 00:15 and 00:45 events fall on the next calendar day
			}
			at := day + offset/24
			delta := 1.0
			if last >= 0 {
				delta = math.Exp2(-(at - last) / halfLifeDays)
			}
			m.Observe(phase(offset), delta)
			last = at
		}
	}

	d := timing.NewDensity(m, timing.KernelCoefficients(kappa, H))
	ix := timing.NewLevelIndex(d, timing.GridSize)
	pAt := func(hours float64) float64 { return ix.TailMass(d.Evaluate(phase(hours))) }

	p2330, p0030, pNoon := pAt(23.5), pAt(0.5), pAt(12)

	// Inside the habitual window: unremarkable. The bound is deliberately generous;
	// the point is categorical, not marginal.
	if p2330 < 0.20 {
		t.Errorf("P(23:30) = %v; a habitual time must be unremarkable", p2330)
	}
	if p0030 < 0.20 {
		t.Errorf("P(00:30) = %v; a habitual time must be unremarkable", p0030)
	}
	// Noon, the antipode of the activity window: strongly unusual.
	if pNoon > 0.02 {
		t.Errorf("P(12:00) = %v; the antipode of all activity must be unusual", pNoon)
	}
	// And the ordering must be categorical, not marginal.
	if pNoon*10 > math.Min(p2330, p0030) {
		t.Errorf("separation is too weak: P(12:00) = %v vs habitual %v / %v",
			pNoon, p2330, p0030)
	}

	// The two sides of midnight must look alike: one mode, not two. This is the
	// assertion a cut-circle representation cannot satisfy.
	if ratio := p2330 / p0030; ratio < 0.5 || ratio > 2 {
		t.Errorf("midnight is a seam: P(23:30) = %v vs P(00:30) = %v", p2330, p0030)
	}

	// The density's own shape agrees: exactly one mode on the whole circle, and it
	// sits inside the activity window across midnight.
	maxima := d.LocalMaxima(timing.GridSize)
	if len(maxima) != 1 {
		t.Fatalf("expected one local maximum, found %d at %v", len(maxima), maxima)
	}
	modeHours := maxima[0] * 24 / (2 * math.Pi)
	if !(modeHours >= 23 || modeHours <= 1) {
		t.Errorf("mode at %.2fh; must lie inside the 23:00–01:00 window", modeHours)
	}

	t.Logf("wraparound control: P(23:30)=%.4f P(00:30)=%.4f P(12:00)=%.6f mode=%.2fh",
		p2330, p0030, pNoon, modeHours)
}

// TestBlendIsExactConvexCombination covers §7.5: borrowing from a parent is a convex
// combination of moment vectors with w = W/(W+τ), exact because (6) is linear.
func TestBlendIsExactConvexCombination(t *testing.T) {
	entity := fixtureMoments(2) // W = 7/4
	parent := timing.NewMoments(2)
	parent.Observe(phase(9), 1) // some parent history
	parent.Observe(phase(9), 1)

	const tau = 7.0 / 4.0 // equal to W, so w = 1/2 exactly
	blend := entity.Blend(parent, tau)

	for i := range 2 {
		closeTo(t, "C blend", blend.C[i], 0.5*entity.C[i]+0.5*parent.C[i], 1e-15)
		closeTo(t, "S blend", blend.S[i], 0.5*entity.S[i]+0.5*parent.S[i], 1e-15)
	}
	closeTo(t, "W blend", blend.W, 0.5*entity.W+0.5*parent.W, 1e-15)

	// τ = 0 disables shrinkage entirely.
	same := entity.Blend(parent, 0)
	for i := range 2 {
		closeTo(t, "no shrinkage C", same.C[i], entity.C[i], 0)
	}

	// The receiver must not have been modified.
	closeTo(t, "entity untouched", entity.W, 1.75, 0)
}
