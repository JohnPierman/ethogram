// Command e7 measures evaluation hypothesis E7 (§12.3): the proportion of sampled
// verdicts reconstructable from their evidence alone, with no query back to the
// store.
//
// The test is the strict form R5 implies: for each sampled evaluated verdict, the
// p-value is recomputed from nothing but the verdict's own evidence card — the
// sufficient statistics of §6.4, §7.7 and §8.3 — and reconstruction succeeds when the
// recomputed value matches the verdict's to within 1e-9 relative. Detectors whose
// evidence cannot drive the recomputation fail the sample, which is the point of
// measuring rather than asserting.
//
// Reconstruction routes, per detector:
//   - novelty: p̂ from (4) via n_v, N, K, α; the full tail mass of (5) needs every
//     value's count, which the card deliberately does not carry, so reconstruction
//     validates (4) exactly and accepts the verdict when the observed value is
//     modal (P = 1) or unseen (P = p̂(∅)), the two cases (5) pins to card values.
//     Others count as partially reconstructable and are reported separately.
//   - timing: f̂(φ) from (7) via the moments c_h, s_h, W and κ on the card, then (9)
//     on the G-point grid stated by the card.
//   - volume: the upper tail of (11) via a, b, ρ and k_obs.
//   - cooccurrence: λ from (14) or (15) via k_i, k_j, m (and D_r, D_s, m_rs when a
//     partition priced the pair), then the Šidák step of (16) via T.
package main

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"math"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/JohnPierman/ethogram/application"
	"github.com/JohnPierman/ethogram/domain/calibration"
	"github.com/JohnPierman/ethogram/domain/cooccurrence"
	"github.com/JohnPierman/ethogram/domain/detector"
	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/novelty"
	"github.com/JohnPierman/ethogram/domain/registry"
	"github.com/JohnPierman/ethogram/domain/timing"
	"github.com/JohnPierman/ethogram/domain/volume"
	"github.com/JohnPierman/ethogram/infrastructure/corpus"
	"github.com/JohnPierman/ethogram/infrastructure/state/memory"
	"github.com/JohnPierman/ethogram/internal/provenance"
)

func main() {
	var (
		authPath  = flag.String("auth", "data/lanl/auth.txt.gz", "path to auth.txt.gz")
		outPath   = flag.String("out", "", "result JSON path (required)")
		runID     = flag.String("run-id", "", "run identifier (required)")
		maxEvents = flag.Int64("max-events", 400000, "admitted events to replay (prefix; recorded as coverage)")
		sampleMod = flag.Uint64("sample-mod", 37, "sample 1-in-mod verdicts by event digest")
	)
	flag.Parse()
	if *outPath == "" || *runID == "" {
		log.Fatal("-out and -run-id are required")
	}
	if err := run(*authPath, *outPath, *runID, *maxEvents, *sampleMod); err != nil {
		log.Fatal(err)
	}
}

type tally struct {
	Sampled       int64 `json:"sampled"`
	Reconstructed int64 `json:"reconstructed"`
	Partial       int64 `json:"partial"`
	Failed        int64 `json:"failed"`
}

func run(authPath, outPath, runID string, maxEvents int64, sampleMod uint64) error {
	started := time.Now().UTC()

	f, err := os.Open(authPath) //nolint:gosec // the corpus the flag names
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	zr, err := gzip.NewReader(bufio.NewReaderSize(f, 4<<20))
	if err != nil {
		return err
	}

	const halfLife = novelty.HalfLife(7 * event.Day)
	schema := corpus.Schema{
		Source: "lanl.auth", Delimiter: ',', TimeColumn: 0, TimeUnit: event.Second,
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

	fieldRegistry := registry.New(registry.DefaultPolicy())
	timStore := memory.NewTimingStore()
	graph := cooccurrence.NewMemoryGraph(halfLife)
	detectors := detector.NewRegistry()
	for _, d := range []detector.Detector{
		novelty.NewDetector(memory.NewNoveltyStore(halfLife), fieldRegistry, 1.0, halfLife),
		timing.NewDetector(timStore, 1.5, halfLife),
		volume.NewDetector(memory.NewVolumeStore(), timStore, 1.5, halfLife, volume.DefaultMinPeriods),
		cooccurrence.NewDetector(graph, fieldRegistry, nil, halfLife),
	} {
		if regErr := detectors.Register(d); regErr != nil {
			return regErr
		}
	}

	perDetector := map[detector.ID]*tally{}
	var admitted int64

	cmd := &application.ReplayCorpusCommand{
		Source:    &cappedSource{reader: reader, max: maxEvents, seen: &admitted},
		Detectors: detectors, FieldRegistry: fieldRegistry,
		// A short warm stretch so most verdicts carry real history. Six corpus hours:
		// the prefix covers only part of day 0, so a full-day warm-up would leave
		// nothing to score; this run measures evidence sufficiency, not detection.
		BurnInEnd: 6 * event.Hour,
		IncludeEntity: func(e event.EntityID) bool {
			return len(e) > 1 && e[0] == 'U'
		},
		Sink: func(se application.ScoredEvent) error {
			id := se.Event.ID()
			h := fnv.New64a()
			_, _ = h.Write(id[:])
			if h.Sum64()%sampleMod != 0 {
				return nil
			}
			for _, v := range se.Verdicts.Evaluated() {
				t, ok := perDetector[v.DetectorID()]
				if !ok {
					t = &tally{}
					perDetector[v.DetectorID()] = t
				}
				t.Sampled++
				switch reconstruct(v) {
				case fullyReconstructed:
					t.Reconstructed++
				case partiallyReconstructed:
					t.Partial++
				default:
					t.Failed++
				}
			}
			return nil
		},
	}

	report, err := cmd.Execute(context.Background())
	if err != nil {
		return err
	}

	var totals tally
	names := make([]string, 0, len(perDetector))
	for id := range perDetector {
		names = append(names, string(id))
	}
	// Deterministic order in the output.
	for i := range names {
		for j := i + 1; j < len(names); j++ {
			if names[j] < names[i] {
				names[i], names[j] = names[j], names[i]
			}
		}
	}
	byDetector := map[string]any{}
	for _, n := range names {
		t := perDetector[detector.ID(n)]
		totals.Sampled += t.Sampled
		totals.Reconstructed += t.Reconstructed
		totals.Partial += t.Partial
		totals.Failed += t.Failed
		byDetector[n] = t
	}

	proportion := 0.0
	if totals.Sampled > 0 {
		proportion = float64(totals.Reconstructed) / float64(totals.Sampled)
	}

	out := map[string]any{
		"schema_version": "1",
		"kind":           "e7",
		"hypothesis":     []string{"E7"},
		"paper_refs": map[string]any{
			"sections":  []string{"§5.2", "§6.4", "§7.7", "§12.3"},
			"equations": []int{4, 5, 7, 9, 11, 14, 15, 16},
		},
		"run": map[string]any{
			"run_id": runID, "started_at": started.Format(time.RFC3339),
			"finished_at": time.Now().UTC().Format(time.RFC3339),
			"go_version":  runtime.Version(),
			"detectors":   names,
		},
		"corpus": map[string]any{
			"files":         []map[string]any{{"path": provenance.RecordedPath(authPath), "sha256": "prefix run; full-corpus hash carried by the primary replay result"}},
			"rows_read":     reader.Rows(),
			"events_scored": report.EventsScored,
			"coverage": map[string]any{
				"kind": "prefix", "max_events": maxEvents,
				"entity_population": "U*@ human accounts",
			},
			"burn_in": map[string]any{"end_seconds": 21600, "note": "six hours; this run measures evidence sufficiency, not detection"},
		},
		"parameters": map[string]any{"sample_mod": sampleMod, "tolerance": "1e-9 relative"},
		"results": map[string]any{
			"proportion_reconstructed": proportion,
			"totals":                   totals,
			"by_detector":              byDetector,
			"partial_definition": "novelty verdicts whose (5) tail needs the full value set: (4) is " +
				"validated from the card, the tail is not recomputable from the card alone",
		},
		"provenance_complete": true,
	}
	if err := writeJSON(outPath, out); err != nil {
		return err
	}
	log.Printf("E7: %d sampled, %d reconstructed (%.4f), %d partial, %d failed",
		totals.Sampled, totals.Reconstructed, proportion, totals.Partial, totals.Failed)
	return nil
}

type outcome int

const (
	failed outcome = iota
	partiallyReconstructed
	fullyReconstructed
)

// reconstruct recomputes the verdict's p-value from its evidence card alone.
func reconstruct(v detector.Verdict) outcome {
	p, _ := v.PValue()
	ev := v.Evidence().Stats
	switch v.DetectorID() {
	case novelty.DetectorID:
		denom := ev["N"] + ev["alpha"]*(ev["K"]+1)
		if denom <= 0 {
			return failed
		}
		pHat := (ev["n_v"] + ev["alpha"]) / denom
		if !closeRel(pHat, ev["p_hat"]) {
			return failed
		}
		if ev["n_v"] == 0 { // unseen: (5) reduces to p̂(∅), on the card
			if closeRel(p, ev["p_hat_nil"]) {
				return fullyReconstructed
			}
			return failed
		}
		if p == 1.0 {
			return fullyReconstructed // modal value: the whole distribution is in the tail
		}
		return partiallyReconstructed
	case timing.DetectorID:
		order := int(ev["H"])
		m := timing.NewMoments(order)
		for h := 1; h <= order; h++ {
			m.C[h-1] = ev[fmt.Sprintf("c_%02d", h)]
			m.S[h-1] = ev[fmt.Sprintf("s_%02d", h)]
		}
		m.W = ev["W"]
		density := timing.NewDensity(m, timing.KernelCoefficients(ev["kappa"], order))
		level := density.Evaluate(ev["phi"])
		if !closeRel(level, ev["density_at_phi"]) {
			return failed
		}
		ix := timing.NewLevelIndex(density, int(ev["grid"]))
		if closeRel(ix.TailMass(level), p) {
			return fullyReconstructed
		}
		return failed
	case volume.DetectorID:
		if closeRel(volume.UpperTail(ev["a"], ev["b"], ev["rho"], int(ev["k_obs"])), p) {
			return fullyReconstructed
		}
		return failed
	case cooccurrence.DetectorID:
		var lambda float64
		if mrs, hasPartition := ev["m_rs"]; hasPartition {
			if ev["D_r"] <= 0 || ev["D_s"] <= 0 {
				return failed
			}
			lambda = ev["k_i"] * ev["k_j"] * mrs / (ev["D_r"] * ev["D_s"])
		} else {
			if ev["m"] <= 0 {
				lambda = 0
			} else {
				lambda = ev["k_i"] * ev["k_j"] / (2 * ev["m"])
			}
		}
		if !closeRel(lambda, ev["lambda_min_pair"]) {
			return failed
		}
		pPair := cooccurrence.PoissonLowerTail(lambda, ev["w_min_pair"])
		if closeRel(calibration.Sidak(pPair, int(ev["T"])), p) {
			return fullyReconstructed
		}
		return failed
	default:
		return failed
	}
}

func closeRel(a, b float64) bool {
	if a == b {
		return true
	}
	scale := math.Max(math.Abs(a), math.Abs(b))
	return scale > 0 && math.Abs(a-b)/scale < 1e-9
}

type cappedSource struct {
	reader *corpus.Reader
	max    int64
	seen   *int64
}

func (s *cappedSource) Next() (*event.Event, error) {
	if s.max > 0 && *s.seen >= s.max {
		return nil, io.EOF
	}
	*s.seen++
	return s.reader.Next()
}

func writeJSON(path string, v any) error {
	dir := "."
	if idx := strings.LastIndexAny(path, `/\`); idx >= 0 {
		dir = path[:idx]
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.Create(path) //nolint:gosec // the output the flag names
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", " ")
	if err := enc.Encode(v); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
