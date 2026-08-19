package main

import "sort"

// secondsPerDay converts a corpus timestamp to the day the run buckets alerts by.
const secondsPerDay = int64(86400)

// The gap table: how far the composite is from its first true positive.
//
// Detection at a budget is a count, and while that count is zero it is the same number
// whatever happens to the scores underneath it. A calibration change that moved every
// labelled event from a thousand log-units outside the alert cut to five outside it
// would be reported, by recall alone, as no change at all. That is not a tolerable
// instrument for work whose whole current activity is repairing calibration.
//
// The gap is the distance in log-units between the most extreme labelled event of a day
// and the alert that sits at the budget's cut on that day. It is positive when the
// labelled event is the less extreme of the two — that is, when it is missed — so a
// repair shows up as the gap falling toward zero, and a regression shows up as it
// rising, long before either crosses into a changed detection count.

// gapCut is the distance from one day's best labelled event to one budget's cut.
type gapCut struct {
	Budget int `json:"budget_per_day"`
	// CutLogP is the log p-value of the alert at the budget's cut, nil when the day
	// retained fewer alerts than the budget and there is therefore no cut to clear.
	CutLogP *float64 `json:"cut_log_p"`
	// Gap is bestLabelled − cut, in log-units. Positive means missed by that much.
	Gap *float64 `json:"gap_log_units"`
}

// gapRow is one scored day.
type gapRow struct {
	Day             int64    `json:"day"`
	LabelledScored  int      `json:"labelled_scored"`
	AlertsRetained  int      `json:"alerts_retained"`
	BestLabelledLog *float64 `json:"best_labelled_log_p"`
	Cuts            []gapCut `json:"cuts"`
}

// gapDistribution counts labelled events by how far they sit from a budget's cut,
// pooled across days. The point of the bucketing is to separate the labelled events a
// calibration repair could plausibly reach from the ones it could not: an event two
// hundred log-units outside the cut is not a near miss, it is an event that looks
// exactly like ordinary traffic because it is ordinary traffic.
type gapDistribution struct {
	Budget           int `json:"budget_per_day"`
	Inside           int `json:"inside_the_cut"`
	WithinTen        int `json:"within_10_log_units"`
	WithinFifty      int `json:"within_50_log_units"`
	WithinTwoHundred int `json:"within_200_log_units"`
	Beyond           int `json:"beyond_200_log_units"`
	Unmeasurable     int `json:"unmeasurable"`
}

// gapAnalysis is the whole table, plus the sign convention stated where it is read.
type gapAnalysis struct {
	Note         string            `json:"note"`
	PerDay       []gapRow          `json:"per_day"`
	Distribution []gapDistribution `json:"distribution"`
}

// gapTable computes the per-day gaps and the pooled distribution.
//
// Both the labelled log p-values and the alert log p-values are read as recorded; no
// score is recomputed here, because a number that did not come out of the run does not
// belong in the analysis of it.
func gapTable(a arm, pop redTeamPopulation, budgets []int) gapAnalysis {
	labelledByDay := map[int64][]float64{}
	for _, k := range pop.keys {
		logP, ok := pop.logP[k]
		if !ok {
			continue
		}
		labelledByDay[k.t/secondsPerDay] = append(labelledByDay[k.t/secondsPerDay], logP)
	}

	days := make([]int64, 0, len(a.perDay))
	for day := range a.perDay {
		days = append(days, day)
	}
	sort.Slice(days, func(i, j int) bool { return days[i] < days[j] })

	analysis := gapAnalysis{
		Note: "gap = best labelled log p − the cut's log p, in log-units. Positive means " +
			"the labelled event is LESS extreme than the cut and is therefore missed; the " +
			"figure is how far the composite would have to move it to publish it. Null " +
			"where the day scored no labelled event, or retained fewer alerts than the budget.",
		PerDay: make([]gapRow, 0, len(days)),
	}

	for _, day := range days {
		rows := a.perDay[day]
		row := gapRow{
			Day:            day,
			LabelledScored: len(labelledByDay[day]),
			AlertsRetained: len(rows),
			Cuts:           make([]gapCut, 0, len(budgets)),
		}
		if best, ok := minOf(labelledByDay[day]); ok {
			row.BestLabelledLog = &best
		}
		for _, b := range budgets {
			cut := gapCut{Budget: b}
			if len(rows) >= b && b > 0 {
				// perDay is ordered most extreme first, as the run retained it.
				c := rows[b-1].LogP
				cut.CutLogP = &c
				if row.BestLabelledLog != nil {
					g := *row.BestLabelledLog - c
					cut.Gap = &g
				}
			}
			row.Cuts = append(row.Cuts, cut)
		}
		analysis.PerDay = append(analysis.PerDay, row)
	}

	analysis.Distribution = gapDistributions(a, pop, budgets)
	return analysis
}

// gapDistributions buckets every labelled event by its distance from its own day's cut.
func gapDistributions(a arm, pop redTeamPopulation, budgets []int) []gapDistribution {
	out := make([]gapDistribution, 0, len(budgets))
	for _, b := range budgets {
		d := gapDistribution{Budget: b}
		for _, k := range pop.keys {
			logP, ok := pop.logP[k]
			if !ok {
				d.Unmeasurable++
				continue
			}
			rows := a.perDay[k.t/secondsPerDay]
			if len(rows) < b || b <= 0 {
				d.Unmeasurable++
				continue
			}
			switch gap := logP - rows[b-1].LogP; {
			case gap <= 0:
				d.Inside++
			case gap <= 10:
				d.WithinTen++
			case gap <= 50:
				d.WithinFifty++
			case gap <= 200:
				d.WithinTwoHundred++
			default:
				d.Beyond++
			}
		}
		out = append(out, d)
	}
	return out
}

// minOf returns the smallest value, which for a log p-value is the most extreme.
func minOf(xs []float64) (float64, bool) {
	if len(xs) == 0 {
		return 0, false
	}
	best := xs[0]
	for _, x := range xs[1:] {
		if x < best {
			best = x
		}
	}
	return best, true
}
