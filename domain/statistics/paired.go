package statistics

import (
	"math"
	"sort"
)

// McNemarResult is the paired comparison of two detectors' binary outcomes on the
// same events.
type McNemarResult struct {
	// BothDetected and NeitherDetected are the concordant pairs, which carry no
	// information about the difference and do not enter the statistic.
	BothDetected    int `json:"both_detected"`
	NeitherDetected int `json:"neither_detected"`

	// OnlyA and OnlyB are the discordant pairs: the events one arm caught and the
	// other missed. The whole test lives here.
	OnlyA int `json:"only_a"`
	OnlyB int `json:"only_b"`

	// Statistic is the continuity-corrected χ² on one degree of freedom, or the exact
	// binomial when the discordant count is small.
	Statistic float64 `json:"statistic"`
	PValue    float64 `json:"p_value"`
	Exact     bool    `json:"exact"`

	// Delta is OnlyA − OnlyB: how many more events arm A caught than arm B.
	Delta int `json:"delta"`
}

// McNemar tests whether two arms differ in detection on the same events.
//
// §12.3 calls E4 and E9 "direct ablation on identical data", and E1 compares the
// framework against baselines at a matched budget over the same corpus. In every case
// the two arms are measured on the same events, so the observations are paired and an
// unpaired test would be answering a different question with the wrong variance: the
// events both arms agree on tell us nothing about which is better, and only the
// discordant pairs carry evidence.
//
// detectedA and detectedB must be aligned: index i is the same event in both.
//
// With few discordant pairs the χ² approximation is unreliable, so the exact binomial
// test is used when their total is below 25; the result records which was applied.
func McNemar(detectedA, detectedB []bool) McNemarResult {
	var r McNemarResult
	n := min(len(detectedA), len(detectedB))
	for i := range n {
		switch {
		case detectedA[i] && detectedB[i]:
			r.BothDetected++
		case detectedA[i] && !detectedB[i]:
			r.OnlyA++
		case !detectedA[i] && detectedB[i]:
			r.OnlyB++
		default:
			r.NeitherDetected++
		}
	}
	r.Delta = r.OnlyA - r.OnlyB

	discordant := r.OnlyA + r.OnlyB
	if discordant == 0 {
		// The arms agree on every event: no evidence of a difference, and the
		// statistic is undefined rather than zero-with-significance.
		r.PValue = 1
		return r
	}

	if discordant < 25 {
		r.Exact = true
		r.PValue = exactBinomialTwoSided(r.OnlyA, discordant)
		return r
	}

	// Edwards' continuity correction, the standard form for the χ² approximation.
	diff := math.Abs(float64(r.OnlyA-r.OnlyB)) - 1
	if diff < 0 {
		diff = 0
	}
	r.Statistic = diff * diff / float64(discordant)
	r.PValue = chiSquareOneDFSurvival(r.Statistic)
	return r
}

// exactBinomialTwoSided returns the two-sided exact binomial p-value for k successes
// in n trials under p = 1/2, summing the tail of outcomes no more likely than the one
// observed.
func exactBinomialTwoSided(k, n int) float64 {
	if n == 0 {
		return 1
	}
	observed := binomialPMFHalf(k, n)
	total := 0.0
	for i := 0; i <= n; i++ {
		p := binomialPMFHalf(i, n)
		// The 1e-12 relative slack admits the mirror outcome, whose probability is
		// equal in exact arithmetic but may differ in the last bits.
		if p <= observed*(1+1e-12) {
			total += p
		}
	}
	return math.Min(1, total)
}

// binomialPMFHalf returns C(n,k)/2^n, evaluated through log-gamma so large n does not
// overflow the binomial coefficient.
func binomialPMFHalf(k, n int) float64 {
	lg1, _ := math.Lgamma(float64(n) + 1)
	lg2, _ := math.Lgamma(float64(k) + 1)
	lg3, _ := math.Lgamma(float64(n-k) + 1)
	return math.Exp(lg1 - lg2 - lg3 - float64(n)*math.Ln2)
}

// chiSquareOneDFSurvival returns Pr(X ≥ x) for X ~ χ²(1), which is erfc(√(x/2)).
func chiSquareOneDFSurvival(x float64) float64 {
	if x <= 0 {
		return 1
	}
	return math.Erfc(math.Sqrt(x / 2))
}

// BootstrapDelta is a paired bootstrap interval for the difference between two arms'
// detection counts.
type BootstrapDelta struct {
	Observed     float64 `json:"observed_delta"`
	Low          float64 `json:"low"`
	High         float64 `json:"high"`
	Level        float64 `json:"level"`
	Resamples    int     `json:"resamples"`
	Seed         uint64  `json:"seed"`
	ExcludesZero bool    `json:"excludes_zero"`
}

// PairedBootstrapDelta resamples paired outcomes to bound the difference in detection
// counts between two arms.
//
// The resampling is over EVENTS, not over the two arms separately: an event is drawn
// once and both arms' outcomes for it travel together, which is what preserves the
// pairing. Resampling the arms independently would inflate the interval by the
// between-arm variance the pairing exists to remove.
//
// The generator is an explicit counter-based hash seeded from the caller, so the
// interval is reproducible from the seed recorded in the result file. This is
// deliberately not math/rand: the domain forbids it, and a global generator would
// make the interval depend on evaluation order.
func PairedBootstrapDelta(detectedA, detectedB []bool, resamples int, seed uint64) BootstrapDelta {
	n := min(len(detectedA), len(detectedB))
	out := BootstrapDelta{Level: 0.95, Resamples: resamples, Seed: seed}
	if n == 0 || resamples <= 0 {
		return out
	}

	observedA, observedB := 0, 0
	for i := range n {
		if detectedA[i] {
			observedA++
		}
		if detectedB[i] {
			observedB++
		}
	}
	out.Observed = float64(observedA - observedB)

	// The loop counters are non-negative by construction, so the conversions below
	// are reinterpretations rather than arithmetic; they index the draw, they do not
	// compute with it.
	un := uint64(n) //nolint:gosec // n > 0, checked above
	deltas := make([]float64, 0, resamples)
	for r := range resamples {
		a, b := 0, 0
		for i := range n {
			idx := int(splitmix64(seed, uint64(r), uint64(i)) % un) //nolint:gosec // non-negative loop counters
			if detectedA[idx] {
				a++
			}
			if detectedB[idx] {
				b++
			}
		}
		deltas = append(deltas, float64(a-b))
	}
	sort.Float64s(deltas)

	out.Low = percentile(deltas, 0.025)
	out.High = percentile(deltas, 0.975)
	out.ExcludesZero = (out.Low > 0) || (out.High < 0)
	return out
}

// percentile returns the value at the given fraction of a sorted slice, by the
// nearest-rank convention, which needs no interpolation rule to be reproducible.
func percentile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(q * float64(len(sorted)))
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// splitmix64 is a counter-based generator: a pure function of its inputs, so a
// resample index maps to the same draw on every run and on every machine.
func splitmix64(seed, stream, counter uint64) uint64 {
	x := seed ^ (stream * 0x9E3779B97F4A7C15) ^ (counter * 0xBF58476D1CE4E5B9)
	x ^= x >> 30
	x *= 0xBF58476D1CE4E5B9
	x ^= x >> 27
	x *= 0x94D049BB133111EB
	x ^= x >> 31
	return x
}
