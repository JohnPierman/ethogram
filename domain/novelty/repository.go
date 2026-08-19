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
