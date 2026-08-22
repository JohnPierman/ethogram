// The full-size statistical simulations, at the sizes the issues state.
//
// They live behind a build tag because the coverage gate runs the whole suite with -race and
// -covermode=atomic, and instrumented Monte-Carlo blew a ten-minute CI timeout. The default
// suite keeps a smaller version of each, so the code paths stay covered and a regression still
// fails there; this file is where the numbers reported in the CHANGELOG, the README and the
// paper come from.
//
// Run with `make simulation`. It is the same split the repository already uses for `corpus` and
// `integration` tests, and for the same reason: a gate that times out is a gate people route
// around.

//go:build simulation

package calibration

import (
	"math"
	"math/rand"
	"sort"
	"testing"
)

// TestSimulationFDRIsControlledOverTheStream is #16's acceptance criterion at its stated size:
// at least 1,000 streams of at least 10,000 hypotheses, at non-null fractions of 0, 0.001, 0.01
// and 0.1. The all-null case is the one that matters, because a procedure controlling nothing
// looks fine on a stream full of signal.
func TestSimulationFDRIsControlledOverTheStream(t *testing.T) {
	const (
		streams = 1_000
		length  = 10_000
		q       = 0.1
	)

	for _, nonNull := range []float64{0, 0.001, 0.01, 0.1} {
		rng := rand.New(rand.NewSource(int64(1_000 + int(nonNull*100_000))))
		totalFDR := 0.0
		discoveries, truePositives := 0, 0

		for s := 0; s < streams; s++ {
			rule, err := NewLORD(0.005, q, DefaultGamma())
			if err != nil {
				t.Fatalf("NewLORD: %v", err)
			}
			false_, true_ := 0, 0
			for i := 0; i < length; i++ {
				isSignal := rng.Float64() < nonNull
				logP := math.Log(rng.Float64())
				if isSignal {
					logP -= 12
				}
				hit := rule.Test(logP)
				if !hit {
					continue
				}
				if isSignal {
					true_++
				} else {
					false_++
				}
			}
			discoveries += false_ + true_
			truePositives += true_
			if r := false_ + true_; r > 0 {
				totalFDR += float64(false_) / float64(r)
			}
		}

		realised := totalFDR / float64(streams)
		t.Logf("non-null %.3f: realised FDR %.4f at q = %g over %d streams of %d "+
			"(%d discoveries, %d true)", nonNull, realised, q, streams, length,
			discoveries, truePositives)

		// Monte-Carlo error on a mean of 1,000 stream-level ratios is at most
		// 0.5/sqrt(1000) ~ 0.016; the allowance is two standard errors.
		if realised > q+0.032 {
			t.Errorf("non-null %.3f: realised FDR %.4f exceeds q = %g beyond Monte-Carlo "+
				"error", nonNull, realised, q)
		}
	}
}

// TestSimulationCrossFittingSweep is the full λ sweep the reported table comes from.
//
// The claim it establishes is not "in-sample weighting is unsafe" but something sharper:
// discoveries come from p-values orders of magnitude below λ, so an estimator reading only
// above λ is nearly independent of the tests it reweights — which makes in-sample weighting
// almost safe at Storey's conventional λ = 0.5 by accident of λ, and unmistakably unsafe once λ
// falls into the region the discoveries come from.
func TestSimulationCrossFittingSweep(t *testing.T) {
	const (
		replicates = 1_000
		m          = 2_000
		q          = 0.1
		strata     = 20
		monteCarlo = 0.032
	)

	inSample := []float64{}
	for _, lambda := range []float64{0.5, 0.25, 0.1, 0.05, 0.02} {
		crossFitted := StratifiedOptions{Strata: strata, Folds: 5, Lambda: lambda, Seed: 1}
		naive := StratifiedOptions{Strata: strata, Folds: 1, Lambda: lambda, Seed: 1}

		gotCross := realisedFDR(t, replicates, m, crossFitted, q, 42)
		gotNaive := realisedFDR(t, replicates, m, naive, q, 42)
		inSample = append(inSample, gotNaive)
		t.Logf("lambda=%.2f  cross-fitted %.4f  in-sample %.4f  (q=%g)",
			lambda, gotCross, gotNaive, q)

		if gotCross > q+monteCarlo {
			t.Errorf("lambda=%.2f: cross-fitted realised FDR %.4f exceeds q = %g beyond the "+
				"Monte-Carlo allowance", lambda, gotCross, q)
		}
	}

	if inSample[len(inSample)-1] <= inSample[0] {
		t.Errorf("in-sample inflation does not grow as lambda falls (%.4f against %.4f), so "+
			"the stated mechanism is not the one operating",
			inSample[len(inSample)-1], inSample[0])
	}
	if inSample[len(inSample)-1] <= q+0.032 {
		t.Errorf("in-sample realised FDR at the smallest lambda is %.4f, which does not "+
			"clearly exceed q = %g: without a measured failure there is no case for "+
			"cross-fitting", inSample[len(inSample)-1], q)
	}
}

// TestSimulationHigherCriticismDoesNotInflate is the non-inflation table: the property that
// lets two entity-days of very different size be ranked against each other, and the one
// Fisher's sum lacks.
func TestSimulationHigherCriticismDoesNotInflate(t *testing.T) {
	rng := rand.New(rand.NewSource(17))
	const replicates = 400

	upper := map[int]float64{}
	sizes := []int{100, 1_000, 10_000}

	for _, n := range sizes {
		values := make([]float64, 0, replicates)
		nonPositive := 0
		for r := 0; r < replicates; r++ {
			logP := make([]float64, n)
			for i := range logP {
				logP[i] = math.Log(rng.Float64())
			}
			got, err := HigherCriticism(logP, n, DefaultAlpha0)
			if err != nil {
				t.Fatalf("n=%d replicate %d: %v", n, r, err)
			}
			if !got.Positive {
				nonPositive++
			}
			values = append(values, got.Statistic)
		}
		sort.Float64s(values)
		upper[n] = values[replicates*95/100]
		t.Logf("n=%6d  median HC* %+.3f  95th %+.3f  sqrt(2 ln ln n) %.3f  "+
			"non-positive days %d of %d",
			n, values[len(values)/2], upper[n], NullScale(n), nonPositive, replicates)
	}

	small, large := upper[sizes[0]], upper[sizes[len(sizes)-1]]
	if large > 2*small {
		t.Errorf("the 95th percentile grows from %.3f at n=%d to %.3f at n=%d, more than "+
			"doubling: a normalised statistic must not inflate with the number of tests",
			small, sizes[0], large, sizes[len(sizes)-1])
	}
}

// TestSimulationTopKFidelity is the measurement the retained tail depth of 32 is chosen from,
// at the size the reported table comes from.
func TestSimulationTopKFidelity(t *testing.T) {
	rng := rand.New(rand.NewSource(3232))
	const (
		replicates = 300
		n          = 5_000
	)

	for _, k := range []int{8, 16, 32, 64} {
		agree, disagree := 0, 0
		for r := 0; r < replicates; r++ {
			logP := make([]float64, n)
			for i := range logP {
				logP[i] = math.Log(rng.Float64())
			}
			for i := 0; i < 20; i++ {
				logP[i] = math.Log(rng.Float64() * 1e-3)
			}

			full, err := HigherCriticism(logP, n, DefaultAlpha0)
			if err != nil {
				t.Fatalf("k=%d replicate %d: %v", k, r, err)
			}
			smallest := append([]float64(nil), logP...)
			sort.Float64s(smallest)
			topK, err := HigherCriticism(smallest[:k], n, DefaultAlpha0)
			if err != nil {
				t.Fatalf("k=%d replicate %d: %v", k, r, err)
			}
			if topK.Rank == full.Rank && topK.LogStatistic == full.LogStatistic {
				agree++
			} else {
				disagree++
			}
		}
		t.Logf("k=%2d: exact agreement on %d of %d sparse days, disagreement on %d",
			k, agree, replicates, disagree)
		if k >= 32 && agree != replicates {
			t.Errorf("k=%d disagreed with the full statistic on %d of %d sparse days",
				k, disagree, replicates)
		}
	}
}

// TestSimulationHigherCriticismBeatsFisherOnASparseSignal is the head-to-head against the
// aggregate it replaces, at reporting size.
func TestSimulationHigherCriticismBeatsFisherOnASparseSignal(t *testing.T) {
	rng := rand.New(rand.NewSource(1704))
	const (
		replicates = 200
		quiet      = 2_000
		busy       = 8_000
		planted    = 25
	)

	fisher := func(logP []float64) float64 {
		total := 0.0
		for _, lp := range logP {
			total += -2 * lp
		}
		return total
	}
	day := func(n int, withSignal bool) []float64 {
		logP := make([]float64, n)
		for i := range logP {
			logP[i] = math.Log(rng.Float64())
		}
		if withSignal {
			for i := 0; i < planted; i++ {
				logP[i] = math.Log(rng.Float64() * 1e-4)
			}
		}
		return logP
	}

	hcWins, fisherWins := 0, 0
	for r := 0; r < replicates; r++ {
		signal := day(quiet, true)
		noise := day(busy, false)

		hcSignal, err := HigherCriticism(signal, quiet, DefaultAlpha0)
		if err != nil {
			t.Fatalf("replicate %d: %v", r, err)
		}
		hcNoise, err := HigherCriticism(noise, busy, DefaultAlpha0)
		if err != nil {
			t.Fatalf("replicate %d: %v", r, err)
		}
		if MoreExtreme(hcSignal, hcNoise) {
			hcWins++
		}
		if fisher(signal) > fisher(noise) {
			fisherWins++
		}
	}

	t.Logf("the signal-carrying day ranks first in %d of %d replicates under Higher "+
		"Criticism and %d of %d under Fisher's sum", hcWins, replicates, fisherWins, replicates)
	if hcWins < replicates || fisherWins > 0 {
		t.Errorf("Higher Criticism %d of %d, Fisher %d of %d: the separation this test "+
			"reports is not the one it measured", hcWins, replicates, fisherWins, replicates)
	}
}
