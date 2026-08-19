package detector

import (
	"context"

	"github.com/JohnPierman/ethogram/domain/event"
)

// Detector is the abstraction of §5.2.
//
// §5.2 defines a detector as a pair of maps: Score(e, S) returning a finite set of
// verdicts, and Observe(e, S) returning updated state. Every detector must state the
// null hypothesis under which its p-value is computed, which is what licenses the
// combination in §10; see [Detector.NullHypothesis].
//
// # Enforcing Score before Observe
//
// §5.2 requires that Score precede Observe for any event, and warns that violating
// it is silent: if the event updates state before being scored, then a first-ever
// value is already a known value by the time it is scored, and novelty detection
// quietly dies while continuing to emit plausible numbers.
//
// This interface discharges the constraint through capability separation rather than
// through convention or a runtime check. Score receives no means of writing state:
// its state access is a read-only repository injected at construction, and the only
// value it can return that leads to a state change is an [Observation]. The write
// capability lives solely behind [Observation.Commit]. A detector therefore cannot
// update history while scoring, because it holds no writer with which to do so, and
// the framework cannot update history before scoring, because the Observation that
// carries the update does not exist until Score has produced it.
//
// This is stronger than validating an unforgeable token handed from Score to
// Observe. A token establishes that Score ran first, but still leaves Score holding
// a writer it might use; removing the writer removes the failure mode outright. The
// mathematical content of §5.2 is preserved exactly: the state update is a pure
// function of the event and of the pre-event state S⁻, computed while scoring
// against S⁻, and applied afterwards.
type Detector interface {
	// ID names the detector, for result JSON keys, dashboard labels, and the
	// per-(tenant, source) calibration sets of §10.1.
	ID() ID

	// NullHypothesis states H₀ in one sentence, as §5.2 requires of every detector.
	// It is rendered on the evidence card so that an analyst can see what was
	// actually tested.
	NullHypothesis() string

	// Score evaluates e against persisted history and returns the verdicts together
	// with the state update the event implies.
	//
	// Score must not consult any statistic of the batch in which e arrived (R1), nor
	// wall-clock time (R4). Decay is driven by e's timestamp and by each state row's
	// own last-observed timestamp, per §6.2.
	//
	// A detector lacking its inputs returns an abstained verdict, never a neutral
	// score (R3). Returning an empty verdict set is legitimate and distinct from
	// abstaining: it means the event induced no test for this detector.
	Score(ctx context.Context, e *event.Event) (Verdicts, Observation, error)
}

// Observation is the Observe(e, S) half of §5.2, materialised as a value.
//
// It carries the state update implied by an event, computed from the pre-event state
// while the event was being scored. Committing it is the only route by which a
// detector's state advances.
type Observation interface {
	// EventID returns the event the observation derives from, so that a caller
	// cannot commit an update belonging to a different event.
	EventID() event.ID

	// DetectorID returns the owning detector.
	DetectorID() ID

	// Commit applies the update. It is idempotent per event: committing the same
	// observation twice must not double-count, since a replayed stream would
	// otherwise inflate the decayed counts of §6.2.
	Commit(ctx context.Context) error
}

// NoObservation is returned by a detector whose state is unaffected by an event, for
// instance when every eligible field was excluded by the identifier guard of §5.1.
// Committing it is a no-op.
type NoObservation struct {
	Event    event.ID
	Detector ID
}

// EventID returns the originating event.
func (n NoObservation) EventID() event.ID { return n.Event }

// DetectorID returns the owning detector.
func (n NoObservation) DetectorID() ID { return n.Detector }

// Commit does nothing and cannot fail.
func (n NoObservation) Commit(context.Context) error { return nil }

// Registry holds the detectors that participate in a run, in a fixed order.
//
// Order is fixed at registration and never derived from a map, because §10.2 sums
// logarithms across detectors and floating-point addition is not associative: the
// order of registration is part of the combined statistic. E8 asserts that scores
// are bit-identical across runs, which requires this.
type Registry struct {
	detectors []Detector
	seen      map[ID]struct{}
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{seen: make(map[ID]struct{})}
}

// Register appends a detector. Registering the same ID twice is an error, since it
// would make the combination in §10.2 double-count one null.
func (r *Registry) Register(d Detector) error {
	if _, dup := r.seen[d.ID()]; dup {
		return &DuplicateDetectorError{ID: d.ID()}
	}
	r.seen[d.ID()] = struct{}{}
	r.detectors = append(r.detectors, d)
	return nil
}

// All returns the registered detectors in registration order.
func (r *Registry) All() []Detector {
	out := make([]Detector, len(r.detectors))
	copy(out, r.detectors)
	return out
}

// Len returns the number of registered detectors.
func (r *Registry) Len() int { return len(r.detectors) }

// DuplicateDetectorError reports a repeated detector ID.
type DuplicateDetectorError struct{ ID ID }

func (e *DuplicateDetectorError) Error() string {
	return "detector: duplicate detector id " + string(e.ID)
}
