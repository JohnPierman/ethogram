package main

// The README renderer. Sections appear in a fixed order; a section whose backing key
// is absent from the result file is either omitted entirely (the optional result
// blocks) or rendered as an explicit "not recorded" line naming the key (the
// provenance every run is expected to carry). Nothing in between: a value that was
// not read out of the file is not written.

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// renderReadme writes the run report from the result document alone. The output is a
// pure function of the document and the source file name, so rendering twice yields
// identical bytes.
func renderReadme(data map[string]any, sourceFile string) string {
	var b strings.Builder
	writeHeader(&b, data)
	writeProvenance(&b, data)
	writeCorpus(&b, data)
	writeSampling(&b, data)
	writeParameters(&b, data)
	writeDetection(&b, data)
	writeCategories(&b, data)
	writeHeadToHead(&b, data)
	writeCalibration(&b, data)
	writeRuntime(&b, data)
	writeFooter(&b, data, sourceFile)
	return b.String()
}

func writeHeader(b *strings.Builder, data map[string]any) {
	runID, _ := strAt(data, "run", "run_id") // presence enforced before rendering
	fmt.Fprintf(b, "# Run %s\n\n", runID)
	kind := notRecorded("kind")
	if k, ok := strAt(data, "kind"); ok && k != "" {
		kind = "`" + k + "`"
	}
	claims := "claims no hypothesis"
	if hs := stringItems(data["hypothesis"]); len(hs) > 0 {
		claims = "claims " + strings.Join(hs, ", ")
	}
	fmt.Fprintf(b, "A result of kind %s; it %s.\n", kind, claims)
}

// writeProvenance renders the fields a run is expected to record about itself. A
// field that exists becomes a table row; the absent ones are named beneath the table
// rather than silently dropped, because an unrecorded git sha is itself a finding.
func writeProvenance(b *strings.Builder, data map[string]any) {
	b.WriteString("\n## Provenance\n\n| Field | Value |\n| --- | --- |\n")
	var missing []string
	row := func(label, key, value string, ok bool) {
		if !ok {
			missing = append(missing, "`"+key+"`")
			return
		}
		fmt.Fprintf(b, "| %s | %s |\n", label, escapeCell(value))
	}
	runID, okID := strAt(data, "run", "run_id")
	row("Run id", "run.run_id", runID, okID)
	sha, okSHA := strAt(data, "run", "git_sha")
	row("Git sha", "run.git_sha", sha, okSHA)
	dirtyValue, okDirty := dirtyStatement(data)
	row("Git dirty", "run.git_dirty", dirtyValue, okDirty)
	started, okStart := strAt(data, "run", "started_at")
	row("Started", "run.started_at", started, okStart)
	finished, okFin := strAt(data, "run", "finished_at")
	row("Finished", "run.finished_at", finished, okFin)
	goVersion, okGo := strAt(data, "run", "go_version")
	row("Go version", "run.go_version", goVersion, okGo)
	wall, okWall := numAt(data, "runtime", "wall_seconds")
	row("Wall seconds", "runtime.wall_seconds", fmtNum(wall), okWall)
	eps, okEPS := numAt(data, "runtime", "events_per_sec")
	row("Events/sec", "runtime.events_per_sec", fmtNum(eps), okEPS)
	if len(missing) > 0 {
		fmt.Fprintf(b, "\nNot recorded: %s.\n", strings.Join(missing, ", "))
	}
}

// dirtyStatement renders the recorded dirty flag, making a dirty tree impossible to
// miss: such a run is not reproducible from the recorded sha alone.
func dirtyStatement(data map[string]any) (string, bool) {
	dirty, ok := boolAt(data, "run", "git_dirty")
	if !ok {
		return "", false
	}
	if dirty {
		return "**DIRTY — the working tree had uncommitted changes when this run was recorded**", true
	}
	return "clean", true
}

func writeCorpus(b *strings.Builder, data map[string]any) {
	b.WriteString("\n## Corpus\n\n")
	corpus, ok := mapAt(data, "corpus")
	if !ok {
		fmt.Fprintf(b, "The corpus is %s.\n", notRecorded("corpus"))
		if parent, okParent := strAt(data, "run", "parent_run"); okParent && parent != "" {
			fmt.Fprintf(b, "This result derives from parent run `%s` (`run.parent_run`).\n", parent)
		}
		return
	}
	if syn, okSyn := strAt(corpus, "synthetic"); okSyn && syn != "" {
		fmt.Fprintf(b, "Synthetic input, as recorded: %s\n\n", syn)
	}
	writeCorpusFiles(b, corpus)
	writeCorpusCounts(b, corpus)
	writeBurnIn(b, corpus)
	writeCoverage(b, corpus)
}

func writeCorpusFiles(b *strings.Builder, corpus map[string]any) {
	files, ok := corpus["files"].([]any)
	if !ok || len(files) == 0 {
		if _, okSyn := corpus["synthetic"]; !okSyn {
			fmt.Fprintf(b, "The corpus files are %s.\n\n", notRecorded("corpus.files"))
		}
		return
	}
	b.WriteString("| File | sha256 (truncated to twelve characters) |\n| --- | --- |\n")
	for i, raw := range files {
		name := notRecorded(fmt.Sprintf("corpus.files[%d].path", i))
		digest := notRecorded(fmt.Sprintf("corpus.files[%d].sha256", i))
		if fm, okFile := raw.(map[string]any); okFile {
			if p, okPath := fm["path"].(string); okPath && p != "" {
				name = filepath.Base(p)
			}
			if d, okDigest := fm["sha256"].(string); okDigest && d != "" {
				digest = shortDigest(d)
			}
		}
		fmt.Fprintf(b, "| %s | %s |\n", escapeCell(name), escapeCell(digest))
	}
	b.WriteString("\nDigests are truncated for the page; result.json beside this report carries them in full.\n\n")
}

func writeCorpusCounts(b *strings.Builder, corpus map[string]any) {
	b.WriteString("| Figure | Value |\n| --- | --- |\n")
	for _, item := range []struct{ label, key string }{
		{"Rows read", "rows_read"},
		{"Events warmed (burn-in)", "events_warmed"},
		{"Events scored", "events_scored"},
		{"Events skipped", "events_skipped"},
		{"Row errors", "row_errors"},
	} {
		value := notRecorded("corpus." + item.key)
		if v, ok := numAt(corpus, item.key); ok {
			value = fmtNum(v)
		}
		fmt.Fprintf(b, "| %s | %s |\n", item.label, value)
	}
	b.WriteString("\n")
}

func writeBurnIn(b *strings.Builder, corpus map[string]any) {
	end, ok := numAt(corpus, "burn_in", "end_seconds")
	if !ok {
		fmt.Fprintf(b, "The burn-in boundary is %s.\n", notRecorded("corpus.burn_in.end_seconds"))
		return
	}
	line := fmt.Sprintf("Burn-in ends at %s seconds of corpus time", fmtNum(end))
	if commit, okCommit := strAt(corpus, "burn_in", "fixed_at_commit"); okCommit && commit != "" {
		line += fmt.Sprintf(", fixed at commit `%s`", commit)
	}
	b.WriteString(line + ".\n")
}

// writeCoverage prefers the run's own coverage statement: the run knows which cap was
// applied, and a description reconstructed here could misdescribe what was measured.
func writeCoverage(b *strings.Builder, corpus map[string]any) {
	if s, ok := strAt(corpus, "coverage", "statement"); ok && s != "" {
		fmt.Fprintf(b, "Coverage, as recorded: %s.\n", s)
		return
	}
	if k, ok := strAt(corpus, "coverage", "kind"); ok && k != "" {
		fmt.Fprintf(b, "Coverage kind, as recorded: %s (`corpus.coverage.statement` is absent).\n", k)
		return
	}
	fmt.Fprintf(b, "Coverage is %s.\n", notRecorded("corpus.coverage"))
}

// writeSampling renders the entity-sampling record. A sampled run announces itself
// with a warning, because a detection rate measured on a sample whose labelled share
// is inflated is not comparable to one measured on the full population.
func writeSampling(b *strings.Builder, data map[string]any) {
	b.WriteString("\n## Sampling\n\n")
	sample, ok := mapAt(data, "parameters", "entity_sample")
	if !ok {
		fmt.Fprintf(b, "No entity sampling block is recorded (`parameters.entity_sample` is absent); the full population was scored.\n")
		return
	}
	if applied, okApplied := boolAt(sample, "applied"); !okApplied || !applied {
		b.WriteString("Entity sampling was not applied: the full population was scored.\n")
		return
	}
	keep := notRecorded("parameters.entity_sample.keep_one_in_n")
	if v, okKeep := numAt(sample, "keep_one_in_n"); okKeep {
		keep = fmtNum(v)
	}
	selector := notRecorded("parameters.entity_sample.selector")
	if s, okSel := strAt(sample, "selector"); okSel && s != "" {
		selector = s
	}
	fmt.Fprintf(b, "> **WARNING — entity sampling was applied.** One entity in %s was kept; selector: %s.\n>\n", keep, selector)
	if note, okNote := strAt(sample, "note"); okNote && note != "" {
		fmt.Fprintf(b, "> %s\n>\n", note)
	} else {
		fmt.Fprintf(b, "> The sampling note is %s.\n>\n", notRecorded("parameters.entity_sample.note"))
	}
	b.WriteString("> A sampled run's detection rate is not comparable to a full-population one.\n")
}

// namedParameters are the parameters every scoring run is expected to record; each is
// listed even when absent, so an unrecorded parameter is visible rather than missing.
var namedParameters = []string{"alpha", "half_life_days", "bandwidth_hours", "grid", "top_k_per_day"}

func writeParameters(b *strings.Builder, data map[string]any) {
	b.WriteString("\n## Parameters\n\n")
	params, ok := mapAt(data, "parameters")
	if !ok {
		fmt.Fprintf(b, "Parameters are %s.\n", notRecorded("parameters"))
		return
	}
	b.WriteString("| Parameter | Value |\n| --- | --- |\n")
	named := map[string]bool{}
	for _, k := range namedParameters {
		named[k] = true
		value := notRecorded("parameters." + k)
		if s, okScalar := scalar(params[k]); okScalar {
			value = s
		}
		fmt.Fprintf(b, "| %s | %s |\n", k, escapeCell(value))
	}
	extras := make([]string, 0, len(params))
	for k := range params {
		if _, isScalar := scalar(params[k]); isScalar && !named[k] {
			extras = append(extras, k)
		}
	}
	sort.Strings(extras)
	for _, k := range extras {
		s, _ := scalar(params[k])
		fmt.Fprintf(b, "| %s | %s |\n", k, escapeCell(s))
	}
}

// numCell renders a recorded number, or names the key it did not find.
func numCell(m map[string]any, key string) string {
	if v, ok := numAt(m, key); ok {
		return fmtNum(v)
	}
	return notRecorded(key)
}

// strCell renders a recorded string, or names the key it did not find.
func strCell(m map[string]any, key string) string {
	if v, ok := strAt(m, key); ok && v != "" {
		return escapeCell(v)
	}
	return notRecorded(key)
}

// pctIntervalCell renders a recorded {point, low, high} interval as a percentage, in
// the same shape cmd/partii renders it.
func pctIntervalCell(m map[string]any, key string) string {
	point, low, high, ok := intervalAt(m, key)
	if !ok {
		return notRecorded(key)
	}
	return fmt.Sprintf("%s%% [%s, %s]", pct(point), pct(low), pct(high))
}

func writeDetection(b *strings.Builder, data map[string]any) {
	rows, ok := listAt(data, "results", "detection")
	if !ok || len(rows) == 0 {
		return
	}
	b.WriteString("\n## Detection at matched budget\n\n")
	b.WriteString("| Budget/day | Alerts | True positives | False negatives | Red-team scored | Recall | Precision |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- | --- |\n")
	for i, raw := range rows {
		rm, okRow := raw.(map[string]any)
		if !okRow {
			fmt.Fprintf(b, "| %s | | | | | | |\n", notRecorded(fmt.Sprintf("results.detection[%d]", i)))
			continue
		}
		fmt.Fprintf(b, "| %s | %s | %s | %s | %s | %s | %s |\n",
			numCell(rm, "budget_per_day"), numCell(rm, "alerts"),
			numCell(rm, "true_positives"), numCell(rm, "false_negatives"),
			numCell(rm, "red_team_scored"),
			pctIntervalCell(rm, "recall_wilson"), pctIntervalCell(rm, "precision_wilson"))
	}
}

func writeCategories(b *strings.Builder, data map[string]any) {
	counts, ok := mapAt(data, "results", "anomaly_categories", "counts")
	if !ok || len(counts) == 0 {
		return
	}
	b.WriteString("\n## Anomaly categories\n\n| Category | Scored events | Red-team events |\n| --- | --- | --- |\n")
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		cm, okCat := counts[name].(map[string]any)
		if !okCat {
			fmt.Fprintf(b, "| %s | %s | |\n", escapeCell(name),
				notRecorded("results.anomaly_categories.counts."+name))
			continue
		}
		fmt.Fprintf(b, "| %s | %s | %s |\n", escapeCell(name),
			numCell(cm, "scored_events"), numCell(cm, "red_team_events"))
	}
	if def, okDef := strAt(data, "results", "anomaly_categories", "definition"); okDef && def != "" {
		fmt.Fprintf(b, "\nDefinition, as recorded: %s\n", def)
	} else {
		fmt.Fprintf(b, "\nThe category definition is %s.\n", notRecorded("results.anomaly_categories.definition"))
	}
}

// ratioCell renders times_better when the file carries it, and the em-dash when the
// file records the ratio as undefined; the recorded explanation is returned so it can
// be shown beneath the table. A ratio is never manufactured.
func ratioCell(row map[string]any) (cell, explanation string) {
	if v, ok := numAt(row, "times_better"); ok {
		return fmt.Sprintf("%.1f×", v), ""
	}
	why, _ := strAt(row, "ratio_undefined")
	return "—", why
}

// deltaCell renders the recorded recall difference in percentage points.
func deltaCell(m map[string]any) string {
	if v, ok := numAt(m, "delta_percentage_points"); ok {
		return fmt.Sprintf("%+.1f", v)
	}
	return notRecorded("delta_percentage_points")
}

func writeHeadToHead(b *strings.Builder, data map[string]any) {
	rows, ok := listAt(data, "results", "head_to_head")
	if !ok || len(rows) == 0 {
		return
	}
	b.WriteString("\n## Head-to-head\n\n| Budget/day | Baseline | Framework detected | Baseline detected | Δ (pp) | Ratio |\n| --- | --- | --- | --- | --- | --- |\n")
	var explanations []string
	for i, raw := range rows {
		rm, okRow := raw.(map[string]any)
		if !okRow {
			fmt.Fprintf(b, "| %s | | | | | |\n", notRecorded(fmt.Sprintf("results.head_to_head[%d]", i)))
			continue
		}
		ratio, why := ratioCell(rm)
		explanations = appendUnique(explanations, why)
		fmt.Fprintf(b, "| %s | %s | %s | %s | %s | %s |\n",
			numCell(rm, "budget_per_day"), strCell(rm, "baseline"),
			numCell(rm, "framework_detected"), numCell(rm, "baseline_detected"),
			deltaCell(rm), ratio)
	}
	for _, why := range explanations {
		fmt.Fprintf(b, "\nWhere the ratio column shows —, the run records: %s.\n", why)
	}
}

func writeCalibration(b *strings.Builder, data map[string]any) {
	bh, okBH := listAt(data, "results", "calibration_bh")
	by, okBY := listAt(data, "results", "calibration_by")
	if (!okBH || len(bh) == 0) && (!okBY || len(by) == 0) {
		return
	}
	b.WriteString("\n## Calibration\n\n| Procedure | Nominal q | Discoveries | True positives | Realised FDR | Conservatism ratio | Saturated days |\n| --- | --- | --- | --- | --- | --- | --- |\n")
	censored := false
	for _, block := range []struct {
		key  string
		rows []any
	}{{"results.calibration_bh", bh}, {"results.calibration_by", by}} {
		censored = writeCalibrationRows(b, block.key, block.rows) || censored
	}
	if censored {
		b.WriteString("\n**Warning: rows above record `saturated_days` greater than zero. The discovery count is right-censored by the retention limit, and the realised FDR derived from it is a lower bound on the count, not a measurement.**\n")
	}
}

// writeCalibrationRows renders one procedure's rows and reports whether any recorded
// saturated_days is greater than zero, so the caller can raise the censoring warning.
func writeCalibrationRows(b *strings.Builder, key string, rows []any) bool {
	censored := false
	for i, raw := range rows {
		rm, okRow := raw.(map[string]any)
		if !okRow {
			fmt.Fprintf(b, "| %s | | | | | | |\n", notRecorded(fmt.Sprintf("%s[%d]", key, i)))
			continue
		}
		if v, okSat := numAt(rm, "saturated_days"); okSat && v > 0 {
			censored = true
		}
		fdr := notRecorded("realised_fdr")
		if point, low, high, okFDR := intervalAt(rm, "realised_fdr"); okFDR {
			fdr = fmt.Sprintf("%s [%s, %s]", fmtSig(point), fmtSig(low), fmtSig(high))
		}
		nominal := notRecorded("nominal_q")
		if v, okQ := numAt(rm, "nominal_q"); okQ {
			nominal = fmtSig(v)
		}
		fmt.Fprintf(b, "| %s | %s | %s | %s | %s | %s | %s |\n",
			strCell(rm, "procedure"), nominal,
			numCell(rm, "discoveries"), numCell(rm, "true_positives"),
			fdr, numCell(rm, "conservatism_ratio"), numCell(rm, "saturated_days"))
	}
	return censored
}

func writeRuntime(b *strings.Builder, data map[string]any) {
	b.WriteString("\n## Runtime\n\n")
	rt, ok := mapAt(data, "runtime")
	if !ok {
		fmt.Fprintf(b, "The runtime block is %s.\n", notRecorded("runtime"))
		return
	}
	b.WriteString("| Figure | Value |\n| --- | --- |\n")
	keys := make([]string, 0, len(rt))
	for k := range rt {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		value, okScalar := scalar(rt[k])
		if !okScalar {
			value = "recorded as a non-scalar value; see result.json"
		}
		fmt.Fprintf(b, "| %s | %s |\n", escapeCell(k), escapeCell(value))
	}
}

func writeFooter(b *strings.Builder, data map[string]any, sourceFile string) {
	b.WriteString("\n---\n\n")
	if command, ok := strAt(data, "run", "command"); ok && command != "" {
		fmt.Fprintf(b, "Reproduce with:\n\n```\n%s\n```\n\n", command)
	} else {
		b.WriteString("The command line was not recorded (`run.command` is absent).\n\n")
	}
	fmt.Fprintf(b, "Rendered by cmd/runreport from %s only. A number that does not appear in the result file does not appear here.\n", sourceFile)
}
