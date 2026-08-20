package cellgrid_test

import (
	"context"
	"slices"
	"testing"

	"github.com/JohnPierman/ethogram/domain/cellgrid"
	"github.com/JohnPierman/ethogram/domain/detector"
	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/novelty"
	"github.com/JohnPierman/ethogram/domain/timing"
)

// memoryCounts is a minimal in-memory ValueCountRepository for the ablation's cells.
type memoryCounts struct {
	halfLife novelty.HalfLife
	rows     map[string]map[string]*row
}

type row struct {
	count    float64
	first    event.Timestamp
	lastSeen event.Timestamp
}

func newMemoryCounts(h novelty.HalfLife) *memoryCounts {
	return &memoryCounts{halfLife: h, rows: map[string]map[string]*row{}}
}

func key(s event.SourceID, e event.EntityID, f event.FieldPath) string {
	return string(s) + "\x1f" + string(e) + "\x1f" + string(f)
}

func (m *memoryCounts) FindAllByEntityField(_ context.Context, s event.SourceID, e event.EntityID, f event.FieldPath, at event.Timestamp) ([]novelty.ValueRow, error) {
	byValue := m.rows[key(s, e, f)]
	values := make([]string, 0, len(byValue))
	for v := range byValue {
		values = append(values, v)
	}
	slices.Sort(values)
	out := make([]novelty.ValueRow, 0, len(values))
	for _, v := range values {
		r := byValue[v]
		out = append(out, novelty.ValueRow{
			Value: v, Count: novelty.Decay(r.count, r.lastSeen, at, m.halfLife),
			FirstSeen: r.first, LastSeen: r.lastSeen,
		})
	}
	return out, nil
}

func (m *memoryCounts) SaveObservation(_ context.Context, s event.SourceID, e event.EntityID, f event.FieldPath, value string, at event.Timestamp) error {
	k := key(s, e, f)
	byValue, ok := m.rows[k]
	if !ok {
		byValue = map[string]*row{}
		m.rows[k] = byValue
	}
	r, ok := byValue[value]
	if !ok {
		byValue[value] = &row{count: 1, first: at, lastSeen: at}
		return nil
	}
	r.count = novelty.Accumulate(r.count, r.lastSeen, at, m.halfLife)
	r.lastSeen = at
	return nil
}

// timingMemory is the timing state repository from the timing tests, duplicated
// minimally here to wire the circular detector alongside the ablation.
type timingMemory struct{ states map[string]*timing.State }

func (m *timingMemory) FindByEntity(_ context.Context, s event.SourceID, e event.EntityID) (*timing.State, bool, error) {
	st, ok := m.states[string(s)+"\x1f"+string(e)]
	if !ok {
		return nil, false, nil
	}
	c := &timing.State{Moments: timing.NewMoments(st.Moments.H()), LastSeen: st.LastSeen}
	copy(c.Moments.C, st.Moments.C)
	copy(c.Moments.S, st.Moments.S)
	c.Moments.W = st.Moments.W
	return c, true, nil
}

func (m *timingMemory) SaveState(_ context.Context, s event.SourceID, e event.EntityID, st *timing.State) error {
	m.states[string(s)+"\x1f"+string(e)] = st
	return nil
}

const (
	src    = event.SourceID("lanl.auth")
	entity = event.EntityID("U66@DOM1")
	hl     = novelty.HalfLife(14 * event.Day)
)

func mk(at event.Timestamp, offset int64) *event.Event {
	e := event.New(src, entity, at, map[event.FieldPath]event.Value{
		"auth.authentication_type": event.NewValue("Kerberos"),
	}, offset)
	return &e
}

func TestCellOf(t *testing.T) {
	if got := cellgrid.CellOf(0); got != 0 {
		t.Fatalf("CellOf(0) = %d", got)
	}
	if got := cellgrid.CellOf(23 * event.Hour); got != 23 {
		t.Fatalf("CellOf(23h) = %d", got)
	}
	// Hour 167 is the last weekly cell; hour 168 wraps to 0.
	if got := cellgrid.CellOf(167 * event.Hour); got != 167 {
		t.Fatalf("CellOf(167h) = %d", got)
	}
	if got := cellgrid.CellOf(168 * event.Hour); got != 0 {
		t.Fatalf("CellOf(168h) = %d", got)
	}
}

// TestCellGridExhibitsTheBinEdgeDefect documents, in executable form, why E9 exists.
//
// One entity is active every night from 23:10 to 00:50, for fourteen nights. Both
// detectors then score a probe at 01:20, thirty minutes past the observed edge of the
// habit. The circular density of §7.2 smooths over the edge: half an hour past
// habitual activity, under a 1.5-hour bandwidth, is unremarkable. The cell grid has
// no notion of adjacency (§7.1: "an entity's activity at 09:00 is uninformative about
// 09:30"), so cell 01 is simply an unseen category and the probe scores as novel — a
// bin-edge artefact "for no reason relating to behaviour". The assertion is an order
// of magnitude: the grid's p-value at least ten times smaller than the circular
// detector's on identical history (measured here at roughly twenty-three times). The
// operational magnitude on real data is E9's business, not this unit test's.
func TestCellGridExhibitsTheBinEdgeDefect(t *testing.T) {
	ctx := context.Background()

	cells := cellgrid.NewDetector(newMemoryCounts(hl), 1.0, hl)
	// The level-set mass explicitly, not the production default. This test compares two
	// REPRESENTATIONS of the same clock -- a 168-cell grid against the circular density --
	// so it needs the statistic that reads the density directly. The standardised form of
	// #26 divides by the spread of the entity's own ln U, and this entity is perfectly
	// regular by construction: four events a night inside one narrow window, so that spread
	// is zero and the detector correctly declines to standardise against it.
	circular := timing.NewDetector(&timingMemory{states: map[string]*timing.State{}}, 1.5, hl, false)

	offset := int64(0)
	for night := range 14 {
		for _, minutes := range []event.Timestamp{
			23*60 + 10, 23*60 + 40, 0*60 + 20, 0*60 + 50, // 23:10, 23:40, 00:20, 00:50
		} {
			day := event.Timestamp(night)
			if minutes < 12*60 {
				day++ // times after midnight fall on the next calendar day
			}
			at := day*event.Day + minutes*event.Minute
			offset++
			for _, d := range []detector.Detector{cells, circular} {
				_, obs, err := d.Score(ctx, mk(at, offset))
				if err != nil {
					t.Fatal(err)
				}
				if err := obs.Commit(ctx); err != nil {
					t.Fatal(err)
				}
			}
		}
	}

	probeAt := 15*event.Day + 1*event.Hour + 20*event.Minute // 01:20
	pOf := func(d detector.Detector) float64 {
		verdicts, _, err := d.Score(ctx, mk(probeAt, offset+1))
		if err != nil {
			t.Fatal(err)
		}
		p, ok := verdicts[0].PValue()
		if !ok {
			t.Fatal("expected an evaluated verdict")
		}
		return p
	}

	pCells, pCircular := pOf(cells), pOf(circular)

	if pCircular < 0.05 {
		t.Errorf("circular P(01:20) = %v; half an hour past a nightly habit must not be alarming", pCircular)
	}
	if pCells*10 > pCircular {
		t.Errorf("the bin-edge artefact did not manifest: cells P = %v vs circular P = %v",
			pCells, pCircular)
	}
	t.Logf("bin-edge defect: cells P(01:20) = %.5f, circular P(01:20) = %.4f", pCells, pCircular)
}

// TestCellGridScoreBeforeObserveAndDeterminism mirrors the framework guarantees: the
// ablation must be a fair comparator, not a strawman implementation.
func TestCellGridScoreBeforeObserveAndDeterminism(t *testing.T) {
	ctx := context.Background()
	repo := newMemoryCounts(hl)
	d := cellgrid.NewDetector(repo, 1.0, hl)

	e := mk(9*event.Hour, 1)
	verdicts, obs, err := d.Score(ctx, e)
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := verdicts[0].PValue(); p != 1.0 {
		t.Fatalf("cold start must give exactly P = 1, got %v", p)
	}
	if len(repo.rows) != 0 {
		t.Fatal("Score wrote state")
	}
	for range 3 {
		if err := obs.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	rows, _ := repo.FindAllByEntityField(ctx, src, entity, "__weekly_hour_cell__", 9*event.Hour)
	if len(rows) != 1 || rows[0].Count != 1 {
		t.Fatalf("triple commit produced %+v", rows)
	}

	// Repeat scoring is byte-identical.
	first, _, _ := d.Score(ctx, mk(10*event.Hour, 2))
	second, _, _ := d.Score(ctx, mk(10*event.Hour, 2))
	if first.Digest() != second.Digest() {
		t.Fatal("repeated scoring differed")
	}
}
