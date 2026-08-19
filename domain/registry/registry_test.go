package registry_test

import (
	"fmt"
	"maps"
	"slices"
	"testing"

	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/registry"
)

const src = event.SourceID("lanl.auth")

// LANL auth field paths. The parser emits generic paths of this shape and lets the
// registry infer kinds; it does not hardcode the nine columns, which is what makes
// E6's zero-code-change claim testable.
const (
	fSrcUser     = event.FieldPath("auth.source_user")
	fDstUser     = event.FieldPath("auth.destination_user")
	fSrcComputer = event.FieldPath("auth.source_computer")
	fDstComputer = event.FieldPath("auth.destination_computer")
	fAuthType    = event.FieldPath("auth.authentication_type")
	fLogonType   = event.FieldPath("auth.logon_type")
	fOrientation = event.FieldPath("auth.authentication_orientation")
	fSuccess     = event.FieldPath("auth.success_failure")

	// A synthetic per-event-unique field, for the §12.5 identifier control.
	fCorrelation = event.FieldPath("auth.correlation_id")
)

// authEvent builds an event resembling a real LANL auth row.
func authEvent(i int, extra map[event.FieldPath]event.Value) *event.Event {
	fields := map[event.FieldPath]event.Value{
		fSrcUser:     event.NewValue("U66@DOM1"),
		fDstUser:     event.NewValue("U147@DOM1"),
		fSrcComputer: event.NewValue(fmt.Sprintf("C%d", 600+i%7)),
		fDstComputer: event.NewValue(fmt.Sprintf("C%d", 700+i%11)),
		fAuthType:    event.NewValue([]string{"Negotiate", "Kerberos", "NTLM"}[i%3]),
		fLogonType:   event.NewValue([]string{"Batch", "Service", "Network"}[i%3]),
		fOrientation: event.NewValue("LogOn"),
		fSuccess:     event.NewValue([]string{"Success", "Fail"}[i%2]),
	}
	maps.Copy(fields, extra)
	e := event.New(src, "U66@DOM1", event.Timestamp(i+1)*event.Second, fields, int64(i))
	return &e
}

func feed(r *registry.Registry, n int, extra func(i int) map[event.FieldPath]event.Value) {
	for i := range n {
		var ex map[event.FieldPath]event.Value
		if extra != nil {
			ex = extra(i)
		}
		r.ObserveEvent(authEvent(i, ex))
	}
}

// ---------------------------------------------------------------------------
// §12.5 negative control: the identifier control
// ---------------------------------------------------------------------------

// TestControlIdentifier is the identifier control of §12.5:
//
//	"An identifier control confirms a field taking distinct values per event is
//	 classified per §5.1 and contributes no state and no verdicts."
//
// §5.1 records why this guard is load-bearing. A field taking a distinct value on
// essentially every event is formally maximally novel on every observation. Untreated
// it saturates any novelty detector, induces unbounded state growth, and in §8 renders
// every one of its graph nodes a singleton, dissolving the block structure.
func TestControlIdentifier(t *testing.T) {
	r := registry.New(registry.DefaultPolicy())

	// A correlation id taking a distinct value on every single event.
	feed(r, 500, func(i int) map[event.FieldPath]event.Value {
		return map[event.FieldPath]event.Value{
			fCorrelation: event.NewValue(fmt.Sprintf("6f1c%08x-corr", i)),
		}
	})

	kind, known := r.KindOf(src, fCorrelation)
	if !known {
		t.Fatal("the registry must record the field, even to exclude it")
	}
	if kind != registry.KindIdentifier {
		t.Fatalf("a per-event-unique field must classify as identifier, got %s", kind)
	}

	// Contributes no state: it is not eligible for the co-occurrence graph, so it
	// creates no nodes and therefore no singleton blocks.
	if kind.IsEligible() {
		t.Fatal("an identifier must not be eligible for the §8.2 co-occurrence graph")
	}
	// Contributes no verdicts: Detector I must not score it.
	if kind.IsScoreable() {
		t.Fatal("an identifier must not be scoreable by Detector I (§6)")
	}

	// It must be absent from F_elig.
	for _, e := range r.FindEligibleBySource(src) {
		if e.Path == fCorrelation {
			t.Fatal("the identifier appeared in F_elig")
		}
	}

	// And the ordinary fields must be unaffected: the guard must not be so eager that
	// it swallows legitimate categorical fields.
	for _, f := range []event.FieldPath{fAuthType, fLogonType, fSrcComputer, fDstComputer} {
		k, _ := r.KindOf(src, f)
		if k == registry.KindIdentifier {
			t.Errorf("field %q was misclassified as an identifier", f)
		}
		if !k.IsEligible() {
			t.Errorf("field %q should be eligible, got %s", f, k)
		}
	}

	// The statistics supporting the verdict must be visible, per §5.1.
	entries := r.FindBySource(src)
	var corr *registry.Entry
	for _, e := range entries {
		if e.Path == fCorrelation {
			corr = e
		}
	}
	if corr == nil {
		t.Fatal("no entry for the correlation field")
	}
	if ratio := corr.Stats.DistinctRatio(); ratio < 0.99 {
		t.Fatalf("distinct ratio = %v, want ~1 for a per-event-unique field", ratio)
	}
	t.Logf("identifier control: %q ratio %.4f over %d observations -> %s",
		corr.Path, corr.Stats.DistinctRatio(), corr.Stats.Observations, corr.Kind)
}

// TestIdentifierGuardWaitsForEvidence: early in a field's life every value is new, so
// a small sample makes any field look like an identifier. The guard must not classify
// on that basis, or a categorical field observed briefly would be permanently excluded.
func TestIdentifierGuardWaitsForEvidence(t *testing.T) {
	r := registry.New(registry.DefaultPolicy())

	// Ten events, every destination distinct: ratio 1.0, but far too little evidence.
	feed(r, 10, func(i int) map[event.FieldPath]event.Value {
		return map[event.FieldPath]event.Value{
			fCorrelation: event.NewValue(fmt.Sprintf("id-%d", i)),
		}
	})

	kind, _ := r.KindOf(src, fCorrelation)
	if kind == registry.KindIdentifier {
		t.Fatal("the identifier verdict must not be reached on 10 observations")
	}
	if kind != registry.KindUnknown {
		t.Fatalf("below MinObservations the kind must be unknown, got %s", kind)
	}
	// Unknown is withheld from the graph: admitting a field before its kind settles
	// would let an identifier in by default.
	if kind.IsEligible() {
		t.Fatal("an unknown kind must not be eligible")
	}
}

// ---------------------------------------------------------------------------
// Inference
// ---------------------------------------------------------------------------

func TestInferenceOnLANLShapedFields(t *testing.T) {
	r := registry.New(registry.DefaultPolicy())
	feed(r, 500, nil)

	for _, tc := range []struct {
		field event.FieldPath
		want  registry.FieldKind
		why   string
	}{
		{fSuccess, registry.KindBoolean, "Success/Fail is the K = 2 case of equation (4)"},
		{fAuthType, registry.KindCategorical, "a bounded, recurring value set"},
		{fLogonType, registry.KindCategorical, "a bounded, recurring value set"},
		{fSrcComputer, registry.KindCategorical, "7 distinct values over 500 events"},
		{fOrientation, registry.KindCategorical, "a single recurring value"},
	} {
		got, known := r.KindOf(src, tc.field)
		if !known {
			t.Errorf("%q: not in the registry", tc.field)
			continue
		}
		if got != tc.want {
			t.Errorf("%q: got %s, want %s (%s)", tc.field, got, tc.want, tc.why)
		}
	}
}

// TestBooleanPrecedesNumeric pins a rule-ordering decision: "0" and "1" satisfy both
// the boolean and the numeric test, and the boolean reading is the informative one.
func TestBooleanPrecedesNumeric(t *testing.T) {
	stats := registry.NewFieldStats("flag")
	for i := range 200 {
		stats.Observe([]string{"0", "1"}[i%2], int64(i), 10_000)
	}
	if got := registry.Infer(*stats, registry.DefaultPolicy()); got != registry.KindBoolean {
		t.Fatalf("0/1 must infer as boolean, got %s", got)
	}
}

// TestNumericToleratesSentinels: a mostly numeric field with a few sentinel tokens is
// still numeric, which is why NumericFraction is below one. A duration taking a fresh
// value on nearly every event is continuous.
func TestNumericToleratesSentinels(t *testing.T) {
	stats := registry.NewFieldStats("flows.duration")
	for i := range 1000 {
		v := fmt.Sprintf("%d", i)
		if i%500 == 0 {
			v = "unknown"
		}
		stats.Observe(v, int64(i), 10_000)
	}
	if got := registry.Infer(*stats, registry.DefaultPolicy()); got != registry.KindContinuous {
		t.Fatalf("want continuous despite sentinels, got %s", got)
	}
}

func TestExclusionByConfiguration(t *testing.T) {
	policy := registry.DefaultPolicy()
	policy.ExcludedFields = []string{"auth.authentication_type"}

	stats := registry.NewFieldStats("auth.authentication_type")
	for i := range 500 {
		stats.Observe("Negotiate", int64(i), 10_000)
	}
	got := registry.Infer(*stats, policy)
	if got != registry.KindExcluded {
		t.Fatalf("configuration must win over statistics, got %s", got)
	}
	if got.IsEligible() || got.IsScoreable() {
		t.Fatal("an excluded field must contribute nothing")
	}
}

// ---------------------------------------------------------------------------
// §13.3 finite state
// ---------------------------------------------------------------------------

// TestValueSetTruncationIsReported covers §13.3: under the cardinality bounds required
// for finite state, the reserved novelty mass of equation (4) is no longer exact, and
// the condition must be reported in evidence rather than concealed.
func TestValueSetTruncationIsReported(t *testing.T) {
	policy := registry.DefaultPolicy()
	policy.MaxTrackedValues = 100

	stats := registry.NewFieldStats("auth.destination_computer")
	for i := range 500 {
		stats.Observe(fmt.Sprintf("C%d", i), int64(i), policy.MaxTrackedValues)
	}

	if !stats.IsTruncated() {
		t.Fatal("hitting the value-set bound must be recorded (§13.3)")
	}
	if got := stats.DistinctValues(); got != 100 {
		t.Fatalf("tracked values = %d, want the bound of 100", got)
	}
	// The ratio is computed from the bound, understating it. That direction is safe:
	// it can only fail to classify an identifier, never invent one.
	if ratio := stats.DistinctRatio(); ratio > 1.0 {
		t.Fatalf("ratio %v exceeds 1", ratio)
	}
}

// ---------------------------------------------------------------------------
// §5.3 presence posterior
// ---------------------------------------------------------------------------

// TestPresencePosteriorDetectsAFieldCeasing covers the §5.3 mechanism: a source
// silently ceasing to emit a field must be detected as abstained_unexpected rather
// than manifesting as quietly degraded scores.
func TestPresencePosteriorDetectsAFieldCeasing(t *testing.T) {
	r := registry.New(registry.DefaultPolicy())

	// The field is present throughout a first stretch.
	feed(r, 200, func(i int) map[event.FieldPath]event.Value {
		return map[event.FieldPath]event.Value{
			fCorrelation: event.NewValue([]string{"a", "b", "c"}[i%3]),
		}
	})
	if got := r.StatusForAbsent(src, fCorrelation); got != registry.AbsenceUnexpected {
		t.Fatalf("a field the source ordinarily emits must be unexpected when absent, got %v", got)
	}

	// A field never seen at all is structural, not unexpected.
	if got := r.StatusForAbsent(src, "auth.never_emitted"); got != registry.AbsenceStructural {
		t.Fatalf("an unseen field must be structural, got %v", got)
	}

	// Now the source stops emitting it. Presence decays toward absence, and the
	// classification flips, which is the signal §5.3 wants.
	feed(r, 600, nil)
	if got := r.StatusForAbsent(src, fCorrelation); got != registry.AbsenceStructural {
		t.Fatalf("after the field ceased, presence should have fallen below the threshold, got %v", got)
	}
}

func TestBetaPosterior(t *testing.T) {
	b := registry.NewBeta()
	if b.Mean() != 0.5 {
		t.Fatalf("a uniform prior has mean 0.5, got %v", b.Mean())
	}
	if b.Observations() != 0 {
		t.Fatalf("a prior carries no evidence, got %v", b.Observations())
	}
	b.Alpha += 9
	b.Beta += 1
	if got := b.Mean(); got < 0.8 || got > 0.85 {
		t.Fatalf("mean = %v, want ~0.833", got)
	}
	if !b.IsOrdinarilyPresent(0.5) {
		t.Fatal("9 of 10 present should read as ordinarily present")
	}
}

// ---------------------------------------------------------------------------
// R4 and R2
// ---------------------------------------------------------------------------

// TestRegistryIterationIsSorted covers trap 1 for the registry specifically. The
// registry drives which fields each detector examines, so a nondeterministic traversal
// would reorder the float accumulations of (5) and (18).
func TestRegistryIterationIsSorted(t *testing.T) {
	r := registry.New(registry.DefaultPolicy())
	feed(r, 300, nil)

	paths := make([]event.FieldPath, 0)
	for _, e := range r.FindBySource(src) {
		paths = append(paths, e.Path)
	}
	if !slices.IsSorted(paths) {
		t.Fatalf("FindBySource must return sorted entries, got %v", paths)
	}

	eligible := make([]event.FieldPath, 0)
	for _, e := range r.FindEligibleBySource(src) {
		eligible = append(eligible, e.Path)
	}
	if !slices.IsSorted(eligible) {
		t.Fatalf("FindEligibleBySource must return sorted entries, got %v", eligible)
	}

	if !r.HasSource(src) {
		t.Fatal("HasSource must report a known source")
	}
	if r.HasSource("never.seen") {
		t.Fatal("HasSource must not report an unknown source")
	}
	if got := r.Sources(); len(got) != 1 || got[0] != src {
		t.Fatalf("Sources() = %v", got)
	}
}

// TestInferenceIsDeterministic: §5.1 requires inference be deterministic. The same
// statistics must always give the same kind.
func TestInferenceIsDeterministic(t *testing.T) {
	build := func() *registry.Registry {
		r := registry.New(registry.DefaultPolicy())
		feed(r, 400, func(i int) map[event.FieldPath]event.Value {
			return map[event.FieldPath]event.Value{
				fCorrelation: event.NewValue(fmt.Sprintf("u-%d", i)),
			}
		})
		return r
	}

	first := build()
	want := make(map[event.FieldPath]registry.FieldKind)
	for _, e := range first.FindBySource(src) {
		want[e.Path] = e.Kind
	}

	for range 16 {
		got := build()
		for _, e := range got.FindBySource(src) {
			if want[e.Path] != e.Kind {
				t.Fatalf("field %q classified %s then %s", e.Path, want[e.Path], e.Kind)
			}
		}
	}
}

// TestUnusableValuesAreNotClassifiedFrom covers LANL's literal "?": a field that is
// mostly unusable must not be classified from its few interpretable values.
func TestUnusableValuesAreNotClassifiedFrom(t *testing.T) {
	stats := registry.NewFieldStats("auth.logon_type")
	for i := range 400 {
		if i%100 == 0 {
			stats.Observe("Batch", int64(i), 10_000)
		} else {
			stats.ObserveUnusable(int64(i))
		}
	}
	if stats.Observations != 4 {
		t.Fatalf("usable observations = %d, want 4", stats.Observations)
	}
	if stats.UnusableObservations != 396 {
		t.Fatalf("unusable observations = %d, want 396", stats.UnusableObservations)
	}
	// Four usable observations is below MinObservations, so nothing is asserted.
	if got := registry.Infer(*stats, registry.DefaultPolicy()); got != registry.KindUnknown {
		t.Fatalf("want unknown on 4 usable observations, got %s", got)
	}
}

func TestKindVocabulary(t *testing.T) {
	for kind, want := range map[registry.FieldKind]string{
		registry.KindCategorical: "categorical",
		registry.KindBoolean:     "boolean",
		registry.KindDiscrete:    "discrete",
		registry.KindContinuous:  "continuous",
		registry.KindIdentifier:  "identifier",
		registry.KindExcluded:    "excluded",
		registry.KindUnknown:     "unknown",
	} {
		if got := kind.String(); got != want {
			t.Errorf("Kind(%d).String() = %q, want %q", kind, got, want)
		}
	}
	if got, want := len(registry.Kinds()), 7; got != want {
		t.Fatalf("Kinds() returned %d, want %d", got, want)
	}
}

func TestFieldStatsValuesAreSorted(t *testing.T) {
	stats := registry.NewFieldStats("auth.authentication_type")
	for i, v := range []string{"NTLM", "Kerberos", "Negotiate", "Kerberos"} {
		stats.Observe(v, int64(i), 10_000)
	}
	got := stats.Values()
	if !slices.IsSorted(got) {
		t.Fatalf("Values() must be sorted, got %v", got)
	}
	if len(got) != 3 {
		t.Fatalf("distinct values = %d, want 3", len(got))
	}
	if stats.FirstSeen != 0 && stats.FirstSeen > stats.LastSeen {
		t.Fatalf("FirstSeen %d after LastSeen %d", stats.FirstSeen, stats.LastSeen)
	}
}

func TestEmptyStatsAreNeverIdentifier(t *testing.T) {
	stats := registry.NewFieldStats("auth.nothing")
	if got := stats.DistinctRatio(); got != 0 {
		t.Fatalf("ratio = %v for empty stats, want 0", got)
	}
	if got := registry.Infer(*stats, registry.DefaultPolicy()); got != registry.KindUnknown {
		t.Fatalf("empty stats must be unknown, got %s", got)
	}
	if stats.IsBooleanPair() {
		t.Fatal("empty stats must not report a boolean pair")
	}
}
