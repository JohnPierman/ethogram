package main

// Figures F3-F6, each generated only when a result file carries the data it needs;
// the evidence figures F1, F2, and F8 live in figures_evidence.go under the same
// contract. A figure with no data is not drawn with placeholders — it is simply
// absent, listed under "figures not yet available", and the scoreboard's NOT RUN
// card covers the hypothesis. Every emitted figure is recorded in
// docs/figures/manifest.json against the run_id that produced it, which is what
// verifyProvenance checks.

import (
	"encoding/json"
	"fmt"
	"html/template"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// figureCard is a figure as embedded in report.html: the inline SVG plus the
// provenance footer naming every run that contributed a number to it.
type figureCard struct {
	ID   string
	SVG  template.HTML
	Prov []string
}

// builtFigure is one generated figure before writing: the canvas renders both the
// inline and the standalone form, and RunID is the manifest entry. Width and Height
// override the default figureWidth × figureHeight viewBox for the hand-designed
// evidence figures; zero means the default.
type builtFigure struct {
	ID       string
	FileName string
	RunID    string
	Canvas   *canvas
	Prov     []string
	Width    int
	Height   int
}

func (f *builtFigure) viewBoxSize() (w, h int) {
	if f.Width > 0 && f.Height > 0 {
		return f.Width, f.Height
	}
	return figureWidth, figureHeight
}

func svgOpenSized(w, h int) string {
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" width="%d" height="%d" role="img">`,
		w, h, w, h)
}

// inlineSVG renders the figure for report.html at the figure's own viewBox;
// default-sized figures keep the canvas's own rendering byte for byte.
func (f *builtFigure) inlineSVG() string {
	w, h := f.viewBoxSize()
	if w == figureWidth && h == figureHeight {
		return f.Canvas.Inline()
	}
	return svgOpenSized(w, h) + "\n" + strings.Join(f.Canvas.elems, "\n") + "\n</svg>"
}

// standaloneSVG renders the figure for docs/figures/ with the token style block
// prepended, at the figure's own viewBox.
func (f *builtFigure) standaloneSVG() string {
	w, h := f.viewBoxSize()
	if w == figureWidth && h == figureHeight {
		return f.Canvas.Standalone()
	}
	return svgOpenSized(w, h) + "\n" + figureStyle + "\n" + strings.Join(f.Canvas.elems, "\n") + "\n</svg>\n"
}

// pendingCatalogue names all eight figures of the evaluation. F1-F6 and F8 are
// produced by this renderer when their data exists; F7 is built elsewhere and counts
// as present when its file has been dropped into the figures directory.
var pendingCatalogue = []struct{ ID, Title, Glob string }{
	{"F1", "circular-density evidence", ""},
	{"F2", "co-occurrence graph", ""},
	{"F3", "calibration", ""},
	{"F4", "detection at matched budget", ""},
	{"F5", "per-detector p-value histograms", ""},
	{"F6", "ablation: circular vs 168-cell grid", ""},
	{"F7", "E5 degradation", "f7-*.svg"},
	{"F8", "wraparound render", ""},
}

// ownedFigureFile reports whether a figure file in the figures directory is one this
// renderer produces (and may therefore replace or remove).
func ownedFigureFile(name string) bool {
	for _, prefix := range []string{"f1-", "f2-", "f3-", "f4-", "f5-", "f6-", "f8-"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// buildFigures generates every figure whose data exists, writes the standalone SVGs
// and the manifest, and returns the inline cards plus the not-yet-available list.
func buildFigures(results []resultFile, figuresDir string) ([]figureCard, []string, error) {
	if err := os.MkdirAll(figuresDir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("create figures dir: %w", err)
	}
	built := generateFigures(results)
	if err := writeFigures(figuresDir, built); err != nil {
		return nil, nil, err
	}
	if err := writeManifest(figuresDir, built); err != nil {
		return nil, nil, err
	}
	cards := make([]figureCard, 0, len(built))
	generated := make(map[string]bool, len(built))
	for _, f := range built {
		generated[f.ID] = true
		cards = append(cards, figureCard{
			ID: f.ID, SVG: template.HTML(f.inlineSVG()), Prov: f.Prov, //nolint:gosec // markup this renderer built itself
		})
	}
	pending, err := pendingFigures(figuresDir, generated)
	if err != nil {
		return nil, nil, err
	}
	return cards, pending, nil
}

func generateFigures(results []resultFile) []*builtFigure {
	generators := []func([]resultFile) (*builtFigure, bool){
		figureF1, figureF2, figureF3, figureF4, figureF5, figureF6, figureF8,
	}
	out := []*builtFigure{}
	for _, generate := range generators {
		if f, ok := generate(results); ok {
			out = append(out, f)
		}
	}
	return out
}

// writeFigures writes the standalone SVGs and removes stale figures this renderer
// owns whose backing data has gone; figures built elsewhere are never touched.
func writeFigures(dir string, built []*builtFigure) error {
	fresh := make(map[string]bool, len(built))
	for _, f := range built {
		fresh[f.FileName] = true
	}
	existing, err := filepath.Glob(filepath.Join(dir, "*.svg"))
	if err != nil {
		return err
	}
	for _, p := range existing {
		name := filepath.Base(p)
		if ownedFigureFile(name) && !fresh[name] {
			if err := os.Remove(p); err != nil {
				return fmt.Errorf("remove stale figure %s: %w", name, err)
			}
		}
	}
	for _, f := range built {
		path := filepath.Join(dir, f.FileName)
		if err := os.WriteFile(path, []byte(f.standaloneSVG()), 0o644); err != nil { //nolint:gosec // world-readable by intent
			return fmt.Errorf("write %s: %w", path, err)
		}
	}
	return nil
}

// writeManifest maps every emitted figure to the run that produced it. Entries this
// renderer owns are replaced wholesale; entries written by the other figure builders
// (F7) are preserved, extending the manifest verifyProvenance reads.
func writeManifest(dir string, built []*builtFigure) error {
	path := filepath.Join(dir, "manifest.json")
	manifest := map[string]string{}
	if raw, err := os.ReadFile(path); err == nil { //nolint:gosec // the manifest this renderer maintains
		if err := json.Unmarshal(raw, &manifest); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
	}
	for name := range manifest {
		if ownedFigureFile(name) {
			delete(manifest, name)
		}
	}
	for _, f := range built {
		manifest[f.FileName] = f.RunID
	}
	raw, err := json.MarshalIndent(manifest, "", " ") // Marshal sorts keys: deterministic
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o644) //nolint:gosec // world-readable by intent
}

func pendingFigures(dir string, generated map[string]bool) ([]string, error) {
	pending := []string{}
	for _, f := range pendingCatalogue {
		if f.Glob == "" {
			if !generated[f.ID] {
				pending = append(pending, f.ID+" "+f.Title)
			}
			continue
		}
		matches, err := filepath.Glob(filepath.Join(dir, f.Glob))
		if err != nil {
			return nil, err
		}
		if len(matches) == 0 {
			pending = append(pending, f.ID+" "+f.Title+" (built elsewhere)")
		}
	}
	return pending, nil
}

// ---------------------------------------------------------------------------
// Shared plotting helpers
// ---------------------------------------------------------------------------

// scale maps a data value to a pixel coordinate linearly; log axes pass log10 values.
type scale struct{ lo, hi, p0, p1 float64 }

func (s scale) at(v float64) float64 {
	if s.hi == s.lo {
		return (s.p0 + s.p1) / 2
	}
	return s.p0 + (v-s.lo)/(s.hi-s.lo)*(s.p1-s.p0)
}

// logClamp returns log10(v) bounded below by the axis floor; non-positive values,
// which a log axis cannot place, are drawn at the floor.
func logClamp(v, floor float64) float64 {
	if v <= 0 {
		return floor
	}
	if l := math.Log10(v); l > floor {
		return l
	}
	return floor
}

// niceStep picks a 1/2/5-series tick step no smaller than 1.
func niceStep(raw float64) float64 {
	if raw <= 1 {
		return 1
	}
	mag := math.Pow(10, math.Floor(math.Log10(raw)))
	for _, m := range []float64{1, 2, 5} {
		if raw <= m*mag {
			return m * mag
		}
	}
	return 10 * mag
}

func decadeLabel(k float64) string {
	return strconv.FormatFloat(math.Pow(10, k), 'g', -1, 64)
}

func drawTitle(c *canvas, title, subtitle string) {
	c.BoldText(16, 24, 14.5, "start", "var(--ink)", title)
	if subtitle != "" {
		c.Text(16, 42, 11, "start", "var(--ink-3)", subtitle)
	}
}

type legendEntry struct {
	Label  string
	Colour string
	Swatch string // "line" or "rect"
	Dashed bool
}

func drawLegend(c *canvas, x, y float64, entries []legendEntry) {
	for i, e := range entries {
		ry := y + float64(i)*15
		if e.Swatch == "rect" {
			c.Rect(x, ry-7, 16, 9, e.Colour, 0.85)
		} else {
			if e.Dashed {
				c.DashedLine(x, ry-3, x+16, ry-3, e.Colour, 1.6)
			} else {
				c.Line(x, ry-3, x+16, ry-3, e.Colour, 1.6)
			}
			c.Circle(x+8, ry-3, 2.5, e.Colour)
		}
		c.Text(x+22, ry, 10, "start", "var(--ink)", e.Label)
	}
}

// drawLogAxes draws the frame, decade gridlines, tick labels, and axis titles for a
// log-log plot whose scales already hold log10 domains with integer endpoints.
func drawLogAxes(c *canvas, sx, sy scale, xTitle, yTitle string) {
	x0, x1 := sx.at(sx.lo), sx.at(sx.hi)
	yB, yT := sy.at(sy.lo), sy.at(sy.hi)
	c.Line(x0, yB, x1, yB, "var(--rule-2)", 1)
	c.Line(x0, yB, x0, yT, "var(--rule-2)", 1)
	for k := sx.lo; k <= sx.hi; k++ {
		px := sx.at(k)
		c.Line(px, yT, px, yB, "var(--rule)", 0.5)
		c.Text(px, yB+16, 10, "middle", "var(--ink-3)", decadeLabel(k))
	}
	for k := sy.lo; k <= sy.hi; k++ {
		py := sy.at(k)
		c.Line(x0, py, x1, py, "var(--rule)", 0.5)
		c.Text(x0-6, py+3.5, 10, "end", "var(--ink-3)", decadeLabel(k))
	}
	c.Text((x0+x1)/2, yB+38, 11, "middle", "var(--ink-2)", xTitle)
	c.VText(22, (yB+yT)/2, 11, "var(--ink-2)", yTitle)
}

// ---------------------------------------------------------------------------
// F3 — calibration: realised FDR against nominal q
// ---------------------------------------------------------------------------

func figureF3(results []resultFile) (*builtFigure, bool) {
	r, ok := firstAnalysisWithCalibration(results)
	if !ok {
		return nil, false
	}
	bh := calibrationSeries(r, "calibration_bh")
	if len(bh) == 0 {
		return nil, false
	}
	by := calibrationSeries(r, "calibration_by")
	c := &canvas{}
	drawF3(c, bh, by)
	return &builtFigure{
		ID: "F3", FileName: "f3-calibration.svg", RunID: runID(r),
		Canvas: c, Prov: []string{provLine(r)},
	}, true
}

func drawF3(c *canvas, bh, by []calibrationPoint) {
	xLo, xHi, yLo, yHi := f3Domain(bh, by)
	sx := scale{xLo, xHi, 74, 694}
	sy := scale{yLo, yHi, 336, 64}
	drawTitle(c, "F3 · calibration: realised FDR against nominal q (log-log)",
		"conservatism (below the diagonal) is the predicted direction (§10.2)")
	drawLogAxes(c, sx, sy, "nominal q", "realised FDR")
	if dLo, dHi := math.Max(xLo, yLo), math.Min(xHi, yHi); dLo < dHi {
		c.DashedLine(sx.at(dLo), sy.at(dLo), sx.at(dHi), sy.at(dHi), "var(--rule-2)", 1.2)
		c.Text(sx.at(dHi)-10, sy.at(dHi)-8, 10, "end", "var(--ink-3)", "y = x")
	}
	if band := wilsonBand(bh, sx, sy, yLo); len(band) >= 3 {
		c.Polygon(band, "var(--accent)", 0.15)
	}
	drawCalibrationSeries(c, bh, sx, sy, yLo, "var(--accent)", 3)
	annotateDiscoveries(c, bh, sx, sy, yLo)
	drawCalibrationSeries(c, by, sx, sy, yLo, "var(--accent-2)", 2.5)
	drawLegend(c, 86, 80, f3Legend(bh, by))
}

func f3Legend(bh, by []calibrationPoint) []legendEntry {
	entries := []legendEntry{}
	if len(bh) > 0 {
		entries = append(entries, legendEntry{f3ProcedureLabel(bh, "BH"), "var(--accent)", "line", false})
	}
	if len(by) > 0 {
		entries = append(entries, legendEntry{f3ProcedureLabel(by, "BY"), "var(--accent-2)", "line", false})
	}
	return entries
}

func f3ProcedureLabel(pts []calibrationPoint, fallback string) string {
	if pts[0].Procedure != "" {
		return pts[0].Procedure
	}
	return fallback
}

// f3Domain finds integer log10 decade bounds covering every nominal q and every
// positive realised FDR and Wilson bound, widened so the y=x diagonal is drawable.
// A realised FDR of exactly zero cannot sit on a log axis; the domain gains one
// decade below the smallest positive value and zeros are clamped to that floor.
func f3Domain(bh, by []calibrationPoint) (xLo, xHi, yLo, yHi float64) {
	minX, maxX := math.Inf(1), math.Inf(-1)
	minY, maxY := math.Inf(1), math.Inf(-1)
	hasZero := false
	for _, series := range [][]calibrationPoint{bh, by} {
		for _, p := range series {
			minX = math.Min(minX, p.NominalQ)
			maxX = math.Max(maxX, p.NominalQ)
			for _, v := range []float64{p.RealisedFDR, p.WilsonLow, p.WilsonHigh} {
				if v > 0 {
					minY = math.Min(minY, v)
					maxY = math.Max(maxY, v)
				} else {
					hasZero = true
				}
			}
		}
	}
	xLo, xHi = math.Floor(math.Log10(minX)), math.Ceil(math.Log10(maxX))
	if xHi == xLo {
		xHi = xLo + 1
	}
	if math.IsInf(minY, 1) { // every y value is zero: fall back to the x domain
		minY, maxY = math.Pow(10, xLo), math.Pow(10, xHi)
	}
	yLo = math.Min(math.Floor(math.Log10(minY)), xLo)
	yHi = math.Max(math.Ceil(math.Log10(maxY)), xHi)
	if hasZero {
		yLo--
	}
	if yHi == yLo {
		yHi = yLo + 1
	}
	return xLo, xHi, yLo, yHi
}

// wilsonBand builds the filled confidence polygon: the upper bounds left to right,
// then the lower bounds right to left.
func wilsonBand(pts []calibrationPoint, sx, sy scale, floor float64) []point {
	band := make([]point, 0, 2*len(pts))
	for _, p := range pts {
		band = append(band, point{sx.at(math.Log10(p.NominalQ)), sy.at(logClamp(p.WilsonHigh, floor))})
	}
	for i := len(pts) - 1; i >= 0; i-- {
		p := pts[i]
		band = append(band, point{sx.at(math.Log10(p.NominalQ)), sy.at(logClamp(p.WilsonLow, floor))})
	}
	return band
}

func drawCalibrationSeries(c *canvas, pts []calibrationPoint, sx, sy scale, floor float64, colour string, radius float64) {
	line := make([]point, 0, len(pts))
	for _, p := range pts {
		line = append(line, point{sx.at(math.Log10(p.NominalQ)), sy.at(logClamp(p.RealisedFDR, floor))})
	}
	if len(line) > 1 {
		c.Path(line, colour, 1.6)
	}
	for _, pt := range line {
		c.Circle(pt.X, pt.Y, radius, colour)
	}
}

func annotateDiscoveries(c *canvas, pts []calibrationPoint, sx, sy scale, floor float64) {
	for _, p := range pts {
		x := sx.at(math.Log10(p.NominalQ))
		y := sy.at(logClamp(p.RealisedFDR, floor))
		c.Text(x, y-9, 9, "middle", "var(--ink-3)", fmt.Sprintf("n=%.0f", p.Discoveries))
	}
}

// ---------------------------------------------------------------------------
// F4 — detection at matched alert budget
// ---------------------------------------------------------------------------

type f4Series struct {
	Name   string
	Colour string
	Dashed bool
	Points []budgetDetection
}

func figureF4(results []resultFile) (*builtFigure, bool) {
	series := []f4Series{}
	prov := []string{}
	primaryRun := ""
	for i, r := range allOfKind(results, "replay") {
		pts := budgetDetections(resultsBlock(r))
		if len(pts) == 0 {
			continue
		}
		series = append(series, f4Series{frameworkArmName(r), "var(--accent)", i > 0, pts})
		prov = append(prov, provLine(r))
		if primaryRun == "" {
			primaryRun = runID(r)
		}
	}
	if len(series) == 0 { // the figure is anchored on replay output
		return nil, false
	}
	series, prov = appendBaselineSeries(results, series, prov)
	c := &canvas{}
	drawF4(c, series)
	return &builtFigure{
		ID: "F4", FileName: "f4-detection-at-budget.svg", RunID: primaryRun,
		Canvas: c, Prov: prov,
	}, true
}

func appendBaselineSeries(results []resultFile, series []f4Series, prov []string) ([]f4Series, []string) {
	baselines := allOfKind(results, "baselines")
	if len(baselines) == 0 {
		return series, prov
	}
	r := baselines[0]
	colours := []string{"var(--ink-3)", "var(--accent-2)", "var(--crit)", "var(--good)"}
	added := false
	for i, model := range baselineModels {
		pts := budgetDetections(getMap(resultsBlock(r), model))
		if len(pts) == 0 {
			continue
		}
		series = append(series, f4Series{model, colours[i%len(colours)], false, pts})
		added = true
	}
	if added {
		prov = append(prov, provLine(r))
	}
	return series, prov
}

func drawF4(c *canvas, series []f4Series) {
	budgets := budgetUnion(series)
	maxTP, totals := f4Extent(series)
	sy := scale{0, math.Max(maxTP*1.15, 1), 336, 64}
	drawTitle(c, "F4 · detection at matched alert budget", "")
	positions := drawCategoricalXAxis(c, budgets, sy, "alert budget per day")
	drawLinearYAxis(c, sy, 74, 694, f4YTitle(totals))
	for _, s := range series {
		drawF4Line(c, s, positions, sy)
	}
	drawLegend(c, 86, 80, f4Legend(series, totals))
}

func f4Extent(series []f4Series) (maxTP float64, totals []float64) {
	for _, s := range series {
		for _, p := range s.Points {
			maxTP = math.Max(maxTP, p.TruePositives)
			if !containsFloat(totals, p.RedTeamTotal) {
				totals = append(totals, p.RedTeamTotal)
			}
		}
	}
	return maxTP, totals
}

func containsFloat(xs []float64, v float64) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func f4YTitle(totals []float64) string {
	if len(totals) == 1 {
		return fmt.Sprintf("red-team events detected (of %.0f scored)", totals[0])
	}
	return "red-team events detected (of N scored; N per series in the legend)"
}

func f4Legend(series []f4Series, totals []float64) []legendEntry {
	entries := make([]legendEntry, 0, len(series))
	for _, s := range series {
		label := s.Name
		if len(totals) > 1 && len(s.Points) > 0 {
			label += fmt.Sprintf(" (of %.0f)", s.Points[0].RedTeamTotal)
		}
		entries = append(entries, legendEntry{label, s.Colour, "line", s.Dashed})
	}
	return entries
}

func budgetUnion(series []f4Series) []int {
	seen := map[int]bool{}
	out := []int{}
	for _, s := range series {
		for _, p := range s.Points {
			if !seen[p.Budget] {
				seen[p.Budget] = true
				out = append(out, p.Budget)
			}
		}
	}
	sort.Ints(out)
	return out
}

// drawCategoricalXAxis draws the x axis with one evenly spaced position per budget
// and returns the pixel position of each budget.
func drawCategoricalXAxis(c *canvas, budgets []int, sy scale, title string) map[int]float64 {
	yB := sy.at(sy.lo)
	c.Line(74, yB, 694, yB, "var(--rule-2)", 1)
	positions := make(map[int]float64, len(budgets))
	for i, b := range budgets {
		px := 74 + (float64(i)+0.5)/float64(len(budgets))*620
		positions[b] = px
		c.Line(px, yB, px, yB+4, "var(--rule-2)", 1)
		c.Text(px, yB+18, 10, "middle", "var(--ink-3)", strconv.Itoa(b))
	}
	c.Text(384, yB+38, 11, "middle", "var(--ink-2)", title)
	return positions
}

func drawLinearYAxis(c *canvas, sy scale, x0, x1 float64, title string) {
	c.Line(x0, sy.at(sy.lo), x0, sy.at(sy.hi), "var(--rule-2)", 1)
	step := niceStep((sy.hi - sy.lo) / 4)
	for v := sy.lo; v <= sy.hi; v += step {
		py := sy.at(v)
		c.Line(x0, py, x1, py, "var(--rule)", 0.5)
		c.Text(x0-6, py+3.5, 10, "end", "var(--ink-3)", strconv.FormatFloat(v, 'f', 0, 64))
	}
	c.VText(22, (sy.at(sy.lo)+sy.at(sy.hi))/2, 11, "var(--ink-2)", title)
}

func drawF4Line(c *canvas, s f4Series, positions map[int]float64, sy scale) {
	line := make([]point, 0, len(s.Points))
	for _, p := range s.Points {
		line = append(line, point{positions[p.Budget], sy.at(p.TruePositives)})
	}
	if len(line) > 1 {
		if s.Dashed {
			c.DashedPath(line, s.Colour, 1.8)
		} else {
			c.Path(line, s.Colour, 1.8)
		}
	}
	for _, pt := range line {
		c.Circle(pt.X, pt.Y, 3.5, s.Colour)
	}
}

// ---------------------------------------------------------------------------
// F5 — per-detector p-value histograms
// ---------------------------------------------------------------------------

func figureF5(results []resultFile) (*builtFigure, bool) {
	r, ok := firstReplayWith(results, "p_histograms")
	if !ok {
		return nil, false
	}
	hists := detectorHistograms(r)
	if len(hists) == 0 {
		return nil, false
	}
	c := &canvas{}
	drawF5(c, hists)
	return &builtFigure{
		ID: "F5", FileName: "f5-p-histograms.svg", RunID: runID(r),
		Canvas: c, Prov: []string{provLine(r)},
	}, true
}

func drawF5(c *canvas, hists []detectorHistogram) {
	drawTitle(c, "F5 · per-detector p-value histograms",
		"y: log10(count+1); dashed: the per-bin count a uniform p-distribution would give")
	cols := 3
	if len(hists) < cols {
		cols = len(hists)
	}
	rows := (len(hists) + cols - 1) / cols
	const areaTop, areaBottom = 52.0, 398.0
	panelW := float64(figureWidth) / float64(cols)
	panelH := (areaBottom - areaTop) / float64(rows)
	for i, h := range hists {
		px := float64(i%cols) * panelW
		py := areaTop + float64(i/cols)*panelH
		drawF5Panel(c, h, px, py, panelW, panelH)
	}
	c.Text(360, 416, 10, "middle", "var(--ink-2)", "log10 p")
}

// uniformBinCounts returns, for each log-spaced bin over [1e-12, 1], the count a
// uniform p-distribution would place there given the observed total mass: bin i
// covers [10^(-12+i/20), 10^(-12+(i+1)/20)), so its share is proportional to its
// width in p-space, count_i = total * (upper-lower) / (1 - 1e-12).
func uniformBinCounts(total float64, n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		lower := math.Pow(10, -12+float64(i)/20)
		upper := math.Pow(10, -12+float64(i+1)/20)
		out[i] = total * (upper - lower) / (1 - 1e-12)
	}
	return out
}

func drawF5Panel(c *canvas, h detectorHistogram, px, py, w, ht float64) {
	x0, x1 := px+36, px+w-8
	yTop, yBase := py+22, py+ht-18
	total := h.total()
	uniform := uniformBinCounts(total, len(h.Counts))
	sy := scale{0, f5PanelMax(h.Counts, uniform), yBase, yTop}
	title := fmt.Sprintf("%s · n=%.0f", h.Detector, total+h.Under)
	if h.Under > 0 {
		title += fmt.Sprintf(" · under 1e-12: %.0f", h.Under)
	}
	c.Text(px+36, py+14, 10.5, "start", "var(--ink-2)", title)
	drawF5PanelAxes(c, sy, x0, x1, yBase, yTop)
	drawF5PanelBars(c, h.Counts, sy, x0, x1, yBase)
	drawF5PanelReference(c, uniform, sy, x0, x1)
}

func f5PanelMax(counts, uniform []float64) float64 {
	maxLog := 1.0
	for _, v := range counts {
		maxLog = math.Max(maxLog, math.Log10(v+1))
	}
	for _, v := range uniform {
		maxLog = math.Max(maxLog, math.Log10(v+1))
	}
	return maxLog * 1.06
}

func drawF5PanelAxes(c *canvas, sy scale, x0, x1, yBase, yTop float64) {
	c.Line(x0, yBase, x1, yBase, "var(--rule-2)", 1)
	c.Line(x0, yBase, x0, yTop, "var(--rule-2)", 1)
	sx := scale{-12, 0, x0, x1}
	for _, k := range []float64{-12, -9, -6, -3, 0} {
		tx := sx.at(k)
		c.Line(tx, yBase, tx, yBase+3, "var(--rule-2)", 1)
		c.Text(tx, yBase+13, 8.5, "middle", "var(--ink-3)", strconv.FormatFloat(k, 'f', 0, 64))
	}
	for k := 0.0; k <= sy.hi; k += 2 {
		ty := sy.at(k)
		c.Line(x0-3, ty, x0, ty, "var(--rule-2)", 1)
		c.Text(x0-5, ty+3, 8.5, "end", "var(--ink-3)", strconv.FormatFloat(k, 'f', 0, 64))
	}
}

func drawF5PanelBars(c *canvas, counts []float64, sy scale, x0, x1, yBase float64) {
	bw := (x1 - x0) / float64(len(counts))
	for i, count := range counts {
		if count <= 0 {
			continue
		}
		top := sy.at(math.Log10(count + 1))
		c.Rect(x0+float64(i)*bw, top, bw, yBase-top, "var(--accent)", 0.5)
	}
}

// drawF5PanelReference draws the uniform reference as a horizontal dashed segment
// per bin at that bin's uniform count, stepped across the panel.
func drawF5PanelReference(c *canvas, uniform []float64, sy scale, x0, x1 float64) {
	bw := (x1 - x0) / float64(len(uniform))
	steps := make([]point, 0, 2*len(uniform))
	for i, u := range uniform {
		yv := sy.at(math.Log10(u + 1))
		steps = append(steps,
			point{x0 + float64(i)*bw, yv}, point{x0 + float64(i+1)*bw, yv})
	}
	c.DashedPath(steps, "var(--ink-3)", 1)
}

// ---------------------------------------------------------------------------
// F6 — ablation: circular representation against the 168-cell grid (E9)
// ---------------------------------------------------------------------------

func figureF6(results []resultFile) (*builtFigure, bool) {
	r, ok := firstReplayWith(results, "e9_cell_arm")
	if !ok {
		return nil, false
	}
	e9 := getMap(resultsBlock(r), "e9_cell_arm")
	circular := budgetDetections(resultsBlock(r))
	cell := budgetDetections(e9)
	if len(circular) == 0 || len(cell) == 0 {
		return nil, false
	}
	c := &canvas{}
	drawF6(c, circular, cell, straddlerSubtitle(e9))
	return &builtFigure{
		ID: "F6", FileName: "f6-ablation-cell-grid.svg", RunID: runID(r),
		Canvas: c, Prov: []string{provLine(r)},
	}, true
}

func straddlerSubtitle(e9 map[string]any) string {
	srt := getMap(e9, "straddler_red_team")
	if srt == nil {
		return ""
	}
	part := func(key string) string {
		m := getMap(srt, key)
		if m == nil {
			return ""
		}
		n, _ := getFloat(m, "n")
		below, _ := getFloat(m, "p_below_0.01")
		return fmt.Sprintf("%.0f/%.0f", below, n)
	}
	cs, cn := part("circular_arm_straddlers"), part("circular_arm_nonstraddlers")
	ls, ln := part("cell_arm_straddlers"), part("cell_arm_nonstraddlers")
	if cs == "" {
		return ""
	}
	return fmt.Sprintf("red-team p≤0.01 — straddlers: circular %s vs cell %s · non-straddlers: circular %s vs cell %s",
		cs, ls, cn, ln)
}

func drawF6(c *canvas, circular, cell []budgetDetection, subtitle string) {
	drawTitle(c, "F6 · ablation: circular representation vs 168-cell grid (E9)", subtitle)
	budgets := budgetUnion([]f4Series{{Points: circular}, {Points: cell}})
	maxTP := 1.0
	for _, p := range append(append([]budgetDetection{}, circular...), cell...) {
		maxTP = math.Max(maxTP, p.TruePositives)
	}
	sy := scale{0, maxTP * 1.2, 336, 72}
	drawLinearYAxis(c, sy, 74, 694, "red-team true positives")
	drawF6Groups(c, budgets, circular, cell, sy)
	drawLegend(c, 86, 88, []legendEntry{
		{"circular arm (§7.2)", "var(--accent)", "rect", false},
		{"168-cell grid arm", "var(--ink-3)", "rect", false},
	})
}

func drawF6Groups(c *canvas, budgets []int, circular, cell []budgetDetection, sy scale) {
	yB := sy.at(sy.lo)
	c.Line(74, yB, 694, yB, "var(--rule-2)", 1)
	byBudget := func(pts []budgetDetection) map[int]float64 {
		m := make(map[int]float64, len(pts))
		for _, p := range pts {
			m[p.Budget] = p.TruePositives
		}
		return m
	}
	circM, cellM := byBudget(circular), byBudget(cell)
	gw := 620.0 / float64(len(budgets))
	bw := math.Min(44, gw/3)
	for i, b := range budgets {
		centre := 74 + (float64(i)+0.5)*gw
		if tp, ok := circM[b]; ok {
			drawF6Bar(c, centre-bw-2, tp, bw, sy, yB, "var(--accent)")
		}
		if tp, ok := cellM[b]; ok {
			drawF6Bar(c, centre+2, tp, bw, sy, yB, "var(--ink-3)")
		}
		c.Text(centre, yB+18, 10, "middle", "var(--ink-3)", strconv.Itoa(b))
	}
	c.Text(384, yB+38, 11, "middle", "var(--ink-2)", "alert budget per day")
}

func drawF6Bar(c *canvas, x, tp, bw float64, sy scale, yB float64, colour string) {
	top := sy.at(tp)
	c.Rect(x, top, bw, yB-top, colour, 0.85)
	c.Text(x+bw/2, top-5, 9, "middle", "var(--ink-2)", strconv.FormatFloat(tp, 'f', 0, 64))
}
