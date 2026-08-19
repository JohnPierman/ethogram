package main

// Formatting helpers. A value is only ever formatted, never invented: every function
// here takes a figure that was read out of the result file, and notRecorded is the
// one sentence written when the read failed.

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// notRecorded is the sentence the report writes for an absent key. It names the key,
// so a reader can check the claim against result.json sitting beside the report.
func notRecorded(key string) string {
	return fmt.Sprintf("not recorded (`%s` is absent)", key)
}

// fmtInt renders a whole recorded figure with thousands separators, for figures a
// reader must be able to scan.
func fmtInt(v float64) string {
	s := fmt.Sprintf("%.0f", v)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var parts []string
	for len(s) > 3 {
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	parts = append([]string{s}, parts...)
	out := strings.Join(parts, ",")
	if neg {
		out = "-" + out
	}
	return out
}

// fmtNum renders a recorded number: whole values with thousands separators, anything
// else round-tripped so the page shows the file's own value rather than a rounding.
func fmtNum(v float64) string {
	if v == math.Trunc(v) && math.Abs(v) < 1e15 {
		return fmtInt(v)
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// fmtSig renders a measured value to three significant figures.
func fmtSig(v float64) string { return strconv.FormatFloat(v, 'g', 3, 64) }

// pct renders a proportion read from a result file as a percentage with one decimal.
func pct(v float64) string { return strconv.FormatFloat(100*v, 'f', 1, 64) }

// shortDigest truncates a recorded digest for the page; the file carries it in full.
func shortDigest(d string) string {
	const shown = 12
	if len(d) <= shown {
		return d
	}
	return d[:shown] + "…"
}

// escapeCell keeps a recorded string from breaking the Markdown table quoting it.
func escapeCell(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	return strings.ReplaceAll(s, "\n", " ")
}

// appendUnique appends items not already present, preserving first-seen order, so an
// explanation shared by every row renders once.
func appendUnique(dst []string, items ...string) []string {
	for _, it := range items {
		if it == "" {
			continue
		}
		seen := false
		for _, have := range dst {
			if have == it {
				seen = true
				break
			}
		}
		if !seen {
			dst = append(dst, it)
		}
	}
	return dst
}
