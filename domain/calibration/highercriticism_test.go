package calibration

import (
	"math"
	"math/rand"
	"sort"
	"testing"
)

// TestHigherCriticismMatchesTheDefinitionByHand computes the statistic on a small set the
// long way, so a regression in the log-space arithmetic is visible against the formula
// rather than against a previous run of the same code.
func TestHigherCriticismMatchesTheDefinitionByHand(t *testing.T) {
	pValues := []float64{0.001, 0.02, 0.3, 0.4, 0.55, 0.6, 0.7, 0.8, 0.9, 0.95}
	n := len(pValues)
	logP := make([]float64, n)
	for i, p := range pValues {
		logP[i] = math.Log(p)
	}

	// The whole range, so every rank is examined.
	got, err := HigherCriticism(logP, n, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sorted := append([]float64(nil), pValues...)
	sort.Float64s(sorted)
	want, wantRank := math.Inf(-1), 0
	for i := 1; i <= n; i++ {
		p := sorted[i-1]
		term := math.Sqrt(float64(n)) * (float64(i)/float64(n) - p) / math.Sqrt(p*(1-p))
		if term > want {
			want, wantRank = term, i
		}
	}

	if !got.Positive {
		t.Fatalf("the statistic came back non-positive; the by-hand maximum is %g", want)
	}
	if math.Abs(got.Statistic-want)/want > 1e-12 {
		t.Errorf("statistic is %g, want %g", got.Statistic, want)
	}
	if got.Rank != wantRank {
		t.Errorf("attaining rank is %d, want %d", got.Rank, wantRank)
	}
	if math.Abs(got.LogStatistic-math.Log(want)) > 1e-12 {
		t.Errorf("log statistic is %g, want %g", got.LogStatistic, math.Log(want))
	}
	if got.N != n || got.Considered != n {
		t.Errorf("N=%d Considered=%d, want %d and %d", got.N, got.Considered, n, n)
	}
	if got.Truncated {
		t.Error("a complete input was reported as truncated")
	}
}

// TestTheLogarithmSurvivesInputsTheStatisticCannot is the reason the log is the primary
// representation. At ln P = −4000 the term is of order e^2000, far above float64's ceiling,
// and two such days must still be separable — in p-space both p-values are exactly zero and
// therefore tied, which is the defect this design exists to avoid.
func TestTheLogarithmSurvivesInputsTheStatisticCannot(t *testing.T) {
	deep := make([]float64, 100)
	for i := range deep {
		deep[i] = math.Log(0.5)
	}
	deep[0] = -4000

	deeper := append([]float64(nil), deep...)
	deeper[0] = -8000

	a, err := HigherCriticism(deep, len(deep), DefaultAlpha0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b, err := HigherCriticism(deeper, len(deeper), DefaultAlpha0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if math.Exp(deep[0]) != 0 {
		t.Fatal("this test assumes ln P = -4000 underflows to zero in p-space")
	}
	if !math.IsInf(a.Statistic, 1) || !math.IsInf(b.Statistic, 1) {
		t.Errorf("statistics are %g and %g; both should overflow, which is why the "+
			"logarithm is the field to rank on", a.Statistic, b.Statistic)
	}
	if !(b.LogStatistic > a.LogStatistic) {
		t.Errorf("ln P = -8000 gives log statistic %g against -4000's %g: the deeper tail "+
			"must rank ahead, and in p-space both are zero and would tie",
			b.LogStatistic, a.LogStatistic)
	}
	if !MoreExtreme(b, a) || MoreExtreme(a, b) {
		t.Error("MoreExtreme does not order two overflowed statistics")
	}
}

// TestAnUnderflowedPValueIsAcceptedNotRefused pins the boundary case the corpus produces
// constantly: a p-value that reached zero. Refusing it would reject exactly the events the
// framework exists to surface.
func TestAnUnderflowedPValueIsAcceptedNotRefused(t *testing.T) {
	logP := []float64{math.Inf(-1), math.Log(0.4), math.Log(0.8)}
	got, err := HigherCriticism(logP, len(logP), 1)
	if err != nil {
		t.Fatalf("an exactly-zero p-value was refused: %v", err)
	}
	if !got.Positive || !math.IsInf(got.LogStatistic, 1) {
		t.Errorf("a p-value of exactly zero gave log statistic %g, want +Inf: the term "+
			"is genuinely infinite", got.LogStatistic)
	}
	if got.Rank != 1 {
		t.Errorf("the maximum is at rank %d, want 1", got.Rank)
	}
}

// TestAQuietDayHasNoPositiveTermAndSaysSo covers the outcome that is easy to confuse with
// an error: every p-value above its own rank fraction. The day was quieter than uniform,
// which is well defined and entirely ordinary.
func TestAQuietDayHasNoPositiveTermAndSaysSo(t *testing.T) {
	logP := make([]float64, 50)
	for i := range logP {
		logP[i] = math.Log(0.97)
	}
	got, err := HigherCriticism(logP, len(logP), DefaultAlpha0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Positive {
		t.Fatalf("a day of p = 0.97 reported a positive statistic of %g", got.Statistic)
	}
	if math.IsInf(got.Statistic, 0) || math.IsNaN(got.Statistic) {
		t.Errorf("the non-positive statistic is %g, want a finite negative number",
			got.Statistic)
	}
	if got.Statistic >= 0 {
		t.Errorf("the statistic is %g, want negative", got.Statistic)
	}
	// And it must rank behind any day that did produce a positive term.
	busy, err := HigherCriticism([]float64{math.Log(0.001), math.Log(0.5)}, 50, DefaultAlpha0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !MoreExtreme(busy, got) {
		t.Error("a day with a positive statistic does not rank ahead of one without")
	}
}

// TestHigherCriticismRefusesIncoherentInput fixes what is a caller error and what is data.
func TestHigherCriticismRefusesIncoherentInput(t *testing.T) {
	ok := []float64{math.Log(0.1), math.Log(0.5)}
	for _, tc := range []struct {
		name   string
		logP   []float64
		n      int
		alpha0 float64
	}{
		{"empty", nil, 10, 0.1},
		{"no tests", ok, 0, 0.1},
		{"negative tests", ok, -1, 0.1},
		{"fewer tests than p-values", ok, 1, 0.1},
		{"p-value above one", []float64{0.5}, 10, 0.1},
		{"NaN", []float64{math.NaN()}, 10, 0.1},
		{"alpha0 at zero", ok, 10, 0},
		{"alpha0 above one", ok, 10, 1.5},
		{"alpha0 negative", ok, 10, -0.1},
	} {
		if _, err := HigherCriticism(tc.logP, tc.n, tc.alpha0); err == nil {
			t.Errorf("%s: accepted, want refused", tc.name)
		}
	}

	// A log p-value of exactly zero is p = 1, which is coherent.
	if _, err := HigherCriticism([]float64{0, math.Log(0.5)}, 10, 0.1); err != nil {
		t.Errorf("p = 1 was refused: %v", err)
	}
}

// TestTheInputIsNotMutated matters because the caller is holding an entity-day's retained
// tail and will use it again for the other two aggregations.
func TestTheInputIsNotMutated(t *testing.T) {
	logP := []float64{math.Log(0.7), math.Log(0.01), math.Log(0.3)}
	before := append([]float64(nil), logP...)
	if _, err := HigherCriticism(logP, 100, DefaultAlpha0); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for i := range before {
		if logP[i] != before[i] {
			t.Fatalf("input %d changed from %g to %g", i, before[i], logP[i])
		}
	}
}

// TestTheStatisticDoesNotInflateWithTheNumberOfTests is the property the whole issue turns
// on, and it is the one Fisher's sum lacks: the statistic's null distribution must be
// essentially the same for a day of a hundred events and a day of ten thousand, so that two
// entity-days of very different size can be ranked against each other at all.
//
// The theoretical scale is sqrt(2 ln ln n), which grows from 1.75 at n = 100 to 2.11 at
// n = 10,000 — a 21% change across two orders of magnitude. Fisher's sum over the same range
// grows a hundredfold. The test pins that the measured null tracks the slow rate and not the
// fast one.
//
// A null day frequently has no positive term at all: the smallest of n uniforms sits above
// 1/n about half the time, so the maximum over the first few ranks is often negative. That
// is not a degenerate case, it is what makes the statistic discriminating, and an earlier
// version of this test treated it as a failure and was wrong to.
func TestTheStatisticDoesNotInflateWithTheNumberOfTests(t *testing.T) {
	rng := rand.New(rand.NewSource(17))
	const replicates = 400

	medians := map[int]float64{}
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
			if math.IsInf(got.Statistic, 0) || math.IsNaN(got.Statistic) {
				t.Fatalf("n=%d replicate %d: a uniform sample gave %g, which no null day "+
					"should reach", n, r, got.Statistic)
			}
			values = append(values, got.Statistic)
		}
		sort.Float64s(values)
		medians[n] = values[len(values)/2]
		upper[n] = values[replicates*95/100]
		t.Logf("n=%6d  median HC* %+.3f  95th %+.3f  sqrt(2 ln ln n) %.3f  "+
			"non-positive days %d of %d",
			n, medians[n], upper[n], NullScale(n), nonPositive, replicates)
	}

	// The 95th percentile is the quantity a threshold would be set from, so it is the one
	// that must not run away with n. Across two orders of magnitude the theoretical scale
	// moves by 21%; allowing a factor of two absorbs Monte-Carlo error and the discreteness
	// of the alpha0 cap while still failing loudly on a lost normalisation, which would
	// move this by a factor of ten or more.
	small, large := upper[sizes[0]], upper[sizes[len(sizes)-1]]
	if large > 2*small {
		t.Errorf("the 95th percentile grows from %.3f at n=%d to %.3f at n=%d, more than "+
			"doubling: a normalised statistic must not inflate with the number of tests, "+
			"which is the whole reason this replaces Fisher's sum",
			small, sizes[0], large, sizes[len(sizes)-1])
	}
	// And it must move in the same direction as the theory rather than shrinking away,
	// which would mean the alpha0 cap is swallowing the maximum.
	if large < small/2 {
		t.Errorf("the 95th percentile falls from %.3f at n=%d to %.3f at n=%d, which the "+
			"theoretical scale does not do", small, sizes[0], large, sizes[len(sizes)-1])
	}
}

// TestASparseSignalBeatsTheNullAndFisherDoesNot is the claim the whole issue rests on: a
// sparse cluster of moderate anomalies inside an ordinary day is the alternative Fisher's
// sum dilutes and Higher Criticism is optimal against.
//
// It compares both statistics on the same days, because "HC finds it" is only interesting
// beside "the statistic it replaces does not".
func TestASparseSignalBeatsTheNullAndFisherDoesNot(t *testing.T) {
	rng := rand.New(rand.NewSource(1704))
	const (
		replicates = 200
		quiet      = 2_000 // a busy but ordinary entity-day
		busy       = 8_000 // a busier one, with the same sparse signal
		planted    = 25
	)

	// Fisher's statistic at entity scope, the aggregate this replaces.
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
		// The comparison that matters: a small day carrying the signal against a large day
		// carrying none. An aggregate whose scale is set by traffic ranks the large one
		// first; one normalised by the number of tests does not.
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

	if hcWins < replicates {
		t.Errorf("Higher Criticism ranked the signal first in only %d of %d replicates",
			hcWins, replicates)
	}
	if fisherWins > 0 {
		t.Errorf("Fisher's sum ranked the signal first in %d of %d replicates; this test "+
			"is built so that it never should, since its scale is set by the four-times "+
			"larger day", fisherWins, replicates)
	}
}

// TestTopKReproducesTheFullStatisticOnASparseSignal is the bounded-storage question, and it
// is answered by measurement rather than by assertion because that is what issue #17 asks
// for: retaining every p-value per entity-day would break the property that an entity-day is
// fixed size.
//
// The measured answer at k = 32 on a sparse alternative is exact agreement. The failure mode
// is a dense signal, which the second half of this test exhibits deliberately — and there
// the result is reported as truncated rather than as a complete maximum.
func TestTopKReproducesTheFullStatisticOnASparseSignal(t *testing.T) {
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
			// Sparse: 20 planted events in 5,000, which is the regime a campaign presents.
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
			if !topK.Truncated {
				t.Fatalf("k=%d: a %d-of-%d prefix was not reported as truncated", k, k, n)
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
			t.Errorf("k=%d disagreed with the full statistic on %d of %d sparse days; "+
				"the retention bound has to be faithful in the regime it is chosen for",
				k, disagree, replicates)
		}
	}

	// The dense case, where the maximum genuinely falls beyond a small k. The point is not
	// that top-k is exact here — it is not — but that the result says so.
	dense := make([]float64, n)
	for i := range dense {
		dense[i] = math.Log(rng.Float64())
	}
	for i := 0; i < n/4; i++ {
		dense[i] = math.Log(rng.Float64() * 0.05)
	}
	full, err := HigherCriticism(dense, n, DefaultAlpha0)
	if err != nil {
		t.Fatalf("dense: %v", err)
	}
	sort.Float64s(dense)
	topK, err := HigherCriticism(dense[:8], n, DefaultAlpha0)
	if err != nil {
		t.Fatalf("dense top-k: %v", err)
	}
	if !topK.Truncated {
		t.Error("the dense top-k result is not marked truncated")
	}
	if full.Rank <= 8 {
		t.Skipf("this dense sample happened to attain its maximum at rank %d, inside the "+
			"prefix, so it does not exhibit the failure mode", full.Rank)
	}
	if topK.LogStatistic == full.LogStatistic {
		t.Error("a truncated maximum matched the full one on a dense signal, so this test " +
			"is not exhibiting what it claims")
	}
}

// TestHigherCriticismIsDeterministic covers R4 including the tie case, where many p-values
// are exactly equal and the sort must not be free to reorder them.
func TestHigherCriticismIsDeterministic(t *testing.T) {
	rng := rand.New(rand.NewSource(4))
	logP := make([]float64, 500)
	for i := range logP {
		// A coarse grid, so exact ties are common.
		logP[i] = math.Log(float64(1+rng.Intn(20)) / 20)
	}
	first, err := HigherCriticism(logP, len(logP), DefaultAlpha0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for trial := 0; trial < 20; trial++ {
		// Shuffle the input: the statistic is a function of the multiset, so a permutation
		// must not change it.
		rng.Shuffle(len(logP), func(i, j int) { logP[i], logP[j] = logP[j], logP[i] })
		again, err := HigherCriticism(logP, len(logP), DefaultAlpha0)
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		if again != first {
			t.Fatalf("trial %d: a permutation of the same input gave %+v, want %+v",
				trial, again, first)
		}
	}
}

// TestNullScaleIsDefinedAtTheSmallSizes covers the entity-days a real corpus is full of:
// accounts with one, two or three events on a day.
func TestNullScaleIsDefinedAtTheSmallSizes(t *testing.T) {
	for _, n := range []int{-1, 0, 1, 2} {
		if got := NullScale(n); got != 0 {
			t.Errorf("NullScale(%d) = %g, want 0 where ln ln n is undefined", n, got)
		}
	}
	if got := NullScale(3); got <= 0 || math.IsNaN(got) {
		t.Errorf("NullScale(3) = %g, want a positive number", got)
	}
	if NullScale(10_000) <= NullScale(100) {
		t.Error("the null scale does not grow with n")
	}
}
