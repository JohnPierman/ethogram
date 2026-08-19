// Command subset writes a smaller corpus in the source format, so that a full pass of
// the framework can be rehearsed in minutes rather than hours.
//
// The need is a measurement, not a guess. Filtering entities inside the replay engine
// turned out to buy only about 1.6×, so this command exists to give a run less to read:
// one pass over the source now, and every run afterwards reads a file a fraction of the
// size.
//
// A later measurement corrected the reasoning, and the correction is worth recording
// because it redirects where the time goes. This command's own pass over days 0–13 read
// 239,471,460 rows in 2 minutes 12 seconds, while the sampled replay of the same window
// took 55 minutes for 8.7M warmed-and-scored events. Reading and parsing the corpus was
// never what dominated a run — scoring is, at roughly 2,700 events/s — so a subset saves
// about two minutes of a fifty-five minute run and no more.
//
// The levers that actually shorten a run are, in order: the scoring window
// (replay's -maxseconds), dropping the shadow arms, which score every event and are
// excluded from the combination in any case, and the entity sample. Burn-in is roughly
// half the events and is frozen, so it sets the floor.
//
// # What is sampled
//
// Entities, never events. A per-entity detector is a statement about one entity's own
// history, so thinning events within an entity would corrupt exactly the histories
// under test; dropping whole entities leaves every retained history intact and merely
// shrinks the population. The co-occurrence graph and the population marginals are
// consequently built from the retained entities only, which is a real difference from a
// full run and is why a subset is for rehearsal rather than for a headline figure.
//
// Every entity named by a red-team label is kept regardless of the sample, so the
// labelled population is not itself thinned. That inflates the labelled share of the
// corpus, and a detection rate measured on a subset is therefore NOT comparable to one
// measured on the full population. The manifest written alongside the corpus records
// this in terms, so the file cannot be mistaken for the original later.
package main

import (
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"hash/fnv"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

func main() {
	var (
		authPath    = flag.String("auth", "data/lanl/auth.txt.gz", "source auth.txt.gz")
		redteamPath = flag.String("redteam", "data/lanl/redteam.txt.gz", "source redteam.txt.gz")
		outPath     = flag.String("out", "", "output auth .txt.gz (required)")
		manifest    = flag.String("manifest", "", "output manifest JSON (defaults to <out>.manifest.json)")
		maxSeconds  = flag.Int64("maxseconds", 1209600, "stop at this corpus timestamp, seconds")
		sample      = flag.Int("entity-sample", 16, "keep 1 source entity in N; labelled entities are always kept")
		residue     = flag.Int("sample-residue", 0, "which residue class of the entity hash to keep (0..N-1). Residue 0 is what the replay engine's own -entity-sample selects; any other draws a DISJOINT set of entities, for a held-out evaluation")
	)
	flag.Parse()
	if *outPath == "" {
		log.Fatal("-out is required")
	}
	if *sample < 1 {
		log.Fatal("-entity-sample must be at least 1")
	}
	mf := *manifest
	if mf == "" {
		mf = *outPath + ".manifest.json"
	}
	if *residue < 0 || (*sample > 1 && *residue >= *sample) {
		log.Fatalf("-sample-residue must lie in [0, %d)", *sample)
	}
	if err := run(*authPath, *redteamPath, *outPath, mf, *maxSeconds, uint64(*sample), uint64(*residue)); err != nil {
		log.Fatal(err)
	}
}

func run(authPath, redteamPath, outPath, manifestPath string, maxSeconds int64, sample, residue uint64) error {
	started := time.Now().UTC()

	labelled, labelRows, err := labelledUsers(redteamPath)
	if err != nil {
		return fmt.Errorf("read red-team labels: %w", err)
	}
	log.Printf("red-team labels: %d rows naming %d distinct users", labelRows, len(labelled))

	in, err := os.Open(authPath) //nolint:gosec // the corpus path the operator named
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	zr, err := gzip.NewReader(bufio.NewReaderSize(in, 4<<20))
	if err != nil {
		return err
	}

	out, err := os.Create(outPath) //nolint:gosec // the output path the operator named
	if err != nil {
		return err
	}
	bw := bufio.NewWriterSize(out, 4<<20)
	zw := gzip.NewWriter(bw)

	var (
		read, written, keptLabelled int64
		lastT                       int64
		keptUsers                   = map[string]struct{}{}
	)
	sc := bufio.NewScanner(zr)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		read++

		// The corpus is ordered in time, so the window ends the pass rather than
		// filtering it: there is nothing after this point that could be wanted.
		comma := strings.IndexByte(line, ',')
		if comma <= 0 {
			continue
		}
		t, convErr := strconv.ParseInt(line[:comma], 10, 64)
		if convErr != nil {
			continue
		}
		if maxSeconds > 0 && t >= maxSeconds {
			lastT = t
			break
		}
		lastT = t

		rest := line[comma+1:]
		next := strings.IndexByte(rest, ',')
		if next <= 0 {
			continue
		}
		user := rest[:next]

		_, isLabelled := labelled[user]
		if !isLabelled && !inSample(user, sample, residue) {
			continue
		}
		if isLabelled {
			keptLabelled++
		}
		keptUsers[user] = struct{}{}

		if _, werr := zw.Write([]byte(line)); werr != nil {
			return werr
		}
		if _, werr := zw.Write([]byte("\n")); werr != nil {
			return werr
		}
		written++

		if read%50_000_000 == 0 {
			log.Printf("... read=%d kept=%d (%.2f%%) at corpus day %.2f",
				read, written, 100*float64(written)/float64(read), float64(t)/86400)
		}
	}
	if scErr := sc.Err(); scErr != nil {
		return scErr
	}
	// Close in order: the gzip trailer, then the buffer, then the file. A failure at
	// any of the three means the corpus on disk is incomplete, so none may be ignored.
	if err = zw.Close(); err != nil {
		return fmt.Errorf("close gzip writer: %w", err)
	}
	if err = bw.Flush(); err != nil {
		return fmt.Errorf("flush output buffer: %w", err)
	}
	if err = out.Close(); err != nil {
		return fmt.Errorf("close output file: %w", err)
	}

	sum, err := fileSHA256(outPath)
	if err != nil {
		return err
	}
	finished := time.Now().UTC()

	m := map[string]any{
		"kind":          "corpus-subset",
		"source":        authPath,
		"output":        outPath,
		"output_sha256": sum,
		"started_at":    started.Format(time.RFC3339),
		"finished_at":   finished.Format(time.RFC3339),
		"window": map[string]any{
			"max_seconds":    maxSeconds,
			"last_timestamp": lastT,
			"days":           float64(lastT) / 86400,
		},
		"sampling": map[string]any{
			"keep_one_entity_in_n":     sample,
			"sample_residue":           residue,
			"selector":                 "FNV-1a 64 of the source entity identifier, modulo N, equals the residue",
			"labelled_entities_exempt": true,
			"disjointness": "two subsets of the same N and different residues share no " +
				"unlabelled entity, so a measurement on one is held out from the other. " +
				"Every residue carries the full labelled population, because labelled " +
				"entities are exempt from the sample, so the two remain comparable on the " +
				"thing being detected",
			"note": "a sample of ENTITIES, not of events: per-entity histories are left " +
				"whole and only whole entities are dropped. Every labelled entity is kept " +
				"regardless of the sample, so the labelled share of this subset is inflated " +
				"relative to the full population, and a detection rate measured on it is " +
				"NOT comparable to one measured on the full corpus. The co-occurrence graph " +
				"and the population marginals are built from the retained entities only",
		},
		"counts": map[string]any{
			"rows_read":            read,
			"rows_written":         written,
			"retained_fraction":    float64(written) / float64(read),
			"distinct_users_kept":  len(keptUsers),
			"labelled_rows_kept":   keptLabelled,
			"labelled_users_total": len(labelled),
		},
	}
	raw, err := json.MarshalIndent(m, "", " ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(manifestPath, raw, 0o600); err != nil {
		return err
	}

	log.Printf("wrote %s: read=%d kept=%d (%.3f%%), %d distinct users, %d labelled rows, sha256=%s",
		outPath, read, written, 100*float64(written)/float64(read),
		len(keptUsers), keptLabelled, sum[:12])
	log.Printf("wrote %s", manifestPath)
	return nil
}

// inSample is the deterministic selector the replay engine uses, extended by a residue
// so that disjoint samples of the same corpus can be drawn.
//
// The engine's own selector is the residue-zero case, so a subset and an in-engine sample
// of the same N still retain the same entities. Any other residue selects a set that
// shares no entity with it, which is what a held-out evaluation needs: measuring twice on
// the same accounts measures memorisation, not generalisation. Labelled entities are
// exempt from the sample everywhere, so every residue carries the full labelled
// population and the two measurements remain comparable on the thing being detected.
func inSample(user string, n, residue uint64) bool {
	if n <= 1 {
		return true
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(user))
	return h.Sum64()%n == residue%n
}

// labelledUsers reads the distinct source users named by the red-team file.
func labelledUsers(path string) (map[string]struct{}, int, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // the labels path the operator named
	if err != nil {
		return nil, 0, err
	}
	zr, err := gzip.NewReader(strings.NewReader(string(raw)))
	if err != nil {
		return nil, 0, err
	}
	users := map[string]struct{}{}
	rows := 0
	sc := bufio.NewScanner(zr)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		parts := strings.Split(line, ",")
		if len(parts) != 4 {
			return nil, 0, fmt.Errorf("red-team row %d has %d fields, want 4", rows+1, len(parts))
		}
		rows++
		users[parts[1]] = struct{}{}
	}
	return users, rows, sc.Err()
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // hashing the file just written
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	buf := make([]byte, 1<<20)
	for {
		n, rerr := f.Read(buf)
		if n > 0 {
			h.Write(buf[:n])
		}
		if rerr != nil {
			break
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
