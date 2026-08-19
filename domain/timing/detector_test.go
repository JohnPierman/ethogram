package timing_test

import (
	"context"
	"testing"

	"github.com/JohnPierman/ethogram/domain/detector"
	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/novelty"
	"github.com/JohnPierman/ethogram/domain/timing"
)

// memoryStates is a test-only StateRepository.
type memoryStates struct {
	states map[string]*timing.State
}

func newMemoryStates() *memoryStates { return &memoryStates{states: map[string]*timing.State{}} }

func stateKey(s event.SourceID, e event.EntityID) string { return string(s) + "\x1f" + string(e) }

func (m *memoryStates) FindByEntity(_ context.Context, s event.SourceID, e event.EntityID) (*timing.State, bool, error) {
	st, ok := m.states[stateKey(s, e)]
	if !ok {
		return nil, false, nil
	}
	// Return a copy so a caller cannot mutate stored state outside Commit, which is
	// the same guarantee a database round-trip gives.
	c := &timing.State{Moments: timing.NewMoments(st.Moments.H()), LastSeen: st.LastSeen}
	copy(c.Moments.C, st.Moments.C)
	copy(c.Moments.S, st.Moments.S)
	c.Moments.W = st.Moments.W
	return c, true, nil
}

func (m *memoryStates) SaveState(_ context.Context, s event.SourceID, e event.EntityID, st *timing.State) error {
	m.states[stateKey(s, e)] = st
	return nil
}

const (
	tSrc    = event.SourceID("lanl.auth")
	tEntity = event.EntityID("U66@DOM1")
)

func tEvent(at event.Timestamp) *event.Event {
	e := event.New(tSrc, tEntity, at, map[event.FieldPath]event.Value{
		"auth.authentication_type": event.NewValue("Kerberos"),
	}, 1)
	return &e
}

func TestTimingDetectorColdStartEvaluatesAtOne(t *testing.T) {
	d := timing.NewDetector(newMemoryStates(), 1.5, novelty.HalfLife(7*event.Day))
	verdicts, obs, err := d.Score(context.Background(), tEvent(3*event.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(verdicts) != 1 {
		t.Fatalf("want one verdict, got %d", len(verdicts))
	}
	p, ok := verdicts[0].PValue()
	if !ok || p != 1.0 {
		t.Fatalf("cold start must evaluate at exactly P = 1, got %v (ok=%v)", p, ok)
	}
	if obs == nil {
		t.Fatal("an observation must be returned for commit")
	}
}

// TestTimingDetectorLearnsHabit builds a 09:00 habit and requires a habitual time to
// be unremarkable and the antipode unusual, end to end through the §5.2 interface.
func TestTimingDetectorLearnsHabit(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryStates()
	d := timing.NewDetector(repo, 1.5, novelty.HalfLife(7*event.Day))

	// Score-then-commit thirty daily events at 09:00.
	for i := range 30 {
		e := tEvent(event.Timestamp(i)*event.Day + 9*event.Hour)
		_, obs, err := d.Score(ctx, e)
		if err != nil {
			t.Fatal(err)
		}
		if err := obs.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}

	pAt := func(at event.Timestamp) float64 {
		verdicts, _, err := d.Score(ctx, tEvent(at))
		if err != nil {
			t.Fatal(err)
		}
		p, _ := verdicts[0].PValue()
		return p
	}

	habitual := pAt(30*event.Day + 9*event.Hour)
	nearby := pAt(30*event.Day + 10*event.Hour)
	antipode := pAt(30*event.Day + 21*event.Hour)

	if habitual < 0.5 {
		t.Errorf("P(09:00) = %v; the habitual time must be unremarkable", habitual)
	}
	if antipode > 0.01 {
		t.Errorf("P(21:00) = %v; the antipode of a tight habit must be unusual", antipode)
	}
	if !(habitual >= nearby && nearby >= antipode) {
		t.Errorf("ordering violated: %v, %v, %v", habitual, nearby, antipode)
	}
	t.Logf("habit: P(09:00)=%.4f P(10:00)=%.4f P(21:00)=%.6f", habitual, nearby, antipode)
}

// TestGridTailMassAgreesWithLevelIndex ties the dot-product scoring path to the
// LevelIndex construction the paper describes: same moments, same level, same mass.
func TestGridTailMassAgreesWithLevelIndex(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryStates()
	d := timing.NewDetector(repo, 1.5, novelty.HalfLife(7*event.Day))

	for i := range 10 {
		e := tEvent(event.Timestamp(i)*event.Day + 9*event.Hour)
		_, obs, _ := d.Score(ctx, e)
		if err := obs.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	st, ok, _ := repo.FindByEntity(ctx, tSrc, tEntity)
	if !ok {
		t.Fatal("state missing")
	}

	kappa := timing.KappaForBandwidthHours(1.5)
	H := timing.HarmonicOrder(kappa)
	density := timing.NewDensity(st.Moments, timing.KernelCoefficients(kappa, H))
	ix := timing.NewLevelIndex(density, timing.GridSize)

	for _, hour := range []event.Timestamp{0, 4, 9, 12, 15, 21} {
		verdicts, _, err := d.Score(ctx, tEvent(30*event.Day+hour*event.Hour))
		if err != nil {
			t.Fatal(err)
		}
		got, _ := verdicts[0].PValue()
		level := density.Evaluate(timing.PhaseOfTimestamp(hour * event.Hour))
		want := ix.TailMass(level)
		if diff := got - want; diff > 1e-9 || diff < -1e-9 {
			t.Errorf("hour %d: detector path %v, LevelIndex path %v", hour, got, want)
		}
	}
}

// TestTimingScoreBeforeObserve: the first event of an entity must not see itself.
// After a single committed event, scoring the same time is P = 1 (it is the mode);
// but the FIRST score, before any commit, must also be 1 for the cold-start reason,
// and crucially the state after one commit carries exactly one observation.
func TestTimingScoreBeforeObserve(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryStates()
	d := timing.NewDetector(repo, 1.5, novelty.HalfLife(7*event.Day))

	e := tEvent(9 * event.Hour)
	verdicts, obs, err := d.Score(ctx, e)
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := verdicts[0].PValue(); p != 1.0 {
		t.Fatalf("first score must be cold-start P = 1, got %v", p)
	}
	// W must still be zero: scoring did not write.
	if _, ok, _ := repo.FindByEntity(ctx, tSrc, tEntity); ok {
		t.Fatal("Score wrote state; §5.2 forbids it")
	}

	if err := obs.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := obs.Commit(ctx); err != nil { // idempotent
		t.Fatal(err)
	}
	st, ok, _ := repo.FindByEntity(ctx, tSrc, tEntity)
	if !ok {
		t.Fatal("commit did not persist")
	}
	if st.Moments.W != 1 {
		t.Fatalf("double commit double-counted: W = %v, want 1", st.Moments.W)
	}
}

// TestTimingEvidenceCarriesMoments: R5 requires the verdict carry what recomputation
// needs — the moments, κ, H, W and the grid parameters.
func TestTimingEvidenceCarriesMoments(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryStates()
	d := timing.NewDetector(repo, 1.5, novelty.HalfLife(7*event.Day))
	_, obs, _ := d.Score(ctx, tEvent(9*event.Hour))
	if err := obs.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	verdicts, _, err := d.Score(ctx, tEvent(event.Day+10*event.Hour))
	if err != nil {
		t.Fatal(err)
	}
	ev := verdicts[0].Evidence().Stats
	for _, name := range []string{"phi", "density_at_phi", "kappa", "bandwidth_hours",
		"H", "W", "grid", "grid_floor", "half_life_us", "c_01", "s_01", "c_11", "s_11"} {
		if _, ok := ev[name]; !ok {
			t.Errorf("evidence is missing %q", name)
		}
	}
	if verdicts[0].Evidence().Labels["observed_clock"] != "10:00" {
		t.Errorf("observed_clock = %q, want 10:00", verdicts[0].Evidence().Labels["observed_clock"])
	}
	if verdicts[0].Status() != detector.StatusEvaluated {
		t.Errorf("status = %s", verdicts[0].Status())
	}
}
