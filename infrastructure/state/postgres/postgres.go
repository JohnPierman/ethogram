// Package postgres provides the durable Postgres implementations of the domain state
// repositories, applying the identical lazy-decay semantics the memory package uses:
// a row stores (count, last_seen_us) and the discount 2^(−Δt/T½) is applied on read
// (§6.2). The decay arithmetic runs in Go, on the fetched row, never in SQL, so both
// implementations apply the same float operations to the same inputs and a run scores
// bit-identically whichever store backs it (R4).
//
// State stays finite in the §13.3 sense by the same argument as the memory package:
// novelty rows are bounded by the tracked value sets, timing state is exactly 2H + 1
// floats per entity (§7.2), volume state is a fixed struct, and the graph holds one
// row per node, per edge and per source total. Lazy decay never deletes a row, so row
// counts are high-water counts, exactly as in memory.
//
// Every SELECT feeding scoring carries an explicit total ORDER BY (README trap 5:
// Postgres row order is undefined without one), and value ordering is pinned with
// COLLATE "C" so it is the byte-wise order Go's string comparison uses even if a
// database were initialised without the compose file's --locale=C.
package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/JohnPierman/ethogram/domain/cooccurrence"
	"github.com/JohnPierman/ethogram/domain/novelty"
	"github.com/JohnPierman/ethogram/domain/timing"
	"github.com/JohnPierman/ethogram/domain/volume"
)

// migrations is the embedded schema, one idempotent statement per table
// (CREATE TABLE IF NOT EXISTS), so New may be called repeatedly against the same
// schema without a migration ledger. Tables live in whatever schema the connection's
// search_path selects, which is how the integration suite isolates a run.
var migrations = []string{
	`CREATE TABLE IF NOT EXISTS novelty_value_count (
		source        text   NOT NULL,
		entity        text   NOT NULL,
		field         text   NOT NULL,
		value         text   NOT NULL,
		count         float8 NOT NULL,
		first_seen_us bigint NOT NULL,
		last_seen_us  bigint NOT NULL,
		PRIMARY KEY (source, entity, field, value)
	)`,
	`CREATE TABLE IF NOT EXISTS timing_state (
		source       text     NOT NULL,
		entity       text     NOT NULL,
		c            float8[] NOT NULL,
		s            float8[] NOT NULL,
		w            float8   NOT NULL,
		last_seen_us bigint   NOT NULL,
		PRIMARY KEY (source, entity)
	)`,
	`CREATE TABLE IF NOT EXISTS volume_state (
		source       text   NOT NULL,
		entity       text   NOT NULL,
		a            float8 NOT NULL,
		b            float8 NOT NULL,
		period_index bigint NOT NULL,
		period_count bigint NOT NULL,
		window_index bigint NOT NULL,
		window_count bigint NOT NULL,
		last_seen_us bigint NOT NULL,
		PRIMARY KEY (source, entity)
	)`,
	`CREATE TABLE IF NOT EXISTS graph_node (
		source       text   NOT NULL,
		field        text   NOT NULL,
		value        text   NOT NULL,
		degree       float8 NOT NULL,
		last_seen_us bigint NOT NULL,
		PRIMARY KEY (source, field, value)
	)`,
	`CREATE TABLE IF NOT EXISTS graph_edge (
		source       text   NOT NULL,
		field_a      text   NOT NULL,
		value_a      text   NOT NULL,
		field_b      text   NOT NULL,
		value_b      text   NOT NULL,
		weight       float8 NOT NULL,
		last_seen_us bigint NOT NULL,
		PRIMARY KEY (source, field_a, value_a, field_b, value_b)
	)`,
	`CREATE TABLE IF NOT EXISTS graph_total (
		source       text   PRIMARY KEY,
		weight       float8 NOT NULL,
		last_seen_us bigint NOT NULL
	)`,
}

// Store owns the connection pool and exposes the four repository implementations.
// Like the memory stores, it is written for the single-threaded scoring loop: row
// locks serialise concurrent writers on existing rows, but the store makes no further
// concurrency promises the memory implementations do not make.
type Store struct {
	pool        *pgxpool.Pool
	noveltyRepo *NoveltyStore
	timingRepo  *TimingStore
	volumeRepo  *VolumeStore
	graphRepo   *GraphStore
}

// New connects to connString, applies the embedded migrations, and returns the store.
// halfLife is the decay half-life T½ of §6.2, shared by the novelty rows and the
// co-occurrence graph exactly as the memory constructors share it.
func New(ctx context.Context, connString string, halfLife novelty.HalfLife) (*Store, error) {
	pool, err := pgxpool.New(ctx, connString)
	if err != nil {
		return nil, fmt.Errorf("postgres: create pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: ping: %w", err)
	}
	if err := migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: migrate: %w", err)
	}
	return &Store{
		pool:        pool,
		noveltyRepo: &NoveltyStore{pool: pool, halfLife: halfLife},
		timingRepo:  &TimingStore{pool: pool},
		volumeRepo:  &VolumeStore{pool: pool},
		graphRepo:   &GraphStore{pool: pool, halfLife: halfLife},
	}, nil
}

// migrate applies every embedded statement inside one transaction, so a partially
// created schema never survives a failure.
func migrate(ctx context.Context, pool *pgxpool.Pool) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	// Rollback after a successful commit returns pgx.ErrTxClosed by design; that is
	// the only discarded outcome, and any real failure surfaces on Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	for i, statement := range migrations {
		if _, err := tx.Exec(ctx, statement); err != nil {
			return fmt.Errorf("statement %d: %w", i, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// Novelty returns the novelty.ValueCountRepository implementation.
func (s *Store) Novelty() novelty.ValueCountRepository { return s.noveltyRepo }

// Timing returns the timing.StateRepository implementation.
func (s *Store) Timing() timing.StateRepository { return s.timingRepo }

// Volume returns the volume.StateRepository implementation.
func (s *Store) Volume() volume.StateRepository { return s.volumeRepo }

// Graph returns the cooccurrence.GraphRepository implementation.
func (s *Store) Graph() cooccurrence.GraphRepository { return s.graphRepo }

// Close releases the connection pool.
func (s *Store) Close() { s.pool.Close() }
