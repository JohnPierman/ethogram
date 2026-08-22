package novelty

import (
	"math"
	"slices"

	"github.com/JohnPierman/ethogram/domain/event"
)

// Bounded per-entity value counts (§13.3, issue #3).
//
// # What is unbounded, and why the registry's bound does not fix it
//
// The registry bounds the value set it holds *per field* to decide that field's kind, and
// records when the bound binds. The per-entity counts equation (4) sums over are a different
// structure and are not bounded at all: an address or hostname field blows through any
// sensible ceiling on the first day, and the state grows with the vocabulary for as long as
// the vocabulary keeps opening. The numeric side of the population marginal already has a
// bounded sketch; this is the categorical counterpart.
//
// # The sketch, and the error it states
//
// Space-saving [Metwally, Agrawal and El Abbadi, 2005]. Keep at most Capacity entries. On a
// value already held, fold the observation into it. On a new value with room, admit it. On a
// new value with no room, take the entry of least weight, hand its weight to the new value,
// and evict it.
//
// That last move is what makes the error stateable rather than merely small. Writing m for the
// least weight held at the moment of an eviction:
//
//   - a held count over-states the true count by at most the m it inherited, and never
//     under-states it;
//   - an evicted value's true weight is at most the m it was evicted at;
//   - the total weight is exact, because eviction moves weight rather than discarding it.
//
// [Bounded.Overstatement] carries the running bound, so a caller can say how wrong a count may
// be instead of hoping it is close. This is the same discipline the truncation caveat already
// follows: §13.3 requires the condition be reported, not concealed.
//
// # What it costs equation (4), and what it does not
//
// The reserved unseen mass needs the total and the distinct count. The total is exact. The
// distinct count is not: values evicted and later re-admitted are counted twice over the run,
// so [Bounded.DistinctSeen] is an upper bound on the true vocabulary while [Bounded.Held] is a
// lower one, and the pair brackets it. Good–Turing needs the singleton weight, which is
// recoverable from the held entries and is the quantity least damaged by eviction: a singleton
// is exactly the kind of entry that gets evicted, so the count of *evictions* is itself
// evidence that the vocabulary is still opening. [Bounded.EvictedSingletons] records it.
//
// # Determinism (R4)
//
// The eviction victim is the least-weight entry, ties broken by value ascending — a total order,
// so two sketches fed the same observations in the same order hold the same entries. No clock
// and no randomness.

// Bounded is a fixed-size value-count sketch for one (entity, field).
type Bounded struct {
	capacity int
	rows     map[string]*BoundedRow
	sorted   []string

	total float64
	// overstatement is the largest weight ever handed to an admitted value, which bounds how
	// far any held count can exceed the truth.
	overstatement float64
	distinctSeen  int64
	evictions     int64
	// evictedSingletons counts evictions of entries whose inherited weight was zero, so the
	// entry was carrying its own single observation and nothing else.
	evictedSingletons int64
}

// BoundedRow is one held value.
type BoundedRow struct {
	Value     string
	Count     float64
	FirstSeen event.Timestamp
	LastSeen  event.Timestamp
	// Inherited is the weight this entry was admitted with, which is how much of Count may
	// belong to values that are no longer held.
	Inherited float64
}

// NewBounded returns an empty sketch holding at most capacity values. A capacity below one is
// meaningless and is raised to one: a sketch that can hold nothing cannot report a total.
func NewBounded(capacity int) *Bounded {
	if capacity < 1 {
		capacity = 1
	}
	return &Bounded{capacity: capacity, rows: make(map[string]*BoundedRow, capacity)}
}

// Capacity is the bound.
func (b *Bounded) Capacity() int { return b.capacity }

// Held is how many values are currently held, a lower bound on the true vocabulary size.
func (b *Bounded) Held() int { return len(b.sorted) }

// DistinctSeen is how many admissions have happened, an upper bound on the true vocabulary
// size: a value evicted and re-admitted is counted twice.
func (b *Bounded) DistinctSeen() int64 { return b.distinctSeen }

// Evictions and EvictedSingletons are how often the bound bound, and how often what it
// displaced was carrying a single observation of its own.
func (b *Bounded) Evictions() int64         { return b.evictions }
func (b *Bounded) EvictedSingletons() int64 { return b.evictedSingletons }

// Total is the exact total weight observed, undecayed by any later read. Eviction moves weight
// rather than discarding it, so this is not an estimate.
func (b *Bounded) Total() float64 { return b.total }

// Overstatement is the largest weight any single held count may exceed the truth by.
//
// It is the largest weight ever inherited at an admission, which is the standard space-saving
// bound: nothing held has been credited with more than that from values it did not observe.
func (b *Bounded) Overstatement() float64 { return b.overstatement }

// Saturated reports whether the bound has ever bound. Below it the sketch is exact and says so,
// which matters because most fields never reach it and their counts should not carry a caveat
// they have not earned.
func (b *Bounded) Saturated() bool { return b.evictions > 0 }

// Observe folds one observation of value at time `at`, decaying the entry it lands on by the
// §6.2 rule. It returns the value evicted, if any, so a caller can record what it lost.
func (b *Bounded) Observe(value string, at event.Timestamp, halfLife HalfLife) (evicted string) {
	b.total++

	if row, held := b.rows[value]; held {
		row.Count = Accumulate(row.Count, row.LastSeen, at, halfLife)
		if at > row.LastSeen {
			row.LastSeen = at
		}
		return ""
	}

	if len(b.sorted) < b.capacity {
		b.admit(value, at, 0)
		return ""
	}

	// The bound binds. The least-weight entry hands its weight to the new value and goes.
	victim := b.least(at, halfLife)
	inherited := Decay(b.rows[victim].Count, b.rows[victim].LastSeen,
		at, halfLife)
	if b.rows[victim].Inherited <= 0 {
		b.evictedSingletons++
	}
	b.remove(victim)
	b.evictions++
	if inherited > b.overstatement {
		b.overstatement = inherited
	}
	b.admit(value, at, inherited)
	return victim
}

// least is the eviction victim: the entry of least decayed weight, ties broken by value
// ascending so the choice is a total order (R4).
func (b *Bounded) least(at event.Timestamp, halfLife HalfLife) string {
	best, bestWeight := "", math.Inf(1)
	for _, v := range b.sorted {
		row := b.rows[v]
		w := Decay(row.Count, row.LastSeen, at, halfLife)
		if w < bestWeight {
			best, bestWeight = v, w
		}
	}
	return best
}

func (b *Bounded) admit(value string, at event.Timestamp, inherited float64) {
	b.rows[value] = &BoundedRow{
		Value: value, Count: inherited + 1,
		FirstSeen: at, LastSeen: at, Inherited: inherited,
	}
	idx, _ := slices.BinarySearch(b.sorted, value)
	b.sorted = append(b.sorted, "")
	copy(b.sorted[idx+1:], b.sorted[idx:])
	b.sorted[idx] = value
	b.distinctSeen++
}

func (b *Bounded) remove(value string) {
	delete(b.rows, value)
	if idx, found := slices.BinarySearch(b.sorted, value); found {
		b.sorted = slices.Delete(b.sorted, idx, idx+1)
	}
}

// Rows returns the held values in ascending value order, decayed to `at`.
//
// Ascending because that is the repository contract equation (5)'s float accumulation depends
// on, and the same order the unbounded store and the Postgres store both promise.
func (b *Bounded) Rows(at event.Timestamp, halfLife HalfLife) []BoundedRow {
	out := make([]BoundedRow, 0, len(b.sorted))
	for _, v := range b.sorted {
		row := b.rows[v]
		out = append(out, BoundedRow{
			Value:     v,
			Count:     Decay(row.Count, row.LastSeen, at, halfLife),
			FirstSeen: row.FirstSeen,
			LastSeen:  row.LastSeen,
			Inherited: row.Inherited,
		})
	}
	return out
}

// SingletonWeight is the weight held by entries carrying a single observation of their own,
// which is the numerator Good–Turing's reserved mass needs.
//
// Entries admitted with inherited weight are excluded however small their count: an entry
// holding weight handed to it by an evicted value is not a singleton, and counting it as one
// would raise the unseen mass on the strength of an eviction rather than an observation.
func (b *Bounded) SingletonWeight(at event.Timestamp, halfLife HalfLife) float64 {
	var singletons float64
	for _, v := range b.sorted {
		row := b.rows[v]
		if row.Inherited > 0 {
			continue
		}
		w := Decay(row.Count, row.LastSeen, at, halfLife)
		if w >= 1-singletonTolerance && w <= 1+singletonTolerance {
			singletons++
		}
	}
	return singletons
}
