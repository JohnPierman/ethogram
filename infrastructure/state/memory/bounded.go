package memory

import (
	"context"
	"sort"

	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/novelty"
)

// BoundedNoveltyStore is [NoveltyStore] with a per-(entity, field) ceiling on how many values
// it holds (§13.3, issue #3).
//
// # Why a second store rather than a parameter
//
// Because the two make different claims and a run should say which it made. The unbounded store
// answers equation (4) exactly and grows with the vocabulary; this one answers it approximately
// and does not grow. A single store with a capacity of zero meaning "unbounded" would let a run
// record a number without recording which of those two things it is.
//
// The domain carries the sketch and the error bound it states; this is the repository around it.
// The bound is per (entity, field), which is the scope equation (4) sums over: bounding the
// store globally would let one busy account's vocabulary evict a quiet account's, and the null
// is per entity precisely so that cannot happen.
type BoundedNoveltyStore struct {
	halfLife novelty.HalfLife
	capacity int
	sets     map[fieldKey]*novelty.Bounded
}

// NewBoundedNoveltyStore returns an empty store holding at most capacity values per
// (entity, field), decaying at halfLife.
func NewBoundedNoveltyStore(halfLife novelty.HalfLife, capacity int) *BoundedNoveltyStore {
	return &BoundedNoveltyStore{
		halfLife: halfLife,
		capacity: capacity,
		sets:     make(map[fieldKey]*novelty.Bounded),
	}
}

// FindAllByEntityField implements novelty.ValueCountRepository, in ascending value order — the
// repository contract equation (5)'s float accumulation depends on, promised identically by the
// unbounded and the Postgres stores.
func (s *BoundedNoveltyStore) FindAllByEntityField(_ context.Context, src event.SourceID,
	en event.EntityID, f event.FieldPath, at event.Timestamp) ([]novelty.ValueRow, error) {

	set := s.sets[fieldKey{entityKey{src, en}, f}]
	if set == nil {
		return nil, nil
	}
	rows := set.Rows(at, s.halfLife)
	out := make([]novelty.ValueRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, novelty.ValueRow{
			Value:     r.Value,
			Count:     r.Count,
			FirstSeen: r.FirstSeen,
			LastSeen:  r.LastSeen,
		})
	}
	return out, nil
}

// SaveObservation implements novelty.ValueCountRepository. Eviction happens here and is
// reported through [BoundedNoveltyStore.Report] rather than logged, because how often the bound
// binds is a measurement.
func (s *BoundedNoveltyStore) SaveObservation(_ context.Context, src event.SourceID,
	en event.EntityID, f event.FieldPath, value string, at event.Timestamp) error {

	key := fieldKey{entityKey{src, en}, f}
	set, ok := s.sets[key]
	if !ok {
		set = novelty.NewBounded(s.capacity)
		s.sets[key] = set
	}
	set.Observe(value, at, s.halfLife)
	return nil
}

// Rows reports the number of value rows held, for table T5's state measurements. It is bounded
// by capacity times the number of (entity, field) pairs, which is the whole point.
func (s *BoundedNoveltyStore) Rows() int64 {
	var n int64
	for _, set := range s.sets {
		n += int64(set.Held())
	}
	return n
}

// Report is what the bound cost, so a run that used it says so with numbers.
//
// The overstatement is reported as its maximum over sets rather than a mean: the claim a bounded
// store can make is "no held count is wrong by more than this", and an average would describe a
// typical set while the score of any single event depends on its own.
func (s *BoundedNoveltyStore) Report() map[string]any {
	var (
		saturated         int
		evictions         int64
		evictedSingletons int64
		worst             float64
		heldTotal         int64
		seenTotal         int64
	)
	worstKey := ""
	keys := make([]fieldKey, 0, len(s.sets))
	for k := range s.sets {
		keys = append(keys, k)
	}
	// One fixed order, so the reported worst case cannot depend on map iteration (R4).
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].entityKey != keys[j].entityKey {
			if keys[i].source != keys[j].source {
				return keys[i].source < keys[j].source
			}
			return keys[i].entity < keys[j].entity
		}
		return keys[i].field < keys[j].field
	})
	for _, k := range keys {
		set := s.sets[k]
		heldTotal += int64(set.Held())
		seenTotal += set.DistinctSeen()
		evictions += set.Evictions()
		evictedSingletons += set.EvictedSingletons()
		if set.Saturated() {
			saturated++
		}
		if set.Overstatement() > worst {
			worst = set.Overstatement()
			worstKey = string(k.entity) + " " + string(k.field)
		}
	}

	return map[string]any{
		"bounded":                   true,
		"capacity_per_entity_field": s.capacity,
		"sets":                      len(s.sets),
		"sets_saturated":            saturated,
		"values_held":               heldTotal,
		"values_admitted":           seenTotal,
		"evictions":                 evictions,
		"evicted_singletons":        evictedSingletons,
		"worst_overstatement":       worst,
		"worst_overstatement_at":    worstKey,
		"note": "space-saving: a held count never under-states the truth and over-states it " +
			"by at most worst_overstatement, an evicted value's true weight is at most the " +
			"least weight held when it went, and the total is exact because eviction moves " +
			"weight rather than discarding it",
		"vocabulary_note": "values_held brackets the true vocabulary from below and " +
			"values_admitted from above: a value evicted and re-admitted is counted twice",
	}
}

// TailCountOfCounts implements novelty.TailReporter: the singleton weight belonging to values
// this store has evicted for (source, entity, field).
//
// It reports the count of evicted entries that were carrying a single observation of their own,
// which is the N₁ contribution eviction removed from the held rows. Without it a bounded store
// hands the Good–Turing reserve a distribution with the tail cut off, and the estimator reads a
// closed vocabulary where the truth is open — measured, that took novelty's detections on the
// open-vocabulary corpus from 864 of 864 to 0.
//
// `saturated` is false for a set that has never evicted anything, so an unsaturated set is
// reported as having no missing tail rather than a tail of zero weight. The two differ: the
// first is exact and the second is an estimate that happens to be zero.
func (s *BoundedNoveltyStore) TailCountOfCounts(_ context.Context, src event.SourceID,
	en event.EntityID, f event.FieldPath) (singletons float64, saturated bool, err error) {

	set := s.sets[fieldKey{entityKey{src, en}, f}]
	if set == nil || !set.Saturated() {
		return 0, false, nil
	}
	return float64(set.EvictedSingletons()), true, nil
}
