// Command wraparound executes the §12.5 wraparound control as a recorded run and
// writes its result JSON, including the fitted curves that figures F1 and F8 render.
//
// The control is synthetic by definition — §12.5 constructs "a synthetic entity
// active exclusively between 23:00 and 01:00" — so a run of this command is a real
// run of real scoring code on the control's prescribed input, not an invented
// number. The JSON carries the schedule, both representations' scores at the three
// prescribed probe times, the full fitted circular density on the evaluation grid,
// the 168-cell masses, and pass/fail against the §12.5 acceptance criteria.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"math"
	"os"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/JohnPierman/ethogram/domain/cellgrid"
	"github.com/JohnPierman/ethogram/domain/detector"
	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/novelty"
	"github.com/JohnPierman/ethogram/domain/timing"
)

func main() {
	var (
		outPath = flag.String("out", "", "result JSON path (required)")
		runID   = flag.String("run-id", "", "run identifier (required)")
	)
	flag.Parse()
	if *outPath == "" || *runID == "" {
		log.Fatal("both -out and -run-id are required")
	}
	if err := run(*outPath, *runID); err != nil {
		log.Fatal(err)
	}
}

// memoryTiming and memoryCounts are minimal in-process repositories for the two
// representations under test.
type memoryTiming struct{ state *timing.State }

func (m *memoryTiming) FindByEntity(context.Context, event.SourceID, event.EntityID) (*timing.State, bool, error) {
	if m.state == nil {
		return nil, false, nil
	}
	c := &timing.State{Moments: timing.NewMoments(m.state.Moments.H()), LastSeen: m.state.LastSeen}
	copy(c.Moments.C, m.state.Moments.C)
	copy(c.Moments.S, m.state.Moments.S)
	c.Moments.W = m.state.Moments.W
	return c, true, nil
}

func (m *memoryTiming) SaveState(_ context.Context, _ event.SourceID, _ event.EntityID, s *timing.State) error {
	m.state = s
	return nil
}

type memoryCounts struct {
	halfLife novelty.HalfLife
	rows     map[string]*cell
}

type cell struct {
	count    float64
	first    event.Timestamp
	lastSeen event.Timestamp
}

func (m *memoryCounts) FindAllByEntityField(_ context.Context, _ event.SourceID, _ event.EntityID, _ event.FieldPath, at event.Timestamp) ([]novelty.ValueRow, error) {
	values := make([]string, 0, len(m.rows))
	for v := range m.rows {
		values = append(values, v)
	}
	slices.Sort(values)
	out := make([]novelty.ValueRow, 0, len(values))
	for _, v := range values {
		r := m.rows[v]
		out = append(out, novelty.ValueRow{
			Value: v, Count: novelty.Decay(r.count, r.lastSeen, at, m.halfLife),
			FirstSeen: r.first, LastSeen: r.lastSeen,
		})
	}
	return out, nil
}

func (m *memoryCounts) SaveObservation(_ context.Context, _ event.SourceID, _ event.EntityID, _ event.FieldPath, value string, at event.Timestamp) error {
	r, ok := m.rows[value]
	if !ok {
		m.rows[value] = &cell{count: 1, first: at, lastSeen: at}
		return nil
	}
	r.count = novelty.Accumulate(r.count, r.lastSeen, at, m.halfLife)
	r.lastSeen = at
	return nil
}

func run(outPath, runID string) error {
	started := time.Now().UTC()
	ctx := context.Background()

	const (
		bandwidthHours = 1.5
		halfLife       = novelty.HalfLife(14 * event.Day)
		alpha          = 1.0
		nights         = 14
	)

	circular := timing.NewDetector(&memoryTiming{}, bandwidthHours, halfLife)
	cells := cellgrid.NewDetector(&memoryCounts{halfLife: halfLife, rows: map[string]*cell{}}, alpha, halfLife)

	// The prescribed schedule: activity exclusively between 23:00 and 01:00, spread
	// deterministically, for fourteen nights.
	offsets := []float64{23.0, 23.25, 23.5, 23.75, 0.25, 0.5, 0.75, 1.0}
	var schedule []event.Timestamp
	for night := range nights {
		for _, hour := range offsets {
			day := event.Timestamp(night)
			if hour < 12 {
				day++
			}
			schedule = append(schedule,
				day*event.Day+event.Timestamp(hour*float64(event.Hour)))
		}
	}
	slices.Sort(schedule)

	offset := int64(0)
	for _, at := range schedule {
		offset++
		e := event.New("control.wraparound", "SYNTHETIC-NIGHT-ENTITY", at,
			map[event.FieldPath]event.Value{"control.marker": event.NewValue("night")}, offset)
		for _, d := range []detector.Detector{circular, cells} {
			_, obs, err := d.Score(ctx, &e)
			if err != nil {
				return err
			}
			if err := obs.Commit(ctx); err != nil {
				return err
			}
		}
	}

	// Probe the three prescribed times on the day after the schedule ends.
	probeDay := event.Timestamp(nights + 1)
	probe := func(hour float64) (pCirc, pCell float64) {
		at := probeDay*event.Day + event.Timestamp(hour*float64(event.Hour))
		offset++
		e := event.New("control.wraparound", "SYNTHETIC-NIGHT-ENTITY", at,
			map[event.FieldPath]event.Value{"control.marker": event.NewValue("night")}, offset)
		vs1, _, err := circular.Score(ctx, &e)
		if err != nil {
			log.Fatal(err)
		}
		vs2, _, err := cells.Score(ctx, &e)
		if err != nil {
			log.Fatal(err)
		}
		p1, _ := vs1[0].PValue()
		p2, _ := vs2[0].PValue()
		return p1, p2
	}

	c2330, cell2330 := probe(23.5)
	c0030, cell0030 := probe(0.5)
	cNoon, cellNoon := probe(12.0)

	// The full curves for the figures: P(hour) for both representations across the
	// whole day, and the fitted density itself.
	kappa := timing.KappaForBandwidthHours(bandwidthHours)
	order := timing.HarmonicOrder(kappa)
	// Rebuild the density from the circular detector's persisted state by scoring:
	// the evidence card carries the moments; probe once and read them back.
	vs, _, err := circular.Score(ctx, mkProbe(probeDay*event.Day, offset+100))
	if err != nil {
		return err
	}
	ev := vs[0].Evidence().Stats
	m := timing.NewMoments(order)
	for h := 1; h <= order; h++ {
		m.C[h-1] = ev[keyOf("c", h)]
		m.S[h-1] = ev[keyOf("s", h)]
	}
	m.W = ev["W"]
	density := timing.NewDensity(m, timing.KernelCoefficients(kappa, order))

	type curvePoint struct {
		Hour    float64 `json:"hour"`
		Density float64 `json:"density"`
		PCirc   float64 `json:"p_circular"`
		PCell   float64 `json:"p_cells"`
	}
	curve := make([]curvePoint, 0, 96)
	for i := range 96 {
		hour := float64(i) / 4.0
		phi := 2 * math.Pi * hour / 24
		f := density.Evaluate(phi)
		pC, pCl := probeAt(ctx, circular, cells, probeDay, hour, &offset)
		curve = append(curve, curvePoint{Hour: hour, Density: f, PCirc: pC, PCell: pCl})
	}

	pass := c2330 >= 0.20 && c0030 >= 0.20 && cNoon <= 0.02
	cellsFail := cellNoon > cNoon || cell2330 < c2330/2 || cell0030 < c0030/2

	out := map[string]any{
		"schema_version": "1",
		"kind":           "control",
		"hypothesis":     []string{"E9"},
		"paper_refs":     map[string]any{"sections": []string{"§7.1", "§7.2", "§12.5"}, "equations": []int{6, 7, 8, 9}},
		"run": map[string]any{
			"run_id": runID, "started_at": started.Format(time.RFC3339),
			"finished_at": time.Now().UTC().Format(time.RFC3339),
			"go_version":  runtime.Version(),
			"note": "the §12.5 wraparound control: a synthetic entity is the control's " +
				"prescribed input; the scores are real runs of the scoring code",
		},
		"corpus": map[string]any{
			"files":     []map[string]any{},
			"synthetic": "entity active exclusively 23:00-01:00 for 14 nights, 8 events per night",
			"coverage":  map[string]any{"kind": "control"},
		},
		"parameters": map[string]any{
			"bandwidth_hours": bandwidthHours, "kappa": kappa, "H": order,
			"half_life_days": 14, "alpha": alpha, "grid": timing.GridSize,
		},
		"results": map[string]any{
			"probes": map[string]any{
				"circular":  map[string]float64{"p_23_30": c2330, "p_00_30": c0030, "p_12_00": cNoon},
				"cells_168": map[string]float64{"p_23_30": cell2330, "p_00_30": cell0030, "p_12_00": cellNoon},
			},
			"acceptance": map[string]any{
				"criteria":          "23:30 and 00:30 unremarkable (P >= 0.20), 12:00 unusual (P <= 0.02)",
				"circular_passes":   pass,
				"cells_show_defect": cellsFail,
			},
			"curve": curve,
		},
		"provenance_complete": true,
	}
	if err := writeJSON(outPath, out); err != nil {
		return err
	}
	log.Printf("wraparound control: circular P(23:30)=%.4f P(00:30)=%.4f P(12:00)=%.6f pass=%v; "+
		"cells P(23:30)=%.4f P(00:30)=%.4f P(12:00)=%.6f defect=%v",
		c2330, c0030, cNoon, pass, cell2330, cell0030, cellNoon, cellsFail)
	return nil
}

func mkProbe(at event.Timestamp, offset int64) *event.Event {
	e := event.New("control.wraparound", "SYNTHETIC-NIGHT-ENTITY", at,
		map[event.FieldPath]event.Value{"control.marker": event.NewValue("night")}, offset)
	return &e
}

func probeAt(ctx context.Context, circular *timing.Detector, cells *cellgrid.Detector, day event.Timestamp, hour float64, offset *int64) (float64, float64) {
	*offset++
	e := mkProbe(day*event.Day+event.Timestamp(hour*float64(event.Hour)), *offset)
	vs1, _, err := circular.Score(ctx, e)
	if err != nil {
		log.Fatal(err)
	}
	vs2, _, err := cells.Score(ctx, e)
	if err != nil {
		log.Fatal(err)
	}
	p1, _ := vs1[0].PValue()
	p2, _ := vs2[0].PValue()
	return p1, p2
}

func keyOf(prefix string, h int) string {
	if h < 10 {
		return prefix + "_0" + string(rune('0'+h))
	}
	return prefix + "_" + string(rune('0'+h/10)) + string(rune('0'+h%10))
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
