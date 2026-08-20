// Command volumegate turns the volume_gate_probe block of an ungated replay into the
// committed decision record for #25: which completed-period threshold the volume arm's
// R3 abstention is set to, and what every rejected candidate would have done instead.
//
// # Why this is a separate artefact
//
// The probe can only measure candidates at or above the threshold the run itself armed,
// because a gated event carries no p-value. So the run that DECIDES the threshold must be
// ungated, and the runs that USE it are not. Keeping the decision in its own result file
// means the choice stays legible after the production runs have moved on, and the options
// that were not taken stay on the record beside the one that was.
//
// # The decision rule, stated rather than implied
//
// Smallest candidate that moves the realised cut off the 1e-12 floor at EVERY budget,
// subject to abstaining on no more than -max-abstain-share of events. Both halves matter:
// a gate that does not clear the floor has not fixed the queue, and one that abstains on
// most events has made the arm silent rather than correct. The rule is recorded in the
// output, and the chosen value is recorded beside the rule's own recommendation so a
// disagreement between them is visible rather than buried.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"
)

func main() {
	log.SetFlags(0)
	log.SetPrefix("volumegate: ")

	runPath := flag.String("run", "", "ungated replay result JSON (required)")
	outPath := flag.String("out", "", "decision result JSON (required)")
	runID := flag.String("run-id", "", "identifier for this analysis (required)")
	choose := flag.Int64("choose", -1, "the completed-period threshold to adopt; -1 records the rule's recommendation without adopting one")
	maxShare := flag.Float64("max-abstain-share", 0.10, "the largest share of events a candidate may abstain on and still be admissible")
	flag.Parse()

	if *runPath == "" || *outPath == "" || *runID == "" {
		flag.Usage()
		log.Fatal("-run, -out and -run-id are required")
	}
	if err := run(*runPath, *outPath, *runID, *choose, *maxShare); err != nil {
		log.Fatal(err)
	}
}

func run(runPath, outPath, runID string, choose int64, maxShare float64) error {
	started := time.Now().UTC()

	raw, err := os.ReadFile(runPath) //nolint:gosec // the input the flag names
	if err != nil {
		return fmt.Errorf("read run: %w", err)
	}
	var parent map[string]any
	if err := json.Unmarshal(raw, &parent); err != nil {
		return fmt.Errorf("parse run: %w", err)
	}

	results, ok := parent["results"].(map[string]any)
	if !ok {
		return fmt.Errorf("%s: no results block", runPath)
	}
	probe, ok := results["volume_gate_probe"].(map[string]any)
	if !ok {
		return fmt.Errorf("%s: no volume_gate_probe block; the run predates #25's instrument", runPath)
	}
	if measured, _ := probe["measured"].(bool); !measured {
		return fmt.Errorf("%s: probe reports measured=false (%v)", runPath, probe["reason"])
	}
	if armed := numberOf(probe["armed_min_periods"]); armed != 0 {
		return fmt.Errorf("%s armed the gate at %v, so candidates below it carry no p-value; "+
			"the deciding run must be ungated (-volume-min-periods 0)", runPath, armed)
	}

	rows, ok := probe["candidates"].([]any)
	if !ok {
		return fmt.Errorf("%s: probe has no candidates", runPath)
	}

	verdicts := make([]map[string]any, 0, len(rows))
	var recommended *int64
	for _, r := range rows {
		row, rowOK := r.(map[string]any)
		if !rowOK {
			continue
		}
		mp := int64(numberOf(row["min_periods"]))
		clears, budgets := clearsFloorEverywhere(row)
		share := numberOf(row["abstained_share"])
		admissible := mp > 0 && clears && share <= maxShare

		v := map[string]any{
			"min_periods":                  mp,
			"clears_floor_at_every_budget": clears,
			"budgets_off_the_floor":        budgets,
			"abstained_share":              share,
			"within_share_limit":           share <= maxShare,
			"admissible":                   admissible,
			"measurement":                  row,
		}
		verdicts = append(verdicts, v)
		if admissible && recommended == nil {
			m := mp
			recommended = &m
		}
	}

	decision := map[string]any{
		"rule": "smallest candidate that moves the realised cut off the " +
			"1e-12 floor at every budget, subject to abstaining on no more than " +
			"max_abstain_share of events. A gate that does not clear the floor has not " +
			"fixed the queue; one that abstains on most events has made the arm silent " +
			"rather than correct",
		"max_abstain_share":     maxShare,
		"candidates_considered": candidateList(verdicts),
	}
	if recommended != nil {
		decision["recommended"] = *recommended
	} else {
		decision["recommended"] = nil
		decision["recommendation_note"] = "no candidate satisfied the rule; the measurement " +
			"is the result and the arm's zeros are not explained by the abstention alone"
	}
	if choose >= 0 {
		decision["chosen"] = choose
		decision["agrees_with_recommendation"] = recommended != nil && *recommended == choose
		if recommended == nil || *recommended != choose {
			decision["divergence_note"] = "the adopted threshold is not the rule's " +
				"recommendation; the reason belongs in the CHANGELOG and the paper, not here"
		}
	} else {
		decision["chosen"] = nil
	}

	out := map[string]any{
		"schema_version": "1",
		"kind":           "volume-abstention-gate",
		"hypothesis": []string{
			"R3: a detector with no basis for an opinion abstains rather than reporting " +
				"its prior's tail as the entity's",
		},
		"paper_refs": map[string]any{
			"sections":  []string{"§5.1", "§5.3", "§6.2", "§7.4"},
			"equations": []int{10, 11},
			"issues":    []string{"#24", "#25"},
		},
		"run": map[string]any{
			"run_id":      runID,
			"parent_run":  parentRunID(parent),
			"parent_file": runPath,
			"started_at":  started.Format(time.RFC3339),
			"finished_at": time.Now().UTC().Format(time.RFC3339),
			"go_version":  runtime.Version(),
		},
		"corpus": parent["corpus"],
		"parameters": map[string]any{
			"threshold_unit":      probe["threshold_unit"],
			"definition":          probe["definition"],
			"miscalibrated_below": probe["miscalibrated_below"],
			"max_abstain_share":   maxShare,
		},
		"results": map[string]any{
			"volume_events":      probe["volume_events"],
			"evaluated":          probe["evaluated"],
			"sub_1e-12_ungated":  probe["sub_1e-12_ungated"],
			"labelled_evaluated": probe["labelled_evaluated"],
			"candidates":         verdicts,
			"decision":           decision,
		},
		"provenance_complete": parent["provenance_complete"],
	}

	if err := writeJSON(outPath, out); err != nil {
		return err
	}
	log.Printf("wrote %s", outPath)
	return nil
}

// clearsFloorEverywhere reports whether every budget's realised cut sits above the
// miscalibrated floor, and names the budgets that do.
func clearsFloorEverywhere(row map[string]any) (bool, []string) {
	at, ok := row["at_budget"].(map[string]any)
	if !ok || len(at) == 0 {
		return false, nil
	}
	off := make([]string, 0, len(at))
	all := true
	for name, v := range at {
		cell, cellOK := v.(map[string]any)
		if !cellOK {
			all = false
			continue
		}
		if offFloor, _ := cell["off_the_1e-12_floor"].(bool); offFloor {
			off = append(off, name)
			continue
		}
		all = false
	}
	sort.Strings(off)
	return all, off
}

func candidateList(verdicts []map[string]any) []int64 {
	out := make([]int64, 0, len(verdicts))
	for _, v := range verdicts {
		out = append(out, v["min_periods"].(int64))
	}
	return out
}

func numberOf(v any) float64 {
	f, _ := v.(float64)
	return f
}

func parentRunID(parent map[string]any) any {
	r, ok := parent["run"].(map[string]any)
	if !ok {
		return nil
	}
	return r["run_id"]
}

func writeJSON(path string, v any) error {
	dir := "."
	if idx := strings.LastIndexAny(path, `/\`); idx >= 0 {
		dir = path[:idx]
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.Create(path) //nolint:gosec // the output the flag names
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", " ")
	if err := enc.Encode(v); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
