package calibration

import (
	"math"
	"sort"
)

// Correlations accumulates the pairwise dependence between detectors, so that Brown's
// correction of equation (19) has the covariance it requires.
//
// # Why this exists
//
// Fisher's method, equation (18), assumes the combined p-values are independent. The
// detectors of §6 to §9 are not: Detector I and Detector IV score the same fields at
// different scopes, and an event whose value is new to its entity is very often also
// rare in the population; §7.2 and §7.4 both read the same event stream for one entity.
// Combining dependent p-values as though they were independent inflates X² and drives
// the combined tail far below what the evidence supports.
//
// The consequence is not theoretical. Measured on LANL days 7 to 13 with plain Fisher,
// 34.7% of all scored events fell below p = 1e−12, the retained alert set was drawn
// entirely from that mass, and the labelled attack events — themselves extreme — were
// outranked by benign events on every scored day.
//
// # What is estimated
//
// Equation (19) needs cov(−2 ln P_i, −2 ln P_j). [KostMcDermott] approximates that term
// from the correlation of the underlying statistics, which is what one uses when only a
// correlation is available. Here the paired values are available directly: burn-in
// scores every event without emitting it, so the covariance can be estimated from the
// quantity itself rather than through a polynomial fitted to it. Both are recorded — the
// direct estimate is used, and the Kost–McDermott value implied by the same data is
// reported beside it so the two can be compared.
//
// # When it is estimated
//
// During burn-in only, and frozen at the boundary. §8.2 makes the same demand of the
// Leiden partition and for the same reason: a quantity used to score an event must not
// have been fitted on that event. Freezing also keeps the combination deterministic —
// the covariance is a fixed input to the scoring window rather than something that
// drifts as the window is consumed.
type Correlations struct {
	pairs map[pairKey]*pairStats
	// minObservations is the number of co-evaluations below which a pair's estimate is
	// discarded as unsupported and its covariance treated as zero, which degrades that
	// pair to Fisher's independence assumption rather than to a number invented from a
	// handful of points.
	minObservations int
}

type pairKey struct{ a, b string }

// pairStats holds the streaming moments for one detector pair. Sums accumulate in
// event order, which is fixed, so the estimate is reproducible (R4).
type pairStats struct {
	n                   float64
	sumX, sumY          float64
	sumXX, sumYY, sumXY float64
}

// NewCorrelations returns an accumulator. minObservations is the support a pair must
// reach before its covariance is used.
func NewCorrelations(minObservations int) *Correlations {
	if minObservations < 2 {
		minObservations = 2
	}
	return &Correlations{pairs: map[pairKey]*pairStats{}, minObservations: minObservations}
}

// Observe records one event's evaluated verdicts.
//
// ids must be in canonical order and negTwoLogP must hold the matching −2 ln P values.
// Only pairs where both detectors evaluated contribute: an abstention is the absence of
// a statement, so it cannot inform how two detectors move together.
func (c *Correlations) Observe(ids []string, negTwoLogP []float64) {
	if len(ids) != len(negTwoLogP) {
		return
	}
	for i := range ids {
		for j := i + 1; j < len(ids); j++ {
			a, b := ids[i], ids[j]
			x, y := negTwoLogP[i], negTwoLogP[j]
			if a > b {
				a, b = b, a
				x, y = y, x
			}
			if math.IsNaN(x) || math.IsNaN(y) || math.IsInf(x, 0) || math.IsInf(y, 0) {
				continue
			}
			k := pairKey{a, b}
			p := c.pairs[k]
			if p == nil {
				p = &pairStats{}
				c.pairs[k] = p
			}
			p.n++
			p.sumX += x
			p.sumY += y
			p.sumXX += x * x
			p.sumYY += y * y
			p.sumXY += x * y
		}
	}
}

// PairEstimate is one pair's measured dependence, for the record.
type PairEstimate struct {
	A string `json:"a"`
	B string `json:"b"`
	// N is the number of events on which both detectors evaluated.
	N int `json:"n"`
	// Covariance is the direct sample covariance of −2 ln P, which is the term
	// equation (19) consumes.
	Covariance float64 `json:"covariance"`
	// Correlation is the Pearson correlation of the same pair, reported so the
	// dependence can be read on a scale that does not depend on the spread.
	Correlation float64 `json:"correlation"`
	// KostMcDermottCovariance is what the polynomial of [KostMcDermott] would give for
	// this correlation. It is recorded, not used: the direct estimate needs no
	// approximation when the paired values are in hand.
	KostMcDermottCovariance float64 `json:"kost_mcdermott_covariance"`
	// Used is false when N fell below the support threshold, in which case the pair
	// contributes zero covariance and degrades to independence.
	Used bool `json:"used"`
}

// CovarianceModel is a frozen estimate, safe to consult concurrently.
type CovarianceModel struct {
	cov       map[pairKey]float64
	estimates []PairEstimate
}

// Freeze closes the estimate. Pairs below the support threshold are recorded but
// excluded, so the record shows what was measured and what was declined.
func (c *Correlations) Freeze() *CovarianceModel {
	keys := make([]pairKey, 0, len(c.pairs))
	for k := range c.pairs {
		keys = append(keys, k)
	}
	// Sorted so both the emitted record and the float accumulation are reproducible.
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].a != keys[j].a {
			return keys[i].a < keys[j].a
		}
		return keys[i].b < keys[j].b
	})

	m := &CovarianceModel{cov: map[pairKey]float64{}}
	for _, k := range keys {
		p := c.pairs[k]
		est := PairEstimate{A: k.a, B: k.b, N: int(p.n)}
		if p.n >= float64(c.minObservations) {
			// Sample covariance and Pearson correlation from the streaming moments.
			covariance := (p.sumXY - p.sumX*p.sumY/p.n) / (p.n - 1)
			varX := (p.sumXX - p.sumX*p.sumX/p.n) / (p.n - 1)
			varY := (p.sumYY - p.sumY*p.sumY/p.n) / (p.n - 1)
			est.Covariance = covariance
			if varX > 0 && varY > 0 {
				est.Correlation = covariance / math.Sqrt(varX*varY)
			}
			est.KostMcDermottCovariance = KostMcDermott(est.Correlation)
			if !math.IsNaN(covariance) && !math.IsInf(covariance, 0) {
				est.Used = true
				m.cov[k] = covariance
			}
		}
		m.estimates = append(m.estimates, est)
	}
	return m
}

// Estimates returns the per-pair record, in a fixed order.
func (m *CovarianceModel) Estimates() []PairEstimate {
	out := make([]PairEstimate, len(m.estimates))
	copy(out, m.estimates)
	return out
}

// Matrix builds the J×J covariance for the detectors that evaluated on one event, in
// the order given.
//
// A pair with no usable estimate contributes zero, which is exactly Fisher's
// assumption for that pair: the correction falls back per pair rather than wholesale,
// so a detector newly added to the composition does not discard the dependence already
// measured among the others.
func (m *CovarianceModel) Matrix(ids []string) [][]float64 {
	n := len(ids)
	out := make([][]float64, n)
	for i := range out {
		out[i] = make([]float64, n)
	}
	for i := range ids {
		for j := i + 1; j < n; j++ {
			a, b := ids[i], ids[j]
			if a > b {
				a, b = b, a
			}
			v := m.cov[pairKey{a, b}]
			out[i][j] = v
			out[j][i] = v
		}
	}
	return out
}
