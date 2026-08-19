package calibration_test

import (
	"math"
	"testing"

	"github.com/JohnPierman/ethogram/domain/calibration"
)

// TestConformalIsSuperUniformUnderABrokenModel is the property the whole mechanism
// exists for, and it is stated deliberately against a model that is WRONG.
//
// The detector here is catastrophically miscalibrated in the direction the volume and
// co-occurrence detectors were measured to be: it reports p-values as small as e^−1000
// for perfectly ordinary data. Fisher's method would treat those as overwhelming
// evidence. The conformal p-value is a rank, so it does not care what the model claims —
// under exchangeability at most a q fraction of fresh scores may fall at or below q.
func TestConformalIsSuperUniformUnderABrokenModel(t *testing.T) {
	c := calibration.NewConformal(100)

	// A deterministic "broken model": ordinary data mapped onto absurd p-values.
	// The sequence is fixed, so burn-in and scoring are exchangeable by construction.
	// −ln p sweeps 0..600, i.e. p from 1 down to about 1e−261: absurd for ordinary data
	// but still representable, since e^−1000 underflows to exactly zero and a zero is
	// not a p-value at all (R3).
	brokenP := func(i int) float64 {
		return math.Exp(-float64((i*37)%600) - 0.5)
	}

	const burnIn = 20000
	for i := range burnIn {
		c.Observe("broken", math.Log(brokenP(i)))
	}
	m := c.Freeze()

	for _, q := range []float64{0.01, 0.05, 0.10, 0.25, 0.50} {
		below := 0
		const scoring = 20000
		for i := burnIn; i < burnIn+scoring; i++ {
			p, ok := m.Calibrate("broken", math.Log(brokenP(i)))
			if !ok {
				t.Fatal("the detector had 20,000 burn-in observations; it must be calibrated")
			}
			if p <= q {
				below++
			}
		}
		realised := float64(below) / float64(20000)
		// Super-uniform: at most q, with room for the discreteness of the histogram.
		if realised > q+0.02 {
			t.Errorf("q = %.2f: %.4f of scored events fell at or below it; a conformal "+
				"p-value must be super-uniform however wrong the model is", q, realised)
		}
	}
}

// TestConformalRanksRatherThanTrustsTheModel: the ordering of the model's p-values is
// preserved, because a rank is a monotone function of the score. Calibration must fix
// the scale without scrambling the order the detector expressed.
func TestConformalRanksRatherThanTrustsTheModel(t *testing.T) {
	c := calibration.NewConformal(10)
	for i := 1; i <= 1000; i++ {
		c.Observe("d", math.Log(float64(i)/1000))
	}
	m := c.Freeze()

	previous := 0.0
	for _, p := range []float64{1e-9, 1e-6, 1e-3, 0.01, 0.1, 0.5, 1.0} {
		got, ok := m.Calibrate("d", math.Log(p))
		if !ok {
			t.Fatal("expected a calibrated detector")
		}
		if got < previous {
			t.Errorf("conformal p fell from %v to %v as the model p rose to %v; "+
				"calibration must be monotone in the score", previous, got, p)
		}
		previous = got
	}
}

// TestConformalCannotFallBelowItsFloor pins the limitation that must be designed around
// rather than discovered in a run: n burn-in observations cannot express a p-value below
// 1/(n+1), so every event past the burn-in tail ties at the floor.
func TestConformalCannotFallBelowItsFloor(t *testing.T) {
	c := calibration.NewConformal(10)
	const n = 1000
	for i := 1; i <= n; i++ {
		c.Observe("d", math.Log(float64(i)/n)) // nothing below 1/1000
	}
	m := c.Freeze()

	floor, ok := m.Floor("d")
	if !ok {
		t.Fatal("expected a floor")
	}
	if want := 1 / float64(n+1); floor != want {
		t.Fatalf("floor = %v, want %v", floor, want)
	}

	// Two events, wildly different under the model, both past the burn-in tail.
	a, _ := m.Calibrate("d", math.Log(1e-30))
	b, _ := m.Calibrate("d", math.Log(1e-300))
	if a != floor || b != floor {
		t.Errorf("events past the burn-in tail gave %v and %v, want both at the floor %v",
			a, b, floor)
	}
	if a != b {
		t.Error("the floor must be reported honestly as a tie, not disguised as a ranking")
	}
}

// TestConformalDeclinesBelowMinObservations: a detector seen a handful of times is left
// on its model p-value rather than calibrated against noise, and says so.
func TestConformalDeclinesBelowMinObservations(t *testing.T) {
	c := calibration.NewConformal(1000)
	for i := range 999 {
		c.Observe("sparse", math.Log(float64(i+1)/1000))
	}
	c.Observe("dense", math.Log(0.5))
	for range 1500 {
		c.Observe("dense", math.Log(0.5))
	}
	m := c.Freeze()

	// Uncalibrated reports ok = false and nothing else: the input is a logarithm and the
	// output a probability, so there is no "unchanged" value to hand back. The caller
	// keeps its own p-value, which is what the combination does.
	if _, ok := m.Calibrate("sparse", math.Log(1e-9)); ok {
		t.Error("sparse detector reported a calibration; below the floor of support it " +
			"must decline, leaving the caller on the model p-value")
	}
	if _, ok := m.Calibrate("dense", math.Log(1e-9)); !ok {
		t.Error("dense detector must be calibrated")
	}
	if _, ok := m.Calibrate("never-seen", math.Log(0.5)); ok {
		t.Error("an unseen detector must not report a calibration")
	}
}

// TestConformalRejectsNonLogPValues: the argument is ln p, so the valid domain is
// (−inf, 0] excluding the infinities. A positive value is a probability above one; ±inf
// asserts certainty or impossibility, neither of which any null in §6–§9 produces. R3
// forbids encoding an abstention as a number, so none of them is recorded.
//
// Note that 0 and −1 ARE valid here and would not be as linear p-values: they are ln 1
// and ln 0.368. Reading a log-space argument as though it were a probability is exactly
// the confusion this signature exists to prevent.
func TestConformalRejectsNonLogPValues(t *testing.T) {
	c := calibration.NewConformal(1)
	for _, bad := range []float64{0.5, 1.5, math.NaN(), math.Inf(1), math.Inf(-1)} {
		c.Observe("d", bad)
	}
	m := c.Freeze()
	if _, ok := m.Calibrate("d", math.Log(0.5)); ok {
		t.Error("only out-of-domain values were observed; nothing may be calibrated from them")
	}

	for range 10 {
		c.Observe("d", math.Log(0.25))
	}
	m = c.Freeze()
	for _, bad := range []float64{math.NaN(), math.Inf(-1), 1} {
		if _, ok := m.Calibrate("d", bad); ok {
			t.Errorf("ln p = %v must not be calibrated", bad)
		}
	}
	if _, ok := m.Calibrate("d", math.Log(0.25)); !ok {
		t.Error("a valid ln p must still calibrate after the invalid ones were refused")
	}
}

// TestSidakLogAgreesWithSidakAndSurvivesUnderflow pins the log-space correction against
// the direct one wherever the direct one is usable, and past the point where it is not.
//
// The direct form floors at the smallest positive float64, so every minimum below about
// 1e-320 corrects to the same number. That is the tie ranking cannot survive, and it is
// the reason this function exists.
func TestSidakLogAgreesWithSidakAndSurvivesUnderflow(t *testing.T) {
	for _, tests := range []int{1, 2, 5, 20} {
		for _, minP := range []float64{1e-300, 1e-100, 1e-12, 1e-9, 1e-4, 0.01, 0.3, 0.9} {
			got := calibration.SidakLog(math.Log(minP), tests)
			want := math.Log(calibration.Sidak(minP, tests))
			if diff := got - want; diff > 1e-9 || diff < -1e-9 {
				t.Errorf("tests=%d minP=%v: SidakLog = %v, ln(Sidak) = %v",
					tests, minP, got, want)
			}
		}
	}

	// Below the direct form's floor the log form must stay finite, ordered, and shifted
	// by exactly ln(tests).
	previous := 0.0
	for _, logMinP := range []float64{-800, -900, -1000, -1200} {
		got := calibration.SidakLog(logMinP, 5)
		if math.IsInf(got, 0) || math.IsNaN(got) {
			t.Fatalf("logMinP=%v gave %v", logMinP, got)
		}
		if want := math.Log(5) + logMinP; math.Abs(got-want) > 1e-12 {
			t.Errorf("logMinP=%v: got %v, want ln(5) + logMinP = %v", logMinP, got, want)
		}
		if previous != 0 && got >= previous {
			t.Errorf("ordering lost: %v then %v", previous, got)
		}
		previous = got
	}
}

// TestSidakLogDegenerates: one test is no multiplicity, and a minimum at one is certain.
func TestSidakLogDegenerates(t *testing.T) {
	if got := calibration.SidakLog(-7, 1); got != -7 {
		t.Errorf("one test must return the minimum unchanged, got %v", got)
	}
	if got := calibration.SidakLog(0, 5); got != 0 {
		t.Errorf("minP = 1 must return ln 1 = 0, got %v", got)
	}
}

// TestFrozenModelIgnoresLaterObservations is the guard on what "frozen" means.
//
// §10.1's requirement is that the quantity used to score an event was not fitted on it,
// and a model that shared its counts with the still-live accumulator would satisfy that
// by convention only: anything observed after the boundary would silently change the
// distribution a handed-out model scores against.
func TestFrozenModelIgnoresLaterObservations(t *testing.T) {
	c := calibration.NewConformal(10)
	for i := 1; i <= 1000; i++ {
		c.Observe("d", math.Log(float64(i)/1000))
	}
	m := c.Freeze()

	before, ok := m.Calibrate("d", math.Log(0.001))
	if !ok {
		t.Fatal("expected a calibrated detector")
	}
	floorBefore, _ := m.Floor("d")

	// Everything after the boundary is exactly what must not count, and it is chosen to
	// move the answer a great deal if it did: a hundred thousand values at the extreme.
	for range 100000 {
		c.Observe("d", math.Log(1e-9))
	}

	after, _ := m.Calibrate("d", math.Log(0.001))
	floorAfter, _ := m.Floor("d")
	if after != before {
		t.Errorf("calibration moved from %v to %v on observations made after the "+
			"boundary; the frozen model must share no state with the accumulator",
			before, after)
	}
	if floorAfter != floorBefore {
		t.Errorf("floor moved from %v to %v after the boundary", floorBefore, floorAfter)
	}

	// Freezing again, by contrast, must see them: the accumulator itself is not frozen.
	if second, _ := c.Freeze().Calibrate("d", 0.001); second == before {
		t.Error("a fresh Freeze must reflect the later observations; only the model " +
			"handed out earlier is fixed")
	}
}

// TestConformalIsDeterministic: the same observations in the same order must produce the
// same calibration, byte for byte, or R4 fails at the combination.
func TestConformalIsDeterministic(t *testing.T) {
	build := func() *calibration.ConformalModel {
		c := calibration.NewConformal(10)
		for i := 1; i <= 5000; i++ {
			c.Observe("a", math.Log(float64(i%997+1)/1000))
			c.Observe("b", -float64(i%400))
		}
		return c.Freeze()
	}
	first, second := build(), build()
	for _, id := range []string{"a", "b"} {
		for _, p := range []float64{1e-12, 1e-6, 0.01, 0.5, 1} {
			x, _ := first.Calibrate(id, math.Log(p))
			y, _ := second.Calibrate(id, math.Log(p))
			if x != y {
				t.Fatalf("%s at p = %v: %v then %v", id, p, x, y)
			}
		}
	}
}
