package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRedrawFromTheCurveFileReproducesTheFigure is what makes redrawing without the replay
// trustworthy.
//
// The replay behind the figure retains ten thousand alerts a day for every arm, runs to 46 MB
// and is deliberately not in the repository; the curve file it derives is. So a change to the
// DRAWING has to be applicable without re-measuring, and that is only safe if the two paths
// produce the same picture. The alternative -- editing the committed SVG by hand -- is how a
// figure stops matching the numbers it claims to plot.
func TestRedrawFromTheCurveFileReproducesTheFigure(t *testing.T) {
	fw := writeTmp(t, "fw.json", frameworkFixture(4_000_000, 500, map[int][2]int{
		10: {70, 7}, 100: {700, 14}, 1000: {7000, 40},
	}))
	bl := writeTmp(t, "bl.json", baselinesFixture(1_000_000, 4, 500, 7, 14,
		map[string]map[int]int{
			"entity_ewma": {10: 0, 100: 2, 1000: 9},
			"lof":         {10: 0, 100: 0, 1000: 5},
		}))
	dir := t.TempDir()
	curve := filepath.Join(dir, "curve.json")
	fromReplay := filepath.Join(dir, "from-replay.svg")
	if err := run(fw, bl, outputs{curve: curve, svg: fromReplay}, "c-001", "", "composite", 0.01); err != nil {
		t.Fatal(err)
	}
	fromCurve := filepath.Join(dir, "from-curve.svg")
	if err := redraw(curve, outputs{svg: fromCurve}); err != nil {
		t.Fatal(err)
	}
	a, err := os.ReadFile(fromReplay)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(fromCurve)
	if err != nil {
		t.Fatal(err)
	}
	if len(a) == 0 {
		t.Fatal("the replay path drew nothing")
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("redrawing from the curve file produced a different figure: %d bytes from the "+
			"replay against %d from the curve", len(a), len(b))
	}
}

// TestRedrawRefusesAFileItCannotDraw: the two ways a curve file can be unusable are being the
// wrong kind of file and having lost the population every line is plotted against. Both must
// fail with a reason rather than draw an unlabelled or mis-scaled figure.
func TestRedrawRefusesAFileItCannotDraw(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]struct {
		file map[string]any
		want string
	}{
		"wrong kind": {map[string]any{"kind": "baselines"}, "not a budget-curve"},
		"no population": {map[string]any{
			"kind": "budget-curve",
			"results": map[string]any{"drawn": []any{map[string]any{
				"name": "x", "points": []any{map[string]any{"budget": 10}},
			}}},
		}, "missing the population"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			in := writeTmp(t, "curve.json", tc.file)
			err := redraw(in, outputs{svg: filepath.Join(dir, "out.svg")})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want an error naming %q, got %v", tc.want, err)
			}
		})
	}
}

// TestScopeFollowsTheNullNotTheAuthor pins what the figure's two colours mean. entity_ewma is
// a comparator and belongs in the same family as our own arms, because it judges an event
// against the account that produced it; that is the distinction section 1.2 draws, and
// colouring by authorship instead would hide it.
func TestScopeFollowsTheNullNotTheAuthor(t *testing.T) {
	cases := map[string]struct {
		s    series
		want string
	}{
		"our own arm":        {series{Name: "ethogram/novelty", Ours: true}, scopeEntity},
		"per-entity rival":   {series{Name: "entity_ewma"}, scopeEntity},
		"pooled comparator":  {series{Name: "lof"}, scopePopulation},
		"unknown comparator": {series{Name: "something_new"}, scopePopulation},
	}
	for name, tc := range cases {
		if got := scopeOf(tc.s); got != tc.want {
			t.Errorf("%s: scopeOf(%q) = %q, want %q", name, tc.s.Name, got, tc.want)
		}
	}
}

// TestScopeIsRecordedInTheCurveFile: the figure asserts a classification with its colours, so
// the classification has to be checkable from the recorded run rather than only from the source
// that drew it.
func TestScopeIsRecordedInTheCurveFile(t *testing.T) {
	fw := writeTmp(t, "fw.json", frameworkFixture(4_000_000, 500, map[int][2]int{10: {70, 7}}))
	bl := writeTmp(t, "bl.json", baselinesFixture(1_000_000, 4, 500, 7, 14,
		map[string]map[int]int{"entity_ewma": {10: 1}, "lof": {10: 0}}))
	dir := t.TempDir()
	outPath := filepath.Join(dir, "o.json")
	if err := run(fw, bl, outputs{curve: outPath, svg: filepath.Join(dir, "o.svg")}, "c-001", "", "composite", 0.01); err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	b, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	params, ok := out["parameters"].(map[string]any)
	if !ok {
		t.Fatal("the curve file records no parameters block")
	}
	byScope, ok := params["scope_by_series"].(map[string]any)
	if !ok {
		t.Fatal("the curve file records no scope_by_series")
	}
	for name, want := range map[string]string{
		"ethogram/composite": scopeEntity,
		"entity_ewma":        scopeEntity,
		"lof":                scopePopulation,
	} {
		if got := byScope[name]; got != want {
			t.Errorf("scope_by_series[%q] = %v, want %q", name, got, want)
		}
	}
}

// TestLegendListsTheStrongestComparatorFirst: the column beside the names counts labelled
// events, and a column of counts in no order reads as though it were in one.
func TestLegendListsTheStrongestComparatorFirst(t *testing.T) {
	all := []series{
		{Name: "hst", Points: []point{{TP: 1}}},
		{Name: "lof", Points: []point{{TP: 140}}},
		{Name: "ocsvm", Points: []point{{TP: 4}}},
	}
	var got []string
	for _, s := range legendGroup(all, scopePopulation) {
		got = append(got, s.Name)
	}
	if want := "lof,ocsvm,hst"; strings.Join(got, ",") != want {
		t.Fatalf("legend order %q, want %q", strings.Join(got, ","), want)
	}
}

// TestOurArmsLeadTheirFamily: inside a family this framework's arms come first and in the order
// they were asked for, so the figure is read against the arm the section discusses rather than
// against whichever one happened to reach the most.
func TestOurArmsLeadTheirFamily(t *testing.T) {
	all := []series{
		{Name: "entity_ewma", Points: []point{{TP: 82}}},
		{Name: "ethogram/novelty", Ours: true, Points: []point{{TP: 387}}},
		{Name: "ethogram/composite", Ours: true, Points: []point{{TP: 416}}},
	}
	got := legendGroup(all, scopeEntity)
	if got[0].Name != "ethogram/novelty" || got[1].Name != "ethogram/composite" {
		t.Fatalf("our arms must lead, in the order given; got %q then %q", got[0].Name, got[1].Name)
	}
	if got[2].Name != "entity_ewma" {
		t.Fatalf("the comparator must follow our arms; got %q", got[2].Name)
	}
}

// TestZeroPrecisionIsDrawnOnTheFloorRow: a budget at which a method found nothing has a
// precision of exactly zero, which no log axis can carry. It goes on its own row below a break
// rather than off the figure, because a line that vanishes reads as unmeasured.
func TestZeroPrecisionIsDrawnOnTheFloorRow(t *testing.T) {
	sc := scales{b0: 1, b1: 4, p0: -5, p1: 0}
	if got := sc.y(0); got != floorY {
		t.Errorf("y(0) = %v, want the floor row %v", got, floorY)
	}
	if got := sc.y(1e-5); got != plotB {
		t.Errorf("y(1e-5) = %v, want the axis foot %v", got, plotB)
	}
	if got := sc.y(1); got != plotT {
		t.Errorf("y(1) = %v, want the axis head %v", got, plotT)
	}
	if sc.x(10) >= sc.x(10000) {
		t.Error("the budget axis must increase to the right")
	}
}

// TestTheTwoFrameworkArmsAreDistinguishable is a regression test for the defect that prompted
// this figure's redesign. Both arms were drawn as a heavy solid line in the same colour, and
// both were named at their own right-hand end -- where they converge to within 0.03 decades --
// so the two labels overprinted and neither line could be told from the other.
func TestTheTwoFrameworkArmsAreDistinguishable(t *testing.T) {
	styles := assignStyles([]series{
		{Name: "ethogram/novelty", Ours: true},
		{Name: "ethogram/composite", Ours: true},
	})
	if styles["ethogram/novelty"].dash == styles["ethogram/composite"].dash {
		t.Fatalf("both arms drawn with dash %q; they share a colour and a weight, so the dash "+
			"is the only channel left to separate them", styles["ethogram/novelty"].dash)
	}
}

// TestEveryMethodIsNamedExactlyOnce is the other half of that regression. Identity now rests on
// a legend, which cannot collide, instead of on a label at each line's converging end.
func TestEveryMethodIsNamedExactlyOnce(t *testing.T) {
	all := []series{
		{Name: "ethogram/novelty", Label: "this framework, novelty arm", Ours: true,
			Points: []point{{Budget: 10, Alerts: 70, TP: 11, Precision: 0.157}}},
		{Name: "ethogram/composite", Label: "this framework, composite", Ours: true,
			Points: []point{{Budget: 10, Alerts: 70, TP: 0}}},
		{Name: "entity_ewma", Label: "entity_ewma",
			Points: []point{{Budget: 10, Alerts: 70, TP: 0}}},
		{Name: "lof", Label: "lof",
			Points: []point{{Budget: 10, Alerts: 70, TP: 0}}},
	}
	svg, err := renderSVG(all, nil, 4_190_603, 549, 7)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(svg, "<polyline"); n != len(all) {
		t.Errorf("%d polylines for %d series", n, len(all))
	}
	for _, want := range []string{
		">this framework &#183; novelty arm</text>",
		">this framework &#183; composite</text>",
		">entity_ewma</text>",
		">lof</text>",
	} {
		if n := strings.Count(svg, want); n != 1 {
			t.Errorf("%q appears %d times in the figure, want exactly 1", want, n)
		}
	}
}

// TestTwoSigFigsMatchesThePublishedLift: the abstract calls the tightest budget a lift of about
// 1,200, and the figure has to agree with it rather than print the 1,199 an integer rounding of
// 1,199.5 would give.
func TestTwoSigFigsMatchesThePublishedLift(t *testing.T) {
	for in, want := range map[float64]int{1199.5: 1200, 1: 1, 21.8: 22, 0.44: 0, 0: 0} {
		if got := twoSigFigs(in); got != want {
			t.Errorf("twoSigFigs(%v) = %d, want %d", in, got, want)
		}
	}
}
