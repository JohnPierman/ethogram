// Command dashboard renders an interactive evaluation dashboard from result JSON.
//
// It is the counterpart to cmd/report and keeps the same rule: no number appears unless
// it came out of a recorded run. Where cmd/report renders a fixed page per hypothesis,
// this command distils every run in the results directory into one embedded index and
// lets a reader move between runs, budgets and arms without regenerating anything —
// which is what makes it useful while measurements are still arriving.
//
// Three things it does that a static report cannot:
//
//   - it puts the framework's arms and the §12.4 baselines in ONE table, grouped by the
//     scope of the question each asks, because the project's central finding is that
//     per-entity detectors carried signal and population-scope ones did not;
//   - it breaks detection down by anomaly category, with population_rare marked as the
//     control it is, so an advantage is attributable to a KIND of anomaly rather than
//     asserted in aggregate;
//   - it computes integrity warnings from each run's own recorded numbers, so a run that
//     scored no background population announces itself instead of having to be noticed.
//
// The output embeds its data and loads no external resource, so it can be opened from
// disk, committed, and published without a server.
//
// The page carries no build timestamp on purpose: regenerating it from unchanged results
// must produce an unchanged file, or CI cannot tell a real change from a rebuild.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// hypotheses fixes the scoreboard: every E-id of §12.3 appears whether or not a result
// exists, which is what makes an unrun experiment visible rather than absent.
var hypotheses = []Hypothesis{
	{ID: "E1", Title: "Detections published baselines miss, at matched alert budget"},
	{ID: "E2", Title: "Lower alert volume at matched detection"},
	{ID: "E3", Title: "Realised FDR tracks nominal q"},
	{ID: "E4", Title: "Local conditioning against its single-block degeneration"},
	{ID: "E5", Title: "Composite validity under schema growth"},
	{ID: "E6", Title: "A source with unseen fields admitted without code change"},
	{ID: "E7", Title: "Verdicts analyst-verifiable from evidence alone"},
	{ID: "E8", Title: "Batch independence: identical events, differing batches, identical scores"},
	{ID: "E9", Title: "Circular representation against the 168-cell grid"},
}

// controlCategory is the category the framework is expected to show NO advantage in: it
// is what isolation-based detectors answer well, and it is retained so that an advantage
// elsewhere can be read against a row where none should appear.
const controlCategory = "population_rare"

func main() {
	var (
		resultsDir = flag.String("results", "results", "directory of result JSON files")
		outPath    = flag.String("out", "docs/dashboard.html", "dashboard output path")
		indexPath  = flag.String("index", "", "optional path to also write the raw index JSON")
		check      = flag.Bool("check", false,
			"verify the committed dashboard matches the results instead of writing it")
	)
	flag.Parse()

	index, err := build(*resultsDir)
	if err != nil {
		log.Fatal(err)
	}

	// -check is the CI gate. It compares in Go rather than shelling out to a diff tool,
	// so it behaves the same on every platform the project is developed on.
	if *check {
		if err := verifyCurrent(index, *outPath); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("dashboard up to date: %s matches %d result files\n",
			*outPath, len(index.Runs)+len(index.Baselines))
		return
	}

	if err := render(index, *outPath); err != nil {
		log.Fatal(err)
	}
	if *indexPath != "" {
		raw, err := json.MarshalIndent(index, "", " ")
		if err != nil {
			log.Fatal(err)
		}
		if err := os.WriteFile(*indexPath, raw, 0o644); err != nil {
			log.Fatal(err)
		}
	}
	log.Printf("wrote %s: %d runs, %d baseline files, budgets %v",
		*outPath, len(index.Runs), len(index.Baselines), index.Budgets)
}

// build reads every result file and distils the index.
func build(dir string) (Index, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return Index{}, err
	}
	sort.Strings(paths) // deterministic ordering, so a rebuild is a no-op

	type loaded struct {
		file string
		data map[string]any
	}
	var files []loaded
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			return Index{}, fmt.Errorf("read %s: %w", path, err)
		}
		var data map[string]any
		if err := json.Unmarshal(raw, &data); err != nil {
			return Index{}, fmt.Errorf("parse %s: %w", path, err)
		}
		// The same provenance gate cmd/report applies: a file without a schema version
		// and a run block is not a result, and rendering numbers from it would defeat
		// the point of the directory.
		for _, required := range []string{"schema_version", "run"} {
			if _, ok := data[required]; !ok {
				return Index{}, fmt.Errorf("%s: result file missing %q; refusing to "+
					"render numbers without provenance", path, required)
			}
		}
		files = append(files, loaded{file: filepath.Base(path), data: data})
	}

	documents := make([]map[string]any, 0, len(files))
	for _, f := range files {
		documents = append(documents, f.data)
	}

	index := Index{Budgets: discoverBudgets(documents)}

	// The synthetic ground truth is read first, in its own pass, because both the runs and
	// the baselines need it to attribute a detection to an attack type and the directory is
	// walked in name order — which does not put the taxonomy first.
	for _, f := range files {
		if strOf(f.data, "kind") == "attack-taxonomy" {
			index.AttackTypes = extractAttackTypes(f.file, f.data)
		}
	}
	victimType := map[string]string{}
	if index.AttackTypes != nil {
		victimType = index.AttackTypes.VictimType
	}

	for _, f := range files {
		switch strOf(f.data, "kind") {
		case "baselines":
			index.Baselines = append(index.Baselines,
				extractBaseline(f.file, f.data, victimType))
		case "replay":
			index.Runs = append(index.Runs,
				extractRun(f.file, f.data, index.Budgets, victimType))
		case "analysis":
			if taxonomy := extractTaxonomy(f.data); len(taxonomy) > 0 && len(index.Categories) == 0 {
				index.Categories = taxonomy
			}
		}
	}
	if len(index.Categories) == 0 {
		index.Categories = fallbackTaxonomy(index.Runs)
	}
	index.Hypotheses = scoreboard(documents)

	// Newest run first: the reader almost always wants the latest measurement, and
	// started_at is recorded on every run.
	sort.SliceStable(index.Runs, func(i, j int) bool {
		return index.Runs[i].Started > index.Runs[j].Started
	})
	sort.SliceStable(index.Baselines, func(i, j int) bool {
		return index.Baselines[i].RunID < index.Baselines[j].RunID
	})
	return index, nil
}

// scoreboard lists every hypothesis with the runs claiming it, so one with no run
// renders as NOT RUN rather than being omitted.
func scoreboard(documents []map[string]any) []Hypothesis {
	claims := map[string][]string{}
	for _, data := range documents {
		runID := strOf(mapOf(data, "run"), "run_id")
		for _, raw := range listOf(data, "hypothesis") {
			if id, ok := raw.(string); ok {
				claims[id] = append(claims[id], runID)
			}
		}
	}
	out := make([]Hypothesis, 0, len(hypotheses))
	for _, h := range hypotheses {
		h.Runs = claims[h.ID]
		sort.Strings(h.Runs)
		out = append(out, h)
	}
	return out
}

// discoverBudgets collects every alert budget any result recorded, so the page offers
// exactly the budgets that were measured rather than a hard-coded list that could
// silently disagree with the runs.
func discoverBudgets(all []map[string]any) []int {
	seen := map[int]bool{}
	var walk func(node any, depth int)
	walk = func(node any, depth int) {
		if depth > 6 {
			return
		}
		block, ok := node.(map[string]any)
		if !ok {
			return
		}
		for key, value := range block {
			if key == "detections_at_budget" {
				for name := range mapOf(block, key) {
					if budget, ok := budgetOf(name); ok {
						seen[budget] = true
					}
				}
				continue
			}
			walk(value, depth+1)
		}
	}
	for _, data := range all {
		walk(mapOf(data, "results"), 0)
	}
	out := make([]int, 0, len(seen))
	for budget := range seen {
		out = append(out, budget)
	}
	sort.Ints(out)
	return out
}

// extractTaxonomy reads the category descriptions out of an analysis result.
//
// The taxonomy is defined once, in cmd/analyse, and travels inside the results it
// produced. Re-declaring it here would create a second source of truth that could drift
// from the one the numbers were computed under.
func extractTaxonomy(data map[string]any) []Category {
	rows := listOf(mapOf(data, "results"), "category_comparison")
	if len(rows) == 0 {
		rows = listOf(mapOf(data, "results"), "category_census")
	}
	seen := map[string]bool{}
	var out []Category
	for _, raw := range rows {
		meta := mapOf(raw, "category")
		id := strOf(meta, "id")
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, Category{
			ID:        id,
			Test:      strOf(meta, "structural_test"),
			Contrast:  strOf(meta, "contrast_with_marginal_detectors"),
			IsControl: id == controlCategory,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// fallbackTaxonomy names the categories a run reported when no analysis result is
// present to describe them, so the page still renders rows rather than dropping them.
// extractAttackTypes reads a cmd/inject taxonomy document.
func extractAttackTypes(file string, data map[string]any) *AttackTypes {
	victims := map[string]string{}
	for account, raw := range mapOf(data, "victim_type") {
		if kind, ok := raw.(string); ok {
			victims[account] = kind
		}
	}
	if len(victims) == 0 {
		// Without the account-to-type map nothing can be attributed, and a panel of empty
		// columns reads as "no detections" rather than "no ground truth".
		return nil
	}
	premise := map[string]string{}
	for kind, raw := range mapOf(data, "premise") {
		if text, ok := raw.(string); ok {
			premise[kind] = text
		}
	}
	planted := map[string]int{}
	for kind, raw := range mapOf(data, "per_type") {
		planted[kind] = intOf(raw, "events")
	}

	// Any planted kind the recorded order omits is appended rather than dropped, so a
	// taxonomy written by an older injector still shows every column it has.
	var order []string
	seen := map[string]bool{}
	for _, raw := range listOf(data, "order") {
		if kind, ok := raw.(string); ok && planted[kind] > 0 && !seen[kind] {
			order = append(order, kind)
			seen[kind] = true
		}
	}
	remainder := make([]string, 0, len(planted))
	for kind := range planted {
		if !seen[kind] {
			remainder = append(remainder, kind)
		}
	}
	sort.Strings(remainder)
	order = append(order, remainder...)

	return &AttackTypes{
		RunID:      strOf(mapOf(data, "run"), "run_id"),
		File:       file,
		VictimType: victims,
		Premise:    premise,
		Planted:    planted,
		Order:      order,
		Note:       strOf(data, "note"),
	}
}

func fallbackTaxonomy(runs []Run) []Category {
	seen := map[string]bool{}
	for _, run := range runs {
		for id := range run.Categories {
			seen[id] = true
		}
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]Category, 0, len(ids))
	for _, id := range ids {
		out = append(out, Category{
			ID:        id,
			Test:      "described in the analysis result; none is present in this directory",
			IsControl: id == controlCategory,
		})
	}
	return out
}

// verifyCurrent fails when the committed page disagrees with the current results.
//
// The page carries no build timestamp, so rendering unchanged results reproduces the
// file byte for byte. A difference therefore means a result changed without the
// dashboard being regenerated, which would show a reader stale numbers underneath a
// current run's provenance footer — the one failure the whole provenance discipline
// exists to prevent.
func verifyCurrent(index Index, outPath string) error {
	want, err := page(index, outPath)
	if err != nil {
		return err
	}
	got, err := os.ReadFile(outPath)
	if err != nil {
		return fmt.Errorf("%s does not exist; run `make dashboard` and commit it: %w",
			outPath, err)
	}
	if !bytes.Equal(got, want) {
		return fmt.Errorf("%s is stale: it does not match the current results. "+
			"Run `make dashboard` and commit the result", outPath)
	}
	return nil
}

// render writes the page with the index embedded.
func render(index Index, outPath string) error {
	body, err := page(index, outPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(outPath, body, 0o644)
}

// page builds the rendered bytes without writing them, so the writer and the checker
// cannot drift apart.
func page(index Index, outPath string) ([]byte, error) {
	payload, err := json.Marshal(index)
	if err != nil {
		return nil, err
	}
	// The payload sits inside a <script> block, so a literal "</script>" in any recorded
	// note would end the block early and break the page. encoding/json escapes <, > and &
	// as <, > and & by default, which makes that unrepresentable — but the
	// page's correctness then rests on a library default rather than on anything visible
	// here, so it is asserted rather than assumed.
	if bytes.Contains(payload, []byte("</")) {
		return nil, fmt.Errorf("refusing to write %s: the index contains an unescaped %q, "+
			"which would terminate the script block early; encoding/json's HTML escaping "+
			"appears to be off", outPath, "</")
	}
	return []byte(strings.Replace(dashboardTemplate, "/*DATA*/", string(payload), 1)), nil
}
