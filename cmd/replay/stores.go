package main

import (
	"context"
	"fmt"

	"github.com/JohnPierman/ethogram/domain/cooccurrence"
	"github.com/JohnPierman/ethogram/domain/drift"
	"github.com/JohnPierman/ethogram/domain/marginal"
	"github.com/JohnPierman/ethogram/domain/novelty"
	"github.com/JohnPierman/ethogram/domain/noveltyrate"
	"github.com/JohnPierman/ethogram/domain/timing"
	"github.com/JohnPierman/ethogram/domain/volume"
	"github.com/JohnPierman/ethogram/infrastructure/state/memory"
	"github.com/JohnPierman/ethogram/infrastructure/state/postgres"
)

// The state seam (#48).
//
// # What was missing
//
// Every file in results/ was produced with the in-memory stores, so the framework had no
// recorded measurement that the persistent path produces the same numbers end to end -- only
// per-operation equivalence tests over synthetic observations. Those are strong evidence and a
// different claim: a replay folds state across millions of events and seven days, and an
// equivalence that holds per call can still be broken by a transaction boundary, a partial
// write, or a field added without a column. The last has now happened twice.
//
// The reason it stayed missing is that `cmd/replay` had no Postgres path at all. It named the
// memory constructors directly, so there was nothing to switch; and three of the seven arms --
// `noveltyrate`, `drift` and `marginal` -- had no Postgres store to switch to. The
// schema-drift guard recorded those three as absent tables rather than as a gap to fill.
//
// # What this is not
//
// A full-corpus Postgres replay. Every scored event does a find and a save per stateful arm,
// so the persistent path is bounded by round trips rather than arithmetic: even at a generous
// five thousand round trips a second that is well over a day for the injected corpus. Once a
// matched prefix agrees exactly, extending it tests the database's throughput rather than the
// framework's correctness, and the framework claims nothing about that throughput.

// storeKind is what the -store flag selects.
type storeKind string

const (
	storeMemory   storeKind = "memory"
	storePostgres storeKind = "postgres"
)

// parseStore resolves the flag.
func parseStore(s string) (storeKind, error) {
	switch storeKind(s) {
	case storeMemory:
		return storeMemory, nil
	case storePostgres:
		return storePostgres, nil
	default:
		return "", fmt.Errorf("unknown store %q: want memory or postgres", s)
	}
}

// stateStores is the state layer a replay wires its detectors to.
//
// It exists so the choice of backing store is made once, at the top, rather than by six
// constructors scattered through the wiring. Every field is the domain's own repository
// interface, which is what makes the substitution a substitution rather than a rewrite.
type stateStores struct {
	kind        storeKind
	novelty     novelty.ValueCountRepository
	noveltyRate noveltyrate.StateRepository
	timing      timing.StateRepository
	volume      volume.StateRepository
	drift       drift.StateRepository
	marginal    marginal.Repository

	// graph is the co-occurrence graph. It is kept as the concrete memory type rather than
	// the interface because the burn-in graph export is a method on it, and that export is
	// how the Leiden partition is computed; see newStateStores for what -store postgres
	// does about it.
	graph *cooccurrence.MemoryGraph

	// bounded is the per-(entity, field) value ceiling report (#3), or nil where the store
	// holds every value. Reported rather than inferred from the flag, so a run says what its
	// state actually cost.
	bounded func() map[string]any

	// counts reports the final store sizes for the run record.
	//
	// They belong in the equivalence comparison rather than masked out of it: a store size
	// is deterministic, and two runs producing identical scores from different-sized stores
	// would be a defect the score comparison alone could miss. The memory stores answer
	// from their own maps; the Postgres ones issue one count query per table, once, at the
	// end of the run.
	counts func(ctx context.Context) map[string]any
	// close releases whatever the store holds, and is never nil.
	close func()
}

// newStateStores builds the state layer.
//
// dsn is read only for the Postgres kind. The graph stays in memory in both cases and the run
// record says so: the co-occurrence arm the graph serves is reported as superseded and off,
// and the burn-in export that computes the Leiden partition is a method on the memory graph
// rather than on the repository interface. Substituting it would mean either moving the export
// behind the interface or losing the partition, neither of which is what #48 is asking about.
func newStateStores(ctx context.Context, kind storeKind, dsn string, truncate bool,
	maxValues int, halfLife novelty.HalfLife) (*stateStores, error) {

	graph := cooccurrence.NewMemoryGraph(halfLife)

	if kind == storeMemory {
		tim := memory.NewTimingStore()
		vol := memory.NewVolumeStore()

		// The bounded store is a different claim rather than a tuned version of the same
		// one: it answers equation (4) approximately and does not grow with the vocabulary,
		// where the unbounded store answers it exactly and does. Which was used is recorded.
		var (
			nov         novelty.ValueCountRepository
			rowCount    func() int64
			boundReport func() map[string]any
		)
		if maxValues > 0 {
			b := memory.NewBoundedNoveltyStore(halfLife, maxValues)
			nov, rowCount, boundReport = b, b.Rows, b.Report
		} else {
			u := memory.NewNoveltyStore(halfLife)
			nov, rowCount = u, u.Rows
		}

		return &stateStores{
			kind:        storeMemory,
			bounded:     boundReport,
			novelty:     nov,
			noveltyRate: memory.NewNoveltyRateStore(),
			timing:      tim,
			volume:      vol,
			drift:       memory.NewDriftStore(),
			marginal:    memory.NewMarginalStore(halfLife),
			graph:       graph,
			counts: func(context.Context) map[string]any {
				return map[string]any{
					"novelty_rows":    rowCount(),
					"timing_entities": tim.Entities(),
					"volume_entities": vol.Entities(),
				}
			},
			close: func() {},
		}, nil
	}

	store, err := postgres.New(ctx, dsn, halfLife)
	if err != nil {
		return nil, fmt.Errorf("postgres store: %w", err)
	}
	if truncate {
		// The equivalence comparison is against a memory store, which necessarily starts
		// empty. Without this the persistent side starts with whatever earlier runs left
		// behind, and the first attempt at that comparison disagreed in 5,062 places for
		// exactly that reason -- a defect in the harness, not in either store.
		if err := store.Truncate(ctx); err != nil {
			store.Close()
			return nil, err
		}
	}
	return &stateStores{
		kind:        storePostgres,
		novelty:     store.Novelty(),
		noveltyRate: store.NoveltyRate(),
		timing:      store.Timing(),
		volume:      store.Volume(),
		drift:       store.Drift(),
		marginal:    store.Marginal(),
		graph:       graph,
		counts: func(ctx context.Context) map[string]any {
			out := map[string]any{}
			for name, count := range map[string]func() (int64, error){
				"novelty_rows":    func() (int64, error) { return store.NoveltyRows(ctx) },
				"timing_entities": func() (int64, error) { return store.TimingEntities(ctx) },
				"volume_entities": func() (int64, error) { return store.VolumeEntities(ctx) },
			} {
				rows, err := count()
				if err != nil {
					// Reported rather than fatal: the run's scores are already computed
					// and a failed count query is not a reason to lose them. It will show
					// as a difference in the equivalence comparison, which is where a
					// reader should see it.
					out[name] = map[string]any{"error": err.Error()}
					continue
				}
				out[name] = rows
			}
			return out
		},
		close: store.Close,
	}, nil
}

// record is the store's provenance for the run record.
//
// The comparison #48 asks for is between two runs whose result files must agree once the run
// id and the timing fields are masked, so this block is deliberately the ONLY place the store
// choice appears in a result. A field that named the store anywhere else would make the two
// files differ for a reason that is not a defect.
func (s *stateStores) record() map[string]any {
	values := map[string]any{
		"bounded": false,
		"note": "every value the entity has been seen with is held, so equation (4)'s " +
			"reserved mass is exact and the state grows with the vocabulary (§13.3)",
	}
	if s.bounded != nil {
		values = s.bounded()
	}
	return map[string]any{
		"kind":         string(s.kind),
		"value_counts": values,
		"note": "the co-occurrence graph is in memory under both kinds: the burn-in edge " +
			"export that computes the Leiden partition is a method on the memory graph " +
			"rather than on the repository interface, and the arm it serves is reported " +
			"as superseded and off",
		"arms_backed": []string{
			"novelty", "noveltyrate", "timing", "volume", "drift", "marginal",
		},
	}
}
