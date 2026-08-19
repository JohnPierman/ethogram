package volume_test

import (
	"math"
	"testing"

	"github.com/JohnPierman/ethogram/domain/volume"
)

func closeTo(t *testing.T, name string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s = %.18f, want %.18f (difference %.3e)", name, got, want, got-want)
	}
}

// ---------------------------------------------------------------------------
// Equation (11), hand-computed fixtures.
//
// Integral shape, a = 2, b = 1, ρ = 1:
//
//	Pr(K = k) = Γ(k+2)/(k!·Γ(2)) · (1/2)² · (1/2)^k = (k+1)/4 · (1/2)^k
//
// so Pr(0..3) = 1/4, 1/4, 3/16, 1/8, and the check Σ_k Pr(k) = (1/4)·1/(1−½)² = 1.
// Upper tails, exactly:
//
//	Pr(K ≥ 0) = 1,  Pr(K ≥ 1) = 3/4,  Pr(K ≥ 2) = 1/2,
//	Pr(K ≥ 3) = 5/16,  Pr(K ≥ 4) = 3/16
//
// Non-integral shape, a = 1/2, b = 1, ρ = 1 — the case §7.4 names as the reason to
// evaluate through log-gamma:
//
//	Pr(K = 0) = (1/2)^(1/2) = √2/2
//	Pr(K = 1) = Γ(3/2)/Γ(1/2) · (1/2)^(1/2) · (1/2) = √2/8
//	Pr(K ≥ 1) = 1 − √2/2,   Pr(K ≥ 2) = 1 − 5√2/8
// ---------------------------------------------------------------------------

func TestUpperTailAgainstHandComputedFixture(t *testing.T) {
	t.Run("integral shape a = 2", func(t *testing.T) {
		for _, tc := range []struct {
			kObs int
			want float64
		}{
			{0, 1},
			{1, 3.0 / 4.0},
			{2, 1.0 / 2.0},
			{3, 5.0 / 16.0},
			{4, 3.0 / 16.0},
		} {
			got := volume.UpperTail(2, 1, 1, tc.kObs)
			closeTo(t, "Pr(K >= kObs)", got, tc.want, 1e-14)
		}
	})

	t.Run("non-integral shape a = 1/2", func(t *testing.T) {
		root := math.Sqrt2 / 2
		for _, tc := range []struct {
			kObs int
			want float64
		}{
			{0, 1},
			{1, 1 - root},
			{2, 1 - 5*math.Sqrt2/8},
		} {
			got := volume.UpperTail(0.5, 1, 1, tc.kObs)
			closeTo(t, "Pr(K >= kObs)", got, tc.want, 1e-14)
		}
	})

	t.Run("rho = 1 reduces to the whole-period model", func(t *testing.T) {
		// §7.4: the window being the entire period is the whole-period count model.
		// Same distribution as the first fixture by construction; the assertion is
		// that ρ enters only through b+ρ and ρ/(b+ρ), so scaling both b and ρ leaves
		// the success fraction unchanged but shifts the size: check a known identity
		// instead of duplicating the table — Pr(K ≥ 1) = 1 − (b/(b+ρ))^a.
		for _, rho := range []float64{0.25, 0.5, 1, 2} {
			got := volume.UpperTail(2, 1, rho, 1)
			base := 1 / (1 + rho)
			want := 1 - base*base
			closeTo(t, "Pr(K >= 1)", got, want, 1e-14)
		}
	})
}

// TestUpperTailDeepTailHasNoCancellation: summing upward from kObs keeps a tiny tail
// accurate where 1 − lower would return exactly 0 from cancellation.
func TestUpperTailDeepTailHasNoCancellation(t *testing.T) {
	p := volume.UpperTail(2, 1, 1, 200)
	if p <= 0 {
		t.Fatalf("deep tail collapsed to %v; upward summation must preserve it", p)
	}
	if p > 1e-50 {
		t.Fatalf("deep tail = %v; expected a very small positive value", p)
	}
	// Exact for a = 2: Pr(K ≥ k) = (k/2 + 1)·(1/2)^k.
	want := (float64(200)/2 + 1) * math.Pow(0.5, 200)
	rel := math.Abs(p-want) / want
	if rel > 1e-10 {
		t.Fatalf("deep tail relative error %v against the closed form", rel)
	}
}

// TestUpperTailMonotonicity: the tail is non-increasing in kObs and increasing in ρ at
// fixed kObs — more expected activity makes a given count less surprising the other
// way around: a larger window expectation raises the tail at fixed k.
func TestUpperTailMonotonicity(t *testing.T) {
	prev := 1.1
	for k := 0; k <= 30; k++ {
		got := volume.UpperTail(1.5, 2, 0.7, k)
		if got > prev {
			t.Fatalf("tail rose at k = %d: %v > %v", k, got, prev)
		}
		prev = got
	}

	prevRho := 0.0
	for _, rho := range []float64{0.1, 0.3, 0.7, 1.5} {
		got := volume.UpperTail(1.5, 2, rho, 5)
		if got < prevRho {
			t.Fatalf("tail fell as rho grew: rho = %v gave %v < %v", rho, got, prevRho)
		}
		prevRho = got
	}
}

// TestOverdispersionIsStructural pins the §7.4 property: Var[K]/E[K] = (b+ρ)/b > 1
// for all b, ρ > 0, so the predictive is necessarily overdispersed relative to
// Poisson. Checked both from the closed form and against the pmf directly.
func TestOverdispersionIsStructural(t *testing.T) {
	for _, tc := range []struct{ a, b, rho float64 }{
		{2, 1, 1}, {0.5, 1, 1}, {3, 4, 0.25}, {10, 2, 5},
	} {
		mean, variance := volume.PredictiveMoments(tc.a, tc.b, tc.rho)
		if ratio := variance / mean; ratio <= 1 {
			t.Errorf("a=%v b=%v rho=%v: Var/E = %v, must exceed 1", tc.a, tc.b, tc.rho, ratio)
		}
		closeTo(t, "Var/E", variance/mean, (tc.b+tc.rho)/tc.b, 1e-12)

		// Cross-check the closed-form moments against the pmf, recovered from
		// differences of consecutive upper tails: Pr(K = k) = P(≥k) − P(≥k+1).
		var m0, m1, m2 float64
		for k := range 400 {
			pk := volume.UpperTail(tc.a, tc.b, tc.rho, k) - volume.UpperTail(tc.a, tc.b, tc.rho, k+1)
			m0 += pk
			m1 += float64(k) * pk
			m2 += float64(k) * float64(k) * pk
		}
		closeTo(t, "pmf total", m0, 1, 1e-9)
		closeTo(t, "pmf mean", m1, mean, 1e-6)
		closeTo(t, "pmf variance", m2-m1*m1, variance, 1e-4)
	}
}

// TestDegenerateInputsTakeLimitingValues: no history, empty windows, and zero counts
// all yield P = 1 — with no evidence, no count is anomalous, matching the cold-start
// convention of §6.2 and §7.5 — and never a fabricated small p-value (R3).
func TestDegenerateInputsTakeLimitingValues(t *testing.T) {
	cases := map[string]float64{
		"kObs 0":        volume.UpperTail(2, 1, 1, 0),
		"no history a":  volume.UpperTail(0, 1, 1, 5),
		"no history b":  volume.UpperTail(2, 0, 1, 5),
		"empty window":  volume.UpperTail(2, 1, 0, 5),
		"negative kObs": volume.UpperTail(2, 1, 1, -3),
	}
	for name, got := range cases {
		if got != 1 {
			t.Errorf("%s: P = %v, want exactly 1", name, got)
		}
	}

	mean, variance := volume.PredictiveMoments(0, 1, 1)
	if mean != 0 || variance != 0 {
		t.Errorf("degenerate moments = (%v, %v), want zeros", mean, variance)
	}
}

// TestUpperTailDispersedMatchesTheSummation pins the identity Pr(K ≥ k) = I_q(k, r)
// against the summation UpperTail performs, in the regime where the summation still
// converges quickly. The closed form exists for speed — the summation needs of the
// order of 40,000 terms per event once the dispersion is large — so the two must agree
// wherever both are usable, or the speed was bought by changing the answer.
func TestUpperTailDispersedMatchesTheSummation(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mean, phi float64
		kObs      int
		tolerance float64
	}{
		{"mild dispersion, count near the mean", 20, 2, 25, 1e-12},
		{"mild dispersion, deep in the tail", 20, 2, 80, 1e-12},
		{"moderate dispersion", 50, 5, 120, 1e-12},
		{"count equal to one", 10, 3, 1, 1e-12},
		{"count far below the mean", 100, 4, 3, 1e-12},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := tc.mean / (tc.phi - 1)
			// UpperTail(a=r, b=r, rho=mean) is NB(r, mean): aρ/b = mean and
			// b/(b+ρ) = r/(r+mean).
			want := volume.UpperTail(r, r, tc.mean, tc.kObs)
			got := volume.UpperTailDispersed(tc.mean, tc.phi, tc.kObs)
			if diff := got - want; diff > tc.tolerance || diff < -tc.tolerance {
				t.Errorf("I_q(k,r) = %v, summation = %v, difference %v exceeds %v",
					got, want, diff, tc.tolerance)
			}
		})
	}
}

// TestUpperTailDispersedIsMonotonic: the tail must fall as the observed count climbs,
// and rise as the measured dispersion widens the null. Either failing would mean the
// repair had inverted the detector.
func TestUpperTailDispersedIsMonotonic(t *testing.T) {
	previous := 1.0
	for k := 1; k <= 200; k += 10 {
		p := volume.UpperTailDispersed(50, 10, k)
		if p > previous {
			t.Fatalf("tail rose at k = %d: %v after %v", k, p, previous)
		}
		previous = p
	}

	previous = 0
	for _, phi := range []float64{1.5, 2, 5, 20, 100, 1000} {
		p := volume.UpperTailDispersed(50, phi, 300)
		if p < previous {
			t.Fatalf("tail fell as dispersion widened to φ = %v: %v after %v",
				phi, p, previous)
		}
		previous = p
	}
}

// TestUpperTailDispersedDegenerates: φ ≤ 1 is the caller's signal that nothing was
// measured, and a detector with nothing measured asserts nothing.
func TestUpperTailDispersedDegenerates(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mean, phi float64
		kObs      int
	}{
		{"no dispersion measured", 50, 1, 500},
		{"dispersion below one", 50, 0.5, 500},
		{"no expectation", 0, 10, 500},
		{"no count", 50, 10, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if p := volume.UpperTailDispersed(tc.mean, tc.phi, tc.kObs); p != 1 {
				t.Errorf("P = %v, want exactly 1", p)
			}
		})
	}
}

// TestGammaUpdate covers equation (10) with hand-computed values at δ = 1/2:
//
//	start a = 0, b = 0
//	observe k = 4: a = 4,           b = 1
//	observe k = 2: a = 2 + 2 = 4,   b = 3/2
//	observe k = 0: a = 2,           b = 7/4       mean = 8/7
func TestGammaUpdate(t *testing.T) {
	var g volume.GammaPosterior
	g.Observe(4, 0.5) // discount of empty state is a no-op on zeros
	closeTo(t, "a after first", g.A, 4, 0)
	closeTo(t, "b after first", g.B, 1, 0)

	g.Observe(2, 0.5)
	closeTo(t, "a after second", g.A, 4, 0)
	closeTo(t, "b after second", g.B, 1.5, 0)

	g.Observe(0, 0.5)
	closeTo(t, "a after third", g.A, 2, 0)
	closeTo(t, "b after third", g.B, 1.75, 0)
	closeTo(t, "posterior mean", g.Mean(), 8.0/7.0, 1e-15)

	empty := volume.GammaPosterior{}
	if empty.Mean() != 0 {
		t.Errorf("empty posterior mean = %v, want 0", empty.Mean())
	}
}

// TestDeterminism: identical inputs give bit-identical tails, repeatedly.
func TestDeterminism(t *testing.T) {
	first := volume.UpperTail(1.7, 2.3, 0.9, 7)
	for range 64 {
		if got := volume.UpperTail(1.7, 2.3, 0.9, 7); math.Float64bits(got) != math.Float64bits(first) {
			t.Fatalf("tail changed between calls: %x vs %x",
				math.Float64bits(got), math.Float64bits(first))
		}
	}
}
