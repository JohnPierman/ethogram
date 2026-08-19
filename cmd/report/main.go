// Command report renders the static evaluation report from result JSON files.
//
// The one rule this command exists to enforce: no number appears in the report unless
// it came out of a real run. The renderer reads ONLY results/*.json written by the
// experiment commands; a hypothesis with no result file renders as a literal NOT RUN
// card, never blank, never zero, never omitted from the scoreboard; and every figure
// and table carries a provenance footer naming the run that produced it.
//
// With -verify-provenance the command checks instead of rendering: every figure in the
// figures directory must be listed in the manifest the renderer wrote, every manifest
// entry must name a result file that exists and is provenance-complete, and any orphan
// fails the build.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// hypotheses fixes the scoreboard: every E-id of §12.3 appears whether or not any
// result exists, which is what makes an unrun experiment visible rather than absent.
var hypotheses = []struct {
	ID, Title string
}{
	{"E1", "Detections published baselines miss, at matched alert budget"},
	{"E2", "Lower alert volume at matched detection"},
	{"E3", "Realised FDR tracks nominal q; conservatism bounded and quantified"},
	{"E4", "Local conditioning (14) against its single-block degeneration (15)"},
	{"E5", "Composite validity under schema growth (treatments A/B/C)"},
	{"E6", "A source with unseen fields admitted without code change"},
	{"E7", "Verdicts analyst-verifiable from evidence alone"},
	{"E8", "Batch-independence: identical events, differing batches, identical scores"},
	{"E9", "Circular representation against the 168-cell grid"},
}

type resultFile struct {
	Path string
	Data map[string]any
}

func main() {
	var (
		resultsDir = flag.String("results", "results", "directory of result JSON files")
		figuresDir = flag.String("figures", "docs/figures", "directory for SVG figures")
		outPath    = flag.String("out", "docs/report.html", "report output path")
		verify     = flag.Bool("verify-provenance", false, "verify figures against results and exit")
		check      = flag.Bool("check", false, "verify the committed figures and report match what the current results would render, and exit")
	)
	flag.Parse()

	results, err := loadResults(*resultsDir)
	if err != nil {
		log.Fatal(err)
	}

	if *verify {
		if err := verifyProvenance(results, *figuresDir); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("provenance verified: %d result files, figures directory %s\n",
			len(results), *figuresDir)
		return
	}

	if *check {
		if err := checkCurrent(results, *figuresDir, *outPath); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("figures and report are current with %d result files\n", len(results))
		return
	}

	if err := render(results, *figuresDir, *outPath); err != nil {
		log.Fatal(err)
	}
	log.Printf("wrote %s from %d result files", *outPath, len(results))
}

// isRenderedArtefact reports whether a file in the figures directory is one this renderer
// owns. Only these are held to the currency check.
func isRenderedArtefact(name string) bool {
	return strings.HasSuffix(name, ".svg") || name == "manifest.json"
}

// checkCurrent fails when the committed figures or report differ from what the current
// results would render.
//
// # Why this exists
//
// docs/dashboard.html and docs/paper.html have had currency gates for some time; the
// figures did not, and the gap was not theoretical. Running the renderer as a diagnostic
// silently rewrote four committed SVGs and produced two the repository had never held —
// which means the committed figures had been stale against the committed results, and
// nothing anywhere said so. A figure carries a run id in the manifest, so a reader takes it
// as evidence from that run; a stale one is evidence from a run that no longer exists.
//
// The renderer embeds no timestamp, so unchanged results reproduce every byte and a
// difference here means a measurement moved without the evidence following it.
func checkCurrent(results []resultFile, figuresDir, outPath string) error {
	tmp, mkErr := os.MkdirTemp("", "report-check-")
	if mkErr != nil {
		return mkErr
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	freshOut := filepath.Join(tmp, "report.html")
	if renderErr := render(results, tmp, freshOut); renderErr != nil {
		return fmt.Errorf("render for comparison: %w", renderErr)
	}

	var stale []string
	same := func(fresh, committed, label string) error {
		want, readErr := os.ReadFile(fresh)
		if readErr != nil {
			return readErr
		}
		got, cmpErr := os.ReadFile(committed)
		switch {
		case errors.Is(cmpErr, fs.ErrNotExist):
			stale = append(stale, label+" is not committed at all")
			return nil
		case cmpErr != nil:
			return cmpErr
		case !bytes.Equal(got, want):
			stale = append(stale, label+" differs from what the current results render")
		}
		return nil
	}

	if sameErr := same(freshOut, outPath, outPath); sameErr != nil {
		return sameErr
	}

	entries, err := os.ReadDir(tmp)
	if err != nil {
		return err
	}
	rendered := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || e.Name() == "report.html" {
			continue
		}
		rendered[e.Name()] = true
		if sameErr := same(filepath.Join(tmp, e.Name()),
			filepath.Join(figuresDir, e.Name()),
			filepath.Join(figuresDir, e.Name())); sameErr != nil {
			return sameErr
		}
	}

	// A committed figure the current results no longer produce is stale in the other
	// direction: it is evidence for a run that has gone, and leaving it is how a deleted
	// result keeps a picture on the page.
	//
	// Scoped to what this renderer emits. The directory also holds artefacts produced by
	// other targets — `make screenshots` writes a PNG there — and calling those stale
	// because the SVG renderer did not write them would fail the build for the wrong
	// reason, which is the fastest way to get a check ignored.
	committed, err := os.ReadDir(figuresDir)
	if err != nil {
		return err
	}
	for _, e := range committed {
		if e.IsDir() || rendered[e.Name()] || !isRenderedArtefact(e.Name()) {
			continue
		}
		stale = append(stale, filepath.Join(figuresDir, e.Name())+
			" is committed but the current results do not produce it")
	}

	if len(stale) > 0 {
		return fmt.Errorf("%d committed artefact(s) have drifted from the results:\n  %s\n\n"+
			"Run `make figures` and commit the result. The renderer embeds no timestamp, so "+
			"unchanged results reproduce every byte and a difference here means a "+
			"measurement moved without the evidence following it",
			len(stale), strings.Join(stale, "\n  "))
	}
	return nil
}

func loadResults(dir string) ([]resultFile, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths) // deterministic report ordering
	out := make([]resultFile, 0, len(paths))
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", p, err)
		}
		var data map[string]any
		if err := json.Unmarshal(raw, &data); err != nil {
			return nil, fmt.Errorf("parse %s: %w", p, err)
		}
		for _, required := range []string{"schema_version", "run"} {
			if _, ok := data[required]; !ok {
				return nil, fmt.Errorf("%s: result file missing %q; refusing to render "+
					"numbers without provenance", p, required)
			}
		}
		// Every result must name the data it consumed, with a checksum. A run over a
		// corpus records that under "corpus"; a run over an artefact exported by an
		// earlier run — the baseline sidecar over a feature file, an analysis over a
		// replay result — records it under "input" or "parent_run" instead. All three
		// are provenance; what is refused is a result that names no source at all.
		_, hasCorpus := data["corpus"]
		_, hasInput := data["input"]
		hasParent := false
		if run, ok := data["run"].(map[string]any); ok {
			if parent, ok := run["parent_run"].(string); ok && parent != "" {
				hasParent = true
			}
		}
		if !hasCorpus && !hasInput && !hasParent {
			return nil, fmt.Errorf("%s: result file names no data source (no %q, %q or "+
				"run.parent_run); refusing to render numbers without provenance",
				p, "corpus", "input")
		}
		out = append(out, resultFile{Path: filepath.Base(p), Data: data})
	}
	return out, nil
}

// verifyProvenance fails if any figure lacks a backing result file. Figures are
// matched through the manifest the renderer writes alongside them.
func verifyProvenance(results []resultFile, figuresDir string) error {
	known := make(map[string]bool, len(results))
	for _, r := range results {
		if run, ok := r.Data["run"].(map[string]any); ok {
			if id, ok := run["run_id"].(string); ok {
				known[id] = true
			}
		}
	}

	manifestPath := filepath.Join(figuresDir, "manifest.json")
	manifest := map[string]string{} // figure file -> run_id
	if raw, err := os.ReadFile(manifestPath); err == nil {
		if err := json.Unmarshal(raw, &manifest); err != nil {
			return fmt.Errorf("parse %s: %w", manifestPath, err)
		}
	}

	svgs, err := filepath.Glob(filepath.Join(figuresDir, "*.svg"))
	if err != nil {
		return err
	}
	var problems []string
	for _, svg := range svgs {
		name := filepath.Base(svg)
		runID, listed := manifest[name]
		if !listed {
			problems = append(problems, fmt.Sprintf("figure %s is not in the manifest: "+
				"no run is recorded as having produced it", name))
			continue
		}
		if !known[runID] {
			problems = append(problems, fmt.Sprintf("figure %s cites run %q, which has "+
				"no result file", name, runID))
		}
	}
	for name, runID := range manifest {
		if _, err := os.Stat(filepath.Join(figuresDir, name)); os.IsNotExist(err) {
			problems = append(problems, fmt.Sprintf("manifest lists %s (run %s) but the "+
				"figure does not exist", name, runID))
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("provenance verification failed:\n  %s",
			strings.Join(problems, "\n  "))
	}
	return nil
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

type hypothesisCard struct {
	ID, Title string
	Runs      []runSummary
	NotRun    bool
}

type runSummary struct {
	File, RunID, GitSHA, Started, Finished string
	Dirty                                  bool
	Coverage, Detectors                    string
	ProvenanceComplete                     bool
	Highlights                             []highlight
}

type highlight struct{ Label, Value string }

// render writes the report: the headline banner when an analysis result carries one,
// the scoreboard, then the figures (each generated only when a result carries its
// data, and written both inline and as a standalone SVG with a manifest entry), then
// the tables.
func render(results []resultFile, figuresDir, outPath string) error {
	cards := make([]hypothesisCard, 0, len(hypotheses))
	for _, h := range hypotheses {
		card := hypothesisCard{ID: h.ID, Title: h.Title}
		for _, r := range results {
			if !claimsHypothesis(r.Data, h.ID) {
				continue
			}
			card.Runs = append(card.Runs, summarise(r))
		}
		card.NotRun = len(card.Runs) == 0
		cards = append(cards, card)
	}

	figures, pending, err := buildFigures(results, figuresDir)
	if err != nil {
		return fmt.Errorf("build figures: %w", err)
	}
	tables := buildTables(results)

	var buf strings.Builder
	if err := reportTemplate.Execute(&buf, map[string]any{
		"Cards":              cards,
		"Results":            len(results),
		"Banner":             buildHeadline(results),
		"Figures":            figures,
		"Pending":            pending,
		"Tables":             tables,
		"CategoryComparison": buildCategoryComparison(results),
	}); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(outPath, []byte(buf.String()), 0o644)
}

func claimsHypothesis(data map[string]any, id string) bool {
	raw, ok := data["hypothesis"]
	if !ok {
		return false
	}
	list, ok := raw.([]any)
	if !ok {
		return false
	}
	for _, v := range list {
		if s, ok := v.(string); ok && s == id {
			return true
		}
	}
	return false
}

func summarise(r resultFile) runSummary {
	s := runSummary{File: r.Path}
	if run, ok := r.Data["run"].(map[string]any); ok {
		s.RunID, _ = run["run_id"].(string)
		s.GitSHA, _ = run["git_sha"].(string)
		if len(s.GitSHA) > 12 {
			s.GitSHA = s.GitSHA[:12]
		}
		s.Started, _ = run["started_at"].(string)
		s.Finished, _ = run["finished_at"].(string)
		s.Dirty, _ = run["git_dirty"].(bool)
		if d, ok := run["detectors"].([]any); ok {
			parts := make([]string, 0, len(d))
			for _, v := range d {
				if str, ok := v.(string); ok {
					parts = append(parts, str)
				}
			}
			s.Detectors = strings.Join(parts, ", ")
		}
	}
	if c, ok := r.Data["corpus"].(map[string]any); ok {
		if cov, ok := c["coverage"].(map[string]any); ok {
			// The run's own statement when it wrote one: it knows which cap was
			// applied, and a row-count description of a time-capped run misdescribes
			// what was measured.
			if statement, ok := cov["statement"].(string); ok && statement != "" {
				s.Coverage = statement
			} else {
				kind, _ := cov["kind"].(string)
				s.Coverage = kind
				if kind == "prefix" {
					if mr, ok := cov["max_rows"].(float64); ok && mr > 0 {
						s.Coverage = fmt.Sprintf("prefix: first %.0f rows only", mr)
					}
				}
			}
		}
		for _, k := range []string{"rows_read", "events_scored"} {
			if v, ok := c[k].(float64); ok {
				s.Highlights = append(s.Highlights, highlight{k, fmt.Sprintf("%.0f", v)})
			}
		}
	}
	if pc, ok := r.Data["provenance_complete"].(bool); ok {
		s.ProvenanceComplete = pc
	}
	if res, ok := r.Data["results"].(map[string]any); ok {
		if det, ok := res["detections_at_budget"].(map[string]any); ok {
			keys := make([]string, 0, len(det))
			for k := range det {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if m, ok := det[k].(map[string]any); ok {
					tp, _ := m["true_positives"].(float64)
					total, _ := m["red_team_total"].(float64)
					s.Highlights = append(s.Highlights,
						highlight{k, fmt.Sprintf("%.0f of %.0f red-team events", tp, total)})
				}
			}
		}
	}
	return s
}

// reportTemplate uses the whitepaper's own design tokens so a screenshot sits beside
// the paper without looking foreign. Light and dark palettes, no external resources.
var reportTemplate = template.Must(template.New("report").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Calibrated Behavioural Anomaly Detection — Evaluation</title>
<style>
:root{
  --ink:#17191B; --ink-2:#454B50; --ink-3:#6B7379;
  --accent:#1C4657; --accent-2:#7E5209;
  --rule:#D9DEE0; --rule-2:#B3BBBF;
  --panel:#F1F4F5; --panel-2:#FBF7EF;
  --crit:#862B36; --good:#1F5537; --page:#FFFFFF;
}
@media (prefers-color-scheme: dark){
  :root:not([data-theme="light"]){
    --ink:#E8EAEB; --ink-2:#B8BEC2; --ink-3:#8C9499;
    --accent:#7FB3C8; --accent-2:#D9A94E;
    --rule:#3A4147; --rule-2:#525A61;
    --panel:#22272B; --panel-2:#2A2622;
    --crit:#D98996; --good:#7FBF98; --page:#17191B;
  }
}
:root[data-theme="dark"]{
  --ink:#E8EAEB; --ink-2:#B8BEC2; --ink-3:#8C9499;
  --accent:#7FB3C8; --accent-2:#D9A94E;
  --rule:#3A4147; --rule-2:#525A61;
  --panel:#22272B; --panel-2:#2A2622;
  --crit:#D98996; --good:#7FBF98; --page:#17191B;
}
*{box-sizing:border-box;margin:0;padding:0}
body{background:var(--page);color:var(--ink);
  font-family:Georgia,"Sitka Text",Cambria,serif;line-height:1.55;
  max-width:1100px;margin:0 auto;padding:2.5rem 1.5rem;font-variant-numeric:tabular-nums}
h1{font-size:1.7rem;color:var(--accent);margin-bottom:.3rem}
h2{font-size:1.15rem;color:var(--accent);margin:1.6rem 0 .6rem;
  border-bottom:1px solid var(--rule);padding-bottom:.25rem}
.subtitle{color:var(--ink-3);font-family:"Segoe UI",Calibri,sans-serif;font-size:.9rem}
.banner{background:var(--panel);border:1px solid var(--accent);
  border-left:6px solid var(--accent);border-radius:6px;
  padding:1.3rem 1.5rem;margin:1.3rem 0}
.banner p{font-size:1.35rem;line-height:1.5}
.banner .trace{color:var(--ink-3);font-size:.78rem;margin-top:.6rem;
  font-family:"Cascadia Mono",Consolas,monospace}
.card{background:var(--panel);border:1px solid var(--rule);border-radius:6px;
  padding:1rem 1.2rem;margin:.8rem 0}
.card.notrun{background:var(--panel-2);border-style:dashed}
.eid{font-family:"Cascadia Mono",Consolas,monospace;color:var(--accent-2);
  font-weight:bold;margin-right:.5rem}
.notrun-mark{color:var(--crit);font-family:"Segoe UI",Calibri,sans-serif;
  font-weight:600;letter-spacing:.06em}
.run{margin:.6rem 0 .2rem;padding:.6rem .8rem;background:var(--page);
  border:1px solid var(--rule);border-radius:4px}
.hl{display:inline-block;margin:.15rem 1rem .15rem 0;font-size:.92rem}
.hl b{color:var(--ink-2);font-family:"Segoe UI",Calibri,sans-serif;font-size:.8rem;
  text-transform:uppercase;letter-spacing:.04em;margin-right:.3rem}
.prov{color:var(--ink-3);font-size:.78rem;font-family:"Cascadia Mono",Consolas,monospace;
  border-top:1px solid var(--rule);margin-top:.5rem;padding-top:.4rem}
.prov .dirty{color:var(--crit)}
.coverage{color:var(--accent-2);font-size:.85rem}
.reading{background:var(--panel-2);border:1px solid var(--rule);border-radius:6px;
  padding:1rem 1.2rem;margin:1.2rem 0}
.reading h2{margin-top:0;border:0;padding:0;font-size:1.05rem}
.reading p{margin:.5rem 0;font-size:.95rem;max-width:78ch}
.card svg{max-width:100%;height:auto;display:block}
.pending{color:var(--ink-3);font-size:.85rem;font-family:"Segoe UI",Calibri,sans-serif;
  margin:.8rem 0}
.pending ul{margin:.3rem 0 0 1.4rem}
.tbltitle{margin-bottom:.2rem}
table{border-collapse:collapse;width:100%;font-size:.9rem;margin:.4rem 0}
th{font-family:"Segoe UI",Calibri,sans-serif;font-size:.72rem;text-transform:uppercase;
  letter-spacing:.05em;color:var(--ink-2);text-align:left;
  border-bottom:1px solid var(--rule-2);padding:.35rem .6rem}
td{border-bottom:1px solid var(--rule);padding:.3rem .6rem}
th:not(:first-child),td:not(:first-child){text-align:right}
.tblwrap{overflow-x:auto}
.delta-good{color:var(--good);font-weight:600}
.delta-crit{color:var(--crit);font-weight:600}
td.unmeasurable{text-align:left;color:var(--ink-2);font-style:italic}
table.tax th,table.tax td{text-align:left}
.note{color:var(--ink-3);font-size:.8rem;margin:.3rem 0 .1rem;
  font-family:"Segoe UI",Calibri,sans-serif}
.caveats{color:var(--ink-3);font-size:.8rem;margin:.3rem 0 .1rem 1.4rem;
  font-family:"Segoe UI",Calibri,sans-serif}
.caption{color:var(--ink-3);font-size:.8rem;margin:.3rem 0 .1rem;
  font-family:"Segoe UI",Calibri,sans-serif}
footer{margin-top:2.5rem;color:var(--ink-3);font-size:.8rem;
  border-top:1px solid var(--rule-2);padding-top:.8rem}
</style>
</head>
<body>
<h1>Calibrated Behavioural Anomaly Detection</h1>
{{with .Banner}}
<div class="banner">
  <p>{{.Sentence}}</p>
  <div class="trace">{{if .BaselineRunID}}baseline run {{.BaselineRunID}} · {{end}}{{.File}}</div>
</div>
{{end}}
<p class="subtitle">Pre-registered evaluation on public corpora · every number below
was produced by a recorded run · hypotheses without a run are marked NOT RUN</p>

<h2>Hypothesis scoreboard (§12.3)</h2>
{{range .Cards}}
<div class="card{{if .NotRun}} notrun{{end}}">
  <div><span class="eid">{{.ID}}</span>{{.Title}}
  {{if .NotRun}} — <span class="notrun-mark">NOT RUN</span>{{end}}</div>
  {{range .Runs}}
  <div class="run">
    {{range .Highlights}}<span class="hl"><b>{{.Label}}</b>{{.Value}}</span>{{end}}
    {{if .Coverage}}<div class="coverage">coverage: {{.Coverage}} · detectors: {{.Detectors}}</div>{{end}}
    <div class="prov">run {{.RunID}} · {{.File}} · git {{.GitSHA}}{{if .Dirty}}
      <span class="dirty">(dirty tree)</span>{{end}} ·
      {{.Started}} → {{.Finished}} ·
      {{if .ProvenanceComplete}}provenance complete{{else}}<span class="dirty">provenance incomplete (smoke)</span>{{end}}</div>
  </div>
  {{end}}
</div>
{{end}}

<h2>Figures</h2>
{{range .Figures}}
<div class="card">
  {{.SVG}}
  {{range .Prov}}<div class="prov">{{.}}</div>{{end}}
</div>
{{end}}
{{if .Pending}}
<div class="pending">figures not yet available (no backing result file):
  <ul>{{range .Pending}}<li>{{.}}</li>{{end}}</ul>
</div>
{{end}}

<h2>Tables</h2>
{{with .CategoryComparison}}
<div class="card">
  <div class="tbltitle"><span class="eid">T-E1E2</span>Per-category comparison at matched alert budget</div>
  <div class="tblwrap"><table>
  <thead><tr><th>category</th><th>baseline</th><th>red-team events</th>
    <th>framework detected</th><th>framework recall</th><th>baseline detected</th>
    <th>baseline recall</th><th>Δ recall (percentage points)</th><th>ratio</th>
    <th>common days</th></tr></thead>
  <tbody>
  {{range .Rows}}<tr><td>{{.Category}}</td><td>{{.Baseline}}</td>{{if .Unmeasurable}}<td colspan="8" class="unmeasurable">{{.Unmeasurable}}</td>{{else}}<td>{{.Events}}</td><td>{{.Framework}}</td><td>{{.FrameworkRecall}}</td><td>{{.BaselineDetected}}</td><td>{{.BaselineRecall}}</td>{{if .DeltaClass}}<td class="{{.DeltaClass}}">{{.Delta}}</td>{{else}}<td>{{.Delta}}</td>{{end}}<td>{{.Ratio}}</td><td>{{.CommonDays}}</td>{{end}}</tr>
  {{end}}</tbody>
  </table></div>
  <div class="caption">{{.Caption}}</div>
  {{if .Note}}<div class="note">recall denominator — {{.Note}}</div>{{end}}
  {{if .Caveats}}<ul class="caveats">{{range .Caveats}}<li>{{.}}</li>{{end}}</ul>{{end}}
  {{if .Taxonomy}}
  <div class="caption">The categories, and why a marginal outlier detector cannot express them</div>
  <div class="tblwrap"><table class="tax">
  <thead><tr><th>category</th><th>whitepaper</th><th>structural test</th>
    <th>contrast with marginal detectors</th></tr></thead>
  <tbody>
  {{range .Taxonomy}}<tr><td>{{.ID}}</td><td>{{.WhitepaperSection}}</td><td>{{.StructuralTest}}</td><td>{{.Contrast}}</td></tr>
  {{end}}</tbody>
  </table></div>
  {{end}}
  {{range .Prov}}<div class="prov">{{.}}</div>{{end}}
</div>
{{end}}
{{range .Tables}}
<div class="card">
  <div class="tbltitle"><span class="eid">{{.ID}}</span>{{.Title}}</div>
  <div class="tblwrap">{{.Table}}</div>
  {{if .Caption}}<div class="caption">{{.Caption}}</div>{{end}}
  {{range .Prov}}<div class="prov">{{.}}</div>{{end}}
</div>
{{end}}
{{if and (not .Tables) (not .CategoryComparison)}}
<p class="pending">no tables yet: no result file carries the blocks they read</p>
{{end}}

<footer>Rendered by cmd/report from results/*.json only ({{.Results}} file(s)).
A number that does not appear in a result file does not appear here.</footer>
</body>
</html>
`))
