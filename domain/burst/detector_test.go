package burst

import (
	"context"
	"errors"
	"math"
	"testing"

	"github.com/JohnPierman/ethogram/domain/calibration"
	"github.com/JohnPierman/ethogram/domain/detector"
	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/novelty"
)

const testSource = event.SourceID("test")

// forever is a half-life long enough that nothing decays over a test's span.
const forever = novelty.HalfLife(1 << 62)

// --- a store that can be caught sharing -------------------------------------

// stateStore is deliberately the NAIVE store: FindByEntity hands back the stored pointer's
// struct by assignment, sharing the Recent backing array. That is what the production stores
// must not do, and a detector that relies on them not doing it is one refactor from breaking
// silently. Testing against the unsafe store proves the detector is safe on its own terms.
type stateStore struct {
	states map[event.EntityID]*State
	shared bool // when true, FindByEntity shares the slice; when false it clones properly
	saves  int
	err    error
}

func newStateStore(shared bool) *stateStore {
	return &stateStore{states: map[event.EntityID]*State{}, shared: shared}
}

func (s *stateStore) FindByEntity(_ context.Context, _ event.SourceID,
	e event.EntityID) (*State, bool, error) {
	if s.err != nil {
		return nil, false, s.err
	}
	st, ok := s.states[e]
	if !ok {
		return nil, false, nil
	}
	if s.shared {
		c := *st // shallow: Recent shares the backing array
		return &c, true, nil
	}
	return st.Clone(), true, nil
}

func (s *stateStore) SaveState(_ context.Context, _ event.SourceID, e event.EntityID,
	st *State) error {
	if s.err != nil {
		return s.err
	}
	s.saves++
	s.states[e] = st
	return nil
}

func newTestEvent(entity string, atSeconds int64) *event.Event {
	e := event.New(testSource, event.EntityID(entity),
		event.Timestamp(atSeconds)*event.Second,
		map[event.FieldPath]event.Value{"dst": event.NewValue("host")}, 0)
	return &e
}

// establish drives an entity past the abstention gate through the detector's own Score/Commit
// pair, so the state under test was built the way a replay builds it.
func establish(t *testing.T, d *Detector, entity string, from int64, gap int64,
	count int) int64 {
	t.Helper()
	at := from
	for i := 0; i < count; i++ {
		at += gap
		_, obs, err := d.Score(context.Background(), newTestEvent(entity, at))
		if err != nil {
			t.Fatalf("score during setup: %v", err)
		}
		if err := obs.Commit(context.Background()); err != nil {
			t.Fatalf("commit during setup: %v", err)
		}
	}
	return at
}

// --- §5.2 ------------------------------------------------------------------

// TestScoreDoesNotAdvanceState is §5.2's requirement at this arm, and it is not a formality
// here: the scan has to be over a set of arrivals INCLUDING the event being scored, so Score
// folds the arrival into what it was handed. If that were the stored state, the event would be
// observed before it was scored — the silent failure §5.2 exists to prevent — and the only thing
// standing between the two is [State.Clone].
//
// The store used here is the unsafe one, sharing its Recent backing array on every read, so the
// test fails if the detector ever stops cloning.
func TestScoreDoesNotAdvanceState(t *testing.T) {
	store := newStateStore(true)
	d := NewDetector(store, forever)
	at := establish(t, d, "alice", 0, 600, 60)

	before, _, err := store.FindByEntity(context.Background(), testSource, "alice")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	heldBefore := len(before.Recent)
	observedBefore := before.Observed
	gapsBefore := before.Gaps

	// Score without committing, twice.
	e := newTestEvent("alice", at+90)
	first, _, err := d.Score(context.Background(), e)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	second, _, err := d.Score(context.Background(), e)
	if err != nil {
		t.Fatalf("rescore: %v", err)
	}

	after, _, err := store.FindByEntity(context.Background(), testSource, "alice")
	if err != nil {
		t.Fatalf("reread: %v", err)
	}
	if len(after.Recent) != heldBefore || after.Observed != observedBefore ||
		after.Gaps != gapsBefore {
		t.Errorf("scoring advanced the stored state: %d held / %d observed / %.0f gap-sum "+
			"became %d / %d / %.0f. Score holds no writer, so this can only be the clone "+
			"sharing its backing array", heldBefore, observedBefore, gapsBefore,
			len(after.Recent), after.Observed, after.Gaps)
	}

	// And the consequence a reader cares about: the same event scored twice must score the
	// same. If the first score advanced the state, the second sees a different history.
	firstLog, ok1 := first[0].LogPValue()
	secondLog, ok2 := second[0].LogPValue()
	if !ok1 || !ok2 {
		t.Fatalf("expected evaluated verdicts, got %v and %v", first[0].Status(),
			second[0].Status())
	}
	if firstLog != secondLog {
		t.Errorf("the same event scored ln p %g then %g", firstLog, secondLog)
	}
}

// TestCommitAdvancesStateExactlyOnce pairs with the above: the write capability lives only
// behind Commit, and it must move the state by one arrival.
func TestCommitAdvancesStateExactlyOnce(t *testing.T) {
	store := newStateStore(false)
	d := NewDetector(store, forever)
	at := establish(t, d, "bob", 0, 300, 40)

	before, _, _ := store.FindByEntity(context.Background(), testSource, "bob")
	_, obs, err := d.Score(context.Background(), newTestEvent("bob", at+300))
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	if err := obs.Commit(context.Background()); err != nil {
		t.Fatalf("commit: %v", err)
	}
	after, _, _ := store.FindByEntity(context.Background(), testSource, "bob")

	if after.Observed != before.Observed+1 {
		t.Errorf("observed gaps went %d -> %d, want one more", before.Observed, after.Observed)
	}
	if after.LastSeen != event.Timestamp(at+300)*event.Second {
		t.Errorf("last seen is %d, want the committed arrival", after.LastSeen)
	}
	if len(after.Recent) > MaxWindow {
		t.Errorf("holding %d timestamps, above the %d bound", len(after.Recent), MaxWindow)
	}
}

// TestTheObservationNamesItsEvent covers the guard that stops an update belonging to one event
// being committed against another.
func TestTheObservationNamesItsEvent(t *testing.T) {
	d := NewDetector(newStateStore(false), forever)
	e := newTestEvent("carol", 100)
	_, obs, err := d.Score(context.Background(), e)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	if obs.EventID() != e.ID() {
		t.Errorf("observation names event %v, want %v", obs.EventID(), e.ID())
	}
	if obs.DetectorID() != DetectorID {
		t.Errorf("observation names detector %q, want %q", obs.DetectorID(), DetectorID)
	}
}

// --- R3 --------------------------------------------------------------------

// TestAColdEntityAbstainsWithItsNumbers is R3: no basis is an outcome with a stated cause, and
// the cause has to carry the gate's own terms or a reader cannot tell how far off it is.
func TestAColdEntityAbstainsWithItsNumbers(t *testing.T) {
	d := NewDetector(newStateStore(false), forever)
	verdicts, _, err := d.Score(context.Background(), newTestEvent("dave", 10))
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	if len(verdicts) != 1 {
		t.Fatalf("got %d verdicts, want 1", len(verdicts))
	}
	v := verdicts[0]
	if _, ok := v.LogPValue(); ok {
		t.Fatal("a first-ever event produced a p-value; there is no rate to test against")
	}
	if !v.Status().IsAbstained() {
		t.Errorf("status %v is not an abstention", v.Status())
	}
	stats := v.Evidence().Stats
	for _, want := range []string{"observed_gaps", "minimum_gaps", "arrivals_held",
		"minimum_held"} {
		if _, ok := stats[want]; !ok {
			t.Errorf("the abstention does not report %q, so a reader cannot see how far "+
				"from the gate this entity is", want)
		}
	}
	if stats["minimum_gaps"] != MinGaps {
		t.Errorf("minimum_gaps is %v, want %d", stats["minimum_gaps"], MinGaps)
	}
}

// TestTheGateIsOnUndiscountedGapsThroughTheDetector is #37's defect checked at the seam rather
// than in the statistic: a gate on the DISCOUNTED count saturates at 1/(1−δ), so under a short
// half-life it becomes unsatisfiable however long the entity is watched. Driving the detector
// with a two-minute half-life and hourly arrivals must still produce an opinion.
func TestTheGateIsOnUndiscountedGapsThroughTheDetector(t *testing.T) {
	store := newStateStore(false)
	d := NewDetector(store, novelty.HalfLife(120*event.Second))
	at := establish(t, d, "erin", 0, 3600, MinGaps+20)

	stored, _, _ := store.FindByEntity(context.Background(), testSource, "erin")
	if stored.Count > 3 {
		t.Fatalf("the discounted count reached %g under a two-minute half-life; this test "+
			"assumes it saturates near one", stored.Count)
	}
	verdicts, _, err := d.Score(context.Background(), newTestEvent("erin", at+3600))
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	if _, ok := verdicts[0].LogPValue(); !ok {
		t.Errorf("with %d observed gaps and a discounted count of %.2f the arm abstained "+
			"(%v): the gate must count undiscounted gaps or it is unsatisfiable rather than "+
			"slow", stored.Observed, stored.Count, verdicts[0].Status())
	}
}

// --- R5 --------------------------------------------------------------------

// TestTheEvidenceLetsTheArithmeticBeRedone is R5. The evidence card must carry every number the
// p-value was computed from, and the check is the strong one: recompute the p-value from the card
// alone and compare.
func TestTheEvidenceLetsTheArithmeticBeRedone(t *testing.T) {
	store := newStateStore(false)
	d := NewDetector(store, forever)
	// A quiet account, then a burst.
	at := establish(t, d, "frank", 0, 1200, 60)
	at = establish(t, d, "frank", at, 90, 11)

	verdicts, _, err := d.Score(context.Background(), newTestEvent("frank", at+90))
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	logP, ok := verdicts[0].LogPValue()
	if !ok {
		t.Fatalf("abstained: %v", verdicts[0].Status())
	}
	stats := verdicts[0].Evidence().Stats

	for _, want := range []string{"window_arrivals", "span_seconds", "rate_per_second",
		"windows_examined", "uncorrected_log_p"} {
		if _, present := stats[want]; !present {
			t.Fatalf("the evidence does not report %q", want)
		}
	}

	// Redo it: the lower incomplete gamma at k−1 and rate*span, then Sidak over the windows.
	k := stats["window_arrivals"]
	redoneMin := calibration.GammaLowerTailLog(k-1, stats["rate_per_second"]*stats["span_seconds"])
	if math.Abs(redoneMin-stats["uncorrected_log_p"]) > 1e-9*math.Abs(redoneMin) {
		t.Errorf("recomputing the uncorrected tail from the card gives %g, the card says %g",
			redoneMin, stats["uncorrected_log_p"])
	}
	redone := calibration.SidakLog(stats["uncorrected_log_p"], int(stats["windows_examined"]))
	if math.Abs(redone-logP) > 1e-9*math.Abs(redone) {
		t.Errorf("recomputing the corrected tail from the card gives %g, the verdict says %g",
			redone, logP)
	}

	// And the reported mean gap must be the rate's reciprocal, since that is the form an
	// analyst actually reads.
	if got, want := stats["mean_gap_seconds"], 1/stats["rate_per_second"]; got != want {
		t.Errorf("mean_gap_seconds is %g, want %g", got, want)
	}
	t.Logf("ln p %.2f from %d arrivals over %.0f s at %.2e/s across %d windows",
		logP, int(k), stats["span_seconds"], stats["rate_per_second"],
		int(stats["windows_examined"]))
}

// --- errors and identity ---------------------------------------------------

// TestAStoreFailureIsWrappedNotSwallowed: a read that fails must surface as an error rather than
// as a cold start, which would silently turn every event into an abstention.
func TestAStoreFailureIsWrappedNotSwallowed(t *testing.T) {
	store := newStateStore(false)
	sentinel := errors.New("disk on fire")
	store.err = sentinel
	d := NewDetector(store, forever)

	if _, _, err := d.Score(context.Background(), newTestEvent("gail", 10)); err == nil {
		t.Error("a failing store produced no error from Score")
	} else if !errors.Is(err, sentinel) {
		t.Errorf("Score lost the cause: %v", err)
	}

	// And on the commit path, which is where a swallowed error loses state silently.
	store.err = nil
	_, obs, err := d.Score(context.Background(), newTestEvent("gail", 20))
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	store.err = sentinel
	if err := obs.Commit(context.Background()); err == nil {
		t.Error("a failing store produced no error from Commit")
	} else if !errors.Is(err, sentinel) {
		t.Errorf("Commit lost the cause: %v", err)
	}
}

// TestIdentityAndNull covers the two strings a result file and an evidence card are built from.
// The null in particular is what §5.2 requires of every detector and what licenses the
// combination in §10.
func TestIdentityAndNull(t *testing.T) {
	d := NewDetector(newStateStore(false), forever)
	if d.ID() != DetectorID {
		t.Errorf("ID is %q", d.ID())
	}
	var _ detector.Detector = d
	null := d.NullHypothesis()
	for _, want := range []string{"Poisson", "Gamma", "Sidak"} {
		if !contains(null, want) {
			t.Errorf("the stated null does not mention %q: %q", want, null)
		}
	}
}

// TestReportCountsWhatItHolds covers the eligibility summary, which is what separates "found
// nothing" from "never able to speak".
func TestReportCountsWhatItHolds(t *testing.T) {
	store := newStateStore(false)
	d := NewDetector(store, forever)
	establish(t, d, "eligible", 0, 600, MinGaps+5)
	establish(t, d, "cold", 0, 600, 2)

	var eligible, total, held int64
	for _, st := range store.states {
		total++
		held += int64(len(st.Recent))
		if st.Eligible() {
			eligible++
		}
	}
	if total != 2 {
		t.Fatalf("holding %d entities, want 2", total)
	}
	if eligible != 1 {
		t.Errorf("%d entities eligible, want 1: an entity with two arrivals has no rate",
			eligible)
	}
	if got := store.states["cold"].Eligible(); got {
		t.Error("a two-arrival entity reported itself eligible")
	}
	if held > int64(MaxWindow)*total {
		t.Errorf("holding %d timestamps across %d entities, above the %d-per-entity bound",
			held, total, MaxWindow)
	}
	var nilState *State
	if nilState.Eligible() {
		t.Error("a nil state reported itself eligible")
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
