package main

import (
	"fmt"
	"math"
	"sort"

	"github.com/JohnPierman/ethogram/domain/calibration"
	"github.com/JohnPierman/ethogram/domain/detector"
	"github.com/JohnPierman/ethogram/domain/drift"
	"github.com/JohnPierman/ethogram/domain/marginal"
	"github.com/JohnPierman/ethogram/domain/novelty"
	"github.com/JohnPierman/ethogram/domain/noveltyrate"
	"github.com/JohnPierman/ethogram/domain/routing"
	"github.com/JohnPierman/ethogram/domain/timing"
	"github.com/JohnPierman/ethogram/domain/volume"
)

// Per-entity routing, scored (#41).
//
// # Why this is the construction with headroom
//
// Expected detection is linear in a global allocation over arms, so the best global allocation
// is always a single arm and no reweighting can beat it -- a theorem, not a property of this
// corpus. And the best single arm guarantees nothing against an adversary choosing the
// mechanism, because every arm is blind to something.
//
// Routing is not a global allocation. It chooses per entity, so it can send an established
// account to a per-entity null and a cold one to the population null and collect both. The clue
// is already measured: on the planted corpus the population `marginal`'s detections at 100
// alerts a day overlap the per-entity arms' by zero.
//
// # The two decisions, stated before the run
//
// Both were fixed before anything was measured, because choosing either against the evaluation
// labels is the "quantities chosen on the outcome" defect Appendix F lists.
//
// **How alerts are charged: one queue, ranked by each arm's own conformal quantile.** A routed
// run does not naturally produce one ranking. Putting every arm's model p-value in one queue
// compares numbers from different nulls on different scales, which is precisely the defect the
// corrected minimum was measured failing on, reappearing one layer up. Giving each arm a share
// of the budget proportional to the entities routed to it makes the shares a free parameter and
// turns the result into a budget split, which is already measured to lose.
//
// A conformal p-value is a rank in the arm's own burn-in distribution, so it is the one scale
// every arm shares by construction -- super-uniform whatever that arm's null does, which is the
// property that makes the comparison meaningful rather than merely numeric. It is also already
// computed and frozen at the boundary for the combination, so routing reads it rather than
// inventing a second calibration.
//
// **The preference order.** Each arm's requirement is its own documented abstention threshold,
// not a number chosen here: `noveltyrate` states MinHistory = 50, `drift` needs MinWeight = 8
// closed periods, `timing` needs spread for a standardised circular statistic to exist. The
// order puts the arms that condition on the entity first and the population `marginal` last,
// which is what §1.2 argues for and what the framework's own abstention rules imply. It has
// never been shown to be the *best* such order, and searching for one against the evaluation
// labels would produce an oracle wearing a policy's clothes -- so [routing.Policy.Admitted]
// records every arm that would also have been admissible, and a different order can be
// evaluated from a recorded run without a re-run.

// routingMode is what the -route flag selects.
type routingMode string

const (
	routingOff routingMode = "none"
	routingOn  routingMode = "policy"
)

// parseRouting resolves the flag.
func parseRouting(s string) (routingMode, error) {
	switch routingMode(s) {
	case routingOff:
		return routingOff, nil
	case routingOn:
		return routingOn, nil
	default:
		return "", fmt.Errorf("unknown routing %q: want none or policy", s)
	}
}

// statedPolicy is the preference order this run uses, with each threshold taken from the arm's
// own abstention rule rather than chosen here.
func statedPolicy() (routing.Policy, error) {
	return routing.NewPolicy([]routing.Requirement{
		// The broadest per-entity arm first: it is scale-free by construction, comparing an
		// account's novelty rate against its own history rather than against another
		// account's volume, and it is the arm that reaches the most planted mechanisms.
		{Arm: string(noveltyrate.DetectorID), MinEvents: noveltyrate.MinHistory,
			MinDistinctValues: 2},
		// Then the per-entity novelty null, which needs a vocabulary to be estimated over.
		// Twenty events is the smallest history over which the reserved-mass estimate of
		// equation (4) is not simply the prior.
		{Arm: string(novelty.DetectorID), MinEvents: 20, MinDistinctValues: 2},
		// Timing only where the account has spread: without it there is nothing for a
		// standardised circular statistic to be formed against, which is the arm's own
		// documented abstention cause.
		{Arm: string(timing.DetectorID), MinEvents: 20, NeedsTimingSpread: true},
		// Drift needs its null's closed periods, and states how many.
		{Arm: string(drift.DetectorID), MinCompletedPeriods: drift.MinWeight},
		// The population marginal last and unconditionally: it is the arm that asks nothing
		// of the entity's own history, so it is what remains when the per-entity nulls are
		// not well specified. Last rather than absent, because an account with no history
		// is exactly the case §1.2 says a population null answers badly and still answers.
		{Arm: string(marginal.DetectorID)},
	})
}

// entityProfile accumulates, over the burn-in window, the facts a [routing.Profile] needs.
//
// Every field comes from a detector's own evidence rather than from a second pass over the
// values: the arms already compute these quantities to decide their own abstentions, and
// recomputing them here would let the two disagree.
type entityProfile struct {
	events           int64
	distinctValues   int64
	completedPeriods int64
	hasTimingSpread  bool
}

// router carries the profile pass, the frozen policy decision per entity, and the routed
// queue.
type router struct {
	mode   routingMode
	policy routing.Policy

	profiles map[string]*entityProfile
	// decided is the frozen routing decision per entity, taken once at the boundary. An
	// entity absent from it was not seen during burn-in.
	decided map[string]routedEntity
	model   *calibration.ConformalModel
	frozen  bool

	// Counters, all of which are measurements the issue asks for rather than diagnostics.
	routedTo      map[string]int64
	abstained     int64
	overrodeCount map[string]int64
	unseen        int64
	uncalibrated  map[string]int64
	noVerdict     int64
	scored        int64

	perDay  map[int64]*dayAlerts
	redTeam []redTeamScore
}

// routedEntity is one entity's frozen decision.
type routedEntity struct {
	Arm string `json:"arm"`
	// Admitted is every arm whose null the entity's profile also satisfies, so a different
	// preference order can be evaluated from a recorded run without a second replay.
	Admitted []string        `json:"admitted"`
	Profile  routing.Profile `json:"profile"`
}

func newRouter(mode routingMode) (*router, error) {
	policy, err := statedPolicy()
	if err != nil {
		return nil, fmt.Errorf("routing: the stated policy is malformed: %w", err)
	}
	return &router{
		mode:          mode,
		policy:        policy,
		profiles:      map[string]*entityProfile{},
		decided:       map[string]routedEntity{},
		routedTo:      map[string]int64{},
		overrodeCount: map[string]int64{},
		uncalibrated:  map[string]int64{},
		perDay:        map[int64]*dayAlerts{},
	}, nil
}

// on reports whether routing is being scored, so every call site is one guard.
func (r *router) on() bool { return r != nil && r.mode == routingOn }

// observeBurnIn folds one burn-in event's verdicts into its entity's profile.
func (r *router) observeBurnIn(entity string, verdicts detector.Verdicts) {
	if !r.on() || r.frozen {
		return
	}
	p, ok := r.profiles[entity]
	if !ok {
		p = &entityProfile{}
		r.profiles[entity] = p
	}
	p.events++

	for _, v := range verdicts {
		evidence := v.Evidence()
		switch v.DetectorID() {
		case novelty.DetectorID:
			// The entity's vocabulary size, taken as the largest any single field reached:
			// the novelty null is estimated per field, and the field with the most values
			// is the one the estimate is best supported on.
			if k, present := evidence.Stats["K"]; present && int64(k) > p.distinctValues {
				p.distinctValues = int64(k)
			}
		case volume.DetectorID:
			if c, present := evidence.Stats["completed_periods"]; present &&
				int64(c) > p.completedPeriods {
				p.completedPeriods = int64(c)
			}
		case timing.DetectorID:
			// Spread is the absence of the arm's own no-spread abstention. Reading the
			// cause rather than recomputing a variance keeps the two from disagreeing.
			if v.Reason() != timing.CauseNoSpread.String() {
				p.hasTimingSpread = true
			}
		}
	}
}

// freeze takes the routing decision for every entity seen during burn-in, once, before the
// first scored event, and holds the conformal model the queue is ranked on.
func (r *router) freeze(model *calibration.ConformalModel) {
	if !r.on() || r.frozen {
		return
	}
	r.frozen = true
	r.model = model

	entities := make([]string, 0, len(r.profiles))
	for e := range r.profiles {
		entities = append(entities, e)
	}
	sort.Strings(entities)

	for _, e := range entities {
		p := r.profiles[e]
		profile := routing.Profile{
			Events:           p.events,
			DistinctValues:   p.distinctValues,
			CompletedPeriods: p.completedPeriods,
			HasTimingSpread:  p.hasTimingSpread,
		}
		admitted := r.policy.Admitted(profile)
		arm, ok := r.policy.Route(profile)
		if !ok {
			// R3: no arm's null is well specified for this entity, so the router declines
			// rather than picking one. Counted, because how often that happens is a
			// measurement.
			r.abstained++
			continue
		}
		r.routedTo[arm]++
		if len(admitted) > 1 {
			// The stated preference chose over at least one other admissible arm. Counted
			// per winning arm, so the cost of the order is readable without a re-run.
			r.overrodeCount[arm]++
		}
		r.decided[e] = routedEntity{Arm: arm, Admitted: admitted, Profile: profile}
	}
}

// observe files one scored event into the routed queue, if its entity was routed.
//
// The ranking key is the routed arm's conformal p-value: a rank in that arm's own burn-in
// distribution, which is the one scale every arm shares. An arm with no conformal floor at all
// contributes nothing rather than contributing a model p-value on its own scale, which would
// reintroduce the cross-scale comparison this design exists to avoid.
func (r *router) observe(se scoredForRouting, topK int) {
	if !r.on() || !r.frozen {
		return
	}
	decision, ok := r.decided[se.entity]
	if !ok {
		// The entity produced no burn-in event, so it has no frozen profile and no
		// decision. Counted rather than routed to a default: a decision taken at scoring
		// time would be fitted on the event it scores.
		r.unseen++
		return
	}
	r.scored++

	logP, present := se.byArm[detector.ID(decision.Arm)]
	if !present {
		// The routed arm abstained on this event, which is a real answer: the entity was
		// routed to the arm whose null suits its history, and that arm declined to speak
		// about this event.
		r.noVerdict++
		return
	}
	if r.model == nil {
		r.uncalibrated[decision.Arm]++
		return
	}
	conformal, calibrated := r.model.Calibrate(decision.Arm, logP)
	if !calibrated {
		r.uncalibrated[decision.Arm]++
		return
	}

	day := se.tSeconds / 86400
	da, exists := r.perDay[day]
	if !exists {
		da = &dayAlerts{}
		r.perDay[day] = da
	}
	logConformal := math.Log(conformal)
	da.push(alert{
		P: conformal, LogP: logConformal, ModelLogP: logP,
		TSeconds: se.tSeconds, Entity: se.entity,
		SrcComp: se.srcComp, DstComp: se.dstComp, IsRedTeam: se.isRed, J: 1,
		MinDetector: decision.Arm, Categories: se.categories,
	}, topK)
	if se.isRed {
		r.redTeam = append(r.redTeam, redTeamScore{
			Key: se.key, P: conformal, LogP: logConformal, ModelLogP: logP,
			TSeconds: se.tSeconds, Entity: se.entity, J: 1, Categories: se.categories,
		})
	}
}

// scoredForRouting is what the router needs about one scored event, so the accumulator's own
// per-event locals are passed rather than recomputed.
type scoredForRouting struct {
	entity     string
	key        string
	srcComp    string
	dstComp    string
	tSeconds   int64
	isRed      bool
	categories []string
	byArm      map[detector.ID]float64
}

// record is the routed arm's block in the result.
func (r *router) record(budgets []int) map[string]any {
	if r == nil || r.mode == routingOff {
		return map[string]any{
			"mode": string(routingOff),
			"note": "per-entity routing was implemented but not scored in this run. It is " +
				"the only construction that can exceed the best single arm at equal cost, " +
				"because it is not a global allocation: expected detection is linear in a " +
				"global allocation, so the best one is always a single arm",
		}
	}

	days := make([]int64, 0, len(r.perDay))
	for d := range r.perDay {
		days = append(days, d)
	}
	sort.Slice(days, func(i, j int) bool { return days[i] < days[j] })

	detections := make(map[string]any, len(budgets))
	for _, b := range budgets {
		alerts, tp := 0, 0
		byMechanism := map[string]int{}
		for _, d := range days {
			day := r.perDay[d].alerts
			n := b
			if n > len(day) {
				n = len(day)
			}
			alerts += n
			for _, al := range day[:n] {
				if !al.IsRedTeam {
					continue
				}
				tp++
				for _, c := range al.Categories {
					byMechanism[c]++
				}
			}
		}
		detections[fmt.Sprintf("budget_%d_per_day", b)] = map[string]any{
			"alerts":         alerts,
			"true_positives": tp,
			"red_team_total": len(r.redTeam),
			"by_category":    byMechanism,
		}
	}

	order := make([]map[string]any, 0)
	for _, req := range r.policy.Order() {
		order = append(order, map[string]any{
			"arm":                   req.Arm,
			"min_events":            req.MinEvents,
			"min_distinct_values":   req.MinDistinctValues,
			"min_completed_periods": req.MinCompletedPeriods,
			"needs_timing_spread":   req.NeedsTimingSpread,
			"entities_routed":       r.routedTo[req.Arm],
			"overrode_another":      r.overrodeCount[req.Arm],
			"uncalibrated_events":   r.uncalibrated[req.Arm],
		})
	}

	return map[string]any{
		"mode":                 string(r.mode),
		"detections_at_budget": detections,
		"preference_order":     order,
		"entities_profiled":    len(r.profiles),
		"entities_routed":      len(r.decided),
		"entities_abstained":   r.abstained,
		"events_scored":        r.scored,
		"events_unseen_entity": r.unseen,
		"events_arm_abstained": r.noVerdict,
		"charging": "one queue ranked by the routed arm's conformal p-value, which is a rank " +
			"in that arm's own burn-in distribution and therefore the one scale every arm " +
			"shares. Stated before the run: putting model p-values from different nulls in " +
			"one queue is the cross-scale comparison the corrected minimum was measured " +
			"failing on, and giving each arm a budget share makes the shares a free " +
			"parameter and the result a budget split, which is already measured to lose",
		"order_note": "each threshold is the arm's own documented abstention rule rather than " +
			"a number chosen here, and the order puts the arms that condition on the entity " +
			"before the population marginal. It has not been shown to be the best such " +
			"order; searching for one against the evaluation labels would be an oracle " +
			"wearing a policy's clothes, so every also-admissible arm is recorded per " +
			"entity and a different order can be evaluated without a re-run",
		"abstention_note": "an entity no arm admits is declined rather than assigned, which " +
			"is R3 at the routing layer. An entity with no burn-in event has no frozen " +
			"profile at all, and its events are counted separately: a decision taken at " +
			"scoring time would be fitted on the event it scores",
		"entities": r.decidedRecord(),
	}
}

// decidedRecord is the per-entity decision, so the routing is auditable rather than asserted.
//
// Every routed entity, not a sample: it is bounded by the number of entities rather than by
// events, which is the same reason every entity-day is kept.
func (r *router) decidedRecord() map[string]any {
	out := make(map[string]any, len(r.decided))
	for entity, decision := range r.decided {
		out[entity] = decision
	}
	return out
}
