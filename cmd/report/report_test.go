package main

// Tests render exclusively from fixtures the test writes into t.TempDir(), never
// from the repository's results/, and exclusively into temp output paths, never
// into docs/. Fixture values are deliberately arbitrary and clearly synthetic.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func renderDirs(t *testing.T) (resultsDir, figuresDir, outPath string) {
	t.Helper()
	tmp := t.TempDir()
	resultsDir = filepath.Join(tmp, "results")
	if err := os.MkdirAll(resultsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return resultsDir, filepath.Join(tmp, "figures"), filepath.Join(tmp, "report.html")
}

func writeFixture(t *testing.T, dir, name string, doc map[string]any) {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func renderAll(t *testing.T, resultsDir, figuresDir, outPath string) string {
	t.Helper()
	results, err := loadResults(resultsDir)
	if err != nil {
		t.Fatalf("loadResults: %v", err)
	}
	if err = render(results, figuresDir, outPath); err != nil {
		t.Fatalf("render: %v", err)
	}
	raw, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func readManifest(t *testing.T, figuresDir string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(figuresDir, "manifest.json"))
	if err != nil {
		t.Fatalf("manifest not written: %v", err)
	}
	manifest := map[string]string{}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("manifest not parseable: %v", err)
	}
	return manifest
}

func replayFixture() map[string]any {
	counts := make([]int, 240)
	counts[0], counts[100], counts[239] = 5, 11, 200
	budget := func(alerts, tp int) map[string]any {
		return map[string]any{"alerts": alerts, "true_positives": tp, "red_team_total": 37}
	}
	return map[string]any{
		"schema_version": "1",
		"kind":           "replay",
		"hypothesis":     []string{"E1", "E2", "E3"},
		"run": map[string]any{
			"run_id": "replay-fixture-001", "git_sha": "0123456789abcdef0123",
			"git_dirty": false, "started_at": "2026-01-01T00:00:00Z",
			"finished_at": "2026-01-01T01:00:00Z",
			"detectors":   []string{"novelty", "timing", "volume", "cooccurrence"},
			"partition":   "leiden",
		},
		"corpus": map[string]any{
			"rows_read": 1000, "events_scored": 800,
			"coverage": map[string]any{"kind": "full"},
		},
		"provenance_complete": true,
		"results": map[string]any{
			"detections_at_budget": map[string]any{
				"budget_10_per_day":  budget(28, 4),
				"budget_25_per_day":  budget(70, 9),
				"budget_50_per_day":  budget(140, 17),
				"budget_100_per_day": budget(280, 23),
			},
			"p_histograms": map[string]any{
				"combined": map[string]any{"counts": counts, "under_1e_12": 2},
				"novelty":  map[string]any{"counts": counts, "under_1e_12": 0},
			},
			"status_counts": map[string]any{
				"novelty": map[string]any{
					"evaluated": 111, "abstained_structural": 22,
					"abstained_unexpected": 3, "abstained_unusable": 4,
				},
			},
		},
		"runtime": map[string]any{
			"wall_seconds": 12.5, "events_per_sec": 8250, "heap_alloc_mb": 64.5,
			"heap_sys_mb": 128.25, "novelty_rows": 54321, "timing_entities": 432,
			"volume_entities": 433, "graph_nodes": 210, "graph_edges": 978,
		},
	}
}

func analysisFixture() map[string]any {
	pt := func(q float64, disc, tp int, fdr, lo, hi, cons float64, sat int, proc string) map[string]any {
		return map[string]any{
			"nominal_q": q, "discoveries": disc, "true_positives": tp,
			"realised_fdr": fdr, "wilson_low_95": lo, "wilson_high_95": hi,
			"conservatism_ratio": cons, "saturated_days": sat, "procedure": proc,
		}
	}
	return map[string]any{
		"schema_version": "1",
		"kind":           "analysis",
		"hypothesis":     []string{"E3"},
		"run": map[string]any{
			"run_id": "analysis-fixture-001", "started_at": "2026-01-02T00:00:00Z",
			"finished_at": "2026-01-02T00:05:00Z",
		},
		"corpus":              map[string]any{"coverage": map[string]any{"kind": "full"}},
		"provenance_complete": true,
		"results": map[string]any{
			"calibration_bh": []any{
				pt(0.001, 5, 5, 0, 0, 0.4344, 0, 0, "benjamini-hochberg"),
				pt(0.01, 40, 39, 0.025, 0.0044, 0.1268, 2.5, 1, "benjamini-hochberg"),
				pt(0.25, 200, 150, 0.25, 0.1929, 0.3164, 1, 2, "benjamini-hochberg"),
			},
			"calibration_by": []any{
				pt(0.01, 10, 10, 0, 0, 0.2775, 0, 0, "benjamini-yekutieli"),
				pt(0.25, 60, 55, 0.0833, 0.0367, 0.1781, 0.33, 0, "benjamini-yekutieli"),
			},
		},
	}
}

func e7Fixture() map[string]any {
	return map[string]any{
		"schema_version": "1",
		"kind":           "e7",
		"hypothesis":     []string{"E7"},
		"run": map[string]any{
			"run_id": "e7-fixture-001", "started_at": "2026-01-03T00:00:00Z",
			"finished_at": "2026-01-03T00:01:00Z",
		},
		"corpus": map[string]any{
			"events_scored": 500,
			"coverage":      map[string]any{"kind": "prefix", "max_rows": 700000},
		},
		"provenance_complete": true,
		"results": map[string]any{
			"proportion_reconstructed": 0.5,
			"totals": map[string]any{
				"sampled": 100, "reconstructed": 50, "partial": 49, "failed": 1,
			},
			"by_detector": map[string]any{
				"timing": map[string]any{
					"sampled": 100, "reconstructed": 50, "partial": 49, "failed": 1,
				},
			},
		},
	}
}

func e9ReplayFixture() map[string]any {
	doc := replayFixture()
	doc["hypothesis"] = []string{"E1", "E2", "E3", "E9"}
	results := doc["results"].(map[string]any)
	results["e9_cell_arm"] = map[string]any{
		"detections_at_budget": map[string]any{
			"budget_10_per_day": map[string]any{"true_positives": 2, "red_team_total": 37},
			"budget_25_per_day": map[string]any{"true_positives": 6, "red_team_total": 37},
		},
		"straddler_entities": 12,
		"straddler_red_team": map[string]any{
			"circular_arm_straddlers":    map[string]any{"n": 5, "p_below_0.01": 4},
			"circular_arm_nonstraddlers": map[string]any{"n": 12, "p_below_0.01": 10},
			"cell_arm_straddlers":        map[string]any{"n": 5, "p_below_0.01": 1},
			"cell_arm_nonstraddlers":     map[string]any{"n": 12, "p_below_0.01": 9},
		},
	}
	return doc
}

func baselinesFixture() map[string]any {
	model := func(tp10, tp100 int) map[string]any {
		return map[string]any{"detections_at_budget": map[string]any{
			"budget_10_per_day":  map[string]any{"detections": tp10, "red_team_total": 41},
			"budget_100_per_day": map[string]any{"detections": tp100, "red_team_total": 41},
		}}
	}
	return map[string]any{
		"schema_version":      "1",
		"kind":                "baselines",
		"hypothesis":          []string{"E1", "E2"},
		"run":                 map[string]any{"run_id": "baselines-fixture-001"},
		"corpus":              map[string]any{"coverage": map[string]any{"kind": "full"}},
		"provenance_complete": true,
		"results": map[string]any{
			"iforest": model(1, 3), "eif": model(2, 6),
			"hst": model(3, 9), "rrcf": model(4, 12),
		},
	}
}

// 1. An empty results directory renders a full report: every scoreboard card NOT
// RUN, every figure listed as pending, and an empty manifest written.
func TestRenderEmptyResultsDir(t *testing.T) {
	resultsDir, figuresDir, outPath := renderDirs(t)
	html := renderAll(t, resultsDir, figuresDir, outPath)

	if got := strings.Count(html, `notrun-mark">NOT RUN`); got != 9 {
		t.Errorf("want 9 NOT RUN scoreboard cards, got %d", got)
	}
	for _, want := range []string{
		"F1 circular-density evidence", "F2 co-occurrence graph", "F3 calibration",
		"F4 detection at matched budget", "F5 per-detector p-value histograms",
		"F6 ablation: circular vs 168-cell grid", "F7 E5 degradation",
		"F8 wraparound render",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("pending list missing %q", want)
		}
	}
	if manifest := readManifest(t, figuresDir); len(manifest) != 0 {
		t.Errorf("want empty manifest, got %v", manifest)
	}
	if svgs, _ := filepath.Glob(filepath.Join(figuresDir, "*.svg")); len(svgs) != 0 {
		t.Errorf("no figure may exist without data, got %v", svgs)
	}
}

// 2. A replay result produces F4 and F5, manifest entries with the right run_id,
// inline SVG in the HTML, and T1/T3/T5 rows carrying the fixture's exact numbers.
func TestReplayFixtureFiguresAndTables(t *testing.T) {
	resultsDir, figuresDir, outPath := renderDirs(t)
	writeFixture(t, resultsDir, "replay.json", replayFixture())
	html := renderAll(t, resultsDir, figuresDir, outPath)

	manifest := readManifest(t, figuresDir)
	for _, name := range []string{"f4-detection-at-budget.svg", "f5-p-histograms.svg"} {
		if _, err := os.Stat(filepath.Join(figuresDir, name)); err != nil {
			t.Errorf("figure %s not written: %v", name, err)
		}
		if manifest[name] != "replay-fixture-001" {
			t.Errorf("manifest[%s] = %q, want replay-fixture-001", name, manifest[name])
		}
	}
	for _, name := range []string{"f3-calibration.svg", "f6-ablation-cell-grid.svg"} {
		if _, err := os.Stat(filepath.Join(figuresDir, name)); err == nil {
			t.Errorf("figure %s must not exist: no result carries its data", name)
		}
	}
	if !strings.Contains(html, "<svg") {
		t.Error("inline <svg> missing from the report")
	}
	if !strings.Contains(html, "red-team events detected (of 37 scored)") {
		t.Error("F4 y-axis title with red_team_total missing")
	}
	wantCells := []string{
		// T1, budget 10: alerts 28, TP 4, FN 37-4=33, recall 4/37, precision 4/28.
		"<td>28</td>", "<td>4</td>", "<td>33</td>", "<td>0.108</td>", "<td>0.143</td>",
		// T3: status counts and histogram mass (216 binned + 2 under for combined).
		"<td>111</td>", "<td>22</td>", "<td>218</td>",
		// T5 runtime numbers.
		"<td>12.5</td>", "<td>8250</td>", "<td>54321</td>",
	}
	for _, cell := range wantCells {
		if !strings.Contains(html, cell) {
			t.Errorf("table cell %s missing from the report", cell)
		}
	}
	if !strings.Contains(html, "abstained_structural") {
		t.Error("T3 status column header missing")
	}
}

// 3. An analysis result produces F3 with the y = x diagonal and the T2 rows.
func TestAnalysisFixtureCalibration(t *testing.T) {
	resultsDir, figuresDir, outPath := renderDirs(t)
	writeFixture(t, resultsDir, "analysis.json", analysisFixture())
	html := renderAll(t, resultsDir, figuresDir, outPath)

	raw, err := os.ReadFile(filepath.Join(figuresDir, "f3-calibration.svg"))
	if err != nil {
		t.Fatalf("F3 not written: %v", err)
	}
	if !strings.Contains(string(raw), "y = x") {
		t.Error("F3 diagonal label missing from the standalone SVG")
	}
	if got := readManifest(t, figuresDir)["f3-calibration.svg"]; got != "analysis-fixture-001" {
		t.Errorf("manifest run for F3 = %q, want analysis-fixture-001", got)
	}
	if !strings.Contains(html, "y = x") {
		t.Error("F3 diagonal label missing from the inline SVG")
	}
	for _, cell := range []string{
		"<td>benjamini-hochberg</td>", "<td>0.001</td>", "<td>0.0250</td>",
		"<td>[0.0044, 0.1268]</td>",
	} {
		if !strings.Contains(html, cell) {
			t.Errorf("T2 cell %s missing from the report", cell)
		}
	}
}

// 4. verifyProvenance accepts exactly what render produced and rejects an orphan
// figure by name.
func TestVerifyProvenanceRoundTrip(t *testing.T) {
	resultsDir, figuresDir, outPath := renderDirs(t)
	writeFixture(t, resultsDir, "replay.json", replayFixture())
	writeFixture(t, resultsDir, "analysis.json", analysisFixture())
	renderAll(t, resultsDir, figuresDir, outPath)

	results, err := loadResults(resultsDir)
	if err != nil {
		t.Fatal(err)
	}
	if err = verifyProvenance(results, figuresDir); err != nil {
		t.Fatalf("verifyProvenance must pass on what render produced: %v", err)
	}
	orphan := filepath.Join(figuresDir, "orphan.svg")
	if err = os.WriteFile(orphan, []byte("<svg xmlns=\"http://www.w3.org/2000/svg\"/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = verifyProvenance(results, figuresDir)
	if err == nil {
		t.Fatal("verifyProvenance must fail on an orphan figure")
	}
	if !strings.Contains(err.Error(), "orphan.svg") {
		t.Errorf("error must name the orphan file, got: %v", err)
	}
}

// 5. Rendering the same results twice yields byte-identical figures, manifest,
// and report.
func TestDeterministicRender(t *testing.T) {
	resultsDir, _, _ := renderDirs(t)
	writeFixture(t, resultsDir, "replay.json", replayFixture())
	writeFixture(t, resultsDir, "analysis.json", analysisFixture())
	writeFixture(t, resultsDir, "e7.json", e7Fixture())

	tmp := t.TempDir()
	figsA, outA := filepath.Join(tmp, "figs-a"), filepath.Join(tmp, "a.html")
	figsB, outB := filepath.Join(tmp, "figs-b"), filepath.Join(tmp, "b.html")
	renderAll(t, resultsDir, figsA, outA)
	renderAll(t, resultsDir, figsB, outB)

	names := []string{"f3-calibration.svg", "f4-detection-at-budget.svg",
		"f5-p-histograms.svg", "manifest.json"}
	for _, name := range names {
		a, err := os.ReadFile(filepath.Join(figsA, name))
		if err != nil {
			t.Fatalf("first render missing %s: %v", name, err)
		}
		b, err := os.ReadFile(filepath.Join(figsB, name))
		if err != nil {
			t.Fatalf("second render missing %s: %v", name, err)
		}
		if !bytes.Equal(a, b) {
			t.Errorf("%s differs between renders", name)
		}
	}
	a, _ := os.ReadFile(outA)
	b, _ := os.ReadFile(outB)
	if !bytes.Equal(a, b) {
		t.Error("report.html differs between renders")
	}
}

// A replay carrying e9_cell_arm produces F6 with the straddler subtitle, and a
// baselines result joins F4 and T1; no drawn coordinate may be NaN or infinite.
func TestF6AblationAndBaselines(t *testing.T) {
	resultsDir, figuresDir, outPath := renderDirs(t)
	writeFixture(t, resultsDir, "replay.json", e9ReplayFixture())
	writeFixture(t, resultsDir, "baselines.json", baselinesFixture())
	html := renderAll(t, resultsDir, figuresDir, outPath)

	raw, err := os.ReadFile(filepath.Join(figuresDir, "f6-ablation-cell-grid.svg"))
	if err != nil {
		t.Fatalf("F6 not written: %v", err)
	}
	if !strings.Contains(string(raw), "straddlers: circular 4/5 vs cell 1/5") {
		t.Error("F6 straddler subtitle missing the fixture counts")
	}
	if got := readManifest(t, figuresDir)["f6-ablation-cell-grid.svg"]; got != "replay-fixture-001" {
		t.Errorf("manifest run for F6 = %q, want replay-fixture-001", got)
	}
	for _, want := range []string{"iforest", "eif", "hst", "rrcf"} {
		if !strings.Contains(html, want) {
			t.Errorf("baseline series %q missing from the report", want)
		}
	}
	// Baselines record no alert count, so T1 renders those cells as absent.
	if !strings.Contains(html, "<td>"+absentCell+"</td>") {
		t.Error("T1 baseline rows must render absent alert cells, not invented ones")
	}
	svgs, _ := filepath.Glob(filepath.Join(figuresDir, "*.svg"))
	for _, p := range append(svgs, outPath) {
		content, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		for _, bad := range []string{"NaN", "Inf"} {
			if strings.Contains(string(content), bad) {
				t.Errorf("%s contains %s coordinates", filepath.Base(p), bad)
			}
		}
	}
}

// 6. A result without figure data (the E7 kind) produces tables but no figure:
// F3-F6 must not exist and the manifest stays empty.
func TestNoFigureWithoutData(t *testing.T) {
	resultsDir, figuresDir, outPath := renderDirs(t)
	writeFixture(t, resultsDir, "e7.json", e7Fixture())
	html := renderAll(t, resultsDir, figuresDir, outPath)

	if svgs, _ := filepath.Glob(filepath.Join(figuresDir, "*.svg")); len(svgs) != 0 {
		t.Errorf("no figure may be generated from an E7-only results dir, got %v", svgs)
	}
	if manifest := readManifest(t, figuresDir); len(manifest) != 0 {
		t.Errorf("want empty manifest, got %v", manifest)
	}
	if !strings.Contains(html, "proportion reconstructed 0.5000") {
		t.Error("T-E7 caption with the fixture proportion missing")
	}
	if !strings.Contains(html, "<td>49</td>") {
		t.Error("T-E7 partial count missing")
	}
}
