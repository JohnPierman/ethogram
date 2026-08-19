// Command features exports the numeric feature vectors the baseline detectors of
// §12.4 consume: Isolation Forest, Extended Isolation Forest, Half-Space Trees, and
// Robust Random Cut Forest, run by sidecar/baselines.py.
//
// The baselines embody the standard formulation of §3: a fixed-length numeric vector
// per event, with no per-entity state. The encoding below is therefore deliberately
// the conventional one, not the framework's: categorical values are hashed to [0, 1)
// with a stable FNV-1a, time becomes hour-of-day and day-of-week fractions, and the
// schema is fixed at export time. Baselines are not part of the framework and are not
// held to R2 or R4 (§12.4); the export itself is still deterministic so the
// comparison is reproducible.
//
// Sampling: streaming the full scored window through the Python baselines is not
// feasible for the tree ensembles, so the export carries a deterministic 1-in-N
// uniform sample (FNV-1a of the event digest, no RNG) plus every red-team event,
// each row flagged. Alert-budget thresholds are estimated from the uniform sample's
// score quantiles; recall at a budget is computed over the red-team rows, which are
// excluded from threshold estimation so their inclusion cannot bias it. The result
// JSON records both counts and the sampling rate.
package main

import (
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"os"
	"strings"
	"time"

	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/infrastructure/corpus"
)

func main() {
	var (
		authPath    = flag.String("auth", "data/lanl/auth.txt.gz", "path to auth.txt.gz")
		redteamPath = flag.String("redteam", "data/lanl/redteam.txt.gz", "path to redteam.txt.gz")
		outPath     = flag.String("out", "", "output CSV .gz path (required)")
		fromSec     = flag.Int64("from", 604800, "window start, corpus seconds (the frozen burn-in end)")
		toSec       = flag.Int64("to", 2592000, "window end, corpus seconds")
		sampleMod   = flag.Uint64("sample-mod", 100, "keep rows whose digest hash % mod == 0 (uniform 1-in-mod sample)")
	)
	flag.Parse()
	if *outPath == "" {
		log.Fatal("-out is required")
	}
	if err := run(*authPath, *redteamPath, *outPath, *fromSec, *toSec, *sampleMod); err != nil {
		log.Fatal(err)
	}
}

func run(authPath, redteamPath, outPath string, fromSec, toSec int64, sampleMod uint64) error {
	started := time.Now()

	redteam, err := loadRedTeamKeys(redteamPath)
	if err != nil {
		return err
	}

	f, err := os.Open(authPath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	zr, err := gzip.NewReader(bufio.NewReaderSize(f, 4<<20))
	if err != nil {
		return err
	}

	schema := corpus.Schema{
		Source:       "lanl.auth",
		Delimiter:    ',',
		TimeColumn:   0,
		TimeUnit:     event.Second,
		EntityColumn: 1,
		Columns: []event.FieldPath{
			"", "auth.source_user", "auth.destination_user", "auth.source_computer",
			"auth.destination_computer", "auth.authentication_type", "auth.logon_type",
			"auth.authentication_orientation", "auth.success_failure",
		},
		MissingToken: "?",
		EntityFilter: func(entity string) bool {
			return len(entity) > 1 && entity[0] == 'U' && entity[1] >= '0' && entity[1] <= '9'
		},
	}
	reader := corpus.NewReader(zr, schema)

	out, err := os.Create(outPath)
	if err != nil {
		return err
	}
	zw := gzip.NewWriter(out)
	w := bufio.NewWriterSize(zw, 1<<20)

	// Header. Feature order is fixed and documented; sidecar/baselines.py reads it.
	if _, err := fmt.Fprintln(w, "t,entity,hour_frac,dow_frac,src_user_h,dst_user_h,"+
		"src_comp_h,dst_comp_h,auth_type_h,logon_type_h,orientation_h,outcome,"+
		"is_redteam,in_sample"); err != nil {
		return err
	}

	var exported, redteamRows, sampled int64
	fromAt := event.Timestamp(fromSec) * event.Second
	toAt := event.Timestamp(toSec) * event.Second

	for {
		e, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			continue // malformed rows are counted by the reader
		}
		if e.OccurredAt() < fromAt {
			continue
		}
		if e.OccurredAt() >= toAt {
			break
		}

		tSeconds := int64(e.OccurredAt() / event.Second)
		key := fmt.Sprintf("%d|%s|%s|%s", tSeconds, e.Entity(),
			text(e, "auth.source_computer"), text(e, "auth.destination_computer"))
		_, isRed := redteam[key]

		id := e.ID()
		h := fnv.New64a()
		_, _ = h.Write(id[:])
		inSample := h.Sum64()%sampleMod == 0

		if !isRed && !inSample {
			continue
		}

		hourFrac := float64(tSeconds%86400) / 86400
		dowFrac := float64((tSeconds/86400)%7) / 7

		if _, err := fmt.Fprintf(w, "%d,%s,%.6f,%.6f,%s,%s,%s,%s,%s,%s,%s,%s,%d,%d\n",
			tSeconds, e.Entity(), hourFrac, dowFrac,
			hashFeature(text(e, "auth.source_user")),
			hashFeature(text(e, "auth.destination_user")),
			hashFeature(text(e, "auth.source_computer")),
			hashFeature(text(e, "auth.destination_computer")),
			hashFeature(text(e, "auth.authentication_type")),
			hashFeature(text(e, "auth.logon_type")),
			hashFeature(text(e, "auth.authentication_orientation")),
			outcome(text(e, "auth.success_failure")),
			boolInt(isRed), boolInt(inSample)); err != nil {
			return err
		}
		exported++
		if isRed {
			redteamRows++
		}
		if inSample {
			sampled++
		}
	}

	if err := w.Flush(); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}

	log.Printf("features: %d rows exported (%d red-team, %d uniform-sampled, mod %d) "+
		"from window [%d, %d) in %s; rows read %d, prefiltered %d",
		exported, redteamRows, sampled, sampleMod, fromSec, toSec,
		time.Since(started).Round(time.Second), reader.Rows(), reader.FilteredByEntity())
	return nil
}

func text(e *event.Event, f event.FieldPath) string {
	v, ok := e.Get(f)
	if !ok || !v.IsUsable() {
		return ""
	}
	return v.Text()
}

// hashFeature maps a categorical value to a stable fraction in [0, 1); the empty
// string (absent or unusable) maps to -1, a value outside the feature's range, which
// is the conventional missing-value encoding for the tree baselines.
func hashFeature(value string) string {
	if value == "" {
		return "-1"
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(value))
	return fmt.Sprintf("%.6f", float64(h.Sum64()>>11)/float64(1<<53))
}

func outcome(value string) string {
	switch value {
	case "Success":
		return "1"
	case "Fail":
		return "0"
	default:
		return "-1"
	}
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func loadRedTeamKeys(path string) (map[string]struct{}, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	zr, err := gzip.NewReader(strings.NewReader(string(raw)))
	if err != nil {
		return nil, err
	}
	keys := make(map[string]struct{})
	sc := bufio.NewScanner(zr)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		parts := strings.Split(line, ",")
		if len(parts) != 4 {
			return nil, fmt.Errorf("redteam: %d fields in %q", len(parts), line)
		}
		keys[parts[0]+"|"+parts[1]+"|"+parts[2]+"|"+parts[3]] = struct{}{}
	}
	log.Printf("red-team keys: %d (file sha256 %s)", len(keys), hex.EncodeToString(sum[:])[:16])
	return keys, sc.Err()
}
