package allocation

import (
	"fmt"
	"math"

	"github.com/JohnPierman/ethogram/domain/objective"
)

// OperatingPoint is the score above which an alert is worth raising, together with
// everything that went into it, so a reader can recompute the arithmetic by hand.
//
// A budget answers "how many alerts can be looked at". This answers a different question —
// "which alerts pay for themselves" — and the two are not interchangeable. A budget applied
// as a quota emits the quota on a quiet day, all of it benign; a threshold emits nothing on
// a quiet day and more than the quota during an incident. The budget's remaining job is as a
// capacity valve, and it is a finding when it binds rather than a number to truncate to.
//
// A value object, validated on construction, compared by value.
type OperatingPoint struct {
	// MinimumScore is the threshold on [Weight.LogLikelihoodRatio]. An alert scoring above
	// it has positive expected utility under the stated exchange rate.
	MinimumScore float64
	// BaseRate is the prior probability that an arbitrary event is one worth alerting on.
	BaseRate float64
	// ValueRatio is what a caught incident is worth in units of one wasted investigation.
	ValueRatio float64
	// MinimumPosterior is the posterior probability an alert must reach, which is the
	// exchange rate expressed the way a Bayes decision states it.
	MinimumPosterior float64
}

// Threshold derives the operating point for this score from a stated exchange rate and a
// measured base rate.
//
// The derivation is the Bayes decision rule and nothing more. Alert when the posterior
// probability of the alert being worth raising exceeds the ratio of costs,
//
//	P(worth raising | evidence) > c_review / (c_review + c_miss)
//
// which [objective.Utility.MinimumPrecision] already reports. Written in odds and taking
// logs, with s the score and π the base rate,
//
//	s > −ln(v/c) + ln((1 − π)/π)
//
// so a catch worth more lowers the threshold and a rarer event raises it, both by the
// amount the arithmetic says and not by an amount anybody chose.
//
// # What this is not
//
// Not a calibrated posterior. The score is a likelihood ratio under a Beta(a, 1) working
// model for how labelled events distribute over a detector's own null quantiles, fitted
// from tens of labelled events. That model is a convenience with the right shape — a single
// parameter, monotone, exact at a = 1 — and not a claim about the true density. So the
// threshold is the operating point this model implies at this exchange rate, and moving the
// exchange rate moves it in the right direction by roughly the right amount; it is not a
// guarantee that the realised precision will equal [OperatingPoint.MinimumPosterior]. The
// honest use is comparative — the whole cost curve rather than one point — and the reason to
// state the rate at all is that a stated assumption can be argued with, where a chosen
// alert count cannot.
func Threshold(u objective.Utility, baseRate float64) (OperatingPoint, error) {
	if math.IsNaN(baseRate) || baseRate <= 0 || baseRate >= 1 {
		return OperatingPoint{}, fmt.Errorf(
			"allocation: base rate %v is outside (0, 1); a prior of 0 or 1 admits no evidence",
			baseRate)
	}
	v := u.ValueRatio()
	if v <= 0 || math.IsInf(v, 0) || math.IsNaN(v) {
		return OperatingPoint{}, fmt.Errorf(
			"allocation: value ratio %v is not a finite positive number", v)
	}
	return OperatingPoint{
		MinimumScore:     -math.Log(v) + math.Log((1-baseRate)/baseRate),
		BaseRate:         baseRate,
		ValueRatio:       v,
		MinimumPosterior: u.MinimumPrecision(),
	}, nil
}

// Admits reports whether an alert of this score clears the operating point.
func (o OperatingPoint) Admits(score float64) bool { return score > o.MinimumScore }
