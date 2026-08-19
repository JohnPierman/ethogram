// Command runreport produces a per-run report folder from a single experiment result
// JSON file.
//
// The one rule this command exists to enforce is the same rule that governs the
// dashboard and Part II: no number appears in README.md unless it came out of the
// result file. A key that is absent renders as an explicit "not recorded" line naming
// the key — never a zero, never a guess, never a plausible-looking figure. The folder
// carries the result file itself, byte for byte, so every line of the report can be
// checked against the exact bytes that produced it.
//
// For a result whose run.run_id is lanl-d7-14-005 the command writes:
//
//	<out-dir>/lanl-d7-14-005/README.md    the human-readable run report
//	<out-dir>/lanl-d7-14-005/result.json  a verbatim copy of the input
//	<out-dir>/lanl-d7-14-005/summary.json a small machine-readable digest
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

func main() {
	var (
		resultPath = flag.String("result", "", "path to one result JSON file (required)")
		outDir     = flag.String("out-dir", "runs", "directory that receives the per-run folder")
	)
	flag.Parse()
	if *resultPath == "" {
		log.Fatal("-result is required")
	}
	dir, err := writeRunFolder(*resultPath, *outDir)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("wrote %s", dir)
}

// writeRunFolder reads one result file and writes the run's report folder under
// outDir, named by the run's own recorded identity. It returns the folder path so the
// caller can name what was written.
func writeRunFolder(resultPath, outDir string) (string, error) {
	raw, err := os.ReadFile(resultPath)
	if err != nil {
		return "", fmt.Errorf("read result %s: %w", resultPath, err)
	}
	var data map[string]any
	if err = json.Unmarshal(raw, &data); err != nil {
		return "", fmt.Errorf("parse result %s: %w", resultPath, err)
	}
	runID, err := runIDOf(data, resultPath)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(outDir, runID)
	if err = os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create run folder %s: %w", dir, err)
	}
	readme := renderReadme(data, filepath.Base(resultPath))
	if err = os.WriteFile(filepath.Join(dir, "README.md"), []byte(readme), 0o644); err != nil {
		return "", fmt.Errorf("write README.md for run %s: %w", runID, err)
	}
	// The copy is the input bytes untouched, not a re-encoding: the report must stay
	// checkable against the exact file that produced it, and a round trip through the
	// JSON encoder would silently reorder keys and reformat numbers.
	if err = os.WriteFile(filepath.Join(dir, "result.json"), raw, 0o644); err != nil {
		return "", fmt.Errorf("write result.json for run %s: %w", runID, err)
	}
	summary, err := renderSummary(data)
	if err != nil {
		return "", fmt.Errorf("build summary for run %s: %w", runID, err)
	}
	if err = os.WriteFile(filepath.Join(dir, "summary.json"), summary, 0o644); err != nil {
		return "", fmt.Errorf("write summary.json for run %s: %w", runID, err)
	}
	return dir, nil
}

// runIDOf names the folder after the run's recorded identity and refuses to invent
// one: a result whose run.run_id is absent is a hard error, because a folder name
// made up by the renderer would be a guess presented as provenance.
func runIDOf(data map[string]any, resultPath string) (string, error) {
	runID, ok := strAt(data, "run", "run_id")
	if !ok || runID == "" {
		return "", fmt.Errorf("%s: run.run_id is absent; refusing to invent a folder name for a run that did not record its identity", resultPath)
	}
	// The id becomes a directory name; one carrying path separators or dot segments
	// would write outside the output directory.
	if runID != filepath.Base(runID) || runID == "." || runID == ".." {
		return "", fmt.Errorf("%s: run.run_id %q is not usable as a folder name", resultPath, runID)
	}
	return runID, nil
}
