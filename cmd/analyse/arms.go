package main

import (
	"fmt"
	"sort"

	"github.com/JohnPierman/ethogram/domain/objective"
	"github.com/JohnPierman/ethogram/domain/statistics"
)

// Per-arm detection with intervals.
//
// A detection count on its own is not a measurement anybody can act on. Eleven labelled
// events in seventy alerts and sixty in seven hundred are both "the novelty detector at a
// tight budget", and whether the first is evidence of a better operating point or of a
// smaller sample is exactly what an interval answers. The primary detection table already
// carries Wilson intervals, but only for the arm the run was analysed against; every
// per-detector arm was reported as a bare integer.
//
// Both proportions here are binomial at a matched budget, which is what makes an interval
// the right summary: recall is true positives out of the labelled events the arm evaluated,
// precision is true positives out of the alerts it was allowed. Wilson rather than the
// normal approximation because these counts are small and sit near zero, where the normal
// interval leaves [0, 1] and loses coverage.
//
// # What the interval does not cover
//
// Only sampling error in the counts, under the assumption that alerts are independent
// draws. On the real campaign they are not: 549 labelled events fall on 104 accounts and
// two days carry most of them, so the effective sample size is nearer the account or the
// campaign-day than the event, and these intervals are correspondingly optimistic. On the
// planted corpus the same is true by construction, with eight victims per attack type and
// a deterministic choice of planted values. The interval is a lower bound on the
// uncertainty, and reporting it is better than reporting none as long as it is read that
// way.

// armDetection is one arm at one budget, with both proportions and their intervals.
type armDetection struct {
	Arm            string              `json:"arm"`
	Group          string              `json:"group"`
	Budget         int                 `json:"budget_per_day"`
	Alerts         int                 `json:"alerts"`
	TruePositives  int                 `json:"true_positives"`
	FalsePositives int                 `json:"false_positives"`
	RedTeamScored  int                 `json:"red_team_scored"`
	ScoredEvents   int                 `json:"scored_events"`
	Recall         statistics.Interval `json:"recall_wilson"`
	Precision      statistics.Interval `json:"precision_wilson"`

	// FalseAlarmRate is false positives over events scored: the per-event rate α that
	// appears in the base-rate identity
	//
	//	P(intrusion | alarm) = r·π / (r·π + (1 − π)·α)
	//
	// for recall r and base rate π. It is recorded because it is the only quantity on
	// which this framework is comparable to a published detector at all. Precision and
	// recall are properties of a corpus and a budget; α is a property of the method, and
	// it is what the base-rate argument constrains.
	//
	// Note what the identity implies and the figure it feeds makes explicit: a curve of α
	// against attainable precision assumes r = 1. This framework's operating points are
	// far off that curve, because r is 0.02 to 0.37 rather than 1 — so a low α here buys
	// less precision than a perfect-recall detector at the same α would get. Reading the
	// two as comparable is the error the recall column exists to prevent.
	FalseAlarmRate float64 `json:"false_alarm_rate"`
}

// armIntervals builds the per-arm detection table from a replay result's recorded counts.
//
// It reads counts rather than per-day alert sets, so it covers the arms a paired test
// cannot: the per-detector arms record their detections at each budget but not the alert
// sets those detections came from, and an interval on a binomial proportion needs only the
// two integers.
func armIntervals(results map[string]any, budgets objective.Budgets, scored int) []armDetection {
	out := []armDetection{}

	arms, _ := results["detector_arms"].(map[string]any)
	if inner, ok := arms["arms"].(map[string]any); ok {
		names := make([]string, 0, len(inner))
		for name := range inner {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			block, _ := inner[name].(map[string]any)
			out = append(out, armRows(name, armGroup(name), block, budgets, scored)...)
		}
	}

	// The two p-value combinations and the union groupings, so the comparison a reader
	// wants -- a combination against its own best component -- is one table lookup.
	if block, ok := results["min_p_arm"].(map[string]any); ok {
		out = append(out, armRows("corrected minimum", "combination", block, budgets, scored)...)
	}
	if at, ok := results["detections_at_budget"].(map[string]any); ok {
		out = append(out, armRows("composite", "combination",
			map[string]any{"detections_at_budget": at}, budgets, scored)...)
	}
	if union, ok := results["union_arm"].(map[string]any); ok {
		for _, g := range []struct{ key, label string }{
			{"entity_scope_arms", "union, per-entity arms"},
			{"all_arms", "union, all arms"},
		} {
			grp, ok := union[g.key].(map[string]any)
			if !ok {
				continue
			}
			for _, acct := range []struct{ key, label string }{
				{"at_equal_cost", " (equal cost)"},
				{"at_equal_depth", " (equal depth)"},
			} {
				at, ok := grp[acct.key].(map[string]any)
				if !ok {
					continue
				}
				out = append(out, armRows(g.label+acct.label, "combination",
					map[string]any{"detections_at_budget": at}, budgets, scored)...)
			}
		}
	}
	return out
}

// armRows renders one arm at every budget the run recorded it at.
func armRows(name, group string, block map[string]any, budgets objective.Budgets,
	scored int) []armDetection {
	at, ok := block["detections_at_budget"].(map[string]any)
	if !ok {
		return nil
	}
	scoped := intAt(block, "red_team_scored")
	rows := make([]armDetection, 0, len(budgets))
	for _, b := range budgets {
		cell, ok := at[fmt.Sprintf("budget_%d_per_day", b)].(map[string]any)
		if !ok {
			// The arm was not measured at this budget. Absent rather than zero: the two
			// are different statements and only one of them is a measurement.
			continue
		}
		alerts, tp := intAt(cell, "alerts"), intAt(cell, "true_positives")
		denom := scoped
		if denom == 0 {
			denom = intAt(cell, "red_team_total")
		}
		row := armDetection{
			Arm: name, Group: group, Budget: b, Alerts: alerts, TruePositives: tp,
			FalsePositives: alerts - tp, RedTeamScored: denom, ScoredEvents: scored,
			Recall:    statistics.WilsonInterval(tp, denom),
			Precision: statistics.WilsonInterval(tp, alerts),
		}
		if scored > 0 {
			row.FalseAlarmRate = float64(alerts-tp) / float64(scored)
		}
		rows = append(rows, row)
	}
	return rows
}

// armGroup separates the two scopes, which is the paper's central distinction and so is
// carried on the row rather than left to a reader who knows the detector names.
func armGroup(name string) string {
	switch name {
	case "marginal", "cooccurrence":
		return "population detector"
	default:
		return "per-entity detector"
	}
}

func intAt(m map[string]any, key string) int {
	if v, ok := m[key].(float64); ok {
		return int(v)
	}
	return 0
}

// intFromCorpus reads a count from a run's corpus block. Zero when absent, which the
// callers treat as "not derivable" rather than as a measured zero.
func intFromCorpus(run map[string]any, key string) int {
	corpus, ok := run["corpus"].(map[string]any)
	if !ok {
		return 0
	}
	return intAt(corpus, key)
}
