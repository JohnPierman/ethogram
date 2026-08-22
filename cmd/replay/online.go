package main

import (
	"fmt"
	"math"
	"sort"

	"github.com/JohnPierman/ethogram/domain/calibration"
	"github.com/JohnPierman/ethogram/domain/detector"
)

// Online error control across the replay (#16).
//
// # What this measures that the per-day step-up cannot
//
// The recorded false discovery rates come from Benjamini–Hochberg applied per corpus day,
// which needs the day's whole event count as its denominator. That is a correct measurement
// and not a deployable rule: an operator at 14:00 cannot use a threshold that depends on how
// many events arrive by 23:59. LORD++ needs only the prefix, so it is the same question asked
// in a form a live system could answer, and running both on the same stream is what makes the
// gap between them a number.
//
// # Why two streams and not eight
//
// #16's own instruction: run it on the single best calibrated detector, not the composite, and
// report the composite as the negative control. The reason is recorded in the framework
// already -- the composite's realised FDR is 1.0 at every nominal q, because its p-values are
// anti-conservative enough that a step-up rejects nearly everything and q has no purchase. An
// online rule inherits exactly that: it will spend its wealth on false rejections, earn it
// back, and spend again. That is worth measuring as a negative control and would be
// misleading as a headline.
//
// The cost also argues for two. A level is O(runs of rejections) to compute, so a stream that
// rejects in a scattered pattern costs the retention cap per event; two streams over four and
// a half million events is seconds, and eight is minutes for no additional finding.
//
// # The trajectory is the finding
//
// Not the totals. The claim under test is that a productive period buys a higher alerting rate
// and a barren one decays towards silence without reaching it, and that is a shape over time.
// So the record is per corpus day: tests, rejections, how many were labelled, the level and
// the wealth at the day's end.

// onlineMode is what the -online flag selects.
type onlineMode string

const (
	onlineNone    onlineMode = "none"
	onlineLORD    onlineMode = "lord"
	onlineSaffron onlineMode = "saffron"
)

// parseOnline resolves the flag.
//
// `saffron` is named in #16's sketch of the flag and is refused rather than aliased to LORD++.
// It is a different procedure -- it discards p-values above a candidacy threshold and counts
// candidates rather than tests -- so a run recorded as `saffron` that ran LORD++ would have a
// provenance block that lies. None of #16's acceptance criteria name it, and the negative
// control below is the reason to be sceptical that a more powerful spending rule is what this
// corpus needs: where the p-values are not calibrated, no spending rule recovers the level.
func parseOnline(s string) (onlineMode, error) {
	switch onlineMode(s) {
	case onlineNone:
		return onlineNone, nil
	case onlineLORD:
		return onlineLORD, nil
	case onlineSaffron:
		return "", fmt.Errorf("online rule %q is not implemented: it needs candidate and "+
			"discard accounting that LORD++ does not have, and aliasing it would record a "+
			"run whose provenance names a procedure it did not run", s)
	default:
		return "", fmt.Errorf("unknown online rule %q: want none or lord", s)
	}
}

// onlineDefaults are the rule's parameters.
//
// q = 0.1 matches the tightest nominal level the batch procedures are already recorded at, so
// the two are comparable. The starting wealth is q/20: it has to be at or below q, and small
// enough that the procedure earns its alerting rate rather than being handed it, which is the
// behaviour under test.
const (
	onlineQ        = 0.1
	onlineWealth   = onlineQ / 20
	onlineNegative = "composite"
)

// onlineControl runs one rule per watched stream and records the per-day trajectory.
type onlineControl struct {
	mode onlineMode
	// arm is the detector whose stream is the headline measurement; the composite is
	// always carried beside it as the negative control.
	arm   detector.ID
	rules map[string]*calibration.LORD
	days  map[string]map[int64]*onlineDay
}

// onlineDay is one stream's one corpus day.
type onlineDay struct {
	Tests         int64   `json:"tests"`
	Rejections    int64   `json:"rejections"`
	TruePositives int64   `json:"true_positives"`
	Labelled      int64   `json:"labelled_tests"`
	LevelAtEnd    float64 `json:"level_at_end"`
	WealthAtEnd   float64 `json:"wealth_at_end"`
	SpentAtEnd    float64 `json:"spent_at_end"`
}

func newOnlineControl(mode onlineMode, arm detector.ID) *onlineControl {
	return &onlineControl{
		mode:  mode,
		arm:   arm,
		rules: map[string]*calibration.LORD{},
		days:  map[string]map[int64]*onlineDay{},
	}
}

// on reports whether any online rule is running, so every call site is a single guard and the
// default path is untouched.
func (o *onlineControl) on() bool { return o != nil && o.mode == onlineLORD }

// watches reports whether a stream is one of the two being measured.
func (o *onlineControl) watches(stream string) bool {
	if !o.on() {
		return false
	}
	return stream == onlineNegative || stream == string(o.arm)
}

// observe puts one log p-value through a stream's rule and folds the outcome into the day.
//
// It returns nothing: the rule's decisions are a parallel measurement, never an input to the
// alert ranking, so no ordering here can change what the run reports about anything else.
func (o *onlineControl) observe(stream string, day int64, logP float64, isRed bool) {
	if !o.watches(stream) {
		return
	}
	rule, ok := o.rules[stream]
	if !ok {
		created, err := calibration.NewLORD(onlineWealth, onlineQ, calibration.DefaultGamma())
		if err != nil {
			// Unreachable: the constants above are checked by the constructor's own test.
			// A rule that cannot be built is recorded as an absent stream rather than
			// panicking a run that is otherwise fine.
			return
		}
		rule = created
		o.rules[stream] = rule
		o.days[stream] = map[int64]*onlineDay{}
	}

	rejected := rule.Test(logP)

	byDay := o.days[stream]
	d, ok := byDay[day]
	if !ok {
		d = &onlineDay{}
		byDay[day] = d
	}
	d.Tests++
	if isRed {
		d.Labelled++
	}
	if rejected {
		d.Rejections++
		if isRed {
			d.TruePositives++
		}
	}
	// Overwritten on every event, so what survives is the value at the day's last event.
	d.LevelAtEnd = rule.Level()
	d.WealthAtEnd = rule.Wealth()
	d.SpentAtEnd = rule.Spent()
}

// record is the block the result file carries.
func (o *onlineControl) record() map[string]any {
	if o == nil || o.mode == onlineNone {
		return map[string]any{
			"mode": string(onlineNone),
			"note": "no online rule was run. The recorded false discovery rates come from " +
				"the per-day step-up, which needs the day's whole event count and is " +
				"therefore a measurement rather than a deployable threshold",
		}
	}

	streams := make([]string, 0, len(o.rules))
	for name := range o.rules {
		streams = append(streams, name)
	}
	sort.Strings(streams)

	out := make(map[string]any, len(streams)+3)
	for _, name := range streams {
		rule := o.rules[name]
		byDay := o.days[name]

		dayKeys := make([]int64, 0, len(byDay))
		for d := range byDay {
			dayKeys = append(dayKeys, d)
		}
		sort.Slice(dayKeys, func(i, j int) bool { return dayKeys[i] < dayKeys[j] })

		trajectory := make([]map[string]any, 0, len(dayKeys))
		var rejections, truePositives, tests int64
		for _, d := range dayKeys {
			row := byDay[d]
			tests += row.Tests
			rejections += row.Rejections
			truePositives += row.TruePositives
			realised := 0.0
			if row.Rejections > 0 {
				realised = float64(row.Rejections-row.TruePositives) / float64(row.Rejections)
			}
			trajectory = append(trajectory, map[string]any{
				"day":            d,
				"tests":          row.Tests,
				"rejections":     row.Rejections,
				"true_positives": row.TruePositives,
				"labelled_tests": row.Labelled,
				"realised_fdr":   realised,
				"level_at_end":   row.LevelAtEnd,
				"wealth_at_end":  row.WealthAtEnd,
				"spent_at_end":   row.SpentAtEnd,
			})
		}

		realised := 0.0
		if rejections > 0 {
			realised = float64(rejections-truePositives) / float64(rejections)
		}
		entry := map[string]any{
			"rule":                rule.Describe(),
			"tests":               tests,
			"rejections":          rejections,
			"true_positives":      truePositives,
			"realised_fdr":        realised,
			"final_level":         rule.Level(),
			"final_wealth":        rule.Wealth(),
			"omitted_mass":        rule.OmittedMass(),
			"truncated_runs":      rule.TruncatedRuns(),
			"per_day_trajectory":  trajectory,
			"is_negative_control": name == onlineNegative,
		}
		if name == onlineNegative {
			entry["note"] = "the negative control. The composite's p-values are " +
				"anti-conservative enough that a step-up at any nominal q rejects nearly " +
				"everything, and an online rule inherits that exactly: it spends its " +
				"wealth on false rejections, earns it back, and spends again. A realised " +
				"rate far above q here is the expected reading and is a statement about " +
				"the calibration of the input, not about the spending rule"
		}
		out[name] = entry
	}

	return map[string]any{
		"mode":     string(o.mode),
		"q":        onlineQ,
		"wealth":   onlineWealth,
		"streams":  out,
		"headline": string(o.arm),
		"note": "LORD++ needs only the prefix of the stream, where the per-day step-up " +
			"needs the day's whole event count -- so this is the same error-control " +
			"question asked in a form a live system could answer. The per-day trajectory " +
			"is the finding rather than the totals: the claim under test is that a " +
			"productive period buys a higher alerting rate and a barren one decays " +
			"towards silence without reaching it, and that is a shape over time",
		"wealth_note": "wealth is unbounded above under a stream that rejects everything, " +
			"which the negative control does: each rejection earns q while the level is " +
			"capped at q and approaches it from below, so earning exceeds spending by the " +
			"unspent tail of the spending sequence. Growth here means unspent budget, not " +
			"runaway alerting -- the level is what is bounded, and it never exceeds q",
	}
}

// levelFloorNote records the property the issue exists for, as a computed statement rather
// than a claim: after the run's longest barren stretch the level is still positive.
func (o *onlineControl) neverSilent() map[string]any {
	if !o.on() {
		return nil
	}
	out := map[string]any{}
	for name, rule := range o.rules {
		level := rule.Level()
		out[name] = map[string]any{
			"final_level":       level,
			"final_log_level":   rule.LogLevel(),
			"strictly_positive": level > 0 && !math.IsInf(rule.LogLevel(), -1),
		}
	}
	out["note"] = "the level after the last event of the run. A fixed-q batch rule that " +
		"stops rejecting has no mechanism to start again; this one decays without reaching " +
		"zero, so a barren stretch lowers the alerting rate rather than ending it"
	return out
}
