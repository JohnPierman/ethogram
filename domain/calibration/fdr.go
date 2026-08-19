package calibration

import (
	"cmp"
	"slices"
)

// Discovery is one flagged test of §10.3: Index is the p-value's position in the
// caller's slice, PValue the value itself, both carried so an analyst can recompute
// the threshold arithmetic by hand (R5).
type Discovery struct {
	Index  int
	PValue float64
}

// BenjaminiHochberg applies the Benjamini–Hochberg step-up procedure [71] at false
// discovery rate q: sort the p-values ascending, find the largest rank i with
//
//	p_(i) ≤ (i/m)·q,   m = len(pValues)
//
// and report every p-value of rank ≤ i as a discovery. The FDR guarantee holds under
// independence or positive regression dependence; BenjaminiYekutieli covers
// arbitrary dependence.
//
// The sort breaks ties by original index — a total order, so equal p-values cannot
// reorder between runs (R4). Discoveries are returned sorted by Index ascending. An
// empty input or q ≤ 0 yields an empty, non-nil slice: no tests means no
// discoveries, not an error.
func BenjaminiHochberg(pValues []float64, q float64) []Discovery {
	return stepUp(pValues, q)
}

// BenjaminiYekutieli is the Benjamini–Yekutieli variant [72], valid under arbitrary
// dependence between the tests: the Benjamini–Hochberg procedure with q replaced by
// q/H_m, where H_m = Σ_{i=1..m} 1/i. The harmonic number is accumulated in ascending
// i order — one fixed summation order, as R4 requires everywhere.
func BenjaminiYekutieli(pValues []float64, q float64) []Discovery {
	m := len(pValues)
	if m == 0 {
		return []Discovery{}
	}
	harmonic := 0.0
	for i := 1; i <= m; i++ {
		harmonic += 1 / float64(i)
	}
	return stepUp(pValues, q/harmonic)
}

// stepUp is the shared step-up walk: rank the p-values ascending with original-index
// tiebreak, scan from the largest rank down for the first rank whose p-value sits at
// or below its threshold (i/m)·q — "≤", as [71] defines it, so a p-value exactly on
// its threshold is discovered — and return everything at or below that rank, by
// original index. pValues is not mutated.
func stepUp(pValues []float64, q float64) []Discovery {
	discoveries := []Discovery{}
	m := len(pValues)
	if m == 0 || q <= 0 {
		return discoveries
	}

	ranked := make([]Discovery, m)
	for i, p := range pValues {
		ranked[i] = Discovery{Index: i, PValue: p}
	}
	slices.SortFunc(ranked, func(a, b Discovery) int {
		if byValue := cmp.Compare(a.PValue, b.PValue); byValue != 0 {
			return byValue
		}
		return cmp.Compare(a.Index, b.Index)
	})

	largestPassing := 0
	for rank := m; rank >= 1; rank-- {
		if ranked[rank-1].PValue <= float64(rank)/float64(m)*q {
			largestPassing = rank
			break
		}
	}

	discoveries = append(discoveries, ranked[:largestPassing]...)
	slices.SortFunc(discoveries, func(a, b Discovery) int {
		return cmp.Compare(a.Index, b.Index)
	})
	return discoveries
}
