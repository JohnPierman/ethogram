package drift_test

import (
	"context"
	"testing"

	"github.com/JohnPierman/ethogram/domain/detector"
	"github.com/JohnPierman/ethogram/domain/drift"
	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/novelty"
)

type memDrift struct{ states map[string]*drift.State }

func dkey(s event.SourceID, e event.EntityID) string { return string(s) + "\x1f" + string(e) }

func (m *memDrift) FindByEntity(_ context.Context, s event.SourceID, e event.EntityID) (*drift.State, bool, error) {
	st, ok := m.states[dkey(s, e)]
	if !ok {
		return nil, false, nil
	}
	c := *st
	return &c, true, nil
}

func (m *memDrift) SaveState(_ context.Context, s event.SourceID, e event.EntityID, st *drift.State) error {
	m.states[dkey(s, e)] = st
	return nil
}

const dSrc = event.SourceID("lanl.auth")

func dEvent(entity event.EntityID, at event.Timestamp, offset int64) *event.Event {
	e := event.New(dSrc, entity, at, map[event.FieldPath]event.Value{
		"auth.authentication_type": event.NewValue("Kerberos"),
	}, offset)
	return &e
}

func dWire(t *testing.T, minPeriods int64) (*drift.Detector, *memDrift) {
	t.Helper()
	store := &memDrift{states: map[string]*drift.State{}}
	d, err := drift.NewDetector(store, novelty.HalfLife(7*event.Day), drift.DefaultShift, minPeriods)
	if err != nil {
		t.Fatalf("NewDetector: %v", err)
	}
	return d, store
}

// feed scores and commits perDay events on each of days [0, days), returning the p-value the
// last event of the final day received and whether the arm was evaluated at all.
func feed(t *testing.T, d *drift.Detector, entity event.EntityID, counts []int) (float64, bool) {
	t.Helper()
	ctx := context.Background()
	var (
		offset int64
		lastP  float64
		lastOK bool
	)
	for day, n := range counts {
		for i := 0; i < n; i++ {
			// Spread the day's events across working hours so exposure advances.
			at := event.Timestamp(int64(day)*int64(event.Day)) +
				event.Timestamp(int64(i+1)*int64(event.Hour)*8/int64(n+1))
			verdicts, obs, err := d.Score(ctx, dEvent(entity, at, offset))
			if err != nil {
				t.Fatalf("Score: %v", err)
			}
			offset++
			if len(verdicts) != 1 {
				t.Fatalf("want one verdict, got %d", len(verdicts))
			}
			v := verdicts[0]
			if p, ok := v.PValue(); ok {
				lastP, lastOK = p, true
			} else {
				lastOK = false
			}
			if err := obs.Commit(ctx); err != nil {
				t.Fatalf("Commit: %v", err)
			}
		}
	}
	return lastP, lastOK
}

func repeat(n, v int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = v
	}
	return out
}

// cycle repeats a pattern of per-day counts n times. A pattern rather than a constant,
// because a perfectly regular account produces the same cumulative sum every period and the
// arm abstains on it by design -- see TestDetectorAbstainsOnAPerfectlyRegularAccount.
func cycle(n int, pattern []int) []int {
	out := make([]int, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, pattern[i%len(pattern)])
	}
	return out
}

var (
	steadyPattern  = []int{8, 12, 10, 9, 11, 10, 13, 7, 10, 10}
	shiftedPattern = []int{10, 16, 13, 12, 14, 13, 17, 9, 13, 13}
)

// TestDetectorAbstainsBeforeItHasANull. R3 at the detector boundary: an entity with no closed
// periods has no baseline, so there is no reference value and no statistic. The arm must say so
// rather than report the prior's tail, which is the defect the volume arm's own gate exists for.
func TestDetectorAbstainsBeforeItHasANull(t *testing.T) {
	d, _ := dWire(t, drift.DefaultMinPeriods)
	ctx := context.Background()
	verdicts, obs, err := d.Score(ctx, dEvent("U1@DOM1", event.Timestamp(3*event.Hour), 0))
	if err != nil {
		t.Fatalf("Score: %v", err)
	}
	if got := verdicts[0].Status(); got == detector.StatusEvaluated {
		t.Errorf("first-ever event was evaluated, want an abstention")
	}
	// The observation must still be returned, or the abstention becomes permanent.
	if obs == nil {
		t.Fatal("no observation returned with the abstention: the arm would never learn")
	}
	if err := obs.Commit(ctx); err != nil {
		t.Fatalf("Commit: %v", err)
	}
}

// TestDetectorScoresASustainedShiftBelowAStationaryStream. The measurement the arm exists for,
// through the full detector rather than the bare statistic: two entities are given identical
// stationary history and then diverge, one continuing flat and one raised by a third.
func TestDetectorScoresASustainedShiftBelowAStationaryStream(t *testing.T) {
	d, _ := dWire(t, drift.DefaultMinPeriods)

	flat := append(cycle(14, steadyPattern), cycle(7, steadyPattern)...)
	shifted := append(cycle(14, steadyPattern), cycle(7, shiftedPattern)...)

	pFlat, okFlat := feed(t, d, "U-flat@DOM1", flat)
	pShift, okShift := feed(t, d, "U-shift@DOM1", shifted)

	if !okFlat || !okShift {
		t.Fatalf("expected both entities to be evaluated, got flat=%v shifted=%v",
			okFlat, okShift)
	}
	t.Logf("p: stationary %.4g, shifted %.4g (ratio %.1fx)", pFlat, pShift, pFlat/pShift)
	if pShift >= pFlat {
		t.Errorf("shifted stream scored %.4g and stationary %.4g; the shifted stream must be"+
			" the more surprising", pShift, pFlat)
	}
	// The separation must be worth having. It is also what pins drift.NullDiscount: a
	// discounted null tracks the drift and collapses this ratio from about 237 to about 2.
	if pFlat/pShift < 50 {
		t.Errorf("separation is only %.1fx, want at least 50x; has the null been discounted?",
			pFlat/pShift)
	}
}

// TestDetectorRespondsWithinTheDay. A statistic that only moved at midnight could not threshold
// a stream: an operator at 14:00 needs the arm to have an opinion about the day so far. The
// current period is charged the exposure elapsed, so accumulating events inside one day must
// drive the p-value down.
func TestDetectorRespondsWithinTheDay(t *testing.T) {
	d, store := dWire(t, drift.DefaultMinPeriods)
	entity := event.EntityID("U-intraday@DOM1")
	feed(t, d, entity, cycle(16, steadyPattern))

	ctx := context.Background()
	var first, last float64
	var seen int
	for i := 0; i < 40; i++ {
		at := event.Timestamp(int64(16)*int64(event.Day)) +
			event.Timestamp(int64(i+1)*int64(event.Hour)/2)
		verdicts, obs, err := d.Score(ctx, dEvent(entity, at, int64(1000+i)))
		if err != nil {
			t.Fatalf("Score: %v", err)
		}
		if p, ok := verdicts[0].PValue(); ok {
			if seen == 0 {
				first = p
			}
			last = p
			seen++
		}
		if err := obs.Commit(ctx); err != nil {
			t.Fatalf("Commit: %v", err)
		}
	}
	if seen < 2 {
		t.Fatalf("only %d events were evaluated inside the day", seen)
	}
	if last >= first {
		t.Errorf("the p-value did not fall as the day accumulated: %.4g then %.4g", first, last)
	}
	if _, ok, _ := store.FindByEntity(ctx, dSrc, entity); !ok {
		t.Error("no state persisted for the entity")
	}
}

// TestDetectorAbstainsOnAPerfectlyRegularAccount. An account whose count is identical every
// period produces an identical cumulative sum every period, so its own null has no spread and
// there is nothing to standardise against. That is an abstention under R3 rather than a
// manufactured extreme score, and it is asserted because the alternative -- a floor on the
// spread -- would turn a rounding difference into an alert.
func TestDetectorAbstainsOnAPerfectlyRegularAccount(t *testing.T) {
	d, _ := dWire(t, drift.DefaultMinPeriods)
	if _, ok := feed(t, d, "U-clockwork@DOM1", repeat(21, 10)); ok {
		t.Error("a perfectly regular account was evaluated, want an abstention for want of spread")
	}
}

// TestCommitIsIdempotent. The pipeline may commit an observation more than once; a second
// commit that counted the event again would inflate the entity's own baseline and mask the
// drift the arm is looking for.
func TestCommitIsIdempotent(t *testing.T) {
	d, store := dWire(t, 0)
	ctx := context.Background()
	entity := event.EntityID("U-idem@DOM1")

	_, obs, err := d.Score(ctx, dEvent(entity, event.Timestamp(2*event.Hour), 0))
	if err != nil {
		t.Fatalf("Score: %v", err)
	}
	for i := 0; i < 5; i++ {
		if err := obs.Commit(ctx); err != nil {
			t.Fatalf("Commit: %v", err)
		}
	}
	st, ok, _ := store.FindByEntity(ctx, dSrc, entity)
	if !ok {
		t.Fatal("no state persisted")
	}
	if st.PeriodCount != 1 {
		t.Errorf("PeriodCount = %d after five commits of one observation, want 1",
			st.PeriodCount)
	}
}

// TestEmptyPeriodsDecayTheAccumulatedEvidence. An entity that goes quiet must not keep the
// evidence it accumulated while busy: the floor at zero is what makes this a test for a present
// elevation, and the fold across skipped periods is where that gets applied.
func TestEmptyPeriodsDecayTheAccumulatedEvidence(t *testing.T) {
	d, store := dWire(t, 0)
	ctx := context.Background()
	entity := event.EntityID("U-quiet@DOM1")

	// Ten busy days, then one event twelve days later.
	feed(t, d, entity, cycle(10, []int{28, 33, 30, 29, 31}))
	before, _, _ := store.FindByEntity(ctx, dSrc, entity)

	at := event.Timestamp(int64(22) * int64(event.Day))
	_, obs, err := d.Score(ctx, dEvent(entity, at, 9999))
	if err != nil {
		t.Fatalf("Score: %v", err)
	}
	if err := obs.Commit(ctx); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	after, _, _ := store.FindByEntity(ctx, dSrc, entity)

	if after.CompletedPeriods <= before.CompletedPeriods {
		t.Errorf("skipped periods were not closed: %d then %d",
			before.CompletedPeriods, after.CompletedPeriods)
	}
	if after.Cusum > before.Cusum {
		t.Errorf("twelve empty periods raised the cumulative sum, %v to %v",
			before.Cusum, after.Cusum)
	}
}

// TestNewDetectorRejectsAnUnusableShift. The shift is the arm's one tuning parameter; a value
// that describes no increase would make the reference value undefined, and a run configured
// that way must fail at the boundary rather than emit a column of errors.
func TestNewDetectorRejectsAnUnusableShift(t *testing.T) {
	store := &memDrift{states: map[string]*drift.State{}}
	for name, shift := range map[string]float64{
		"one":       1,
		"below one": 0.5,
		"negative":  -2,
	} {
		if _, err := drift.NewDetector(store, novelty.HalfLife(7*event.Day), shift, 8); err == nil {
			t.Errorf("%s: accepted, want an error", name)
		}
	}
	if _, err := drift.NewDetector(store, novelty.HalfLife(7*event.Day), drift.DefaultShift, -1); err == nil {
		t.Error("accepted a negative minimum period count, want an error")
	}
}

// TestDetectorIdentityAndNull. Every detector states its own null in one sentence, which the
// evidence card renders; an arm whose null is not stated cannot be read by an analyst.
func TestDetectorIdentityAndNull(t *testing.T) {
	d, _ := dWire(t, 8)
	if d.ID() != drift.DetectorID {
		t.Errorf("ID = %q, want %q", d.ID(), drift.DetectorID)
	}
	if len(d.NullHypothesis()) < 40 {
		t.Errorf("NullHypothesis is too terse to be a statement of H0: %q", d.NullHypothesis())
	}
}
