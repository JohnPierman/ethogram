// Package memory provides in-process implementations of the domain state
// repositories, applying the identical lazy-decay semantics the Postgres schema uses:
// a row stores (count, last_seen) and the discount is applied on read (§6.2).
//
// The replay engine uses these for full-corpus runs, where a database round-trip per
// event would dominate the wall-clock that table T5 reports. The Postgres
// implementations remain the durable store; determinism (R4) is unaffected by the
// choice because both apply the same arithmetic to the same inputs, and E8 asserts
// the scores, not the storage.
package memory

import (
	"context"
	"slices"

	"github.com/JohnPierman/ethogram/domain/burst"
	"github.com/JohnPierman/ethogram/domain/drift"
	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/marginal"
	"github.com/JohnPierman/ethogram/domain/novelty"
	"github.com/JohnPierman/ethogram/domain/noveltyrate"
	"github.com/JohnPierman/ethogram/domain/timing"
	"github.com/JohnPierman/ethogram/domain/volume"
)

type entityKey struct {
	source event.SourceID
	entity event.EntityID
}

type fieldKey struct {
	entityKey
	field event.FieldPath
}

// ---------------------------------------------------------------------------
// novelty.ValueCountRepository
// ---------------------------------------------------------------------------

type valueRow struct {
	count     float64
	firstSeen event.Timestamp
	lastSeen  event.Timestamp
}

// valueSet is one (entity, field)'s value rows together with their value order,
// maintained on insertion.
//
// The order is kept here rather than recomputed per read because equation (5) is
// evaluated once per scoreable field per event: rebuilding and sorting the value
// list on every score made the read O(K log K) and dominated the scoring path at
// corpus scale, where K reaches the hundreds for a field like a destination
// computer. Insertion is O(K) in the worst case and amortises to nothing, since a
// value is inserted once and read forever after.
type valueSet struct {
	rows   map[string]*valueRow
	sorted []string // ascending; the repository's ordering contract
}

// NoveltyStore implements novelty.ValueCountRepository.
type NoveltyStore struct {
	halfLife novelty.HalfLife
	sets     map[fieldKey]*valueSet
}

// NewNoveltyStore returns an empty store decaying at halfLife.
func NewNoveltyStore(halfLife novelty.HalfLife) *NoveltyStore {
	return &NoveltyStore{halfLife: halfLife, sets: make(map[fieldKey]*valueSet)}
}

// FindAllByEntityField implements novelty.ValueCountRepository: rows decayed to at, in
// ascending Value order (the repository contract; Postgres does it with ORDER BY).
func (s *NoveltyStore) FindAllByEntityField(_ context.Context, src event.SourceID, en event.EntityID, f event.FieldPath, at event.Timestamp) ([]novelty.ValueRow, error) {
	set := s.sets[fieldKey{entityKey{src, en}, f}]
	if set == nil || len(set.sorted) == 0 {
		return nil, nil
	}
	out := make([]novelty.ValueRow, 0, len(set.sorted))
	for _, v := range set.sorted {
		r := set.rows[v]
		out = append(out, novelty.ValueRow{
			Value:     v,
			Count:     novelty.Decay(r.count, r.lastSeen, at, s.halfLife),
			FirstSeen: r.firstSeen,
			LastSeen:  r.lastSeen,
		})
	}
	return out, nil
}

// SaveObservation implements novelty.ValueCountRepository with the §6.2 lazy rule.
func (s *NoveltyStore) SaveObservation(_ context.Context, src event.SourceID, en event.EntityID, f event.FieldPath, value string, at event.Timestamp) error {
	key := fieldKey{entityKey{src, en}, f}
	set, ok := s.sets[key]
	if !ok {
		set = &valueSet{rows: make(map[string]*valueRow, 4)}
		s.sets[key] = set
	}
	r, ok := set.rows[value]
	if !ok {
		set.rows[value] = &valueRow{count: 1, firstSeen: at, lastSeen: at}
		idx, _ := slices.BinarySearch(set.sorted, value)
		set.sorted = append(set.sorted, "")
		copy(set.sorted[idx+1:], set.sorted[idx:])
		set.sorted[idx] = value
		return nil
	}
	r.count = novelty.Accumulate(r.count, r.lastSeen, at, s.halfLife)
	if at > r.lastSeen {
		r.lastSeen = at
	}
	return nil
}

// Rows reports the number of value rows held, for table T5's state measurements.
func (s *NoveltyStore) Rows() int64 {
	var n int64
	for _, set := range s.sets {
		n += int64(len(set.sorted))
	}
	return n
}

// ---------------------------------------------------------------------------
// timing.StateRepository
// ---------------------------------------------------------------------------

// TimingStore implements timing.StateRepository.
type TimingStore struct {
	states map[entityKey]*timing.State
}

// NewTimingStore returns an empty store.
func NewTimingStore() *TimingStore {
	return &TimingStore{states: make(map[entityKey]*timing.State)}
}

// FindByEntity implements timing.StateRepository. The returned state is a copy, so a
// caller cannot mutate the store outside SaveState — the same isolation a database
// round-trip provides, which is what keeps Score writeless (§5.2).
func (s *TimingStore) FindByEntity(_ context.Context, src event.SourceID, en event.EntityID) (*timing.State, bool, error) {
	st, ok := s.states[entityKey{src, en}]
	if !ok {
		return nil, false, nil
	}
	// Whole-struct copy first, so a field added to timing.State later is carried rather
	// than silently dropped: naming each field here once cost a run in which the timing
	// detector abstained on every event because two new accumulators never came back out
	// of the store. Only the moments need a deep copy, because they hold slices.
	c := *st
	c.Moments = timing.NewMoments(st.Moments.H())
	copy(c.Moments.C, st.Moments.C)
	copy(c.Moments.S, st.Moments.S)
	c.Moments.W = st.Moments.W
	return &c, true, nil
}

// SaveState implements timing.StateRepository.
func (s *TimingStore) SaveState(_ context.Context, src event.SourceID, en event.EntityID, st *timing.State) error {
	s.states[entityKey{src, en}] = st
	return nil
}

// Entities reports the number of entities held, for table T5.
func (s *TimingStore) Entities() int64 { return int64(len(s.states)) }

// ---------------------------------------------------------------------------
// volume.StateRepository
// ---------------------------------------------------------------------------

// VolumeStore implements volume.StateRepository.
type VolumeStore struct {
	states map[entityKey]*volume.State
}

// NewVolumeStore returns an empty store.
func NewVolumeStore() *VolumeStore {
	return &VolumeStore{states: make(map[entityKey]*volume.State)}
}

// FindByEntity implements volume.StateRepository, returning a copy.
func (s *VolumeStore) FindByEntity(_ context.Context, src event.SourceID, en event.EntityID) (*volume.State, bool, error) {
	st, ok := s.states[entityKey{src, en}]
	if !ok {
		return nil, false, nil
	}
	c := *st
	return &c, true, nil
}

// SaveState implements volume.StateRepository.
func (s *VolumeStore) SaveState(_ context.Context, src event.SourceID, en event.EntityID, st *volume.State) error {
	s.states[entityKey{src, en}] = st
	return nil
}

// Entities reports the number of entities held, for table T5.
func (s *VolumeStore) Entities() int64 { return int64(len(s.states)) }

// ---------------------------------------------------------------------------
// drift.StateRepository
// ---------------------------------------------------------------------------

// DriftStore implements drift.StateRepository.
//
// Its own store rather than a field on the volume state: the two arms answer different
// questions about the same counts, and keeping the rows separate leaves every volume figure
// untouched by a run that adds this arm.
type DriftStore struct {
	states map[entityKey]*drift.State
}

// NewDriftStore returns an empty store.
func NewDriftStore() *DriftStore {
	return &DriftStore{states: make(map[entityKey]*drift.State)}
}

// FindByEntity implements drift.StateRepository, returning a copy.
func (s *DriftStore) FindByEntity(_ context.Context, src event.SourceID, en event.EntityID) (*drift.State, bool, error) {
	st, ok := s.states[entityKey{src, en}]
	if !ok {
		return nil, false, nil
	}
	c := *st
	return &c, true, nil
}

// SaveState implements drift.StateRepository.
func (s *DriftStore) SaveState(_ context.Context, src event.SourceID, en event.EntityID, st *drift.State) error {
	s.states[entityKey{src, en}] = st
	return nil
}

// Entities reports the number of entities held, for table T5.
func (s *DriftStore) Entities() int64 { return int64(len(s.states)) }

// ---------------------------------------------------------------------------
// noveltyrate.StateRepository
// ---------------------------------------------------------------------------

// NoveltyRateStore implements noveltyrate.StateRepository.
type NoveltyRateStore struct {
	states map[entityKey]*noveltyrate.State
}

// NewNoveltyRateStore returns an empty store.
func NewNoveltyRateStore() *NoveltyRateStore {
	return &NoveltyRateStore{states: make(map[entityKey]*noveltyrate.State)}
}

// FindByEntity implements noveltyrate.StateRepository, returning a copy so that a caller
// holding the result cannot mutate stored state behind the detector's back.
func (s *NoveltyRateStore) FindByEntity(_ context.Context, src event.SourceID, en event.EntityID) (*noveltyrate.State, bool, error) {
	st, ok := s.states[entityKey{src, en}]
	if !ok {
		return nil, false, nil
	}
	c := *st
	return &c, true, nil
}

// SaveState implements noveltyrate.StateRepository.
func (s *NoveltyRateStore) SaveState(_ context.Context, src event.SourceID, en event.EntityID, st *noveltyrate.State) error {
	s.states[entityKey{src, en}] = st
	return nil
}

// Entities reports the number of entities held, for table T5.
func (s *NoveltyRateStore) Entities() int64 { return int64(len(s.states)) }

// ---------------------------------------------------------------------------
// marginal.Repository
// ---------------------------------------------------------------------------

// populationKey scopes a marginal to (source, field). No entity appears: that absence
// is Detector IV's definition (§9), and the contrast with fieldKey above is the
// per-entity/population distinction the two detectors exist to draw.
type populationKey struct {
	source event.SourceID
	field  event.FieldPath
}

// MarginalStore implements marginal.Repository.
//
// Categorical marginals reuse the valueSet layout of NoveltyStore — value rows behind
// an insertion-maintained sorted slice — for the same reason: equation (5) reads the
// whole value list once per scoreable field per event, and re-sorting on that path is
// measurable at corpus scale. Numeric marginals hold one bounded sketch per
// (source, field), which does not decay; §9 gates on its weight instead.
type MarginalStore struct {
	halfLife novelty.HalfLife
	sets     map[populationKey]*valueSet
	sketches map[populationKey]*marginal.Sketch
}

// NewMarginalStore returns an empty store decaying categorical counts at halfLife.
func NewMarginalStore(halfLife novelty.HalfLife) *MarginalStore {
	return &MarginalStore{
		halfLife: halfLife,
		sets:     make(map[populationKey]*valueSet),
		sketches: make(map[populationKey]*marginal.Sketch),
	}
}

// FindCategorical implements marginal.Repository: rows decayed to at, in ascending
// Value order (the repository contract; Postgres does it with ORDER BY).
func (s *MarginalStore) FindCategorical(_ context.Context, src event.SourceID, f event.FieldPath, at event.Timestamp) ([]marginal.ValueCount, error) {
	set := s.sets[populationKey{src, f}]
	if set == nil || len(set.sorted) == 0 {
		return nil, nil
	}
	out := make([]marginal.ValueCount, 0, len(set.sorted))
	for _, v := range set.sorted {
		r := set.rows[v]
		out = append(out, marginal.ValueCount{
			Value: v,
			Count: novelty.Decay(r.count, r.lastSeen, at, s.halfLife),
		})
	}
	return out, nil
}

// Cardinality implements marginal.Repository in constant time: the sorted slice is
// maintained on write, so its length is the distinct value count and no scan is needed.
func (s *MarginalStore) Cardinality(_ context.Context, src event.SourceID, f event.FieldPath) (int, error) {
	set := s.sets[populationKey{src, f}]
	if set == nil {
		return 0, nil
	}
	return len(set.sorted), nil
}

// FindNumeric implements marginal.Repository. The returned sketch is a copy, so a
// caller cannot mutate the store outside SaveNumeric — the same isolation a database
// round-trip provides, which is what keeps Score writeless (§5.2). at is unused
// because the sketch does not decay (§9 gates on its weight).
func (s *MarginalStore) FindNumeric(_ context.Context, src event.SourceID, f event.FieldPath, _ event.Timestamp) (*marginal.Sketch, bool, error) {
	sk, ok := s.sketches[populationKey{src, f}]
	if !ok {
		return nil, false, nil
	}
	return sk.Clone(), true, nil
}

// SaveCategorical implements marginal.Repository with the §6.2 lazy rule, following
// SaveObservation above at population scope.
func (s *MarginalStore) SaveCategorical(_ context.Context, src event.SourceID, f event.FieldPath, value string, at event.Timestamp) error {
	key := populationKey{src, f}
	set, ok := s.sets[key]
	if !ok {
		set = &valueSet{rows: make(map[string]*valueRow, 4)}
		s.sets[key] = set
	}
	r, ok := set.rows[value]
	if !ok {
		set.rows[value] = &valueRow{count: 1, firstSeen: at, lastSeen: at}
		idx, _ := slices.BinarySearch(set.sorted, value)
		set.sorted = append(set.sorted, "")
		copy(set.sorted[idx+1:], set.sorted[idx:])
		set.sorted[idx] = value
		return nil
	}
	r.count = novelty.Accumulate(r.count, r.lastSeen, at, s.halfLife)
	if at > r.lastSeen {
		r.lastSeen = at
	}
	return nil
}

// SaveNumeric implements marginal.Repository, folding one observation of unit weight
// into the field's sketch. at is unused because the sketch does not decay.
func (s *MarginalStore) SaveNumeric(_ context.Context, src event.SourceID, f event.FieldPath, x float64, _ event.Timestamp) error {
	key := populationKey{src, f}
	sk, ok := s.sketches[key]
	if !ok {
		sk = marginal.NewSketch(marginal.DefaultMaxCentroids)
		s.sketches[key] = sk
	}
	sk.Add(x, 1)
	return nil
}

// Fields reports the number of (source, field) marginals held, categorical and
// numeric together, for table T5's state measurements.
func (s *MarginalStore) Fields() int64 {
	return int64(len(s.sets) + len(s.sketches))
}

// Rows reports the categorical value rows plus sketch centroids held — the state a
// durable store would carry row for row — for table T5.
func (s *MarginalStore) Rows() int64 {
	var n int64
	for _, set := range s.sets {
		n += int64(len(set.sorted))
	}
	for _, sk := range s.sketches {
		n += int64(sk.Centroids())
	}
	return n
}

// ---------------------------------------------------------------------------
// burst.StateRepository
// ---------------------------------------------------------------------------

// BurstStore implements burst.StateRepository (#53).
type BurstStore struct {
	states map[entityKey]*burst.State
}

// NewBurstStore returns an empty store.
func NewBurstStore() *BurstStore {
	return &BurstStore{states: make(map[entityKey]*burst.State)}
}

// FindByEntity implements burst.StateRepository, returning a deep copy so that a caller
// holding the result cannot mutate stored state behind the detector's back.
//
// Deep, not shallow: burst.State holds a slice of recent arrivals and the detector appends
// the event being scored to what it is handed. A struct copy would share that array, so the
// append would write the scored event into stored state — scoring before observing in form
// while observing before scoring in fact, which is the silent failure §5.2 exists to prevent.
// The other stores here copy by assignment because their states are scalars only.
func (s *BurstStore) FindByEntity(_ context.Context, src event.SourceID, en event.EntityID) (*burst.State, bool, error) {
	st, ok := s.states[entityKey{src, en}]
	if !ok {
		return nil, false, nil
	}
	return st.Clone(), true, nil
}

// SaveState implements burst.StateRepository.
func (s *BurstStore) SaveState(_ context.Context, src event.SourceID, en event.EntityID, st *burst.State) error {
	s.states[entityKey{src, en}] = st
	return nil
}

// Report summarises the held state for the run record, including how many entities have
// cleared the abstention gate. An arm reporting no detections is a different claim from an
// arm that was never eligible to make one, and only the second is a gap in coverage.
func (s *BurstStore) Report() burst.Report {
	r := burst.Report{Entities: int64(len(s.states)), MaxWindow: burst.MaxWindow}
	rates := make([]float64, 0, len(s.states))
	for _, st := range s.states {
		r.TimestampsHeld += int64(len(st.Recent))
		if st.Eligible() {
			r.Eligible++
			rates = append(rates, st.Rate())
		}
	}
	if len(rates) > 0 {
		slices.Sort(rates)
		r.MedianRateHertz = rates[len(rates)/2]
	}
	return r
}

// Entities reports the number of entities held, for table T5.
func (s *BurstStore) Entities() int64 { return int64(len(s.states)) }
