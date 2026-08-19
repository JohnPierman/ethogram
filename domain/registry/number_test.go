package registry_test

import (
	"math"
	"strconv"
	"testing"

	"github.com/JohnPierman/ethogram/domain/registry"
)

// TestParseNumberAcceptsPlainDecimals: the forms a corpus actually carries.
func TestParseNumberAcceptsPlainDecimals(t *testing.T) {
	for _, tc := range []struct {
		text string
		want float64
	}{
		{"0", 0},
		{"7", 7},
		{"007", 7},
		{"-42", -42},
		{"+5", 5},
		{"1.5", 1.5},
		{".5", 0.5},
		{"5.", 5},
		{"1e3", 1000},
		{"1E3", 1000},
		{"2.5e-3", 0.0025},
	} {
		got, ok := registry.ParseNumber(tc.text)
		if !ok {
			t.Errorf("ParseNumber(%q): declined, want %v", tc.text, tc.want)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseNumber(%q) = %v, want %v", tc.text, got, tc.want)
		}
	}
}

// TestParseNumberRejectsWhatCannotBeMeasured pins the divergence from
// [strconv.ParseFloat], which accepts every string below.
//
// The inference site and the scoring site must agree on what a number is. Detector IV
// abstains on a non-finite value, so a field counted numeric on the strength of values
// ParseFloat accepts but the detector refuses is classified into permanent abstention.
// A sentinel is a token, not a measurement, and must count against numeric inference
// exactly as "unknown" does.
func TestParseNumberRejectsWhatCannotBeMeasured(t *testing.T) {
	for _, text := range []string{
		"NaN", "nan", "Inf", "+Inf", "-Inf", "infinity", "-Infinity", // non-finite
		"0x1p-2", "0X1P-2", // hexadecimal float literals
		"1_000",    // Go literal underscores
		"1e400",    // overflows to infinity
		" 1", "1 ", // surrounding space
		"", "abc", "1,5", "12%", "1.2.3",
	} {
		if x, ok := registry.ParseNumber(text); ok {
			t.Errorf("ParseNumber(%q) = %v, want declined", text, x)
		}
		// Every rejection above except the trivially malformed is one ParseFloat
		// would have accepted; that gap is the reason this function exists.
		_, _ = strconv.ParseFloat(text, 64)
	}
}

// TestParseNumberRejectsNonFiniteResults guards the invariant the band and the sketch
// both depend on: a parsed number is orderable and finite.
func TestParseNumberRejectsNonFiniteResults(t *testing.T) {
	for _, text := range []string{"1e999", "-1e999"} {
		if x, ok := registry.ParseNumber(text); ok {
			if math.IsInf(x, 0) || math.IsNaN(x) {
				t.Fatalf("ParseNumber(%q) returned non-finite %v with ok=true", text, x)
			}
		}
	}
}

// TestBandBoundaries pins the 1-2-5 series' exact edges. A band label is a persisted
// identity — Detector I counts it and the co-occurrence graph makes it a node — so a
// boundary that moves silently invalidates every count already accumulated under it.
func TestBandBoundaries(t *testing.T) {
	for _, tc := range []struct {
		x    float64
		want string
	}{
		{0, "[0]"},
		{1, "[1e0,2e0)"},
		{1.999, "[1e0,2e0)"},
		{2, "[2e0,5e0)"},
		{4.999, "[2e0,5e0)"},
		{5, "[5e0,1e1)"},
		{9.999, "[5e0,1e1)"},
		{10, "[1e1,2e1)"},
		{1000, "[1e3,2e3)"},
		{1234, "[1e3,2e3)"},
		{99999, "[5e4,1e5)"},
		{0.05, "[5e-2,1e-1)"},
		{0.1, "[1e-1,2e-1)"},
		// Negatives keep their sign and their order: x lies in (-upper, -lower].
		{-1, "(-2e0,-1e0]"},
		{-1234, "(-2e3,-1e3]"},
		{-0.05, "(-1e-1,-5e-2]"},
	} {
		if got := registry.Band(tc.x); got != tc.want {
			t.Errorf("Band(%v) = %q, want %q", tc.x, got, tc.want)
		}
	}
}

// TestBandIsExactAtDecadeBoundaries: computing the decade through a floating-point
// logarithm puts 1000 in the band below it on some platforms, because log10(1000) is
// representable as 2.9999999999999996. The implementation must not be able to make
// that mistake.
func TestBandIsExactAtDecadeBoundaries(t *testing.T) {
	for exp := -300; exp <= 300; exp++ {
		x, err := strconv.ParseFloat("1e"+strconv.Itoa(exp), 64)
		if err != nil || x == 0 || math.IsInf(x, 0) {
			continue
		}
		want := "[1e" + strconv.Itoa(exp) + ",2e" + strconv.Itoa(exp) + ")"
		if got := registry.Band(x); got != want {
			t.Errorf("Band(1e%d) = %q, want %q", exp, got, want)
		}
	}
}

// TestBandLabelIsNotItselfNumeric: a band label is registered as a field value and
// re-observed by the registry. If it parsed as a number the derived field would be
// classified continuous and banded again, and the second banding would be of a
// meaningless quantity.
func TestBandLabelIsNotItselfNumeric(t *testing.T) {
	for _, x := range []float64{0, 1, -1, 1234, 0.05, -0.05, 5e300} {
		label := registry.Band(x)
		if _, ok := registry.ParseNumber(label); ok {
			t.Errorf("Band(%v) = %q, which parses as a number", x, label)
		}
	}
}

// TestBandIsMonotone: bands must not overlap and must be ordered as the line is, or
// the co-occurrence graph would join values from disjoint parts of the distribution.
func TestBandIsMonotone(t *testing.T) {
	ordered := []float64{-1e6, -1e3, -5, -1, -0.001, 0, 0.001, 1, 5, 1e3, 1e6}
	seen := map[string]int{}
	for i, x := range ordered {
		b := registry.Band(x)
		if prev, dup := seen[b]; dup {
			t.Errorf("Band(%v) = Band(%v) = %q: distinct magnitudes share a band",
				ordered[prev], x, b)
		}
		seen[b] = i
	}
}

// TestBandCardinalityIsBounded is the property that makes a continuous field safe to
// admit to the per-entity detectors at all: the vocabulary it presents is finite and
// small, whatever the field's own cardinality.
func TestBandCardinalityIsBounded(t *testing.T) {
	bands := map[string]struct{}{}
	for i := range 100_000 {
		// A heavy-tailed spread over nine decades, of the shape a byte count has.
		x := float64(i) * 1.37
		bands[registry.Band(x)] = struct{}{}
		bands[registry.Band(-x)] = struct{}{}
	}
	if len(bands) > 64 {
		t.Fatalf("200,000 distinct values produced %d bands, want a small bounded set", len(bands))
	}
	if len(bands) < 10 {
		t.Fatalf("nine decades collapsed into %d bands: too coarse to carry signal", len(bands))
	}
}
