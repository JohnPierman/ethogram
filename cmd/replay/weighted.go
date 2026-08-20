package main

import (
	"fmt"
	"sort"

	"github.com/JohnPierman/ethogram/domain/allocation"
	"github.com/JohnPierman/ethogram/domain/detector"
)

// The weighted arm: divide the budget by what each detector has earned.
//
// Sections 5.2 and 5.4 of the paper leave one diagnosis standing. Neither p-value
// combination reaches its best component, and the union at equal cost is beaten by it at
// every budget, but the reason is not that the detectors fail to complement each other. It
// is that every rule tried divides a fixed budget by quota rather than by quality: rank
// fusion takes rank 1 from every arm, then rank 2, so with J arms each reads about B/J of
// the depth it had alone, and an arm that detects nothing still draws B/J.
//
// This arm scores each alert on one scale instead, and lets the allocation fall out of the
// scoring. Two quantities per detector, both fitted on burn-in and frozen at the boundary:
//
//   - a [allocation.Tail], the detector's own null over its log p-value, which is what makes
//     alerts from detectors sharing no p-value scale comparable;
//   - an [allocation.Weight], how sharply that detector's labelled burn-in events separate
//     from its own null.
//
// An alert's score is the log-likelihood ratio the two imply. A detector whose labelled
// events sat where any event sits fits the uninformative weight, scores zero on everything
// it holds, and enters the queue only if the informative detectors fail to fill it -- so
// `volume`, which detects nothing anywhere, stops costing a sixth of the budget. There is no
// share parameter, because a common scale plus a fitted weight already implies a share.
//
// # Why the labels this uses are not the labels it is judged on
//
// The weights are fitted on the 49 labelled events that fall before the burn-in boundary and
// evaluated on the 700 after it. The two sets are disjoint by construction -- the boundary
// was fixed before any measurement -- and `application` asserts the partition in a test
// rather than trusting it. A weight fitted on the scoring window would be an oracle, which
// is what the cutoff analysis already is and says it is.
//
// # Why the score reads no rank
//
// Deliberately. A score that reads an alert's rank among the day's events cannot be computed
// until the day ends, so it cannot be deployed however well it evaluates here. Every quantity
// this arm reads is a property of the single alert or of frozen state, so the same arithmetic
// that ranks a batch thresholds a stream.

// weightedArm holds the frozen state the arm scores with, per detector.
type weightedArm struct {
	tails   map[detector.ID]*allocation.Tail
	weights map[detector.ID]allocation.Weight
	reports map[detector.ID]allocation.FitReport
	// skipped records detectors the fit could not cover, with the reason, so an arm that
	// silently dropped a detector is distinguishable from one that weighted it at zero.
	skipped map[detector.ID]string
}

// weightedExcessCount is how many of a detector's most extreme burn-in observations the
// tail's exponential extension is fitted from. Zero takes the package default.
const weightedExcessCount = 0

// fitWeightedArm builds the frozen state from the burn-in mirror.
//
// It returns nil when no burn-in labelled event was recorded, which is the honest outcome
// rather than an arm with every weight uninformative: a corpus whose labels all fall after
// the boundary supports no fitted weight at all, and reporting one fitted from nothing would
// be reporting a quota again under another name.
func (a *accumulator) fitWeightedArm() *weightedArm {
	if len(a.burnInLabelled) == 0 {
		return nil
	}
	w := &weightedArm{
		tails:   make(map[detector.ID]*allocation.Tail),
		weights: make(map[detector.ID]allocation.Weight),
		reports: make(map[detector.ID]allocation.FitReport),
		skipped: make(map[detector.ID]string),
	}

	for _, id := range a.ledgerArmIDs() {
		sample, total := a.burnInTailSample(id)
		if len(sample) < 2 {
			w.skipped[id] = "fewer than two burn-in observations retained"
			continue
		}
		tail, err := allocation.NewTail(sample, total, weightedExcessCount)
		if err != nil {
			w.skipped[id] = err.Error()
			continue
		}
		observed, censored := a.burnInFitSample(id, tail)
		weight, report, err := allocation.Fit(observed, censored)
		if err != nil {
			w.skipped[id] = err.Error()
			continue
		}
		w.tails[id] = tail
		w.weights[id] = weight
		w.reports[id] = report
	}
	if len(w.tails) == 0 {
		return nil
	}
	return w
}

// burnInTailSample is one detector's retained burn-in log p-values and the number of events
// it evaluated over the same days.
//
// Both are summed over the mirrored days only. Those are the days carrying a labelled event,
// which are the only days the mirror retains, so the denominator matches the sample rather
// than counting evaluations from days the sample does not cover.
func (a *accumulator) burnInTailSample(id detector.ID) ([]float64, int) {
	byDay, ok := a.burnInPerDay[id]
	if !ok {
		return nil, 0
	}
	days := make([]int64, 0, len(byDay))
	for d := range byDay {
		days = append(days, d)
	}
	sort.Slice(days, func(i, j int) bool { return days[i] < days[j] })

	sample := make([]float64, 0, len(days)*burnInLedgerDepth)
	total := 0
	for _, d := range days {
		total += int(a.burnInScored[id][d])
		for _, al := range byDay[d].alerts {
			sample = append(sample, al.LogP)
		}
	}
	return sample, total
}

// burnInFitSample is the weight's fitting sample for one detector: the tail quantiles of the
// labelled burn-in events it surfaced, and censoring points for those it evaluated and did
// not.
//
// A detector that ABSTAINED on a labelled event contributes neither, which is why the
// abstention is checked before anything else: abstention is the absence of an opinion and
// scoring it as a miss would penalise a detector for declining to guess (R3).
func (a *accumulator) burnInFitSample(id detector.ID, tail *allocation.Tail) (observed, censored []float64) {
	surfaced := make(map[string]float64)
	for _, da := range a.burnInPerDay[id] {
		for _, al := range da.alerts {
			if !al.IsRedTeam {
				continue
			}
			k := labelledKey(al)
			q := tail.LogQuantile(al.LogP)
			if prev, seen := surfaced[k]; !seen || q < prev {
				surfaced[k] = q
			}
		}
	}

	// The censoring point is the least extreme value the detector retained: an event it
	// evaluated but did not surface sits somewhere below that, and how far below is not
	// known. Taken over the mirrored days, so it is the shallowest cut the sample supports.
	shallowest, haveShallowest := 0.0, false
	for _, da := range a.burnInPerDay[id] {
		if len(da.alerts) == 0 {
			continue
		}
		q := tail.LogQuantile(da.alerts[len(da.alerts)-1].LogP)
		if !haveShallowest || q > shallowest {
			shallowest, haveShallowest = q, true
		}
	}

	// Sorted by key so the sample is assembled in one order whatever order the map yields
	// (R4); Fit sorts internally as well, and both are cheap at this size.
	keys := make([]string, 0, len(a.burnInLabelled))
	byKey := make(map[string]ledgerLabelled, len(a.burnInLabelled))
	for _, rec := range a.burnInLabelled {
		k := fmt.Sprintf("%d|%s", rec.TSeconds, rec.Entity)
		if _, seen := byKey[k]; !seen {
			keys = append(keys, k)
		}
		byKey[k] = rec
	}
	sort.Strings(keys)

	for _, k := range keys {
		if _, evaluated := byKey[k].LogDetector[string(id)]; !evaluated {
			continue // abstained: no opinion, and not a miss
		}
		if q, ok := surfaced[k]; ok {
			observed = append(observed, q)
			continue
		}
		if haveShallowest {
			censored = append(censored, shallowest)
		}
	}
	return observed, censored
}

// scoredAlert is one alert with the score its own detector's weight gives it.
type scoredAlert struct {
	al    alert
	arm   detector.ID
	score float64
}

// weightedLess is the deterministic total order: highest score first, then the canonical
// event identity.
//
// The tie-break is the identity and never a p-value, for the reason `union.go` gives: an
// uninformative detector scores every alert it holds at exactly zero, so ties are structural
// rather than rare, and breaking them on log p would hand every one to whichever detector's
// numbers happen to be smallest.
func weightedLess(x, y scoredAlert) bool {
	if x.score != y.score {
		return x.score > y.score
	}
	if x.al.TSeconds != y.al.TSeconds {
		return x.al.TSeconds < y.al.TSeconds
	}
	if x.al.Entity != y.al.Entity {
		return x.al.Entity < y.al.Entity
	}
	if x.al.SrcComp != y.al.SrcComp {
		return x.al.SrcComp < y.al.SrcComp
	}
	return x.al.DstComp < y.al.DstComp
}

// scoreDay scores every arm's retained alerts for one day and returns them deduplicated,
// best first. An alert several detectors raised keeps the highest score any of them gave it.
func (w *weightedArm) scoreDay(byArm map[detector.ID]*dayAlerts, ids []detector.ID,
	depth int) []scoredAlert {
	best := make(map[string]*scoredAlert)
	order := make([]string, 0, depth*len(ids))

	for _, id := range ids {
		tail, ok := w.tails[id]
		if !ok {
			continue
		}
		weight := w.weights[id]
		da, ok := byArm[id]
		if !ok || da == nil {
			continue
		}
		n := depth
		if n > len(da.alerts) {
			n = len(da.alerts)
		}
		for _, al := range da.alerts[:n] {
			s := weight.LogLikelihoodRatio(tail.LogQuantile(al.LogP))
			k := alertIdentity(al)
			cur, seen := best[k]
			if !seen {
				best[k] = &scoredAlert{al: al, arm: id, score: s}
				order = append(order, k)
				continue
			}
			if s > cur.score {
				cur.score, cur.arm, cur.al = s, id, al
			}
		}
	}

	out := make([]scoredAlert, 0, len(order))
	for _, k := range order {
		out = append(out, *best[k])
	}
	sort.Slice(out, func(i, j int) bool { return weightedLess(out[i], out[j]) })
	return out
}

// weightedResults measures the arm at every budget, beside the arms it is being compared
// against rather than instead of them.
func (a *accumulator) weightedResults(budgets []int) map[string]any {
	w := a.fitWeightedArm()
	if w == nil {
		return map[string]any{
			"measured": false,
			"reason": "no labelled event falls before the burn-in boundary, so no weight " +
				"can be fitted on data disjoint from the scoring window. Reported as " +
				"unmeasured rather than as an arm whose weights are all uninformative",
		}
	}

	ids := a.unionArmIDs(false)
	entityIDs := a.unionArmIDs(true)
	return map[string]any{
		"measured": true,
		"score": "ln a + (a - 1) ln q, the log-likelihood ratio of an alert being labelled " +
			"against its being background, for q the alert's tail quantile under its own " +
			"detector's frozen burn-in null and a that detector's fitted weight",
		"fitting_window": "burn-in only: the labelled events before the boundary, disjoint " +
			"from those after it. The partition is asserted in application's tests",
		"streamable": "every quantity is a property of the alert or of state frozen at the " +
			"boundary, so the same score thresholds a stream; no rank among the day's " +
			"events is read anywhere",
		"weights":           w.weightRecord(),
		"all_arms":          a.weightedGrouping(w, ids, budgets),
		"entity_scope_arms": a.weightedGrouping(w, entityIDs, budgets),
	}
}

// weightRecord states every fitted weight and the evidence behind it, so a reader can see
// which detectors earned a share and on how much.
func (w *weightedArm) weightRecord() map[string]any {
	out := make(map[string]any, len(w.weights)+len(w.skipped))
	for id, weight := range w.weights {
		r := w.reports[id]
		tail := w.tails[id]
		out[string(id)] = map[string]any{
			"a":                  weight.A(),
			"informative":        weight.IsInformative(),
			"observed":           r.Observed,
			"censored":           r.Censored,
			"deviance":           r.Deviance,
			"significant":        r.Significant,
			"clamped":            r.Clamped,
			"tail_observations":  tail.Observations(),
			"tail_evaluations":   tail.Evaluations(),
			"tail_threshold":     tail.Threshold(),
			"tail_scale":         tail.Scale(),
			"deviance_threshold": allocation.DevianceThreshold,
		}
	}
	for id, reason := range w.skipped {
		out[string(id)] = map[string]any{"fitted": false, "reason": reason}
	}
	return out
}

// weightedGrouping measures one grouping of detectors at every budget.
func (a *accumulator) weightedGrouping(w *weightedArm, ids []detector.ID,
	budgets []int) map[string]any {
	days := a.unionArmDays(ids)
	redTeam := a.unionRedTeamScored(ids)

	out := make(map[string]any, len(budgets))
	for _, b := range budgets {
		counts := newUnionCounts()
		for _, d := range days {
			scored := w.scoreDay(a.armsOnDay(ids, d), ids, b)
			n := b
			if n > len(scored) {
				n = len(scored)
			}
			day := make([]fusedAlert, 0, n)
			for _, s := range scored[:n] {
				day = append(day, fusedAlert{al: s.al, rank: 1,
					carriers: []detector.ID{s.arm}})
			}
			counts.tally(day, b)
		}
		out[fmt.Sprintf("budget_%d_per_day", b)] = counts.report(redTeam, b*len(days))
	}
	return map[string]any{
		"arms":            armNames(ids),
		"red_team_scored": redTeam,
		"at_equal_cost":   out,
	}
}
