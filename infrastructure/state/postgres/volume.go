package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/volume"
)

// VolumeStore implements volume.StateRepository. The stored state is the fixed-size
// struct of §7.4 — the Gamma(a, b) posterior over completed periods plus the running
// period and window counters — which is what §13.3's finite-state requirement asks
// of it. The per-period discount is folded by the domain on commit, so the row is
// stored and returned verbatim, exactly as the memory store does.
type VolumeStore struct {
	pool *pgxpool.Pool
}

// FindByEntity implements volume.StateRepository. The lookup is by primary key, so
// the single-row result is trivially totally ordered. The returned state is a fresh
// struct per call — the same isolation the memory store provides by copying.
func (s *VolumeStore) FindByEntity(ctx context.Context, src event.SourceID, en event.EntityID) (*volume.State, bool, error) {
	var (
		a, b                                               float64
		periodIndex, periodCount, windowIndex, windowCount int64
		completedPeriods                                   int64
		lastSeen                                           int64
	)
	err := s.pool.QueryRow(ctx, `
		SELECT a, b, period_index, period_count, completed_periods, window_index, window_count, last_seen_us
		  FROM volume_state
		 WHERE source = $1 AND entity = $2`,
		string(src), string(en)).Scan(&a, &b, &periodIndex, &periodCount, &completedPeriods, &windowIndex, &windowCount, &lastSeen)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil // cold start, not an error (§7.5)
	}
	if err != nil {
		return nil, false, fmt.Errorf("postgres: volume select: %w", err)
	}
	return &volume.State{
		Rate:             volume.GammaPosterior{A: a, B: b},
		PeriodIndex:      periodIndex,
		PeriodCount:      periodCount,
		CompletedPeriods: completedPeriods,
		WindowIndex:      windowIndex,
		WindowCount:      windowCount,
		LastSeen:         event.Timestamp(lastSeen),
	}, true, nil
}

// SaveState implements volume.StateRepository. As with timing, the contract is
// whole-state replacement, so a plain upsert in one round trip is exact.
func (s *VolumeStore) SaveState(ctx context.Context, src event.SourceID, en event.EntityID, st *volume.State) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO volume_state
		       (source, entity, a, b, period_index, period_count, completed_periods, window_index, window_count, last_seen_us)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (source, entity) DO UPDATE
		   SET a = EXCLUDED.a,
		       b = EXCLUDED.b,
		       period_index = EXCLUDED.period_index,
		       period_count = EXCLUDED.period_count,
		       completed_periods = EXCLUDED.completed_periods,
		       window_index = EXCLUDED.window_index,
		       window_count = EXCLUDED.window_count,
		       last_seen_us = EXCLUDED.last_seen_us`,
		string(src), string(en), st.Rate.A, st.Rate.B,
		st.PeriodIndex, st.PeriodCount, st.CompletedPeriods,
		st.WindowIndex, st.WindowCount, int64(st.LastSeen))
	if err != nil {
		return fmt.Errorf("postgres: volume upsert: %w", err)
	}
	return nil
}
