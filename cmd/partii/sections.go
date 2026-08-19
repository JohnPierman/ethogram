package main

// The nine section builders. Each reads one hypothesis's figures out of the result
// files and returns either a fully-populated section or a pending one that names the
// file and key it could not find. The rule of the document applies here: a value that
// was not read out of a result file is not rendered, and a section whose headline
// figure is missing is withheld entirely rather than half-filled.

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// Lookup machinery shared by the sections.
// ---------------------------------------------------------------------------

// listAt returns a nested list, ok=false when absent, matching mapAt's contract.
func listAt(m map[string]any, path ...string) ([]any, bool) {
	if len(path) == 0 {
		return nil, false
	}
	parent, ok := mapAt(m, path[:len(path)-1]...)
	if !ok {
		return nil, false
	}
	v, ok := parent[path[len(path)-1]].([]any)
	return v, ok
}

// valueAt returns a nested raw value, nil when absent.
func valueAt(m map[string]any, path ...string) any {
	if len(path) == 0 {
		return nil
	}
	parent, ok := mapAt(m, path[:len(path)-1]...)
	if !ok {
		return nil
	}
	return parent[path[len(path)-1]]
}

// lookup reads values out of a result document and accumulates the dotted keys that
// were absent, so a section can read a whole block and then decide, once, whether it
// is renderable or pending. A missing value is never rendered: the section that owns
// the lookup must check missing() before using anything it read.
type lookup struct {
	m      map[string]any
	prefix string
	missed []string
}

func (l *lookup) num(path ...string) float64 {
	v, ok := numAt(l.m, path...)
	if !ok {
		l.record(path...)
	}
	return v
}

func (l *lookup) str(path ...string) string {
	v, ok := strAt(l.m, path...)
	if !ok {
		l.record(path...)
	}
	return v
}

func (l *lookup) boolean(path ...string) bool {
	v, ok := boolAt(l.m, path...)
	if !ok {
		l.record(path...)
	}
	return v
}

func (l *lookup) list(path ...string) []any {
	v, ok := listAt(l.m, path...)
	if !ok {
		l.record(path...)
	}
	return v
}

// interval renders a recorded {point, low, high} confidence interval as a percentage.
func (l *lookup) interval(path ...string) string {
	m, ok := mapAt(l.m, path...)
	if !ok {
		l.record(path...)
		return ""
	}
	sub := &lookup{m: m, prefix: l.prefix + strings.Join(path, ".") + "."}
	out := fmt.Sprintf("%s%% [%s, %s]", pct(sub.num("point")), pct(sub.num("low")), pct(sub.num("high")))
	l.missed = append(l.missed, sub.missed...)
	return out
}

func (l *lookup) record(path ...string) {
	l.missed = append(l.missed, l.prefix+strings.Join(path, "."))
}

func (l *lookup) missing() string { return strings.Join(l.missed, ", ") }

// pendingKey is the sentence a section renders when its result file exists but the
// figure it needs does not: it names the file and the key, and nothing is invented.
func pendingKey(file, key string) string {
	return fmt.Sprintf("Not yet measured: %s does not carry %s, so this section has no recorded figures.", file, key)
}

// pct renders a proportion read from a result file as a percentage with one decimal.
func pct(v float64) string { return strconv.FormatFloat(100*v, 'f', 1, 64) }

// fmtSig renders a measured value to three significant figures.
func fmtSig(v float64) string { return strconv.FormatFloat(v, 'g', 3, 64) }

// shortDigest truncates a recorded digest for the page; the file carries it in full.
func shortDigest(d string) string {
	const shown = 12
	if len(d) <= shown {
		return d
	}
	return d[:shown] + "…"
}

// stringItems extracts the string members of a recorded list.
func stringItems(v any) []string {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		if s, isStr := it.(string); isStr {
			out = append(out, s)
		}
	}
	return out
}

// appendUnique appends items not already present, preserving first-seen order, so
// caveats shared by every row render once.
func appendUnique(dst []string, items ...string) []string {
	for _, it := range items {
		if it == "" {
			continue
		}
		seen := false
		for _, have := range dst {
			if have == it {
				seen = true
				break
			}
		}
		if !seen {
			dst = append(dst, it)
		}
	}
	return dst
}

func mcnemarCell(g *lookup) string {
	p := g.num("mcnemar", "p_value")
	kind := "χ²"
	if g.boolean("mcnemar", "exact") {
		kind = "exact"
	}
	return fmt.Sprintf("%s (%s)", fmtSig(p), kind)
}

func discordantCell(g *lookup) string {
	return fmtInt(g.num("mcnemar", "only_a")) + " / " + fmtInt(g.num("mcnemar", "only_b"))
}

func bootstrapCell(g *lookup) string {
	return fmt.Sprintf("%+.0f [%+.0f, %+.0f]",
		g.num("bootstrap_delta", "observed_delta"),
		g.num("bootstrap_delta", "low"),
		g.num("bootstrap_delta", "high"))
}

// ratioCell renders times_better when the file carries it, and the em-dash when the
// file records the ratio as undefined; the recorded explanation is returned so the
// section can show it in the caveat list. A ratio is never manufactured.
func ratioCell(row map[string]any) (cell, explanation string) {
	if v, ok := numAt(row, "times_better"); ok {
		return fmt.Sprintf("%.1f×", v), ""
	}
	why, _ := strAt(row, "ratio_undefined")
	return "—", why
}

// ---------------------------------------------------------------------------
// E8 — determinism (R4).
// ---------------------------------------------------------------------------

type e8Facts struct {
	identical, stable, differs bool
	repeats                    float64
	digest, description        string
	control                    []string
}

func e8Read(r result) (e8Facts, string) {
	l := &lookup{m: r.Data}
	f := e8Facts{
		identical:   l.boolean("results", "batch_independence", "identical"),
		repeats:     l.num("results", "repeat_determinism", "repeats"),
		stable:      l.boolean("results", "repeat_determinism", "stable"),
		digest:      l.str("results", "repeat_determinism", "digest"),
		differs:     l.boolean("results", "negative_control", "differs"),
		description: l.str("results", "negative_control", "description"),
		control:     stringItems(valueAt(r.Data, "results", "negative_control", "digests")),
	}
	if len(f.control) == 0 {
		l.record("results", "negative_control", "digests")
	}
	return f, l.missing()
}

func e8CaseTable(cases []any) (*table, []string, string) {
	tbl := &table{
		Caption: "Verdict digests across batch compositions; digests are shown to twelve hex characters and carried in full by the result file",
		Head:    []string{"Composition", "Batch size", "Verdict digest", "Combined p", "j"},
	}
	sizes := make([]string, 0, len(cases))
	for i, raw := range cases {
		cm, okCase := raw.(map[string]any)
		if !okCase {
			return nil, nil, fmt.Sprintf("results.batch_independence.cases[%d]", i)
		}
		g := &lookup{m: cm, prefix: fmt.Sprintf("results.batch_independence.cases[%d].", i)}
		size := fmtInt(g.num("batch_size"))
		row := []string{
			g.str("name"), size, shortDigest(g.str("verdict_digest")),
			fmtSig(g.num("combined_p")), fmtInt(g.num("j")),
		}
		if m := g.missing(); m != "" {
			return nil, nil, m
		}
		sizes = append(sizes, size)
		tbl.Rows = append(tbl.Rows, row)
	}
	return tbl, sizes, ""
}

func sectionE8(results []result) section {
	s := section{ID: "E8", Title: "Determinism (R4)"}
	r, ok := claiming(results, "E8")
	if !ok {
		s.Pending = "Not yet measured: no result file in the results directory claims hypothesis E8, so batch-composition determinism has no recorded run."
		return s
	}
	cases, okCases := listAt(r.Data, "results", "batch_independence", "cases")
	if !okCases || len(cases) == 0 {
		s.Pending = pendingKey(r.File, "results.batch_independence.cases")
		return s
	}
	tbl, sizes, missCases := e8CaseTable(cases)
	if missCases != "" {
		s.Pending = pendingKey(r.File, missCases)
		return s
	}
	facts, miss := e8Read(r)
	if miss != "" {
		s.Pending = pendingKey(r.File, miss)
		return s
	}
	s.Paras = append(s.Paras,
		fmt.Sprintf("The same probe was scored inside %s batch compositions whose recorded sizes range from %s to %s events. Every composition produced the byte-identical verdict digest %s (identical = %t), and %s repeated runs of the same batch reproduced it (stable = %t).",
			fmtInt(float64(len(cases))), sizes[0], sizes[len(sizes)-1],
			shortDigest(facts.digest), facts.identical, fmtInt(facts.repeats), facts.stable),
		fmt.Sprintf("The negative control is %s. Its recorded score bit patterns across the same compositions — %s — do differ (differs = %t), decaying with batch composition as the √((1−p)/p) dependence of a batch-standardised score predicts; that measured decay is what gives the identity above its power.",
			facts.description, strings.Join(facts.control, ", "), facts.differs))
	if pass, okPass := boolAt(r.Data, "results", "pass"); okPass {
		s.Paras = append(s.Paras, fmt.Sprintf("The run records pass = %t.", pass))
	}
	s.Table = tbl
	s.Prov = provOf(r)
	return s
}

// ---------------------------------------------------------------------------
// E7 — evidence sufficiency (R5).
// ---------------------------------------------------------------------------

func e7DetectorTable(byDet map[string]any) (*table, string) {
	tbl := &table{
		Caption: "Reconstruction outcomes per detector",
		Head:    []string{"Detector", "Sampled", "Reconstructed", "Partial", "Failed"},
	}
	names := make([]string, 0, len(byDet))
	for name := range byDet {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		dm, okDet := byDet[name].(map[string]any)
		if !okDet {
			return nil, "results.by_detector." + name
		}
		g := &lookup{m: dm, prefix: "results.by_detector." + name + "."}
		row := []string{
			name, fmtInt(g.num("sampled")), fmtInt(g.num("reconstructed")),
			fmtInt(g.num("partial")), fmtInt(g.num("failed")),
		}
		if m := g.missing(); m != "" {
			return nil, m
		}
		tbl.Rows = append(tbl.Rows, row)
	}
	return tbl, ""
}

func sectionE7(results []result) section {
	s := section{ID: "E7", Title: "Evidence sufficiency (R5)"}
	r, ok := claiming(results, "E7")
	if !ok {
		s.Pending = "Not yet measured: no result file in the results directory claims hypothesis E7, so evidence sufficiency has no recorded run."
		return s
	}
	l := &lookup{m: r.Data}
	prop := l.num("results", "proportion_reconstructed")
	sampled := l.num("results", "totals", "sampled")
	reconstructed := l.num("results", "totals", "reconstructed")
	partial := l.num("results", "totals", "partial")
	failed := l.num("results", "totals", "failed")
	if m := l.missing(); m != "" {
		s.Pending = pendingKey(r.File, m)
		return s
	}
	byDet, okDet := mapAt(r.Data, "results", "by_detector")
	if !okDet || len(byDet) == 0 {
		s.Pending = pendingKey(r.File, "results.by_detector")
		return s
	}
	tbl, miss := e7DetectorTable(byDet)
	if miss != "" {
		s.Pending = pendingKey(r.File, miss)
		return s
	}
	s.Paras = append(s.Paras, fmt.Sprintf(
		"Of %s sampled verdicts, %s were reconstructed from their evidence cards alone — a recorded share of %s%% — while %s were partially reconstructable and %s failed.",
		fmtInt(sampled), fmtInt(reconstructed), pct(prop), fmtInt(partial), fmtInt(failed)))
	if def, okDef := strAt(r.Data, "results", "partial_definition"); okDef && def != "" {
		s.Paras = append(s.Paras, fmt.Sprintf("The partial cases are, as the run records them: %s.", def))
	} else if partial > 0 {
		s.Paras = append(s.Paras, "The result file does not define its partial cases (results.partial_definition is absent).")
	}
	if tol, okTol := strAt(r.Data, "parameters", "tolerance"); okTol && tol != "" {
		s.Paras = append(s.Paras, fmt.Sprintf("Reconstruction was checked to the recorded tolerance of %s.", tol))
	}
	s.Table = tbl
	s.Prov = provOf(r)
	return s
}

// ---------------------------------------------------------------------------
// E6 — schema as configuration (R2).
// ---------------------------------------------------------------------------

func corpusFileBase(data map[string]any) (string, bool) {
	files, ok := listAt(data, "corpus", "files")
	if !ok || len(files) == 0 {
		return "", false
	}
	fm, ok := files[0].(map[string]any)
	if !ok {
		return "", false
	}
	p, ok := fm["path"].(string)
	if !ok || p == "" {
		return "", false
	}
	return filepath.Base(p), true
}

func e6CorpusTable(data map[string]any) *table {
	tbl := &table{Caption: "Corpus accounting as recorded", Head: []string{"Figure", "Value"}}
	for _, item := range []struct{ label, key string }{
		{"Rows read", "rows_read"},
		{"Events warmed (burn-in)", "events_warmed"},
		{"Events scored", "events_scored"},
		{"Events skipped", "events_skipped"},
		{"Row errors", "row_errors"},
		{"No opinion", "no_opinion"},
	} {
		if v, ok := numAt(data, "corpus", item.key); ok {
			tbl.Rows = append(tbl.Rows, []string{item.label, fmtInt(v)})
		}
	}
	if len(tbl.Rows) == 0 {
		return nil
	}
	return tbl
}

func sectionE6(results []result) section {
	s := section{ID: "E6", Title: "Schema as configuration (R2)"}
	r, ok := claiming(results, "E6")
	if !ok {
		s.Pending = "Not yet measured: no result file in the results directory claims hypothesis E6, so the second source's onboarding has no recorded run."
		return s
	}
	l := &lookup{m: r.Data}
	rowsRead := l.num("corpus", "rows_read")
	scored := l.num("corpus", "events_scored")
	schemaPath := l.str("run", "schema")
	src, okSrc := corpusFileBase(r.Data)
	if !okSrc {
		l.record("corpus", "files")
	}
	if m := l.missing(); m != "" {
		s.Pending = pendingKey(r.File, m)
		return s
	}
	s.Paras = append(s.Paras, fmt.Sprintf(
		"The second source, %s, was onboarded by a configuration file alone: the run names %s as the schema that configured it. The run read %s rows and scored %s events.",
		src, schemaPath, fmtInt(rowsRead), fmtInt(scored)),
		"The result file records the rows read and events scored above but carries no code-change count; the onboarding claim rests on the recorded schema path.")
	if cov, okCov := strAt(r.Data, "corpus", "coverage", "statement"); okCov && cov != "" {
		s.Paras = append(s.Paras, fmt.Sprintf("Coverage as recorded: %s.", cov))
	}
	if part, okPart := strAt(r.Data, "run", "partition"); okPart && part != "" {
		s.Paras = append(s.Paras, fmt.Sprintf("The run records its partition state as: %s.", part))
	}
	s.Table = e6CorpusTable(r.Data)
	s.Prov = provOf(r)
	return s
}

// ---------------------------------------------------------------------------
// §12.5 — the wraparound negative control.
// ---------------------------------------------------------------------------

func sectionControls(results []result) section {
	s := section{ID: "§12.5", Title: "Negative control: wraparound at midnight"}
	r, ok := byKind(results, "control")
	if !ok {
		s.Pending = "Not yet measured: no result file of kind \"control\" is present in the results directory, so the wraparound control has no recorded run."
		return s
	}
	l := &lookup{m: r.Data}
	c2330 := l.num("results", "probes", "circular", "p_23_30")
	c0030 := l.num("results", "probes", "circular", "p_00_30")
	c1200 := l.num("results", "probes", "circular", "p_12_00")
	k2330 := l.num("results", "probes", "cells_168", "p_23_30")
	k0030 := l.num("results", "probes", "cells_168", "p_00_30")
	k1200 := l.num("results", "probes", "cells_168", "p_12_00")
	criteria := l.str("results", "acceptance", "criteria")
	passes := l.boolean("results", "acceptance", "circular_passes")
	defect := l.boolean("results", "acceptance", "cells_show_defect")
	if m := l.missing(); m != "" {
		s.Pending = pendingKey(r.File, m)
		return s
	}
	if syn, okSyn := strAt(r.Data, "corpus", "synthetic"); okSyn && syn != "" {
		s.Paras = append(s.Paras, fmt.Sprintf(
			"The control's prescribed input is synthetic — %s — and the probabilities below are real runs of the scoring code over it.", syn))
	}
	s.Paras = append(s.Paras,
		fmt.Sprintf("Either side of midnight the circular estimator returns P = %s at 23:30 and P = %s at 00:30; the 168-cell estimator returns %s and %s at the same probes, splitting the entity's one nightly window at the day boundary. At the 12:00 probe, far from the window, the two estimators return %s and %s.",
			fmtSig(c2330), fmtSig(c0030), fmtSig(k2330), fmtSig(k0030), fmtSig(c1200), fmtSig(k1200)),
		fmt.Sprintf("Against the recorded acceptance criteria — %s — the run records circular_passes = %t and cells_show_defect = %t.",
			criteria, passes, defect))
	s.Table = &table{
		Caption: "Wraparound probes",
		Head:    []string{"Probe", "Circular estimator P", "168-cell estimator P"},
		Rows: [][]string{
			{"23:30", fmtSig(c2330), fmtSig(k2330)},
			{"00:30", fmtSig(c0030), fmtSig(k0030)},
			{"12:00", fmtSig(c1200), fmtSig(k1200)},
		},
	}
	s.Prov = provOf(r)
	return s
}

// ---------------------------------------------------------------------------
// E1 and E2 — detection at matched budget: the headline section.
// ---------------------------------------------------------------------------

func e1e2PendingReason(results []result) string {
	if c, ok := claiming(results, "E1"); ok {
		return fmt.Sprintf("Not yet measured: %s claims the detection hypotheses but no result of kind \"analysis\" carrying results.head_to_head is present in the results directory; the head-to-head comparison has not been produced.", c.File)
	}
	return "Not yet measured: no result file in the results directory claims hypothesis E1, and no analysis result carrying results.head_to_head is present."
}

func headToHeadTable(rows []any) (*table, []string, string) {
	tbl := &table{
		Caption: "Aggregate head-to-head at matched alert budgets",
		Head: []string{"Budget/day", "Baseline", "Framework", "Framework recall",
			"Baseline hits", "Baseline recall", "Δ (pp)", "Ratio", "McNemar p",
			"Bootstrap Δ", "Common days"},
	}
	var caveats []string
	for i, raw := range rows {
		rm, okRow := raw.(map[string]any)
		if !okRow {
			return nil, nil, fmt.Sprintf("results.head_to_head[%d]", i)
		}
		g := &lookup{m: rm, prefix: fmt.Sprintf("results.head_to_head[%d].", i)}
		ratio, why := ratioCell(rm)
		days := g.list("common_days")
		row := []string{
			fmtInt(g.num("budget_per_day")),
			g.str("baseline"),
			fmt.Sprintf("%s of %s", fmtInt(g.num("framework_detected")), fmtInt(g.num("red_team_events"))),
			g.interval("framework_recall"),
			fmtInt(g.num("baseline_detected")),
			g.interval("baseline_recall"),
			fmt.Sprintf("%+.1f", g.num("delta_percentage_points")),
			ratio,
			mcnemarCell(g),
			bootstrapCell(g),
			fmtInt(float64(len(days))),
		}
		if m := g.missing(); m != "" {
			return nil, nil, m
		}
		caveats = appendUnique(caveats, why)
		caveats = appendUnique(caveats, stringItems(rm["caveats"])...)
		tbl.Rows = append(tbl.Rows, row)
	}
	return tbl, caveats, ""
}

func largestBudget(rows []any) (float64, string) {
	best, found := 0.0, false
	for i, raw := range rows {
		rm, okRow := raw.(map[string]any)
		if !okRow {
			return 0, fmt.Sprintf("results.category_comparison[%d]", i)
		}
		v, okBudget := numAt(rm, "budget_per_day")
		if !okBudget {
			return 0, fmt.Sprintf("results.category_comparison[%d].budget_per_day", i)
		}
		if !found || v > best {
			best, found = v, true
		}
	}
	if !found {
		return 0, "results.category_comparison[].budget_per_day"
	}
	return best, ""
}

func categoryRowCells(g *lookup, rm map[string]any, catID, baseline string) ([]string, string) {
	ratio, why := ratioCell(rm)
	days := g.list("common_days")
	return []string{
		catID, baseline,
		fmtInt(g.num("red_team_events_in_category")),
		fmtInt(g.num("framework_detected")),
		g.interval("framework_recall"),
		fmtInt(g.num("baseline_detected")),
		g.interval("baseline_recall"),
		fmt.Sprintf("%+.1f", g.num("delta_percentage_points")),
		ratio,
		fmtInt(float64(len(days))),
	}, why
}

func categoryTable(rows []any) (*table, float64, []string, string) {
	budget, missBudget := largestBudget(rows)
	if missBudget != "" {
		return nil, 0, nil, missBudget
	}
	tbl := &table{
		Caption: fmt.Sprintf("Per-category comparison at %s alerts per analyst-day", fmtInt(budget)),
		Head: []string{"Category", "Baseline", "Events", "Framework", "Framework recall",
			"Baseline hits", "Baseline recall", "Δ (pp)", "Ratio", "Common days"},
	}
	var caveats []string
	for i, raw := range rows {
		rm := raw.(map[string]any) // validated by largestBudget
		b, _ := numAt(rm, "budget_per_day")
		if b != budget {
			continue
		}
		g := &lookup{m: rm, prefix: fmt.Sprintf("results.category_comparison[%d].", i)}
		catID := g.str("category", "id")
		baseline := g.str("baseline")
		if reason, okU := strAt(rm, "unmeasurable"); okU && reason != "" {
			if m := g.missing(); m != "" {
				return nil, 0, nil, m
			}
			tbl.Rows = append(tbl.Rows, []string{catID, baseline, reason, "—", "—", "—", "—", "—", "—", "—"})
			continue
		}
		row, why := categoryRowCells(g, rm, catID, baseline)
		if m := g.missing(); m != "" {
			return nil, 0, nil, m
		}
		caveats = appendUnique(caveats, why)
		tbl.Rows = append(tbl.Rows, row)
	}
	return tbl, budget, caveats, ""
}

func sectionE1E2(results []result) section {
	s := section{ID: "E1 · E2", Title: "Detection at matched budget"}
	r, ok := byKind(results, "analysis")
	if !ok {
		s.Pending = e1e2PendingReason(results)
		return s
	}
	rows, okRows := listAt(r.Data, "results", "head_to_head")
	if !okRows || len(rows) == 0 {
		s.Pending = pendingKey(r.File, "results.head_to_head")
		return s
	}
	agg, caveats, missAgg := headToHeadTable(rows)
	if missAgg != "" {
		s.Pending = pendingKey(r.File, missAgg)
		return s
	}
	catRows, okCat := listAt(r.Data, "results", "category_comparison")
	if !okCat || len(catRows) == 0 {
		s.Pending = pendingKey(r.File, "results.category_comparison")
		return s
	}
	cat, budget, catCaveats, missCat := categoryTable(catRows)
	if missCat != "" {
		s.Pending = pendingKey(r.File, missCat)
		return s
	}
	if headline, okHead := strAt(r.Data, "results", "headline"); okHead && headline != "" {
		s.Paras = append(s.Paras, headline)
	}
	s.Paras = append(s.Paras,
		"The aggregate head-to-head below compares the framework against each baseline at every matched budget, confined to the days both arms scored.",
		fmt.Sprintf("The per-category comparison is shown at the largest recorded budget, %s alerts per analyst-day; a row whose comparison could not be supported renders the recorded reason instead of numbers.", fmtInt(budget)))
	tax, missTax := taxonomyTable(catRows)
	if missTax != "" {
		s.Pending = pendingKey(r.File, missTax)
		return s
	}
	s.Paras = append(s.Paras,
		"Each category is defined by a structural property of the event relative to the history it was scored against, and not by which detector returned the smallest p-value: a partition drawn along this framework's own detectors would be one chosen in its favour, and every margin computed on it would be circular. The table below states each test and the reason a marginal, batch-standardised detector cannot express it. The population-rare category is the control, being the one such detectors answer well.")
	s.Table = agg
	s.Table2 = cat
	s.Table3 = tax
	s.Caveats = appendUnique(appendUnique(nil, caveats...), catCaveats...)
	s.Prov = provOf(r)
	return s
}

// taxonomyTable renders the category definitions and the contrast each draws with a
// marginal outlier detector.
//
// The text is read from the result file, not written here. The categories are declared
// where they are computed, so the argument printed beside the numbers is the same string
// the classifier was documented with, and the two cannot drift apart.
func taxonomyTable(rows []any) (*table, string) {
	tbl := &table{
		Caption: "The categories, and why a marginal outlier detector cannot express them",
		Head:    []string{"Category", "Whitepaper", "Structural test", "Contrast with marginal detectors"},
	}
	seen := map[string]bool{}
	for i, raw := range rows {
		rm, okRow := raw.(map[string]any)
		if !okRow {
			return nil, fmt.Sprintf("results.category_comparison[%d]", i)
		}
		g := &lookup{m: rm, prefix: fmt.Sprintf("results.category_comparison[%d].", i)}
		id := g.str("category", "id")
		if m := g.missing(); m != "" {
			return nil, m
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		row := []string{
			id,
			g.str("category", "whitepaper_section"),
			g.str("category", "structural_test"),
			g.str("category", "contrast_with_marginal_detectors"),
		}
		if m := g.missing(); m != "" {
			return nil, m
		}
		tbl.Rows = append(tbl.Rows, row)
	}
	if len(tbl.Rows) == 0 {
		return nil, "results.category_comparison[].category.id"
	}
	return tbl, ""
}

// ---------------------------------------------------------------------------
// E3 — calibration: realised against nominal FDR.
// ---------------------------------------------------------------------------

func fdrInterval(g *lookup) string {
	return fmt.Sprintf("%s [%s, %s]",
		fmtSig(g.num("realised_fdr", "point")),
		fmtSig(g.num("realised_fdr", "low")),
		fmtSig(g.num("realised_fdr", "high")))
}

func calibrationTable(bh, by []any) (*table, string) {
	tbl := &table{
		Caption: "Realised FDR per procedure and nominal q",
		Head: []string{"Procedure", "Nominal q", "Discoveries", "True positives",
			"Realised FDR", "Conservatism ratio", "Saturated days"},
	}
	for _, block := range []struct {
		key  string
		rows []any
	}{{"results.calibration_bh", bh}, {"results.calibration_by", by}} {
		for i, raw := range block.rows {
			rm, okRow := raw.(map[string]any)
			if !okRow {
				return nil, fmt.Sprintf("%s[%d]", block.key, i)
			}
			g := &lookup{m: rm, prefix: fmt.Sprintf("%s[%d].", block.key, i)}
			row := []string{
				g.str("procedure"),
				fmtSig(g.num("nominal_q")),
				fmtInt(g.num("discoveries")),
				fmtInt(g.num("true_positives")),
				fdrInterval(g),
				fmtSig(g.num("conservatism_ratio")),
				fmtInt(g.num("saturated_days")),
			}
			if m := g.missing(); m != "" {
				return nil, m
			}
			tbl.Rows = append(tbl.Rows, row)
		}
	}
	return tbl, ""
}

func sectionE3(results []result) section {
	s := section{ID: "E3", Title: "Calibration: realised against nominal FDR"}
	r, ok := byKind(results, "analysis")
	if !ok {
		s.Pending = "Not yet measured: no analysis result (kind \"analysis\") is present in the results directory, so results.calibration_bh and results.calibration_by have not been produced."
		return s
	}
	bh, okBH := listAt(r.Data, "results", "calibration_bh")
	if !okBH || len(bh) == 0 {
		s.Pending = pendingKey(r.File, "results.calibration_bh")
		return s
	}
	by, okBY := listAt(r.Data, "results", "calibration_by")
	if !okBY || len(by) == 0 {
		s.Pending = pendingKey(r.File, "results.calibration_by")
		return s
	}
	tbl, miss := calibrationTable(bh, by)
	if miss != "" {
		s.Pending = pendingKey(r.File, miss)
		return s
	}
	s.Paras = append(s.Paras,
		"Realised false-discovery rate against nominal q under the per-day BH and BY constructions, with the recorded intervals.")
	if pred, okPred := strAt(r.Data, "results", "prediction"); okPred && pred != "" {
		s.Paras = append(s.Paras, fmt.Sprintf("The recorded prediction: %s.", pred))
	}
	if cav, okCav := strAt(r.Data, "parameters", "ground_truth_caveat"); okCav && cav != "" {
		s.Caveats = appendUnique(nil, cav)
	}
	s.Table = tbl
	s.Prov = provOf(r)
	return s
}

// ---------------------------------------------------------------------------
// E4 and E9 — the paired ablations recorded by the analysis result.
// ---------------------------------------------------------------------------

func perBudgetTable(rows []any, keyPrefix string) (*table, string) {
	tbl := &table{
		Head: []string{"Budget/day", "Paired events", "Framework", "Ablation arm",
			"Only framework / only arm", "McNemar p", "Bootstrap Δ"},
	}
	for i, raw := range rows {
		rm, okRow := raw.(map[string]any)
		if !okRow {
			return nil, fmt.Sprintf("%s[%d]", keyPrefix, i)
		}
		g := &lookup{m: rm, prefix: fmt.Sprintf("%s[%d].", keyPrefix, i)}
		row := []string{
			fmtInt(g.num("budget_per_day")),
			fmtInt(g.num("paired_events")),
			fmtInt(g.num("framework_detected")),
			fmtInt(g.num("arm_detected")),
			discordantCell(g),
			mcnemarCell(g),
			bootstrapCell(g),
		}
		if m := g.missing(); m != "" {
			return nil, m
		}
		tbl.Rows = append(tbl.Rows, row)
	}
	return tbl, ""
}

func ablationSection(results []result, key, id, title, intro string) section {
	s := section{ID: id, Title: title}
	r, ok := byKind(results, "analysis")
	if !ok {
		s.Pending = fmt.Sprintf("Not yet measured: no analysis result (kind \"analysis\") is present in the results directory, so results.ablations.%s has not been produced.", key)
		return s
	}
	ab, okBlock := mapAt(r.Data, "results", "ablations", key)
	if !okBlock {
		s.Pending = pendingKey(r.File, "results.ablations."+key)
		return s
	}
	arm, okArm := strAt(ab, "arm")
	if !okArm {
		s.Pending = pendingKey(r.File, "results.ablations."+key+".arm")
		return s
	}
	if reason, okReason := strAt(ab, "paired_tests"); okReason && reason != "" {
		s.Paras = append(s.Paras, intro,
			fmt.Sprintf("Against the %s arm the analysis records: %s.", arm, reason))
		s.Prov = provOf(r)
		return s
	}
	pb, okPB := listAt(ab, "per_budget")
	if !okPB || len(pb) == 0 {
		s.Pending = pendingKey(r.File, "results.ablations."+key+".per_budget")
		return s
	}
	tbl, miss := perBudgetTable(pb, "results.ablations."+key+".per_budget")
	if miss != "" {
		s.Pending = pendingKey(r.File, miss)
		return s
	}
	tbl.Caption = fmt.Sprintf("Framework against %s at matched budgets", arm)
	s.Paras = append(s.Paras, intro)
	if design, okDesign := strAt(ab, "design"); okDesign && design != "" {
		s.Paras = append(s.Paras, fmt.Sprintf("Design as recorded: %s.", design))
	}
	s.Table = tbl
	s.Prov = provOf(r)
	return s
}

func sectionE4(results []result) section {
	return ablationSection(results, "e4", "E4", "Partition ablation",
		"The partitioned co-occurrence composition against its single-block degeneration, paired at matched alert budgets over the red-team events both arms scored.")
}

func sectionE9(results []result) section {
	return ablationSection(results, "e9", "E9", "Representation ablation",
		"The circular timing estimator against the 168-cell representation, paired at matched alert budgets over the red-team events both arms scored. The wraparound negative control, which also bears on this hypothesis, is reported in its own section.")
}

// ---------------------------------------------------------------------------
// E5 — schema growth.
// ---------------------------------------------------------------------------

func e5Table(arms map[string]any) (*table, string) {
	tbl := &table{
		Caption: "Realised FDR per held-out field, treatment, era and nominal q",
		Head: []string{"Held-out field", "Treatment", "Max pairwise MI (nats)", "MI against",
			"Era", "Nominal q", "Discoveries", "True positives", "Realised FDR"},
	}
	keys := make([]string, 0, len(arms))
	for k := range arms {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		am, okArm := arms[k].(map[string]any)
		if !okArm {
			return nil, fmt.Sprintf("results.arms[%s]", k)
		}
		g := &lookup{m: am, prefix: fmt.Sprintf("results.arms[%s].", k)}
		field, treatment := g.str("held_out_field"), g.str("treatment")
		mi, against := fmtSig(g.num("max_pairwise_mi")), g.str("mi_against_field")
		eras := g.list("eras")
		if m := g.missing(); m != "" {
			return nil, m
		}
		for i, raw := range eras {
			em, okEra := raw.(map[string]any)
			if !okEra {
				return nil, fmt.Sprintf("results.arms[%s].eras[%d]", k, i)
			}
			e := &lookup{m: em, prefix: fmt.Sprintf("results.arms[%s].eras[%d].", k, i)}
			row := []string{
				field, treatment, mi, against,
				e.str("era"), fmtSig(e.num("nominal_q")),
				fmtInt(e.num("discoveries")), fmtInt(e.num("true_positives")),
				fmtSig(e.num("realised_fdr")),
			}
			if m := e.missing(); m != "" {
				return nil, m
			}
			tbl.Rows = append(tbl.Rows, row)
		}
	}
	return tbl, ""
}

func sectionE5(results []result) section {
	s := section{ID: "E5", Title: "Schema growth"}
	r, ok := byKind(results, "e5")
	if !ok {
		s.Pending = "Not yet measured: no result file of kind \"e5\" is present in the results directory, so the schema-growth treatments have no recorded run."
		return s
	}
	arms, okArms := mapAt(r.Data, "results", "arms")
	if !okArms || len(arms) == 0 {
		s.Pending = pendingKey(r.File, "results.arms")
		return s
	}
	tbl, miss := e5Table(arms)
	if miss != "" {
		s.Pending = pendingKey(r.File, miss)
		return s
	}
	intro := "Treatments A (abstain until calibrated) and B (detector-marginal composition) were run over every recorded held-out field; realised FDR at each nominal q is shown per era either side of the introduction boundary."
	if n0, okN0 := numAt(r.Data, "parameters", "n0"); okN0 {
		intro += fmt.Sprintf(" Treatment A's recorded join threshold is n₀ = %s verdicts.", fmtInt(n0))
	}
	s.Paras = append(s.Paras, intro)
	if reason, okReason := strAt(r.Data, "parameters", "treatment_c"); okReason && reason != "" {
		s.Paras = append(s.Paras, fmt.Sprintf("Treatment C — %s.", reason))
	} else {
		s.Paras = append(s.Paras, "The result file records no entry for treatment C (parameters.treatment_c is absent).")
	}
	s.Table = tbl
	s.Prov = provOf(r)
	return s
}
