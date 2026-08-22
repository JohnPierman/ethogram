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

	"github.com/JohnPierman/ethogram/domain/burst"
	"github.com/JohnPierman/ethogram/domain/cooccurrence"
	"github.com/JohnPierman/ethogram/domain/drift"
	"github.com/JohnPierman/ethogram/domain/marginal"
	"github.com/JohnPierman/ethogram/domain/novelty"
	"github.com/JohnPierman/ethogram/domain/noveltyrate"
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
		log_u_sum    float8   NOT NULL DEFAULT 0,
		log_u_sumsq  float8   NOT NULL DEFAULT 0,
		observed     bigint   NOT NULL DEFAULT 0,
		last_seen_us bigint   NOT NULL,
		PRIMARY KEY (source, entity)
	)`,
	`ALTER TABLE timing_state ADD COLUMN IF NOT EXISTS log_u_sum float8 NOT NULL DEFAULT 0`,
	`ALTER TABLE timing_state ADD COLUMN IF NOT EXISTS log_u_sumsq float8 NOT NULL DEFAULT 0`,
	// The undiscounted observation count the standardisation gate reads, for the same reason
	// as volume's above: a discounted weight saturates and cannot answer a sample-size
	// question.
	`ALTER TABLE timing_state ADD COLUMN IF NOT EXISTS observed bigint NOT NULL DEFAULT 0`,
	`CREATE TABLE IF NOT EXISTS volume_state (
		source       text   NOT NULL,
		entity       text   NOT NULL,
		a            float8 NOT NULL,
		b            float8 NOT NULL,
		period_index bigint NOT NULL,
		period_count bigint NOT NULL,
		completed_periods bigint NOT NULL DEFAULT 0,
		window_index bigint NOT NULL,
		window_count bigint NOT NULL,
		window_expected float8 NOT NULL DEFAULT 0,
		dispersion_windows float8 NOT NULL DEFAULT 0,
		dispersion_sum float8 NOT NULL DEFAULT 0,
		dispersion_observed bigint NOT NULL DEFAULT 0,
		last_seen_us bigint NOT NULL,
		PRIMARY KEY (source, entity)
	)`,
	`ALTER TABLE volume_state ADD COLUMN IF NOT EXISTS completed_periods bigint NOT NULL DEFAULT 0`,
	// The dispersion accumulators and the open window's recorded expectation. Absent until
	// #33: SaveState never wrote them and FindByEntity never read them, so a Postgres-backed
	// run lost the measured width of every entity's null across a restart and silently
	// resumed against equation (11) un-widened -- which since #42 also decides whether the
	// arm abstains, so the loss changed a verdict's kind and not only its number.
	//
	// Defaulting to zero is the correct cold value: it is what a fresh `volume.State` carries,
	// so an existing row migrates to "dispersion not yet measured" rather than to a fabricated
	// one, and the arm re-measures from the next completed window.
	`ALTER TABLE volume_state ADD COLUMN IF NOT EXISTS window_expected float8 NOT NULL DEFAULT 0`,
	`ALTER TABLE volume_state ADD COLUMN IF NOT EXISTS dispersion_windows float8 NOT NULL DEFAULT 0`,
	`ALTER TABLE volume_state ADD COLUMN IF NOT EXISTS dispersion_sum float8 NOT NULL DEFAULT 0`,
	// The undiscounted window count the dispersion gate reads. Defaulting to zero migrates an
	// existing row to "not yet measured", which is what a fresh state carries, so the arm
	// re-measures from the next completed window rather than resuming on a fabricated count.
	`ALTER TABLE volume_state ADD COLUMN IF NOT EXISTS dispersion_observed bigint NOT NULL DEFAULT 0`,
	// The three tables below complete the persistent path. Before them, `noveltyrate`,
	// `drift` and `marginal` had no Postgres store at all, which the schema-drift guard
	// recorded as an absence -- and which made #48's end-to-end equivalence claim
	// impossible to state honestly, since three of the seven arms would have run in memory
	// on both sides of the comparison.
	`CREATE TABLE IF NOT EXISTS noveltyrate_state (
		source        text   NOT NULL,
		entity        text   NOT NULL,
		history_novel float8 NOT NULL,
		history_total float8 NOT NULL,
		window_index  bigint NOT NULL,
		window_novel  bigint NOT NULL,
		window_total  bigint NOT NULL,
		last_seen_us  bigint NOT NULL,
		PRIMARY KEY (source, entity)
	)`,
	// recent_us is bounded at burst.MaxWindow entries by the domain, so the row is fixed
	// width; see BurstStore for why it is an array rather than a child table.
	`CREATE TABLE IF NOT EXISTS burst_state (
		source       text     NOT NULL,
		entity       text     NOT NULL,
		recent_us    bigint[] NOT NULL,
		gaps         float8   NOT NULL,
		gap_count    float8   NOT NULL,
		observed     bigint   NOT NULL,
		last_seen_us bigint   NOT NULL,
		PRIMARY KEY (source, entity)
	)`,
	`CREATE TABLE IF NOT EXISTS drift_state (
		source            text   NOT NULL,
		entity            text   NOT NULL,
		rate_a            float8 NOT NULL,
		rate_b            float8 NOT NULL,
		cusum             float8 NOT NULL,
		null_sum          float8 NOT NULL,
		null_sumsq        float8 NOT NULL,
		null_w            float8 NOT NULL,
		null_observed     bigint NOT NULL DEFAULT 0,
		period_index      bigint NOT NULL,
		period_count      bigint NOT NULL,
		completed_periods bigint NOT NULL DEFAULT 0,
		last_seen_us      bigint NOT NULL,
		PRIMARY KEY (source, entity)
	)`,
	`CREATE TABLE IF NOT EXISTS marginal_value_count (
		source       text   NOT NULL,
		field        text   NOT NULL,
		value        text   NOT NULL,
		count        float8 NOT NULL,
		last_seen_us bigint NOT NULL,
		PRIMARY KEY (source, field, value)
	)`,
	`CREATE TABLE IF NOT EXISTS marginal_sketch (
		source        text     NOT NULL,
		field         text     NOT NULL,
		max_centroids int      NOT NULL,
		means         float8[] NOT NULL,
		weights       float8[] NOT NULL,
		last_seen_us  bigint   NOT NULL,
		PRIMARY KEY (source, field)
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
	pool            *pgxpool.Pool
	noveltyRepo     *NoveltyStore
	noveltyRateRepo *NoveltyRateStore
	burstRepo       *BurstStore
	timingRepo      *TimingStore
	volumeRepo      *VolumeStore
	driftRepo       *DriftStore
	marginalRepo    *MarginalStore
	graphRepo       *GraphStore
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
		pool:            pool,
		noveltyRepo:     &NoveltyStore{pool: pool, halfLife: halfLife},
		noveltyRateRepo: &NoveltyRateStore{pool: pool},
		burstRepo:       &BurstStore{pool: pool},
		timingRepo:      &TimingStore{pool: pool},
		volumeRepo:      &VolumeStore{pool: pool},
		driftRepo:       &DriftStore{pool: pool},
		marginalRepo:    &MarginalStore{pool: pool, halfLife: halfLife},
		graphRepo:       &GraphStore{pool: pool, halfLife: halfLife},
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

// NoveltyRate returns the noveltyrate.StateRepository implementation.
func (s *Store) NoveltyRate() noveltyrate.StateRepository { return s.noveltyRateRepo }

// Burst returns the inter-arrival state repository (#53).
func (s *Store) Burst() burst.StateRepository { return s.burstRepo }

// Drift returns the drift.StateRepository implementation.
func (s *Store) Drift() drift.StateRepository { return s.driftRepo }

// Marginal returns the marginal.Repository implementation.
func (s *Store) Marginal() marginal.Repository { return s.marginalRepo }

// Graph returns the cooccurrence.GraphRepository implementation.
func (s *Store) Graph() cooccurrence.GraphRepository { return s.graphRepo }

// Close releases the connection pool.
func (s *Store) Close() { s.pool.Close() }

// Rows counts the persisted novelty value-count rows.
//
// It exists for the run record, and for one reason beyond reporting: the final store size is
// deterministic, so it belongs in the equivalence comparison of #48 rather than being masked
// out of it. Two runs that produce identical scores from different-sized stores would be a
// defect the score comparison alone could miss.
func (s *NoveltyStore) Rows(ctx context.Context) (int64, error) {
	var rows int64
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM novelty_value_count`).Scan(&rows); err != nil {
		return 0, fmt.Errorf("postgres: novelty row count: %w", err)
	}
	return rows, nil
}

// Entities counts the persisted timing states.
func (s *TimingStore) Entities(ctx context.Context) (int64, error) {
	var rows int64
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM timing_state`).Scan(&rows); err != nil {
		return 0, fmt.Errorf("postgres: timing entity count: %w", err)
	}
	return rows, nil
}

// Entities counts the persisted volume states.
func (s *VolumeStore) Entities(ctx context.Context) (int64, error) {
	var rows int64
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM volume_state`).Scan(&rows); err != nil {
		return 0, fmt.Errorf("postgres: volume entity count: %w", err)
	}
	return rows, nil
}

// NoveltyRows, TimingEntities and VolumeEntities expose the per-table counts through the
// store, so a caller holding a *Store does not need the concrete repository types.
func (s *Store) NoveltyRows(ctx context.Context) (int64, error) {
	return s.noveltyRepo.Rows(ctx)
}

func (s *Store) TimingEntities(ctx context.Context) (int64, error) {
	return s.timingRepo.Entities(ctx)
}

func (s *Store) VolumeEntities(ctx context.Context) (int64, error) {
	return s.volumeRepo.Entities(ctx)
}

// tables is every table this store owns, in the order Truncate empties them.
//
// Listed explicitly rather than discovered from the catalogue, so that truncating cannot
// reach a table this store did not create. The database it connects to is shared with the
// integration suite and with whatever else a developer has put there.
var tables = []string{
	"novelty_value_count",
	"noveltyrate_state",
	"burst_state",
	"timing_state",
	"volume_state",
	"drift_state",
	"marginal_value_count",
	"marginal_sketch",
	"graph_edge",
	"graph_node",
}

// Truncate empties this store's tables, in one transaction.
//
// It exists for the equivalence harness of #48 and for nothing else. The comparison is
// between a memory store, which necessarily starts empty, and a persistent one, which does
// not: the first run of that comparison disagreed in 5,062 places because the database had
// five hours of earlier runs in it, and the Postgres side saw 4.5 times as many observations
// per detector pair as a result. That was a defect in the harness rather than in either
// store, and this is the repair.
//
// It names its own tables rather than truncating the schema, because the database is shared.
func (s *Store) Truncate(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: truncate begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, table := range tables {
		// The identifiers are the constants above, never caller input.
		if _, err := tx.Exec(ctx, "TRUNCATE TABLE "+table); err != nil {
			return fmt.Errorf("postgres: truncate %s: %w", table, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: truncate commit: %w", err)
	}
	return nil
}
