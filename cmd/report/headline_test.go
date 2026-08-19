package main

// Tests for the headline banner and the per-category comparison table. Like the other
// renderer tests, these render exclusively from synthetic fixtures in t.TempDir();
// the one exception reads the committed results/ directory read-only, because what it
// fixes — -verify-provenance still passing over what the repository actually carries —
// cannot be stated against a fixture.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fixtureHeadline = "At 100 alerts per analyst-day the framework detected 74 of " +
	"89 red-team events; the best baseline detected 9."

const fixtureRatioUndefined = "the baseline detected nothing in this category"

const fixtureUnmeasurable = "no labelled event in this run exhibits this category, " +
	"so neither arm can be credited or faulted for it"

func novelValueCategory() map[string]any {
	return map[string]any{
		"id":                 "novel_value",
		"structural_test":    "the entity has history for the field but has never taken this value",
		"whitepaper_section": "S3.1, S6",
		"contrast_with_marginal_detectors": "a detector fitted to a pooled feature cloud " +
			"holds no per-entity state",
	}
}

func populationRareCategory() map[string]any {
	return map[string]any{
		"id":                 "population_rare",
		"structural_test":    "the value holds less than one part in a thousand of the field mass",
		"whitepaper_section": "S9",
		"contrast_with_marginal_detectors": "this is the category isolation-based " +
			"detectors answer well",
	}
}

func burstVolumeCategory() map[string]any {
	return map[string]any{
		"id":                 "burst_volume",
		"structural_test":    "the entity emitted a burst far above its own recorded rate",
		"whitepaper_section": "S7.2",
		"contrast_with_marginal_detectors": "a pooled detector has no per-entity rate " +
			"to compare against",
	}
}

func recallInterval(point, low, high float64, n int) map[string]any {
	return map[string]any{"point": point, "low": low, "high": high, "n": n}
}

// unmeasurableRowFixture carries the zero-valued counters cmd/analyse writes alongside
// the sentence; the renderer must render the sentence and never those zeros.
func unmeasurableRowFixture() map[string]any {
	return map[string]any{
		"category": populationRareCategory(), "budget_per_day": 100,
		"baseline": "iforest", "unmeasurable": fixtureUnmeasurable,
		"red_team_events_in_category": 0, "framework_detected": 0,
		"framework_recall":  recallInterval(0, 0, 0, 0),
		"baseline_detected": 0, "baseline_recall": recallInterval(0, 0, 0, 0),
		"delta_percentage_points": 0, "common_days": []any{},
	}
}

// headlineAnalysisFixture is an analysis result carrying the headline and the
// per-category comparison: a row with a recorded ratio, a row whose ratio is recorded
// as undefined, a negative-delta row, a zero-delta row, an unmeasurable row, and a
// smaller-budget row the largest-budget rule must exclude. Values are deliberately
// arbitrary and clearly synthetic.
func headlineAnalysisFixture() map[string]any {
	return map[string]any{
		"schema_version": "1",
		"kind":           "analysis",
		"hypothesis":     []string{"E1", "E2"},
		"run": map[string]any{
			"run_id": "analysis-headline-001", "parent_run": "replay-fixture-001",
			"started_at": "2026-01-04T00:00:00Z", "finished_at": "2026-01-04T00:05:00Z",
		},
		"provenance_complete": true,
		"results": map[string]any{
			"headline":        fixtureHeadline,
			"baseline_source": map[string]any{"run_id": "baselines-fixture-901"},
			"red_team_population": map[string]any{
				"n": 89, "source": "events named by the corpus red-team file within the replayed window",
			},
			"category_comparison": []any{
				map[string]any{
					"category": novelValueCategory(), "budget_per_day": 100,
					"red_team_events_in_category": 20, "framework_detected": 15,
					"framework_recall": recallInterval(0.75, 0.531, 0.888, 20),
					"baseline":         "iforest", "baseline_detected": 3,
					"baseline_recall":         recallInterval(0.15, 0.052, 0.36, 20),
					"delta_percentage_points": 60.0, "times_better": 5.0,
					"common_days": []any{1, 2, 3},
				},
				map[string]any{
					"category": novelValueCategory(), "budget_per_day": 100,
					"red_team_events_in_category": 20, "framework_detected": 12,
					"framework_recall": recallInterval(0.6, 0.387, 0.781, 20),
					"baseline":         "rrcf", "baseline_detected": 0,
					"baseline_recall":         recallInterval(0, 0, 0.161, 20),
					"delta_percentage_points": 60.0, "ratio_undefined": fixtureRatioUndefined,
					"common_days": []any{1, 2, 3},
				},
				unmeasurableRowFixture(),
				map[string]any{
					"category": burstVolumeCategory(), "budget_per_day": 100,
					"red_team_events_in_category": 16, "framework_detected": 3,
					"framework_recall": recallInterval(0.188, 0.061, 0.434, 16),
					"baseline":         "iforest", "baseline_detected": 5,
					"baseline_recall":         recallInterval(0.313, 0.139, 0.561, 16),
					"delta_percentage_points": -12.5, "times_better": 0.6,
					"common_days": []any{1, 2, 3},
				},
				map[string]any{
					"category": burstVolumeCategory(), "budget_per_day": 100,
					"red_team_events_in_category": 16, "framework_detected": 4,
					"framework_recall": recallInterval(0.25, 0.098, 0.508, 16),
					"baseline":         "rrcf", "baseline_detected": 4,
					"baseline_recall":         recallInterval(0.25, 0.098, 0.508, 16),
					"delta_percentage_points": 0, "times_better": 1.0,
					"common_days": []any{1, 2, 3},
				},
				map[string]any{
					"category": novelValueCategory(), "budget_per_day": 10,
					"red_team_events_in_category": 17, "framework_detected": 6,
					"framework_recall": recallInterval(0.353, 0.174, 0.587, 17),
					"baseline":         "iforest", "baseline_detected": 1,
					"baseline_recall":         recallInterval(0.059, 0.01, 0.27, 17),
					"delta_percentage_points": 29.4, "times_better": 6.0,
					"common_days": []any{1, 2, 3},
				},
			},
		},
	}
}

func renderHeadlineFixture(t *testing.T) string {
	t.Helper()
	resultsDir, figuresDir, outPath := renderDirs(t)
	writeFixture(t, resultsDir, "analysis-headline.json", headlineAnalysisFixture())
	return renderAll(t, resultsDir, figuresDir, outPath)
}

// 1a. A result carrying results.headline renders it verbatim as the banner, with the
// baseline run and the analysis file name beneath it for traceability.
func TestHeadlineBannerRendersVerbatim(t *testing.T) {
	html := renderHeadlineFixture(t)

	if !strings.Contains(html, fixtureHeadline) {
		t.Error("the recorded headline sentence is not rendered verbatim")
	}
	if !strings.Contains(html, `class="banner"`) {
		t.Error("no banner element was rendered")
	}
	trace := `<div class="trace">baseline run baselines-fixture-901 · analysis-headline.json</div>`
	if !strings.Contains(html, trace) {
		t.Error("the banner trace does not name the baseline run and the analysis file")
	}
}

// 1b. Without a headline there is no banner element at all — not an empty box — and
// an empty headline string is the same absence.
func TestNoBannerWithoutHeadline(t *testing.T) {
	resultsDir, figuresDir, outPath := renderDirs(t)
	writeFixture(t, resultsDir, "analysis.json", analysisFixture())
	if html := renderAll(t, resultsDir, figuresDir, outPath); strings.Contains(html, `class="banner"`) {
		t.Error("a banner element was rendered with no recorded headline")
	}

	resultsDir, figuresDir, outPath = renderDirs(t)
	doc := headlineAnalysisFixture()
	doc["results"].(map[string]any)["headline"] = ""
	writeFixture(t, resultsDir, "analysis-headline.json", doc)
	html := renderAll(t, resultsDir, figuresDir, outPath)
	if strings.Contains(html, `class="banner"`) {
		t.Error("a banner element was rendered from an empty headline")
	}
	if !strings.Contains(html, "T-E1E2") {
		t.Error("the category table must render independently of the banner")
	}
}

// 2. An unmeasurable row renders its recorded sentence across the numeric columns and
// no digits — the zero-valued counters written alongside it must not reach the page.
func TestUnmeasurableRowRendersSentenceNotNumbers(t *testing.T) {
	resultsDir, figuresDir, outPath := renderDirs(t)
	doc := headlineAnalysisFixture()
	doc["results"].(map[string]any)["category_comparison"] = []any{unmeasurableRowFixture()}
	writeFixture(t, resultsDir, "analysis-headline.json", doc)
	html := renderAll(t, resultsDir, figuresDir, outPath)

	wantRow := `<td>population_rare</td><td>iforest</td>` +
		`<td colspan="8" class="unmeasurable">` + fixtureUnmeasurable + `</td>`
	if !strings.Contains(html, wantRow) {
		t.Error("the unmeasurable row does not render its recorded sentence across the numeric columns")
	}
	for _, leaked := range []string{"<td>0</td>", ">0.000", "&#43;0.0", `class="delta`} {
		if strings.Contains(html, leaked) {
			t.Errorf("an unmeasurable row leaked %q into the page", leaked)
		}
	}
}

// 3. A recorded ratio renders; an undefined one renders the em dash with the recorded
// explanation in the caveat list, and no ratio is manufactured from the counts.
func TestRatioUndefinedRendersEmDashNeverARatio(t *testing.T) {
	html := renderHeadlineFixture(t)

	if !strings.Contains(html, "<td>5.0×</td>") {
		t.Error("the recorded times_better ratio is not rendered")
	}
	// The undefined-ratio row: delta, then the em dash where the ratio would sit.
	// html/template escapes "+" to &#43; (an anti-UTF-7 measure); it displays as +.
	if !strings.Contains(html, `&#43;60.0</td><td>—</td><td>3</td>`) {
		t.Error("the undefined ratio does not render as an em dash")
	}
	if !strings.Contains(html, "<li>"+fixtureRatioUndefined+"</li>") {
		t.Error("the recorded ratio_undefined explanation is missing from the caveat list")
	}
	// framework 12 against baseline 0 must never become a manufactured ratio.
	for _, invented := range []string{"12.0×", "Inf", "NaN"} {
		if strings.Contains(html, invented) {
			t.Errorf("a ratio was manufactured: %q", invented)
		}
	}
}

// The delta column is signed percentage points with the existing tones: positive
// good, negative critical, zero neutral.
func TestDeltaSignedPercentagePoints(t *testing.T) {
	html := renderHeadlineFixture(t)

	if !strings.Contains(html, "Δ recall (percentage points)") {
		t.Error("the delta column is not labelled as percentage points")
	}
	// html/template escapes "+" to &#43; (an anti-UTF-7 measure); it displays as +.
	if !strings.Contains(html, `class="delta-good">&#43;60.0</td>`) {
		t.Error("a positive delta is not rendered signed with the good tone")
	}
	if !strings.Contains(html, `class="delta-crit">-12.5</td>`) {
		t.Error("a negative delta is not rendered signed with the critical tone")
	}
	if !strings.Contains(html, "<td>0.0</td>") {
		t.Error("a zero delta is not rendered neutrally, without a tone class")
	}
}

// 4. An absent category_comparison block renders no table at all and does not panic;
// an empty list is the same absence, and the banner still renders whole.
func TestAbsentCategoryComparisonRendersNoTable(t *testing.T) {
	resultsDir, figuresDir, outPath := renderDirs(t)
	writeFixture(t, resultsDir, "analysis.json", analysisFixture())
	html := renderAll(t, resultsDir, figuresDir, outPath)
	if strings.Contains(html, "T-E1E2") || strings.Contains(html, "Per-category comparison") {
		t.Error("a category table was rendered with no category_comparison block")
	}

	resultsDir, figuresDir, outPath = renderDirs(t)
	doc := headlineAnalysisFixture()
	doc["results"].(map[string]any)["category_comparison"] = []any{}
	writeFixture(t, resultsDir, "analysis-headline.json", doc)
	html = renderAll(t, resultsDir, figuresDir, outPath)
	if strings.Contains(html, "T-E1E2") {
		t.Error("a category table was rendered from an empty category_comparison list")
	}
	if !strings.Contains(html, `class="banner"`) {
		t.Error("the banner must render independently of the category table")
	}
}

// 5. The table is shown at the largest recorded budget, the caption says which, and
// the smaller-budget rows do not leak in.
func TestLargestBudgetSelected(t *testing.T) {
	html := renderHeadlineFixture(t)

	if !strings.Contains(html, "largest recorded budget: 100 alerts per analyst-day") {
		t.Error("the caption does not state the budget the table is shown at")
	}
	if strings.Contains(html, "<td>17</td>") || strings.Contains(html, "6.0×") {
		t.Error("a smaller-budget row leaked into the table")
	}
	if strings.Contains(html, "no tables yet") {
		t.Error("the category table did not count as a table")
	}
	if !strings.Contains(html, "recall denominator — events named by the corpus red-team file") {
		t.Error("the red_team_population.source note is missing beside the recall figures")
	}
}

// 6. The taxonomy deduplicates by category id, keeping first-seen order: a category
// compared against two baselines and two budgets states its argument once.
func TestTaxonomyDeduplicatesByCategory(t *testing.T) {
	html := renderHeadlineFixture(t)

	contrast := "a detector fitted to a pooled feature cloud holds no per-entity state"
	if got := strings.Count(html, contrast); got != 1 {
		t.Errorf("novel_value states its contrast %d times, want once", got)
	}
	structurals := []string{
		"the entity has history for the field but has never taken this value",
		"the value holds less than one part in a thousand of the field mass",
		"the entity emitted a burst far above its own recorded rate",
	}
	last := -1
	for _, want := range structurals {
		at := strings.Index(html, want)
		if at < 0 {
			t.Errorf("taxonomy omits %q", want)
			continue
		}
		if at < last {
			t.Errorf("taxonomy is not in first-seen order at %q", want)
		}
		last = at
	}
	for _, want := range []string{"S3.1, S6", "S9", "S7.2",
		"this is the category isolation-based detectors answer well",
		"a pooled detector has no per-entity rate to compare against"} {
		if !strings.Contains(html, want) {
			t.Errorf("taxonomy omits %q", want)
		}
	}
}

// 7. -verify-provenance still passes over the committed results/ directory. This test
// reads the repository's results and figures; it writes nothing.
func TestVerifyProvenanceOnCommittedResults(t *testing.T) {
	resultsDir := filepath.Join("..", "..", "results")
	figuresDir := filepath.Join("..", "..", "docs", "figures")
	if _, err := os.Stat(resultsDir); err != nil {
		t.Skipf("committed results directory not present: %v", err)
	}
	results, err := loadResults(resultsDir)
	if err != nil {
		t.Fatalf("loadResults over the committed results/: %v", err)
	}
	if err := verifyProvenance(results, figuresDir); err != nil {
		t.Fatalf("-verify-provenance no longer passes on the committed results/: %v", err)
	}
}
