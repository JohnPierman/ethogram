package cooccurrence_test

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/JohnPierman/ethogram/domain/cooccurrence"
	"github.com/JohnPierman/ethogram/domain/detector"
	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/novelty"
	"github.com/JohnPierman/ethogram/domain/registry"
)

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
	fSrcComputer = event.FieldPath("auth.source_computer")
	fSuccess     = event.FieldPath("auth.success_failure")
	fCorrelation = event.FieldPath("auth.correlation_id")
)

func mkEvent(entity event.EntityID, at event.Timestamp, fields map[event.FieldPath]event.Value, offset int64) *event.Event {
	e := event.New(src, entity, at, fields, offset)
	return &e
}

// warmRegistry feeds enough varied events for kinds to settle: auth_type,
// destination and source computer categorical, success boolean, correlation
// identifier. Four eligible fields exist, so an event carrying all of them
// induces T = 6 pairs.
func warmRegistry(withIdentifier bool) *registry.Registry {
	reg := registry.New(registry.DefaultPolicy())
	for i := range warmEvents {
		fields := map[event.FieldPath]event.Value{
			fAuthType:    event.NewValue([]string{"Negotiate", "Kerberos", "NTLM"}[i%3]),
			fDstComputer: event.NewValue(fmt.Sprintf("C%d", 700+i%11)),
			fSrcComputer: event.NewValue(fmt.Sprintf("C%d", 100+i%7)),
			fSuccess:     event.NewValue([]string{"Success", "Fail"}[i%2]),
		}
		if withIdentifier {
			fields[fCorrelation] = event.NewValue(fmt.Sprintf("corr-%08d", i))
		}
		reg.ObserveEvent(mkEvent("warm", event.Timestamp(i+1)*event.Second, fields, int64(i)))
	}
	return reg
}

func newWiredDetector(reg *registry.Registry) (*cooccurrence.Detector, *cooccurrence.MemoryGraph) {
	graph := cooccurrence.NewMemoryGraph(dayHL)
	return cooccurrence.NewDetector(graph, reg, nil, dayHL), graph
}

// replayPairHistory commits 100 (Kerberos, C625) events then 100 (NTLM, C777)
// events, so all four values are individually frequent while only two pairings
// have ever been observed. Returns the last committed timestamp.
func replayPairHistory(ctx context.Context, t *testing.T, d *cooccurrence.Detector) event.Timestamp {
	t.Helper()
	var at event.Timestamp
	for i := range 200 {
		auth, dst := "Kerberos", "C625"
		if i >= 100 {
			auth, dst = "NTLM", "C777"
		}
		at = event.Timestamp(i+1) * event.Second
		e := mkEvent(entityU66, at, map[event.FieldPath]event.Value{
			fAuthType:    event.NewValue(auth),
			fDstComputer: event.NewValue(dst),
		}, int64(i))
		_, obs, err := d.Score(ctx, e)
		if err != nil {
			t.Fatal(err)
		}
		if commitErr := obs.Commit(ctx); commitErr != nil {
			t.Fatal(commitErr)
		}
	}
	return at
}

// scoreOne scores a two-field probe and returns its single verdict (§8.5) without
// committing.
func scoreOne(ctx context.Context, t *testing.T, d *cooccurrence.Detector, at event.Timestamp, auth, dst string, offset int64) detector.Verdict {
	t.Helper()
	e := mkEvent(entityU66, at, map[event.FieldPath]event.Value{
		fAuthType:    event.NewValue(auth),
		fDstComputer: event.NewValue(dst),
	}, offset)
	verdicts, _, err := d.Score(ctx, e)
	if err != nil {
		t.Fatal(err)
	}
	if len(verdicts) != 1 {
		t.Fatalf("§8.5: one verdict per event, got %d", len(verdicts))
	}
	return verdicts[0]
}

// ---------------------------------------------------------------------------
// Behaviour
// ---------------------------------------------------------------------------

// TestColdGraphScoresExactlyOne: an empty graph asserts nothing. m = 0 sends the
// fallback to λ = 0, so every pair prices at P = 1 and Sidak(1, T) = 1 exactly —
// an evaluated verdict, not an abstention, because the model has an answer:
// nothing is unusual yet.
func TestColdGraphScoresExactlyOne(t *testing.T) {
	ctx := context.Background()
	reg := warmRegistry(false)
	d, _ := newWiredDetector(reg)

	e := mkEvent(entityU66, event.Hour, map[event.FieldPath]event.Value{
		fAuthType:    event.NewValue("Kerberos"),
		fDstComputer: event.NewValue("C625"),
		fSuccess:     event.NewValue("Success"),
	}, 1)
	verdicts, _, err := d.Score(ctx, e)
	if err != nil {
		t.Fatal(err)
	}
	if len(verdicts) != 1 {
		t.Fatalf("§8.5: one verdict per event, got %d", len(verdicts))
	}
	p, ok := verdicts[0].PValue()
	if !ok {
		t.Fatal("a cold graph must still evaluate, not abstain")
	}
	if p != 1 {
		t.Errorf("cold graph scored P = %v, want exactly 1", p)
	}
}

// TestFrequentValuesInfrequentCoOccurrence is the §8.1 signal, Figure 1's dashed
// edge: Kerberos and C777 are each individually common, but have never co-occurred,
// and that absence is precisely what the lower tail prices. The habitual pairing
// must stay unremarkable.
func TestFrequentValuesInfrequentCoOccurrence(t *testing.T) {
	ctx := context.Background()
	reg := warmRegistry(false)
	d, _ := newWiredDetector(reg)
	last := replayPairHistory(ctx, t, d)

	habitual, ok := scoreOne(ctx, t, d, last+event.Minute, "Kerberos", "C625", 300).PValue()
	if !ok {
		t.Fatal("habitual probe did not evaluate")
	}
	probe, ok := scoreOne(ctx, t, d, last+event.Minute, "Kerberos", "C777", 301).PValue()
	if !ok {
		t.Fatal("anomalous probe did not evaluate")
	}

	if habitual <= 0.5 {
		t.Errorf("habitual pairing scored P = %v, want > 0.5", habitual)
	}
	if probe >= 0.05 {
		t.Errorf("never-co-occurring pairing of individually common values scored "+
			"P = %v, want < 0.05", probe)
	}
}

// TestProbePairNotInGraphAtScoringTime: the probe's own pair must not have reached
// the graph by scoring time (§5.2). The first (Kerberos, C777) event must show
// w = 0 in its evidence; anything else means state leaked into scoring.
func TestProbePairNotInGraphAtScoringTime(t *testing.T) {
	ctx := context.Background()
	reg := warmRegistry(false)
	d, _ := newWiredDetector(reg)
	last := replayPairHistory(ctx, t, d)

	v := scoreOne(ctx, t, d, last+event.Minute, "Kerberos", "C777", 300)
	w, ok := v.Evidence().Stats["w_min_pair"]
	if !ok {
		t.Fatal("evidence is missing w_min_pair")
	}
	if w != 0 {
		t.Errorf("w_min_pair = %v at scoring time, want 0; state leaked into scoring", w)
	}
}

// TestControlIdentifierContributesNoNodes is the detector half of the §12.5
// identifier control: an identifier-kind field contributes no node, so F_e
// excludes it, no verdict names it, and no graph row is ever created for it.
func TestControlIdentifierContributesNoNodes(t *testing.T) {
	ctx := context.Background()
	reg := warmRegistry(true) // correlation_id settles as identifier
	d, graph := newWiredDetector(reg)

	for i := range 50 {
		e := mkEvent(entityU66, event.Timestamp(i+1)*event.Minute, map[event.FieldPath]event.Value{
			fAuthType:    event.NewValue("Kerberos"),
			fDstComputer: event.NewValue("C625"),
			fCorrelation: event.NewValue(fmt.Sprintf("live-%08d", i)),
		}, int64(i))
		verdicts, obs, err := d.Score(ctx, e)
		if err != nil {
			t.Fatal(err)
		}
		if got := verdicts[0].Evidence().Stats["F_e"]; got != 2 {
			t.Fatalf("F_e = %v with an identifier present, want 2: the identifier "+
				"must contribute no node", got)
		}
		for _, f := range verdicts[0].Target().Fields {
			if f == fCorrelation {
				t.Fatal("verdict names the identifier field")
			}
		}
		if err := obs.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}

	if graph.HasNode(cooccurrence.NodeID{Field: fCorrelation, Value: "live-00000000"}) {
		t.Error("graph holds a node for the identifier field")
	}
	// Only two (field, value) pairs were ever eligible, so 50 commits with unique
	// correlation ids must leave exactly 2 node rows and 1 edge row.
	if got := graph.Nodes(); got != 2 {
		t.Errorf("graph has %d node rows, want 2; identifier values leaked in", got)
	}
	if got := graph.Edges(); got != 1 {
		t.Errorf("graph has %d edge rows, want 1", got)
	}
}

// TestOneVerdictPerEventWithManyEligibleFields: four eligible fields induce
// T = 6 pairwise tests (16), yet §8.5 emits ONE verdict, naming the minimising
// pair.
func TestOneVerdictPerEventWithManyEligibleFields(t *testing.T) {
	ctx := context.Background()
	reg := warmRegistry(false)
	d, _ := newWiredDetector(reg)

	e := mkEvent(entityU66, event.Hour, map[event.FieldPath]event.Value{
		fAuthType:    event.NewValue("Kerberos"),
		fDstComputer: event.NewValue("C625"),
		fSrcComputer: event.NewValue("C104"),
		fSuccess:     event.NewValue("Success"),
	}, 1)
	verdicts, _, err := d.Score(ctx, e)
	if err != nil {
		t.Fatal(err)
	}
	if len(verdicts) != 1 {
		t.Fatalf("§8.5: one verdict per event, got %d", len(verdicts))
	}
	stats := verdicts[0].Evidence().Stats
	if stats["T"] != 6 {
		t.Errorf("T = %v with four eligible fields, want 6", stats["T"])
	}
	if stats["F_e"] != 4 {
		t.Errorf("F_e = %v, want 4", stats["F_e"])
	}
	if got := len(verdicts[0].Target().Fields); got != 2 {
		t.Errorf("target names %d fields, want the minimising pair's 2", got)
	}
}

// TestAbstainsWithFewerThanTwoEligibleFields: one eligible field induces no pair,
// which is abstained_unusable, not structural — the source does produce these
// fields; this event simply carries too few of them.
func TestAbstainsWithFewerThanTwoEligibleFields(t *testing.T) {
	ctx := context.Background()
	reg := warmRegistry(false)
	d, graph := newWiredDetector(reg)

	e := mkEvent(entityU66, event.Hour, map[event.FieldPath]event.Value{
		fAuthType: event.NewValue("Kerberos"),
	}, 1)
	verdicts, obs, err := d.Score(ctx, e)
	if err != nil {
		t.Fatal(err)
	}
	if len(verdicts) != 1 {
		t.Fatalf("got %d verdicts, want 1", len(verdicts))
	}
	v := verdicts[0]
	if v.Status() != detector.StatusAbstainedUnusable {
		t.Errorf("status = %s, want abstained_unusable", v.Status())
	}
	if _, ok := v.PValue(); ok {
		t.Error("abstained verdict carries a p-value")
	}
	if got := v.Evidence().Stats["F_e"]; got != 1 {
		t.Errorf("F_e = %v, want 1", got)
	}
	if err := obs.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if graph.Nodes() != 0 || graph.Edges() != 0 {
		t.Error("a pairless event must contribute no graph state")
	}
}

// TestFallbackCaveatAndPartitionLabels: with no partition, the (15) fallback is
// used AND reported (§8.4); once SetPartition supplies one, the caveat disappears
// and the block identities and partition provenance appear in the evidence.
func TestFallbackCaveatAndPartitionLabels(t *testing.T) {
	ctx := context.Background()
	reg := warmRegistry(false)
	d, _ := newWiredDetector(reg)

	for i := range 10 {
		e := mkEvent(entityU66, event.Timestamp(i+1)*event.Second, map[event.FieldPath]event.Value{
			fAuthType:    event.NewValue("Kerberos"),
			fDstComputer: event.NewValue("C625"),
		}, int64(i))
		_, obs, err := d.Score(ctx, e)
		if err != nil {
			t.Fatal(err)
		}
		if commitErr := obs.Commit(ctx); commitErr != nil {
			t.Fatal(commitErr)
		}
	}

	before := scoreOne(ctx, t, d, 20*event.Second, "Kerberos", "C625", 100).Evidence()
	if len(before.Caveats) == 0 {
		t.Error("nil partition: the (15) fallback must be reported as a caveat (§8.4)")
	}
	if got := before.Labels["fallback"]; got != "configuration-model (15)" {
		t.Errorf("fallback label = %q, want %q", got, "configuration-model (15)")
	}
	if !slices.Contains(before.Equations, 15) || slices.Contains(before.Equations, 14) {
		t.Errorf("fallback evidence equations = %v, want 15 and not 14", before.Equations)
	}

	d.SetPartition(&cooccurrence.Partition{
		Seed:          7,
		GraphChecksum: "fixture",
		Resolution:    1,
		Blocks: map[cooccurrence.NodeID]cooccurrence.BlockID{
			{Field: fAuthType, Value: "Kerberos"}: "b1",
			{Field: fDstComputer, Value: "C625"}:  "b1",
		},
		DegreeSums: map[cooccurrence.BlockID]float64{"b1": 20},
		BlockWeights: map[cooccurrence.BlockPair]float64{
			cooccurrence.NewBlockPair("b1", "b1"): 20,
		},
		TotalDegree: 20,
	})

	after := scoreOne(ctx, t, d, 20*event.Second, "Kerberos", "C625", 101).Evidence()
	if len(after.Caveats) != 0 {
		t.Errorf("partition set: no caveat expected, got %v", after.Caveats)
	}
	if after.Labels["block_r"] != "b1" || after.Labels["block_s"] != "b1" {
		t.Errorf("block labels = %q, %q, want b1, b1",
			after.Labels["block_r"], after.Labels["block_s"])
	}
	if after.Labels["partition_seed"] != "7" || after.Labels["partition_checksum"] != "fixture" {
		t.Errorf("partition provenance labels missing or wrong: %v", after.Labels)
	}
	if _, ok := after.Labels["fallback"]; ok {
		t.Error("fallback label present although the partition was used")
	}
	for _, name := range []string{"D_r", "D_s", "m_rs"} {
		if _, ok := after.Stats[name]; !ok {
			t.Errorf("§8.4 evidence is missing %q", name)
		}
	}
	if !slices.Contains(after.Equations, 14) || slices.Contains(after.Equations, 15) {
		t.Errorf("partition evidence equations = %v, want 14 and not 15", after.Equations)
	}
}

// TestObservationCommitIsIdempotent: a replayed delivery must not double-count,
// and the canonical edge key makes (a, b) and (b, a) one edge.
func TestObservationCommitIsIdempotent(t *testing.T) {
	ctx := context.Background()
	reg := warmRegistry(false)
	d, graph := newWiredDetector(reg)

	e := mkEvent(entityU66, event.Hour, map[event.FieldPath]event.Value{
		fAuthType:    event.NewValue("Kerberos"),
		fDstComputer: event.NewValue("C625"),
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

	a := cooccurrence.NodeID{Field: fAuthType, Value: "Kerberos"}
	b := cooccurrence.NodeID{Field: fDstComputer, Value: "C625"}
	w, err := graph.FindEdgeWeight(ctx, src, a, b, event.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if w != 1 {
		t.Fatalf("triple commit left w = %v, want 1", w)
	}
	reversed, err := graph.FindEdgeWeight(ctx, src, b, a, event.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if reversed != w {
		t.Errorf("FindEdgeWeight(b, a) = %v, FindEdgeWeight(a, b) = %v; "+
			"the canonical key must make them one edge", reversed, w)
	}
}
