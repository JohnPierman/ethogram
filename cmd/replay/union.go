package main

import (
	"fmt"
	"sort"

	"github.com/JohnPierman/ethogram/domain/cooccurrence"
	"github.com/JohnPierman/ethogram/domain/detector"
	"github.com/JohnPierman/ethogram/domain/marginal"
)

// The union arm: alert when ANY detector ranks an event highly.
//
// Both existing combinations ask the detectors to agree, and both pay for it. Fisher (18)
// sums log p-values and so averages an informative detector with uninformative ones. The
// corrected minimum (16) asks the right question -- is any single detector extreme -- but
// answers it by comparing raw p-values across detectors that share no scale, which is why
// one arm carries the overwhelming majority of that arm's queue and the others are crowded
// out of a budget they were never really allowed to spend.
//
// This arm asks the minimum's question and removes the scale from the answer. Every
// contributing arm ranks the day on its own p-value alone; an event's fused score is the
// BEST rank it reaches in any arm. An event ranked first by one detector is ranked first
// here whatever the others made of it, so a detector whose p-values are numerically large
// cannot be displaced by one whose p-values are numerically small. Only the within-arm
// ORDER is read. No p-value is compared across arms anywhere in this file.
//
// The arms' alert sets are not disjoint, and an event several arms rank highly is one
// alert, not several. Alerts are deduplicated on the same (t, entity, src, dst) identity
// the min-p arm reports its labelled events under, and the arms that carried each alert
// are recorded so the queue's composition is visible rather than asserted.
//
// What this arm is NOT: a p-value. No null distributes a fused rank, so this arm reports a
// selection and not a calibrated tail probability. It is admissible because R6 no longer
// asks alert volume to follow from a stated error rate; the objective is expected utility
// over the selection. Per-alert evidence is unaffected, since each verdict still carries
// the p-value of the detector that raised it (R5).

// fusedAlert is one deduplicated union alert: the event, the best rank any contributing
// arm gave it, and every arm that ranked it inside the depth considered.
type fusedAlert struct {
	al       alert
	rank     int
	carriers []detector.ID
}

// populationScopeArms are the arms that ask about the population rather than the entity.
//
// They are named here so a union can be taken over the entity-scope arms alone. That
// grouping follows the design's own argument -- the entity is the unit of analysis -- and
// never which arms scored well, so it costs no labels and is not an oracle choice.
var populationScopeArms = map[detector.ID]bool{
	cooccurrence.DetectorID: true,
	marginal.DetectorID:     true,
}

// alertIdentity is the deduplication key: the same four fields, in the same order, that
// the min-p arm reports a labelled event under, so a key means one thing in this file and
// in every result it is compared against.
func alertIdentity(al alert) string {
	return fmt.Sprintf("%d|%s|%s|%s", al.TSeconds, al.Entity, al.SrcComp, al.DstComp)
}

// fusedLess is the deterministic total order on union alerts: best rank first, then the
// canonical event identity.
//
// The tie-break is the identity and NOT the p-value, which matters more than it looks.
// Rank collisions here are not rare, they are structural: every contributing arm has a
// rank 1, so J arms collide at every rank. Breaking those ties on log p would hand each
// collision to whichever detector's p-values are numerically smallest, reintroducing at
// the tie-break the exact bias this arm exists to remove. An identity carries no scale.
func fusedLess(a, b fusedAlert) bool {
	if a.rank != b.rank {
		return a.rank < b.rank
	}
	if a.al.TSeconds != b.al.TSeconds {
		return a.al.TSeconds < b.al.TSeconds
	}
	if a.al.Entity != b.al.Entity {
		return a.al.Entity < b.al.Entity
	}
	if a.al.SrcComp != b.al.SrcComp {
		return a.al.SrcComp < b.al.SrcComp
	}
	return a.al.DstComp < b.al.DstComp
}

// fuseDay returns the deduplicated union of the given arms' top `depth` alerts for one
// day, ordered best-fused-rank first.
//
// Insertion order is collected separately and the result sorted by fusedLess, so no map
// iteration order reaches the output (R4).
func fuseDay(byArm map[detector.ID]*dayAlerts, ids []detector.ID, depth int) []fusedAlert {
	seen := make(map[string]*fusedAlert)
	order := make([]string, 0, depth*len(ids))

	for _, id := range ids {
		da, ok := byArm[id]
		if !ok || da == nil {
			continue
		}
		n := depth
		if n > len(da.alerts) {
			n = len(da.alerts)
		}
		for i, al := range da.alerts[:n] {
			order = appendFused(seen, order, al, i+1, id)
		}
	}

	out := make([]fusedAlert, 0, len(order))
	for _, k := range order {
		out = append(out, *seen[k])
	}
	sort.Slice(out, func(i, j int) bool { return fusedLess(out[i], out[j]) })
	return out
}

// appendFused files one arm's ranked alert into the union, recording the arm as a carrier
// and keeping the best rank the alert has reached so far.
func appendFused(seen map[string]*fusedAlert, order []string, al alert, rank int,
	id detector.ID) []string {
	k := alertIdentity(al)
	f, ok := seen[k]
	if !ok {
		seen[k] = &fusedAlert{al: al, rank: rank, carriers: []detector.ID{id}}
		return append(order, k)
	}
	f.carriers = append(f.carriers, id)
	if rank < f.rank {
		f.rank = rank
		// Keep the record from the arm that ranked it best, so the p-value reported
		// alongside the alert is the one that earned it its position.
		f.al = al
	}
	return order
}

// unionArmDays is every day any contributing arm produced alerts on, ascending.
func (a *accumulator) unionArmDays(ids []detector.ID) []int64 {
	present := make(map[int64]bool)
	for _, id := range ids {
		for d := range a.detectorPerDay[id] {
			present[d] = true
		}
	}
	days := make([]int64, 0, len(present))
	for d := range present {
		days = append(days, d)
	}
	sort.Slice(days, func(i, j int) bool { return days[i] < days[j] })
	return days
}

// unionArmIDs lists the arms in a grouping, ascending, so a union is deterministic.
func (a *accumulator) unionArmIDs(entityOnly bool) []detector.ID {
	ids := make([]detector.ID, 0, len(a.detectorPerDay))
	for id := range a.detectorPerDay {
		if entityOnly && populationScopeArms[id] {
			continue
		}
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// unionRedTeamScored counts the labelled events at least one contributing arm evaluated.
// That is the union's denominator: an arm cannot detect what it never scored.
func (a *accumulator) unionRedTeamScored(ids []detector.ID) int {
	keys := make(map[string]bool)
	for _, id := range ids {
		for _, rt := range a.detectorRedTeamScored[id] {
			keys[rt.Key] = true
		}
	}
	return len(keys)
}

// unionCounts is one accounting of a union queue.
type unionCounts struct {
	alerts    int
	truePos   int
	carriedBy map[detector.ID]int
	exclusive map[detector.ID]int
	// caught names the labelled events inside the queue, keyed as the run's
	// red_team_scored list keys them.
	caught map[string]bool
}

func newUnionCounts() *unionCounts {
	return &unionCounts{
		carriedBy: make(map[detector.ID]int),
		exclusive: make(map[detector.ID]int),
		caught:    make(map[string]bool),
	}
}

// labelledKey identifies a labelled event the way the run's red_team_scored list does:
// time and entity, without the components.
//
// This arm has to record WHICH labelled events it caught and cannot leave that to be
// reconstructed later. A per-detector arm's catch is recoverable after the fact, because
// ranking the labelled events on that detector's p-value reproduces the arm's own order.
// A fused rank is not a p-value and no labelled event carries one, so nothing downstream
// could rebuild this queue. Without these keys every per-attack-type row for the union
// would be blank.
func labelledKey(al alert) string {
	return fmt.Sprintf("%d|%s", al.TSeconds, al.Entity)
}

// caughtKeys is the caught set, sorted, so the output is deterministic (R4).
func (c *unionCounts) caughtKeys() []string {
	out := make([]string, 0, len(c.caught))
	for k := range c.caught {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// tally folds one day's fused alerts, truncated to limit, into the counts. A limit of 0
// means no truncation: emit the whole deduplicated union.
func (c *unionCounts) tally(day []fusedAlert, limit int) {
	n := len(day)
	if limit > 0 && limit < n {
		n = limit
	}
	c.alerts += n
	for _, f := range day[:n] {
		if f.al.IsRedTeam {
			c.truePos++
			c.caught[labelledKey(f.al)] = true
		}
		for _, id := range f.carriers {
			c.carriedBy[id]++
		}
		if len(f.carriers) == 1 && f.al.IsRedTeam {
			c.exclusive[f.carriers[0]]++
		}
	}
}

// report renders the counts, with precision so a reader is not left to divide.
func (c *unionCounts) report(redTeamTotal, permitted int) map[string]any {
	out := map[string]any{
		"alerts":         c.alerts,
		"true_positives": c.truePos,
		"red_team_total": redTeamTotal,
		"carried_by":     idCounts(c.carriedBy),
		// A labelled alert only one arm ranked inside the depth: exactly what
		// dropping that arm from the union would cost.
		"exclusive_true_positives": idCounts(c.exclusive),
		// Which labelled events, not just how many. A fused rank cannot be
		// reconstructed downstream, so the per-attack-type table depends on this.
		"caught_red_team": c.caughtKeys(),
		// truePos counts alerts and this counts distinct labelled events. They
		// differ when one labelled (t, entity) produced alerts on several
		// component pairs, and a per-type table must use the distinct count.
		"distinct_true_positives": len(c.caught),
	}
	if c.alerts > 0 {
		out["precision"] = float64(c.truePos) / float64(c.alerts)
	}
	if permitted > 0 {
		out["budget_multiple"] = float64(c.alerts) / float64(permitted)
	}
	return out
}

func idCounts(m map[detector.ID]int) map[string]int {
	out := make(map[string]int, len(m))
	for id, n := range m {
		out[string(id)] = n
	}
	return out
}

// unionGrouping measures one grouping of arms at every budget, under both accountings.
func (a *accumulator) unionGrouping(ids []detector.ID, budgets []int) map[string]any {
	days := a.unionArmDays(ids)
	redTeam := a.unionRedTeamScored(ids)

	atCost := make(map[string]any, len(budgets))
	atDepth := make(map[string]any, len(budgets))
	for _, b := range budgets {
		cost, depth := newUnionCounts(), newUnionCounts()
		for _, d := range days {
			fused := fuseDay(a.armsOnDay(ids, d), ids, b)
			cost.tally(fused, b)
			depth.tally(fused, 0)
		}
		key := fmt.Sprintf("budget_%d_per_day", b)
		atCost[key] = cost.report(redTeam, b*len(days))
		atDepth[key] = depth.report(redTeam, b*len(days))
	}

	return map[string]any{
		"arms":            armNames(ids),
		"red_team_scored": redTeam,
		"at_equal_cost":   atCost,
		"at_equal_depth":  atDepth,
	}
}

// armsOnDay gathers one day's alert list from each contributing arm.
func (a *accumulator) armsOnDay(ids []detector.ID, day int64) map[detector.ID]*dayAlerts {
	byArm := make(map[detector.ID]*dayAlerts, len(ids))
	for _, id := range ids {
		byArm[id] = a.detectorPerDay[id][day]
	}
	return byArm
}

func armNames(ids []detector.ID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, string(id))
	}
	return out
}

// unionArmResults reports the union arm beside the composite and the per-detector arms.
func (a *accumulator) unionArmResults(budgets []int) map[string]any {
	return map[string]any{
		"combination": "rank fusion over the per-detector arms: an alert's score is the " +
			"best rank it reaches in any contributing arm, deduplicated on " +
			"(t, entity, src, dst), ties broken on that identity and never on a p-value",
		"rationale": "the corrected minimum (16) asks whether any single detector is " +
			"extreme, which is the right question, but compares raw p-values across " +
			"detectors sharing no scale, so the arm whose p-values are numerically " +
			"smallest takes the budget. Reading only the within-arm order asks the same " +
			"question with the scale removed. This arm reports a selection, not a " +
			"p-value: no null distributes a fused rank, and R6 no longer requires one",
		"accountings": "at_equal_cost truncates the union to the same alerts-per-day " +
			"every other arm is allowed, so it is comparable to them. at_equal_depth " +
			"lets every arm keep its own top B and emits the whole deduplicated union, " +
			"which costs more than B per day; budget_multiple states how much more",
		"all_arms":          a.unionGrouping(a.unionArmIDs(false), budgets),
		"entity_scope_arms": a.unionGrouping(a.unionArmIDs(true), budgets),
	}
}
