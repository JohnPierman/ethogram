package burst_test

import (
	"math"
	"math/rand/v2"
	"sort"
	"testing"

	"github.com/JohnPierman/ethogram/domain/burst"
	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/novelty"
)

// noDecay is a half-life long enough that nothing decays over a test's span, so what a test
// measures is the statistic rather than the discount.
const noDecay = novelty.HalfLife(1 << 62)

// poissonStream feeds a state a homogeneous Poisson stream at the given rate, evaluating after
// each arrival once the arm stops abstaining, and returns the corrected log p-values.
func poissonStream(rng *rand.Rand, rate float64, arrivals int) []float64 {
	s := &burst.State{}
	at := event.Timestamp(0)
	out := make([]float64, 0, arrivals)
	for i := 0; i < arrivals; i++ {
		// Exponential gap, rounded to whole seconds as a real corpus records them.
		gap := math.Ceil(rng.ExpFloat64() / rate)
		at += event.Timestamp(gap) * event.Second
		s.Observe(at, noDecay)
		if scan, abstain := burst.Evaluate(s); abstain == burst.AbstainNone {
			out = append(out, scan.LogP)
		}
	}
	return out
}

// TestTheNullIsCalibratedOnItsOwnProcess is the check #53 requires before any detection claim,
// and it is the check `volume` and the partitioned `cooccurrence` arm both failed: 24.7% and
// 99.0% of scored events below 1e−12, which no correct null produces.
//
// The arm's null says these are p-values under a homogeneous Poisson process, so feeding it one
// must produce values no smaller than uniform predicts. The correction is deliberately
// conservative — Šidák over nested, positively dependent windows — so the distribution should sit
// at or above uniform rather than on it, and the test pins the direction as well as the size.
func TestTheNullIsCalibratedOnItsOwnProcess(t *testing.T) {
	rng := rand.New(rand.NewPCG(53, 53))
	const arrivals = 60_000

	for _, rate := range []float64{1.0 / 60, 1.0 / 600, 1.0 / 3600} {
		logPs := poissonStream(rng, rate, arrivals)
		if len(logPs) < arrivals/2 {
			t.Fatalf("rate %g: only %d of %d arrivals produced a scan; the abstention gate is "+
				"swallowing the sample this test needs", rate, len(logPs), arrivals)
		}

		// The fractions below each level, against what uniform predicts.
		for _, level := range []float64{0.5, 0.1, 0.01, 1e-3, 1e-4} {
			logLevel := math.Log(level)
			below := 0
			for _, lp := range logPs {
				if lp <= logLevel {
					below++
				}
			}
			got := float64(below) / float64(len(logPs))
			// Conservative is the requirement: at or below the nominal fraction, with room
			// for Monte-Carlo error at the small levels. A null that fired MORE often than
			// uniform is the defect this test exists to catch.
			allowance := 3 * math.Sqrt(level*(1-level)/float64(len(logPs)))
			if got > level+allowance {
				t.Errorf("rate %g: %.5f of scans fall below p = %g, above the %g uniform "+
					"predicts: the null is anti-conservative, which is the defect that "+
					"disqualified two other arms", rate, got, level, level)
			}
		}

		// And nothing at all in the region a detection claim would live in. A correct null
		// puts one event in 1e12 below 1e−12; on 60,000 draws, seeing any is a defect.
		deep := 0
		for _, lp := range logPs {
			if lp <= math.Log(1e-12) {
				deep++
			}
		}
		if deep > 0 {
			t.Errorf("rate %g: %d of %d scans on a pure Poisson stream fall below 1e-12",
				rate, deep, len(logPs))
		}
		t.Logf("rate %-8g scans %6d  below 0.1: %.4f  below 0.01: %.4f  below 1e-4: %.5f",
			rate, len(logPs), fractionBelow(logPs, 0.1), fractionBelow(logPs, 0.01),
			fractionBelow(logPs, 1e-4))
	}
}

func fractionBelow(logPs []float64, level float64) float64 {
	logLevel := math.Log(level)
	below := 0
	for _, lp := range logPs {
		if lp <= logLevel {
			below++
		}
	}
	return float64(below) / float64(len(logPs))
}

// TestABurstIsDetectedAndItsSizeIsNamed is the alternative the arm exists for: twelve arrivals at
// ninety-second intervals inside an account whose own rate is far slower. That is the planted
// low-and-slow mechanism exactly, and every existing arm reaches 0 of 288 on it.
func TestABurstIsDetectedAndItsSizeIsNamed(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 7))
	// A quiet account: one event every twenty minutes on average, established over a week.
	const rate = 1.0 / 1200
	s := &burst.State{}
	at := event.Timestamp(0)
	for i := 0; i < 500; i++ {
		at += event.Timestamp(math.Ceil(rng.ExpFloat64()/rate)) * event.Second
		s.Observe(at, noDecay)
	}
	quiet, abstain := burst.Evaluate(s)
	if abstain != burst.AbstainNone {
		t.Fatalf("the arm abstained on an established account: %v", abstain)
	}

	// Now the plant: twelve arrivals ninety seconds apart.
	var burstScan burst.Scan
	for i := 0; i < 12; i++ {
		at += 90 * event.Second
		s.Observe(at, noDecay)
		burstScan, abstain = burst.Evaluate(s)
		if abstain != burst.AbstainNone {
			t.Fatalf("the arm abstained mid-burst: %v", abstain)
		}
	}

	t.Logf("quiet ln p %.2f; after a twelve-event burst ln p %.2f at window %d over %.0f s "+
		"(rate %.2e/s, %d windows examined)", quiet.LogP, burstScan.LogP, burstScan.Window,
		burstScan.SpanSeconds, burstScan.Rate, burstScan.Windows)

	if !(burstScan.LogP < quiet.LogP) {
		t.Errorf("the burst scored ln p %g against the quiet account's %g; a burst must be "+
			"more surprising than the stream it interrupts", burstScan.LogP, quiet.LogP)
	}
	// The bar is a realised alert cut, not the 1e-12 background pile. Those are different
	// numbers and the first version of this test used the wrong one: 1e-12 is the level at
	// which `volume`'s miscalibrated null was piling up background, whereas the cut a 1000-a-day
	// budget actually lands on is nearer 1e-5. An arm has to clear the cut, not the pile.
	if burstScan.LogP > math.Log(1e-6) {
		t.Errorf("the burst scored ln p %g, above 1e-6: the realised cut at 1000 alerts a day "+
			"is nearer 1e-5, so an arm that cannot beat 1e-6 on its own designed alternative "+
			"would not surface it", burstScan.LogP)
	}
	// The window it names should be the burst, not an accident of the buffer.
	if burstScan.Window < 8 {
		t.Errorf("the minimum was attained at a window of %d arrivals; a twelve-event burst "+
			"should be found at a window near twelve", burstScan.Window)
	}
	if burstScan.SpanSeconds > 12*90 {
		t.Errorf("the named span is %.0f s, wider than the burst it should have found",
			burstScan.SpanSeconds)
	}
}

// TestTheCorrectionIsChargedAndIsConservative pins that the Šidák correction is applied over the
// windows examined and moves the p-value the safe way. An uncorrected scan statistic is the
// anti-conservative null that disqualified two other arms.
func TestTheCorrectionIsChargedAndIsConservative(t *testing.T) {
	rng := rand.New(rand.NewPCG(11, 11))
	s := &burst.State{}
	at := event.Timestamp(0)
	for i := 0; i < 200; i++ {
		at += event.Timestamp(math.Ceil(rng.ExpFloat64()*300)) * event.Second
		s.Observe(at, noDecay)
	}
	for i := 0; i < 10; i++ {
		at += 60 * event.Second
		s.Observe(at, noDecay)
	}
	scan, abstain := burst.Evaluate(s)
	if abstain != burst.AbstainNone {
		t.Fatalf("abstained: %v", abstain)
	}

	if scan.Windows < burst.MaxWindow-burst.MinWindow {
		t.Errorf("only %d windows were charged with %d arrivals held; the correction must "+
			"charge every window the scan looked at", scan.Windows, len(s.Recent))
	}
	if !(scan.LogP > scan.LogMinP) {
		t.Errorf("the corrected ln p %g is not above the uncorrected %g; a multiplicity "+
			"correction that does not raise the p-value is not being applied",
			scan.LogP, scan.LogMinP)
	}
}

// TestAbstentionsAreOutcomesNotErrors covers R3 at this arm, and specifically that the sample-size
// gate counts undiscounted gaps.
//
// A gate on the discounted count is the defect #37 fixed in three arms at once: a discounted count
// saturates at 1/(1−δ), so a minimum above that ceiling can never be reached however long the
// entity is watched. Here a short half-life is used with far more than MinGaps arrivals, and the
// arm must still form an opinion.
func TestAbstentionsAreOutcomesNotErrors(t *testing.T) {
	if _, abstain := burst.Evaluate(nil); abstain != burst.AbstainTooFewArrivals {
		t.Errorf("a nil state gave %v", abstain)
	}
	if _, abstain := burst.Evaluate(&burst.State{}); abstain != burst.AbstainTooFewArrivals {
		t.Errorf("an empty state gave %v", abstain)
	}

	// Enough arrivals for a window but not enough gaps for a rate.
	few := &burst.State{}
	for i := 1; i <= burst.MinWindow+1; i++ {
		few.Observe(event.Timestamp(i)*60*event.Second, noDecay)
	}
	if _, abstain := burst.Evaluate(few); abstain != burst.AbstainTooFewGaps {
		t.Errorf("four arrivals gave %v, want the too-few-gaps abstention", abstain)
	}

	// Simultaneous arrivals contribute no gaps, so no rate — and must not divide by zero.
	tied := &burst.State{}
	for i := 0; i < burst.MinGaps+10; i++ {
		tied.Observe(1000*event.Second, noDecay)
	}
	if scan, abstain := burst.Evaluate(tied); abstain == burst.AbstainNone {
		t.Errorf("a stream of simultaneous arrivals produced a scan: %+v", scan)
	}

	// A short half-life must not make the gate unsatisfiable. Two-minute half-life, hourly
	// arrivals: the discounted count saturates near one and the undiscounted one keeps
	// counting, so the arm must speak.
	short := novelty.HalfLife(120 * event.Second)
	sparse := &burst.State{}
	for i := 1; i <= burst.MinGaps+20; i++ {
		sparse.Observe(event.Timestamp(i)*3600*event.Second, short)
	}
	if sparse.Count > 3 {
		t.Fatalf("the discounted gap count reached %g under a two-minute half-life; this test "+
			"assumes it saturates near one", sparse.Count)
	}
	if _, abstain := burst.Evaluate(sparse); abstain != burst.AbstainNone {
		t.Errorf("with %d observed gaps and a discounted count of %.2f the arm gave %v: the "+
			"gate must count undiscounted gaps or it is unsatisfiable rather than slow",
			sparse.Observed, sparse.Count, abstain)
	}
}

// TestStateIsBounded is §13.3: the per-entity state must not grow with the number of events.
func TestStateIsBounded(t *testing.T) {
	s := &burst.State{}
	for i := 1; i <= 100_000; i++ {
		s.Observe(event.Timestamp(i)*event.Second, noDecay)
	}
	if len(s.Recent) != burst.MaxWindow {
		t.Errorf("holding %d timestamps after 100,000 arrivals, want %d",
			len(s.Recent), burst.MaxWindow)
	}
	if s.Observed != 99_999 {
		t.Errorf("counted %d gaps from 100,000 arrivals, want 99,999", s.Observed)
	}
}

// TestEvaluateIsDeterministic covers R4: identical arrivals give an identical scan, and the
// window attaining the minimum is chosen by a fixed order.
func TestEvaluateIsDeterministic(t *testing.T) {
	rng := rand.New(rand.NewPCG(4, 4))
	arrivals := make([]event.Timestamp, 300)
	at := event.Timestamp(0)
	for i := range arrivals {
		at += event.Timestamp(math.Ceil(rng.ExpFloat64()*120)) * event.Second
		arrivals[i] = at
	}

	var first burst.Scan
	for trial := 0; trial < 5; trial++ {
		s := &burst.State{}
		for _, a := range arrivals {
			s.Observe(a, noDecay)
		}
		got, abstain := burst.Evaluate(s)
		if abstain != burst.AbstainNone {
			t.Fatalf("trial %d abstained: %v", trial, abstain)
		}
		if trial == 0 {
			first = got
			continue
		}
		if got != first {
			t.Fatalf("trial %d gave %+v, want %+v", trial, got, first)
		}
	}
}

// TestARaisedRateMakesAShortSpanLessSurprising pins the direction of the estimator's
// self-inclusion, which is the one place this arm departs from §5.2's score-then-commit order.
//
// The scan must include the arrival that completes a burst, so the rate it is judged against is
// formed from gaps that include the burst too. That is conservative: a burst raises the estimated
// rate, and a higher rate makes a short span less surprising. Stated in the package comment and
// checked here, because a reader is entitled to know which way an unusual choice leans.
// The first version of this test raised the rate by inserting 120 arrivals ten seconds apart,
// which is itself a far tighter burst than the one it then appended -- so the scan found the
// inserted arrivals and the comparison measured two changes at once. It reported the opposite of
// the truth and the detector was correct throughout. Isolating the rate means varying only the
// background interval and leaving the buffer's final contents identical.
func TestARaisedRateMakesAShortSpanLessSurprising(t *testing.T) {
	build := func(background event.Timestamp) burst.Scan {
		s := &burst.State{}
		at := event.Timestamp(0)
		for i := 0; i < 400; i++ {
			at += background * event.Second
			s.Observe(at, noDecay)
		}
		// The same burst in both, and nothing else close: the only difference reaching the
		// scan is the rate the span is judged against.
		for i := 0; i < 6; i++ {
			at += 90 * event.Second
			s.Observe(at, noDecay)
		}
		scan, abstain := burst.Evaluate(s)
		if abstain != burst.AbstainNone {
			t.Fatalf("abstained at background %d: %v", background, abstain)
		}
		return scan
	}

	quiet, busy := build(1200), build(300)
	t.Logf("background 1200 s: rate %.2e, ln p %.2f | background 300 s: rate %.2e, ln p %.2f",
		quiet.Rate, quiet.LogP, busy.Rate, busy.LogP)

	if !(busy.Rate > quiet.Rate) {
		t.Fatalf("the busier background did not raise the rate: %g against %g",
			busy.Rate, quiet.Rate)
	}
	if quiet.SpanSeconds != busy.SpanSeconds || quiet.Window != busy.Window {
		t.Fatalf("the two scans found different windows (%d over %.0f s against %d over "+
			"%.0f s), so this comparison is not isolating the rate",
			quiet.Window, quiet.SpanSeconds, busy.Window, busy.SpanSeconds)
	}
	if !(busy.LogP > quiet.LogP) {
		t.Errorf("a higher rate gave ln p %g against %g: a busier account must find the same "+
			"span less surprising, or the self-inclusion leans the unsafe way",
			busy.LogP, quiet.LogP)
	}
}

// TestTheDeepTailDoesNotFloor is the log-space requirement. A tight burst on a quiet account
// reaches ln p below −1000, where the p-value itself is zero and every such event ties with every
// other — the defect four other sites in this codebase already had.
func TestTheDeepTailDoesNotFloor(t *testing.T) {
	build := func(interval event.Timestamp) burst.Scan {
		s := &burst.State{}
		at := event.Timestamp(0)
		for i := 0; i < 400; i++ {
			at += 7200 * event.Second
			s.Observe(at, noDecay)
		}
		for i := 0; i < burst.MaxWindow; i++ {
			at += interval * event.Second
			s.Observe(at, noDecay)
		}
		scan, abstain := burst.Evaluate(s)
		if abstain != burst.AbstainNone {
			t.Fatalf("abstained: %v", abstain)
		}
		return scan
	}

	tight, tighter := build(4), build(1)
	t.Logf("32 arrivals at 4 s: ln p %.1f; at 1 s: ln p %.1f", tight.LogP, tighter.LogP)
	if math.Exp(tight.LogP) != 0 {
		t.Skip("this corpus shape does not underflow, so it does not exercise the floor")
	}
	if !(tighter.LogP < tight.LogP) {
		t.Errorf("a tighter burst gave ln p %g against %g; in p-space both are zero and the "+
			"log is what has to separate them", tighter.LogP, tight.LogP)
	}
	if math.IsInf(tighter.LogP, 0) || math.IsNaN(tighter.LogP) {
		t.Errorf("the tighter burst gave ln p %g, which no result file can carry", tighter.LogP)
	}
}

// TestScansSortIntoAUsefulOrder is a small sanity check that the statistic ranks: a set of
// streams with increasingly tight bursts must come out in that order.
func TestScansSortIntoAUsefulOrder(t *testing.T) {
	intervals := []event.Timestamp{600, 300, 120, 60, 30}
	logPs := make([]float64, len(intervals))
	for i, interval := range intervals {
		s := &burst.State{}
		at := event.Timestamp(0)
		for j := 0; j < 300; j++ {
			at += 1200 * event.Second
			s.Observe(at, noDecay)
		}
		for j := 0; j < 10; j++ {
			at += interval * event.Second
			s.Observe(at, noDecay)
		}
		scan, abstain := burst.Evaluate(s)
		if abstain != burst.AbstainNone {
			t.Fatalf("interval %d abstained: %v", interval, abstain)
		}
		logPs[i] = scan.LogP
	}
	sorted := append([]float64(nil), logPs...)
	sort.Sort(sort.Reverse(sort.Float64Slice(sorted)))
	for i := range logPs {
		if logPs[i] != sorted[i] {
			t.Fatalf("tighter bursts do not score lower: %v", logPs)
		}
	}
}

// TestTiedArrivalsCarryNoEvidence is the larger of this arm's two calibration corrections, and it
// is the one that changed the most.
//
// LANL records to the whole second and one logical action emits several authentication rows, so an
// entity routinely has many arrivals sharing an instant. The first version of this arm stored them
// all and floored a zero-length window at one second, calling that the conservative reading. It was
// not conservative at all: thirty-two arrivals inside one second is astronomically improbable under
// any renewal null however wide, so the log's own granularity became the most extreme evidence in
// the corpus. Measured on 230,699 real scored events, the share below 1e-12 was 5.77% with ties
// floored and 0.021% with ties collapsed.
//
// The contract now is that a repeated instant is not stored, so the scan cannot testify about
// structure the log did not record. Two things must hold: the p-value stays representable, and a
// run of tied arrivals is not extreme.
func TestTiedArrivalsCarryNoEvidence(t *testing.T) {
	s := &burst.State{}
	at := event.Timestamp(0)
	for i := 0; i < 200; i++ {
		at += 300 * event.Second
		s.Observe(at, noDecay)
	}
	held := len(s.Recent)
	observed := s.Observed

	// Six more rows at the instant of the last arrival, exactly as a corpus records one logon
	// that produced several authentications.
	for i := 0; i < 6; i++ {
		s.Observe(at, noDecay)
	}
	if len(s.Recent) != held {
		t.Errorf("six tied arrivals grew the scan buffer from %d to %d instants; a repeated "+
			"timestamp must not be stored, or the log's resolution re-enters the statistic",
			held, len(s.Recent))
	}
	if s.Observed != observed {
		t.Errorf("tied arrivals contributed %d gaps; a zero gap is not evidence about a rate",
			s.Observed-observed)
	}

	scan, abstain := burst.Evaluate(s)
	if abstain != burst.AbstainNone {
		t.Fatalf("abstained after tied arrivals: %v", abstain)
	}
	t.Logf("after six tied arrivals: ln p %.4f at window %d over %.0f s",
		scan.LogP, scan.Window, scan.SpanSeconds)

	if math.IsInf(scan.LogP, 0) || math.IsNaN(scan.LogP) {
		t.Fatalf("ln p is %g, which no verdict will accept and no result file can carry",
			scan.LogP)
	}
	if scan.SpanSeconds < burst.ResolutionSeconds {
		t.Errorf("the named span is %.0f s, below the corpus resolution", scan.SpanSeconds)
	}
	// The decisive assertion: a pile of tied rows on an ordinary account must not be extreme.
	if scan.LogP < math.Log(1e-3) {
		t.Errorf("six rows sharing one timestamp scored ln p %g. The log cannot resolve "+
			"structure inside a second, so it cannot testify about it, and treating a tie as "+
			"a one-second burst is what put 5.77%% of real events below 1e-12", scan.LogP)
	}
}

// TestATieHeavyStreamDoesNotPileInTheDeepTail is the same correction measured in aggregate rather
// than at one event, on the shape a real corpus actually has: an ordinary rhythm where each arrival
// brings a handful of rows at the same instant.
func TestATieHeavyStreamDoesNotPileInTheDeepTail(t *testing.T) {
	rng := rand.New(rand.NewPCG(19, 19))
	s := &burst.State{}
	at := event.Timestamp(0)
	scans, deep := 0, 0
	for i := 0; i < 20_000; i++ {
		at += event.Timestamp(math.Ceil(rng.ExpFloat64()*300)) * event.Second
		// One logon, four rows.
		for r := 0; r < 4; r++ {
			s.Observe(at, noDecay)
			scan, abstain := burst.Evaluate(s)
			if abstain != burst.AbstainNone {
				continue
			}
			scans++
			if scan.LogP <= math.Log(1e-12) {
				deep++
			}
		}
	}
	t.Logf("%d scans over a tie-heavy stream, %d below 1e-12 (%.4f%%)", scans, deep,
		100*float64(deep)/float64(scans))
	if share := float64(deep) / float64(scans); share > 0.0005 {
		t.Errorf("%.4f%% of scans below 1e-12 on a stream whose only unusual feature is that "+
			"the log repeats a timestamp. That is the log being scored rather than the "+
			"entity", 100*share)
	}
}

// TestNoWindowEverYieldsANonFinitePValue sweeps the shapes a corpus actually produces —
// ties, unit gaps, huge gaps, and a rate at each extreme — and insists every one is
// representable. The verdict constructor rejects a non-finite p-value, so a single shape
// that produces one takes down a replay that has already run for an hour.
func TestNoWindowEverYieldsANonFinitePValue(t *testing.T) {
	for _, background := range []event.Timestamp{1, 2, 60, 3600, 86400} {
		for _, burstGap := range []event.Timestamp{0, 1, 2, 90, 3600, 86400} {
			s := &burst.State{}
			at := event.Timestamp(0)
			for i := 0; i < 120; i++ {
				at += background * event.Second
				s.Observe(at, noDecay)
			}
			for i := 0; i < burst.MaxWindow; i++ {
				at += burstGap * event.Second
				s.Observe(at, noDecay)
			}
			scan, abstain := burst.Evaluate(s)
			if abstain != burst.AbstainNone {
				continue // an abstention is a legitimate outcome; a bad number is not
			}
			if math.IsInf(scan.LogP, 0) || math.IsNaN(scan.LogP) {
				t.Errorf("background %d s, burst gap %d s: ln p = %g",
					background, burstGap, scan.LogP)
			}
			if scan.LogP > 0 {
				t.Errorf("background %d s, burst gap %d s: ln p = %g, which is a p-value "+
					"above one", background, burstGap, scan.LogP)
			}
		}
	}
}
