package objective_test

import (
	"math"
	"testing"

	"github.com/JohnPierman/ethogram/domain/objective"
)

// The corpus this project measures on: 42,218,530 events over seven days of which 549 are
// labelled. Both quantities are the paper's own, so the table below is a second derivation of
// a published table rather than a restatement of it.
const (
	corpusBaseRate     = 549.0 / 42218530.0
	corpusEventsPerDay = 42218530.0 / 7.0
)

// roundTo2SigFigs rounds to the precision the published table is printed at, so the
// comparison is against the table's own figures rather than against its rounding.
func roundTo2SigFigs(v float64) float64 {
	if v == 0 {
		return 0
	}
	mag := math.Pow(10, math.Floor(math.Log10(math.Abs(v)))-1)
	return math.Round(v/mag) * mag
}

func mustRatio(t *testing.T, miss, review float64) objective.CostRatio {
	t.Helper()
	c, err := objective.NewCostRatio(miss, review)
	if err != nil {
		t.Fatalf("NewCostRatio(%v, %v): %v", miss, review, err)
	}
	return c
}

// TestCostCurveReproducesThePublishedBaseRateTable is the acceptance criterion of #14: two
// derivations of the same table must agree.
//
// The table is the paper's, computed from this corpus's own base rate. Here it is derived from
// the cost model instead — each row's precision becomes a cost ratio, and the operating point
// that ratio implies must land on the published alpha and the published per-day counts.
func TestCostCurveReproducesThePublishedBaseRateTable(t *testing.T) {
	for _, row := range []struct {
		precision   float64
		alpha       float64
		falsePerDay float64
		totalPerDay float64
	}{
		{0.90, 1.4e-6, 9, 87},
		{0.50, 1.3e-5, 78, 157},
		{0.25, 3.9e-5, 235, 314},
		{0.10, 1.2e-4, 706, 784},
		{0.05, 2.5e-4, 1490, 1569},
	} {
		ratio, err := objective.RatioForPrecision(row.precision)
		if err != nil {
			t.Fatalf("RatioForPrecision(%v): %v", row.precision, err)
		}
		o, err := objective.Threshold(ratio, corpusBaseRate, corpusEventsPerDay)
		if err != nil {
			t.Fatalf("Threshold: %v", err)
		}

		// The published alpha carries two significant figures, so agreement is asserted at
		// exactly that precision: rounding the derived value and requiring equality tests
		// the identity, where a relative tolerance would be testing the table's rounding.
		// The epsilon is for float representation only -- 1.2e-4 as a literal and as the
		// result of a division are not bit-identical -- not a tolerance on the identity.
		if got := roundTo2SigFigs(o.Alpha); math.Abs(got-row.alpha) > 1e-9*row.alpha {
			t.Errorf("precision %.0f%%: alpha = %.6g, rounds to %.6g, published %.6g",
				100*row.precision, o.Alpha, got, row.alpha)
		}
		if got := math.Round(o.ExpectedFalsePerDay); math.Abs(got-row.falsePerDay) > 1 {
			t.Errorf("precision %.0f%%: false alerts/day = %.0f, published %.0f",
				100*row.precision, got, row.falsePerDay)
		}
		if got := math.Round(o.ExpectedAlertsPerDay()); math.Abs(got-row.totalPerDay) > 1 {
			t.Errorf("precision %.0f%%: total alerts/day = %.0f, published %.0f",
				100*row.precision, got, row.totalPerDay)
		}
		// The threshold a precision asks for IS that precision, which is the identity
		// RatioForPrecision inverts. If this drifts the whole table is meaningless.
		if math.Abs(o.PosteriorThreshold-row.precision) > 1e-12 {
			t.Errorf("precision %.0f%%: posterior threshold = %v", 100*row.precision,
				o.PosteriorThreshold)
		}
	}
}

// TestAlphaIsMonotoneInBothArguments pins the two monotonicity properties over a
// decade-spanning grid.
//
// **Correction to #14 as written.** The issue asks that alpha be "monotone decreasing in
// baseRate". It is monotone *increasing*, and the paper's own arithmetic says so: alpha =
// p(1−tau)/(tau(1−p)), whose factor p/(1−p) rises with p. The reading is straightforward — a
// rarer target demands a stricter false-alarm rate to reach the same precision, so a *higher*
// base rate affords a looser one. At tau = 0.5 the identity collapses to alpha ≈ p, which is
// the paper's "half a queue is real only at alpha ≈ 1.3e-5" against a base rate of 1.30e-5;
// were alpha decreasing in p that sentence could not hold.
//
// The second property the issue states is correct: alpha rises with the miss-to-review ratio.
func TestAlphaIsMonotoneInBothArguments(t *testing.T) {
	ratio := mustRatio(t, 10, 1)
	previous := 0.0
	for _, p := range []float64{1e-7, 1e-6, 1e-5, 1e-4, 1e-3, 1e-2, 1e-1} {
		o, err := objective.Threshold(ratio, p, 0)
		if err != nil {
			t.Fatalf("Threshold at base rate %v: %v", p, err)
		}
		if o.Alpha <= previous {
			t.Errorf("alpha did not rise with the base rate: %.4g at p=%v after %.4g",
				o.Alpha, p, previous)
		}
		previous = o.Alpha
	}

	previous = 0.0
	for _, miss := range []float64{1, 10, 100, 1e3, 1e4, 1e5} {
		o, err := objective.Threshold(mustRatio(t, miss, 1), corpusBaseRate, 0)
		if err != nil {
			t.Fatalf("Threshold at miss cost %v: %v", miss, err)
		}
		if o.Alpha <= previous {
			t.Errorf("alpha did not rise with the miss:review ratio: %.4g at %v after %.4g",
				o.Alpha, miss, previous)
		}
		previous = o.Alpha
	}
}

// TestAlphaClampIsReportedNotSilent. At an extreme ratio the arithmetic asks for alpha > 1,
// which means "alert on everything and still fall short of this precision". That is a finding
// about the base rate and the operator must see it, so the clamp is a reported flag rather
// than a quiet minimum.
func TestAlphaClampIsReportedNotSilent(t *testing.T) {
	// A base rate near one half with an enormous miss cost drives the demanded alpha past 1.
	o, err := objective.Threshold(mustRatio(t, 1e6, 1), 0.4, 0)
	if err != nil {
		t.Fatalf("Threshold: %v", err)
	}
	if !o.AlphaClamped {
		t.Errorf("alpha = %.4g at a threshold of %.4g and the clamp was not reported",
			o.Alpha, o.PosteriorThreshold)
	}
	if o.Alpha != 1 {
		t.Errorf("a clamped alpha is %v, want exactly 1", o.Alpha)
	}

	// And the ordinary case must not claim to have been clamped.
	fine, err := objective.Threshold(mustRatio(t, 10, 1), corpusBaseRate, 0)
	if err != nil {
		t.Fatalf("Threshold: %v", err)
	}
	if fine.AlphaClamped {
		t.Error("an unclamped operating point reports a clamp")
	}
}

// TestAccuracyEquivalentIsTheHalfThreshold. Maximising accuracy is not free of a cost model;
// it carries one-to-one without saying so, and the paper's rejection of scalar accuracy-like
// objectives turns on that. The value is named so the comparison can be made in code.
func TestAccuracyEquivalentIsTheHalfThreshold(t *testing.T) {
	c := objective.AccuracyEquivalent()
	if got := c.PosteriorThreshold(); math.Abs(got-0.5) > 1e-15 {
		t.Errorf("PosteriorThreshold() = %v, want 0.5", got)
	}
	if got := c.Ratio(); got != 1 {
		t.Errorf("Ratio() = %v, want 1", got)
	}
	// At this corpus's base rate the one-to-one point demands an alpha of about the base
	// rate itself, which is the paper's headline arithmetic.
	o, err := objective.Threshold(c, corpusBaseRate, corpusEventsPerDay)
	if err != nil {
		t.Fatalf("Threshold: %v", err)
	}
	if rel := math.Abs(o.Alpha-corpusBaseRate) / corpusBaseRate; rel > 1e-4 {
		t.Errorf("alpha at the accuracy-equivalent point = %.4g against a base rate of %.4g",
			o.Alpha, corpusBaseRate)
	}
}

// TestCostCurveIsOrderedByDemandedPrecision. The curve is rendered as a curve, so its points
// must arrive in a monotone order rather than in whatever order the caller listed them.
func TestCostCurveIsOrderedByDemandedPrecision(t *testing.T) {
	var ratios []objective.CostRatio
	for _, p := range []float64{0.05, 0.90, 0.25, 0.10, 0.50} {
		r, err := objective.RatioForPrecision(p)
		if err != nil {
			t.Fatalf("RatioForPrecision(%v): %v", p, err)
		}
		ratios = append(ratios, r)
	}
	curve, err := objective.CostCurve(ratios, corpusBaseRate, corpusEventsPerDay)
	if err != nil {
		t.Fatalf("CostCurve: %v", err)
	}
	if len(curve) != len(ratios) {
		t.Fatalf("curve has %d points for %d ratios", len(curve), len(ratios))
	}
	for i := 1; i < len(curve); i++ {
		if curve[i].PosteriorThreshold >= curve[i-1].PosteriorThreshold {
			t.Errorf("curve is not ordered by demanded precision at %d: %v then %v",
				i, curve[i-1].PosteriorThreshold, curve[i].PosteriorThreshold)
		}
		if curve[i].Alpha <= curve[i-1].Alpha {
			t.Errorf("alpha is not monotone along the curve at %d", i)
		}
	}
}

// TestCostRatioRefusesUnusableCosts. A zero review cost makes "alert on everything" optimal
// and a zero miss cost makes "alert on nothing" optimal; neither is a cost model worth
// deriving a threshold from, and the message must name which side was wrong.
func TestCostRatioRefusesUnusableCosts(t *testing.T) {
	for name, tc := range map[string][2]float64{
		"zero miss":         {0, 1},
		"zero review":       {1, 0},
		"negative miss":     {-1, 1},
		"negative review":   {1, -1},
		"miss not a number": {math.NaN(), 1},
		"infinite review":   {1, math.Inf(1)},
	} {
		if _, err := objective.NewCostRatio(tc[0], tc[1]); err == nil {
			t.Errorf("%s: accepted, want an error", name)
		}
	}
	for name, p := range map[string]float64{
		"zero": 0, "one": 1, "above one": 1.5, "negative": -0.5, "not a number": math.NaN(),
	} {
		if _, err := objective.RatioForPrecision(p); err == nil {
			t.Errorf("RatioForPrecision(%s): accepted, want an error", name)
		}
	}
}

// TestThresholdRefusesAnImpossibleBaseRate. A base rate of zero or one is not a detection
// problem, and the counts derived from it would be meaningless rather than merely extreme.
func TestThresholdRefusesAnImpossibleBaseRate(t *testing.T) {
	c := mustRatio(t, 10, 1)
	for name, p := range map[string]float64{
		"zero": 0, "one": 1, "above one": 2, "negative": -1e-6, "not a number": math.NaN(),
	} {
		if _, err := objective.Threshold(c, p, 0); err == nil {
			t.Errorf("base rate %s: accepted, want an error", name)
		}
	}
	if _, err := objective.Threshold(c, corpusBaseRate, -1); err == nil {
		t.Error("negative exposure: accepted, want an error")
	}
	if _, err := objective.CostCurve(nil, corpusBaseRate, 0); err == nil {
		t.Error("an empty curve was accepted, want an error")
	}
}
