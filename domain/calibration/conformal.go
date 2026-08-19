package calibration

import "math"

// Conformal accumulates, per detector, the burn-in distribution of that detector's
// p-values, so that §10.1's conformal calibration can replace a model-based tail with
// an empirical one.
//
// # Why this exists
//
// Every detector of §6 to §9 states a null and derives a p-value from it. A p-value is
// only calibrated if the null is true, and two of these nulls have been measured to be
// false on real telemetry: before repair the volume detector put 24.7% of all scored
// events below 1e−12 and the partitioned co-occurrence arm 99.0%, which no correct null
// does. §10.1's answer is not to keep improving the models but to stop depending on
// them being right.
//
// The split-conformal p-value is
//
//	p_conf = (1 + #{i : p_i ≤ p}) / (n + 1)
//
// over n scores the same detector produced during burn-in. Under exchangeability it is
// super-uniform whatever the model does — the guarantee follows from ranking alone, not
// from the null being true — so a detector whose model tail is far too small produces a
// conformal p-value that is merely small, and one whose model is sound is left where it
// was.
//
// # When it is estimated
//
// During burn-in only, and frozen at the boundary. §8.2 makes the same demand of the
// Leiden partition and [Correlations] of the covariance, for the same reason: a quantity
// used to score an event must not have been fitted on that event.
//
// # What it cannot do
//
// p_conf can never fall below 1/(n+1). Every event more extreme than the whole burn-in
// sample ties there, and a detector whose scoring-window tail runs past its burn-in tail
// will pile thousands of events onto that floor. Calibration and ranking are different
// jobs: threshold on the conformal p-value, but break ties on the underlying model
// score, or the alert budget is allocated by whatever order the ties happen to arrive
// in. [ConformalModel.Calibrate] returns the floor honestly rather than inventing
// resolution it does not have; keeping the model's own log p beside it is the caller's
// responsibility.
type Conformal struct {
	detectors       map[string]*tailHistogram
	minObservations uint64
}

// conformalBinWidth is the histogram resolution in nats of −ln p. A quarter of a nat
// distinguishes p-values about 28% apart, which is far finer than the ranking needs
// given that ties are broken on the model score anyway.
const conformalBinWidth = 0.25

// conformalMaxNats bounds the histogram. −ln p = 4096 is p ≈ 1e−1779, past anything
// float64 represents, so the top bin collects a genuinely empty region in practice.
const conformalMaxNats = 4096

const conformalBins = int(conformalMaxNats / conformalBinWidth)

// tailHistogram counts burn-in scores by −ln p, with suffix sums after freezing.
type tailHistogram struct {
	counts []uint64
	total  uint64
	// atLeast[i] is the number of observations in bin i or any more extreme bin,
	// computed once at Freeze so calibration is a single lookup.
	atLeast []uint64
}

// NewConformal returns an accumulator. A detector with fewer than minObservations
// burn-in scores is left uncalibrated rather than calibrated against a handful of
// points, and the caller keeps that detector's model p-value.
func NewConformal(minObservations int) *Conformal {
	floor := uint64(0)
	if minObservations > 0 {
		floor = uint64(minObservations)
	}
	return &Conformal{
		detectors:       map[string]*tailHistogram{},
		minObservations: floor,
	}
}

// Observe records one burn-in ln p-value for a detector. Values outside (−inf, 0] are
// ignored: an abstention has no p-value to rank, and R3 forbids encoding one.
//
// It takes the logarithm rather than the p-value because that is what the histogram bins
// on, and because a detector whose tail underflows would otherwise arrive already tied —
// calibration cannot separate what the caller has already collapsed.
func (c *Conformal) Observe(detectorID string, logP float64) {
	if math.IsNaN(logP) || math.IsInf(logP, 0) || logP > 0 {
		return
	}
	h, ok := c.detectors[detectorID]
	if !ok {
		h = &tailHistogram{counts: make([]uint64, conformalBins)}
		c.detectors[detectorID] = h
	}
	h.counts[conformalBin(logP)]++
	h.total++
}

// conformalBin maps ln p to its bin by x = −ln p, clamped into range. Larger bin index
// means a more extreme p-value.
func conformalBin(logP float64) int {
	x := -logP
	if x < 0 {
		x = 0
	}
	bin := int(x / conformalBinWidth)
	if bin >= conformalBins {
		bin = conformalBins - 1
	}
	return bin
}

// Freeze computes the suffix sums and returns the model used for the scoring window.
//
// The frozen model shares nothing with the accumulator: it keeps the suffix sums and the
// total it was built from, and not the live counts. That is what makes it frozen in fact
// rather than by convention — a caller that went on observing after the boundary could
// otherwise mutate the distribution a handed-out model was scoring against, and §10.1's
// whole requirement is that the quantity used to score an event was not fitted on it.
func (c *Conformal) Freeze() *ConformalModel {
	m := &ConformalModel{
		detectors:       make(map[string]*tailHistogram, len(c.detectors)),
		minObservations: c.minObservations,
	}
	for id, h := range c.detectors {
		if h.total < c.minObservations {
			continue
		}
		frozen := &tailHistogram{
			total:   h.total,
			atLeast: make([]uint64, conformalBins),
		}
		// Suffix sums from the most extreme bin downward, in a fixed order (R4).
		var running uint64
		for i := conformalBins - 1; i >= 0; i-- {
			running += h.counts[i]
			frozen.atLeast[i] = running
		}
		m.detectors[id] = frozen
	}
	return m
}

// ConformalModel is a frozen conformal calibration, safe to read while scoring.
type ConformalModel struct {
	detectors       map[string]*tailHistogram
	minObservations uint64
}

// Calibrate converts a detector's model ln p-value into its conformal p-value.
//
// The input is a logarithm and the output is not: a conformal p-value is a rank bounded
// below by 1/(n+1), so it cannot underflow and needs no log-space representation.
//
// The count of burn-in scores at least as extreme is read from the containing bin, which
// counts the whole bin as at least as extreme. That rounds p_conf upward — toward the
// less significant — so the error is conservative, which is the only direction a
// calibration may err in.
//
// ok is false when the detector was never calibrated, in which case the caller keeps the
// model p-value and should record that it did.
func (m *ConformalModel) Calibrate(detectorID string, logP float64) (conformal float64, ok bool) {
	if m == nil {
		return 0, false
	}
	h, found := m.detectors[detectorID]
	if !found || h.total == 0 {
		return 0, false
	}
	if math.IsNaN(logP) || math.IsInf(logP, 0) || logP > 0 {
		return 0, false
	}
	atLeast := h.atLeast[conformalBin(logP)]
	return float64(1+atLeast) / float64(h.total+1), true
}

// Floor is the smallest conformal p-value a detector can produce, 1/(n+1). An alert set
// drawn entirely from the floor is not a ranking, and a caller reporting one should say
// so rather than present the order as meaningful.
func (m *ConformalModel) Floor(detectorID string) (floor float64, ok bool) {
	if m == nil {
		return 0, false
	}
	h, found := m.detectors[detectorID]
	if !found || h.total == 0 {
		return 0, false
	}
	return 1 / float64(h.total+1), true
}

// Calibrated reports the detectors carrying a frozen distribution, and how many burn-in
// observations each rests on, for the result's provenance.
func (m *ConformalModel) Calibrated() map[string]uint64 {
	if m == nil {
		return nil
	}
	out := make(map[string]uint64, len(m.detectors))
	for id, h := range m.detectors {
		out[id] = h.total
	}
	return out
}
