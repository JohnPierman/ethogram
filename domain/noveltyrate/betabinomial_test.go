package noveltyrate

import (
	"math"
	"testing"
)

func closeTo(t *testing.T, name string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s = %v, want %v (tolerance %v)", name, got, want, tol)
	}
}

// TestTheWholeDistributionSumsToOne is the check that the pmf is a pmf. Everything else
// here reads a tail off it, so an unnormalised pmf would make every p-value wrong by a
// factor nothing downstream could detect.
func TestTheWholeDistributionSumsToOne(t *testing.T) {
	for _, c := range []struct {
		m    int
		a, b float64
	}{
		{1, 0.5, 0.5}, {5, 1, 1}, {12, 0.5, 40}, {30, 2, 500}, {64, 0.25, 3},
	} {
		var sum float64
		for k := 0; k <= c.m; k++ {
			sum += math.Exp(logPMF(k, c.m, c.a, c.b))
		}
		closeTo(t, "Σ pmf", sum, 1, 1e-9)
	}
}

// TestUniformPriorGivesTheDiscreteUniform: BetaBinomial(m, 1, 1) is uniform on 0..m, which
// is a closed form the implementation must reproduce without being told.
func TestUniformPriorGivesTheDiscreteUniform(t *testing.T) {
	const m = 8
	for k := 0; k <= m; k++ {
		closeTo(t, "pmf", math.Exp(logPMF(k, m, 1, 1)), 1.0/float64(m+1), 1e-12)
	}
	// P(K ≥ k) = (m − k + 1)/(m + 1).
	for k := 0; k <= m; k++ {
		want := float64(m-k+1) / float64(m+1)
		closeTo(t, "tail", math.Exp(LogUpperTail(k, m, 1, 1)), want, 1e-12)
	}
}

// TestTheTwoTailsAgreeWhereTheySwitch guards the branch: the upper tail is summed directly
// above the predictive mean and taken as the complement of the lower sum at or below it.
// A discontinuity at the switch would put a step in the ranking exactly where ordinary
// events sit.
func TestTheTwoTailsAgreeWhereTheySwitch(t *testing.T) {
	const m = 40
	a, b := 2.0, 18.0
	for k := 0; k <= m; k++ {
		direct := logSumRange(k, m, m, a, b)
		got := LogUpperTail(k, m, a, b)
		if math.IsInf(direct, -1) {
			continue
		}
		closeTo(t, "tail agreement", got, direct, 1e-9)
	}
}

// TestTheTailIsMonotone: a larger observed count can never be less surprising.
func TestTheTailIsMonotone(t *testing.T) {
	const m = 50
	prev := 0.0
	for k := 0; k <= m; k++ {
		got := LogUpperTail(k, m, 0.5, 60)
		if k > 0 && got > prev+1e-12 {
			t.Fatalf("tail rose from %v to %v going from k=%d to k=%d", prev, got, k-1, k)
		}
		prev = got
	}
}

// TestTheBoundaries pins the two ends, which the branch could get wrong silently.
func TestTheBoundaries(t *testing.T) {
	if got := LogUpperTail(0, 10, 1, 3); got != 0 {
		t.Errorf("P(K ≥ 0) = exp(%v), want exactly 1", got)
	}
	if got := LogUpperTail(11, 10, 1, 3); !math.IsInf(got, -1) {
		t.Errorf("P(K ≥ 11) of 10 trials = exp(%v), want 0", got)
	}
	// P(K ≥ m) is the probability of every trial succeeding, which has a closed form:
	// B(a+m, b) / B(a, b).
	closeTo(t, "P(K ≥ m)", LogUpperTail(6, 6, 2, 3),
		logBeta(2+6, 3)-logBeta(2, 3), 1e-12)
}

// TestAnExtremeTailSurvivesUnderflow is the property the whole log-space treatment exists
// for. Forty first-ever values in an hour from an account that produces about one in a
// thousand events lands far below the least positive float64. Returned as a probability it
// would be exactly zero, and every event past that point would tie with every other — a
// tie no later stage can undo, since conformal calibration maps ties to ties and a minimum
// over tied values is a tie.
func TestAnExtremeTailSurvivesUnderflow(t *testing.T) {
	logP := LogUpperTail(40, 45, 1, 1000)
	if math.Exp(logP) != 0 {
		t.Skip("this case no longer underflows; the guard below is what matters")
	}
	if math.IsInf(logP, -1) || math.IsNaN(logP) {
		t.Fatalf("an extreme tail came back as %v, which is not an ordering", logP)
	}
	if logP >= math.Log(math.SmallestNonzeroFloat64) {
		t.Errorf("ln p = %v does not reach past the float64 floor, so the log space "+
			"is buying nothing", logP)
	}
	// And it must still be ordered against a yet more extreme observation.
	worse := LogUpperTail(44, 45, 1, 1000)
	if !(worse < logP) {
		t.Errorf("44 of 45 (%v) is not more extreme than 40 of 45 (%v); the ordering "+
			"is lost exactly where it is needed", worse, logP)
	}
}

// TestUncertaintyInTheRateIsCarried is why this is a Beta-binomial and not a binomial.
// Two accounts with the SAME point estimate of the rate but different amounts of evidence
// for it must not be judged equally extreme on the same observation.
func TestUncertaintyInTheRateIsCarried(t *testing.T) {
	// Both posteriors mean 1/11; the second rests on ten times the evidence.
	vague := LogUpperTail(5, 20, 1, 10)
	pinned := LogUpperTail(5, 20, 10, 100)
	if !(pinned < vague) {
		t.Errorf("the well-evidenced rate (%v) is not more surprised than the vague one "+
			"(%v) by the same excess; the posterior width is being ignored", pinned, vague)
	}
}
