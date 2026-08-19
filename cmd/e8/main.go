// Command e8 executes evaluation hypothesis E8 (§12.3) as a recorded run and writes
// its result JSON:
//
//	"Batch-independence holds. Identical events replayed in differing batch
//	 compositions must yield identical scores, a direct test of R1 and of (1)'s
//	 removal."
//
// E8 needs no corpus, which is why §12.3 lists it first: it is the gate every other
// hypothesis depends on. The check is run here against the wired framework — every
// detector the replay path registers, and the equation (18) combination, not a fixture
// — so the recorded digests describe the system that produces every other result in
// results/. That claim is only true while wire() and the replay path agree; see wire().
//
// Three properties are measured, each with its own acceptance:
//
//  1. Batch independence: one probe event scored inside batch compositions that differ
//     in size by two orders of magnitude, requiring byte-identical verdicts and an
//     identical combined p-value. Co-resident events are appended AFTER the probe,
//     which is the direction batch standardisation gets wrong, since μ̂_B in equation
//     (1) is computed over the whole window including events that arrive later.
//  2. Repeat determinism (R4): the same event against the same state, scored many
//     times, byte-identical every time.
//  3. The negative control: a detector standardising against the batch per equation
//     (1) must FAIL the same check. A check that cannot fail is not evidence, so a
//     run in which the control passes is itself a failure and is recorded as one.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/JohnPierman/ethogram/application"
	"github.com/JohnPierman/ethogram/domain/cooccurrence"
	"github.com/JohnPierman/ethogram/domain/detector"
	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/marginal"
	"github.com/JohnPierman/ethogram/domain/novelty"
	"github.com/JohnPierman/ethogram/domain/registry"
	"github.com/JohnPierman/ethogram/domain/timing"
	"github.com/JohnPierman/ethogram/domain/volume"
	"github.com/JohnPierman/ethogram/infrastructure/state/memory"
)

const (
	source   = event.SourceID("lanl.auth")
	probeE   = event.EntityID("U66@DOM1")
	fillerE  = event.EntityID("U3005@DOM1")
	halfLife = novelty.HalfLife(7 * event.Day)
)

func main() {
	var (
		outPath = flag.String("out", "", "result JSON path (required)")
		runID   = flag.String("run-id", "", "run identifier (required)")
		repeats = flag.Int("repeats", 64, "repeat count for the R4 determinism check")
	)
	flag.Parse()
	if *outPath == "" || *runID == "" {
		log.Fatal("both -out and -run-id are required")
	}
	if err := run(*outPath, *runID, *repeats); err != nil {
		log.Fatal(err)
	}
}

func mk(entity event.EntityID, at event.Timestamp, authType, dst, logon string, offset int64) *event.Event {
	e := event.New(source, entity, at, map[event.FieldPath]event.Value{
		"auth.authentication_type":  event.NewValue(authType),
		"auth.destination_computer": event.NewValue(dst),
		"auth.logon_type":           event.NewValue(logon),
		"auth.success_failure":      event.NewValue("Success"),
	}, offset)
	return &e
}

// history is the state the probe is judged against, identical in every case.
func history() []*event.Event {
	var out []*event.Event
	offset := int64(0)
	for day := range 12 {
		for _, hour := range []event.Timestamp{9, 11, 14} {
			offset++
			out = append(out, mk(probeE, event.Timestamp(day)*event.Day+hour*event.Hour,
				"Kerberos", "C625", "Network", offset))
		}
	}
	return out
}

// probe is the event whose scores must not move: a novel value at an unusual hour.
func probe() *event.Event {
	return mk(probeE, 12*event.Day+3*event.Hour, "NTLM", "C17693", "Batch", 9001)
}

// filler is co-resident traffic appended after the probe.
func filler(n int) []*event.Event {
	out := make([]*event.Event, 0, n)
	for i := range n {
		out = append(out, mk(fillerE, 12*event.Day+4*event.Hour+event.Timestamp(i)*event.Minute,
			"NTLM", fmt.Sprintf("C%d", 30000+i), "Network", int64(20000+i)))
	}
	return out
}

// wiredDetectorIDs lists what wire() actually registers, sorted, so the provenance block
// states the composition that was gated rather than a list kept by hand. The
// hand-written one said four detectors after Detector IV was added, so every E8 result
// since then claimed a composition it had not tested — the same defect the replay path
// carried, and the one thing a provenance block exists to prevent.
func wiredDetectorIDs() []string {
	reg, _ := wire()
	all := reg.All()
	out := make([]string, 0, len(all))
	for _, d := range all {
		out = append(out, string(d.ID()))
	}
	sort.Strings(out)
	return out
}

// minMarginalObservations mirrors the replay path's Detector IV abstention floor (§9),
// so the gate scores the detector under the same configuration the recorded runs use.
const minMarginalObservations = 1000

// wire assembles the full framework over fresh in-memory state.
//
// Every detector the replay path registers must be registered here too. The gate's
// worth is that its digests describe the system producing the other results, and a
// detector absent from this list is a detector whose batch independence and determinism
// nothing measures — which is exactly what happened to Detector IV between its addition
// and this comment.
func wire() (*detector.Registry, *registry.Registry) {
	fieldRegistry := registry.New(registry.DefaultPolicy())
	timStore := memory.NewTimingStore()
	detectors := detector.NewRegistry()
	for _, d := range []detector.Detector{
		novelty.NewDetector(memory.NewNoveltyStore(halfLife), fieldRegistry, 1.0, halfLife),
		timing.NewDetector(timStore, 1.5, halfLife),
		volume.NewDetector(memory.NewVolumeStore(), timStore, 1.5, halfLife),
		cooccurrence.NewDetector(cooccurrence.NewMemoryGraph(halfLife), fieldRegistry, nil, halfLife),
		marginal.NewDetector(memory.NewMarginalStore(halfLife), fieldRegistry, 1.0,
			minMarginalObservations, halfLife),
	} {
		if err := detectors.Register(d); err != nil {
			panic(err) // registration of a fixed detector set cannot fail in practice
		}
	}
	return detectors, fieldRegistry
}

// controlPoint records one composition's batch-relative z-score against the closed
// form of equation (2).
type controlPoint struct {
	BatchSize     int     `json:"batch_size"`
	CampaignShare float64 `json:"campaign_share_p"`
	ZMeasured     float64 `json:"z_measured"`
	ZPredicted    float64 `json:"z_predicted_sqrt_1_minus_p_over_p"`
	AbsoluteError float64 `json:"absolute_error"`
}

type caseResult struct {
	Name         string  `json:"name"`
	BatchSize    int     `json:"batch_size"`
	Digest       string  `json:"verdict_digest"`
	CombinedP    float64 `json:"combined_p"`
	CombinedBits string  `json:"combined_p_bits"`
	J            int     `json:"j"`
}

// scoreProbeInBatch replays a batch through the wired framework and returns the
// probe's canonical digest and combination.
func scoreProbeInBatch(ctx context.Context, batch []*event.Event, probeID event.ID) (caseResult, error) {
	detectors, fieldRegistry := wire()

	var found *application.ScoredEvent
	cmd := &application.ReplayCorpusCommand{
		Source:        &sliceSource{events: batch},
		Detectors:     detectors,
		FieldRegistry: fieldRegistry,
		// Everything is scored: batch independence is about scoring, not warm-up.
		BurnInEnd: 0,
		Sink: func(se application.ScoredEvent) error {
			if se.Event.ID() == probeID {
				c := se
				found = &c
			}
			return nil
		},
	}
	if _, err := cmd.Execute(ctx); err != nil {
		return caseResult{}, err
	}
	if found == nil {
		return caseResult{}, errors.New("probe was not scored")
	}
	digest := found.Verdicts.Digest()
	res := caseResult{
		BatchSize: len(batch),
		Digest:    fmt.Sprintf("%x", digest),
	}
	if found.Combined != nil {
		res.CombinedP = found.Combined.P
		res.CombinedBits = fmt.Sprintf("%#016x", math.Float64bits(found.Combined.P))
		res.J = found.Combined.J
	}
	return res, nil
}

func run(outPath, runID string, repeats int) error {
	started := time.Now().UTC()
	ctx := context.Background()

	h, p := history(), probe()
	probeID := p.ID()

	build := func(tail []*event.Event) []*event.Event {
		batch := make([]*event.Event, 0, len(h)+1+len(tail))
		batch = append(batch, h...)
		batch = append(batch, p)
		batch = append(batch, tail...)
		return batch
	}

	compositions := []struct {
		name string
		tail []*event.Event
	}{
		{"probe_alone", nil},
		{"one_co_resident", filler(1)},
		{"campaign_50", filler(50)},
		{"campaign_500", filler(500)},
		{"campaign_2000", filler(2000)},
	}

	var cases []caseResult
	for _, c := range compositions {
		res, err := scoreProbeInBatch(ctx, build(c.tail), probeID)
		if err != nil {
			return fmt.Errorf("case %s: %w", c.name, err)
		}
		res.Name = c.name
		cases = append(cases, res)
	}

	identical := true
	for _, c := range cases[1:] {
		if c.Digest != cases[0].Digest || c.CombinedBits != cases[0].CombinedBits {
			identical = false
		}
	}

	// R4: repeated scoring of the same event against the same state.
	repeatDigest := ""
	repeatStable := true
	for range repeats {
		res, err := scoreProbeInBatch(ctx, build(nil), probeID)
		if err != nil {
			return err
		}
		if repeatDigest == "" {
			repeatDigest = res.Digest
			continue
		}
		if res.Digest != repeatDigest {
			repeatStable = false
		}
	}

	// The negative control: equation (1) applied to the same compositions. Its
	// z-scores are also checked against the closed form of equation (2), which is
	// the sharpest single argument of §3.2: for a batch in which a fraction p share
	// one value of a count feature and the rest another, the campaign event's own
	// z-score is exactly √((1−p)/p), independent of the gap between the two values,
	// and therefore strictly decreasing in p. Measuring it here turns the paper's
	// derivation into a confirmed prediction rather than an assertion.
	controlDigests := make([]string, 0, len(compositions))
	controlPoints := make([]controlPoint, 0, len(compositions))
	for _, c := range compositions {
		batch := build(c.tail)
		digest, z := batchRelativeDigest(batch, probeID)
		controlDigests = append(controlDigests, digest)

		// The campaign is the co-resident filler plus the probe: they share the
		// long destination-name value, the rest of the batch shares the short one.
		campaign := float64(len(c.tail) + 1)
		share := campaign / float64(len(batch))
		predicted := math.Sqrt((1 - share) / share)
		controlPoints = append(controlPoints, controlPoint{
			BatchSize: len(batch), CampaignShare: share,
			ZMeasured: z, ZPredicted: predicted,
			AbsoluteError: math.Abs(z - predicted),
		})
	}
	controlDiffers := false
	for _, d := range controlDigests[1:] {
		if d != controlDigests[0] {
			controlDiffers = true
		}
	}

	// Equation (2) holds when every measured z matches its closed form, and the
	// sequence is strictly decreasing in the campaign's share of the batch.
	equation2Holds := true
	for i, cp := range controlPoints {
		if cp.AbsoluteError > 1e-9 {
			equation2Holds = false
		}
		if i > 0 && controlPoints[i].CampaignShare > controlPoints[i-1].CampaignShare &&
			controlPoints[i].ZMeasured >= controlPoints[i-1].ZMeasured {
			equation2Holds = false
		}
	}

	pass := identical && repeatStable && controlDiffers && equation2Holds

	out := map[string]any{
		"schema_version": "1",
		"kind":           "e8",
		"hypothesis":     []string{"E8"},
		"paper_refs": map[string]any{
			"sections":  []string{"§3.2", "§4", "§5.2", "§12.3"},
			"equations": []int{1, 2, 18},
		},
		"run": map[string]any{
			"run_id": runID, "started_at": started.Format(time.RFC3339),
			"finished_at": time.Now().UTC().Format(time.RFC3339),
			"go_version":  runtime.Version(),
			"os_arch":     runtime.GOOS + "/" + runtime.GOARCH,
			"detectors":   wiredDetectorIDs(),
		},
		"corpus": map[string]any{
			"files": []map[string]any{},
			"synthetic": "E8 requires no corpus (§12.3); the probe and its history are " +
				"constructed so the batch compositions differ by two orders of magnitude",
			"coverage": map[string]any{"kind": "control"},
		},
		"parameters": map[string]any{
			"repeats":        repeats,
			"compositions":   len(compositions),
			"half_life_days": 7, "bandwidth_hours": 1.5, "alpha": 1.0,
		},
		"results": map[string]any{
			"batch_independence": map[string]any{
				"identical": identical,
				"cases":     cases,
			},
			"repeat_determinism": map[string]any{
				"stable": repeatStable, "repeats": repeats, "digest": repeatDigest,
			},
			"negative_control": map[string]any{
				"description": "a detector standardising against the batch per equation (1); " +
					"its scores MUST differ across these compositions, or the check has no power",
				"differs": controlDiffers,
				"digests": controlDigests,
			},
			"pass": pass,
		},
		"provenance_complete": true,
	}
	if err := writeJSON(outPath, out); err != nil {
		return err
	}
	log.Printf("E8: identical=%v across batch sizes %v; repeats stable=%v (%d); "+
		"negative control differs=%v; equation (2) holds=%v; PASS=%v",
		identical, sizes(cases), repeatStable, repeats, controlDiffers, equation2Holds, pass)
	for _, cp := range controlPoints {
		log.Printf("  eq(2): n=%5d p=%.5f  z measured %.6f  predicted %.6f  |err| %.2e",
			cp.BatchSize, cp.CampaignShare, cp.ZMeasured, cp.ZPredicted, cp.AbsoluteError)
	}
	if !pass {
		return errors.New("E8 failed; the result file records the failure")
	}
	return nil
}

func sizes(cases []caseResult) []int {
	out := make([]int, len(cases))
	for i, c := range cases {
		out[i] = c.BatchSize
	}
	return out
}

// batchRelativeDigest scores the probe under equation (1): standardisation against
// the batch under evaluation. This is the §3.2 formulation the framework rejects, and
// its scores must move as the composition changes.
func batchRelativeDigest(batch []*event.Event, probeID event.ID) (digest string, z float64) {
	const field = event.FieldPath("auth.destination_computer")
	xs := make([]float64, 0, len(batch))
	for _, e := range batch {
		if v, ok := e.Get(field); ok {
			xs = append(xs, float64(len(v.Text())))
		}
	}
	mean := 0.0
	for _, x := range xs {
		mean += x
	}
	if len(xs) > 0 {
		mean /= float64(len(xs))
	}
	varsum := 0.0
	for _, x := range xs {
		varsum += (x - mean) * (x - mean)
	}
	sd := 0.0
	if len(xs) > 0 {
		sd = math.Sqrt(varsum / float64(len(xs))) // population sd, as §3.2 specifies
	}

	for _, e := range batch {
		if e.ID() != probeID {
			continue
		}
		v, _ := e.Get(field)
		if sd > 0 {
			z = (float64(len(v.Text())) - mean) / sd
		}
		return fmt.Sprintf("%#016x", math.Float64bits(z)), z
	}
	return "probe-absent", 0
}

type sliceSource struct {
	events []*event.Event
	next   int
}

func (s *sliceSource) Next() (*event.Event, error) {
	if s.next >= len(s.events) {
		return nil, io.EOF
	}
	e := s.events[s.next]
	s.next++
	return e, nil
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
