package main

import (
	"math"
	"math/rand"
	"sort"
	"testing"

	"github.com/JohnPierman/ethogram/domain/calibration"
	"github.com/JohnPierman/ethogram/domain/novelty"
	"github.com/JohnPierman/ethogram/domain/volume"
)

// TestTheRetainedTailIsTheSmallestAndStaysBounded pins the one property that makes an
// entity-day usable for Higher Criticism without making it unbounded: it holds the k
// smallest log p-values, ascending, and never more than k however many events arrive.
func TestTheRetainedTailIsTheSmallestAndStaysBounded(t *testing.T) {
	rng := rand.New(rand.NewSource(17))
	ed := &entityDay{}
	all := make([]float64, 0, 5000)
	for i := 0; i < 5000; i++ {
		logP := math.Log(rng.Float64())
		all = append(all, logP)
		ed.observeTail(logP)
	}

	if len(ed.Tail) != entityDayTailDepth {
		t.Fatalf("the tail holds %d values, want exactly %d", len(ed.Tail), entityDayTailDepth)
	}
	if !sort.Float64sAreSorted(ed.Tail) {
		t.Error("the tail is not ascending, so its order statistics are not order statistics")
	}
	sort.Float64s(all)
	for i := 0; i < entityDayTailDepth; i++ {
		if ed.Tail[i] != all[i] {
			t.Fatalf("tail[%d] is %g, want the %dth smallest %g", i, ed.Tail[i], i, all[i])
		}
	}
}

// TestAShortDayKeepsEverythingItHas covers the entity-days a real corpus is mostly made of:
// accounts with one or two events. Nothing may be dropped and nothing invented.
func TestAShortDayKeepsEverythingItHas(t *testing.T) {
	ed := &entityDay{}
	ed.observeTail(math.Log(0.4))
	ed.observeTail(math.Log(0.01))
	ed.observeTail(math.Log(0.9))
	want := []float64{math.Log(0.01), math.Log(0.4), math.Log(0.9)}
	if len(ed.Tail) != len(want) {
		t.Fatalf("the tail holds %d of 3 values", len(ed.Tail))
	}
	for i := range want {
		if ed.Tail[i] != want[i] {
			t.Errorf("tail[%d] is %g, want %g", i, ed.Tail[i], want[i])
		}
	}
}

// TestTheTailIsIndependentOfArrivalOrder is the determinism requirement (R4) at this layer:
// the retained tail is a property of the day's multiset of scores, so replaying the same
// day's events in any order must retain the same values.
func TestTheTailIsIndependentOfArrivalOrder(t *testing.T) {
	rng := rand.New(rand.NewSource(4))
	values := make([]float64, 200)
	for i := range values {
		// A coarse grid so exact ties are common; ties are where an order-dependent
		// implementation diverges.
		values[i] = math.Log(float64(1+rng.Intn(40)) / 40)
	}

	first := &entityDay{}
	for _, v := range values {
		first.observeTail(v)
	}
	for trial := 0; trial < 20; trial++ {
		rng.Shuffle(len(values), func(i, j int) { values[i], values[j] = values[j], values[i] })
		again := &entityDay{}
		for _, v := range values {
			again.observeTail(v)
		}
		if len(again.Tail) != len(first.Tail) {
			t.Fatalf("trial %d: tail length %d against %d", trial, len(again.Tail), len(first.Tail))
		}
		for i := range first.Tail {
			if again.Tail[i] != first.Tail[i] {
				t.Fatalf("trial %d: tail[%d] is %g under one order and %g under another",
					trial, i, again.Tail[i], first.Tail[i])
			}
		}
	}
}

// TestHigherCriticismOfAnEntityDayRecordsFailuresRatherThanDroppingRows covers the states a
// corpus presents that are not faults: a day with no retained tail, and a day whose value
// overflows float64 and therefore cannot be written as JSON.
func TestHigherCriticismOfAnEntityDayRecordsFailuresRatherThanDroppingRows(t *testing.T) {
	empty := higherCriticismOf(&entityDay{Events: 5})
	if empty.Error == "" {
		t.Error("an entity-day with no retained tail produced no explanation")
	}
	if empty.Statistic != nil {
		t.Error("a statistic was reported for an entity-day with nothing to compute it from")
	}

	// A day whose most extreme event underflowed: the statistic is infinite, the logarithm
	// is what survives, and JSON must carry null rather than a number that is not one.
	deep := &entityDay{Events: 400, Tail: []float64{-4000, math.Log(0.5), math.Log(0.6)}}
	got := higherCriticismOf(deep)
	if got.Error != "" {
		t.Fatalf("a representable log p-value was refused: %s", got.Error)
	}
	if !got.Positive {
		t.Fatal("a day with ln P = -4000 produced no positive term")
	}
	if got.Statistic != nil {
		t.Errorf("the statistic is reported as %g, but at ln P = -4000 it overflows and "+
			"JSON has no infinity", *got.Statistic)
	}
	if math.IsInf(got.LogStatistic, 0) || got.LogStatistic < 1000 {
		t.Errorf("the log statistic is %g, want a large finite number", got.LogStatistic)
	}
	if got.NullScale <= 0 {
		t.Errorf("the null scale is %g, want positive for 400 events", got.NullScale)
	}

	// An ordinary day reports the value itself.
	ordinary := higherCriticismOf(&entityDay{
		Events: 400, Tail: []float64{math.Log(0.0001), math.Log(0.4)},
	})
	if ordinary.Statistic == nil {
		t.Error("an ordinary day's statistic was reported as absent")
	}
}

// TestTheRoundTripThroughTheResultFilePreservesTheRanking is the property that keeps the
// recorded numbers and the ranking from drifting apart: the comparison happens once, in the
// domain, and the result file's own fields are what reach it.
func TestTheRoundTripThroughTheResultFilePreservesTheRanking(t *testing.T) {
	days := []*entityDay{
		{Events: 400, Tail: []float64{-4000, math.Log(0.5)}},           // overflows
		{Events: 400, Tail: []float64{-8000, math.Log(0.5)}},           // overflows further
		{Events: 400, Tail: []float64{math.Log(1e-6), math.Log(0.5)}},  // ordinary, extreme
		{Events: 400, Tail: []float64{math.Log(0.4), math.Log(0.5)}},   // ordinary, mild
		{Events: 400, Tail: []float64{math.Log(0.98), math.Log(0.99)}}, // quieter than uniform
	}
	rows := make([]entityDayHigherCriticism, len(days))
	for i, ed := range days {
		rows[i] = higherCriticismOf(ed)
	}

	// Rank by the flattened rows, exactly as entityDayResults does.
	order := []int{0, 1, 2, 3, 4}
	sort.SliceStable(order, func(i, j int) bool {
		return calibration.MoreExtreme(rows[order[i]].result(), rows[order[j]].result())
	})

	// ln P = -8000 must lead, then -4000, then 1e-6, then 0.4, and the quiet day last.
	want := []int{1, 0, 2, 3, 4}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("the ranking is %v, want %v: the round trip through the result "+
				"file's fields has changed the order", order, want)
		}
	}
}

// TestTruncationIsReportedNotHidden pins that a bounded tail which could not reach the
// alpha0 cap says so. A truncated maximum presented as a complete one is the failure mode
// the retention bound risks, and the count of them is what tells a reader whether the bound
// was adequate on the data at hand.
func TestTruncationIsReportedNotHidden(t *testing.T) {
	// 5,000 events puts the alpha0 = 0.1 cap at rank 500, far beyond a 32-deep tail.
	busy := &entityDay{Events: 5_000}
	rng := rand.New(rand.NewSource(9))
	for i := 0; i < 5_000; i++ {
		busy.observeTail(math.Log(rng.Float64()))
	}
	got := higherCriticismOf(busy)
	if !got.Truncated {
		t.Error("a 32-deep tail against a cap of 500 was not reported as truncated")
	}
	if got.Considered != entityDayTailDepth {
		t.Errorf("%d ranks were considered, want the %d the tail holds",
			got.Considered, entityDayTailDepth)
	}

	// A day short enough that the cap falls inside the tail is not truncated.
	quiet := &entityDay{Events: 20}
	for i := 0; i < 20; i++ {
		quiet.observeTail(math.Log(rng.Float64()))
	}
	if higherCriticismOf(quiet).Truncated {
		t.Error("a day whose every event is retained was reported as truncated")
	}
}

// TestParseOnlinePinsWhatIsSupported fixes the flag's contract, including that `saffron` is
// refused rather than aliased to LORD++. A run whose provenance names a procedure it did not
// run is worse than a run that does not start.
func TestParseOnlinePinsWhatIsSupported(t *testing.T) {
	for _, tc := range []struct {
		in      string
		want    onlineMode
		wantErr bool
	}{
		{"none", onlineNone, false},
		{"lord", onlineLORD, false},
		{"saffron", "", true},
		{"addis", "", true},
		{"", "", true},
		{"LORD", "", true},
	} {
		got, err := parseOnline(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("%q: accepted as %q, want refused", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: refused with %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestTheOnlineControlWatchesTwoStreamsAndOnlyTwo pins the scope decision. #16 asks for the
// rule on the best calibrated detector with the composite as a negative control, and running
// it on every arm would cost minutes of replay for no additional finding.
func TestTheOnlineControlWatchesTwoStreamsAndOnlyTwo(t *testing.T) {
	o := newOnlineControl(onlineLORD, novelty.DetectorID)
	if !o.watches(string(novelty.DetectorID)) {
		t.Error("the headline arm is not watched")
	}
	if !o.watches(onlineNegative) {
		t.Error("the negative control is not watched")
	}
	if o.watches(string(volume.DetectorID)) {
		t.Error("an unnamed arm is watched, which costs a replay minutes for no finding")
	}

	off := newOnlineControl(onlineNone, novelty.DetectorID)
	if off.on() || off.watches(onlineNegative) {
		t.Error("the none mode watches something")
	}
	if rec := off.record(); rec["mode"] != string(onlineNone) {
		t.Errorf("the none mode records mode %v", rec["mode"])
	}
	if off.neverSilent() != nil {
		t.Error("the none mode reported a level it never issued")
	}
}

// TestTheOnlineTrajectoryIsRecordedPerDay covers the shape that is the finding: a productive
// day buys a higher level and a barren one lowers it without reaching zero.
func TestTheOnlineTrajectoryIsRecordedPerDay(t *testing.T) {
	o := newOnlineControl(onlineLORD, novelty.DetectorID)
	stream := string(novelty.DetectorID)

	// Day 1 carries signal, day 2 carries none.
	for i := 0; i < 2_000; i++ {
		logP := math.Log(0.5)
		isRed := false
		if i%50 == 0 {
			logP = -30
			isRed = true
		}
		o.observe(stream, 1, logP, isRed)
	}
	levelAfterProductiveDay := o.rules[stream].Level()

	for i := 0; i < 2_000; i++ {
		o.observe(stream, 2, math.Log(0.9), false)
	}
	levelAfterBarrenDay := o.rules[stream].Level()

	if !(levelAfterBarrenDay < levelAfterProductiveDay) {
		t.Errorf("the level is %.3e after a barren day against %.3e after a productive "+
			"one; a barren stretch must lower the alerting rate",
			levelAfterBarrenDay, levelAfterProductiveDay)
	}
	if !(levelAfterBarrenDay > 0) {
		t.Errorf("the level reached %g after one barren day, so the rule has gone silent",
			levelAfterBarrenDay)
	}

	rec := o.record()
	streams, ok := rec["streams"].(map[string]any)
	if !ok {
		t.Fatalf("the record carries no streams: %v", rec)
	}
	entry, ok := streams[stream].(map[string]any)
	if !ok {
		t.Fatalf("the record carries no entry for %s", stream)
	}
	trajectory, ok := entry["per_day_trajectory"].([]map[string]any)
	if !ok || len(trajectory) != 2 {
		t.Fatalf("the trajectory has %v entries, want one per corpus day", entry["per_day_trajectory"])
	}
	if trajectory[0]["day"] != int64(1) || trajectory[1]["day"] != int64(2) {
		t.Errorf("the trajectory is not in day order: %v then %v",
			trajectory[0]["day"], trajectory[1]["day"])
	}
	if tp, _ := trajectory[0]["true_positives"].(int64); tp == 0 {
		t.Error("the productive day recorded no true positive")
	}
	if rejections, _ := trajectory[1]["rejections"].(int64); rejections != 0 {
		t.Errorf("the barren day recorded %d rejections at p = 0.9", rejections)
	}

	silent, ok := o.neverSilent()[stream].(map[string]any)
	if !ok {
		t.Fatalf("the never-silent record carries no entry for %s", stream)
	}
	if silent["strictly_positive"] != true {
		t.Errorf("the never-silent record says %v", silent["strictly_positive"])
	}
}
