package timing_test

import (
	"math"
	"testing"

	"github.com/JohnPierman/ethogram/domain/timing"
)

// besselRatioFixtures holds reference values of r_h = I_h(κ)/I_0(κ) for h = 1..6,
// generated with scipy.special.iv as iv(h, κ)/iv(0, κ).
var besselRatioFixtures = []struct {
	kappa float64
	want  []float64
}{
	{0.5, []float64{
		2.4249961258080197e-01, 3.0001549676792218e-02, 2.4872151664641962e-03,
		1.5496767922185971e-04, 7.7322989144410676e-06, 3.2170093303835431e-07,
	}},
	{1.0, []float64{
		4.4638996589653451e-01, 1.0722006820693100e-01, 1.7509693068810565e-02,
		2.1619097940676056e-03, 2.1441471626972079e-04, 1.7762631370397425e-05,
	}},
	{6.5, []float64{
		9.1948802947494790e-01, 7.1708060631540027e-01, 4.7820765635777862e-01,
		2.7565815429283558e-01, 1.3893608184351944e-01, 6.1910336072036457e-02,
	}},
	{20, []float64{
		9.7467050788980703e-01, 9.0253294921101923e-01, 7.9416391804760345e-01,
		6.6428377379673831e-01, 5.2845040852890790e-01, 4.0005856953228436e-01,
	}},
	{100, []float64{
		9.9498737300516837e-01, 9.8010025253989630e-01, 9.5578336290357280e-01,
		9.2275325076568215e-01, 8.8196310284231882e-01, 8.3455694048145079e-01,
	}},
	{700, []float64{
		9.9928545881842856e-01, 9.9714489868909006e-01, 9.9358748796877494e-01,
		9.8862843450650062e-01, 9.8228887728870184e-01, 9.7459573625951967e-01,
	}},
}

func TestKernelCoefficientsMatchReferenceValues(t *testing.T) {
	const relTol = 1e-12
	worst := 0.0
	for _, fixture := range besselRatioFixtures {
		got := timing.KernelCoefficients(fixture.kappa, len(fixture.want))
		if len(got) != len(fixture.want) {
			t.Fatalf("kappa=%v: got %d coefficients, want %d",
				fixture.kappa, len(got), len(fixture.want))
		}
		for i, want := range fixture.want {
			relErr := math.Abs(got[i]-want) / want
			worst = math.Max(worst, relErr)
			if relErr > relTol {
				t.Errorf("kappa=%v h=%d: got %.17g, want %.17g (relative error %.3g)",
					fixture.kappa, i+1, got[i], want, relErr)
			}
		}
	}
	t.Logf("worst relative error against the fixture table: %.3g", worst)
}

// TestKernelCoefficientsLargeKappaAsymptote checks the large-κ limit of equation (8):
// r_h → exp(−h²/(2κ)). The asymptote is poor at small κ, so it is only asserted from
// κ = 20 upwards.
//
// The expansion of the ratio is r_h = exp(−h²/(2κ) − h²/(4κ²) − …), so even a perfect
// implementation deviates from the leading term by about h²/(4κ²) — some 5.6e-3 at
// κ = 20, h = 3, shrinking quadratically in κ. The tolerance is therefore a 2e-3 base
// plus twice that known correction, which keeps the bound essentially at the base for
// κ ≥ 100 while accommodating the genuine next-order term at κ = 20.
func TestKernelCoefficientsLargeKappaAsymptote(t *testing.T) {
	const baseRelTol = 2e-3
	for _, kappa := range []float64{20, 100, 700} {
		got := timing.KernelCoefficients(kappa, 3)
		for h := 1; h <= 3; h++ {
			relTol := baseRelTol + float64(h*h)/(2*kappa*kappa)
			asymptote := math.Exp(-float64(h*h) / (2 * kappa))
			relDev := math.Abs(got[h-1]-asymptote) / got[h-1]
			if relDev > relTol {
				t.Errorf("kappa=%v h=%d: r_h=%.17g deviates from asymptote %.17g by %.3g (tolerance %.3g)",
					kappa, h, got[h-1], asymptote, relDev, relTol)
			}
		}
	}
}

// TestKernelCoefficientsOverflowRegime exercises the regime where I_h(κ) and I_0(κ)
// individually overflow float64, so computing them separately would yield Inf/Inf. The
// ratios must remain finite, strictly inside (0, 1), and strictly decreasing in h.
func TestKernelCoefficientsOverflowRegime(t *testing.T) {
	const kappa, order = 5000, 200
	got := timing.KernelCoefficients(kappa, order)
	if len(got) != order {
		t.Fatalf("got %d coefficients, want %d", len(got), order)
	}
	previous := 1.0
	for i, r := range got {
		h := i + 1
		if math.IsNaN(r) || math.IsInf(r, 0) {
			t.Fatalf("h=%d: coefficient %v is not finite", h, r)
		}
		if r <= 0 || r >= 1 {
			t.Errorf("h=%d: coefficient %v is outside (0, 1)", h, r)
		}
		if r >= previous {
			t.Errorf("h=%d: coefficient %v is not strictly below its predecessor %v",
				h, r, previous)
		}
		previous = r
	}
}

func TestKernelCoefficientsDegenerateInputs(t *testing.T) {
	zeros := timing.KernelCoefficients(0, 8)
	if len(zeros) != 8 {
		t.Fatalf("kappa=0: got length %d, want 8", len(zeros))
	}
	for i, r := range zeros {
		if r != 0 {
			t.Errorf("kappa=0 h=%d: got %v, want 0", i+1, r)
		}
	}

	if got := timing.KernelCoefficients(6.5, 0); len(got) != 0 {
		t.Errorf("H=0: got length %d, want 0", len(got))
	}

	if got := timing.KappaForBandwidthHours(0); got != 0 {
		t.Errorf("KappaForBandwidthHours(0): got %v, want 0", got)
	}
	if got := timing.HarmonicOrder(0); got != 1 {
		t.Errorf("HarmonicOrder(0): got %d, want 1", got)
	}
}

// TestPaperWorkedExample reproduces the worked example of §7.2: a 1.5-hour bandwidth
// gives κ ≈ 6.5 and a truncation order of H = 11.
func TestPaperWorkedExample(t *testing.T) {
	kappa := timing.KappaForBandwidthHours(1.5)
	if math.Abs(kappa-6.5)/6.5 > 0.02 {
		t.Errorf("KappaForBandwidthHours(1.5): got %v, want within 2%% of 6.5", kappa)
	}
	if got := timing.HarmonicOrder(kappa); got != 11 {
		t.Errorf("HarmonicOrder(%v): got %d, want 11", kappa, got)
	}
}

func TestKernelCoefficientsDeterministic(t *testing.T) {
	first := timing.KernelCoefficients(6.5, 11)
	second := timing.KernelCoefficients(6.5, 11)
	if len(first) != len(second) {
		t.Fatalf("lengths differ: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if math.Float64bits(first[i]) != math.Float64bits(second[i]) {
			t.Errorf("h=%d: bit patterns differ: %x vs %x",
				i+1, math.Float64bits(first[i]), math.Float64bits(second[i]))
		}
	}
}
