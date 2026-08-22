package main

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/JohnPierman/ethogram/domain/detector"
	"github.com/JohnPierman/ethogram/domain/marginal"
	"github.com/JohnPierman/ethogram/domain/novelty"
	"github.com/JohnPierman/ethogram/domain/noveltyrate"
	"github.com/JohnPierman/ethogram/domain/routing"
)

// TestParseRoutingPinsWhatIsSupported fixes the flag's contract.
func TestParseRoutingPinsWhatIsSupported(t *testing.T) {
	for _, tc := range []struct {
		in      string
		want    routingMode
		wantErr bool
	}{
		{"none", routingOff, false},
		{"policy", routingOn, false},
		{"oracle", "", true},
		{"", "", true},
		{"Policy", "", true},
	} {
		got, err := parseRouting(tc.in)
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

// TestTheStatedPolicyIsTheArmsOwnThresholds is what keeps the preference order defensible from
// the requirements rather than from the labels. Every threshold has to be a constant an arm
// already declares, or it is a number chosen here — and a number chosen here, on a corpus whose
// labels are known, is the defect the issue names.
func TestTheStatedPolicyIsTheArmsOwnThresholds(t *testing.T) {
	policy, err := statedPolicy()
	if err != nil {
		t.Fatalf("statedPolicy: %v", err)
	}
	order := policy.Order()

	// The per-entity arms come before the population marginal, which is what §1.2 argues
	// for: an account that is permanently unusual is not thereby suspicious.
	if order[len(order)-1].Arm != string(marginal.DetectorID) {
		t.Errorf("the last arm in the order is %q, want the population marginal",
			order[len(order)-1].Arm)
	}
	if order[0].Arm != string(noveltyrate.DetectorID) {
		t.Errorf("the first arm is %q, want the scale-free per-entity arm",
			order[0].Arm)
	}

	// The thresholds trace to the arms' own declarations.
	byArm := map[string]routing.Requirement{}
	for _, r := range order {
		byArm[r.Arm] = r
	}
	if got := byArm[string(noveltyrate.DetectorID)].MinEvents; got != noveltyrate.MinHistory {
		t.Errorf("noveltyrate requires %d events, but the arm declares MinHistory = %d",
			got, noveltyrate.MinHistory)
	}
	// The marginal asks nothing of the entity: it is what remains when no per-entity null is
	// well specified, so a requirement on it would leave some entities unroutable for no
	// reason the framework states.
	m := byArm[string(marginal.DetectorID)]
	if m.MinEvents != 0 || m.MinDistinctValues != 0 || m.MinCompletedPeriods != 0 ||
		m.NeedsTimingSpread {
		t.Errorf("the population marginal carries a precondition: %+v", m)
	}
}

// TestARouterThatIsOffTouchesNothing is the default-path guarantee: every earlier run must
// remain reproducible, and the off mode must not need a nil check at any call site.
func TestARouterThatIsOffTouchesNothing(t *testing.T) {
	r, err := newRouter(routingOff)
	if err != nil {
		t.Fatalf("newRouter: %v", err)
	}
	if r.on() {
		t.Fatal("the off mode reports itself as on")
	}
	r.observeBurnIn("u1", nil)
	r.freeze(nil)
	r.observe(scoredForRouting{entity: "u1"}, 10)
	if len(r.profiles) != 0 || len(r.decided) != 0 || len(r.perDay) != 0 {
		t.Errorf("the off mode accumulated state: %d profiles, %d decisions, %d days",
			len(r.profiles), len(r.decided), len(r.perDay))
	}
	rec := r.record([]int{10})
	if rec["mode"] != string(routingOff) {
		t.Errorf("recorded mode is %v, want none", rec["mode"])
	}
	if _, err := json.Marshal(rec); err != nil {
		t.Errorf("the off record does not serialise: %v", err)
	}
}

// TestTheDecisionIsFrozenAtTheBoundary is the property that makes a routed number readable: a
// decision taken at scoring time would be fitted on the event it scores.
func TestTheDecisionIsFrozenAtTheBoundary(t *testing.T) {
	r, err := newRouter(routingOn)
	if err != nil {
		t.Fatalf("newRouter: %v", err)
	}
	// An established account, profiled over burn-in.
	r.profiles["settled"] = &entityProfile{
		events: 500, distinctValues: 40, completedPeriods: 12, hasTimingSpread: true,
	}
	// And one with almost nothing, which only the population arm can speak about.
	r.profiles["cold"] = &entityProfile{events: 3, distinctValues: 1}
	r.freeze(nil)

	if got := r.decided["settled"].Arm; got != string(noveltyrate.DetectorID) {
		t.Errorf("the settled account routed to %q, want the first admissible per-entity arm",
			got)
	}
	if got := r.decided["cold"].Arm; got != string(marginal.DetectorID) {
		t.Errorf("the cold account routed to %q, want the population marginal", got)
	}
	// The settled account satisfies more than one arm, which the record has to carry so a
	// different order can be evaluated later without a re-run.
	if len(r.decided["settled"].Admitted) < 2 {
		t.Errorf("the settled account records %v as admissible; more than one arm admits it",
			r.decided["settled"].Admitted)
	}
	if r.overrodeCount[string(noveltyrate.DetectorID)] != 1 {
		t.Errorf("the stated preference overrode another admissible arm %d times, want 1",
			r.overrodeCount[string(noveltyrate.DetectorID)])
	}

	// Observations after the boundary change nothing, and a second freeze is a no-op.
	before := r.decided["cold"].Arm
	r.observeBurnIn("cold", nil)
	r.freeze(nil)
	if r.decided["cold"].Arm != before {
		t.Error("a decision changed after the boundary")
	}
	if r.profiles["cold"].events != 3 {
		t.Errorf("the profile kept accumulating after freezing: %d events",
			r.profiles["cold"].events)
	}
}

// TestAnEntityNoArmAdmitsIsDeclined is R3 at the routing layer. It matters that this is
// possible at all: a router that always picks something has no way to say "no null here is well
// specified", which is the answer the framework insists on everywhere else.
func TestAnEntityNoArmAdmitsIsDeclined(t *testing.T) {
	// A policy of per-entity arms only, so an account with no history is unroutable. The
	// production order ends with the unconditional marginal precisely so this does not
	// happen there, and the mechanism still has to work.
	policy, err := routing.NewPolicy([]routing.Requirement{
		{Arm: string(noveltyrate.DetectorID), MinEvents: 50},
		{Arm: string(novelty.DetectorID), MinEvents: 20},
	})
	if err != nil {
		t.Fatalf("NewPolicy: %v", err)
	}
	r, err := newRouter(routingOn)
	if err != nil {
		t.Fatalf("newRouter: %v", err)
	}
	r.policy = policy
	r.profiles["cold"] = &entityProfile{events: 2}
	r.profiles["settled"] = &entityProfile{events: 900}
	r.freeze(nil)

	if r.abstained != 1 {
		t.Errorf("%d entities were declined, want 1", r.abstained)
	}
	if _, decided := r.decided["cold"]; decided {
		t.Error("an entity no arm admits was routed anyway")
	}

	// And its events are not silently attributed to some arm.
	r.observe(scoredForRouting{entity: "cold", byArm: nil}, 10)
	if r.scored != 0 {
		t.Errorf("%d events were scored for a declined entity", r.scored)
	}
	if r.unseen != 1 {
		t.Errorf("a declined entity's event was counted as %d unseen, want 1", r.unseen)
	}
}

// TestAnUncalibratedArmContributesNothing pins the charging rule at its boundary. The queue is
// ranked on the routed arm's conformal quantile because that is the one scale every arm shares;
// an arm with no burn-in distribution has no such quantile, and filing its model p-value in the
// same queue would be exactly the cross-scale comparison the rule exists to avoid.
func TestAnUncalibratedArmContributesNothing(t *testing.T) {
	r, err := newRouter(routingOn)
	if err != nil {
		t.Fatalf("newRouter: %v", err)
	}
	r.profiles["u1"] = &entityProfile{events: 500, distinctValues: 40}
	r.freeze(nil) // no conformal model at all
	arm := r.decided["u1"].Arm

	r.observe(scoredForRouting{
		entity: "u1", tSeconds: 100,
		byArm: map[detector.ID]float64{detector.ID(arm): math.Log(1e-9)},
	}, 10)

	if r.uncalibrated[arm] != 1 {
		t.Errorf("%d uncalibrated events recorded for %q, want 1", r.uncalibrated[arm], arm)
	}
	if len(r.perDay) != 0 {
		t.Error("an uncalibrated event reached the queue")
	}

	// An arm that abstained on the event is a different state and is counted separately: the
	// entity was routed to the arm whose null suits its history, and that arm declined.
	r.observe(scoredForRouting{entity: "u1", tSeconds: 100, byArm: nil}, 10)
	if r.noVerdict != 1 {
		t.Errorf("%d abstentions recorded, want 1", r.noVerdict)
	}
}

// TestTheRoutedRecordSerialises covers the whole block, since a result that cannot be written is
// a run thrown away — which has happened twice in this repository.
func TestTheRoutedRecordSerialises(t *testing.T) {
	r, err := newRouter(routingOn)
	if err != nil {
		t.Fatalf("newRouter: %v", err)
	}
	r.profiles["settled"] = &entityProfile{
		events: 500, distinctValues: 40, completedPeriods: 12, hasTimingSpread: true,
	}
	r.profiles["cold"] = &entityProfile{events: 1}
	r.freeze(nil)

	rec := r.record([]int{10, 100})
	body, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("the routed record does not serialise: %v", err)
	}
	if bad := nonFinite(rec); len(bad) > 0 {
		t.Errorf("the routed record carries values JSON cannot express: %v", bad)
	}
	for _, want := range []string{
		"preference_order", "entities_abstained", "entities", "charging", "order_note",
	} {
		if !containsKey(body, want) {
			t.Errorf("the record does not carry %q", want)
		}
	}
}

func containsKey(body []byte, key string) bool {
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return false
	}
	_, ok := doc[key]
	return ok
}
