package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/JohnPierman/ethogram/domain/burst"
	"github.com/JohnPierman/ethogram/domain/event"
)

// BurstStore implements burst.StateRepository.
//
// The stored state is three numbers, a timestamp and a bounded array of at most
// burst.MaxWindow arrival timestamps, so the row is fixed width regardless of event count --
// the §13.3 requirement. The array is a bigint[] rather than a child table because it is
// read and written whole, always, and never queried into: a child table would turn one round
// trip into two and buy nothing, and the repository contract here is whole-state replacement.
//
// No read-time decay applies. The §6.2 fold happens in the domain, so the row is stored and
// returned verbatim exactly as the memory store does; discounting here as well would
// discount twice.
type BurstStore struct {
	pool *pgxpool.Pool
}

// FindByEntity implements burst.StateRepository. The lookup is by primary key, and the state
// is freshly built with its own array so a caller cannot mutate the store outside SaveState
// -- the isolation that keeps Score writeless (§5.2) and that burst.State.Clone exists for.
func (s *BurstStore) FindByEntity(ctx context.Context, src event.SourceID,
	en event.EntityID) (*burst.State, bool, error) {

	var (
		recent               []int64
		gaps, squared, count float64
		observed             int64
		lastSeen             int64
	)
	err := s.pool.QueryRow(ctx, `
		SELECT recent_us, gaps, gaps_squared, gap_count, observed, last_seen_us
		  FROM burst_state
		 WHERE source = $1 AND entity = $2`,
		string(src), string(en)).Scan(&recent, &gaps, &squared, &count, &observed, &lastSeen)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil // cold start, not an error
	}
	if err != nil {
		return nil, false, fmt.Errorf("postgres: burst select: %w", err)
	}

	// Arrivals come back oldest first, the order the array was written in and the order
	// burst.Evaluate reads spans from. A store that returned them reversed would make every
	// span negative and the arm would abstain on every event while reporting no defect.
	held := make([]event.Timestamp, len(recent))
	for i, us := range recent {
		held[i] = event.Timestamp(us)
	}
	return &burst.State{
		Recent:      held,
		Gaps:        gaps,
		GapsSquared: squared,
		Count:       count,
		Observed:    observed,
		LastSeen:    event.Timestamp(lastSeen),
	}, true, nil
}

// SaveState implements burst.StateRepository as a single-round-trip upsert. The fold already
// happened in the domain, so whole-state replacement is exact and no arithmetic is owed here.
func (s *BurstStore) SaveState(ctx context.Context, src event.SourceID,
	en event.EntityID, st *burst.State) error {

	recent := make([]int64, len(st.Recent))
	for i, at := range st.Recent {
		recent[i] = int64(at)
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO burst_state (source, entity, recent_us, gaps, gaps_squared, gap_count,
		                         observed, last_seen_us)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (source, entity) DO UPDATE
		   SET recent_us    = EXCLUDED.recent_us,
		       gaps         = EXCLUDED.gaps,
		       gaps_squared = EXCLUDED.gaps_squared,
		       gap_count    = EXCLUDED.gap_count,
		       observed     = EXCLUDED.observed,
		       last_seen_us = EXCLUDED.last_seen_us`,
		string(src), string(en), recent, st.Gaps, st.GapsSquared, st.Count, st.Observed,
		int64(st.LastSeen))
	if err != nil {
		return fmt.Errorf("postgres: burst upsert: %w", err)
	}
	return nil
}

// BurstEntities reports how many entities hold inter-arrival state, for the run record.
func (s *Store) BurstEntities(ctx context.Context) (int64, error) {
	var n int64
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM burst_state`).Scan(&n); err != nil {
		return 0, fmt.Errorf("postgres: burst count: %w", err)
	}
	return n, nil
}

// Report summarises the held inter-arrival state, and it is deliberately not a SQL aggregate.
//
// The rows are read and handed to the domain's [burst.Summariser], the same accumulator the
// memory store feeds, so the two agree by construction. The first store-equivalence run over this
// arm disagreed in exactly the five keys this produces -- the scores were identical over 4,491
// events and only the report differed, because this store did not compute it. Writing the
// arithmetic again in SQL would have made them agree by coincidence at best: `percentile_cont`
// interpolates and the domain's median takes the upper of two middle values, so an even entity
// count would have disagreed again and the difference would have read as a defect in the arm.
//
// The read is bounded by the number of entities, not events, and it happens once at the end of a
// run — the same scope the memory store's map walk has.
func (s *Store) BurstReport(ctx context.Context) (burst.Report, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT recent_us, gaps, gaps_squared, gap_count, observed, last_seen_us
		  FROM burst_state`)
	if err != nil {
		return burst.Report{}, fmt.Errorf("postgres: burst report select: %w", err)
	}
	defer rows.Close()

	sum := burst.NewSummariser()
	for rows.Next() {
		var (
			recent               []int64
			gaps, squared, count float64
			observed             int64
			lastSeen             int64
		)
		if err := rows.Scan(&recent, &gaps, &squared, &count, &observed,
			&lastSeen); err != nil {
			return burst.Report{}, fmt.Errorf("postgres: burst report scan: %w", err)
		}
		held := make([]event.Timestamp, len(recent))
		for i, us := range recent {
			held[i] = event.Timestamp(us)
		}
		sum.Add(&burst.State{
			Recent:      held,
			Gaps:        gaps,
			GapsSquared: squared,
			Count:       count,
			Observed:    observed,
			LastSeen:    event.Timestamp(lastSeen),
		})
	}
	if err := rows.Err(); err != nil {
		return burst.Report{}, fmt.Errorf("postgres: burst report rows: %w", err)
	}
	return sum.Report(), nil
}
