package drift

import (
	"context"
	"fmt"
	"math"

	"github.com/JohnPierman/ethogram/domain/detector"
	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/novelty"
	"github.com/JohnPierman/ethogram/domain/volume"
)

// DetectorID names the sequential-change arm.
const DetectorID = detector.ID("drift")

// DefaultMinPeriods is the fewest closed periods this arm will form an opinion on.
//
// It matches [MinWeight] because the two gates are the same requirement seen from two sides:
// the null is one observation per closed period, so fewer closed periods than MinWeight
// cannot produce a null to standardise against. Stating both keeps the abstention legible in
// a result file, where "too few periods" and "no spread" are different diagnoses.
//
// It interacts with the length of the burn-in window, and on this corpus the interaction
// costs a day: a seven-day burn-in leaves an entity with seven closed periods at the
// boundary, so the arm's first opinion falls on the second scored day and the first is a
// column of abstentions. Lowering the gate to seven would recover that day and would be
// fitting a parameter to the length of one corpus's burn-in, so it is reported instead.
const DefaultMinPeriods = MinWeight

// NullDiscount is the discount applied to the null over the cumulative sum: none.
//
// The two estimators this arm carries are discounted differently, on purpose. The baseline
// rate keeps the framework's per-period discount, because an account whose workload has
// genuinely changed should be measured against its current level. The null over S does not,
// because it is the reference distribution a change is judged against and a discounted one
// tracks the change: an attack sustained across the window raises its own null and scores
// itself ordinary. That is section 6.2's "a sustained intrusion enlarges its own reference
// set" in its acute form, and here it is avoidable.
//
// Measured: on synthetic streams fitted on identical stationary history, the undiscounted null
// separates a sustained +30% shift from matched stationary variation by 237x where the
// discounted one separates it by 2x. TestDetectorScoresASustainedShiftBelowAStationaryStream
// pins that, so reintroducing the discount fails a test rather than quietly costing detections.
//
// The cost is the opposite error: an account whose level shifted legitimately a year ago keeps
// the null from before the shift. The baseline being discounted absorbs most of that, since a
// higher baseline raises the reference value and floors the sum again.
const NullDiscount = 1.0

// State is an entity's drift state: a baseline rate over closed periods, the cumulative sum
// as of the last close, and the null over the sums the entity's own closed periods produced.
//
// Fixed size regardless of event count — five floats and three integers — which is the same
// property equation (10)'s posterior has and the reason this arm costs nothing to carry.
type State struct {
	// Rate is the Gamma posterior over the entity's events per period, containing closed
	// periods only. Its own rather than the volume arm's: the baseline a change statistic
	// needs is the level *before* the change, and sharing an estimator with an arm that
	// updates on every event would let the drift being tested raise its own baseline.
	Rate volume.GammaPosterior

	// Cusum is S as of the last closed period.
	Cusum float64
	// Null holds the moments of the S values the entity's closed periods produced.
	Null Null

	// PeriodIndex is the day Cusum and Rate have been folded up to.
	PeriodIndex int64
	// PeriodCount is the running event count within PeriodIndex.
	PeriodCount int64
	// CompletedPeriods is the undiscounted number of periods that have closed. Null.W is
	// the same count under the discount and saturates at 1/(1-delta), so it cannot express
	// "how many periods" once an entity is established.
	CompletedPeriods int64

	LastSeen event.Timestamp
}

// StateRepository persists per-entity drift state.
type StateRepository interface {
	FindByEntity(ctx context.Context, source event.SourceID, entity event.EntityID) (*State, bool, error)
	SaveState(ctx context.Context, source event.SourceID, entity event.EntityID, s *State) error
}

// Detector is the sequential-change arm.
//
// H₀: the entity's event rate has not shifted upward, so its cumulative sum of per-period
// excesses is drawn from the distribution its own closed periods have produced.
//
// It answers a different question from the volume arm rather than a better version of the
// same one. Equation (11) asks whether *this period* is surprising under a null widened by
// the entity's habitual variation; this asks whether the excesses are *accumulating*. A
// modest sustained shift is inside the first null in every period and outside the second
// after enough of them, which is why the two are complements and both are retained.
type Detector struct {
	repository StateRepository
	halfLife   novelty.HalfLife
	shift      float64
	minPeriods int64
}

// NewDetector wires the drift detector. shift is the multiplicative change the reference
// value is tuned for, and is a stated parameter: see [DefaultShift].
func NewDetector(repo StateRepository, halfLife novelty.HalfLife, shift float64, minPeriods int64) (*Detector, error) {
	if _, err := Reference(1, shift); err != nil {
		return nil, err
	}
	if minPeriods < 0 {
		return nil, fmt.Errorf("%w: minimum periods %d is negative", ErrShift, minPeriods)
	}
	return &Detector{
		repository: repo,
		halfLife:   halfLife,
		shift:      shift,
		minPeriods: minPeriods,
	}, nil
}

// ID implements detector.Detector.
func (d *Detector) ID() detector.ID { return DetectorID }

// NullHypothesis implements detector.Detector, stating H₀ as §5.2 requires.
func (d *Detector) NullHypothesis() string {
	return "the entity's rate has not shifted upward: its cumulative sum of per-period " +
		"excesses is drawn from the distribution its own closed periods produced"
}

// Score evaluates the entity's accumulated excess against its own null.
//
// The running period is charged the whole reference value while contributing only the events
// seen so far, which makes the scored statistic commensurable with the null: at the close of
// the period it is exactly the value the null is built from, and before then it is below it.
// The arm therefore grows more confident as a period fills rather than starting a day
// confident and decaying, and it still responds within the day, monotonically in the count.
//
// Charging the reference in proportion to elapsed time instead would be the textbook handling
// of unequal exposure, and it is wrong here: every scored event is conditioned on its own
// existence, so at small exposure the count is at least one where the reference is nearly
// zero, and the statistic is inflated for every entity rather than for a drifting one.
func (d *Detector) Score(ctx context.Context, e *event.Event) (detector.Verdicts, detector.Observation, error) {
	state, ok, err := d.repository.FindByEntity(ctx, e.Source(), e.Entity())
	if err != nil {
		return nil, nil, fmt.Errorf("drift: find state: %w", err)
	}
	if !ok {
		state = &State{}
	}

	day := int64(e.OccurredAt() / event.Day)
	delta := periodDelta(d.halfLife)

	a, b := effectiveRate(state, day, delta)
	completed := effectiveCompletedPeriods(state, day)
	baseline := volume.GammaPosterior{A: a, B: b}.Mean()

	target := detector.Target{Event: e.ID(), Entity: e.Entity()}
	obs := &observation{
		repository: d.repository,
		source:     e.Source(),
		entity:     e.Entity(),
		at:         e.OccurredAt(),
		day:        day,
		eventID:    e.ID(),
		delta:      delta,
		shift:      d.shift,
	}

	labels := map[string]string{"period": fmt.Sprintf("day %d", day)}

	// R3, first gate: with no baseline there is no reference value, so there is no
	// statistic. This is the cold start and it is an abstention, not a p-value of one.
	if baseline <= 0 || completed < d.minPeriods {
		v, abErr := detector.NewAbstained(DetectorID, target,
			detector.StatusAbstainedUnusable,
			"too few closed periods to estimate this entity's baseline rate",
			detector.NewEvidence([]int{10}, map[string]float64{
				"completed_periods": float64(completed),
				"minimum":           float64(d.minPeriods),
				"baseline_rate":     baseline,
			}, labels))
		if abErr != nil {
			return nil, nil, fmt.Errorf("drift: abstain: %w", abErr)
		}
		return detector.Verdicts{v}, obs, nil
	}

	reference, err := Reference(baseline, d.shift)
	if err != nil {
		return nil, nil, fmt.Errorf("drift: reference: %w", err)
	}

	sum, null := effectiveCusum(state, day, reference)
	pending := int64(1)
	if state.PeriodIndex == day {
		pending = state.PeriodCount + 1
	}
	current := Next(sum, float64(pending), reference)

	z, usable := null.Standardise(current)
	stats := map[string]float64{
		"baseline_rate":       baseline,
		"reference":           reference,
		"shift":               d.shift,
		"cusum":               current,
		"cusum_at_last_close": sum,
		"pending_count":       float64(pending),
		"null_mean":           null.Sum / max1(null.W),
		"null_weight":         null.W,
		"completed_periods":   float64(completed),
		"half_life_us":        float64(d.halfLife),
	}

	// R3, second gate: the null exists in principle but carries no spread — a perfectly
	// regular account produces the same sum every period — so there is nothing to
	// standardise against and no opinion to be had on this scale.
	if !usable {
		v, abErr := detector.NewAbstained(DetectorID, target,
			detector.StatusAbstainedUnusable,
			"this entity's own cumulative sums carry no spread to standardise against",
			detector.NewEvidence([]int{10}, stats, labels))
		if abErr != nil {
			return nil, nil, fmt.Errorf("drift: abstain: %w", abErr)
		}
		return detector.Verdicts{v}, obs, nil
	}

	stats["z"] = z
	verdict, err := detector.NewEvaluated(DetectorID, target, UpperTail(z),
		detector.NewEvidence([]int{10}, stats, labels))
	if err != nil {
		return nil, nil, fmt.Errorf("drift: verdict: %w", err)
	}
	return detector.Verdicts{verdict}, obs, nil
}

// periodDelta is the per-period discount δ = 2^(−period/T½), the equation (10) form of the
// shared power discounting.
func periodDelta(halfLife novelty.HalfLife) float64 {
	if halfLife <= 0 {
		return 1
	}
	return math.Exp2(-float64(event.Day) / float64(halfLife))
}

// effectiveRate folds every period closed since the state was written, without mutating it:
// scoring must not (§5.2), and the same fold is applied persistently on commit. It mirrors
// the volume arm's fold exactly — the stored running period first, then the empty periods
// between, which contribute no events and one period of exposure each.
func effectiveRate(s *State, day int64, delta float64) (a, b float64) {
	a, b = s.Rate.A, s.Rate.B
	if s.LastSeen == 0 && s.PeriodCount == 0 && a == 0 && b == 0 {
		return a, b
	}
	if day <= s.PeriodIndex {
		return a, b
	}
	a = delta*a + float64(s.PeriodCount)
	b = delta*b + 1
	for p := s.PeriodIndex + 1; p < day; p++ {
		a = delta * a
		b = delta*b + 1
	}
	return a, b
}

// effectiveCusum folds the same closed periods into the cumulative sum and its null.
//
// The reference value used for the fold is the one the posterior implies at scoring time
// rather than the one each period implied when it closed. That is the same approximation the
// volume arm's fold makes about the rate, and it is bounded by the discount: a period old
// enough for the reference to have moved far is a period the discount has already shrunk.
func effectiveCusum(s *State, day int64, reference float64) (float64, Null) {
	sum, null := s.Cusum, s.Null
	if day <= s.PeriodIndex {
		return sum, null
	}
	sum = Next(sum, float64(s.PeriodCount), reference)
	null.Observe(sum, NullDiscount)
	for p := s.PeriodIndex + 1; p < day; p++ {
		sum = Next(sum, 0, reference)
		null.Observe(sum, NullDiscount)
	}
	return sum, null
}

// effectiveCompletedPeriods reports how many periods have closed as of day, mirroring
// effectiveRate's fold and, like it, without mutating state.
func effectiveCompletedPeriods(s *State, day int64) int64 {
	if s.LastSeen == 0 && s.PeriodCount == 0 && s.Rate.A == 0 && s.Rate.B == 0 {
		return 0
	}
	if day <= s.PeriodIndex {
		return s.CompletedPeriods
	}
	return s.CompletedPeriods + (day - s.PeriodIndex)
}

func max1(w float64) float64 {
	if w <= 0 {
		return 1
	}
	return w
}

// observation applies the period fold and the counter update, strictly after scoring.
type observation struct {
	repository StateRepository
	source     event.SourceID
	entity     event.EntityID
	at         event.Timestamp
	day        int64
	eventID    event.ID
	delta      float64
	shift      float64
	committed  bool
}

func (o *observation) EventID() event.ID       { return o.eventID }
func (o *observation) DetectorID() detector.ID { return DetectorID }

// Commit closes every period that has elapsed and advances the running count. Idempotent
// per observation.
func (o *observation) Commit(ctx context.Context) error {
	if o.committed {
		return nil
	}
	state, ok, err := o.repository.FindByEntity(ctx, o.source, o.entity)
	if err != nil {
		return fmt.Errorf("drift: find state for commit: %w", err)
	}
	if !ok {
		state = &State{PeriodIndex: o.day}
	}

	if o.day > state.PeriodIndex {
		// The reference the fold uses is the one the baseline implied *before* these
		// periods closed, which is the level a change is being measured against.
		baseline := volume.GammaPosterior{A: state.Rate.A, B: state.Rate.B}.Mean()
		if baseline > 0 {
			if reference, refErr := Reference(baseline, o.shift); refErr == nil {
				state.Cusum = Next(state.Cusum, float64(state.PeriodCount), reference)
				state.Null.Observe(state.Cusum, NullDiscount)
				for p := state.PeriodIndex + 1; p < o.day; p++ {
					state.Cusum = Next(state.Cusum, 0, reference)
					state.Null.Observe(state.Cusum, NullDiscount)
				}
			}
		}
		a, b := effectiveRate(state, o.day, o.delta)
		state.Rate = volume.GammaPosterior{A: a, B: b}
		state.CompletedPeriods += o.day - state.PeriodIndex
		state.PeriodIndex = o.day
		state.PeriodCount = 0
	}
	state.PeriodCount++

	if o.at > state.LastSeen {
		state.LastSeen = o.at
	}
	if err := o.repository.SaveState(ctx, o.source, o.entity, state); err != nil {
		return fmt.Errorf("drift: save state: %w", err)
	}
	o.committed = true
	return nil
}
