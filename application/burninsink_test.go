package application_test

import (
	"context"
	"errors"
	"testing"

	"github.com/JohnPierman/ethogram/application"
	"github.com/JohnPierman/ethogram/domain/event"
)

// The burn-in sink exists so a quantity can be fitted on data the scoring window has not
// seen, and the whole value of that depends on one property: the two sinks must partition
// the stream. If a single event reached both, a weight fitted on the first would have seen
// an event it is later used to score, and the rule built on it would be an oracle -- the
// same defect §8.2 rules out for the partition and §3.6 admits to for the cutoff analysis.
//
// The property is cheap to state and easy to break silently, which is exactly the kind
// that belongs in a test rather than in a comment.

// TestBurnInSinkAndSinkPartitionTheStream is the assertion issue #20 requires: the fitting
// window and the evaluation window share no event.
func TestBurnInSinkAndSinkPartitionTheStream(t *testing.T) {
	detectors, fieldRegistry := wireFramework(t)
	events, _, _ := corpusEvents()

	const boundary = 10 * event.Day
	warmed := map[event.ID]bool{}
	scored := map[event.ID]bool{}

	cmd := &application.ReplayCorpusCommand{
		Source:        &sliceSource{events: events},
		Detectors:     detectors,
		FieldRegistry: fieldRegistry,
		BurnInEnd:     boundary,
		IncludeEntity: func(e event.EntityID) bool { return e[0] == 'U' },
		BurnInSink: func(se application.ScoredEvent) error {
			warmed[se.Event.ID()] = true
			if at := se.Event.OccurredAt(); at >= boundary {
				t.Errorf("burn-in sink received an event at %v, at or past the boundary %v",
					at, boundary)
			}
			// No combination exists before the boundary: the covariance and the
			// conformal null are frozen there, so a combined score at burn-in would
			// have been computed from an estimate that did not yet exist.
			if se.Combined != nil {
				t.Error("burn-in event carries a combination; nothing has been frozen yet")
			}
			return nil
		},
		Sink: func(se application.ScoredEvent) error {
			scored[se.Event.ID()] = true
			if at := se.Event.OccurredAt(); at < boundary {
				t.Errorf("sink received an event at %v, before the boundary %v", at, boundary)
			}
			return nil
		},
	}

	report, err := cmd.Execute(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(warmed) == 0 || len(scored) == 0 {
		t.Fatalf("a partition of nothing proves nothing: warmed %d, scored %d",
			len(warmed), len(scored))
	}
	for id := range warmed {
		if scored[id] {
			t.Errorf("event %v reached both sinks; the windows are not disjoint", id)
		}
	}
	if int64(len(warmed)) != report.EventsWarmed {
		t.Errorf("burn-in sink saw %d events, report counted %d warmed",
			len(warmed), report.EventsWarmed)
	}
	if int64(len(scored)) != report.EventsScored {
		t.Errorf("sink saw %d events, report counted %d scored",
			len(scored), report.EventsScored)
	}
}

// TestBurnInSinkSeesEveryBurnInEventEvenWithoutEstimators pins that the sink does not
// quietly depend on a Correlations or Conformal estimator being configured.
//
// It nearly did. Both estimators live behind the same `!scored` branch, and the branch was
// guarded on either of them being set, so adding the sink to that block without widening
// the guard would have made the mirror silently empty on any run with calibration off --
// and an empty fitting sample fits every weight to 1, which reads as "no arm is
// informative" rather than as "nothing was measured".
func TestBurnInSinkSeesEveryBurnInEventEvenWithoutEstimators(t *testing.T) {
	detectors, fieldRegistry := wireFramework(t)
	events, _, _ := corpusEvents()

	seen := 0
	cmd := &application.ReplayCorpusCommand{
		Source:        &sliceSource{events: events},
		Detectors:     detectors,
		FieldRegistry: fieldRegistry,
		BurnInEnd:     10 * event.Day,
		IncludeEntity: func(e event.EntityID) bool { return e[0] == 'U' },
		BurnInSink:    func(application.ScoredEvent) error { seen++; return nil },
		Sink:          func(application.ScoredEvent) error { return nil },
		// Correlations and Conformal deliberately nil.
	}

	report, err := cmd.Execute(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if int64(seen) != report.EventsWarmed || seen == 0 {
		t.Fatalf("burn-in sink saw %d of %d warmed events with no estimator configured",
			seen, report.EventsWarmed)
	}
}

// TestBurnInSinkErrorStopsTheReplay keeps a failing sink from being swallowed. A mirror
// that fails halfway produces a short fitting sample, and a short sample is not a visible
// failure -- it is a weight quietly fitted on less evidence than the run reports.
func TestBurnInSinkErrorStopsTheReplay(t *testing.T) {
	detectors, fieldRegistry := wireFramework(t)
	events, _, _ := corpusEvents()

	sentinel := errors.New("mirror is full")
	cmd := &application.ReplayCorpusCommand{
		Source:        &sliceSource{events: events},
		Detectors:     detectors,
		FieldRegistry: fieldRegistry,
		BurnInEnd:     10 * event.Day,
		IncludeEntity: func(e event.EntityID) bool { return e[0] == 'U' },
		BurnInSink:    func(application.ScoredEvent) error { return sentinel },
		Sink:          func(application.ScoredEvent) error { return nil },
	}

	if _, err := cmd.Execute(context.Background()); !errors.Is(err, sentinel) {
		t.Fatalf("Execute error = %v, want it to wrap %v", err, sentinel)
	}
}
