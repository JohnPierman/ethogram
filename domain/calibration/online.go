package calibration

import (
	"errors"
	"fmt"
	"math"
)

// Online error control (§10.3, issue #16).
//
// # Why the batch procedures do not deploy
//
// Benjamini–Hochberg needs the whole batch: the step-up threshold (i/m)·q depends on m, the
// number of tests, and on each p-value's rank among all of them. A live detector has a
// prefix, not a batch. The offline construction here scores a fixed corpus and applies the
// step-up per corpus day, keeping the exact per-day denominator because retaining only the
// top K alerts would destroy it — correct for a measurement and not deployable, because an
// operator at 14:00 cannot use a threshold that depends on how many events arrive by 23:59.
//
// # The second problem, which is the one that motivated this
//
// A rule that minimises false positives has a degenerate optimum at alerting on nothing, and
// a fixed-q batch run that stops rejecting has no mechanism to start again. It has no memory
// of having been right.
//
// Alpha-investing (Foster and Stine 2008) answers exactly that. The procedure holds a wealth of error budget,
// spends from it on every test, and earns back on every rejection, so a productive period
// buys a higher alerting rate and a barren one decays towards silence without ever reaching
// it. [LORD] is the modern member of the family used here
// (Ramdas, Yang, Wainwright and Jordan 2017).
//
// The refund is also where analyst feedback belongs. A confirmed true positive is the event
// that should buy more budget, and this is the first place in the framework with somewhere
// to put a disposition signal.
//
// # The wealth identity, which is what makes the guarantee checkable
//
// Writing R_t for the rejections so far,
//
//	W_t = W_0 − Σ_{s ≤ t} α_s + q·R_t
//
// is the wealth after t tests. LORD++ chooses its levels so this is never negative — each
// term of the level is drawn from a spending sequence summing to one, so the W_0 term can
// spend at most W_0 in total and each rejection's term at most q.
//
// It is NOT bounded above, and measuring it is what established that. Under a stream that
// rejects everything, wealth grows linearly — 430 over 200,000 tests at q = 0.1. Each
// rejection earns exactly q while the level is capped at q and approaches it from below, so
// earning exceeds spending by the unspent tail of the sequence, about 0.002 per test, and the
// difference accumulates. Issue #16 asked for a bound here and there is none to have.
//
// Wealth growing in this direction is unspent budget rather than runaway alerting. The
// quantity that could do harm is the LEVEL, and that is bounded: measured across all-reject,
// alternating and bursty streams, no test was ever conducted above 0.098 against q = 0.1.
//
// # Capability separation (§5.2)
//
// [OnlineRule.Level] must not mutate and [OnlineRule.Observe] is the only thing that does,
// which is the same split the detectors enforce between scoring and committing. The interface
// deliberately never sees a p-value, so a rule cannot set its own level by peeking at the
// test it is about to judge.

// OnlineRule is the alpha-wealth contract.
type OnlineRule interface {
	// Level is the test level for the NEXT hypothesis. Pure: calling it any number of
	// times returns the same value and leaves the rule unchanged.
	Level() float64
	// LogLevel is ln of Level, for comparison against log p-values. The corpus reaches
	// ln P = −4000, where the p-value itself is zero and every comparison against a level
	// is a comparison of zeros.
	LogLevel() float64
	// Candidacy is the threshold below which a p-value counts towards the rule's candidate
	// accounting, or zero for a rule with no such notion. The caller computes candidacy
	// from this and reports it through [Outcome], which keeps the p-value out of the rule.
	Candidacy() float64
	// Observe commits the outcome of the test the current level was issued for, and is the
	// only method that advances the rule.
	Observe(Outcome)
	// Tests and Rejections are the counts so far.
	Tests() int
	Rejections() int
	// Wealth is the alpha-wealth identity above: what remains of the error budget.
	Wealth() float64
}

// Outcome is what a caller reports back about one test.
type Outcome struct {
	// Rejected is whether the p-value fell at or below the level the rule issued.
	Rejected bool
	// Candidate is whether the p-value fell below [OnlineRule.Candidacy], for rules that
	// use candidate accounting. Ignored by rules that do not, so a caller that always
	// reports it is correct everywhere.
	Candidate bool
}

// GammaSequence is a non-negative, non-increasing spending sequence summing to one over the
// positive integers. It is indexed from one; index zero or below spends nothing, which is
// what makes a rejection contribute only from the test after it.
type GammaSequence interface {
	At(j int) float64
	// Name identifies the sequence in a run record.
	Name() string
}

// gammaLogSquared is the sequence γ_j ∝ 1/(j·ln²(max(j,2))) recommended throughout the LORD
// literature, normalised to sum to one.
//
// The normalising constant is the sum of the unnormalised terms, which converges: the tail
// integral ∫_J^∞ dj/(j ln²j) is 1/ln J, so the series is finite even though it decays only
// logarithmically. That slow decay is what makes truncation awkward; see [lordRuns].
type gammaLogSquared struct {
	scale float64
}

// gammaSumTerms is how far the normalising sum is accumulated explicitly before the
// analytic tail 1/ln J is added.
//
// One million terms costs a few milliseconds once, at package initialisation, and the
// Euler–Maclaurin error at that point is of the order of the last term itself,
// 1/(10⁶·ln²10⁶) ≈ 7·10⁻¹¹ against a sum near four — eleven digits, which is more than any
// level computed from it can carry. Accumulated in one fixed ascending order (R4).
const gammaSumTerms = 1_000_000

var defaultGamma = newGammaLogSquared()

func newGammaLogSquared() gammaLogSquared {
	total := 0.0
	for j := 1; j <= gammaSumTerms; j++ {
		total += unnormalisedGamma(j)
	}
	// The tail ∫_J^∞ dj/(j ln²j) = 1/ln J, added rather than truncated: dropping it would
	// leave the sequence summing to slightly less than one, which inflates every level.
	total += 1 / math.Log(float64(gammaSumTerms))
	return gammaLogSquared{scale: 1 / total}
}

func unnormalisedGamma(j int) float64 {
	if j < 1 {
		return 0
	}
	base := float64(j)
	if j < 2 {
		base = 1
	}
	ln := math.Log(math.Max(float64(j), 2))
	return 1 / (base * ln * ln)
}

func (g gammaLogSquared) At(j int) float64 {
	if j < 1 {
		return 0
	}
	return g.scale * unnormalisedGamma(j)
}

func (g gammaLogSquared) Name() string {
	return "gamma_j proportional to 1/(j ln^2 max(j,2)), normalised to sum to one"
}

// DefaultGamma returns the standard spending sequence.
func DefaultGamma() GammaSequence { return defaultGamma }

// lordRuns is how many contiguous runs of rejections contribute to a level.
//
// The exact LORD++ level spends a term per rejection, which is O(R) per test: on four and a
// half million tests with a miscalibrated arm rejecting most of them that is O(t²), and the
// spending sequence decays too slowly for a tail bound to make truncation cheap — the mass
// beyond lag L falls only as 1/ln L.
//
// So the sum is taken over contiguous RUNS of rejections rather than over rejections, against
// a cumulative table of the sequence. A run [a,b] contributes G(t−a) − G(t−b−1) exactly, in
// constant time, which makes the two cases that matter both fast and exact: a calibrated arm
// rejects rarely and has few runs, and an arm that rejects everything has one. What remains
// expensive is a stream that alternates, which no corpus produces and which the cap below
// bounds.
//
// Where the cap binds, the oldest runs are dropped. Every omitted term is positive, so a
// truncated level is a SMALLER level: the rule rejects less than the exact procedure would,
// which cannot break error control and can only cost power. [LORD.OmittedMass] reports how
// much spending was dropped, so that cost is a recorded number and not an unstated
// approximation.
const lordRuns = 512

// rejectionRun is a maximal contiguous block of rejected tests, inclusive at both ends.
type rejectionRun struct{ from, to int }

// LORD is the LORD++ procedure of Ramdas et al. (2017).
//
// The level for test t is
//
//	α_t = γ_t·W₀ + (q − W₀)·γ_{t−τ₁} + q·Σ_{j ≥ 2} γ_{t−τ_j}
//
// over the rejection times τ_j, which is alpha-investing with the refund made explicit: the
// first rejection returns the difference between the target level and the starting wealth,
// and every later one returns the full target level, each spread forward over the spending
// sequence.
//
// It controls the marginal false discovery rate over the whole stream for independent
// p-values, and does so without ever reaching zero: γ_t is strictly positive at every finite
// t, so a barren stretch decays the level without silencing it. That is the property the
// issue exists for and it is pinned by test.
type LORD struct {
	q       float64
	initial float64
	gamma   GammaSequence

	tests   int
	rejects int
	runs    []rejectionRun

	// cumulative[k] is Σ_{j=1..k} γ_j, with cumulative[0] = 0, grown on demand and
	// accumulated in one fixed ascending order (R4).
	cumulative []float64

	spent     float64
	omitted   float64
	truncated int
}

// NewLORD returns a LORD++ rule.
//
// wealth is the starting error budget W₀ and must lie in (0, q]: the procedure spends it
// before any rejection has been earned, so a starting wealth above the target level would
// spend more than the guarantee allows. q is the target marginal false discovery rate.
func NewLORD(wealth, q float64, gamma GammaSequence) (*LORD, error) {
	if q <= 0 || q >= 1 {
		return nil, fmt.Errorf("calibration: q must lie in (0,1), got %g", q)
	}
	if wealth <= 0 {
		return nil, fmt.Errorf("calibration: starting wealth must be positive, got %g", wealth)
	}
	if wealth > q {
		return nil, fmt.Errorf(
			"calibration: starting wealth %g exceeds q = %g, which spends more than the "+
				"guarantee allows before anything has been earned", wealth, q)
	}
	if gamma == nil {
		gamma = DefaultGamma()
	}
	return &LORD{q: q, initial: wealth, gamma: gamma, cumulative: []float64{0}}, nil
}

// cumulativeUpTo is G(k) = Σ_{j=1..k} γ_j, extending the table as needed.
//
// Growing the table mutates it, so this is called only from [LORD.levelFor], whose contract
// with [LORD.Level] is that the table is a cache of a pure function of the sequence: the
// value returned for any k is the same however many times it is asked for, and extending the
// table cannot change a value already in it. Purity is a property of the answers, and this
// preserves it.
func (l *LORD) cumulativeUpTo(k int) float64 {
	if k < 1 {
		return 0
	}
	for len(l.cumulative) <= k {
		next := len(l.cumulative)
		l.cumulative = append(l.cumulative, l.cumulative[next-1]+l.gamma.At(next))
	}
	return l.cumulative[k]
}

// levelFor is the level for test number `next`, one-indexed.
func (l *LORD) levelFor(next int) float64 {
	level := l.gamma.At(next) * l.initial

	for index := len(l.runs) - 1; index >= 0; index-- {
		run := l.runs[index]
		// Lags run from next−run.to (the newest rejection in the run) up to next−run.from.
		low, high := next-run.to, next-run.from
		if high < 1 {
			continue
		}
		if low < 1 {
			low = 1
		}

		first := run.from
		if index == 0 && l.truncated == 0 {
			// The oldest surviving run begins with the first rejection ever, which refunds
			// only q − W₀ because W₀ has already been spent against the same budget. Once
			// any run has been dropped this is no longer identifiable, and crediting the
			// full q is the conservative reading — it can only lower the level, since
			// q − W₀ ≤ q.
			lag := next - first
			if lag >= 1 {
				level += (l.q - l.initial) * l.gamma.At(lag)
				high = lag - 1
			}
		}
		if high >= low {
			level += l.q * (l.cumulativeUpTo(high) - l.cumulativeUpTo(low-1))
		}
	}

	if level < 0 {
		// Unreachable while the constructor refuses a starting wealth above q, and kept
		// because a negative level is not a level: clamping visibly beats issuing one.
		return 0
	}
	return level
}

// Level is the level for the next hypothesis, and does not change any answer the rule gives.
func (l *LORD) Level() float64 { return l.levelFor(l.tests + 1) }

// LogLevel is ln of the level. A level of zero gives −Inf, which no p-value can fall at or
// below in log space, so the comparison stays correct at the boundary.
func (l *LORD) LogLevel() float64 { return math.Log(l.Level()) }

// Candidacy is zero: LORD++ has no candidate accounting.
func (l *LORD) Candidacy() float64 { return 0 }

// Observe commits one test's outcome. It and [LORD.Test] are the only methods that advance
// the rule.
func (l *LORD) Observe(outcome Outcome) {
	// The level the caller tested against is the one for this test. Recorded before the
	// counter advances, so the wealth identity is exact.
	l.commit(l.levelFor(l.tests+1), outcome.Rejected)
}

// commit is the shared advance: spend the level, count the test, and extend or open a run of
// rejections. One implementation, so Test and Observe cannot drift apart.
func (l *LORD) commit(level float64, rejected bool) {
	l.spent += level
	l.tests++
	if !rejected {
		return
	}
	l.rejects++

	if n := len(l.runs); n > 0 && l.runs[n-1].to == l.tests-1 {
		l.runs[n-1].to = l.tests
		return
	}
	l.runs = append(l.runs, rejectionRun{from: l.tests, to: l.tests})
	if len(l.runs) <= lordRuns {
		return
	}
	// The cap binds: drop the oldest run and record the spending it would still have
	// contributed over the remaining stream, bounded by its whole unspent share of the
	// sequence.
	dropped := l.runs[0]
	for at := dropped.from; at <= dropped.to; at++ {
		l.omitted += l.q * (1 - l.cumulativeUpTo(l.tests-at))
	}
	l.runs = l.runs[1:]
	l.truncated++
}

// Test decides one hypothesis from its log p-value and advances the rule, in a single pass
// over the rejection history.
//
// It exists for cost rather than for convenience. A level is O(runs) to compute, and calling
// [LORD.Level] to decide and then [LORD.Observe] to commit computes it twice -- which over
// four and a half million events on two streams is the difference between twenty seconds and
// a minute. Level stays pure and is what an audit reads; this is what a replay calls.
//
// The comparison is in log space: at ln P = -4000 the p-value is zero, and in p-space every
// such event is tied with every other and with any level below the smallest positive float64.
func (l *LORD) Test(logP float64) bool {
	level := l.levelFor(l.tests + 1)
	rejected := logP <= math.Log(level)
	l.commit(level, rejected)
	return rejected
}

// Tests and Rejections are the counts so far.
func (l *LORD) Tests() int      { return l.tests }
func (l *LORD) Rejections() int { return l.rejects }

// Wealth is W₀ − Σα_s + q·R: what remains of the error budget.
//
// It is never negative, because each term of every level is drawn from a sequence summing to
// one, so the starting wealth can fund at most W₀ of spending in total and each rejection at
// most q. It is not bounded above: see this file's header for why, and for the bound that does
// hold, which is on the level rather than on the wealth.
func (l *LORD) Wealth() float64 {
	return l.initial - l.spent + l.q*float64(l.rejects)
}

// OmittedMass is the spending dropped by capping the rejection history, and
// TruncatedRuns how many runs were dropped.
//
// Both are zero on any stream with at most lordRuns runs of rejections. Where they are not,
// the level issued is smaller than the exact procedure's, so the rule is conservative and the
// cost is power rather than error control.
func (l *LORD) OmittedMass() float64 { return l.omitted }
func (l *LORD) TruncatedRuns() int   { return l.truncated }

// Spent is the total level issued across every test, the Σα_s of the wealth identity.
func (l *LORD) Spent() float64 { return l.spent }

// Describe is the rule's provenance for a run record.
func (l *LORD) Describe() map[string]any {
	return map[string]any{
		"rule":           "LORD++",
		"q":              l.q,
		"initial_wealth": l.initial,
		"gamma":          l.gamma.Name(),
		"level": "alpha_t = gamma_t W0 + (q - W0) gamma_{t-tau_1} + " +
			"q sum_{j>=2} gamma_{t-tau_j}",
		"retained_runs":  lordRuns,
		"omitted_mass":   l.omitted,
		"truncated_runs": l.truncated,
		"tests":          l.tests,
		"rejections":     l.rejects,
		"wealth":         l.Wealth(),
		"spent":          l.spent,
		"guarantee": "marginal FDR at or below q over the whole stream for independent " +
			"p-values, with a level strictly positive at every finite t -- so a barren " +
			"stretch decays the alerting rate without ever silencing it",
	}
}

// ErrNoRule reports a stream run with no rule.
var ErrNoRule = errors.New("calibration: online control needs a rule")

// RunOnline applies a rule to a stream of log p-values in arrival order and returns, for each
// test, whether it was rejected.
//
// The comparison is logP ≤ ln α, in log space, because the corpus's p-values underflow: at
// ln P = −4000 the p-value is zero, every such event is tied with every other, and a
// comparison in p-space cannot tell any of them from a level of 10⁻³⁰⁰.
//
// The trace is returned rather than accumulated internally so that a caller can record
// whatever slice of it a result file should carry, without this function knowing about result
// files.
func RunOnline(rule OnlineRule, logP []float64) ([]bool, []float64, error) {
	if rule == nil {
		return nil, nil, ErrNoRule
	}
	rejected := make([]bool, len(logP))
	levels := make([]float64, len(logP))
	for i, lp := range logP {
		level := rule.Level()
		levels[i] = level
		hit := lp <= math.Log(level)
		rejected[i] = hit
		// Candidacy is computed here, from the threshold the rule publishes, so the rule
		// still never sees a p-value. A rule with no candidate accounting publishes zero
		// and nothing is ever a candidate, which is the right answer for it.
		candidate := rule.Candidacy() > 0 && lp < math.Log(rule.Candidacy())
		rule.Observe(Outcome{Rejected: hit, Candidate: candidate})
	}
	return rejected, levels, nil
}
