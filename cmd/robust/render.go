package main

// Rendering. Every table here is meant to be read without a paragraph explaining it, so the
// column that decides the comparison is the one furthest right and the units are in the
// header rather than in prose above it.

import (
	"fmt"
	"sort"
	"strings"
)

// short is the mechanism label the tables use. The taxonomy's keys are long enough that
// seven of them plus a total will not fit a printed column.
var short = map[string]string{
	"credential_spray":     "spray",
	"lateral_chain":        "lateral",
	"off_hours":            "off-hrs",
	"privilege_escalation": "priv-esc",
	"low_and_slow":         "low+slow",
	"account_takeover":     "takeover",
	"real campaign":        "real",
}

func label(mech string) string {
	if s, ok := short[mech]; ok {
		return s
	}
	return mech
}

func (a *Analysis) markdown() string {
	var b strings.Builder

	fmt.Fprintf(&b, "**Detection rate by mechanism, at %d alerts a day.**\n\n", a.Budget)
	b.WriteString("| Arm |")
	for _, mech := range a.Mechanisms {
		fmt.Fprintf(&b, " %s |", label(mech))
	}
	b.WriteString(" worst |\n|---|")
	for range a.Mechanisms {
		b.WriteString("---|")
	}
	b.WriteString("---|\n")
	for _, arm := range a.Arms {
		fmt.Fprintf(&b, "| `%s` |", arm)
		worst := 1.0
		for _, mech := range a.Mechanisms {
			r := a.Rate[arm][mech]
			if r < worst {
				worst = r
			}
			fmt.Fprintf(&b, " %s |", rate(r))
		}
		fmt.Fprintf(&b, " %s |\n", rate(worst))
	}
	b.WriteString("| *best of any arm* |")
	for _, mech := range a.Mechanisms {
		best := 0.0
		for _, arm := range a.Arms {
			if r := a.Rate[arm][mech]; r > best {
				best = r
			}
		}
		fmt.Fprintf(&b, " **%s** |", rate(best))
	}
	b.WriteString(" |\n")

	if len(a.Unreachable) > 0 {
		fmt.Fprintf(&b, "\nUnreached by every arm: **%s**. Each such mechanism fixes the "+
			"value of the game at zero.\n", strings.Join(labels(a.Unreachable), ", "))
	}

	b.WriteString("\n**What each objective allocates.**\n\n")
	b.WriteString("| Objective | Guarantee | Allocation | Over best single arm |\n")
	b.WriteString("|---|---|---|---|\n")
	// Where every arm is blind to some mechanism they all guarantee nothing, and naming one
	// of them would report a tie as a choice.
	best := fmt.Sprintf("whole budget to `%s`", a.Maximin.BestPure)
	if a.Maximin.BestPureValue == 0 {
		best = "any one arm — every arm is blind to some mechanism"
	}
	fmt.Fprintf(&b, "| best single arm | %s | %s | — |\n",
		rate(a.Maximin.BestPureValue), best)
	fmt.Fprintf(&b, "| maximin | %s | %s | %s |\n",
		rate(a.Maximin.Value), mixture(a.Maximin), delta(a.Maximin.Gain))
	fmt.Fprintf(&b, "| competitive ratio | %s of achievable | %s | %s |\n",
		rate(a.Competitive.Value), mixture(a.Competitive), delta(a.Competitive.Gain))

	if len(a.Competitive.Retained) > 0 {
		b.WriteString("\n**Fraction of the achievable detection the robust allocation retains.**\n\n")
		b.WriteString("|")
		keys := sortedByLabel(a.Competitive.Retained, a.Mechanisms)
		for _, mech := range keys {
			fmt.Fprintf(&b, " %s |", label(mech))
		}
		b.WriteString("\n|")
		for range keys {
			b.WriteString("---|")
		}
		b.WriteString("\n|")
		for _, mech := range keys {
			fmt.Fprintf(&b, " %s |", rate(a.Competitive.Retained[mech]))
		}
		b.WriteString("\n")
	}

	if a.Price != nil {
		b.WriteString("\n**What the guarantee costs.**\n\n")
		b.WriteString("| Allocation | Expected rate | Worst-case rate |\n|---|---|---|\n")
		fmt.Fprintf(&b, "| best single arm (`%s`) | **%s** | %s |\n",
			a.Price.BayesArm, rate(a.Price.BayesExpected), rate(a.Price.BayesWorstCase))
		fmt.Fprintf(&b, "| robust mixture | %s | **%s** |\n",
			rate(a.Price.RobustExpected), rate(a.Price.RobustWorstCase))
		fmt.Fprintf(&b, "\nRobustness gives up %.0f%% of expected detection to buy it.\n",
			100*a.Price.FractionGivenUp)
	}

	if len(a.AttackerCost) > 0 {
		b.WriteString("\n**Equilibrium when the adversary pays for the mechanism it chooses.**\n\n")
		b.WriteString("| λ | Value | Allocation | Adversary's reply |\n|---|---|---|---|\n")
		for _, c := range a.AttackerCost {
			fmt.Fprintf(&b, "| %g | %s | %s | %s |\n",
				c.Lambda, rate(c.Value), weights(c.Mix), weights(c.Response))
		}
	}

	if len(a.Shadow) > 0 {
		b.WriteString("\n**Where the next unit of detector work is worth spending.** " +
			"Gain in the guarantee from raising one cell by 0.10.\n\n")
		b.WriteString("| Arm | Mechanism | Current | Gain |\n|---|---|---|---|\n")
		for i, s := range a.Shadow {
			if i >= 8 {
				fmt.Fprintf(&b, "\n%d further cells carry a smaller positive gain; "+
					"none lies outside the mechanisms above.\n", len(a.Shadow)-8)
				break
			}
			fmt.Fprintf(&b, "| `%s` | %s | %s | **+%.4f** |\n",
				s.Arm, label(s.Mechanism), rate(s.Current), s.Gain)
		}
	}

	if len(a.RandomisedVsRule) > 0 {
		b.WriteString("\n**Randomising against dividing.** Total labelled events found, " +
			"and the worst mechanism's retained fraction.\n\n")
		b.WriteString("| Rule | Found | Alerts | Worst-case retained |\n|---|---|---|---|\n")
		for _, r := range a.RandomisedVsRule {
			worst := rate(r.WorstCase)
			note := ""
			if r.Note != "" {
				note = " *(" + r.Note + ")*"
			}
			fmt.Fprintf(&b, "| %s%s | %.0f | %d | %s |\n",
				r.Name, note, r.Detected, r.Alerts, worst)
		}
	}
	return b.String()
}

func labels(mechs []string) []string {
	out := make([]string, len(mechs))
	for i, m := range mechs {
		out[i] = label(m)
	}
	return out
}

// sortedByLabel keeps the retained columns in the matrix's own mechanism order, so this
// table's columns line up with the one above it.
func sortedByLabel(m map[string]float64, order []string) []string {
	var out []string
	for _, mech := range order {
		if _, ok := m[mech]; ok {
			out = append(out, mech)
		}
	}
	if len(out) == len(m) {
		return out
	}
	out = out[:0]
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func rate(v float64) string {
	if v == 0 {
		return "0"
	}
	return fmt.Sprintf("%.3f", v)
}

func delta(v float64) string {
	if v <= 1e-12 {
		return "none"
	}
	return fmt.Sprintf("**+%.3f**", v)
}

func mixture(s Solved) string {
	if len(s.Support) == 0 {
		return "—"
	}
	parts := make([]string, 0, len(s.Support))
	for _, arm := range s.Support {
		parts = append(parts, fmt.Sprintf("`%s` %.2f", arm, s.Mix[arm]))
	}
	return strings.Join(parts, ", ")
}

func weights(m map[string]float64) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if m[keys[i]] != m[keys[j]] {
			return m[keys[i]] > m[keys[j]]
		}
		return keys[i] < keys[j]
	})
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %.2f", label(k), m[k]))
	}
	return strings.Join(parts, ", ")
}
