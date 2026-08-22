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
// # The null
//
// An entity's arrivals under a homogeneous Poisson process at its own rate λ have exponential
// gaps, so the span of k consecutive arrivals is Gamma(k−1, 1/λ). The span being *short* is
// therefore a lower tail:
//
//	p_k = P(k−1, λ·w_k)
//
// with w_k the span of the last k arrivals and P the regularised lower incomplete gamma. That is
// exact rather than asymptotic, it needs no binning, and the alternative it is powerful against
// is precisely a burst.
//
// λ is estimated from the entity's own gaps under the same §6.2 discount the other arms use —
// the maximum-likelihood exponential rate, decayed count over decayed gap sum. Its own, never the
// population's: the framework's premise is that the reference set for an event is the account
// that produced it.
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
// Per entity: the last [MaxWindow] arrival timestamps, a decayed gap sum, a decayed gap count, and
// an undiscounted gap count. Fixed size regardless of how long the entity is watched.
//
// # R3, and the gate that is not on a discounted weight
//
// Below [MinGaps] observed gaps the rate estimate is not worth having and the arm abstains. The
// gate counts the *undiscounted* gaps, which is the defect #37 fixed in three arms at once: a
// discounted count saturates at 1/(1−δ), so a minimum above that ceiling is unsatisfiable
// forever rather than merely slow to reach.
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

	// MinGaps is the fewest observed gaps, undiscounted, that the rate estimate is formed on.
	//
	// Thirty because the estimate is a rate over a sample and the arm's whole claim rests on
	// it: at ten gaps the standard error on λ̂ is about a third of λ̂ itself, which is wider
	// than the effect the test looks for.
	MinGaps = 30

	// ResolutionSeconds is the timestamp resolution of the corpus, and the floor a window's
	// span is measured at.
	//
	// This is not a nicety. LANL records to the whole second, so several of an entity's
	// arrivals routinely share a timestamp and the recorded span of a window containing them
	// is zero. Under a continuous null a zero-duration span of three or more arrivals has
	// probability exactly zero, so the uncorrected tail is ln p = -Inf -- which is not a
	// p-value, and which the verdict constructor correctly refuses. The first corpus run of
	// this arm died on it in the first minute.
	//
	// The observations are interval-censored: a recorded span of w seconds means the true
	// span lies within a second of w. The lower tail P(a, lambda*w) is increasing in w, so the
	// LARGER reading is the less surprising one, and the conservative treatment of a tie is
	// therefore to read it as the coarsest span the resolution admits rather than as zero.
	// Flooring at one second does that, and does it only where it matters: for every window
	// whose span is a second or more the recorded value is used unchanged.
	//
	// The alternative -- abstaining on a tie -- is backwards, because the tightest bursts are
	// precisely the ones that produce ties, so the arm would fall silent on its own strongest
	// evidence.
	ResolutionSeconds = 1
)

// State is one entity's frozen inter-arrival evidence. Fixed size (§13.3).
type State struct {
	// Recent holds the last MaxWindow arrival timestamps, oldest first. The scan reads spans
	// from it and nothing else does.
	Recent []event.Timestamp

	// Gaps is the decayed sum of observed inter-arrival times, in seconds, and Count the
	// decayed number of them. The rate estimate is Count/Gaps, the discounted
	// maximum-likelihood exponential rate.
	Gaps  float64
	Count float64

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
func (s *State) Rate() float64 {
	if s == nil || s.Gaps <= 0 || s.Count <= 0 {
		return 0
	}
	return s.Count / s.Gaps
}

// Observe folds one arrival into the state, at the §6.2 discount.
//
// The gap is measured from the previous arrival, so the first arrival contributes a timestamp and
// no gap. Out-of-order and simultaneous arrivals contribute a timestamp and no gap either: a
// non-positive gap is not evidence about a rate, and LANL's one-second resolution guarantees
// ties, so treating a tie as a zero gap would drive the rate estimate to infinity on the most
// ordinary data there is.
func (s *State) Observe(at event.Timestamp, halfLife novelty.HalfLife) {
	if len(s.Recent) > 0 {
		if gap := float64(at-s.LastSeen) / float64(event.Second); gap > 0 {
			factor := novelty.DecayFactor(s.LastSeen, at, halfLife)
			s.Gaps = s.Gaps*factor + gap
			s.Count = s.Count*factor + 1
			s.Observed++
		}
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
	// SpanSeconds is that window's span and Rate the entity's own estimated rate, so the
	// arithmetic can be redone by hand from the verdict alone (R5).
	SpanSeconds float64
	Rate        float64
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
)

func (a Abstention) String() string {
	switch a {
	case AbstainTooFewGaps:
		return "too few of this entity's own inter-arrival gaps to estimate its rate"
	case AbstainTooFewArrivals:
		return "too few arrivals held to form a window"
	case AbstainNoRate:
		return "this entity's observed gaps sum to zero, so it has no finite arrival rate"
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

	newest := s.Recent[len(s.Recent)-1]
	best := Scan{LogMinP: math.Inf(1), Rate: rate}
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
		logP := calibration.GammaLowerTailLog(float64(k-1), rate*span)
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
