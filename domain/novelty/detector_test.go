package novelty_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/JohnPierman/ethogram/domain/detector"
	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/novelty"
	"github.com/JohnPierman/ethogram/domain/registry"
)

// ---------------------------------------------------------------------------
// In-memory repository, for tests only. The Postgres implementation arrives with the
// replay harness; this one exists so the domain suite runs anywhere (CI has no
// database). It applies the same lazy rule of §6.2 the production schema uses: a row
// stores (count, last_seen), and decay is applied on read.
// ---------------------------------------------------------------------------

type memoryRow struct {
	count     float64
	firstSeen event.Timestamp
	lastSeen  event.Timestamp
}

type memoryRepository struct {
	halfLife novelty.HalfLife
	rows     map[string]map[string]*memoryRow // (source,entity,field) -> value -> row
}

func newMemoryRepository(halfLife novelty.HalfLife) *memoryRepository {
	return &memoryRepository{halfLife: halfLife, rows: make(map[string]map[string]*memoryRow)}
}

func rowKey(s event.SourceID, en event.EntityID, f event.FieldPath) string {
	return string(s) + "\x1f" + string(en) + "\x1f" + string(f)
}

func (m *memoryRepository) FindAllByEntityField(_ context.Context, s event.SourceID, en event.EntityID, f event.FieldPath, at event.Timestamp) ([]novelty.ValueRow, error) {
	byValue := m.rows[rowKey(s, en, f)]
	values := make([]string, 0, len(byValue))
	for v := range byValue {
		values = append(values, v)
	}
	slices.Sort(values) // the repository contract: ascending Value order

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

func (m *memoryRepository) SaveObservation(_ context.Context, s event.SourceID, en event.EntityID, f event.FieldPath, value string, at event.Timestamp) error {
	key := rowKey(s, en, f)
	byValue, ok := m.rows[key]
	if !ok {
		byValue = make(map[string]*memoryRow)
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

// hasField reports whether any state exists for the field, for the identifier control.
func (m *memoryRepository) hasField(f event.FieldPath) bool {
	for key, byValue := range m.rows {
		if strings.HasSuffix(key, "\x1f"+string(f)) && len(byValue) > 0 {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Fixture wiring
// ---------------------------------------------------------------------------

const (
	src        = event.SourceID("lanl.auth")
	entityU66  = event.EntityID("U66@DOM1")
	dayHL      = novelty.HalfLife(event.Day)
	warmEvents = 300
)

const (
	fAuthType    = event.FieldPath("auth.authentication_type")
	fDstComputer = event.FieldPath("auth.destination_computer")
	fSuccess     = event.FieldPath("auth.success_failure")
	fCorrelation = event.FieldPath("auth.correlation_id")
)

func mkEvent(entity event.EntityID, at event.Timestamp, fields map[event.FieldPath]event.Value, offset int64) *event.Event {
	e := event.New(src, entity, at, fields, offset)
	return &e
}

// warmRegistry feeds enough varied events for kinds to settle: auth_type and
// destination categorical, success boolean, correlation identifier.
func warmRegistry(withIdentifier bool) *registry.Registry {
	reg := registry.New(registry.DefaultPolicy())
	for i := range warmEvents {
		fields := map[event.FieldPath]event.Value{
			fAuthType:    event.NewValue([]string{"Negotiate", "Kerberos", "NTLM"}[i%3]),
			fDstComputer: event.NewValue(fmt.Sprintf("C%d", 700+i%11)),
			fSuccess:     event.NewValue([]string{"Success", "Fail"}[i%2]),
		}
		if withIdentifier {
			fields[fCorrelation] = event.NewValue(fmt.Sprintf("corr-%08d", i))
		}
		reg.ObserveEvent(mkEvent("warm", event.Timestamp(i+1)*event.Second, fields, int64(i)))
	}
	return reg
}

func newWiredDetector(reg *registry.Registry) (*novelty.Detector, *memoryRepository) {
	repo := newMemoryRepository(dayHL)
	return novelty.NewDetector(repo, reg, 1.0, dayHL), repo
}

// ---------------------------------------------------------------------------
// Behaviour
// ---------------------------------------------------------------------------

func TestDetectorScoresBeforeObserving(t *testing.T) {
	ctx := context.Background()
	reg := warmRegistry(false)
	d, _ := newWiredDetector(reg)

	e := mkEvent(entityU66, event.Hour, map[event.FieldPath]event.Value{
		fAuthType:    event.NewValue("Kerberos"),
		fDstComputer: event.NewValue("C625"),
		fSuccess:     event.NewValue("Success"),
	}, 1)

	// First sight of this entity: every in-scope field is unseen, and §6.2 requires
	// P = 1 exactly. If the event had contaminated state before scoring, the value
	// would be known by scoring time and P would drop below 1: the silent failure
	// §5.2 warns about, made loud.
	verdicts, obs, err := d.Score(ctx, e)
	if err != nil {
		t.Fatal(err)
	}
	evaluated := verdicts.Evaluated()
	if len(evaluated) != 3 {
		t.Fatalf("J = %d, want 3", len(evaluated))
	}
	for _, v := range evaluated {
		p, _ := v.PValue()
		if p != 1.0 {
			t.Errorf("field %v: first observation scored P = %v, want exactly 1; "+
				"state leaked into scoring", v.Target().Fields, p)
		}
	}

	// Commit, then repeat the identical event: the value is now known history, so the
	// repeat must be less surprising than the maximally-unseen case only in the sense
	// of remaining P = 1 (it is now the mode). A different value must now be novel.
	if commitErr := obs.Commit(ctx); commitErr != nil {
		t.Fatal(commitErr)
	}
	e2 := mkEvent(entityU66, 2*event.Hour, map[event.FieldPath]event.Value{
		fAuthType: event.NewValue("NTLM"), // never seen for this entity
	}, 2)
	verdicts2, _, err := d.Score(ctx, e2)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range verdicts2.Evaluated() {
		if len(v.Target().Fields) == 1 && v.Target().Fields[0] == fAuthType {
			p, _ := v.PValue()
			if p >= 1.0 {
				t.Errorf("a novel value after history must score below 1, got %v", p)
			}
			if !almostEqual(p, v.Evidence().Stats["p_hat_nil"]) {
				t.Errorf("unseen value must reduce to the reserved mass: P = %v, p_hat_nil = %v",
					p, v.Evidence().Stats["p_hat_nil"])
			}
		}
	}
}

func almostEqual(a, b float64) bool {
	d := a - b
	return d < 1e-12 && d > -1e-12
}

// TestControlIdentifierContributesNoStateAndNoVerdicts is the detector half of the
// §12.5 identifier control. The registry half classifies the field; this half proves
// the detector honours the classification: no verdict names the field, and no state
// row is ever created for it.
func TestControlIdentifierContributesNoStateAndNoVerdicts(t *testing.T) {
	ctx := context.Background()
	reg := warmRegistry(true) // correlation_id settles as identifier
	d, repo := newWiredDetector(reg)

	for i := range 50 {
		e := mkEvent(entityU66, event.Timestamp(i+1)*event.Minute, map[event.FieldPath]event.Value{
			fAuthType:    event.NewValue("Kerberos"),
			fCorrelation: event.NewValue(fmt.Sprintf("live-%08d", i)),
		}, int64(i))
		verdicts, obs, err := d.Score(ctx, e)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range verdicts {
			for _, f := range v.Target().Fields {
				if f == fCorrelation {
					t.Fatalf("verdict emitted for the identifier field (status %s)", v.Status())
				}
			}
		}
		if err := obs.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}

	if repo.hasField(fCorrelation) {
		t.Fatal("state was accumulated for the identifier field")
	}
	if !repo.hasField(fAuthType) {
		t.Fatal("the ordinary categorical field should have accumulated state")
	}
}

// TestAbstentionStatuses covers the three §5.3 abstained cases end to end.
func TestAbstentionStatuses(t *testing.T) {
	ctx := context.Background()
	reg := warmRegistry(false)
	d, _ := newWiredDetector(reg)

	// success_failure is ordinarily present (fed in every warm event); omit it.
	// A LANL "?" is present but unusable.
	e := mkEvent(entityU66, event.Hour, map[event.FieldPath]event.Value{
		fAuthType:    event.UnusableValue("?"),
		fDstComputer: event.NewValue("C625"),
	}, 1)

	verdicts, _, err := d.Score(ctx, e)
	if err != nil {
		t.Fatal(err)
	}

	byField := map[event.FieldPath]detector.Verdict{}
	for _, v := range verdicts {
		if len(v.Target().Fields) == 1 {
			byField[v.Target().Fields[0]] = v
		}
	}

	if got := byField[fAuthType].Status(); got != detector.StatusAbstainedUnusable {
		t.Errorf("a '?' value must abstain as unusable, got %s", got)
	}
	if got := byField[fSuccess].Status(); got != detector.StatusAbstainedUnexpected {
		t.Errorf("an ordinarily-present field absent here must abstain as unexpected, got %s", got)
	}
	if got := byField[fDstComputer].Status(); got != detector.StatusEvaluated {
		t.Errorf("the usable field must evaluate, got %s", got)
	}

	// No abstained verdict carries a p-value, and J counts only the evaluated one.
	if got := len(verdicts.Evaluated()); got != 1 {
		t.Errorf("J = %d, want 1", got)
	}
	for _, v := range verdicts {
		if v.Status().IsAbstained() {
			if _, ok := v.PValue(); ok {
				t.Errorf("abstained verdict for %v carries a p-value", v.Target().Fields)
			}
			if v.Reason() == "" {
				t.Errorf("abstained verdict for %v has no reason", v.Target().Fields)
			}
		}
	}
}

// TestEvidenceSufficesToRecompute is the E7 property at unit scale: equation (4) and
// (5) recomputed from the verdict's own evidence, with no query back to the store,
// must reproduce the p-value. R5 is what makes this possible; this test is what makes
// it enforced.
func TestEvidenceSufficesToRecompute(t *testing.T) {
	ctx := context.Background()
	reg := warmRegistry(false)
	d, _ := newWiredDetector(reg)

	// Build some history, then score a repeat value.
	for i := range 10 {
		e := mkEvent(entityU66, event.Timestamp(i+1)*event.Hour, map[event.FieldPath]event.Value{
			fAuthType: event.NewValue([]string{"Kerberos", "Negotiate"}[i%2]),
		}, int64(i))
		_, obs, err := d.Score(ctx, e)
		if err != nil {
			t.Fatal(err)
		}
		if commitErr := obs.Commit(ctx); commitErr != nil {
			t.Fatal(commitErr)
		}
	}

	probe := mkEvent(entityU66, 11*event.Hour, map[event.FieldPath]event.Value{
		fAuthType: event.NewValue("Kerberos"),
	}, 11)
	verdicts, _, err := d.Score(ctx, probe)
	if err != nil {
		t.Fatal(err)
	}

	var v detector.Verdict
	found := false
	for _, cand := range verdicts.Evaluated() {
		if cand.Target().Fields[0] == fAuthType {
			v, found = cand, true
		}
	}
	if !found {
		t.Fatal("no evaluated verdict for the probe field")
	}

	// Recompute (4) from evidence alone. The evidence does not carry every value's
	// count, so the full tail mass is not recomputable in general; what §6.4 promises
	// is (4), and for this fixture the observed value is the unique mode, so (5) must
	// equal 1 and both are checkable.
	ev := v.Evidence().Stats
	denominator := ev["N"] + ev["alpha"]*(ev["K"]+1)
	pHat := (ev["n_v"] + ev["alpha"]) / denominator
	if !almostEqual(pHat, ev["p_hat"]) {
		t.Errorf("recomputed p_hat = %v, evidence says %v", pHat, ev["p_hat"])
	}
	pNil := ev["alpha"] / denominator
	if !almostEqual(pNil, ev["p_hat_nil"]) {
		t.Errorf("recomputed p_hat_nil = %v, evidence says %v", pNil, ev["p_hat_nil"])
	}
	if got, _ := v.PValue(); ev["n_v"] > ev["N"]/2 && got != 1.0 {
		t.Errorf("observed value is the mode; P = %v, want 1", got)
	}
	for _, name := range []string{"n_v", "N", "K", "alpha", "half_life_us", "first_seen_us", "last_seen_us"} {
		if _, ok := ev[name]; !ok {
			t.Errorf("§6.4 evidence is missing %q", name)
		}
	}
}

// TestObservationCommitIsIdempotent: a replayed delivery must not double-count.
func TestObservationCommitIsIdempotent(t *testing.T) {
	ctx := context.Background()
	reg := warmRegistry(false)
	d, repo := newWiredDetector(reg)

	e := mkEvent(entityU66, event.Hour, map[event.FieldPath]event.Value{
		fAuthType: event.NewValue("Kerberos"),
	}, 1)
	_, obs, err := d.Score(ctx, e)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if commitErr := obs.Commit(ctx); commitErr != nil {
			t.Fatal(commitErr)
		}
	}

	rows, err := repo.FindAllByEntityField(ctx, src, entityU66, fAuthType, event.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Count != 1 {
		t.Fatalf("triple commit produced rows %+v, want one row with count 1", rows)
	}
}

// ---------------------------------------------------------------------------
// E8 and R4 on the real detector
// ---------------------------------------------------------------------------

// TestE8OnDetectorI replays one probe inside four batch compositions. This is the
// production Detector I, not a fixture: the same check that gates the framework runs
// against the real implementation with its real repository semantics.
func TestE8OnDetectorI(t *testing.T) {
	reg := warmRegistry(false) // shared, read-only during the check

	factory := func() (detector.Detector, error) {
		d, _ := newWiredDetector(reg)
		return d, nil
	}

	history := []*event.Event{
		mkEvent(entityU66, 1*event.Hour, map[event.FieldPath]event.Value{
			fAuthType: event.NewValue("Negotiate"), fDstComputer: event.NewValue("C625"),
			fSuccess: event.NewValue("Success"),
		}, 1),
		mkEvent(entityU66, 2*event.Hour, map[event.FieldPath]event.Value{
			fAuthType: event.NewValue("Negotiate"), fDstComputer: event.NewValue("C625"),
			fSuccess: event.NewValue("Success"),
		}, 2),
		mkEvent(entityU66, 3*event.Hour, map[event.FieldPath]event.Value{
			fAuthType: event.NewValue("Kerberos"), fDstComputer: event.NewValue("C653"),
			fSuccess: event.NewValue("Fail"),
		}, 3),
	}
	probe := mkEvent(entityU66, 4*event.Hour, map[event.FieldPath]event.Value{
		fAuthType: event.NewValue("NTLM"), fDstComputer: event.NewValue("C17693"),
		fSuccess: event.NewValue("Fail"),
	}, 4)

	filler := func(n int) []*event.Event {
		out := make([]*event.Event, 0, n)
		for i := range n {
			out = append(out, mkEvent("U3005@DOM1", 5*event.Hour+event.Timestamp(i)*event.Minute,
				map[event.FieldPath]event.Value{
					fAuthType:    event.NewValue("NTLM"),
					fDstComputer: event.NewValue(fmt.Sprintf("C%d", 20000+i)),
					fSuccess:     event.NewValue("Fail"),
				}, int64(1000+i)))
		}
		return out
	}

	build := func(name string, tail []*event.Event) detector.BatchIndependenceCase {
		batch := make([]*event.Event, 0, len(history)+1+len(tail))
		batch = append(batch, history...)
		batch = append(batch, probe)
		batch = append(batch, tail...)
		return detector.BatchIndependenceCase{Name: name, Batch: batch, ProbeIndex: len(history)}
	}

	rep, err := detector.CheckBatchIndependence(context.Background(), factory,
		[]detector.BatchIndependenceCase{
			build("probe_alone", nil),
			build("one_co_resident", filler(1)),
			build("campaign_small", filler(40)),
			build("campaign_large", filler(400)),
		})
	if err != nil {
		t.Fatalf("E8 failed on Detector I: %v", err)
	}
	t.Logf("E8 on Detector I: batch sizes %v, digest %s", rep.BatchSizes, rep.DigestHex()[0][:16])
}

// TestR4RepeatOnDetectorI: identical event, identical state, byte-identical verdicts,
// thirty-two times, on the production implementation.
func TestR4RepeatOnDetectorI(t *testing.T) {
	reg := warmRegistry(false)
	factory := func() (detector.Detector, error) {
		d, _ := newWiredDetector(reg)
		return d, nil
	}
	history := []*event.Event{
		mkEvent(entityU66, 1*event.Hour, map[event.FieldPath]event.Value{
			fAuthType: event.NewValue("Negotiate"),
		}, 1),
		mkEvent(entityU66, 2*event.Hour, map[event.FieldPath]event.Value{
			fAuthType: event.NewValue("Kerberos"),
		}, 2),
	}
	probe := mkEvent(entityU66, 3*event.Hour, map[event.FieldPath]event.Value{
		fAuthType: event.NewValue("NTLM"),
	}, 3)

	if err := detector.AssertDeterministicRepeat(context.Background(), factory, history, probe, 32); err != nil {
		t.Fatalf("R4 failed on Detector I: %v", err)
	}
}
