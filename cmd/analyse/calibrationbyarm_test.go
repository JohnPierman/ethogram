package main

import (
	"math"
	"strings"
	"testing"
)

// The within-run control of #55 is the whole reason the composite-conformal claim is readable, so
// the two functions that build it get tested rather than trusted. The failure mode that matters is
// the quiet one: a control that silently compares an arm with itself, or that inherits the
// conformal ordering it is meant to be independent of, would still produce a table.

func rowsWith(pairs ...[2]float64) []alertRow {
	out := make([]alertRow, 0, len(pairs))
	for i, p := range pairs {
		model := p[1]
		out = append(out, alertRow{
			LogP:      p[0],
			ModelLogP: &model,
			TSeconds:  int64(i),
			P:         math.Exp(p[0]),
		})
	}
	return out
}

// TestPreConformalArmSubstitutesAndResorts is the core of the control: the derived arm must carry
// the model statistic AND be ordered by it, not by the conformal value the replay wrote.
//
// Order matters because the step-up walks its input ascending. An arm carrying the right numbers in
// the wrong order would produce a table that looks plausible and answers nothing.
func TestPreConformalArmSubstitutesAndResorts(t *testing.T) {
	primary := arm{name: "framework", perDay: map[int64][]alertRow{
		// Written in conformal order (ascending LogP) while the model statistic runs the
		// other way, which is exactly what a conformal floor produces: the extreme events
		// tie at the floor and their model values still separate them.
		7: rowsWith([2]float64{-14, -3}, [2]float64{-13, -900}, [2]float64{-12, -40}),
	}}

	control, ok := preConformalArm(primary)
	if !ok {
		t.Fatal("no control derived from an arm whose model statistic differs")
	}
	if control.name != "composite-model" {
		t.Errorf("control is named %q", control.name)
	}
	got := control.arm.perDay[7]
	if len(got) != 3 {
		t.Fatalf("control has %d rows, want 3", len(got))
	}
	want := []float64{-900, -40, -3}
	for i, w := range want {
		if got[i].LogP != w {
			t.Errorf("row %d carries ln p %g, want %g: the control must be ordered by the "+
				"model statistic, not by the conformal value the replay wrote",
				i, got[i].LogP, w)
		}
		if got[i].ModelLogP != nil {
			t.Errorf("row %d still carries a model pointer; it has become the statistic", i)
		}
	}
	// The source arm must be untouched: it is the other half of the comparison.
	if primary.perDay[7][0].LogP != -14 {
		t.Errorf("deriving the control mutated the arm it was derived from")
	}
}

// TestPreConformalArmDeclinesWhenThereIsNothingToControl covers the two ways the control is
// meaningless: no model statistic recorded at all, and a model statistic identical to the
// conformal one. In both cases the arm would be compared with itself, and a table showing an arm
// agreeing with itself reads as evidence when it is a tautology.
func TestPreConformalArmDeclinesWhenThereIsNothingToControl(t *testing.T) {
	// A run without -conformal-composite records no model statistic.
	absent := arm{perDay: map[int64][]alertRow{
		7: {{LogP: -10, TSeconds: 1}, {LogP: -9, TSeconds: 2}},
	}}
	if _, ok := preConformalArm(absent); ok {
		t.Error("a control was derived from rows carrying no model statistic")
	}

	// Present but identical, which is what the replay writes when the conformal did not
	// replace the value.
	identical := arm{perDay: map[int64][]alertRow{
		7: rowsWith([2]float64{-10, -10}, [2]float64{-9, -9}),
	}}
	if _, ok := preConformalArm(identical); ok {
		t.Error("a control was derived from a statistic identical to the conformal one, so " +
			"the comparison would be an arm against itself")
	}

	if _, ok := preConformalArm(arm{perDay: map[int64][]alertRow{}}); ok {
		t.Error("a control was derived from an empty arm")
	}
}

// TestWithinRunNamesTheDirection pins the finding text against the four outcomes it distinguishes,
// because that sentence is what a reader takes away and an inverted one would be worse than none.
func TestWithinRunNamesTheDirection(t *testing.T) {
	at := func(q float64, disc int, realised float64) calibrationPoint {
		p := calibrationPoint{NominalQ: q, Discoveries: disc}
		p.RealisedFDR.Point = realised
		return p
	}

	for _, tc := range []struct {
		name          string
		conformalised []calibrationPoint
		model         []calibrationPoint
		wantSubstring string
	}{
		{
			// The measured outcome: the uncalibrated form rejects at the tightest level and
			// the conformalised one rejects nothing anywhere.
			name:          "conformalised never rejects",
			conformalised: []calibrationPoint{at(0.001, 0, 0), at(0.25, 0, 0)},
			model:         []calibrationPoint{at(0.001, 732, 1.0), at(0.25, 2950, 0.978)},
			wantSubstring: "rejects nothing at any level on the grid",
		},
		{
			// The measured outcome on the real corpus: it rejects, but only much later.
			name:          "conformalised rejects later",
			conformalised: []calibrationPoint{at(0.001, 0, 0), at(0.1, 1010, 1.0)},
			model:         []calibrationPoint{at(0.001, 732, 1.0), at(0.1, 2593, 0.977)},
			wantSubstring: "The level has purchase it did not have",
		},
		{
			name:          "no difference",
			conformalised: []calibrationPoint{at(0.01, 100, 0.9)},
			model:         []calibrationPoint{at(0.01, 120, 0.9)},
			wantSubstring: "the composite's own distribution is not what was missing",
		},
		{
			name:          "neither rejects",
			conformalised: []calibrationPoint{at(0.01, 0, 0)},
			model:         []calibrationPoint{at(0.01, 0, 0)},
			wantSubstring: "cannot separate them",
		},
	} {
		got := withinRun(
			armCalibration{Arm: "composite", Points: tc.conformalised},
			armCalibration{Arm: "composite-model", Points: tc.model},
		)
		finding, _ := got["finding"].(string)
		if !strings.Contains(finding, tc.wantSubstring) {
			t.Errorf("%s: finding does not contain %q\n  got: %s",
				tc.name, tc.wantSubstring, finding)
		}
		// And the levels it quotes must be the first rejecting ones, since the whole claim is
		// about where each form starts rejecting.
		for key, want := range map[string]float64{
			"conformalised_tightest_rejecting_level": firstRejecting(tc.conformalised),
			"uncalibrated_tightest_rejecting_level":  firstRejecting(tc.model),
		} {
			block, _ := got[key].(map[string]any)
			if block == nil {
				t.Fatalf("%s: %s is absent", tc.name, key)
			}
			if q, _ := block["nominal_q"].(float64); q != want {
				t.Errorf("%s: %s quotes q = %g, want %g", tc.name, key, q, want)
			}
		}
	}
}

func firstRejecting(points []calibrationPoint) float64 {
	for _, p := range points {
		if p.Discoveries > 0 {
			return p.NominalQ
		}
	}
	return 0
}

// TestLocaliseReadsResponsivenessNotWorstCase guards the fix that mattered most in this file. The
// first version compared worst-case realised rates, and since both rules land far above every
// nominal level it read the measurement backwards: it called them alike when what separates them is
// that one responds to the level and the other does not.
func TestLocaliseReadsResponsivenessNotWorstCase(t *testing.T) {
	inert := armCalibration{Arm: "composite", RealisedSpread: 0.0044, Points: []calibrationPoint{
		{NominalQ: 0.001, Discoveries: 3861}, {NominalQ: 0.25, Discoveries: 5348},
	}}
	responsive := armCalibration{Arm: "min-p", RealisedSpread: 0.9639, Points: []calibrationPoint{
		{NominalQ: 0.001, Discoveries: 0}, {NominalQ: 0.005, Discoveries: 119},
	}}
	responsive.Points[1].RealisedFDR.Point = 0.672

	got := localise(inert, responsive)
	finding, _ := got["finding"].(string)
	if !strings.Contains(finding, "two defects and they are separable") {
		t.Errorf("an inert composite beside a responsive minimum was not called separable:\n  %s",
			finding)
	}
	if !strings.Contains(finding, "3861") {
		t.Error("the finding does not quote how many events the inert rule rejects at its " +
			"tightest level, which is the fact that makes it inert rather than merely wrong")
	}

	// Both inert is a different diagnosis, and it must not be confused with the above.
	bothInert := responsive
	bothInert.RealisedSpread = 0.01
	got = localise(inert, bothInert)
	if finding, _ := got["finding"].(string); !strings.Contains(finding,
		"upstream of the combination") {
		t.Errorf("two inert rules were not blamed upstream:\n  %s", finding)
	}
}
