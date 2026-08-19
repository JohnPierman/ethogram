package cooccurrence

import (
	"bufio"
	"fmt"
	"io"
	"sort"

	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/novelty"
)

// ExportEdges writes the graph's edges for one source as TSV, decayed to at, in a
// deterministic total order, for the offline Leiden batch of §8.2:
//
//	field_a <TAB> value_a <TAB> field_b <TAB> value_b <TAB> weight
//
// The export happens at the burn-in boundary so the partition is computed from
// burn-in state only and never conditions on the scoring window it will later be used
// to score. Weights below epsilon are omitted: a row decayed to nothing carries no
// information the partition could use, and the cut keeps the exported graph finite in
// the §13.3 sense (the omission is deterministic, so R4 holds).
func (m *MemoryGraph) ExportEdges(w io.Writer, source event.SourceID, at event.Timestamp, epsilon float64) (edges int64, err error) {
	keys := make([]edgeKey, 0, len(m.edges))
	for k := range m.edges {
		if k.source == source {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.a.Field != b.a.Field {
			return a.a.Field < b.a.Field
		}
		if a.a.Value != b.a.Value {
			return a.a.Value < b.a.Value
		}
		if a.b.Field != b.b.Field {
			return a.b.Field < b.b.Field
		}
		return a.b.Value < b.b.Value
	})

	bw := bufio.NewWriterSize(w, 1<<20)
	for _, k := range keys {
		weight := novelty.Decay(m.edges[k].weight, m.edges[k].lastSeen, at, m.halfLife)
		if weight <= epsilon {
			continue
		}
		if _, err := fmt.Fprintf(bw, "%s\t%s\t%s\t%s\t%.17g\n",
			k.a.Field, k.a.Value, k.b.Field, k.b.Value, weight); err != nil {
			return edges, err
		}
		edges++
	}
	return edges, bw.Flush()
}
