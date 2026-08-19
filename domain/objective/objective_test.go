package objective_test

import (
	"math"
	"testing"

	"github.com/JohnPierman/ethogram/domain/objective"
)

func mustOutcome(t *testing.T, tp, fp, labelled int) objective.Outcome {
	t.Helper()
	o, err := objective.NewOutcome(tp, fp, labelled)
	if err != nil {
		t.Fatalf("NewOutcome(%d, %d, %d): %v", tp, fp, labelled, err)
	}
	return o
}

// ---------------------------------------------------------------------------
// Outcome invariants
// ---------------------------------------------------------------------------

// TestOutcomeRejectsImpossibleCounts. An outcome is a value object and validates on
// construction, so a defective one cannot be scored: a negative count and, more usefully,
// more true positives than the population holds positives, which is the shape a
// double-counted label produces.
func TestOutcomeRejectsImpossibleCounts(t *testing.T) {
	for _, tc := range []struct {
		name           string
		tp, fp, labels int
	}{
		{"negative true positives", -1, 10, 46},
		{"negative false positives", 1, -1, 46},
		{"negative labelled", 1, 10, -1},
		{"more true positives than labelled", 47, 10, 46},
	} {
		if _, err := objective.NewOutcome(tc.tp, tc.fp, tc.labels); err == nil {
			t.Errorf("%s: accepted, want an error", tc.name)
		}
	}
}

// TestEmptyOutcomeIsValidAndScoresNothing. Alerting on nothing is a legitimate
// configuration and must be representable — it is the degenerate point every objective
// here has to be able to reject on its merits rather than by refusing to evaluate it.
func TestEmptyOutcomeIsValidAndScoresNothing(t *testing.T) {
	o := mustOutcome(t, 0, 0, 46)

	if o.Alerted() != 0 {
		t.Errorf("Alerted = %d, want 0", o.Alerted())
	}
	if got := o.Precision(); got != 0 {
		t.Errorf("Precision = %v, want 0 — an empty queue has no precision to speak of", got)
	}
	if got := o.Recall(); got != 0 {
		t.Errorf("Recall = %v, want 0", got)
	}
	if _, ok := o.Ratio(); ok {
		t.Error("Ratio must decline on an empty outcome rather than return 0/0")
	}
}

func TestOutcomeArithmetic(t *testing.T) {
	o := mustOutcome(t, 25, 175, 46)

	if got := o.Alerted(); got != 200 {
		t.Errorf("Alerted = %d, want 200", got)
	}
	if got, want := o.Precision(), 0.125; math.Abs(got-want) > 1e-12 {
		t.Errorf("Precision = %v, want %v", got, want)
	}
	if got, want := o.Recall(), 25.0/46.0; math.Abs(got-want) > 1e-12 {
		t.Errorf("Recall = %v, want %v", got, want)
	}
	ratio, ok := o.Ratio()
	if !ok {
		t.Fatal("Ratio declined on a well-formed outcome")
	}
	if want := 25.0 / 175.0; math.Abs(ratio-want) > 1e-12 {
		t.Errorf("Ratio = %v, want %v", ratio, want)
	}
}

// TestRatioDeclinesWithoutFalsePositives: TP/FP is unbounded, and a perfect queue is
// exactly where it stops being a number. Reporting +Inf would put a value in a JSON field
// that no consumer can compare, so the type declines instead.
func TestRatioDeclinesWithoutFalsePositives(t *testing.T) {
	if _, ok := mustOutcome(t, 5, 0, 46).Ratio(); ok {
		t.Error("Ratio must decline when there are no false positives")
	}
}

// TestLiftIsRelativeToTheBaseRate: precision alone cannot say whether a ranking is doing
// any work, because a 12.5% precision is excellent against a 2.6% base rate and worthless
// against a 50% one.
func TestLiftIsRelativeToTheBaseRate(t *testing.T) {
	o := mustOutcome(t, 25, 175, 46)

	got := o.Lift(46.0 / 1777.0)
	if want := 0.125 / (46.0 / 1777.0); math.Abs(got-want) > 1e-9 {
		t.Errorf("Lift = %v, want %v", got, want)
	}
	if o.Lift(0) != 0 {
		t.Error("Lift against a zero base rate must be 0, not an infinity")
	}
}

// ---------------------------------------------------------------------------
// The break-even ratio, which takes no parameter
// ---------------------------------------------------------------------------

// TestBreakEvenValueRatio is the quantity this package exists to report by default. It
// carries no chosen constant, so it can be recorded for every run without anyone having
// fixed a cost model: it states the exchange rate at which the operating point begins to
// pay, and leaves the operator to say whether their exchange rate clears it.
func TestBreakEvenValueRatio(t *testing.T) {
	got, ok := mustOutcome(t, 25, 175, 46).BreakEvenValueRatio()
	if !ok {
		t.Fatal("break-even declined on an outcome with true positives")
	}
	if want := 7.0; math.Abs(got-want) > 1e-12 {
		t.Errorf("break-even = %v, want %v — 175 false positives against 25 true", got, want)
	}
}

// TestBreakEvenDeclinesWithoutTruePositives: with no true positives there is no exchange
// rate that makes the queue worth reading, and the honest report is "none", not a large
// number that looks like a demanding threshold.
func TestBreakEvenDeclinesWithoutTruePositives(t *testing.T) {
	if _, ok := mustOutcome(t, 0, 175, 46).BreakEvenValueRatio(); ok {
		t.Error("break-even must decline when nothing true was found")
	}
}

// ---------------------------------------------------------------------------
// Utility
// ---------------------------------------------------------------------------

func TestUtilityRejectsANonPositiveValueRatio(t *testing.T) {
	for _, ratio := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		if _, err := objective.NewUtility(ratio); err == nil {
			t.Errorf("NewUtility(%v): accepted, want an error", ratio)
		}
	}
}

// TestUtilityScoreIsInUnitsOfOneFalsePositive. Only the ratio v/c is identifiable from
// counts, so the objective is stated in units of c and the operator supplies one number
// rather than two. U = v·TP − FP.
func TestUtilityScoreIsInUnitsOfOneFalsePositive(t *testing.T) {
	u, err := objective.NewUtility(10)
	if err != nil {
		t.Fatal(err)
	}

	if got, want := u.Score(mustOutcome(t, 25, 175, 46)), 75.0; math.Abs(got-want) > 1e-12 {
		t.Errorf("Score = %v, want %v", got, want)
	}
	if got := u.Score(mustOutcome(t, 0, 0, 46)); got != 0 {
		t.Errorf("the empty queue scores %v, want exactly 0", got)
	}
}

// TestUtilityAgreesWithTheBreakEvenRatio pins the identity that connects the two halves of
// this package: an operating point is worth showing exactly when its true-to-false ratio
// beats the operator's exchange rate, so U > 0 and TP/FP > 1/v are the same statement.
func TestUtilityAgreesWithTheBreakEvenRatio(t *testing.T) {
	o := mustOutcome(t, 25, 175, 46)
	breakEven, ok := o.BreakEvenValueRatio()
	if !ok {
		t.Fatal("break-even declined")
	}

	below, err := objective.NewUtility(breakEven * 0.99)
	if err != nil {
		t.Fatal(err)
	}
	above, err := objective.NewUtility(breakEven * 1.01)
	if err != nil {
		t.Fatal(err)
	}

	if below.IsWorthwhile(o) {
		t.Error("below the break-even the queue must not be worth reading")
	}
	if !above.IsWorthwhile(o) {
		t.Error("above the break-even the queue must be worth reading")
	}
	if at, _ := objective.NewUtility(breakEven); at.IsWorthwhile(o) {
		t.Error("exactly at the break-even the queue is a wash, so not worthwhile")
	}
}

// TestMinimumPrecision states the same threshold in the units an operator reads on a
// dashboard: with a true positive worth v false ones, a queue pays iff its precision
// exceeds 1/(1+v).
func TestMinimumPrecision(t *testing.T) {
	u, err := objective.NewUtility(7)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := u.MinimumPrecision(), 1.0/8.0; math.Abs(got-want) > 1e-12 {
		t.Errorf("MinimumPrecision = %v, want %v", got, want)
	}
}

// ---------------------------------------------------------------------------
// Choosing an operating point
// ---------------------------------------------------------------------------

// TestBestRejectsTheDegenerateCorner is the whole reason this package is not a bare
// TP/FP ratio.
//
// These are the measured operating points of `lanl-conformal-d7-9-001`. Ranked by TP/FP
// the winner is the first: one true positive, one false positive, a ratio of 1.0 — and two
// alerts, finding 1 of 46 campaign-days. It satisfies "maximise TP/FP subject to TP > 0"
// and is useless. Under utility at any exchange rate above the break-even it loses to the
// full queue, because utility rewards true positives found rather than the purity of the
// queue they arrive in.
func TestBestRejectsTheDegenerateCorner(t *testing.T) {
	measured := []objective.Outcome{
		mustOutcome(t, 1, 1, 46),    // 1 alert/day
		mustOutcome(t, 1, 19, 46),   // 10/day
		mustOutcome(t, 4, 46, 46),   // 25/day
		mustOutcome(t, 12, 88, 46),  // 50/day
		mustOutcome(t, 25, 175, 46), // 100/day
	}

	// Ranked purely on TP/FP the corner wins, which is the failure mode.
	bestRatio, bestRatioAt := -1.0, -1
	for i, o := range measured {
		if r, ok := o.Ratio(); ok && r > bestRatio {
			bestRatio, bestRatioAt = r, i
		}
	}
	if bestRatioAt != 0 {
		t.Fatalf("fixture: TP/FP should peak at the corner, peaked at index %d", bestRatioAt)
	}

	u, err := objective.NewUtility(10)
	if err != nil {
		t.Fatal(err)
	}
	at, ok := u.Best(measured)
	if !ok {
		t.Fatal("Best found no operating point")
	}
	if at != len(measured)-1 {
		t.Errorf("utility chose index %d, want the full queue at %d", at, len(measured)-1)
	}
}

// TestBestFollowsTheExchangeRate: the choice is the operator's, expressed in one number,
// and a low enough valuation genuinely does prefer the small clean queue. That is not a
// defect — it is what "a true positive is barely worth more than a false one" means.
func TestBestFollowsTheExchangeRate(t *testing.T) {
	measured := []objective.Outcome{
		mustOutcome(t, 1, 1, 46),
		mustOutcome(t, 25, 175, 46),
	}

	// Crossover: 25v − 175 > v − 1  <=>  v > 7.25.
	for _, tc := range []struct {
		ratio float64
		want  int
	}{
		{5, 0},
		{7, 0},
		{8, 1},
		{20, 1},
	} {
		u, err := objective.NewUtility(tc.ratio)
		if err != nil {
			t.Fatal(err)
		}
		got, ok := u.Best(measured)
		if !ok {
			t.Fatalf("v/c = %v: no operating point chosen", tc.ratio)
		}
		if got != tc.want {
			t.Errorf("v/c = %v: chose index %d, want %d", tc.ratio, got, tc.want)
		}
	}
}

// TestBestBreaksTiesTowardsTheSmallerQueue. Two configurations of equal utility are not
// equal to an analyst, and the ordering of the slice is the budget ordering, so the
// tie-break must be the first index and must be deterministic (R4) rather than depending
// on iteration order.
func TestBestBreaksTiesTowardsTheSmallerQueue(t *testing.T) {
	// v/c = 2: (1 TP, 0 FP) scores 2, and (2 TP, 2 FP) also scores 2.
	tied := []objective.Outcome{
		mustOutcome(t, 1, 0, 46),
		mustOutcome(t, 2, 2, 46),
	}
	u, err := objective.NewUtility(2)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := u.Best(tied)
	if !ok {
		t.Fatal("no operating point chosen")
	}
	if got != 0 {
		t.Errorf("tie chose index %d, want the earlier (smaller) queue at 0", got)
	}
}

// TestBestDeclinesOnNoCandidates keeps the caller honest: there is no sensible default
// operating point, so an empty candidate set is a declined answer and not index zero.
func TestBestDeclinesOnNoCandidates(t *testing.T) {
	u, err := objective.NewUtility(10)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := u.Best(nil); ok {
		t.Error("Best must decline when given no candidates")
	}
}

// TestBestPrefersAlertingOnNothingWhenNothingPays. If every candidate has negative
// utility the objective must be allowed to say so, because that is the finding — at this
// exchange rate the detector is not worth deploying. Silently returning the least bad
// queue would hide it.
func TestBestPrefersAlertingOnNothingWhenNothingPays(t *testing.T) {
	losing := []objective.Outcome{
		mustOutcome(t, 1, 100, 46),
		mustOutcome(t, 2, 300, 46),
	}
	u, err := objective.NewUtility(2)
	if err != nil {
		t.Fatal(err)
	}

	at, ok := u.Best(losing)
	if !ok {
		t.Fatal("Best declined on a non-empty candidate set")
	}
	if u.Score(losing[at]) >= 0 {
		t.Fatalf("fixture: expected every candidate to lose, index %d scores %v",
			at, u.Score(losing[at]))
	}
	if u.IsWorthwhile(losing[at]) {
		t.Error("the best of a losing set must still not be reported as worthwhile")
	}
}
