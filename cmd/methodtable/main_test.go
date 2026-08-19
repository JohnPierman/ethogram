package main

import (
	"reflect"
	"strings"
	"testing"
)

func ev(key, kind string, scores map[string]float64) labelled {
	return labelled{key: key, kind: kind, scores: scores}
}

// The reconstruction: a method that ranks on one score catches exactly the n most extreme
// labelled events. Anything else here would be an estimate dressed as a measurement.
func TestCaughtByScoreTakesTheMostExtreme(t *testing.T) {
	events := []labelled{
		ev("1|U1", "spray", map[string]float64{"novelty": 0.5}),
		ev("2|U2", "spray", map[string]float64{"novelty": 0.001}),
		ev("3|U3", "lateral", map[string]float64{"novelty": 0.01}),
	}
	score := func(e labelled) (float64, bool) {
		p, ok := e.scores["novelty"]
		return p, ok
	}

	got := caughtByScore(events, score, 2)

	if len(got) != 2 {
		t.Fatalf("caught %d, want 2", len(got))
	}
	if got[0].key != "2|U2" || got[1].key != "3|U3" {
		t.Errorf("caught %s and %s, want the two smallest p-values", got[0].key, got[1].key)
	}
}

// A labelled event a detector never scored cannot have been caught by that detector's arm,
// and must not be borrowed from another detector's ranking to fill a quota.
func TestCaughtByScoreSkipsUnscoredEvents(t *testing.T) {
	events := []labelled{
		ev("1|U1", "spray", map[string]float64{"timing": 0.001}),
		ev("2|U2", "spray", map[string]float64{"novelty": 0.9}),
	}
	score := func(e labelled) (float64, bool) {
		p, ok := e.scores["novelty"]
		return p, ok
	}

	got := caughtByScore(events, score, 5)

	if len(got) != 1 || got[0].key != "2|U2" {
		t.Errorf("caught %v, want only the event novelty scored", keysOf(got))
	}
}

func TestCaughtByScoreZeroCountCatchesNothing(t *testing.T) {
	events := []labelled{ev("1|U1", "spray", map[string]float64{"novelty": 0.001})}
	score := func(e labelled) (float64, bool) { p, ok := e.scores["novelty"]; return p, ok }

	if got := caughtByScore(events, score, 0); len(got) != 0 {
		t.Errorf("caught %v at count 0, want nothing", keysOf(got))
	}
}

func keysOf(es []labelled) []string {
	out := make([]string, 0, len(es))
	for _, e := range es {
		out = append(out, e.key)
	}
	return out
}

// The distinction the table exists to preserve: a method measured and finding nothing reads
// 0, a method never measured at this budget reads --. Collapsing them would report an
// unmeasured method as a failed one.
func TestCellSeparatesUnmeasuredFromZero(t *testing.T) {
	measured := row{measured: true, caught: map[string]int{}}
	if got := cell(measured, "spray", 320); got != "0" {
		t.Errorf("measured zero rendered %q, want %q", got, "0")
	}

	unmeasured := row{measured: false}
	if got := cell(unmeasured, "spray", 320); got != "--" {
		t.Errorf("unmeasured rendered %q, want %q", got, "--")
	}
}

// A clean sweep is the one result worth emphasising in a table this size.
func TestCellMarksACompleteSweep(t *testing.T) {
	r := row{measured: true, caught: map[string]int{"takeover": 120}}
	if got := cell(r, "takeover", 120); got != "**120/120**" {
		t.Errorf("full recall rendered %q, want %q", got, "**120/120**")
	}
	partial := row{measured: true, caught: map[string]int{"takeover": 64}}
	if got := cell(partial, "takeover", 120); got != "64" {
		t.Errorf("partial recall rendered %q, want %q", got, "64")
	}
}

// The baselines stop short of the framework's widest budget. A row must say which budget it
// was measured at rather than presenting a narrower one as the one asked for.
func TestBaselineBudgetFallsBackAndSaysSo(t *testing.T) {
	all := map[string]any{
		"budget_10_per_day":  map[string]any{"detections": 1.0},
		"budget_100_per_day": map[string]any{"detections": 3.0},
	}

	at, used := baselineBudget(all, 1000)
	if used != 100 {
		t.Errorf("fell back to %d, want 100", used)
	}
	if intOf(at, "detections") != 3 {
		t.Errorf("returned the wrong budget's block: %v", at)
	}

	exact, used := baselineBudget(all, 10)
	if used != 10 || intOf(exact, "detections") != 1 {
		t.Errorf("exact match returned budget %d / %v", used, exact)
	}

	if _, used := baselineBudget(all, 5); used != -1 {
		t.Errorf("no budget at or below 5 should report -1, got %d", used)
	}
}

// Baselines name a detection as a record; the min-p arm names one as a four-part string.
// Both must reduce to the (t, entity) identity the rest of the table is keyed on.
func TestNormaliseKeyAcceptsBothForms(t *testing.T) {
	rec := map[string]any{"t": 918900.0, "entity": "U3089@DOM1"}
	if got := normaliseKey(rec); got != "918900|U3089@DOM1" {
		t.Errorf("record form gave %q", got)
	}
	if got := normaliseKey("635015|U737@DOM1|C19932|C529"); got != "635015|U737@DOM1" {
		t.Errorf("four-part string gave %q", got)
	}
	if got := normaliseKey("635015|U737@DOM1"); got != "635015|U737@DOM1" {
		t.Errorf("two-part string gave %q", got)
	}
}

// Groups must read in the framework's own order, not alphabetically, or the baselines
// interleave with the arms.
func TestSortRowsOrdersByGroup(t *testing.T) {
	tb := &table{rows: []row{
		{name: "pca", group: "baseline (population)"},
		{name: "novelty", group: "per-entity detector"},
		{name: "composite", group: "combination"},
		{name: "marginal", group: "population detector"},
	}}

	tb.sortRows()

	want := []string{"novelty", "marginal", "composite", "pca"}
	got := make([]string, 0, len(tb.rows))
	for _, r := range tb.rows {
		got = append(got, r.name)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("row order = %v, want %v", got, want)
	}
}

// A labelled event on an account the taxonomy does not name is an event of the real
// campaign, never an unattributed blank.
func TestKindOfDefaultsToRealCampaign(t *testing.T) {
	victim := map[string]string{"U202@DOM1": "account_takeover"}
	if got := kindOf(victim, "U202@DOM1"); got != "account_takeover" {
		t.Errorf("victim classified as %q", got)
	}
	if got := kindOf(victim, "U737@DOM1"); got != realCampaign {
		t.Errorf("non-victim classified as %q, want %q", got, realCampaign)
	}
}

// The rendered table must carry the denominators. A recall number without them is unreadable.
func TestRenderStatesThePlantedCounts(t *testing.T) {
	tb := &table{
		budget:  1000,
		types:   []string{"low_and_slow", realCampaign},
		planted: map[string]int{"low_and_slow": 288, realCampaign: 549},
		rows: []row{{name: "`volume`", group: "per-entity detector", measured: true,
			caught: map[string]int{}}},
	}

	md := tb.render()

	if !strings.Contains(md, "low+slow 288") || !strings.Contains(md, "real 549") {
		t.Errorf("rendered table omits the planted counts:\n%s", md)
	}
	if !strings.Contains(md, "*per-entity detector*") {
		t.Errorf("rendered table omits the group header:\n%s", md)
	}
}
