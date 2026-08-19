package cooccurrence

import (
	"context"
	"fmt"
	"math"
	"strconv"

	"github.com/JohnPierman/ethogram/domain/calibration"
	"github.com/JohnPierman/ethogram/domain/detector"
	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/novelty"
	"github.com/JohnPierman/ethogram/domain/registry"
)

// DetectorID names Detector III in result JSON, dashboard labels, and the
// per-detector calibration sets of §10.1.
const DetectorID = detector.ID("cooccurrence")

// FieldRegistry is the view of the §5.1 registry this detector needs: which fields
// a source carries with what kind. *registry.Registry satisfies it. Eligibility —
// neither identifier nor excluded, and settled — is the entries' Kind.IsEligible(),
// which is what keeps an identifier field from rendering every one of its graph
// nodes a singleton and dissolving the block structure (§5.1, §12.5).
type FieldRegistry interface {
	// FindBySource returns the entries for a source in ascending field-path order.
	FindBySource(source event.SourceID) []*registry.Entry
}

// Detector is Detector III: population co-occurrence (§8).
//
// H₀: each pair's decayed co-occurrence weight w_ij is drawn from the Poisson-DCSBM
// null of equation (13), priced by the maximum-likelihood rates of (14) against the
// offline partition, or by the single-block (15) when no partition is held.
type Detector struct {
	// id overrides DetectorID for ablation arms; empty means the canonical id.
	id detector.ID

	// readOnly suppresses state updates: Score still returns verdicts but its
	// observation is inert. An ablation arm sharing the primary's graph must not
	// also fold events into it, or every co-occurrence weight would be counted once
	// per arm and both arms would score against a graph neither would see in
	// production.
	readOnly bool

	graph    GraphRepository
	registry FieldRegistry

	// partition is the offline Leiden result (§8.2); nil runs the detector in
	// fallback mode, where every pair degenerates to (15) and says so.
	partition *Partition

	halfLife novelty.HalfLife
}

// NewDetector wires Detector III. partition may be nil: the detector then runs in
// fallback mode until the offline batch supplies one via SetPartition. halfLife is
// T½ of §6.2, under which the graph repository decays its rows.
func NewDetector(graph GraphRepository, reg FieldRegistry, partition *Partition, halfLife novelty.HalfLife) *Detector {
	return &Detector{
		graph:     graph,
		registry:  reg,
		partition: partition,
		halfLife:  halfLife,
	}
}

// SetPartition swaps in a freshly computed offline partition (§8.2). The scheduled
// batch calls it between events, never during a Score: scoring reads the partition
// without a lock, so the swap must be serialised with scoring, which the
// single-threaded replay loop provides by construction.
func (d *Detector) SetPartition(p *Partition) { d.partition = p }

// ID implements detector.Detector.
func (d *Detector) ID() detector.ID {
	if d.id != "" {
		return d.id
	}
	return DetectorID
}

// WithID returns a copy of the detector reporting a different identifier.
//
// It exists for ablation arms (E4): the two arms of the co-occurrence comparison are
// the same detector over the same graph, differing only in whether a partition is
// installed, so they must be distinguishable in verdicts, histograms and status
// counts. Without a distinct identifier the two arms' statistics would merge into one
// and the ablation would measure nothing.
func (d *Detector) WithID(id detector.ID) *Detector {
	c := *d
	c.id = id
	return &c
}

// ReadOnly returns a copy whose observations are inert.
//
// The E4 ablation arms share one graph: the primary maintains it, and the arm under
// comparison must read the identical state without contributing to it. Without this
// the shared graph would receive each event's co-occurrences twice.
func (d *Detector) ReadOnly() *Detector {
	c := *d
	c.readOnly = true
	return &c
}

// NullHypothesis implements detector.Detector, stating H₀ as §5.2 requires.
func (d *Detector) NullHypothesis() string {
	return "each pair's decayed co-occurrence weight is drawn from the Poisson-DCSBM " +
		"null w_ij ~ Poisson(θ_i θ_j ω_{z(i)z(j)}) of equation (13), with the " +
		"maximum-likelihood rates of §8.3"
}

// Score evaluates every pair of the event's eligible fields against the population
// graph and emits ONE verdict per event (§8.5): the minimising pair's lower tail,
// Šidák-corrected by equation (16) for the T = F_e(F_e−1)/2 tests the event
// induced.
//
// Nodes are collected in the registry's sorted field order and pairs enumerated in
// (i < j) order, so every accumulation and every tie-break runs over a fixed
// sequence (R4). The graph is read strictly pre-event: the pairs the event itself
// carries reach the graph only through the returned Observation (§5.2).
func (d *Detector) Score(ctx context.Context, e *event.Event) (detector.Verdicts, detector.Observation, error) {
	nodes := d.eligibleNodes(e)
	if len(nodes) < 2 {
		return d.abstainNoPair(e, len(nodes))
	}

	// m is a graph-level scalar: fetched once per event, not per pair, so every
	// pair is priced against the same total.
	total, err := d.graph.FindTotalWeight(ctx, e.Source(), e.OccurredAt())
	if err != nil {
		return nil, nil, fmt.Errorf("cooccurrence: find total weight: %w", err)
	}
	degrees, err := d.findDegrees(ctx, e, nodes)
	if err != nil {
		return nil, nil, err
	}
	best, tests, err := d.minimisingPair(ctx, e, nodes, degrees, total)
	if err != nil {
		return nil, nil, err
	}

	verdict, err := detector.NewEvaluatedLog(d.ID(),
		detector.Target{
			Event:  e.ID(),
			Entity: e.Entity(),
			Fields: []event.FieldPath{nodes[best.i].Field, nodes[best.j].Field},
		},
		calibration.SidakLog(best.logP, tests),
		d.evidence(nodes, degrees, total, tests, best))
	if err != nil {
		return nil, nil, fmt.Errorf("cooccurrence: verdict: %w", err)
	}

	if d.readOnly {
		return detector.Verdicts{verdict},
			detector.NoObservation{Event: e.ID(), Detector: d.ID()}, nil
	}
	obs := &observation{
		graph:      d.graph,
		source:     e.Source(),
		at:         e.OccurredAt(),
		eventID:    e.ID(),
		detectorID: d.ID(),
		pending:    allPairs(nodes),
	}
	return detector.Verdicts{verdict}, obs, nil
}

// eligibleNodes collects the event's present, usable values for the source's
// eligible fields, in the registry's ascending field-path order: the fixed
// enumeration every pair loop inherits (R4). An identifier or excluded field
// contributes no node, and an unsettled kind is withheld rather than admitted by
// default (§5.1).
//
// A node's value is the field's vocabulary item rather than the value's own text
// ([registry.FieldKind.Token]). The two differ only for a continuous field, where the
// node is a magnitude band: a node per raw measurement would make every node a
// singleton and dissolve the block structure this detector depends on, which is the
// failure §8.2's binning exists to prevent. A value that does not project contributes
// no node, exactly as an unusable one does.
func (d *Detector) eligibleNodes(e *event.Event) []NodeID {
	entries := d.registry.FindBySource(e.Source())
	out := make([]NodeID, 0, len(entries))
	for _, entry := range entries {
		if !entry.Kind.IsEligible() {
			continue
		}
		value, present := e.Get(entry.Path)
		if !present || !value.IsUsable() {
			continue
		}
		token, projected := entry.Kind.Token(value.Text())
		if !projected {
			continue
		}
		out = append(out, NodeID{Field: entry.Path, Value: token})
	}
	return out
}

// abstainNoPair reports the fewer-than-two-node case. The status is
// abstained_unusable, not structural: the source does produce these fields — this
// event simply carries too few of them to induce a pair.
func (d *Detector) abstainNoPair(e *event.Event, eligible int) (detector.Verdicts, detector.Observation, error) {
	v, err := detector.NewAbstained(d.ID(),
		detector.Target{Event: e.ID(), Entity: e.Entity()},
		detector.StatusAbstainedUnusable,
		"fewer than two eligible fields present; no pair to test",
		detector.NewEvidence([]int{16}, map[string]float64{
			"F_e": float64(eligible),
		}, nil))
	if err != nil {
		return nil, nil, fmt.Errorf("cooccurrence: abstain: %w", err)
	}
	return detector.Verdicts{v}, detector.NoObservation{Event: e.ID(), Detector: d.ID()}, nil
}

// findDegrees reads each node's pre-event degree once, in node order.
func (d *Detector) findDegrees(ctx context.Context, e *event.Event, nodes []NodeID) ([]float64, error) {
	out := make([]float64, len(nodes))
	for i, n := range nodes {
		k, err := d.graph.FindDegree(ctx, e.Source(), n, e.OccurredAt())
		if err != nil {
			return nil, fmt.Errorf("cooccurrence: find degree of %q: %w", n.Field, err)
		}
		out[i] = k
	}
	return out, nil
}

// minPair records the pair currently holding the minimum P_ij, with the sufficient
// statistics its evidence will need.
type minPair struct {
	i, j int
	// logP is ln P_ij. The minimum is tracked in log space because the linear tail
	// underflows across the whole region the minimum is drawn from.
	logP         float64
	lambda       float64
	w            float64
	usedFallback bool
}

// minimisingPair prices every pair in fixed (i < j) order and tracks the minimum
// P_ij. The comparison is strict, so a tie resolves to the first pair in the fixed
// iteration order, deterministically (R4).
func (d *Detector) minimisingPair(ctx context.Context, e *event.Event, nodes []NodeID, degrees []float64, total float64) (minPair, int, error) {
	best := minPair{logP: math.Inf(1)} // above any log probability; the first pair claims it
	tests := 0
	for i := range nodes {
		for j := i + 1; j < len(nodes); j++ {
			w, err := d.graph.FindEdgeWeight(ctx, e.Source(), nodes[i], nodes[j], e.OccurredAt())
			if err != nil {
				return minPair{}, 0, fmt.Errorf("cooccurrence: find edge weight (%q, %q): %w",
					nodes[i].Field, nodes[j].Field, err)
			}
			lambda, usedFallback := Lambda(d.partition, nodes[i], nodes[j], degrees[i], degrees[j], total)
			// The tail is taken in log space: λ runs to thousands on this graph, so the
			// linear tail underflows and every extreme pair reports the same floored
			// number. See PoissonLowerTailLog.
			logP := PoissonLowerTailLog(lambda, w)
			tests++
			if logP < best.logP {
				best = minPair{i: i, j: j, logP: logP, lambda: lambda, w: w, usedFallback: usedFallback}
			}
		}
	}
	return best, tests, nil
}

// evidence assembles the §8.4 evidence for the minimising pair: enough for an
// analyst to recompute (14) or (15), the lower tail, and the Šidák correction of
// (16) by hand (R5). T½ is included because recomputing the decayed weights
// requires it.
func (d *Detector) evidence(nodes []NodeID, degrees []float64, total float64, tests int, best minPair) detector.Evidence {
	stats := map[string]float64{
		"lambda_min_pair": best.lambda,
		"w_min_pair":      best.w,
		"k_i":             degrees[best.i],
		"k_j":             degrees[best.j],
		"m":               total,
		"T":               float64(tests),
		"F_e":             float64(len(nodes)),
		"log_p_min":       best.logP,
		"half_life_us":    float64(d.halfLife),
	}
	labels := map[string]string{
		"value_i": nodes[best.i].Value,
		"value_j": nodes[best.j].Value,
	}

	equations := []int{12, 13, 14, 16}
	var caveats []string
	if best.usedFallback {
		equations = []int{12, 13, 15, 16}
		labels["fallback"] = "configuration-model (15)"
		caveats = append(caveats,
			"no partition available; single-block degeneration (15) used and reported (§8.4)")
	} else {
		r, s := d.partition.Blocks[nodes[best.i]], d.partition.Blocks[nodes[best.j]]
		stats["D_r"] = d.partition.DegreeSums[r]
		stats["D_s"] = d.partition.DegreeSums[s]
		stats["m_rs"] = d.partition.BlockWeights[NewBlockPair(r, s)]
		labels["block_r"] = string(r)
		labels["block_s"] = string(s)
	}
	if d.partition != nil {
		labels["partition_seed"] = strconv.FormatInt(d.partition.Seed, 10)
		labels["partition_checksum"] = d.partition.GraphChecksum
	}
	return detector.NewEvidence(equations, stats, labels, caveats...)
}

// nodePair is one pending co-occurrence update.
type nodePair struct {
	a, b NodeID
}

// allPairs enumerates every (i < j) pair in the fixed node order: the T pairs of
// (16), which are exactly the pairs the Observation folds into the graph.
func allPairs(nodes []NodeID) []nodePair {
	out := make([]nodePair, 0, len(nodes)*(len(nodes)-1)/2)
	for i := range nodes {
		for j := i + 1; j < len(nodes); j++ {
			out = append(out, nodePair{a: nodes[i], b: nodes[j]})
		}
	}
	return out
}

// observation carries the graph update implied by an event — one co-occurrence per
// pair — computed while scoring against the pre-event graph and applied strictly
// afterwards (§5.2).
type observation struct {
	graph      GraphRepository
	source     event.SourceID
	at         event.Timestamp
	eventID    event.ID
	detectorID detector.ID
	pending    []nodePair
	committed  bool
}

// EventID implements detector.Observation.
func (o *observation) EventID() event.ID { return o.eventID }

// DetectorID implements detector.Observation.
func (o *observation) DetectorID() detector.ID { return o.detectorID }

// Commit applies the update. Idempotent per observation: a second commit is a
// no-op, so a replayed delivery cannot double-count an edge weight.
func (o *observation) Commit(ctx context.Context) error {
	if o.committed {
		return nil
	}
	for _, pr := range o.pending {
		if err := o.graph.SaveCoOccurrence(ctx, o.source, pr.a, pr.b, o.at); err != nil {
			return fmt.Errorf("cooccurrence: save co-occurrence (%q, %q): %w", pr.a.Field, pr.b.Field, err)
		}
	}
	o.committed = true
	return nil
}
