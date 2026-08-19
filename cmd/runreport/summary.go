package main

// The machine-readable digest written beside the report. Only recorded values appear:
// an absent key is omitted rather than written as a zero, so a consumer can tell
// "not measured" apart from "measured as nothing".

import (
	"encoding/json"
	"fmt"
)

func renderSummary(data map[string]any) ([]byte, error) {
	s := map[string]any{}
	putStr := func(name string, path ...string) {
		if v, ok := strAt(data, path...); ok && v != "" {
			s[name] = v
		}
	}
	putStr("run_id", "run", "run_id")
	putStr("kind", "kind")
	putStr("git_sha", "run", "git_sha")
	if v, ok := boolAt(data, "run", "git_dirty"); ok {
		s["git_dirty"] = v
	}
	if hs := stringItems(data["hypothesis"]); len(hs) > 0 {
		s["hypotheses"] = hs
	}
	putStr("coverage", "corpus", "coverage", "statement")
	if _, ok := s["coverage"]; !ok {
		putStr("coverage", "corpus", "coverage", "kind")
	}
	counts := map[string]any{}
	for _, k := range []string{"rows_read", "events_warmed", "events_scored", "events_skipped", "row_errors"} {
		if v, ok := numAt(data, "corpus", k); ok {
			counts[k] = v
		}
	}
	if len(counts) > 0 {
		s["counts"] = counts
	}
	if v, ok := numAt(data, "runtime", "wall_seconds"); ok {
		s["wall_seconds"] = v
	}
	out, err := json.MarshalIndent(s, "", " ")
	if err != nil {
		return nil, fmt.Errorf("marshal summary: %w", err)
	}
	return append(out, '\n'), nil
}
