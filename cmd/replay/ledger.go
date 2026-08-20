package main

import (
	"fmt"
	"math"
	"sort"

	"github.com/JohnPierman/ethogram/application"
	"github.com/JohnPierman/ethogram/domain/detector"
	"github.com/JohnPierman/ethogram/domain/event"
)

// The alert ledger: every per-detector arm's ranked queue, per day, written out whole.
//
// It exists because of an arithmetic problem in the development loop rather than in the
// statistics. A replay of one corpus is eighty minutes, and the question this project
// now faces -- how to divide a fixed budget across arms of very unequal quality -- has
// a large space of candidate answers and no theory that picks one. Screening them at
// eighty minutes each is not a loop anybody iterates in.
//
// The obvious cheap substitute does not work, and it was tried first. Each arm's queue
// can be approximately reconstructed from the p-histogram every result already carries:
// invert the histogram at the budget to get the arm's cut, then read each labelled
// event's membership off its own recorded per-detector p-value. Both are committed. But
// the histogram is twenty bins to the decade and the real arms take their top B PER DAY
// where a reconstruction pools the run, and the two errors compound rather than cancel:
// measured against the recorded truth on `lanl-r11-b1000-union-d7-14-002`, the
// reconstruction read Detector I at 43 where the run recorded 11, and `noveltyrate` at
// 217 where the run recorded 185. Errors of that size do not screen a rule whose whole
// margin is a few per cent.
//
// So the ledger records the ORDER itself, which is the only thing an allocation rule
// reads. A rule evaluated against it is exact rather than estimated, and the in-tree
// implementation of whichever rule wins must reproduce the ledger's number for that
// rule -- which is a sharper test than any assertion written by hand.
//
// It is an intermediate artefact and is written wherever the flag points, never into
// `results/`. A file in `results/` is a measurement with provenance, and the provenance
// gate rejects the whole directory when it finds anything else there.

// burnInLedgerDepth is how deep each arm's burn-in queue is retained.
//
// Set to the largest budget, which is also the deepest an allocation rule can ever read:
// no arm can be given more than the whole budget, so a labelled burn-in event sitting
// below that depth is one no rule could have surfaced anyway. Past it the event is
// recorded as CENSORED rather than as absent -- a different statement, and the estimator
// has to be told which it is.
//
// It was 4000 first, on the reasoning that a labelled event below the budget still says
// something about an arm's quality. That is true and it cost too much to keep. [dayAlerts]
// holds a sorted slice, and Detector I's p-value for a first-ever value is about one over
// the entity's history length, so it grows steadily MORE extreme as a busy account's day
// goes on: insertions land near the head of the list and memmove the whole of it. At
// depth 4000 across six arms that measured 2.85x on the whole replay -- burn-in mirroring
// cost nearly twice the rest of the pipeline put together. At 1000 it is a quarter of
// that, and the evidence lost is evidence about ranks no budget reaches.
const burnInLedgerDepth = 1000

// ledgerAlert is one arm's alert at one rank, with everything an allocation rule can
// legitimately read and nothing it cannot.
//
// The field names are short because there are of the order of a hundred thousand of
// these and the long forms cost more in file size than they return in legibility.
type ledgerAlert struct {
	Rank int `json:"r"`
	// LogP is the arm's own model log p-value, the quantity the arm ranked on. It is
	// recorded so a rule can be checked for reading it, NOT so a rule may compare it
	// across arms: the arms share no scale and that is the defect §3.4 diagnoses.
	LogP     float64 `json:"lp"`
	TSeconds int64   `json:"t"`
	Entity   string  `json:"e"`
	SrcComp  string  `json:"s"`
	DstComp  string  `json:"d"`
	// RedTeam and LabelledKey are the ground truth. A rule may read them on burn-in
	// days and must not on scoring days; `ledger.py` asserts that split rather than
	// trusting it.
	RedTeam     bool     `json:"rt"`
	LabelledKey string   `json:"lk,omitempty"`
	Categories  []string `json:"cat,omitempty"`
}

// ledgerLabelled is one labelled event, with its own score under every arm that evaluated
// it. On burn-in days this is the fitting sample; on scoring days it is the evaluation
// target.
type ledgerLabelled struct {
	Key      string `json:"key"`
	TSeconds int64  `json:"t"`
	Entity   string `json:"e"`
	Day      int64  `json:"day"`
	// LogDetector is each arm's log p-value for this event.
	//
	// The log and not the p-value. A detector's tail reaches ln P = -4000 on this corpus,
	// which is zero as a float64, and a weight fitted from a sample of zeros is fitted
	// from nothing. Burn-in labelled events happen to sit well short of that -- histories
	// are short early in the corpus, so Detector I's p-value for a first-ever value is
	// around one over a few hundred -- but "happens to be representable on this corpus" is
	// not a property to rely on, and the field a future corpus needs is this one.
	LogDetector map[string]float64 `json:"lp"`
	// Detector is the same values as p-values, retained for readability of small samples.
	// Zero where the log underflows; never read for fitting.
	Detector map[string]float64 `json:"p"`
}

// ledgerWindow is one window -- burn-in or scoring -- of the ledger.
type ledgerWindow struct {
	Days []int64 `json:"days"`
	// Scored is how many events each arm EVALUATED on each day. It is the denominator
	// of a within-arm rank, and without it a rank is a position with no scale: rank 100
	// of 600,000 and rank 100 of 900 are not the same evidence.
	Scored map[string]map[string]int64 `json:"scored"`
	// Queues is arm -> day -> ranked alerts, best first.
	Queues map[string]map[string][]ledgerAlert `json:"queues"`
	// Labelled is every labelled event in the window, whether or not any arm ranked it
	// inside its retained depth. An arm that never surfaced a labelled event is
	// evidence about that arm, and dropping the event would hide it.
	Labelled []ledgerLabelled `json:"labelled"`
}

// observeBurnIn mirrors the per-arm ranking on burn-in days.
//
// It is the same construction as [accumulator.observeDetectorArms] over the same
// verdicts -- deliberately, because a weight fitted under one ranking and applied under
// another is fitted on nothing. The two differ only in depth, and in there being no
// combination to record: at burn-in neither the covariance nor the conformal model is
// frozen, so no combined score exists for these events and none is invented.
func (a *accumulator) observeBurnIn(se application.ScoredEvent) error {
	tSeconds := int64(se.Event.OccurredAt() / event.Second)
	day := tSeconds / 86400
	if !a.burnInFitDays[day] {
		// A burn-in day carrying no labelled event cannot inform a weight: the fit reads
		// where the labelled events sit in each arm's order that day, and a day with none
		// contributes no term to the likelihood. On LANL the 49 pre-boundary labels fall
		// on four of the seven burn-in days, so skipping the other three is a third of
		// this mirror's cost for no loss of evidence at all.
		//
		// It is a cost decision and not a statistical one, which is why it is expressed
		// as the day set rather than folded into the estimator.
		return nil
	}
	entity := string(se.Event.Entity())

	srcComp := fieldText(se.Event, "auth.source_computer")
	dstComp := fieldText(se.Event, "auth.destination_computer")

	// The cheap gate first. Matching the four-tuple costs a format per event and burn-in
	// is tens of millions of them, so the entity set -- one map lookup on a string
	// already in hand -- decides whether that cost is paid at all.
	var key string
	isRed := false
	if _, named := a.labels.users[entity]; named {
		key = redKey(tSeconds, entity, srcComp, dstComp)
		_, isRed = a.labels.keys[key]
	}

	best := map[detector.ID]float64{}
	for _, v := range se.Verdicts {
		logP, ok := v.LogPValue()
		if !ok {
			continue
		}
		if prev, seen := best[v.DetectorID()]; !seen || logP < prev {
			best[v.DetectorID()] = logP
		}
	}

	ids := make([]detector.ID, 0, len(best))
	for id := range best {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	if isRed {
		logs := make(map[string]float64, len(ids))
		perDetector := make(map[string]float64, len(ids))
		for _, id := range ids {
			logs[string(id)] = best[id]
			perDetector[string(id)] = math.Exp(best[id])
		}
		a.burnInLabelled = append(a.burnInLabelled, ledgerLabelled{
			Key: key, TSeconds: tSeconds, Entity: entity, Day: day,
			LogDetector: logs, Detector: perDetector,
		})
	}

	for _, id := range ids {
		logP := best[id]
		a.bumpScored(a.burnInScored, id, day)
		byDay, ok := a.burnInPerDay[id]
		if !ok {
			byDay = make(map[int64]*dayAlerts)
			a.burnInPerDay[id] = byDay
		}
		da, ok := byDay[day]
		if !ok {
			da = &dayAlerts{}
			byDay[day] = da
		}
		da.push(alert{
			P: math.Exp(logP), LogP: logP, ModelLogP: logP, TSeconds: tSeconds,
			Entity: entity, SrcComp: srcComp, DstComp: dstComp,
			IsRedTeam: isRed, J: 1, MinDetector: string(id),
		}, burnInLedgerDepth)
	}
	return nil
}

// bumpScored counts one evaluation by one arm on one day.
func (a *accumulator) bumpScored(into map[detector.ID]map[int64]int64, id detector.ID, day int64) {
	byDay, ok := into[id]
	if !ok {
		byDay = make(map[int64]int64)
		into[id] = byDay
	}
	byDay[day]++
}

// ledger assembles the whole ledger. Every map is walked in sorted key order so the
// file is byte-identical across runs of the same corpus (R4).
func (a *accumulator) ledger(runID string, budgetMax int, burnInSec int64) map[string]any {
	return map[string]any{
		"schema_version": "1",
		"kind":           "alert-ledger",
		"note": "per-arm ranked queues, per day, for screening budget-allocation rules " +
			"offline. An intermediate artefact and not a measurement: the numbers a rule " +
			"scores here are exact, but the rule that wins still has to be implemented in " +
			"the replay and recorded as a run before it is reportable",
		"run_id":              runID,
		"budget_max":          budgetMax,
		"burn_in_end_seconds": burnInSec,
		"burn_in_depth":       burnInLedgerDepth,
		"arms":                armNames(a.ledgerArmIDs()),
		"scoring": a.ledgerWindow(a.detectorPerDay, a.armScored,
			a.scoringLabelled(), a.topK),
		"burn_in": a.ledgerWindow(a.burnInPerDay, a.burnInScored,
			a.burnInLabelled, burnInLedgerDepth),
	}
}

// ledgerArmIDs is every arm either window saw, ascending.
func (a *accumulator) ledgerArmIDs() []detector.ID {
	present := make(map[detector.ID]bool)
	for id := range a.detectorPerDay {
		present[id] = true
	}
	for id := range a.burnInPerDay {
		present[id] = true
	}
	ids := make([]detector.ID, 0, len(present))
	for id := range present {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// scoringLabelled restates the scoring window's labelled events in the ledger's shape,
// from the same records the result file reports.
func (a *accumulator) scoringLabelled() []ledgerLabelled {
	out := make([]ledgerLabelled, 0, len(a.redTeamScored))
	for _, rt := range a.redTeamScored {
		out = append(out, ledgerLabelled{
			Key: rt.Key, TSeconds: rt.TSeconds, Entity: rt.Entity,
			Day: rt.TSeconds / 86400, Detector: rt.Detectors,
		})
	}
	return out
}

// ledgerWindow renders one window.
func (a *accumulator) ledgerWindow(perDay map[detector.ID]map[int64]*dayAlerts,
	scored map[detector.ID]map[int64]int64, labelled []ledgerLabelled,
	depth int) ledgerWindow {
	dayset := make(map[int64]bool)
	for _, byDay := range perDay {
		for d := range byDay {
			dayset[d] = true
		}
	}
	days := make([]int64, 0, len(dayset))
	for d := range dayset {
		days = append(days, d)
	}
	sort.Slice(days, func(i, j int) bool { return days[i] < days[j] })

	ids := make([]detector.ID, 0, len(perDay))
	for id := range perDay {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	outScored := make(map[string]map[string]int64, len(ids))
	outQueues := make(map[string]map[string][]ledgerAlert, len(ids))
	for _, id := range ids {
		byDayScored := make(map[string]int64)
		for d, n := range scored[id] {
			byDayScored[fmt.Sprintf("%d", d)] = n
		}
		outScored[string(id)] = byDayScored

		byDayQueue := make(map[string][]ledgerAlert)
		for _, d := range days {
			da, ok := perDay[id][d]
			if !ok || da == nil {
				continue
			}
			n := depth
			if n > len(da.alerts) {
				n = len(da.alerts)
			}
			q := make([]ledgerAlert, 0, n)
			for i, al := range da.alerts[:n] {
				la := ledgerAlert{
					Rank: i + 1, LogP: al.LogP, TSeconds: al.TSeconds,
					Entity: al.Entity, SrcComp: al.SrcComp, DstComp: al.DstComp,
					RedTeam: al.IsRedTeam, Categories: al.Categories,
				}
				if al.IsRedTeam {
					la.LabelledKey = labelledKey(al)
				}
				q = append(q, la)
			}
			byDayQueue[fmt.Sprintf("%d", d)] = q
		}
		outQueues[string(id)] = byDayQueue
	}

	if labelled == nil {
		// An empty window is an empty list, never null. A consumer distinguishing "no
		// labelled events here" from "this field was not written" is doing work the
		// encoding should have done for it.
		labelled = []ledgerLabelled{}
	}
	return ledgerWindow{
		Days: days, Scored: outScored, Queues: outQueues, Labelled: labelled,
	}
}
