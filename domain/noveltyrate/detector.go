// Package noveltyrate asks whether an entity is producing MORE first-ever values than it
// normally does, rather than how improbable any single first-ever value was.
//
// # Why this detector exists
//
// Detector I is correctly calibrated and structurally unable to alert on most accounts.
// For an unseen value its estimate reduces to the reserved mass α/(n + α(K+1)), which with
// α = 1 is close to 1/n. Measured on planted attacks, `p × n` has median 1.15 across 32
// victims spanning 263 to 20,666 events of history and four attack types: the p-value for
// a first-ever value is essentially one over the size of the account's history, almost
// independent of what the value was.
//
// The consequence is not subtle. An alert budget of 700 events against 4.49 million
// requires p of about 1.6e-4 or smaller, so an account needs on the order of 117,000
// events of accumulated history before ANY single novel value it produces can win a slot.
// Ordinary accounts have thousands. They are not hard to detect on; they are ineligible.
// Of 856 planted attack events — every one carrying a genuinely first-ever value for its
// victim — Detector I alerted on none, and the most extreme of them missed the threshold
// by a factor of seven.
//
// Aggregating to the entity-day does not fix it, because Fisher's sum of X² grows with the
// event count and so favours busy entities again; the standardised form removes that bias
// and most of the signal with it. And the shape-sensitive Good-Turing reserve makes it
// worse still: measured, novelty fell 60 to 46 and pairing 59 to 36, because a long
// history means many values seen exactly once, so a high singleton rate makes a first-ever
// value look EXPECTED for precisely the accounts Detector I ranks highest.
//
// Every one of those scores is either proportional to 1/n, proportional to n, or free of n
// and of signal together. What is needed is a question whose answer does not scale with
// volume at all.
//
// # The question this asks instead
//
// Not "how improbable is this value under the entity's null" but "is this account
// producing first-ever values at a higher rate than it historically does". An account that
// touches one new host a week and touches forty in ten minutes has departed from itself by
// a factor that says nothing about how busy it is. That is scale-free by construction: the
// comparison is always against the same account's own rate, never against another
// account's volume.
//
// H0: with the entity's own historical novelty rate theta ~ Beta(a, b) estimated from its
// decayed counts, the number of first-ever values among the m events of the current window
// is K ~ BetaBinomial(m, a, b). The p-value is P(K >= k_obs).
//
// # What it does not cover, by construction
//
// It sees only novelty. An attack that reuses every value the account already uses — one
// that departs in timing or in volume alone — is invisible to it and must remain the
// business of Detectors II(a) and II(b). Reporting it as a general improvement would be
// wrong. Measured on planted attacks at 1000 alerts a day it takes credential spray from
// 0 to 117 of 320, lateral movement from 0 to 26 of 40 and account takeover to 64 of 120,
// and leaves off-hours and low-and-slow exactly where they were: a real effect on three of
// six types, between a third and two thirds of each, and nothing on the other three. An
// earlier version of this comment said "from nothing to nearly everything", which the
// measurement does not support.
package noveltyrate

import (
	"context"
	"fmt"
	"math"

	"github.com/JohnPierman/ethogram/domain/detector"
	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/novelty"
	"github.com/JohnPierman/ethogram/domain/registry"
)

// DetectorID names this detector in result JSON, dashboard labels and the per-source
// calibration sets of section 10.1.
const DetectorID = detector.ID("noveltyrate")

// WindowSeconds is the width of the window whose novelty count is tested, in corpus
// seconds. An hour matches the volume detector's window, and is short enough that a burst
// confined to ten minutes is not diluted by a day of ordinary traffic around it.
const WindowSeconds = 3600

// The Jeffreys prior on the novelty rate. Beta(1/2, 1/2) is the reference prior for a
// Bernoulli rate: it keeps a silent account from asserting that novelty is impossible for
// it, which would make that account's first new value infinitely surprising.
const (
	PriorAlpha = 0.5
	PriorBeta  = 0.5
)

// MinHistory is the decayed event count below which this detector abstains.
//
// A rate estimated from a handful of events is not a rate. Below this the Beta posterior
// is dominated by the prior, and the p-value would report the prior's opinion as though it
// were the account's — which R3 forbids. An abstention says "no basis"; a number does not.
const MinHistory = 50

// State is an entity's novelty-rate state. Fixed size regardless of event count: two
// decayed scalars for the history and two integers for the window in progress.
type State struct {
	// HistoryNovel and HistoryTotal are decayed counts over COMPLETED windows. The
	// window in progress is deliberately excluded, so that an entity is never judged
	// against a rate the current burst has already inflated.
	HistoryNovel float64
	HistoryTotal float64

	// WindowIndex is the window the running counts belong to, as a count of
	// WindowSeconds since the corpus epoch.
	WindowIndex int64
	// WindowNovel and WindowTotal count the window in progress, EXCLUDING the event
	// being scored: k_obs is WindowNovel + 1 when that event carries a first-ever
	// value, and m is WindowTotal + 1 either way.
	WindowNovel int64
	WindowTotal int64

	LastSeen event.Timestamp
}

// StateRepository persists per-entity novelty-rate state.
type StateRepository interface {
	FindByEntity(ctx context.Context, source event.SourceID, entity event.EntityID) (*State, bool, error)
	SaveState(ctx context.Context, source event.SourceID, entity event.EntityID, s *State) error
}

// Detector is the novelty-rate detector.
type Detector struct {
	states   StateRepository
	values   novelty.ValueCountRepository
	registry novelty.FieldRegistry
	halfLife novelty.HalfLife
}

// NewDetector wires the detector. values and registry are the same ones Detector I reads,
// because "has this entity seen this value before" is the same question in both places and
// answering it from a second store would let the two disagree.
func NewDetector(states StateRepository, values novelty.ValueCountRepository,
	reg novelty.FieldRegistry, halfLife novelty.HalfLife) *Detector {
	return &Detector{states: states, values: values, registry: reg, halfLife: halfLife}
}

// ID implements detector.Detector.
func (d *Detector) ID() detector.ID { return DetectorID }

// NullHypothesis implements detector.Detector.
func (d *Detector) NullHypothesis() string {
	return "the number of first-ever values among this entity's events in the current " +
		"window is drawn from its own historical novelty rate, K ~ BetaBinomial(m, a, b) " +
		"with the rate estimated from the entity's decayed counts over completed windows"
}

// windowOf returns the window index an instant falls in.
func windowOf(at event.Timestamp) int64 { return int64(at) / WindowSeconds }

// Score implements detector.Detector.
//
// It reads state and never writes it: the window roll below is computed as a view of the
// stored state at this event's instant, and only the returned Observation can apply it.
func (d *Detector) Score(ctx context.Context, e *event.Event) (detector.Verdicts, detector.Observation, error) {
	target := detector.Target{Event: e.ID(), Entity: e.Entity()}

	novel, fields, err := d.isNovel(ctx, e)
	if err != nil {
		return nil, nil, err
	}
	target.Fields = fields
	if len(fields) == 0 {
		// No in-scope field was present and usable, so the event induces no test. An
		// empty verdict set is not an abstention: it means there was nothing to ask.
		return nil, detector.NoObservation{Event: e.ID(), Detector: DetectorID}, nil
	}

	state, _, err := d.states.FindByEntity(ctx, e.Source(), e.Entity())
	if err != nil {
		return nil, nil, fmt.Errorf("noveltyrate: read state for %q: %w", e.Entity(), err)
	}
	if state == nil {
		state = &State{WindowIndex: windowOf(e.OccurredAt())}
	}

	histNovel, histTotal, winNovel, winTotal := d.view(state, e.OccurredAt())
	obs := &observation{
		detector: d, event: e.ID(), source: e.Source(), entity: e.Entity(),
		at: e.OccurredAt(), novel: novel,
	}

	if histTotal < MinHistory {
		v, abstainErr := detector.NewAbstained(DetectorID, target,
			detector.StatusAbstainedUnusable,
			"too little history to estimate this entity's novelty rate",
			detector.NewEvidence(nil, map[string]float64{
				"history_events": histTotal,
				"minimum":        MinHistory,
			}, nil))
		if abstainErr != nil {
			return nil, nil, fmt.Errorf("noveltyrate: abstain: %w", abstainErr)
		}
		return detector.Verdicts{v}, obs, nil
	}

	k := winNovel
	if novel {
		k++
	}
	m := winTotal + 1
	a := PriorAlpha + histNovel
	b := PriorBeta + math.Max(histTotal-histNovel, 0)

	logP := LogUpperTail(int(k), int(m), a, b)
	v, err := detector.NewEvaluatedLog(DetectorID, target, logP, detector.NewEvidence(nil,
		map[string]float64{
			"window_novel":   float64(k),
			"window_events":  float64(m),
			"history_novel":  histNovel,
			"history_events": histTotal,
			"history_rate":   histNovel / histTotal,
			"window_rate":    float64(k) / float64(m),
		}, map[string]string{"window": "3600s"}))
	if err != nil {
		return nil, nil, fmt.Errorf("noveltyrate: verdict: %w", err)
	}
	return detector.Verdicts{v}, obs, nil
}

// view returns the state as of an instant: history decayed, and the window in progress
// rolled into history if this event opens a new one.
//
// This is the read-side half of the roll. Applying it is the Observation's business; doing
// it here would make Score a writer and defeat the capability separation of section 5.2.
func (d *Detector) view(s *State, at event.Timestamp) (histNovel, histTotal float64, winNovel, winTotal int64) {
	histNovel = novelty.Decay(s.HistoryNovel, s.LastSeen, at, d.halfLife)
	histTotal = novelty.Decay(s.HistoryTotal, s.LastSeen, at, d.halfLife)
	if windowOf(at) != s.WindowIndex {
		// The stored window has closed. It joins the history, and the event being
		// scored opens an empty one.
		return histNovel + float64(s.WindowNovel), histTotal + float64(s.WindowTotal), 0, 0
	}
	return histNovel, histTotal, s.WindowNovel, s.WindowTotal
}

// isNovel reports whether any in-scope field of the event carries a value this entity has
// never been seen with, and returns the fields that were actually testable.
func (d *Detector) isNovel(ctx context.Context, e *event.Event) (bool, []event.FieldPath, error) {
	var (
		novel  bool
		tested []event.FieldPath
	)
	for _, entry := range d.registry.FindBySource(e.Source()) {
		if !entry.Kind.IsScoreable() || entry.Kind == registry.KindUnknown {
			continue
		}
		value, present := e.Get(entry.Path)
		if !present || !value.IsUsable() {
			continue
		}
		// The history is keyed on the vocabulary item Detector I stored, so novelty is
		// asked of the token and not of the value's text (§5.1). For a continuous field
		// those differ, and comparing the text against banded history would find every
		// measurement novel — the field's novelty rate would then be one by
		// construction. A value that does not project is not testable, exactly as an
		// unusable one is not.
		token, projected := entry.Kind.Token(value.Text())
		if !projected {
			continue
		}
		tested = append(tested, entry.Path)
		if novel {
			continue // already established; the remaining fields only extend `tested`
		}
		rows, err := d.values.FindAllByEntityField(ctx, e.Source(), e.Entity(), entry.Path, e.OccurredAt())
		if err != nil {
			return false, nil, fmt.Errorf("noveltyrate: read %q history: %w", entry.Path, err)
		}
		seen := false
		for _, r := range rows {
			if r.Value == token && r.Count > 0 {
				seen = true
				break
			}
		}
		if !seen {
			novel = true
		}
	}
	return novel, tested, nil
}

// observation carries the state update implied by one event.
type observation struct {
	detector *Detector
	event    event.ID
	source   event.SourceID
	entity   event.EntityID
	at       event.Timestamp
	novel    bool
}

func (o *observation) EventID() event.ID       { return o.event }
func (o *observation) DetectorID() detector.ID { return DetectorID }

// Commit applies the window roll and the event's own contribution.
func (o *observation) Commit(ctx context.Context) error {
	d := o.detector
	state, _, err := d.states.FindByEntity(ctx, o.source, o.entity)
	if err != nil {
		return fmt.Errorf("noveltyrate: read state for %q: %w", o.entity, err)
	}
	if state == nil {
		state = &State{WindowIndex: windowOf(o.at)}
	}

	histNovel, histTotal, winNovel, winTotal := d.view(state, o.at)
	next := &State{
		HistoryNovel: histNovel,
		HistoryTotal: histTotal,
		WindowIndex:  windowOf(o.at),
		WindowNovel:  winNovel,
		WindowTotal:  winTotal + 1,
		LastSeen:     o.at,
	}
	if o.novel {
		next.WindowNovel++
	}
	if err := d.states.SaveState(ctx, o.source, o.entity, next); err != nil {
		return fmt.Errorf("noveltyrate: save state for %q: %w", o.entity, err)
	}
	return nil
}
