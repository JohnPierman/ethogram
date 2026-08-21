package routing_test

import (
	"testing"

	"github.com/JohnPierman/ethogram/domain/routing"
)

// The policy the framework's own abstention rules imply, in the order the paper's evidence
// supports: the per-entity arms first where their history supports them, the population
// marginal as the arm that needs nothing of the entity, and timing only where the account has
// spread for a standardised circular statistic to exist.
//
// Stated, not fitted. Reordering it to maximise detections on the evaluation labels would make
// the result an oracle.
func standardPolicy(t *testing.T) routing.Policy {
	t.Helper()
	p, err := routing.NewPolicy([]routing.Requirement{
		{Arm: "noveltyrate", MinEvents: 50, MinDistinctValues: 2},
		{Arm: "novelty", MinEvents: 20, MinDistinctValues: 2},
		{Arm: "timing", MinEvents: 20, NeedsTimingSpread: true},
		{Arm: "drift", MinCompletedPeriods: 8},
		{Arm: "marginal"},
	})
	if err != nil {
		t.Fatalf("NewPolicy: %v", err)
	}
	return p
}

// TestRouteSendsAnEstablishedAccountToItsOwnNull. The framework's premise is that an account
// with history should be judged against its own behaviour, and the routing policy has to
// express that rather than contradict it.
func TestRouteSendsAnEstablishedAccountToItsOwnNull(t *testing.T) {
	p := standardPolicy(t)
	arm, ok := p.Route(routing.Profile{
		Events: 4000, DistinctValues: 60, CompletedPeriods: 30, HasTimingSpread: true,
	})
	if !ok {
		t.Fatal("abstained on an account with ample history")
	}
	if arm != "noveltyrate" {
		t.Errorf("routed to %q, want noveltyrate", arm)
	}
}

// TestRouteSendsAColdAccountToThePopulationNull. The complementarity the headroom rests on: an
// account whose own history cannot support a per-entity null is exactly the account a
// population-scope arm can still say something about, and routing it there is the whole point.
func TestRouteSendsAColdAccountToThePopulationNull(t *testing.T) {
	p := standardPolicy(t)
	arm, ok := p.Route(routing.Profile{Events: 3, DistinctValues: 1})
	if !ok {
		t.Fatal("abstained on a cold account the population arm can still score")
	}
	if arm != "marginal" {
		t.Errorf("routed to %q, want marginal", arm)
	}
}

// TestRouteSkipsAnArmWhoseNullTheEntityCannotSupport. A perfectly regular account has no
// spread for a standardised circular statistic, which is the abstention section 6.2 records.
// The router must pass over that arm rather than send the entity to a detector that will then
// abstain, since a routed entity that abstains has spent its assignment on nothing.
func TestRouteSkipsAnArmWhoseNullTheEntityCannotSupport(t *testing.T) {
	p := standardPolicy(t)
	// Enough events for timing's history but no spread, and too few distinct values for
	// either novelty arm.
	arm, ok := p.Route(routing.Profile{
		Events: 400, DistinctValues: 1, CompletedPeriods: 20, HasTimingSpread: false,
	})
	if !ok {
		t.Fatal("abstained where an arm was still admissible")
	}
	if arm == "timing" {
		t.Error("routed to timing, which has no spread to standardise against here")
	}
	if arm != "drift" {
		t.Errorf("routed to %q, want drift", arm)
	}
}

// TestRouteAbstainsWhereNoNullIsWellSpecified. R3, at the routing layer. An entity no
// detector can speak about must produce no assignment; routing it anyway would manufacture an
// opinion out of the preference order.
func TestRouteAbstainsWhereNoNullIsWellSpecified(t *testing.T) {
	p, err := routing.NewPolicy([]routing.Requirement{
		{Arm: "noveltyrate", MinEvents: 50, MinDistinctValues: 2},
		{Arm: "drift", MinCompletedPeriods: 8},
	})
	if err != nil {
		t.Fatalf("NewPolicy: %v", err)
	}
	if arm, ok := p.Route(routing.Profile{Events: 1}); ok {
		t.Errorf("routed a first-ever event to %q, want an abstention", arm)
	}
}

// TestAdmittedRecordsWhatThePreferenceOverrode. The cost of a stated preference is how often it
// passed over another admissible arm, and that is only measurable if the alternatives are
// recorded. A policy that reported only its choice could not be evaluated against a different
// order without re-running.
func TestAdmittedRecordsWhatThePreferenceOverrode(t *testing.T) {
	p := standardPolicy(t)
	profile := routing.Profile{
		Events: 4000, DistinctValues: 60, CompletedPeriods: 30, HasTimingSpread: true,
	}
	admitted := p.Admitted(profile)
	if len(admitted) < 2 {
		t.Fatalf("Admitted = %v, want several arms for an account with ample history", admitted)
	}
	arm, ok := p.Route(profile)
	if !ok || arm != admitted[0] {
		t.Errorf("Route returned %q but Admitted leads with %q", arm, admitted[0])
	}
	for _, want := range []string{"noveltyrate", "novelty", "timing", "drift", "marginal"} {
		found := false
		for _, got := range admitted {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Errorf("%s is admissible for this profile but not reported", want)
		}
	}
}

// TestRequirementAdmitsEachThresholdIndependently. Every threshold must bind on its own, or a
// profile failing one condition would be admitted because it passed the others.
func TestRequirementAdmitsEachThresholdIndependently(t *testing.T) {
	r := routing.Requirement{
		Arm: "x", MinEvents: 10, MinDistinctValues: 3,
		MinCompletedPeriods: 4, NeedsTimingSpread: true,
	}
	full := routing.Profile{
		Events: 10, DistinctValues: 3, CompletedPeriods: 4, HasTimingSpread: true,
	}
	if !r.Admits(full) {
		t.Fatal("a profile exactly at every threshold was refused")
	}
	for name, p := range map[string]routing.Profile{
		"one event short":  {Events: 9, DistinctValues: 3, CompletedPeriods: 4, HasTimingSpread: true},
		"one value short":  {Events: 10, DistinctValues: 2, CompletedPeriods: 4, HasTimingSpread: true},
		"one period short": {Events: 10, DistinctValues: 3, CompletedPeriods: 3, HasTimingSpread: true},
		"no timing spread": {Events: 10, DistinctValues: 3, CompletedPeriods: 4, HasTimingSpread: false},
		"empty profile":    {},
	} {
		if r.Admits(p) {
			t.Errorf("%s: admitted, want refused", name)
		}
	}
}

// TestNewPolicyRejectsAMalformedOrder. The order is the policy, so a duplicate or unnamed arm
// is a defect in the statement of it rather than something to resolve silently.
func TestNewPolicyRejectsAMalformedOrder(t *testing.T) {
	for name, order := range map[string][]routing.Requirement{
		"empty":         {},
		"unnamed arm":   {{Arm: ""}},
		"duplicate arm": {{Arm: "a"}, {Arm: "a"}},
		"negative events": {
			{Arm: "a", MinEvents: -1},
		},
		"negative periods": {
			{Arm: "a", MinCompletedPeriods: -5},
		},
		"negative values": {
			{Arm: "a", MinDistinctValues: -2},
		},
	} {
		if _, err := routing.NewPolicy(order); err == nil {
			t.Errorf("%s: accepted, want an error", name)
		}
	}
}

// TestPolicyDoesNotAliasItsOrder. The policy is a value object: a caller mutating the slice it
// passed in, or the slice it read back, must not change how entities are routed.
func TestPolicyDoesNotAliasItsOrder(t *testing.T) {
	order := []routing.Requirement{
		{Arm: "noveltyrate", MinEvents: 50},
		{Arm: "marginal"},
	}
	p, err := routing.NewPolicy(order)
	if err != nil {
		t.Fatalf("NewPolicy: %v", err)
	}
	order[0].Arm = "tampered"
	read := p.Order()
	read[1].Arm = "also tampered"

	arm, ok := p.Route(routing.Profile{Events: 100})
	if !ok || arm != "noveltyrate" {
		t.Errorf("routing changed to %q after the caller's slice was mutated", arm)
	}
	if again := p.Order(); again[1].Arm != "marginal" {
		t.Errorf("Order() returned an aliased slice: second arm is now %q", again[1].Arm)
	}
}
