package burst

import (
	"context"
	"fmt"
	"math"

	"github.com/JohnPierman/ethogram/domain/detector"
	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/novelty"
)

// DetectorID names this detector in result JSON, dashboard labels and the per-source
// calibration sets of §10.1.
const DetectorID = detector.ID("burst")

// StateRepository persists per-entity inter-arrival state.
type StateRepository interface {
	FindByEntity(ctx context.Context, source event.SourceID, entity event.EntityID) (*State, bool, error)
	SaveState(ctx context.Context, source event.SourceID, entity event.EntityID, s *State) error
}

// Detector is the sub-hourly inter-arrival detector. See the package comment for the gap it
// exists for, the null, and why the multiplicity correction is the conservative one.
type Detector struct {
	states   StateRepository
	halfLife novelty.HalfLife
}

// NewDetector wires the detector.
func NewDetector(states StateRepository, halfLife novelty.HalfLife) *Detector {
	return &Detector{states: states, halfLife: halfLife}
}

// ID implements detector.Detector.
func (d *Detector) ID() detector.ID { return DetectorID }

// NullHypothesis implements detector.Detector.
func (d *Detector) NullHypothesis() string {
	return "this entity's arrivals follow a homogeneous Poisson process at its own decayed " +
		"rate, so the span of k consecutive arrivals is Gamma(k-1, 1/lambda); the p-value is " +
		"the lower tail of the shortest such span over the windows examined, corrected by " +
		"Sidak for having taken a minimum over them"
}

// Score implements detector.Detector.
//
// It reads state and never writes it. The scan must be over a set of arrivals *including* the
// one being scored — see [Evaluate] — so Score evaluates a clone with that arrival folded in and
// leaves the stored state untouched; the identical fold is applied for real by the returned
// Observation. Cloning rather than copying is load-bearing, for the reason [State.Clone] gives.
func (d *Detector) Score(ctx context.Context, e *event.Event) (detector.Verdicts, detector.Observation, error) {
	target := detector.Target{Event: e.ID(), Entity: e.Entity()}

	stored, _, err := d.states.FindByEntity(ctx, e.Source(), e.Entity())
	if err != nil {
		return nil, nil, fmt.Errorf("burst: read state for %q: %w", e.Entity(), err)
	}

	view := stored.Clone()
	if view == nil {
		view = &State{}
	}
	view.Observe(e.OccurredAt(), d.halfLife)

	obs := &observation{
		detector: d, event: e.ID(), source: e.Source(), entity: e.Entity(),
		at: e.OccurredAt(),
	}

	scan, abstained := Evaluate(view)
	if abstained != AbstainNone {
		// R3: no basis is an outcome with a stated cause, never a neutral score. The
		// numbers are the gate's own terms so a reader can see how far off it is.
		v, abstainErr := detector.NewAbstained(DetectorID, target,
			detector.StatusAbstainedUnusable, abstained.String(),
			detector.NewEvidence(nil, map[string]float64{
				"observed_gaps": float64(view.Observed),
				"minimum_gaps":  MinGaps,
				"arrivals_held": float64(len(view.Recent)),
				"minimum_held":  MinWindow,
			}, nil))
		if abstainErr != nil {
			return nil, nil, fmt.Errorf("burst: abstain: %w", abstainErr)
		}
		return detector.Verdicts{v}, obs, nil
	}

	// R5: every number the p-value was computed from, so the arithmetic can be redone by hand
	// from the evidence card alone. mean_gap is the rate's reciprocal because an analyst reads
	// "one event every 20 minutes" and not "8.3e-4 events per second".
	evidence := detector.NewEvidence(nil, map[string]float64{
		"window_arrivals":     float64(scan.Window),
		"span_seconds":        scan.SpanSeconds,
		"rate_per_second":     scan.Rate,
		"mean_gap_seconds":    1 / scan.Rate,
		"expected_span":       float64(scan.Window-1) / scan.Rate,
		"windows_examined":    float64(scan.Windows),
		"uncorrected_log_p":   scan.LogMinP,
		"correction_log_lift": scan.LogP - scan.LogMinP,
	}, map[string]string{
		"combination": "Sidak over nested windows, which over-states the multiplicity for " +
			"positively dependent tests and is therefore conservative",
	})

	v, err := detector.NewEvaluatedLog(DetectorID, target, scan.LogP, evidence)
	if err != nil {
		return nil, nil, fmt.Errorf("burst: verdict: %w", err)
	}
	return detector.Verdicts{v}, obs, nil
}

// observation carries the state update implied by one event: a single arrival folded in at the
// §6.2 discount.
type observation struct {
	detector *Detector
	event    event.ID
	source   event.SourceID
	entity   event.EntityID
	at       event.Timestamp
}

func (o *observation) EventID() event.ID       { return o.event }
func (o *observation) DetectorID() detector.ID { return DetectorID }

// Commit folds the arrival into the stored state.
func (o *observation) Commit(ctx context.Context) error {
	d := o.detector
	state, _, err := d.states.FindByEntity(ctx, o.source, o.entity)
	if err != nil {
		return fmt.Errorf("burst: read state for %q: %w", o.entity, err)
	}
	if state == nil {
		state = &State{}
	} else {
		state = state.Clone()
	}
	state.Observe(o.at, d.halfLife)
	if err := d.states.SaveState(ctx, o.source, o.entity, state); err != nil {
		return fmt.Errorf("burst: save state for %q: %w", o.entity, err)
	}
	return nil
}

// Report summarises one run's inter-arrival state for table T5 and the result JSON.
type Report struct {
	// Entities is how many entities hold state, and TimestampsHeld the total number of
	// arrival timestamps across them: the §13.3 claim in a form a reader can check, since
	// the second divided by the first must not exceed MaxWindow.
	Entities        int64   `json:"entities"`
	TimestampsHeld  int64   `json:"timestamps_held"`
	MaxWindow       int     `json:"max_window"`
	Eligible        int64   `json:"eligible_entities"`
	MedianRateHertz float64 `json:"median_rate_hertz"`
}

// Eligibility reports whether an entity has cleared the abstention gate, which is what
// separates "this arm found nothing" from "this arm was never able to speak".
func (s *State) Eligible() bool {
	if s == nil {
		return false
	}
	if s.Observed < MinGaps || len(s.Recent) < MinWindow {
		return false
	}
	r := s.Rate()
	return r > 0 && !math.IsInf(r, 0) && !math.IsNaN(r)
}
