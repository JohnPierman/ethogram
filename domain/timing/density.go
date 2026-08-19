package timing

import (
	"math"
	"sort"
)

// Density is the fitted circular density of equation (7):
//
//	f̂(φ) = (1/2π) [ 1 + 2 Σ_{h=1}^{H} r_h ( (C_h/W) cos hφ + (S_h/W) sin hφ ) ]
//
// where r_h = I_h(κ)/I_0(κ) are the kernel's Fourier coefficients. The estimator is
// periodic by construction, non-negativity holds up to truncation at H, and the
// evaluation clamps at zero for safety, exactly as §7.2 prescribes.
type Density struct {
	moments *Moments

	// a and b are the per-harmonic products r_h·(C_h/W) and r_h·(S_h/W), computed
	// once at construction. Hoisting them out of every evaluation matters twice
	// over: the scoring path evaluates the density on a G-point grid per event, and
	// recomputing the divisions inside that loop costs thousands of divisions per
	// event; and fixing the association here gives Evaluate and the grid fast path
	// one identical arithmetic, so their results agree bit for bit (R4).
	a, b []float64
}

// NewDensity binds moments to kernel coefficients. Both must share H.
func NewDensity(m *Moments, coefficients []float64) *Density {
	d := &Density{moments: m}
	order := m.H()
	if order > len(coefficients) {
		order = len(coefficients)
	}
	d.a = make([]float64, order)
	d.b = make([]float64, order)
	if m.W > 0 {
		for h := 1; h <= order; h++ {
			d.a[h-1] = coefficients[h-1] * (m.C[h-1] / m.W)
			d.b[h-1] = coefficients[h-1] * (m.S[h-1] / m.W)
		}
	}
	return d
}

// Evaluate returns f̂(φ), clamped at zero.
//
// With no observations (W = 0) every moment is zero and the density is uniform,
// 1/2π: the model correctly declines to find any hour unusual before it has grounds
// to (§7.5), with no special-case prior.
func (d *Density) Evaluate(phi float64) float64 {
	if d.moments.W <= 0 {
		return 1 / (2 * math.Pi)
	}
	sum := 0.0
	for h := 1; h <= len(d.a); h++ {
		hphi := float64(h) * phi
		sum += d.a[h-1]*math.Cos(hphi) + d.b[h-1]*math.Sin(hphi)
	}
	f := (1 + 2*sum) / (2 * math.Pi)
	if f < 0 {
		return 0
	}
	return f
}

// LocalMaxima returns the grid angles at which the density attains a local maximum, in
// ascending angle order, for the §7.7 evidence view, where they are rendered as clock
// times. Neighbours wrap around the circle.
//
// Only maxima above the uniform level 1/2π are reported. Truncation at H leaves a
// small ripple where the series is clamped at zero, and the ripple's positive bumps
// are local maxima of the clamped function without being modes of behaviour: a mode of
// habitual activity is a time more likely than chance, and a peak below the uniform
// density is not that. Rendering ripple peaks as clock times would hand the analyst a
// phantom mode, which is worse than the truncation error it derives from.
func (d *Density) LocalMaxima(grid int) []float64 {
	values := make([]float64, grid)
	for g := range grid {
		values[g] = d.Evaluate(2 * math.Pi * float64(g) / float64(grid))
	}
	uniform := 1 / (2 * math.Pi)
	var out []float64
	for g := range grid {
		prev := values[(g-1+grid)%grid]
		next := values[(g+1)%grid]
		if values[g] > prev && values[g] >= next && values[g] > uniform {
			out = append(out, 2*math.Pi*float64(g)/float64(grid))
		}
	}
	return out
}

// LevelIndex is the level-to-mass lookup of §7.2: f̂ evaluated on a fixed grid of G
// points, sorted, and accumulated, rebuilt when moments change. Scoring is then a
// density evaluation and a binary search. Deterministic, discharging R4.
type LevelIndex struct {
	grid int

	// levels are the grid densities in ascending order; cumulative[i] is the
	// normalised mass of grid cells with density ≤ levels[i]. Accumulation runs in
	// this fixed ascending order, so the float sum is identical on every rebuild.
	levels     []float64
	cumulative []float64

	// floor is the resolution limit: mass below roughly one grid cell cannot be
	// resolved by a G-point lookup, so tail masses are reported no smaller than this
	// rather than extrapolated past what the grid can support. It also keeps the
	// p-value strictly positive, which equation (18) requires of anything whose
	// logarithm it takes.
	floor float64
}

// GridSize is G of §7.2.
const GridSize = 512

// NewLevelIndex builds the lookup from a density.
//
// The masses are normalised by the grid total, so the tail mass at the mode is exactly
// 1 and the result is a probability whatever small distortion the zero-clamp
// introduced. Equation (7) integrates to 1 exactly in the absence of clamping, since
// truncation removes only zero-mean harmonics; clamping can only add mass, and the
// normalisation puts the total back to 1.
func NewLevelIndex(d *Density, grid int) *LevelIndex {
	if grid <= 0 {
		grid = GridSize
	}
	levels := make([]float64, grid)
	for g := range grid {
		levels[g] = d.Evaluate(2 * math.Pi * float64(g) / float64(grid))
	}
	sort.Float64s(levels)

	total := 0.0
	cumulative := make([]float64, grid)
	for i, f := range levels { // ascending order: one fixed summation order (R4)
		total += f
		cumulative[i] = total
	}
	if total > 0 {
		for i := range cumulative {
			cumulative[i] /= total
		}
	}

	return &LevelIndex{
		grid:       grid,
		levels:     levels,
		cumulative: cumulative,
		floor:      1 / (2 * float64(grid)),
	}
}

// TailMass returns P(φ) of equation (9) for a point whose density is level: the total
// probability of times no more probable than the observed one, from the lookup, by
// binary search.
//
// The result lies in [floor, 1]. The floor is the grid's resolution limit, documented
// on the type; §7.2's construction quantises mass to about one part in G, so reporting
// less would claim a precision the lookup does not have.
func (ix *LevelIndex) TailMass(level float64) float64 {
	// The rightmost grid level ≤ the queried level.
	idx := sort.SearchFloat64s(ix.levels, math.Nextafter(level, math.Inf(1))) - 1
	if idx < 0 {
		return ix.floor
	}
	mass := ix.cumulative[idx]
	if mass < ix.floor {
		return ix.floor
	}
	if mass > 1 {
		return 1
	}
	return mass
}

// Floor exposes the resolution limit for evidence rendering (R5): an analyst reading a
// floored p-value should see that it is a floor, not a measurement.
func (ix *LevelIndex) Floor() float64 { return ix.floor }
