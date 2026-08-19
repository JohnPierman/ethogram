// Package cellgrid implements the 168-cell timing representation of §7.1 — the
// construction the paper argues is defective — as an ablation detector for
// evaluation hypothesis E9.
//
// The week is partitioned into 168 hourly cells, each carrying an independent
// decayed count. Everything else is held identical to the framework: the estimator
// is the same smoothed Dirichlet predictive of equation (4) and the same discrete
// tail mass of equation (5) that Detector I uses, applied to cell indices as the
// categorical value set, under the same decay. The comparison therefore isolates
// exactly what E9 wants isolated: the representation of time, not the estimator, the
// smoothing, or the decay.
//
// The paper's four §7.1 defects are all present here by construction: adjacency
// carries no statistical weight, the circle is cut at the week boundary, per-entity
// data fragments 168 ways, and bin edges create artefacts. §12.5's wraparound control
// fails for this representation, and E9 measures the operational cost on real data,
// reported separately for midnight-straddling entities.
//
// This detector is scored as a shadow (never combined into the framework's statistic)
// or substituted for the circular detector in an ablation combination; it exists to
// be measured against, not to detect.
package cellgrid

import (
	"context"
	"fmt"

	"github.com/JohnPierman/ethogram/domain/detector"
	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/novelty"
)

// DetectorID names the ablation detector. The name states what it is.
const DetectorID = detector.ID("timing-cells-168")

// Cells is the number of weekly hourly cells.
const Cells = 168

// CellOf maps a timestamp to its weekly hourly cell index.
func CellOf(t event.Timestamp) int {
	hour := int64(t / event.Hour)
	cell := hour % Cells
	if cell < 0 {
		cell += Cells
	}
	return int(cell)
}

// Detector is the 168-cell ablation.
//
// H₀: the event's weekly hour cell is drawn from the entity's historical
// distribution over the 168 cells, estimated from decayed counts — the §7.1
// construction, scored with Detector I's estimator so only the representation
// differs from §7.2.
type Detector struct {
	repository novelty.ValueCountRepository
	estimator  novelty.Estimator
	halfLife   novelty.HalfLife
}

// cellField is the pseudo-field under which cell counts are stored. It is not a
// corpus field and never passes through the registry; the detector names it
// deliberately, because this detector is the ablation the registry-driven design is
// being compared against.
const cellField = event.FieldPath("__weekly_hour_cell__")

// NewDetector wires the ablation over its own value-count store. alpha and halfLife
// must match Detector I's, so the estimator is identical.
func NewDetector(repo novelty.ValueCountRepository, alpha float64, halfLife novelty.HalfLife) *Detector {
	return &Detector{
		repository: repo,
		estimator:  novelty.Estimator{Alpha: alpha},
		halfLife:   halfLife,
	}
}

// ID implements detector.Detector.
func (d *Detector) ID() detector.ID { return DetectorID }

// NullHypothesis implements detector.Detector.
func (d *Detector) NullHypothesis() string {
	return "the event's weekly hour cell is drawn from the entity's historical " +
		"distribution over 168 independent cells (§7.1, the representation E9 ablates)"
}

// Score evaluates the event's cell against the entity's cell history.
func (d *Detector) Score(ctx context.Context, e *event.Event) (detector.Verdicts, detector.Observation, error) {
	cell := CellOf(e.OccurredAt())
	value := cellValue(cell)

	rows, err := d.repository.FindAllByEntityField(ctx, e.Source(), e.Entity(), cellField, e.OccurredAt())
	if err != nil {
		return nil, nil, fmt.Errorf("cellgrid: find counts: %w", err)
	}
	history := make([]novelty.ValueCount, 0, len(rows))
	for _, r := range rows {
		history = append(history, novelty.ValueCount{Value: r.Value, Count: r.Count})
	}

	est := d.estimator.Estimate(history, value)

	stats := map[string]float64{
		"cell":         float64(cell),
		"n_cell":       est.NObserved,
		"N":            est.Total,
		"K_cells_seen": float64(est.Distinct),
		"alpha":        est.Alpha,
		"p_hat":        est.PHatObserved,
		"half_life_us": float64(d.halfLife),
	}
	labels := map[string]string{
		"cell_clock": fmt.Sprintf("day %d, %02d:00-%02d:00", cell/24, cell%24, (cell%24 + 1)),
	}

	verdict, err := detector.NewEvaluated(DetectorID,
		detector.Target{Event: e.ID(), Entity: e.Entity()},
		est.TailMass,
		detector.NewEvidence([]int{4, 5}, stats, labels,
			"ablation representation (§7.1); measured by E9, not part of the framework's combination"))
	if err != nil {
		return nil, nil, fmt.Errorf("cellgrid: verdict: %w", err)
	}

	obs := &observation{
		repository: d.repository,
		source:     e.Source(),
		entity:     e.Entity(),
		at:         e.OccurredAt(),
		value:      value,
		eventID:    e.ID(),
	}
	return detector.Verdicts{verdict}, obs, nil
}

func cellValue(cell int) string { return fmt.Sprintf("%03d", cell) }

type observation struct {
	repository novelty.ValueCountRepository
	source     event.SourceID
	entity     event.EntityID
	at         event.Timestamp
	value      string
	eventID    event.ID
	committed  bool
}

func (o *observation) EventID() event.ID       { return o.eventID }
func (o *observation) DetectorID() detector.ID { return DetectorID }

// Commit folds the event's cell into the entity's cell counts. Idempotent.
func (o *observation) Commit(ctx context.Context) error {
	if o.committed {
		return nil
	}
	if err := o.repository.SaveObservation(ctx, o.source, o.entity, cellField, o.value, o.at); err != nil {
		return fmt.Errorf("cellgrid: save observation: %w", err)
	}
	o.committed = true
	return nil
}
