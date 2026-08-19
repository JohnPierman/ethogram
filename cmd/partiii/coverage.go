package main

import (
	"bufio"
	"fmt"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
)

// Coverage figures inside a generated document used to be hand-written, which is the same
// class of defect the detector list once had: a number maintained by hand inside an
// artefact that presents itself as derived. It drifted, as such numbers do —
// `domain/cooccurrence` was listed at 75.5% while `go test -cover` reported 72.6%, and
// `domain/volume` had moved to 89.7%.
//
// The table is now measured out of a coverage profile. When none is supplied the document
// says so and prints no table, on the same rule the report renderer applies to a missing
// result: an absent measurement renders as NOT MEASURED, never as a stale number.

// coverageTarget is the project's gate, applied in CI as an aggregate over the domain and
// application layers. Per-package figures are reported against it regardless.
const coverageTarget = 80.0

// packageCoverage is one package's statement coverage.
type packageCoverage struct {
	Package string
	Percent float64
	Covered int
	Total   int
}

// readCoverage parses a `go test -coverprofile` file into per-package statement coverage.
//
// The format is one header line (`mode: atomic`) followed by one line per block:
//
//	<import path>/<file>.go:<startLine>.<startCol>,<endLine>.<endCol> <numStmt> <count>
//
// A block counts as covered when its execution count is non-zero, which is what
// `go tool cover -func` reports and therefore what the gate in the Makefile applies.
func readCoverage(profilePath, modulePath string) ([]packageCoverage, error) {
	file, err := os.Open(profilePath)
	if err != nil {
		return nil, fmt.Errorf("read coverage profile: %w", err)
	}
	defer func() { _ = file.Close() }()

	type counter struct{ covered, total int }
	byPackage := map[string]*counter{}

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "mode:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 3 {
			return nil, fmt.Errorf("coverage profile: malformed block %q", line)
		}
		colon := strings.LastIndex(fields[0], ":")
		if colon < 0 {
			return nil, fmt.Errorf("coverage profile: block names no file: %q", line)
		}
		statements, err := strconv.Atoi(fields[1])
		if err != nil {
			return nil, fmt.Errorf("coverage profile: statement count in %q: %w", line, err)
		}
		count, err := strconv.Atoi(fields[2])
		if err != nil {
			return nil, fmt.Errorf("coverage profile: execution count in %q: %w", line, err)
		}

		pkg := path.Dir(fields[0][:colon])
		pkg = strings.TrimPrefix(strings.TrimPrefix(pkg, modulePath), "/")
		if pkg == "" || pkg == "." {
			pkg = "(root)"
		}
		entry, ok := byPackage[pkg]
		if !ok {
			entry = &counter{}
			byPackage[pkg] = entry
		}
		entry.total += statements
		if count > 0 {
			entry.covered += statements
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("coverage profile: %w", err)
	}
	if len(byPackage) == 0 {
		return nil, fmt.Errorf("coverage profile %s contains no blocks", profilePath)
	}

	out := make([]packageCoverage, 0, len(byPackage))
	for pkg, entry := range byPackage {
		percent := 0.0
		if entry.total > 0 {
			percent = 100 * float64(entry.covered) / float64(entry.total)
		}
		out = append(out, packageCoverage{
			Package: pkg, Percent: percent,
			Covered: entry.covered, Total: entry.total,
		})
	}
	// Descending by coverage, then by name, so the document's ordering is stable across
	// regenerations and two runs of the same profile produce the same bytes.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Percent != out[j].Percent {
			return out[i].Percent > out[j].Percent
		}
		return out[i].Package < out[j].Package
	})
	return out, nil
}

// coverageTable renders the measured table, or nil when nothing was measured.
func coverageTable(measured []packageCoverage) *table {
	if len(measured) == 0 {
		return nil
	}
	rows := make([][]string, 0, len(measured))
	for _, entry := range measured {
		verdict := "met"
		if entry.Percent < coverageTarget {
			verdict = "below target"
		}
		rows = append(rows, []string{
			"`" + entry.Package + "`",
			fmt.Sprintf("%.1f%%", entry.Percent),
			verdict,
		})
	}
	return &table{
		Caption: fmt.Sprintf("Statement coverage per package, measured from a coverage "+
			"profile at generation time; the project target is %.0f%%.", coverageTarget),
		Head: []string{"Package", "Coverage", fmt.Sprintf("Against the %.0f%% target",
			coverageTarget)},
		Rows: rows,
	}
}

// coverageParagraph describes what was measured, or states that nothing was.
func coverageParagraph(measured []packageCoverage) string {
	if len(measured) == 0 {
		return "**Coverage: NOT MEASURED for this rendering.** No coverage profile was " +
			"supplied, so no table is printed. Regenerate with `make cover` followed by " +
			"`go run ./cmd/partiii -coverage coverage.out ...` to measure it in. These " +
			"figures were previously maintained by hand inside this generated document " +
			"and had drifted from what `go test -cover` reported; a stale number is worse " +
			"than an absent one, so the table is omitted rather than approximated."
	}
	below := make([]string, 0, len(measured))
	for _, entry := range measured {
		if entry.Percent < coverageTarget {
			below = append(below, "`"+entry.Package+"`")
		}
	}
	shortfall := "Every package meets the target individually."
	if len(below) > 0 {
		shortfall = fmt.Sprintf(
			"%d of them fall below the target individually — %s — which is stated here "+
				"rather than smoothed over by the aggregate.",
			len(below), strings.Join(below, ", "))
	}
	return fmt.Sprintf(
		"Coverage is measured from a `go test -coverprofile` run at the time this "+
			"document is generated, not maintained by hand: the figures below are read "+
			"out of the profile, so they cannot drift from what the tests actually "+
			"cover. The project's target is %.0f%%, and the CI gate applies it as an "+
			"aggregate over the domain and application layers; the per-package figures "+
			"are reported regardless. %s",
		coverageTarget, shortfall)
}
