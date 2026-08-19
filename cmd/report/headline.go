package main

// The headline banner and the per-category comparison table, both read from an
// analysis result (cmd/analyse output). The banner sentence was composed by
// cmd/analyse beside the numbers it summarises and is rendered verbatim: rewording or
// re-rounding it here would put a claim on the page that no run produced. The rule
// the other tables follow applies throughout — a row whose comparison the data cannot
// support renders the recorded reason, an undefined ratio renders an em dash with the
// recorded explanation beneath the table, and an absent block renders nothing at all,
// never a half-populated banner and never a zero standing in for a value nobody
// measured.

import "fmt"

// headlineBanner is the document's opening claim: the ready-made sentence an analysis
// result carries under results.headline, with the trace a reader needs to check it —
// the baseline run the sentence compares against and the file the sentence came from.
type headlineBanner struct {
	Sentence      string
	BaselineRunID string
	File          string
}

// buildHeadline returns the banner when an analysis result carries a non-empty
// results.headline, and nil when none does: the banner is whole or absent, never
// half-filled.
func buildHeadline(results []resultFile) *headlineBanner {
	for _, r := range allOfKind(results, "analysis") {
		sentence := getString(resultsBlock(r), "headline")
		if sentence == "" {
			continue
		}
		return &headlineBanner{
			Sentence:      sentence,
			BaselineRunID: getString(getMap(resultsBlock(r), "baseline_source"), "run_id"),
			File:          r.Path,
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Per-category comparison (cmd/analyse output)
// ---------------------------------------------------------------------------

// categoryComparisonCard is the per-category table at the largest recorded budget:
// the rows, the recorded explanations for undefined ratios, the taxonomy the
// categories argue, and the note stating what the recall denominator is.
type categoryComparisonCard struct {
	Caption  string
	Note     string
	Rows     []categoryComparisonRow
	Caveats  []string
	Taxonomy []taxonomyEntry
	Prov     []string
}

// categoryComparisonRow is one category against one baseline. When Unmeasurable is
// non-empty the row carries no comparison and the template renders the recorded
// sentence across the numeric columns instead of numbers.
type categoryComparisonRow struct {
	Category, Baseline string
	Unmeasurable       string
	Events             string
	Framework          string
	FrameworkRecall    string
	BaselineDetected   string
	BaselineRecall     string
	Delta, DeltaClass  string
	Ratio              string
	CommonDays         string
}

// taxonomyEntry is one distinct category's definition. The text is read from the
// result file, not written here: the argument printed beside the numbers is the same
// string the classifier was documented with, and the two cannot drift apart.
type taxonomyEntry struct {
	ID, WhitepaperSection, StructuralTest, Contrast string
}

// buildCategoryComparison returns the card when an analysis result carries a
// non-empty results.category_comparison, and nil when none does.
func buildCategoryComparison(results []resultFile) *categoryComparisonCard {
	for _, r := range allOfKind(results, "analysis") {
		rows, _ := resultsBlock(r)["category_comparison"].([]any)
		if len(rows) == 0 {
			continue
		}
		if card := categoryComparisonCardOf(r, rows); card != nil {
			return card
		}
	}
	return nil
}

// categoryComparisonCardOf builds the card at the largest budget_per_day any row
// records, which the caption states. A row recording no budget cannot be placed at
// any budget and is skipped rather than guessed at.
func categoryComparisonCardOf(r resultFile, raw []any) *categoryComparisonCard {
	budget, ok := largestCategoryBudget(raw)
	if !ok {
		return nil
	}
	card := &categoryComparisonCard{
		Caption: fmt.Sprintf("shown at the largest recorded budget: %s alerts per analyst-day",
			intCell(budget)),
		Note:     getString(getMap(resultsBlock(r), "red_team_population"), "source"),
		Taxonomy: taxonomyEntries(raw),
		Prov:     []string{provLine(r)},
	}
	for _, v := range raw {
		m, okRow := v.(map[string]any)
		if !okRow {
			continue
		}
		b, okBudget := getFloat(m, "budget_per_day")
		if !okBudget || b != budget {
			continue
		}
		row, why := comparisonRow(m)
		card.Rows = append(card.Rows, row)
		card.Caveats = appendCaveat(card.Caveats, why)
	}
	if len(card.Rows) == 0 {
		return nil
	}
	return card
}

// largestCategoryBudget returns the largest budget_per_day any comparison row records.
func largestCategoryBudget(raw []any) (float64, bool) {
	best, found := 0.0, false
	for _, v := range raw {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if b, okBudget := getFloat(m, "budget_per_day"); okBudget && (!found || b > best) {
			best, found = b, true
		}
	}
	return best, found
}

// comparisonRow renders one row's cells: every cell is a recorded value or the em
// dash. The returned explanation is the recorded ratio_undefined reason, when the row
// carries one, for the caveat list beneath the table.
func comparisonRow(m map[string]any) (categoryComparisonRow, string) {
	row := categoryComparisonRow{
		Category: getString(getMap(m, "category"), "id"),
		Baseline: getString(m, "baseline"),
	}
	if u := getString(m, "unmeasurable"); u != "" {
		row.Unmeasurable = u
		return row, ""
	}
	row.Events = intOrAbsent(m, "red_team_events_in_category")
	row.Framework = intOrAbsent(m, "framework_detected")
	row.FrameworkRecall = intervalCell(getMap(m, "framework_recall"))
	row.BaselineDetected = intOrAbsent(m, "baseline_detected")
	row.BaselineRecall = intervalCell(getMap(m, "baseline_recall"))
	if d, ok := getFloat(m, "delta_percentage_points"); ok {
		row.Delta, row.DeltaClass = deltaCell(d)
	} else {
		row.Delta = absentCell
	}
	var why string
	row.Ratio, why = timesBetterCell(m)
	if days, ok := m["common_days"].([]any); ok {
		row.CommonDays = intCell(float64(len(days)))
	} else {
		row.CommonDays = absentCell
	}
	return row, why
}

// intOrAbsent renders a recorded count, or the em dash when the key is absent — never
// a zero standing in for a value nobody measured.
func intOrAbsent(m map[string]any, key string) string {
	if v, ok := getFloat(m, key); ok {
		return intCell(v)
	}
	return absentCell
}

// intervalCell renders a recorded {point, low, high} recall interval; the cell counts
// as present only when all three are.
func intervalCell(m map[string]any) string {
	point, okPoint := getFloat(m, "point")
	low, okLow := getFloat(m, "low")
	high, okHigh := getFloat(m, "high")
	if !okPoint || !okLow || !okHigh {
		return absentCell
	}
	return fixedCell(point, 3) + " [" + fixedCell(low, 3) + ", " + fixedCell(high, 3) + "]"
}

// deltaCell renders delta_percentage_points with an explicit sign and picks the tone:
// a positive delta favours the framework, a negative one the baseline, and a zero
// delta stays neutral.
func deltaCell(v float64) (text, class string) {
	switch {
	case v > 0:
		return fmt.Sprintf("%+.1f", v), "delta-good"
	case v < 0:
		return fmt.Sprintf("%+.1f", v), "delta-crit"
	default:
		return fixedCell(0, 1), ""
	}
}

// timesBetterCell renders times_better when the file carries it, and the em dash when
// the file records the ratio as undefined; the recorded explanation is returned for
// the caveat list. A ratio is never manufactured from the counts.
func timesBetterCell(m map[string]any) (cell, explanation string) {
	if v, ok := getFloat(m, "times_better"); ok {
		return fixedCell(v, 1) + "×", ""
	}
	return absentCell, getString(m, "ratio_undefined")
}

// taxonomyEntries deduplicates the categories by id, keeping first-seen order: a
// category compared against several baselines and budgets states its argument once.
func taxonomyEntries(raw []any) []taxonomyEntry {
	seen := map[string]bool{}
	out := []taxonomyEntry{}
	for _, v := range raw {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		cat := getMap(m, "category")
		id := getString(cat, "id")
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, taxonomyEntry{
			ID:                id,
			WhitepaperSection: textOrAbsent(cat, "whitepaper_section"),
			StructuralTest:    textOrAbsent(cat, "structural_test"),
			Contrast:          textOrAbsent(cat, "contrast_with_marginal_detectors"),
		})
	}
	return out
}

// textOrAbsent reads recorded prose, rendering the em dash when the file carries
// none: these sentences are never written by the renderer itself.
func textOrAbsent(m map[string]any, key string) string {
	if s := getString(m, key); s != "" {
		return s
	}
	return absentCell
}

// appendCaveat appends a non-empty explanation not already present, preserving
// first-seen order, so a reason shared by several rows renders once.
func appendCaveat(dst []string, c string) []string {
	if c == "" {
		return dst
	}
	for _, have := range dst {
		if have == c {
			return dst
		}
	}
	return append(dst, c)
}
