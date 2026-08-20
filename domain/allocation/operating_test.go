package allocation_test

import (
	"math"
	"testing"

	"github.com/JohnPierman/ethogram/domain/allocation"
	"github.com/JohnPierman/ethogram/domain/objective"
)

func mustUtility(t *testing.T, v float64) objective.Utility {
	t.Helper()
	u, err := objective.NewUtility(v)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestThresholdRefusesAnImpossiblePrior(t *testing.T) {
	u := mustUtility(t, 10)
	for _, p := range []float64{0, 1, -0.5, 1.5, math.NaN()} {
		if _, err := allocation.Threshold(u, p); err == nil {
			t.Errorf("base rate %v was accepted; (0, 1) is the admissible range", p)
		}
	}
}

// TestThresholdMovesTheRightWayWithBothInputs is the whole claim of deriving an operating
// point rather than choosing one: the two things an operator can state each move it, in the
// direction the arithmetic requires.
func TestThresholdMovesTheRightWayWithBothInputs(t *testing.T) {
	const base = 1.3e-5 // the measured LANL base rate

	// A catch worth more lowers the bar.
	prev := math.Inf(1)
	for _, v := range []float64{1, 10, 100, 10000} {
		op, err := allocation.Threshold(mustUtility(t, v), base)
		if err != nil {
			t.Fatal(err)
		}
		if op.MinimumScore >= prev {
			t.Errorf("at v/c = %v the threshold is %v, not below the previous %v",
				v, op.MinimumScore, prev)
		}
		prev = op.MinimumScore
	}

	// A rarer event raises it.
	prev = math.Inf(-1)
	for _, p := range []float64{1e-2, 1e-3, 1e-5, 1e-7} {
		op, err := allocation.Threshold(mustUtility(t, 10), p)
		if err != nil {
			t.Fatal(err)
		}
		if op.MinimumScore <= prev {
			t.Errorf("at a base rate of %v the threshold is %v, not above the previous %v",
				p, op.MinimumScore, prev)
		}
		prev = op.MinimumScore
	}
}

// TestThresholdAgreesWithTheBayesRuleItClaimsToBe checks the derivation against the
// posterior it is supposed to implement, rather than against itself. Two routes to one
// number must agree.
func TestThresholdAgreesWithTheBayesRuleItClaimsToBe(t *testing.T) {
	for _, v := range []float64{1, 10, 1000} {
		for _, p := range []float64{1e-2, 1.3e-5} {
			op, err := allocation.Threshold(mustUtility(t, v), p)
			if err != nil {
				t.Fatal(err)
			}
			// An alert exactly at the threshold should carry posterior odds equal to the
			// cost ratio, so its posterior probability is MinimumPosterior.
			priorOdds := p / (1 - p)
			postOdds := priorOdds * math.Exp(op.MinimumScore)
			post := postOdds / (1 + postOdds)
			if math.Abs(post-op.MinimumPosterior) > 1e-9 {
				t.Errorf("v/c=%v base=%v: an alert at the threshold reaches posterior %v, "+
					"but MinimumPosterior is %v", v, p, post, op.MinimumPosterior)
			}
		}
	}
}

func TestAdmitsIsStrictlyAboveTheThreshold(t *testing.T) {
	op, err := allocation.Threshold(mustUtility(t, 10), 1e-4)
	if err != nil {
		t.Fatal(err)
	}
	if op.Admits(op.MinimumScore) {
		t.Error("an alert exactly at the threshold was admitted; it breaks even, it does not pay")
	}
	if !op.Admits(op.MinimumScore + 1e-9) {
		t.Error("an alert above the threshold was refused")
	}
}
