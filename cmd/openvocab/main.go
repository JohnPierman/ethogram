// Command openvocab writes a synthetic corpus whose per-entity vocabularies are genuinely
// open, so that Good–Turing's reserved unseen mass can be tested on the case it exists for
// (#4).
//
// # Why a synthetic corpus, and why LANL cannot answer this
//
// The estimator is built, unit-tested and measured on LANL auth, and there it **costs**
// discrimination: novelty's median labelled percentile moves from 0.07% to 0.18%. That is not
// a wiring failure and the reason is stated in the issue — LANL auth's per-entity vocabularies
// are fairly closed, the attack signal *is* "used a value never used before", and many ordinary
// accounts carry singletons, so Good–Turing correctly raises P(new) for them and dampens
// exactly the signal the attack produces. It is being honest, and on a closed vocabulary the
// honesty costs discrimination.
//
// What remains untested is the case the estimator was written for:
//
//	an account with 3 values, 1000 observations each   true P(new) ~ 0    (4) gives 0.00033
//	an account with 500 values, one observation each   true P(new) ~ 1    (4) gives 0.001
//
// Equation (4) reserves mass from the counts alone, never from the shape, so two histories
// differing by everything receive nearly the same answer. `domain/novelty` already tests that
// the estimator separates them — by more than 100x where equation (4) manages about 3. What is
// untested is whether that separation *buys detections* end to end, and LANL cannot say,
// because it contains almost none of the open-vocabulary traffic the separation is about.
//
// # The design, and why the attack goes on the closed accounts
//
// The corpus holds both kinds of account deliberately:
//
//   - **open** entities, whose destination is a brand-new host on most events, so their
//     singleton rate stays high and their vocabulary never closes;
//   - **closed** entities, which revisit a small fixed set, so a first-ever destination for
//     one of them is genuinely out of character.
//
// The planted attacks go on the **closed** accounts, and that placement is the whole
// experiment. Under equation (4) a first-ever value scores about 1/n whoever produced it, so
// the open accounts — which produce first-ever values constantly and innocently — compete for
// the same alert slots as the attacks and crowd them out. Good–Turing should discount the open
// accounts' novelty and leave the budget to the closed accounts' genuine novelty.
//
// So the prediction, stated here before the run: **Good–Turing wins on this corpus and loses on
// LANL, and the difference is the presence of open-vocabulary traffic to suppress.** If it does
// not win here either, the estimator is not doing what its doc comment claims and that is worth
// more than the confirmation.
//
// # What this corpus is not
//
// Real. It is a generator with stated parameters, so it can demonstrate a mechanism and cannot
// establish a rate: every number from it is a statement about the estimator's response to a
// distribution shape, not about authentication traffic. It carries no claim to realism beyond
// the one property it is built to have.
package main

import (
	"bufio"
	"compress/gzip"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
)

// The LANL auth column layout this writes, so a replay reads it with the built-in schema and
// no code change (R2, E6).
const (
	colTime = iota
	colSrcUser
	colDstUser
	colSrcComp
	colDstComp
	colAuthType
	colLogonType
	colOrientation
	colSuccess
	columns
)

type config struct {
	out       string
	labels    string
	seed      int64
	openN     int
	closedN   int
	days      int64
	perDay    int
	openness  float64
	closedSet int
	victims   int
	burst     int
	bursts    int
	burnInEnd int64
}

func main() {
	log.SetFlags(0)
	cfg := config{}
	flag.StringVar(&cfg.out, "out", "", "corpus path, gzipped (required)")
	flag.StringVar(&cfg.labels, "labels", "", "label path, gzipped (required)")
	flag.Int64Var(&cfg.seed, "seed", 4, "generator seed, recorded in the run that reads this")
	flag.IntVar(&cfg.openN, "open-entities", 150,
		"entities whose destination vocabulary keeps opening")
	flag.IntVar(&cfg.closedN, "closed-entities", 150,
		"entities that revisit a small fixed set")
	flag.Int64Var(&cfg.days, "days", 14, "corpus length in days")
	flag.IntVar(&cfg.perDay, "per-day", 140, "events per entity per day")
	flag.Float64Var(&cfg.openness, "openness", 0.8,
		"probability an open entity's destination is brand new")
	flag.IntVar(&cfg.closedSet, "closed-set", 5,
		"how many destinations a closed entity ever uses")
	flag.IntVar(&cfg.victims, "victims", 24, "closed entities that receive a planted attack")
	flag.IntVar(&cfg.burst, "burst", 12, "planted events per burst")
	flag.IntVar(&cfg.bursts, "bursts", 3, "bursts per victim")
	flag.Int64Var(&cfg.burnInEnd, "burnin", 604800,
		"the burn-in boundary the replay will use; plants land strictly after it")
	flag.Parse()

	if cfg.out == "" || cfg.labels == "" {
		log.Fatal("both -out and -labels are required")
	}
	if err := run(cfg); err != nil {
		log.Fatal(err)
	}
}

// row is one authentication event.
type row struct {
	at     int64
	entity string
	src    string
	dst    string
	// planted marks an event that belongs in the label file.
	planted bool
}

func run(cfg config) error {
	// One seeded generator, used in one fixed order, so the corpus is a pure function of the
	// flags (R4). The seed is a flag rather than a constant so a second corpus can be drawn
	// to check that a result is not an artefact of one draw.
	rng := rand.New(rand.NewSource(cfg.seed)) //nolint:gosec // a corpus generator wants a reproducible draw, not an unpredictable one

	rows := make([]row, 0, (cfg.openN+cfg.closedN)*cfg.perDay*int(cfg.days))

	// Open-vocabulary entities: mostly brand-new destinations, so their singleton rate stays
	// high and equation (4) keeps scoring them as novel.
	for i := 0; i < cfg.openN; i++ {
		entity := fmt.Sprintf("U%04d@OPEN", i)
		seen := []string{}
		for day := int64(0); day < cfg.days; day++ {
			for e := 0; e < cfg.perDay; e++ {
				var dst string
				if len(seen) == 0 || rng.Float64() < cfg.openness {
					dst = fmt.Sprintf("C%s-%06d", "O", len(seen)+i*100_000)
					seen = append(seen, dst)
				} else {
					dst = seen[rng.Intn(len(seen))]
				}
				rows = append(rows, row{
					at:     dayOffset(day, e, cfg.perDay, rng),
					entity: entity,
					src:    fmt.Sprintf("C%s-HOME-%04d", "O", i),
					dst:    dst,
				})
			}
		}
	}

	// Closed-vocabulary entities: a small fixed set, revisited. A first-ever destination for
	// one of these is genuinely out of character, which is what the plants exploit.
	closedHosts := make([][]string, cfg.closedN)
	for i := 0; i < cfg.closedN; i++ {
		entity := fmt.Sprintf("U%04d@SHUT", i)
		hosts := make([]string, cfg.closedSet)
		for h := range hosts {
			hosts[h] = fmt.Sprintf("CS-%04d-%02d", i, h)
		}
		closedHosts[i] = hosts
		for day := int64(0); day < cfg.days; day++ {
			for e := 0; e < cfg.perDay; e++ {
				rows = append(rows, row{
					at:     dayOffset(day, e, cfg.perDay, rng),
					entity: entity,
					src:    fmt.Sprintf("CS-HOME-%04d", i),
					dst:    hosts[rng.Intn(len(hosts))],
				})
			}
		}
	}

	// The plants, on closed accounts, strictly after the burn-in boundary so the victim's own
	// history is established before it is attacked.
	firstScoredDay := cfg.burnInEnd / 86400
	planted := 0
	for v := 0; v < cfg.victims && v < cfg.closedN; v++ {
		entity := fmt.Sprintf("U%04d@SHUT", v)
		for b := 0; b < cfg.bursts; b++ {
			day := firstScoredDay + int64(b)
			if day >= cfg.days {
				break
			}
			// A destination this account has never used and never will, so the signal is
			// exactly "a value never seen for this entity" and nothing else.
			dst := fmt.Sprintf("CX-%04d-%02d", v, b)
			base := day*86400 + 11*3600
			for k := 0; k < cfg.burst; k++ {
				rows = append(rows, row{
					at:      base + int64(k)*90,
					entity:  entity,
					src:     fmt.Sprintf("CS-HOME-%04d", v),
					dst:     dst,
					planted: true,
				})
				planted++
			}
		}
	}

	// One fixed total order: by time, then entity, then destination. The corpus reader
	// requires ascending time, and the tie-break makes the file a pure function of the flags
	// rather than of map iteration.
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].at != rows[j].at {
			return rows[i].at < rows[j].at
		}
		if rows[i].entity != rows[j].entity {
			return rows[i].entity < rows[j].entity
		}
		return rows[i].dst < rows[j].dst
	})

	if err := writeCorpus(cfg.out, rows); err != nil {
		return err
	}
	if err := writeLabels(cfg.labels, rows); err != nil {
		return err
	}

	log.Printf("wrote %s: %d events over %d days, %d open and %d closed entities",
		cfg.out, len(rows), cfg.days, cfg.openN, cfg.closedN)
	log.Printf("wrote %s: %d planted events on %d victims, all after second %d",
		cfg.labels, planted, min(cfg.victims, cfg.closedN), cfg.burnInEnd)
	return nil
}

// dayOffset spreads an entity's events through the working part of a day, so the timing arm
// has a shape to learn and the corpus is not a single instant.
func dayOffset(day int64, index, perDay int, rng *rand.Rand) int64 {
	// Eight hours from 08:00, so an ordinary event is inside business hours.
	span := int64(8 * 3600)
	within := span * int64(index) / int64(perDay)
	return day*86400 + 8*3600 + within + int64(rng.Intn(60))
}

func writeCorpus(path string, rows []row) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path) //nolint:gosec // the path the flag names
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	zw := gzip.NewWriter(f)
	w := bufio.NewWriterSize(zw, 1<<20)

	fields := make([]string, columns)
	for _, r := range rows {
		fields[colTime] = fmt.Sprintf("%d", r.at)
		fields[colSrcUser] = r.entity
		fields[colDstUser] = r.entity
		fields[colSrcComp] = r.src
		fields[colDstComp] = r.dst
		fields[colAuthType] = "Kerberos"
		fields[colLogonType] = "Network"
		fields[colOrientation] = "LogOn"
		fields[colSuccess] = "Success"
		for i, v := range fields {
			if i > 0 {
				_ = w.WriteByte(',')
			}
			_, _ = w.WriteString(v)
		}
		_ = w.WriteByte('\n')
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return f.Close()
}

// writeLabels writes the planted events in the same four-column shape the real labels use, so
// the replay matches them through the same code path.
func writeLabels(path string, rows []row) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path) //nolint:gosec // the path the flag names
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	zw := gzip.NewWriter(f)
	w := bufio.NewWriter(zw)
	for _, r := range rows {
		if !r.planted {
			continue
		}
		if _, err := fmt.Fprintf(w, "%d,%s,%s,%s\n", r.at, r.entity, r.src, r.dst); err != nil {
			return fmt.Errorf("write label for %s at %d: %w", r.entity, r.at, err)
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return f.Close()
}
