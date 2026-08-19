package novelty

import (
	"context"
	"fmt"

	"github.com/JohnPierman/ethogram/domain/detector"
	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/registry"
)

// DetectorID names Detector I in result JSON, dashboard labels, and the per-detector
// calibration sets of §10.1.
const DetectorID = detector.ID("novelty")

// FieldRegistry is the view of the §5.1 registry this detector needs: which fields a
// source carries with what kind, and how to classify an absence. *registry.Registry
// satisfies it.
type FieldRegistry interface {
	// FindBySource returns the entries for a source in ascending field-path order.
	FindBySource(source event.SourceID) []*registry.Entry

	// StatusForAbsent classifies why a field is absent, per §5.3.
	StatusForAbsent(source event.SourceID, f event.FieldPath) registry.AbsenceKind
}

// Detector is Detector I: per-entity categorical novelty (§6).
//
// H₀: e(f) is drawn from the entity's historical distribution over D_f, estimated from
// decayed counts (§6.1). The estimator is equation (4); the p-value is the discrete
// tail mass of equation (5).
type Detector struct {
	repository ValueCountRepository
	registry   FieldRegistry
	estimator  Estimator
	halfLife   HalfLife
}

// NewDetector wires Detector I. alpha is the Dirichlet concentration α of (4);
// halfLife is T½ of §6.2.
func NewDetector(repo ValueCountRepository, reg FieldRegistry, alpha float64, halfLife HalfLife) *Detector {
	return &Detector{
		repository: repo,
		registry:   reg,
		estimator:  Estimator{Alpha: alpha},
		halfLife:   halfLife,
	}
}

// WithOpenVocabulary returns a copy whose estimator reserves unseen mass by the
// Good–Turing rule of [UnseenMass] rather than by equation (4)'s fixed α.
//
// It is a copy rather than a setter so that a detector already handed to a registry
// cannot change its own null halfway through a run, which would break R4 and make the
// recorded result describe a composition that never existed.
func (d *Detector) WithOpenVocabulary() *Detector {
	clone := *d
	clone.estimator.OpenVocabulary = true
	return &clone
}

// ID implements detector.Detector.
func (d *Detector) ID() detector.ID { return DetectorID }

// NullHypothesis implements detector.Detector, stating H₀ as §5.2 requires.
func (d *Detector) NullHypothesis() string {
	return "e(f) is drawn from the entity's historical distribution over D_f, " +
		"estimated from decayed counts (§6.1, equations (4) and (5))"
}

// Score evaluates the event's categorical and boolean fields against the entity's own
// persisted history.
//
// The iteration is over the registry's entries for the source, not over the event's
// fields, for two reasons. First, no detector names a field (R2): what to score is the
// registry's decision. Second, a field the source ordinarily emits but which is absent
// here must yield an abstained_unexpected verdict (§5.3), and only the registry knows
// which fields those are.
//
// Scope: fields whose kind is categorical or boolean. Numeric fields belong to §9,
// identifier and excluded fields contribute no verdicts and no state (§5.1 and the
// §12.5 identifier control), and a field whose kind has not settled abstains as
// unusable rather than being guessed at. State accumulates only for fields in scope,
// so an identifier field accrues nothing even while its classification is pending
// unknown, at the acceptable cost that legitimate fields begin history only once
// their kind settles.
func (d *Detector) Score(ctx context.Context, e *event.Event) (detector.Verdicts, detector.Observation, error) {
	var (
		out     detector.Verdicts
		pending []fieldValue
	)

	for _, entry := range d.registry.FindBySource(e.Source()) {
		target := detector.Target{
			Event:  e.ID(),
			Entity: e.Entity(),
			Fields: []event.FieldPath{entry.Path},
		}

		if !entry.Kind.IsScoreable() {
			// Unknown kind, and the field is present: the input exists but is not yet
			// scoreable, which is abstained_unusable, not silence (R3). Identifier and
			// excluded fields induce no test for this detector at all.
			if entry.Kind == registry.KindUnknown {
				if _, present := e.Get(entry.Path); present {
					v, err := detector.NewAbstained(DetectorID, target,
						detector.StatusAbstainedUnusable,
						"field kind has not settled; scoring would guess at the type",
						detector.NewEvidence([]int{4}, map[string]float64{
							"observations": float64(entry.Stats.Observations),
						}, map[string]string{"field": string(entry.Path), "kind": entry.Kind.String()}))
					if err != nil {
						return nil, nil, fmt.Errorf("novelty: abstain on %q: %w", entry.Path, err)
					}
					out = append(out, v)
				}
			}
			continue
		}

		value, present := e.Get(entry.Path)
		if !present {
			status := detector.StatusAbstainedStructural
			reason := "source does not ordinarily produce this field"
			if d.registry.StatusForAbsent(e.Source(), entry.Path) == registry.AbsenceUnexpected {
				status = detector.StatusAbstainedUnexpected
				reason = "field is ordinarily present for this source but absent here"
			}
			v, err := detector.NewAbstained(DetectorID, target, status, reason,
				detector.NewEvidence([]int{4}, map[string]float64{
					"presence_mean": entry.Presence.Mean(),
				}, map[string]string{"field": string(entry.Path)}))
			if err != nil {
				return nil, nil, fmt.Errorf("novelty: abstain on %q: %w", entry.Path, err)
			}
			out = append(out, v)
			continue
		}

		if !value.IsUsable() {
			v, err := detector.NewAbstained(DetectorID, target,
				detector.StatusAbstainedUnusable, "value is present but not interpretable",
				detector.NewEvidence([]int{4}, nil,
					map[string]string{"field": string(entry.Path), "observed": value.Text()}))
			if err != nil {
				return nil, nil, fmt.Errorf("novelty: abstain on %q: %w", entry.Path, err)
			}
			out = append(out, v)
			continue
		}

		// The vocabulary item this field contributes, which is the value's own text for
		// every bounded kind and a magnitude band for a continuous one (§5.1). Counting
		// the raw text of a continuous field is the saturation the band exists to
		// prevent, so the projection is taken here, once, and everything below — the
		// estimate, the evidence, and the state update — uses the token.
		token, projected := entry.Kind.Token(value.Text())
		if !projected {
			v, err := detector.NewAbstained(DetectorID, target,
				detector.StatusAbstainedUnusable,
				"value does not parse as a finite number for a continuous field",
				detector.NewEvidence([]int{4}, nil,
					map[string]string{"field": string(entry.Path), "observed": value.Text()}))
			if err != nil {
				return nil, nil, fmt.Errorf("novelty: abstain on %q: %w", entry.Path, err)
			}
			out = append(out, v)
			continue
		}

		verdict, err := d.evaluate(ctx, e, entry, target, token, value.Text())
		if err != nil {
			return nil, nil, err
		}
		out = append(out, verdict)
		pending = append(pending, fieldValue{field: entry.Path, value: token})
	}

	obs := &observation{
		repository: d.repository,
		source:     e.Source(),
		entity:     e.Entity(),
		at:         e.OccurredAt(),
		eventID:    e.ID(),
		pending:    pending,
	}
	return out, obs, nil
}

// evaluate scores one present, usable, in-scope field: equations (4) and (5) against
// the pre-event state, with the §6.4 evidence.
//
// observed is the vocabulary item — the token [registry.FieldKind.Token] returned — and
// is what the history is keyed on. measured is the value's own text, which differs only
// for a continuous field and is carried into evidence so the banding can be checked by
// hand (R5).
func (d *Detector) evaluate(ctx context.Context, e *event.Event, entry *registry.Entry, target detector.Target, observed, measured string) (detector.Verdict, error) {
	rows, err := d.repository.FindAllByEntityField(ctx, e.Source(), e.Entity(), entry.Path, e.OccurredAt())
	if err != nil {
		return detector.Verdict{}, fmt.Errorf("novelty: find counts for %q: %w", entry.Path, err)
	}

	history := make([]ValueCount, 0, len(rows))
	var firstSeen, lastSeen event.Timestamp
	for _, r := range rows {
		history = append(history, ValueCount{Value: r.Value, Count: r.Count})
		if r.Value == observed {
			firstSeen, lastSeen = r.FirstSeen, r.LastSeen
		}
	}

	est := d.estimator.Estimate(history, observed)

	// §6.4: distinct values observed, decayed count of the observed value, total
	// decayed count, first- and last-seen timestamps. The parameters α and T½ are
	// included because recomputing (4) by hand requires them (R5).
	stats := map[string]float64{
		"n_v":           est.NObserved,
		"N":             est.Total,
		"K":             float64(est.Distinct),
		"alpha":         est.Alpha,
		"p_hat":         est.PHatObserved,
		"p_hat_nil":     est.PHatUnseen,
		"half_life_us":  float64(d.halfLife),
		"first_seen_us": float64(firstSeen),
		"last_seen_us":  float64(lastSeen),
	}
	labels := map[string]string{
		"field":    string(entry.Path),
		"observed": observed,
	}
	if measured != observed {
		// A continuous field is counted against its band, so "observed" is the band.
		// The measurement it was derived from is retained: R5 requires the verdict be
		// reconstructable by hand, and the band alone does not say what was seen.
		labels["measured"] = measured
	}

	// §13.3: under the cardinality bounds required for finite state the reserved
	// novelty mass of (4) is no longer exact; the condition is reported, not concealed.
	var caveats []string
	if entry.Stats.IsTruncated() {
		caveats = append(caveats,
			"value set pruned at the cardinality bound; reserved novelty mass of (4) is inexact (§13.3)")
	}

	verdict, err := detector.NewEvaluated(DetectorID, target, est.TailMass,
		detector.NewEvidence([]int{4, 5}, stats, labels, caveats...))
	if err != nil {
		return detector.Verdict{}, fmt.Errorf("novelty: verdict for %q: %w", entry.Path, err)
	}
	return verdict, nil
}

// fieldValue is one pending state update.
type fieldValue struct {
	field event.FieldPath
	value string
}

// observation carries the state update implied by an event, computed while scoring
// against the pre-event state and applied strictly afterwards (§5.2).
type observation struct {
	repository ValueCountRepository
	source     event.SourceID
	entity     event.EntityID
	at         event.Timestamp
	eventID    event.ID
	pending    []fieldValue
	committed  bool
}

// EventID implements detector.Observation.
func (o *observation) EventID() event.ID { return o.eventID }

// DetectorID implements detector.Observation.
func (o *observation) DetectorID() detector.ID { return DetectorID }

// Commit applies the update. Idempotent per observation: a second commit is a no-op,
// so a replayed delivery cannot double-count a decayed count.
func (o *observation) Commit(ctx context.Context) error {
	if o.committed {
		return nil
	}
	for _, fv := range o.pending {
		if err := o.repository.SaveObservation(ctx, o.source, o.entity, fv.field, fv.value, o.at); err != nil {
			return fmt.Errorf("novelty: save observation for %q: %w", fv.field, err)
		}
	}
	o.committed = true
	return nil
}
