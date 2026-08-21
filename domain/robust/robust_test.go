package robust_test

import (
	"math"
	"testing"

	"github.com/JohnPierman/ethogram/domain/robust"
)

// The measured payoff matrix at 1000 alerts a day, from the paper's per-mechanism table on
// `lanl-inj-b1000-weighted-d7-14-005`, with the `lof` baseline included because it is the
// only method that reaches low-and-slow at all. Counts, over the planted totals below.
var (
	mechanisms = []string{
		"spray", "lateral", "off-hrs", "priv-esc", "low+slow", "takeover", "real",
	}
	planted = []float64{320, 40, 64, 24, 288, 120, 549}
	arms    = []string{
		"novelty", "noveltyrate", "pairing", "timing", "volume", "marginal", "lof",
	}
	caught = [][]float64{
		{80, 15, 0, 0, 0, 30, 178},
		{117, 26, 0, 4, 0, 64, 173},
		{0, 0, 0, 0, 0, 12, 130},
		{0, 0, 3, 0, 0, 11, 7},
		{0, 0, 0, 0, 0, 0, 0},
		{0, 0, 0, 0, 0, 120, 0},
		{0, 0, 0, 0, 12, 0, 0},
	}
)

// rateMatrix converts the recorded counts into detection rates, which is the unit an
// adversary choosing between mechanisms compares: the mechanisms have very different planted
// totals and a count would make the largest of them look like the best covered.
func rateMatrix(t *testing.T) robust.Matrix {
	t.Helper()
	rate := make([][]float64, len(arms))
	for i := range arms {
		rate[i] = make([]float64, len(mechanisms))
		for j := range mechanisms {
			rate[i][j] = caught[i][j] / planted[j]
		}
	}
	m, err := robust.NewMatrix(arms, mechanisms, rate)
	if err != nil {
		t.Fatalf("NewMatrix: %v", err)
	}
	return m
}

func near(t *testing.T, label string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("%s = %.12f, want %.12f", label, got, want)
	}
}

// TestNewMatrixRejectsMalformedInput. The matrix is a value object and every case here is a
// caller defect that would otherwise produce a plausible equilibrium over the wrong
// rectangle. A duplicate name is the subtle one: it makes one row unreachable by name while
// still contributing to the solve.
func TestNewMatrixRejectsMalformedInput(t *testing.T) {
	ok := [][]float64{{0.1, 0.2}, {0.3, 0.4}}
	for name, tc := range map[string]struct {
		arms, mechs []string
		payoff      [][]float64
	}{
		"no arms":            {nil, []string{"a", "b"}, ok},
		"no mechanisms":      {[]string{"x", "y"}, nil, ok},
		"row count mismatch": {[]string{"x"}, []string{"a", "b"}, ok},
		"ragged payoff":      {[]string{"x", "y"}, []string{"a", "b"}, [][]float64{{0.1, 0.2}, {0.3}}},
		"duplicate arm":      {[]string{"x", "x"}, []string{"a", "b"}, ok},
		"duplicate mech":     {[]string{"x", "y"}, []string{"a", "a"}, ok},
		"empty arm name":     {[]string{"x", ""}, []string{"a", "b"}, ok},
		"negative payoff":    {[]string{"x", "y"}, []string{"a", "b"}, [][]float64{{-0.1, 0.2}, {0.3, 0.4}}},
		"payoff not finite":  {[]string{"x", "y"}, []string{"a", "b"}, [][]float64{{math.NaN(), 0.2}, {0.3, 0.4}}},
	} {
		if _, err := robust.NewMatrix(tc.arms, tc.mechs, tc.payoff); err == nil {
			t.Errorf("%s: accepted, want an error", name)
		}
	}
}

// TestUnreachableNamesTheUncoveredMechanism. The single most consequential reading of the
// matrix, and the one a value alone hides: low-and-slow is reached by no arm of the framework
// and only by the `lof` baseline. Without `lof` the column is empty and caps the game.
func TestUnreachableNamesTheUncoveredMechanism(t *testing.T) {
	rate := make([][]float64, len(arms)-1) // every arm except lof
	for i := range rate {
		rate[i] = make([]float64, len(mechanisms))
		for j := range mechanisms {
			rate[i][j] = caught[i][j] / planted[j]
		}
	}
	m, err := robust.NewMatrix(arms[:len(arms)-1], mechanisms, rate)
	if err != nil {
		t.Fatalf("NewMatrix: %v", err)
	}
	got := m.Unreachable()
	if len(got) != 1 || got[0] != "low+slow" {
		t.Fatalf("Unreachable() = %v, want [low+slow]", got)
	}

	a, err := m.Maximin()
	if err != nil {
		t.Fatalf("Maximin: %v", err)
	}
	near(t, "value with an uncovered mechanism", a.Value, 0)
	if a.GainFromMixing() > 1e-9 {
		t.Errorf("GainFromMixing = %v; no mixture can improve on a column of zeros",
			a.GainFromMixing())
	}
	if a.Response["low+slow"] < 1-1e-9 {
		t.Errorf("the adversary's reply is %v, want all weight on low+slow", a.Response)
	}
}

// TestMaximinReproducesTheRecordedEquilibrium. Cross-checks the solver against an
// independent solution of the same programme. The value is asserted exactly because it is
// what the paper reports; the mixture is asserted through the equilibrium property rather
// than cell by cell, since a linear programme may have several optimal vertices of equal
// value and the guarantee is what is being claimed.
func TestMaximinReproducesTheRecordedEquilibrium(t *testing.T) {
	m := rateMatrix(t)
	a, err := m.Maximin()
	if err != nil {
		t.Fatalf("Maximin: %v", err)
	}
	near(t, "maximin value", a.Value, 3.0/154.0)

	if a.BestPureValue != 0 {
		t.Errorf("BestPureValue = %v, want 0: every arm is blind to some mechanism",
			a.BestPureValue)
	}
	if a.GainFromMixing() <= 0 {
		t.Errorf("GainFromMixing = %v, want positive once low-and-slow is covered",
			a.GainFromMixing())
	}
	// novelty scores zero on every mechanism the adversary's reply is supported on, so no
	// optimal mixture can give it weight. This is the finding that bears on the headline.
	if a.Mix["novelty"] > 1e-9 {
		t.Errorf("novelty holds weight %v in the robust equilibrium, want none", a.Mix["novelty"])
	}
	// The guarantee must hold against every mechanism, not only the ones in the support.
	blended, err := m.Blend(a.Mix)
	if err != nil {
		t.Fatalf("Blend: %v", err)
	}
	for mech, got := range blended {
		if got < a.Value-1e-9 {
			t.Errorf("mechanism %s pays %v, below the guarantee %v", mech, got, a.Value)
		}
	}
}

// TestCompetitiveRatioEqualisesAcrossMechanisms. The objective's signature: normalising each
// mechanism by what the best arm reaches against it turns a degenerate programme into one
// whose optimum retains the same fraction of the achievable everywhere. Equalisation is
// shown rather than asserted, because it is the property that makes the single number in the
// paper mean something.
func TestCompetitiveRatioEqualisesAcrossMechanisms(t *testing.T) {
	m := rateMatrix(t)
	a, dropped, err := m.CompetitiveRatio()
	if err != nil {
		t.Fatalf("CompetitiveRatio: %v", err)
	}
	if len(dropped) != 0 {
		t.Errorf("dropped = %v, want none: every mechanism is reachable with lof present", dropped)
	}
	near(t, "competitive-ratio value", a.Value, 8.0/27.0)
	if a.BestPureValue != 0 {
		t.Errorf("BestPureValue = %v, want 0", a.BestPureValue)
	}

	retained, err := m.Retained(a.Mix)
	if err != nil {
		t.Fatalf("Retained: %v", err)
	}
	// Six of the seven mechanisms sit exactly at the guarantee. The seventh, the real
	// campaign, is not binding: its best arm is novelty and the mixture happens to retain
	// slightly more there.
	atLevel := 0
	for mech, frac := range retained {
		if frac < a.Value-1e-9 {
			t.Errorf("mechanism %s retains %v, below the guarantee %v", mech, frac, a.Value)
		}
		if math.Abs(frac-a.Value) < 1e-9 {
			atLevel++
		}
	}
	if atLevel != 6 {
		t.Errorf("%d mechanisms sit at the guarantee, want 6", atLevel)
	}
	near(t, "retained on the real campaign", retained["real"], 0.299625468165)
}

// TestNoPriorWeightedMixtureBeatsTheBestSingleArm. The package's central negative result,
// checked as a property rather than argued. Expected detection is linear in the mixture
// weights, so its maximum over the simplex is at a vertex; a mixture fitted to any belief
// about which attacks are common therefore cannot beat simply choosing the arm that belief
// favours. The vertices and a spread of interior points are compared directly.
func TestNoPriorWeightedMixtureBeatsTheBestSingleArm(t *testing.T) {
	m := rateMatrix(t)
	// A deterministic spread of priors: each mechanism in turn dominant, plus the uniform.
	priors := []map[string]float64{{}}
	for _, mech := range mechanisms {
		p := map[string]float64{}
		for _, other := range mechanisms {
			p[other] = 1
		}
		p[mech] = 12
		priors = append(priors, p)
	}
	for _, mech := range mechanisms {
		priors[0][mech] = 1
	}

	for _, prior := range priors {
		best := math.Inf(-1)
		for _, arm := range arms {
			p, err := m.PriceOfRobustness(map[string]float64{arm: 1}, prior)
			if err != nil {
				t.Fatalf("PriceOfRobustness: %v", err)
			}
			best = math.Max(best, p.RobustExpected)
		}
		// Interior mixtures, deterministically generated, must all fall short.
		for k := 2; k <= 7; k++ {
			mix := map[string]float64{}
			for i := 0; i < k; i++ {
				mix[arms[i]] = 1 / float64(k)
			}
			p, err := m.PriceOfRobustness(mix, prior)
			if err != nil {
				t.Fatalf("PriceOfRobustness: %v", err)
			}
			if p.RobustExpected > best+1e-12 {
				t.Errorf("a %d-arm mixture expects %v, above the best single arm's %v",
					k, p.RobustExpected, best)
			}
		}
	}
}

// TestPriceOfRobustnessReportsBothSidesOfTheExchange. The robust allocation is worse in
// expectation and better in the worst case, by construction. Both numbers are asserted
// because reporting either alone would misrepresent the decision.
func TestPriceOfRobustnessReportsBothSidesOfTheExchange(t *testing.T) {
	m := rateMatrix(t)
	a, _, err := m.CompetitiveRatio()
	if err != nil {
		t.Fatalf("CompetitiveRatio: %v", err)
	}
	// A mix over mechanism prevalence weighted towards credential attacks and takeover.
	prior := map[string]float64{
		"spray": 0.30, "lateral": 0.12, "off-hrs": 0.08, "priv-esc": 0.05,
		"low+slow": 0.10, "takeover": 0.30, "real": 0.05,
	}
	p, err := m.PriceOfRobustness(a.Mix, prior)
	if err != nil {
		t.Fatalf("PriceOfRobustness: %v", err)
	}
	if p.BayesArm != "noveltyrate" {
		t.Errorf("BayesArm = %q, want noveltyrate", p.BayesArm)
	}
	near(t, "Bayes expected rate", p.BayesExpected, 0.371776753188)
	near(t, "Bayes worst case", p.BayesWorstCase, 0)
	near(t, "robust expected rate", p.RobustExpected, 0.154172131148)
	near(t, "robust worst case", p.RobustWorstCase, 0.012345679012)
	if p.ExpectedGivenUp() <= 0 {
		t.Errorf("ExpectedGivenUp = %v, want positive", p.ExpectedGivenUp())
	}
	if p.WorstCaseBought() <= 0 {
		t.Errorf("WorstCaseBought = %v, want positive", p.WorstCaseBought())
	}
}

// TestAttackerCostRemovesTheSlowMechanismFromTheReply. The value-zero equilibrium assumes an
// uncovered mechanism is free to mount. Low-and-slow is slow by construction, so charging any
// appreciable cost for it should take it out of the adversary's reply and raise the value.
func TestAttackerCostRemovesTheSlowMechanismFromTheReply(t *testing.T) {
	m := rateMatrix(t)
	cost := map[string]float64{
		"spray": 1, "lateral": 3, "off-hrs": 1, "priv-esc": 2,
		"low+slow": 12, "takeover": 4, "real": 4,
	}
	free, err := m.Maximin()
	if err != nil {
		t.Fatalf("Maximin: %v", err)
	}
	priced, err := m.WithAttackerCost(cost, 0.005)
	if err != nil {
		t.Fatalf("WithAttackerCost: %v", err)
	}
	a, err := priced.Maximin()
	if err != nil {
		t.Fatalf("Maximin: %v", err)
	}
	if a.Value <= free.Value {
		t.Errorf("value %v did not rise above the cost-free %v", a.Value, free.Value)
	}
	if a.Response["low+slow"] > 1e-9 {
		t.Errorf("low+slow still carries reply weight %v once it is priced",
			a.Response["low+slow"])
	}
	// Raising lambda cannot lower the guarantee: the adversary is paying more everywhere.
	previous := a.Value
	for _, lambda := range []float64{0.01, 0.02, 0.05} {
		next, err := m.WithAttackerCost(cost, lambda)
		if err != nil {
			t.Fatalf("WithAttackerCost: %v", err)
		}
		got, err := next.Maximin()
		if err != nil {
			t.Fatalf("Maximin: %v", err)
		}
		if got.Value < previous-1e-9 {
			t.Errorf("value fell from %v to %v as lambda rose to %v",
				previous, got.Value, lambda)
		}
		previous = got.Value
	}
}

// TestAttackerCostRejectsAnUnpricedMechanism. A cost map missing a mechanism would silently
// price it at zero, which is exactly the assumption the parameter exists to remove.
func TestAttackerCostRejectsAnUnpricedMechanism(t *testing.T) {
	m := rateMatrix(t)
	if _, err := m.WithAttackerCost(map[string]float64{"spray": 1}, 0.01); err == nil {
		t.Error("accepted a cost map missing most mechanisms, want an error")
	}
	full := map[string]float64{}
	for _, mech := range mechanisms {
		full[mech] = 1
	}
	if _, err := m.WithAttackerCost(full, -1); err == nil {
		t.Error("accepted a negative lambda, want an error")
	}
}

// TestShadowPricesPointAtTheUncoveredColumns. What the allocation question actually turns on.
// Every cell whose improvement raises the guarantee lies in one of the three mechanisms the
// adversary's reply is supported on — the thinly covered ones — and not one lies in the four
// columns the paper's headline compares arms on. Improving the best arm against the attacks
// it already reaches buys nothing at all here.
func TestShadowPricesPointAtTheUncoveredColumns(t *testing.T) {
	m := rateMatrix(t)
	prices, err := m.ShadowPrices(0.10, 1.0)
	if err != nil {
		t.Fatalf("ShadowPrices: %v", err)
	}
	if len(prices) == 0 {
		t.Fatal("no cell raises the guarantee, want several")
	}
	thin := map[string]bool{"low+slow": true, "off-hrs": true, "priv-esc": true}
	for _, s := range prices {
		if !thin[s.Mechanism] {
			t.Errorf("cell (%s, %s) has a positive shadow price %v; expected only the thinly"+
				" covered mechanisms to matter", s.Arm, s.Mechanism, s.Gain)
		}
	}
	// The largest gains sit in the two mechanisms with the least coverage of all.
	for _, s := range prices[:3] {
		if s.Mechanism != "low+slow" && s.Mechanism != "off-hrs" {
			t.Errorf("the largest shadow prices should be on low+slow or off-hrs, got %v", s)
		}
	}
	// Sorted by gain, largest first.
	for i := 1; i < len(prices); i++ {
		if prices[i-1].Gain < prices[i].Gain {
			t.Errorf("shadow prices are not sorted: %v before %v", prices[i-1], prices[i])
		}
	}
	if _, err := m.ShadowPrices(0, 1.0); err == nil {
		t.Error("accepted a zero delta, want an error")
	}
}

// TestBlendIsExactlyLinear. Blend is the randomised allocation: one detector at full budget,
// chosen by lottery. It is a different object from dividing the budget, and its being exactly
// linear in the weights is why it can be evaluated from runs already recorded rather than
// needing a new one.
func TestBlendIsExactlyLinear(t *testing.T) {
	m := rateMatrix(t)
	half, err := m.Blend(map[string]float64{"novelty": 0.5, "marginal": 0.5})
	if err != nil {
		t.Fatalf("Blend: %v", err)
	}
	pureNovelty, err := m.Blend(map[string]float64{"novelty": 1})
	if err != nil {
		t.Fatalf("Blend: %v", err)
	}
	pureMarginal, err := m.Blend(map[string]float64{"marginal": 1})
	if err != nil {
		t.Fatalf("Blend: %v", err)
	}
	for _, mech := range mechanisms {
		near(t, "blend of "+mech, half[mech], 0.5*pureNovelty[mech]+0.5*pureMarginal[mech])
	}
	// A pure blend must reproduce the row it names.
	near(t, "pure marginal on takeover", pureMarginal["takeover"], 1)
}

// TestBlendRejectsAMixtureThatIsNotADistribution. A mixture naming an unknown arm, or one
// whose weights do not sum to one, is a caller defect; treating it as a zero would report a
// guarantee for an allocation nobody could deploy.
func TestBlendRejectsAMixtureThatIsNotADistribution(t *testing.T) {
	m := rateMatrix(t)
	for name, mix := range map[string]map[string]float64{
		"empty":         {},
		"unknown arm":   {"nosuch": 1},
		"does not sum":  {"novelty": 0.5},
		"negative":      {"novelty": 1.5, "marginal": -0.5},
		"not finite":    {"novelty": math.NaN()},
		"sums above 1":  {"novelty": 0.7, "marginal": 0.7},
		"infinite mass": {"novelty": math.Inf(1)},
	} {
		if _, err := m.Blend(mix); err == nil {
			t.Errorf("%s: accepted, want an error", name)
		}
	}
}

// TestSupportOrdersTheHeaviestArmsFirst. The support is how the equilibrium is read in the
// paper, so its order is part of the result rather than an accident of map iteration.
func TestSupportOrdersTheHeaviestArmsFirst(t *testing.T) {
	m := rateMatrix(t)
	a, err := m.Maximin()
	if err != nil {
		t.Fatalf("Maximin: %v", err)
	}
	support := a.Support(1e-9)
	if len(support) < 2 {
		t.Fatalf("Support = %v, want the equilibrium to rest on several arms", support)
	}
	for i := 1; i < len(support); i++ {
		if a.Mix[support[i-1]] < a.Mix[support[i]] {
			t.Errorf("Support is not ordered by weight: %v", support)
		}
	}
	total := 0.0
	for _, arm := range support {
		total += a.Mix[arm]
	}
	near(t, "support weight", total, 1)
}

// TestPriorMustNameKnownMechanisms. A prior naming something the matrix does not hold is a
// mismatch between the belief and the measurement, and silently ignoring it would report an
// exchange rate for a different question.
func TestPriorMustNameKnownMechanisms(t *testing.T) {
	m := rateMatrix(t)
	mix := map[string]float64{"novelty": 1}
	for name, prior := range map[string]map[string]float64{
		"empty":             {},
		"unknown mechanism": {"phishing": 1},
		"no mass":           {"spray": 0},
		"negative":          {"spray": -1},
	} {
		if _, err := m.PriceOfRobustness(mix, prior); err == nil {
			t.Errorf("%s: accepted, want an error", name)
		}
	}
}
