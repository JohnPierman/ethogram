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

// scoreBursts drives one entity that produces a burst of events every gapDays and returns
// the p-values it was scored on after history is established, together with how often it
// abstained and the dispersion it managed to measure.
//
// Burst sizes vary benignly and deterministically: nothing here is an attack, and every
// p-value the arm reports is therefore a false alarm waiting to happen.
func scoreBursts(t *testing.T, gapDays int) (scored []float64, abstained int, windows, phi float64) {
	t.Helper()
	ctx := context.Background()

	mv := &memVolume{states: map[string]*volume.State{}}
	mt := &memTiming{states: map[string]*timing.State{}}
	vd := volume.NewDetector(mv, mt, 1.5, novelty.HalfLife(7*event.Day), 0)
	td := timing.NewDetector(mt, 1.5, novelty.HalfLife(7*event.Day), true)

	sizes := []int{8, 22, 11, 35, 14, 19, 27, 9, 16, 30, 12, 25}
	offset := int64(0)

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
					scored = append(scored, p)
				} else if verdicts[0].Status() == detector.StatusAbstainedUnusable {
					abstained++
				}
			}
			if err := obs.Commit(ctx); err != nil {
				t.Fatalf("Commit: %v", err)
			}
		}
	}

	st, _, _ := mv.FindByEntity(ctx, vSrc, vEntity)
	sort.Float64s(scored)
	return scored, abstained, st.DispersionWindows,
		volume.Dispersion(st.DispersionSum, st.DispersionWindows)
}

// TestSparseEntityIsNotScoredAgainstAnUnwidenedNull is the regression test for the
// sub-1e-12 pile.
//
// The accumulator that measures an entity's dispersion is discounted by elapsed calendar
// hours, so its weight saturates at 1/(1-delta) and an entity whose active windows are far
// enough apart can never reach [volume.MinDispersionWindows]. Such an entity used to fall
// back to equation (11) un-widened -- the narrowest null the arm has -- and a wholly benign
// burst then scored tens of orders of magnitude into the tail. It must now abstain.
func TestSparseEntityIsNotScoredAgainstAnUnwidenedNull(t *testing.T) {
	for _, gapDays := range []int{4, 7} {
		scored, abstained, windows, phi := scoreBursts(t, gapDays)

		if volume.DispersionMeasurable(windows) {
			t.Fatalf("burst every %d days reached %.2f windows, which the gate admits;"+
				" this test needs a sparsity the gate cannot admit", gapDays, windows)
		}
		if phi != 1 {
			t.Errorf("burst every %d days measured phi=%v on %.2f windows; an"+
				" unmeasurable dispersion must report the floor", gapDays, phi, windows)
		}
		if len(scored) != 0 {
			t.Errorf("burst every %d days was scored on %d events with an unmeasurable"+
				" dispersion, smallest p=%.3g; it must abstain instead",
				gapDays, len(scored), scored[0])
		}
		if abstained == 0 {
			t.Errorf("burst every %d days neither scored nor abstained", gapDays)
		}
	}
}

// TestEstablishedEntityStillScores. The abstention must not swallow the entities the arm
// exists to serve: one active often enough to measure its own dispersion is still scored,
// and its ordinary variation stays far from the tail.
func TestEstablishedEntityStillScores(t *testing.T) {
	for _, gapDays := range []int{1, 2} {
		scored, _, windows, phi := scoreBursts(t, gapDays)

		if !volume.DispersionMeasurable(windows) {
			t.Fatalf("burst every %d days reached only %.2f windows", gapDays, windows)
		}
		if len(scored) == 0 {
			t.Fatalf("burst every %d days abstained on everything despite %.2f windows",
				gapDays, windows)
		}
		if phi <= 1 {
			t.Errorf("burst every %d days measured phi=%v; a bursty account must widen"+
				" its own null", gapDays, phi)
		}
		// The whole account is benign, so nothing it does should reach the tail the
		// framework's realised alert cuts sit at.
		if scored[0] < 1e-6 {
			t.Errorf("burst every %d days scored a benign event at %.3g, which would"+
				" compete for an alert slot", gapDays, scored[0])
		}
	}
}

// TestDispersionReachableNamesTheUnsatisfiableRegion. The bound is arithmetic and is
// asserted so that a change to the half-life or to the minimum cannot silently recreate a
// region where the gate can never open.
func TestDispersionReachableNamesTheUnsatisfiableRegion(t *testing.T) {
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
	// Degenerate inputs must not report an unsatisfiable gate.
	for _, tc := range [][2]float64{{0, weekHours}, {24, 0}, {-1, weekHours}} {
		if !volume.DispersionReachable(tc[0], tc[1]) {
			t.Errorf("DispersionReachable(%v, %v) = false on a degenerate input",
				tc[0], tc[1])
		}
	}
}
