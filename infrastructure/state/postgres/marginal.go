package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/marginal"
	"github.com/JohnPierman/ethogram/domain/novelty"
)

// MarginalStore implements marginal.Repository.
//
// It is the population-scope twin of [NoveltyStore]: the same decayed value counts of §6.2,
// keyed by (source, field, value) with no entity column, because §9's null is the
// population's marginal rather than one account's history. The numeric half keeps one
// bounded sketch per (source, field).
//
// # Where the decay is applied
//
// Read time, and the reason is the same one [NoveltyStore] has. A row's count is stored as of
// its own last_seen, so bringing the whole distribution to a common instant is the reader's
// job: equation (5) sums over every value at one moment, and a distribution whose members
// were last discounted at different times is not a distribution.
//
// # Row order is part of the contract
//
// FindCategorical orders by value ascending, which the interface requires for a stated
// reason: the sum of equation (5) is a float accumulation, and Postgres row order is
// undefined without ORDER BY, so an unordered result would make the score depend on storage
// internals. The same ORDER BY is what makes the store equivalent to the memory
// implementation rather than merely similar.
type MarginalStore struct {
	pool     *pgxpool.Pool
	halfLife novelty.HalfLife
}

// FindCategorical implements marginal.Repository, discounting each row to `at`.
func (s *MarginalStore) FindCategorical(ctx context.Context, src event.SourceID,
	field event.FieldPath, at event.Timestamp) ([]marginal.ValueCount, error) {

	rows, err := s.pool.Query(ctx, `
		SELECT value, count, last_seen_us
		  FROM marginal_value_count
		 WHERE source = $1 AND field = $2
		 ORDER BY value ASC`,
		string(src), string(field))
	if err != nil {
		return nil, fmt.Errorf("postgres: marginal select: %w", err)
	}
	defer rows.Close()

	out := []marginal.ValueCount{}
	for rows.Next() {
		var (
			value    string
			count    float64
			lastSeen int64
		)
		if err := rows.Scan(&value, &count, &lastSeen); err != nil {
			return nil, fmt.Errorf("postgres: marginal scan: %w", err)
		}
		out = append(out, marginal.ValueCount{
			Value: value,
			Count: novelty.Decay(count, event.Timestamp(lastSeen), at, s.halfLife),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: marginal rows: %w", err)
	}
	return out, nil
}

// Cardinality implements marginal.Repository without materialising the values, which is the
// whole reason the interface carries it: equation (5)'s tail is linear in the distinct value
// count, and at population scope that count runs to tens of thousands for a field such as a
// destination host. Asking the size first turns a question the detector would answer with an
// abstention into one it declines in constant time.
func (s *MarginalStore) Cardinality(ctx context.Context, src event.SourceID,
	field event.FieldPath) (int, error) {

	var count int64
	err := s.pool.QueryRow(ctx, `
		SELECT count(*)
		  FROM marginal_value_count
		 WHERE source = $1 AND field = $2`,
		string(src), string(field)).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("postgres: marginal cardinality: %w", err)
	}
	return int(count), nil
}

// FindNumeric implements marginal.Repository, rebuilding the sketch from its persisted
// centroids.
//
// The sketch is reconstructed rather than replayed: the stored centroids are what the
// compression rule already produced, and re-adding their means would compress a second time
// and give a different sketch. `at` is accepted because an implementation ageing its sketch
// would need it; this one does not decay the sketch, matching the memory store.
func (s *MarginalStore) FindNumeric(ctx context.Context, src event.SourceID,
	field event.FieldPath, _ event.Timestamp) (*marginal.Sketch, bool, error) {

	var (
		maxCentroids int32
		means        []float64
		weights      []float64
	)
	err := s.pool.QueryRow(ctx, `
		SELECT max_centroids, means, weights
		  FROM marginal_sketch
		 WHERE source = $1 AND field = $2`,
		string(src), string(field)).Scan(&maxCentroids, &means, &weights)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil // no numeric observation yet, not an error
	}
	if err != nil {
		return nil, false, fmt.Errorf("postgres: marginal sketch select: %w", err)
	}

	sketch, err := marginal.RestoreSketch(marginal.SketchState{
		MaxCentroids: int(maxCentroids), Means: means, Weights: weights,
	})
	if err != nil {
		return nil, false, fmt.Errorf("postgres: marginal sketch restore: %w", err)
	}
	return sketch, true, nil
}

// SaveCategorical implements marginal.Repository: select for update, fold with the domain's
// own accumulator, write back, in one transaction.
//
// The fold is not expressed in SQL, and the first version of this file made that mistake. A
// SQL `power(2, -(at - last_seen)/T)` inflates the count whenever an event arrives at or
// before a row's last_seen, because the exponent turns positive — and it would move last_seen
// backwards. Out-of-order and equal timestamps are not hypothetical here: [novelty.DecayFactor]
// absorbs them by returning 1 for a non-positive interval, and LANL's one-second resolution
// guarantees ties. Doing the arithmetic in the domain is what makes this store equivalent to
// the memory one rather than merely similar, which is the whole claim #48 is about.
func (s *MarginalStore) SaveCategorical(ctx context.Context, src event.SourceID,
	field event.FieldPath, value string, at event.Timestamp) error {

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: marginal begin: %w", err)
	}
	// Rollback after a successful commit returns pgx.ErrTxClosed by design; that is the
	// only discarded outcome, and any real failure surfaces on Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		count    float64
		lastSeen int64
	)
	err = tx.QueryRow(ctx, `
		SELECT count, last_seen_us
		  FROM marginal_value_count
		 WHERE source = $1 AND field = $2 AND value = $3
		 FOR UPDATE`,
		string(src), string(field), value).Scan(&count, &lastSeen)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if _, execErr := tx.Exec(ctx, `
			INSERT INTO marginal_value_count (source, field, value, count, last_seen_us)
			VALUES ($1, $2, $3, 1, $4)`,
			string(src), string(field), value, int64(at)); execErr != nil {
			return fmt.Errorf("postgres: marginal insert: %w", execErr)
		}
	case err != nil:
		return fmt.Errorf("postgres: marginal select for update: %w", err)
	default:
		folded := novelty.Accumulate(count, event.Timestamp(lastSeen), at, s.halfLife)
		if int64(at) > lastSeen {
			lastSeen = int64(at)
		}
		if _, execErr := tx.Exec(ctx, `
			UPDATE marginal_value_count
			   SET count = $4, last_seen_us = $5
			 WHERE source = $1 AND field = $2 AND value = $3`,
			string(src), string(field), value, folded, lastSeen); execErr != nil {
			return fmt.Errorf("postgres: marginal update: %w", execErr)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: marginal commit: %w", err)
	}
	return nil
}

// SaveNumeric implements marginal.Repository as read, fold, write.
//
// Unlike the categorical fold this cannot be expressed in SQL: the compression rule merges
// the adjacent centroid pair of least combined weight, which is a loop over the centroids
// rather than an arithmetic update. So the sketch is loaded, advanced by the domain's own
// Add, and written back — the domain keeps the invariant and the store only moves bytes.
func (s *MarginalStore) SaveNumeric(ctx context.Context, src event.SourceID,
	field event.FieldPath, x float64, at event.Timestamp) error {

	if math.IsNaN(x) || math.IsInf(x, 0) {
		// The estimator abstains on such input before it reaches here; refusing rather
		// than storing keeps the ascending order the sketch depends on.
		return fmt.Errorf("postgres: marginal sketch: %g is not a finite observation", x)
	}

	sketch, found, err := s.FindNumeric(ctx, src, field, at)
	if err != nil {
		return err
	}
	if !found {
		sketch = marginal.NewSketch(0)
	}
	sketch.Add(x, 1)
	state := sketch.State()

	// last_seen_us must not move backwards on an out-of-order arrival, for the same reason
	// the categorical fold guards it: the timestamp is what a later read discounts from.
	stamp := int64(at)
	if found {
		var previous int64
		readErr := s.pool.QueryRow(ctx, `
			SELECT last_seen_us FROM marginal_sketch WHERE source = $1 AND field = $2`,
			string(src), string(field)).Scan(&previous)
		if readErr == nil && previous > stamp {
			stamp = previous
		}
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO marginal_sketch (source, field, max_centroids, means, weights,
		                             last_seen_us)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (source, field) DO UPDATE
		   SET max_centroids = EXCLUDED.max_centroids,
		       means         = EXCLUDED.means,
		       weights       = EXCLUDED.weights,
		       last_seen_us  = EXCLUDED.last_seen_us`,
		string(src), string(field), int32(state.MaxCentroids), state.Means, state.Weights, //nolint:gosec // a centroid bound, bounded by DefaultMaxCentroids
		stamp)
	if err != nil {
		return fmt.Errorf("postgres: marginal sketch upsert: %w", err)
	}
	return nil
}
