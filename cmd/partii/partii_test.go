package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"unicode"
)

// ---------------------------------------------------------------------------
// Fixtures. The sentences live in constants so the assertions and the JSON cannot
// drift apart.
// ---------------------------------------------------------------------------

const fixtureHeadline = "At a matched budget of 100 alerts per analyst-day the framework detected 400 of 653 red-team events."

const fixtureRatioUndefined = "the baseline detected nothing at this budget, so a relative improvement is a division by zero; the difference is reported in percentage points of recall"

const fixtureUnmeasurable = "rrcf recorded its detections as a bare count without naming the events, so per-category attribution is impossible for it"

const fixtureCaveat = "the per-day budget of the framework is exact; each baseline threshold is a quantile estimated from a sample"

const fixtureTreatmentC = "NOT RUN: mask embedding and borrowing requires a mask-similarity model; a shallow version degrades to treatment A without reporting it"

const completeAnalysisJSON = `{
 "schema_version": "1",
 "kind": "analysis",
 "hypothesis": ["E1", "E2", "E3", "E4", "E9"],
 "run": {"run_id": "analysis-fixture-001", "git_sha": "abcdef1234567890abcdef", "git_dirty": false},
 "parameters": {
  "ground_truth_caveat": "unlabelled events count as negatives, so realised FDR is an upper bound"
 },
 "results": {
  "headline": "` + fixtureHeadline + `",
  "head_to_head": [
   {
    "budget_per_day": 100,
    "framework_detected": 400,
    "framework_recall": {"point": 0.6126, "low": 0.575, "high": 0.649, "n": 653, "level": 0.95, "kind": "wilson"},
    "baseline": "iforest",
    "baseline_detected": 0,
    "baseline_recall": {"point": 0, "low": 0, "high": 0.006, "n": 653, "level": 0.95, "kind": "wilson"},
    "red_team_events": 653,
    "delta_percentage_points": 61.26,
    "ratio_undefined": "` + fixtureRatioUndefined + `",
    "mcnemar": {"statistic": 400, "p_value": 1.2e-90, "exact": true, "only_a": 400, "only_b": 0},
    "bootstrap_delta": {"observed_delta": 400, "low": 361, "high": 438},
    "common_days": [7, 8, 9],
    "caveats": ["` + fixtureCaveat + `"]
   },
   {
    "budget_per_day": 100,
    "framework_detected": 400,
    "framework_recall": {"point": 0.6126, "low": 0.575, "high": 0.649, "n": 653, "level": 0.95, "kind": "wilson"},
    "baseline": "rrcf",
    "baseline_detected": 40,
    "baseline_recall": {"point": 0.0613, "low": 0.045, "high": 0.082, "n": 653, "level": 0.95, "kind": "wilson"},
    "red_team_events": 653,
    "delta_percentage_points": 55.13,
    "times_better": 10.0,
    "mcnemar": {"statistic": 320, "p_value": 4.4e-71, "exact": false, "only_a": 372, "only_b": 12},
    "bootstrap_delta": {"observed_delta": 360, "low": 322, "high": 397},
    "common_days": [7, 8, 9],
    "caveats": ["` + fixtureCaveat + `"]
   }
  ],
  "category_comparison": [
   {
    "category": {"id": "population_rare", "structural_test": "t", "whitepaper_section": "s", "contrast_with_marginal_detectors": "c"},
    "budget_per_day": 100,
    "red_team_events_in_category": 121,
    "framework_detected": 90,
    "framework_recall": {"point": 0.7438, "low": 0.658, "high": 0.815, "n": 121, "level": 0.95, "kind": "wilson"},
    "baseline": "iforest",
    "baseline_detected": 0,
    "baseline_recall": {"point": 0, "low": 0, "high": 0.031, "n": 121, "level": 0.95, "kind": "wilson"},
    "delta_percentage_points": 74.38,
    "ratio_undefined": "` + fixtureRatioUndefined + `",
    "common_days": [7, 8]
   },
   {
    "category": {"id": "first_contact", "structural_test": "t", "whitepaper_section": "s", "contrast_with_marginal_detectors": "c"},
    "budget_per_day": 100,
    "red_team_events_in_category": 0,
    "framework_detected": 0,
    "baseline": "rrcf",
    "baseline_detected": 0,
    "delta_percentage_points": 0,
    "unmeasurable": "` + fixtureUnmeasurable + `",
    "common_days": [7, 8]
   },
   {
    "category": {"id": "population_rare", "structural_test": "t", "whitepaper_section": "s", "contrast_with_marginal_detectors": "c"},
    "budget_per_day": 50,
    "red_team_events_in_category": 121,
    "framework_detected": 61,
    "framework_recall": {"point": 0.5041, "low": 0.415, "high": 0.593, "n": 121, "level": 0.95, "kind": "wilson"},
    "baseline": "iforest",
    "baseline_detected": 0,
    "baseline_recall": {"point": 0, "low": 0, "high": 0.031, "n": 121, "level": 0.95, "kind": "wilson"},
    "delta_percentage_points": 50.41,
    "ratio_undefined": "` + fixtureRatioUndefined + `",
    "common_days": [7, 8]
   }
  ],
  "calibration_bh": [
   {"nominal_q": 0.01, "procedure": "BH per-day", "discoveries": 120, "true_positives": 118,
    "realised_fdr": {"point": 0.0167, "low": 0.005, "high": 0.04, "n": 120, "level": 0.95, "kind": "wilson"},
    "conservatism_ratio": 0.6, "saturated_days": 2}
  ],
  "calibration_by": [
   {"nominal_q": 0.01, "procedure": "BY per-day", "discoveries": 80, "true_positives": 79,
    "realised_fdr": {"point": 0.0125, "low": 0.003, "high": 0.03, "n": 80, "level": 0.95, "kind": "wilson"},
    "conservatism_ratio": 0.4, "saturated_days": 1}
  ],
  "ablations": {
   "e4": {
    "hypothesis": "E4", "arm": "cooccurrence-partitioned",
    "design": "paired: both arms scored the same events",
    "per_budget": [
     {"budget_per_day": 100, "paired_events": 653, "framework_detected": 400, "arm_detected": 352,
      "mcnemar": {"statistic": 27.4, "p_value": 3.1e-7, "exact": false, "only_a": 66, "only_b": 18},
      "bootstrap_delta": {"observed_delta": 48, "low": 21, "high": 75}}
    ]
   },
   "e9": {
    "hypothesis": "E9", "arm": "timing-cells-168",
    "design": "paired: both arms scored the same events",
    "per_budget": [
     {"budget_per_day": 100, "paired_events": 653, "framework_detected": 400, "arm_detected": 331,
      "mcnemar": {"statistic": 41.2, "p_value": 6.9e-11, "exact": false, "only_a": 84, "only_b": 15},
      "bootstrap_delta": {"observed_delta": 69, "low": 38, "high": 99}}
    ]
   }
  }
 },
 "provenance_complete": true
}`

const e5FixtureJSON = `{
 "schema_version": "1",
 "kind": "e5",
 "hypothesis": ["E5"],
 "run": {"run_id": "e5-fixture-001"},
 "parameters": {
  "n0": 1000,
  "treatment_c": "` + fixtureTreatmentC + `"
 },
 "results": {
  "arms": {
   "auth.logon_type|A": {
    "held_out_field": "auth.logon_type", "treatment": "A",
    "max_pairwise_mi": 0.42, "mi_against_field": "auth.authentication_type",
    "eras": [
     {"era": "pre_introduction", "nominal_q": 0.01, "discoveries": 120, "true_positives": 118, "realised_fdr": 0.0167},
     {"era": "post_introduction", "nominal_q": 0.01, "discoveries": 130, "true_positives": 127, "realised_fdr": 0.0231}
    ]
   },
   "auth.logon_type|B": {
    "held_out_field": "auth.logon_type", "treatment": "B",
    "max_pairwise_mi": 0.42, "mi_against_field": "auth.authentication_type",
    "eras": [
     {"era": "pre_introduction", "nominal_q": 0.01, "discoveries": 121, "true_positives": 118, "realised_fdr": 0.0248},
     {"era": "post_introduction", "nominal_q": 0.01, "discoveries": 140, "true_positives": 131, "realised_fdr": 0.0643}
    ]
   }
  }
 },
 "provenance_complete": true
}`

func mustParse(t *testing.T, file, raw string) result {
	t.Helper()
	var data map[string]any
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		t.Fatalf("fixture %s does not parse: %v", file, err)
	}
	return result{File: file, Data: data}
}

func writeFixture(t *testing.T, dir, name, raw string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(raw), 0o600); err != nil {
		t.Fatalf("write fixture %s: %v", name, err)
	}
}

// sectionBodyHTML renders a single section through the real template and returns the
// section's markup after its heading, so tests can assert on exactly what a reader of
// that section sees.
func sectionBodyHTML(t *testing.T, s section) string {
	t.Helper()
	var buf strings.Builder
	f := findings{Generated: "generated-date", Sections: []section{s}}
	if err := paperTemplate.Execute(&buf, f); err != nil {
		t.Fatalf("execute template: %v", err)
	}
	html := buf.String()
	start := strings.Index(html, "<section>")
	end := strings.Index(html, "</section>")
	if start < 0 || end < 0 {
		t.Fatal("section markers not found in rendered document")
	}
	chunk := html[start:end]
	const headingEnd = "</h2>"
	h := strings.Index(chunk, headingEnd)
	if h < 0 {
		t.Fatal("heading not found in rendered section")
	}
	return chunk[h+len(headingEnd):]
}

// ---------------------------------------------------------------------------
// 1. The provenance rule, executably: a result claiming E1 with an empty results
// block yields a pending section whose rendered body carries no digit at all.
// ---------------------------------------------------------------------------

func TestSectionE1E2PendingWithoutHeadToHead(t *testing.T) {
	empty := mustParse(t, "analysis-pending.json", `{
		"kind": "analysis",
		"hypothesis": ["E1", "E2"],
		"results": {}
	}`)
	s := sectionE1E2([]result{empty})
	if s.Pending == "" {
		t.Fatal("expected a pending section when results.head_to_head is absent")
	}
	if s.Table != nil || s.Table2 != nil {
		t.Fatal("a pending section must carry no table")
	}
	if len(s.Paras) != 0 {
		t.Fatal("a pending section must carry no paragraphs")
	}
	if !strings.Contains(s.Pending, "analysis-pending.json") ||
		!strings.Contains(s.Pending, "results.head_to_head") {
		t.Fatalf("pending must name the file and the missing key, got: %s", s.Pending)
	}
	body := sectionBodyHTML(t, s)
	for _, r := range body {
		if unicode.IsDigit(r) {
			t.Fatalf("pending section body contains a digit:\n%s", body)
		}
	}
}

func TestSectionE1E2MeasuredFromCompleteFixture(t *testing.T) {
	r := mustParse(t, "analysis-complete.json", completeAnalysisJSON)
	s := sectionE1E2([]result{r})
	if s.Pending != "" {
		t.Fatalf("expected a measured section, got pending: %s", s.Pending)
	}
	if len(s.Paras) == 0 || s.Paras[0] != fixtureHeadline {
		t.Fatalf("the recorded headline must be the first paragraph verbatim, got %q", s.Paras)
	}
	if s.Table == nil || s.Table2 == nil {
		t.Fatal("both the aggregate and the per-category tables must be present")
	}
	if got := len(s.Table2.Rows); got != 2 {
		t.Fatalf("category table must hold only the largest-budget rows, got %d rows", got)
	}
	body := sectionBodyHTML(t, s)
	for _, want := range []string{"400 of 653", "61.3% [57.5, 64.9]", "10.0×", fixtureCaveat} {
		if !strings.Contains(body, want) {
			t.Errorf("rendered section is missing %q", want)
		}
	}
}

// ---------------------------------------------------------------------------
// 2. An unmeasurable category row renders its recorded sentence, never a zero.
// ---------------------------------------------------------------------------

func TestUnmeasurableCategoryRowRendersSentenceNotZero(t *testing.T) {
	r := mustParse(t, "analysis-complete.json", completeAnalysisJSON)
	s := sectionE1E2([]result{r})
	if s.Table2 == nil {
		t.Fatal("category table missing")
	}
	found := false
	for _, row := range s.Table2.Rows {
		if row[0] != "first_contact" {
			continue
		}
		found = true
		if !slices.Contains(row, fixtureUnmeasurable) {
			t.Fatalf("unmeasurable row must carry the recorded sentence, got %v", row)
		}
		for _, cell := range row[2:] {
			if cell != fixtureUnmeasurable && cell != "—" {
				t.Fatalf("unmeasurable row must render no numbers, got cell %q", cell)
			}
		}
	}
	if !found {
		t.Fatal("fixture category row not found")
	}
	if body := sectionBodyHTML(t, s); !strings.Contains(body, fixtureUnmeasurable) {
		t.Fatal("the unmeasurable sentence must render in the document")
	}
}

// ---------------------------------------------------------------------------
// 3. A row whose ratio is recorded as undefined never renders a ratio.
// ---------------------------------------------------------------------------

func TestRatioUndefinedNeverRendersRatio(t *testing.T) {
	r := mustParse(t, "analysis-complete.json", completeAnalysisJSON)
	s := sectionE1E2([]result{r})
	if s.Table == nil {
		t.Fatal("aggregate table missing")
	}
	ratioCol := slices.Index(s.Table.Head, "Ratio")
	if ratioCol < 0 {
		t.Fatal("aggregate table has no Ratio column")
	}
	iforestRow := s.Table.Rows[0]
	if iforestRow[1] != "iforest" {
		t.Fatalf("expected the iforest row first, got %v", iforestRow)
	}
	if got := iforestRow[ratioCol]; got != "—" {
		t.Fatalf("undefined ratio must render as the em-dash, got %q", got)
	}
	if !slices.Contains(s.Caveats, fixtureRatioUndefined) {
		t.Fatal("the recorded explanation must appear in the visible caveat list")
	}
	if body := sectionBodyHTML(t, s); !strings.Contains(body, fixtureRatioUndefined) {
		t.Fatal("the recorded explanation must render in the document")
	}
}

// ---------------------------------------------------------------------------
// 4. An empty results directory still renders, with all nine sections pending.
// ---------------------------------------------------------------------------

func TestEmptyResultsDirectoryRendersAllPending(t *testing.T) {
	out := filepath.Join(t.TempDir(), "part-ii.html")
	if err := render(t.TempDir(), out); err != nil {
		t.Fatalf("render from an empty directory must still succeed: %v", err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	html := string(raw)
	if !strings.Contains(html, "0 measured, 9 not yet measured") {
		t.Fatal("expected the scoreboard to report all nine sections pending")
	}
	if !strings.Contains(html, "Not yet measured:") {
		t.Fatal("expected explicit not-yet-measured panels")
	}
	if !strings.Contains(html, "No result files were found") {
		t.Fatal("expected the provenance table to say no result files were found")
	}
}

// ---------------------------------------------------------------------------
// 5. fmtInt thousands separators.
// ---------------------------------------------------------------------------

func TestFmtIntThousandsSeparators(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "0"},
		{7, "7"},
		{999, "999"},
		{1000, "1,000"},
		{1234567, "1,234,567"},
		{-1234567, "-1,234,567"},
	}
	for _, c := range cases {
		if got := fmtInt(c.in); got != c.want {
			t.Errorf("fmtInt(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// ---------------------------------------------------------------------------
// 6. Golden-ish: identical input renders identical bytes apart from the date.
// ---------------------------------------------------------------------------

func TestRenderIsDeterministicUpToGeneratedDate(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "analysis-complete.json", completeAnalysisJSON)
	writeFixture(t, dir, "e5-fixture.json", e5FixtureJSON)
	out1 := filepath.Join(t.TempDir(), "one.html")
	out2 := filepath.Join(t.TempDir(), "two.html")
	if err := render(dir, out1); err != nil {
		t.Fatalf("first render: %v", err)
	}
	if err := render(dir, out2); err != nil {
		t.Fatalf("second render: %v", err)
	}
	raw1, err := os.ReadFile(out1)
	if err != nil {
		t.Fatalf("read first output: %v", err)
	}
	raw2, err := os.ReadFile(out2)
	if err != nil {
		t.Fatalf("read second output: %v", err)
	}
	gen := regexp.MustCompile(`<span class="gen">[^<]*</span>`)
	a := gen.ReplaceAllString(string(raw1), "")
	b := gen.ReplaceAllString(string(raw2), "")
	if a != b {
		t.Fatal("rendering twice from identical input must give identical bytes apart from the generated date")
	}
}

// ---------------------------------------------------------------------------
// E5's deliberately-unrun treatment C renders its recorded reason as text.
// ---------------------------------------------------------------------------

func TestSectionE5PrintsTreatmentCReasonAsText(t *testing.T) {
	r := mustParse(t, "e5-fixture.json", e5FixtureJSON)
	s := sectionE5([]result{r})
	if s.Pending != "" {
		t.Fatalf("expected a measured section, got pending: %s", s.Pending)
	}
	if !strings.Contains(strings.Join(s.Paras, " "), fixtureTreatmentC) {
		t.Fatal("treatment C's recorded reason must be printed as text")
	}
	if s.Table == nil || len(s.Table.Rows) == 0 {
		t.Fatal("the treatment table must be present")
	}
}

// ---------------------------------------------------------------------------
// The committed result files: the sections they claim render measured, and the
// hypotheses without an analysis or e5 result render pending.
// ---------------------------------------------------------------------------

func TestCommittedResultsRenderMeasuredSections(t *testing.T) {
	dir := filepath.Join("..", "..", "results")
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("results directory not available: %v", err)
	}
	rs, err := load(dir)
	if err != nil {
		t.Fatalf("load committed results: %v", err)
	}
	measured := map[string]func([]result) section{
		"E8":       sectionE8,
		"E7":       sectionE7,
		"E6":       sectionE6,
		"controls": sectionControls,
	}
	for name, fn := range measured {
		if s := fn(rs); s.Pending != "" {
			t.Errorf("%s should be measured from the committed results, got pending: %s", name, s.Pending)
		}
	}
	if _, ok := byKind(rs, "analysis"); !ok {
		for name, fn := range map[string]func([]result) section{
			"E1E2": sectionE1E2, "E3": sectionE3, "E4": sectionE4, "E9": sectionE9,
		} {
			if s := fn(rs); s.Pending == "" {
				t.Errorf("%s must be pending while no analysis result exists", name)
			}
		}
	}
	if _, ok := byKind(rs, "e5"); !ok {
		if s := sectionE5(rs); s.Pending == "" {
			t.Error("E5 must be pending while no e5 result exists")
		}
	}
	out := filepath.Join(t.TempDir(), "part-ii.html")
	if renderErr := render(dir, out); renderErr != nil {
		t.Fatalf("render committed results: %v", renderErr)
	}
	raw, readErr := os.ReadFile(out)
	if readErr != nil {
		t.Fatalf("read output: %v", readErr)
	}
	if !regexp.MustCompile(`\d+ measured, \d+ not yet measured`).Match(raw) {
		t.Fatal("scoreboard line missing from the rendered document")
	}
}
