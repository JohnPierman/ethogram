package detector_test

import (
	"context"
	"errors"
	"testing"

	"github.com/JohnPierman/ethogram/domain/detector"
	"github.com/JohnPierman/ethogram/domain/event"
)

// stubDetector is a minimal Detector used to exercise the registry and the verdict
// accessors. It abstains structurally, which is the correct behaviour for a detector
// whose inputs a source does not produce.
type stubDetector struct {
	id detector.ID
}

func (s stubDetector) ID() detector.ID        { return s.id }
func (s stubDetector) NullHypothesis() string { return "stub: no null is tested" }

func (s stubDetector) Score(_ context.Context, e *event.Event) (detector.Verdicts, detector.Observation, error) {
	v, err := detector.NewAbstained(s.id,
		detector.Target{Event: e.ID(), Entity: e.Entity()},
		detector.StatusAbstainedStructural, "source does not produce these inputs",
		detector.NewEvidence([]int{3}, nil, map[string]string{"source": string(e.Source())}))
	if err != nil {
		return nil, nil, err
	}
	return detector.Verdicts{v}, detector.NoObservation{Event: e.ID(), Detector: s.id}, nil
}

// TestRegistryPreservesRegistrationOrder matters because §10.2 sums logarithms across
// detectors and floating-point addition is not associative: registration order is part
// of the combined statistic, so it must not come from a map.
func TestRegistryPreservesRegistrationOrder(t *testing.T) {
	r := detector.NewRegistry()
	ids := []detector.ID{"novelty", "timing", "volume", "cooccurrence", "marginal"}
	for _, id := range ids {
		if err := r.Register(stubDetector{id: id}); err != nil {
			t.Fatalf("Register(%q): %v", id, err)
		}
	}

	if got, want := r.Len(), len(ids); got != want {
		t.Fatalf("Len() = %d, want %d", got, want)
	}
	for i, d := range r.All() {
		if d.ID() != ids[i] {
			t.Fatalf("position %d: got %q, want %q", i, d.ID(), ids[i])
		}
	}

	// All() returns a copy: a caller reordering the result must not change the
	// registry's own order.
	got := r.All()
	got[0], got[1] = got[1], got[0]
	if r.All()[0].ID() != ids[0] {
		t.Fatal("All() must return a copy; the registry order was disturbed")
	}
}

// TestRegistryRejectsDuplicates: registering one detector twice would make §10.2
// count the same null twice, inflating J and the combined statistic.
func TestRegistryRejectsDuplicates(t *testing.T) {
	r := detector.NewRegistry()
	if err := r.Register(stubDetector{id: "novelty"}); err != nil {
		t.Fatal(err)
	}

	err := r.Register(stubDetector{id: "novelty"})
	if err == nil {
		t.Fatal("expected an error when registering a duplicate detector id")
	}
	var dup *detector.DuplicateDetectorError
	if !errors.As(err, &dup) {
		t.Fatalf("want DuplicateDetectorError, got %T: %v", err, err)
	}
	if dup.ID != "novelty" {
		t.Fatalf("error names %q, want novelty", dup.ID)
	}
	if r.Len() != 1 {
		t.Fatalf("a rejected registration must not be recorded; Len() = %d", r.Len())
	}
}

func TestNoObservationIsInert(t *testing.T) {
	e := event.New("s", "u", event.Second, nil, 0)
	obs := detector.NoObservation{Event: e.ID(), Detector: "novelty"}

	if obs.EventID() != e.ID() {
		t.Fatal("EventID must identify the originating event")
	}
	if obs.DetectorID() != "novelty" {
		t.Fatalf("DetectorID = %q", obs.DetectorID())
	}
	if err := obs.Commit(context.Background()); err != nil {
		t.Fatalf("Commit must not fail: %v", err)
	}
}

func TestVerdictAccessorsAndStatusDistribution(t *testing.T) {
	e := event.New("lanl.auth", "U66@DOM1", event.Second, nil, 0)
	tgt := detector.Target{Event: e.ID(), Entity: e.Entity(),
		Fields: []event.FieldPath{"auth.authentication_type"}}
	ev := detector.NewEvidence([]int{4, 5},
		map[string]float64{"N": 3, "K": 2}, map[string]string{"observed": "NTLM"},
		"value set pruned; reserved novelty mass is inexact (§13.3)")

	evaluated, err := detector.NewEvaluated("novelty", tgt, 0.25, ev)
	if err != nil {
		t.Fatal(err)
	}
	if evaluated.DetectorID() != "novelty" {
		t.Fatalf("DetectorID = %q", evaluated.DetectorID())
	}
	if evaluated.Target().Entity != "U66@DOM1" {
		t.Fatalf("Target().Entity = %q", evaluated.Target().Entity)
	}
	if evaluated.Reason() != "" {
		t.Fatalf("an evaluated verdict carries no abstention reason, got %q", evaluated.Reason())
	}
	if got := evaluated.Evidence().Stats["N"]; got != 3 {
		t.Fatalf("evidence N = %v, want 3", got)
	}
	if got := evaluated.Evidence().Caveats; len(got) != 1 {
		t.Fatalf("caveats = %v, want one recorded caveat", got)
	}
	if names := evaluated.Evidence().StatNames(); names[0] != "K" || names[1] != "N" {
		t.Fatalf("StatNames must be sorted, got %v", names)
	}
	if names := evaluated.Evidence().LabelNames(); len(names) != 1 || names[0] != "observed" {
		t.Fatalf("LabelNames = %v", names)
	}

	unexpected, err := detector.NewAbstained("timing", tgt,
		detector.StatusAbstainedUnexpected, "field ordinarily present for this source", ev)
	if err != nil {
		t.Fatal(err)
	}
	if unexpected.Reason() == "" {
		t.Fatal("an abstained verdict must record why")
	}

	unusable, err := detector.NewAbstained("volume", tgt,
		detector.StatusAbstainedUnusable, "below minimum observation count", ev)
	if err != nil {
		t.Fatal(err)
	}

	vs := detector.Verdicts{evaluated, unexpected, unusable}

	// J of §10.2 counts only evaluated verdicts.
	if got, want := len(vs.Evaluated()), 1; got != want {
		t.Fatalf("J = %d, want %d", got, want)
	}

	counts := vs.CountByStatus()
	if len(counts) != 4 {
		t.Fatalf("CountByStatus must report all four §5.3 statuses, got %d", len(counts))
	}
	for status, want := range map[detector.Status]int{
		detector.StatusEvaluated:           1,
		detector.StatusAbstainedUnexpected: 1,
		detector.StatusAbstainedUnusable:   1,
		detector.StatusAbstainedStructural: 0,
	} {
		if counts[status] != want {
			t.Errorf("count[%s] = %d, want %d", status, counts[status], want)
		}
	}
}

func TestStatusVocabulary(t *testing.T) {
	// The strings appear in result JSON and dashboard labels and must match §5.3.
	for status, want := range map[detector.Status]string{
		detector.StatusEvaluated:           "evaluated",
		detector.StatusAbstainedStructural: "abstained_structural",
		detector.StatusAbstainedUnexpected: "abstained_unexpected",
		detector.StatusAbstainedUnusable:   "abstained_unusable",
		detector.StatusUnknown:             "unknown",
	} {
		if got := status.String(); got != want {
			t.Errorf("Status(%d).String() = %q, want %q", status, got, want)
		}
	}

	// The zero value must never read as a legitimate verdict status.
	if detector.StatusUnknown.IsValid() {
		t.Fatal("the zero status must not be valid")
	}
	if detector.StatusUnknown.IsEvaluated() || detector.StatusUnknown.IsAbstained() {
		t.Fatal("the zero status is neither evaluated nor abstained")
	}

	if got, want := len(detector.Statuses()), 4; got != want {
		t.Fatalf("Statuses() returned %d values, want %d", got, want)
	}
	// Fixed order, so report column order is deterministic.
	if detector.Statuses()[0] != detector.StatusEvaluated {
		t.Fatal("Statuses() must list evaluated first")
	}
	for _, s := range detector.Statuses() {
		if !s.IsValid() {
			t.Errorf("Statuses() returned an invalid status %d", s)
		}
	}
}
