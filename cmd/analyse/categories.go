package main

import (
	"fmt"
	"sort"

	"github.com/JohnPierman/ethogram/domain/statistics"
)

// This file reports detection separately for each structural category of anomaly, so
// that the framework's advantage can be attributed to a kind of anomaly rather than
// asserted in aggregate.
//
// The categories are assigned by the replay from each event's own evidence, and are
// properties of the event relative to the history it was scored against — whether this
// entity had ever taken this value, whether the pair had ever co-occurred, whether the
// entity's own fitted density puts this hour below chance. They are deliberately NOT
// assigned by which detector produced the smallest p-value: a partition drawn along our
// own detectors' firing would be a partition chosen in our favour, and every
// per-category margin computed on it would be circular. Under the structural
// definition, both arms are measured over the same subset of labelled events, so the
// per-category comparison is a paired comparison on a partition neither arm chose.
//
// The categories map onto the limitations §3 establishes, which is the point: each row
// of the table is one of the paper's arguments, measured.

// categoryMeta carries the description that travels with each category into the
// results, so a reader of the JSON needs no second document to interpret the row.
type categoryMeta struct {
	ID string `json:"id"`
	// Test is the structural predicate, stated so it can be checked against the code.
	Test string `json:"structural_test"`
	// Section is the whitepaper section that motivates the category.
	Section string `json:"whitepaper_section"`
	// Contrast is why a marginal, batch-standardised outlier detector of the
	// isolation-forest family cannot express this category. For the population-rare
	// category it records instead that this is exactly what such a detector does well,
	// because that category is the control the others are read against.
	Contrast string `json:"contrast_with_marginal_detectors"`
}

// categoryOrder fixes the reporting order and must match the replay's allCategories.
func categoryOrder() []categoryMeta {
	return []categoryMeta{
		{
			ID:      "population_rare",
			Test:    "the observed value holds less than one part in a thousand of its field's observed mass in the population",
			Section: "§9",
			Contrast: "this is the category isolation-based detectors answer well, and it is " +
				"retained as the control: it is the part of the problem the standard " +
				"formulation already solves, so any advantage the framework shows " +
				"elsewhere can be read against a category where it should show none",
		},
		{
			ID:      "novel_value",
			Test:    "the entity has history for the field but has never taken this value",
			Section: "§3.1, §6",
			Contrast: "a detector fitted to a pooled feature cloud holds no per-entity state, " +
				"so the proposition 'this entity has not previously exhibited this value' " +
				"is not expressible in it; a value common in the population is " +
				"unremarkable to it however new it is to the account that produced it",
		},
		{
			ID:      "off_hours",
			Test:    "the entity's own fitted circular density at this time of day is below the uniform level 1/2π",
			Section: "§3.1, §7.1, §7.2",
			Contrast: "an hour-of-day feature is scored against the population's working " +
				"pattern rather than the entity's, and a rectangular encoding cuts the " +
				"circle at midnight, so an entity whose ordinary hours straddle midnight " +
				"is scored as two disjoint populations rather than one (§7.1)",
		},
		{
			ID:      "volume_burst",
			Test:    "the entity's observed count in the window exceeds its own predicted rate",
			Section: "§3.1, §7.4",
			Contrast: "a rate is only anomalous relative to the entity's own history; a " +
				"population-fitted model reads a quiet account's tenfold increase as " +
				"ordinary whenever busier accounts sustain that rate routinely",
		},
		{
			ID:      "novel_pair",
			Test:    "two eligible values have never co-occurred, in a graph that carries mass",
			Section: "§3.3, §8",
			Contrast: "each value is individually frequent, so every marginal is satisfied and " +
				"the combination is invisible to any detector that scores fields " +
				"independently; only a joint model has the question in its vocabulary",
		},
	}
}

// redTeamPopulation is every labelled event the run scored, with the structural
// categories each exhibits.
//
// This is the correct denominator for recall. Deriving it from the retained alert lists
// instead would count only labelled events that reached the top-K of their day, quietly
// dropping the ones scored unremarkably — which is precisely the failure a recall figure
// is supposed to report, so a denominator that excludes them overstates the result.
type redTeamPopulation struct {
	// keys is every labelled event scored, in deterministic order.
	keys []eventKey
	// categories maps an event to the categories it exhibits.
	categories map[eventKey][]string
	// logP maps an event to the combined log p-value it was scored at, whether or not
	// it was alerted. It is what the gap table measures against the alert cut.
	logP map[eventKey]float64
	// source describes where the population came from, for the record.
	source string
}

func (p redTeamPopulation) size() int { return len(p.keys) }

// inCategory returns the labelled events exhibiting a category, preserving order.
func (p redTeamPopulation) inCategory(id string) []eventKey {
	out := make([]eventKey, 0, len(p.keys))
	for _, k := range p.keys {
		for _, c := range p.categories[k] {
			if c == id {
				out = append(out, k)
				break
			}
		}
	}
	return out
}

// loadRedTeamPopulation reads the complete labelled population from the run.
//
// It falls back to the population implied by the retained alerts when the run predates
// the full list, and says so in source, because a denominator that changed silently
// between runs would make two results incomparable without anything on the page saying
// why.
func loadRedTeamPopulation(results map[string]any, fallback arm) redTeamPopulation {
	raw, ok := results["red_team_scored"].([]any)
	if !ok || len(raw) == 0 {
		keys := fallback.redTeamScored()
		return redTeamPopulation{
			keys:       keys,
			categories: map[eventKey][]string{},
			source: "derived from the retained per-day alert lists; the run recorded no " +
				"complete red_team_scored list, so labelled events scored outside the " +
				"retained top-K are absent from the denominator and the recall reported " +
				"here is an upper bound",
		}
	}

	pop := redTeamPopulation{
		categories: map[eventKey][]string{},
		logP:       map[eventKey]float64{},
		source: "the run's complete red_team_scored list: every labelled event that " +
			"received a combined p-value, whether or not it was alerted",
	}
	seen := map[eventKey]bool{}
	for _, v := range raw {
		row, ok := v.(map[string]any)
		if !ok {
			continue
		}
		t, _ := row["t"].(float64)
		entity, _ := row["entity"].(string)
		k := eventKey{int64(t), entity}
		if seen[k] {
			continue
		}
		seen[k] = true
		pop.keys = append(pop.keys, k)

		// log_p is what the gap table measures; a run predating it simply has no gap.
		if logP, ok := row["log_p"].(float64); ok {
			pop.logP[k] = logP
		}

		if cats, ok := row["categories"].([]any); ok {
			list := make([]string, 0, len(cats))
			for _, cv := range cats {
				if s, ok := cv.(string); ok {
					list = append(list, s)
				}
			}
			sort.Strings(list)
			pop.categories[k] = list
		}
	}
	sort.Slice(pop.keys, func(i, j int) bool {
		if pop.keys[i].t != pop.keys[j].t {
			return pop.keys[i].t < pop.keys[j].t
		}
		return pop.keys[i].entity < pop.keys[j].entity
	})
	return pop
}

// categoryRow is one category's comparison at one budget against one baseline.
type categoryRow struct {
	Category categoryMeta `json:"category"`
	Budget   int          `json:"budget_per_day"`

	// RedTeamEvents is the number of labelled events exhibiting this category within
	// the window both arms scored: the denominator both arms are measured against.
	RedTeamEvents int `json:"red_team_events_in_category"`

	// CommonDays is the window the comparison is confined to.
	CommonDays []int64 `json:"common_days"`

	FrameworkDetected int                 `json:"framework_detected"`
	FrameworkRecall   statistics.Interval `json:"framework_recall"`

	BaselineName     string              `json:"baseline"`
	BaselineDetected int                 `json:"baseline_detected"`
	BaselineRecall   statistics.Interval `json:"baseline_recall"`

	DeltaPoints    float64  `json:"delta_percentage_points"`
	TimesBetter    *float64 `json:"times_better,omitempty"`
	RatioUndefined string   `json:"ratio_undefined,omitempty"`

	McNemar   statistics.McNemarResult  `json:"mcnemar"`
	Bootstrap statistics.BootstrapDelta `json:"bootstrap_delta"`

	// Unmeasurable records why a row carries no comparison, when it carries none.
	Unmeasurable string `json:"unmeasurable,omitempty"`
}

// categoryComparison builds the per-category table for every baseline and budget.
func categoryComparison(primary arm, pop redTeamPopulation, arms []baselineArm,
	budgets []int, bootstraps int, seed uint64,
) []categoryRow {
	out := make([]categoryRow, 0, len(categoryOrder())*len(arms)*len(budgets))
	frameworkDays := primary.coveredDays()

	for _, meta := range categoryOrder() {
		for _, b := range budgets {
			frameworkHit := primary.redTeamDetectedAt(b)

			for _, ba := range arms {
				// Confined to the window both arms scored, for the same reason the
				// aggregate comparison is: on every event counted here, both arms had
				// the opportunity to alert.
				restricted, commonDays := restrictToCommonDays(pop, frameworkDays, ba.days)
				events := restricted.inCategory(meta.ID)

				row := categoryRow{
					Category:      meta,
					Budget:        b,
					RedTeamEvents: len(events),
					BaselineName:  ba.name,
					CommonDays:    commonDays,
				}
				if len(events) == 0 {
					row.Unmeasurable = "no labelled event in this run exhibits this category, " +
						"so neither arm can be credited or faulted for it"
					out = append(out, row)
					continue
				}

				baselineHit, named := ba.detectionsFor(b)
				if !named {
					row.Unmeasurable = fmt.Sprintf(
						"%s recorded its detections at budget %d as a count without naming "+
							"the events, so they cannot be attributed to a category",
						ba.name, b)
					out = append(out, row)
					continue
				}

				fa := make([]bool, len(events))
				bb := make([]bool, len(events))
				for i, k := range events {
					fa[i] = frameworkHit[k]
					bb[i] = baselineHit[k]
					if fa[i] {
						row.FrameworkDetected++
					}
					if bb[i] {
						row.BaselineDetected++
					}
				}

				n := len(events)
				row.FrameworkRecall = statistics.WilsonInterval(row.FrameworkDetected, n)
				row.BaselineRecall = statistics.WilsonInterval(row.BaselineDetected, n)
				row.DeltaPoints = 100 * (row.FrameworkRecall.Point - row.BaselineRecall.Point)
				row.McNemar = statistics.McNemar(fa, bb)
				row.Bootstrap = statistics.PairedBootstrapDelta(fa, bb, bootstraps, seed)
				if row.BaselineDetected > 0 {
					ratio := float64(row.FrameworkDetected) / float64(row.BaselineDetected)
					row.TimesBetter = &ratio
				} else {
					row.RatioUndefined = "the baseline detected nothing in this category, so a " +
						"relative improvement is a division by zero; the difference is " +
						"reported in percentage points of recall"
				}
				out = append(out, row)
			}
		}
	}
	return out
}

// categoryCensus counts how many labelled events fall in each category, which is what a
// reader needs to judge how much weight any one row carries.
func categoryCensus(pop redTeamPopulation) []map[string]any {
	out := make([]map[string]any, 0, len(categoryOrder()))
	uncategorised := 0
	for _, k := range pop.keys {
		if len(pop.categories[k]) == 0 {
			uncategorised++
		}
	}
	for _, meta := range categoryOrder() {
		n := len(pop.inCategory(meta.ID))
		share := 0.0
		if pop.size() > 0 {
			share = float64(n) / float64(pop.size())
		}
		out = append(out, map[string]any{
			"category":               meta,
			"red_team_events":        n,
			"share_of_labelled":      share,
			"labelled_total":         pop.size(),
			"labelled_uncategorised": uncategorised,
		})
	}
	return out
}
