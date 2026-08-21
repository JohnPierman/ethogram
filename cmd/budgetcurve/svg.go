package main

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// Geometry and encoding of section 1.1's figure.
//
// # Why the axes are what they are
//
// The figure this replaces plotted the share of alerts that are real against the false-alarm
// rate. On this corpus those two are not independent. Every method is given the same budget,
// so every method emits the same queue -- budget x days alerts -- and alpha is that queue
// over the background population. Alpha was therefore a restatement of the budget: six lines
// collapsed onto eight horizontal bands, the one quantity that actually varies was squeezed
// onto a single axis, and half its points fell in a break column at zero because a method
// that finds nothing has a precision of exactly zero. The lines then doubled back over each
// other and the six end-of-line labels overprinted into one illegible smear.
//
// So the budget takes the horizontal axis, where it is the quantity a SOC actually sets and
// the one every comparison in this paper is matched on; alpha is read off a second scale
// above it, which is exact because alpha follows from the queue size; and precision has the
// vertical axis to itself. Precision spans four decades across the drawn methods, so on its
// own axis they separate instead of converging.
const (
	svgW, svgH = 760.0, 392.0

	plotL, plotR = 92.0, 548.0
	plotT, plotB = 72.0, 322.0

	// xInset keeps the first and last budget off the frame, so their markers are not
	// bisected by it.
	xInset = 16.0

	// floorY is the row for a budget at which a method found nothing. Precision is exactly
	// zero there and has no place on a log axis, and dropping the point would make the line
	// vanish -- which a reader cannot distinguish from the method not having been measured.
	// So zero gets its own row, below a visible break in the axis.
	floorY = 346.0

	legendX     = 574.0
	legendTextX = 602.0
	legendEndX  = 752.0
)

// A method's identity rests on three channels at once, so no reading of the figure depends
// on colour alone: hue for the SCOPE of the null it tests, stroke weight for whether it is
// this framework, and dash for which member of a family it is.
//
//	--fig-a  the per-entity question -- this framework's arms, and any comparator that
//	         conditions on the entity
//	--fig-b  the population question -- the pooled-outlier formulation this work argues
//	         against
//
// Grouping the lines by scope rather than by ours-versus-theirs is deliberate: section 1.2's
// claim is about the reference set a null is built from, and the figure should show that the
// entity-scope family separates from the population-scope family before it shows which line
// inside the blue family is ours.
var (
	entityDashes     = []string{"1 0", "7 3", "2 3", "6 2 1 2"}
	populationDashes = []string{"1 0", "6 3", "2 3", "8 3 2 3"}
)

const (
	scopeEntity     = "entity"
	scopePopulation = "population"
)

// entityScopeBaselines names the comparators whose null is conditioned on the entity, and so
// belong to the same family as this framework's arms rather than to the pooled-outlier one.
//
// The classification is declared here rather than inferred, because the baselines run records
// each model's parameters and not its scope, and guessing from a name would be a guess. It is
// read off those parameters: entity_ewma keeps a per-entity exponentially weighted mean with a
// half-life in that entity's own events and scores an event by its deviation from that mean.
// Anything not named here is drawn as population-scope, and the classification is written into
// the curve file so a reader can check it rather than take it on trust.
var entityScopeBaselines = map[string]bool{"entity_ewma": true}

func scopeOf(s series) string {
	if s.Ours || entityScopeBaselines[s.Name] {
		return scopeEntity
	}
	return scopePopulation
}

// style is how one series is drawn, and how its legend row is drawn to match.
type style struct {
	stroke  string
	width   float64
	dash    string
	radius  float64
	opacity string
	bold    bool
}

// scales maps a budget and a precision onto the canvas.
type scales struct {
	b0, b1 float64 // log10 of the smallest and largest budget drawn
	p0, p1 float64 // log10 decade bounds of the precision axis
}

func (s scales) x(budget int) float64 {
	l, r := plotL+xInset, plotR-xInset
	if s.b1 <= s.b0 {
		return (l + r) / 2
	}
	return l + (math.Log10(float64(budget))-s.b0)/(s.b1-s.b0)*(r-l)
}

// y sends a precision of exactly zero to the floor row rather than off the axis.
func (s scales) y(precision float64) float64 {
	if precision <= 0 {
		return floorY
	}
	return plotB - (math.Log10(precision)-s.p0)/(s.p1-s.p0)*(plotB-plotT)
}

func renderSVG(all, rejected []series, scored, labelled, days int) (string, error) {
	if len(all) == 0 {
		return "", fmt.Errorf("nothing to draw")
	}
	background := scored - labelled
	if background <= 0 {
		return "", fmt.Errorf("%d scored events against %d labelled leaves no background", scored, labelled)
	}
	sc, budgets, err := newScales(all)
	if err != nil {
		return "", err
	}
	styles := assignStyles(all)

	var b strings.Builder
	b.WriteString(`<figure class="fig">` + "\n")
	fmt.Fprintf(&b, `<svg viewBox="0 0 %.0f %.0f" role="img" class="figsvg"`+"\n", svgW, svgH)
	fmt.Fprintf(&b, ` aria-label="%s">`+"\n", ariaLabel(all, labelled))
	b.WriteString(`<g font-family="Segoe UI, Calibri, sans-serif" fill="currentColor">` + "\n")

	writeHeader(&b, scored, labelled, days)
	writeAlphaAxis(&b, sc, budgets, background, days)
	writeFrame(&b, sc, budgets)
	writeBaseRate(&b, sc, labelled, scored)
	writeSeries(&b, all, styles, sc)
	writeCallout(&b, all, sc, labelled, scored)
	writeLegend(&b, all, styles, labelled)

	b.WriteString(`</g>` + "\n</svg>\n")
	fmt.Fprintf(&b, "<figcaption>%s</figcaption>\n</figure>",
		caption(all, rejected, scored, labelled, days))
	return b.String(), nil
}

// newScales takes the frame from the measurement: the budgets that were actually run and the
// decades the precisions actually occupy.
func newScales(all []series) (scales, []int, error) {
	seen := map[int]bool{}
	var budgets []int
	minP, maxP := math.Inf(1), math.Inf(-1)
	for _, s := range all {
		for _, p := range s.Points {
			if !seen[p.Budget] {
				seen[p.Budget] = true
				budgets = append(budgets, p.Budget)
			}
			if p.Precision > 0 {
				minP = math.Min(minP, p.Precision)
				maxP = math.Max(maxP, p.Precision)
			}
		}
	}
	if len(budgets) == 0 {
		return scales{}, nil, fmt.Errorf("no budgets to draw")
	}
	sort.Ints(budgets)
	if math.IsInf(minP, 1) {
		// No method reached a single detection at any budget. Still a figure, and still the
		// honest one, but there is no measured precision to set the axis from.
		minP, maxP = 1e-5, 1
	}
	sc := scales{
		b0: math.Log10(float64(budgets[0])),
		b1: math.Log10(float64(budgets[len(budgets)-1])),
		p0: math.Floor(math.Log10(minP)),
		p1: math.Ceil(math.Log10(maxP)),
	}
	if sc.p1 <= sc.p0 {
		sc.p1 = sc.p0 + 1
	}
	return sc, budgets, nil
}

// assignStyles walks the two scope families independently, so a dash pattern is only ever
// reused across a hue boundary where the hue already separates the two.
func assignStyles(all []series) map[string]style {
	out := make(map[string]style, len(all))
	used := map[string]int{}
	for _, s := range all {
		scope := scopeOf(s)
		dashes, stroke := populationDashes, "var(--fig-b)"
		if scope == scopeEntity {
			dashes, stroke = entityDashes, "var(--fig-a)"
		}
		st := style{stroke: stroke, dash: dashes[used[scope]%len(dashes)],
			width: 1.6, radius: 2.5, opacity: "0.85"}
		if s.Ours {
			st.width, st.radius, st.opacity, st.bold = 2.7, 3.5, "1", true
		}
		used[scope]++
		out[s.Name] = st
	}
	return out
}

func writeHeader(b *strings.Builder, scored, labelled, days int) {
	b.WriteString(`<text x="0" y="14" font-size="12.5" font-weight="600">` +
		`Share of the alert queue that is real, at each alert budget</text>` + "\n")
	fmt.Fprintf(b, `<text x="0" y="30" font-size="9.5" opacity="0.62">%s events scored over %d days &#183; `+
		`%d labelled &#183; a base rate of one in %s. Grouped by the scope of the null each method tests.</text>`+"\n",
		commas(scored), days, labelled, commas(int(math.Round(float64(scored)/float64(labelled)))))
}

// writeAlphaAxis puts the false-alarm rate above the plot rather than on an axis of its own.
//
// It is exact there, not an approximation for convenience: every method given a budget of b
// emits b x days alerts, so the queue size is a property of the budget and alpha is that
// queue over the background population. A method's own alpha is lower by the detections it
// found, which is the correction the caption reports.
func writeAlphaAxis(b *strings.Builder, sc scales, budgets []int, background, days int) {
	fmt.Fprintf(b, `<text x="%.0f" y="48" font-size="9" opacity="0.62">false-alarm rate &#945;, which the `+
		`budget fixes: the queue it buys, over %s background events</text>`+"\n", plotL, commas(background))
	b.WriteString(`<g font-size="9" opacity="0.62">` + "\n")
	for _, bd := range budgets {
		if !isDecade(bd) {
			continue
		}
		alpha := float64(bd*days) / float64(background)
		x := sc.x(bd)
		fmt.Fprintf(b, `<text x="%.1f" y="62" text-anchor="middle">%s</text>`+"\n", x, sci(alpha))
		fmt.Fprintf(b, `<line x1="%.1f" y1="66" x2="%.1f" y2="%.0f" stroke="currentColor" opacity="0.25"/>`+"\n",
			x, x, plotT)
	}
	b.WriteString(`</g>` + "\n")
}

func writeFrame(b *strings.Builder, sc scales, budgets []int) {
	// The plot ground, so a marker sitting on an axis still reads.
	fmt.Fprintf(b, `<rect x="%.0f" y="%.0f" width="%.0f" height="%.0f" fill="currentColor" opacity="0.025"/>`+"\n",
		plotL, plotT, plotR-plotL, plotB-plotT)

	// Precision decades.
	b.WriteString(`<g font-size="9" opacity="0.62">` + "\n")
	for e := sc.p0; e <= sc.p1; e++ {
		y := sc.y(math.Pow(10, e))
		fmt.Fprintf(b, `<line x1="%.0f" y1="%.1f" x2="%.0f" y2="%.1f" stroke="currentColor" opacity="0.16"/>`+"\n",
			plotL, y, plotR, y)
		fmt.Fprintf(b, `<text x="%.0f" y="%.1f" text-anchor="end">%s</text>`+"\n", plotL-8, y+3, percentLabel(e))
	}
	b.WriteString(`</g>` + "\n")

	// Budget ticks. Every measured budget is labelled, because every one of them is a point
	// on the lines; only the decades carry a gridline, so the grid stays quiet.
	b.WriteString(`<g font-size="9" opacity="0.62">` + "\n")
	for _, bd := range budgets {
		x := sc.x(bd)
		if isDecade(bd) {
			fmt.Fprintf(b, `<line x1="%.1f" y1="%.0f" x2="%.1f" y2="%.0f" stroke="currentColor" opacity="0.16"/>`+"\n",
				x, plotT, x, plotB)
		} else {
			fmt.Fprintf(b, `<line x1="%.1f" y1="%.0f" x2="%.1f" y2="%.0f" stroke="currentColor" opacity="0.3"/>`+"\n",
				x, plotB, x, plotB+4)
		}
		fmt.Fprintf(b, `<text x="%.1f" y="%.0f" text-anchor="middle">%s</text>`+"\n", x, plotB+38, commas(bd))
	}
	b.WriteString(`</g>` + "\n")

	// Axis rules.
	fmt.Fprintf(b, `<line x1="%.0f" y1="%.0f" x2="%.0f" y2="%.0f" stroke="currentColor" opacity="0.45"/>`+"\n",
		plotL, plotB, plotR, plotB)
	fmt.Fprintf(b, `<line x1="%.0f" y1="%.0f" x2="%.0f" y2="%.0f" stroke="currentColor" opacity="0.45"/>`+"\n",
		plotL, plotT, plotL, plotB)

	// The break, and the row for "measured, found nothing" beneath it.
	fmt.Fprintf(b, `<path d="M%.0f,%.0f l6,-4 M%.0f,%.0f l6,-4" stroke="currentColor" opacity="0.45" fill="none"/>`+"\n",
		plotL-3, plotB+13, plotL-3, plotB+19)
	// A band rather than a rule. Every method that found nothing at a budget is drawn on the
	// same row, so up to five flat segments coincide there; as a line that invites a reader to
	// count members it cannot separate, and as a region it reads for what it is.
	fmt.Fprintf(b, `<rect x="%.0f" y="%.1f" width="%.0f" height="13" fill="currentColor" opacity="0.05"/>`+"\n",
		plotL, floorY-6.5, plotR-plotL)
	fmt.Fprintf(b, `<text x="%.0f" y="%.1f" font-size="8.5" opacity="0.62" text-anchor="end">found nothing</text>`+"\n",
		plotL-8, floorY+3)

	fmt.Fprintf(b, `<text x="%.0f" y="%.0f" font-size="9.5" opacity="0.7" text-anchor="middle">`+
		`alert budget, alerts per analyst-day</text>`+"\n", (plotL+plotR)/2, plotB+56)
	fmt.Fprintf(b, `<text x="20" y="%.0f" font-size="9.5" opacity="0.7" transform="rotate(-90 20 %.0f)" `+
		`text-anchor="middle">share of the queue that is real</text>`+"\n",
		(plotT+plotB)/2, (plotT+plotB)/2)
}

// writeBaseRate draws the line a queue drawn at random would sit on. It is the reference the
// whole section is about: distance above it is the lift a method buys, read straight off the
// vertical axis, and a method below it has done worse than sampling events uniformly.
func writeBaseRate(b *strings.Builder, sc scales, labelled, scored int) {
	rate := float64(labelled) / float64(scored)
	y := sc.y(rate)
	fmt.Fprintf(b, `<line x1="%.0f" y1="%.1f" x2="%.0f" y2="%.1f" stroke="currentColor" opacity="0.5" stroke-dasharray="5 3"/>`+"\n",
		plotL, y, plotR, y)
	// Right-anchored: the left half of this row is where the risers of the weakest methods
	// cross it, and a label placed there is struck through by them.
	fmt.Fprintf(b, `<text x="%.0f" y="%.1f" font-size="8.5" opacity="0.7" text-anchor="end">`+
		`base rate %s &#8212; a queue drawn at random</text>`+"\n", plotR-6, y-8, sci(rate))
}

// writeSeries draws the population family first and this framework's arms last, so the lines
// the section is about are never crossed out by a comparator.
func writeSeries(b *strings.Builder, all []series, styles map[string]style, sc scales) {
	for _, s := range drawOrder(all) {
		st := styles[s.Name]
		pts := make([]string, 0, len(s.Points))
		for _, p := range s.Points {
			pts = append(pts, fmt.Sprintf("%.1f,%.1f", sc.x(p.Budget), sc.y(p.Precision)))
		}
		if len(pts) == 0 {
			continue
		}
		fmt.Fprintf(b, `<polyline points="%s" fill="none" stroke="%s" stroke-width="%.1f" `+
			`stroke-dasharray="%s" stroke-linejoin="round" opacity="%s"/>`+"\n",
			strings.Join(pts, " "), st.stroke, st.width, st.dash, st.opacity)
		fmt.Fprintf(b, `<g fill="%s" opacity="%s">`, st.stroke, st.opacity)
		for _, p := range s.Points {
			fmt.Fprintf(b, `<circle cx="%.1f" cy="%.1f" r="%.1f"/>`, sc.x(p.Budget), sc.y(p.Precision), st.radius)
		}
		b.WriteString("</g>\n")
	}
}

// drawOrder puts the population baselines first, the entity-scope baselines next and this
// framework's arms last.
func drawOrder(all []series) []series {
	out := make([]series, 0, len(all))
	for _, want := range []func(series) bool{
		func(s series) bool { return !s.Ours && scopeOf(s) == scopePopulation },
		func(s series) bool { return !s.Ours && scopeOf(s) == scopeEntity },
		func(s series) bool { return s.Ours },
	} {
		for _, s := range all {
			if want(s) {
				out = append(out, s)
			}
		}
	}
	return out
}

// writeCallout names the tightest budget on this framework's strongest arm, because that is
// the operating point section 1.1 argues from and it is the one point on the figure a reader
// should not have to interpolate.
func writeCallout(b *strings.Builder, all []series, sc scales, labelled, scored int) {
	best, ok := tightestReach(all)
	if !ok {
		return
	}
	p := best.Points[0]
	x, y := sc.x(p.Budget), sc.y(p.Precision)
	rate := float64(labelled) / float64(scored)
	fmt.Fprintf(b, `<text x="%.1f" y="%.1f" font-size="9.5" font-weight="600" fill="var(--fig-a)">`+
		`%.1f%% real at %s alerts a day</text>`+"\n", x+10, y-16, p.Precision*100, commas(p.Budget))
	fmt.Fprintf(b, `<text x="%.1f" y="%.1f" font-size="9" fill="var(--fig-a)" opacity="0.8">`+
		`%d of the %d labelled events, %s&#215; the base rate</text>`+"\n",
		x+10, y-4, p.TP, labelled, commas(twoSigFigs(p.Precision/rate)))
}

// tightestReach returns the series of ours that reaches a labelled event at the tightest
// budget drawn. It is chosen from the measurement rather than named in the source, so the
// callout follows the data if a re-run changes which arm holds that property.
func tightestReach(all []series) (series, bool) {
	var best series
	found := false
	for _, s := range all {
		if !s.Ours || len(s.Points) == 0 || s.Points[0].TP == 0 {
			continue
		}
		if !found || s.Points[0].Precision > best.Points[0].Precision {
			best, found = s, true
		}
	}
	return best, found
}

// writeLegend replaces the end-of-line labels the previous figure used. Those were placed at
// each line's widest-budget end, which is exactly where the lines converge -- the two arms of
// this framework end 0.03 decades apart -- so all six labels overprinted into one unreadable
// run of text. A legend cannot collide, and it has room to carry the reach each method
// achieved, which precision alone hides.
func writeLegend(b *strings.Builder, all []series, styles map[string]style, labelled int) {
	fmt.Fprintf(b, `<text x="%.0f" y="58" font-size="8" opacity="0.62" text-anchor="end">`+
		`most reached, of %d</text>`+"\n", legendEndX, labelled)
	y := 78.0
	for _, group := range []struct {
		scope, title, colour string
	}{
		{scopeEntity, "CONDITIONED ON THE ENTITY", "var(--fig-a)"},
		{scopePopulation, "CONDITIONED ON THE POPULATION", "var(--fig-b)"},
	} {
		members := legendGroup(all, group.scope)
		if len(members) == 0 {
			continue
		}
		fmt.Fprintf(b, `<text x="%.0f" y="%.0f" font-size="7.5" font-weight="600" letter-spacing="0.06em" `+
			`fill="%s">%s</text>`+"\n", legendX, y, group.colour, group.title)
		y += 16
		for _, s := range members {
			writeLegendRow(b, s, styles[s.Name], y)
			y += 17
		}
		y += 12
	}
}

func writeLegendRow(b *strings.Builder, s series, st style, y float64) {
	fmt.Fprintf(b, `<line x1="%.0f" y1="%.1f" x2="%.0f" y2="%.1f" stroke="%s" stroke-width="%.1f" `+
		`stroke-dasharray="%s" opacity="%s"/>`+"\n",
		legendX, y-3, legendX+20, y-3, st.stroke, st.width, st.dash, st.opacity)
	fmt.Fprintf(b, `<circle cx="%.0f" cy="%.1f" r="%.1f" fill="%s" opacity="%s"/>`+"\n",
		legendX+5, y-3, st.radius, st.stroke, st.opacity)
	weight := "400"
	if st.bold {
		weight = "600"
	}
	fmt.Fprintf(b, `<text x="%.0f" y="%.0f" font-size="9.5" font-weight="%s" fill="%s">%s</text>`+"\n",
		legendTextX, y, weight, st.stroke, legendLabel(s))
	fmt.Fprintf(b, `<text x="%.0f" y="%.0f" font-size="9.5" font-weight="%s" fill="%s" text-anchor="end">%d</text>`+"\n",
		legendEndX, y, weight, st.stroke, bestReach(s))
}

// legendGroup keeps this framework's arms ahead of the comparators inside a family, in the
// order they were asked for, and sorts the comparators after them by what they reached.
//
// Strongest comparator first, because the column beside the names is a count of labelled
// events and a column of counts in no order reads as though it were in one. It also puts the
// closest rival next to our own arms, which is the comparison a reader makes anyway.
func legendGroup(all []series, scope string) []series {
	var ours, theirs []series
	for _, s := range all {
		if scopeOf(s) != scope {
			continue
		}
		if s.Ours {
			ours = append(ours, s)
		} else {
			theirs = append(theirs, s)
		}
	}
	sort.SliceStable(theirs, func(i, j int) bool {
		ri, rj := bestReach(theirs[i]), bestReach(theirs[j])
		if ri != rj {
			return ri > rj
		}
		return theirs[i].Name < theirs[j].Name
	})
	return append(ours, theirs...)
}

// legendLabel shortens the series name to what fits the legend column, keeping the paper's
// own words for itself.
func legendLabel(s series) string {
	if !s.Ours {
		return escapeText(s.Name)
	}
	return strings.Replace(escapeText(s.Label), "this framework, ", "this framework &#183; ", 1)
}

func bestReach(s series) int {
	best := 0
	for _, p := range s.Points {
		if p.TP > best {
			best = p.TP
		}
	}
	return best
}

// twoSigFigs rounds a lift to two significant figures, so this figure and the abstract quote
// the same number: the tightest budget is 1,199.5 times the base rate, which is "about 1,200"
// in the prose, and printing 1,199 here would invite a reader to hunt for a discrepancy that
// is only a rounding rule.
func twoSigFigs(v float64) int {
	if v <= 0 {
		return 0
	}
	mag := math.Pow(10, math.Floor(math.Log10(v))-1)
	return int(math.Round(v/mag) * mag)
}

func isDecade(n int) bool {
	if n <= 0 {
		return false
	}
	for n%10 == 0 {
		n /= 10
	}
	return n == 1
}

// percentLabel writes a precision decade the way an operator reads it. "0.1%" says what one
// alert in a thousand means without the reader converting an exponent.
func percentLabel(e float64) string {
	p := math.Pow(10, e+2)
	if p >= 1 {
		return fmt.Sprintf("%.0f%%", p)
	}
	return fmt.Sprintf("%.*f%%", int(math.Ceil(-math.Log10(p))), p)
}

func decadeLabel(e float64) string {
	sup := map[rune]string{'-': "&#8315;", '0': "&#8304;", '1': "&#185;", '2': "&#178;",
		'3': "&#179;", '4': "&#8308;", '5': "&#8309;", '6': "&#8310;", '7': "&#8311;",
		'8': "&#8312;", '9': "&#8313;"}
	if e == 0 {
		return "1"
	}
	var out strings.Builder
	out.WriteString("10")
	for _, r := range fmt.Sprintf("%d", int(e)) {
		out.WriteString(sup[r])
	}
	return out.String()
}

func ariaLabel(all []series, labelled int) string {
	var parts []string
	for _, s := range all {
		parts = append(parts, fmt.Sprintf("%s, %s scope, reaches at most %d of the %d labelled events",
			s.Label, scopeOf(s), bestReach(s), labelled))
	}
	return escapeText(strings.Join(parts, "; ") + ".")
}

func escapeText(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return strings.ReplaceAll(s, `"`, "&quot;")
}

// caption states the population every line shares, because that is the claim the shared axes
// make and the reader has no other way to check it; what colour means, since a colour is not
// self-describing; and how far a method's own alpha sits below the budget's, which is the one
// thing the horizontal axis cannot show.
//
// It is kept to about the length of the caption it replaces, on purpose. The paper has a
// twenty-page ceiling, a caption is prose like any other, and the first draft of this one ran a
// hundred words longer and cost a page. It does not restate the axes: section 1.1 introduces
// them, and saying it twice is what the length went on.
func caption(all, rejected []series, scored, labelled, days int) string {
	var b strings.Builder
	b.WriteString("<strong>The comparison is a function of the budget, not a point.</strong> ")
	fmt.Fprintf(&b, "Every line is measured on the same slice: %s scored events over %d days, "+
		"of which %d are labelled, a base rate of one in %s. Each point is one method at one measured "+
		"budget. A method that found nothing at a budget is drawn on the <em>found nothing</em> row "+
		"below the axis break rather than omitted, so a flat line there reads as measured rather than "+
		"as missing; the methods leave that row at six different budgets, which is the comparison this "+
		"figure exists to make.\n<br><br>\n",
		commas(scored), days, labelled,
		commas(int(math.Round(float64(scored)/float64(labelled)))))

	fmt.Fprintf(&b, "<strong>Colour is the scope of the null, not who wrote the method.</strong> Blue "+
		"judges an event against the history of the account that produced it, orange against the pooled "+
		"population (§1.2). <code>entity_ewma</code> is a comparator drawn in blue because it keeps a "+
		"per-entity mean; this framework's arms are the heavier strokes in that family, and every "+
		"method's scope is recorded in the curve file. A method's own &#945; is lower than the budget's "+
		"by the detections it found, %s.\n<br><br>\n", alphaCorrection(all))

	fmt.Fprintf(&b, "The figure draws the four comparators with the most detections over the budget range, "+
		"so the choice does not flatter this framework; the other %s reached too little to plot, "+
		"and the table below lists all eight that were run. ", spell(len(rejected)))
	b.WriteString("<em>Every reading here is an upper bound on this framework's disadvantage and a lower " +
		"bound on its precision.</em> The corpus has no true negative class, so an unlabelled alert on " +
		"genuine but unrecorded malicious activity counts as a false alarm (§12.5). The baselines read a " +
		"fixed-length numeric vector per event and only <code>entity_ewma</code> holds per-entity state; " +
		"they are not held to R2 or R4 (§12.4).")
	return b.String()
}

// alphaCorrection reports how far a method's own alpha falls below the budget's, at the two
// ends of the range. It is measured rather than asserted: the gap is the true-positive share
// of the queue, which is largest exactly where the queue is smallest.
func alphaCorrection(all []series) string {
	tight, wide := 0.0, 0.0
	tightB, wideB := 0, 0
	for _, s := range all {
		for _, p := range s.Points {
			if p.Alerts == 0 {
				continue
			}
			share := float64(p.TP) / float64(p.Alerts)
			if tightB == 0 || p.Budget < tightB {
				tightB, tight = p.Budget, 0
			}
			if p.Budget == tightB && share > tight {
				tight = share
			}
			if p.Budget > wideB {
				wideB, wide = p.Budget, 0
			}
			if p.Budget == wideB && share > wide {
				wide = share
			}
		}
	}
	if tightB == 0 {
		return "by a share too small to plot"
	}
	return fmt.Sprintf("at most %.0f%% at %s alerts a day and %.1f%% at %s",
		tight*100, commas(tightB), wide*100, commas(wideB))
}

func commas(n int) string {
	s := fmt.Sprintf("%d", n)
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	return string(out)
}

func sci(v float64) string {
	if v <= 0 {
		return "0"
	}
	e := math.Floor(math.Log10(v))
	m := v / math.Pow(10, e)
	return fmt.Sprintf("%.1f&#215;%s", m, decadeLabel(e))
}

// spell renders a small count as a word, matching the surrounding prose rather than dropping a
// numeral into it.
func spell(n int) string {
	words := []string{"zero", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten"}
	if n >= 0 && n < len(words) {
		return words[n]
	}
	return fmt.Sprintf("%d", n)
}
