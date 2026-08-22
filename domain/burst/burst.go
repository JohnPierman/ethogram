// Package burst implements the sub-hourly inter-arrival test (#53).
//
// # The gap it exists for
//
// Every arm of the framework reaches 0 of 288 planted low-and-slow events, at every budget, on
// all eight victims, and the reason is not that the signal is absent: the unsupervised
// local-outlier-factor baseline reaches 12 of 288 with a density estimate that has no burst
// tolerance at all. The framework's instruments cannot see it.
//
//   - `volume` tests an hourly window, and the plant is twelve events at ninety-second intervals
//     — a seventeen-minute burst. Averaged against fifty-three minutes of nothing, it is
//     unremarkable, and `volume`'s null is *deliberately* over-dispersed so that an ordinary
//     burst does not alert. Repairing that null was measured and did not move the column.
//   - `timing` tests time of day, and the plant sits in the victim's usual hour.
//   - Nothing asks whether these events came too close together.
//
// # The null, and why it is not a Poisson process
//
// The obvious null is a homogeneous Poisson process: exponential gaps, so the span of k
// consecutive arrivals is Gamma(k−1, 1/λ) and a short span is a lower tail. It is exact rather
// than asymptotic, needs no binning, and is powerful against precisely a burst.
//
// It is also wrong for this data, and the measurement that says so was taken before any detection
// claim, because it is the check `volume` and the partitioned `cooccurrence` arm both failed.
// Under the Poisson null the arm is calibrated on its own process — on synthetic streams at one
// event a minute, ten minutes and an hour, 59,970 scans each, the fraction below p = 0.1 is 0.025
// to 0.032 and nothing at all falls below 1e−12 — and on 170,073 real scored events it put 36.7%
// below 1e−12. That is the same defect that disqualified `volume` at 24.7%, and its cause is not
// subtle: real authentication traffic is not a Poisson process. Machine accounts authenticate in
// tight clusters, so short spans are ordinary, and an exponential null finds the ordinary
// astronomically improbable.
//
// So the gaps are given a shape. Under a Gamma(κ, θ) renewal null the span of k arrivals is
// Gamma((k−1)κ, θ), and the p-value is
//
//	p_k = P((k−1)·κ, w_k/θ)
//
// with w_k the span of the last k arrivals and P the regularised lower incomplete gamma. κ below
// one is clustered — over-dispersed gaps, many short and a few very long — and it widens the lower
// tail exactly where clustered traffic lives. κ = 1 is the exponential case and the formula
// reduces to it identically, which is the same degradation §10.2 requires of Brown to Fisher.
//
// κ and θ come from the entity's own decayed gaps by moments, κ̂ = mean²/variance and
// θ̂ = mean/κ̂, so κ̂θ̂ = mean whatever the shape. Its own, never the population's: the framework's
// premise is that the reference set for an event is the account that produced it.
//
// # κ is capped at one, and the cap is the conservative direction
//
// κ̂ above one is under-dispersion: gaps more regular than exponential, which a heartbeat
// produces. Used as measured it would make the null tighter than Poisson, so a machine
// authenticating every sixty seconds would find a fifty-second gap wildly surprising. That is the
// anti-conservative direction, and it is the one this arm exists because of, so κ is capped at one
// and θ set to the mean there: the null is never narrower than exponential. See [MaxShape].
//
// This mirrors the volume arm's dispersion widening, including the part that arm learned the hard
// way: an entity whose dispersion cannot be measured must abstain rather than fall back to the
// narrowest null available.
//
// # Tied timestamps carry no evidence, and this is the larger of the two corrections
//
// Widening the null took the share of real scored events below 1e−12 from 36.7% to 5.77%, which is
// a sixfold improvement onto a number that is still disqualifying: no correct null puts one event
// in twenty below a level a correct null reaches one time in a million million.
//
// The mechanism behind the residual is not the arrival process at all. LANL records to the whole
// second, and one logical action emits several authentication rows, so an entity routinely has many
// arrivals sharing a timestamp. A window covering them has a recorded span of zero, and reading
// that as "the coarsest span the resolution admits" — one second — makes thirty-two arrivals inside
// one second astronomically improbable under any renewal null, however wide. That is not a burst
// being detected. It is the log's granularity being scored.
//
// So arrivals that repeat a timestamp are not stored. The scan buffer holds DISTINCT instants, a
// window of k of them spans at least k−1 seconds, and sub-second structure — which the log did not
// record — is not testified about. On the same 230,699 real scored events the share below 1e−12
// falls from 5.77% to **0.021%**, which is below what the `timing` arm produces (0.056%) and inside
// the bar the other arms are held to.
//
// The two corrections are independent and both are needed: the widening is what stops ordinary
// clustering dominating, and collapsing ties is what stops the log's own resolution being read as
// evidence. Neither alone reaches a usable null.
//
// # The multiplicity correction, stated before any measurement
//
// The shortest surprising window over a range of k is a scan statistic, and treating it as one
// test is exactly the mistake `volume` and the partitioned `cooccurrence` arm were both measured
// making — 24.7% and 99.0% of scored events below 1e−12, which no correct null produces. So the
// minimum over the k examined is corrected by Šidák, equation (16), over the number of windows.
//
// Šidák assumes independence and the windows are nested, hence strongly positively dependent. For
// a minimum over positively dependent tests the independence correction *over*-states the
// multiplicity, so the corrected p-value is conservative. That is the safe direction and it is
// the reason for choosing it over a sharper scan-statistic approximation: this arm's whole
// justification is that two others had anti-conservative nulls, and it would be absurd to
// introduce a third with a null tuned for power.
//
// # Bounded state (§13.3)
//
// Per entity: the last [MaxWindow] DISTINCT arrival instants, a decayed gap sum, a decayed sum of
// squared gaps, a decayed gap count, and an undiscounted gap count. Fixed size regardless of how
// long the entity is watched. The squared sum is the second moment the shape is fitted from, and it
// is the difference between this arm and the Poisson version of it.
//
// # R3, and the gate that is not on a discounted weight
//
// Below [MinGaps] observed gaps the null's parameters are not worth having and the arm abstains. The
// gate counts the *undiscounted* gaps, which is the defect #37 fixed in three arms at once: a
// discounted count saturates at 1/(1−δ), so a minimum above that ceiling is unsatisfiable
// forever rather than merely slow to reach.
//
// An entity whose gap dispersion cannot be estimated abstains too, rather than falling back to the
// exponential null. That null is the narrowest this arm has, and falling back to the narrowest
// available null is the defect #45 found in the volume arm.
package burst

import (
	"math"

	"github.com/JohnPierman/ethogram/domain/calibration"
	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/novelty"
)

const (
	// MaxWindow is the largest number of consecutive arrivals the scan considers, and the
	// number of timestamps held per entity.
	//
	// The planted mechanism is twelve events, so a bound below that could not see it however
	// well calibrated it was; 32 leaves room for a burst twice that size without making the
	// per-entity state or the multiplicity correction expensive. The correction charges the
	// number of windows examined, so a larger bound is not free: it costs power on every
	// event to buy reach on a few.
	MaxWindow = 32

	// MinWindow is the smallest number of consecutive arrivals worth testing.
	//
	// Two arrivals is one gap, and one gap short of the mean is the single most common thing
	// an exponential produces — its density is highest at zero. Three is the smallest window
	// whose shortness is evidence rather than an ordinary draw.
	MinWindow = 3

	// MinGaps is the fewest observed gaps, undiscounted, the null's parameters are formed on.
	//
	// Fifty rather than thirty because the null needs a variance and not only a mean. The
	// relative standard error of a variance is about sqrt(2/n) even for a well-behaved sample
	// and worse for a heavy-tailed one, so thirty gaps leave the shape uncertain by a quarter
	// — and the shape is what decides whether a span is surprising, so an uncertain shape is
	// not a widened null but a random one.
	MinGaps = 50

	// ResolutionSeconds is the timestamp resolution of the corpus, and the floor a window's
	// span is measured at.
	//
	// Since [State.Observe] no longer stores an arrival that repeats a timestamp, a window of
	// k held instants spans at least k−1 seconds and this floor is unreachable through
	// in-order input. It remains as a guard for out-of-order arrivals, where the oldest and
	// newest held instants can coincide without being adjacent, and a zero span would give
	// ln p = -Inf -- not a p-value, and refused by the verdict constructor. The first corpus
	// run of this arm died on exactly that, before ties were collapsed.
	//
	// The floor rather than an abstention because the observations are interval-censored: a
	// recorded span of w seconds means the true span lies within a second of w, and the lower
	// tail is increasing in w, so the coarsest reading the resolution admits is the
	// conservative one.
	ResolutionSeconds = 1

	// MaxShape is the largest κ the null may use: exactly one, the exponential case.
	//
	// A measured κ above one is under-dispersion, and honouring it would make the null
	// narrower than Poisson — see the package comment. The cap is the whole reason this arm
	// can claim a conservative null on data whose shape it does not know in advance.
	MaxShape = 1.0

	// MinShape floors κ so that one pathological entity cannot produce a null so wide that
	// nothing is ever surprising, and so that (k−1)κ stays a usable gamma shape.
	//
	// An entity with κ = 1e−6 has gaps whose variance is a million times the square of their
	// mean, which is not clustering but a mixture of processes wearing one name. The floor is
	// where the arm stops pretending a single renewal null describes it; the p-value is then
	// merely very conservative, which is the safe way to be wrong.
	MinShape = 0.01
)

// State is one entity's frozen inter-arrival evidence. Fixed size (§13.3).
type State struct {
	// Recent holds the last MaxWindow DISTINCT arrival instants, oldest first: a repeated
	// timestamp is not stored. The scan reads spans from it and nothing else does, and the
	// distinctness is what keeps the log's one-second resolution out of the statistic -- see
	// [State.Observe].
	Recent []event.Timestamp

	// Gaps is the decayed sum of observed inter-arrival times in seconds, GapsSquared the
	// decayed sum of their squares, and Count the decayed number of them. Together they are the
	// two moments the Gamma(κ, θ) null is fitted from; see the package comment.
	//
	// GapsSquared is what separates this arm from the Poisson version that measured 36.7% of
	// real events below 1e−12. A first moment gives a rate, and a rate alone cannot express
	// that an account's gaps are clustered.
	Gaps        float64
	GapsSquared float64
	Count       float64

	// Observed is the UNDISCOUNTED number of gaps folded in, and is what [MinGaps] counts.
	// See the package comment: a discounted count saturates and cannot express "how many".
	Observed int64

	LastSeen event.Timestamp
}

// Clone returns a deep copy.
//
// A shallow copy is not enough and the difference is a real defect rather than a style
// preference: Recent is a slice, so `*s` shares its backing array, and [State.Observe] appends
// into it. A store handing out `*s` and a detector appending the event being scored would write
// the scored event into the stored state -- scoring before observing in form while observing
// before scoring in fact, which is precisely the silent failure §5.2 is built to prevent.
func (s *State) Clone() *State {
	if s == nil {
		return nil
	}
	c := *s
	c.Recent = append([]event.Timestamp(nil), s.Recent...)
	return &c
}

// Rate is the entity's own arrival rate in events per second, or zero where no estimate exists.
// It is the reciprocal of the mean gap, and is reported as evidence because an analyst reads a
// rate; the null itself is parameterised by [State.Shape].
func (s *State) Rate() float64 {
	if s == nil || s.Gaps <= 0 || s.Count <= 0 {
		return 0
	}
	return s.Count / s.Gaps
}

// Shape returns the Gamma(κ, θ) parameters of the entity's own gaps, by moments, with κ capped at
// [MaxShape] and floored at [MinShape]. ok is false where no estimate exists.
//
// κθ = mean identically, including at the cap and the floor, because θ is derived from that
// identity rather than from variance/mean. So the fitted null always has the entity's own mean
// gap and the shape decides only how the mass around it is spread.
func (s *State) Shape() (shape, scale float64, ok bool) {
	if s == nil || s.Count <= 0 || s.Gaps <= 0 {
		return 0, 0, false
	}
	mean := s.Gaps / s.Count
	if mean <= 0 || math.IsInf(mean, 0) || math.IsNaN(mean) {
		return 0, 0, false
	}
	variance := s.GapsSquared/s.Count - mean*mean
	if math.IsNaN(variance) || math.IsInf(variance, 0) {
		return 0, 0, false
	}
	if variance <= 0 {
		// Numerically constant gaps: a perfect heartbeat, which is under-dispersion at its
		// limit. The cap applies, so it is scored as exponential rather than as certainty.
		return MaxShape, mean / MaxShape, true
	}
	shape = mean * mean / variance
	switch {
	case math.IsNaN(shape):
		return 0, 0, false
	case shape > MaxShape:
		shape = MaxShape
	case shape < MinShape:
		shape = MinShape
	}
	return shape, mean / shape, true
}

// Observe folds one arrival into the state, at the §6.2 discount.
//
// The gap is measured from the previous arrival, so the first arrival contributes a timestamp and
// no gap. A non-positive gap is not evidence about a rate, and LANL's one-second resolution
// guarantees ties, so treating a tie as a zero gap would drive the rate estimate to infinity on
// the most ordinary data there is.
//
// An arrival that REPEATS the last held instant is not stored at all, which is the larger of this
// arm's two calibration corrections and is explained in the package comment: the scan reasons about
// spans, the log cannot resolve structure inside one second, and reading a tie as a one-second span
// made the log's own granularity the most extreme evidence in the corpus. Collapsing ties took the
// share of real scored events below 1e−12 from 5.77% to 0.021%.
//
// The gap arithmetic above is unaffected, because it already skipped non-positive gaps: this
// changes what the scan can see, not what the rate is estimated from.
func (s *State) Observe(at event.Timestamp, halfLife novelty.HalfLife) {
	if len(s.Recent) > 0 {
		if gap := float64(at-s.LastSeen) / float64(event.Second); gap > 0 {
			factor := novelty.DecayFactor(s.LastSeen, at, halfLife)
			s.Gaps = s.Gaps*factor + gap
			s.GapsSquared = s.GapsSquared*factor + gap*gap
			s.Count = s.Count*factor + 1
			s.Observed++
		}
	}
	if n := len(s.Recent); n > 0 && s.Recent[n-1] == at {
		// A repeated instant. The rate has already been updated (with nothing, since the
		// gap was zero) and the scan must not see a second copy.
		return
	}
	s.Recent = append(s.Recent, at)
	if len(s.Recent) > MaxWindow {
		s.Recent = s.Recent[len(s.Recent)-MaxWindow:]
	}
	if at > s.LastSeen {
		s.LastSeen = at
	}
}

// Scan is the result of one event's scan over window sizes.
type Scan struct {
	// LogP is ln of the Šidák-corrected minimum over the windows examined, and is the
	// quantity to rank and threshold on. It is computed in log space throughout: the
	// uncorrected tail reaches ln p below −1000 for a tight burst on a quiet account, which
	// is zero as a float64.
	LogP float64
	// LogMinP is the uncorrected minimum, and Window the number of consecutive arrivals that
	// attained it, so the correction's effect is readable and the window is nameable in
	// evidence (R5).
	LogMinP float64
	Window  int
	// SpanSeconds is that window's span, Rate the entity's own estimated rate, and Shape and
	// Scale the fitted Gamma null's parameters, so the arithmetic can be redone by hand from
	// the verdict alone (R5).
	SpanSeconds float64
	Rate        float64
	Shape       float64
	Scale       float64
	// Windows is how many window sizes were examined, which is what the correction charges.
	Windows int
}

// Abstention is why no scan was produced.
type Abstention int

const (
	// AbstainNone means a scan was produced.
	AbstainNone Abstention = iota
	// AbstainTooFewGaps: the entity has not supplied MinGaps observed gaps, so there is no
	// rate estimate worth testing against.
	AbstainTooFewGaps
	// AbstainTooFewArrivals: fewer than MinWindow arrivals are held, so no window exists.
	AbstainTooFewArrivals
	// AbstainNoRate: the gaps sum to nothing, which a stream of simultaneous arrivals
	// produces. A rate of infinity is not an estimate.
	AbstainNoRate
	// AbstainNoShape: the gaps admit no dispersion estimate, so the only null left would be
	// the un-widened exponential one -- the narrowest this arm has, and the one measured
	// putting 36.7% of real events below 1e-12. Falling back to it is the mistake the volume
	// arm made; abstaining is the outcome.
	AbstainNoShape
)

func (a Abstention) String() string {
	switch a {
	case AbstainTooFewGaps:
		return "too few of this entity's own inter-arrival gaps to estimate its rate"
	case AbstainTooFewArrivals:
		return "too few arrivals held to form a window"
	case AbstainNoRate:
		return "this entity's observed gaps sum to zero, so it has no finite arrival rate"
	case AbstainNoShape:
		return "this entity's gaps admit no dispersion estimate, and the only null left " +
			"would be the un-widened exponential one, which is the narrowest this arm has"
	default:
		return ""
	}
}

// Evaluate scans the state's held arrivals for the most surprising short window, correcting the
// minimum by Šidák over the windows examined.
//
// The state must already include the event being scored — the scan is over arrivals, and the
// arrival that completes a burst is part of it. The caller therefore observes before evaluating,
// which is the opposite of the order §5.2 imposes on the other arms and is stated here because it
// is a real difference: what is being tested is a property of a *set* of arrivals rather than of
// one event against prior state, and excluding the newest arrival would test the wrong set. The
// rate the set is judged against is still formed from gaps that include it, which is a
// conservative direction: a burst raises the estimated rate and a higher rate makes a short span
// less surprising.
func Evaluate(s *State) (Scan, Abstention) {
	switch {
	case s == nil || len(s.Recent) < MinWindow:
		return Scan{}, AbstainTooFewArrivals
	case s.Observed < MinGaps:
		return Scan{}, AbstainTooFewGaps
	}
	rate := s.Rate()
	if rate <= 0 || math.IsInf(rate, 0) || math.IsNaN(rate) {
		return Scan{}, AbstainNoRate
	}
	shape, scale, ok := s.Shape()
	if !ok || scale <= 0 || math.IsInf(scale, 0) {
		return Scan{}, AbstainNoShape
	}

	newest := s.Recent[len(s.Recent)-1]
	best := Scan{LogMinP: math.Inf(1), Rate: rate, Shape: shape, Scale: scale}
	windows := 0

	// Ascending k, one fixed order (R4). k is the number of arrivals in the window, so the
	// span covers k−1 gaps and the waiting time is Gamma(k−1).
	for k := MinWindow; k <= len(s.Recent); k++ {
		oldest := s.Recent[len(s.Recent)-k]
		span := float64(newest-oldest) / float64(event.Second)
		if span < 0 {
			// Out-of-order arrivals in the buffer; the span is not a duration.
			continue
		}
		if span < ResolutionSeconds {
			// A tie at the corpus's resolution, read conservatively; see
			// [ResolutionSeconds].
			span = ResolutionSeconds
		}
		windows++
		// The span of k arrivals is the sum of k-1 Gamma(κ, θ) gaps, hence Gamma((k-1)κ, θ).
		logP := calibration.GammaLowerTailLog(float64(k-1)*shape, span/scale)
		if logP < best.LogMinP {
			best.LogMinP = logP
			best.Window = k
			best.SpanSeconds = span
		}
	}
	if windows == 0 {
		return Scan{}, AbstainTooFewArrivals
	}

	best.Windows = windows
	best.LogP = calibration.SidakLog(best.LogMinP, windows)
	return best, AbstainNone
}
