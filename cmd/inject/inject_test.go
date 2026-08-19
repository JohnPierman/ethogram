package main

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// A victim with a settled habit: always Kerberos/Network, from C10 to C11, around 09:00.
func victim() *profile {
	p := &profile{
		entity:       "U500@DOM1",
		events:       1000,
		dstComputers: map[string]int{"C11": 900, "C12": 100},
		srcComputers: map[string]int{"C10": 1000},
		authTypes:    map[string]int{"Kerberos": 1000},
		logonTypes:   map[string]int{"Network": 1000},
		lastRow: []string{"1", "U500@DOM1", "U500@DOM1", "C10", "C11",
			"Kerberos", "Network", "LogOn", "Success"},
	}
	p.hours[9] = 1000
	return p
}

// corpusVocabulary is a realistically skewed population: a handful of busy hosts and a long
// tail of rare ones. The skew is the point — a uniform vocabulary cannot distinguish a
// generator that plants ordinary values from one that plants rare ones.
func corpusVocabulary() vocabulary {
	computers := []string{"C10", "C11", "C12"}
	freq := map[string]int{
		"C10": 50000, "C11": 40000, "C12": 5000,
		"Kerberos": 900000, "NTLM": 90000, "Negotiate": 500,
		"Network": 800000, "Batch": 60000, "Interactive": 300,
	}
	for i := range 60 {
		name := "C" + strconv.Itoa(100+i)
		computers = append(computers, name)
		// C100 is busy; the tail falls away sharply, as a real host distribution does.
		freq[name] = 30000 / (i + 1)
	}
	return vocabulary{
		computers: computers,
		authTypes: []string{"Kerberos", "NTLM", "Negotiate"},
		logons:    []string{"Network", "Batch", "Interactive"},
		frequency: freq,
	}
}

// TestPlantedValuesAreOrdinaryForEveryoneElse is the fix for a defect the first injected run
// exposed, and it is the property that makes the per-type table mean what it says.
//
// The original generator sampled the unused vocabulary uniformly. Most values in an open
// vocabulary are rare, so it planted values that were rare population-wide, and the measured
// result was that the POPULATION marginal detector scored privilege_escalation at a median p of
// 2.8e-06 while per-entity novelty — the thing the type claims to isolate — sat at 3.4e-03. The
// type was measuring population rarity under a per-entity name.
//
// A planted value must be unprecedented for the victim and unremarkable for everyone else.
func TestPlantedValuesAreOrdinaryForEveryoneElse(t *testing.T) {
	vocab := corpusVocabulary()
	p := victim()

	// The victim uses Kerberos, so the commonest authentication type it has NOT used is NTLM
	// at 90,000 occurrences — never Negotiate at 500.
	if got := unusedValue(p.authTypes, vocab.authTypes, vocab.frequency); got != "NTLM" {
		t.Errorf("unusedValue = %q, want NTLM, the commonest type this account never used; "+
			"picking the rare Negotiate would plant population rarity and the population "+
			"detector would win for the wrong reason", got)
	}
	if got := unusedValue(p.logonTypes, vocab.logons, vocab.frequency); got != "Batch" {
		t.Errorf("unusedValue = %q, want Batch, not the rare Interactive", got)
	}

	// Destinations must come off the top of the frequency order, not the tail.
	targets := unusedComputers(p, vocab, 5)
	if len(targets) != 5 {
		t.Fatalf("got %d targets, want 5", len(targets))
	}
	for i := 1; i < len(targets); i++ {
		if vocab.frequency[targets[i]] > vocab.frequency[targets[i-1]] {
			t.Errorf("targets are not in descending population frequency: %s (%d) follows "+
				"%s (%d)", targets[i], vocab.frequency[targets[i]],
				targets[i-1], vocab.frequency[targets[i-1]])
		}
	}
	// The busiest unused host in this vocabulary is C100 at 30,000.
	if targets[0] != "C100" {
		t.Errorf("busiest unused destination = %q, want C100", targets[0])
	}
	// And none of them may be a host the account already uses, in either direction.
	for _, target := range targets {
		if p.dstComputers[target] != 0 || p.srcComputers[target] != 0 {
			t.Errorf("%q is already used by this account, so it is not novel for it", target)
		}
	}
}

// TestPlantedValuesAreNotJustTheAlphabeticallyFirst: the ordering must come from frequency, so
// a vocabulary whose rare values sort first still yields the busy ones.
func TestPlantedValuesAreNotJustTheAlphabeticallyFirst(t *testing.T) {
	vocab := vocabulary{
		computers: []string{"C001", "C002", "C900"},
		frequency: map[string]int{"C001": 3, "C002": 7, "C900": 99000},
	}
	got := mostOrdinaryUnused(map[string]int{}, vocab.computers, vocab.frequency, 2)
	if len(got) != 2 || got[0] != "C900" || got[1] != "C002" {
		t.Errorf("mostOrdinaryUnused = %v, want [C900 C002] by descending frequency", got)
	}
}

const (
	from = int64(604800)
	to   = int64(1209600)
)

func generateFor(t *testing.T, kind AttackType) []injected {
	t.Helper()
	out := generate(kind, victim(), corpusVocabulary(), from, to, nil)
	if len(out) == 0 {
		t.Fatalf("%s produced no events", kind)
	}
	return out
}

// TestNoInjectedEventLandsOnASecondTheVictimAlreadyOccupies guards a defect verification
// found in the first injected corpus: one privilege_escalation event shared a second, a source
// and a destination with a real background row. A label is keyed on those four fields and not
// on the authentication type, so that key matched two rows, and the background row could earn
// credit for the synthetic label.
func TestNoInjectedEventLandsOnASecondTheVictimAlreadyOccupies(t *testing.T) {
	// privilege_escalation and low_and_slow hold the host and the hour at the account's
	// usual values, which is exactly where its real traffic already is.
	for _, kind := range []AttackType{PrivilegeEscalation, LowAndSlow, OffHours} {
		free := generate(kind, victim(), corpusVocabulary(), from, to, nil)

		busy := map[int64]bool{}
		for _, ev := range free {
			at, _ := strconv.ParseInt(ev.row[colTime], 10, 64)
			busy[at] = true
		}
		if len(busy) == 0 {
			t.Fatalf("%s produced no events", kind)
		}

		// Re-generate with every one of those seconds already taken by real traffic.
		shifted := generate(kind, victim(), corpusVocabulary(), from, to, busy)
		if len(shifted) != len(free) {
			t.Errorf("%s produced %d events against an occupied clock and %d against a free "+
				"one; shifting must not drop events", kind, len(shifted), len(free))
		}
		for _, ev := range shifted {
			at, _ := strconv.ParseInt(ev.row[colTime], 10, 64)
			for _, was := range free {
				if wasAt, _ := strconv.ParseInt(was.row[colTime], 10, 64); wasAt == at {
					t.Errorf("%s event stayed at second %d, which the victim already "+
						"occupies; its label key would match a real background row too",
						kind, at)
				}
			}
		}
	}
}

func TestTwoInjectedEventsNeverShareASecond(t *testing.T) {
	// Every label key must name exactly one row, or a detection cannot be attributed.
	for _, kind := range allTypes() {
		seen := map[string]bool{}
		for _, ev := range generateFor(t, kind) {
			key := ev.row[colTime] + "," + ev.row[colSrcUser] + "," +
				ev.row[colSrcComp] + "," + ev.row[colDstComp]
			if seen[key] {
				t.Errorf("%s produced two events with the label key %q", kind, key)
			}
			seen[key] = true
		}
	}
}

// TestEveryInjectedValueOccursInTheCorpus is the constraint that makes the whole exercise
// honest. An event carrying a value that appears nowhere in the corpus would test whether the
// framework can spot a malformed record, which is not the question and would flatter it.
func TestEveryInjectedValueOccursInTheCorpus(t *testing.T) {
	vocab := corpusVocabulary()
	known := slices.Contains[[]string, string]
	for _, kind := range allTypes() {
		for _, ev := range generateFor(t, kind) {
			if !known(vocab.computers, ev.row[colDstComp]) {
				t.Errorf("%s: destination %q does not occur in the corpus",
					kind, ev.row[colDstComp])
			}
			if !known(vocab.computers, ev.row[colSrcComp]) {
				t.Errorf("%s: source %q does not occur in the corpus",
					kind, ev.row[colSrcComp])
			}
			if !known(vocab.authTypes, ev.row[colAuthType]) {
				t.Errorf("%s: authentication type %q does not occur in the corpus",
					kind, ev.row[colAuthType])
			}
			if !known(vocab.logons, ev.row[colLogon]) {
				t.Errorf("%s: logon type %q does not occur in the corpus",
					kind, ev.row[colLogon])
			}
		}
	}
}

func TestInjectedEventsLandInsideTheWindowAndKeepTheVictimsIdentity(t *testing.T) {
	for _, kind := range allTypes() {
		for _, ev := range generateFor(t, kind) {
			at, err := strconv.ParseInt(ev.row[colTime], 10, 64)
			if err != nil {
				t.Fatalf("%s: unparseable timestamp %q", kind, ev.row[colTime])
			}
			if at < from || at >= to {
				t.Errorf("%s: event at %d falls outside the scoring window [%d, %d)",
					kind, at, from, to)
			}
			if ev.row[colSrcUser] != "U500@DOM1" {
				t.Errorf("%s: entity is %q, not the victim", kind, ev.row[colSrcUser])
			}
			if len(ev.row) != columns {
				t.Errorf("%s: row has %d columns, want %d", kind, len(ev.row), columns)
			}
		}
	}
}

// TestSingleSignalTypesChangeOnlyOneThing is what makes the per-type table a coverage map.
// If off_hours also introduced a novel destination, a detection could not be attributed to
// the timing detector, and the experiment would measure nothing in particular.
func TestSingleSignalTypesChangeOnlyOneThing(t *testing.T) {
	p := victim()

	// off_hours: every value familiar, only the hour moved.
	for _, ev := range generateFor(t, OffHours) {
		if p.dstComputers[ev.row[colDstComp]] == 0 {
			t.Errorf("off_hours introduced an unfamiliar destination %q", ev.row[colDstComp])
		}
		if p.authTypes[ev.row[colAuthType]] == 0 {
			t.Errorf("off_hours introduced an unfamiliar authentication type %q",
				ev.row[colAuthType])
		}
		at, _ := strconv.ParseInt(ev.row[colTime], 10, 64)
		if hour := (at / 3600) % 24; hour == 9 {
			t.Error("off_hours event is at the account's usual hour, so it carries no signal")
		}
	}

	// privilege_escalation: only the authentication type is new.
	for _, ev := range generateFor(t, PrivilegeEscalation) {
		if p.authTypes[ev.row[colAuthType]] != 0 {
			t.Errorf("privilege_escalation used a familiar authentication type %q",
				ev.row[colAuthType])
		}
		if p.dstComputers[ev.row[colDstComp]] == 0 {
			t.Errorf("privilege_escalation also changed the destination to %q, so a "+
				"detection could not be attributed to novelty on the auth type",
				ev.row[colDstComp])
		}
		at, _ := strconv.ParseInt(ev.row[colTime], 10, 64)
		if hour := (at / 3600) % 24; hour != 9 {
			t.Errorf("privilege_escalation also moved the hour to %d", hour)
		}
	}

	// low_and_slow: nothing new at all, only more of it.
	for _, ev := range generateFor(t, LowAndSlow) {
		if p.dstComputers[ev.row[colDstComp]] == 0 || p.authTypes[ev.row[colAuthType]] == 0 {
			t.Error("low_and_slow introduced an unfamiliar value; it must isolate volume")
		}
		at, _ := strconv.ParseInt(ev.row[colTime], 10, 64)
		if hour := (at / 3600) % 24; hour != 9 {
			t.Errorf("low_and_slow also moved the hour to %d", hour)
		}
	}
}

func TestMultiSignalTypesDoChangeSeveralThings(t *testing.T) {
	p := victim()

	// credential_spray: many destinations, all new to this account.
	spray := generateFor(t, CredentialSpray)
	seen := map[string]bool{}
	for _, ev := range spray {
		if p.dstComputers[ev.row[colDstComp]] != 0 {
			t.Errorf("credential_spray reused a familiar destination %q", ev.row[colDstComp])
		}
		seen[ev.row[colDstComp]] = true
	}
	if len(seen) < 10 {
		t.Errorf("credential_spray reached %d distinct destinations; a spray needs many",
			len(seen))
	}

	// lateral_chain: each hop is sourced from the previous hop's destination.
	chain := generateFor(t, LateralChain)
	if len(chain) < 3 {
		t.Fatalf("lateral_chain produced %d hops, too few to be a path", len(chain))
	}
	for i := 1; i < len(chain); i++ {
		if chain[i].row[colSrcComp] != chain[i-1].row[colDstComp] {
			t.Errorf("hop %d starts at %q but the previous hop ended at %q; the events are "+
				"not a connected path and the pairing signal is not what is being tested",
				i, chain[i].row[colSrcComp], chain[i-1].row[colDstComp])
		}
	}

	// account_takeover: the upper bound, everything moves at once.
	for _, ev := range generateFor(t, AccountTakeover) {
		if p.dstComputers[ev.row[colDstComp]] != 0 {
			t.Error("account_takeover reused a familiar destination")
		}
		if p.authTypes[ev.row[colAuthType]] != 0 {
			t.Error("account_takeover reused a familiar authentication type")
		}
	}
}

func TestVictimsExcludeTheAccountsTheRealLabelsName(t *testing.T) {
	// Synthetic and real ground truth must never collide, or a detection cannot be
	// attributed to either.
	profiles := map[string]*profile{
		"U500@DOM1": victim(),
		"U66@DOM1":  victim(), // named by the real labels
		"U1@DOM1":   thin(),   // too little history
		"C9$@DOM1":  victim(), // a machine account the replay would not score
	}
	profiles["U66@DOM1"].entity = "U66@DOM1"
	profiles["C9$@DOM1"].entity = "C9$@DOM1"

	got := chooseVictims(profiles, map[string]bool{"U66@DOM1": true}, 200)
	for _, v := range got {
		if v == "U66@DOM1" {
			t.Error("an account named by the real labels was chosen as a victim")
		}
		if v == "U1@DOM1" {
			t.Error("an account with too little history was chosen; the cold-start " +
				"convention makes everything unremarkable for it anyway")
		}
	}
	if len(got) != 2 {
		// U500 and C9$ both qualify on history; the human-account filter is applied during
		// profiling, not here, so this asserts the history rules only.
		t.Errorf("chose %v, want the two accounts with sufficient history", got)
	}
}

func thin() *profile {
	p := victim()
	p.entity = "U1@DOM1"
	p.events = 5
	return p
}

func TestOnlyHumanAccountsAreProfiled(t *testing.T) {
	// The schema admits ^U[0-9] as entities, so an attack on a machine account would never
	// be scored and would silently measure nothing.
	for account, want := range map[string]bool{
		"U500@DOM1": true, "U1@DOM1": true,
		"C1035$@DOM1": false, "SYSTEM@C1934": false, "ANONYMOUS LOGON@C586": false,
	} {
		if got := isHumanAccount(account); got != want {
			t.Errorf("isHumanAccount(%q) = %v, want %v", account, got, want)
		}
	}
}

// TestSamplingIsCarriedForwardFromTheSourceCorpus guards the derived-corpus path into #39.
// The injected corpus is built over a residue-7 entity sample; if its manifest drops the
// sampling block, the replay's guard against sampling twice stops firing on precisely the
// corpus that carries the synthetic ground truth.
func TestSamplingIsCarriedForwardFromTheSourceCorpus(t *testing.T) {
	corpus := filepath.Join(t.TempDir(), "auth-holdout-r7-d0-14.txt.gz")
	body := `{"kind":"corpus-subset","sampling":{"keep_one_entity_in_n":16,` +
		`"sample_residue":7,"labelled_entities_exempt":true}}`
	if err := os.WriteFile(corpus+".manifest.json", []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := inheritedSampling(corpus)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("no sampling block was inherited, so the replay would read the injected " +
			"corpus as a full population and permit sampling it again")
	}
	if got["keep_one_entity_in_n"] != float64(16) || got["sample_residue"] != float64(7) {
		t.Errorf("inherited %v, want the source's 1-in-16 at residue 7", got)
	}
	if got["inherited_from"] == nil {
		t.Error("the inherited block does not say where it came from")
	}
}

func TestNoSamplingIsInheritedFromAnUnsampledCorpus(t *testing.T) {
	// The full corpus carries no manifest, and a corpus prepared by hand carries no
	// sampling block. Neither may be reported as a sample.
	dir := t.TempDir()
	absent := filepath.Join(dir, "auth.txt.gz")
	got, err := inheritedSampling(absent)
	if err != nil || got != nil {
		t.Errorf("a corpus with no manifest yielded (%v, %v), want (nil, nil)", got, err)
	}

	plain := filepath.Join(dir, "plain.txt.gz")
	if writeErr := os.WriteFile(plain+manifestSuffix, []byte(`{"kind":"other"}`), 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	got, err = inheritedSampling(plain)
	if err != nil || got != nil {
		t.Errorf("a manifest with no sampling block yielded (%v, %v), want (nil, nil)", got, err)
	}
}

// writeCorpus builds a tiny gzipped corpus: `accounts` human accounts with a settled habit
// over the burn-in, plus a little traffic inside the injection window.
//
// Written in time order, because that is the shape of the real corpus and what the merge in
// writeAugmented relies on. Building it account-by-account produced an unsorted fixture, and
// the resulting failure was in the test rather than in the code under test.
func writeCorpus(t *testing.T, path string, accounts int) {
	t.Helper()
	type row struct {
		at   int64
		text string
	}
	var rows []row
	add := func(at int64, user, src, dst, auth, logon string) {
		rows = append(rows, row{at: at, text: fmt.Sprintf(
			"%d,%s,%s,%s,%s,%s,%s,LogOn,Success", at, user, user, src, dst, auth, logon)})
	}
	for i := range accounts {
		user := fmt.Sprintf("U%d@DOM1", 100+i)
		src := fmt.Sprintf("C%d", 200+i)
		// 300 burn-in events at 09:00 across two hosts: enough history to qualify as a
		// victim, and somewhere familiar for a single-signal attack to depart from.
		for n := range 300 {
			add(int64(n)*60+32400, user, src, fmt.Sprintf("C%d", 400+(n%2)),
				"Kerberos", "Network")
		}
		// Real traffic inside the window, so occupiedSeconds has something to find and the
		// collision-avoidance path is exercised.
		for n := range 20 {
			add(700000+int64(n)*60, user, src, "C400", "Kerberos", "Network")
		}
	}
	// Vocabulary breadth: destinations and value kinds nobody in the cohort used, so "never
	// used by this account" is satisfiable without inventing a value.
	for i := range 80 {
		add(1000, "U1@DOM1", "C1", fmt.Sprintf("C%d", 600+i), "NTLM", "Batch")
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].at < rows[j].at })

	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := gzip.NewWriter(file)
	for _, r := range rows {
		if _, err := fmt.Fprintln(zw, r.text); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestRunWritesACorpusLabelsAndATaxonomyThatAgree is the end-to-end check. The three outputs
// are read back by three different consumers — the replay reads the corpus, the replay's
// evaluation reads the labels, the dashboard reads the taxonomy — so a disagreement between
// them surfaces as a wrong measurement rather than as an error.
func TestRunWritesACorpusLabelsAndATaxonomyThatAgree(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "auth.txt.gz")
	writeCorpus(t, source, 20)

	realLabels := filepath.Join(dir, "redteam.txt.gz")
	writeCorpus(t, realLabels, 0) // no real labels, so no account is excluded

	cfg := config{
		authPath: source,
		outPath:  filepath.Join(dir, "injected.txt.gz"),
		// The labels and the taxonomy deliberately go to different directories in real use;
		// here only that both are written and agree matters.
		labelsPath: filepath.Join(dir, "labels.txt.gz"),
		manifest:   filepath.Join(dir, "injected.txt.gz"+manifestSuffix),
		taxonomy:   filepath.Join(dir, "taxonomy.json"),
		runID:      "inject-test-001",
		realLabels: realLabels,
		profileEnd: 604800, from: 604800, to: 1209600,
		perType: 2, minEvents: 200, seed: 42,
	}
	if err := run(cfg); err != nil {
		t.Fatal(err)
	}

	// Every label must name a row that is actually in the corpus, or the ground truth is
	// unreachable and counts as a permanent miss for every model.
	labels := map[string]int{}
	forEachRow(t, cfg.labelsPath, func(parts []string) {
		if len(parts) != 4 {
			t.Fatalf("label has %d fields, want 4: %v", len(parts), parts)
		}
		labels[strings.Join(parts, ",")]++
	})
	if len(labels) == 0 {
		t.Fatal("no labels were written")
	}

	found := map[string]int{}
	var prev int64 = -1
	forEachRow(t, cfg.outPath, func(parts []string) {
		if len(parts) != columns {
			return
		}
		at, err := strconv.ParseInt(parts[colTime], 10, 64)
		if err != nil {
			t.Fatalf("unparseable timestamp %q", parts[colTime])
		}
		if at < prev {
			t.Fatalf("corpus is not in time order: %d follows %d; the replay reads a "+
				"monotone stream", at, prev)
		}
		prev = at
		key := strings.Join([]string{parts[colTime], parts[colSrcUser],
			parts[colSrcComp], parts[colDstComp]}, ",")
		if _, ok := labels[key]; ok {
			found[key]++
		}
	})
	for key := range labels {
		switch found[key] {
		case 1: // exactly one row, which is what makes a detection attributable
		case 0:
			t.Errorf("label %q names no row in the corpus", key)
		default:
			t.Errorf("label %q matches %d rows, so a detection could not be attributed to "+
				"the planted event rather than to background traffic", key, found[key])
		}
	}

	// The taxonomy must carry provenance, or results/ refuses it.
	var taxonomy struct {
		SchemaVersion int    `json:"schema_version"`
		Kind          string `json:"kind"`
		Run           struct {
			RunID string `json:"run_id"`
		} `json:"run"`
		VictimType map[string]string `json:"victim_type"`
		Premise    map[string]string `json:"premise"`
		PerType    map[string]struct {
			Events  int      `json:"events"`
			Victims []string `json:"victims"`
		} `json:"per_type"`
	}
	raw, err := os.ReadFile(cfg.taxonomy)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &taxonomy); err != nil {
		t.Fatal(err)
	}
	if taxonomy.SchemaVersion == 0 || taxonomy.Kind != "attack-taxonomy" ||
		taxonomy.Run.RunID != cfg.runID {
		t.Errorf("taxonomy provenance = version %d, kind %q, run %q; results/ refuses a "+
			"document without it", taxonomy.SchemaVersion, taxonomy.Kind, taxonomy.Run.RunID)
	}
	if len(taxonomy.VictimType) != cfg.perType*len(allTypes()) {
		t.Errorf("taxonomy names %d victims, want %d",
			len(taxonomy.VictimType), cfg.perType*len(allTypes()))
	}

	// The victim map and the per-type block are two views of one assignment, and the
	// dashboard reads the first while the counts come from the second.
	for kind, block := range taxonomy.PerType {
		if taxonomy.Premise[kind] == "" {
			t.Errorf("%s has no stated premise, so a zero against it cannot be read", kind)
		}
		for _, account := range block.Victims {
			if got := taxonomy.VictimType[account]; got != kind {
				t.Errorf("%s is a victim of %s in per_type but %q in victim_type",
					account, kind, got)
			}
		}
	}

	// Every label's account must be a named victim, or a planted event would be attributed
	// to the real campaign and quietly counted in the wrong column.
	for key := range labels {
		account := strings.Split(key, ",")[1]
		if taxonomy.VictimType[account] == "" {
			t.Errorf("label on %s belongs to no named attack type", account)
		}
	}

	// The corpus manifest is a different document with a different job: it arms the
	// replay's guard against sampling an already-sampled corpus.
	if _, err := os.Stat(cfg.manifest); err != nil {
		t.Errorf("no corpus manifest beside the corpus: %v", err)
	}
}

func forEachRow(t *testing.T, path string, fn func(parts []string)) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	zr, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = zr.Close() }()
	sc := bufio.NewScanner(zr)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		fn(strings.Split(sc.Text(), ","))
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestGenerationIsDeterministic(t *testing.T) {
	// The corpus and its labels must be reproducible from the recorded seed, or a result
	// measured against them cannot be re-derived.
	run := func() []string {
		out := generate(CredentialSpray, victim(), corpusVocabulary(), from, to, nil)
		var keys []string
		for _, ev := range out {
			keys = append(keys, ev.row[colTime]+"|"+ev.row[colDstComp])
		}
		return keys
	}
	a, b := run(), run()
	if len(a) != len(b) {
		t.Fatalf("two runs produced %d and %d events", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("event %d differs between runs: %q against %q", i, a[i], b[i])
		}
	}
}
