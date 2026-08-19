package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests guard the defect that destroyed `lanl-holdout-r7-d7-9-001`: entity
// sampling applied twice, with disjoint selectors, which dropped every background entity
// and left a run measuring labelled traffic against itself. The run completed and wrote a
// plausible result, so the only defence is to refuse before it starts.

// writeManifest puts a cmd/subset manifest beside a corpus path.
func writeManifest(t *testing.T, corpus string, keepOneIn, residue int) {
	t.Helper()
	body := `{
	  "kind": "corpus-subset",
	  "source": "auth.txt.gz",
	  "counts": {"distinct_users_kept": 4016},
	  "sampling": {
	    "keep_one_entity_in_n": ` + itoa(keepOneIn) + `,
	    "sample_residue": ` + itoa(residue) + `,
	    "labelled_entities_exempt": true,
	    "selector": "FNV-1a 64 of the source entity identifier, modulo N, equals the residue"
	  }
	}`
	if err := os.WriteFile(corpus+manifestSuffix, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}

func corpusIn(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "auth-sample.txt.gz")
	if err := os.WriteFile(path, []byte("corpus"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestSamplingIsInheritedThroughADerivedCorpus covers the near-miss that followed the
// original defect. cmd/inject writes an augmented corpus over a residue-7 subset, under its
// own kind. While the guard keyed on `kind == "corpus-subset"` it saw nothing to guard, so
// the injected corpus — the one carrying the synthetic ground truth — was the single corpus
// on which #39 could still happen silently.
func TestSamplingIsInheritedThroughADerivedCorpus(t *testing.T) {
	corpus := corpusIn(t)
	body := `{
	  "kind": "corpus-injection",
	  "source": "auth-holdout-r7-d0-14.txt.gz",
	  "counts": {"distinct_users_kept": 4016},
	  "sampling": {
	    "keep_one_entity_in_n": 16,
	    "sample_residue": 7,
	    "labelled_entities_exempt": true,
	    "selector": "inherited from the source corpus",
	    "inherited_from": "auth-holdout-r7-d0-14.txt.gz.manifest.json"
	  }
	}`
	if err := os.WriteFile(corpus+manifestSuffix, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	subset, err := readCorpusSubset(corpus)
	if err != nil {
		t.Fatal(err)
	}
	if subset == nil {
		t.Fatal("a derived corpus carrying an inherited sampling block was not recognised " +
			"as a sample; a corpus built from a sample is still a sample")
	}
	if err := checkResampling(runConfig{authPath: corpus, entitySample: 16}, subset); err == nil {
		t.Fatal("entity-sampling a derived corpus was permitted")
	}
	if got := corpusSubsetRecord(subset)["manifest_kind"]; got != "corpus-injection" {
		t.Errorf("record names the manifest kind %v; the result should say which kind of "+
			"derived corpus was read", got)
	}
}

func TestResamplingASubsetIsRefused(t *testing.T) {
	corpus := corpusIn(t)
	writeManifest(t, corpus, 16, 7)

	subset, err := readCorpusSubset(corpus)
	if err != nil {
		t.Fatal(err)
	}
	if subset == nil {
		t.Fatal("a corpus-subset manifest was not recognised")
	}

	err = checkResampling(runConfig{authPath: corpus, entitySample: 16}, subset)
	if err == nil {
		t.Fatal("entity-sampling an already-sampled corpus was permitted; with disjoint " +
			"selectors this drops every background entity and the run measures labelled " +
			"traffic against itself")
	}
	// The message has to carry the diagnosis, because the person reading it is about to
	// decide whether to pass -allow-resampling.
	for _, want := range []string{"residue 7", "disjoint", "-allow-resampling"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not mention %q:\n%s", want, err)
		}
	}
}

func TestResamplingIsPermittedWhenAskedForExplicitly(t *testing.T) {
	corpus := corpusIn(t)
	writeManifest(t, corpus, 16, 7)
	subset, _ := readCorpusSubset(corpus)

	cfg := runConfig{authPath: corpus, entitySample: 16, allowResampling: true}
	if err := checkResampling(cfg, subset); err != nil {
		t.Fatalf("-allow-resampling did not permit the run: %v", err)
	}
}

func TestAnUnsampledRunOverASubsetIsPermitted(t *testing.T) {
	// The corrected holdout run is exactly this case: the corpus is a subset, and the
	// replay applies no sample of its own. It must not be refused.
	corpus := corpusIn(t)
	writeManifest(t, corpus, 16, 7)
	subset, _ := readCorpusSubset(corpus)

	if err := checkResampling(runConfig{authPath: corpus}, subset); err != nil {
		t.Fatalf("a run that applied no sampling of its own was refused: %v", err)
	}
}

func TestACorpusWithoutAManifestIsNotTreatedAsASubset(t *testing.T) {
	corpus := corpusIn(t)

	subset, err := readCorpusSubset(corpus)
	if err != nil {
		t.Fatalf("a corpus with no manifest must not be an error: %v", err)
	}
	if subset != nil {
		t.Fatal("a corpus with no manifest was reported as a subset")
	}
	if err := checkResampling(runConfig{authPath: corpus, entitySample: 16}, subset); err != nil {
		t.Fatalf("sampling the full corpus was refused: %v", err)
	}
}

func TestAMalformedManifestIsAnErrorRatherThanIgnored(t *testing.T) {
	// Silently treating an unreadable manifest as "no manifest" would restore exactly the
	// assumption this guard exists to remove: that a corpus is the full population unless
	// something proves otherwise.
	corpus := corpusIn(t)
	if err := os.WriteFile(corpus+manifestSuffix, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readCorpusSubset(corpus); err == nil {
		t.Fatal("a malformed corpus manifest was ignored rather than reported")
	}
}

func TestTheResultRecordsWhatTheCorpusWasNotOnlyWhatTheRunDid(t *testing.T) {
	corpus := corpusIn(t)
	writeManifest(t, corpus, 16, 7)
	subset, _ := readCorpusSubset(corpus)

	record := corpusSubsetRecord(subset)
	if record["is_subset"] != true {
		t.Error("a subset corpus was not recorded as one")
	}
	if record["sample_residue"] != 7 || record["keep_one_entity_in_n"] != 16 {
		t.Errorf("sampling not recorded faithfully: %v", record)
	}

	// And the negative case must not overclaim: absence of a manifest is not evidence
	// that the corpus is the full population.
	none := corpusSubsetRecord(nil)
	if none["is_subset"] != false {
		t.Error("a corpus with no manifest was recorded as a subset")
	}
	note, _ := none["note"].(string)
	if !strings.Contains(note, "not positive evidence") {
		t.Errorf("the no-manifest note overclaims; it must not assert a full population: %q",
			note)
	}
}

func TestAnUnsampledRunDoesNotClaimTheFullPopulation(t *testing.T) {
	// The result used to say "the full admitted entity population was scored" whenever
	// the replay applied no sample, which is false on a subset corpus and is how a
	// sampled measurement gets quoted as a population one.
	record := entitySampleRecord(runConfig{})
	if record["applied"] != false {
		t.Fatal("an unsampled run was recorded as sampled")
	}
	note, _ := record["note"].(string)
	if strings.Contains(note, "full admitted entity population") {
		t.Errorf("the unsampled note claims a full population the replay cannot know: %q",
			note)
	}
	if !strings.Contains(note, "corpus_subset") {
		t.Errorf("the unsampled note does not point at the record that can answer it: %q",
			note)
	}
}
