package statistics_test

import (
	"math"
	"testing"

	"github.com/JohnPierman/ethogram/domain/statistics"
)

const weekHours = 7 * 24.0

// TestSaturatingWeightIsTheGeometricLimit. The identity the whole file rests on: folding one
// observation every gap converges to 1/(1-delta) rather than growing without bound, which is
// what makes a minimum weight a claim about coverage instead of a warm-up.
func TestSaturatingWeightIsTheGeometricLimit(t *testing.T) {
	for _, gap := range []float64{1, 6, 12, 24, 72, weekHours} {
		want := statistics.SaturatingWeight(gap, weekHours)

		// Simulate the fold directly. Enough iterations to be at the limit for any of
		// these gaps, and the comparison is to the closed form.
		delta := math.Exp2(-gap / weekHours)
		got := 0.0
		for i := 0; i < 100000; i++ {
			got = delta*got + 1
		}
		if math.Abs(got-want) > 1e-9*want {
			t.Errorf("gap %v: folded weight converged to %.6f, closed form says %.6f",
				gap, got, want)
		}
	}
}

// TestSaturatingWeightIsDecreasingInTheGap. Sparser observations mean a lower ceiling, which is
// the direction that makes the trap a trap: the entities an arm most needs history for are the
// ones least able to accumulate it.
func TestSaturatingWeightIsDecreasingInTheGap(t *testing.T) {
	previous := math.Inf(1)
	for _, gap := range []float64{1, 2, 4, 8, 12, 24, 48, 96} {
		w := statistics.SaturatingWeight(gap, weekHours)
		if w >= previous {
			t.Errorf("gap %v gives weight %.3f, not below the previous %.3f",
				gap, w, previous)
		}
		previous = w
	}
}

// TestTheThreeMeasuredCases pins the bounds of the three arms that hit this, so a change to a
// half-life or a minimum cannot silently make one of them unsatisfiable again.
//
// The numbers are the arms' own: timing requires a discounted weight of 20 and drift and volume
// require 8 and 5 respectively, all at the framework's seven-day half-life, but on different
// timescales -- timing per event, volume per hourly window, drift per daily period.
func TestTheThreeMeasuredCases(t *testing.T) {
	for _, tc := range []struct {
		name      string
		minimum   float64
		gap       float64
		halfLife  float64
		reachable bool
	}{
		// timing: 20, per event, at the seven-day half-life in hours. A busy account
		// clears it; one active once a day never does.
		{"timing, events an hour apart", 20, 1, weekHours, true},
		{"timing, events twelve hours apart", 20, 12, weekHours, true},
		{"timing, events once a day", 20, 24, weekHours, false},
		{"timing, events once a week", 20, weekHours, weekHours, false},

		// volume: 5, per hourly window.
		{"volume, windows a day apart", 5, 24, weekHours, true},
		{"volume, windows two days apart", 5, 48, weekHours, true},
		{"volume, windows three days apart", 5, 72, weekHours, false},

		// drift: 8, per daily period, so the gap is one day and the half-life is in days.
		// The boundary is 1/log2(8/7) = 5.19 days: the arm's own comment says "at least
		// about 5.2 days", and this is that claim as a test. A five-day half-life is just
		// inside the unreachable side, at a ceiling of 7.73 against a minimum of 8.
		{"drift, daily periods at a seven-day half-life", 8, 1, 7, true},
		{"drift, daily periods at a six-day half-life", 8, 1, 6, true},
		{"drift, daily periods at a five-day half-life", 8, 1, 5, false},
		{"drift, daily periods at a four-day half-life", 8, 1, 4, false},
	} {
		got := statistics.MinimumWeightReachable(tc.minimum, tc.gap, tc.halfLife)
		if got != tc.reachable {
			t.Errorf("%s: reachable = %v, want %v (ceiling %.2f against minimum %v)",
				tc.name, got, tc.reachable,
				statistics.SaturatingWeight(tc.gap, tc.halfLife), tc.minimum)
		}
	}
}

// TestMaximumGapForWeightInvertsTheCeiling. The inverse is what lets an arm's coverage be
// stated in the units an operator thinks in, so it must agree with the forward function at the
// boundary it names.
func TestMaximumGapForWeightInvertsTheCeiling(t *testing.T) {
	for _, minimum := range []float64{2, 5, 8, 20, 100} {
		gap := statistics.MaximumGapForWeight(minimum, weekHours)
		if gap <= 0 {
			t.Fatalf("minimum %v: no gap reported", minimum)
		}
		// At the boundary the ceiling equals the minimum.
		if w := statistics.SaturatingWeight(gap, weekHours); math.Abs(w-minimum) > 1e-9*minimum {
			t.Errorf("minimum %v: at gap %.6f the ceiling is %.6f", minimum, gap, w)
		}
		// Just inside is reachable, just outside is not.
		if !statistics.MinimumWeightReachable(minimum, gap*0.999, weekHours) {
			t.Errorf("minimum %v: a gap just below the boundary is unreachable", minimum)
		}
		if statistics.MinimumWeightReachable(minimum, gap*1.001, weekHours) {
			t.Errorf("minimum %v: a gap just above the boundary is reachable", minimum)
		}
	}

	// The timing arm's coverage, as the sentence a reader can check.
	if gap := statistics.MaximumGapForWeight(20, weekHours); math.Abs(gap-12.4) > 0.1 {
		t.Errorf("the standardised timing statistic's coverage boundary is %.2f hours,"+
			" expected about 12.4", gap)
	}
}

// TestDegenerateInputsReportUnbounded. No discounting means no ceiling, and a minimum met by a
// single observation is not a constraint. Neither case should be reported as unsatisfiable.
func TestDegenerateInputsReportUnbounded(t *testing.T) {
	for name, tc := range map[string][2]float64{
		"zero gap":         {0, weekHours},
		"zero half-life":   {24, 0},
		"negative gap":     {-1, weekHours},
		"gap not a number": {math.NaN(), weekHours},
	} {
		if w := statistics.SaturatingWeight(tc[0], tc[1]); !math.IsInf(w, 1) {
			t.Errorf("%s: weight %v, want unbounded", name, w)
		}
		if !statistics.MinimumWeightReachable(1e9, tc[0], tc[1]) {
			t.Errorf("%s: reported unreachable despite no discounting", name)
		}
	}
	for name, minimum := range map[string]float64{
		"one": 1, "below one": 0.5, "zero": 0, "infinite": math.Inf(1),
	} {
		if gap := statistics.MaximumGapForWeight(minimum, weekHours); gap != 0 {
			t.Errorf("minimum %s: gap %v, want 0", name, gap)
		}
	}
}
