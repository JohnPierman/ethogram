// Command partii renders Part II of the whitepaper, "Measured Performance", from the
// committed result files.
//
// Part I fixes the framework, the derivations and the evaluation design, and states in
// its abstract, §12 and §16 that the measurements appear in Part II. This command
// produces that document rather than editing Part I, so Part I's scope statements stay
// true and the reviewed manuscript is left as it was passed.
//
// The document carries no hand-typed numbers. Every figure in it is read from
// results/*.json, and a hypothesis whose result file is absent renders as an explicit
// "not yet measured" line naming what is missing. The same rule that governs the
// dashboard governs the paper: a number that did not come out of a recorded run does
// not appear.
//
// Visual identity is lifted from Part I's own stylesheet — Letter page at 22/21 mm,
// 10.2 pt Georgia justified, the same heading scale, the same design tokens — so the
// two documents sit beside each other without looking foreign.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func main() {
	var (
		resultsDir = flag.String("results", "results", "directory of result JSON files")
		outPath    = flag.String("out", "", "output HTML path (required)")
	)
	flag.Parse()
	if *outPath == "" {
		log.Fatal("-out is required")
	}
	if err := render(*resultsDir, *outPath); err != nil {
		log.Fatal(err)
	}
}

type result struct {
	File string
	Data map[string]any
}

// findings is everything the document reports, assembled from the result files.
type findings struct {
	Generated string
	Sources   []sourceRow
	Sections  []section
	Measured  int
	Pending   int
}

type sourceRow struct {
	RunID, File, GitSHA, Coverage, Corpus string
	Dirty                                 bool
}

// section is one hypothesis's write-up. Body paragraphs and tables are built only from
// values found in the results; Pending carries the reason when nothing was found.
type section struct {
	ID, Title string
	Pending   string
	Paras     []string
	Table     *table
	// Table2 and Caveats exist for the head-to-head section, whose design calls for
	// two tables (aggregate and per-category) and a visible list of the caveats that
	// qualify the claim.
	Table2 *table
	// Table3 carries the taxonomy: for each category, the structural test that
	// defines it and the reason a marginal, batch-standardised detector cannot
	// express it. It is the argument the per-category numbers are evidence for, and
	// it is read from the result file like everything else rather than written here.
	Table3  *table
	Caveats []string
	Prov    string
}

type table struct {
	Caption string
	Head    []string
	Rows    [][]string
}

func render(resultsDir, outPath string) error {
	results, err := load(resultsDir)
	if err != nil {
		return err
	}

	f := findings{Generated: time.Now().UTC().Format("2 January 2006")}
	for _, r := range results {
		f.Sources = append(f.Sources, sourceOf(r))
	}

	f.Sections = []section{
		sectionE8(results),
		sectionE7(results),
		sectionE6(results),
		sectionControls(results),
		sectionE1E2(results),
		sectionE3(results),
		sectionE4(results),
		sectionE5(results),
		sectionE9(results),
	}
	for _, s := range f.Sections {
		if s.Pending == "" {
			f.Measured++
		} else {
			f.Pending++
		}
	}

	var buf strings.Builder
	if err := paperTemplate.Execute(&buf, f); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(outPath, []byte(buf.String()), 0o644); err != nil { //nolint:gosec // a document meant to be read
		return err
	}
	log.Printf("wrote %s from %d result files: %d sections measured, %d pending",
		outPath, len(results), f.Measured, f.Pending)
	return nil
}

func load(dir string) ([]result, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	out := make([]result, 0, len(paths))
	for _, p := range paths {
		raw, err := os.ReadFile(p) //nolint:gosec // the results directory the flag names
		if err != nil {
			return nil, err
		}
		var data map[string]any
		if err := json.Unmarshal(raw, &data); err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		out = append(out, result{File: filepath.Base(p), Data: data})
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Lookup helpers. Every one returns ok=false when the value is absent, so a missing
// measurement becomes a pending line rather than a zero.
// ---------------------------------------------------------------------------

func mapAt(m map[string]any, path ...string) (map[string]any, bool) {
	cur := m
	for _, k := range path {
		next, ok := cur[k].(map[string]any)
		if !ok {
			return nil, false
		}
		cur = next
	}
	return cur, true
}

func numAt(m map[string]any, path ...string) (float64, bool) {
	if len(path) == 0 {
		return 0, false
	}
	parent, ok := mapAt(m, path[:len(path)-1]...)
	if !ok {
		return 0, false
	}
	v, ok := parent[path[len(path)-1]].(float64)
	return v, ok
}

func strAt(m map[string]any, path ...string) (string, bool) {
	if len(path) == 0 {
		return "", false
	}
	parent, ok := mapAt(m, path[:len(path)-1]...)
	if !ok {
		return "", false
	}
	v, ok := parent[path[len(path)-1]].(string)
	return v, ok
}

func boolAt(m map[string]any, path ...string) (bool, bool) {
	if len(path) == 0 {
		return false, false
	}
	parent, ok := mapAt(m, path[:len(path)-1]...)
	if !ok {
		return false, false
	}
	v, ok := parent[path[len(path)-1]].(bool)
	return v, ok
}

// byKind returns the first result of a given kind, in filename order.
func byKind(results []result, kind string) (result, bool) {
	for _, r := range results {
		if k, _ := r.Data["kind"].(string); k == kind {
			return r, true
		}
	}
	return result{}, false
}

// claiming returns the first result claiming a hypothesis.
func claiming(results []result, id string) (result, bool) {
	for _, r := range results {
		list, ok := r.Data["hypothesis"].([]any)
		if !ok {
			continue
		}
		for _, v := range list {
			if s, ok := v.(string); ok && s == id {
				return r, true
			}
		}
	}
	return result{}, false
}

func provOf(r result) string {
	runID, _ := strAt(r.Data, "run", "run_id")
	sha, _ := strAt(r.Data, "run", "git_sha")
	if len(sha) > 12 {
		sha = sha[:12]
	}
	dirty, _ := boolAt(r.Data, "run", "git_dirty")
	mark := ""
	if dirty {
		mark = ", working tree dirty"
	}
	return fmt.Sprintf("Source: %s, run %s, commit %s%s.", r.File, runID, sha, mark)
}

func sourceOf(r result) sourceRow {
	row := sourceRow{File: r.File}
	row.RunID, _ = strAt(r.Data, "run", "run_id")
	sha, _ := strAt(r.Data, "run", "git_sha")
	if len(sha) > 12 {
		sha = sha[:12]
	}
	row.GitSHA = sha
	row.Dirty, _ = boolAt(r.Data, "run", "git_dirty")
	if s, ok := strAt(r.Data, "corpus", "coverage", "statement"); ok {
		row.Coverage = s
	} else if s, ok := strAt(r.Data, "corpus", "coverage", "kind"); ok {
		row.Coverage = s
	} else {
		row.Coverage = "—"
	}
	if files, ok := r.Data["corpus"].(map[string]any); ok {
		if list, ok := files["files"].([]any); ok && len(list) > 0 {
			if fm, ok := list[0].(map[string]any); ok {
				if p, ok := fm["path"].(string); ok {
					row.Corpus = filepath.Base(p)
				}
			}
		}
		if row.Corpus == "" {
			if syn, ok := files["synthetic"].(string); ok && syn != "" {
				row.Corpus = "synthetic (control)"
			}
		}
	}
	if row.Corpus == "" {
		if _, ok := r.Data["input"]; ok {
			row.Corpus = "derived from an exported feature file"
		} else {
			row.Corpus = "—"
		}
	}
	return row
}

func fmtInt(v float64) string {
	s := fmt.Sprintf("%.0f", v)
	// Thousands separators, for figures a reader must be able to scan.
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

var paperTemplate = template.Must(template.New("partii").Parse(paperHTML))
