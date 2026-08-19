package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// A corpus written by cmd/subset carries a manifest recording that it is already an
// entity sample. Applying -entity-sample to such a corpus samples an already-sampled
// population, and this file exists because doing so once destroyed a run without
// anything in the artefact saying so.
//
// cmd/subset selects entities by residue: `hash(entity) % N == residue`. The replay's own
// sampler selects `hash(entity) % N == 0`. Residue 7 and residue 0 are **disjoint**, so
// applying the replay's sampler to a residue-7 corpus drops every background entity and
// leaves only the ones the sampler exempts — the labelled ones. `lanl-holdout-r7-d7-9-001`
// skipped 3,077,318 events that way and scored 99 entities, all 99 of them red-team
// accounts. It then wrote a complete result with full provenance and numbers in an
// entirely plausible range, and was read as a held-out replication for a day.
//
// The lesson generalises past this one selector. A sample of a sample is almost never
// what a caller means, and when the two selectors disagree the loss is total and silent.
// So the default is to refuse, and the override records itself in the result.

// corpusSubset is the part of a corpus manifest this command needs. Fields the manifest
// carries but the guard does not consult are deliberately absent: the check should fail if
// the sampling block is missing, not if an unrelated field is renamed.
//
// Kind is read for the record but deliberately NOT used to decide whether the guard
// applies. A corpus derived from a sample is still a sample, and a derived corpus carries
// its own kind: cmd/inject writes "corpus-injection" over a residue-7 subset. Gating on one
// literal kind would have disarmed the guard on exactly the corpus that most needs it, so
// what arms it is the presence of an inherited sampling block.
type corpusSubset struct {
	Kind     string `json:"kind"`
	Source   string `json:"source"`
	Sampling struct {
		KeepOneEntityInN int    `json:"keep_one_entity_in_n"`
		SampleResidue    int    `json:"sample_residue"`
		LabelledExempt   bool   `json:"labelled_entities_exempt"`
		Selector         string `json:"selector"`
	} `json:"sampling"`
	Counts struct {
		DistinctUsersKept int `json:"distinct_users_kept"`
	} `json:"counts"`
}

// manifestSuffix is the convention cmd/subset writes by default.
const manifestSuffix = ".manifest.json"

// readCorpusSubset returns the manifest beside a corpus, or nil when there is none.
//
// A missing manifest is not an error: the full LANL corpus has none, and neither does a
// corpus prepared by hand. A malformed one *is* an error, because the alternative is to
// treat a corpus of unknown provenance as if it were the full population, which is the
// mistake this guard exists to prevent.
func readCorpusSubset(authPath string) (*corpusSubset, error) {
	raw, err := os.ReadFile(authPath + manifestSuffix)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read corpus manifest for %s: %w", authPath, err)
	}
	var manifest corpusSubset
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, fmt.Errorf("parse %s%s: %w", authPath, manifestSuffix, err)
	}
	if manifest.Sampling.KeepOneEntityInN <= 1 {
		return nil, nil
	}
	return &manifest, nil
}

// checkResampling refuses to sample a corpus that is already an entity sample.
//
// It returns an error rather than warning, because a warning on a detached run scrolls
// past into a log nobody reads while the result file looks fine.
func checkResampling(cfg runConfig, subset *corpusSubset) error {
	if subset == nil || cfg.entitySample <= 1 || cfg.allowResampling {
		return nil
	}
	disjoint := ""
	if subset.Sampling.SampleResidue != 0 {
		disjoint = fmt.Sprintf(
			"\n\nTHIS CASE IS THE TOTAL ONE. The corpus keeps residue %d and this "+
				"sampler keeps residue 0; those sets are disjoint, so EVERY background "+
				"entity would be dropped and only the labelled entities, which the "+
				"sampler exempts, would survive. The run would complete, write a "+
				"well-formed result, and measure labelled traffic against itself.",
			subset.Sampling.SampleResidue)
	}
	return fmt.Errorf(
		"refusing to entity-sample a corpus that is already an entity sample.\n\n"+
			"  corpus:     %s\n"+
			"  manifest:   %s%s\n"+
			"  it already keeps 1 entity in %d (residue %d), %d distinct users\n"+
			"  this run asked for a further 1 in %d%s\n\n"+
			"If a smaller sample is what you want, build it with cmd/subset and point "+
			"-auth at the result, so the sampling is recorded in one place. If you "+
			"genuinely intend to sample twice, pass -allow-resampling and the result "+
			"will record that you did",
		cfg.authPath, cfg.authPath, manifestSuffix,
		subset.Sampling.KeepOneEntityInN, subset.Sampling.SampleResidue,
		subset.Counts.DistinctUsersKept, cfg.entitySample, disjoint)
}

// corpusSubsetRecord describes the corpus's own sampling in the result.
//
// It is recorded whether or not the replay sampled, because "the replay applied no
// sample" and "the full population was scored" are different statements and only the
// first is something a replay can know. Reporting the second on a subset corpus is how a
// sampled measurement gets quoted as a population one.
func corpusSubsetRecord(subset *corpusSubset) map[string]any {
	if subset == nil {
		return map[string]any{
			"is_subset": false,
			"note": "no cmd/subset manifest accompanies this corpus, so nothing here " +
				"records it as a subset. That is not positive evidence that it is the " +
				"full population — a corpus prepared by other means carries no manifest " +
				"either",
		}
	}
	return map[string]any{
		"is_subset":                true,
		"manifest_kind":            subset.Kind,
		"keep_one_entity_in_n":     subset.Sampling.KeepOneEntityInN,
		"sample_residue":           subset.Sampling.SampleResidue,
		"labelled_entities_exempt": subset.Sampling.LabelledExempt,
		"distinct_users_kept":      subset.Counts.DistinctUsersKept,
		"selector":                 subset.Sampling.Selector,
		"source":                   subset.Source,
		"note": "the corpus read by this run is itself an entity sample, so every " +
			"population-scope quantity — the co-occurrence graph, the population " +
			"marginals, and the population_rare census computed from them — is measured " +
			"against the retained entities rather than the full population, and a " +
			"detection rate measured here is not comparable to a full-population one",
	}
}
