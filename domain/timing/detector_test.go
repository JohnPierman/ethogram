package timing_test

import (
	"context"
	"math"
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
	c := *st
	c.Moments = timing.NewMoments(st.Moments.H())
	copy(c.Moments.C, st.Moments.C)
	copy(c.Moments.S, st.Moments.S)
	c.Moments.W = st.Moments.W
	return &c, true, nil
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
	d := timing.NewDetector(newMemoryStates(), 1.5, novelty.HalfLife(7*event.Day), false)
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
	d := timing.NewDetector(repo, 1.5, novelty.HalfLife(7*event.Day), false)

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
	d := timing.NewDetector(repo, 1.5, novelty.HalfLife(7*event.Day), false)

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
	d := timing.NewDetector(repo, 1.5, novelty.HalfLife(7*event.Day), false)

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
	d := timing.NewDetector(repo, 1.5, novelty.HalfLife(7*event.Day), false)
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

// ---------------------------------------------------------------------------
// #26: the level-set mass is floored at its own alert cut
// ---------------------------------------------------------------------------

const gridFloor = 1.0 / (2 * float64(timing.GridSize))

// warmClock drives an entity through `days` days of events inside a working window, so
// that its per-entity ln U null rests on real weight, and returns the detector.
func warmClock(t *testing.T, standardise bool, days int) *timing.Detector {
	t.Helper()
	d := timing.NewDetector(newMemoryStates(), 1.5, novelty.HalfLife(7*event.Day), standardise)
	ctx := context.Background()
	for day := range days {
		for h := 9; h < 17; h++ {
			at := event.Timestamp(day)*event.Day + event.Timestamp(h)*event.Hour
			_, obs, err := d.Score(ctx, tEvent(at))
			if err != nil {
				t.Fatal(err)
			}
			if err := obs.Commit(ctx); err != nil {
				t.Fatal(err)
			}
		}
	}
	return d
}

// TestLevelSetMassIsFlooredAtItsOwnCut states the defect. The mass cannot report below
// 1/(2G) = 9.77e-04, which is at or above the arm's realised cut at 10 and 100 alerts a
// day, so at those budgets the detector cannot alert whatever it observes.
func TestLevelSetMassIsFlooredAtItsOwnCut(t *testing.T) {
	d := warmClock(t, false, 6)
	// 03:00 for an account that has only ever worked 09:00-17:00.
	verdicts, _, err := d.Score(context.Background(), tEvent(6*event.Day+3*event.Hour))
	if err != nil {
		t.Fatal(err)
	}
	p, ok := verdicts[0].PValue()
	if !ok {
		t.Fatal("want an evaluated verdict")
	}
	if p < gridFloor {
		t.Fatalf("mass %v fell below the grid floor %v; the floor is the defect under test", p, gridFloor)
	}
	t.Logf("level-set mass at 03:00 against a 09:00-17:00 clock: %.4e (floor %.4e)", p, gridFloor)
}

// TestStandardisedStatisticGoesBelowTheGridFloor is the fix: the same event, scored
// against the entity's own realised ln U, is not bounded by the grid's resolution.
func TestStandardisedStatisticGoesBelowTheGridFloor(t *testing.T) {
	d := warmClock(t, true, 6)
	verdicts, _, err := d.Score(context.Background(), tEvent(6*event.Day+3*event.Hour))
	if err != nil {
		t.Fatal(err)
	}
	v := verdicts[0]
	if v.Status() != detector.StatusEvaluated {
		t.Fatalf("want an evaluated verdict after six days, got %s: %s", v.Status(), v.Reason())
	}
	p, _ := v.PValue()
	if p >= gridFloor {
		t.Fatalf("standardised p = %v, not below the grid floor %v; #26 is not addressed", p, gridFloor)
	}
	st := v.Evidence().Stats
	if st["log_u_null_ok"] != 1 {
		t.Fatal("the per-entity null should be estimable after six days")
	}
	t.Logf("standardised p = %.4e at z = %.3f (mean ln U %.3f, sd %.3f)",
		p, st["log_u_z"], st["log_u_mean"], st["log_u_sd"])
}

// TestStandardisedAbstainsBelowMinWeight: with too little of the entity's own history the
// null is noise, so the detector abstains rather than standardising against it, and
// still returns the observation so the entity can cross the threshold later.
func TestStandardisedAbstainsBelowMinWeight(t *testing.T) {
	d := timing.NewDetector(newMemoryStates(), 1.5, novelty.HalfLife(7*event.Day), true)
	verdicts, obs, err := d.Score(context.Background(), tEvent(9*event.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if verdicts[0].Status() != detector.StatusAbstainedUnusable {
		t.Fatalf("want abstained_unusable on a cold start, got %s", verdicts[0].Status())
	}
	if _, ok := verdicts[0].PValue(); ok {
		t.Fatal("an abstained verdict carries no p-value (R3)")
	}
	if obs == nil {
		t.Fatal("abstaining must still return an observation")
	}
}

// TestUnstandardisedPathIsUnchanged guards every other figure in the paper: with the flag
// off the reported statistic is exactly the level-set mass, so a run that does not ask for
// #26's statistic measures precisely what it measured before.
func TestUnstandardisedPathIsUnchanged(t *testing.T) {
	d := warmClock(t, false, 6)
	verdicts, _, err := d.Score(context.Background(), tEvent(6*event.Day+3*event.Hour))
	if err != nil {
		t.Fatal(err)
	}
	p, _ := verdicts[0].PValue()
	if got := verdicts[0].Evidence().Stats["tail_mass"]; got != p {
		t.Fatalf("reported %v but tail_mass %v; the default path must report the mass exactly", p, got)
	}
	if verdicts[0].Evidence().Stats["standardised"] != 0 {
		t.Fatal("the default path must record that it did not standardise")
	}
}

// TestStandardisedIsMonotoneWithinAnEntity: the standardised form must not reorder an
// entity's own events, or it would be a different question rather than the same question
// on a scale that can express it.
func TestStandardisedIsMonotoneWithinAnEntity(t *testing.T) {
	ctx := context.Background()
	d := warmClock(t, true, 6)
	type row struct{ mass, reported float64 }
	var rows []row
	for h := 0; h < 24; h++ {
		verdicts, _, err := d.Score(ctx, tEvent(6*event.Day+event.Timestamp(h)*event.Hour))
		if err != nil {
			t.Fatal(err)
		}
		if verdicts[0].Status() != detector.StatusEvaluated {
			continue
		}
		st := verdicts[0].Evidence().Stats
		p, _ := verdicts[0].PValue()
		rows = append(rows, row{st["tail_mass"], p})
	}
	if len(rows) < 12 {
		t.Fatalf("only %d evaluated hours; expected the whole clock", len(rows))
	}
	for i := range rows {
		for j := range rows {
			if rows[i].mass < rows[j].mass && rows[i].reported > rows[j].reported {
				t.Fatalf("mass %v < %v but reported %v > %v: ordering inverted",
					rows[i].mass, rows[j].mass, rows[i].reported, rows[j].reported)
			}
		}
	}
}

// TestStandardisedVerdictSatisfiesR5 is #26's second caution discharged: the reported
// statistic is no longer a density-based tail mass, so the verdict must still carry
// everything needed to rebuild it. An analyst holding only the evidence card recomputes the
// p-value here, from the ln U the event received and the entity's own mean and spread.
func TestStandardisedVerdictSatisfiesR5(t *testing.T) {
	d := warmClock(t, true, 6)
	verdicts, _, err := d.Score(context.Background(), tEvent(6*event.Day+3*event.Hour))
	if err != nil {
		t.Fatal(err)
	}
	v := verdicts[0]
	reported, ok := v.PValue()
	if !ok {
		t.Fatal("want an evaluated verdict")
	}
	st := v.Evidence().Stats

	// Rebuild the statistic from the card alone.
	logU, mean, sd := st["log_u"], st["log_u_mean"], st["log_u_sd"]
	if sd <= 0 {
		t.Fatal("evidence must carry a usable spread")
	}
	z := (logU - mean) / sd
	rebuilt := 0.5 * math.Erfc(-z/math.Sqrt2)

	if math.Abs(rebuilt-reported)/reported > 1e-12 {
		t.Fatalf("rebuilt %.6e from the evidence but the verdict reports %.6e", rebuilt, reported)
	}
	// And ln U must itself be reconstructible from the mass the card also carries.
	if got := math.Log(st["tail_mass"]); math.Abs(got-logU) > 1e-12 {
		t.Fatalf("ln(tail_mass) = %v but log_u = %v", got, logU)
	}
	if st["log_u_z"] != z {
		t.Fatalf("card reports z = %v, recomputed %v", st["log_u_z"], z)
	}
}
