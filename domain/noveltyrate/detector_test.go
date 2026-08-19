package noveltyrate

import (
	"context"
	"math"
	"testing"

	"github.com/JohnPierman/ethogram/domain/detector"
	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/novelty"
	"github.com/JohnPierman/ethogram/domain/registry"
)

const (
	source = event.SourceID("test")
	dst    = event.FieldPath("dst")
)

// --- fakes -----------------------------------------------------------------

type valueStore struct {
	rows map[string]map[string]float64 // entity -> value -> count
}

func newValueStore() *valueStore {
	return &valueStore{rows: map[string]map[string]float64{}}
}

func (s *valueStore) seed(entity string, values ...string) {
	if s.rows[entity] == nil {
		s.rows[entity] = map[string]float64{}
	}
	for _, v := range values {
		s.rows[entity][v]++
	}
}

func (s *valueStore) FindAllByEntityField(_ context.Context, _ event.SourceID,
	e event.EntityID, _ event.FieldPath, _ event.Timestamp) ([]novelty.ValueRow, error) {
	var out []novelty.ValueRow
	for v, c := range s.rows[string(e)] {
		out = append(out, novelty.ValueRow{Value: v, Count: c})
	}
	return out, nil
}

func (s *valueStore) SaveObservation(context.Context, event.SourceID, event.EntityID,
	event.FieldPath, string, event.Timestamp) error {
	return nil
}

type stateStore struct{ states map[string]*State }

func newStateStore() *stateStore { return &stateStore{states: map[string]*State{}} }

func (s *stateStore) FindByEntity(_ context.Context, _ event.SourceID,
	e event.EntityID) (*State, bool, error) {
	st, ok := s.states[string(e)]
	if !ok {
		return nil, false, nil
	}
	clone := *st
	return &clone, true, nil
}

func (s *stateStore) SaveState(_ context.Context, _ event.SourceID, e event.EntityID,
	st *State) error {
	clone := *st
	s.states[string(e)] = &clone
	return nil
}

type fieldRegistry struct{}

func (fieldRegistry) FindBySource(event.SourceID) []*registry.Entry {
	return []*registry.Entry{{Path: dst, Kind: registry.KindCategorical}}
}

func (fieldRegistry) StatusForAbsent(event.SourceID, event.FieldPath) registry.AbsenceKind {
	return registry.AbsenceStructural
}

// --- helpers ---------------------------------------------------------------

func newEvent(t *testing.T, entity string, at int64, value string) *event.Event {
	t.Helper()
	e := event.New(source, event.EntityID(entity), event.Timestamp(at),
		map[event.FieldPath]event.Value{dst: event.NewValue(value)}, 0)
	return &e
}

// runHistory feeds `windows` complete hours of `perWindow` events each, of which
// `novelPerWindow` carry a value the entity has never seen, and returns the detector and
// its stores positioned at the start of the next window.
func runHistory(t *testing.T, entity string, windows, perWindow, novelPerWindow int) (
	*Detector, *valueStore, *stateStore, int64) {
	t.Helper()
	values, states := newValueStore(), newStateStore()
	d := NewDetector(states, values, fieldRegistry{}, novelty.HalfLife(7*86400))

	serial := 0
	for w := range windows {
		base := int64(w) * WindowSeconds
		for i := range perWindow {
			at := base + int64(i)
			v := "known"
			if i < novelPerWindow {
				serial++
				v = "new" + string(rune('a'+serial%26)) + string(rune('a'+serial/26))
			}
			e := newEvent(t, entity, at, v)
			_, obs, err := d.Score(t.Context(), e)
			if err != nil {
				t.Fatalf("score: %v", err)
			}
			if err := obs.Commit(t.Context()); err != nil {
				t.Fatalf("commit: %v", err)
			}
			values.seed(entity, v)
		}
	}
	return d, values, states, int64(windows) * WindowSeconds
}

// burst scores `n` novel events inside one window and returns the last verdict's ln p.
func burst(t *testing.T, d *Detector, values *valueStore, entity string, start int64, n int) float64 {
	t.Helper()
	var logP float64
	for i := range n {
		v := "burst" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		e := newEvent(t, entity, start+int64(i), v)
		vs, obs, err := d.Score(t.Context(), e)
		if err != nil {
			t.Fatalf("score: %v", err)
		}
		if len(vs) != 1 {
			t.Fatalf("got %d verdicts, want 1", len(vs))
		}
		if lp, ok := vs[0].LogPValue(); ok {
			logP = lp
		}
		if err := obs.Commit(t.Context()); err != nil {
			t.Fatalf("commit: %v", err)
		}
		values.seed(entity, v)
	}
	return logP
}

// window scores a window of `total` events of which the first `novel` carry values the
// entity has never seen, and returns the last verdict's ln p. burst is the special case
// where every event is novel; an ordinary window needs the denominator too, or "two novel
// values" would be read as two out of two rather than two out of forty.
func window(t *testing.T, d *Detector, values *valueStore, entity string, start int64,
	total, novel int) float64 {
	t.Helper()
	var logP float64
	for i := range total {
		v := "known"
		if i < novel {
			v = "w" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		}
		e := newEvent(t, entity, start+int64(i), v)
		vs, obs, err := d.Score(t.Context(), e)
		if err != nil {
			t.Fatalf("score: %v", err)
		}
		if lp, ok := vs[0].LogPValue(); ok {
			logP = lp
		}
		if err := obs.Commit(t.Context()); err != nil {
			t.Fatalf("commit: %v", err)
		}
		values.seed(entity, v)
	}
	return logP
}

// --- the property this detector exists for ---------------------------------

// TestTheScoreDoesNotScaleWithHowBusyTheAccountIs is the whole point.
//
// Detector I's p-value for a first-ever value is essentially 1/n, so a small account cannot
// reach any useful threshold however it behaves — measured on planted attacks, `p × n` had
// median 1.15 and no attack on an ordinary account could be alerted at all. This detector
// must not inherit that. Two accounts departing from their own habit by the same factor
// must score comparably even when one carries fifty times the traffic of the other.
func TestTheScoreDoesNotScaleWithHowBusyTheAccountIs(t *testing.T) {
	// Both accounts historically produce novelty at 1 event in 20. Both then produce a
	// window of 20 events of which 15 are novel — the same departure, different volumes.
	small, _, _, _ := runHistory(t, "small", 5, 20, 1)
	large, _, _, _ := runHistory(t, "large", 5, 1000, 50)

	smallValues := small.values.(*valueStore)
	largeValues := large.values.(*valueStore)

	pSmall := burst(t, small, smallValues, "small", 5*WindowSeconds, 15)
	pLarge := burst(t, large, largeValues, "large", 5*WindowSeconds, 15)

	if math.IsInf(pSmall, 0) || math.IsInf(pLarge, 0) {
		t.Fatalf("a burst scored an infinite log p: small %v large %v", pSmall, pLarge)
	}
	// They need not be identical — the larger account's rate is better pinned down, so it
	// is legitimately a little more surprised — but they must be the same order of
	// magnitude, where Detector I would differ by the 50x volume ratio.
	ratio := pSmall / pLarge
	if ratio < 0.25 || ratio > 4 {
		t.Errorf("ln p for the same departure differs by %.2gx between a small account "+
			"(%v) and one 50x busier (%v); the score is still tracking volume",
			ratio, pSmall, pLarge)
	}
	// And both must be extreme enough to be worth alerting on at all.
	if pSmall > math.Log(1e-6) {
		t.Errorf("a small account going from 5%% novelty to 75%% scores ln p = %v, which "+
			"is not extreme enough to win a slot at any realistic budget", pSmall)
	}
}

// TestAnOrdinaryWindowIsNotSurprising: an account behaving exactly as it always has must
// not be alerted on. Without this the detector would fire on everything and the budget
// would be allocated by tie-break.
func TestAnOrdinaryWindowIsNotSurprising(t *testing.T) {
	d, values, _, next := runHistory(t, "u", 6, 40, 2)
	// Its historical rate is 2 in 40; produce exactly that again, denominator included.
	logP := window(t, d, values, "u", next, 40, 2)
	if logP < math.Log(0.01) {
		t.Errorf("an account repeating its own habit scored ln p = %v (p = %.3g), which "+
			"would put ordinary behaviour in the alert budget", logP, math.Exp(logP))
	}
}

// TestMoreNoveltyIsNeverLessSurprising within one window.
func TestMoreNoveltyIsNeverLessSurprising(t *testing.T) {
	prev := math.Inf(1)
	for _, n := range []int{1, 3, 6, 12, 24} {
		d, values, _, next := runHistory(t, "u", 6, 40, 2)
		got := window(t, d, values, "u", next, 40, n)
		if got > prev+1e-9 {
			t.Errorf("%d novel values scored ln p = %v, less surprising than the previous "+
				"smaller burst at %v", n, got, prev)
		}
		prev = got
	}
}

// TestTooLittleHistoryAbstainsRatherThanGuessing. R3 forbids a neutral score standing in
// for absent evidence: a rate estimated from ten events is the prior's opinion, not the
// account's, and reporting it as a p-value would launder the prior into the ranking.
func TestTooLittleHistoryAbstainsRatherThanGuessing(t *testing.T) {
	d, values, _, next := runHistory(t, "u", 1, 10, 1)
	e := newEvent(t, "u", next, "brand-new")
	vs, _, err := d.Score(t.Context(), e)
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 {
		t.Fatalf("got %d verdicts, want 1", len(vs))
	}
	if vs[0].Status() != detector.StatusAbstainedUnusable {
		t.Errorf("status = %v, want abstained_unusable for an account with 10 events of "+
			"history", vs[0].Status())
	}
	if _, ok := vs[0].PValue(); ok {
		t.Error("an abstained verdict reported a p-value")
	}
	_ = values
}

// TestTheWindowInProgressIsExcludedFromTheRateItIsJudgedAgainst.
//
// If the burst were folded into the history as it happened, the account's estimated rate
// would rise with the attack and the attack would talk itself down. The stored history
// covers completed windows only, and this pins that.
func TestTheWindowInProgressIsExcludedFromTheRateItIsJudgedAgainst(t *testing.T) {
	d, values, states, next := runHistory(t, "u", 6, 40, 2)

	before, _, err := states.FindByEntity(t.Context(), source, "u")
	if err != nil {
		t.Fatal(err)
	}
	burst(t, d, values, "u", next, 20)
	after, _, err := states.FindByEntity(t.Context(), source, "u")
	if err != nil {
		t.Fatal(err)
	}

	// The history must have grown by exactly the window that closed when the burst
	// opened a new one — not by the burst itself.
	if after.WindowNovel != 20 || after.WindowTotal != 20 {
		t.Errorf("window counts = %d novel of %d, want the burst held in the open window",
			after.WindowNovel, after.WindowTotal)
	}
	grew := after.HistoryTotal - before.HistoryTotal
	if grew > float64(before.WindowTotal)+1e-9 {
		t.Errorf("history grew by %v, more than the %d events of the window that closed; "+
			"the burst is inflating the rate it is judged against",
			grew, before.WindowTotal)
	}
}

// TestScoreDoesNotMutateState is the section 5.2 capability separation, checked rather than
// assumed. Violating it is silent: the event would be counted before being scored, so a
// burst would partly explain itself away while continuing to emit plausible numbers.
func TestScoreDoesNotMutateState(t *testing.T) {
	d, values, states, next := runHistory(t, "u", 6, 40, 2)
	before, _, err := states.FindByEntity(t.Context(), source, "u")
	if err != nil {
		t.Fatal(err)
	}

	for range 5 {
		if _, _, scoreErr := d.Score(t.Context(), newEvent(t, "u", next, "never-seen")); scoreErr != nil {
			t.Fatal(scoreErr)
		}
	}
	after, _, err := states.FindByEntity(t.Context(), source, "u")
	if err != nil {
		t.Fatal(err)
	}
	if *before != *after {
		t.Errorf("scoring changed state:\n before %+v\n after  %+v", *before, *after)
	}
	_ = values
}

// TestRepeatedScoringOfTheSameEventIsIdentical (R4): the same event scored twice against
// the same state must produce the same verdict, or a replay would not reproduce the run.
func TestRepeatedScoringOfTheSameEventIsIdentical(t *testing.T) {
	d, _, _, next := runHistory(t, "u", 6, 40, 2)
	e := newEvent(t, "u", next, "never-seen")

	first, _, err := d.Score(t.Context(), e)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := d.Score(t.Context(), e)
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest() != second.Digest() {
		t.Error("scoring the same event twice produced different verdicts")
	}
}

// TestAKnownValueStillCountsTowardsTheWindow: the denominator is every event, not only the
// novel ones. Counting only novel events would make one novel value in a thousand look the
// same as one in one.
func TestAKnownValueStillCountsTowardsTheWindow(t *testing.T) {
	d, values, states, next := runHistory(t, "u", 6, 40, 2)
	for i := range 7 {
		e := newEvent(t, "u", next+int64(i), "known")
		_, obs, err := d.Score(t.Context(), e)
		if err != nil {
			t.Fatal(err)
		}
		if err := obs.Commit(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	st, _, err := states.FindByEntity(t.Context(), source, "u")
	if err != nil {
		t.Fatal(err)
	}
	if st.WindowTotal != 7 || st.WindowNovel != 0 {
		t.Errorf("window = %d novel of %d, want 0 of 7", st.WindowNovel, st.WindowTotal)
	}
	_ = values
}
