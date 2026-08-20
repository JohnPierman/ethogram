// Command corpuscheck verifies the derived corpus against the digests the recorded runs
// cite, and fails closed.
//
// # Why this exists
//
// Every result file in `results/` records the SHA-256 of each input it read. That proves
// which file was read and says nothing about whether a second machine has the same one, so
// reproducing a result meant deriving the inputs, running a two-hour replay, and discovering
// only from the numbers that something upstream differed. The cheapest place to learn that a
// corpus is wrong is before the replay.
//
// # Why one entry is allowed two digests
//
// The combined label file was, for one release, produced by hand and recorded nowhere;
// `cmd/inject -combined-labels` now builds it. The rebuilt file has the same 1,605 rows in
// the same order as the shipped one and a different SHA-256, because the two were written by
// different gzip encoders. Both are therefore listed, with the reason on the line.
//
// That is a real exception and not a loosening. Nothing downstream can observe the encoding:
// the replay's label loader builds sets and a count, so line order and compression level are
// unobservable to scoring, and the digest is provenance of the file that was read. Where a
// digest mismatch WOULD change a result — the auth corpora, whose contents the detectors
// score — exactly one digest is accepted.
package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	log.SetFlags(0)
	log.SetPrefix("corpuscheck: ")

	digests := flag.String("digests", "config/corpus-digests.txt",
		"expected digests, one `sha256  name  [# note]` per line")
	dir := flag.String("dir", "data/lanl", "directory holding the derived corpus")
	flag.Parse()

	expected, err := readDigests(*digests)
	if err != nil {
		log.Fatal(err)
	}
	if len(expected) == 0 {
		log.Fatalf("%s lists no digests; a check that cannot fail is not evidence", *digests)
	}

	var missing, wrong, ok int
	for _, e := range expected {
		path := filepath.Join(*dir, e.name)
		got, err := digest(path)
		if os.IsNotExist(err) {
			fmt.Printf("  MISSING  %-42s see DATA.md\n", e.name)
			missing++
			continue
		}
		if err != nil {
			log.Fatalf("%s: %v", e.name, err)
		}
		if e.accepts(got) {
			fmt.Printf("  ok       %-42s %s\n", e.name, got[:16])
			ok++
			continue
		}
		fmt.Printf("  WRONG    %-42s got %s\n", e.name, got[:16])
		for _, want := range e.sums {
			fmt.Printf("           %-42s want %s\n", "", want[:16])
		}
		if e.note != "" {
			fmt.Printf("           %-42s %s\n", "", e.note)
		}
		wrong++
	}

	fmt.Printf("\n%d ok, %d wrong, %d missing\n", ok, wrong, missing)
	if wrong > 0 {
		log.Fatal("a derived input does not match what the recorded runs read; " +
			"a replay from here would not reproduce them. Re-derive with `make corpus`, " +
			"or if the corpus is deliberately new, update config/corpus-digests.txt in the " +
			"same commit as the runs that read it")
	}
	if missing > 0 {
		log.Fatal("inputs are missing; `make corpus` derives them from auth.txt.gz and " +
			"redteam.txt.gz, which DATA.md says how to obtain")
	}
}

// expectation is one file and the digests admissible for it.
type expectation struct {
	name string
	sums []string
	note string
}

func (e expectation) accepts(got string) bool {
	for _, s := range e.sums {
		if strings.EqualFold(s, got) {
			return true
		}
	}
	return false
}

// readDigests parses the manifest. A line is `sha256[,sha256]  name  [# note]`; blank lines
// and lines beginning with # are comments.
func readDigests(path string) ([]expectation, error) {
	f, err := os.Open(path) //nolint:gosec // the path the flag names
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var out []expectation
	sc := bufio.NewScanner(f)
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		body, note, _ := strings.Cut(text, "#")
		fields := strings.Fields(body)
		if len(fields) != 2 {
			return nil, fmt.Errorf("%s:%d: want `sha256 name`, got %q", path, line, body)
		}
		sums := strings.Split(fields[0], ",")
		for _, s := range sums {
			if len(s) != 64 {
				return nil, fmt.Errorf("%s:%d: %q is not a sha256", path, line, s)
			}
		}
		out = append(out, expectation{
			name: fields[1], sums: sums, note: strings.TrimSpace(note),
		})
	}
	return out, sc.Err()
}

func digest(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // the path the manifest names
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
