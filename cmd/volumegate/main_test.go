package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// probeFixture builds a replay result carrying a volume_gate_probe block. Each candidate
// is described by the share it abstains on and whether every budget clears the floor,
// which is all the decision rule reads.
func probeFixture(armed float64, cands []struct {
	mp    float64
	share float64
	clear bool
}) map[string]any {
	rows := make([]any, 0, len(cands))
	for _, c := range cands {
		rows = append(rows, map[string]any{
			"min_periods":     c.mp,
			"measurable":      true,
			"abstained_share": c.share,
			"at_budget": map[string]any{
				"budget_10_per_day":   map[string]any{"off_the_1e-12_floor": c.clear},
				"budget_1000_per_day": map[string]any{"off_the_1e-12_floor": c.clear},
			},
		})
	}
	return map[string]any{
		"run": map[string]any{"run_id": "parent-001"},
		"results": map[string]any{
			"volume_gate_probe": map[string]any{
				"measured":          true,
				"armed_min_periods": armed,
				"candidates":        rows,
			},
		},
	}
}

func writeFixture(t *testing.T, v any) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "run.json")
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func decisionOf(t *testing.T, outPath string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out["results"].(map[string]any)["decision"].(map[string]any)
}

// TestRecommendsSmallestAdmissible is the rule of #25: smallest threshold that clears the
// floor while abstaining on a small share. Candidate 1 clears nothing, candidate 2 clears
// but abstains on far too much, candidate 3 is the answer.
func TestRecommendsSmallestAdmissible(t *testing.T) {
	in := writeFixture(t, probeFixture(0, []struct {
		mp    float64
		share float64
		clear bool
	}{
		{0, 0.00, false},
		{1, 0.01, false},
		{2, 0.80, true},
		{3, 0.04, true},
		{5, 0.09, true},
	}))
	out := filepath.Join(t.TempDir(), "gate.json")
	if err := run(in, out, "gate-001", 3, 0.10); err != nil {
		t.Fatal(err)
	}
	d := decisionOf(t, out)
	if got := d["recommended"]; got != float64(3) {
		t.Fatalf("recommended = %v, want 3", got)
	}
	if got := d["agrees_with_recommendation"]; got != true {
		t.Fatalf("agrees_with_recommendation = %v, want true", got)
	}
}

// TestNoCandidateSatisfiesTheRuleIsAResult: if nothing clears the floor, that is reported
// as the finding rather than silently adopting the least bad option.
func TestNoCandidateSatisfiesTheRuleIsAResult(t *testing.T) {
	in := writeFixture(t, probeFixture(0, []struct {
		mp    float64
		share float64
		clear bool
	}{
		{1, 0.01, false},
		{3, 0.02, false},
	}))
	out := filepath.Join(t.TempDir(), "gate.json")
	if err := run(in, out, "gate-002", -1, 0.10); err != nil {
		t.Fatal(err)
	}
	d := decisionOf(t, out)
	if d["recommended"] != nil {
		t.Fatalf("recommended = %v, want nil", d["recommended"])
	}
	if _, ok := d["recommendation_note"]; !ok {
		t.Fatal("a rule that selects nothing must say so")
	}
}

// TestDivergenceFromTheRuleIsRecorded: adopting something other than the recommendation
// is permitted and must be visible.
func TestDivergenceFromTheRuleIsRecorded(t *testing.T) {
	in := writeFixture(t, probeFixture(0, []struct {
		mp    float64
		share float64
		clear bool
	}{
		{1, 0.01, true},
		{3, 0.02, true},
	}))
	out := filepath.Join(t.TempDir(), "gate.json")
	if err := run(in, out, "gate-003", 3, 0.10); err != nil {
		t.Fatal(err)
	}
	d := decisionOf(t, out)
	if d["recommended"] != float64(1) {
		t.Fatalf("recommended = %v, want 1", d["recommended"])
	}
	if d["agrees_with_recommendation"] != false {
		t.Fatal("want agrees_with_recommendation false")
	}
	if _, ok := d["divergence_note"]; !ok {
		t.Fatal("a divergence must be recorded")
	}
}

// TestRefusesAGatedParent: a run that armed the gate cannot decide the threshold, because
// the events below it carry no p-value. Failing closed is the point.
func TestRefusesAGatedParent(t *testing.T) {
	in := writeFixture(t, probeFixture(3, []struct {
		mp    float64
		share float64
		clear bool
	}{{3, 0.02, true}}))
	err := run(in, filepath.Join(t.TempDir(), "gate.json"), "gate-004", 3, 0.10)
	if err == nil {
		t.Fatal("want an error for a gated parent")
	}
	if !strings.Contains(err.Error(), "must be ungated") {
		t.Fatalf("error should name the fix, got %v", err)
	}
}
