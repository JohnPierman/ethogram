package main

import (
	"math"
	"sort"
	"strconv"
)

// Realised error rate per combination rule (#55).
//
// # The question
//
// The composite's realised false discovery rate is 0.978 at every nominal q, and the rate does
// not move with q at all: a nominal level that changes nothing is not a control. Conformal
// calibration is applied to each detector's p-value in those runs, and it is a guarantee about
// each *marginal* — super-uniform by ranking alone, whatever that detector's null does — not
// about their combination.
//
// So the defect is somewhere between calibrated inputs and an uncalibrated output, and there are
// three candidates: Fisher's sum over dependent p-values is anti-conservative, Brown's correction
// is an approximation of that dependence, and the covariance is measured on burn-in and frozen.
//
// # What running the same procedure on the min-p arm localises
//
// The Šidák-corrected minimum consumes exactly the same calibrated inputs and combines them a
// different way, with a different dependence structure: a minimum over J tests corrected by
// 1 − (1 − p)^J rather than a sum of −2 ln p referred to χ². If its realised rate is near q, the
// inputs are fine and the defect is in Fisher's sum. If it is equally far off, the defect is
// upstream of the combination rule and no better sum will fix it.
//
// That is the cheap half of the diagnosis and it needs no new run: both arms record their own
// per-day alert sets, and the step-up is the same procedure applied to each.

// armCalibration is one combination rule's realised rate against the level it was asked for.
type armCalibration struct {
	Arm         string `json:"arm"`
	Combination string `json:"combination"`
	// Points are the step-up outcomes over the nominal grid.
	Points []calibrationPoint `json:"points"`
	// Flat records that the realised rate does not respond to the nominal level: the
	// spread across the whole q grid, which for a working control should be of the order
	// of the grid's own range.
	RealisedSpread float64 `json:"realised_spread"`
	Verdict        string  `json:"verdict"`
}

// calibrationByArm runs the same step-up on each recorded combination rule and reports the
// comparison, so "the composite is uncalibrated" becomes "the composite is uncalibrated and the
// minimum is too / is not".
func calibrationByArm(arms []namedArm, days []int64, perDayM map[int64]float64,
	topK int) map[string]any {

	rows := make([]armCalibration, 0, len(arms))
	for _, a := range arms {
		if len(a.arm.perDay) == 0 {
			continue
		}
		points := calibrate(a.arm, days, perDayM, topK, false)
		row := armCalibration{
			Arm:         a.name,
			Combination: a.combination,
			Points:      points,
		}

		low, high := math.Inf(1), math.Inf(-1)
		for _, p := range points {
			if p.RealisedFDR.Point < low {
				low = p.RealisedFDR.Point
			}
			if p.RealisedFDR.Point > high {
				high = p.RealisedFDR.Point
			}
		}
		if !math.IsInf(low, 0) && !math.IsInf(high, 0) {
			row.RealisedSpread = high - low
		}

		// The verdict is about whether the level has purchase, which is a different question
		// from whether the rate is low. A rule realising 0.98 at every q is not "slightly
		// miscalibrated": the knob is disconnected.
		switch {
		case len(points) == 0:
			row.Verdict = "no discoveries at any level, so the realised rate is undefined"
		case high > 0.5 && row.RealisedSpread < 0.05:
			row.Verdict = "the level has no purchase: the realised rate is far above every " +
				"nominal q and does not move with it, so the p-values the step-up is " +
				"applied to are not calibrated"
		case high > 0.5:
			row.Verdict = "the realised rate is far above the nominal level but does respond " +
				"to it, so the procedure is working on inputs that are anti-conservative " +
				"rather than on inputs it cannot read at all"
		default:
			row.Verdict = "the realised rate is within reach of the nominal level"
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Arm < rows[j].Arm })

	out := map[string]any{
		"per_arm": rows,
		"question": "the composite's realised FDR does not move with q, so the level has no " +
			"purchase on it. Running the same step-up on a rule that consumes the same " +
			"calibrated inputs and combines them differently says whether the defect is in " +
			"the combination or upstream of it",
		"reading": "the Sidak-corrected minimum and Fisher's sum take identical inputs. A " +
			"minimum near q would put the defect in Fisher's sum over dependent p-values; " +
			"both equally far off puts it upstream, where no better sum can reach it",
	}

	// The comparison itself, stated rather than left for a reader to compute from the rows.
	byName := map[string]armCalibration{}
	for _, r := range rows {
		byName[r.Arm] = r
	}
	composite, hasComposite := byName["composite"]
	minimum, hasMinimum := byName["min-p"]
	if hasComposite && hasMinimum {
		out["localisation"] = localise(composite, minimum)
	}
	// The within-run control, where the run recorded the pre-conformal statistic. It answers a
	// narrower question than the cross-rule comparison and answers it without a confound.
	if model, ok := byName["composite-model"]; ok && hasComposite {
		out["within_run_control"] = withinRun(composite, model)
	}
	return out
}

// localise states what the two rules together imply about where the defect is.
//
// It compares **responsiveness** rather than worst-case rate, and the first version of this
// function compared the rates and read the measurement backwards. Both rules realise a rate far
// above every nominal level, so on worst case they look alike; what separates them is that the
// minimum's realised rate moves from 0.00 to 0.96 across the q grid and makes no discoveries at
// all at the tightest level, while Fisher's moves by 0.004 and makes 3,861. A level that changes
// nothing is a different failure from a level that changes the answer but lands in the wrong
// place, and only the second is a calibration problem the inputs could explain.
func localise(composite, minimum armCalibration) map[string]any {
	worst := func(a armCalibration) float64 {
		high := 0.0
		for _, p := range a.Points {
			if p.RealisedFDR.Point > high {
				high = p.RealisedFDR.Point
			}
		}
		return high
	}
	// The tightest level at which the rule makes any discovery at all, and what it realised
	// there. A rule whose level has purchase refuses to reject when the level is tight enough.
	tightest := func(a armCalibration) (q, realised float64, discoveries int) {
		for _, p := range a.Points {
			if p.Discoveries > 0 {
				return p.NominalQ, p.RealisedFDR.Point, p.Discoveries
			}
		}
		return 0, 0, 0
	}

	compositeWorst, minimumWorst := worst(composite), worst(minimum)
	compositeQ, compositeRealised, compositeDisc := tightest(composite)
	minimumQ, minimumRealised, minimumDisc := tightest(minimum)

	// Responsive means the realised rate tracks the level at all. The threshold is deliberately
	// loose: the question is whether the knob is connected, not whether it is accurate.
	const responsive = 0.10
	compositeResponds := composite.RealisedSpread >= responsive
	minimumResponds := minimum.RealisedSpread >= responsive

	var finding string
	switch {
	case minimumResponds && !compositeResponds:
		finding = "there are two defects and they are separable. Fisher's sum makes the level " +
			"inert -- its realised rate moves by " +
			fmtFloat(composite.RealisedSpread) + " across a q grid spanning 0.001 to 0.25, " +
			"and it rejects " + fmtInt(compositeDisc) + " events even at the tightest level -- " +
			"while the minimum over identical inputs moves by " +
			fmtFloat(minimum.RealisedSpread) + " and rejects nothing until q reaches " +
			fmtFloat(minimumQ) + ". So the combination rule is where the level's purchase is " +
			"lost. But the minimum still realises " + fmtFloat(minimumRealised) + " at that " +
			"level, so the inputs are anti-conservative as well: repairing the sum would give " +
			"a responsive control that still lands far from where it was asked to"
	case !minimumResponds && !compositeResponds:
		finding = "the defect is upstream of the combination. Two rules with different " +
			"dependence structures, over identical inputs, both realise a rate far above " +
			"every nominal level and neither responds to it, so no choice of combination rule " +
			"recovers it and conformal calibration of each marginal is not sufficient for a " +
			"joint statement"
	case compositeWorst < 0.25 && minimumWorst < 0.25:
		finding = "both rules control the rate; there is nothing to localise"
	default:
		finding = "both rules respond to the level and both land above it, which puts the " +
			"defect in the inputs rather than in either combination rule"
	}

	return map[string]any{
		"composite_worst_realised":  compositeWorst,
		"minimum_worst_realised":    minimumWorst,
		"composite_realised_spread": composite.RealisedSpread,
		"minimum_realised_spread":   minimum.RealisedSpread,
		"composite_tightest_level": map[string]any{
			"nominal_q": compositeQ, "realised": compositeRealised,
			"discoveries": compositeDisc,
		},
		"minimum_tightest_level": map[string]any{
			"nominal_q": minimumQ, "realised": minimumRealised, "discoveries": minimumDisc,
		},
		"statistic": "responsiveness, not worst-case rate. Both rules land far above every " +
			"nominal level, so on the worst case they look alike; what separates them is " +
			"whether the realised rate moves with the level at all",
		"finding": finding,
	}
}

// fmtFloat and fmtInt render a number into the finding text, so the sentence a reader sees
// carries the measurement rather than referring to a field they have to go and find.
func fmtFloat(v float64) string { return strconv.FormatFloat(v, 'g', 3, 64) }
func fmtInt(v int) string       { return strconv.Itoa(v) }

// preConformalArm derives the within-run control: the same alerts ranked and thresholded by the
// combination's own log tail, before the composite conformal replaced it.
//
// This is the comparison #55 actually needs. Setting the conformalised composite beside the min-p
// arm compares two combination RULES; setting it beside its own pre-conformal statistic compares
// one rule with and without the calibration, on identical events, identical inputs, an identical
// covariance and an identical burn-in split. The alternative -- comparing against a separately
// configured run -- confounds the calibration with the split, and the split is not innocuous: it
// halves the window both input estimates are fitted on and moves every cross-arm quantity.
//
// It returns false where the run applied no composite conformal, since then LogP already IS the
// model statistic and the control would be the arm compared with itself.
func preConformalArm(primary arm) (namedArm, bool) {
	out := arm{name: "composite-model", perDay: map[int64][]alertRow{}}
	differs := false
	for day, rows := range primary.perDay {
		converted := make([]alertRow, 0, len(rows))
		for _, r := range rows {
			if r.ModelLogP == nil {
				continue
			}
			if *r.ModelLogP != r.LogP {
				differs = true
			}
			r.LogP = *r.ModelLogP
			r.ModelLogP = nil
			converted = append(converted, r)
		}
		if len(converted) == 0 {
			continue
		}
		// Re-sorted because the ordering the replay wrote is by the conformal value, and the
		// step-up walks its input ascending. Ties broken by instant, as loadArm does, so the
		// two arms differ only in the statistic and not in how ties are resolved.
		sort.Slice(converted, func(i, j int) bool {
			if converted[i].LogP != converted[j].LogP {
				return converted[i].LogP < converted[j].LogP
			}
			return converted[i].TSeconds < converted[j].TSeconds
		})
		out.perDay[day] = converted
	}
	if !differs || len(out.perDay) == 0 {
		return namedArm{}, false
	}
	return namedArm{
		name:        "composite-model",
		combination: "Fisher (18) with Brown's correction (19), NOT conformally calibrated as a composite -- the same run's own control",
		arm:         out,
	}, true
}

// withinRun states what the control establishes, which is a narrower and stronger claim than the
// cross-rule comparison: not "some other combination behaves differently" but "this combination,
// on these inputs, behaves differently once its own distribution is used".
func withinRun(conformalised, model armCalibration) map[string]any {
	tightestRejecting := func(a armCalibration) (q float64, disc int, realised float64) {
		for _, p := range a.Points {
			if p.Discoveries > 0 {
				return p.NominalQ, p.Discoveries, p.RealisedFDR.Point
			}
		}
		return 0, 0, 0
	}
	cq, cd, cr := tightestRejecting(conformalised)
	mq, md, mr := tightestRejecting(model)

	var finding string
	switch {
	case md > 0 && cd == 0:
		finding = "the calibration is what the combination needed. On identical inputs the " +
			"uncalibrated composite rejects " + fmtInt(md) + " events at q = " +
			fmtFloat(mq) + " and realises " + fmtFloat(mr) + " there, while the " +
			"conformalised composite rejects nothing at any level on the grid. A level that " +
			"refuses when it is tight is what a level is for, and section 10.1's guarantee " +
			"about each marginal did not supply it"
	case md > 0 && cq > mq:
		finding = "the calibration is what the combination needed. On identical inputs the " +
			"uncalibrated composite already rejects " + fmtInt(md) + " events at q = " +
			fmtFloat(mq) + ", where the conformalised composite rejects nothing until q " +
			"reaches " + fmtFloat(cq) + ". The level has purchase it did not have. It still " +
			"lands at " + fmtFloat(cr) + " where it does reject, so the inputs bound how " +
			"accurate that level can be -- calibrating the combination fixes the knob, not " +
			"the evidence"
	case md > 0 && cq == mq:
		finding = "the calibration does not move the level. Both forms first reject at " +
			"q = " + fmtFloat(mq) + ", so the composite's own distribution is not what was " +
			"missing and the defect is upstream of the combination"
	default:
		finding = "the uncalibrated composite rejects nothing on this grid either, so the " +
			"control cannot separate them"
	}

	return map[string]any{
		"comparison": "the same run, the same events, the same conformally calibrated inputs, " +
			"the same frozen covariance and the same burn-in split. The only difference is " +
			"whether the combination was ranked against its own burn-in distribution",
		"why_not_across_runs": "a run configured without -conformal-composite has an unsplit " +
			"burn-in, so both input estimates are fitted on twice the window. Comparing " +
			"across runs confounds the calibration with the split, and the split moves every " +
			"cross-arm quantity",
		"conformalised_tightest_rejecting_level": map[string]any{
			"nominal_q": cq, "discoveries": cd, "realised": cr,
		},
		"uncalibrated_tightest_rejecting_level": map[string]any{
			"nominal_q": mq, "discoveries": md, "realised": mr,
		},
		"conformalised_realised_spread": conformalised.RealisedSpread,
		"uncalibrated_realised_spread":  model.RealisedSpread,
		"finding":                       finding,
	}
}
