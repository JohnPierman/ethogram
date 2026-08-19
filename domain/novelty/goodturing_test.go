package novelty_test

import (
	"math"
	"testing"

	"github.com/JohnPierman/ethogram/domain/novelty"
)

func counts(spec map[string]float64) []novelty.ValueCount {
	out := make([]novelty.ValueCount, 0, len(spec))
	for v, c := range spec {
		out = append(out, novelty.ValueCount{Value: v, Count: c})
	}
	return out
}

// TestUnseenMassSeparatesOpenFromClosedVocabularies is the whole reason the estimator
// exists, stated as the case equation (4) cannot tell apart.
//
// Both histories below have the same order of total weight. One is an account that has
// used three addresses for months; the other has used five hundred once each. The
// probability that the next value is new differs by orders of magnitude between them, and
// the Dirichlet reserve — which reads only the totals — gives nearly the same answer to
// both.
func TestUnseenMassSeparatesOpenFromClosedVocabularies(t *testing.T) {
	closed := counts(map[string]float64{"10.0.0.1": 1000, "10.0.0.2": 1000, "10.0.0.3": 1000})

	open := make([]novelty.ValueCount, 0, 500)
	for i := range 500 {
		open = append(open, novelty.ValueCount{Value: string(rune('a'+i%26)) + string(rune('a'+i/26)), Count: 1})
	}

	closedMass, closedGT := novelty.UnseenMass(closed, 1)
	openMass, openGT := novelty.UnseenMass(open, 1)

	t.Logf("closed vocabulary: P(new) = %.6f (good-turing=%v)", closedMass, closedGT)
	t.Logf("open   vocabulary: P(new) = %.6f (good-turing=%v)", openMass, openGT)

	if !openGT {
		t.Error("500 singletons is ample support; the estimate must come from the shape")
	}
	if openMass < 0.5 {
		t.Errorf("open vocabulary P(new) = %v; every value seen exactly once means the "+
			"next one is very likely new", openMass)
	}
	if closedMass > 0.01 {
		t.Errorf("closed vocabulary P(new) = %v; three values seen a thousand times each "+
			"means the next one is very likely NOT new", closedMass)
	}
	// The separation is the point: the two must differ by orders of magnitude, where the
	// Dirichlet reserve differs by a factor of about three.
	if ratio := openMass / closedMass; ratio < 100 {
		t.Errorf("open/closed ratio = %.1f; equation (4) alone achieves about 3, and "+
			"failing to separate these is the defect this estimator addresses", ratio)
	}
}

// TestUnseenMassFallsBackWithoutSupport: a handful of observations cannot support a
// singleton rate, and the estimator must say so rather than read noise as shape.
func TestUnseenMassFallsBackWithoutSupport(t *testing.T) {
	for _, n := range []int{0, 1, 5, 20} {
		history := make([]novelty.ValueCount, 0, n)
		for i := range n {
			history = append(history, novelty.ValueCount{Value: string(rune('a' + i)), Count: 1})
		}
		mass, gt := novelty.UnseenMass(history, 1)
		if gt {
			t.Errorf("n=%d: reported a Good-Turing estimate on too little support", n)
		}
		if mass <= 0 || mass > 1 {
			t.Errorf("n=%d: fallback mass %v outside (0,1]", n, mass)
		}
	}
}

// TestUnseenMassIsBounded: the estimate may not reach zero or one. Zero asserts a new
// value is impossible and would poison the logarithm the combination takes; one leaves no
// mass for the values already observed.
func TestUnseenMassIsBounded(t *testing.T) {
	// Every value a singleton, so the raw rate is exactly 1.
	all := make([]novelty.ValueCount, 0, 200)
	for i := range 200 {
		all = append(all, novelty.ValueCount{Value: string(rune('a'+i%26)) + string(rune('0'+i/26)), Count: 1})
	}
	mass, _ := novelty.UnseenMass(all, 1)
	if mass >= 1 {
		t.Errorf("P(new) = %v must stay below one", mass)
	}
	if mass <= 0 {
		t.Errorf("P(new) = %v must stay above zero", mass)
	}

	// A settled vocabulary with no singletons at all must still reserve something.
	settled := counts(map[string]float64{"a": 5000, "b": 4000, "c": 3000})
	mass, gt := novelty.UnseenMass(settled, 1)
	if gt {
		t.Error("no singletons means no Good-Turing estimate; the reserve is the fallback")
	}
	if mass <= 0 {
		t.Errorf("a settled vocabulary still reserves mass for the unseen, got %v", mass)
	}
}

// TestUnseenMassAdmitsDecayedSingletons: counts decay lazily and are not integral, so a
// value at 0.9 units of weight was seen once and has aged. Refusing to count it would
// make the estimate collapse as soon as any decay had occurred.
func TestUnseenMassAdmitsDecayedSingletons(t *testing.T) {
	fresh := make([]novelty.ValueCount, 0, 100)
	aged := make([]novelty.ValueCount, 0, 100)
	for i := range 100 {
		v := string(rune('a'+i%26)) + string(rune('0'+i/26))
		fresh = append(fresh, novelty.ValueCount{Value: v, Count: 1})
		aged = append(aged, novelty.ValueCount{Value: v, Count: 0.9})
	}
	freshMass, freshGT := novelty.UnseenMass(fresh, 1)
	agedMass, agedGT := novelty.UnseenMass(aged, 1)
	if !freshGT || !agedGT {
		t.Fatalf("both must use Good-Turing: fresh=%v aged=%v", freshGT, agedGT)
	}
	if math.Abs(freshMass-agedMass) > 0.15 {
		t.Errorf("decay moved P(new) from %v to %v; a lazily decayed singleton is still "+
			"a singleton", freshMass, agedMass)
	}
}

// TestOpenVocabularyKeepsUnitMass: the reserved mass and the renormalised observed
// values must still carry unit mass, or equation (5)'s tail is built on a distribution
// that is not one and the p-values stop being probabilities.
//
// This is the property that would fail silently — every score would still look plausible.
func TestOpenVocabularyKeepsUnitMass(t *testing.T) {
	histories := map[string][]novelty.ValueCount{
		"open":    {},
		"closed":  counts(map[string]float64{"a": 900, "b": 800, "c": 700}),
		"mixed":   counts(map[string]float64{"a": 500, "b": 1, "c": 1, "d": 1, "e": 1}),
		"decayed": counts(map[string]float64{"a": 40.5, "b": 0.9, "c": 0.95, "d": 12.25}),
	}
	for i := range 120 {
		histories["open"] = append(histories["open"],
			novelty.ValueCount{Value: string(rune('a'+i%26)) + string(rune('0'+i/26)), Count: 1})
	}

	est := novelty.Estimator{Alpha: 1, OpenVocabulary: true}
	for name, history := range histories {
		e := est.Estimate(history, "a")
		sum := e.PHatUnseen
		for _, vc := range history {
			if vc.Count <= 0 {
				continue
			}
			sum += est.Estimate(history, vc.Value).PHatObserved
		}
		if math.Abs(sum-1) > 1e-9 {
			t.Errorf("%s: masses sum to %v, want 1", name, sum)
		}
		if e.TailMass <= 0 || e.TailMass > 1 {
			t.Errorf("%s: tail mass %v outside (0,1]", name, e.TailMass)
		}
	}
}

// TestOpenVocabularyLeavesClosedVocabulariesAlone: an account with a settled value set
// must score as it did before, or the change is not a refinement but a different
// detector. The fallback path is what guarantees this.
func TestOpenVocabularyLeavesClosedVocabulariesAlone(t *testing.T) {
	history := counts(map[string]float64{"Kerberos": 4000, "NTLM": 3000, "Negotiate": 2000})
	before := novelty.Estimator{Alpha: 1}
	after := novelty.Estimator{Alpha: 1, OpenVocabulary: true}

	for _, v := range []string{"Kerberos", "NTLM", "Negotiate", "never-seen"} {
		b := before.Estimate(history, v)
		a := after.Estimate(history, v)
		if math.Abs(a.TailMass-b.TailMass) > 1e-12 {
			t.Errorf("%q: tail moved from %v to %v on a closed vocabulary", v, b.TailMass, a.TailMass)
		}
	}
}

// TestOpenVocabularyMakesAFirstValueLessAlarmingWhenNoveltyIsNormal is the behavioural
// point, and it cuts the way that costs alerts rather than the way that wins them.
//
// An account that has produced a new value nearly every time is not remarkable for
// producing another. Equation (4) calls it extraordinary anyway, because its reserve
// depends only on the counts; the Good–Turing reserve is large, so the same event scores
// as ordinary. That is a *reduction* in alerts on exactly the accounts that generate the
// most of them.
func TestOpenVocabularyMakesAFirstValueLessAlarmingWhenNoveltyIsNormal(t *testing.T) {
	churning := make([]novelty.ValueCount, 0, 300)
	for i := range 300 {
		churning = append(churning,
			novelty.ValueCount{Value: string(rune('a'+i%26)) + string(rune('0'+i/26)), Count: 1})
	}
	settled := counts(map[string]float64{"one": 150, "two": 150})

	dirichlet := novelty.Estimator{Alpha: 1}
	goodTuring := novelty.Estimator{Alpha: 1, OpenVocabulary: true}

	churnDir := dirichlet.Estimate(churning, "brand-new").TailMass
	churnGT := goodTuring.Estimate(churning, "brand-new").TailMass
	settledGT := goodTuring.Estimate(settled, "brand-new").TailMass

	t.Logf("first-ever value, churning account: (4) = %.6f, Good-Turing = %.6f", churnDir, churnGT)
	t.Logf("first-ever value, settled account:  Good-Turing = %.6f", settledGT)

	if churnGT <= churnDir {
		t.Errorf("a churning account's new value scored %v under Good-Turing against %v "+
			"under (4); it must be LESS surprising, not more", churnGT, churnDir)
	}
	if settledGT >= churnGT {
		t.Errorf("a settled account's first new value (%v) must be more surprising than "+
			"a churning account's (%v)", settledGT, churnGT)
	}
}

// TestUnseenMassIgnoresNonPositiveCounts: a row decayed to nothing is not a value.
func TestUnseenMassIgnoresNonPositiveCounts(t *testing.T) {
	withZeros := counts(map[string]float64{"a": 1000, "b": 1000, "gone": 0, "also": -1})
	without := counts(map[string]float64{"a": 1000, "b": 1000})
	a, _ := novelty.UnseenMass(withZeros, 1)
	b, _ := novelty.UnseenMass(without, 1)
	if a != b {
		t.Errorf("zero and negative counts changed the estimate: %v against %v", a, b)
	}
}
