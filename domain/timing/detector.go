package timing

import (
	"context"
	"fmt"
	"math"
	"sync"

	"github.com/JohnPierman/ethogram/domain/detector"
	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/novelty"
)

// DetectorID names the timing half of Detector II.
const DetectorID = detector.ID("timing")

// State is an entity's circular-timing state: the moments of equation (6) and the
// entity's last-observed timestamp, from which the next observation's discount is
// computed. Fixed size, 2H + 1 floats plus one timestamp, regardless of event count.
type State struct {
	Moments  *Moments
	LastSeen event.Timestamp

	// LogUSum and LogUSumSq accumulate ln U over the entity's own scored events, under
	// the same discount as the moments, so that U can be standardised against what this
	// entity's clock actually produces rather than against what an order-H density says
	// it should.
	//
	// Two numbers, so the state stays fixed size (§13.3). The mean and variance they
	// give are the whole per-entity null: #26 asks for a rank against the entity's own
	// historical density values, and retaining those values would make the state grow
	// without bound.
	//
	// Why this is not a no-op. U = P(f(Φ) ≤ f(φ)) is exactly uniform under a CORRECTLY
	// specified f, and a monotone transform of a uniform adds nothing. f here is a
	// truncated order-H Fourier density with a zero clamp and a decayed kernel, so the
	// realised U is not uniform, and the departure is per entity: an account that
	// habitually works odd hours has its oddness smoothed away by the truncation and
	// scores low U routinely. Standardising separates that account from one for which the
	// same U is genuinely a first.
	LogUSum   float64
	LogUSumSq float64
}

// DefaultStandardise selects the production timing statistic. False keeps the level-set
// mass of equation (9); true selects the per-entity standardised form of #26. Flipped only
// by a recorded run, and reported in every run's parameters block.
//
// True, on the measurement. The mass is floored at 1/(2G) = 9.77e-04, which sat at or above
// the arm's own realised alert cut at 10 and 100 alerts a day, so the detector could not
// alert there whatever it observed. Standardising ln U against the entity's own realised
// spread removes the floor and turns that artefact into a measurement: detections go from
// 0/0/6 to 1/2/7 on the real campaign and from 0/1/12 to 2/9/21 on the planted corpus, at
// 10/100/1000 alerts a day. The off-hours separation the arm was built for is not traded
// away for it -- it improves, from 5.7x to 18.6x against the nearest other planted
// mechanism. Every other arm is unchanged on both corpora.
const DefaultStandardise = true

// MinStandardiseWeight is the least discounted observation weight at which the
// standardised statistic is formed. Below it the entity has too few of its own U values
// for a mean and a spread to mean anything, and the detector abstains rather than
// standardising against noise -- the abstention question #26 anticipates.
const MinStandardiseWeight = 20

// standardise reports the per-entity mean and standard deviation of ln U, and whether
// they rest on enough weight to use.
func (s *State) standardise() (mean, sd float64, ok bool) {
	w := s.Moments.W
	if w < MinStandardiseWeight {
		return 0, 0, false
	}
	mean = s.LogUSum / w
	variance := s.LogUSumSq/w - mean*mean
	if variance <= 0 {
		// A perfectly regular account -- every event inside one narrow window -- receives
		// essentially the same ln U every time, so there is no spread to standardise
		// against and no opinion to be had on this scale. Reported as an abstention rather
		// than fixed up with an arbitrary floor on the spread, which would manufacture
		// extreme scores out of a rounding difference.
		return mean, 0, false
	}
	return mean, math.Sqrt(variance), true
}

// StateRepository persists per-entity circular state.
type StateRepository interface {
	// FindByEntity returns the entity's state, or ok = false when the entity has
	// none, which is cold start rather than an error (§7.5).
	FindByEntity(ctx context.Context, source event.SourceID, entity event.EntityID) (*State, bool, error)

	// SaveState persists the updated state.
	SaveState(ctx context.Context, source event.SourceID, entity event.EntityID, s *State) error
}

// Detector is the timing half of Detector II (§7.2).
//
// H₀: the event's time of day φ is drawn from the entity's historical circular
// density, estimated by von Mises kernel density estimation over decayed
// trigonometric moments.
type Detector struct {
	repository   StateRepository
	coefficients []float64
	kappa        float64
	bandwidthH   float64
	order        int
	halfLife     novelty.HalfLife
	grid         *gridTables
	// standardise selects the statistic of #26: U standardised against the entity's own
	// realised ln U rather than reported as a raw level-set mass. Off by default until a
	// recorded run justifies the flip, and recorded in the result either way.
	standardise bool
}

// NewDetector wires the timing detector. bandwidthHours is the operator-facing
// smoothing parameter of equation (8); κ and H are derived from it.
func NewDetector(repo StateRepository, bandwidthHours float64, halfLife novelty.HalfLife, standardise bool) *Detector {
	kappa := KappaForBandwidthHours(bandwidthHours)
	order := HarmonicOrder(kappa)
	return &Detector{
		repository:   repo,
		coefficients: KernelCoefficients(kappa, order),
		kappa:        kappa,
		bandwidthH:   bandwidthHours,
		order:        order,
		halfLife:     halfLife,
		grid:         newGridTables(order, GridSize),
		standardise:  standardise,
	}
}

// ID implements detector.Detector.
func (d *Detector) ID() detector.ID { return DetectorID }

// NullHypothesis implements detector.Detector, stating H₀ as §5.2 requires.
func (d *Detector) NullHypothesis() string {
	return "the event's time of day is drawn from the entity's historical circular " +
		"density, estimated per §7.2 equations (6), (7) and (9)"
}

// Score evaluates the event's time of day against the entity's own fitted density.
//
// Cold start needs no special handling (§7.5): with no observations the moments are
// zero, (7) reduces to the uniform density, and (9) returns P = 1 for every time — an
// evaluated verdict, not an abstention, because the model has an answer: nothing is
// unusual yet.
//
// The density read here needs no read-time decay: discounting every moment and W by
// the same factor leaves every C_h/W and S_h/W ratio unchanged, so the fitted shape is
// invariant and only the effective weight, which the evidence reports, would move.
func (d *Detector) Score(ctx context.Context, e *event.Event) (detector.Verdicts, detector.Observation, error) {
	state, ok, err := d.repository.FindByEntity(ctx, e.Source(), e.Entity())
	if err != nil {
		return nil, nil, fmt.Errorf("timing: find state: %w", err)
	}
	if !ok {
		state = &State{Moments: NewMoments(d.order)}
	}

	phi := PhaseOfTimestamp(e.OccurredAt())
	density := NewDensity(state.Moments, d.coefficients)
	level := density.Evaluate(phi)
	tail, modes := d.grid.TailMass(density, level)

	// ln U is what the per-entity null is kept over. U is bounded below by the grid, so
	// this is bounded too; the standardised form below is not, which is the point of #26.
	logU := math.Log(tail)
	mean, sd, haveNull := state.standardise()

	// Computed whenever the null exists, so that a run measures the statistic it did not
	// select. REPORTED only when selected: gating the report on haveNull alone would
	// change every timing figure in every run that never asked for #26's statistic.
	var z, standardisedP float64
	if haveNull {
		z = (logU - mean) / sd
		// Lower tail of the standard normal. Small U sits far below the entity's own mean
		// ln U, so z is very negative and this is unbounded below, where a level-set mass
		// on a G-point grid floors at 1/(2G).
		standardisedP = 0.5 * math.Erfc(-z/math.Sqrt2)
		if standardisedP <= 0 {
			standardisedP = math.SmallestNonzeroFloat64
		}
		if standardisedP > 1 {
			standardisedP = 1
		}
	}
	reported := tail
	if d.standardise && haveNull {
		reported = standardisedP
	}

	stats := map[string]float64{
		"phi":             phi,
		"density_at_phi":  level,
		"kappa":           d.kappa,
		"bandwidth_hours": d.bandwidthH,
		"H":               float64(d.order),
		"W":               state.Moments.W,
		"grid":            float64(GridSize),
		"grid_floor":      1 / (2 * float64(GridSize)),
		"half_life_us":    float64(d.halfLife),
		// Both statistics on every verdict, whichever is reported: it is what lets one
		// pass measure the alternative instead of costing a second run (#26).
		"tail_mass":     tail,
		"log_u":         logU,
		"standardised":  boolAsFloat(d.standardise),
		"log_u_null_ok": boolAsFloat(haveNull),
	}
	if haveNull {
		stats["log_u_mean"] = mean
		stats["log_u_sd"] = sd
		stats["log_u_z"] = z
		stats["standardised_p"] = standardisedP
	}
	// The moments themselves are the sufficient statistics (R5): an analyst holding
	// them and κ can recompute (7) and (9) by hand.
	for h := 1; h <= d.order; h++ {
		stats[fmt.Sprintf("c_%02d", h)] = state.Moments.C[h-1]
		stats[fmt.Sprintf("s_%02d", h)] = state.Moments.S[h-1]
	}
	labels := map[string]string{
		"observed_clock": clockOf(phi),
		"modes":          modesAsClock(modes),
	}

	target := detector.Target{Event: e.ID(), Entity: e.Entity()}

	obs := &observation{
		repository: d.repository,
		source:     e.Source(),
		entity:     e.Entity(),
		at:         e.OccurredAt(),
		phi:        phi,
		eventID:    e.ID(),
		halfLife:   d.halfLife,
		order:      d.order,
		logU:       logU,
	}

	// Mixing the two statistics inside one arm would put two different nulls in one
	// ranked queue, so where the standardised form is selected and not yet estimable the
	// detector abstains instead of falling back. The observation is still returned: the
	// entity must keep accruing ln U or it never crosses the threshold.
	if d.standardise && !haveNull {
		v, abstainErr := detector.NewAbstained(DetectorID, target,
			detector.StatusAbstainedUnusable,
			"too little of this entity's own timing history to standardise against",
			detector.NewEvidence([]int{6, 7, 8, 9}, map[string]float64{
				"W":       state.Moments.W,
				"minimum": MinStandardiseWeight,
			}, labels))
		if abstainErr != nil {
			return nil, nil, fmt.Errorf("timing: abstain: %w", abstainErr)
		}
		return detector.Verdicts{v}, obs, nil
	}

	verdict, err := detector.NewEvaluated(DetectorID, target, reported,
		detector.NewEvidence([]int{6, 7, 8, 9}, stats, labels))
	if err != nil {
		return nil, nil, fmt.Errorf("timing: verdict: %w", err)
	}
	return detector.Verdicts{verdict}, obs, nil
}

// clockOf renders an angle as HH:MM, for the §7.7 evidence view.
func clockOf(phi float64) string {
	hours := phi * 24 / (2 * math.Pi)
	h := int(hours)
	m := int((hours - float64(h)) * 60)
	return fmt.Sprintf("%02d:%02d", h%24, m)
}

// modesAsClock renders up to three local maxima as clock times.
func modesAsClock(maxima []float64) string {
	if len(maxima) > 3 {
		maxima = maxima[:3]
	}
	out := ""
	for i, m := range maxima {
		if i > 0 {
			out += " "
		}
		out += clockOf(m)
	}
	return out
}

// observation carries the (6) update, applied strictly after scoring (§5.2).
type observation struct {
	repository StateRepository
	source     event.SourceID
	entity     event.EntityID
	at         event.Timestamp
	phi        float64
	eventID    event.ID
	halfLife   novelty.HalfLife
	order      int
	// logU is the ln U this event was scored at, folded into the per-entity null on
	// commit so that the null describes what this entity's clock actually produces.
	logU      float64
	committed bool
}

func (o *observation) EventID() event.ID       { return o.eventID }
func (o *observation) DetectorID() detector.ID { return DetectorID }

// Commit folds the event into the moments with δ = 2^(−Δt/T½) from the entity's
// previous observation. Idempotent per observation.
func (o *observation) Commit(ctx context.Context) error {
	if o.committed {
		return nil
	}
	state, ok, err := o.repository.FindByEntity(ctx, o.source, o.entity)
	if err != nil {
		return fmt.Errorf("timing: find state for commit: %w", err)
	}
	if !ok {
		state = &State{Moments: NewMoments(o.order)}
	}
	delta := 1.0
	if state.Moments.W > 0 {
		delta = novelty.DecayFactor(state.LastSeen, o.at, o.halfLife)
	}
	state.Moments.Observe(o.phi, delta)
	state.LogUSum = delta*state.LogUSum + o.logU
	state.LogUSumSq = delta*state.LogUSumSq + o.logU*o.logU
	if o.at > state.LastSeen {
		state.LastSeen = o.at
	}
	if err := o.repository.SaveState(ctx, o.source, o.entity, state); err != nil {
		return fmt.Errorf("timing: save state: %w", err)
	}
	o.committed = true
	return nil
}

// gridTables precomputes cos(h·φ_g) and sin(h·φ_g) for the fixed evaluation grid, so
// that scoring costs one H×G dot product instead of H×G trigonometric evaluations.
// The tables depend only on H and G, never on data, so they are shared and immutable.
type gridTables struct {
	grid int
	cos  [][]float64 // cos[h-1][g]
	sin  [][]float64

	// buffers recycles the per-call grid scratch. Scoring allocates one G-float
	// buffer per event otherwise, which at corpus scale is measurable purely as
	// collector pressure. A pool is safe here because the buffer never escapes the
	// call and holds no pointers.
	buffers sync.Pool
}

func newGridTables(order, grid int) *gridTables {
	t := &gridTables{
		grid: grid,
		cos:  make([][]float64, order),
		sin:  make([][]float64, order),
	}
	t.buffers.New = func() any { b := make([]float64, grid); return &b }
	for h := 1; h <= order; h++ {
		t.cos[h-1] = make([]float64, grid)
		t.sin[h-1] = make([]float64, grid)
		for g := range grid {
			hphi := float64(h) * 2 * math.Pi * float64(g) / float64(grid)
			t.cos[h-1][g] = math.Cos(hphi)
			t.sin[h-1][g] = math.Sin(hphi)
		}
	}
	return t
}

// TailMass evaluates equation (9) on the grid and, from the same pass, the local
// maxima of the fitted density for the §7.7 evidence view: the grid densities are
// already in hand, so the maxima cost one comparison per point instead of a second
// full evaluation, which profiling showed dominating the scoring path.
//
// The mass is the normalised weight of grid cells whose density does not exceed
// level — equivalent to building a LevelIndex and querying it once, without the sort,
// and in the fixed grid order (R4). The density is evaluated through [Density]'s
// precomputed per-harmonic products, so this fast path and Density.Evaluate perform
// bit-identical arithmetic; the trig tables replace the per-point cos and sin calls.
//
// Modes follow LocalMaxima's rule exactly: a strict rise from the predecessor, no
// fall to the successor, and above the uniform level, so truncation ripple near
// clamped regions cannot render a phantom mode. Floored at 1/2G, the grid's
// resolution limit, exactly as LevelIndex does.
func (t *gridTables) TailMass(d *Density, level float64) (mass float64, modes []float64) {
	if d.moments.W <= 0 {
		return 1, nil
	}
	order := len(d.a)
	threshold := math.Nextafter(level, math.Inf(1))
	bufPtr := t.buffers.Get().(*[]float64)
	values := *bufPtr
	defer t.buffers.Put(bufPtr)

	// Harmonic-major accumulation: each pass reads one contiguous table row, which
	// the hardware prefetches, where a grid-major loop strides across H separate
	// rows per point. Each values[g] still receives its terms in ascending h, the
	// same order Density.Evaluate uses, so the arithmetic is unchanged term for
	// term (R4).
	for g := range values {
		values[g] = 0
	}
	for h := 0; h < order; h++ {
		ah, bh := d.a[h], d.b[h]
		cosRow, sinRow := t.cos[h], t.sin[h]
		for g, c := range cosRow {
			values[g] += ah*c + bh*sinRow[g]
		}
	}

	var total, below float64
	for g, sum := range values {
		f := (1 + 2*sum) / (2 * math.Pi)
		if f < 0 {
			f = 0
		}
		values[g] = f
		total += f
		// The threshold includes cells at exactly the queried level, consistent with
		// LevelIndex.TailMass.
		if f <= threshold {
			below += f
		}
	}

	uniform := 1 / (2 * math.Pi)
	for g := range t.grid {
		prev := values[(g-1+t.grid)%t.grid]
		next := values[(g+1)%t.grid]
		if values[g] > prev && values[g] >= next && values[g] > uniform {
			modes = append(modes, 2*math.Pi*float64(g)/float64(t.grid))
		}
	}

	if total <= 0 {
		return 1, modes
	}
	mass = below / total
	floor := 1 / (2 * float64(t.grid))
	if mass < floor {
		return floor, modes
	}
	if mass > 1 {
		return 1, modes
	}
	return mass, modes
}

// boolAsFloat renders a flag into the numeric evidence map, so a reader can tell from a
// result file which statistic produced it.
func boolAsFloat(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
