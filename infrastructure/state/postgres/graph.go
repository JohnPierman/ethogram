package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/JohnPierman/ethogram/domain/cooccurrence"
	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/novelty"
)

// GraphStore implements cooccurrence.GraphRepository with the same lazy rule as
// cooccurrence.MemoryGraph (§6.2, §8.2): every row stores (weight, last_seen_us) and
// the discount 2^(−Δt/T½) is applied on read, in Go, via the novelty decay helpers,
// so the two implementations are bit-identical (R4). Scope is the population per
// source, and the graph holds one row per node, per edge and per source total —
// finite state in the §13.3 sense.
type GraphStore struct {
	pool     *pgxpool.Pool
	halfLife novelty.HalfLife
}

// canonicalEdge returns an edge's endpoints ordered by (Field, Value), the identical
// canonical orientation cooccurrence.MemoryGraph fixes, so that (a, b) and (b, a)
// address one row however a caller happened to enumerate the pair.
func canonicalEdge(a, b cooccurrence.NodeID) (cooccurrence.NodeID, cooccurrence.NodeID) {
	if b.Field != a.Field {
		if b.Field < a.Field {
			return b, a
		}
		return a, b
	}
	if b.Value < a.Value {
		return b, a
	}
	return a, b
}

// FindEdgeWeight implements cooccurrence.GraphRepository: w_ij decayed to at, 0 when
// the row is absent — a zero count, not an error (§6.2). Primary-key lookup, so the
// single-row result is trivially totally ordered.
func (s *GraphStore) FindEdgeWeight(ctx context.Context, src event.SourceID, a, b cooccurrence.NodeID, at event.Timestamp) (float64, error) {
	lo, hi := canonicalEdge(a, b)
	var (
		weight   float64
		lastSeen int64
	)
	err := s.pool.QueryRow(ctx, `
		SELECT weight, last_seen_us
		  FROM graph_edge
		 WHERE source = $1 AND field_a = $2 AND value_a = $3 AND field_b = $4 AND value_b = $5`,
		string(src), string(lo.Field), lo.Value, string(hi.Field), hi.Value).Scan(&weight, &lastSeen)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("postgres: edge select: %w", err)
	}
	return novelty.Decay(weight, event.Timestamp(lastSeen), at, s.halfLife), nil
}

// FindDegree implements cooccurrence.GraphRepository: k_i decayed to at, 0 when
// absent.
func (s *GraphStore) FindDegree(ctx context.Context, src event.SourceID, n cooccurrence.NodeID, at event.Timestamp) (float64, error) {
	var (
		degree   float64
		lastSeen int64
	)
	err := s.pool.QueryRow(ctx, `
		SELECT degree, last_seen_us
		  FROM graph_node
		 WHERE source = $1 AND field = $2 AND value = $3`,
		string(src), string(n.Field), n.Value).Scan(&degree, &lastSeen)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("postgres: node select: %w", err)
	}
	return novelty.Decay(degree, event.Timestamp(lastSeen), at, s.halfLife), nil
}

// FindTotalWeight implements cooccurrence.GraphRepository: m decayed to at, 0 when
// absent.
func (s *GraphStore) FindTotalWeight(ctx context.Context, src event.SourceID, at event.Timestamp) (float64, error) {
	var (
		weight   float64
		lastSeen int64
	)
	err := s.pool.QueryRow(ctx, `
		SELECT weight, last_seen_us
		  FROM graph_total
		 WHERE source = $1`,
		string(src)).Scan(&weight, &lastSeen)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("postgres: total select: %w", err)
	}
	return novelty.Decay(weight, event.Timestamp(lastSeen), at, s.halfLife), nil
}

// SaveCoOccurrence implements cooccurrence.GraphRepository: w_ab += 1 after lazy
// decay, k_a += 1, k_b += 1, m += 1, all at at.
//
// The four folds run strictly in sequence inside one transaction, each as a SELECT
// ... FOR UPDATE followed by an upsert of the Go-computed fold. A single INSERT ...
// ON CONFLICT DO UPDATE per row cannot apply the Go-side decay that R4 requires for
// bit-identity with the memory store, and batching the reads ahead of the writes
// would diverge from MemoryGraph's sequential fold order — correctness over
// cleverness. Decay is linear, so the degree and total rows maintained by this same
// rule stay exactly consistent with the edge rows they aggregate, for the reason set
// out at novelty.Accumulate.
func (s *GraphStore) SaveCoOccurrence(ctx context.Context, src event.SourceID, a, b cooccurrence.NodeID, at event.Timestamp) error {
	lo, hi := canonicalEdge(a, b)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: graph begin: %w", err)
	}
	// Rollback after a successful commit returns pgx.ErrTxClosed by design; that is
	// the only discarded outcome, and any real failure surfaces on Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	if err := s.foldEdge(ctx, tx, src, lo, hi, at); err != nil {
		return err
	}
	if err := s.foldNode(ctx, tx, src, lo, at); err != nil {
		return err
	}
	if err := s.foldNode(ctx, tx, src, hi, at); err != nil {
		return err
	}
	if err := s.foldTotal(ctx, tx, src, at); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: graph commit: %w", err)
	}
	return nil
}

// foldWeight applies the §6.2 fold to a fetched (weight, last_seen_us) pair. An
// absent row arrives as the zero pair and folds to weight 1, exactly as
// MemoryGraph's fresh cell does; last_seen only advances, so out-of-order arrival is
// absorbed rather than rejuvenating the row.
func (s *GraphStore) foldWeight(weight float64, lastSeen int64, at event.Timestamp) (float64, int64) {
	folded := novelty.Accumulate(weight, event.Timestamp(lastSeen), at, s.halfLife)
	if int64(at) > lastSeen {
		lastSeen = int64(at)
	}
	return folded, lastSeen
}

// foldEdge locks, folds and upserts the canonical edge row.
func (s *GraphStore) foldEdge(ctx context.Context, tx pgx.Tx, src event.SourceID, lo, hi cooccurrence.NodeID, at event.Timestamp) error {
	var (
		weight   float64
		lastSeen int64
	)
	err := tx.QueryRow(ctx, `
		SELECT weight, last_seen_us
		  FROM graph_edge
		 WHERE source = $1 AND field_a = $2 AND value_a = $3 AND field_b = $4 AND value_b = $5
		 FOR UPDATE`,
		string(src), string(lo.Field), lo.Value, string(hi.Field), hi.Value).Scan(&weight, &lastSeen)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("postgres: edge select for update: %w", err)
	}
	folded, seen := s.foldWeight(weight, lastSeen, at)
	if _, err := tx.Exec(ctx, `
		INSERT INTO graph_edge (source, field_a, value_a, field_b, value_b, weight, last_seen_us)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (source, field_a, value_a, field_b, value_b) DO UPDATE
		   SET weight = EXCLUDED.weight, last_seen_us = EXCLUDED.last_seen_us`,
		string(src), string(lo.Field), lo.Value, string(hi.Field), hi.Value, folded, seen); err != nil {
		return fmt.Errorf("postgres: edge upsert: %w", err)
	}
	return nil
}

// foldNode locks, folds and upserts one degree row.
func (s *GraphStore) foldNode(ctx context.Context, tx pgx.Tx, src event.SourceID, n cooccurrence.NodeID, at event.Timestamp) error {
	var (
		degree   float64
		lastSeen int64
	)
	err := tx.QueryRow(ctx, `
		SELECT degree, last_seen_us
		  FROM graph_node
		 WHERE source = $1 AND field = $2 AND value = $3
		 FOR UPDATE`,
		string(src), string(n.Field), n.Value).Scan(&degree, &lastSeen)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("postgres: node select for update: %w", err)
	}
	folded, seen := s.foldWeight(degree, lastSeen, at)
	if _, err := tx.Exec(ctx, `
		INSERT INTO graph_node (source, field, value, degree, last_seen_us)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (source, field, value) DO UPDATE
		   SET degree = EXCLUDED.degree, last_seen_us = EXCLUDED.last_seen_us`,
		string(src), string(n.Field), n.Value, folded, seen); err != nil {
		return fmt.Errorf("postgres: node upsert: %w", err)
	}
	return nil
}

// foldTotal locks, folds and upserts the source's total-weight row.
func (s *GraphStore) foldTotal(ctx context.Context, tx pgx.Tx, src event.SourceID, at event.Timestamp) error {
	var (
		weight   float64
		lastSeen int64
	)
	err := tx.QueryRow(ctx, `
		SELECT weight, last_seen_us
		  FROM graph_total
		 WHERE source = $1
		 FOR UPDATE`,
		string(src)).Scan(&weight, &lastSeen)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("postgres: total select for update: %w", err)
	}
	folded, seen := s.foldWeight(weight, lastSeen, at)
	if _, err := tx.Exec(ctx, `
		INSERT INTO graph_total (source, weight, last_seen_us)
		VALUES ($1, $2, $3)
		ON CONFLICT (source) DO UPDATE
		   SET weight = EXCLUDED.weight, last_seen_us = EXCLUDED.last_seen_us`,
		string(src), folded, seen); err != nil {
		return fmt.Errorf("postgres: total upsert: %w", err)
	}
	return nil
}
