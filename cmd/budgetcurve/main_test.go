package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func frameworkFixture(scored, labelled int, budgets map[int][2]int) map[string]any {
	det := map[string]any{}
	for b, v := range budgets {
		det[budgetKey(b)] = map[string]any{
			"alerts": float64(v[0]), "true_positives": float64(v[1]),
			"red_team_total": float64(labelled),
		}
	}
	return map[string]any{
		"run":    map[string]any{"run_id": "fw-001"},
		"corpus": map[string]any{"events_scored": float64(scored)},
		"results": map[string]any{
			"detections_at_budget": det,
		},
	}
}

func baselinesFixture(rowsSample, sampleRate, redteam, daysFrom, daysTo int,
	methods map[string]map[int]int) map[string]any {
	res := map[string]any{}
	for name, byBudget := range methods {
		det := map[string]any{}
		for b, hits := range byBudget {
			det[budgetKey(b)] = map[string]any{"detections": float64(hits), "red_team_total": float64(redteam)}
		}
		res[name] = map[string]any{"detections_at_budget": det}
	}
	return map[string]any{
		"run":   map[string]any{"run_id": "bl-001"},
		"input": map[string]any{"rows_sample": float64(rowsSample), "rows_redteam": float64(redteam)},
		"parameters": map[string]any{
			"sample_rate": float64(sampleRate),
			"days_from":   float64(daysFrom), "days_to": float64(daysTo),
		},
		"results": res,
	}
}

func writeTmp(t *testing.T, name string, v any) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestRefusesDifferentPopulations is the guard the figure exists to respect. Shared axes
// assert that the lines scored the same events; if they did not, the figure must not be
// drawn at all rather than drawn with a caveat.
func TestRefusesDifferentPopulations(t *testing.T) {
	fw := writeTmp(t, "fw.json", frameworkFixture(4_190_603, 549, map[int][2]int{10: {70, 8}}))
	// 1.34M x 100 = 134M events: the old days-7-to-30 baselines, a different slice.
	bl := writeTmp(t, "bl.json", baselinesFixture(1_344_903, 100, 549, 7, 30,
		map[string]map[int]int{"iforest": {10: 0}}))
	dir := t.TempDir()
	err := run(fw, bl, filepath.Join(dir, "o.json"), filepath.Join(dir, "o.svg"), "c-001", "", "composite", 0.01)
	if err == nil {
		t.Fatal("want a refusal when the populations differ")
	}
	if !strings.Contains(err.Error(), "not the same slice") {
		t.Fatalf("the error must name the reason, got %v", err)
	}
}

// TestRefusesDifferentGroundTruth: same population size is not enough; the label sets must
// match too, or recall means two different things on one pair of axes.
func TestRefusesDifferentGroundTruth(t *testing.T) {
	fw := writeTmp(t, "fw.json", frameworkFixture(4_000_000, 549, map[int][2]int{10: {70, 8}}))
	bl := writeTmp(t, "bl.json", baselinesFixture(1_000_000, 4, 653, 7, 14,
		map[string]map[int]int{"iforest": {10: 0}}))
	dir := t.TempDir()
	err := run(fw, bl, filepath.Join(dir, "o.json"), filepath.Join(dir, "o.svg"), "c-001", "", "composite", 0.01)
	if err == nil || !strings.Contains(err.Error(), "not the same ground truth") {
		t.Fatalf("want a ground-truth refusal, got %v", err)
	}
}

// TestPrecisionAndAlphaAreComputedFromTheQueue pins the two axes.
func TestPrecisionAndAlphaAreComputedFromTheQueue(t *testing.T) {
	const scored, labelled = 4_000_000, 500
	fw := writeTmp(t, "fw.json", frameworkFixture(scored, labelled, map[int][2]int{
		10: {70, 7}, 100: {700, 14},
	}))
	bl := writeTmp(t, "bl.json", baselinesFixture(1_000_000, 4, labelled, 7, 14,
		map[string]map[int]int{"iforest": {10: 0, 100: 3}}))
	dir := t.TempDir()
	outPath := filepath.Join(dir, "o.json")
	if err := run(fw, bl, outPath, filepath.Join(dir, "o.svg"), "c-001", "", "composite", 0.01); err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	b, _ := os.ReadFile(outPath)
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	drawn := out["results"].(map[string]any)["drawn"].([]any)
	ours := drawn[0].(map[string]any)
	if ours["ours"] != true {
		t.Fatal("our series must be first in drawn")
	}
	p0 := ours["points"].([]any)[0].(map[string]any)
	// 7 of 70 alerts real; 63 false alerts over 3,999,500 background events.
	if got := p0["precision"].(float64); got != 0.1 {
		t.Fatalf("precision = %v, want 0.1", got)
	}
	wantAlpha := 63.0 / float64(scored-labelled)
	if got := p0["false_alarm_rate"].(float64); got != wantAlpha {
		t.Fatalf("alpha = %v, want %v", got, wantAlpha)
	}
}

// TestZeroDetectionPointsSurvive: a budget at which a method found nothing must stay in the
// series. Dropping it would make the line vanish, which a reader cannot distinguish from
// the method not having been measured.
func TestZeroDetectionPointsSurvive(t *testing.T) {
	fw := writeTmp(t, "fw.json", frameworkFixture(4_000_000, 500, map[int][2]int{10: {70, 7}}))
	bl := writeTmp(t, "bl.json", baselinesFixture(1_000_000, 4, 500, 7, 14,
		map[string]map[int]int{"iforest": {10: 0, 100: 0}}))
	dir := t.TempDir()
	outPath := filepath.Join(dir, "o.json")
	if err := run(fw, bl, outPath, filepath.Join(dir, "o.svg"), "c-001", "", "composite", 0.01); err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	b, _ := os.ReadFile(outPath)
	_ = json.Unmarshal(b, &out)
	for _, s := range out["results"].(map[string]any)["drawn"].([]any) {
		m := s.(map[string]any)
		if m["name"] == "iforest" {
			if n := len(m["points"].([]any)); n != 2 {
				t.Fatalf("iforest kept %d points, want 2 including the zero", n)
			}
			return
		}
	}
	t.Fatal("iforest was not drawn at all")
}

// TestSelectsTheStrongestFour: with more comparators available than the figure can carry,
// the ones kept must be the strongest, so the comparison is not flattered by the choice.
func TestSelectsTheStrongestFour(t *testing.T) {
	fw := writeTmp(t, "fw.json", frameworkFixture(4_000_000, 500, map[int][2]int{100: {700, 20}}))
	bl := writeTmp(t, "bl.json", baselinesFixture(1_000_000, 4, 500, 7, 14,
		map[string]map[int]int{
			"weak1": {100: 0}, "weak2": {100: 0}, "weak3": {100: 1},
			"strongA": {100: 40}, "strongB": {100: 30}, "strongC": {100: 20}, "strongD": {100: 10},
		}))
	dir := t.TempDir()
	outPath := filepath.Join(dir, "o.json")
	if err := run(fw, bl, outPath, filepath.Join(dir, "o.svg"), "c-001", "", "composite", 0.01); err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	b, _ := os.ReadFile(outPath)
	_ = json.Unmarshal(b, &out)
	got := map[string]bool{}
	for _, s := range out["results"].(map[string]any)["drawn"].([]any) {
		got[s.(map[string]any)["name"].(string)] = true
	}
	for _, want := range []string{"strongA", "strongB", "strongC", "strongD"} {
		if !got[want] {
			t.Fatalf("%s should have been selected; drawn = %v", want, got)
		}
	}
	for _, unwanted := range []string{"weak1", "weak2", "weak3"} {
		if got[unwanted] {
			t.Fatalf("%s should not have been selected", unwanted)
		}
	}
}
