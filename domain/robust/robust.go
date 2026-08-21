// Package robust allocates a fixed alert budget across detectors when the mix of attack
// mechanisms is not known in advance.
//
// # Why allocation by prior cannot help
//
// Read a per-mechanism detection table as a payoff matrix: rows are detectors, columns are
// the mechanism an adversary chooses, and an entry is that detector's detection rate against
// that mechanism at a fixed budget. Suppose the mix of mechanisms p is known — from
// published incident statistics, say. The expected detection of a randomised allocation w is
//
//	w'(A p),
//
// which is linear in w. A linear function on a simplex attains its maximum at a vertex, so
// the best allocation is always a single detector and no prior-weighted mixture can beat the
// best single arm. This is not a property of any particular corpus. It is the reason the
// exhaustive search over budget splits in the paper finds its optimum at the corner, and it
// means "weight the detectors by how common each attack is" is answered before it is tried.
//
// # What a mixture is for
//
// A mixture earns its keep only where the objective stops being linear in w, and the
// interesting case is the adversarial one: the mechanism is chosen to be the one the
// allocation covers worst. [Matrix.Maximin] solves that game. On a portfolio with a
// mechanism no detector reaches at all, its answer is that the game is worth nothing —
// correctly, because the adversary simply plays that mechanism, and no reweighting of rows
// changes a column of zeros. A zero there is a coverage defect and this package will report
// it as one rather than allocate around it.
//
// [Matrix.CompetitiveRatio] is the objective to use once that is understood. Normalising
// each mechanism by the best any detector achieves against it asks a different and better
// question — what fraction of the reachable detection does this allocation retain, against
// the mechanism where it retains least — and its answer is an interior mixture that
// equalises across mechanisms.
//
// Neither objective is free, and [Matrix.PriceOfRobustness] is what states the bill: the
// expected detection given up, under a stated prior, to buy the worst-case guarantee. The
// choice between them is the operator's and this package declines to make it.
//
// # Units
//
// Nothing here names a detector, a mechanism, a corpus or a budget, and the payoff may be a
// rate or a count as long as one matrix is consistent: it is arithmetic over a rectangle of
// non-negative numbers. [Matrix.Blend] is the same linearity as above, used deliberately —
// the expected payoff of randomising over detectors, which is exactly evaluable from each
// detector's recorded performance at full budget and needs no new measurement.
package robust

import (
	"fmt"
	"math"
	"sort"
)

// Matrix is a payoff matrix over detectors and the attack mechanisms they are scored
// against. A value object, validated on construction and compared by value.
//
// The payoff is the caller's unit — a detection rate or a detection count — and only
// consistency within one matrix is required. Rates make the mechanisms comparable, which
// matters whenever an adversary chooses between them; counts answer "how many would this
// have found", which is what an operator asks.
type Matrix struct {
	arms       []string
	mechanisms []string
	payoff     [][]float64
}

// NewMatrix returns the payoff matrix with the given detectors, mechanisms and payoffs,
// where payoff[i][j] is what arms[i] achieves against mechanisms[j].
//
// Names must be unique and non-empty: they are how every result below is reported, and a
// duplicate would silently make one row unreachable.
func NewMatrix(arms, mechanisms []string, payoff [][]float64) (Matrix, error) {
	if len(arms) == 0 {
		return Matrix{}, fmt.Errorf("%w: no arms", ErrMatrix)
	}
	if len(mechanisms) == 0 {
		return Matrix{}, fmt.Errorf("%w: no mechanisms", ErrMatrix)
	}
	if len(payoff) != len(arms) {
		return Matrix{}, fmt.Errorf(
			"%w: %d payoff rows for %d arms", ErrMatrix, len(payoff), len(arms))
	}
	for _, names := range [][]string{arms, mechanisms} {
		seen := make(map[string]bool, len(names))
		for _, n := range names {
			if n == "" {
				return Matrix{}, fmt.Errorf("%w: an empty name", ErrMatrix)
			}
			if seen[n] {
				return Matrix{}, fmt.Errorf("%w: %q appears twice", ErrMatrix, n)
			}
			seen[n] = true
		}
	}
	for i, row := range payoff {
		if len(row) != len(mechanisms) {
			return Matrix{}, fmt.Errorf(
				"%w: arm %q has %d payoffs for %d mechanisms",
				ErrMatrix, arms[i], len(row), len(mechanisms))
		}
		for j, v := range row {
			if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
				return Matrix{}, fmt.Errorf(
					"%w: payoff for %q against %q is %v", ErrMatrix, arms[i], mechanisms[j], v)
			}
		}
	}
	return Matrix{
		arms:       append([]string(nil), arms...),
		mechanisms: append([]string(nil), mechanisms...),
		payoff:     clone(payoff),
	}, nil
}

// Arms returns the detector names, in the order given.
func (m Matrix) Arms() []string { return append([]string(nil), m.arms...) }

// Mechanisms returns the mechanism names, in the order given.
func (m Matrix) Mechanisms() []string { return append([]string(nil), m.mechanisms...) }

// Allocation is a solved allocation: what it guarantees, how it divides the budget, and the
// mechanism mix that holds it to that guarantee.
//
// BestPure is reported beside it because it is the allocation actually in use — one detector,
// the whole budget — and the gain from mixing is the difference the caller is deciding about.
type Allocation struct {
	Value         float64
	Mix           map[string]float64
	Response      map[string]float64
	BestPure      string
	BestPureValue float64
}

// GainFromMixing is what the mixture guarantees over the best single detector. It is zero or
// negative exactly when no mixture improves on using one detector alone.
func (a Allocation) GainFromMixing() float64 { return a.Value - a.BestPureValue }

// Support returns the detectors carrying weight above tol, heaviest first. The equilibrium
// of a matrix this sparse usually rests on two or three arms, and reading the support is how
// one sees which.
func (a Allocation) Support(tol float64) []string {
	out := make([]string, 0, len(a.Mix))
	for arm, w := range a.Mix {
		if w > tol {
			out = append(out, arm)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if a.Mix[out[i]] != a.Mix[out[j]] {
			return a.Mix[out[i]] > a.Mix[out[j]]
		}
		return out[i] < out[j]
	})
	return out
}

// Maximin returns the allocation maximising the payoff against the worst mechanism for it.
//
// On a portfolio with a mechanism no detector reaches, the value is zero and the answer is
// degenerate: the adversary plays that mechanism and every allocation is equally bad.
// [Matrix.Unreachable] names those mechanisms, and it should be consulted before this value
// is reported as though it graded the allocation rather than the coverage.
func (m Matrix) Maximin() (Allocation, error) { return m.solve(m.payoff) }

// CompetitiveRatio returns the allocation maximising the worst-case fraction of the
// reachable payoff it retains.
//
// Each mechanism is divided by the best any single detector achieves against it, so the
// objective asks how much of what was available this allocation keeps rather than how much
// it finds — which is what makes it well posed where [Matrix.Maximin] is not. Mechanisms no
// detector reaches carry no information under this normalisation, since every allocation
// ties at zero, and are dropped; they are returned so the omission is visible.
func (m Matrix) CompetitiveRatio() (Allocation, []string, error) {
	unreachable := m.Unreachable()
	keep := make([]int, 0, len(m.mechanisms))
	for j := range m.mechanisms {
		if m.columnMax(j) > 0 {
			keep = append(keep, j)
		}
	}
	if len(keep) == 0 {
		return Allocation{}, unreachable, fmt.Errorf(
			"%w: no mechanism is reached by any arm", ErrMatrix)
	}

	scaled := make([][]float64, len(m.arms))
	for i := range m.arms {
		scaled[i] = make([]float64, len(keep))
		for k, j := range keep {
			scaled[i][k] = m.payoff[i][j] / m.columnMax(j)
		}
	}
	kept := make([]string, len(keep))
	for k, j := range keep {
		kept[k] = m.mechanisms[j]
	}
	sub, err := Matrix{arms: m.arms, mechanisms: kept, payoff: scaled}.solve(scaled)
	return sub, unreachable, err
}

// Unreachable names the mechanisms no arm reaches at all. Each one caps the value of the
// whole game at zero, so this is the first thing to read off a matrix.
func (m Matrix) Unreachable() []string {
	var out []string
	for j, name := range m.mechanisms {
		if m.columnMax(j) <= 0 {
			out = append(out, name)
		}
	}
	return out
}

// Retained reports, per mechanism, the fraction of the reachable payoff a mixture keeps. At
// the competitive-ratio optimum these are equal across the mechanisms in the support, which
// is the signature of an equalising equilibrium and worth showing rather than asserting.
func (m Matrix) Retained(mix map[string]float64) (map[string]float64, error) {
	w, err := m.weights(mix)
	if err != nil {
		return nil, err
	}
	out := make(map[string]float64, len(m.mechanisms))
	for j, name := range m.mechanisms {
		best := m.columnMax(j)
		if best <= 0 {
			continue
		}
		got := 0.0
		for i := range m.arms {
			got += w[i] * m.payoff[i][j]
		}
		out[name] = got / best
	}
	return out, nil
}

// Blend returns the expected payoff per mechanism of randomising over detectors with the
// given weights: one detector at full budget, chosen by lottery.
//
// This is the linearity in the package comment used on purpose. Because each detector runs
// at the full budget, every term is that detector's recorded performance at the budget
// already measured, so the mixture is exactly evaluable from existing runs. It is a
// different object from dividing the budget, which gives every detector a fraction of its
// depth and is what the paper's split search measures.
func (m Matrix) Blend(mix map[string]float64) (map[string]float64, error) {
	w, err := m.weights(mix)
	if err != nil {
		return nil, err
	}
	out := make(map[string]float64, len(m.mechanisms))
	for j, name := range m.mechanisms {
		got := 0.0
		for i := range m.arms {
			got += w[i] * m.payoff[i][j]
		}
		out[name] = got
	}
	return out, nil
}

// Price is what a worst-case guarantee costs in expected payoff, under a stated prior.
//
// Both columns are reported because neither alone is a decision. The robust allocation is
// worse in expectation by construction; whether that is worth its guarantee depends on
// whether the operator is graded on how much is found or on not being blind to a mechanism.
type Price struct {
	Prior           map[string]float64
	BayesArm        string
	BayesExpected   float64
	BayesWorstCase  float64
	RobustExpected  float64
	RobustWorstCase float64
}

// ExpectedGivenUp is the expected payoff the robust allocation forgoes.
func (p Price) ExpectedGivenUp() float64 { return p.BayesExpected - p.RobustExpected }

// WorstCaseBought is the worst-case payoff the robust allocation gains.
func (p Price) WorstCaseBought() float64 { return p.RobustWorstCase - p.BayesWorstCase }

// PriceOfRobustness prices the given allocation against the best single detector under a
// stated prior over mechanisms.
//
// The prior is normalised, and the weights it is given are the caller's to justify: this
// reports an exchange rate at a stated belief and does not estimate the belief.
func (m Matrix) PriceOfRobustness(mix, prior map[string]float64) (Price, error) {
	w, err := m.weights(mix)
	if err != nil {
		return Price{}, err
	}
	p, err := m.normalisedPrior(prior)
	if err != nil {
		return Price{}, err
	}

	bayesArm, bayesExpected, bayesWorst := "", math.Inf(-1), 0.0
	for i, arm := range m.arms {
		expected := 0.0
		for j := range m.mechanisms {
			expected += p[j] * m.payoff[i][j]
		}
		if expected > bayesExpected {
			bayesArm, bayesExpected, bayesWorst = arm, expected, m.rowMin(i)
		}
	}

	robustExpected, robustWorst := 0.0, math.Inf(1)
	for j := range m.mechanisms {
		got := 0.0
		for i := range m.arms {
			got += w[i] * m.payoff[i][j]
		}
		robustExpected += p[j] * got
		robustWorst = math.Min(robustWorst, got)
	}

	named := make(map[string]float64, len(p))
	for j, name := range m.mechanisms {
		named[name] = p[j]
	}
	return Price{
		Prior:           named,
		BayesArm:        bayesArm,
		BayesExpected:   bayesExpected,
		BayesWorstCase:  bayesWorst,
		RobustExpected:  robustExpected,
		RobustWorstCase: robustWorst,
	}, nil
}

// WithAttackerCost returns the matrix with each mechanism's payoff raised by lambda times
// its cost to the adversary.
//
// The degenerate equilibrium of an uncovered mechanism assumes that mechanism is free to
// mount. A sustained low-rate campaign is not: it is slow by construction, and an adversary
// trading detection against dwell time does not choose it merely because it is unseen.
// Charging that cost is a modelling decision and lambda is a stated parameter, never fitted
// — a cost fitted to the same labels the allocation is scored on would make the equilibrium
// a restatement of the corpus.
func (m Matrix) WithAttackerCost(cost map[string]float64, lambda float64) (Matrix, error) {
	if math.IsNaN(lambda) || math.IsInf(lambda, 0) || lambda < 0 {
		return Matrix{}, fmt.Errorf("%w: lambda is %v, want a non-negative finite number",
			ErrMatrix, lambda)
	}
	shifted := clone(m.payoff)
	for j, name := range m.mechanisms {
		c, ok := cost[name]
		if !ok {
			return Matrix{}, fmt.Errorf("%w: no attacker cost for %q", ErrMatrix, name)
		}
		if math.IsNaN(c) || math.IsInf(c, 0) || c < 0 {
			return Matrix{}, fmt.Errorf("%w: attacker cost for %q is %v", ErrMatrix, name, c)
		}
		for i := range shifted {
			shifted[i][j] += lambda * c
		}
	}
	return Matrix{arms: m.arms, mechanisms: m.mechanisms, payoff: shifted}, nil
}

// Shadow is the gain in the worst-case guarantee from improving one detector against one
// mechanism. It answers "where is the next unit of work worth spending", which on a sparse
// matrix is rarely the detector the headline names.
type Shadow struct {
	Arm       string
	Mechanism string
	Current   float64
	Gain      float64
}

// ShadowPrices returns the cells whose improvement by delta raises the maximin value,
// largest gain first. A payoff is not raised past ceiling; pass an infinite ceiling where
// the unit has no natural maximum.
func (m Matrix) ShadowPrices(delta, ceiling float64) ([]Shadow, error) {
	if math.IsNaN(delta) || math.IsInf(delta, 0) || delta <= 0 {
		return nil, fmt.Errorf("%w: delta is %v, want a positive finite number", ErrMatrix, delta)
	}
	base, err := m.Maximin()
	if err != nil {
		return nil, err
	}

	var out []Shadow
	for i, arm := range m.arms {
		for j, mech := range m.mechanisms {
			bumped := clone(m.payoff)
			bumped[i][j] = math.Min(ceiling, bumped[i][j]+delta)
			trial, err := Matrix{arms: m.arms, mechanisms: m.mechanisms, payoff: bumped}.Maximin()
			if err != nil {
				return nil, err
			}
			if gain := trial.Value - base.Value; gain > 1e-9 {
				out = append(out, Shadow{
					Arm: arm, Mechanism: mech, Current: m.payoff[i][j], Gain: gain,
				})
			}
		}
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].Gain != out[b].Gain {
			return out[a].Gain > out[b].Gain
		}
		if out[a].Arm != out[b].Arm {
			return out[a].Arm < out[b].Arm
		}
		return out[a].Mechanism < out[b].Mechanism
	})
	return out, nil
}

// ---------------------------------------------------------------------------
// internals
// ---------------------------------------------------------------------------

func (m Matrix) solve(payoff [][]float64) (Allocation, error) {
	g, err := Solve(payoff)
	if err != nil {
		return Allocation{}, err
	}

	mix := make(map[string]float64, len(m.arms))
	for i, arm := range m.arms {
		mix[arm] = g.Row[i]
	}
	response := make(map[string]float64, len(m.mechanisms))
	for j, mech := range m.mechanisms {
		response[mech] = g.Column[j]
	}

	bestArm, bestValue := "", math.Inf(-1)
	for i, arm := range m.arms {
		lo := math.Inf(1)
		for j := range payoff[i] {
			lo = math.Min(lo, payoff[i][j])
		}
		if lo > bestValue {
			bestArm, bestValue = arm, lo
		}
	}
	return Allocation{
		Value: g.Value, Mix: mix, Response: response,
		BestPure: bestArm, BestPureValue: bestValue,
	}, nil
}

// weights resolves a named mixture into positional weights, requiring it to be a
// distribution over exactly the matrix's arms. A mixture naming an arm the matrix does not
// hold is a caller error and not a zero.
func (m Matrix) weights(mix map[string]float64) ([]float64, error) {
	if len(mix) == 0 {
		return nil, fmt.Errorf("%w: the mixture is empty", ErrMatrix)
	}
	index := make(map[string]int, len(m.arms))
	for i, arm := range m.arms {
		index[arm] = i
	}
	w := make([]float64, len(m.arms))
	total := 0.0
	for arm, x := range mix {
		i, ok := index[arm]
		if !ok {
			return nil, fmt.Errorf("%w: the mixture names %q, which is not an arm", ErrMatrix, arm)
		}
		if math.IsNaN(x) || math.IsInf(x, 0) || x < 0 {
			return nil, fmt.Errorf("%w: the weight on %q is %v", ErrMatrix, arm, x)
		}
		w[i] = x
		total += x
	}
	if math.Abs(total-1) > 1e-6 {
		return nil, fmt.Errorf("%w: the mixture sums to %v, want 1", ErrMatrix, total)
	}
	return w, nil
}

func (m Matrix) normalisedPrior(prior map[string]float64) ([]float64, error) {
	if len(prior) == 0 {
		return nil, fmt.Errorf("%w: the prior is empty", ErrMatrix)
	}
	p := make([]float64, len(m.mechanisms))
	index := make(map[string]int, len(m.mechanisms))
	for j, mech := range m.mechanisms {
		index[mech] = j
	}
	total := 0.0
	for mech, x := range prior {
		j, ok := index[mech]
		if !ok {
			return nil, fmt.Errorf(
				"%w: the prior names %q, which is not a mechanism", ErrMatrix, mech)
		}
		if math.IsNaN(x) || math.IsInf(x, 0) || x < 0 {
			return nil, fmt.Errorf("%w: the prior weight on %q is %v", ErrMatrix, mech, x)
		}
		p[j] = x
		total += x
	}
	if total <= 0 {
		return nil, fmt.Errorf("%w: the prior has no mass", ErrMatrix)
	}
	for j := range p {
		p[j] /= total
	}
	return p, nil
}

func (m Matrix) columnMax(j int) float64 {
	hi := 0.0
	for i := range m.arms {
		hi = math.Max(hi, m.payoff[i][j])
	}
	return hi
}

func (m Matrix) rowMin(i int) float64 {
	lo := math.Inf(1)
	for j := range m.mechanisms {
		lo = math.Min(lo, m.payoff[i][j])
	}
	return lo
}

func clone(a [][]float64) [][]float64 {
	out := make([][]float64, len(a))
	for i := range a {
		out[i] = append([]float64(nil), a[i]...)
	}
	return out
}
