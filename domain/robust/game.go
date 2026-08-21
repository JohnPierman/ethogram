package robust

import (
	"errors"
	"fmt"
	"math"
)

// ErrMatrix reports a payoff matrix that is not rectangular, not finite, or empty.
var ErrMatrix = errors.New("robust: the payoff matrix is malformed")

// ErrSolver reports that the solver reached a state its own invariants forbid. It exists so
// that a defect in this file surfaces as an error rather than as a plausible equilibrium.
var ErrSolver = errors.New("robust: the solver failed an internal consistency check")

// Game is a solved two-person zero-sum game: the value, the maximiser's strategy over rows,
// and the minimiser's strategy over columns.
//
// Both strategies are distributions. Value is what the maximiser can guarantee and what the
// minimiser can hold it to; those two numbers coincide, which is von Neumann's theorem and
// is what makes a single Value meaningful.
type Game struct {
	Value  float64
	Row    []float64
	Column []float64
}

// Solve returns the equilibrium of the zero-sum game whose payoff to the row player is a.
//
// The reduction is Dantzig's. Shifting every entry by a constant moves the value by that
// constant and leaves both optimal strategies unchanged, so the matrix is first shifted to
// be strictly positive; on a positive matrix the minimiser's problem
//
//	max 1'y  subject to  A y <= 1,  y >= 0
//
// is feasible at the origin and bounded, and its optimum gives the value as 1/(1'y) and the
// strategy as y/(1'y). The maximiser's strategy is the same computation on -A', the game
// with the roles exchanged, whose value is the negation of this one — which the solver then
// checks, because two independent derivations of one number are worth comparing.
//
// Ties are broken by Bland's rule. That is a determinism requirement rather than a
// performance one: a recorded equilibrium that a second run disagrees with is not a
// measurement, and Bland's rule also removes the possibility of cycling.
func Solve(a [][]float64) (Game, error) {
	if err := validate(a); err != nil {
		return Game{}, err
	}

	column, value, err := minimiserOptimum(a)
	if err != nil {
		return Game{}, err
	}
	row, negated, err := minimiserOptimum(negateTranspose(a))
	if err != nil {
		return Game{}, err
	}
	if math.Abs(value+negated) > 1e-7 {
		return Game{}, fmt.Errorf(
			"%w: the value is %v from one side and %v from the other", ErrSolver, value, -negated)
	}
	return Game{Value: value, Row: row, Column: column}, nil
}

// validate rejects a matrix the solver cannot index into or cannot shift into a positive
// one. Every case here is a programming error in the caller rather than a property of a
// measurement, so it is reported and not repaired.
func validate(a [][]float64) error {
	if len(a) == 0 {
		return fmt.Errorf("%w: no rows", ErrMatrix)
	}
	width := len(a[0])
	if width == 0 {
		return fmt.Errorf("%w: no columns", ErrMatrix)
	}
	for i, row := range a {
		if len(row) != width {
			return fmt.Errorf("%w: row %d has %d columns, want %d", ErrMatrix, i, len(row), width)
		}
		for j, v := range row {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return fmt.Errorf("%w: entry (%d, %d) is %v", ErrMatrix, i, j, v)
			}
		}
	}
	return nil
}

// negateTranspose returns the game with the two roles exchanged. Its value is the negation
// of the original's, and its minimiser's optimum is the original's maximiser's optimum.
func negateTranspose(a [][]float64) [][]float64 {
	out := make([][]float64, len(a[0]))
	for j := range out {
		out[j] = make([]float64, len(a))
		for i := range a {
			out[j][i] = -a[i][j]
		}
	}
	return out
}

// minimiserOptimum returns the column player's optimal strategy and the value of the game.
func minimiserOptimum(a [][]float64) ([]float64, float64, error) {
	shift := 1 - minEntry(a)
	shifted := make([][]float64, len(a))
	for i := range a {
		shifted[i] = make([]float64, len(a[i]))
		for j := range a[i] {
			shifted[i][j] = a[i][j] + shift
		}
	}

	y, total, err := maximiseOnes(shifted)
	if err != nil {
		return nil, 0, err
	}
	if total <= 0 {
		return nil, 0, fmt.Errorf("%w: the shifted programme optimised to %v", ErrSolver, total)
	}

	scale := 1 / total
	strategy := make([]float64, len(y))
	for j := range y {
		strategy[j] = y[j] * scale
	}
	return strategy, scale - shift, nil
}

func minEntry(a [][]float64) float64 {
	lo := math.Inf(1)
	for _, row := range a {
		for _, v := range row {
			lo = math.Min(lo, v)
		}
	}
	return lo
}

// maximiseOnes solves max 1'y subject to a·y <= 1, y >= 0, for a with every entry at least
// 1. It returns the optimal y and the optimal objective.
//
// Those preconditions are what make a bare primal simplex sufficient: the origin is
// feasible, so no first phase is needed, and every entry being at least 1 forces
// sum(y) <= 1, so the programme is bounded.
func maximiseOnes(a [][]float64) ([]float64, float64, error) {
	rows, cols := len(a), len(a[0])
	// Columns 0..cols-1 are the structural variables, cols..cols+rows-1 the slacks, and the
	// final column the right-hand side.
	width := cols + rows + 1
	rhs := width - 1

	tab := make([][]float64, rows)
	basis := make([]int, rows)
	for i := range tab {
		tab[i] = make([]float64, width)
		copy(tab[i], a[i])
		tab[i][cols+i] = 1
		tab[i][rhs] = 1
		basis[i] = cols + i
	}
	obj := make([]float64, width)
	for j := 0; j < cols; j++ {
		obj[j] = -1
	}

	const eps = 1e-11
	// The bound is Bland's rule's guarantee of no repeated basis, with room to spare; it is
	// a backstop against a defect here rather than an expected limit.
	maxPivots := 50 * (rows + cols) * (rows + cols)
	for pivots := 0; ; pivots++ {
		if pivots > maxPivots {
			return nil, 0, fmt.Errorf("%w: no optimum after %d pivots", ErrSolver, pivots)
		}

		enter := -1
		for j := 0; j < width-1; j++ {
			if obj[j] < -eps {
				enter = j
				break
			}
		}
		if enter < 0 {
			break
		}

		leave, best := -1, math.Inf(1)
		for i := 0; i < rows; i++ {
			if tab[i][enter] <= eps {
				continue
			}
			ratio := tab[i][rhs] / tab[i][enter]
			if ratio < best-eps || (ratio < best+eps && leave >= 0 && basis[i] < basis[leave]) {
				leave, best = i, ratio
			}
		}
		if leave < 0 {
			return nil, 0, fmt.Errorf("%w: the programme is unbounded", ErrSolver)
		}

		pivot(tab, obj, basis, leave, enter, rows, width)
	}

	y := make([]float64, cols)
	for i := 0; i < rows; i++ {
		if basis[i] < cols {
			y[basis[i]] = tab[i][rhs]
		}
	}
	return y, obj[rhs], nil
}

// pivot performs one simplex exchange, bringing enter into the basis in place of the
// variable currently basic in row leave.
func pivot(tab [][]float64, obj []float64, basis []int, leave, enter, rows, width int) {
	p := tab[leave][enter]
	for j := 0; j < width; j++ {
		tab[leave][j] /= p
	}
	for i := 0; i < rows; i++ {
		if i == leave {
			continue
		}
		if f := tab[i][enter]; f != 0 {
			for j := 0; j < width; j++ {
				tab[i][j] -= f * tab[leave][j]
			}
		}
	}
	if f := obj[enter]; f != 0 {
		for j := 0; j < width; j++ {
			obj[j] -= f * tab[leave][j]
		}
	}
	basis[leave] = enter
}
