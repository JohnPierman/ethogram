// Command budgetcurve turns a framework replay and a baselines run into §1.1's figure:
// one line per detector through its own operating points, across the whole budget range.
//
// # Why a command rather than a hand-drawn figure
//
// The figure it replaces was typed. It carried one arithmetic curve, a band for where
// published detectors operate, and two rules for this framework's own operating points, so
// a reader could see the base-rate argument but not how the framework compares to a named
// method at a budget they care about. Five lines through measured points answer that, and a
// hundred coordinates cannot be kept correct by hand across a re-run.
//
// # What it will not do
//
// It refuses to plot two detectors that did not read the same events. The whole point of
// §1.1's axes is that a comparison is made at a matched number of alerts per analyst-day;
// putting lines from different windows or different samplings on shared axes would assert a
// comparability the data does not support, which is the error the section exists to warn
// against. So the framework's scored-event count and labelled count are checked against the
// baselines' own recorded population before anything is drawn.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"
)

func main() {
	log.SetFlags(0)
	log.SetPrefix("budgetcurve: ")

	runPath := flag.String("run", "", "framework replay result JSON, recorded at the wide budget set (required)")
	basePath := flag.String("baselines", "", "baselines result JSON over the same slice (required)")
	outPath := flag.String("out", "", "curve data JSON (required)")
	svgPath := flag.String("svg", "", "figure SVG (required)")
	runID := flag.String("run-id", "", "identifier for this derivation (required)")
	methods := flag.String("methods", "", "comma-separated baseline methods to draw; empty selects the strongest four by total detections")
	arms := flag.String("arms", "composite", "comma-separated framework series to draw. `composite` is the combined verdict; any other name is a per-detector arm, for instance `novelty`. Each is a FIXED arm across every budget: picking the best arm per budget would be choosing with the evaluation labels in hand, which is an oracle rather than a deployable configuration. Drawing the composite beside a single arm is deliberate where they disagree -- omitting the weaker one would overstate the framework")
	tolerance := flag.Float64("population-tolerance", 0.01, "the largest relative difference between the framework's scored events and the baselines' estimated population that still counts as the same slice")
	flag.Parse()

	if *runPath == "" || *basePath == "" || *outPath == "" || *svgPath == "" || *runID == "" {
		flag.Usage()
		log.Fatal("-run, -baselines, -out, -svg and -run-id are required")
	}
	if err := run(*runPath, *basePath, *outPath, *svgPath, *runID, *methods, *arms, *tolerance); err != nil {
		log.Fatal(err)
	}
}

// point is one detector at one budget.
type point struct {
	Budget    int     `json:"budget"`
	Alerts    int     `json:"alerts"`
	TP        int     `json:"true_positives"`
	Precision float64 `json:"precision"`
	Alpha     float64 `json:"false_alarm_rate"`
}

type series struct {
	Name   string  `json:"name"`
	Label  string  `json:"label"`
	Ours   bool    `json:"ours"`
	Points []point `json:"points"`
}

func run(runPath, basePath, outPath, svgPath, runID, methodSpec, armSpec string, tolerance float64) error {
	started := time.Now().UTC()

	var fw, bl map[string]any
	if err := readJSON(runPath, &fw); err != nil {
		return err
	}
	if err := readJSON(basePath, &bl); err != nil {
		return err
	}

	corpus, _ := fw["corpus"].(map[string]any)
	scored := int(numOf(corpus["events_scored"]))
	if scored == 0 {
		return fmt.Errorf("%s records no events_scored", runPath)
	}

	fwResults, _ := fw["results"].(map[string]any)
	armNames := splitSpec(armSpec)
	if len(armNames) == 0 {
		return fmt.Errorf("-arms named nothing to draw")
	}
	armDets := make(map[string]map[string]any, len(armNames))
	for _, name := range armNames {
		if name == "composite" {
			det, _ := fwResults["detections_at_budget"].(map[string]any)
			if len(det) == 0 {
				return fmt.Errorf("%s has no top-level detections_at_budget", runPath)
			}
			armDets[name] = det
			continue
		}
		armsBlock, _ := fwResults["detector_arms"].(map[string]any)
		byName, _ := armsBlock["arms"].(map[string]any)
		one, ok := byName[name].(map[string]any)
		if !ok {
			return fmt.Errorf("%s records no arm named %q", runPath, name)
		}
		det, _ := one["detections_at_budget"].(map[string]any)
		if len(det) == 0 {
			return fmt.Errorf("%s: arm %q has no detections_at_budget", runPath, name)
		}
		armDets[name] = det
	}
	fwDet := armDets[armNames[0]]

	// The labelled population, taken from the framework's own accounting.
	labelled := 0
	for _, v := range fwDet {
		cell, _ := v.(map[string]any)
		labelled = int(numOf(cell["red_team_total"]))
		break
	}
	if labelled == 0 {
		return fmt.Errorf("%s records no labelled events", runPath)
	}
	background := scored - labelled
	if background <= 0 {
		return fmt.Errorf("%s: %d scored events against %d labelled", runPath, scored, labelled)
	}

	// Same slice, or refuse. A figure that puts these lines on shared axes is asserting
	// that they scored the same events.
	blInput, _ := bl["input"].(map[string]any)
	blRows := numOf(blInput["rows_sample"])
	blParams, _ := bl["parameters"].(map[string]any)
	mult := numOf(blParams["sample_rate"])
	if mult == 0 {
		mult = numOf(blParams["population_multiplier"])
	}
	estimated := blRows * mult
	if estimated == 0 {
		return fmt.Errorf("%s: cannot establish the baselines' population from rows_sample and sample_rate", basePath)
	}
	drift := math.Abs(estimated-float64(scored)) / float64(scored)
	if drift > tolerance {
		return fmt.Errorf("the baselines estimate a population of %.0f events where the framework scored %d, "+
			"a relative difference of %.2f%% above the %.2f%% tolerance; these are not the same slice and "+
			"must not be drawn on shared axes", estimated, scored, drift*100, tolerance*100)
	}
	blRed := int(numOf(blInput["rows_redteam"]))
	if blRed != labelled {
		return fmt.Errorf("the baselines read %d labelled events and the framework scored %d; not the same ground truth",
			blRed, labelled)
	}

	// Days, from the baselines' own window, so a baseline's alert count is budget x days.
	dayFrom, dayTo := int(numOf(blParams["days_from"])), int(numOf(blParams["days_to"]))
	days := dayTo - dayFrom
	if days <= 0 {
		return fmt.Errorf("%s: window days_from=%d days_to=%d", basePath, dayFrom, dayTo)
	}

	oursAll := make([]series, 0, len(armNames))
	for _, name := range armNames {
		det := armDets[name]
		s := series{Name: "ethogram/" + name, Label: frameworkLabel(name), Ours: true}
		for _, b := range sortedBudgets(det) {
			cell, _ := det[budgetKey(b)].(map[string]any)
			s.Points = append(s.Points, newPoint(b, int(numOf(cell["alerts"])), int(numOf(cell["true_positives"])), background))
		}
		oursAll = append(oursAll, s)
	}

	blResults, _ := bl["results"].(map[string]any)
	all := make([]series, 0, len(blResults))
	for name, v := range blResults {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		det, ok := m["detections_at_budget"].(map[string]any)
		if !ok {
			continue
		}
		s := series{Name: name, Label: name}
		for _, b := range sortedBudgets(det) {
			cell, _ := det[budgetKey(b)].(map[string]any)
			tp := int(numOf(cell["detections"]))
			s.Points = append(s.Points, newPoint(b, b*days, tp, background))
		}
		if len(s.Points) > 0 {
			all = append(all, s)
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })

	chosen, rejected := selectMethods(all, methodSpec)
	drawn := append(append([]series{}, oursAll...), chosen...)

	svg, err := renderSVG(drawn, rejected, scored, labelled, days)
	if err != nil {
		return err
	}
	if err := os.WriteFile(svgPath, []byte(svg), 0o644); err != nil { //nolint:gosec // the output the flag names
		return fmt.Errorf("write svg: %w", err)
	}

	out := map[string]any{
		"schema_version": "1",
		"kind":           "budget-curve",
		"hypothesis": []string{
			"the framework's advantage over published detectors is a function of the alert " +
				"budget, and is reported across the range rather than at one operating point",
		},
		"paper_refs": map[string]any{
			"sections": []string{"§1.1", "§5.7", "§12.4"},
			"issues":   []string{"#29"},
		},
		"run": map[string]any{
			"run_id":         runID,
			"framework_run":  runIDOf(fw),
			"framework_file": runPath,
			"baselines_run":  runIDOf(bl),
			"baselines_file": basePath,
			"started_at":     started.Format(time.RFC3339),
			"finished_at":    time.Now().UTC().Format(time.RFC3339),
			"go_version":     runtime.Version(),
		},
		"corpus": corpus,
		"parameters": map[string]any{
			"scored_events":                  scored,
			"labelled_events":                labelled,
			"background_events":              background,
			"base_rate":                      float64(labelled) / float64(scored),
			"scored_days":                    days,
			"baselines_estimated_population": estimated,
			"population_relative_difference": drift,
			"population_tolerance":           tolerance,
			"precision":                      "true positives over alerts emitted, both from the method's own top-budget-per-day queue",
			"alpha":                          "false alerts over background events; the corpus has no true negative class (§12.5), so every unlabelled alert counts as false and alpha is an upper bound",
			"selection":                      selectionNote(methodSpec),
			"framework_arms":                 armNames,
			"arm_selection": "a single arm held fixed across every budget. Selecting the best " +
				"arm at each budget would be selecting with the evaluation labels in hand, which " +
				"bounds what an oracle could reach and is not a configuration anyone can deploy",
		},
		"results": map[string]any{
			"drawn":    drawn,
			"measured": all,
			"rejected": rejected,
		},
	}
	if err := writeJSON(outPath, out); err != nil {
		return err
	}
	log.Printf("wrote %s and %s: %d series, %d budgets", outPath, svgPath, len(drawn), len(oursAll[0].Points))
	return nil
}

func frameworkLabel(name string) string {
	if name == "composite" {
		return "this framework, composite"
	}
	return "this framework, " + name + " arm"
}

// splitSpec parses a comma-separated flag into trimmed, non-empty names.
func splitSpec(spec string) []string {
	out := make([]string, 0, 4)
	for _, part := range strings.Split(spec, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func newPoint(budget, alerts, tp, background int) point {
	p := point{Budget: budget, Alerts: alerts, TP: tp}
	if alerts > 0 {
		p.Precision = float64(tp) / float64(alerts)
	}
	if background > 0 {
		p.Alpha = float64(alerts-tp) / float64(background)
	}
	return p
}

// selectMethods keeps the named methods, or the strongest four by total detections when no
// names are given. Strongest rather than arbitrary: a comparison against the weakest
// available baselines would flatter the framework by choice of comparator.
func selectMethods(all []series, spec string) (chosen, rejected []series) {
	if strings.TrimSpace(spec) != "" {
		want := map[string]bool{}
		for _, n := range strings.Split(spec, ",") {
			want[strings.TrimSpace(n)] = true
		}
		for _, s := range all {
			if want[s.Name] {
				chosen = append(chosen, s)
			} else {
				rejected = append(rejected, s)
			}
		}
		return chosen, rejected
	}

	byStrength := make([]series, len(all))
	copy(byStrength, all)
	sort.SliceStable(byStrength, func(i, j int) bool {
		ti, tj := totalTP(byStrength[i]), totalTP(byStrength[j])
		if ti != tj {
			return ti > tj
		}
		return byStrength[i].Name < byStrength[j].Name
	})
	n := min(4, len(byStrength))
	chosen = byStrength[:n]
	rejected = byStrength[n:]
	sort.Slice(chosen, func(i, j int) bool { return chosen[i].Name < chosen[j].Name })
	sort.Slice(rejected, func(i, j int) bool { return rejected[i].Name < rejected[j].Name })
	return chosen, rejected
}

func totalTP(s series) int {
	t := 0
	for _, p := range s.Points {
		t += p.TP
	}
	return t
}

func selectionNote(spec string) string {
	if strings.TrimSpace(spec) != "" {
		return "methods named on the command line: " + spec
	}
	return "the four baselines with the most detections summed over the budget range, so the " +
		"comparison is against the strongest available comparators rather than the weakest"
}

func sortedBudgets(det map[string]any) []int {
	out := make([]int, 0, len(det))
	for k := range det {
		var b int
		if _, err := fmt.Sscanf(k, "budget_%d_per_day", &b); err == nil {
			out = append(out, b)
		}
	}
	sort.Ints(out)
	return out
}

func budgetKey(b int) string { return fmt.Sprintf("budget_%d_per_day", b) }

func numOf(v any) float64 {
	f, _ := v.(float64)
	return f
}

func runIDOf(m map[string]any) any {
	r, ok := m["run"].(map[string]any)
	if !ok {
		return nil
	}
	return r["run_id"]
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path) //nolint:gosec // the input the flag names
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}

func writeJSON(path string, v any) error {
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
