// Package objective states what an alerting configuration is *for*, as a quantity that
// can be maximised.
//
// # Why a ratio is not enough
//
// The natural first statement of the goal is "maximise TP/FP, and do not accept TP = 0".
// It does not work, and the reason is worth stating because the failure is not obvious.
//
// TP/FP is precision in disguise: with P = TP/(TP+FP), the ratio is P/(1−P), which is
// strictly increasing in P. Maximising one is exactly maximising the other, so the ratio
// contributes no information a precision figure did not already carry — and it inherits
// precision's degeneracy. The maximiser of precision is the smallest queue that contains a
// true positive, and forbidding TP = 0 does not prevent that: it moves the corner from
// "alert on nothing" to "alert on one thing". Measured on `lanl-conformal-d7-9-001`, the
// top-ranked entity-day of one day is a genuine red-team account, so TP/FP is maximised at
// one alert per day — a ratio of 1.0, two alerts, and 1 of 46 campaign-days found.
//
// The defect is structural rather than a matter of tuning. Any objective that is a
// function of precision alone is scale-free, and a scale-free quantity cannot say how many
// rows to show. An objective must therefore contain a term that grows with the number of
// true positives found.
//
// # The objective
//
//	U = v·TP − c·FP
//
// v is what one true positive is worth and c what one false positive costs. Only their
// ratio is identifiable from counts, so [Utility] takes one number — v in units of c — and
// the score is in units of one false positive.
//
// This has three properties the ratio lacks. It rewards true positives found, so a large
// queue that finds many can beat a small clean one. It is defined and finite everywhere,
// including at TP = 0 and FP = 0. And its parameter is a quantity an operator can actually
// estimate — how many benign rows a caught incident is worth triaging — rather than a taste
// parameter.
//
// The operator's intuition is not discarded, it is located: U > 0 exactly when
// TP/FP > c/v, so a target true-to-false ratio *is* the exchange rate, and
// [Outcome.BreakEvenValueRatio] reports it. That quantity takes no parameter at all, which
// is why it is what this package reports by default: recording it commits nobody to a cost
// model, and a threshold chosen after seeing a result is not a threshold.
//
// # Corpus-agnostic
//
// Nothing here names a corpus, a detector, a field, or an entity. It is arithmetic over
// four integers, so the same objective grades a run on any corpus and any unit of
// analysis — event, entity, entity-day. Deliberately: the unit is the caller's to choose
// and R6 is about entities while the recorded runs are about events.
package objective

import (
	"errors"
	"fmt"
	"math"
)

// Outcome is the labelled result of one alerting configuration: what it alerted on, and
// how much of the population it could have found.
//
// A value object, validated on construction, compared by value. It holds counts and not
// rates, because every rate below is derived and a stored rate could disagree with its own
// numerator.
type Outcome struct {
	truePositives  int
	falsePositives int
	labelled       int
}

// NewOutcome returns the outcome of alerting on truePositives + falsePositives items, out
// of a population containing labelled positives.
//
// Alerting on nothing is valid and returns a usable zero outcome: it is the configuration
// every objective here must be able to reject on its merits, so refusing to represent it
// would put the comparison out of reach.
func NewOutcome(truePositives, falsePositives, labelled int) (Outcome, error) {
	switch {
	case truePositives < 0:
		return Outcome{}, fmt.Errorf("objective: true positives %d is negative", truePositives)
	case falsePositives < 0:
		return Outcome{}, fmt.Errorf("objective: false positives %d is negative", falsePositives)
	case labelled < 0:
		return Outcome{}, fmt.Errorf("objective: labelled %d is negative", labelled)
	case truePositives > labelled:
		// Not a defensive check but a real defect detector: a label credited twice, or a
		// labelled population counted at a different unit from the alerts, both surface
		// here rather than as a recall above one in a report.
		return Outcome{}, fmt.Errorf(
			"objective: %d true positives exceeds the %d labelled positives in the population",
			truePositives, labelled)
	}
	return Outcome{truePositives: truePositives, falsePositives: falsePositives, labelled: labelled}, nil
}

// TruePositives is the number of alerts that were genuinely positive.
func (o Outcome) TruePositives() int { return o.truePositives }

// FalsePositives is the number of alerts that were not.
func (o Outcome) FalsePositives() int { return o.falsePositives }

// Labelled is the number of positives the population contains, the denominator of recall.
func (o Outcome) Labelled() int { return o.labelled }

// Alerted is the size of the queue an analyst was handed.
func (o Outcome) Alerted() int { return o.truePositives + o.falsePositives }

// Precision is the share of the queue that was genuinely positive, and 0 for an empty
// queue. Zero rather than undefined because precision is only ever read here beside a
// recall and a count, where a nil would have to be special-cased by every consumer to
// mean the same thing.
func (o Outcome) Precision() float64 {
	if o.Alerted() == 0 {
		return 0
	}
	return float64(o.truePositives) / float64(o.Alerted())
}

// Recall is the share of the population's positives the queue found, and 0 when the
// population holds none.
func (o Outcome) Recall() float64 {
	if o.labelled == 0 {
		return 0
	}
	return float64(o.truePositives) / float64(o.labelled)
}

// Ratio is TP/FP, and reports false when there are no false positives.
//
// It declines rather than returning an infinity because that is the honest answer: the
// ratio is unbounded, and a perfect queue is precisely where it stops being a number a
// consumer can compare or serialise. Prefer [Outcome.Precision], which is bounded and
// carries the same ordering.
func (o Outcome) Ratio() (float64, bool) {
	if o.falsePositives == 0 {
		return 0, false
	}
	return float64(o.truePositives) / float64(o.falsePositives), true
}

// Lift is precision over the population's base rate: how much better than picking at
// random the queue is. It is 0 for a non-positive base rate.
//
// Precision alone cannot say whether a ranking is doing any work — 12.5% is excellent
// against a base rate of 2.6% and worthless against one of 50% — which is why this is
// reported beside it.
func (o Outcome) Lift(baseRate float64) float64 {
	if baseRate <= 0 || math.IsNaN(baseRate) || math.IsInf(baseRate, 0) {
		return 0
	}
	return o.Precision() / baseRate
}

// BreakEvenValueRatio is the exchange rate at which this operating point starts to pay:
// the value a true positive must carry, in units of the cost of a false positive, for
// U to be positive. It is FP/TP, and reports false when nothing true was found.
//
// This is the quantity to record by default, because it takes no parameter. It converts
// the question "is this queue worth reading" into one an operator can answer from their
// own operation — "is catching one of these worth triaging this many benign rows" — without
// anybody having fixed a cost model in advance, and without a constant chosen after seeing
// the result.
func (o Outcome) BreakEvenValueRatio() (float64, bool) {
	if o.truePositives == 0 {
		return 0, false
	}
	return float64(o.falsePositives) / float64(o.truePositives), true
}

// Utility is the objective: U = v·TP − c·FP, parameterised by v/c.
type Utility struct {
	// valueRatio is v/c, what one true positive is worth in units of the cost of one
	// false positive.
	valueRatio float64
}

// ErrValueRatio reports a value ratio that is not a positive finite number.
var ErrValueRatio = errors.New("objective: the value ratio must be a positive finite number")

// NewUtility returns the objective for an operator who considers one true positive worth
// valueRatio false positives.
//
// There is deliberately no default. The ratio is an operational fact — it belongs to the
// operator's cost of triage and their cost of a miss — and a default supplied here would
// be a constant chosen by the implementation to make its own results read well.
func NewUtility(valueRatio float64) (Utility, error) {
	if math.IsNaN(valueRatio) || math.IsInf(valueRatio, 0) || valueRatio <= 0 {
		return Utility{}, fmt.Errorf("%w, got %v", ErrValueRatio, valueRatio)
	}
	return Utility{valueRatio: valueRatio}, nil
}

// ValueRatio is v/c, as supplied. Recorded in a run's provenance: the objective's value
// is meaningless without it.
func (u Utility) ValueRatio() float64 { return u.valueRatio }

// Score is U in units of the cost of one false positive: v·TP − FP.
//
// An empty queue scores exactly 0, which is the reference every other configuration is
// measured against — a negative score means the queue costs more to read than it is worth.
func (u Utility) Score(o Outcome) float64 {
	return u.valueRatio*float64(o.TruePositives()) - float64(o.FalsePositives())
}

// IsWorthwhile reports whether the queue is worth handing to an analyst at all: whether it
// beats alerting on nothing. Equivalently, whether TP/FP exceeds c/v.
//
// Strictly greater than zero, so a queue that exactly breaks even is not worthwhile. A
// wash is not a reason to spend an analyst's attention.
func (u Utility) IsWorthwhile(o Outcome) bool { return u.Score(o) > 0 }

// MinimumPrecision is the same threshold in the units a dashboard reads: a queue pays iff
// its precision exceeds 1/(1+v).
//
// It is the decision boundary a calibrated per-item probability would be thresholded at,
// which is where this objective would select the queue directly rather than choosing among
// budgets. That requires a calibrated posterior, which is a separate and unfinished piece
// of work; until then the selection is over the budgets a caller offers to [Utility.Best].
func (u Utility) MinimumPrecision() float64 { return 1 / (1 + u.valueRatio) }

// Best returns the index of the utility-maximising outcome, and false when there are no
// candidates.
//
// Ties go to the earliest index. Callers pass candidates in ascending budget order, so the
// earliest is the smallest queue: of two configurations an operator is indifferent to by
// the objective, the one that costs less attention wins. The rule is an index comparison
// and never a map traversal, so the answer is reproducible (R4).
//
// A set in which every candidate scores negative still returns its best member rather than
// declining, because "the least bad losing configuration" and "no configuration pays" are
// different findings and the caller must be able to report the second. Ask
// [Utility.IsWorthwhile] of the winner to tell them apart.
func (u Utility) Best(outcomes []Outcome) (int, bool) {
	if len(outcomes) == 0 {
		return 0, false
	}
	best, bestScore := 0, u.Score(outcomes[0])
	for i := 1; i < len(outcomes); i++ {
		if score := u.Score(outcomes[i]); score > bestScore {
			best, bestScore = i, score
		}
	}
	return best, true
}
