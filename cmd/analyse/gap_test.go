package main

import "testing"

// alertsDescending builds one day's retained alerts, most extreme first, which is the
// order the run records them in and the order the cut is read from.
func alertsDescending(day int64, logPs ...float64) []alertRow {
	rows := make([]alertRow, 0, len(logPs))
	for i, lp := range logPs {
		rows = append(rows, alertRow{
			LogP:     lp,
			TSeconds: day*day24 + int64(i),
			Entity:   "U1@DOM1",
		})
	}
	return rows
}

const day24 = int64(86400)

func gapFixture() (arm, redTeamPopulation) {
	a := arm{
		name: "primary",
		perDay: map[int64][]alertRow{
			// Day 7: four alerts. The cut at budget 2 is −30.
			7: alertsDescending(7, -50, -30, -20, -10),
			// Day 8: two alerts only, so a budget of 4 has no cut on this day.
			8: alertsDescending(8, -80, -70),
		},
	}

	// Three labelled events: one inside day 7's budget-2 cut, one outside it, and one
	// on day 8 where the budget cannot be met.
	inside := eventKey{7*day24 + 100, "U9@DOM1"}
	outside := eventKey{7*day24 + 200, "U8@DOM1"}
	dayEight := eventKey{8*day24 + 300, "U7@DOM1"}

	pop := redTeamPopulation{
		keys:       []eventKey{inside, outside, dayEight},
		categories: map[eventKey][]string{},
		logP: map[eventKey]float64{
			inside:   -40, // more extreme than the −30 cut
			outside:  -25, // less extreme: missed by 5
			dayEight: -60,
		},
		source: "fixture",
	}
	return a, pop
}

// TestGapMeasuresDistanceToTheCut pins the sign convention and the arithmetic. The
// convention is the whole point of the table: positive means missed.
func TestGapMeasuresDistanceToTheCut(t *testing.T) {
	a, pop := gapFixture()
	got := gapTable(a, pop, []int{2})

	if len(got.PerDay) != 2 {
		t.Fatalf("expected two scored days, got %d", len(got.PerDay))
	}

	day7 := got.PerDay[0]
	if day7.Day != 7 {
		t.Fatalf("days must be ordered ascending; first is %d", day7.Day)
	}
	if day7.LabelledScored != 2 {
		t.Errorf("day 7 labelled = %d, want 2", day7.LabelledScored)
	}
	if day7.BestLabelledLog == nil || *day7.BestLabelledLog != -40 {
		t.Fatalf("day 7 best labelled = %v, want −40: the best is the most extreme, "+
			"which is the smallest log p", day7.BestLabelledLog)
	}
	if len(day7.Cuts) != 1 {
		t.Fatalf("expected one budget, got %d", len(day7.Cuts))
	}
	cut := day7.Cuts[0]
	if cut.CutLogP == nil || *cut.CutLogP != -30 {
		t.Fatalf("day 7 cut at budget 2 = %v, want −30 (the second most extreme alert)",
			cut.CutLogP)
	}
	// −40 − (−30) = −10: the labelled event is ten log-units MORE extreme than the
	// cut, so it is inside, and the gap is negative.
	if cut.Gap == nil || *cut.Gap != -10 {
		t.Errorf("day 7 gap = %v, want −10; negative must mean the labelled event "+
			"cleared the cut", cut.Gap)
	}

	day8 := got.PerDay[1]
	if day8.Cuts[0].CutLogP == nil || *day8.Cuts[0].CutLogP != -70 {
		t.Errorf("day 8 retained exactly two alerts, so a budget of two has a cut at −70")
	}
}

// TestGapReportsNoCutWhenTheDayIsShortOfTheBudget: a day that retained fewer alerts than
// the budget has no cut to clear, and must report that rather than inventing one from
// the last alert it happens to hold.
func TestGapReportsNoCutWhenTheDayIsShortOfTheBudget(t *testing.T) {
	a, pop := gapFixture()
	got := gapTable(a, pop, []int{4})

	day8 := got.PerDay[1]
	if day8.Day != 8 {
		t.Fatalf("second day is %d, want 8", day8.Day)
	}
	if day8.Cuts[0].CutLogP != nil || day8.Cuts[0].Gap != nil {
		t.Errorf("day 8 holds two alerts against a budget of four; cut = %v gap = %v, "+
			"both must be null", day8.Cuts[0].CutLogP, day8.Cuts[0].Gap)
	}

	// The labelled event on that day is therefore unmeasurable at this budget, not
	// "beyond 200" and not "inside".
	dist := got.Distribution[0]
	if dist.Unmeasurable != 1 {
		t.Errorf("unmeasurable = %d, want 1: a day without a cut cannot bucket its "+
			"labelled events", dist.Unmeasurable)
	}
}

// TestGapDistributionSeparatesNearMissesFromHopelessOnes is the reason the buckets
// exist. Recall alone cannot distinguish a labelled event five log-units outside the
// cut from one a thousand outside it, and those two call for entirely different work.
func TestGapDistributionSeparatesNearMissesFromHopelessOnes(t *testing.T) {
	a := arm{
		name:   "primary",
		perDay: map[int64][]alertRow{7: alertsDescending(7, -100, -90)},
	}
	near := eventKey{7*day24 + 1, "U1@DOM1"}
	middling := eventKey{7*day24 + 2, "U2@DOM1"}
	hopeless := eventKey{7*day24 + 3, "U3@DOM1"}
	within := eventKey{7*day24 + 4, "U4@DOM1"}
	pop := redTeamPopulation{
		keys:       []eventKey{near, middling, hopeless, within},
		categories: map[eventKey][]string{},
		logP: map[eventKey]float64{
			near:     -85,  // 5 outside the −90 cut
			middling: -60,  // 30 outside
			hopeless: -900, // inside, hugely
			within:   -10,  // 80 outside
		},
	}

	dist := gapTable(a, pop, []int{2}).Distribution[0]
	if dist.Inside != 1 {
		t.Errorf("inside = %d, want 1", dist.Inside)
	}
	if dist.WithinTen != 1 {
		t.Errorf("within 10 = %d, want 1", dist.WithinTen)
	}
	if dist.WithinFifty != 1 {
		t.Errorf("within 50 = %d, want 1", dist.WithinFifty)
	}
	if dist.WithinTwoHundred != 1 {
		t.Errorf("within 200 = %d, want 1", dist.WithinTwoHundred)
	}
	if dist.Beyond != 0 {
		t.Errorf("beyond 200 = %d, want 0", dist.Beyond)
	}
}

// TestGapIsAbsentWhenTheRunRecordedNoLogP: a run predating log_p in red_team_scored
// must produce a table with no gaps rather than gaps computed against zero.
func TestGapIsAbsentWhenTheRunRecordedNoLogP(t *testing.T) {
	a, pop := gapFixture()
	pop.logP = nil

	got := gapTable(a, pop, []int{2})
	for _, row := range got.PerDay {
		if row.BestLabelledLog != nil {
			t.Errorf("day %d reported a best labelled log p from a run that has none", row.Day)
		}
		for _, c := range row.Cuts {
			if c.Gap != nil {
				t.Errorf("day %d reported a gap from a run with no labelled log p", row.Day)
			}
		}
	}
	if got.Distribution[0].Unmeasurable != len(pop.keys) {
		t.Errorf("unmeasurable = %d, want all %d labelled events",
			got.Distribution[0].Unmeasurable, len(pop.keys))
	}
}
