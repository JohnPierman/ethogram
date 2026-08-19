package main

// The tests enforce the command's one rule: every number in README.md came out of the
// result file. The minimal fixture below carries no numbers at all, so its rendered
// report must carry none either — any digit in that output is an invented figure.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// writeRun renders one fixture into a fresh out directory and returns the run folder.
func writeRun(t *testing.T, fixture string) string {
	t.Helper()
	tmp := t.TempDir()
	resultPath := filepath.Join(tmp, "fixture.json")
	if err := os.WriteFile(resultPath, []byte(fixture), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	dir, err := writeRunFolder(resultPath, filepath.Join(tmp, "runs"))
	if err != nil {
		t.Fatalf("writeRunFolder: %v", err)
	}
	return dir
}

func readmeOf(t *testing.T, dir string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	return string(raw)
}

// ---------------------------------------------------------------------------
// 1. A result carrying only provenance renders with nothing invented.
// ---------------------------------------------------------------------------

const minimalFixture = `{"schema_version": "one", "run": {"run_id": "minimal-run"}}`

func TestMinimalResultInventsNothing(t *testing.T) {
	dir := writeRun(t, minimalFixture)
	readme := readmeOf(t, dir)

	if !strings.Contains(readme, "# Run minimal-run") {
		t.Error("the report does not open with the run id")
	}
	for _, heading := range []string{
		"## Detection at matched budget", "## Anomaly categories",
		"## Head-to-head", "## Calibration",
	} {
		if strings.Contains(readme, heading) {
			t.Errorf("optional section %q rendered although the result carries no data for it", heading)
		}
	}
	if !strings.Contains(readme, "not recorded") {
		t.Error("absent keys are not named as not recorded")
	}
	// The fixture carries no numbers, so the report must carry none: a digit here is
	// a figure that did not come out of the result file.
	if digit := regexp.MustCompile(`[0-9]`).FindString(readme); digit != "" {
		t.Errorf("the report contains digit %q although the result file carries no numbers:\n%s", digit, readme)
	}

	var summary map[string]any
	raw, err := os.ReadFile(filepath.Join(dir, "summary.json"))
	if err != nil {
		t.Fatalf("read summary.json: %v", err)
	}
	if err := json.Unmarshal(raw, &summary); err != nil {
		t.Fatalf("parse summary.json: %v", err)
	}
	if summary["run_id"] != "minimal-run" {
		t.Errorf("summary run_id = %v, want minimal-run", summary["run_id"])
	}
	for key := range summary {
		if key != "run_id" {
			t.Errorf("summary carries %q although the result did not record it", key)
		}
	}
}

// ---------------------------------------------------------------------------
// 2. A sampled run renders the warning blockquote with the recorded note verbatim.
// ---------------------------------------------------------------------------

const sampledNote = "a deterministic sample of ENTITIES, not of events: a detection rate measured here is NOT comparable to one measured on the full population"

const sampledFixture = `{
 "schema_version": "1",
 "run": {"run_id": "sampled-run"},
 "parameters": {
  "entity_sample": {
   "applied": true,
   "keep_one_in_n": 100,
   "selector": "FNV-1a 64 of the entity identifier, modulo N, equals zero",
   "note": "` + sampledNote + `"
  }
 }
}`

func TestSampledRunRendersWarningBlockquote(t *testing.T) {
	readme := readmeOf(t, writeRun(t, sampledFixture))
	if !strings.Contains(readme, "> "+sampledNote) {
		t.Error("the recorded sampling note is not quoted verbatim in a blockquote")
	}
	if !strings.Contains(readme, "WARNING") {
		t.Error("the sampling section carries no warning")
	}
	if !strings.Contains(readme, "100") {
		t.Error("the recorded keep_one_in_n is not rendered")
	}
}

const unsampledFixture = `{
 "schema_version": "1",
 "run": {"run_id": "unsampled-run"},
 "parameters": {"entity_sample": {"applied": false}}
}`

func TestUnsampledRunStatesFullPopulation(t *testing.T) {
	readme := readmeOf(t, writeRun(t, unsampledFixture))
	if !strings.Contains(readme, "full population was scored") {
		t.Error("an unsampled run does not state that the full population was scored")
	}
	if strings.Contains(readme, "WARNING") {
		t.Error("an unsampled run renders the sampling warning")
	}
}

// ---------------------------------------------------------------------------
// 3. Saturated calibration days raise the censoring warning; unsaturated do not.
// ---------------------------------------------------------------------------

func calibrationFixture(saturatedDays int) string {
	return fmt.Sprintf(`{
 "schema_version": "1",
 "run": {"run_id": "calibration-run"},
 "results": {
  "calibration_bh": [
   {"procedure": "benjamini-hochberg", "nominal_q": 0.05, "discoveries": 120,
    "true_positives": 80,
    "realised_fdr": {"point": 0.333, "low": 0.25, "high": 0.42},
    "conservatism_ratio": 6.7, "saturated_days": %d}
  ]
 }
}`, saturatedDays)
}

func TestSaturatedDaysRaiseCensoringWarning(t *testing.T) {
	readme := readmeOf(t, writeRun(t, calibrationFixture(3)))
	if !strings.Contains(readme, "right-censored") {
		t.Error("saturated_days > 0 does not raise the right-censoring warning")
	}
	if !strings.Contains(readme, "benjamini-hochberg") {
		t.Error("the calibration table does not render the recorded procedure")
	}
}

func TestUnsaturatedDaysRaiseNoWarning(t *testing.T) {
	readme := readmeOf(t, writeRun(t, calibrationFixture(0)))
	if strings.Contains(readme, "right-censored") {
		t.Error("saturated_days = 0 raises the right-censoring warning")
	}
	if !strings.Contains(readme, "## Calibration") {
		t.Error("the calibration section is missing although the result carries calibration_bh")
	}
}

// ---------------------------------------------------------------------------
// 4. ratio_undefined renders the em-dash and the explanation; times_better a ratio.
// ---------------------------------------------------------------------------

const ratioExplanation = "the baseline detected nothing at this budget, so a relative improvement is a division by zero"

const headToHeadFixture = `{
 "schema_version": "1",
 "run": {"run_id": "ratio-run"},
 "results": {
  "head_to_head": [
   {"budget_per_day": 10, "baseline": "eif", "framework_detected": 5,
    "baseline_detected": 0, "delta_percentage_points": 0.9,
    "ratio_undefined": "` + ratioExplanation + `"},
   {"budget_per_day": 25, "baseline": "pca", "framework_detected": 8,
    "baseline_detected": 2, "delta_percentage_points": 1.1, "times_better": 4}
  ]
 }
}`

func TestRatioColumnNeverManufacturesARatio(t *testing.T) {
	readme := readmeOf(t, writeRun(t, headToHeadFixture))
	if !strings.Contains(readme, ratioExplanation) {
		t.Error("the recorded ratio_undefined explanation is not rendered")
	}
	if !strings.Contains(readme, "4.0×") {
		t.Error("the recorded times_better ratio is not rendered")
	}
	undefinedRow := ""
	for _, line := range strings.Split(readme, "\n") {
		if strings.Contains(line, "| eif |") {
			undefinedRow = line
		}
	}
	if undefinedRow == "" {
		t.Fatal("the head-to-head row for the eif baseline is missing")
	}
	if !strings.Contains(undefinedRow, "—") {
		t.Errorf("the undefined-ratio row carries no em-dash: %s", undefinedRow)
	}
	if strings.Contains(undefinedRow, "×") {
		t.Errorf("a ratio was printed for a row whose ratio is recorded as undefined: %s", undefinedRow)
	}
}

// ---------------------------------------------------------------------------
// 5. result.json is the input file, byte for byte.
// ---------------------------------------------------------------------------

func TestResultCopyIsByteIdentical(t *testing.T) {
	// Unusual whitespace and key order survive only if the copy is verbatim rather
	// than a decode-and-re-encode.
	fixture := "{\r\n  \"run\": {\"run_id\": \"copy-run\"},\r\n\t\"schema_version\":   \"1\"\r\n}\r\n"
	dir := writeRun(t, fixture)
	copied, err := os.ReadFile(filepath.Join(dir, "result.json"))
	if err != nil {
		t.Fatalf("read result.json: %v", err)
	}
	if !bytes.Equal(copied, []byte(fixture)) {
		t.Error("result.json is not byte-identical to the input result file")
	}
}

// ---------------------------------------------------------------------------
// 6. A missing run.run_id is a hard error naming the key.
// ---------------------------------------------------------------------------

func TestMissingRunIDIsAHardError(t *testing.T) {
	for name, fixture := range map[string]string{
		"empty run block": `{"schema_version": "1", "kind": "replay", "run": {}}`,
		"no run block":    `{"schema_version": "1", "kind": "replay"}`,
	} {
		t.Run(name, func(t *testing.T) {
			tmp := t.TempDir()
			resultPath := filepath.Join(tmp, "fixture.json")
			if err := os.WriteFile(resultPath, []byte(fixture), 0o600); err != nil {
				t.Fatalf("write fixture: %v", err)
			}
			outDir := filepath.Join(tmp, "runs")
			_, err := writeRunFolder(resultPath, outDir)
			if err == nil {
				t.Fatal("a result without run.run_id was accepted")
			}
			if !strings.Contains(err.Error(), "run.run_id") {
				t.Errorf("the error does not name run.run_id: %v", err)
			}
			if _, statErr := os.Stat(outDir); !os.IsNotExist(statErr) {
				t.Error("a folder was created for a run that did not record its identity")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 7. Rendering is deterministic: the same input yields identical bytes.
// ---------------------------------------------------------------------------

const richFixture = `{
 "schema_version": "1",
 "kind": "replay",
 "hypothesis": ["E1", "E3"],
 "run": {"run_id": "rich-run",
  "git_sha": "0123456789abcdef0123456789abcdef01234567",
  "git_dirty": true, "started_at": "2026-08-15T00:00:00Z",
  "finished_at": "2026-08-15T01:00:00Z", "go_version": "go1.26.3"},
 "corpus": {
  "files": [{"path": "data/auth.txt.gz",
   "sha256": "9c6b0cc261b0edd19324f6fd1839743224938a7f644ed202ca70bd70a89bf672"}],
  "rows_read": 1000, "events_warmed": 400, "events_scored": 600,
  "events_skipped": 0, "row_errors": 0,
  "burn_in": {"end_seconds": 604800},
  "coverage": {"statement": "corpus days 7.00 to 14.00 scored (burn-in days 0 to 7.00)"}
 },
 "parameters": {"alpha": 1, "half_life_days": 7, "bandwidth_hours": 1.5,
  "grid": 512, "top_k_per_day": 200, "zeta": 3, "beta": 2},
 "results": {
  "detection": [
   {"budget_per_day": 10, "alerts": 70, "true_positives": 0,
    "false_negatives": 549, "red_team_scored": 549,
    "recall_wilson": {"point": 0, "low": 0, "high": 0.00694857},
    "precision_wilson": {"point": 0, "low": 0, "high": 0.05202}}
  ],
  "anomaly_categories": {
   "counts": {
    "novel_value": {"scored_events": 75077, "red_team_events": 215},
    "off_hours": {"scored_events": 16210028, "red_team_events": 181},
    "volume_burst": {"scored_events": 14955042, "red_team_events": 199}
   },
   "definition": "structural properties of an event relative to the history it was scored against"
  }
 },
 "runtime": {"wall_seconds": 14920.5, "events_per_sec": 5324.04, "gc_percent": 400,
  "heap_alloc_mb": 1420.2}
}`

func TestRenderingIsDeterministic(t *testing.T) {
	first := writeRun(t, richFixture)
	second := writeRun(t, richFixture)
	for _, name := range []string{"README.md", "summary.json"} {
		a, err := os.ReadFile(filepath.Join(first, name))
		if err != nil {
			t.Fatalf("read first %s: %v", name, err)
		}
		b, err := os.ReadFile(filepath.Join(second, name))
		if err != nil {
			t.Fatalf("read second %s: %v", name, err)
		}
		if !bytes.Equal(a, b) {
			t.Errorf("%s differs between two renders of the same input", name)
		}
	}

	readme := readmeOf(t, first)
	if !strings.Contains(readme, "9c6b0cc261b0…") {
		t.Error("the corpus digest is not truncated to twelve characters")
	}
	if strings.Contains(readme, "9c6b0cc261b0edd19324") {
		t.Error("the corpus digest is rendered untruncated")
	}
	if !strings.Contains(readme, "0.0% [0.0, 0.7]") {
		t.Error("the recorded recall interval is not rendered as a percentage interval")
	}
	if !strings.Contains(readme, "DIRTY") {
		t.Error("the recorded dirty tree is not rendered as a visible warning")
	}
	if !strings.Contains(readme, "16,210,028") {
		t.Error("the recorded category count is not rendered")
	}
}
