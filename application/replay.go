// Package application orchestrates the domain: it feeds events through the detectors
// in the Â§5.2 order, combines evaluated verdicts per Â§10.2, and hands scored events to
// a sink. It contains no statistics of its own; every number is produced by the domain
// packages it composes.
package application

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"

	"github.com/JohnPierman/ethogram/domain/calibration"
	"github.com/JohnPierman/ethogram/domain/derive"
	"github.com/JohnPierman/ethogram/domain/detector"
	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/registry"
)

// EventSource yields events in corpus order. Next returns io.EOF at end of stream; a
// malformed row is returned as an error the caller can inspect without stopping.
type EventSource interface {
	Next() (*event.Event, error)
}

// CombinedScore is the Â§10.2 combination for one event.
type CombinedScore struct {
	// X2 is Fisher's statistic, equation (18), over the evaluated verdicts in
	// canonical order.
	X2 float64
	// J is the number of evaluated verdicts; the reference distribution is Ï‡Â²(2J).
	J int
	// P is the combined p-value.
	P float64

	// LogP is ln P, and it is the quantity to rank and threshold on.
	//
	// P underflows. Past roughly XÂ² = 1450 at two degrees of freedom the tail is
	// smaller than the least positive float64, so every event from there downwards
	// reports exactly zero and becomes indistinguishable from every other. On a corpus
	// of tens of millions that is not a rare edge: measured on LANL days 7â€“13, all
	// 1,400 retained alerts had P exactly zero while labelled attack events sat at
	// 1eâˆ’274, representable and far less extreme, and could never enter the alert set
	// because the ordering among the zeros was decided by the tie-break rather than by
	// evidence.
	//
	// LogP is the same ordering without the floor, so it separates events that P ties.
	// P is retained because it is what Â§6â€“Â§9 define and what a verdict card must show;
	// LogP is what any comparison between events should use.
	LogP float64

	// C and F are Brown's scale and effective degrees of freedom, equation (19). With
	// no measured dependence they are exactly 1 and 2J, which is Fisher, and the
	// distance of C from 1 is the readable measure of how much dependence the
	// correction absorbed.
	C float64
	F float64

	// Corrected records whether equation (19) was applied at all, so a result cannot
	// be read as corrected when the run had no dependence estimate to apply.
	Corrected bool

	// CorrectionRejected is empty unless the burn-in covariance implied a variance for
	// X2 that no joint distribution can produce, in which case it carries the reason and
	// the combination degraded to plain Fisher.
	//
	// A run used to abort at the boundary when this happened, which threw away the whole
	// experiment over one unusable estimate. Section 10.2 already requires degradation to
	// Fisher where the covariance is unusable; a covariance that is present but invalid is
	// the same case, and the honest handling is the same degradation plus a record of it.
	// Silence would be worse than either: a reader would see Corrected false and take it
	// for a run that never estimated a covariance at all.
	CorrectionRejected string

	// Conformal records whether Â§10.1's conformal calibration replaced any detector's
	// model p-value before the combination.
	Conformal bool

	// MinPLogP is ln of the Å idÃ¡k-corrected smallest p-value, 1 âˆ’ (1 âˆ’ min_i P_i)^J,
	// and MinDetector names the detector that produced the minimum.
	//
	// It is recorded beside Fisher, not instead of it, because the two ask different
	// questions and the difference is measurable rather than arguable. Fisher (18) sums
	// log p-values and asks whether the evidence is jointly unusual, which is the right
	// question when every detector is informative. Measured on LANL days 7â€“8, four of the
	// five are not: their labelled events sit at the 18th to 36th percentile of their own
	// distributions, where any event sits, while novelty's sit at the 0.07th. Summing an
	// informative detector with four uninformative ones averages the information away â€”
	// novelty alone would surface 57 of 262 labelled events at 100 alerts a day, and the
	// five-detector Fisher composite surfaces none.
	//
	// The minimum asks the question the framework actually poses: did ANY of these
	// detectors find this entity out of character? Â§8.5 already combines this way inside
	// Detector III, taking the minimising pair and correcting it by equation (16); this
	// is the same construction one level up, over detectors.
	MinPLogP    float64
	MinDetector string

	// ModelLogP is the combined log tail computed from the detectors' own model
	// p-values, always, whether or not conformal calibration was applied.
	//
	// It exists because a conformal p-value cannot fall below 1/(n+1): every event more
	// extreme than the whole burn-in sample ties at that floor, and on a corpus of
	// millions that is thousands of events tied together. Thresholding is the conformal
	// value's job, since only it has a calibrated meaning; ordering within a tie is not,
	// and ModelLogP is what separates them. Ranking on the conformal value alone would
	// allocate the alert budget by arrival order â€” the same defect the P/LogP split
	// above was introduced to remove, reached by a different route.
	ModelLogP float64
}

// ScoredEvent is one post-burn-in event with its verdicts and combination.
type ScoredEvent struct {
	Event    *event.Event
	Verdicts detector.Verdicts

	// Combined is nil when J = 0: the system reports no opinion rather than a null
	// score (R3, Â§10.2).
	Combined *CombinedScore

	// ShadowVerdicts come from ablation detectors that score every event but are
	// excluded from the combination, for E4 and E9. Keyed order follows the shadow
	// registration order.
	ShadowVerdicts detector.Verdicts
}

// ReplayReport summarises a replay for the run's provenance block.
type ReplayReport struct {
	RowsRead      int64
	RowErrors     int64
	EventsSeen    int64 // events parsed, before the entity filter
	EventsSkipped int64 // excluded by the entity filter
	EventsWarmed  int64 // processed during burn-in (scored and observed, not sunk)
	EventsScored  int64 // post burn-in, sunk
	NoOpinion     int64 // post burn-in events with J = 0
}

// ReplayCorpusCommand streams a corpus through the framework.
//
// Ordering per event is fixed and is the Â§5.2 contract: every detector scores against
// pre-event state; the combination is computed; only then are observations committed
// and the field registry updated. Burn-in events are scored and observed but not
// combined or sunk, so state warms under exactly the same code path that scoring
// uses, and the first-ever value of an entity remains novel at the moment it is
// scored even during warm-up.
type ReplayCorpusCommand struct {
	Source        EventSource
	Detectors     *detector.Registry
	FieldRegistry *registry.Registry

	// Deriver, when set, infers structure inside field values and adds a coarser field
	// beside each one that has it â€” a /24 beside an address, a parent domain beside a
	// hostname, a major version beside a build string. Nil disables derivation entirely.
	//
	// A novel /24 is a different and usually stronger signal than a novel exact address,
	// and on an open vocabulary the exact form is mostly singletons, so every event looks
	// like a first. The derived field is an ordinary field to everything downstream.
	Deriver *derive.Inferrer

	// Shadows are ablation detectors (E4, E9): scored on every event, never combined.
	Shadows []detector.Detector

	// BurnInEnd is the frozen split: events before it warm state only.
	BurnInEnd event.Timestamp

	// IncludeEntity restricts the entity population; nil includes everything. The
	// restriction is part of the run's coverage statement.
	IncludeEntity func(event.EntityID) bool

	// Sink receives every post-burn-in event. Required.
	Sink func(ScoredEvent) error

	// OnRowError observes malformed rows; nil counts them silently.
	OnRowError func(error)

	// OnBurnInComplete fires once, immediately before the first post-burn-in event is
	// scored. The E4 pipeline exports the co-occurrence graph here, so the offline
	// partition is computed from burn-in state only and never conditions on the
	// scoring window it will be used to score.
	OnBurnInComplete func() error

	// Correlations, when set, estimates the pairwise dependence between detectors
	// during burn-in and is frozen at the boundary, after which the combination
	// applies Brown's correction of equation (19) instead of plain Fisher.
	//
	// Nil means the combination stays at Fisher, which assumes independence. That is
	// the wrong assumption for these detectors â€” Â§6 and Â§9 score the same fields at
	// different scopes â€” so nil is for tests and for deliberately measuring the
	// uncorrected composite, not a sensible default for a real run.
	Correlations *calibration.Correlations

	// Conformal, when set, accumulates each detector's burn-in p-value distribution and
	// is frozen at the boundary, after which every detector's model p-value is replaced
	// by its rank in that distribution (Â§10.1) before the combination.
	//
	// Nil leaves each detector on its own model tail, which is correct only while the
	// nulls are. Two of them have been measured not to be, which is the argument for
	// setting this; the argument against setting it blindly is the floor at 1/(n+1),
	// documented on [calibration.Conformal] and handled by CombinedScore.ModelLogP.
	Conformal *calibration.Conformal

	// BurnInSink, when set, receives every burn-in event with its verdicts, exactly as
	// Sink receives every scored one. Combined is always nil: the combination needs the
	// covariance and the conformal estimate, and neither is frozen until the boundary
	// this event precedes.
	//
	// It exists so that a quantity may be FITTED on burn-in through the same code path
	// that will later apply it. Â§8.2's rule for the partition -- a quantity used to score
	// an event must not have been fitted on it -- read from the other side: the burn-in
	// window is the only labelled data a deployable rule may learn from, because a weight
	// fitted on the scoring window is an oracle wearing a weight's clothes.
	//
	// Nil costs nothing. Burn-in events are scored either way, since state has to warm
	// under the same path scoring uses; this only decides whether the result is shown to
	// anybody.
	BurnInSink func(ScoredEvent) error

	burnInDone bool
	covariance *calibration.CovarianceModel
	conformal  *calibration.ConformalModel
}

// Covariance returns the frozen dependence estimate, or nil if none was made. It is
// exposed so the run can record what the correction was given.
func (c *ReplayCorpusCommand) Covariance() *calibration.CovarianceModel { return c.covariance }

// ConformalModel returns the frozen conformal calibration, or nil if none was made,
// so the run can record which detectors it covered and on how many observations.
func (c *ReplayCorpusCommand) ConformalModel() *calibration.ConformalModel { return c.conformal }

// Execute runs the replay to end of stream.
func (c *ReplayCorpusCommand) Execute(ctx context.Context) (*ReplayReport, error) {
	if c.Sink == nil {
		return nil, errors.New("application: replay requires a sink")
	}
	report := &ReplayReport{}

	for {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		e, err := c.Source.Next()
		if errors.Is(err, io.EOF) {
			return report, nil
		}
		if err != nil {
			report.RowErrors++
			if c.OnRowError != nil {
				c.OnRowError(err)
			}
			continue
		}
		report.RowsRead++
		report.EventsSeen++

		if c.IncludeEntity != nil && !c.IncludeEntity(e.Entity()) {
			report.EventsSkipped++
			continue
		}

		if err := c.processEvent(ctx, e, report); err != nil {
			return report, err
		}
	}
}

// processEvent applies the per-event pipeline in the fixed order.
func (c *ReplayCorpusCommand) processEvent(ctx context.Context, e *event.Event, report *ReplayReport) error {
	scored := e.OccurredAt() >= c.BurnInEnd
	if scored && !c.burnInDone {
		c.burnInDone = true
		// Freeze the dependence estimate before the first scored event, for the reason
		// Â§8.2 gives for the partition: a quantity used to score an event must not have
		// been fitted on it.
		if c.Correlations != nil {
			c.covariance = c.Correlations.Freeze()
		}
		if c.Conformal != nil {
			c.conformal = c.Conformal.Freeze()
		}
		if c.OnBurnInComplete != nil {
			if err := c.OnBurnInComplete(); err != nil {
				return fmt.Errorf("application: burn-in completion hook: %w", err)
			}
		}
	}

	// Derived coarse fields, where the source has structure the framework can infer.
	// Applied before scoring and before the registry sees the event, because a detector
	// iterates the registry's entries and a derived field is only scoreable once it is
	// one of them.
	//
	// The augmented event carries its own digest. That is not a defect to be worked
	// around: the identifier is a digest of content, the derived fields ARE content, and
	// a run with derivation on is in any case not comparable to one without it â€” the
	// composition differs. Preserving the original identifier would make two different
	// events claim to be the same one.
	if c.Deriver != nil {
		if derived := c.Deriver.Augment(e); len(derived) > 0 {
			augmented := e.With(derived)
			e = &augmented
		}
	}

	var (
		all          detector.Verdicts
		observations []detector.Observation
	)
	for _, d := range c.Detectors.All() {
		verdicts, obs, err := d.Score(ctx, e)
		if err != nil {
			return fmt.Errorf("application: %s.Score: %w", d.ID(), err)
		}
		all = append(all, verdicts...)
		if obs != nil {
			observations = append(observations, obs)
		}
	}

	var shadow detector.Verdicts
	for _, d := range c.Shadows {
		verdicts, obs, err := d.Score(ctx, e)
		if err != nil {
			return fmt.Errorf("application: shadow %s.Score: %w", d.ID(), err)
		}
		shadow = append(shadow, verdicts...)
		if obs != nil {
			observations = append(observations, obs)
		}
	}

	if !scored && (c.Correlations != nil || c.Conformal != nil || c.BurnInSink != nil) {
		// Burn-in scores every event without emitting it, which is exactly the sample
		// these estimates need and the only one they may use.
		all.SortCanonical()
		if c.Correlations != nil {
			observeDependence(c.Correlations, all)
		}
		if c.Conformal != nil {
			for _, v := range all.Evaluated() {
				if logP, ok := v.LogPValue(); ok {
					c.Conformal.Observe(string(v.DetectorID()), logP)
				}
			}
		}
		if c.BurnInSink != nil {
			shadow.SortCanonical()
			if err := c.BurnInSink(ScoredEvent{
				Event: e, Verdicts: all, ShadowVerdicts: shadow,
			}); err != nil {
				return fmt.Errorf("application: burn-in sink: %w", err)
			}
		}
	}

	if scored {
		all.SortCanonical()
		shadow.SortCanonical()

		combined, err := combineWith(all, c.covariance, c.conformal)
		if err != nil {
			return err
		}
		if combined == nil {
			report.NoOpinion++
		}
		if err := c.Sink(ScoredEvent{
			Event:          e,
			Verdicts:       all,
			Combined:       combined,
			ShadowVerdicts: shadow,
		}); err != nil {
			return fmt.Errorf("application: sink: %w", err)
		}
		report.EventsScored++
	} else {
		report.EventsWarmed++
	}

	// Observe strictly after scoring (Â§5.2), in registration order, then the field
	// registry, so a first-ever value was still novel at scoring time.
	for _, obs := range observations {
		if err := obs.Commit(ctx); err != nil {
			return fmt.Errorf("application: %s.Observe: %w", obs.DetectorID(), err)
		}
	}
	c.FieldRegistry.ObserveEvent(e)
	if c.Deriver != nil {
		// Fed after scoring for the same reason the registry is: what a field's values
		// look like is learned from history, and an event must not contribute to the
		// structure it is judged under.
		for f, v := range e.All() {
			if v.IsUsable() {
				c.Deriver.Observe(e.Source(), f, v.Text())
			}
		}
	}
	return nil
}

// observeDependence feeds one burn-in event's evaluated verdicts to the estimator, as
// (detector id, -2 ln P) pairs in canonical order.
func observeDependence(acc *calibration.Correlations, verdicts detector.Verdicts) {
	evaluated := verdicts.Evaluated()
	if len(evaluated) < 2 {
		return // a single verdict has no pair to inform
	}
	ids := make([]string, 0, len(evaluated))
	stats := make([]float64, 0, len(evaluated))
	for _, v := range evaluated {
		p, ok := v.PValue()
		if !ok {
			continue
		}
		ids = append(ids, string(v.DetectorID()))
		stats = append(stats, -2*math.Log(p))
	}
	acc.Observe(ids, stats)
}

// combineWith applies Â§10.2's combination to the evaluated verdicts, which arrive
// already in canonical order; abstentions reduce J rather than contributing any value
// (Â§10.2, R3). J = 0 yields nil: no opinion, never a null score.
//
// With a covariance model it applies Brown's correction, equation (19): the effective
// degrees of freedom f and the scale c absorb the dependence Fisher would otherwise
// count as independent evidence. With nil it is exactly Fisher, equation (18), and
// Â§10.2 requires that the degradation be to Fisher rather than to failure.
func combineWith(verdicts detector.Verdicts, cov *calibration.CovarianceModel,
	conf *calibration.ConformalModel) (*CombinedScore, error) {
	evaluated := verdicts.Evaluated()
	if len(evaluated) == 0 {
		return nil, nil
	}
	// Everything here is ln p, not p. A detector's tail can run to ln P â‰ˆ âˆ’4000, which a
	// linear p floors at 5eâˆ’324 and reads back as âˆ’744 â€” identically for every event past
	// the floor, which is the tie that ranking cannot survive.
	logPs := make([]float64, 0, len(evaluated))
	modelLogPs := make([]float64, 0, len(evaluated))
	conformalApplied := false
	minLogP, minDetector := math.Inf(1), ""
	for _, v := range evaluated {
		logP, ok := v.LogPValue()
		if !ok {
			// Unreachable by construction: Evaluated() returns only verdicts whose
			// status carries a p-value. Guarded so a regression fails loudly.
			return nil, errors.New("application: evaluated verdict without a p-value")
		}
		modelLogPs = append(modelLogPs, logP)
		if calibrated, ok := conf.Calibrate(string(v.DetectorID()), logP); ok {
			// A conformal p-value is a rank, bounded below by 1/(n+1), so it cannot
			// underflow and its logarithm is taken safely here.
			logP = math.Log(calibrated)
			conformalApplied = true
		}
		logPs = append(logPs, logP)

		// The minimum is taken over the same values the combination consumes, so that
		// conformal calibration â€” which matters far more to a minimum than to a sum,
		// since a minimum is dominated by whichever detector has the smallest floor â€”
		// applies to both alike. Ties keep the first in canonical order, which the
		// caller has already fixed, so the named detector is reproducible (R4).
		if logP < minLogP {
			minLogP, minDetector = logP, string(v.DetectorID())
		}
	}
	ids := make([]string, 0, len(evaluated))
	for _, v := range evaluated {
		ids = append(ids, string(v.DetectorID()))
	}

	// Brown's covariance is estimated during burn-in from the detectors' own p-values,
	// because those are what the combination consumed when the estimate was made. Under
	// conformal calibration the combination consumes calibrated p-values instead, and the
	// two live on different scales: âˆ’2 ln P runs to thousands on a miscalibrated model
	// tail and to tens on a rank. Dividing a statistic by a scale measured on the other
	// one produces a number that means nothing, so equation (19) is not applied to the
	// calibrated statistic â€” the degradation is to Fisher, which Â§10.2 requires and which
	// costs little here, the measured correlations being 0.03 to 0.15.
	//
	// The estimates cannot simply be swapped: conformal is frozen at the same boundary as
	// the covariance, so estimating the covariance on calibrated values is circular. A
	// split burn-in would resolve it and is recorded as future work.
	corrective := cov
	if conformalApplied {
		corrective = nil
	}

	x2, c, f, logP, err := combineTail(logPs, ids, corrective)
	var correctionRejected string
	if err != nil && corrective != nil {
		// The covariance the burn-in measured implies a non-positive variance for X2 on
		// this composition, which no joint distribution of the statistics can produce.
		// Degrade to plain Fisher and record why, rather than abandoning the run: the
		// measurement that remains is worth more than the one that does not exist, and
		// the degradation is the one section 10.2 already prescribes for an unusable
		// covariance.
		correctionRejected = err.Error()
		corrective = nil
		x2, c, f, logP, err = combineTail(logPs, ids, nil)
	}
	if err != nil {
		return nil, err
	}
	score := &CombinedScore{
		X2:                 x2,
		J:                  len(logPs),
		P:                  math.Exp(logP),
		LogP:               logP,
		C:                  c,
		F:                  f,
		Corrected:          corrective != nil,
		CorrectionRejected: correctionRejected,
		Conformal:          conformalApplied,
		ModelLogP:          logP,
		MinPLogP:           calibration.SidakLog(minLogP, len(logPs)),
		MinDetector:        minDetector,
	}
	if conformalApplied {
		// The same combination over the detectors' own p-values, kept for the tie-break
		// the conformal floor makes necessary. Not a second opinion on significance: it
		// is an ordering within a set the conformal value has already tied. Brown does
		// apply here, because these are the p-values its covariance was measured on.
		if _, _, _, modelLogP, err := combineTail(modelLogPs, ids, cov); err == nil {
			score.ModelLogP = modelLogP
		}
	}
	return score, nil
}

// combineTail applies Â§10.2 to one vector of p-values: Fisher (18), then Brown (19) when
// a covariance model is present. It returns the statistic, Brown's scale and effective
// degrees of freedom, the combined tail and its logarithm.
//
// With no covariance model the scale is exactly 1 and the degrees of freedom exactly 2J,
// which is Fisher; Â§10.2 requires that the degradation be to Fisher rather than to
// failure.
func combineTail(logPs []float64, ids []string, cov *calibration.CovarianceModel) (
	x2, c, f, logTail float64, err error) {
	x2, df, fisherLogTail, err := calibration.FisherLog(logPs)
	if err != nil {
		return 0, 0, 0, 0, fmt.Errorf("application: Fisher: %w", err)
	}
	if cov == nil {
		return x2, 1, float64(df), fisherLogTail, nil
	}
	c, f, brownLogTail, err := calibration.BrownFromStatistic(x2, len(logPs), cov.Matrix(ids))
	if err != nil {
		return 0, 0, 0, 0, fmt.Errorf("application: Brown: %w", err)
	}
	return x2, c, f, brownLogTail, nil
}
