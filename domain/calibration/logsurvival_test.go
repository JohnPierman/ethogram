package calibration_test

import (
	"math"
	"testing"

	"github.com/JohnPierman/ethogram/domain/calibration"
)

// The log survival function exists to keep the ordering of extreme events meaningful
// where the survival function itself underflows to zero. These tests fix both halves of
// that claim: that it agrees with the linear function wherever the linear one is
// trustworthy, and that it keeps discriminating where the linear one has stopped.

func TestLogSurvivalAgreesWithSurvivalInRange(t *testing.T) {
	// Wherever Q is comfortably representable the two must be the same function.
	cases := []struct {
		x  float64
		df int
	}{
		{0.5, 2}, {1, 2}, {5, 2}, {10, 2}, {50, 2},
		{1, 4}, {10, 6}, {100, 10}, {200, 20}, {400, 50},
		{600, 100}, {50, 200}, {700, 300},
	}
	for _, c := range cases {
		lin := calibration.ChiSquareSurvival(c.x, c.df)
		if lin <= 0 || lin > 1 {
			t.Fatalf("fixture (x=%v, df=%d) has Q=%v, outside the range this test covers",
				c.x, c.df, lin)
		}
		got := calibration.ChiSquareLogSurvival(c.x, c.df)
		want := math.Log(lin)
		// Relative agreement in log space; the two paths do the same arithmetic in a
		// different order, so exact equality is not owed.
		if math.Abs(got-want) > 1e-9*math.Max(1, math.Abs(want)) {
			t.Errorf("x=%v df=%d: log survival = %v, want %v (Q=%v)", c.x, c.df, got, want, lin)
		}
	}
}

func TestLogSurvivalDiscriminatesWhereSurvivalUnderflows(t *testing.T) {
	// This is the defect the function exists to remove. At these statistics the linear
	// survival is exactly zero for every input, so the events are indistinguishable;
	// the logarithm must still order them.
	xs := []float64{1500, 2000, 5000, 20000, 100000}
	prev := math.Inf(0)
	for _, x := range xs {
		lin := calibration.ChiSquareSurvival(x, 2)
		lg := calibration.ChiSquareLogSurvival(x, 2)

		if lin != 0 {
			t.Fatalf("x=%v: linear survival is %v, expected underflow to 0 — "+
				"the premise of this test no longer holds", x, lin)
		}
		if math.IsInf(lg, 0) || math.IsNaN(lg) {
			t.Fatalf("x=%v: log survival is %v, which discriminates no better than zero", x, lg)
		}
		if lg >= prev {
			t.Errorf("x=%v: log survival %v is not below the previous %v; the ordering "+
				"is not strictly decreasing in the statistic", x, lg, prev)
		}
		prev = lg
	}
}

func TestLogSurvivalOrdersEventsThatLinearScoringTies(t *testing.T) {
	// The concrete failure measured on LANL: an alert pool pinned at exactly zero,
	// against a labelled event that is far less extreme but representable. Under the
	// linear survival the labelled event loses to a tie; under the log survival it is
	// correctly ordered as the less extreme of the two.
	const labelled = 1300.0 // ~1e-274 at 2 df: small, representable
	const pooled = 3000.0   // underflows to exactly 0

	linLabelled := calibration.ChiSquareSurvival(labelled, 2)
	linPooled := calibration.ChiSquareSurvival(pooled, 2)
	if linPooled != 0 {
		t.Fatalf("fixture no longer underflows: pooled Q = %v", linPooled)
	}
	if !(linLabelled > linPooled) {
		t.Fatalf("fixture invalid: labelled Q = %v is not above pooled Q = %v",
			linLabelled, linPooled)
	}

	logLabelled := calibration.ChiSquareLogSurvival(labelled, 2)
	logPooled := calibration.ChiSquareLogSurvival(pooled, 2)
	if !(logPooled < logLabelled) {
		t.Errorf("log survival does not order the pair: pooled %v, labelled %v",
			logPooled, logLabelled)
	}
	if math.IsInf(logPooled, 0) {
		t.Error("the pooled event's log survival is infinite, so it cannot be ordered")
	}
}

func TestLogSurvivalIsMonotoneInDegreesOfFreedom(t *testing.T) {
	// At a fixed statistic, more degrees of freedom means a less extreme tail. This is
	// why ranking on the statistic alone is wrong when J varies between events, and the
	// log tail is the quantity that makes them comparable.
	const x = 800.0
	prev := math.Inf(-1)
	for _, df := range []int{2, 4, 10, 40, 200} {
		lg := calibration.ChiSquareLogSurvival(x, df)
		if lg <= prev {
			t.Errorf("df=%d: log survival %v is not above the previous %v", df, lg, prev)
		}
		prev = lg
	}
}

func TestLogSurvivalBoundaries(t *testing.T) {
	// ln 1 = 0 at and below the origin, matching ChiSquareSurvival's 1.
	for _, c := range []struct {
		x  float64
		df int
	}{{0, 2}, {-1, 2}, {10, 0}, {10, -3}} {
		if got := calibration.ChiSquareLogSurvival(c.x, c.df); got != 0 {
			t.Errorf("x=%v df=%d: got %v, want 0 (ln 1)", c.x, c.df, got)
		}
	}
	// Never positive: a survival probability cannot exceed one.
	for _, x := range []float64{0.001, 1, 100, 10000} {
		if got := calibration.ChiSquareLogSurvival(x, 6); got > 0 {
			t.Errorf("x=%v: log survival %v is positive", x, got)
		}
	}
}

func TestLogSurvivalIsDeterministic(t *testing.T) {
	// R4: the same input must give bit-identical output on every evaluation.
	for _, x := range []float64{7, 1234.5, 99999} {
		first := calibration.ChiSquareLogSurvival(x, 8)
		for range 64 {
			if got := calibration.ChiSquareLogSurvival(x, 8); math.Float64bits(got) != math.Float64bits(first) {
				t.Fatalf("x=%v: repeated evaluation differs: %v then %v", x, first, got)
			}
		}
	}
}
