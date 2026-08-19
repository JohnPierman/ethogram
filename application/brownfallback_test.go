package application

import (
	"math"
	"strings"
	"testing"

	"github.com/JohnPierman/ethogram/domain/calibration"
	"github.com/JohnPierman/ethogram/domain/detector"
	"github.com/JohnPierman/ethogram/domain/event"
)

// evaluatedPair builds two evaluated verdicts with the given tail masses, which is the
// minimum the combination needs.
func evaluatedPair(t *testing.T, pA, pB float64) detector.Verdicts {
	t.Helper()
	target := detector.Target{
		Event:  event.ID{1, 2, 3},
		Entity: event.EntityID("U1"),
		Fields: []event.FieldPath{"f"},
	}
	out := detector.Verdicts{}
	for i, p := range []float64{pA, pB} {
		id := detector.ID("a")
		if i == 1 {
			id = detector.ID("b")
		}
		v, err := detector.NewEvaluated(id, target, p, detector.NewEvidence([]int{18},
			map[string]float64{"tail": p}, map[string]string{"field": "f"}))
		if err != nil {
			t.Fatalf("building verdict %d: %v", i, err)
		}
		out = append(out, v)
	}
	return out
}

// impossibleCovariance returns a frozen covariance whose pairwise estimate is so negative
// that Var[X2] = 4j + 2*sum goes below zero — the condition no joint distribution of the
// statistics can produce, and the one that used to abort a run at the burn-in boundary.
func impossibleCovariance(t *testing.T) *calibration.CovarianceModel {
	t.Helper()
	c := calibration.NewCorrelations(2)
	ids := []string{"a", "b"}
	// Perfectly anti-correlated at large magnitude: one statistic is 100 exactly when the
	// other is 0. The sample covariance is then hugely negative.
	for range 8 {
		c.Observe(ids, []float64{100, 0})
		c.Observe(ids, []float64{0, 100})
	}
	m := c.Freeze()
	if m == nil {
		t.Fatal("covariance did not freeze")
	}
	matrix := m.Matrix(ids)
	variance := 4*float64(len(ids)) + 2*matrix[0][1]
	if variance > 0 {
		t.Fatalf("fixture: Var[X2] = %v, need it non-positive to exercise the fallback", variance)
	}
	return m
}

// TestBrownFallbackDegradesToFisherRatherThanAbandoningTheRun.
//
// A covariance implying a non-positive variance used to fail the whole replay at the
// boundary, discarding hours of work over one unusable estimate. Section 10.2 already
// prescribes degradation to Fisher where the covariance is unusable; a covariance that is
// present but invalid is the same case.
func TestBrownFallbackDegradesToFisherRatherThanAbandoningTheRun(t *testing.T) {
	score, err := combineWith(evaluatedPair(t, 1e-6, 1e-3), impossibleCovariance(t), nil)
	if err != nil {
		t.Fatalf("the run was abandoned instead of degrading: %v", err)
	}
	if score == nil {
		t.Fatal("no score returned")
	}
	if score.Corrected {
		t.Error("Corrected must be false: the correction was rejected, not applied")
	}
	if score.CorrectionRejected == "" {
		t.Error("the rejection must be recorded, or a reader cannot tell this run from one " +
			"that never estimated a covariance")
	}
	if !strings.Contains(score.CorrectionRejected, "not positive") {
		t.Errorf("CorrectionRejected = %q, want the variance reason", score.CorrectionRejected)
	}
	// The degraded statistic must still be a usable p-value.
	if math.IsNaN(score.LogP) || math.IsInf(score.LogP, 0) || score.LogP > 0 {
		t.Errorf("LogP = %v, want a finite non-positive log p-value", score.LogP)
	}
	if score.J != 2 {
		t.Errorf("J = %d, want 2", score.J)
	}
	// c = 1 and f = df is what plain Fisher reports, so the record shows the degradation
	// rather than carrying moments from a correction that was not applied.
	if score.C != 1 {
		t.Errorf("C = %v, want exactly 1 for plain Fisher", score.C)
	}
}

// TestBrownAppliesWhenTheCovarianceIsUsable is the guard against the fallback becoming the
// normal path: a well-behaved covariance must still be applied and recorded as applied.
func TestBrownAppliesWhenTheCovarianceIsUsable(t *testing.T) {
	c := calibration.NewCorrelations(2)
	ids := []string{"a", "b"}
	// Mildly positively correlated, which is what the detectors actually are.
	for i := range 12 {
		x := float64(2 + i%3)
		c.Observe(ids, []float64{x, x + 0.4})
	}
	cov := c.Freeze()

	score, err := combineWith(evaluatedPair(t, 1e-6, 1e-3), cov, nil)
	if err != nil {
		t.Fatal(err)
	}
	if score.CorrectionRejected != "" {
		t.Errorf("a usable covariance was rejected: %q", score.CorrectionRejected)
	}
	if !score.Corrected {
		t.Error("Corrected must be true when the correction was applied")
	}
}

// TestNoCovarianceIsPlainFisherAndNotARejection: absent and invalid are different states
// and must not be reported as one.
func TestNoCovarianceIsPlainFisherAndNotARejection(t *testing.T) {
	score, err := combineWith(evaluatedPair(t, 1e-6, 1e-3), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if score.Corrected {
		t.Error("Corrected must be false with no covariance")
	}
	if score.CorrectionRejected != "" {
		t.Errorf("no covariance is not a rejection, got %q", score.CorrectionRejected)
	}
}
