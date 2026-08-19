// Package pairing is Detector III's per-entity replacement: is this combination of
// values novel for THIS entity, against its own history?
//
// # Why the population form was demoted
//
// §8's population co-occurrence null asks how often the population's degree structure
// predicts two values should have been paired. That fires on every stable personal
// preference: an account that has always used one authentication type and never another
// is, under the configuration model, astronomically improbable on every event it
// produces. §7.6 disavows exactly that — an entity habitually departing from the
// population norm is not thereby anomalous — so the population form contradicts the
// framework's own governing principle, not merely its calibration.
//
// Measured, it put 18.4% of scored events below 1e−12, contributed nothing to detection,
// and once the underflow was repaired showed ln P reaching −39,278. That is not a loose
// null but a meaningless one.
//
// # Why it was demoted rather than deleted
//
// The signal §8 targets is real and nothing else in the framework covers it: two values
// each individually familiar which have scarcely been seen *together*. On LANL, 29 of the
// 549 labelled events are novel pairings. Every marginal is satisfied on such an event,
// so a detector scoring fields independently cannot express the question at all.
//
// # Why this needs no new mathematics
//
// A pairing IS a value — of a field that happens to be composite. So Detector I's
// estimator scores it exactly as it scores any other value: the same decayed counts, the
// same reserved mass for the unseen, the same cold-start convention by which a
// first observation is never anomalous, and, where enabled, the same Good–Turing
// treatment of an open vocabulary. Pair vocabularies are open almost by construction,
// which makes that last point more than incidental.
//
// Nothing downstream learns that pairs exist: the synthetic field path is opaque to the
// registry, the store and the evidence card alike, which is what keeps this
// field-agnostic (R2).
package pairing

import (
	"context"
	"fmt"

	"github.com/JohnPierman/ethogram/domain/calibration"
	"github.com/JohnPierman/ethogram/domain/cooccurrence"
	"github.com/JohnPierman/ethogram/domain/detector"
	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/novelty"
	"github.com/JohnPierman/ethogram/domain/registry"
)

// DetectorID names this detector in result JSON, dashboard labels, and the per-detector
// calibration sets of §10.1.
const DetectorID = detector.ID("pairing")

// FieldRegistry is the view of the §5.1 registry this detector needs.
type FieldRegistry interface {
	FindBySource(source event.SourceID) []*registry.Entry
}

// Detector scores the novelty of a value pairing against the entity's own history.
type Detector struct {
	repository novelty.ValueCountRepository
	registry   FieldRegistry
	estimator  novelty.Estimator
	halfLife   novelty.HalfLife
}

// NewDetector wires the per-entity pairing detector over the same per-entity value store
// Detector I uses. It needs no store of its own, because a pairing is addressed as a
// value of a synthetic field.
func NewDetector(repo novelty.ValueCountRepository, reg FieldRegistry,
	alpha float64, halfLife novelty.HalfLife) *Detector {
	return &Detector{
		repository: repo,
		registry:   reg,
		estimator:  novelty.Estimator{Alpha: alpha},
		halfLife:   halfLife,
	}
}

// WithOpenVocabulary returns a copy whose estimator reserves unseen mass by Good–Turing
// rather than by equation (4)'s fixed α.
//
// A copy rather than a setter, for the reason Detector I gives: a detector already handed
// to a registry must not change its own null halfway through a run, which would break R4
// and make the recorded result describe a composition that never existed.
func (d *Detector) WithOpenVocabulary() *Detector {
	clone := *d
	clone.estimator.OpenVocabulary = true
	return &clone
}

// ID implements detector.Detector.
func (d *Detector) ID() detector.ID { return DetectorID }

// NullHypothesis implements detector.Detector, stating H₀ as §5.2 requires.
func (d *Detector) NullHypothesis() string {
	return "the observed pairing of two eligible field values is drawn from the entity's " +
		"own historical distribution over pairings it has exhibited, estimated from " +
		"decayed counts exactly as equations (4) and (5) estimate a single value"
}

// pairScore is one tested pairing.
type pairScore struct {
	i, j     int
	value    string
	estimate novelty.Estimate
}

// Score tests every pair of eligible values the event carries against the entity's own
// pairing history, and reports the most surprising one under a Šidák correction for the
// number of pairs tested.
//
// One verdict per event, not one per pair. §8.5 takes the minimising pair and corrects it
// by Šidák over the T tests, equation (16), and the same construction applies here:
// emitting C(F,2) verdicts would let a single event dominate the combination's degrees of
// freedom purely by carrying many fields.
//
// Enumeration is in the registry's sorted field order with pairs in (i < j) order, so
// every comparison and every tie-break runs over a fixed sequence (R4). History is read
// strictly pre-event; the pairings this event carries reach the store only through the
// returned Observation (§5.2).
func (d *Detector) Score(ctx context.Context, e *event.Event) (detector.Verdicts, detector.Observation, error) {
	nodes := d.eligibleNodes(e)
	if len(nodes) < 2 {
		return d.abstainNoPair(e, len(nodes))
	}

	var (
		best   pairScore
		found  bool
		tests  int
		update []pendingPair
	)
	for i := 0; i < len(nodes); i++ {
		for j := i + 1; j < len(nodes); j++ {
			field := cooccurrence.PairField(nodes[i], nodes[j])
			value := cooccurrence.PairValue(nodes[i], nodes[j])
			update = append(update, pendingPair{field: field, value: value})

			estimate, err := d.estimatePair(ctx, e, field, value)
			if err != nil {
				return nil, nil, err
			}
			tests++
			// Strictly less than, so ties keep the first pair in canonical order and the
			// reported pairing is reproducible (R4).
			if !found || estimate.TailMass < best.estimate.TailMass {
				best = pairScore{i: i, j: j, value: value, estimate: estimate}
				found = true
			}
		}
	}

	verdict, err := detector.NewEvaluated(DetectorID,
		detector.Target{
			Event:  e.ID(),
			Entity: e.Entity(),
			Fields: []event.FieldPath{nodes[best.i].Field, nodes[best.j].Field},
		},
		calibration.Sidak(best.estimate.TailMass, tests),
		d.evidence(nodes, best, tests))
	if err != nil {
		return nil, nil, fmt.Errorf("pairing: verdict: %w", err)
	}

	return detector.Verdicts{verdict}, &observation{
		repository: d.repository,
		source:     e.Source(),
		entity:     e.Entity(),
		at:         e.OccurredAt(),
		eventID:    e.ID(),
		pending:    update,
	}, nil
}

// estimatePair reads the entity's history for one synthetic pairing field and estimates
// the observed pairing's tail mass under equations (4) and (5).
func (d *Detector) estimatePair(ctx context.Context, e *event.Event,
	field event.FieldPath, value string) (novelty.Estimate, error) {
	rows, err := d.repository.FindAllByEntityField(ctx, e.Source(), e.Entity(), field, e.OccurredAt())
	if err != nil {
		return novelty.Estimate{}, fmt.Errorf("pairing: find counts for %q: %w", field, err)
	}
	history := make([]novelty.ValueCount, 0, len(rows))
	for _, r := range rows {
		history = append(history, novelty.ValueCount{Value: r.Value, Count: r.Count})
	}
	return d.estimator.Estimate(history, value), nil
}

// eligibleNodes collects the event's present, usable values for the source's eligible
// fields, in the registry's ascending field-path order: the fixed enumeration every pair
// loop inherits (R4). An identifier or excluded field contributes no node, and an
// unsettled kind is withheld rather than admitted by default (§5.1).
//
// This is deliberately the same eligibility rule the co-occurrence graph applies, and
// the same projection to a vocabulary item, so that the two detectors test the same set
// of pairings and their measurements are comparable. A continuous field pairs on its
// magnitude band; see [cooccurrence.Detector] for why the raw measurement cannot be a
// node.
func (d *Detector) eligibleNodes(e *event.Event) []cooccurrence.NodeID {
	entries := d.registry.FindBySource(e.Source())
	out := make([]cooccurrence.NodeID, 0, len(entries))
	for _, entry := range entries {
		if !entry.Kind.IsEligible() {
			continue
		}
		value, present := e.Get(entry.Path)
		if !present || !value.IsUsable() {
			continue
		}
		token, projected := entry.Kind.Token(value.Text())
		if !projected {
			continue
		}
		out = append(out, cooccurrence.NodeID{Field: entry.Path, Value: token})
	}
	return out
}

// abstainNoPair reports the fewer-than-two-node case. Unusable rather than structural:
// the source does produce these fields, this event simply carries too few to induce a
// pair (R3 — the detector declines rather than asserting normality).
func (d *Detector) abstainNoPair(e *event.Event, eligible int) (detector.Verdicts, detector.Observation, error) {
	v, err := detector.NewAbstained(DetectorID,
		detector.Target{Event: e.ID(), Entity: e.Entity()},
		detector.StatusAbstainedUnusable,
		"fewer than two eligible fields present; no pairing to test",
		detector.NewEvidence([]int{16}, map[string]float64{
			"F_e": float64(eligible),
		}, nil))
	if err != nil {
		return nil, nil, fmt.Errorf("pairing: abstain: %w", err)
	}
	return detector.Verdicts{v}, detector.NoObservation{Event: e.ID(), Detector: DetectorID}, nil
}

// evidence carries what a reader needs to recompute the verdict by hand (R5): the two
// fields and values that formed the reported pairing, the decayed counts behind the
// estimate, and the number of tests the Šidák correction was taken over.
//
// The pairing is reported as its two constituent fields and values rather than as the
// synthetic encoding, so an analyst never has to know that the encoding exists.
func (d *Detector) evidence(nodes []cooccurrence.NodeID, best pairScore, tests int) detector.Evidence {
	first, second := nodes[best.i], nodes[best.j]
	stats := map[string]float64{
		"n_v":          best.estimate.NObserved,
		"N":            best.estimate.Total,
		"K":            float64(best.estimate.Distinct),
		"alpha":        best.estimate.Alpha,
		"p_hat":        best.estimate.PHatObserved,
		"p_hat_nil":    best.estimate.PHatUnseen,
		"tests":        float64(tests),
		"F_e":          float64(len(nodes)),
		"half_life_us": float64(d.halfLife),
	}
	labels := map[string]string{
		"first_field":  string(first.Field),
		"first_value":  first.Value,
		"second_field": string(second.Field),
		"second_value": second.Value,
		"scope":        "per-entity: the pairing is scored against this entity's own history, not the population's",
	}
	return detector.NewEvidence([]int{4, 5, 16}, stats, labels)
}

// pendingPair is one pairing to record once scoring has finished.
type pendingPair struct {
	field event.FieldPath
	value string
}

// observation carries the state update implied by an event, computed while scoring
// against the pre-event state and applied strictly afterwards (§5.2).
//
// Every pairing the event carries is recorded, not only the one reported. The verdict
// reports the most surprising pairing; the entity's history is of all of them.
type observation struct {
	repository novelty.ValueCountRepository
	source     event.SourceID
	entity     event.EntityID
	at         event.Timestamp
	eventID    event.ID
	pending    []pendingPair
	committed  bool
}

// EventID implements detector.Observation.
func (o *observation) EventID() event.ID { return o.eventID }

// DetectorID implements detector.Observation.
func (o *observation) DetectorID() detector.ID { return DetectorID }

// Commit applies the update. Idempotent per observation: a second commit is a no-op, so a
// replayed delivery cannot double-count a decayed count.
func (o *observation) Commit(ctx context.Context) error {
	if o.committed {
		return nil
	}
	for _, p := range o.pending {
		if err := o.repository.SaveObservation(ctx, o.source, o.entity, p.field, p.value, o.at); err != nil {
			return fmt.Errorf("pairing: save observation for %q: %w", p.field, err)
		}
	}
	o.committed = true
	return nil
}
