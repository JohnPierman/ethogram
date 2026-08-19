package main

import (
	"strings"
	"testing"
)

// The per-category numbers are evidence for an argument, and the argument has to reach
// the page or the tables are a list of figures with nothing to say. These tests fix
// that the taxonomy is rendered, that it is deduplicated across budgets and baselines,
// and that its text is read from the result rather than written into the renderer.

const taxonomyFixture = `{
 "schema_version": "1",
 "kind": "analysis",
 "hypothesis": ["E1", "E2"],
 "run": {"run_id": "taxonomy-fixture-001", "git_sha": "abcdef1234567890", "git_dirty": false},
 "results": {
  "headline": "The framework led at the matched budget.",
  "head_to_head": [
   {
    "budget_per_day": 100,
    "framework_detected": 10,
    "framework_recall": {"point": 0.5, "low": 0.3, "high": 0.7, "n": 20, "level": 0.95, "kind": "wilson"},
    "baseline": "iforest",
    "baseline_detected": 0,
    "baseline_recall": {"point": 0, "low": 0, "high": 0.16, "n": 20, "level": 0.95, "kind": "wilson"},
    "red_team_events": 20,
    "delta_percentage_points": 50,
    "ratio_undefined": "the baseline detected nothing at this budget",
    "mcnemar": {"statistic": 10, "p_value": 0.002, "exact": true, "only_a": 10, "only_b": 0},
    "bootstrap_delta": {"observed_delta": 10, "low": 5, "high": 15},
    "common_days": [7],
    "caveats": ["the encoding was not tuned in the baselines' favour"]
   }
  ],
  "category_comparison": [
   {
    "category": {
     "id": "novel_value",
     "structural_test": "the entity has history for the field but has never taken this value",
     "whitepaper_section": "S3.1, S6",
     "contrast_with_marginal_detectors": "a detector fitted to a pooled feature cloud holds no per-entity state"
    },
    "budget_per_day": 100,
    "red_team_events_in_category": 20,
    "framework_detected": 10,
    "framework_recall": {"point": 0.5, "low": 0.3, "high": 0.7, "n": 20, "level": 0.95, "kind": "wilson"},
    "baseline": "iforest",
    "baseline_detected": 0,
    "baseline_recall": {"point": 0, "low": 0, "high": 0.16, "n": 20, "level": 0.95, "kind": "wilson"},
    "delta_percentage_points": 50,
    "ratio_undefined": "the baseline detected nothing in this category",
    "common_days": [7]
   },
   {
    "category": {
     "id": "novel_value",
     "structural_test": "the entity has history for the field but has never taken this value",
     "whitepaper_section": "S3.1, S6",
     "contrast_with_marginal_detectors": "a detector fitted to a pooled feature cloud holds no per-entity state"
    },
    "budget_per_day": 100,
    "red_team_events_in_category": 20,
    "framework_detected": 8,
    "framework_recall": {"point": 0.4, "low": 0.22, "high": 0.61, "n": 20, "level": 0.95, "kind": "wilson"},
    "baseline": "rrcf",
    "baseline_detected": 2,
    "baseline_recall": {"point": 0.1, "low": 0.03, "high": 0.3, "n": 20, "level": 0.95, "kind": "wilson"},
    "delta_percentage_points": 30,
    "times_better": 4.0,
    "common_days": [7]
   },
   {
    "category": {
     "id": "population_rare",
     "structural_test": "the value holds less than one part in a thousand of its field's mass",
     "whitepaper_section": "S9",
     "contrast_with_marginal_detectors": "this is the category isolation-based detectors answer well"
    },
    "budget_per_day": 100,
    "red_team_events_in_category": 6,
    "framework_detected": 3,
    "framework_recall": {"point": 0.5, "low": 0.19, "high": 0.81, "n": 6, "level": 0.95, "kind": "wilson"},
    "baseline": "iforest",
    "baseline_detected": 3,
    "baseline_recall": {"point": 0.5, "low": 0.19, "high": 0.81, "n": 6, "level": 0.95, "kind": "wilson"},
    "delta_percentage_points": 0,
    "times_better": 1.0,
    "common_days": [7]
   }
  ]
 },
 "provenance_complete": true
}`

func TestTaxonomyReachesThePage(t *testing.T) {
	r := mustParse(t, "analysis-taxonomy.json", taxonomyFixture)
	s := sectionE1E2([]result{r})

	if s.Pending != "" {
		t.Fatalf("section is pending: %s", s.Pending)
	}
	if s.Table3 == nil {
		t.Fatal("no taxonomy table was produced")
	}

	// One row per category, not one per comparison: novel_value appears twice in the
	// comparison, once against each baseline, and must be stated once.
	if len(s.Table3.Rows) != 2 {
		t.Fatalf("taxonomy has %d rows, want 2 (novel_value once, population_rare once)",
			len(s.Table3.Rows))
	}

	joined := strings.Join([]string{
		strings.Join(s.Table3.Rows[0], " | "),
		strings.Join(s.Table3.Rows[1], " | "),
	}, " || ")

	for _, want := range []string{
		"novel_value",
		"population_rare",
		"holds no per-entity state",
		"isolation-based detectors answer well",
		"never taken this value",
		"S3.1, S6",
		"S9",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("taxonomy omits %q\ngot: %s", want, joined)
		}
	}
}

func TestTheDocumentDeclaresItsOwnEncoding(t *testing.T) {
	// The prose carries section signs, en dashes and Greek letters, and the file is
	// written as UTF-8 and meant to be opened from disk. A browser given no encoding
	// falls back to a locale default, which on a Windows host turns every "§" into
	// "Â§" — so the declaration is part of the document being correct, not decoration.
	if !strings.Contains(paperHTML, `<meta charset="utf-8">`) {
		t.Error("the template declares no character encoding")
	}
	for _, want := range []string{"<!doctype html>", "<html lang=\"en\">", "<head>", "<body>", "</body>", "</html>"} {
		if !strings.Contains(paperHTML, want) {
			t.Errorf("the template is not a complete document: missing %q", want)
		}
	}
	// And the section sign must survive into the template as one rune, not as the two
	// that a double-encoding would produce.
	if strings.Contains(paperHTML, "Â§") {
		t.Error("the template contains a double-encoded section sign")
	}
}

func TestTaxonomyTextIsNotWrittenIntoTheRenderer(t *testing.T) {
	// The contrast a reader sees must be the string the classifier was declared with.
	// Substituting the renderer's own wording would let the argument on the page drift
	// away from the code that produced the numbers beside it.
	r := mustParse(t, "analysis-taxonomy.json",
		strings.ReplaceAll(taxonomyFixture,
			"a detector fitted to a pooled feature cloud holds no per-entity state",
			"SENTINEL-CONTRAST-TEXT"))
	s := sectionE1E2([]result{r})
	if s.Table3 == nil {
		t.Fatal("no taxonomy table")
	}
	var found bool
	for _, row := range s.Table3.Rows {
		for _, cell := range row {
			if cell == "SENTINEL-CONTRAST-TEXT" {
				found = true
			}
		}
	}
	if !found {
		t.Error("the renderer did not carry the result's own contrast text through")
	}
}

func TestTaxonomyPendingWhenTheCategoryBlockIsIncomplete(t *testing.T) {
	// A category naming no contrast leaves the argument unstated, and an unstated
	// argument must not be papered over with a blank cell.
	r := mustParse(t, "analysis-taxonomy.json",
		strings.ReplaceAll(taxonomyFixture,
			`"contrast_with_marginal_detectors": "this is the category isolation-based detectors answer well"`,
			`"unused_key": "x"`))
	s := sectionE1E2([]result{r})
	if s.Pending == "" {
		t.Fatal("an incomplete category block rendered as measured")
	}
	if !strings.Contains(s.Pending, "contrast_with_marginal_detectors") {
		t.Errorf("the pending reason does not name the missing key: %s", s.Pending)
	}
}
