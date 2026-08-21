package robust_test

import (
	"math"
	"testing"

	"github.com/JohnPierman/ethogram/domain/robust"
)

const tol = 1e-9

func closeTo(got, want float64) bool { return math.Abs(got-want) < 1e-6 }

// TestSolveRockPaperScissors. The canonical game with no pure equilibrium: every strategy
// beats one and loses to one, so both sides must randomise uniformly and the value is zero.
// A solver that returns a pure strategy here is not solving the game.
func TestSolveRockPaperScissors(t *testing.T) {
	a := [][]float64{
		{0, -1, 1},
		{1, 0, -1},
		{-1, 1, 0},
	}
	g, err := robust.Solve(a)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if !closeTo(g.Value, 0) {
		t.Errorf("value = %v, want 0", g.Value)
	}
	for i, w := range g.Row {
		if !closeTo(w, 1.0/3.0) {
			t.Errorf("row[%d] = %v, want 1/3", i, w)
		}
	}
	for j, q := range g.Column {
		if !closeTo(q, 1.0/3.0) {
			t.Errorf("column[%d] = %v, want 1/3", j, q)
		}
	}
}

// TestSolveMatchingPennies. A two-by-two game whose only equilibrium is the even mixture,
// included because it exercises the shift the solver applies to make the matrix positive:
// the value is zero and lies strictly inside the range of the entries.
func TestSolveMatchingPennies(t *testing.T) {
	g, err := robust.Solve([][]float64{{1, -1}, {-1, 1}})
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if !closeTo(g.Value, 0) {
		t.Errorf("value = %v, want 0", g.Value)
	}
	for i, w := range g.Row {
		if !closeTo(w, 0.5) {
			t.Errorf("row[%d] = %v, want 0.5", i, w)
		}
	}
}

// TestSolveFindsPureSaddleWhenOneExists. Where a saddle point exists the solver must return
// it rather than a mixture that happens to share its value, because the strategy is the
// answer and not only the number. Row 1 dominates and column 0 is the minimiser's best
// reply, so the equilibrium is pure at their intersection.
func TestSolveFindsPureSaddleWhenOneExists(t *testing.T) {
	a := [][]float64{
		{3, 5, 7},
		{4, 6, 8},
	}
	g, err := robust.Solve(a)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if !closeTo(g.Value, 4) {
		t.Errorf("value = %v, want 4", g.Value)
	}
	if !closeTo(g.Row[1], 1) {
		t.Errorf("row = %v, want all weight on the dominant row 1", g.Row)
	}
	if !closeTo(g.Column[0], 1) {
		t.Errorf("column = %v, want all weight on column 0", g.Column)
	}
}

// TestSolveZeroColumnGivesValueZero. The case this package exists to expose. A column every
// row scores zero on is a mechanism no detector reaches, and it fixes the value of the whole
// game at zero however good the other columns are: the minimiser simply always plays it.
func TestSolveZeroColumnGivesValueZero(t *testing.T) {
	a := [][]float64{
		{0.9, 0},
		{0.8, 0},
	}
	g, err := robust.Solve(a)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if !closeTo(g.Value, 0) {
		t.Errorf("value = %v, want 0 — a zero column caps the game", g.Value)
	}
	if !closeTo(g.Column[1], 1) {
		t.Errorf("column = %v, want all weight on the uncovered mechanism", g.Column)
	}
}

// TestSolveValueIsBoundedByThePureStrategies. A property the value must satisfy on any
// matrix: mixing cannot do worse than the best row's worst case, nor better than the
// minimiser's best column. Checked on an asymmetric matrix with no structure to exploit.
func TestSolveValueIsBoundedByThePureStrategies(t *testing.T) {
	a := [][]float64{
		{0.25, 0.37, 0.00, 0.32},
		{0.36, 0.65, 0.16, 0.31},
		{0.00, 0.00, 0.00, 1.00},
	}
	g, err := robust.Solve(a)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	maximin := math.Inf(-1)
	for _, row := range a {
		lo := math.Inf(1)
		for _, v := range row {
			lo = math.Min(lo, v)
		}
		maximin = math.Max(maximin, lo)
	}
	minimax := math.Inf(1)
	for j := range a[0] {
		hi := math.Inf(-1)
		for i := range a {
			hi = math.Max(hi, a[i][j])
		}
		minimax = math.Min(minimax, hi)
	}
	if g.Value < maximin-tol || g.Value > minimax+tol {
		t.Errorf("value %v outside [%v, %v]", g.Value, maximin, minimax)
	}
}

// TestSolveStrategiesAreDistributions. Both sides' answers are probability distributions, so
// they must be non-negative and sum to one. A solver that returns an unnormalised vector
// produces plausible-looking weights that are not a strategy.
func TestSolveStrategiesAreDistributions(t *testing.T) {
	g, err := robust.Solve([][]float64{{0.25, 0.37, 0}, {0.36, 0.65, 0.16}})
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	for _, v := range [][]float64{g.Row, g.Column} {
		sum := 0.0
		for _, x := range v {
			if x < -tol {
				t.Errorf("negative weight %v in %v", x, v)
			}
			sum += x
		}
		if !closeTo(sum, 1) {
			t.Errorf("weights %v sum to %v, want 1", v, sum)
		}
	}
}

// TestSolveEquilibriumIsMutuallyUnimprovable. The defining property, checked directly rather
// than trusted: neither side can gain by deviating to any pure strategy. This is what makes
// the reported value a guarantee and not merely the output of an optimiser.
func TestSolveEquilibriumIsMutuallyUnimprovable(t *testing.T) {
	a := [][]float64{
		{0.25, 0.37, 0.00, 0.00, 0.25},
		{0.36, 0.65, 0.00, 0.16, 0.53},
		{0.00, 0.00, 0.04, 0.00, 0.09},
		{0.00, 0.00, 0.00, 0.00, 1.00},
	}
	g, err := robust.Solve(a)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	// No column the minimiser could switch to pays it less than the value.
	for j := range a[0] {
		got := 0.0
		for i := range a {
			got += g.Row[i] * a[i][j]
		}
		if got < g.Value-1e-6 {
			t.Errorf("column %d pays %v, below the value %v", j, got, g.Value)
		}
	}
	// No row the maximiser could switch to earns it more than the value.
	for i := range a {
		got := 0.0
		for j := range a[i] {
			got += g.Column[j] * a[i][j]
		}
		if got > g.Value+1e-6 {
			t.Errorf("row %d earns %v, above the value %v", i, got, g.Value)
		}
	}
}

// TestSolveRejectsMalformedMatrices. The solver is arithmetic over a caller-supplied matrix,
// so a ragged or empty one is a programming error and must be reported rather than indexed
// into.
func TestSolveRejectsMalformedMatrices(t *testing.T) {
	for name, a := range map[string][][]float64{
		"no rows":            {},
		"no columns":         {{}},
		"ragged":             {{1, 2}, {3}},
		"not finite":         {{math.NaN(), 1}, {1, 1}},
		"positive infinity":  {{math.Inf(1), 1}, {1, 1}},
		"ragged second row":  {{1, 2, 3}, {1, 2}},
		"empty row after ok": {{1}, {}},
	} {
		if _, err := robust.Solve(a); err == nil {
			t.Errorf("%s: accepted, want an error", name)
		}
	}
}

// TestSolveIsDeterministic. R4: nothing in the scoring path may depend on a random draw. The
// solver uses Bland's rule precisely so that repeated calls cannot disagree, and a solver
// that ties differently between runs would make a recorded equilibrium unreproducible.
func TestSolveIsDeterministic(t *testing.T) {
	a := [][]float64{
		{0.25, 0.37, 0.00, 0.32},
		{0.36, 0.65, 0.16, 0.31},
		{0.00, 0.00, 0.00, 1.00},
		{0.00, 0.00, 0.00, 1.00},
	}
	first, err := robust.Solve(a)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	for i := 0; i < 25; i++ {
		again, err := robust.Solve(a)
		if err != nil {
			t.Fatalf("Solve: %v", err)
		}
		if again.Value != first.Value {
			t.Fatalf("value changed between runs: %v then %v", first.Value, again.Value)
		}
		for k := range first.Row {
			if again.Row[k] != first.Row[k] {
				t.Fatalf("row strategy changed between runs at %d", k)
			}
		}
	}
}
