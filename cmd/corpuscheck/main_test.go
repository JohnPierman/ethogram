package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeManifest(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "digests.txt")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const sumA = "77d2897145ce4fd1d66e2534ae4a8e52310ea4f915edc6c6750d8d826131961f"
const sumB = "98eab9bbcd54ee93ce6d3dc5fb420aaa918903aebed98be5b7fa19ecad664c29"

func TestReadDigestsParsesTheManifest(t *testing.T) {
	p := writeManifest(t, "# a comment\n\n"+
		sumA+"  auth-r11-d0-14.txt.gz\n"+
		sumA+","+sumB+"  labels-combined-r7.txt.gz  # two encodings, see DATA.md\n")

	got, err := readDigests(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("parsed %d entries, want 2", len(got))
	}
	if got[0].name != "auth-r11-d0-14.txt.gz" || len(got[0].sums) != 1 {
		t.Errorf("first entry = %+v", got[0])
	}
	if len(got[1].sums) != 2 {
		t.Errorf("second entry should admit two digests: %+v", got[1])
	}
	if !strings.Contains(got[1].note, "two encodings") {
		t.Errorf("the note was dropped: %q", got[1].note)
	}
}

// TestReadDigestsRefusesAMalformedManifest matters because the manifest is the gate. A line
// that silently fails to parse is a file that silently stops being checked.
func TestReadDigestsRefusesAMalformedManifest(t *testing.T) {
	for name, body := range map[string]string{
		"no filename":    sumA + "\n",
		"extra field":    sumA + "  a.gz  b.gz\n",
		"truncated sum":  "77d28971  a.gz\n",
		"not hex length": strings.Repeat("z", 64) + "9  a.gz\n",
		"empty second":   sumA + ",  a.gz\n",
	} {
		if _, err := readDigests(writeManifest(t, body)); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestAcceptsMatchesEitherDigestCaseInsensitively(t *testing.T) {
	e := expectation{name: "x", sums: []string{sumA, sumB}}
	if !e.accepts(sumA) || !e.accepts(sumB) {
		t.Error("a listed digest was rejected")
	}
	if !e.accepts(strings.ToUpper(sumA)) {
		t.Error("an upper-case digest was rejected; hex case is not a difference")
	}
	if e.accepts(strings.Repeat("0", 64)) {
		t.Error("an unlisted digest was accepted")
	}
}

// TestDigestReadsARealFile pins that the digest is of the file's bytes, so a caller cannot
// pass a check by, say, matching on the name.
func TestDigestReadsARealFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(p, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	const abc = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	got, err := digest(p)
	if err != nil {
		t.Fatal(err)
	}
	if got != abc {
		t.Errorf("digest = %s, want the SHA-256 of \"abc\" %s", got, abc)
	}
	if _, err := digest(filepath.Join(t.TempDir(), "absent")); !os.IsNotExist(err) {
		t.Errorf("a missing file gave %v, want a not-exist error so it reports as MISSING", err)
	}
}
