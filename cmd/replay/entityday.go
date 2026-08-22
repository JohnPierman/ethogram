package main

import (
	"fmt"
	"math"
	"sort"

	"github.com/JohnPierman/ethogram/domain/calibration"
)

// Higher Criticism at entity scope, the third aggregation of issue #17.
//
// The two already here both have a scale set by traffic rather than by evidence. Fisher's
// sum grows linearly in the event count, so the busiest accounts sort to the top by
// arithmetic -- 71 of 172 real entity-days against 1 of 8 planted ones. Standardising it
// against chi-square(2n) removes the mean but leaves a statistic whose reference
// distribution assumes independence that one entity's own stream does not have.
//
// Higher Criticism is normalised by construction and is built for the alternative a campaign
// actually presents: a sparse cluster of moderate anomalies inside an otherwise ordinary day.
// The domain package carries the statistic and the reasoning; this file is the adapter
// between an entity-day's retained tail and it.

// higherCriticismOf computes the statistic for one entity-day from its retained tail.
//
// Failures are recorded on the row rather than returned, because none of them is a fault in
// the run: an entity-day with no retained tail is a state of the data, and a result file that
// dropped such rows would be reporting a population it had quietly filtered.
func higherCriticismOf(ed *entityDay) entityDayHigherCriticism {
	out := entityDayHigherCriticism{
		LogStatistic: math.Inf(-1),
		NullScale:    calibration.NullScale(int(ed.Events)),
	}
	if len(ed.Tail) == 0 || ed.Events <= 0 {
		out.Error = "no retained tail, so there are no order statistics to take a maximum over"
		return out
	}

	hc, err := calibration.HigherCriticism(ed.Tail, int(ed.Events), calibration.DefaultAlpha0)
	if err != nil {
		out.Error = err.Error()
		return out
	}

	out.LogStatistic = hc.LogStatistic
	out.Positive = hc.Positive
	out.Rank = hc.Rank
	out.PValueLog = hc.PValueLog
	out.Considered = hc.Considered
	out.Truncated = hc.Truncated
	// JSON carries no infinity, so an overflowed statistic is reported as absent rather
	// than as a number it is not. LogStatistic is the field that survived it and is what
	// the ranking used.
	if !math.IsInf(hc.Statistic, 0) && !math.IsNaN(hc.Statistic) {
		value := hc.Statistic
		out.Statistic = &value
	}
	return out
}

// result rebuilds the domain value from the flattened row, so the ranking and the recorded
// numbers cannot drift apart: there is one comparison, in the domain, and this is how the
// result file's own fields reach it.
func (h entityDayHigherCriticism) result() calibration.HigherCriticismResult {
	statistic := math.Inf(1)
	if h.Statistic != nil {
		statistic = *h.Statistic
	}
	return calibration.HigherCriticismResult{
		LogStatistic: h.LogStatistic,
		Statistic:    statistic,
		Positive:     h.Positive,
		Rank:         h.Rank,
		PValueLog:    h.PValueLog,
		N:            0,
		Considered:   h.Considered,
		Truncated:    h.Truncated,
	}
}

// entityDaysFromEventBudget reports which entity-days the EVENT-level ranking already
// surfaces, at each event budget.
//
// Issue #17 asks for this beside the entity-day rankings, and the reason is that the claim
// under test is not "Higher Criticism beats Fisher's sum" -- it is that the entity-day is a
// better unit than the event. That claim is only meaningful against what ranking events
// already achieves, and the two are not comparable budget-for-budget: 100 alerts a day is
// 100 events under one and 100 accounts under the other, and 100 events can name as few as
// one account.
//
// So this reports both numbers. The alerts figure is how many distinct entity-days the
// event budget touched, which is the entity-level coverage an operator actually gets from
// the event-level system; the true positives are how many of those carried a labelled event.
// A reader can then see whether an entity-day ranking is finding accounts the event ranking
// misses, or the same accounts by a different route.
func (a *accumulator) entityDaysFromEventBudget(budgets []int, labelled int) map[string]any {
	days := make([]int64, 0, len(a.perDay))
	for d := range a.perDay {
		days = append(days, d)
	}
	sort.Slice(days, func(i, j int) bool { return days[i] < days[j] })

	out := make(map[string]any, len(budgets)+1)
	for _, b := range budgets {
		touched := map[entityDayKey]bool{}
		withLabel := map[entityDayKey]bool{}
		events := 0
		for _, d := range days {
			alerts := a.perDay[d].alerts
			take := b
			if take > len(alerts) {
				take = len(alerts)
			}
			events += take
			for _, al := range alerts[:take] {
				key := entityDayKey{entity: al.Entity, day: d}
				touched[key] = true
				if al.IsRedTeam {
					withLabel[key] = true
				}
			}
		}
		out[fmt.Sprintf("budget_%d_per_day", b)] = map[string]any{
			"events_alerted":       events,
			"entity_days_touched":  len(touched),
			"true_positives":       len(withLabel),
			"labelled_entity_days": labelled,
		}
	}
	out["note"] = "the entity-days the composite's event-level ranking already reaches, so " +
		"an entity-day ranking is compared against what it has to beat rather than against " +
		"nothing. The two budgets are not the same unit: an event budget of 100 buys 100 " +
		"events, which may name as few as one account, and this is how many accounts it " +
		"actually named"
	return out
}
