package main

import (
	"testing"

	"github.com/JohnPierman/ethogram/domain/objective"
)

// pushAll seeds one (day, period bucket) cell.
func pushAll(t *testing.T, p *volumeGateProbe, day int64, periods int64, ps []float64, labelled []float64) {
	t.Helper()
	cell, ok := p.cells[day]
	if !ok {
		cell = &[volumeGateBuckets]volumeGateBucketStats{}
		p.cells[day] = cell
	}
	b := &cell[volumeGateBucket(periods)]
	for _, v := range ps {
		b.events++
		if v <= miscalibratedP {
			b.subThreshold++
		}
		b.push(v, p.topK)
		p.seen++
	}
	b.labelled = append(b.labelled, labelled...)
}

// TestCutIsPerDayNotPooled is the bug this test exists for. The budget is per day, so a
// labelled event competes only against its own day. Day 7's queue is saturated with
// 1e-20 background, so its labelled event at 1e-8 misses; day 8's cut is loose, so its
// labelled event at 1e-4 lands. A single pooled cut across both days would take the
// loosest (1e-3) and wrongly credit BOTH.
func TestCutIsPerDayNotPooled(t *testing.T) {
	p := newVolumeGateProbe(1000, 0)
	pushAll(t, p, 7, 9, []float64{1e-20, 1e-20, 1e-20}, []float64{1e-8})
	pushAll(t, p, 8, 9, []float64{1e-3, 2e-3}, []float64{1e-4})

	c, ok := p.cutAt(0, 2)
	if !ok {
		t.Fatal("want a cut")
	}
	if c.days != 2 {
		t.Fatalf("days = %d, want 2", c.days)
	}
	if c.labelledClearing != 1 {
		t.Fatalf("labelledClearing = %d, want 1: only day 8's labelled event reaches its own day's cut", c.labelledClearing)
	}
	if c.alerts != 4 {
		t.Fatalf("alerts = %d, want 4 (2 per day at budget 2)", c.alerts)
	}
	// Day 7's cut is on the floor, day 8's is not, so the candidate has not cleared the
	// floor everywhere and must not be reported as if it had.
	if c.daysOffFloor != 1 {
		t.Fatalf("daysOffFloor = %d, want 1", c.daysOffFloor)
	}
}

// TestGateAdmitsOnlyBucketsAtOrAboveThreshold: raising minPeriods must drop the
// low-period cells out of both the queue and the labelled count.
func TestGateAdmitsOnlyBucketsAtOrAboveThreshold(t *testing.T) {
	p := newVolumeGateProbe(1000, 0)
	pushAll(t, p, 7, 0, []float64{1e-20, 1e-20}, []float64{1e-20}) // first period
	pushAll(t, p, 7, 4, []float64{1e-5}, []float64{1e-5})          // four completed

	atZero, _ := p.cutAt(0, 10)
	if atZero.labelledClearing != 2 {
		t.Fatalf("ungated labelledClearing = %d, want 2", atZero.labelledClearing)
	}
	atThree, _ := p.cutAt(3, 10)
	if atThree.labelledClearing != 1 {
		t.Fatalf("gated labelledClearing = %d, want 1", atThree.labelledClearing)
	}
	if atThree.cutMax != 1e-5 {
		t.Fatalf("gated cut = %v, want 1e-5", atThree.cutMax)
	}

	removedEvents, removedSub, removedLabelled, _, keptLabelled := p.totals(3)
	if removedEvents != 2 || removedSub != 2 || removedLabelled != 1 || keptLabelled != 1 {
		t.Fatalf("totals(3) = %d,%d,%d,_,%d; want 2,2,1,_,1",
			removedEvents, removedSub, removedLabelled, keptLabelled)
	}
}

// TestBucketingCoversEveryCandidate: the last bucket must be "that many or more", or a
// threshold of 5 would silently admit nothing.
func TestBucketingCoversEveryCandidate(t *testing.T) {
	for _, c := range volumeGateCandidates {
		if volumeGateBucket(c) >= volumeGateBuckets {
			t.Fatalf("candidate %d has no bucket", c)
		}
	}
	if volumeGateBucket(99) != volumeGateBuckets-1 {
		t.Fatal("a large period count must fall in the final bucket")
	}
}

// TestResultsReportsNotMeasuredWithoutVerdicts keeps the block honest on a run where
// volume did not participate.
func TestResultsReportsNotMeasuredWithoutVerdicts(t *testing.T) {
	p := newVolumeGateProbe(1000, 0)
	out := p.results(objective.Budgets{10})
	if out["measured"] != false {
		t.Fatalf("measured = %v, want false", out["measured"])
	}
}
