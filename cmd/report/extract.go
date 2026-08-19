package main

// Typed views over the untyped result JSON. Extraction is deliberately lenient about
// absence — a missing block means the figure or table that needs it is simply not
// produced — and strict about never inventing a value: nothing here has a default
// that could be mistaken for data.

import (
	"fmt"
	"sort"
	"strings"
)

func getMap(m map[string]any, key string) map[string]any {
	v, _ := m[key].(map[string]any)
	return v
}

func getString(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func getFloat(m map[string]any, key string) (float64, bool) {
	v, ok := m[key].(float64)
	return v, ok
}

func kindOf(r resultFile) string { return getString(r.Data, "kind") }

func resultsBlock(r resultFile) map[string]any { return getMap(r.Data, "results") }

func runID(r resultFile) string { return getString(getMap(r.Data, "run"), "run_id") }

func allOfKind(results []resultFile, kind string) []resultFile {
	out := []resultFile{}
	for _, r := range results {
		if kindOf(r) == kind {
			out = append(out, r)
		}
	}
	return out
}

// firstReplayWith returns the first replay result (in file order, which loadResults
// sorts) whose results block carries the named key.
func firstReplayWith(results []resultFile, key string) (resultFile, bool) {
	for _, r := range allOfKind(results, "replay") {
		if _, ok := resultsBlock(r)[key]; ok {
			return r, true
		}
	}
	return resultFile{}, false
}

// firstAnalysisWithCalibration returns the first analysis result carrying
// results.calibration_bh.
func firstAnalysisWithCalibration(results []resultFile) (resultFile, bool) {
	for _, r := range allOfKind(results, "analysis") {
		if _, ok := resultsBlock(r)["calibration_bh"]; ok {
			return r, true
		}
	}
	return resultFile{}, false
}

// ---------------------------------------------------------------------------
// Calibration (cmd/analyse output)
// ---------------------------------------------------------------------------

type calibrationPoint struct {
	NominalQ      float64
	Discoveries   float64
	TruePositives float64
	RealisedFDR   float64
	WilsonLow     float64
	WilsonHigh    float64
	Conservatism  float64
	SaturatedDays float64
	Procedure     string
}

func calibrationSeries(r resultFile, key string) []calibrationPoint {
	raw, _ := resultsBlock(r)[key].([]any)
	pts := make([]calibrationPoint, 0, len(raw))
	for _, v := range raw {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		p := calibrationPoint{Procedure: getString(m, "procedure")}
		p.NominalQ, _ = getFloat(m, "nominal_q")
		p.Discoveries, _ = getFloat(m, "discoveries")
		p.TruePositives, _ = getFloat(m, "true_positives")
		p.RealisedFDR, _ = getFloat(m, "realised_fdr")
		p.WilsonLow, _ = getFloat(m, "wilson_low_95")
		p.WilsonHigh, _ = getFloat(m, "wilson_high_95")
		p.Conservatism, _ = getFloat(m, "conservatism_ratio")
		p.SaturatedDays, _ = getFloat(m, "saturated_days")
		if p.NominalQ > 0 {
			pts = append(pts, p)
		}
	}
	sort.Slice(pts, func(i, j int) bool { return pts[i].NominalQ < pts[j].NominalQ })
	return pts
}

// ---------------------------------------------------------------------------
// Detection at matched budget (cmd/replay and sidecar/baselines.py outputs)
// ---------------------------------------------------------------------------

type budgetDetection struct {
	Budget        int
	Alerts        float64
	HasAlerts     bool
	TruePositives float64
	RedTeamTotal  float64
}

// budgetDetections parses a detections_at_budget block. The replay engine writes
// per-budget true_positives and alerts; the baselines sidecar writes detections and
// no alert count, so Alerts is optional and TruePositives falls back to detections.
func budgetDetections(block map[string]any) []budgetDetection {
	det := getMap(block, "detections_at_budget")
	out := make([]budgetDetection, 0, len(det))
	for key, v := range det {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		var budget int
		if _, err := fmt.Sscanf(key, "budget_%d_per_day", &budget); err != nil {
			continue
		}
		d := budgetDetection{Budget: budget}
		if tp, ok := getFloat(m, "true_positives"); ok {
			d.TruePositives = tp
		} else if tp, ok := getFloat(m, "detections"); ok {
			d.TruePositives = tp
		} else {
			continue
		}
		d.RedTeamTotal, _ = getFloat(m, "red_team_total")
		d.Alerts, d.HasAlerts = getFloat(m, "alerts")
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Budget < out[j].Budget })
	return out
}

// baselineModels is the fixed order in which sidecar baselines are drawn and
// tabulated; the colours in F4 are assigned positionally against this list.
var baselineModels = []string{"iforest", "eif", "hst", "rrcf"}

// frameworkArmName names a replay arm by its detectors and partition mode, which is
// what distinguishes the E4 arms from one another.
func frameworkArmName(r resultFile) string {
	run := getMap(r.Data, "run")
	parts := []string{}
	if d, ok := run["detectors"].([]any); ok {
		for _, v := range d {
			if s, ok := v.(string); ok {
				parts = append(parts, s)
			}
		}
	}
	name := strings.Join(parts, "+")
	if name == "" {
		name = "framework"
	}
	if p := getString(run, "partition"); p != "" {
		name += " · partition " + p
	}
	return name
}

// ---------------------------------------------------------------------------
// Per-detector p-value histograms (cmd/replay output)
// ---------------------------------------------------------------------------

type detectorHistogram struct {
	Detector string
	Counts   []float64
	Under    float64 // mass below 1e-12, outside the binned range
}

func (h detectorHistogram) total() float64 {
	t := 0.0
	for _, c := range h.Counts {
		t += c
	}
	return t
}

func detectorHistograms(r resultFile) []detectorHistogram {
	raw := getMap(resultsBlock(r), "p_histograms")
	out := make([]detectorHistogram, 0, len(raw))
	for name, v := range raw {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		counts, ok := m["counts"].([]any)
		if !ok {
			continue
		}
		h := detectorHistogram{Detector: name, Counts: make([]float64, 0, len(counts))}
		for _, c := range counts {
			f, _ := c.(float64)
			h.Counts = append(h.Counts, f)
		}
		h.Under, _ = getFloat(m, "under_1e_12")
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Detector < out[j].Detector })
	return out
}

// ---------------------------------------------------------------------------
// Circular evidence (the §12.5 wraparound control output)
// ---------------------------------------------------------------------------

// curvePoint is one grid point of the control's fitted circular density with the
// two arms' p-values at that time of day.
type curvePoint struct {
	Hour, Density, PCircular, PCells float64
}

// firstControlWithCurve returns the first control result carrying results.curve.
func firstControlWithCurve(results []resultFile) (resultFile, bool) {
	for _, r := range allOfKind(results, "control") {
		if _, ok := resultsBlock(r)["curve"]; ok {
			return r, true
		}
	}
	return resultFile{}, false
}

// evidenceCurve parses results.curve, sorted by hour. A point missing any of its
// four fields is dropped rather than defaulted: absence must never become a
// drawable zero.
func evidenceCurve(r resultFile) []curvePoint {
	raw, _ := resultsBlock(r)["curve"].([]any)
	pts := make([]curvePoint, 0, len(raw))
	for _, v := range raw {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		var p curvePoint
		var okHour, okDensity, okCircular, okCells bool
		p.Hour, okHour = getFloat(m, "hour")
		p.Density, okDensity = getFloat(m, "density")
		p.PCircular, okCircular = getFloat(m, "p_circular")
		p.PCells, okCells = getFloat(m, "p_cells")
		if okHour && okDensity && okCircular && okCells {
			pts = append(pts, p)
		}
	}
	sort.Slice(pts, func(i, j int) bool { return pts[i].Hour < pts[j].Hour })
	return pts
}

// probeTriple carries one arm's p-values at the three §12.5 probe times.
type probeTriple struct {
	P2330, P0030, P1200 float64
}

// probeTripleOf reads results.probes.<arm>; the triple counts as present only when
// all three probe values are.
func probeTripleOf(r resultFile, arm string) (probeTriple, bool) {
	m := getMap(getMap(resultsBlock(r), "probes"), arm)
	var t probeTriple
	var ok2330, ok0030, ok1200 bool
	t.P2330, ok2330 = getFloat(m, "p_23_30")
	t.P0030, ok0030 = getFloat(m, "p_00_30")
	t.P1200, ok1200 = getFloat(m, "p_12_00")
	return t, ok2330 && ok0030 && ok1200
}

// acceptanceBlock is results.acceptance of a control result: the pre-registered
// criteria text and whether each arm met them.
type acceptanceBlock struct {
	Criteria        string
	CircularPasses  bool
	CellsShowDefect bool
}

func acceptanceOf(r resultFile) (acceptanceBlock, bool) {
	m := getMap(resultsBlock(r), "acceptance")
	a := acceptanceBlock{Criteria: getString(m, "criteria")}
	a.CircularPasses, _ = m["circular_passes"].(bool)
	a.CellsShowDefect, _ = m["cells_show_defect"].(bool)
	return a, a.Criteria != ""
}

func paramsBlock(r resultFile) map[string]any { return getMap(r.Data, "parameters") }

// ---------------------------------------------------------------------------
// Measured co-occurrence graph size (cmd/replay runtime block)
// ---------------------------------------------------------------------------

type graphSize struct {
	Nodes, Edges float64
}

// graphSizeOf reads the measured node and edge counts; the size counts as present
// only when both are.
func graphSizeOf(r resultFile) (graphSize, bool) {
	rt := getMap(r.Data, "runtime")
	var g graphSize
	var okNodes, okEdges bool
	g.Nodes, okNodes = getFloat(rt, "graph_nodes")
	g.Edges, okEdges = getFloat(rt, "graph_edges")
	return g, okNodes && okEdges
}

// ---------------------------------------------------------------------------
// Status distribution (cmd/replay output; the four §5.3 statuses)
// ---------------------------------------------------------------------------

var statusNames = []string{
	"evaluated", "abstained_structural", "abstained_unexpected", "abstained_unusable",
}

type statusRow struct {
	Detector string
	Counts   map[string]float64
}

func statusRowsOf(r resultFile) []statusRow {
	raw := getMap(resultsBlock(r), "status_counts")
	out := make([]statusRow, 0, len(raw))
	for det, v := range raw {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		row := statusRow{Detector: det, Counts: map[string]float64{}}
		for _, s := range statusNames {
			if f, ok := getFloat(m, s); ok {
				row.Counts[s] = f
			}
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Detector < out[j].Detector })
	return out
}

// ---------------------------------------------------------------------------
// Provenance footer lines
// ---------------------------------------------------------------------------

// provLine renders one result file's provenance for a figure or table footer:
// run_id · file · git sha (dirty marker) · coverage. Fields a result does not carry
// (the analysis command records no git sha of its own) are omitted, never invented.
func provLine(r resultFile) string {
	s := summarise(r)
	parts := []string{"run " + s.RunID, s.File}
	if s.GitSHA != "" {
		g := "git " + s.GitSHA
		if s.Dirty {
			g += " (dirty tree)"
		}
		parts = append(parts, g)
	}
	if s.Coverage != "" {
		parts = append(parts, "coverage: "+s.Coverage)
	}
	return strings.Join(parts, " · ")
}
