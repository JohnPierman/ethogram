package volume

import (
	"context"
	"fmt"
	"math"

	"github.com/JohnPierman/ethogram/domain/detector"
	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/novelty"
	"github.com/JohnPierman/ethogram/domain/timing"
)

// DetectorID names the volume half of Detector II.
const DetectorID = detector.ID("volume")

// DefaultMinPeriods is the production abstention threshold: the fewest completed periods
// this detector will form an opinion on. Zero disables the gate. The value is chosen by
// the measurement recorded in results/volume-abstention-gate.json, not by taste, and is
// reported in every run's parameters block.
//
// One, and it is the R3 line rather than the best-detecting value, because the measurement
// found no value that detects. With zero completed periods the posterior IS the prior and
// there is no basis for an opinion; the detector currently expresses that as P = 1, which
// is an opinion, and R3 requires an abstention instead.
//
// Two, three and five were measured and rejected. None moves the realised cut off the
// 1e-12 floor on either corpus, because the sub-1e-12 pile is not a cold-start artefact:
// a first period scores P = 1 exactly, so the gate at one removes NONE of it, and even at
// five only 10.6% of it goes. The rest belongs to entities with established history whose
// habitual day-to-day variation the predictive of equation (11) is too narrow to tolerate.
// Adopting five to collect the four detections it happens to reach on the planted corpus
// alone, at 4.9% of events abstained and 132 labelled events withheld, would be a
// threshold chosen after seeing the result.
const DefaultMinPeriods = 1

// State is an entity's volume state: the Gamma posterior of equation (10) over
// completed periods, and the running counters for the current period and window.
// Fixed size regardless of event count.
type State struct {
	// Rate is the Gamma(a, b) posterior over the entity's events per period (day),
	// containing completed periods only.
	Rate GammaPosterior

	// PeriodIndex is the day the posterior has been folded up to (exclusive).
	PeriodIndex int64
	// PeriodCount is the running event count within PeriodIndex.
	PeriodCount int64
	// CompletedPeriods is the undiscounted number of periods that have closed and been
	// folded into Rate. It is the sample size the posterior rests on, and the quantity
	// the abstention of R3 gates on: Rate.B is the same count under the per-period
	// discount and saturates at 1/(1-delta), so it cannot express "how many periods"
	// once an entity is established, and a partial first period is the degenerate case
	// the gate exists to exclude.
	CompletedPeriods int64

	// WindowIndex is the calendar hour of the running window count.
	WindowIndex int64
	// WindowCount is the events so far in WindowIndex, excluding the event being
	// scored; §7.4's k_obs is WindowCount + 1 at scoring time.
	WindowCount int64
	// WindowExpected is the count equation (11) expected of WindowIndex, captured when
	// the window opened, so that the window's Pearson residual can be folded when it
	// closes without recomputing a posterior that has since moved on.
	WindowExpected float64

	// DispersionWindows and DispersionSum accumulate the discounted count of completed
	// windows and the discounted sum of their Pearson residuals (k − m)² / m. Their
	// ratio is the φ̂ of Dispersion, and both are fixed size regardless of event count.
	DispersionWindows float64
	DispersionSum     float64

	LastSeen event.Timestamp
}

// StateRepository persists per-entity volume state.
type StateRepository interface {
	FindByEntity(ctx context.Context, source event.SourceID, entity event.EntityID) (*State, bool, error)
	SaveState(ctx context.Context, source event.SourceID, entity event.EntityID, s *State) error
}

// Detector is the volume half of Detector II (§7.4).
//
// H₀: the count observed in the window Ω is drawn from the entity's own predictive,
// K | μ ~ Poisson(μ·ρ(Ω)) with μ ~ Gamma(a, b), the negative binomial of equation
// (11). Timing answers when; this answers how much.
//
// The window Ω is the calendar hour containing the event, and ρ(Ω) is the fraction of
// the entity's daily activity expected in that hour, integrated from the timing
// detector's fitted density (§7.4: ρ(Ω) = ∫_Ω f̂(φ) dφ). The two halves of Detector II
// therefore share state deliberately: this detector reads the timing moments, and
// §10.2's Brown correction exists precisely because such shared inputs correlate the
// detectors' statistics.
type Detector struct {
	repository   StateRepository
	timingState  timing.StateRepository
	coefficients []float64
	order        int
	halfLife     novelty.HalfLife // over event time, for the per-period discount
	// minPeriods is the fewest completed periods this detector will form an opinion
	// on. Zero disables the gate, which is the pre-#25 behaviour and is retained so
	// that the diagnostic run measuring candidate thresholds can see every p-value.
	minPeriods int64
}

// NewDetector wires the volume detector. bandwidthHours must match the timing
// detector's, so that ρ is integrated under the same kernel.
func NewDetector(repo StateRepository, timingState timing.StateRepository, bandwidthHours float64, halfLife novelty.HalfLife, minPeriods int64) *Detector {
	kappa := timing.KappaForBandwidthHours(bandwidthHours)
	order := timing.HarmonicOrder(kappa)
	return &Detector{
		repository:   repo,
		timingState:  timingState,
		coefficients: timing.KernelCoefficients(kappa, order),
		order:        order,
		halfLife:     halfLife,
		minPeriods:   minPeriods,
	}
}

// ID implements detector.Detector.
func (d *Detector) ID() detector.ID { return DetectorID }

// NullHypothesis implements detector.Detector, stating H₀ as §5.2 requires.
func (d *Detector) NullHypothesis() string {
	return "the count observed in the window is drawn from the entity's negative " +
		"binomial predictive, per §7.4 equations (10) and (11)"
}

// Score evaluates the running count in the event's calendar hour against the entity's
// own predictive. Cold start gives P = 1 exactly through UpperTail's degenerate
// handling: with no completed periods the posterior is empty and no count is
// anomalous, matching §6.2 and §7.5.
func (d *Detector) Score(ctx context.Context, e *event.Event) (detector.Verdicts, detector.Observation, error) {
	state, ok, err := d.repository.FindByEntity(ctx, e.Source(), e.Entity())
	if err != nil {
		return nil, nil, fmt.Errorf("volume: find state: %w", err)
	}
	if !ok {
		state = &State{}
	}

	day := int64(e.OccurredAt() / event.Day)
	hour := int64(e.OccurredAt() / event.Hour)

	// The effective posterior at scoring time folds every completed period up to the
	// event's day, computed functionally: scoring must not mutate state (§5.2), so
	// the same fold is applied again, persistently, on commit.
	a, b := effectivePosterior(state, day, d.periodDelta())
	completed := effectiveCompletedPeriods(state, day)

	kObs := int64(1)
	if state.WindowIndex == hour {
		kObs = state.WindowCount + 1
	}

	rho := d.windowFraction(ctx, e, hour)

	// The null is equation (11) widened by the dispersion the entity's own completed
	// windows exhibit. Where the entity's counts scatter no more than the model predicts
	// this is (11) exactly; where the dispersion cannot be measured at all the arm
	// abstains below rather than falling back to (11), which would be the narrowest null
	// it has.
	mean, variance := PredictiveMoments(a, b, rho)
	phi := Dispersion(state.DispersionSum, state.DispersionWindows)
	p := UpperTail(a, b, rho, int(kObs))
	if phi > 1 && mean > 0 {
		p = UpperTailDispersed(mean, phi, int(kObs))
		variance = mean * phi
	}

	stats := map[string]float64{
		"a":                  a,
		"b":                  b,
		"rho":                rho,
		"k_obs":              float64(kObs),
		"rate_mean":          GammaPosterior{A: a, B: b}.Mean(),
		"expected_count":     mean,
		"variance":           variance,
		"dispersion":         phi,
		"dispersion_windows": state.DispersionWindows,
		"completed_periods":  float64(completed),
		"min_periods":        float64(d.minPeriods),
		"half_life_us":       float64(d.halfLife),
	}
	labels := map[string]string{
		"window": fmt.Sprintf("hour %02d:00-%02d:00", (hour % 24), (hour%24 + 1)),
	}

	target := detector.Target{Event: e.ID(), Entity: e.Entity()}

	// Built before the gate, and returned by both paths: abstaining is a statement about
	// this event, not a decision to stop learning. An entity that never accrues state
	// never leaves the gate, so withholding the observation here would make the
	// abstention permanent instead of provisional.
	obs := &observation{
		repository:  d.repository,
		source:      e.Source(),
		entity:      e.Entity(),
		at:          e.OccurredAt(),
		day:         day,
		hour:        hour,
		eventID:     e.ID(),
		periodDelta: d.periodDelta(),
		expected:    mean,
		halfLife:    d.halfLife,
	}

	// R3: with fewer completed periods than the posterior needs, there is no basis for
	// an opinion and the detector says so, rather than reporting the prior's tail as if
	// it were the entity's. Equation (11) on a partial first period is exactly that.
	if d.minPeriods > 0 && completed < d.minPeriods {
		v, abstainErr := detector.NewAbstained(DetectorID, target,
			detector.StatusAbstainedUnusable,
			"too few completed periods to estimate this entity's rate",
			detector.NewEvidence([]int{10, 11}, map[string]float64{
				"completed_periods": float64(completed),
				"minimum":           float64(d.minPeriods),
			}, labels))
		if abstainErr != nil {
			return nil, nil, fmt.Errorf("volume: abstain: %w", abstainErr)
		}
		return detector.Verdicts{v}, obs, nil
	}

	// R3: the dispersion is the WIDTH of the null, and an unmeasurable width is not a
	// narrow one. Falling back to equation (11) un-widened asserted the narrowest null the
	// arm has for exactly the entities whose variation it had no evidence about, and a
	// wholly benign account bursting every four days then put 31.6% of its own events below
	// 1e-12, reaching 1e-45. See [DispersionReachable] for why observation alone could not
	// fix it: past about three days between active windows the discounted weight saturates
	// below [MinDispersionWindows], so the estimate was unreachable however long the entity
	// was watched.
	//
	// The gate is deliberately narrow. It fires only where the arm would otherwise report a
	// real tail -- mean > 0, so the posterior and the window fraction both exist -- because
	// the no-history state already reports P = 1 by the convention of [UpperTail], and that
	// costs nothing: a p-value of 1 cannot win an alert slot. Widening the gate to cover
	// cold start would change what the volume-gate probe of section 6.2 can measure from a
	// single ungated pass, for no gain.
	if mean > 0 && !DispersionMeasurable(state.DispersionWindows) {
		v, abstainErr := detector.NewAbstained(DetectorID, target,
			detector.StatusAbstainedUnusable,
			"too few completed windows to measure this entity's dispersion, so the "+
				"width of its null is unknown",
			detector.NewEvidence([]int{10, 11}, stats, labels))
		if abstainErr != nil {
			return nil, nil, fmt.Errorf("volume: abstain: %w", abstainErr)
		}
		return detector.Verdicts{v}, obs, nil
	}

	verdict, err := detector.NewEvaluated(DetectorID, target, p,
		detector.NewEvidence([]int{10, 11}, stats, labels))
	if err != nil {
		return nil, nil, fmt.Errorf("volume: verdict: %w", err)
	}
	return detector.Verdicts{verdict}, obs, nil
}

// periodDelta is the per-period discount δ = 2^(−period/T½), the equation (10) form
// of the shared power discounting.
func (d *Detector) periodDelta() float64 {
	if d.halfLife <= 0 {
		return 1
	}
	return math.Exp2(-float64(event.Day) / float64(d.halfLife))
}

// windowFraction integrates the timing density over the event's calendar hour, in
// closed form from the moments:
//
//	∫_α^β f̂ = (β−α)/2π + (1/π) Σ_h (r_h/h) [ (C_h/W)(sin hβ − sin hα)
//	                                        − (S_h/W)(cos hβ − cos hα) ]
//
// The closed form integrates the unclamped series, which can go slightly negative
// where the truncated density would clamp; the result is clamped to [0, 1]. With no
// timing state the density is uniform and ρ = 1/24 for an hour window.
func (d *Detector) windowFraction(ctx context.Context, e *event.Event, hour int64) float64 {
	const uniformHour = 1.0 / 24.0

	ts, ok, err := d.timingState.FindByEntity(ctx, e.Source(), e.Entity())
	if err != nil || !ok || ts.Moments.W <= 0 {
		return uniformHour
	}
	m := ts.Moments

	alpha := 2 * math.Pi * float64(hour%24) / 24
	beta := alpha + 2*math.Pi/24

	sum := 0.0
	order := d.order
	if order > m.H() {
		order = m.H()
	}
	for h := 1; h <= order; h++ {
		fh := float64(h)
		sum += d.coefficients[h-1] / fh *
			((m.C[h-1]/m.W)*(math.Sin(fh*beta)-math.Sin(fh*alpha)) -
				(m.S[h-1]/m.W)*(math.Cos(fh*beta)-math.Cos(fh*alpha)))
	}
	rho := (beta-alpha)/(2*math.Pi) + sum/math.Pi
	if rho < 0 {
		return 0
	}
	if rho > 1 {
		return 1
	}
	return rho
}

// effectivePosterior folds completed periods into (a, b) up to day, without mutating
// the stored state. Each completed period contributes a ← δ·a + k, b ← δ·b + 1, the
// pending period's running count first and empty periods with k = 0, which §7.4's
// per-period form of the shared discounting prescribes.
func effectivePosterior(s *State, day int64, delta float64) (a, b float64) {
	a, b = s.Rate.A, s.Rate.B
	if s.LastSeen == 0 && s.PeriodCount == 0 && a == 0 && b == 0 {
		return a, b // never observed
	}
	if day <= s.PeriodIndex {
		return a, b
	}
	// Fold the stored running period, then the empty periods between.
	a = delta*a + float64(s.PeriodCount)
	b = delta*b + 1
	for p := s.PeriodIndex + 1; p < day; p++ {
		a = delta * a
		b = delta*b + 1
	}
	return a, b
}

// effectiveCompletedPeriods reports how many periods have closed as of day. It mirrors
// effectivePosterior's fold exactly — one period for the stored running period, then one
// for each empty period between — and like it does not mutate state, because scoring must
// not (§5.2). The same increment is applied persistently on commit.
func effectiveCompletedPeriods(s *State, day int64) int64 {
	if s.LastSeen == 0 && s.PeriodCount == 0 && s.Rate.A == 0 && s.Rate.B == 0 {
		return 0 // never observed
	}
	if day <= s.PeriodIndex {
		return s.CompletedPeriods
	}
	return s.CompletedPeriods + (day - s.PeriodIndex)
}

// observation applies the counter and posterior updates, strictly after scoring.
type observation struct {
	repository  StateRepository
	source      event.SourceID
	entity      event.EntityID
	at          event.Timestamp
	day         int64
	hour        int64
	eventID     event.ID
	periodDelta float64
	// expected is the count equation (11) expected of this event's window, carried so
	// that a window opening here records the expectation it will later be judged against.
	expected  float64
	halfLife  novelty.HalfLife
	committed bool
}

// windowDelta is the discount applied to the dispersion accumulators across n elapsed
// windows, the same power discounting as (10) at the window timescale.
func (o *observation) windowDelta(windows int64) float64 {
	if o.halfLife <= 0 || windows <= 0 {
		return 1
	}
	return math.Exp2(-float64(windows) * float64(event.Hour) / float64(o.halfLife))
}

func (o *observation) EventID() event.ID       { return o.eventID }
func (o *observation) DetectorID() detector.ID { return DetectorID }

// Commit folds completed periods into the posterior and advances the counters.
// Idempotent per observation.
func (o *observation) Commit(ctx context.Context) error {
	if o.committed {
		return nil
	}
	state, ok, err := o.repository.FindByEntity(ctx, o.source, o.entity)
	if err != nil {
		return fmt.Errorf("volume: find state for commit: %w", err)
	}
	if !ok {
		state = &State{PeriodIndex: o.day, WindowIndex: o.hour, WindowExpected: o.expected}
	}

	if o.day > state.PeriodIndex {
		a, b := effectivePosterior(state, o.day, o.periodDelta)
		state.Rate = GammaPosterior{A: a, B: b}
		state.CompletedPeriods += o.day - state.PeriodIndex
		state.PeriodIndex = o.day
		state.PeriodCount = 0
	}
	state.PeriodCount++

	if o.hour != state.WindowIndex {
		// The window just closed. Fold its Pearson residual against the expectation
		// recorded when it opened, then open the new one. A window with no recorded
		// expectation cannot contribute, and neither can one expected to be empty.
		if state.WindowCount > 0 && state.WindowExpected > 0 {
			delta := o.windowDelta(o.hour - state.WindowIndex)
			residual := float64(state.WindowCount) - state.WindowExpected
			state.DispersionSum = delta*state.DispersionSum + residual*residual/state.WindowExpected
			state.DispersionWindows = delta*state.DispersionWindows + 1
		}
		state.WindowIndex = o.hour
		state.WindowCount = 0
		state.WindowExpected = o.expected
	}
	state.WindowCount++

	if o.at > state.LastSeen {
		state.LastSeen = o.at
	}
	if err := o.repository.SaveState(ctx, o.source, o.entity, state); err != nil {
		return fmt.Errorf("volume: save state: %w", err)
	}
	o.committed = true
	return nil
}
