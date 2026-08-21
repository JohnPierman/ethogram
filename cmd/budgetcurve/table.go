package main

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// The headline table that sits under section 1.1's figure.
//
// # Why a table as well as a curve
//
// The figure answers "how does this change with the budget", which is the question the section
// argues. It is a poor answer to "what did each method actually do", because reading a count off
// a log axis is an estimate and four of the eight measured comparators are not drawn at all. A
// reader arriving at the paper wants the second question answered first, in numbers they do not
// have to interpolate.
//
// So the table fixes the budget and lists every method that was run, including the four the
// figure leaves out for want of room. Leaving those to a sentence in a caption invites a reader
// to wonder whether the omitted ones would have done better, and the answer -- that most of them
// reach nothing at either budget -- is the strongest row in the table.
//
// # Why two budgets and not one
//
// One budget makes the comparison look like a property of the methods when most of it is a
// property of what was affordable. At 100 alerts a day seven of the eight comparators reach
// nothing and this framework reaches 11% of the campaign; at 1,000 it reaches 37% and two
// comparators have found something. A reader given only the first would take the shutout for a
// permanent fact, and a reader given only the second would miss that it closes as the budget
// widens. Both columns are the section's claim, and the claim is that it is budget-dependent.
//
// # Why it is generated
//
// It crosses ten methods with two budgets and three derived quantities, and every one of them
// moves when the run moves. Sixty cells kept correct by hand across a re-measurement is not a
// reasonable expectation -- it is the defect this repository has already fixed twice.

// tableBudgets are the operating points the table reports.
//
// Not the tightest and not the widest. At 10 alerts a day only one method has reached anything
// and the table is a column of zeros with one entry; at 10,000 every method has reached
// something and the separation the section is about has closed. These two are where both
// readings are informative at once, and both are plausible shifts for a real team.
var tableBudgets = []int{100, 1000}

// renderTable emits the figure block cmd/thesis injects at the `budget-table` anchor.
func renderTable(ours, measured []series, scored, labelled, days int, budgets []int) (string, error) {
	rows := tableRows(ours, measured)
	if len(rows) == 0 {
		return "", fmt.Errorf("no methods to tabulate")
	}
	if len(budgets) == 0 {
		return "", fmt.Errorf("no budgets to tabulate")
	}
	for _, bd := range budgets {
		if _, ok := pointAt(rows[0], bd); !ok {
			return "", fmt.Errorf("%s records no budget of %d a day, so the table cannot report it",
				rows[0].Name, bd)
		}
	}

	var b strings.Builder
	b.WriteString(`<figure class="fig">` + "\n")
	b.WriteString(`<div class="tablewrap">` + "\n<table>\n")
	writeTableHead(&b, budgets)
	b.WriteString("<tbody>\n")
	for _, r := range rows {
		writeTableRow(&b, r, budgets, labelled)
	}
	b.WriteString("</tbody>\n</table>\n</div>\n")
	fmt.Fprintf(&b, "<figcaption>%s</figcaption>\n</figure>",
		tableCaption(rows, ours, scored, labelled, days, budgets))
	return b.String(), nil
}

// writeTableHead groups three columns under each budget, so a reader compares two methods at one
// budget by reading down and one method across budgets by reading along.
func writeTableHead(b *strings.Builder, budgets []int) {
	b.WriteString(`<thead>` + "\n" +
		`<tr><th rowspan="2">Method</th><th rowspan="2">Judged against</th>`)
	for _, bd := range budgets {
		fmt.Fprintf(b, `<th colspan="3" class="group">%s alerts / analyst-day</th>`, commas(bd))
	}
	b.WriteString("</tr>\n<tr>")
	for range budgets {
		b.WriteString(`<th class="num group">Found</th><th class="num">Precision</th>` +
			`<th class="num">Recall</th>`)
	}
	b.WriteString("</tr>\n</thead>\n")
}

func writeTableRow(b *strings.Builder, r series, budgets []int, labelled int) {
	name := escapeText(tableLabel(r))
	if r.Ours {
		name = "<strong>" + name + "</strong>"
	}
	fmt.Fprintf(b, "<tr><td>%s</td><td>%s</td>", name, judgedAgainst(r))
	for _, bd := range budgets {
		p, ok := pointAt(r, bd)
		if !ok {
			b.WriteString(`<td class="num group">not run</td><td class="num">not run</td>` +
				`<td class="num">not run</td>`)
			continue
		}
		fmt.Fprintf(b, `<td class="num group">%d</td><td class="num">%s</td><td class="num">%s</td>`,
			p.TP, pct2(p.Precision), pct2(float64(p.TP)/float64(labelled)))
	}
	b.WriteString("</tr>\n")
}

// tableRows lists this framework's arms first and then every measured comparator, entity-scope
// before population-scope and strongest first inside each family.
func tableRows(ours, measured []series) []series {
	byFamily := map[string][]series{}
	for _, s := range measured {
		byFamily[scopeOf(s)] = append(byFamily[scopeOf(s)], s)
	}
	for _, family := range byFamily {
		sort.SliceStable(family, func(i, j int) bool {
			ri, rj := bestReach(family[i]), bestReach(family[j])
			if ri != rj {
				return ri > rj
			}
			return family[i].Name < family[j].Name
		})
	}
	out := make([]series, 0, len(ours)+len(measured))
	out = append(out, ours...)
	out = append(out, byFamily[scopeEntity]...)
	return append(out, byFamily[scopePopulation]...)
}

func pointAt(s series, budget int) (point, bool) {
	for _, p := range s.Points {
		if p.Budget == budget {
			return p, true
		}
	}
	return point{}, false
}

// judgedAgainst spells the scope out rather than printing this repository's shorthand, because a
// headline table is read cold and "entity" is not self-explanatory.
func judgedAgainst(s series) string {
	if scopeOf(s) == scopeEntity {
		return "its own history"
	}
	return "the population"
}

func tableLabel(s series) string {
	if s.Ours {
		return s.Label
	}
	return s.Name
}

// pct2 writes a share to two significant figures, and writes a measured zero as "0".
//
// Not as a dash: a dash reads as "not measured", and every one of these methods was run on
// exactly the same events. Reaching nothing is the result, not the absence of one.
func pct2(v float64) string {
	if v <= 0 {
		return "0"
	}
	p := v * 100
	switch {
	case p >= 10:
		return fmt.Sprintf("%.0f%%", p)
	case p >= 1:
		return fmt.Sprintf("%.1f%%", p)
	default:
		return fmt.Sprintf("%.*f%%", int(math.Ceil(-math.Log10(p)))+1, p)
	}
}

func tableCaption(rows, ours []series, scored, labelled, days int, budgets []int) string {
	baseRate := float64(labelled) / float64(scored)
	var b strings.Builder

	b.WriteString("<strong>Every method that was run, at two budgets.</strong> ")
	fmt.Fprintf(&b, "A budget of <i>b</i> alerts per analyst-day buys a queue of <i>b</i>&#8239;&#215;&#8239;%d "+
		"alerts over the %d days scored, whichever method spends it. <i>Found</i> is labelled events "+
		"reached, <i>precision</i> the share of the queue that is real, and <i>recall</i> the share of "+
		"the %d labelled events reached.\n<br><br>\n", days, days, labelled)

	writeShutoutSentence(&b, rows, budgets)

	if lead, ok := strongestArm(ours, budgets); ok {
		var reach, lifts []string
		for i, bd := range budgets {
			p, ok := pointAt(lead, bd)
			if !ok {
				continue
			}
			recall := pct2(float64(p.TP) / float64(labelled))
			if i == 0 {
				reach = append(reach, fmt.Sprintf("%s of the campaign at %s alerts a day",
					recall, commas(bd)))
			} else {
				reach = append(reach, fmt.Sprintf("%s at %s", recall, commas(bd)))
			}
			lifts = append(lifts, commas(twoSigFigs(p.Precision/baseRate))+"&#215;")
		}
		if len(reach) > 0 {
			fmt.Fprintf(&b, "This framework's %s reaches %s, against a base rate of %s &#8212; "+
				"a lift of %s.\n<br><br>\n",
				escapeText(armShortName(lead)), joinClauses(reach), sci(baseRate), joinClauses(lifts))
		}
	}

	b.WriteString("<em>Recall counts what the red team wrote down, not what happened.</em> The label " +
		"file records an authorised exercise rather than annotating the log exhaustively, so " +
		"unrecorded malicious activity counts against precision and is absent from the recall " +
		"denominator (§12.5): both columns are lower bounds. Only <code>entity_ewma</code> holds " +
		"per-entity state, and no comparator is held to R2 or R4 (§12.4).")
	return b.String()
}

// writeShutoutSentence reports how many comparators reached nothing, at each budget. It is the
// table's strongest reading and it is counted from the rows rather than asserted, so it cannot
// survive a re-measurement that changes it.
func writeShutoutSentence(b *strings.Builder, rows []series, budgets []int) {
	var parts []string
	for _, bd := range budgets {
		zero, total := zeroCount(rows, bd)
		if total == 0 {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s of the %s at %s a day", spell(zero), spell(total), commas(bd)))
	}
	if len(parts) == 0 {
		return
	}
	fmt.Fprintf(b, "<strong>Most of the comparators reach nothing at all:</strong> %s. ",
		joinClauses(parts))
	b.WriteString("A zero is measured, not missing &#8212; every method read the same events, which " +
		"is why the figure above needs a row below its axis.\n<br><br>\n")
}

// zeroCount reports how many comparators reached nothing at the budget, out of how many were
// measured. This framework's own arms are excluded: the sentence it feeds is about the
// comparators, and counting ourselves among them would make it say less than it means.
func zeroCount(rows []series, budget int) (zero, total int) {
	for _, r := range rows {
		if r.Ours {
			continue
		}
		p, ok := pointAt(r, budget)
		if !ok {
			continue
		}
		total++
		if p.TP == 0 {
			zero++
		}
	}
	return zero, total
}

// strongestArm is the arm of ours that reached the most at the TIGHTEST budget tabulated.
//
// Not the most anywhere. The composite reaches more than the novelty arm at the widest budget and
// nothing at all at the tightest, which is section 5.4's finding; ranking by the maximum would
// make this caption introduce the weaker arm as the headline. Chosen from the measurement rather
// than named here, so the caption follows the data if a re-run changes which arm leads.
func strongestArm(ours []series, budgets []int) (series, bool) {
	if len(budgets) == 0 {
		return series{}, false
	}
	var best series
	found := false
	for _, s := range ours {
		p, ok := pointAt(s, budgets[0])
		if !ok {
			continue
		}
		if bp, _ := pointAt(best, budgets[0]); !found || p.TP > bp.TP {
			best, found = s, true
		}
	}
	return best, found
}

// armShortName drops the "this framework, " the series label carries, so the caption can name
// the arm inside a sentence of its own rather than opening with a lowercase label.
func armShortName(s series) string {
	return strings.TrimPrefix(s.Label, "this framework, ")
}

func joinClauses(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	default:
		return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
	}
}
