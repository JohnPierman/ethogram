package main

import (
	"strings"
	"testing"

	"github.com/JohnPierman/ethogram/domain/calibration"
)

// The dependence record has to say whether equation (19) reached the combination, not
// merely whether a covariance was measured. Under conformal calibration the covariance is
// still estimated and frozen at the boundary, but it is deliberately withheld: it is
// measured on the detectors' own p-values, where -2 ln P runs to thousands, and the
// calibrated statistic it would divide runs to tens.
//
// Reporting `applied: true` on such a run — which every conformal result did — tells a
// reader the combination was corrected when it was not. These tests hold the record and
// the arithmetic together.

func TestDependenceIsRecordedAsWithheldUnderConformal(t *testing.T) {
	model := measuredCovariance(t)

	record := dependenceRecord(model, true)
	if record["estimated"] != true {
		t.Error("the covariance WAS estimated under conformal; the record denies it")
	}
	if record["applied"] != false {
		t.Fatal("the record claims equation (19) corrected the combination, but under " +
			"conformal calibration it is withheld: a covariance measured on model " +
			"p-values cannot scale a statistic built from ranks")
	}
	reason, _ := record["withheld_reason"].(string)
	if reason == "" {
		t.Fatal("withholding the correction must be explained, or a reader sees only a " +
			"false flag and no account of it")
	}
	for _, want := range []string{"conformal", "Fisher", "ModelLogP"} {
		if !strings.Contains(reason, want) {
			t.Errorf("the reason does not mention %q:\n%s", want, reason)
		}
	}
}

func TestDependenceIsRecordedAsAppliedWithoutConformal(t *testing.T) {
	record := dependenceRecord(measuredCovariance(t), false)
	if record["estimated"] != true || record["applied"] != true {
		t.Fatalf("without conformal the correction is applied; record = %v", record)
	}
	if _, present := record["withheld_reason"]; present {
		t.Error("a correction that was applied must carry no withholding reason")
	}
}

func TestNoCovarianceIsRecordedAsNeitherEstimatedNorApplied(t *testing.T) {
	record := dependenceRecord(nil, false)
	if record["estimated"] != false || record["applied"] != false {
		t.Fatalf("with no covariance both flags are false; record = %v", record)
	}
	note, _ := record["note"].(string)
	if !strings.Contains(note, "Fisher") {
		t.Errorf("the degradation to Fisher must be stated: %q", note)
	}
}

// measuredCovariance builds a frozen covariance over two detectors with enough paired
// burn-in observations to clear minCorrelationObservations.
func measuredCovariance(t *testing.T) *calibration.CovarianceModel {
	t.Helper()
	correlations := calibration.NewCorrelations(minCorrelationObservations)
	ids := []string{"novelty", "volume"}
	for i := range minCorrelationObservations + 10 {
		// Two weakly dependent streams of -2 ln P; the exact values do not matter to the
		// record, only that the pair clears the support threshold and is frozen.
		correlations.Observe(ids, []float64{
			1.0 + 0.01*float64(i%97),
			0.8 + 0.01*float64(i%89),
		})
	}
	return correlations.Freeze()
}
