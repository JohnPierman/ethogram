package main

import (
	"math"
	"sort"
	"testing"
)

// The retained set determines which alerts the BH scan can ever see, so an error here
// would not crash anything — it would quietly change every discovery count in the
// experiment. The heap is therefore checked against an exhaustively known answer rather
// than a spot check.

// splitmix64 gives a deterministic, seed-reproducible sequence without math/rand, which
// the architecture tests forbid in this repository.
func splitmix64(state *uint64) uint64 {
	*state += 0x9E3779B97F4A7C15
	z := *state
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

func TestPushRetainsTheSmallestPValues(t *testing.T) {
	const n = 5 * topK

	a := &arm{perDay: map[int64]*dayStats{}}
	seed := uint64(20260815)
	ps := make([]float64, 0, n)
	for i := range n {
		// Spread p across (0,1]; the exact distribution does not matter, only that the
		// smallest topK are identifiable.
		p := float64(splitmix64(&seed)%1_000_000_001)/1e9 + 1e-12
		ps = append(ps, p)
		a.push(7, alertRec{p: p, t: int64(i)})
	}

	got := a.perDay[7].sortedAlerts()
	if len(got) != topK {
		t.Fatalf("retained %d alerts, want %d", len(got), topK)
	}

	// The answer, computed independently of the heap under test. The standard library's
	// sort is a different algorithm on a different data structure, which is what makes
	// it an independent check; a hand-rolled insertion sort would be no more independent
	// and is quadratic, which at this size cost ten minutes under the race detector and
	// tripped the default test timeout in CI.
	want := append([]float64(nil), ps...)
	sort.Float64s(want)
	want = want[:topK]

	for i := range want {
		if math.Float64bits(got[i].p) != math.Float64bits(want[i]) {
			t.Fatalf("rank %d: retained p=%v, want %v", i, got[i].p, want[i])
		}
	}
}

func TestSortedAlertsAreAscending(t *testing.T) {
	a := &arm{perDay: map[int64]*dayStats{}}
	seed := uint64(7)
	for i := range topK * 3 {
		p := float64(splitmix64(&seed)%1_000_000_001)/1e9 + 1e-12
		a.push(9, alertRec{p: p, t: int64(i)})
	}
	got := a.perDay[9].sortedAlerts()
	for i := 1; i < len(got); i++ {
		if alertLess(got[i], got[i-1]) {
			t.Fatalf("rank %d is out of order: %v before %v", i, got[i-1].p, got[i].p)
		}
	}
}

func TestPushBelowCapacityKeepsEverything(t *testing.T) {
	// The BH scan must see every alert on a quiet day, not a truncated set.
	a := &arm{perDay: map[int64]*dayStats{}}
	for i := range 10 {
		a.push(11, alertRec{p: float64(10-i) / 100, t: int64(i)})
	}
	got := a.perDay[11].sortedAlerts()
	if len(got) != 10 {
		t.Fatalf("retained %d of 10", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i].p < got[i-1].p {
			t.Fatalf("not ascending at %d", i)
		}
	}
}

func TestRetentionIsIndependentOfArrivalOrder(t *testing.T) {
	// Two arms fed the same alerts in opposite orders must retain the same set, or the
	// experiment's result would depend on how the corpus happened to be ordered.
	const n = topK + 500
	ps := make([]float64, n)
	seed := uint64(99)
	for i := range n {
		ps[i] = float64(splitmix64(&seed)%1_000_000_001)/1e9 + 1e-12
	}

	forward := &arm{perDay: map[int64]*dayStats{}}
	for i, p := range ps {
		forward.push(1, alertRec{p: p, t: int64(i)})
	}
	backward := &arm{perDay: map[int64]*dayStats{}}
	for i := len(ps) - 1; i >= 0; i-- {
		backward.push(1, alertRec{p: ps[i], t: int64(i)})
	}

	f, b := forward.perDay[1].sortedAlerts(), backward.perDay[1].sortedAlerts()
	if len(f) != len(b) {
		t.Fatalf("retained %d forward and %d backward", len(f), len(b))
	}
	for i := range f {
		if math.Float64bits(f[i].p) != math.Float64bits(b[i].p) || f[i].t != b[i].t {
			t.Fatalf("rank %d differs: forward (p=%v,t=%d) backward (p=%v,t=%d)",
				i, f[i].p, f[i].t, b[i].p, b[i].t)
		}
	}
}

func TestRedTeamFlagSurvivesRetention(t *testing.T) {
	// A retained alert must carry its label, or every true-positive count is zero for
	// a reason that has nothing to do with detection.
	a := &arm{perDay: map[int64]*dayStats{}}
	for i := range topK + 100 {
		a.push(3, alertRec{p: float64(i+1) / 1e6, t: int64(i), isRed: i == 0})
	}
	got := a.perDay[3].sortedAlerts()
	if !got[0].isRed {
		t.Error("the smallest-p alert lost its red-team label in retention")
	}
	reds := 0
	for _, r := range got {
		if r.isRed {
			reds++
		}
	}
	if reds != 1 {
		t.Errorf("retained %d labelled alerts, want exactly 1", reds)
	}
}
