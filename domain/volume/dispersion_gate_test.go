package volume_test

import (
	"context"
	"sort"
	"testing"

	"github.com/JohnPierman/ethogram/domain/detector"
	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/novelty"
	"github.com/JohnPierman/ethogram/domain/timing"
	"github.com/JohnPierman/ethogram/domain/volume"
)

// burstResult is what one entity's whole history produced.
type burstResult struct {
	scored    []float64 // sorted p-values, after history is established
	abstained int
	observed  int64   // undiscounted completed windows: what the gate counts
	weight    float64 // discounted completed windows: what it used to count
	phi       float64
}

// scoreBursts drives one entity that produces a burst of events every gapDays. Burst sizes vary
// benignly and deterministically: nothing here is an attack, so every p-value the arm reports is
// a false alarm waiting to happen.
func scoreBursts(t *testing.T, gapDays int) burstResult {
	t.Helper()
	ctx := context.Background()

	mv := &memVolume{states: map[string]*volume.State{}}
	mt := &memTiming{states: map[string]*timing.State{}}
	vd := volume.NewDetector(mv, mt, 1.5, novelty.HalfLife(7*event.Day), 0)
	td := timing.NewDetector(mt, 1.5, novelty.HalfLife(7*event.Day), true)

	sizes := []int{8, 22, 11, 35, 14, 19, 27, 9, 16, 30, 12, 25}
	offset := int64(0)
	var out burstResult

	for i := 0; i < len(sizes)*2; i++ {
		n := sizes[i%len(sizes)]
		day := i * gapDays
		for k := 0; k < n; k++ {
			at := event.Timestamp(int64(day)*int64(event.Day) +
				int64(10)*int64(event.Hour) + int64(k)*int64(event.Hour)/int64(n+1))
			e := vEvent(at, offset)
			offset++

			if _, obs, err := td.Score(ctx, e); err == nil {
				_ = obs.Commit(ctx)
			}
			verdicts, obs, err := vd.Score(ctx, e)
			if err != nil {
				t.Fatalf("Score: %v", err)
			}
			if i >= len(sizes) {
				if p, ok := verdicts[0].PValue(); ok {
					out.scored = append(out.scored, p)
				} else if verdicts[0].Status() == detector.StatusAbstainedUnusable {
					out.abstained++
				}
			}
			if err := obs.Commit(ctx); err != nil {
				t.Fatalf("Commit: %v", err)
			}
		}
	}

	st, _, _ := mv.FindByEntity(ctx, vSrc, vEntity)
	sort.Float64s(out.scored)
	out.observed = st.DispersionObserved
	out.weight = st.DispersionWindows
	out.phi = volume.Dispersion(st.DispersionSum, st.DispersionWindows, st.DispersionObserved)
	return out
}

// TestSparseEntityMeasuresItsOwnDispersion is the regression test for the sub-1e-12 pile.
//
// A benign account bursting every four or seven days used to put 31.6% and 41.7% of its own
// events below 1e-12, reaching 1e-45, and could never have stopped: the gate counted a
// DISCOUNTED window weight, which saturates at 1/(1-delta), so at that sparsity the minimum was
// unsatisfiable however long the entity was watched, and the arm fell back to equation (11)
// un-widened -- the narrowest null it has -- for exactly the entity whose variation it had no
// evidence about.
//
// The gate now counts undiscounted observations, so such an entity measures its own dispersion
// and is scored against a null that fits it.
func TestSparseEntityMeasuresItsOwnDispersion(t *testing.T) {
	for _, gapDays := range []int{4, 7} {
		r := scoreBursts(t, gapDays)

		// The old gate could not have admitted this entity: its discounted weight is below
		// the minimum and stays there. That is the condition the fix removes, asserted so
		// the test cannot quietly stop exercising it.
		if volume.DispersionReachable(float64(gapDays)*24, 7*24) {
			t.Fatalf("burst every %d days is inside the old reachable region; this test"+
				" needs a sparsity the discounted gate could never admit", gapDays)
		}
		if r.weight >= volume.MinDispersionWindows {
			t.Fatalf("burst every %d days reached a discounted weight of %.2f", gapDays,
				r.weight)
		}

		// The new gate admits it, on undiscounted observations.
		if !volume.DispersionMeasurable(r.observed) {
			t.Errorf("burst every %d days observed %d windows and is still not measurable",
				gapDays, r.observed)
		}
		if len(r.scored) == 0 {
			t.Fatalf("burst every %d days abstained on everything despite %d observed"+
				" windows", gapDays, r.observed)
		}
		if r.phi <= 1 {
			t.Errorf("burst every %d days measured phi=%v; a bursty account must widen its"+
				" own null", gapDays, r.phi)
		}

		// And the point of all of it: no benign event competes for an alert slot.
		if r.scored[0] < 1e-6 {
			t.Errorf("burst every %d days scored a benign event at %.3g, which would"+
				" compete for an alert slot", gapDays, r.scored[0])
		}
	}
}

// TestEstablishedEntityStillScores. The change must not disturb the entities the arm already
// served: one active often enough is still scored, still widens its null, and still keeps its
// ordinary variation far from the tail.
func TestEstablishedEntityStillScores(t *testing.T) {
	for _, gapDays := range []int{1, 2} {
		r := scoreBursts(t, gapDays)

		if !volume.DispersionMeasurable(r.observed) {
			t.Fatalf("burst every %d days observed only %d windows", gapDays, r.observed)
		}
		if len(r.scored) == 0 {
			t.Fatalf("burst every %d days abstained on everything", gapDays)
		}
		if r.phi <= 1 {
			t.Errorf("burst every %d days measured phi=%v", gapDays, r.phi)
		}
		if r.scored[0] < 1e-6 {
			t.Errorf("burst every %d days scored a benign event at %.3g", gapDays,
				r.scored[0])
		}
	}
}

// TestColdEntityStillAbstains. The abstention has to survive the fix for the case it was
// written for: an entity with no completed windows has no measured width for its null, and
// asserting the narrowest one is what produced the pile.
func TestColdEntityStillAbstains(t *testing.T) {
	ctx := context.Background()
	mv := &memVolume{states: map[string]*volume.State{}}
	mt := &memTiming{states: map[string]*timing.State{}}
	vd := volume.NewDetector(mv, mt, 1.5, novelty.HalfLife(7*event.Day), 0)

	if volume.DispersionMeasurable(0) {
		t.Error("an entity with no observed windows is reported measurable")
	}

	// One event: no window has closed, so nothing can have been measured.
	verdicts, obs, err := vd.Score(ctx, vEvent(9*event.Hour, 0))
	if err != nil {
		t.Fatalf("Score: %v", err)
	}
	if err := obs.Commit(ctx); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	// Cold start keeps the documented P = 1 convention rather than abstaining, because a
	// p-value of 1 cannot win an alert slot and the volume-gate probe reads it.
	if p, ok := verdicts[0].PValue(); ok && p != 1 {
		t.Errorf("cold start scored %v, want exactly 1", p)
	}
}

// TestDispersionReachableNamesTheOldUnsatisfiableRegion. The bound the fix removes is retained
// and asserted, so the constraint stays visible: it is what the sparse-entity test above uses to
// prove it is exercising the case that used to be unreachable.
func TestDispersionReachableNamesTheOldUnsatisfiableRegion(t *testing.T) {
	const weekHours = 7 * 24
	for _, tc := range []struct {
		gapHours float64
		want     bool
	}{
		{1, true}, {24, true}, {48, true}, {72, false}, {weekHours, false},
	} {
		if got := volume.DispersionReachable(tc.gapHours, weekHours); got != tc.want {
			t.Errorf("DispersionReachable(%v, %v) = %v, want %v",
				tc.gapHours, weekHours, got, tc.want)
		}
	}
	for _, tc := range [][2]float64{{0, weekHours}, {24, 0}, {-1, weekHours}} {
		if !volume.DispersionReachable(tc[0], tc[1]) {
			t.Errorf("DispersionReachable(%v, %v) = false on a degenerate input",
				tc[0], tc[1])
		}
	}
}
