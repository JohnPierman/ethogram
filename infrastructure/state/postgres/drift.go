package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/JohnPierman/ethogram/domain/drift"
	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/volume"
)

// DriftStore implements drift.StateRepository.
//
// The stored state is nine numbers per entity: the two parameters of the arm's own Gamma
// posterior over closed periods, the CUSUM as of the last closed period, the three moments
// of the null that period's sums have produced plus the undiscounted count behind them, the
// period in progress and its running count, and last_seen. Fixed size regardless of event
// count, as §13.3 requires.
//
// The undiscounted counts are stored separately from the discounted weight on purpose, and
// it is the same distinction #37 turned into one bound across three arms: a discounted
// weight saturates at 1/(1−δ) and cannot express "how many periods", so a sample-size gate
// reading it is unsatisfiable rather than merely slow. Both are persisted because both are
// state, and a schema that carried only the weight would silently reintroduce the defect on
// the first restart.
//
// No read-time decay: the fold happens in the domain's Commit and the contract is whole-state
// replacement, so the row is stored and returned verbatim, as the memory store does.
type DriftStore struct {
	pool *pgxpool.Pool
}

// FindByEntity implements drift.StateRepository.
func (s *DriftStore) FindByEntity(ctx context.Context, src event.SourceID,
	en event.EntityID) (*drift.State, bool, error) {

	var (
		rateA, rateB       float64
		cusum              float64
		nullSum, nullSumSq float64
		nullW              float64
		nullObserved       int64
		periodIndex        int64
		periodCount        int64
		completedPeriods   int64
		lastSeen           int64
	)
	err := s.pool.QueryRow(ctx, `
		SELECT rate_a, rate_b, cusum, null_sum, null_sumsq, null_w, null_observed,
		       period_index, period_count, completed_periods, last_seen_us
		  FROM drift_state
		 WHERE source = $1 AND entity = $2`,
		string(src), string(en)).Scan(&rateA, &rateB, &cusum, &nullSum, &nullSumSq,
		&nullW, &nullObserved, &periodIndex, &periodCount, &completedPeriods, &lastSeen)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil // cold start, not an error
	}
	if err != nil {
		return nil, false, fmt.Errorf("postgres: drift select: %w", err)
	}
	return &drift.State{
		Rate:  volume.GammaPosterior{A: rateA, B: rateB},
		Cusum: cusum,
		Null: drift.Null{
			Sum: nullSum, SumSq: nullSumSq, W: nullW, Observed: nullObserved,
		},
		PeriodIndex:      periodIndex,
		PeriodCount:      periodCount,
		CompletedPeriods: completedPeriods,
		LastSeen:         event.Timestamp(lastSeen),
	}, true, nil
}

// SaveState implements drift.StateRepository as a single-round-trip upsert.
func (s *DriftStore) SaveState(ctx context.Context, src event.SourceID,
	en event.EntityID, st *drift.State) error {

	_, err := s.pool.Exec(ctx, `
		INSERT INTO drift_state
		       (source, entity, rate_a, rate_b, cusum, null_sum, null_sumsq, null_w,
		        null_observed, period_index, period_count, completed_periods, last_seen_us)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (source, entity) DO UPDATE
		   SET rate_a            = EXCLUDED.rate_a,
		       rate_b            = EXCLUDED.rate_b,
		       cusum             = EXCLUDED.cusum,
		       null_sum          = EXCLUDED.null_sum,
		       null_sumsq        = EXCLUDED.null_sumsq,
		       null_w            = EXCLUDED.null_w,
		       null_observed     = EXCLUDED.null_observed,
		       period_index      = EXCLUDED.period_index,
		       period_count      = EXCLUDED.period_count,
		       completed_periods = EXCLUDED.completed_periods,
		       last_seen_us      = EXCLUDED.last_seen_us`,
		string(src), string(en), st.Rate.A, st.Rate.B, st.Cusum,
		st.Null.Sum, st.Null.SumSq, st.Null.W, st.Null.Observed,
		st.PeriodIndex, st.PeriodCount, st.CompletedPeriods, int64(st.LastSeen))
	if err != nil {
		return fmt.Errorf("postgres: drift upsert: %w", err)
	}
	return nil
}
