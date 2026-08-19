package marginal

import (
	"context"
	"fmt"
	"math"
	"strconv"

	"github.com/JohnPierman/ethogram/domain/detector"
	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/registry"
)

// DetectorID names Detector IV in result JSON, dashboard labels, and the per-detector
// calibration sets of §10.1.
const DetectorID = detector.ID("marginal")

// FieldRegistry is the view of the §5.1 registry this detector needs: which fields a
// source carries with what kind. Unlike Detector I's view it carries no absence
// classifier, because this detector emits no verdict for an absent field at all (see
// [Detector.Score]). *registry.Registry satisfies it.
type FieldRegistry interface {
	// FindBySource returns the entries for a source in ascending field-path order.
	FindBySource(source event.SourceID) []*registry.Entry
}

// Detector is Detector IV: the population-scope marginal outlier (§9).
//
// H₀: the observed value is drawn from the population marginal for its field. Where
// Detector I asks whether this entity has done this (§6), this detector asks whether
// this value is rare in the population — the category a conventional isolation forest
// over a pooled feature cloud catches, kept precisely so that the framework's other
// detectors can be credited only with what they add beyond it (§9).
type Detector struct {
	repository Repository
	registry   FieldRegistry
	estimator  Estimator
	halfLife   HalfLife

	// maxCardinality is the largest distinct value count for which this detector will
	// answer. See MaxCardinality.
	maxCardinality int
}

// MaxCardinality is the ceiling on a field's distinct value count above which
// Detector IV abstains.
//
// It is the reciprocal of the same one-in-a-thousand share that sets the abstention
// floor, and the two are the same statement made from opposite ends. A field with K
// distinct values gives its average value a share of 1/K, so once K exceeds a thousand
// the average value is already rarer than the threshold the detector is being asked
// about, and the answer stops separating the rare from the ordinary: nearly every value
// is in the tail, so being in the tail says nothing. Below the floor the marginal is
// too thin to resolve the question; above the ceiling the question no longer
// discriminates. Both are limits of resolution, not tuned constants, and neither may be
// moved after seeing a result without moving the threshold they derive from and
// re-running.
//
// The ceiling is also what keeps the detector affordable. Equation (5)'s tail sums the
// whole distribution, so the work is linear in K; at population scope K belongs to the
// source rather than to one entity, and a host or account field runs to tens of
// thousands. Measured on LANL auth, admitting those fields cost a factor of four in
// throughput for a verdict that, by the argument above, carried no information.
//
// What the ceiling excludes is not lost. §6 already scores those fields per entity,
// which is where they carry signal — a host is interesting because THIS account has not
// used it, not because few accounts have. The §12.4 baselines are unaffected: they
// score every field, so the head-to-head comparison is not narrowed by this bound.
const MaxCardinality = 1000

// NewDetector wires Detector IV. alpha is the Dirichlet concentration α of (4);
// minObservations is the §9 abstention floor; halfLife is T½ of §6.2, applied to the
// categorical counts by the repository and carried into evidence so that a verdict
// can be recomputed by hand (R5).
func NewDetector(repo Repository, reg FieldRegistry, alpha, minObservations float64, halfLife HalfLife) *Detector {
	return &Detector{
		repository: repo,
		registry:   reg,
		estimator:  Estimator{Alpha: alpha, MinObservations: minObservations},
		halfLife:   halfLife,

		maxCardinality: MaxCardinality,
	}
}

// ID implements detector.Detector.
func (d *Detector) ID() detector.ID { return DetectorID }

// NullHypothesis implements detector.Detector, stating H₀ as §5.2 requires.
func (d *Detector) NullHypothesis() string {
	return "the observed value is drawn from the population marginal for its field (§9)"
}

// Score evaluates the event's fields against the population marginal per
// (source, field): categorical and boolean fields through equations (4) and (5),
// numeric fields through the two-sided tail of the quantile sketch.
//
// The iteration is over the registry's entries for the source, in their sorted order
// and never a map: no detector names a field (R2), and the float accumulations
// downstream require a fixed traversal (R4).
//
// # Absence yields no verdict
//
// A field absent from the event produces nothing here, not an abstention. Detector I
// already reports absence through the Beta posterior of §5.3; a second verdict per
// absent field would double-count the same observation in §10.2's J. One absence, one
// report.
//
// # Scope
//
// Identifier and excluded fields induce no test and no state (§5.1 and the §12.5
// identifier control). A field whose kind has not settled also induces none: Detector
// I already reports that condition as abstained_unusable, and this detector is
// confined to fields whose kind admits a marginal (§9). A field that is present but
// unusable, or a numeric field whose value does not parse as a finite number,
// abstains as unusable rather than being guessed at (R3).
//
// # State accumulates through abstention
//
// The observation commits every scored field's value, including those scored while
// the marginal was still below the §9 minimum: that accumulation is what ends the
// abstention, and an abstention that also blocked learning would be permanent.
func (d *Detector) Score(ctx context.Context, e *event.Event) (detector.Verdicts, detector.Observation, error) {
	var (
		out         detector.Verdicts
		categorical []categoricalValue
		numeric     []numericValue
	)

	for _, entry := range d.registry.FindBySource(e.Source()) {
		// A continuous field is both countable (through its band) and tailable
		// (against the sketch), so the sketch is asked for first: this detector keeps
		// no per-value rows, so it is free to use the finer instrument, and banding
		// here would discard resolution for nothing. §5.1 sets out why entity scope
		// cannot make the same choice.
		usesSketch := entry.Kind.UsesNumericMarginal()
		isCategorical := !usesSketch && entry.Kind.IsScoreable()
		if !isCategorical && !usesSketch {
			continue // identifier, excluded, or unsettled: no test here (see above)
		}

		value, present := e.Get(entry.Path)
		if !present {
			continue // absence is Detector I's to report (see above)
		}

		target := detector.Target{
			Event:  e.ID(),
			Entity: e.Entity(),
			Fields: []event.FieldPath{entry.Path},
		}
		equations := []int{4, 5}
		if !isCategorical {
			equations = nil
		}

		if !value.IsUsable() {
			v, err := detector.NewAbstained(DetectorID, target,
				detector.StatusAbstainedUnusable, "value is present but not interpretable",
				detector.NewEvidence(equations, nil, map[string]string{
					"field":    string(entry.Path),
					"observed": value.Text(),
					"scope":    "population",
				}))
			if err != nil {
				return nil, nil, fmt.Errorf("marginal: abstain on %q: %w", entry.Path, err)
			}
			out = append(out, v)
			continue
		}

		if isCategorical {
			verdict, err := d.evaluateCategorical(ctx, e, entry, target, value.Text())
			if err != nil {
				return nil, nil, err
			}
			out = append(out, verdict)
			categorical = append(categorical, categoricalValue{field: entry.Path, value: value.Text()})
			continue
		}

		x, parseErr := strconv.ParseFloat(value.Text(), 64)
		if parseErr != nil || math.IsInf(x, 0) || math.IsNaN(x) {
			v, err := detector.NewAbstained(DetectorID, target,
				detector.StatusAbstainedUnusable,
				"value does not parse as a finite number for a numeric field",
				detector.NewEvidence(equations, nil, map[string]string{
					"field":    string(entry.Path),
					"observed": value.Text(),
					"scope":    "population",
				}))
			if err != nil {
				return nil, nil, fmt.Errorf("marginal: abstain on %q: %w", entry.Path, err)
			}
			out = append(out, v)
			continue
		}

		verdict, err := d.evaluateNumeric(ctx, e, entry, target, value.Text(), x)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, verdict)
		numeric = append(numeric, numericValue{field: entry.Path, x: x})
	}

	obs := &observation{
		repository:  d.repository,
		source:      e.Source(),
		at:          e.OccurredAt(),
		eventID:     e.ID(),
		categorical: categorical,
		numeric:     numeric,
	}
	return out, obs, nil
}

// evaluateCategorical scores one present, usable categorical or boolean field against
// the population marginal: equations (4) and (5) on the pre-event state.
//
// The estimator is shared with §6 — the arithmetic is Detector I's, delegated — and
// only the SCOPE of the rows differs: these counts pool every entity the source has
// seen. The evidence says so, carrying the label scope=population, so that a verdict
// card cannot be mistaken for a per-entity one.
func (d *Detector) evaluateCategorical(ctx context.Context, e *event.Event, entry *registry.Entry, target detector.Target, observed string) (detector.Verdict, error) {
	// Ask the size before paying for the history: above the ceiling the detector
	// declines in constant time rather than summing a distribution whose answer it
	// would not use. See MaxCardinality for why the bound is where it is.
	k, err := d.repository.Cardinality(ctx, e.Source(), entry.Path)
	if err != nil {
		return detector.Verdict{}, fmt.Errorf("marginal: cardinality for %q: %w", entry.Path, err)
	}
	if k > d.maxCardinality {
		ev := detector.NewEvidence(nil,
			map[string]float64{"K": float64(k), "max_cardinality": float64(d.maxCardinality)},
			map[string]string{"scope": "population", "observed": observed})
		return detector.NewAbstained(DetectorID, target, detector.StatusAbstainedUnusable,
			fmt.Sprintf("the population marginal for %q holds %d distinct values, above the "+
				"ceiling of %d: with that many values the average share is already below the "+
				"rarity being tested, so the tail no longer separates the rare from the "+
				"ordinary (§9); §6 scores this field per entity, where it does carry signal",
				entry.Path, k, d.maxCardinality), ev)
	}

	history, err := d.repository.FindCategorical(ctx, e.Source(), entry.Path, e.OccurredAt())
	if err != nil {
		return detector.Verdict{}, fmt.Errorf("marginal: find categorical marginal for %q: %w", entry.Path, err)
	}

	est := d.estimator.EstimateCategorical(history, observed)

	// §9's evidence at population scope: the same sufficient statistics as §6.4, the
	// parameters α and T½ because recomputing (4) by hand requires them (R5), and the
	// abstention floor because the abstained/evaluated boundary must be checkable too.
	stats := map[string]float64{
		"n_v":              est.NObserved,
		"N":                est.Total,
		"K":                float64(est.Distinct),
		"alpha":            est.Alpha,
		"p_hat":            est.PHatObserved,
		"p_hat_nil":        est.PHatUnseen,
		"half_life_us":     float64(d.halfLife),
		"min_observations": d.estimator.MinObservations,
	}
	labels := map[string]string{
		"field":    string(entry.Path),
		"observed": observed,
		"scope":    "population",
	}

	if est.Abstained {
		verdict, abstainErr := detector.NewAbstained(DetectorID, target,
			detector.StatusAbstainedUnusable, est.AbstainReason,
			detector.NewEvidence([]int{4, 5}, stats, labels))
		if abstainErr != nil {
			return detector.Verdict{}, fmt.Errorf("marginal: abstain on %q: %w", entry.Path, abstainErr)
		}
		return verdict, nil
	}

	verdict, err := detector.NewEvaluated(DetectorID, target, est.TailMass,
		detector.NewEvidence([]int{4, 5}, stats, labels))
	if err != nil {
		return detector.Verdict{}, fmt.Errorf("marginal: verdict for %q: %w", entry.Path, err)
	}
	return verdict, nil
}

// evaluateNumeric scores one present, usable, parseable numeric field: the two-sided
// tail of the population quantile sketch on the pre-event state.
//
// No equation number is carried: the sketch is this implementation's, in the spirit
// of the t-digest cited at [44], so the labels name it instead and the stats suffice
// to recompute the tail from the reported CDF by hand (R5).
func (d *Detector) evaluateNumeric(ctx context.Context, e *event.Event, entry *registry.Entry, target detector.Target, observed string, x float64) (detector.Verdict, error) {
	sketch, _, err := d.repository.FindNumeric(ctx, e.Source(), entry.Path, e.OccurredAt())
	if err != nil {
		return detector.Verdict{}, fmt.Errorf("marginal: find numeric marginal for %q: %w", entry.Path, err)
	}

	est := d.estimator.EstimateNumeric(sketch, x)

	labels := map[string]string{
		"field":    string(entry.Path),
		"observed": observed,
		"scope":    "population",
		"sketch":   "deterministic bounded centroid sketch, two-sided tail (§9)",
	}

	if est.Abstained {
		stats := map[string]float64{
			"weight":           est.Total,
			"min_observations": d.estimator.MinObservations,
		}
		verdict, abstainErr := detector.NewAbstained(DetectorID, target,
			detector.StatusAbstainedUnusable, est.AbstainReason,
			detector.NewEvidence(nil, stats, labels))
		if abstainErr != nil {
			return detector.Verdict{}, fmt.Errorf("marginal: abstain on %q: %w", entry.Path, abstainErr)
		}
		return verdict, nil
	}

	// A non-abstained estimate implies the sketch exists: EstimateNumeric abstains on
	// a nil sketch, whatever the floor.
	stats := map[string]float64{
		"x":                x,
		"cdf":              sketch.CDF(x),
		"weight":           est.Total,
		"centroids":        float64(sketch.Centroids()),
		"min_observations": d.estimator.MinObservations,
	}
	verdict, err := detector.NewEvaluated(DetectorID, target, est.TailMass,
		detector.NewEvidence(nil, stats, labels))
	if err != nil {
		return detector.Verdict{}, fmt.Errorf("marginal: verdict for %q: %w", entry.Path, err)
	}
	return verdict, nil
}

// categoricalValue is one pending categorical state update.
type categoricalValue struct {
	field event.FieldPath
	value string
}

// numericValue is one pending numeric state update.
type numericValue struct {
	field event.FieldPath
	x     float64
}

// observation carries the state update implied by an event, computed while scoring
// against the pre-event state and applied strictly afterwards (§5.2). No entity
// appears here: the update is to the population marginal (§9).
type observation struct {
	repository  Repository
	source      event.SourceID
	at          event.Timestamp
	eventID     event.ID
	categorical []categoricalValue
	numeric     []numericValue
	committed   bool
}

// EventID implements detector.Observation.
func (o *observation) EventID() event.ID { return o.eventID }

// DetectorID implements detector.Observation.
func (o *observation) DetectorID() detector.ID { return DetectorID }

// Commit applies the update. Idempotent per observation: a second commit is a no-op,
// so a replayed delivery can inflate neither the population counts nor the sketch
// weight.
func (o *observation) Commit(ctx context.Context) error {
	if o.committed {
		return nil
	}
	for _, cv := range o.categorical {
		if err := o.repository.SaveCategorical(ctx, o.source, cv.field, cv.value, o.at); err != nil {
			return fmt.Errorf("marginal: save categorical observation for %q: %w", cv.field, err)
		}
	}
	for _, nv := range o.numeric {
		if err := o.repository.SaveNumeric(ctx, o.source, nv.field, nv.x, o.at); err != nil {
			return fmt.Errorf("marginal: save numeric observation for %q: %w", nv.field, err)
		}
	}
	o.committed = true
	return nil
}
