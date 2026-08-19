package marginal_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/JohnPierman/ethogram/domain/detector"
	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/marginal"
	"github.com/JohnPierman/ethogram/domain/novelty"
	"github.com/JohnPierman/ethogram/domain/registry"
	"github.com/JohnPierman/ethogram/infrastructure/state/memory"
)

// ---------------------------------------------------------------------------
// Fixture wiring, mirroring domain/novelty/detector_test.go. The registry is warmed
// with enough varied events for kinds to settle, and state lives in the same
// in-memory stores the replay engine uses, so these tests exercise the production
// repository semantics rather than a fixture's.
// ---------------------------------------------------------------------------

const (
	src        = event.SourceID("lanl.auth")
	entityU66  = event.EntityID("U66@DOM1")
	dayHL      = marginal.HalfLife(event.Day)
	warmEvents = 300

	fixtureAlpha  = 1.0
	fixtureMinObs = 50.0
)

const (
	fAuthType    = event.FieldPath("auth.authentication_type")
	fDstComputer = event.FieldPath("auth.destination_computer")
	fSuccess     = event.FieldPath("auth.success_failure")
	fBytes       = event.FieldPath("auth.bytes_transferred")
	fCorrelation = event.FieldPath("auth.correlation_id")
)

func mkEvent(entity event.EntityID, at event.Timestamp, fields map[event.FieldPath]event.Value, offset int64) *event.Event {
	e := event.New(src, entity, at, fields, offset)
	return &e
}

// warmRegistry feeds enough varied events for kinds to settle: auth_type and
// destination categorical, success boolean, bytes numeric, correlation identifier.
func warmRegistry(withIdentifier bool) *registry.Registry {
	reg := registry.New(registry.DefaultPolicy())
	for i := range warmEvents {
		fields := map[event.FieldPath]event.Value{
			fAuthType:    event.NewValue([]string{"Negotiate", "Kerberos", "NTLM"}[i%3]),
			fDstComputer: event.NewValue(fmt.Sprintf("C%d", 700+i%11)),
			fSuccess:     event.NewValue([]string{"Success", "Fail"}[i%2]),
			fBytes:       event.NewValue(fmt.Sprintf("%d", 100+i%97)),
		}
		if withIdentifier {
			fields[fCorrelation] = event.NewValue(fmt.Sprintf("corr-%08d", i))
		}
		reg.ObserveEvent(mkEvent("warm", event.Timestamp(i+1)*event.Second, fields, int64(i)))
	}
	return reg
}

func newWiredDetector(reg *registry.Registry) (*marginal.Detector, *memory.MarginalStore) {
	repo := memory.NewMarginalStore(dayHL)
	return marginal.NewDetector(repo, reg, fixtureAlpha, fixtureMinObs, dayHL), repo
}

// feed scores an event and commits its observation, the §5.2 order.
func feed(t *testing.T, ctx context.Context, d detector.Detector, e *event.Event) {
	t.Helper()
	_, obs, err := d.Score(ctx, e)
	if err != nil {
		t.Fatal(err)
	}
	if commitErr := obs.Commit(ctx); commitErr != nil {
		t.Fatal(commitErr)
	}
}

// verdictFor returns the single-field verdict for f, if any.
func verdictFor(vs detector.Verdicts, f event.FieldPath) (detector.Verdict, bool) {
	for _, v := range vs {
		fields := v.Target().Fields
		if len(fields) == 1 && fields[0] == f {
			return v, true
		}
	}
	return detector.Verdict{}, false
}

func mustPValue(t *testing.T, vs detector.Verdicts, f event.FieldPath) float64 {
	t.Helper()
	v, ok := verdictFor(vs, f)
	if !ok {
		t.Fatalf("no verdict for %s", f)
	}
	p, ok := v.PValue()
	if !ok {
		t.Fatalf("verdict for %s carries no p-value (status %s, reason %q)",
			f, v.Status(), v.Reason())
	}
	return p
}

// ---------------------------------------------------------------------------
// Behaviour
// ---------------------------------------------------------------------------

// TestPopulationScopeIsTheOppositeOfEntityScope is the executable statement of §9's
// role: Detector IV catches what is rare in the population however habitual for the
// entity, Detector I the reverse, and on a pair of events chosen to split the two
// questions their orderings must be opposite. That contrast is the whole point of
// carrying both — it is what lets the framework credit each detector only with what
// it adds beyond a conventional isolation forest over a pooled feature cloud.
func TestPopulationScopeIsTheOppositeOfEntityScope(t *testing.T) {
	ctx := context.Background()
	reg := warmRegistry(false)
	dIV, _ := newWiredDetector(reg)
	dI := novelty.NewDetector(memory.NewNoveltyStore(dayHL), reg, fixtureAlpha, dayHL)

	// The population: a hundred entities each authenticate with Negotiate three
	// times, and one entity — U66 — always uses NTLM. Both detectors watch the same
	// stream, each folding it into its own scope.
	offset := int64(0)
	for i := range 300 {
		e := mkEvent(event.EntityID(fmt.Sprintf("P%03d@DOM1", i%100)),
			event.Timestamp(i+1)*event.Second,
			map[event.FieldPath]event.Value{fAuthType: event.NewValue("Negotiate")}, offset)
		offset++
		feed(t, ctx, dIV, e)
		feed(t, ctx, dI, e)
	}
	for i := range 20 {
		e := mkEvent(entityU66, event.Timestamp(400+i)*event.Second,
			map[event.FieldPath]event.Value{fAuthType: event.NewValue("NTLM")}, offset)
		offset++
		feed(t, ctx, dIV, e)
		feed(t, ctx, dI, e)
	}

	// Probe A: U66 does what U66 always does — which almost nobody else does.
	// Probe B: U66 does what the population always does — for the first time.
	probeRare := mkEvent(entityU66, 500*event.Second,
		map[event.FieldPath]event.Value{fAuthType: event.NewValue("NTLM")}, offset)
	probeCommon := mkEvent(entityU66, 501*event.Second,
		map[event.FieldPath]event.Value{fAuthType: event.NewValue("Negotiate")}, offset+1)

	scoreOnly := func(d detector.Detector, e *event.Event) detector.Verdicts {
		vs, _, err := d.Score(ctx, e)
		if err != nil {
			t.Fatal(err)
		}
		return vs
	}

	pIVRare := mustPValue(t, scoreOnly(dIV, probeRare), fAuthType)
	pIVCommon := mustPValue(t, scoreOnly(dIV, probeCommon), fAuthType)
	pIRare := mustPValue(t, scoreOnly(dI, probeRare), fAuthType)
	pICommon := mustPValue(t, scoreOnly(dI, probeCommon), fAuthType)

	// Detector IV: rare in the population scores low; the population's mode scores
	// ~1, whoever is doing it and however new it is to them.
	if pIVRare >= 0.2 {
		t.Errorf("population-rare value: Detector IV P = %v, want low", pIVRare)
	}
	if pIVCommon < 0.99 {
		t.Errorf("population-common value: Detector IV P = %v, want ~1", pIVCommon)
	}

	// Detector I on the same two events: the opposite ordering. NTLM is everything
	// U66 has ever done; Negotiate is new to U66 however common elsewhere.
	if pIRare < 0.99 {
		t.Errorf("entity-habitual value: Detector I P = %v, want ~1", pIRare)
	}
	if pICommon >= 0.2 {
		t.Errorf("entity-novel value: Detector I P = %v, want low", pICommon)
	}

	if pIVRare >= pIVCommon || pIRare <= pICommon {
		t.Errorf("the detectors must order the two events oppositely: "+
			"IV %v vs %v, I %v vs %v", pIVRare, pIVCommon, pIRare, pICommon)
	}
}

// TestDetectorScoresBeforeObserving: state is empty after Score and populated only by
// Commit (§5.2), and a replayed delivery cannot double-count — a triple commit leaves
// a single count of one and a sketch weight of one.
func TestDetectorScoresBeforeObserving(t *testing.T) {
	ctx := context.Background()
	reg := warmRegistry(false)
	d, repo := newWiredDetector(reg)

	e := mkEvent(entityU66, event.Hour, map[event.FieldPath]event.Value{
		fAuthType: event.NewValue("Kerberos"),
		fBytes:    event.NewValue("150"),
	}, 1)

	_, obs, err := d.Score(ctx, e)
	if err != nil {
		t.Fatal(err)
	}

	rows, err := repo.FindCategorical(ctx, src, fAuthType, event.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("Score wrote categorical state: %+v", rows)
	}
	if _, ok, findErr := repo.FindNumeric(ctx, src, fBytes, event.Hour); findErr != nil || ok {
		t.Fatalf("Score wrote numeric state (ok %v, err %v)", ok, findErr)
	}

	for range 3 {
		if commitErr := obs.Commit(ctx); commitErr != nil {
			t.Fatal(commitErr)
		}
	}

	rows, err = repo.FindCategorical(ctx, src, fAuthType, event.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Count != 1 {
		t.Fatalf("triple commit produced rows %+v, want one row with count 1", rows)
	}
	sketch, ok, err := repo.FindNumeric(ctx, src, fBytes, event.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("commit produced no sketch for the numeric field")
	}
	if got := sketch.Weight(); got != 1 {
		t.Fatalf("triple commit produced sketch weight %v, want exactly 1", got)
	}
}

// TestControlIdentifierContributesNoStateAndNoVerdicts is the §12.5 identifier
// control at population scope: no verdict names the identifier field and no marginal
// row is ever created for it. The loop runs past the §9 floor, so it also confirms
// that state accumulated through the early abstentions is what ends them.
func TestControlIdentifierContributesNoStateAndNoVerdicts(t *testing.T) {
	ctx := context.Background()
	reg := warmRegistry(true) // correlation_id settles as identifier
	d, repo := newWiredDetector(reg)

	var last detector.Verdict
	for i := range 60 {
		e := mkEvent(entityU66, event.Timestamp(i+1)*event.Minute, map[event.FieldPath]event.Value{
			fAuthType:    event.NewValue("Kerberos"),
			fCorrelation: event.NewValue(fmt.Sprintf("live-%08d", i)),
		}, int64(i))
		verdicts, obs, err := d.Score(ctx, e)
		if err != nil {
			t.Fatal(err)
		}
		if v, found := verdictFor(verdicts, fCorrelation); found {
			t.Fatalf("verdict emitted for the identifier field (status %s)", v.Status())
		}
		if v, found := verdictFor(verdicts, fAuthType); found {
			last = v
		}
		if commitErr := obs.Commit(ctx); commitErr != nil {
			t.Fatal(commitErr)
		}
	}

	rows, err := repo.FindCategorical(ctx, src, fCorrelation, event.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatal("state was accumulated for the identifier field")
	}

	rows, err = repo.FindCategorical(ctx, src, fAuthType, event.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Fatal("the ordinary categorical field should have accumulated state")
	}
	if !last.Status().IsEvaluated() {
		t.Errorf("after 60 committed observations against a floor of 50 the verdict "+
			"must be evaluated, got %s (%q)", last.Status(), last.Reason())
	}
}

// TestAbsentFieldsProduceNoVerdict: absence is Detector I's to report, through the
// §5.3 Beta posterior; Detector IV stays silent for an absent field, or the same
// absence would be counted twice in §10.2's J. Contrast novelty's
// TestAbstentionStatuses, which asserts abstained_unexpected for this same shape of
// event.
func TestAbsentFieldsProduceNoVerdict(t *testing.T) {
	ctx := context.Background()
	reg := warmRegistry(false)
	d, _ := newWiredDetector(reg)

	// The warm registry knows four ordinarily-present fields; the event carries one.
	e := mkEvent(entityU66, event.Hour, map[event.FieldPath]event.Value{
		fAuthType: event.NewValue("Kerberos"),
	}, 1)
	verdicts, _, err := d.Score(ctx, e)
	if err != nil {
		t.Fatal(err)
	}

	for _, f := range []event.FieldPath{fDstComputer, fSuccess, fBytes} {
		if v, found := verdictFor(verdicts, f); found {
			t.Errorf("absent field %s produced a verdict (status %s)", f, v.Status())
		}
	}
	if len(verdicts) != 1 {
		t.Errorf("an event with one present field produced %d verdicts, want 1", len(verdicts))
	}
}

// TestColdStartAbstainsBelowMinimumObservations: the first event a source ever
// produces finds an empty population marginal, and §9 abstains rather than scoring
// against it. Detector I's cold start is P = 1 exactly (§6.2) because its null is the
// entity's own history; a population detector claiming a p-value from an empty
// population would be asserting normality on no evidence, which R3 forbids.
func TestColdStartAbstainsBelowMinimumObservations(t *testing.T) {
	ctx := context.Background()
	reg := warmRegistry(false)
	d, _ := newWiredDetector(reg)

	e := mkEvent(entityU66, event.Hour, map[event.FieldPath]event.Value{
		fAuthType: event.NewValue("Kerberos"),
		fBytes:    event.NewValue("150"),
	}, 1)
	verdicts, _, err := d.Score(ctx, e)
	if err != nil {
		t.Fatal(err)
	}

	for _, f := range []event.FieldPath{fAuthType, fBytes} {
		v, found := verdictFor(verdicts, f)
		if !found {
			t.Fatalf("no verdict for %s", f)
		}
		if v.Status() != detector.StatusAbstainedUnusable {
			t.Errorf("%s: cold start must abstain as unusable, got %s", f, v.Status())
		}
		if _, ok := v.PValue(); ok {
			t.Errorf("%s: a cold-start abstention carries a p-value", f)
		}
		if v.Reason() == "" {
			t.Errorf("%s: the abstention carries no reason", f)
		}
	}
}

// TestNumericFieldsScoreAgainstThePopulationSketch covers §9's nonparametric marginal
// end to end: a central value scores near one, an extreme value scores small but
// never zero (the floor feeds equation (18)'s logarithm), and a value that does not
// parse abstains rather than being guessed at (R3).
func TestNumericFieldsScoreAgainstThePopulationSketch(t *testing.T) {
	ctx := context.Background()
	reg := warmRegistry(false)
	d, repo := newWiredDetector(reg)

	// The population: byte counts uniform over 100..199, from a hundred entities.
	for i := range 300 {
		e := mkEvent(event.EntityID(fmt.Sprintf("P%03d@DOM1", i%100)),
			event.Timestamp(i+1)*event.Second,
			map[event.FieldPath]event.Value{fBytes: event.NewValue(fmt.Sprintf("%d", 100+i%100))},
			int64(i))
		feed(t, ctx, d, e)
	}

	sketch, ok, err := repo.FindNumeric(ctx, src, fBytes, 400*event.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("no sketch accumulated for the numeric field")
	}
	if got := sketch.Weight(); got != 300 {
		t.Fatalf("sketch weight = %v, want exactly 300", got)
	}

	score := func(text string) detector.Verdicts {
		e := mkEvent(entityU66, 400*event.Second,
			map[event.FieldPath]event.Value{fBytes: event.NewValue(text)}, 400)
		vs, _, scoreErr := d.Score(ctx, e)
		if scoreErr != nil {
			t.Fatal(scoreErr)
		}
		return vs
	}

	pCentral := mustPValue(t, score("150"), fBytes)
	pExtreme := mustPValue(t, score("5000"), fBytes)
	if pCentral < 0.5 {
		t.Errorf("a central value scored P = %v, want high", pCentral)
	}
	if pExtreme > 0.05 {
		t.Errorf("an extreme value scored P = %v, want small", pExtreme)
	}
	if pExtreme <= 0 {
		t.Errorf("the two-sided tail is floored and can never be zero, got %v", pExtreme)
	}

	verdicts := score("not-a-number")
	v, found := verdictFor(verdicts, fBytes)
	if !found {
		t.Fatal("a non-parsing numeric value produced no verdict")
	}
	if v.Status() != detector.StatusAbstainedUnusable {
		t.Errorf("a non-parsing numeric value must abstain as unusable, got %s", v.Status())
	}
}
