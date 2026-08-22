package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/noveltyrate"
)

// NoveltyRateStore implements noveltyrate.StateRepository.
//
// The stored state is six numbers per entity — two decayed history counts over completed
// windows, the index of the window in progress and its two running counts, and last_seen —
// so it is fixed size regardless of event count, which is the §13.3 finite-state
// requirement.
//
// No read-time decay applies. The §6.2 fold happens in the domain's Commit and the
// repository contract is whole-state replacement, so the row is stored and returned
// verbatim, exactly as the memory store does. Applying a decay here as well would discount
// twice.
//
// It exists because #48 asked for a recorded result through Postgres and this arm had no
// Postgres store at all: the schema-drift guard recorded `noveltyrate_state` as a table that
// did not exist, which made an end-to-end equivalence claim impossible to state honestly.
type NoveltyRateStore struct {
	pool *pgxpool.Pool
}

// FindByEntity implements noveltyrate.StateRepository. The lookup is by primary key, so the
// single-row result is trivially totally ordered, and the returned state is freshly built so
// a caller cannot mutate the store outside SaveState — the isolation that keeps Score
// writeless (§5.2).
func (s *NoveltyRateStore) FindByEntity(ctx context.Context, src event.SourceID,
	en event.EntityID) (*noveltyrate.State, bool, error) {

	var (
		historyNovel, historyTotal float64
		windowIndex                int64
		windowNovel, windowTotal   int64
		lastSeen                   int64
	)
	err := s.pool.QueryRow(ctx, `
		SELECT history_novel, history_total, window_index, window_novel, window_total,
		       last_seen_us
		  FROM noveltyrate_state
		 WHERE source = $1 AND entity = $2`,
		string(src), string(en)).Scan(&historyNovel, &historyTotal, &windowIndex,
		&windowNovel, &windowTotal, &lastSeen)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil // cold start, not an error
	}
	if err != nil {
		return nil, false, fmt.Errorf("postgres: noveltyrate select: %w", err)
	}
	return &noveltyrate.State{
		HistoryNovel: historyNovel,
		HistoryTotal: historyTotal,
		WindowIndex:  windowIndex,
		WindowNovel:  windowNovel,
		WindowTotal:  windowTotal,
		LastSeen:     event.Timestamp(lastSeen),
	}, true, nil
}

// SaveState implements noveltyrate.StateRepository as a single-round-trip upsert: the fold
// already happened in the domain, so whole-state replacement is exact and no arithmetic is
// owed here.
func (s *NoveltyRateStore) SaveState(ctx context.Context, src event.SourceID,
	en event.EntityID, st *noveltyrate.State) error {

	_, err := s.pool.Exec(ctx, `
		INSERT INTO noveltyrate_state
		       (source, entity, history_novel, history_total, window_index,
		        window_novel, window_total, last_seen_us)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (source, entity) DO UPDATE
		   SET history_novel = EXCLUDED.history_novel,
		       history_total = EXCLUDED.history_total,
		       window_index  = EXCLUDED.window_index,
		       window_novel  = EXCLUDED.window_novel,
		       window_total  = EXCLUDED.window_total,
		       last_seen_us  = EXCLUDED.last_seen_us`,
		string(src), string(en), st.HistoryNovel, st.HistoryTotal, st.WindowIndex,
		st.WindowNovel, st.WindowTotal, int64(st.LastSeen))
	if err != nil {
		return fmt.Errorf("postgres: noveltyrate upsert: %w", err)
	}
	return nil
}
