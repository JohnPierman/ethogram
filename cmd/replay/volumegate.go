package main

import (
	"fmt"
	"math"
	"sort"

	"github.com/JohnPierman/ethogram/application"
	"github.com/JohnPierman/ethogram/domain/objective"
	"github.com/JohnPierman/ethogram/domain/volume"
)

// The instrument for #25.
//
// volume never abstains, so it reports the prior's tail on an entity's first period as
// though it were the entity's own: 13,618 events below 1e-12 where a calibrated null
// predicts 4.5e-06, no labelled event below 1.96e-07, and a realised cut pinned at
// 1.12e-12 at every budget. The fix is an abstention gated on completed periods, and the
// threshold has to be chosen from a measurement rather than from taste.
//
// Measuring it by running one replay per candidate would cost two hours a candidate. This
// measures every candidate from a single ungated pass instead, by retaining enough of the
// joint distribution of (completed periods, p) to answer each candidate exactly:
//
//   - per day and per period bucket, the smallest topK p-values. A candidate's cut at
//     budget B reads the B smallest p-values among the buckets it admits, and B never
//     exceeds topK, so retaining topK per bucket is sufficient and the cut is EXACT
//     rather than binned;
//   - per bucket, the totals the other three questions need: events, events in the
//     sub-1e-12 pile, and the labelled events with their p-values, kept individually
//     because there are few of them and their cost is the point.
//
// Run it with -volume-min-periods 0. With the gate armed the probe still reports, but it
// can only measure candidates at or above the armed threshold, because the events below it
// no longer carry a p-value.
// miscalibratedP is the edge of the pile #25 is about. A calibrated null over 4.49M events
// puts 4.5e-06 events below it; volume put 13,618 there.
const miscalibratedP = 1e-12

// volumeGateCandidates are the thresholds #25 nominates, with zero — the pre-#25
// behaviour — measured beside them so the table states what the change is against.
var volumeGateCandidates = []int64{0, 1, 2, 3, 5}

// volumeGateBuckets is one bucket per candidate-relevant period count, the last being
// "that many or more". Its length must cover the largest candidate.
const volumeGateBuckets = 6

func volumeGateBucket(periods int64) int {
	if periods < 0 {
		return 0
	}
	if periods >= volumeGateBuckets-1 {
		return volumeGateBuckets - 1
	}
	return int(periods)
}

// volumeGateBucketStats is one (day, period bucket) cell.
type volumeGateBucketStats struct {
	events       int64
	subThreshold int64
	// smallest holds the cell's most extreme p-values in ascending order, truncated to
	// topK, which is what a per-day budget is drawn from.
	smallest []float64
	labelled []float64
}

func (b *volumeGateBucketStats) push(p float64, k int) {
	if len(b.smallest) >= k && p >= b.smallest[len(b.smallest)-1] {
		return
	}
	i := sort.SearchFloat64s(b.smallest, p)
	b.smallest = append(b.smallest, 0)
	copy(b.smallest[i+1:], b.smallest[i:])
	b.smallest[i] = p
	if len(b.smallest) > k {
		b.smallest = b.smallest[:k]
	}
}

type volumeGateProbe struct {
	topK int
	// cells is day -> bucket -> stats.
	cells map[int64]*[volumeGateBuckets]volumeGateBucketStats
	// armed records the threshold the run itself applied, so a reader can tell which
	// candidates this probe was able to measure.
	armed int64
	// noPValue counts volume verdicts the live gate had already abstained on.
	noPValue int64
	seen     int64
}

func newVolumeGateProbe(topK int, armed int64) *volumeGateProbe {
	return &volumeGateProbe{
		topK:  topK,
		cells: make(map[int64]*[volumeGateBuckets]volumeGateBucketStats),
		armed: armed,
	}
}

// observe records the event's volume verdict. A detector may emit several verdicts for one
// event; the most extreme is what any ranking of that detector would use, which is the
// same rule observeDetectorArms applies.
func (v *volumeGateProbe) observe(se application.ScoredEvent, day int64, isRed bool) {
	best, periods, have := math.Inf(1), int64(0), false
	abstained := false
	for _, vd := range se.Verdicts {
		if vd.DetectorID() != volume.DetectorID {
			continue
		}
		p, ok := vd.PValue()
		if !ok {
			abstained = true
			continue
		}
		if !have || p < best {
			best, have = p, ok
			periods = int64(vd.Evidence().Stats["completed_periods"])
		}
	}
	if !have {
		if abstained {
			v.seen++
			v.noPValue++
		}
		return
	}
	v.seen++

	cell, ok := v.cells[day]
	if !ok {
		cell = &[volumeGateBuckets]volumeGateBucketStats{}
		v.cells[day] = cell
	}
	b := &cell[volumeGateBucket(periods)]
	b.events++
	if best <= miscalibratedP {
		b.subThreshold++
	}
	b.push(best, v.topK)
	if isRed {
		b.labelled = append(b.labelled, best)
	}
}

// volumeGateCut is what one budget does to one candidate. The budget is PER DAY, so a
// labelled event competes only against its own day: a single scalar cut across the whole
// window would credit an event at 1.96e-07 with clearing a cut set by the loosest day,
// when on its own day the cut was 1e-12 and it was nowhere near. Every quantity here is
// therefore accumulated per day and only then summarised.
//
// labelledClearing is the check on this whole instrument: at minPeriods 0 it must equal
// the volume arm's own true_positives at the same budget, because it is the same
// selection computed a second way.
type volumeGateCut struct {
	cutMax    float64
	cutMedian float64
	// alerts is what the arm would emit over the window: at most budget per scored day.
	alerts           int64
	labelledClearing int64
	days             int
	daysOffFloor     int
}

// cutAt reports, per day, that day's least extreme admitted alert within the budget — the
// loosest p-value that still earned a slot — and counts the labelled events that reach
// their OWN day's cut.
func (v *volumeGateProbe) cutAt(minPeriods int64, budget int) (volumeGateCut, bool) {
	first := volumeGateBucket(minPeriods)
	var out volumeGateCut
	cuts := make([]float64, 0, len(v.cells))

	for _, cell := range v.cells {
		merged := make([]float64, 0, budget)
		for b := first; b < volumeGateBuckets; b++ {
			merged = append(merged, cell[b].smallest...)
		}
		sort.Float64s(merged)
		if len(merged) > budget {
			merged = merged[:budget]
		}
		if len(merged) == 0 {
			continue
		}
		dayCut := merged[len(merged)-1]
		cuts = append(cuts, dayCut)
		out.alerts += int64(len(merged))
		out.days++
		if dayCut > miscalibratedP {
			out.daysOffFloor++
		}
		for b := first; b < volumeGateBuckets; b++ {
			for _, lp := range cell[b].labelled {
				if lp <= dayCut {
					out.labelledClearing++
				}
			}
		}
	}
	if out.days == 0 {
		return out, false
	}
	sort.Float64s(cuts)
	out.cutMax = cuts[len(cuts)-1]
	out.cutMedian = cuts[len(cuts)/2]
	return out, true
}

// totals sums the cells a candidate removes and the cells it keeps.
func (v *volumeGateProbe) totals(minPeriods int64) (removedEvents, removedSub, removedLabelled, keptSub, keptLabelled int64) {
	cut := volumeGateBucket(minPeriods)
	for _, cell := range v.cells {
		for b := range volumeGateBuckets {
			c := &cell[b]
			if b < cut {
				removedEvents += c.events
				removedSub += c.subThreshold
				removedLabelled += int64(len(c.labelled))
				continue
			}
			keptSub += c.subThreshold
			keptLabelled += int64(len(c.labelled))
		}
	}
	return removedEvents, removedSub, removedLabelled, keptSub, keptLabelled
}

func (v *volumeGateProbe) results(budgets objective.Budgets) map[string]any {
	if v.seen == 0 {
		return map[string]any{"measured": false, "reason": "no volume verdicts observed"}
	}

	var evaluated, subTotal, labelledTotal int64
	for _, cell := range v.cells {
		for b := range volumeGateBuckets {
			evaluated += cell[b].events
			subTotal += cell[b].subThreshold
			labelledTotal += int64(len(cell[b].labelled))
		}
	}

	rows := make([]map[string]any, 0, len(volumeGateCandidates))
	for _, cand := range volumeGateCandidates {
		if cand < v.armed {
			rows = append(rows, map[string]any{
				"min_periods": cand,
				"measurable":  false,
				"reason":      "below the threshold this run armed; those events carry no p-value",
			})
			continue
		}
		removedEvents, removedSub, removedLabelled, keptSub, keptLabelled := v.totals(cand)

		cuts := make(map[string]any, len(budgets))
		for _, b := range budgets {
			c, ok := v.cutAt(cand, b)
			if !ok {
				continue
			}
			cuts[fmt.Sprintf("budget_%d_per_day", b)] = map[string]any{
				"realised_cut_worst_day":   c.cutMax,
				"realised_cut_median_day":  c.cutMedian,
				"alerts":                   c.alerts,
				"scored_days":              c.days,
				"days_off_the_1e-12_floor": c.daysOffFloor,
				"off_the_1e-12_floor":      c.daysOffFloor == c.days,
				"labelled_clearing_cut":    c.labelledClearing,
			}
		}

		share := float64(removedEvents+v.noPValue) / float64(v.seen)
		rows = append(rows, map[string]any{
			"min_periods":         cand,
			"measurable":          true,
			"abstained_events":    removedEvents + v.noPValue,
			"abstained_share":     share,
			"sub_1e-12_removed":   removedSub,
			"sub_1e-12_remaining": keptSub,
			"labelled_removed":    removedLabelled,
			"labelled_remaining":  keptLabelled,
			"at_budget":           cuts,
		})
	}

	return map[string]any{
		"measured":            true,
		"definition":          "one ungated pass, every candidate derived from the retained joint distribution of (completed periods, p). A candidate's realised cut is exact, not binned: each day contributes its most extreme `budget` admitted alerts and the cut is the least extreme p-value in that pooled queue. abstained_share counts events the candidate would withhold plus any the run's own gate already withheld, over all events volume saw. off_the_1e-12_floor is true only where EVERY scored day's cut clears the floor, because the budget is per day. At min_periods 0, labelled_clearing_cut must equal the volume arm's own true_positives at the same budget: it is the same selection computed a second way, and a disagreement means this instrument is wrong rather than the arm.",
		"threshold_unit":      "completed periods, not events: the Gamma posterior of equation (10) is over completed periods and a partial first period is the degenerate case the gate exists to exclude",
		"armed_min_periods":   v.armed,
		"volume_events":       v.seen,
		"evaluated":           evaluated,
		"already_abstained":   v.noPValue,
		"miscalibrated_below": miscalibratedP,
		"sub_1e-12_ungated":   subTotal,
		"labelled_evaluated":  labelledTotal,
		"candidates":          rows,
	}
}

// volumeGateResults is nil-safe, so a run constructed without the probe still writes a
// result rather than panicking.
func (a *accumulator) volumeGateResults(budgets objective.Budgets) map[string]any {
	if a.volGate == nil {
		return map[string]any{"measured": false, "reason": "probe not constructed"}
	}
	return a.volGate.results(budgets)
}

// volumeRecord states the volume arm's abstention threshold and what it is for.
func volumeRecord(cfg runConfig) map[string]any {
	if cfg.volMinPeriods <= 0 {
		return map[string]any{
			"min_periods": 0,
			"abstains":    false,
			"note": "the arm forms an opinion on an entity's first period, reporting the " +
				"prior's tail as though it were the entity's. Retained only for the " +
				"diagnostic that measures candidate thresholds; see #25 and the " +
				"volume_gate_probe block of this result",
		}
	}
	return map[string]any{
		"min_periods": cfg.volMinPeriods,
		"abstains":    true,
		"unit": "completed periods, not events: the Gamma posterior of equation (10) is " +
			"over completed periods, so a partial first period is the degenerate case",
		"status": "abstained_unusable",
		"note": "R3: below this many completed periods the arm has no basis for an " +
			"opinion and says so. Chosen by the measurement in " +
			"results/volume-abstention-gate.json",
	}
}
