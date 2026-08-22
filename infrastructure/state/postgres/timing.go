package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/timing"
)

// TimingStore implements timing.StateRepository. The stored state is exactly 2H + 1
// floats per entity (§7.2) — C[] and S[] as float8[] plus W — alongside last_seen_us,
// fixed size regardless of event count, which is the §13.3 finite-state requirement.
//
// No read-time decay applies here: discounting every moment and W by the same factor
// leaves the fitted shape invariant (see timing.Detector.Score), so the row is stored
// and returned verbatim, exactly as the memory store does.
type TimingStore struct {
	pool *pgxpool.Pool
}

// FindByEntity implements timing.StateRepository. The lookup is by primary key, so
// the single-row result is trivially totally ordered. The returned state is built
// from freshly scanned slices, so a caller cannot mutate the store outside SaveState
// — the isolation that keeps Score writeless (§5.2).
func (s *TimingStore) FindByEntity(ctx context.Context, src event.SourceID, en event.EntityID) (*timing.State, bool, error) {
	var (
		c, sn              []float64
		w                  float64
		logUSum, logUSumSq float64
		observed           int64
		lastSeen           int64
	)
	err := s.pool.QueryRow(ctx, `
		SELECT c, s, w, log_u_sum, log_u_sumsq, observed, last_seen_us
		  FROM timing_state
		 WHERE source = $1 AND entity = $2`,
		string(src), string(en)).Scan(&c, &sn, &w, &logUSum, &logUSumSq, &observed, &lastSeen)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil // cold start, not an error (§7.5)
	}
	if err != nil {
		return nil, false, fmt.Errorf("postgres: timing select: %w", err)
	}
	return &timing.State{
		Moments:   &timing.Moments{C: c, S: sn, W: w},
		LastSeen:  event.Timestamp(lastSeen),
		LogUSum:   logUSum,
		LogUSumSq: logUSumSq,
		Observed:  observed,
	}, true, nil
}

// SaveState implements timing.StateRepository. The repository contract is whole-state
// replacement — the §6.2 fold already happened in the domain's Commit — so a plain
// upsert in one round trip is exact; no Go-side arithmetic is owed here.
func (s *TimingStore) SaveState(ctx context.Context, src event.SourceID, en event.EntityID, st *timing.State) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO timing_state
		       (source, entity, c, s, w, log_u_sum, log_u_sumsq, observed, last_seen_us)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (source, entity) DO UPDATE
		   SET c = EXCLUDED.c,
		       s = EXCLUDED.s,
		       w = EXCLUDED.w,
		       log_u_sum = EXCLUDED.log_u_sum,
		       log_u_sumsq = EXCLUDED.log_u_sumsq,
		       observed = EXCLUDED.observed,
		       last_seen_us = EXCLUDED.last_seen_us`,
		string(src), string(en), st.Moments.C, st.Moments.S, st.Moments.W,
		st.LogUSum, st.LogUSumSq, st.Observed, int64(st.LastSeen))
	if err != nil {
		return fmt.Errorf("postgres: timing upsert: %w", err)
	}
	return nil
}
