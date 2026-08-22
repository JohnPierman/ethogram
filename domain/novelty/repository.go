package novelty

import (
	"context"

	"github.com/JohnPierman/ethogram/domain/event"
)

// ValueRow is one stored value's state for an (entity, field), brought up to date at a
// requested instant.
//
// Count is the decayed count n_v at that instant, per the lazy rule of §6.2: the row
// stores (count, last_seen) and the discount 2^(−Δt/T½) is applied on read from the
// row's own timestamp, so no sweep job exists. FirstSeen and LastSeen are part of the
// §6.4 evidence.
type ValueRow struct {
	Value     string
	Count     float64
	FirstSeen event.Timestamp
	LastSeen  event.Timestamp
}

// ValueCountRepository persists per-(entity, field) decayed value counts.
//
// Implementations must return rows in ascending Value order: row order feeds the float
// accumulation of equation (5), and an unordered result would make the sum depend on
// storage internals (trap 5: Postgres row order is undefined without ORDER BY). The
// estimator sorts defensively regardless, but the contract keeps the property visible
// at the boundary where it can be enforced by a query.
type ValueCountRepository interface {
	// FindAllByEntityField returns every stored value row for (source, entity, field),
	// decayed to at, in ascending Value order. A missing (entity, field) returns an
	// empty slice: cold start is N = 0, K = 0, not an error (§6.2).
	FindAllByEntityField(ctx context.Context, source event.SourceID, entity event.EntityID, field event.FieldPath, at event.Timestamp) ([]ValueRow, error)

	// SaveObservation folds one observation into the row for (source, entity, field,
	// value) at the event time: count ← count·2^(−Δt/T½) + 1, last_seen ← at, per
	// §6.2. Creates the row if absent with first_seen = at.
	SaveObservation(ctx context.Context, source event.SourceID, entity event.EntityID, field event.FieldPath, value string, at event.Timestamp) error
}

// TailReporter is an optional capability a bounded value-count store may offer: the evidence
// about values it is no longer holding (§13.3, issue #3).
//
// # Why it exists
//
// Good–Turing reads the singleton rate, and the singleton rate lives in the tail. A
// heavy-hitters sketch evicts the tail one value at a time, so a store that only reports what
// it currently holds hands the estimator a distribution with the tail cut off — and the
// estimator then reads a *closed* vocabulary where the truth is an open one, which is the exact
// confound the Good–Turing reserve was introduced to remove.
//
// Measured before this interface existed: on the open-vocabulary corpus, bounding the per-entity
// counts took novelty's detections from 864 of 864 to **0**, at both a 64-value and a 256-value
// ceiling. The sketch was doing its job — 23x less state, every heavy hitter kept, an error bound
// of 16.6 — and the composition was still wrong, because the shape information the estimator
// needs had been thrown away one eviction at a time.
//
// So an evicting store reports its count-of-counts. This is the "plus Good–Turing's
// count-of-counts for the tail" half of the issue's own design, and leaving it out is what made
// the first measurement fail.
//
// # Optional by design
//
// The unbounded and Postgres stores hold everything, so their tail is not missing and they do
// not implement this. A caller type-asserts and proceeds without it when absent, which keeps the
// capability out of the interface every store has to satisfy.
type TailReporter interface {
	// TailCountOfCounts returns the singleton weight belonging to values this store has
	// evicted for (source, entity, field), and whether the bound has ever bound for it.
	//
	// Singleton weight rather than a count of evictions: what Good–Turing needs is N₁, the
	// weight sitting on values seen exactly once, and an eviction whose victim carried more
	// than one observation is not evidence that the vocabulary is opening.
	TailCountOfCounts(ctx context.Context, source event.SourceID, entity event.EntityID,
		field event.FieldPath) (singletons float64, saturated bool, err error)
}
