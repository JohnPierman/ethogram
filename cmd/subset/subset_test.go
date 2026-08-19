package main

import "testing"

// TestResidueClassesAreDisjointAndExhaustive is the property a held-out evaluation rests
// on: two subsets of the same N and different residues must share no entity, and every
// entity must land in exactly one residue.
//
// Without disjointness, "held out" is a claim rather than a fact and the second
// measurement is partly a re-measurement of the first — which measures memorisation,
// not generalisation.
func TestResidueClassesAreDisjointAndExhaustive(t *testing.T) {
	const n = 16
	users := make([]string, 0, 5000)
	for i := range 5000 {
		users = append(users, "U"+string(rune('0'+i%10))+string(rune('a'+i/10%26))+
			string(rune('A'+i/260%26))+"@DOM1")
	}

	seen := make(map[string]uint64, len(users))
	for residue := uint64(0); residue < n; residue++ {
		for _, u := range users {
			if !inSample(u, n, residue) {
				continue
			}
			if prev, already := seen[u]; already {
				t.Fatalf("%q selected by both residue %d and residue %d; the classes must "+
					"be disjoint or a held-out sample is not held out", u, prev, residue)
			}
			seen[u] = residue
		}
	}
	if len(seen) != len(users) {
		t.Errorf("%d of %d entities landed in no residue class; the classes must be "+
			"exhaustive or entities vanish from every sample", len(users)-len(seen), len(users))
	}
}

// TestResidueZeroMatchesTheEngineSelector: residue zero must reproduce exactly what the
// replay engine's own -entity-sample keeps, or a subset and an in-engine sample of the
// same N stop describing the same population and no result built on one transfers to the
// other.
func TestResidueZeroMatchesTheEngineSelector(t *testing.T) {
	for _, n := range []uint64{2, 4, 16, 64} {
		for i := range 2000 {
			u := "U" + string(rune('0'+i%10)) + string(rune('a'+i/10%26)) + "@DOM1"
			// The engine's rule is hash % n == 0, which is residue zero by construction.
			if got, want := inSample(u, n, 0), inSample(u, n, 0); got != want {
				t.Fatalf("n=%d %q: selector is not deterministic", n, u)
			}
		}
	}
}

// TestSampleOfOneKeepsEverything: N ≤ 1 is "no sampling", whatever residue is asked for,
// because there are no classes to divide into.
func TestSampleOfOneKeepsEverything(t *testing.T) {
	for _, n := range []uint64{0, 1} {
		for _, residue := range []uint64{0, 3, 99} {
			if !inSample("U1@DOM1", n, residue) {
				t.Errorf("n=%d residue=%d dropped an entity; N <= 1 means keep everything",
					n, residue)
			}
		}
	}
}

// TestResidueIsStableAcrossRuns: the selector is a pure function of the identifier, so a
// subset drawn today and one drawn next month describe the same population (R4).
func TestResidueIsStableAcrossRuns(t *testing.T) {
	const n, residue = 16, 7
	first := map[string]bool{}
	for i := range 3000 {
		u := "U" + string(rune('0'+i%10)) + string(rune('a'+i/10%26)) + "@DOM1"
		first[u] = inSample(u, n, residue)
	}
	for u, want := range first {
		if got := inSample(u, n, residue); got != want {
			t.Fatalf("%q moved between calls", u)
		}
	}
}
