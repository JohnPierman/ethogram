package calibration

import (
	"math"
	"sort"
)

// Brown's correction, measured against the statistic it corrects (#55).
//
// # What this turns into a number
//
// "Brown's correction is an approximation of the dependence" is the kind of statement that
// survives indefinitely because nothing contradicts it. It has an exact measurable content, and
// the reason is worth stating: Brown does not touch the mean.
//
// Fisher's statistic over J p-values has E[X²] = 2J under uniformity, whatever the dependence,
// because expectation is linear and each −2 ln p is Exponential(1/2) marginally. Brown refers
// X²/c to χ²_f with
//
//	c = Var/(2·E)   and   f = 2·E²/Var,   E = 2J,  Var = 4J + 2·Σ_{i<j} Cov(−2 ln p_i, −2 ln p_j)
//
// so c·f = E identically: the assumed distribution has the *same mean* as Fisher's and a
// different variance. Brown is a variance correction and nothing else.
//
// That splits the diagnosis in two, and the halves have different causes and different repairs:
//
//   - **The mean.** If the observed mean of X² departs from 2J, the marginals are not uniform.
//     Brown cannot see that and no covariance estimate repairs it. It is a statement about the
//     inputs, and the [Conformal] calibration of §10.1 is the instrument aimed at it.
//   - **The variance.** If the observed variance departs from 4J + 2ΣCov, the covariance estimate
//     is wrong — mis-measured, or measured on burn-in and since drifted. Observed variance ABOVE
//     the prediction means Brown's tail is too light, which is anti-conservative and is the
//     direction that produces discoveries a nominal level did not license.
//
// # Why per day and per phase
//
// Per corpus day because a single figure over a fortnight would average a correction that was
// right at the start with one that had drifted, and report neither.
//
// Per phase — burn-in against scored — because the covariance is *fitted* on burn-in. In-sample
// the estimate should reproduce the statistic's variance almost exactly; if it does not, the
// correction's form is wrong. If it does in-sample and fails out-of-sample, the form is fine and
// the estimate has drifted. Measuring only one phase cannot separate those, and they call for
// opposite repairs: a different correction against a re-estimated or rolling covariance.
//
// # The contamination, stated
//
// The moments are taken over every event in the bucket, which on scored days includes the true
// positives. There are on the order of a hundred of those against millions of events, so their
// contribution to a mean or a variance is far below the precision any conclusion here rests on.
// It is not zero, and a bucket small enough for it to matter is reported with its count so a
// reader can see that.

// brownBucket accumulates the moments of one (phase, day, J) cell by Welford's method, which is
// used rather than the sum-of-squares form because X² reaches the thousands on a model tail and
// Σx² − (Σx)²/n then differences two large nearly-equal numbers.
type brownBucket struct {
	n        int64
	mean     float64
	m2       float64
	predVar  float64 // Brown's predicted variance, 4J + 2ΣCov, averaged over the cell
	predC    float64
	predF    float64
	predWSum float64
}

func (b *brownBucket) observe(x2, c, f float64) {
	b.n++
	delta := x2 - b.mean
	b.mean += delta / float64(b.n)
	b.m2 += delta * (x2 - b.mean)

	// c and f are constant within a cell whenever the covariance is frozen and J fixed, which
	// is the usual case; they are averaged rather than assumed constant so that a run with a
	// re-estimated covariance still reports something true rather than the last event's value.
	b.predC += c
	b.predF += f
	b.predWSum++
	// Var of c·χ²_f is 2c²f.
	b.predVar += 2 * c * c * f
}

func (b *brownBucket) variance() float64 {
	if b.n < 2 {
		return 0
	}
	return b.m2 / float64(b.n-1)
}

// BrownCell is one (phase, day, J) cell's comparison.
type BrownCell struct {
	Phase string `json:"phase"`
	Day   int64  `json:"day"`
	// J is the number of evaluated detectors the statistic was formed from. Cells are split
	// on it because 2J is the mean the observation is compared against.
	J     int   `json:"j"`
	Count int64 `json:"count"`

	// MeanX2 against ExpectedMean = 2J: the marginals' half of the diagnosis.
	MeanX2       float64 `json:"mean_x2"`
	ExpectedMean float64 `json:"expected_mean"`
	MeanRatio    float64 `json:"mean_ratio"`

	// VarianceX2 against Brown's prediction: the dependence's half.
	VarianceX2        float64 `json:"variance_x2"`
	PredictedVariance float64 `json:"predicted_variance"`
	VarianceRatio     float64 `json:"variance_ratio"`

	// BrownC and BrownF are the correction actually applied, and EmpiricalC and EmpiricalF the
	// scaled-χ² that matches the observed moments. The gap between F and EmpiricalF is the
	// issue's "effective degrees of freedom against an empirical null" in one number.
	BrownC     float64 `json:"brown_c"`
	BrownF     float64 `json:"brown_f"`
	EmpiricalC float64 `json:"empirical_c"`
	EmpiricalF float64 `json:"empirical_f"`

	// Direction says which way the error runs, which matters more than its size: only one
	// direction manufactures discoveries.
	Direction string `json:"direction"`
}

// BrownDiagnostic accumulates the moments of Fisher's statistic per phase, corpus day and J.
//
// State is one small struct per cell, so at seven detectors over a fortnight in two phases it is
// bounded by a couple of hundred cells regardless of how many events pass through — the §13.3
// requirement applies to a diagnostic as much as to an arm.
type BrownDiagnostic struct {
	daySeconds int64
	cells      map[brownKey]*brownBucket
}

type brownKey struct {
	phase string
	day   int64
	j     int
}

// NewBrownDiagnostic returns an accumulator bucketing by corpus day of the given length in
// seconds. A non-positive length is taken as 86,400.
func NewBrownDiagnostic(daySeconds int64) *BrownDiagnostic {
	if daySeconds <= 0 {
		daySeconds = 86400
	}
	return &BrownDiagnostic{daySeconds: daySeconds, cells: map[brownKey]*brownBucket{}}
}

// Observe folds one event's combined statistic in. phase is free text — "burn_in" and "scored"
// are what the replay uses — and c and f are the correction that was applied to this event.
//
// atSeconds is the event's instant in CORPUS SECONDS, and the parameter is named for its unit
// rather than typed because this package holds no timestamp type. The caller converts. That
// caution is not idle: [event.Timestamp] is microseconds, and a sibling arm in this repository
// divides a microsecond timestamp by 3,600 and calls the result an hourly window — it is 3.6
// milliseconds, so one corpus second spans 277 of them.
//
// Non-finite inputs are ignored rather than propagated: a single NaN would destroy every moment
// in its cell, and a diagnostic that reports NaN says less than one that reports a smaller count.
func (d *BrownDiagnostic) Observe(phase string, atSeconds int64, j int, x2, c, f float64) {
	if d == nil || j <= 0 {
		return
	}
	if math.IsNaN(x2) || math.IsInf(x2, 0) || math.IsNaN(c) || math.IsInf(c, 0) ||
		math.IsNaN(f) || math.IsInf(f, 0) || c <= 0 || f <= 0 {
		return
	}
	key := brownKey{phase: phase, day: atSeconds / d.daySeconds, j: j}
	bucket, ok := d.cells[key]
	if !ok {
		bucket = &brownBucket{}
		d.cells[key] = bucket
	}
	bucket.observe(x2, c, f)
}

// MinimumCount is the fewest events a cell needs before its variance is reported.
//
// A variance from a handful of draws is not a variance, and this diagnostic exists to say
// whether a correction is off by a factor — a claim that a noisy cell could manufacture on its
// own. Cells below this are still returned, with their count, so a reader sees the coverage
// rather than a filtered set that looks complete.
const MinimumCount = 100

// Cells returns every cell, ordered by phase, then day, then J, so the output is deterministic
// (R4) and reads down the corpus.
func (d *BrownDiagnostic) Cells() []BrownCell {
	if d == nil {
		return nil
	}
	out := make([]BrownCell, 0, len(d.cells))
	for key, b := range d.cells {
		cell := BrownCell{
			Phase: key.phase, Day: key.day, J: key.j, Count: b.n,
			MeanX2:       b.mean,
			ExpectedMean: 2 * float64(key.j),
		}
		if b.predWSum > 0 {
			cell.BrownC = b.predC / b.predWSum
			cell.BrownF = b.predF / b.predWSum
			cell.PredictedVariance = b.predVar / b.predWSum
		}
		if cell.ExpectedMean > 0 {
			cell.MeanRatio = cell.MeanX2 / cell.ExpectedMean
		}
		if b.n >= MinimumCount {
			cell.VarianceX2 = b.variance()
			if cell.PredictedVariance > 0 {
				cell.VarianceRatio = cell.VarianceX2 / cell.PredictedVariance
			}
			if cell.VarianceX2 > 0 && cell.MeanX2 > 0 {
				// The scaled-χ² whose first two moments are the observed ones.
				cell.EmpiricalC = cell.VarianceX2 / (2 * cell.MeanX2)
				cell.EmpiricalF = 2 * cell.MeanX2 * cell.MeanX2 / cell.VarianceX2
			}
			cell.Direction = brownDirection(cell)
		} else {
			cell.Direction = "too few events in this cell to report a variance"
		}
		out = append(out, cell)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Phase != out[j].Phase {
			return out[i].Phase < out[j].Phase
		}
		if out[i].Day != out[j].Day {
			return out[i].Day < out[j].Day
		}
		return out[i].J < out[j].J
	})
	return out
}

// tolerance is how far a ratio may sit from one before it is called a departure. Loose, because
// the question is whether the correction is off by a factor and not whether it is exact.
const tolerance = 0.25

// brownDirection names which half of the diagnosis a cell implicates and which way it runs.
func brownDirection(c BrownCell) string {
	meanOff := c.MeanRatio > 1+tolerance || c.MeanRatio < 1-tolerance
	varHigh := c.VarianceRatio > 1+tolerance
	varLow := c.VarianceRatio > 0 && c.VarianceRatio < 1-tolerance

	switch {
	case meanOff && varHigh:
		return "both halves fail. The mean departs from 2J, so the marginals are not " +
			"uniform and no covariance estimate reaches that; and the observed variance " +
			"exceeds Brown's prediction, so the tail it computes is too light and the " +
			"correction is anti-conservative on top"
	case meanOff:
		return "the marginals, not the dependence. The mean departs from 2J, which Brown " +
			"does not touch: it matches the mean by construction and corrects only the " +
			"variance, so this is a defect in the inputs and a better covariance cannot " +
			"reach it"
	case varHigh:
		return "the dependence, anti-conservatively. The statistic is more dispersed than " +
			"the covariance predicts, so X2/c is referred to a chi-square that is too " +
			"narrow and the tail comes out too small"
	case varLow:
		return "the dependence, conservatively. The statistic is less dispersed than the " +
			"covariance predicts, so the correction over-charges and loses power without " +
			"risking the level"
	default:
		return "both moments are within tolerance of the assumed distribution"
	}
}

// BrownSummary is the diagnostic reduced to the comparison the issue asks for.
type BrownSummary struct {
	Cells []BrownCell `json:"cells"`
	// Reported is how many cells carried enough events for a variance.
	Reported int `json:"reported_cells"`
	// WorstVarianceRatio and its cell, over reported cells: the largest departure in the
	// anti-conservative direction, which is the one that manufactures discoveries.
	WorstVarianceRatio float64 `json:"worst_variance_ratio"`
	WorstMeanRatio     float64 `json:"worst_mean_ratio"`
	Finding            string  `json:"finding"`
	Method             string  `json:"method"`
}

// Summarise reduces the cells to a statement. It reports the worst departure rather than an
// average, because an average over days would hide a correction that failed on some of them.
func (d *BrownDiagnostic) Summarise() BrownSummary {
	cells := d.Cells()
	s := BrownSummary{
		Cells:              cells,
		WorstVarianceRatio: 1,
		WorstMeanRatio:     1,
		Method: "Brown refers X2/c to chi-square_f with c = Var/(2E) and f = 2E^2/Var, " +
			"E = 2J and Var = 4J + 2*sum of covariances. Since c*f = E identically, Brown " +
			"matches the mean by construction and corrects only the variance: a mean away " +
			"from 2J is a defect in the marginals and a variance away from the prediction " +
			"is a defect in the covariance",
	}
	worstVarSeen, worstMeanSeen := false, false
	for _, c := range cells {
		if c.Count < MinimumCount {
			continue
		}
		s.Reported++
		if c.VarianceRatio > 0 {
			if !worstVarSeen || math.Abs(math.Log(c.VarianceRatio)) >
				math.Abs(math.Log(s.WorstVarianceRatio)) {
				s.WorstVarianceRatio = c.VarianceRatio
				worstVarSeen = true
			}
		}
		if c.MeanRatio > 0 {
			if !worstMeanSeen || math.Abs(math.Log(c.MeanRatio)) >
				math.Abs(math.Log(s.WorstMeanRatio)) {
				s.WorstMeanRatio = c.MeanRatio
				worstMeanSeen = true
			}
		}
	}
	if s.Reported == 0 {
		s.Finding = "no cell carried enough events to report a variance"
	} else {
		s.Finding = brownDirection(BrownCell{
			MeanRatio:     s.WorstMeanRatio,
			VarianceRatio: s.WorstVarianceRatio,
		})
	}
	return s
}
