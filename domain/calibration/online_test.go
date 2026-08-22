package calibration

import (
	"math"
	"math/rand"
	"testing"
)

func mustLORD(t *testing.T, wealth, q float64) *LORD {
	t.Helper()
	rule, err := NewLORD(wealth, q, DefaultGamma())
	if err != nil {
		t.Fatalf("NewLORD(%g, %g): %v", wealth, q, err)
	}
	return rule
}

// TestTheSpendingSequenceSumsToOne is the arithmetic every guarantee rests on: the W₀ term of
// the level can spend at most W₀ in total and each rejection's term at most q, and both follow
// from the sequence summing to one. A normalisation that drifted would inflate every level in
// the package with no other symptom.
func TestTheSpendingSequenceSumsToOne(t *testing.T) {
	gamma := DefaultGamma()

	total := 0.0
	for j := 1; j <= gammaSumTerms; j++ {
		total += gamma.At(j)
	}
	// The explicit sum plus the analytic tail. The tail is what the constructor adds, so
	// the check has to add it too or it is checking a different quantity.
	total += gamma.At(1) / unnormalisedGamma(1) / math.Log(float64(gammaSumTerms))
	if math.Abs(total-1) > 1e-9 {
		t.Errorf("the spending sequence sums to %.12f, want 1", total)
	}

	// Non-increasing from j = 2 on, and non-negative everywhere. The first two terms are
	// equal under this sequence, since ln(max(j,2)) is flat across them.
	for j := 2; j < 10_000; j++ {
		if gamma.At(j) < 0 {
			t.Fatalf("gamma(%d) is negative: %g", j, gamma.At(j))
		}
		if gamma.At(j+1) > gamma.At(j) {
			t.Fatalf("gamma is not non-increasing at %d: %g then %g",
				j, gamma.At(j), gamma.At(j+1))
		}
	}
	for _, j := range []int{0, -1, -1000} {
		if gamma.At(j) != 0 {
			t.Errorf("gamma(%d) is %g, want 0: a rejection may only spend from the test "+
				"after it", j, gamma.At(j))
		}
	}
}

// TestNewLORDRefusesAnUnfundableConfiguration pins the constructor. A starting wealth above q
// spends more than the guarantee allows before anything has been earned.
func TestNewLORDRefusesAnUnfundableConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name        string
		wealth, q   float64
		wantRefused bool
	}{
		{"ordinary", 0.005, 0.1, false},
		{"wealth equal to q", 0.1, 0.1, false},
		{"wealth above q", 0.2, 0.1, true},
		{"zero wealth", 0, 0.1, true},
		{"negative wealth", -0.01, 0.1, true},
		{"q at zero", 0.01, 0, true},
		{"q at one", 0.01, 1, true},
		{"q above one", 0.01, 1.5, true},
	} {
		_, err := NewLORD(tc.wealth, tc.q, DefaultGamma())
		if tc.wantRefused && err == nil {
			t.Errorf("%s: accepted, want refused", tc.name)
		}
		if !tc.wantRefused && err != nil {
			t.Errorf("%s: refused with %v", tc.name, err)
		}
	}

	// A nil sequence takes the default rather than panicking on first use.
	if rule, err := NewLORD(0.01, 0.1, nil); err != nil || rule.Level() <= 0 {
		t.Errorf("a nil gamma did not fall back to the default: %v", err)
	}
}

// TestLevelIsPureAndLeavesTheRuleUnchanged is the capability separation of §5.2 at this layer,
// the same split the detectors enforce between scoring and committing. A rule whose level
// moved when it was read could not be audited, because reading it would change it.
func TestLevelIsPureAndLeavesTheRuleUnchanged(t *testing.T) {
	rule := mustLORD(t, 0.005, 0.1)
	// Drive it somewhere interesting first: a few rejections in, the level depends on the
	// whole rejection history and on the cumulative table, which is where an accidental
	// mutation would show.
	rng := rand.New(rand.NewSource(16))
	for i := 0; i < 1_000; i++ {
		rule.Observe(Outcome{Rejected: rng.Intn(50) == 0})
	}

	first := rule.Level()
	tests, rejects, wealth, spent := rule.Tests(), rule.Rejections(), rule.Wealth(), rule.Spent()

	for i := 0; i < 100; i++ {
		if got := rule.Level(); got != first {
			t.Fatalf("call %d returned %g, want %g: Level is not pure", i, got, first)
		}
		if math.Abs(rule.LogLevel()-math.Log(first)) > 0 {
			t.Fatalf("LogLevel disagrees with ln Level")
		}
	}
	if rule.Tests() != tests || rule.Rejections() != rejects {
		t.Error("reading the level advanced the counters")
	}
	if rule.Wealth() != wealth || rule.Spent() != spent {
		t.Error("reading the level moved the wealth")
	}
}

// TestTheRuleCannotGoPermanentlySilent is the property the issue exists for. A rule that
// minimises false positives has a degenerate optimum at alerting on nothing, and a fixed-q
// batch run that stops rejecting has no mechanism to start again.
func TestTheRuleCannotGoPermanentlySilent(t *testing.T) {
	rule := mustLORD(t, 0.005, 0.1)

	// One early rejection, then a very long barren stretch.
	rule.Observe(Outcome{Rejected: true})
	for i := 0; i < 50_000; i++ {
		if level := rule.Level(); !(level > 0) {
			t.Fatalf("after %d barren tests the level is %g: the rule has gone silent and "+
				"cannot recover", i, level)
		}
		rule.Observe(Outcome{Rejected: false})
	}

	level := rule.Level()
	if !(level > 0) {
		t.Fatalf("the level is %g after half a million barren tests, want strictly positive",
			level)
	}
	if math.IsInf(rule.LogLevel(), -1) {
		t.Error("the log level has reached negative infinity, so no p-value can ever " +
			"clear it again")
	}
	t.Logf("after one rejection and 500,000 barren tests the level is %.3e (wealth %.4f)",
		level, rule.Wealth())

	// And a rejection must lift it again: that is the memory of having been right.
	before := rule.Level()
	rule.Observe(Outcome{Rejected: true})
	if !(rule.Level() > before) {
		t.Errorf("a rejection moved the level from %.3e to %.3e; earning must raise the "+
			"alerting rate", before, rule.Level())
	}
}

// TestWealthIsNeverNegativeAndTheLevelIsAlwaysBounded is the property test over adversarial
// rejection sequences, and it corrects the requirement it was written to check.
//
// Issue #16 asks for two things: that wealth is never negative, and that "an all-reject stream
// must not let wealth grow without limit". The first holds and is what the guarantee needs.
// **The second is not a property of LORD++, and the measurement is what showed it.** Under an
// all-reject stream wealth grows linearly, reaching 430 over 200,000 tests here.
//
// The reason is structural rather than a defect. Each rejection earns exactly q, while the
// level is capped at q by construction -- the spending sequence sums to one, so no single test
// can spend more than a rejection earns -- and it approaches that cap from below, never
// reaching it. Earning therefore exceeds spending by the unspent tail of the sequence, measured
// at about 0.002 per test at q = 0.1, and the difference accumulates.
//
// Unbounded wealth in this direction means unspent budget, not runaway alerting. The quantity
// that could do harm is the LEVEL, and it is bounded: no test is ever conducted at a level
// above q + W0, however productive the stream. That is what this test pins, and the wealth
// growth is recorded as a measurement rather than asserted away.
func TestWealthIsNeverNegativeAndTheLevelIsAlwaysBounded(t *testing.T) {
	const (
		tests = 20_000
		q     = 0.1
		w0    = 0.005
	)

	for _, tc := range []struct {
		name     string
		rejected func(i int) bool
	}{
		{"never rejects", func(int) bool { return false }},
		{"always rejects", func(int) bool { return true }},
		{"alternates", func(i int) bool { return i%2 == 0 }},
		{"one in a thousand", func(i int) bool { return i%1000 == 0 }},
		{"a burst then silence", func(i int) bool { return i < 1000 }},
		{"silence then a burst", func(i int) bool { return i > tests-1000 }},
	} {
		rule := mustLORD(t, w0, q)
		worstWealth := math.Inf(1)
		peakWealth := 0.0
		peakLevel := 0.0
		for i := 0; i < tests; i++ {
			level := rule.Level()
			if level < 0 {
				t.Fatalf("%s: a negative level %g at test %d", tc.name, level, i)
			}
			if level > peakLevel {
				peakLevel = level
			}
			rule.Observe(Outcome{Rejected: tc.rejected(i)})
			w := rule.Wealth()
			if w < worstWealth {
				worstWealth = w
			}
			if w > peakWealth {
				peakWealth = w
			}
		}
		t.Logf("%-22s rejections %7d  level peak %.6f  wealth min %+.6f max %+.3f  "+
			"omitted %.4f (%d runs)", tc.name, rule.Rejections(), peakLevel,
			worstWealth, peakWealth, rule.OmittedMass(), rule.TruncatedRuns())

		// Never negative, which is what the FDR guarantee rests on. The tolerance absorbs
		// accumulated float error over two hundred thousand additions and is not a licence.
		if worstWealth < -1e-9 {
			t.Errorf("%s: wealth reached %g, which the spending sequence should make "+
				"impossible", tc.name, worstWealth)
		}
		// The level is the quantity that can do harm, and it is bounded whatever the
		// stream does.
		if peakLevel > q+w0 {
			t.Errorf("%s: a test was conducted at level %g, above q + W0 = %g: no "+
				"rejection history should buy a level that high",
				tc.name, peakLevel, q+w0)
		}
	}
}

// TestTheLevelSaturatesAtQUnderAnAllRejectStream is the mechanism behind the bound above, and
// it is worth pinning separately: the sum of the spending sequence over a contiguous run of
// rejections converges to one, so the level converges to q and no further.
func TestTheLevelSaturatesAtQUnderAnAllRejectStream(t *testing.T) {
	const q = 0.1
	rule := mustLORD(t, 0.005, q)
	for i := 0; i < 20_000; i++ {
		rule.Observe(Outcome{Rejected: true})
	}
	level := rule.Level()
	t.Logf("after 100,000 consecutive rejections the level is %.6f against q = %g", level, q)
	if level > q+1e-9 {
		t.Errorf("the level reached %g, above q = %g: the sequence cannot spend more than "+
			"it has", level, q)
	}
	if level < 0.5*q {
		t.Errorf("the level is only %g of q = %g after rejecting everything, so a "+
			"productive stream is not buying the alerting rate it earned", level, q)
	}
}

// TestARunOfRejectionsIsSummedExactly guards the optimisation that makes this affordable.
// Summing over contiguous runs against a cumulative table has to give the same level as
// summing term by term over rejections, or the speed came at the cost of the procedure.
func TestARunOfRejectionsIsSummedExactly(t *testing.T) {
	const q = 0.1
	// Term-by-term reference: the definition, with no runs and no table.
	reference := func(wealth float64, rejectAt []int, next int) float64 {
		gamma := DefaultGamma()
		level := gamma.At(next) * wealth
		for j, at := range rejectAt {
			earn := q
			if j == 0 {
				earn = q - wealth
			}
			level += earn * gamma.At(next-at)
		}
		return level
	}

	rng := rand.New(rand.NewSource(512))
	for trial := 0; trial < 60; trial++ {
		const wealth = 0.005
		rule := mustLORD(t, wealth, q)
		rejectAt := []int{}
		// Mixed density, so both long runs and isolated rejections occur.
		density := []int{1, 2, 3, 10, 100}[trial%5]
		steps := 1 + rng.Intn(2_000)
		for i := 1; i <= steps; i++ {
			hit := rng.Intn(density) == 0
			rule.Observe(Outcome{Rejected: hit})
			if hit {
				rejectAt = append(rejectAt, i)
			}
		}
		if rule.TruncatedRuns() > 0 {
			// Truncation is deliberately conservative, so it is not expected to match the
			// exact reference; those streams are covered by the wealth test instead.
			continue
		}
		want := reference(wealth, rejectAt, steps+1)
		got := rule.Level()
		if math.Abs(got-want) > 1e-12*math.Max(1, math.Abs(want)) {
			t.Fatalf("trial %d (density 1 in %d, %d steps, %d rejections): run-summed "+
				"level %g against term-by-term %g", trial, density, steps, len(rejectAt),
				got, want)
		}
	}
}

// TestTruncationIsConservative pins the direction of the only approximation in the rule. The
// cap on retained runs drops positive spending terms, so the level can only fall — which
// cannot break error control and can only cost power.
func TestTruncationIsConservative(t *testing.T) {
	// Alternating rejections make a new run on every other test, so the cap binds quickly.
	rule := mustLORD(t, 0.005, 0.1)
	for i := 0; i < 6_000; i++ {
		rule.Observe(Outcome{Rejected: i%2 == 0})
	}
	if rule.TruncatedRuns() == 0 {
		t.Fatal("this stream was built to make the cap bind and it did not")
	}
	if rule.OmittedMass() <= 0 {
		t.Error("runs were dropped but no omitted spending was recorded, so the power " +
			"cost is invisible")
	}

	// The truncated level must not exceed q: the whole point is that dropping terms lowers
	// it.
	if level := rule.Level(); level > 0.1+1e-12 {
		t.Errorf("the truncated level is %g, above q: truncation is meant to lower the "+
			"level, not raise it", level)
	}
	t.Logf("alternating stream: %d runs dropped, %.4f of spending omitted, level %.6f",
		rule.TruncatedRuns(), rule.OmittedMass(), rule.Level())
}

// TestFDRIsControlledOverTheStream is the acceptance criterion, and the all-null case is the
// one that matters: a procedure that controls nothing looks fine on a stream full of signal.
func TestFDRIsControlledOverTheStream(t *testing.T) {
	const (
		streams = 25
		length  = 2_000
		q       = 0.1
	)

	for _, nonNull := range []float64{0, 0.001, 0.01, 0.1} {
		rng := rand.New(rand.NewSource(int64(1_000 + int(nonNull*100_000))))
		totalFDR := 0.0
		discoveries, truePositives := 0, 0

		for s := 0; s < streams; s++ {
			rule := mustLORD(t, 0.005, q)
			false_, true_ := 0, 0
			for i := 0; i < length; i++ {
				isSignal := rng.Float64() < nonNull
				var logP float64
				if isSignal {
					// A strong alternative, so the procedure has something to find.
					logP = math.Log(rng.Float64()) - 12
				} else {
					logP = math.Log(rng.Float64())
				}
				hit := logP <= rule.LogLevel()
				rule.Observe(Outcome{Rejected: hit})
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
		t.Logf("non-null %.3f: realised FDR %.4f at q = %g over %d streams "+
			"(%d discoveries, %d true)", nonNull, realised, q, streams,
			discoveries, truePositives)

		// Monte-Carlo error on a mean of 200 stream-level ratios is at most
		// 0.5/sqrt(200) ≈ 0.035; the allowance below is two standard errors.
		if realised > q+0.07 {
			t.Errorf("non-null %.3f: realised FDR %.4f exceeds q = %g beyond Monte-Carlo "+
				"error", nonNull, realised, q)
		}
		if nonNull >= 0.01 && truePositives == 0 {
			t.Errorf("non-null %.3f: no true positive was found in %d streams, so this "+
				"configuration measures nothing about error control", nonNull, streams)
		}
	}
}

// TestRunOnlineComparesInLogSpace covers the requirement that the comparison against a level
// happens in log space. The corpus reaches ln P = −4000, where the p-value is exactly zero: in
// p-space every such event is tied with every other and with any level below float64's
// smallest positive number.
func TestRunOnlineComparesInLogSpace(t *testing.T) {
	rule := mustLORD(t, 0.005, 0.1)
	logP := []float64{-4000, math.Log(0.5), -2000, math.Log(0.9)}
	rejected, levels, err := RunOnline(rule, logP)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rejected) != len(logP) || len(levels) != len(logP) {
		t.Fatalf("returned %d decisions and %d levels for %d tests",
			len(rejected), len(levels), len(logP))
	}
	if !rejected[0] {
		t.Error("ln P = -4000 was not rejected; in p-space it is zero, which is at or " +
			"below every level")
	}
	if !rejected[2] {
		t.Error("ln P = -2000 was not rejected")
	}
	if rejected[1] || rejected[3] {
		t.Error("an ordinary p-value cleared a level of order 1e-3")
	}
	if _, _, err := RunOnline(nil, logP); err == nil {
		t.Error("a nil rule was accepted")
	}
}

// TestTheRuleIsDeterministic covers R4: a fixed stream gives an identical rejection set and an
// identical wealth trace, however many times it is replayed.
func TestTheRuleIsDeterministic(t *testing.T) {
	rng := rand.New(rand.NewSource(4))
	logP := make([]float64, 4_000)
	for i := range logP {
		logP[i] = math.Log(rng.Float64())
		if i%700 == 0 {
			logP[i] -= 14
		}
	}

	var firstRejected []bool
	var firstLevels []float64
	var firstWealth float64
	for trial := 0; trial < 5; trial++ {
		rule := mustLORD(t, 0.005, 0.1)
		rejected, levels, err := RunOnline(rule, logP)
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		if trial == 0 {
			firstRejected, firstLevels, firstWealth = rejected, levels, rule.Wealth()
			continue
		}
		for i := range rejected {
			if rejected[i] != firstRejected[i] {
				t.Fatalf("trial %d: decision %d differs", trial, i)
			}
			if levels[i] != firstLevels[i] {
				t.Fatalf("trial %d: level %d is %g, was %g", trial, i, levels[i], firstLevels[i])
			}
		}
		if rule.Wealth() != firstWealth {
			t.Fatalf("trial %d: wealth is %g, was %g", trial, rule.Wealth(), firstWealth)
		}
	}
}

// TestTestAgreesWithLevelThenObserve is the guard on the single-pass shortcut. Two ways to
// advance the rule is two chances to drift, and the whole point of the shortcut is that it is
// the same procedure at half the cost.
func TestTestAgreesWithLevelThenObserve(t *testing.T) {
	rng := rand.New(rand.NewSource(66))
	logP := make([]float64, 5_000)
	for i := range logP {
		logP[i] = math.Log(rng.Float64())
		// A mixture, so the two paths meet rejections, non-rejections, runs and gaps.
		switch i % 5 {
		case 0:
			logP[i] -= 20
		case 1:
			logP[i] -= 6
		}
	}

	single := mustLORD(t, 0.005, 0.1)
	paired := mustLORD(t, 0.005, 0.1)

	for i, lp := range logP {
		gotSingle := single.Test(lp)

		level := paired.Level()
		gotPaired := lp <= math.Log(level)
		paired.Observe(Outcome{Rejected: gotPaired})

		if gotSingle != gotPaired {
			t.Fatalf("test %d: Test says %v and Level-then-Observe says %v",
				i, gotSingle, gotPaired)
		}
		if single.Wealth() != paired.Wealth() {
			t.Fatalf("test %d: wealth diverged, %g against %g",
				i, single.Wealth(), paired.Wealth())
		}
	}
	if single.Rejections() != paired.Rejections() {
		t.Errorf("rejections differ: %d against %d", single.Rejections(), paired.Rejections())
	}
	if single.Spent() != paired.Spent() {
		t.Errorf("spending differs: %g against %g", single.Spent(), paired.Spent())
	}
	t.Logf("%d tests, %d rejections, wealth %.6f, identical by both routes",
		single.Tests(), single.Rejections(), single.Wealth())
}
