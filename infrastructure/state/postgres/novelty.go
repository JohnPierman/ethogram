package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/novelty"
)

// NoveltyStore implements novelty.ValueCountRepository under the §6.2 lazy rule: a
// row stores (count, last_seen_us) and the discount 2^(−Δt/T½) is applied on read,
// from the row's own timestamp, so no sweep job exists. The decay arithmetic runs in
// Go on the fetched row, never in SQL: SQL float evaluation is not guaranteed
// bit-identical to Go's, and R4 demands that this store and the memory store score
// identically.
type NoveltyStore struct {
	pool     *pgxpool.Pool
	halfLife novelty.HalfLife
}

// FindAllByEntityField implements novelty.ValueCountRepository: every stored row for
// (source, entity, field), decayed to at, in ascending Value order. A missing
// (entity, field) returns an empty slice: cold start is N = 0, K = 0, not an error
// (§6.2).
//
// The ORDER BY is the repository contract's total order (README trap 5: Postgres row
// order is undefined without one), and it carries COLLATE "C" so the order is the
// byte-wise one Go's slices.Sort applies in the memory store, independent of the
// database's default collation.
func (s *NoveltyStore) FindAllByEntityField(ctx context.Context, src event.SourceID, en event.EntityID, f event.FieldPath, at event.Timestamp) ([]novelty.ValueRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT value, count, first_seen_us, last_seen_us
		  FROM novelty_value_count
		 WHERE source = $1 AND entity = $2 AND field = $3
		 ORDER BY value COLLATE "C" ASC`,
		string(src), string(en), string(f))
	if err != nil {
		return nil, fmt.Errorf("postgres: novelty select: %w", err)
	}
	defer rows.Close()

	var out []novelty.ValueRow
	for rows.Next() {
		var (
			value               string
			count               float64
			firstSeen, lastSeen int64
		)
		if err := rows.Scan(&value, &count, &firstSeen, &lastSeen); err != nil {
			return nil, fmt.Errorf("postgres: novelty scan: %w", err)
		}
		out = append(out, novelty.ValueRow{
			Value:     value,
			Count:     novelty.Decay(count, event.Timestamp(lastSeen), at, s.halfLife),
			FirstSeen: event.Timestamp(firstSeen),
			LastSeen:  event.Timestamp(lastSeen),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: novelty rows: %w", err)
	}
	return out, nil
}

// SaveObservation implements novelty.ValueCountRepository with the §6.2 fold:
// count ← count·2^(−Δt/T½) + 1, last_seen ← at, creating the row with
// first_seen = at when absent.
//
// A single INSERT ... ON CONFLICT DO UPDATE cannot express this, because the folded
// count must come from novelty.Accumulate in Go for bit-identity with the memory
// store (R4). The row is therefore SELECTed FOR UPDATE inside a transaction, folded
// in Go, and written back — correctness over cleverness. last_seen only advances, so
// an out-of-order arrival is absorbed rather than rejuvenating the row (§6.2).
func (s *NoveltyStore) SaveObservation(ctx context.Context, src event.SourceID, en event.EntityID, f event.FieldPath, value string, at event.Timestamp) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: novelty begin: %w", err)
	}
	// Rollback after a successful commit returns pgx.ErrTxClosed by design; that is
	// the only discarded outcome, and any real failure surfaces on Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		count    float64
		lastSeen int64
	)
	err = tx.QueryRow(ctx, `
		SELECT count, last_seen_us
		  FROM novelty_value_count
		 WHERE source = $1 AND entity = $2 AND field = $3 AND value = $4
		 FOR UPDATE`,
		string(src), string(en), string(f), value).Scan(&count, &lastSeen)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if _, execErr := tx.Exec(ctx, `
			INSERT INTO novelty_value_count
			       (source, entity, field, value, count, first_seen_us, last_seen_us)
			VALUES ($1, $2, $3, $4, 1, $5, $5)`,
			string(src), string(en), string(f), value, int64(at)); execErr != nil {
			return fmt.Errorf("postgres: novelty insert: %w", execErr)
		}
	case err != nil:
		return fmt.Errorf("postgres: novelty select for update: %w", err)
	default:
		folded := novelty.Accumulate(count, event.Timestamp(lastSeen), at, s.halfLife)
		if int64(at) > lastSeen {
			lastSeen = int64(at)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE novelty_value_count
			   SET count = $5, last_seen_us = $6
			 WHERE source = $1 AND entity = $2 AND field = $3 AND value = $4`,
			string(src), string(en), string(f), value, folded, lastSeen); err != nil {
			return fmt.Errorf("postgres: novelty update: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: novelty commit: %w", err)
	}
	return nil
}
