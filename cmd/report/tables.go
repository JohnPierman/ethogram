package main

// Tables T1, T2, T3, T5, and T-E7, each rendered only from blocks a result file
// actually carries. Derived cells (FN, recall, precision) are ratios or differences
// of recorded numbers; nothing is estimated and nothing is invented — a quantity the
// data cannot support renders as an em dash.

import (
	"fmt"
	"html/template"
	"sort"
	"strconv"
	"strings"
)

// tableCard is one table as embedded in report.html with its caption and the
// provenance footer naming every run that contributed a number.
type tableCard struct {
	ID, Title, Caption string
	Table              template.HTML
	Prov               []string
}

func buildTables(results []resultFile) []tableCard {
	builders := []func([]resultFile) (tableCard, bool){
		tableT1, tableT2, tableT3, tableT5, tableTE7,
	}
	out := []tableCard{}
	for _, build := range builders {
		if t, ok := build(results); ok {
			out = append(out, t)
		}
	}
	return out
}

func renderTable(headers []string, rows [][]string) template.HTML {
	var b strings.Builder
	b.WriteString("<table><thead><tr>")
	for _, h := range headers {
		b.WriteString("<th>")
		b.WriteString(template.HTMLEscapeString(h))
		b.WriteString("</th>")
	}
	b.WriteString("</tr></thead><tbody>\n")
	for _, row := range rows {
		b.WriteString("<tr>")
		for _, cellText := range row {
			b.WriteString("<td>")
			b.WriteString(template.HTMLEscapeString(cellText))
			b.WriteString("</td>")
		}
		b.WriteString("</tr>\n")
	}
	b.WriteString("</tbody></table>")
	return template.HTML(b.String()) //nolint:gosec // every cell above is escaped
}

const absentCell = "—"

func intCell(v float64) string { return strconv.FormatFloat(v, 'f', 0, 64) }

func fixedCell(v float64, prec int) string { return strconv.FormatFloat(v, 'f', prec, 64) }

func ratioCell(num, den float64) string {
	if den <= 0 {
		return absentCell
	}
	return fixedCell(num/den, 3)
}

// ---------------------------------------------------------------------------
// T1 — detection at matched alert budget
// ---------------------------------------------------------------------------

func tableT1(results []resultFile) (tableCard, bool) {
	rows := [][]string{}
	prov := []string{}
	captions := []string{}
	for _, r := range allOfKind(results, "replay") {
		dets := budgetDetections(resultsBlock(r))
		if len(dets) == 0 {
			continue
		}
		name := frameworkArmName(r)
		for _, d := range dets {
			rows = append(rows, t1Row(name, d))
		}
		prov = append(prov, provLine(r))
		captions = append(captions, t1Caption(name, dets[0], r))
	}
	rows, prov, captions = appendBaselineRows(results, rows, prov, captions)
	if len(rows) == 0 {
		return tableCard{}, false
	}
	headers := []string{"source", "budget/day", "alerts", "TP", "FN", "recall", "precision"}
	return tableCard{
		ID: "T1", Title: "Detection at matched alert budget",
		Caption: strings.Join(captions, " · "),
		Table:   renderTable(headers, rows), Prov: prov,
	}, true
}

// t1Row derives FN as red_team_total - TP, recall as TP/red_team_total, and
// precision as TP/alerts. The baselines sidecar records no alert count, so its
// alerts and precision cells are absent rather than reconstructed.
func t1Row(name string, d budgetDetection) []string {
	alerts, precision := absentCell, absentCell
	if d.HasAlerts {
		alerts = intCell(d.Alerts)
		precision = ratioCell(d.TruePositives, d.Alerts)
	}
	return []string{
		name, strconv.Itoa(d.Budget), alerts,
		intCell(d.TruePositives), intCell(d.RedTeamTotal - d.TruePositives),
		ratioCell(d.TruePositives, d.RedTeamTotal), precision,
	}
}

func t1Caption(name string, d budgetDetection, r resultFile) string {
	s := fmt.Sprintf("%s: n = %s red-team events scored", name, intCell(d.RedTeamTotal))
	if cov := summarise(r).Coverage; cov != "" {
		s += ", coverage " + cov
	}
	return s
}

func appendBaselineRows(results []resultFile, rows [][]string, prov, captions []string) ([][]string, []string, []string) {
	baselines := allOfKind(results, "baselines")
	if len(baselines) == 0 {
		return rows, prov, captions
	}
	r := baselines[0]
	added := false
	for _, model := range baselineModels {
		dets := budgetDetections(getMap(resultsBlock(r), model))
		if len(dets) == 0 {
			continue
		}
		for _, d := range dets {
			rows = append(rows, t1Row(model, d))
		}
		if !added {
			captions = append(captions, t1Caption("baselines", dets[0], r))
		}
		added = true
	}
	if added {
		prov = append(prov, provLine(r))
	}
	return rows, prov, captions
}

// ---------------------------------------------------------------------------
// T2 — calibration
// ---------------------------------------------------------------------------

func tableT2(results []resultFile) (tableCard, bool) {
	r, ok := firstAnalysisWithCalibration(results)
	if !ok {
		return tableCard{}, false
	}
	pts := append(calibrationSeries(r, "calibration_bh"), calibrationSeries(r, "calibration_by")...)
	if len(pts) == 0 {
		return tableCard{}, false
	}
	headers := []string{"nominal q", "procedure", "discoveries", "TP", "realised FDR",
		"Wilson 95%", "conservatism", "saturated days"}
	rows := make([][]string, 0, len(pts))
	for _, p := range pts {
		rows = append(rows, []string{
			strconv.FormatFloat(p.NominalQ, 'g', -1, 64),
			p.Procedure,
			intCell(p.Discoveries), intCell(p.TruePositives),
			fixedCell(p.RealisedFDR, 4),
			"[" + fixedCell(p.WilsonLow, 4) + ", " + fixedCell(p.WilsonHigh, 4) + "]",
			fixedCell(p.Conservatism, 2), intCell(p.SaturatedDays),
		})
	}
	return tableCard{
		ID: "T2", Title: "Calibration: realised FDR against nominal q",
		Table: renderTable(headers, rows), Prov: []string{provLine(r)},
	}, true
}

// ---------------------------------------------------------------------------
// T3 — per-detector behaviour (§5.3 statuses)
// ---------------------------------------------------------------------------

func tableT3(results []resultFile) (tableCard, bool) {
	r, ok := firstReplayWith(results, "status_counts")
	if !ok {
		if r, ok = firstReplayWith(results, "p_histograms"); !ok {
			return tableCard{}, false
		}
	}
	statuses := statusRowsOf(r)
	hists := detectorHistograms(r)
	if len(statuses) == 0 && len(hists) == 0 {
		return tableCard{}, false
	}
	rows := t3Rows(statuses, hists)
	headers := []string{"detector", "evaluated", "abstained_structural",
		"abstained_unexpected", "abstained_unusable", "n evaluated (p-values)"}
	return tableCard{
		ID: "T3", Title: "Per-detector behaviour (§5.3 statuses)",
		Caption: "p-value mean/variance/KS are not recoverable from the binned histograms; " +
			"the table shows what the result carries",
		Table: renderTable(headers, rows), Prov: []string{provLine(r)},
	}, true
}

func t3Rows(statuses []statusRow, hists []detectorHistogram) [][]string {
	statusByDet := make(map[string]statusRow, len(statuses))
	histN := make(map[string]float64, len(hists))
	detectors := []string{}
	for _, s := range statuses {
		statusByDet[s.Detector] = s
		detectors = append(detectors, s.Detector)
	}
	for _, h := range hists {
		histN[h.Detector] = h.total() + h.Under
		if _, seen := statusByDet[h.Detector]; !seen {
			detectors = append(detectors, h.Detector)
		}
	}
	sort.Strings(detectors)
	rows := make([][]string, 0, len(detectors))
	for _, det := range detectors {
		row := []string{det}
		sr, hasStatus := statusByDet[det]
		for _, s := range statusNames {
			if v, ok := sr.Counts[s]; hasStatus && ok {
				row = append(row, intCell(v))
			} else {
				row = append(row, absentCell)
			}
		}
		if n, ok := histN[det]; ok {
			row = append(row, intCell(n))
		} else {
			row = append(row, absentCell)
		}
		rows = append(rows, row)
	}
	return rows
}

// ---------------------------------------------------------------------------
// T5 — runtime
// ---------------------------------------------------------------------------

var t5Metrics = []struct {
	Label, Key string
	Prec       int
}{
	{"wall seconds", "wall_seconds", 1},
	{"events/sec", "events_per_sec", 0},
	{"heap alloc MB", "heap_alloc_mb", 1},
	{"heap sys MB", "heap_sys_mb", 1},
	{"novelty rows", "novelty_rows", 0},
	{"timing entities", "timing_entities", 0},
	{"volume entities", "volume_entities", 0},
	{"graph nodes", "graph_nodes", 0},
	{"graph edges", "graph_edges", 0},
}

func tableT5(results []resultFile) (tableCard, bool) {
	replays := []resultFile{}
	for _, r := range allOfKind(results, "replay") {
		if getMap(r.Data, "runtime") != nil {
			replays = append(replays, r)
		}
	}
	if len(replays) == 0 {
		return tableCard{}, false
	}
	headers := []string{"metric"}
	prov := []string{}
	for _, r := range replays {
		headers = append(headers, runID(r))
		prov = append(prov, provLine(r))
	}
	rows := make([][]string, 0, len(t5Metrics))
	for _, m := range t5Metrics {
		row := []string{m.Label}
		for _, r := range replays {
			if v, ok := getFloat(getMap(r.Data, "runtime"), m.Key); ok {
				row = append(row, fixedCell(v, m.Prec))
			} else {
				row = append(row, absentCell)
			}
		}
		rows = append(rows, row)
	}
	return tableCard{
		ID: "T5", Title: "Runtime",
		Table: renderTable(headers, rows), Prov: prov,
	}, true
}

// ---------------------------------------------------------------------------
// T-E7 — verdict reconstruction from evidence alone
// ---------------------------------------------------------------------------

func tableTE7(results []resultFile) (tableCard, bool) {
	e7s := allOfKind(results, "e7")
	if len(e7s) == 0 {
		return tableCard{}, false
	}
	r := e7s[0]
	res := resultsBlock(r)
	proportion, hasProportion := getFloat(res, "proportion_reconstructed")
	totals := getMap(res, "totals")
	byDetector := getMap(res, "by_detector")
	if !hasProportion && totals == nil && byDetector == nil {
		return tableCard{}, false
	}
	captions := []string{}
	if hasProportion {
		captions = append(captions, "proportion reconstructed "+fixedCell(proportion, 4))
	}
	if totals != nil {
		captions = append(captions, "totals: "+strings.Join(e7LabelledCounts(totals), ", "))
	}
	headers := []string{"detector", "sampled", "reconstructed", "partial", "failed"}
	rows := te7Rows(byDetector)
	return tableCard{
		ID: "T-E7", Title: "E7: verdict reconstruction from evidence alone",
		Caption: strings.Join(captions, " · "),
		Table:   renderTable(headers, rows), Prov: []string{provLine(r)},
	}, true
}

var e7CountKeys = []string{"sampled", "reconstructed", "partial", "failed"}

// e7Counts returns the four E7 counters as bare table cells.
func e7Counts(m map[string]any) []string {
	out := make([]string, 0, len(e7CountKeys))
	for _, k := range e7CountKeys {
		if v, ok := getFloat(m, k); ok {
			out = append(out, intCell(v))
		} else {
			out = append(out, absentCell)
		}
	}
	return out
}

// e7LabelledCounts returns the four E7 counters prefixed with their keys, for the
// table caption.
func e7LabelledCounts(m map[string]any) []string {
	cells := e7Counts(m)
	out := make([]string, 0, len(cells))
	for i, k := range e7CountKeys {
		if cells[i] == absentCell {
			out = append(out, k+" "+absentCell)
			continue
		}
		out = append(out, k+" "+cells[i])
	}
	return out
}

func te7Rows(byDetector map[string]any) [][]string {
	detectors := make([]string, 0, len(byDetector))
	for det := range byDetector {
		detectors = append(detectors, det)
	}
	sort.Strings(detectors)
	rows := make([][]string, 0, len(detectors))
	for _, det := range detectors {
		m, ok := byDetector[det].(map[string]any)
		if !ok {
			continue
		}
		rows = append(rows, append([]string{det}, e7Counts(m)...))
	}
	return rows
}
