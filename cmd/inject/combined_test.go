package main

import (
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWriteCombinedLabelsReproducesTheShippedFile is a regression guard on a provenance hole.
//
// `data/lanl/labels-combined-r7.txt.gz` is cited by a committed run and, for one release of
// this project, was produced by hand and recorded nowhere: no target, no command and no
// document knew how to rebuild it, so reproducing a result on a second machine required
// reading the file's bytes. This test pins that the tool now rebuilds it.
//
// It is skipped when the corpus is absent, which is every checkout that has not obtained the
// data, so it does not turn a provenance guarantee into a build dependency.
func TestWriteCombinedLabelsReproducesTheShippedFile(t *testing.T) {
	const (
		realPath     = "../../data/lanl/redteam.txt.gz"
		plantedPath  = "../../data/lanl/injected-labels-r7.txt.gz"
		combinedPath = "../../data/lanl/labels-combined-r7.txt.gz"
	)
	for _, p := range []string{realPath, plantedPath, combinedPath} {
		if _, err := os.Stat(p); err != nil {
			t.Skipf("corpus absent (%s); see DATA.md", filepath.Base(p))
		}
	}

	// The planted labels are read back off disk and pushed through the same writer, so the
	// test exercises the ordering and the encoding rather than the generator.
	plants := readPlanted(t, plantedPath)

	out := filepath.Join(t.TempDir(), "combined.txt.gz")
	if err := writeCombinedLabels(out, realPath, plants); err != nil {
		t.Fatal(err)
	}

	gotRows, wantRows := rowsOf(t, out), rowsOf(t, combinedPath)
	if len(gotRows) != len(wantRows) {
		t.Fatalf("rebuilt %d rows, shipped file has %d", len(gotRows), len(wantRows))
	}
	for i := range gotRows {
		if gotRows[i] != wantRows[i] {
			t.Fatalf("row %d differs:\n rebuilt %q\n shipped %q", i, gotRows[i], wantRows[i])
		}
	}

	// The digest is the part a second machine can check without the original. It is
	// reported either way: identical content under a different encoding is still a
	// different file, and the run record cites the file.
	got, want := digest(t, out), digest(t, combinedPath)
	if got != want {
		t.Logf("content identical, encoding differs: rebuilt %s shipped %s", got, want)
		t.Log("the shipped file predates this writer; record the rebuilt digest going forward")
	}
}

func readPlanted(t *testing.T, path string) []injected {
	t.Helper()
	out := []injected{}
	for _, line := range rowsOf(t, path) {
		f := strings.Split(line, ",")
		if len(f) != 4 {
			t.Fatalf("planted label row has %d fields: %q", len(f), line)
		}
		row := make([]string, 9)
		row[colTime], row[colSrcUser] = f[0], f[1]
		row[colSrcComp], row[colDstComp] = f[2], f[3]
		out = append(out, injected{row: row})
	}
	return out
}

func rowsOf(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path) //nolint:gosec // test fixture path
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	sc := bufio.NewScanner(zr)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			out = append(out, line)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func digest(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // test fixture path
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
