package novelty_test

import (
	"fmt"
	"math"
	"math/rand/v2"
	"testing"

	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/novelty"
)

// noDecay is a half-life long enough that nothing decays over a test's timespan, so the
// sketch's own arithmetic is what a test measures rather than the decay's.
const noDecay = novelty.HalfLife(1 << 62)

// TestBoundedIsExactBelowItsBound is the property that keeps the caveat honest. Most fields
// never reach the ceiling, and their counts must not carry a warning they have not earned.
func TestBoundedIsExactBelowItsBound(t *testing.T) {
	b := novelty.NewBounded(16)
	truth := map[string]float64{}
	for i := 0; i < 200; i++ {
		v := fmt.Sprintf("v%d", i%12)
		b.Observe(v, event.Timestamp(i), noDecay)
		truth[v]++
	}

	if b.Saturated() {
		t.Fatalf("12 values in a sketch of 16 saturated it: %d evictions", b.Evictions())
	}
	if b.Overstatement() != 0 {
		t.Errorf("an unsaturated sketch claims an overstatement of %g, want 0",
			b.Overstatement())
	}
	if b.Held() != 12 || b.DistinctSeen() != 12 {
		t.Errorf("held %d and seen %d of 12 distinct values", b.Held(), b.DistinctSeen())
	}
	if b.Total() != 200 {
		t.Errorf("total is %g, want 200", b.Total())
	}
	for _, row := range b.Rows(event.Timestamp(200), noDecay) {
		if row.Count != truth[row.Value] {
			t.Errorf("%s counted %g, truly %g", row.Value, row.Count, truth[row.Value])
		}
	}
}

// TestBoundedStatesAnErrorItDoesNotExceed is the whole point of choosing space-saving over
// simply dropping values: the error is a number the sketch can report, not a hope.
//
// The three claims checked here are the ones the doc comment makes. A held count never
// under-states the truth and over-states it by at most the reported bound; the total is exact;
// and the true vocabulary size is bracketed by Held and DistinctSeen.
func TestBoundedStatesAnErrorItDoesNotExceed(t *testing.T) {
	rng := rand.New(rand.NewPCG(3, 3))
	const (
		capacity  = 64
		vocabSize = 4_000
		draws     = 60_000
	)

	b := novelty.NewBounded(capacity)
	truth := map[string]float64{}
	for i := 0; i < draws; i++ {
		// A heavy head over a long tail, which is the shape a real address field has: a few
		// values carry most of the traffic and the rest are near-singletons.
		var v string
		if rng.Float64() < 0.5 {
			v = fmt.Sprintf("head%d", rng.IntN(20))
		} else {
			v = fmt.Sprintf("tail%d", rng.IntN(vocabSize))
		}
		b.Observe(v, event.Timestamp(i), noDecay)
		truth[v]++
	}

	t.Logf("capacity %d, %d draws over %d distinct values: held %d, seen %d, "+
		"evictions %d (of which singletons %d), overstatement bound %g",
		capacity, draws, len(truth), b.Held(), b.DistinctSeen(),
		b.Evictions(), b.EvictedSingletons(), b.Overstatement())

	if !b.Saturated() {
		t.Fatal("a 64-entry sketch over 4,000 values did not saturate; this test measures nothing")
	}
	if b.Held() > capacity {
		t.Errorf("holding %d entries, above the capacity of %d", b.Held(), capacity)
	}
	if b.Total() != draws {
		t.Errorf("total is %g, want the exact %d: eviction must move weight, not discard it",
			b.Total(), draws)
	}

	bound := b.Overstatement()
	for _, row := range b.Rows(event.Timestamp(draws), noDecay) {
		true_ := truth[row.Value]
		if row.Count < true_-1e-9 {
			t.Errorf("%s counted %g against a true %g: a held count must never under-state",
				row.Value, row.Count, true_)
		}
		if row.Count > true_+bound+1e-9 {
			t.Errorf("%s counted %g against a true %g, over-stating by %g against a stated "+
				"bound of %g", row.Value, row.Count, true_, row.Count-true_, bound)
		}
	}

	// The true vocabulary must sit inside the bracket the sketch reports.
	if int64(b.Held()) > int64(len(truth)) || b.DistinctSeen() < int64(len(truth)) {
		t.Errorf("the bracket [%d, %d] does not contain the true vocabulary of %d",
			b.Held(), b.DistinctSeen(), len(truth))
	}

	// The heavy head is what a bounded sketch exists to keep. Every value carrying real
	// traffic should still be held.
	for i := 0; i < 20; i++ {
		v := fmt.Sprintf("head%d", i)
		found := false
		for _, row := range b.Rows(event.Timestamp(draws), noDecay) {
			if row.Value == v {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s carries %g observations and was evicted; a heavy hitter is the one "+
				"thing this must not lose", v, truth[v])
		}
	}
}

// TestBoundedIsDeterministic covers R4 at this layer, including the tie case: with equal
// weights the victim must be chosen by a total order, or two runs over the same stream hold
// different values.
func TestBoundedIsDeterministic(t *testing.T) {
	stream := make([]string, 5_000)
	rng := rand.New(rand.NewPCG(9, 9))
	for i := range stream {
		// Mostly singletons, so ties for least weight are the common case rather than a
		// corner.
		stream[i] = fmt.Sprintf("v%d", rng.IntN(3_000))
	}

	first := novelty.NewBounded(32)
	for i, v := range stream {
		first.Observe(v, event.Timestamp(i), noDecay)
	}
	want := first.Rows(event.Timestamp(len(stream)), noDecay)

	for trial := 0; trial < 5; trial++ {
		again := novelty.NewBounded(32)
		for i, v := range stream {
			again.Observe(v, event.Timestamp(i), noDecay)
		}
		got := again.Rows(event.Timestamp(len(stream)), noDecay)
		if len(got) != len(want) {
			t.Fatalf("trial %d holds %d entries, want %d", trial, len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("trial %d entry %d is %+v, want %+v", trial, i, got[i], want[i])
			}
		}
		if again.Overstatement() != first.Overstatement() ||
			again.DistinctSeen() != first.DistinctSeen() {
			t.Fatalf("trial %d reports a different error bound or vocabulary bracket", trial)
		}
	}
}

// TestBoundedRowsAreAscending pins the repository contract equation (5)'s float accumulation
// depends on: the unbounded store and the Postgres store both promise ascending value order,
// and a bounded one that did not would make the score depend on which store was used.
func TestBoundedRowsAreAscending(t *testing.T) {
	b := novelty.NewBounded(8)
	for i, v := range []string{"zeta", "alpha", "mu", "beta", "omega", "gamma"} {
		b.Observe(v, event.Timestamp(i), noDecay)
	}
	rows := b.Rows(event.Timestamp(10), noDecay)
	for i := 1; i < len(rows); i++ {
		if rows[i-1].Value >= rows[i].Value {
			t.Fatalf("rows are not ascending at %d: %q then %q",
				i, rows[i-1].Value, rows[i].Value)
		}
	}
}

// TestBoundedSingletonWeightExcludesInheritedEntries covers the interaction with Good–Turing,
// which is where a bounded sketch could quietly corrupt an estimate rather than merely losing
// resolution.
//
// An entry admitted with weight handed to it by an evicted value is not a singleton however
// small its count, and counting it as one would raise the reserved unseen mass on the strength
// of an eviction rather than an observation — inventing evidence that the vocabulary is opening.
func TestBoundedSingletonWeightExcludesInheritedEntries(t *testing.T) {
	b := novelty.NewBounded(2)
	// Two values, then a third that must displace one and inherit its weight.
	b.Observe("a", 1, noDecay)
	b.Observe("a", 2, noDecay)
	b.Observe("a", 3, noDecay) // "a" is heavy
	b.Observe("b", 4, noDecay) // "b" is a singleton
	if got := b.SingletonWeight(5, noDecay); got != 1 {
		t.Errorf("singleton weight is %g with one singleton held, want 1", got)
	}

	b.Observe("c", 6, noDecay) // evicts "b", inheriting its weight
	if !b.Saturated() {
		t.Fatal("the bound did not bind")
	}
	if got := b.SingletonWeight(7, noDecay); got != 0 {
		t.Errorf("singleton weight is %g after the only singleton was displaced by an "+
			"inheriting entry, want 0", got)
	}
}

// TestBoundedDegenerateCapacities covers the boundaries a caller can reach.
func TestBoundedDegenerateCapacities(t *testing.T) {
	for _, capacity := range []int{-5, 0, 1} {
		b := novelty.NewBounded(capacity)
		if b.Capacity() < 1 {
			t.Errorf("capacity %d became %d; a sketch holding nothing cannot report a total",
				capacity, b.Capacity())
		}
		for i := 0; i < 20; i++ {
			b.Observe(fmt.Sprintf("v%d", i), event.Timestamp(i), noDecay)
		}
		if b.Held() > b.Capacity() {
			t.Errorf("capacity %d holds %d entries", b.Capacity(), b.Held())
		}
		if b.Total() != 20 {
			t.Errorf("capacity %d reports total %g, want the exact 20",
				b.Capacity(), b.Total())
		}
	}
}

// TestBoundedDecaysWhatItHolds checks the §6.2 rule reaches the sketch, since a bounded store
// that stopped decaying would quietly become a different estimator.
func TestBoundedDecaysWhatItHolds(t *testing.T) {
	const day = event.Timestamp(86_400)
	halfLife := novelty.HalfLife(7 * day)

	b := novelty.NewBounded(4)
	b.Observe("a", 0, halfLife)
	b.Observe("a", 0, halfLife)

	rows := b.Rows(7*day, halfLife)
	if len(rows) != 1 {
		t.Fatalf("holding %d entries, want 1", len(rows))
	}
	// Two observations at t=0, read one half-life later.
	if want := 1.0; math.Abs(rows[0].Count-want) > 1e-9 {
		t.Errorf("count read one half-life on is %g, want %g", rows[0].Count, want)
	}
	// And the total is the undecayed observation count, which is what the error bound and the
	// eviction accounting are stated against.
	if b.Total() != 2 {
		t.Errorf("total is %g, want the undecayed 2", b.Total())
	}
}
