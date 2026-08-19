package pairing_test

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/JohnPierman/ethogram/domain/cooccurrence"
	"github.com/JohnPierman/ethogram/domain/detector"
	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/novelty"
	"github.com/JohnPierman/ethogram/domain/pairing"
	"github.com/JohnPierman/ethogram/domain/registry"
)

// ---------------------------------------------------------------------------
// Fixtures: the same in-memory per-entity value store Detector I is tested against,
// because this detector deliberately uses that store and no other.
// ---------------------------------------------------------------------------

type memoryRow struct {
	count     float64
	firstSeen event.Timestamp
	lastSeen  event.Timestamp
}

type memoryRepository struct {
	halfLife novelty.HalfLife
	rows     map[string]map[string]*memoryRow
}

func newMemoryRepository(halfLife novelty.HalfLife) *memoryRepository {
	return &memoryRepository{halfLife: halfLife, rows: map[string]map[string]*memoryRow{}}
}

func rowKey(s event.SourceID, en event.EntityID, f event.FieldPath) string {
	return string(s) + "\x1f" + string(en) + "\x1f" + string(f)
}

func (m *memoryRepository) FindAllByEntityField(_ context.Context, s event.SourceID,
	en event.EntityID, f event.FieldPath, at event.Timestamp) ([]novelty.ValueRow, error) {
	byValue := m.rows[rowKey(s, en, f)]
	values := make([]string, 0, len(byValue))
	for v := range byValue {
		values = append(values, v)
	}
	slices.Sort(values)
	out := make([]novelty.ValueRow, 0, len(values))
	for _, v := range values {
		r := byValue[v]
		out = append(out, novelty.ValueRow{
			Value:     v,
			Count:     novelty.Decay(r.count, r.lastSeen, at, m.halfLife),
			FirstSeen: r.firstSeen,
			LastSeen:  r.lastSeen,
		})
	}
	return out, nil
}

func (m *memoryRepository) SaveObservation(_ context.Context, s event.SourceID,
	en event.EntityID, f event.FieldPath, value string, at event.Timestamp) error {
	key := rowKey(s, en, f)
	byValue, ok := m.rows[key]
	if !ok {
		byValue = map[string]*memoryRow{}
		m.rows[key] = byValue
	}
	r, ok := byValue[value]
	if !ok {
		byValue[value] = &memoryRow{count: 1, firstSeen: at, lastSeen: at}
		return nil
	}
	r.count = novelty.Accumulate(r.count, r.lastSeen, at, m.halfLife)
	r.lastSeen = at
	return nil
}

const (
	src          = event.SourceID("lanl.auth")
	dayHL        = novelty.HalfLife(event.Day)
	warmEvents   = 300
	fAuthType    = event.FieldPath("auth.authentication_type")
	fDstComputer = event.FieldPath("auth.destination_computer")
	fLogonType   = event.FieldPath("auth.logon_type")
)

func mkEvent(entity event.EntityID, at event.Timestamp,
	fields map[event.FieldPath]event.Value, offset int64) *event.Event {
	e := event.New(src, entity, at, fields, offset)
	return &e
}

// warmRegistry settles the three fields as categorical, so all three are eligible and
// every event induces C(3,2) = 3 pairings.
func warmRegistry() *registry.Registry {
	reg := registry.New(registry.DefaultPolicy())
	for i := range warmEvents {
		reg.ObserveEvent(mkEvent("warm", event.Timestamp(i+1)*event.Second,
			map[event.FieldPath]event.Value{
				fAuthType:    event.NewValue([]string{"Negotiate", "Kerberos", "NTLM"}[i%3]),
				fDstComputer: event.NewValue(fmt.Sprintf("C%d", 700+i%11)),
				fLogonType:   event.NewValue([]string{"Network", "Batch"}[i%2]),
			}, int64(i)))
	}
	return reg
}

func wired() (*pairing.Detector, *memoryRepository, *registry.Registry) {
	reg := warmRegistry()
	repo := newMemoryRepository(dayHL)
	return pairing.NewDetector(repo, reg, 1.0, dayHL), repo, reg
}

// scoreAndCommit scores an event and applies the resulting observation, which is the
// order §5.2 requires and the order a replay uses.
func scoreAndCommit(t *testing.T, d *pairing.Detector, e *event.Event) detector.Verdicts {
	t.Helper()
	ctx := context.Background()
	verdicts, obs, err := d.Score(ctx, e)
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	if err := obs.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return verdicts
}

func onlyEvaluated(t *testing.T, verdicts detector.Verdicts) detector.Verdict {
	t.Helper()
	if len(verdicts) != 1 {
		t.Fatalf("got %d verdicts, want exactly 1: one per event, not one per pair, or a "+
			"single event carrying many fields dominates the combination's degrees of freedom",
			len(verdicts))
	}
	v := verdicts[0]
	if v.Status() != detector.StatusEvaluated {
		t.Fatalf("verdict status = %v, want evaluated", v.Status())
	}
	return v
}

// ---------------------------------------------------------------------------
// Behaviour
// ---------------------------------------------------------------------------

func TestANovelPairingScoresBelowAHabitualOne(t *testing.T) {
	d, _, _ := wired()

	// The entity establishes a habit: always Kerberos on C700, Network logon.
	for i := range 40 {
		scoreAndCommit(t, d, mkEvent("U1@DOM1", event.Timestamp(i+1)*event.Minute,
			map[event.FieldPath]event.Value{
				fAuthType:    event.NewValue("Kerberos"),
				fDstComputer: event.NewValue("C700"),
				fLogonType:   event.NewValue("Network"),
			}, int64(i)))
	}

	habitual := onlyEvaluated(t, scoreAndCommit(t, d,
		mkEvent("U1@DOM1", 100*event.Minute, map[event.FieldPath]event.Value{
			fAuthType:    event.NewValue("Kerberos"),
			fDstComputer: event.NewValue("C700"),
			fLogonType:   event.NewValue("Network"),
		}, 100)))

	// Every individual value is one this entity has used. Only the COMBINATION is new,
	// which is the signal nothing else in the framework expresses.
	novel := onlyEvaluated(t, scoreAndCommit(t, d,
		mkEvent("U1@DOM1", 101*event.Minute, map[event.FieldPath]event.Value{
			fAuthType:    event.NewValue("NTLM"),
			fDstComputer: event.NewValue("C700"),
			fLogonType:   event.NewValue("Network"),
		}, 101)))

	habitualP, _ := habitual.PValue()
	novelP, _ := novel.PValue()
	if !(novelP < habitualP) {
		t.Errorf("novel pairing scored %.6g, habitual %.6g: a combination this entity has "+
			"never exhibited must score below one it exhibits constantly", novelP, habitualP)
	}
}

func TestAPairingIsJudgedAgainstTheEntityNotThePopulation(t *testing.T) {
	// The whole point of the demotion. A pairing that is overwhelmingly common in the
	// population but new to this entity must still be surprising FOR THIS ENTITY; §7.6
	// says an entity departing from the population norm is not thereby anomalous, and the
	// converse holds too — conforming to it is not thereby unremarkable.
	d, _, _ := wired()

	for i := range 60 {
		scoreAndCommit(t, d, mkEvent(event.EntityID(fmt.Sprintf("crowd%d@DOM1", i%20)),
			event.Timestamp(i+1)*event.Minute, map[event.FieldPath]event.Value{
				fAuthType:    event.NewValue("Kerberos"),
				fDstComputer: event.NewValue("C700"),
				fLogonType:   event.NewValue("Network"),
			}, int64(i)))
	}
	// A quiet entity with a settled, different habit.
	for i := range 40 {
		scoreAndCommit(t, d, mkEvent("loner@DOM1", event.Timestamp(200+i)*event.Minute,
			map[event.FieldPath]event.Value{
				fAuthType:    event.NewValue("NTLM"),
				fDstComputer: event.NewValue("C999"),
				fLogonType:   event.NewValue("Batch"),
			}, int64(200+i)))
	}

	// The crowd's ordinary combination, produced by the loner for the first time.
	v := onlyEvaluated(t, scoreAndCommit(t, d,
		mkEvent("loner@DOM1", 400*event.Minute, map[event.FieldPath]event.Value{
			fAuthType:    event.NewValue("Kerberos"),
			fDstComputer: event.NewValue("C700"),
			fLogonType:   event.NewValue("Network"),
		}, 400)))

	p, _ := v.PValue()
	if p >= 0.5 {
		t.Errorf("a pairing new to this entity scored %.6g; the population's familiarity "+
			"with it must not make it unremarkable for an entity that has never produced it", p)
	}
}

func TestScoringPrecedesObserving(t *testing.T) {
	// §5.2: an event must not be part of the history it is judged against. Violating this
	// is silent — the numbers stay plausible — so it is asserted directly.
	//
	// The probe needs established history first. With no history at all the estimator's
	// cold-start convention scores everything at 1, by design: a first-ever observation
	// is never anomalous, so first and repeat would agree for a reason that has nothing
	// to do with ordering.
	d, _, _ := wired()
	for i := range 30 {
		scoreAndCommit(t, d, mkEvent("U2@DOM1", event.Timestamp(i+1)*event.Minute,
			map[event.FieldPath]event.Value{
				fAuthType:    event.NewValue([]string{"Kerberos", "Negotiate"}[i%2]),
				fDstComputer: event.NewValue(fmt.Sprintf("C%d", 700+i%4)),
				fLogonType:   event.NewValue("Network"),
			}, int64(i)))
	}

	newPairing := func(at event.Timestamp, offset int64) *event.Event {
		return mkEvent("U2@DOM1", at, map[event.FieldPath]event.Value{
			fAuthType:    event.NewValue("NTLM"),
			fDstComputer: event.NewValue("C900"),
			fLogonType:   event.NewValue("Network"),
		}, offset)
	}
	first := onlyEvaluated(t, scoreAndCommit(t, d, newPairing(60*event.Minute, 60)))
	second := onlyEvaluated(t, scoreAndCommit(t, d, newPairing(61*event.Minute, 61)))

	firstP, _ := first.PValue()
	secondP, _ := second.PValue()
	if !(secondP > firstP) {
		t.Errorf("first occurrence %.6g, repeat %.6g: a repeat must be LESS surprising, "+
			"which can only hold if the first was recorded after it was scored",
			firstP, secondP)
	}
}

func TestFewerThanTwoEligibleFieldsAbstains(t *testing.T) {
	// R3: with nothing to pair, the detector declines rather than asserting normality.
	d, _, _ := wired()

	verdicts, _, err := d.Score(context.Background(),
		mkEvent("U3@DOM1", event.Minute, map[event.FieldPath]event.Value{
			fAuthType: event.NewValue("Kerberos"),
		}, 1))
	if err != nil {
		t.Fatal(err)
	}
	if len(verdicts) != 1 {
		t.Fatalf("got %d verdicts, want 1", len(verdicts))
	}
	if got := verdicts[0].Status(); got != detector.StatusAbstainedUnusable {
		t.Errorf("status = %v, want abstained_unusable: the source does produce these "+
			"fields, this event simply carries too few to induce a pair", got)
	}
	if _, ok := verdicts[0].PValue(); ok {
		t.Error("an abstained verdict carries a p-value; R3 makes that unrepresentable")
	}
}

func TestThePairingIsAddressedInACanonicalOrder(t *testing.T) {
	// (a, b) and (b, a) must name one row, or an entity's history is split in two and
	// every pairing looks half as familiar as it is.
	a := cooccurrence.NodeID{Field: fAuthType, Value: "Kerberos"}
	b := cooccurrence.NodeID{Field: fDstComputer, Value: "C700"}

	if cooccurrence.PairField(a, b) != cooccurrence.PairField(b, a) {
		t.Error("pair field depends on argument order")
	}
	if cooccurrence.PairValue(a, b) != cooccurrence.PairValue(b, a) {
		t.Error("pair value depends on argument order")
	}
}

func TestEvidenceNamesBothFieldsAndValuesRatherThanTheEncoding(t *testing.T) {
	// R5: a verdict must be reconstructable by hand, and an analyst must not have to know
	// that pairings are stored as a synthetic composite field to read one.
	d, _, _ := wired()
	for i := range 20 {
		scoreAndCommit(t, d, mkEvent("U4@DOM1", event.Timestamp(i+1)*event.Minute,
			map[event.FieldPath]event.Value{
				fAuthType:    event.NewValue("Kerberos"),
				fDstComputer: event.NewValue("C700"),
				fLogonType:   event.NewValue("Network"),
			}, int64(i)))
	}
	v := onlyEvaluated(t, scoreAndCommit(t, d,
		mkEvent("U4@DOM1", 50*event.Minute, map[event.FieldPath]event.Value{
			fAuthType:    event.NewValue("NTLM"),
			fDstComputer: event.NewValue("C700"),
			fLogonType:   event.NewValue("Network"),
		}, 50)))

	labels := v.Evidence().Labels
	for _, key := range []string{"first_field", "first_value", "second_field", "second_value"} {
		if labels[key] == "" {
			t.Errorf("evidence lacks %q; the pairing cannot be read without it", key)
		}
	}
	for key, value := range labels {
		if value == cooccurrence.PairFieldSeparator || value == cooccurrence.PairValueSeparator {
			t.Errorf("evidence label %q leaks the synthetic encoding", key)
		}
	}

	stats := v.Evidence().Stats
	if stats["tests"] != 3 {
		t.Errorf("tests = %v, want 3: three eligible fields induce C(3,2) pairings, and "+
			"the Sidak correction is taken over that count", stats["tests"])
	}
	// The verdict must name the two fields it is about, for the alert card.
	if len(v.Target().Fields) != 2 {
		t.Errorf("target names %d fields, want 2", len(v.Target().Fields))
	}
}

func TestEveryPairingIsRecordedNotOnlyTheReportedOne(t *testing.T) {
	// The verdict reports the most surprising pairing; the entity's history is of all of
	// them, or the pairings that were not reported never become familiar.
	d, repo, _ := wired()
	scoreAndCommit(t, d, mkEvent("U5@DOM1", event.Minute,
		map[event.FieldPath]event.Value{
			fAuthType:    event.NewValue("Kerberos"),
			fDstComputer: event.NewValue("C700"),
			fLogonType:   event.NewValue("Network"),
		}, 1))

	pairs := 0
	for key, byValue := range repo.rows {
		if len(byValue) > 0 && key != "" {
			pairs++
		}
	}
	if pairs != 3 {
		t.Errorf("recorded %d pairing fields, want 3: all C(3,2) pairings the event "+
			"carried, not merely the one reported", pairs)
	}
}

func TestScoringIsDeterministic(t *testing.T) {
	// R4: identical event and state yield identical output.
	run := func() float64 {
		d, _, _ := wired()
		for i := range 30 {
			scoreAndCommit(t, d, mkEvent("U6@DOM1", event.Timestamp(i+1)*event.Minute,
				map[event.FieldPath]event.Value{
					fAuthType:    event.NewValue([]string{"Kerberos", "NTLM"}[i%2]),
					fDstComputer: event.NewValue(fmt.Sprintf("C%d", 700+i%3)),
					fLogonType:   event.NewValue("Network"),
				}, int64(i)))
		}
		v := onlyEvaluated(t, scoreAndCommit(t, d,
			mkEvent("U6@DOM1", 99*event.Minute, map[event.FieldPath]event.Value{
				fAuthType:    event.NewValue("NTLM"),
				fDstComputer: event.NewValue("C709"),
				fLogonType:   event.NewValue("Network"),
			}, 99)))
		p, _ := v.PValue()
		return p
	}
	if a, b := run(), run(); a != b {
		t.Errorf("two identical runs scored %.17g and %.17g", a, b)
	}
}

func TestOpenVocabularyIsACopyNotASetter(t *testing.T) {
	// A detector already handed to a registry must not change its own null halfway
	// through a run: the recorded result would then describe a composition that never
	// existed, and R4 would not hold across the boundary.
	d, _, _ := wired()
	open := d.WithOpenVocabulary()
	if d == open {
		t.Fatal("WithOpenVocabulary mutated the receiver instead of returning a copy")
	}
	if d.ID() != open.ID() {
		t.Error("the copy changed its identity")
	}
}
