package main

// Hand-authored SVG, no dependencies. Every colour is a CSS variable so an inline
// figure inherits the page palette in light and dark; the standalone copy of the same
// markup carries a prepended <style> block defining those tokens itself. Output is
// deterministic: callers iterate slices, never maps, and every numeric attribute is
// formatted with strconv.FormatFloat(f, 'g', 9, 64).

import (
	"fmt"
	"strconv"
	"strings"
)

// figureWidth and figureHeight are the viewBox of every figure. Cards scale figures
// down responsively, so these are drawing coordinates, not display pixels.
const (
	figureWidth  = 720
	figureHeight = 420
)

// fnum formats a numeric SVG attribute deterministically (R4 applies to the renderer
// too: identical results must yield byte-identical figures).
func fnum(f float64) string { return strconv.FormatFloat(f, 'g', 9, 64) }

var xmlEscaper = strings.NewReplacer(
	"&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")

type point struct{ X, Y float64 }

// canvas accumulates the elements of one figure in emission order.
type canvas struct {
	elems []string
}

func (c *canvas) add(el string) { c.elems = append(c.elems, el) }

// Line draws a solid stroke between two points.
func (c *canvas) Line(x1, y1, x2, y2 float64, stroke string, width float64) {
	c.add(fmt.Sprintf(`<line x1="%s" y1="%s" x2="%s" y2="%s" stroke="%s" stroke-width="%s"/>`,
		fnum(x1), fnum(y1), fnum(x2), fnum(y2), stroke, fnum(width)))
}

// DashedLine draws a dashed stroke between two points.
func (c *canvas) DashedLine(x1, y1, x2, y2 float64, stroke string, width float64) {
	c.add(fmt.Sprintf(`<line x1="%s" y1="%s" x2="%s" y2="%s" stroke="%s" stroke-width="%s" stroke-dasharray="5 4"/>`,
		fnum(x1), fnum(y1), fnum(x2), fnum(y2), stroke, fnum(width)))
}

func pathData(pts []point) string {
	var b strings.Builder
	for i, p := range pts {
		if i == 0 {
			b.WriteString("M")
		} else {
			b.WriteString("L")
		}
		b.WriteString(fnum(p.X))
		b.WriteString(" ")
		b.WriteString(fnum(p.Y))
	}
	return b.String()
}

// Path draws a solid open polyline through the points.
func (c *canvas) Path(pts []point, stroke string, width float64) {
	c.add(fmt.Sprintf(`<path d="%s" fill="none" stroke="%s" stroke-width="%s"/>`,
		pathData(pts), stroke, fnum(width)))
}

// DashedPath draws a dashed open polyline through the points.
func (c *canvas) DashedPath(pts []point, stroke string, width float64) {
	c.add(fmt.Sprintf(`<path d="%s" fill="none" stroke="%s" stroke-width="%s" stroke-dasharray="4 3"/>`,
		pathData(pts), stroke, fnum(width)))
}

// Polygon draws a closed filled region, used for confidence bands.
func (c *canvas) Polygon(pts []point, fill string, opacity float64) {
	c.add(fmt.Sprintf(`<path d="%sZ" fill="%s" fill-opacity="%s" stroke="none"/>`,
		pathData(pts), fill, fnum(opacity)))
}

// Rect draws a filled rectangle, used for bars and legend swatches.
func (c *canvas) Rect(x, y, w, h float64, fill string, opacity float64) {
	c.add(fmt.Sprintf(`<rect x="%s" y="%s" width="%s" height="%s" fill="%s" fill-opacity="%s"/>`,
		fnum(x), fnum(y), fnum(w), fnum(h), fill, fnum(opacity)))
}

// Circle draws a filled marker.
func (c *canvas) Circle(cx, cy, r float64, fill string) {
	c.add(fmt.Sprintf(`<circle cx="%s" cy="%s" r="%s" fill="%s"/>`,
		fnum(cx), fnum(cy), fnum(r), fill))
}

// Text draws a text element; anchor is start, middle, or end. Font families are the
// page's own: inline figures inherit them from the body, standalone figures set them
// in the prepended style block.
func (c *canvas) Text(x, y, size float64, anchor, fill, s string) {
	c.add(fmt.Sprintf(`<text x="%s" y="%s" text-anchor="%s" font-size="%s" fill="%s">%s</text>`,
		fnum(x), fnum(y), anchor, fnum(size), fill, xmlEscaper.Replace(s)))
}

// BoldText draws emphasised text, used for figure titles.
func (c *canvas) BoldText(x, y, size float64, anchor, fill, s string) {
	c.add(fmt.Sprintf(`<text x="%s" y="%s" text-anchor="%s" font-size="%s" font-weight="600" fill="%s">%s</text>`,
		fnum(x), fnum(y), anchor, fnum(size), fill, xmlEscaper.Replace(s)))
}

// VText draws text rotated 90 degrees anticlockwise about its own anchor point,
// used for y-axis titles.
func (c *canvas) VText(x, y, size float64, fill, s string) {
	c.add(fmt.Sprintf(`<text x="%s" y="%s" transform="rotate(-90 %s %s)" text-anchor="middle" font-size="%s" fill="%s">%s</text>`,
		fnum(x), fnum(y), fnum(x), fnum(y), fnum(size), fill, xmlEscaper.Replace(s)))
}

func svgOpen() string {
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" width="%d" height="%d" role="img">`,
		figureWidth, figureHeight, figureWidth, figureHeight)
}

// Inline renders the figure for embedding in report.html, where the page's own
// tokens and font stack apply.
func (c *canvas) Inline() string {
	return svgOpen() + "\n" + strings.Join(c.elems, "\n") + "\n</svg>"
}

// figureStyle defines, inside a standalone SVG, the same tokens the report template
// defines for the page: the light palette on :root and the dark palette behind
// prefers-color-scheme. Both palettes are copies of the template's.
const figureStyle = `<style>
:root{
  --ink:#17191B; --ink-2:#454B50; --ink-3:#6B7379;
  --accent:#1C4657; --accent-2:#7E5209;
  --rule:#D9DEE0; --rule-2:#B3BBBF;
  --panel:#F1F4F5; --panel-2:#FBF7EF;
  --crit:#862B36; --good:#1F5537; --page:#FFFFFF;
}
@media (prefers-color-scheme: dark){
  :root{
    --ink:#E8EAEB; --ink-2:#B8BEC2; --ink-3:#8C9499;
    --accent:#7FB3C8; --accent-2:#D9A94E;
    --rule:#3A4147; --rule-2:#525A61;
    --panel:#22272B; --panel-2:#2A2622;
    --crit:#D98996; --good:#7FBF98; --page:#17191B;
  }
}
svg{background:var(--page);font-family:Georgia,"Sitka Text",Cambria,serif}
</style>`

// Standalone renders the same markup as Inline with the token style block prepended,
// so the file works on its own in docs/figures/ in both light and dark.
func (c *canvas) Standalone() string {
	return svgOpen() + "\n" + figureStyle + "\n" + strings.Join(c.elems, "\n") + "\n</svg>\n"
}
