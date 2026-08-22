package objective

import (
	"errors"
	"fmt"
	"math"
	"sort"
)

// ErrCostRatio reports a pair of costs that cannot define an operating point.
var ErrCostRatio = errors.New("objective: both costs must be positive finite numbers")

// ErrBaseRate reports a base rate that is not a probability strictly inside (0, 1).
var ErrBaseRate = errors.New("objective: the base rate must lie strictly between 0 and 1")

// CostRatio is what a missed intrusion costs against what a wasted review costs.
//
// Only the ratio is identifiable from counts, so the two numbers carry whatever unit the
// operator likes and nothing here interprets them. A value object, validated on construction.
type CostRatio struct {
	missCost   float64
	reviewCost float64
}

// NewCostRatio returns the ratio of a missed intrusion's cost to a wasted review's.
//
// Both must be positive: a zero review cost says an operator can be shown unlimited false
// alarms for free, which makes "alert on everything" optimal and the whole exercise vacuous,
// and a zero miss cost says the opposite. Neither is a cost model worth deriving a threshold
// from, so both are refused rather than clamped.
func NewCostRatio(missCost, reviewCost float64) (CostRatio, error) {
	for name, v := range map[string]float64{"miss": missCost, "review": reviewCost} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
			return CostRatio{}, fmt.Errorf("%w: the %s cost is %v", ErrCostRatio, name, v)
		}
	}
	return CostRatio{missCost: missCost, reviewCost: reviewCost}, nil
}

// MissCost is what one missed intrusion costs.
func (c CostRatio) MissCost() float64 { return c.missCost }

// ReviewCost is what one wasted investigation costs.
func (c CostRatio) ReviewCost() float64 { return c.reviewCost }

// Ratio is the miss cost in units of the review cost, which is the one number an operator
// has to supply and the same quantity [Utility] takes as its value ratio.
func (c CostRatio) Ratio() float64 { return c.missCost / c.reviewCost }

// PosteriorThreshold is the posterior probability above which an alert is worth raising:
//
//	tau = c_review / (c_review + c_miss)
//
// Alerting is worth it when the expected cost of not alerting, P·c_miss, exceeds the expected
// cost of alerting, (1−P)·c_review, and rearranging gives the threshold above. It is the
// decision-theoretic content of the whole cost model: everything else here is arithmetic
// carrying it onto a per-event scale.
//
// At equal costs it is 0.5, which is the point every accuracy-like objective silently assumes
// — one missed breach priced at exactly one wasted click. [AccuracyEquivalent] names it.
func (c CostRatio) PosteriorThreshold() float64 {
	return c.reviewCost / (c.reviewCost + c.missCost)
}

// AccuracyEquivalent is the cost ratio that maximising accuracy silently assumes: one to one.
//
// Its posterior threshold is 0.5. Recorded as a named value because it is the comparison the
// paper's rejection of scalar accuracy-like objectives turns on — those objectives are not
// free of a cost model, they carry this one without saying so.
func AccuracyEquivalent() CostRatio { return CostRatio{missCost: 1, reviewCost: 1} }

// OperatingPoint is one cost ratio carried onto the per-event scale a detector is thresholded
// on, at a stated base rate. Every field is reported so that an analyst can recompute the
// arithmetic by hand, per R5.
type OperatingPoint struct {
	Ratio              CostRatio
	BaseRate           float64
	PosteriorThreshold float64

	// Alpha is the per-event false-alarm rate at which the share of alerts that are real
	// equals PosteriorThreshold, from P(intrusion | alarm) = p / (p + (1−p)·alpha).
	Alpha float64

	// AlphaClamped is true where the arithmetic asked for alpha > 1. That means "alert on
	// everything and you still do not reach this precision", which is a finding about the
	// base rate rather than a configuration, so it is reported rather than applied silently.
	AlphaClamped bool

	// EventsPerDay is the exposure the expected counts below are computed over. Zero leaves
	// them zero: a rate is meaningful without it, a count is not.
	EventsPerDay float64

	ExpectedFalsePerDay float64
	ExpectedTruePerDay  float64
}

// ExpectedAlertsPerDay is what an operator would be shown at this operating point.
func (o OperatingPoint) ExpectedAlertsPerDay() float64 {
	return o.ExpectedFalsePerDay + o.ExpectedTruePerDay
}

// Threshold returns the operating point a cost ratio implies at a base rate, over an exposure
// of eventsPerDay. Pass zero for the exposure to get the rates without the counts.
//
// The identity is Bayes' rule at the threshold, and it is Axelsson's base-rate argument read
// backwards: instead of asking what precision a given alpha buys, it asks what alpha a
// required precision demands. Solving
//
//	p / (p + (1−p)·alpha) = tau   for alpha   gives   alpha = p(1−tau) / (tau(1−p)).
func Threshold(c CostRatio, baseRate, eventsPerDay float64) (OperatingPoint, error) {
	if math.IsNaN(baseRate) || baseRate <= 0 || baseRate >= 1 {
		return OperatingPoint{}, fmt.Errorf("%w, got %v", ErrBaseRate, baseRate)
	}
	if math.IsNaN(eventsPerDay) || math.IsInf(eventsPerDay, 0) || eventsPerDay < 0 {
		return OperatingPoint{}, fmt.Errorf(
			"objective: events per day is %v, want a non-negative finite number", eventsPerDay)
	}

	tau := c.PosteriorThreshold()
	alpha := baseRate * (1 - tau) / (tau * (1 - baseRate))

	clamped := false
	if alpha > 1 {
		alpha, clamped = 1, true
	}

	o := OperatingPoint{
		Ratio: c, BaseRate: baseRate, PosteriorThreshold: tau,
		Alpha: alpha, AlphaClamped: clamped, EventsPerDay: eventsPerDay,
	}
	if eventsPerDay > 0 {
		o.ExpectedFalsePerDay = alpha * (1 - baseRate) * eventsPerDay
		o.ExpectedTruePerDay = baseRate * eventsPerDay
	}
	return o, nil
}

// CostCurve returns the operating point of every cost ratio given, at one base rate and
// exposure, ordered by the precision each demands so the curve reads monotonically.
//
// This is the Drummond and Holte view: a method's performance across the *range* of cost
// ratios rather than at one guessed point. A single chosen ratio is an assumption an operator
// must defend; the curve is what shows how much the choice matters, and on a corpus at this
// base rate it matters a great deal — the alpha demanded by 90% precision and by 5% differ by
// more than two orders of magnitude.
//
// An empty or all-invalid input is an error rather than an empty curve: a curve with no points
// would be rendered as an axis with nothing on it, which reads as a measurement.
func CostCurve(ratios []CostRatio, baseRate, eventsPerDay float64) ([]OperatingPoint, error) {
	if len(ratios) == 0 {
		return nil, fmt.Errorf("objective: a cost curve needs at least one ratio")
	}
	out := make([]OperatingPoint, 0, len(ratios))
	for _, r := range ratios {
		o, err := Threshold(r, baseRate, eventsPerDay)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].PosteriorThreshold > out[j].PosteriorThreshold
	})
	return out, nil
}

// RatioForPrecision returns the cost ratio whose posterior threshold is the given precision,
// which is how a curve is specified by the share of alerts an operator will tolerate being
// wrong rather than by a cost nobody can price.
//
// With the review cost fixed at 1, tau = 1/(1+miss) inverts to miss = (1−tau)/tau.
func RatioForPrecision(precision float64) (CostRatio, error) {
	if math.IsNaN(precision) || precision <= 0 || precision >= 1 {
		return CostRatio{}, fmt.Errorf(
			"objective: precision %v must lie strictly between 0 and 1", precision)
	}
	return NewCostRatio((1-precision)/precision, 1)
}
