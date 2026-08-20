package volume_test

import (
	"context"
	"testing"

	"github.com/JohnPierman/ethogram/domain/detector"
	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/novelty"
	"github.com/JohnPierman/ethogram/domain/timing"
	"github.com/JohnPierman/ethogram/domain/volume"
)

// Test-only repositories.
type memVolume struct{ states map[string]*volume.State }
type memTiming struct{ states map[string]*timing.State }

func key(s event.SourceID, e event.EntityID) string { return string(s) + "\x1f" + string(e) }

func (m *memVolume) FindByEntity(_ context.Context, s event.SourceID, e event.EntityID) (*volume.State, bool, error) {
	st, ok := m.states[key(s, e)]
	if !ok {
		return nil, false, nil
	}
	c := *st
	return &c, true, nil
}

func (m *memVolume) SaveState(_ context.Context, s event.SourceID, e event.EntityID, st *volume.State) error {
	m.states[key(s, e)] = st
	return nil
}

func (m *memTiming) FindByEntity(_ context.Context, s event.SourceID, e event.EntityID) (*timing.State, bool, error) {
	st, ok := m.states[key(s, e)]
	return st, ok, nil
}

func (m *memTiming) SaveState(_ context.Context, s event.SourceID, e event.EntityID, st *timing.State) error {
	m.states[key(s, e)] = st
	return nil
}

const (
	vSrc    = event.SourceID("lanl.auth")
	vEntity = event.EntityID("U66@DOM1")
)

func vEvent(at event.Timestamp, offset int64) *event.Event {
	e := event.New(vSrc, vEntity, at, map[event.FieldPath]event.Value{
		"auth.authentication_type": event.NewValue("Kerberos"),
	}, offset)
	return &e
}

func wire() (*volume.Detector, *memVolume, *memTiming) {
	mv := &memVolume{states: map[string]*volume.State{}}
	mt := &memTiming{states: map[string]*timing.State{}}
	return volume.NewDetector(mv, mt, 1.5, novelty.HalfLife(7*event.Day), 0), mv, mt
}

// wireGated is wire with the R3 abstention of #25 armed at minPeriods.
func wireGated(minPeriods int64) (*volume.Detector, *memVolume, *memTiming) {
	mv := &memVolume{states: map[string]*volume.State{}}
	mt := &memTiming{states: map[string]*timing.State{}}
	return volume.NewDetector(mv, mt, 1.5, novelty.HalfLife(7*event.Day), minPeriods), mv, mt
}

func TestVolumeColdStartEvaluatesAtOneWhenUngated(t *testing.T) {
	d, _, _ := wire()
	verdicts, obs, err := d.Score(context.Background(), vEvent(9*event.Hour, 1))
	if err != nil {
		t.Fatal(err)
	}
	p, ok := verdicts[0].PValue()
	if !ok || p != 1.0 {
		t.Fatalf("cold start must evaluate at exactly P = 1, got %v", p)
	}
	if obs == nil {
		t.Fatal("expected an observation")
	}
}

// TestVolumeBurstScoresLow: an entity settled at a handful of events per day is hit
// with a burst of two hundred in one hour. The upper tail of (11) must collapse.
func TestVolumeBurstScoresLow(t *testing.T) {
	ctx := context.Background()
	d, _, _ := wire()

	// Fourteen days, five events each, spread across working hours.
	offset := int64(0)
	for day := range 14 {
		for i := range 5 {
			offset++
			at := event.Timestamp(day)*event.Day + event.Timestamp(9+i)*event.Hour
			_, obs, err := d.Score(ctx, vEvent(at, offset))
			if err != nil {
				t.Fatal(err)
			}
			if err := obs.Commit(ctx); err != nil {
				t.Fatal(err)
			}
		}
	}

	// The burst: 200 events inside hour 09 of day 14.
	var last float64 = 1
	for i := range 200 {
		offset++
		at := 14*event.Day + 9*event.Hour + event.Timestamp(i)*10*event.Second
		verdicts, obs, err := d.Score(ctx, vEvent(at, offset))
		if err != nil {
			t.Fatal(err)
		}
		p, _ := verdicts[0].PValue()
		if i > 0 && p > last+1e-12 {
			t.Fatalf("burst event %d scored %v, above the previous %v; the tail must fall "+
				"as the running count climbs", i, p, last)
		}
		last = p
		if err := obs.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if last > 1e-6 {
		t.Errorf("after a 200-event burst against a 5-a-day habit, P = %v; must be tiny", last)
	}
	t.Logf("burst end: P = %.3e", last)
}

// TestVolumeToleratesHabitualDayToDayVariation: an entity whose daily volume has always
// varied by an order of magnitude must not be called astronomically significant for
// varying by an order of magnitude again.
//
// This is the calibration §7.4 claims and equation (11) does not deliver. The Gamma
// posterior of (10) expresses uncertainty about the rate μ, and that uncertainty shrinks
// as history accumulates: with T½ = 7 days the discounted period count settles at
// b ≈ 10.6, so the predictive overdispersion Var/E = (b+ρ)/b is at most about 1.09 and
// the null is Poisson in all but name. Real telemetry is not Poisson — events arrive in
// sessions — so the detector rejects the entity's own habitual behaviour, which is what
// puts 24.7% of all scored events below 1e-12 in results/lanl-sample16-d7-14.json.
//
// The requirement is not that volume never fires. It is that a count sitting inside the
// range the entity has itself exhibited, repeatedly, is not evidence against the entity.
func TestVolumeToleratesHabitualDayToDayVariation(t *testing.T) {
	ctx := context.Background()
	mv := &memVolume{states: map[string]*volume.State{}}
	mt := &memTiming{states: map[string]*timing.State{}}
	vd := volume.NewDetector(mv, mt, 1.5, novelty.HalfLife(7*event.Day), 0)
	td := timing.NewDetector(mt, 1.5, novelty.HalfLife(7*event.Day), false)

	// The entity's habit: one session a day at 09:00, of a size that has always swung
	// between 60 and 480 events. Twenty-one days of exactly that.
	sizes := []int{240, 60, 480, 120, 300, 90, 150}
	offset := int64(0)
	feed := func(day int, count int) {
		for i := range count {
			offset++
			at := event.Timestamp(day)*event.Day + 9*event.Hour + event.Timestamp(i)*10*event.Second
			for _, det := range []detector.Detector{td, vd} {
				_, obs, err := det.Score(ctx, vEvent(at, offset))
				if err != nil {
					t.Fatal(err)
				}
				if err := obs.Commit(ctx); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	for day := range 21 {
		feed(day, sizes[day%len(sizes)])
	}

	// Day 21 is an utterly ordinary day for this entity: 240 events, the middle of its
	// own range, in its own habitual hour. Score the last of them.
	var last float64 = 1
	for i := range 240 {
		offset++
		at := 21*event.Day + 9*event.Hour + event.Timestamp(i)*10*event.Second
		verdicts, obs, err := vd.Score(ctx, vEvent(at, offset))
		if err != nil {
			t.Fatal(err)
		}
		last, _ = verdicts[0].PValue()
		if err := obs.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}

	t.Logf("habitual day, 240 events in the habitual hour: P = %.3e", last)
	if last < 1e-6 {
		t.Errorf("a habitual day scored P = %.3e; a count inside the entity's own "+
			"observed range is not evidence against it, and calling it significant at "+
			"this level is what floods the alert budget with benign traffic", last)
	}
}

// TestVolumeUsesTimingDensityForRho: with a tight 09:00 habit, the same count inside
// the habitual hour is less surprising than at 03:00, because ρ(Ω) is larger where
// the fitted density concentrates. This is the coupling §7.4 prescribes, and the
// shared state it creates between the two halves of Detector II is why Brown's
// correction exists (§10.2).
func TestVolumeUsesTimingDensityForRho(t *testing.T) {
	ctx := context.Background()
	mv := &memVolume{states: map[string]*volume.State{}}
	mt := &memTiming{states: map[string]*timing.State{}}
	vd := volume.NewDetector(mv, mt, 1.5, novelty.HalfLife(7*event.Day), 0)
	td := timing.NewDetector(mt, 1.5, novelty.HalfLife(7*event.Day), false)

	// Build both states: 14 days, 5 events per day at 09:00-13:00.
	offset := int64(0)
	for day := range 14 {
		for i := range 5 {
			offset++
			at := event.Timestamp(day)*event.Day + event.Timestamp(9+i)*event.Hour + 30*event.Minute
			for _, det := range []detector.Detector{td, vd} {
				_, obs, err := det.Score(ctx, vEvent(at, offset))
				if err != nil {
					t.Fatal(err)
				}
				if err := obs.Commit(ctx); err != nil {
					t.Fatal(err)
				}
			}
		}
	}

	pAt := func(at event.Timestamp) (float64, float64) {
		verdicts, _, err := vd.Score(ctx, vEvent(at, offset+1))
		if err != nil {
			t.Fatal(err)
		}
		p, _ := verdicts[0].PValue()
		return p, verdicts[0].Evidence().Stats["rho"]
	}

	// Fifth event in the habitual hour vs the small hours. Prime each hour's window
	// count identically: score four, no commits, so k_obs = 1 both times; the
	// difference is ρ alone.
	pHabit, rhoHabit := pAt(14*event.Day + 9*event.Hour + 30*event.Minute)
	pNight, rhoNight := pAt(14*event.Day + 3*event.Hour + 30*event.Minute)

	if rhoHabit <= rhoNight {
		t.Fatalf("rho(09:00) = %v must exceed rho(03:00) = %v under a 09:00 habit",
			rhoHabit, rhoNight)
	}
	if pHabit < pNight {
		t.Errorf("the same count is more surprising in the habitual hour: P(09) = %v < P(03) = %v",
			pHabit, pNight)
	}
	t.Logf("rho(09:30)=%.4f rho(03:30)=%.6f  P(09:30)=%.4f P(03:30)=%.4f",
		rhoHabit, rhoNight, pHabit, pNight)
}

// TestVolumeScoreBeforeObserveAndIdempotence mirrors the §5.2 ordering guarantees.
func TestVolumeScoreBeforeObserveAndIdempotence(t *testing.T) {
	ctx := context.Background()
	d, mv, _ := wire()

	e := vEvent(9*event.Hour, 1)
	_, obs, err := d.Score(ctx, e)
	if err != nil {
		t.Fatal(err)
	}
	if len(mv.states) != 0 {
		t.Fatal("Score wrote state; §5.2 forbids it")
	}
	for range 3 {
		if err := obs.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	st, ok, _ := mv.FindByEntity(ctx, vSrc, vEntity)
	if !ok || st.WindowCount != 1 || st.PeriodCount != 1 {
		t.Fatalf("triple commit produced %+v; counters must be 1", st)
	}
}

// TestVolumePeriodFolding: crossing days folds completed periods into the posterior
// with the per-period discount, including empty intervening days.
func TestVolumePeriodFolding(t *testing.T) {
	ctx := context.Background()
	d, mv, _ := wire()

	// Three events on day 0.
	for i := range 3 {
		_, obs, err := d.Score(ctx, vEvent(event.Timestamp(9+i)*event.Hour, int64(i)))
		if err != nil {
			t.Fatal(err)
		}
		if err := obs.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	// Next event on day 3: days 0 (count 3), 1 and 2 (empty) must fold.
	verdicts, obs, err := d.Score(ctx, vEvent(3*event.Day+9*event.Hour, 10))
	if err != nil {
		t.Fatal(err)
	}
	a := verdicts[0].Evidence().Stats["a"]
	b := verdicts[0].Evidence().Stats["b"]
	if a <= 0 || b <= 0 {
		t.Fatalf("posterior empty at scoring time: a=%v b=%v; completed days must fold "+
			"functionally before scoring", a, b)
	}
	// δ = 2^(−1/7) per day at T½ = 7 days. Folding day0 k=3 then two empty days:
	// a = ((3·δ⁰)·δ + 0)·δ = 3δ² ... precisely: a₁ = δ·0+3 = 3; a₂ = δ·3; a₃ = δ²·3.
	// b: b₁ = δ·0+1 = 1; b₂ = δ+1; b₃ = δ²+δ+1.
	delta := 0.9057236642639067 // 2^(-1/7)
	wantA := 3 * delta * delta
	wantB := delta*delta + delta + 1
	if diff := a - wantA; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("a = %v, want %v", a, wantA)
	}
	if diff := b - wantB; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("b = %v, want %v", b, wantB)
	}
	if err := obs.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	st, _, _ := mv.FindByEntity(ctx, vSrc, vEntity)
	if st.PeriodIndex != 3 || st.PeriodCount != 1 {
		t.Fatalf("commit did not advance the period: %+v", st)
	}
}

// ---------------------------------------------------------------------------
// #25: volume must abstain where the posterior has no completed period to rest on
// ---------------------------------------------------------------------------

// TestVolumeAbstainsBelowMinPeriods is the defect of #25 stated as a test. Scoring an
// entity's first period reports the PRIOR's tail as though it were the entity's, which is
// what put 13,618 events below 1e-12 where a calibrated null predicts 4.5e-06 and pinned
// the realised cut at 1.12e-12 at every budget.
func TestVolumeAbstainsBelowMinPeriods(t *testing.T) {
	d, _, _ := wireGated(1)
	verdicts, obs, err := d.Score(context.Background(), vEvent(9*event.Hour, 1))
	if err != nil {
		t.Fatal(err)
	}
	if len(verdicts) != 1 {
		t.Fatalf("want one verdict, got %d", len(verdicts))
	}
	v := verdicts[0]
	if v.Status() != detector.StatusAbstainedUnusable {
		t.Fatalf("want %s, got %s", detector.StatusAbstainedUnusable, v.Status())
	}
	if _, ok := v.PValue(); ok {
		t.Fatal("an abstained verdict must carry no p-value (R3)")
	}
	if v.Reason() == "" {
		t.Fatal("an abstention must say why")
	}
	if got := v.Evidence().Stats["completed_periods"]; got != 0 {
		t.Fatalf("completed_periods = %v, want 0 on a first period", got)
	}
	if got := v.Evidence().Stats["minimum"]; got != 1 {
		t.Fatalf("minimum = %v, want 1", got)
	}
	if obs == nil {
		t.Fatal("abstaining must still return an observation, or the gate never opens")
	}
}

// TestVolumeAbstentionIsProvisionalNotPermanent is the property that makes the gate safe:
// an abstained event still advances state, so the entity crosses the threshold on schedule.
// Withholding the observation would strand every entity below the gate forever and turn a
// calibration fix into a silent arm.
func TestVolumeAbstentionIsProvisionalNotPermanent(t *testing.T) {
	ctx := context.Background()
	d, _, _ := wireGated(3)

	var abstained, evaluated int
	offset := int64(0)
	for day := range 8 {
		for i := range 4 {
			offset++
			at := event.Timestamp(day)*event.Day + event.Timestamp(9+i)*event.Hour
			verdicts, obs, err := d.Score(ctx, vEvent(at, offset))
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := verdicts[0].PValue(); ok {
				evaluated++
			} else {
				abstained++
			}
			if err := obs.Commit(ctx); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Days 0, 1 and 2 close 0, 1 and 2 periods respectively, so the first three days
	// abstain at a threshold of 3 and every later day evaluates.
	if abstained != 12 {
		t.Errorf("abstained on %d events, want 12 (days 0-2 at 4 events each)", abstained)
	}
	if evaluated != 20 {
		t.Errorf("evaluated %d events, want 20 (days 3-7)", evaluated)
	}
}

// TestVolumeCompletedPeriodsIsUndiscounted separates the new counter from the posterior's
// b. With T½ = 7 days the discounted count settles near 10.6 and cannot express "how many
// periods" for an established entity, which is why the gate needs its own counter.
func TestVolumeCompletedPeriodsIsUndiscounted(t *testing.T) {
	ctx := context.Background()
	d, mv, _ := wireGated(0)

	offset := int64(0)
	const days = 30
	for day := range days {
		offset++
		at := event.Timestamp(day)*event.Day + 9*event.Hour
		verdicts, obs, err := d.Score(ctx, vEvent(at, offset))
		if err != nil {
			t.Fatal(err)
		}
		if got, want := verdicts[0].Evidence().Stats["completed_periods"], float64(day); got != want {
			t.Fatalf("day %d: completed_periods = %v, want %v", day, got, want)
		}
		if err := obs.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}

	st, ok, err := mv.FindByEntity(ctx, vSrc, vEntity)
	if err != nil || !ok {
		t.Fatalf("state missing: ok=%v err=%v", ok, err)
	}
	if st.CompletedPeriods != days-1 {
		t.Errorf("CompletedPeriods = %d, want %d", st.CompletedPeriods, days-1)
	}
	if st.Rate.B > 11 {
		t.Errorf("Rate.B = %v; the discounted count is expected to saturate near 10.6", st.Rate.B)
	}
}
