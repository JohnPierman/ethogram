package main

// Evidence figures F1, F2, and F8. Like F3-F6 they are generated only when a result
// file carries the data they need, and every number, curve, and label on them comes
// out of that file. F1 and F8 render the §12.5 wraparound control's fitted curve and
// probes on an unrolled 24-hour axis; F2 is deliberately a summary panel, because
// the result files carry the measured co-occurrence graph size but no node-level
// export, and a topology nobody measured must not be drawn.

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// Shared 24-hour axis helpers
// ---------------------------------------------------------------------------

// uniformDensity is the no-history reference level of §7.1: a circular density with
// no concentration at all is the constant 1/(2π).
const uniformDensity = 1 / (2 * math.Pi)

// clockLabel formats an hour-of-day value from the curve's own hour field as a
// clock time, e.g. 23.866… becomes "23:52".
func clockLabel(hour float64) string {
	minutes := int(math.Round(hour * 60))
	return fmt.Sprintf("%02d:%02d", minutes/60, minutes%60)
}

// closeCurve appends the wrap point: the curve is circular, so it ends at 24:00
// with the values it starts with at 00:00, drawn explicitly so the wrap reads as
// continuity rather than a seam.
func closeCurve(curve []curvePoint) []curvePoint {
	wrap := curve[0]
	wrap.Hour = 24
	return append(append(make([]curvePoint, 0, len(curve)+1), curve...), wrap)
}

// circularHourDistance is the wrapping distance between two hours of day.
func circularHourDistance(a, b float64) float64 {
	d := math.Abs(a - b)
	return math.Min(d, 24-d)
}

// nearestCurvePoint returns the grid point closest to the given clock hour on the
// circle; ties keep the earliest point, deterministically.
func nearestCurvePoint(curve []curvePoint, hour float64) curvePoint {
	best, bestDist := curve[0], circularHourDistance(curve[0].Hour, hour)
	for _, p := range curve[1:] {
		if d := circularHourDistance(p.Hour, hour); d < bestDist {
			best, bestDist = p, d
		}
	}
	return best
}

// drawHourAxis draws the baseline with hour ticks every two hours labelled as clock
// times, plus the axis title.
func drawHourAxis(c *canvas, sx scale, yBase float64) {
	c.Line(sx.at(0), yBase, sx.at(24), yBase, "var(--rule-2)", 1)
	for h := 0; h <= 24; h += 2 {
		px := sx.at(float64(h))
		c.Line(px, yBase, px, yBase+4, "var(--rule-2)", 1)
		c.Text(px, yBase+16, 11, "middle", "var(--ink-3)", fmt.Sprintf("%02d:00", h))
	}
	c.Text((sx.at(0)+sx.at(24))/2, yBase+38, 11, "middle", "var(--ink-2)", "hour of day")
}

// drawEvidenceTitle draws the larger title and subtitle of the hand-designed
// evidence figures (15 px and 12 px, against drawTitle's 14.5 and 11).
func drawEvidenceTitle(c *canvas, title, subtitle string) {
	c.BoldText(16, 26, 15, "start", "var(--ink)", title)
	if subtitle != "" {
		c.Text(16, 46, 12, "start", "var(--ink-3)", subtitle)
	}
}

// clampedAnchor keeps a label inside the plot: middle-anchored in the interior,
// start- or end-anchored near the left and right edges.
func clampedAnchor(x, loMid, hiMid float64) (float64, string) {
	if x < loMid {
		return x, "start"
	}
	if x > hiMid {
		return x, "end"
	}
	return x, "middle"
}

// labelAnchor is clampedAnchor nudged sideways so an edge-anchored label clears its
// own leader line or probe rule.
func labelAnchor(px float64) (float64, string) {
	x, anchor := clampedAnchor(px, 150, 790)
	switch anchor {
	case "start":
		return x + 4, anchor
	case "end":
		return x - 4, anchor
	}
	return x, anchor
}

// probeMark is one §12.5 probe: the clock time it is defined at, the arm's p-value
// there, and the colour that classifies it (habitual against anomalous).
type probeMark struct {
	Label  string
	Hour   float64
	P      float64
	Colour string
}

func circularProbeMarks(p probeTriple) []probeMark {
	return []probeMark{
		{"23:30", 23.5, p.P2330, "var(--good)"},
		{"00:30", 0.5, p.P0030, "var(--good)"},
		{"12:00", 12, p.P1200, "var(--crit)"},
	}
}

func formatP(p float64) string { return strconv.FormatFloat(p, 'f', 4, 64) }

// ---------------------------------------------------------------------------
// F1 — the circular density evidence view (§7.7)
// ---------------------------------------------------------------------------

// F1 geometry on a 900 × 460 viewBox: margins left 70, right 30, top 70, bottom 55.
const (
	f1PlotLeft  = 70.0
	f1PlotRight = 870.0
	f1PlotTop   = 70.0
	f1PlotBase  = 405.0
)

func figureF1(results []resultFile) (*builtFigure, bool) {
	r, ok := firstControlWithCurve(results)
	if !ok {
		return nil, false
	}
	curve := evidenceCurve(r)
	probes, okProbes := probeTripleOf(r, "circular")
	if len(curve) < 2 || !okProbes || maxDensity(curve) <= 0 {
		return nil, false
	}
	c := &canvas{}
	drawF1(c, curve, probes, paramsBlock(r))
	return &builtFigure{
		ID: "F1", FileName: "f1-circular-density.svg", RunID: runID(r),
		Canvas: c, Prov: []string{provLine(r)}, Width: 900, Height: 460,
	}, true
}

func maxDensity(curve []curvePoint) float64 {
	m := 0.0
	for _, p := range curve {
		m = math.Max(m, p.Density)
	}
	return m
}

func f1Scales(curve []curvePoint) (sx, sy scale) {
	sx = scale{0, 24, f1PlotLeft, f1PlotRight}
	sy = scale{0, maxDensity(curve) * 1.08, f1PlotBase, f1PlotTop}
	return sx, sy
}

func drawF1(c *canvas, curve []curvePoint, probes probeTriple, params map[string]any) {
	sx, sy := f1Scales(curve)
	ext := closeCurve(curve)
	drawEvidenceTitle(c,
		"Fitted circular activity density, with the scored event marked",
		f1Subtitle(params))
	drawF1LevelSet(c, curve, ext, sx, probes.P2330)
	drawF1Uniform(c, sx, sy)
	drawF1Density(c, ext, sx, sy)
	drawHourAxis(c, sx, f1PlotBase)
	c.Line(f1PlotLeft, f1PlotTop, f1PlotLeft, f1PlotBase, "var(--rule-2)", 1)
	c.VText(30, (f1PlotTop+f1PlotBase)/2, 12, "var(--ink-2)", "fitted density f̂(φ)")
	drawF1Modes(c, curve, sx, sy)
	drawF1Probes(c, curve, sx, sy, probes)
}

// f1Subtitle states only the parameters the result file carries; an absent
// parameter is omitted, never defaulted.
func f1Subtitle(params map[string]any) string {
	parts := []string{"von Mises kernel"}
	if v, ok := getFloat(params, "bandwidth_hours"); ok {
		parts = append(parts, fmt.Sprintf("bandwidth %.2f h", v))
	}
	if v, ok := getFloat(params, "kappa"); ok {
		parts = append(parts, fmt.Sprintf("κ = %.2f", v))
	}
	if v, ok := getFloat(params, "H"); ok {
		parts = append(parts, fmt.Sprintf("H = %.0f", v))
	}
	if v, ok := getFloat(params, "grid"); ok {
		parts = append(parts, fmt.Sprintf("grid G = %.0f", v))
	}
	return strings.Join(parts, " · ")
}

// drawF1LevelSet shades, behind the density, the §7.2 level set for the 23:30
// probe: the hours whose fitted density does not exceed the density at the probe.
// The P printed on the label is the probe's own value from the result file — the
// mass of times no more probable than the observed one.
func drawF1LevelSet(c *canvas, curve, ext []curvePoint, sx scale, p2330 float64) {
	thr := nearestCurvePoint(curve, 23.5).Density
	runs := levelSetRuns(ext, thr)
	for _, run := range runs {
		x0, x1 := sx.at(run.From), sx.at(run.To)
		c.Rect(x0, f1PlotTop, x1-x0, f1PlotBase-f1PlotTop, "var(--rule)", 0.35)
	}
	lx, anchor := levelSetLabelAnchor(runs, sx)
	c.Text(lx, 385, 12, anchor, "var(--ink-2)",
		"level set for the 23:30 probe: P = "+formatP(p2330))
}

// hourRun is a contiguous interval of the 24-hour axis.
type hourRun struct {
	From, To float64
}

// levelSetRuns merges the curve segments whose both endpoints sit at or below the
// threshold density into contiguous hour intervals.
func levelSetRuns(ext []curvePoint, thr float64) []hourRun {
	runs := []hourRun{}
	open := false
	start := 0.0
	for i := 0; i+1 < len(ext); i++ {
		in := ext[i].Density <= thr && ext[i+1].Density <= thr
		if in && !open {
			open, start = true, ext[i].Hour
		}
		if !in && open {
			open = false
			runs = append(runs, hourRun{start, ext[i].Hour})
		}
	}
	if open {
		runs = append(runs, hourRun{start, ext[len(ext)-1].Hour})
	}
	return runs
}

// levelSetLabelAnchor places the label just inside the widest shaded run, falling
// back to the plot centre when nothing is shaded.
func levelSetLabelAnchor(runs []hourRun, sx scale) (float64, string) {
	if len(runs) == 0 {
		return (sx.at(0) + sx.at(24)) / 2, "middle"
	}
	widest := runs[0]
	for _, r := range runs[1:] {
		if r.To-r.From > widest.To-widest.From {
			widest = r
		}
	}
	if x := sx.at(widest.From) + 10; x <= 560 { // keep the ~280 px label inside the right margin
		return x, "start"
	}
	return sx.at(widest.To) - 10, "end"
}

// drawF1Uniform draws the dashed reference line at the uniform density 1/(2π).
func drawF1Uniform(c *canvas, sx, sy scale) {
	y := sy.at(uniformDensity)
	c.DashedLine(sx.at(0), y, sx.at(24), y, "var(--rule-2)", 1.2)
	c.Text(sx.at(24)-6, y-6, 11, "end", "var(--ink-3)", "uniform (no history)")
}

// drawF1Density draws the fitted density as a filled area under a 2 px stroke; the
// polygon closes along the baseline so only the curve itself reads as a line.
func drawF1Density(c *canvas, ext []curvePoint, sx, sy scale) {
	top := make([]point, 0, len(ext))
	for _, p := range ext {
		top = append(top, point{sx.at(p.Hour), sy.at(p.Density)})
	}
	area := append(append(make([]point, 0, len(top)+2), top...),
		point{sx.at(24), f1PlotBase}, point{sx.at(0), f1PlotBase})
	c.Polygon(area, "var(--accent)", 0.18)
	c.Path(top, "var(--accent)", 2)
}

// drawF1Modes labels every strict local maximum of the fitted density (wrapping at
// midnight) that rises above the uniform level, as a clock time computed from the
// curve's own hour field.
func drawF1Modes(c *canvas, curve []curvePoint, sx, sy scale) {
	n := len(curve)
	for i, p := range curve {
		prev, next := curve[(i-1+n)%n].Density, curve[(i+1)%n].Density
		if p.Density <= prev || p.Density <= next || p.Density <= uniformDensity {
			continue
		}
		x, anchor := clampedAnchor(sx.at(p.Hour), 110, 830)
		c.Text(x, sy.at(p.Density)-12, 11, anchor, "var(--ink-2)", "mode "+clockLabel(p.Hour))
	}
}

// drawF1Probes marks the three probes as filled circles on the curve, with leader
// lines up to labels in the band above the plot.
func drawF1Probes(c *canvas, curve []curvePoint, sx, sy scale, probes probeTriple) {
	marks := circularProbeMarks(probes)
	labelYs := placeProbeLabels(curve, marks)
	for i, m := range marks {
		pt := nearestCurvePoint(curve, m.Hour)
		px, py := sx.at(pt.Hour), sy.at(pt.Density)
		if py-7 > labelYs[i]+4 {
			c.Line(px, labelYs[i]+4, px, py-7, "var(--ink-3)", 1)
		}
		c.Circle(px, py, 4.5, m.Colour)
		lx, anchor := labelAnchor(px)
		c.Text(lx, labelYs[i], 12, anchor, m.Colour, m.Label+" · P = "+formatP(m.P))
	}
}

// placeProbeLabels assigns each probe label a row above the plot: the base row, or
// a staggered lower row whenever the previous label along the axis sits within two
// hours of it.
func placeProbeLabels(curve []curvePoint, marks []probeMark) []float64 {
	const baseY, rowStep = 64.0, 20.0
	hours := make([]float64, len(marks))
	order := make([]int, len(marks))
	for i, m := range marks {
		hours[i] = nearestCurvePoint(curve, m.Hour).Hour
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return hours[order[a]] < hours[order[b]] })
	ys := make([]float64, len(marks))
	level := 0
	for k, idx := range order {
		if k == 0 || hours[idx]-hours[order[k-1]] >= 2 {
			level = 0
		} else {
			level = 1 - level
		}
		ys[idx] = baseY + float64(level)*rowStep
	}
	return ys
}

// ---------------------------------------------------------------------------
// F8 — the §12.5 wraparound control, rendered
// ---------------------------------------------------------------------------

// F8 geometry on a 900 × 400 viewBox; the p-axis is log10 over [1e-4, 1].
const (
	f8PlotTop  = 82.0
	f8PlotBase = 345.0
	f8LogFloor = -4.0
)

// f8Data is everything F8 draws, all read from one control result.
type f8Data struct {
	Curve      []curvePoint
	Circular   probeTriple
	Cells      probeTriple
	HasCells   bool
	Acceptance acceptanceBlock
}

func figureF8(results []resultFile) (*builtFigure, bool) {
	r, ok := firstControlWithCurve(results)
	if !ok {
		return nil, false
	}
	d := f8Data{Curve: evidenceCurve(r)}
	var okCircular, okAcceptance bool
	d.Circular, okCircular = probeTripleOf(r, "circular")
	d.Cells, d.HasCells = probeTripleOf(r, "cells_168")
	d.Acceptance, okAcceptance = acceptanceOf(r)
	if len(d.Curve) < 2 || !okCircular || !okAcceptance {
		return nil, false
	}
	c := &canvas{}
	drawF8(c, d)
	return &builtFigure{
		ID: "F8", FileName: "f8-wraparound-control.svg", RunID: runID(r),
		Canvas: c, Prov: []string{provLine(r)}, Width: 900, Height: 400,
	}, true
}

func drawF8(c *canvas, d f8Data) {
	sx := scale{0, 24, 70, 870}
	sy := scale{f8LogFloor, 0, f8PlotBase, f8PlotTop}
	drawEvidenceTitle(c, "The §12.5 wraparound control",
		"acceptance: "+d.Acceptance.Criteria)
	c.Text(16, 63, 11, "start", "var(--ink-3)", acceptanceOutcome(d.Acceptance))
	drawF8ActivityWindow(c, sx)
	drawF8LogAxes(c, sx, sy)
	drawF8Curves(c, d.Curve, sx, sy)
	drawF8Probes(c, d, sx)
	drawLegend(c, 120, 304, []legendEntry{
		{"circular p-value (§7.2)", "var(--accent)", "line", false},
		{"168-cell baseline overlaid failing it", "var(--ink-3)", "line", true},
	})
}

// acceptanceOutcome states the two acceptance booleans the control recorded.
func acceptanceOutcome(a acceptanceBlock) string {
	return fmt.Sprintf("circular arm passes: %t · 168-cell arm shows the defect: %t",
		a.CircularPasses, a.CellsShowDefect)
}

// drawF8ActivityWindow shades the entity's 23:00-01:00 activity window, which wraps
// midnight and is therefore two bands on the unrolled axis.
func drawF8ActivityWindow(c *canvas, sx scale) {
	for _, band := range [][2]float64{{23, 24}, {0, 1}} {
		x0, x1 := sx.at(band[0]), sx.at(band[1])
		c.Rect(x0, f8PlotTop, x1-x0, f8PlotBase-f8PlotTop, "var(--good)", 0.1)
	}
	c.Text(sx.at(24)-4, f8PlotTop-5, 11, "end", "var(--good)", "entity's only activity")
}

// drawF8LogAxes draws the frame, the decade gridlines of the log p-axis, and both
// axis titles.
func drawF8LogAxes(c *canvas, sx, sy scale) {
	c.Line(sx.at(0), f8PlotBase, sx.at(0), f8PlotTop, "var(--rule-2)", 1)
	for k := sy.lo; k <= sy.hi; k++ {
		py := sy.at(k)
		c.Line(sx.at(0), py, sx.at(24), py, "var(--rule)", 0.5)
		c.Text(sx.at(0)-6, py+3.5, 10, "end", "var(--ink-3)", decadeLabel(k))
	}
	drawHourAxis(c, sx, f8PlotBase)
	c.VText(24, (f8PlotTop+f8PlotBase)/2, 11, "var(--ink-2)", "p-value (log scale)")
}

// drawF8Curves draws the two arms' p-value curves, closed at 24:00 with their
// midnight values so the wrap reads as continuity.
func drawF8Curves(c *canvas, curve []curvePoint, sx, sy scale) {
	ext := closeCurve(curve)
	circular := make([]point, 0, len(ext))
	cells := make([]point, 0, len(ext))
	for _, p := range ext {
		x := sx.at(p.Hour)
		circular = append(circular, point{x, sy.at(logClamp(p.PCircular, f8LogFloor))})
		cells = append(cells, point{x, sy.at(logClamp(p.PCells, f8LogFloor))})
	}
	c.DashedPath(cells, "var(--ink-3)", 2)
	c.Path(circular, "var(--accent)", 2)
}

// drawF8Probes marks the three probe times with vertical dashed rules and prints
// each arm's p-value at the probe, from the probes block of the result file.
func drawF8Probes(c *canvas, d f8Data, sx scale) {
	cellPs := []float64{d.Cells.P2330, d.Cells.P0030, d.Cells.P1200}
	for i, m := range circularProbeMarks(d.Circular) {
		px := sx.at(m.Hour)
		c.DashedLine(px, f8PlotTop, px, f8PlotBase, "var(--ink-3)", 1)
		lx, anchor := labelAnchor(px)
		c.Text(lx, 100, 11, anchor, "var(--ink-2)", m.Label)
		c.Text(lx, 114, 10.5, anchor, "var(--accent)", "circular "+formatP(m.P))
		if d.HasCells {
			c.Text(lx, 128, 10.5, anchor, "var(--ink-3)", "cells "+formatP(cellPs[i]))
		}
	}
}

// ---------------------------------------------------------------------------
// F2 — the k-partite co-occurrence graph (§8): an honest summary panel
// ---------------------------------------------------------------------------

// figureF2 renders the measured co-occurrence graph size and the detector's p-value
// histogram. The result files carry no node-level graph export, so no topology is
// drawn: a layout would be an invention.
func figureF2(results []resultFile) (*builtFigure, bool) {
	for _, r := range allOfKind(results, "replay") {
		hist, okHist := cooccurrenceHistogram(r)
		size, okSize := graphSizeOf(r)
		if !okHist || !okSize {
			continue
		}
		c := &canvas{}
		drawF2(c, hist, size, r)
		return &builtFigure{
			ID: "F2", FileName: "f2-cooccurrence-summary.svg", RunID: runID(r),
			Canvas: c, Prov: []string{provLine(r)},
		}, true
	}
	return nil, false
}

func cooccurrenceHistogram(r resultFile) (detectorHistogram, bool) {
	for _, h := range detectorHistograms(r) {
		if h.Detector == "cooccurrence" && len(h.Counts) > 0 {
			return h, true
		}
	}
	return detectorHistogram{}, false
}

func drawF2(c *canvas, hist detectorHistogram, size graphSize, r resultFile) {
	drawTitle(c, "F2 · co-occurrence graph (§8.2)",
		"measured graph size and p-value histogram; no node-level export exists to draw")
	drawF2Stat(c, 30, "graph nodes", size.Nodes)
	drawF2Stat(c, 240, "graph edges", size.Edges)
	if p := getString(getMap(r.Data, "run"), "partition"); p != "" {
		c.Text(30, 140, 11, "start", "var(--ink-2)", "partition: "+p)
	}
	c.Text(30, 158, 10, "start", "var(--ink-3)", "source: "+r.Path+" · run "+runID(r))
	c.Text(30, 180, 10, "start", "var(--ink-3)",
		"node-level graph export is not carried in the result files; this panel reports the")
	c.Text(30, 193, 10, "start", "var(--ink-3)",
		"measured graph size rather than drawing an unmeasured topology.")
	drawF5Panel(c, hist, 8, 206, 704, 192)
	c.Text(360, 414, 10, "middle", "var(--ink-2)", "log10 p")
}

// drawF2Stat draws one measured count as a small stat tile.
func drawF2Stat(c *canvas, x float64, label string, v float64) {
	c.Text(x, 82, 10, "start", "var(--ink-3)", label)
	c.BoldText(x, 112, 24, "start", "var(--accent)", strconv.FormatFloat(v, 'f', 0, 64))
}
