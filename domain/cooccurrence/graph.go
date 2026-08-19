// Package cooccurrence implements Detector III, population co-occurrence (§8).
//
// The signal of §8.1 is the pairing, not the parts: two values each individually
// frequent — a familiar authentication type, a familiar destination — that have
// scarcely or never been observed together (Figure 1's dashed edge). Marginal
// detectors are blind to that by construction, which is why the graph exists.
//
// Equation (12) defines the graph: V = {(f, v) : f ∈ F_elig, v observed}, with
// F_elig the fields whose registry kind IsEligible(), edges joining nodes of
// distinct fields only, and w_ij the decayed co-occurrence count. The null of
// equation (13) is Poisson-DCSBM, w_ij ~ Poisson(θ_i θ_j ω_{z(i)z(j)}), whose
// maximum-likelihood rates give equation (14); with no partition it degenerates to
// the single-block configuration model of equation (15). The p-value is a LOWER
// tail: small exactly when co-occurrence ought to have been observed and was not.
// §8.5 emits one verdict per event, Šidák-corrected across the T = F_e(F_e−1)/2
// pairwise tests by equation (16).
//
// The graph is maintained online under the §6.2 lazy rule; the partition is
// computed offline by a scheduled batch (§8.2), never in the scoring path, which
// is what keeps scoring deterministic (R4).
package cooccurrence

import (
	"context"

	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/novelty"
)

// NodeID identifies a graph node: a (field, value) pair, one element of
// V = {(f, v) : f ∈ F_elig, v observed} of equation (12).
type NodeID struct {
	Field event.FieldPath
	Value string
}

// less orders nodes by Field then Value. It fixes the canonical orientation of an
// edge, so that (a, b) and (b, a) name one edge however a caller happened to
// enumerate the pair.
func (n NodeID) less(o NodeID) bool {
	if n.Field != o.Field {
		return n.Field < o.Field
	}
	return n.Value < o.Value
}

// canonicalEdge returns an edge's endpoints in canonical order.
func canonicalEdge(a, b NodeID) (NodeID, NodeID) {
	if b.less(a) {
		return b, a
	}
	return a, b
}

// GraphRepository persists the decayed co-occurrence graph of equation (12).
//
// Scope is the population, not the entity: the graph aggregates every entity's
// events per source (§8.2), because the structure being learned — which values of
// which fields belong together — is a property of the population. Edges join
// nodes of distinct fields only (12); the detector's pair enumeration guarantees
// it, so implementations need not re-check.
//
// All reads are decayed to the requested instant under the §6.2 lazy rule: a row
// stores (weight, last_seen) and the discount 2^(−Δt/T½) is applied on read from
// the row's own timestamp, so no sweep job exists.
type GraphRepository interface {
	// FindEdgeWeight returns w_ij decayed to at; 0 if absent.
	FindEdgeWeight(ctx context.Context, source event.SourceID, a, b NodeID, at event.Timestamp) (float64, error)

	// FindDegree returns k_i decayed to at; 0 if absent.
	FindDegree(ctx context.Context, source event.SourceID, n NodeID, at event.Timestamp) (float64, error)

	// FindTotalWeight returns m, the graph's total decayed edge weight, at at.
	FindTotalWeight(ctx context.Context, source event.SourceID, at event.Timestamp) (float64, error)

	// SaveCoOccurrence folds one observed co-occurrence of a and b at at:
	// w_ab += 1 (after lazy decay), k_a += 1, k_b += 1, m += 1.
	SaveCoOccurrence(ctx context.Context, source event.SourceID, a, b NodeID, at event.Timestamp) error
}

// memoryCell is one stored scalar under the §6.2 lazy rule: a weight alongside its
// own last-observed timestamp, brought up to date only when read.
type memoryCell struct {
	weight   float64
	lastSeen event.Timestamp
}

// read returns the weight decayed to at. A nil cell reads as 0: an absent row is a
// zero count, not an error (§6.2).
func (c *memoryCell) read(at event.Timestamp, halfLife novelty.HalfLife) float64 {
	if c == nil {
		return 0
	}
	return novelty.Decay(c.weight, c.lastSeen, at, halfLife)
}

// fold accumulates one unit into the cell at at. lastSeen only advances, so an
// out-of-order arrival is absorbed rather than rejuvenating the row (§6.2).
func (c *memoryCell) fold(at event.Timestamp, halfLife novelty.HalfLife) {
	c.weight = novelty.Accumulate(c.weight, c.lastSeen, at, halfLife)
	if at > c.lastSeen {
		c.lastSeen = at
	}
}

// nodeKey and edgeKey scope rows per source, matching the per-source scope of §8.2.
type nodeKey struct {
	source event.SourceID
	node   NodeID
}

type edgeKey struct {
	source event.SourceID
	a, b   NodeID // canonical: a ≤ b by (Field, Value)
}

// MemoryGraph implements GraphRepository in process, with the same lazy rule the
// production schema uses: every row stores (weight, last_seen), and decay is
// applied on read via the novelty decay helpers. It exists so the domain suite and
// the in-process replay run anywhere (CI has no database). It is not safe for
// concurrent use, which matches the single-threaded scoring loop.
type MemoryGraph struct {
	halfLife novelty.HalfLife
	edges    map[edgeKey]*memoryCell
	nodes    map[nodeKey]*memoryCell
	totals   map[event.SourceID]*memoryCell
}

// NewMemoryGraph returns an empty graph whose rows decay with halfLife.
func NewMemoryGraph(halfLife novelty.HalfLife) *MemoryGraph {
	return &MemoryGraph{
		halfLife: halfLife,
		edges:    make(map[edgeKey]*memoryCell),
		nodes:    make(map[nodeKey]*memoryCell),
		totals:   make(map[event.SourceID]*memoryCell),
	}
}

// FindEdgeWeight implements GraphRepository.
func (m *MemoryGraph) FindEdgeWeight(_ context.Context, source event.SourceID, a, b NodeID, at event.Timestamp) (float64, error) {
	lo, hi := canonicalEdge(a, b)
	return m.edges[edgeKey{source: source, a: lo, b: hi}].read(at, m.halfLife), nil
}

// FindDegree implements GraphRepository.
func (m *MemoryGraph) FindDegree(_ context.Context, source event.SourceID, n NodeID, at event.Timestamp) (float64, error) {
	return m.nodes[nodeKey{source: source, node: n}].read(at, m.halfLife), nil
}

// FindTotalWeight implements GraphRepository.
func (m *MemoryGraph) FindTotalWeight(_ context.Context, source event.SourceID, at event.Timestamp) (float64, error) {
	return m.totals[source].read(at, m.halfLife), nil
}

// SaveCoOccurrence implements GraphRepository. Decay is linear, so the degree and
// total rows maintained by this same rule stay exactly consistent with the edge
// rows they aggregate, for the reason set out at novelty.Accumulate.
func (m *MemoryGraph) SaveCoOccurrence(_ context.Context, source event.SourceID, a, b NodeID, at event.Timestamp) error {
	lo, hi := canonicalEdge(a, b)
	m.edgeCell(source, lo, hi).fold(at, m.halfLife)
	m.nodeCell(source, lo).fold(at, m.halfLife)
	m.nodeCell(source, hi).fold(at, m.halfLife)
	m.totalCell(source).fold(at, m.halfLife)
	return nil
}

// Edges returns the number of distinct edge rows ever created, for table T5. Lazy
// decay never deletes a row, so this is a high-water count.
func (m *MemoryGraph) Edges() int64 { return int64(len(m.edges)) }

// Nodes returns the number of distinct node rows ever created, for table T5.
func (m *MemoryGraph) Nodes() int64 { return int64(len(m.nodes)) }

// HasNode reports whether any state row exists for n under any source. It exists
// for the §12.5 identifier control: an identifier-kind field must never grow a
// node, and the control asserts absence rather than trusting the guard.
func (m *MemoryGraph) HasNode(n NodeID) bool {
	for key := range m.nodes {
		if key.node == n {
			return true
		}
	}
	return false
}

// edgeCell returns the edge row for (a, b), creating it empty if absent. The
// endpoints must already be in canonical order.
func (m *MemoryGraph) edgeCell(source event.SourceID, a, b NodeID) *memoryCell {
	key := edgeKey{source: source, a: a, b: b}
	c, ok := m.edges[key]
	if !ok {
		c = &memoryCell{}
		m.edges[key] = c
	}
	return c
}

// nodeCell returns the degree row for n, creating it empty if absent.
func (m *MemoryGraph) nodeCell(source event.SourceID, n NodeID) *memoryCell {
	key := nodeKey{source: source, node: n}
	c, ok := m.nodes[key]
	if !ok {
		c = &memoryCell{}
		m.nodes[key] = c
	}
	return c
}

// totalCell returns the source's total-weight row, creating it empty if absent.
func (m *MemoryGraph) totalCell(source event.SourceID) *memoryCell {
	c, ok := m.totals[source]
	if !ok {
		c = &memoryCell{}
		m.totals[source] = c
	}
	return c
}
