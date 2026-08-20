package main

import (
	"fmt"
	"math"
	"strings"
)

// Geometry of the figure. The axes carry the same two quantities §1.1's figure always
// carried — the share of alerts that are real, and the false-alarm rate — but both are
// drawn on log scales, because measured operating points span decades where the previous
// figure's hand-placed ticks spanned one order of magnitude of illustrative values.
const (
	svgW, svgH = 720.0, 380.0
	plotL      = 96.0
	plotR      = 604.0
	plotT      = 44.0
	plotB      = 288.0
	// zeroCol is where a method with no detections at a budget is drawn: precision is
	// exactly zero there and has no place on a log axis. Omitting the point would make
	// the line vanish, which reads as "not measured" rather than "measured, found
	// nothing", so zero gets its own column past a break in the axis.
	zeroCol = 74.0
)

// dashes give each baseline a distinct stroke, so identity never rests on colour: the two
// meaningful hues of this figure set are reserved for the per-entity/population contrast.
var dashes = []string{"1 0", "5 3", "2 3", "8 3 2 3", "6 2 1 2"}

func renderSVG(all, rejected []series, scored, labelled, days int) (string, error) {
	if len(all) == 0 {
		return "", fmt.Errorf("nothing to draw")
	}

	// Decade bounds from the data, so the frame follows the measurement.
	minP, maxP := math.Inf(1), math.Inf(-1)
	minA, maxA := math.Inf(1), math.Inf(-1)
	for _, s := range all {
		for _, p := range s.Points {
			if p.Precision > 0 {
				minP = math.Min(minP, p.Precision)
				maxP = math.Max(maxP, p.Precision)
			}
			if p.Alpha > 0 {
				minA = math.Min(minA, p.Alpha)
				maxA = math.Max(maxA, p.Alpha)
			}
		}
	}
	if math.IsInf(minP, 1) {
		// No method reached a single detection at any budget. Still a figure, and still
		// the honest one, but there is no precision axis to draw.
		minP, maxP = 1e-4, 1
	}
	if math.IsInf(minA, 1) {
		minA, maxA = 1e-7, 1e-1
	}
	x0, x1 := math.Floor(math.Log10(minP)), math.Ceil(math.Log10(maxP))
	y0, y1 := math.Floor(math.Log10(minA)), math.Ceil(math.Log10(maxA))
	if x1 <= x0 {
		x1 = x0 + 1
	}
	if y1 <= y0 {
		y1 = y0 + 1
	}

	xFor := func(p float64) float64 {
		if p <= 0 {
			return zeroCol
		}
		return plotL + (math.Log10(p)-x0)/(x1-x0)*(plotR-plotL)
	}
	// y grows downward, and a LOWER false-alarm rate is better, so the smaller rate sits
	// at the bottom.
	yFor := func(a float64) float64 {
		if a <= 0 {
			a = math.Pow(10, y0)
		}
		return plotB - (math.Log10(a)-y0)/(y1-y0)*(plotB-plotT)
	}

	var b strings.Builder
	b.WriteString(`<figure class="fig">` + "\n")
	fmt.Fprintf(&b, `<svg viewBox="0 0 %.0f %.0f" role="img" class="figsvg"`+"\n", svgW, svgH)
	fmt.Fprintf(&b, ` aria-label="%s">`+"\n", ariaLabel(all))
	b.WriteString(`<g font-size="12" fill="currentColor">` + "\n")

	fmt.Fprintf(&b, `<text x="0" y="14" font-size="11">share of alerts that are real against false-alarm rate, one line per detector, across alert budgets of %s a day</text>`+"\n",
		budgetRange(all))

	// Frame.
	fmt.Fprintf(&b, `<line x1="%.0f" y1="%.0f" x2="%.0f" y2="%.0f" stroke="currentColor" opacity="0.45"/>`+"\n", plotL, plotB, plotR, plotB)
	fmt.Fprintf(&b, `<line x1="%.0f" y1="%.0f" x2="%.0f" y2="%.0f" stroke="currentColor" opacity="0.45"/>`+"\n", plotL, plotT, plotL, plotB)

	// The break that separates the zero column from the log axis.
	fmt.Fprintf(&b, `<line x1="%.0f" y1="%.0f" x2="%.0f" y2="%.0f" stroke="currentColor" opacity="0.45" stroke-dasharray="2 2"/>`+"\n", zeroCol, plotB, zeroCol, plotT)
	fmt.Fprintf(&b, `<text x="%.0f" y="%.0f" font-size="9" opacity="0.7" text-anchor="middle">0</text>`+"\n", zeroCol, plotB+16)
	fmt.Fprintf(&b, `<text x="%.0f" y="%.0f" font-size="8.5" opacity="0.55" text-anchor="middle">no detections</text>`+"\n", zeroCol, plotB+28)

	// Axis ticks, one per decade.
	b.WriteString(`<g font-size="9" opacity="0.65">` + "\n")
	for e := x0; e <= x1; e++ {
		x := xFor(math.Pow(10, e))
		fmt.Fprintf(&b, `<line x1="%.1f" y1="%.0f" x2="%.1f" y2="%.0f" stroke="currentColor" opacity="0.18"/>`+"\n", x, plotT, x, plotB)
		fmt.Fprintf(&b, `<text x="%.1f" y="%.0f" text-anchor="middle">%s</text>`+"\n", x, plotB+16, decadeLabel(e))
	}
	for e := y0; e <= y1; e++ {
		y := yFor(math.Pow(10, e))
		fmt.Fprintf(&b, `<line x1="%.0f" y1="%.1f" x2="%.0f" y2="%.1f" stroke="currentColor" opacity="0.18"/>`+"\n", plotL, y, plotR, y)
		fmt.Fprintf(&b, `<text x="%.0f" y="%.1f" text-anchor="end">%s</text>`+"\n", plotL-8, y+3, decadeLabel(e))
	}
	b.WriteString(`</g>` + "\n")

	fmt.Fprintf(&b, `<text x="%.0f" y="%.0f" font-size="10" opacity="0.7" text-anchor="middle">share of alerts that are real</text>`+"\n", (plotL+plotR)/2, plotB+44)
	fmt.Fprintf(&b, `<text x="24" y="%.0f" font-size="10" opacity="0.7" transform="rotate(-90 24 %.0f)" text-anchor="middle">false-alarm rate</text>`+"\n", (plotT+plotB)/2, (plotT+plotB)/2)

	// One polyline per detector. Ours last, so it draws over the baselines.
	order := make([]series, 0, len(all))
	for _, s := range all {
		if !s.Ours {
			order = append(order, s)
		}
	}
	for _, s := range all {
		if s.Ours {
			order = append(order, s)
		}
	}

	for i, s := range order {
		pts := make([]string, 0, len(s.Points))
		for _, p := range s.Points {
			pts = append(pts, fmt.Sprintf("%.1f,%.1f", xFor(p.Precision), yFor(p.Alpha)))
		}
		if len(pts) == 0 {
			continue
		}
		stroke, width, dash, opacity := "currentColor", 1.4, dashes[i%len(dashes)], "0.7"
		if s.Ours {
			stroke, width, dash, opacity = "var(--fig-a)", 2.6, "1 0", "1"
		}
		fmt.Fprintf(&b, `<polyline points="%s" fill="none" stroke="%s" stroke-width="%.1f" stroke-dasharray="%s" opacity="%s"/>`+"\n",
			strings.Join(pts, " "), stroke, width, dash, opacity)
		fmt.Fprintf(&b, `<g fill="%s" opacity="%s">`+"\n", stroke, opacity)
		for _, p := range s.Points {
			r := 2.6
			if s.Ours {
				r = 3.6
			}
			fmt.Fprintf(&b, `<circle cx="%.1f" cy="%.1f" r="%.1f"/>`, xFor(p.Precision), yFor(p.Alpha), r)
		}
		b.WriteString("\n</g>\n")

		// Direct label at the widest-budget end, so identity never rests on the stroke.
		last := s.Points[len(s.Points)-1]
		lx, ly := xFor(last.Precision)+8, yFor(last.Alpha)+3
		if lx > plotR-40 {
			lx = plotR + 6
		}
		weight := "400"
		if s.Ours {
			weight = "600"
		}
		fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" font-size="10" fill="%s" font-weight="%s" opacity="%s">%s</text>`+"\n",
			lx, ly, stroke, weight, opacity, escapeText(s.Label))
	}

	// Budget annotations on our own line, so a reader can locate a budget on the curve.
	for _, s := range all {
		if !s.Ours {
			continue
		}
		for _, p := range s.Points {
			if p.Budget != 10 && p.Budget != 1000 && p.Budget != 10000 {
				continue
			}
			fmt.Fprintf(&b, `<text x="%.1f" y="%.1f" font-size="8.5" fill="var(--fig-a)" opacity="0.85" text-anchor="middle">%d/day</text>`+"\n",
				xFor(p.Precision), yFor(p.Alpha)-8, p.Budget)
		}
	}

	b.WriteString(`</g>` + "\n</svg>\n")
	fmt.Fprintf(&b, "<figcaption>%s</figcaption>\n</figure>", caption(all, rejected, scored, labelled, days))
	return b.String(), nil
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

func budgetRange(all []series) string {
	for _, s := range all {
		if s.Ours && len(s.Points) > 0 {
			return fmt.Sprintf("%d to %d alerts", s.Points[0].Budget, s.Points[len(s.Points)-1].Budget)
		}
	}
	return "several budgets"
}

func ariaLabel(all []series) string {
	var parts []string
	for _, s := range all {
		best := 0
		for _, p := range s.Points {
			if p.TP > best {
				best = p.TP
			}
		}
		parts = append(parts, fmt.Sprintf("%s reaches at most %d of the labelled events", s.Label, best))
	}
	return escapeText(strings.Join(parts, "; ") + ".")
}

func escapeText(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return strings.ReplaceAll(s, `"`, "&quot;")
}

// caption states the population every line shares, because that is the claim the shared
// axes make and the reader has no other way to check it.
func caption(all, rejected []series, scored, labelled, days int) string {
	var ours series
	for _, s := range all {
		if s.Ours {
			ours = s
		}
	}

	var b strings.Builder
	b.WriteString("<strong>The comparison is a function of the budget, not a point.</strong> ")
	fmt.Fprintf(&b, "Every line is measured on the same slice: %s scored events over %d days, "+
		"of which %d are labelled, a base rate of one in %s. ",
		commas(scored), days, labelled, commas(int(math.Round(float64(scored)/float64(labelled)))))
	b.WriteString("Each point is one detector at one alert budget: the share of its own queue that is real " +
		"on the horizontal axis, and the rate at which it alarms on a background event on the vertical. " +
		"A detector that found nothing at a budget is drawn in the column marked 0 rather than omitted, " +
		"so a flat line at zero reads as measured rather than as missing.\n<br><br>\n")

	if len(ours.Points) > 0 {
		first, last := ours.Points[0], ours.Points[len(ours.Points)-1]
		fmt.Fprintf(&b, "At %d alerts a day this framework's queue is %.1f%% real at a false-alarm rate of %s; "+
			"at %d a day it is %.2f%% real at %s. ",
			first.Budget, first.Precision*100, sci(first.Alpha),
			last.Budget, last.Precision*100, sci(last.Alpha))
	}
	b.WriteString("The baselines are the strongest available comparators rather than a representative sample: " +
		"they are the four of eight measured reference implementations with the most detections summed over " +
		"the budget range, so the comparison is not flattered by the choice. ")
	if names := notDrawn(rejected); names != "" {
		fmt.Fprintf(&b, "The %s not drawn were run on the same events and reached too little to plot: %s.",
			spell(len(rejected)), names)
	}
	b.WriteString("\n<br><br>\n")
	b.WriteString("<em>Both axes are upper bounds on the framework's disadvantage and lower bounds on its " +
		"precision.</em> The corpus has no true negative class, so an unlabelled alert on genuine but " +
		"unrecorded malicious activity counts as a false alarm (§12.5). The baselines read a fixed-length " +
		"numeric vector per event with no per-entity state, which is the standard formulation this work " +
		"argues against, and they are not held to R2 or R4 (§12.4).")
	return b.String()
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

// notDrawn names the measured comparators the figure leaves out, each with the most labelled
// events it reached at any budget. Naming them with their counts is the point: it records that
// they were run and how little they found, rather than leaving a reader to wonder which eight
// were meant or to assume the omitted ones might have done better.
func notDrawn(rejected []series) string {
	names := make([]string, 0, len(rejected))
	for _, r := range rejected {
		best := 0
		for _, p := range r.Points {
			if p.TP > best {
				best = p.TP
			}
		}
		switch best {
		case 0:
			names = append(names, r.Name+" (none at any budget)")
		case 1:
			names = append(names, r.Name+" (one, at the widest budget)")
		default:
			names = append(names, fmt.Sprintf("%s (at most %d)", r.Name, best))
		}
	}
	if len(names) == 0 {
		return ""
	}
	if len(names) == 1 {
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
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
