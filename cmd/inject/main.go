// Command inject plants synthetic attacks of named kinds into a corpus, so detection can be
// measured per attack type against controlled ground truth.
//
// # Why this is needed
//
// The real red team gives one campaign with a fixed type mix, and the mix is uneven: on the
// matched run 194 of 262 labelled events are novel pairings while 100 are volume bursts that
// nothing detects at all. From that alone it is impossible to say whether a detector fails
// because the attack type is hard or because the corpus barely contains it.
//
// Planting attacks of known kinds separates those. Each generator below is designed to
// exercise ONE premise, so the per-type table reads as a coverage map: which detector covers
// which kind of attack, and which kinds nothing covers.
//
// # The methodological constraint that makes this honest
//
// **Every injected event must be individually plausible.** The destination computers, the
// authentication types and the logon types are all drawn from values that occur in the
// corpus; only their combination, their timing or their volume is unprecedented FOR THE
// VICTIM ACCOUNT. An injected event carrying an impossible value would test whether the
// framework can spot a malformed record, which is not the question and would flatter it.
//
// Plausible is not enough on its own, and the first version of this file proved it. It picked
// a value the victim had never used by sampling the vocabulary uniformly — and most values in
// an open vocabulary are rare, so it planted values that were rare POPULATION-WIDE. The
// measured consequence was that the population marginal detector scored privilege_escalation
// at a median p of 2.8e-06 while per-entity novelty, the thing that type claims to isolate,
// sat at 3.4e-03. The type was measuring population rarity under a per-entity name.
//
// So a planted value must be **ordinary for everyone else and unprecedented for this victim**:
// the busiest host the account has never reached, the commonest authentication type it has
// never used. That is the only form in which the per-type table separates per-entity novelty
// from population rarity, which are the two questions this whole project is about
// distinguishing.
//
// This is also why sensitivity measured here is a different claim from detecting the real
// campaign, and must be reported as such: it measures whether a detector responds to a
// mechanism by construction, not whether that mechanism appears in a real intrusion.
//
// Victims are drawn only from accounts the real labels do not name, so synthetic and real
// ground truth never collide.
package main

import (
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"maps"
	"math/rand"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/JohnPierman/ethogram/internal/provenance"
)

// Column indices in a LANL auth row:
// time,src_user,dst_user,src_computer,dst_computer,auth_type,logon_type,orientation,success
const (
	colTime     = 0
	colSrcUser  = 1
	colDstUser  = 2
	colSrcComp  = 3
	colDstComp  = 4
	colAuthType = 5
	colLogon    = 6
	colOrient   = 7
	colSuccess  = 8
	columns     = 9
)

// manifestSuffix is the convention cmd/subset writes and cmd/replay's resampling guard
// looks for. It must stay in step with the copy in cmd/replay: an injected corpus whose
// manifest the guard cannot find is a corpus the guard cannot protect.
const manifestSuffix = ".manifest.json"

// AttackType names a generator. Each is built to exercise one premise so that a per-type
// result reads as coverage rather than as an aggregate.
type AttackType string

const (
	// CredentialSpray: many destinations the account has never used, in a short window.
	// Exercises categorical novelty on the destination and the volume null at once.
	CredentialSpray AttackType = "credential_spray" //nolint:gosec // a type name, not a secret

	// LateralChain: a path of hops through computers the account has never used, each hop
	// sourced from the previous hop's destination. Exercises the pairing question, which no
	// detector scoring fields independently can express.
	LateralChain AttackType = "lateral_chain"

	// OffHours: the account's ordinary activity, at a time far from its own established
	// window. Deliberately single-signal — every value is one the account already uses — so
	// it isolates the timing detector.
	OffHours AttackType = "off_hours"

	// PrivilegeEscalation: an authentication type the account has never used, at its usual
	// time, to its usual host. Single-signal, isolating categorical novelty.
	PrivilegeEscalation AttackType = "privilege_escalation"

	// LowAndSlow: a sustained modest volume increase using only familiar values. Single
	// signal, isolating the volume null — and the case the dispersion widening was built to
	// tolerate, so it is the type most likely to be missed by design.
	LowAndSlow AttackType = "low_and_slow"

	// AccountTakeover: destination, authentication type, logon type and hour all change at
	// once. The easiest case, included as an upper bound: a detector that misses this misses
	// everything.
	AccountTakeover AttackType = "account_takeover"
)

func allTypes() []AttackType {
	return []AttackType{CredentialSpray, LateralChain, OffHours, PrivilegeEscalation,
		LowAndSlow, AccountTakeover}
}

// profile is what an account habitually does, gathered from the corpus before the injection
// window so that "never used" means never used before the attack.
type profile struct {
	entity       string
	events       int
	dstComputers map[string]int
	srcComputers map[string]int
	authTypes    map[string]int
	logonTypes   map[string]int
	hours        [24]int
	lastRow      []string // a real row to clone, so injected rows are shaped like real ones
}

// modalHour is the hour this account is most active in.
func (p *profile) modalHour() int {
	best, at := -1, 0
	for h, n := range p.hours {
		if n > best {
			best, at = n, h
		}
	}
	return at
}

func (p *profile) commonest(counts map[string]int) string {
	best, at := -1, ""
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic tie-breaking
	for _, k := range keys {
		if counts[k] > best {
			best, at = counts[k], k
		}
	}
	return at
}

// vocabulary is the corpus-wide set of plausible values an injected event may use, with how
// often each occurs.
//
// The frequencies are not decoration; they are what keeps the types isolated. The first
// version of this file picked a value the victim had never used by sampling the vocabulary
// UNIFORMLY, and most values in an open vocabulary are rare — so it systematically planted
// values that are rare population-wide. The measured consequence was unmistakable: on
// `lanl-injected-r7-d7-14-001` the POPULATION marginal detector scored privilege_escalation at
// a median p of 2.8e-06 while the per-entity novelty detector, whose premise that type claims
// to isolate, sat at 3.4e-03. The type was measuring population rarity.
//
// A planted value must therefore be **ordinary for everyone else and unprecedented for this
// victim**. That is per-entity novelty with population rarity held out, which is the only form
// in which the per-type table separates the two questions this project is about.
type vocabulary struct {
	computers []string // descending population frequency
	authTypes []string
	logons    []string
	frequency map[string]int
}

// mostOrdinaryUnused returns the values from candidates that this entity has never used,
// ordered by descending population frequency, keeping at most n.
//
// Deterministic and not shuffled: "the most ordinary value this account has never used" is a
// specific value, and randomising among the unused set is exactly what reintroduced population
// rarity. rng is therefore not taken.
func mostOrdinaryUnused(used map[string]int, candidates []string, freq map[string]int,
	n int) []string {
	out := make([]string, 0, len(candidates))
	for _, v := range candidates {
		if used[v] == 0 {
			out = append(out, v)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if freq[out[i]] != freq[out[j]] {
			return freq[out[i]] > freq[out[j]]
		}
		return out[i] < out[j] // deterministic tie-break (R4)
	})
	if n < len(out) {
		out = out[:n]
	}
	return out
}

type injected struct {
	row    []string
	kind   AttackType
	entity string
}

// config is one injection run, gathered so the parameter list stays readable and so every
// value that shapes the corpus is recorded from one place.
type config struct {
	authPath     string
	outPath      string
	labelsPath   string
	combinedPath string
	manifest     string
	taxonomy     string
	runID        string
	realLabels   string
	profileEnd   int64
	from         int64
	to           int64
	perType      int
	minEvents    int
	seed         int64
}

func main() {
	var cfg config
	flag.StringVar(&cfg.authPath, "auth", "", "source corpus .txt.gz (required)")
	flag.StringVar(&cfg.outPath, "out", "", "augmented corpus .txt.gz (required)")
	flag.StringVar(&cfg.labelsPath, "labels", "",
		"labels .txt.gz naming the injected events (required)")
	flag.StringVar(&cfg.manifest, "manifest", "",
		"manifest JSON written beside the corpus, which is where the replay's guard against "+
			"sampling an already-sampled corpus looks (defaults to <out>.manifest.json)")
	flag.StringVar(&cfg.taxonomy, "taxonomy", "",
		"attack-taxonomy JSON, which is what lets a per-type table be computed later: it maps "+
			"each victim account to the one attack type planted on it. Belongs in results/ "+
			"beside the runs it explains, because the dashboard is built from results/ alone "+
			"and must produce the same page without the corpus present "+
			"(defaults to <labels>.taxonomy.json)")
	flag.StringVar(&cfg.runID, "run-id", "",
		"identifier recorded in the taxonomy document (required when -taxonomy is written to "+
			"results/, which is provenance-gated)")
	flag.StringVar(&cfg.combinedPath, "combined-labels", "",
		"write the real and planted labels as one file here, ordered by timestamp. The "+
			"injected corpus is scored against both ground truths at once, so a replay "+
			"needs this file; producing it here rather than by hand is what makes it "+
			"rebuildable from the two inputs")
	flag.StringVar(&cfg.realLabels, "real-labels", "data/lanl/redteam.txt.gz",
		"the real labels, so their accounts are never chosen as victims")
	flag.Int64Var(&cfg.profileEnd, "profile-end", 604800,
		"profile each account from corpus seconds before this (defaults to the burn-in end)")
	flag.Int64Var(&cfg.from, "from", 604800, "earliest injection timestamp, corpus seconds")
	flag.Int64Var(&cfg.to, "to", 1209600, "latest injection timestamp, corpus seconds")
	flag.IntVar(&cfg.perType, "per-type", 4, "victim accounts per attack type")
	flag.IntVar(&cfg.minEvents, "min-events", 200,
		"an account needs this many profiled events to be a victim; too little history and "+
			"the cold-start convention makes everything unremarkable anyway")
	flag.Int64Var(&cfg.seed, "seed", 42, "RNG seed, recorded")
	flag.Parse()

	if cfg.authPath == "" || cfg.outPath == "" || cfg.labelsPath == "" {
		log.Fatal("-auth, -out and -labels are required")
	}
	if cfg.runID == "" {
		log.Fatal("-run-id is required: the taxonomy is read from results/, which refuses a " +
			"document with no provenance")
	}
	// Checked here rather than in the Makefile because the recipe shell on Windows is not
	// /bin/sh and has no `test`. The corpus is 100 MB of LANL data and is not in the
	// repository, so a missing one is the ordinary first-run case and deserves the pointer.
	if _, err := os.Stat(cfg.authPath); err != nil {
		log.Fatalf("no corpus at %s: %v\n\nThe LANL corpus is not in the repository, and in a "+
			"git worktree it is not under the repository root either. Point the Makefile at "+
			"it with `make inject DATA=/path/to/lanl`, or pass -auth directly", cfg.authPath, err)
	}
	if cfg.manifest == "" {
		cfg.manifest = cfg.outPath + manifestSuffix
	}
	if cfg.taxonomy == "" {
		cfg.taxonomy = cfg.labelsPath + ".taxonomy.json"
	}
	if err := run(cfg); err != nil {
		log.Fatal(err)
	}
}

func run(cfg config) error {
	realUsers, err := loadRealLabelUsers(cfg.realLabels)
	if err != nil {
		return fmt.Errorf("read real labels: %w", err)
	}
	log.Printf("real labels name %d accounts; none will be used as a victim", len(realUsers))

	profiles, vocab, err := profileCorpus(cfg.authPath, cfg.profileEnd)
	if err != nil {
		return err
	}
	log.Printf("profiled %d accounts; vocabulary %d computers, %d auth types, %d logon types",
		len(profiles), len(vocab.computers), len(vocab.authTypes), len(vocab.logons))

	victims := chooseVictims(profiles, realUsers, cfg.minEvents)
	if len(victims) < cfg.perType*len(allTypes()) {
		return fmt.Errorf("only %d eligible victims for %d needed (%d types x %d each); "+
			"lower -per-type or -min-events", len(victims), cfg.perType*len(allTypes()),
			len(allTypes()), cfg.perType)
	}
	log.Printf("%d eligible victim accounts", len(victims))

	// Shuffle before assigning. chooseVictims returns the eligible accounts in name order,
	// and taking the first N of that would plant every attack on the alphabetically-first
	// accounts — a systematic sample of whatever those happen to have in common, not a fair
	// one. This is the only place randomness is used, and the seed is recorded.
	rng := rand.New(rand.NewSource(cfg.seed)) //nolint:gosec // determinism is required (R4)
	rng.Shuffle(len(victims), func(i, j int) { victims[i], victims[j] = victims[j], victims[i] })

	assigned := map[AttackType][]string{}
	var chosen []string
	next := 0
	for _, kind := range allTypes() {
		for range cfg.perType {
			assigned[kind] = append(assigned[kind], victims[next])
			chosen = append(chosen, victims[next])
			next++
		}
	}
	// Named in a stable order within each type, so the taxonomy reads the same on a re-run.
	for _, kind := range allTypes() {
		sort.Strings(assigned[kind])
	}
	occupied, err := occupiedSeconds(cfg.authPath, chosen, cfg.from, cfg.to)
	if err != nil {
		return fmt.Errorf("collect occupied seconds: %w", err)
	}

	var plants []injected
	for _, kind := range allTypes() {
		for _, victim := range assigned[kind] {
			plants = append(plants, generate(kind, profiles[victim], vocab,
				cfg.from, cfg.to, occupied[victim])...)
		}
	}
	// Time order, so the augmented stream stays sorted and the reader sees a monotone clock.
	sort.SliceStable(plants, func(i, j int) bool {
		a, _ := strconv.ParseInt(plants[i].row[colTime], 10, 64)
		b, _ := strconv.ParseInt(plants[j].row[colTime], 10, 64)
		return a < b
	})
	log.Printf("generated %d synthetic events across %d types", len(plants), len(allTypes()))

	written, err := writeAugmented(cfg.authPath, cfg.outPath, plants)
	if err != nil {
		return err
	}
	if cfg.combinedPath != "" {
		if err := writeCombinedLabels(cfg.combinedPath, cfg.realLabels, plants); err != nil {
			log.Fatal(err)
		}
	}
	if err := writeLabels(cfg.labelsPath, plants); err != nil {
		return err
	}
	if err := writeManifest(cfg, plants, assigned, written); err != nil {
		return err
	}
	return writeTaxonomy(cfg, plants, assigned)
}

// loadRealLabelUsers reads the accounts the real labels name.
func loadRealLabelUsers(path string) (map[string]bool, error) {
	users := map[string]bool{}
	rows, closeFn, err := openRows(path)
	if err != nil {
		return nil, err
	}
	defer closeFn()
	for rows.Scan() {
		parts := strings.Split(strings.TrimSpace(rows.Text()), ",")
		if len(parts) >= 2 {
			users[parts[1]] = true
		}
	}
	return users, rows.Err()
}

func openRows(path string) (*bufio.Scanner, func(), error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	zr, err := gzip.NewReader(file)
	if err != nil {
		_ = file.Close()
		return nil, nil, err
	}
	sc := bufio.NewScanner(zr)
	sc.Buffer(make([]byte, 0, 256*1024), 1024*1024)
	return sc, func() { _ = zr.Close(); _ = file.Close() }, nil
}

// profileCorpus gathers per-account history before profileEnd, and the corpus vocabulary.
func profileCorpus(path string, profileEnd int64) (map[string]*profile, vocabulary, error) {
	rows, closeFn, err := openRows(path)
	if err != nil {
		return nil, vocabulary{}, err
	}
	defer closeFn()

	profiles := map[string]*profile{}
	// Counted, not just collected: how often a value occurs is what decides whether planting
	// it tests per-entity novelty or population rarity. See vocabulary.
	frequency := map[string]int{}
	computers, authTypes, logons := map[string]bool{}, map[string]bool{}, map[string]bool{}

	for rows.Scan() {
		parts := strings.Split(rows.Text(), ",")
		if len(parts) != columns {
			continue
		}
		// The corpus vocabulary is gathered from the whole file: these are values that
		// genuinely occur, which is what keeps an injected event plausible.
		computers[parts[colDstComp]] = true
		frequency[parts[colDstComp]]++
		if parts[colSrcComp] != parts[colDstComp] {
			frequency[parts[colSrcComp]]++
		}
		if parts[colAuthType] != "?" {
			authTypes[parts[colAuthType]] = true
			frequency[parts[colAuthType]]++
		}
		if parts[colLogon] != "?" {
			logons[parts[colLogon]] = true
			frequency[parts[colLogon]]++
		}

		at, err := strconv.ParseInt(parts[colTime], 10, 64)
		if err != nil || at >= profileEnd {
			continue
		}
		entity := parts[colSrcUser]
		if !isHumanAccount(entity) {
			continue
		}
		p := profiles[entity]
		if p == nil {
			p = &profile{
				entity: entity, dstComputers: map[string]int{}, srcComputers: map[string]int{},
				authTypes: map[string]int{}, logonTypes: map[string]int{},
			}
			profiles[entity] = p
		}
		p.events++
		p.dstComputers[parts[colDstComp]]++
		p.srcComputers[parts[colSrcComp]]++
		p.authTypes[parts[colAuthType]]++
		p.logonTypes[parts[colLogon]]++
		p.hours[(at/3600)%24]++
		p.lastRow = append([]string(nil), parts...)
	}
	if err := rows.Err(); err != nil {
		return nil, vocabulary{}, err
	}
	return profiles, vocabulary{
		computers: sortedKeys(computers),
		authTypes: sortedKeys(authTypes),
		logons:    sortedKeys(logons),
		frequency: frequency,
	}, nil
}

// occupiedSeconds returns, for each named account, the seconds inside [from, to) in which it
// already has a real event.
//
// This is a separate pass because the victims are not known until the profiles are built, and
// recording it for every account instead would hold millions of timestamps to use a few
// thousand. One extra read of the corpus is the cheaper trade.
func occupiedSeconds(path string, accounts []string, from, to int64) (map[string]map[int64]bool, error) {
	wanted := make(map[string]bool, len(accounts))
	for _, a := range accounts {
		wanted[a] = true
	}
	out := make(map[string]map[int64]bool, len(accounts))
	for _, a := range accounts {
		out[a] = map[int64]bool{}
	}

	rows, closeFn, err := openRows(path)
	if err != nil {
		return nil, err
	}
	defer closeFn()
	for rows.Scan() {
		parts := strings.Split(rows.Text(), ",")
		if len(parts) != columns || !wanted[parts[colSrcUser]] {
			continue
		}
		at, err := strconv.ParseInt(parts[colTime], 10, 64)
		if err != nil || at < from || at >= to {
			continue
		}
		out[parts[colSrcUser]][at] = true
	}
	return out, rows.Err()
}

// isHumanAccount mirrors the schema's entity_admit_regex, ^U[0-9]. An injected attack on an
// account the replay would not score is not a test of anything.
func isHumanAccount(entity string) bool {
	return len(entity) >= 2 && entity[0] == 'U' && entity[1] >= '0' && entity[1] <= '9'
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// chooseVictims picks accounts with enough history that are not named by the real labels, in
// deterministic order.
func chooseVictims(profiles map[string]*profile, realUsers map[string]bool, minEvents int) []string {
	var out []string
	for entity, p := range profiles {
		if realUsers[entity] || p.events < minEvents || p.lastRow == nil {
			continue
		}
		// Needs somewhere new to go, and a settled habit to depart from.
		if len(p.dstComputers) < 2 {
			continue
		}
		out = append(out, entity)
	}
	sort.Strings(out)
	return out
}

// unusedComputers returns the n busiest corpus computers this account has never reached,
// either as a source or as a destination.
//
// Busiest, not arbitrary: a host the whole organisation talks to and this account never has is
// per-entity novelty. A host nobody talks to is population rarity wearing the same costume,
// and picking those is what made the population detector win on types that were supposed to
// isolate the per-entity question.
func unusedComputers(p *profile, vocab vocabulary, n int) []string {
	used := make(map[string]int, len(p.dstComputers)+len(p.srcComputers))
	maps.Copy(used, p.dstComputers)
	for c, k := range p.srcComputers {
		used[c] += k
	}
	return mostOrdinaryUnused(used, vocab.computers, vocab.frequency, n)
}

// unusedValue returns the most common corpus value of a field that this account has never
// used, or "" when it has used them all.
func unusedValue(used map[string]int, candidates []string, freq map[string]int) string {
	out := mostOrdinaryUnused(used, candidates, freq, 1)
	if len(out) == 0 {
		return ""
	}
	return out[0]
}

// clone builds an injected row from the victim's own last real row, so every field the
// generator does not deliberately change is one the account genuinely produces.
func clone(p *profile, at int64) []string {
	row := append([]string(nil), p.lastRow...)
	row[colTime] = strconv.FormatInt(at, 10)
	row[colSrcUser] = p.entity
	row[colDstUser] = p.entity
	row[colSrcComp] = p.commonest(p.srcComputers)
	row[colDstComp] = p.commonest(p.dstComputers)
	row[colAuthType] = p.commonest(p.authTypes)
	row[colLogon] = p.commonest(p.logonTypes)
	row[colOrient] = "LogOn"
	row[colSuccess] = "Success"
	return row
}

// atHour returns a timestamp inside [from, to) on the first whole day of the window, at the
// given hour.
func atHour(from, to int64, hour, minute int) int64 {
	day := (from / 86400) + 1
	at := day*86400 + int64(hour)*3600 + int64(minute)*60
	if at >= to {
		at = from + int64(hour)*3600 + int64(minute)*60
	}
	return at
}

// generate produces one victim's worth of a given attack type.
//
// Fully deterministic given the victim and the corpus: every value it plants is "the most
// ordinary one this account has never used", which is a specific value rather than a draw.
// Randomness enters only in choosing WHICH accounts are victims, where it removes a selection
// bias; see run.
//
// occupied names the seconds this victim already has a real event in, and may be nil. A
// label is keyed on (time, entity, source, destination), which does not include the
// authentication type — so an injected event landing on a second the victim already occupies
// produces a key that matches its own row AND a real background row, and the background row
// can earn credit for the synthetic label. privilege_escalation and low_and_slow are the
// exposed cases, because both deliberately hold the host and the hour at the account's usual
// values, which is exactly where its real traffic is. Verification found one such collision
// in 856 events; this shifts each event to the next unoccupied second instead.
func generate(kind AttackType, p *profile, vocab vocabulary, from, to int64,
	occupied map[int64]bool) []injected {
	if p == nil {
		return nil
	}
	if occupied == nil {
		occupied = map[int64]bool{}
	}
	var out []injected
	add := func(row []string) {
		at, err := strconv.ParseInt(row[colTime], 10, 64)
		if err != nil {
			return
		}
		// Also keeps two injected events off the same second, so every label key is unique.
		for occupied[at] && at+1 < to {
			at++
		}
		occupied[at] = true
		row[colTime] = strconv.FormatInt(at, 10)
		out = append(out, injected{row: row, kind: kind, entity: p.entity})
	}
	usual := p.modalHour()

	switch kind {
	case CredentialSpray:
		// 40 destinations it has never reached, inside ten minutes: novel values and a
		// volume spike together.
		targets := unusedComputers(p, vocab, 40)
		base := atHour(from, to, usual, 5)
		for i, target := range targets {
			row := clone(p, base+int64(i)*15)
			row[colDstComp] = target
			add(row)
		}

	case LateralChain:
		// A path: each hop is sourced from the previous hop's destination, so the pairings
		// are new even though every computer is real.
		hops := unusedComputers(p, vocab, 5)
		base := atHour(from, to, usual, 20)
		src := p.commonest(p.srcComputers)
		for i, target := range hops {
			row := clone(p, base+int64(i)*120)
			row[colSrcComp] = src
			row[colDstComp] = target
			add(row)
			src = target
		}

	case OffHours:
		// Single-signal: everything is exactly what this account does, twelve hours out.
		base := atHour(from, to, (usual+12)%24, 30)
		for i := range 8 {
			add(clone(p, base+int64(i)*60))
		}

	case PrivilegeEscalation:
		// Single-signal: an authentication type that exists in the corpus but not in this
		// account's history.
		novel := unusedValue(p.authTypes, vocab.authTypes, vocab.frequency)
		if novel == "" {
			return nil
		}
		base := atHour(from, to, usual, 10)
		for i := range 3 {
			row := clone(p, base+int64(i)*300)
			row[colAuthType] = novel
			add(row)
		}

	case LowAndSlow:
		// Single-signal: familiar values only, a modest sustained increase. The type the
		// dispersion widening was built to tolerate, so the one most likely to be missed by
		// design — which is worth measuring rather than assuming.
		for day := range 3 {
			base := atHour(from, to, usual, 15) + int64(day)*86400
			if base >= to {
				break
			}
			for i := range 12 {
				add(clone(p, base+int64(i)*90))
			}
		}

	case AccountTakeover:
		// Everything changes at once: the upper bound.
		targets := unusedComputers(p, vocab, 15)
		novelAuth := unusedValue(p.authTypes, vocab.authTypes, vocab.frequency)
		novelLogon := unusedValue(p.logonTypes, vocab.logons, vocab.frequency)
		base := atHour(from, to, (usual+11)%24, 45)
		for i, target := range targets {
			row := clone(p, base+int64(i)*45)
			row[colDstComp] = target
			if novelAuth != "" {
				row[colAuthType] = novelAuth
			}
			if novelLogon != "" {
				row[colLogon] = novelLogon
			}
			add(row)
		}
	}
	return out
}

// writeAugmented merges the injected rows into the corpus in time order.
func writeAugmented(authPath, outPath string, plants []injected) (int64, error) {
	rows, closeFn, err := openRows(authPath)
	if err != nil {
		return 0, err
	}
	defer closeFn()

	out, err := os.Create(outPath)
	if err != nil {
		return 0, err
	}
	defer func() { _ = out.Close() }()
	zw := gzip.NewWriter(out)
	writer := bufio.NewWriterSize(zw, 1<<20)

	next := 0
	var written int64
	emit := func(line string) {
		_, _ = writer.WriteString(line)
		_ = writer.WriteByte('\n')
		written++
	}
	plantTime := func(i int) int64 {
		at, _ := strconv.ParseInt(plants[i].row[colTime], 10, 64)
		return at
	}

	for rows.Scan() {
		line := rows.Text()
		comma := strings.IndexByte(line, ',')
		if comma > 0 {
			if at, err := strconv.ParseInt(line[:comma], 10, 64); err == nil {
				for next < len(plants) && plantTime(next) <= at {
					emit(strings.Join(plants[next].row, ","))
					next++
				}
			}
		}
		emit(line)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for ; next < len(plants); next++ {
		emit(strings.Join(plants[next].row, ","))
	}

	if err := writer.Flush(); err != nil {
		return 0, err
	}
	if err := zw.Close(); err != nil {
		return 0, err
	}
	return written, nil
}

// writeLabels writes the injected events in the same shape as the real labels, so the replay
// matches them through the same code path.
func writeLabels(path string, plants []injected) error {
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	zw := gzip.NewWriter(out)
	writer := bufio.NewWriter(zw)
	for _, p := range plants {
		// A short write is sticky on a bufio.Writer, so the Flush below reports it. Losing a
		// label silently would turn a planted event into a permanent miss for every model.
		if _, err := fmt.Fprintf(writer, "%s,%s,%s,%s\n",
			p.row[colTime], p.row[colSrcUser], p.row[colSrcComp], p.row[colDstComp]); err != nil {
			return fmt.Errorf("write label for %s at %s: %w",
				p.row[colSrcUser], p.row[colTime], err)
		}
	}
	if err := writer.Flush(); err != nil {
		return err
	}
	return zw.Close()
}

// perTypeBlock counts what was planted and names who it was planted on, per attack type.
func perTypeBlock(plants []injected, assigned map[AttackType][]string) map[string]any {
	out := map[string]any{}
	for _, kind := range allTypes() {
		count := 0
		for _, p := range plants {
			if p.kind == kind {
				count++
			}
		}
		out[string(kind)] = map[string]any{
			"events":  count,
			"victims": assigned[kind],
		}
	}
	return out
}

// honestyNote travels with both documents, because either can be read on its own.
const honestyNote = "synthetic attacks, plausible by construction: every destination, " +
	"authentication type and logon type is a value that occurs in the corpus, and only the " +
	"combination, the timing or the volume is unprecedented for the victim account. Planted " +
	"values are the most COMMON ones the victim has never used, so that per-entity novelty is " +
	"tested with population rarity held out; choosing arbitrary unused values instead plants " +
	"population-rare ones and the population detector wins for the wrong reason. " +
	"Sensitivity measured against these labels is a claim about whether a detector responds " +
	"to a mechanism, NOT about detecting a real campaign, and the two must not be combined " +
	"into one headline. Victims are disjoint from the accounts the real labels name, so the " +
	"account alone says which ground truth a label belongs to. " +
	"LIMIT ON PRECISION: value choice is deterministic given the corpus, so every victim of " +
	"a type receives the same planted values. The events of one type are therefore not " +
	"independent draws, and its effective sample size is closer to the victim count than to " +
	"the event count — read a per-type rate as a coarse one"

func writeManifest(cfg config, plants []injected, assigned map[AttackType][]string,
	written int64) error {
	sum, err := fileDigest(cfg.outPath)
	if err != nil {
		return err
	}
	sampling, err := inheritedSampling(cfg.authPath)
	if err != nil {
		return err
	}
	doc := map[string]any{
		"kind":            "corpus-injection",
		"source":          provenance.RecordedPath(cfg.authPath),
		"output":          cfg.outPath,
		"output_sha256":   sum,
		"labels":          provenance.RecordedPath(cfg.labelsPath),
		"rows_written":    written,
		"events_injected": len(plants),
		"per_type":        perTypeBlock(plants, assigned),
		"sampling":        sampling,
		"parameters":      parameterBlock(cfg),
		"note":            honestyNote,
	}
	if err := writeJSON(cfg.manifest, doc); err != nil {
		return err
	}
	log.Printf("wrote %s (%d rows, %d injected) and %s",
		cfg.outPath, written, len(plants), cfg.manifest)
	return nil
}

// writeTaxonomy records which attack type was planted on which account.
//
// This is the document a per-type table is computed from, and it lives with the results
// rather than with the corpus for a specific reason: the dashboard is built from results/
// alone and must render the same page on a machine that has no corpus — CI has none. A
// taxonomy reachable only beside a 100 MB corpus would make the per-type panel appear
// locally and vanish in CI, which is the shape of defect that gets read as a passing check.
func writeTaxonomy(cfg config, plants []injected, assigned map[AttackType][]string) error {
	sum, err := fileDigest(cfg.labelsPath)
	if err != nil {
		return err
	}
	victims := map[string]string{}
	for _, kind := range allTypes() {
		for _, account := range assigned[kind] {
			victims[account] = string(kind)
		}
	}
	// The order the types were designed in, which reads as a progression: two multi-signal
	// kinds, then the three single-signal ones that isolate a null, then the upper bound.
	// Alphabetical ordering scatters that, so the order travels with the document.
	order := make([]string, 0, len(allTypes()))
	for _, kind := range allTypes() {
		order = append(order, string(kind))
	}
	doc := map[string]any{
		// results/ is provenance-gated: cmd/dashboard and cmd/report both refuse a file with
		// no schema version and no run block, rather than render numbers from it.
		"schema_version":  1,
		"run":             map[string]any{"run_id": cfg.runID},
		"kind":            "attack-taxonomy",
		"order":           order,
		"corpus":          provenance.RecordedPath(cfg.outPath),
		"corpus_manifest": provenance.RecordedPath(cfg.manifest),
		"labels":          provenance.RecordedPath(cfg.labelsPath),
		"labels_sha256":   sum,
		"events_injected": len(plants),
		"victim_type":     victims,
		"per_type":        perTypeBlock(plants, assigned),
		"premise":         premises(),
		"parameters":      parameterBlock(cfg),
		"note":            honestyNote,
	}
	if err := writeJSON(cfg.taxonomy, doc); err != nil {
		return err
	}
	log.Printf("wrote %s (%d victim accounts across %d types)",
		cfg.taxonomy, len(victims), len(allTypes()))
	return nil
}

// premises states, per type, which single question the generator was built to ask. A per-type
// table without it invites the reader to conclude a detector is bad at an attack when the
// attack was constructed to carry a signal that detector cannot express.
func premises() map[string]string {
	return map[string]string{
		string(CredentialSpray): "many destinations never used by this account, inside ten " +
			"minutes: categorical novelty and the volume null together",
		string(LateralChain): "a connected path of hops, each sourced from the previous " +
			"hop's destination, so the PAIRINGS are new though every computer is real. No " +
			"detector scoring fields independently can express this",
		string(OffHours): "single-signal: every value is one the account already uses, " +
			"twelve hours from its own established window. Isolates the timing null",
		string(PrivilegeEscalation): "single-signal: an authentication type absent from this " +
			"account's history, at its usual hour and host. Isolates categorical novelty",
		string(LowAndSlow): "single-signal: familiar values only, a modest sustained " +
			"increase. Isolates the volume null, and is the case the dispersion widening was " +
			"built to tolerate — so it is the type most likely to be missed BY DESIGN",
		string(AccountTakeover): "destination, authentication type, logon type and hour all " +
			"change at once. An upper bound: a detector that misses this misses everything",
	}
}

func parameterBlock(cfg config) map[string]any {
	return map[string]any{
		"seed": cfg.seed, "per_type_victims": cfg.perType,
		"min_profiled_events": cfg.minEvents, "profile_end_seconds": cfg.profileEnd,
		"inject_from": cfg.from, "inject_to": cfg.to,
	}
}

func writeJSON(path string, doc map[string]any) error {
	raw, err := json.MarshalIndent(doc, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o600)
}

// inheritedSampling copies the source corpus's sampling block into the injected corpus's
// manifest.
//
// A corpus built from an entity sample is still an entity sample, and the replay's guard
// against sampling twice arms itself on the presence of this block. Dropping it here would
// disarm that guard on the one corpus carrying synthetic ground truth — the #39 defect,
// reachable again through a derived file. Nil means the source itself was not a sample.
func inheritedSampling(authPath string) (map[string]any, error) {
	raw, err := os.ReadFile(authPath + manifestSuffix)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read source manifest for %s: %w", authPath, err)
	}
	var source struct {
		Sampling map[string]any `json:"sampling"`
	}
	if err := json.Unmarshal(raw, &source); err != nil {
		return nil, fmt.Errorf("parse source manifest for %s: %w", authPath, err)
	}
	if source.Sampling == nil {
		return nil, nil
	}
	out := make(map[string]any, len(source.Sampling)+1)
	maps.Copy(out, source.Sampling)
	out["inherited_from"] = authPath + manifestSuffix
	return out, nil
}

func fileDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// writeCombinedLabels writes the real labels and the planted ones as one file, ordered by
// timestamp.
//
// It exists because the alternative was a file nobody could rebuild. The injected corpus is
// scored against both ground truths at once — a replay takes one -redteam argument — so a
// combined file is needed, and for one release of this project it was produced by hand and
// never recorded anywhere. A committed result then cited a SHA for a file that no target, no
// command and no document in the repository knew how to make, which is the exact failure the
// provenance rules elsewhere here exist to prevent. Reproducing it took reading its bytes.
//
// Ordering is by timestamp and then by the row itself, which makes the output a deterministic
// function of its two inputs. Nothing downstream depends on the order — `loadRedTeam` builds
// sets and a count, so line order and compression level are unobservable to scoring — but a
// file that is byte-identical on every machine is checkable against a recorded digest, and one
// that is merely equivalent is not.
func writeCombinedLabels(path, realPath string, plants []injected) error {
	rows := make([]string, 0, len(plants)*2)

	campaign, closeFn, err := openRows(realPath)
	if err != nil {
		return fmt.Errorf("combined labels: open real labels: %w", err)
	}
	for campaign.Scan() {
		line := strings.TrimSpace(campaign.Text())
		if line == "" {
			continue
		}
		// The same four-field shape the replay requires. Refused here rather than at the
		// replay, where a malformed row aborts a run hours in.
		if got := len(strings.Split(line, ",")); got != 4 {
			closeFn()
			return fmt.Errorf("combined labels: real label row has %d fields, want 4: %q",
				got, line)
		}
		rows = append(rows, line)
	}
	err = campaign.Err()
	closeFn()
	if err != nil {
		return fmt.Errorf("combined labels: read real labels: %w", err)
	}

	for _, p := range plants {
		rows = append(rows, fmt.Sprintf("%s,%s,%s,%s",
			p.row[colTime], p.row[colSrcUser], p.row[colSrcComp], p.row[colDstComp]))
	}

	// Stable, and keyed on the timestamp alone. Real rows are appended before planted ones
	// and a stable sort preserves that among rows sharing a second, which is what makes the
	// output a deterministic function of the two inputs without needing a tie-break that
	// invents an ordering between two ground truths.
	sort.SliceStable(rows, func(i, j int) bool {
		ti, ei := splitFirstField(rows[i])
		tj, ej := splitFirstField(rows[j])
		if ei != nil || ej != nil {
			return false // unparseable timestamps keep their input position
		}
		return ti < tj
	})

	out, err := os.Create(path) //nolint:gosec // the path the flag names
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	zw := gzip.NewWriter(out)
	writer := bufio.NewWriter(zw)
	for _, r := range rows {
		if _, err := fmt.Fprintln(writer, r); err != nil {
			return fmt.Errorf("combined labels: write %q: %w", r, err)
		}
	}
	if err := writer.Flush(); err != nil {
		return err
	}
	return zw.Close()
}

// splitFirstField reads the leading integer timestamp of a label row.
func splitFirstField(row string) (int64, error) {
	head, _, _ := strings.Cut(row, ",")
	return strconv.ParseInt(head, 10, 64)
}
