package allocation

import (
	"fmt"
	"math"
	"sort"
)

// Tail is one detector's frozen null over its own log p-value: the probability that a
// background event scores at least as extreme as a given alert.
//
// It exists to put several detectors on one scale without comparing their p-values, which
// is the comparison that has no meaning — a detector's p-value is a statement under its own
// null, and two detectors' nulls are two different distributions. A quantile in the
// detector's own fitting sample is comparable across detectors because it is uniform under
// every one of them.
//
// # Why the empirical quantile is not enough
//
// A rank in a sample of n cannot fall below 1/(n+1). Every alert more extreme than the whole
// fitting sample therefore ties at that floor — and on security telemetry that is not a rare
// edge, it is the head of the queue: the fitting window is meant to be quiet and the alerts
// worth having are more extreme than anything in it. A score that ties the head of a queue
// orders it by whatever breaks the tie, which is arrival time, not evidence.
//
// So past a high threshold the tail is EXTENDED rather than floored: an exponential fit to
// the excesses over that threshold, which is the standard peaks-over-threshold form for the
// tail of a distribution whose shape above the threshold is not otherwise modelled. The
// result is strictly decreasing in the log p-value everywhere, so two alerts of different
// extremity can never tie, and it is linear in the log p-value past the threshold, so it
// stays representable where the probability itself does not.
//
// # Frozen
//
// Fitted once, on a window disjoint from the one being scored, and never updated by an
// event it will be used to score. That is the same rule the offline community detection
// follows, and it is also what makes the tail deployable: nothing about it depends on the
// rest of the day.
//
// A value object. Immutable after construction and safe to share.
type Tail struct {
	// sample is the retained extreme log p-values, ascending, so the most extreme is
	// first. Only the extremes are needed: the bulk of a null never reaches a budget.
	sample []float64
	// total is how many events the detector evaluated in the fitting window, which is the
	// denominator that turns a count of the sample into a probability. Without it a
	// position in the sample has no scale — 100th of 600,000 and 100th of 900 are not the
	// same evidence.
	total int
	// threshold is the excess threshold u, and scale the fitted mean excess above it.
	// scale is zero when the sample carried no spread, in which case the extension is not
	// applied and the empirical form is used throughout.
	threshold float64
	scale     float64
}

// DefaultExcessCount is how many of the most extreme observations the exponential tail is
// fitted from.
//
// A peaks-over-threshold fit trades bias against variance in the choice of threshold: too
// few observations and the mean excess is noisy, too many and the exponential form is being
// asked to describe the body of the distribution rather than its tail. Two hundred is
// enough for the mean of an exponential to be determined to a few per cent while remaining
// a small fraction of a retained sample of thousands.
const DefaultExcessCount = 200

// NewTail fits a detector's frozen null from the log p-values it assigned to events in the
// fitting window, and the number of events it evaluated there.
//
// sample need not be sorted and is not modified. It should be the retained extremes rather
// than every observation; total must count every evaluation, retained or not, or the
// quantiles it produces will be too large by the ratio of the two.
//
// excessCount is how many of the most extreme observations the exponential extension is
// fitted from; zero selects [DefaultExcessCount].
func NewTail(sample []float64, total, excessCount int) (*Tail, error) {
	if len(sample) < 2 {
		return nil, ErrEmptyTail
	}
	if total < len(sample) {
		return nil, fmt.Errorf(
			"allocation: tail fitted from %d observations but only %d evaluations; "+
				"total must count every event the detector saw, not only the retained ones",
			len(sample), total)
	}
	if excessCount <= 0 {
		excessCount = DefaultExcessCount
	}

	sorted := make([]float64, 0, len(sample))
	for i, v := range sample {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, fmt.Errorf("allocation: tail observation %d is %v, not finite", i, v)
		}
		if v > 0 {
			return nil, fmt.Errorf(
				"allocation: tail observation %d is %v; a log p-value cannot exceed 0", i, v)
		}
		sorted = append(sorted, v)
	}
	sort.Float64s(sorted)

	t := &Tail{sample: sorted, total: total}

	m := min(excessCount, len(sorted))
	t.threshold = sorted[m-1]
	// Excesses are measured downwards: more negative is more extreme, so an observation
	// below the threshold exceeds it by threshold − observation.
	var sum float64
	var n int
	for _, v := range sorted[:m] {
		if e := t.threshold - v; e > 0 {
			sum += e
			n++
		}
	}
	if n > 0 && sum > 0 {
		t.scale = sum / float64(n)
	}
	return t, nil
}

// Observations is how many retained observations the tail was fitted from.
func (t *Tail) Observations() int { return len(t.sample) }

// Evaluations is how many events the detector evaluated in the fitting window.
func (t *Tail) Evaluations() int { return t.total }

// Threshold is the excess threshold u: the log p-value past which the exponential
// extension takes over from the empirical count.
func (t *Tail) Threshold() float64 { return t.threshold }

// Scale is the fitted mean excess over [Tail.Threshold], or zero when the sample carried no
// spread and the extension was not fitted.
func (t *Tail) Scale() float64 { return t.scale }

// LogQuantile is the natural log of the probability that a background event scores at or
// below logP under this detector's frozen null.
//
// Strictly decreasing in logP wherever the extension applies, and never −Inf, so it is a
// total order on alerts and a representable number at any extremity.
func (t *Tail) LogQuantile(logP float64) float64 {
	if len(t.sample) == 0 {
		return 0 // no null: every event is unremarkable
	}
	if logP < t.threshold && t.scale > 0 {
		// Past the threshold. Anchored on the empirical count AT the threshold so the two
		// pieces meet, then linear in logP, which is why it cannot underflow.
		atThreshold := max(t.countAtOrBelow(t.threshold), 1)
		return math.Log(float64(atThreshold)/float64(t.total)) -
			(t.threshold-logP)/t.scale
	}
	c := t.countAtOrBelow(logP)
	if c == 0 {
		// More extreme than every retained observation, with no extension fitted --
		// reachable only when the sample carried no spread. One evaluation's worth is the
		// tightest statement it supports, and it is a bound rather than an estimate.
		return -math.Log(float64(t.total))
	}
	return math.Log(float64(c) / float64(t.total))
}

// countAtOrBelow is how many retained observations are at or below logP.
func (t *Tail) countAtOrBelow(logP float64) int {
	// sort.SearchFloat64s finds the first index whose value is >= logP; entries strictly
	// below it are the ones at a more extreme p-value, and equal entries are included by
	// advancing past them.
	i := sort.SearchFloat64s(t.sample, logP)
	for i < len(t.sample) && t.sample[i] == logP {
		i++
	}
	return i
}
