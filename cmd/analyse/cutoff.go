package main

import (
	"sort"

	"github.com/JohnPierman/ethogram/domain/objective"
)

// cutoffRow is one budget's utility-optimal alert count beside the budget's own.
//
// It answers a question a budget cannot: given that a budget of 1,000 a day permits 7,000
// alerts over a week, and that the corpus holds only a few hundred labelled events, how
// many of those 7,000 are worth an analyst's attention? A budget is a ceiling on what may
// be emitted, not an instruction to emit it.
type cutoffRow struct {
	Budget int `json:"budget_per_day"`

	// AlertsAtBudget and TruePositivesAtBudget are what emitting the whole budget yields.
	AlertsAtBudget        int `json:"alerts_at_budget"`
	TruePositivesAtBudget int `json:"true_positives_at_budget"`

	// OptimalAlerts is the prefix of that queue maximising U, and may be zero: if no
	// prefix beats an empty queue then the finding is that at this exchange rate the
	// detector is not worth deploying, and the report has to be able to say so.
	OptimalAlerts          int `json:"optimal_alerts"`
	TruePositivesAtOptimal int `json:"true_positives_at_optimal"`

	ObjectiveAtBudget  float64 `json:"objective_at_budget"`
	ObjectiveAtOptimal float64 `json:"objective_at_optimal"`

	// PrecisionAtOptimal is the share of the truncated queue that is genuinely positive.
	PrecisionAtOptimal float64 `json:"precision_at_optimal"`

	// IsWorthwhileAtOptimal reports whether the optimum beats emitting nothing at all.
	IsWorthwhileAtOptimal bool `json:"is_worthwhile_at_optimal"`

	// AlertsSuppressed is the alerts the budget permitted and the objective declined,
	// which is the operator-facing number: work not done for no loss of detection worth
	// having.
	AlertsSuppressed int `json:"alerts_suppressed"`

	// TruePositivesForgone is what suppressing them cost. Reported beside the saving
	// rather than left to be inferred, because a cutoff that quietly drops detections is
	// the failure mode here.
	TruePositivesForgone int `json:"true_positives_forgone"`
}

// cutoffAt computes the utility-optimal truncation of the queue a per-day budget emits.
//
// The alerts a budget emits are pooled across the window and ranked against each other
// rather than truncated per day, because an analyst's attention is not partitioned by
// date: given a fixed quantity of it, the most extreme alerts of the week deserve it
// wherever they fall.
//
// # This is an upper bound, not a deployable rule
//
// The optimum is located using the labels, so it states what a perfect cutoff would have
// achieved on this corpus. It is a measurement of the headroom a cutoff has, not a rule
// that can be shipped: a deployable version needs a per-alert probability of being a true
// positive, and producing one is the calibration work §10.1 describes. Reporting the
// oracle first is the honest order — there is no point building the rule until the headroom
// is known to be worth having.
func cutoffAt(a arm, pop redTeamPopulation, budget int, u objective.Utility) (cutoffRow, bool) {
	labelled := pop.size()

	// The queue the budget would emit: each day's most extreme `budget` alerts.
	pooled := make([]alertRow, 0, budget*len(a.perDay))
	days := make([]int64, 0, len(a.perDay))
	for day := range a.perDay {
		days = append(days, day)
	}
	sort.Slice(days, func(i, j int) bool { return days[i] < days[j] })
	for _, day := range days {
		rows := a.perDay[day]
		if len(rows) > budget {
			rows = rows[:budget]
		}
		pooled = append(pooled, rows...)
	}
	if len(pooled) == 0 {
		return cutoffRow{}, false
	}

	// One canonical order for the pooled queue, so the optimum is reproducible whatever
	// order the day map yielded (R4).
	sort.SliceStable(pooled, func(i, j int) bool {
		if pooled[i].LogP != pooled[j].LogP {
			return pooled[i].LogP < pooled[j].LogP
		}
		if pooled[i].TSeconds != pooled[j].TSeconds {
			return pooled[i].TSeconds < pooled[j].TSeconds
		}
		return pooled[i].Entity < pooled[j].Entity
	})

	// Candidate operating points, longest-prefix last. Index 0 is the empty queue, which
	// is the reference every truncation is measured against.
	outcomes := make([]objective.Outcome, 0, len(pooled)+1)
	empty, err := objective.NewOutcome(0, 0, labelled)
	if err != nil {
		return cutoffRow{}, false
	}
	outcomes = append(outcomes, empty)

	tp := 0
	for i, row := range pooled {
		if row.IsRedTeam {
			tp++
		}
		o, oErr := objective.NewOutcome(tp, i+1-tp, labelled)
		if oErr != nil {
			// More true positives than the population holds: the labelled set and the
			// alert flags disagree, which is a defect in the run rather than a condition
			// to report per budget.
			return cutoffRow{}, false
		}
		outcomes = append(outcomes, o)
	}

	best, ok := u.Best(outcomes)
	if !ok {
		return cutoffRow{}, false
	}
	full := outcomes[len(outcomes)-1]
	optimal := outcomes[best]

	return cutoffRow{
		Budget:                 budget,
		AlertsAtBudget:         full.Alerted(),
		TruePositivesAtBudget:  full.TruePositives(),
		OptimalAlerts:          optimal.Alerted(),
		TruePositivesAtOptimal: optimal.TruePositives(),
		ObjectiveAtBudget:      u.Score(full),
		ObjectiveAtOptimal:     u.Score(optimal),
		PrecisionAtOptimal:     optimal.Precision(),
		IsWorthwhileAtOptimal:  u.IsWorthwhile(optimal),
		AlertsSuppressed:       full.Alerted() - optimal.Alerted(),
		TruePositivesForgone:   full.TruePositives() - optimal.TruePositives(),
	}, true
}

// cutoffTable computes the truncation for every budget of every arm that recorded an alert
// list, or nil when no exchange rate was supplied. Without one there is no objective to
// maximise and therefore no cutoff: the question "how many of these are worth reading" has
// no answer until somebody says what reading one is worth.
//
// Only arms carrying `alerts_per_day` can be cut off, because a cutoff needs the false
// positives interleaved with the true ones. The per-detector arms record detection counts
// without the alert lists behind them, so they cannot be truncated from a recorded run;
// that gap is noted in the output rather than left to be discovered.
func cutoffTable(arms []namedArm, pop redTeamPopulation, budgets objective.Budgets,
	u *objective.Utility) []armCutoff {
	if u == nil {
		return nil
	}
	out := make([]armCutoff, 0, len(arms))
	for _, na := range arms {
		rows := make([]cutoffRow, 0, len(budgets))
		for _, b := range budgets {
			if row, ok := cutoffAt(na.arm, pop, b, *u); ok {
				rows = append(rows, row)
			}
		}
		if len(rows) > 0 {
			out = append(out, armCutoff{Arm: na.name, Combination: na.combination, Budgets: rows})
		}
	}
	return out
}

// namedArm pairs an arm with how its scores were combined, so the cutoff table records
// which construction each row belongs to.
type namedArm struct {
	name        string
	combination string
	arm         arm
}

// armCutoff is one arm's cutoffs across every budget.
type armCutoff struct {
	Arm         string      `json:"arm"`
	Combination string      `json:"combination,omitempty"`
	Budgets     []cutoffRow `json:"budgets"`
}
