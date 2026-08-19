// Package partition loads the offline Leiden result produced by sidecar/partition.py
// into the domain's Partition type. §8.2 requires partitioning be a scheduled batch,
// never in the scoring path; this loader is the boundary where the batch's output
// enters Go, carrying its provenance (seed, input checksum, library versions) with it.
package partition

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/JohnPierman/ethogram/domain/cooccurrence"
	"github.com/JohnPierman/ethogram/domain/event"
)

// nodeSeparator matches sidecar/partition.py: the unit separator, which cannot occur
// in a field path or value.
const nodeSeparator = "\x1f"

// sidecarPartition mirrors the JSON written by sidecar/partition.py.
type sidecarPartition struct {
	Algorithm     string             `json:"algorithm"`
	Seed          int64              `json:"seed"`
	Resolution    float64            `json:"resolution"`
	GraphChecksum string             `json:"graph_checksum"`
	Nodes         int64              `json:"nodes"`
	Edges         int64              `json:"edges"`
	Versions      map[string]string  `json:"versions"`
	Blocks        map[string]string  `json:"blocks"`
	DegreeSums    map[string]float64 `json:"degree_sums"`
	BlockWeights  map[string]float64 `json:"block_weights"`
}

// Load reads a partition JSON and maps it into the scoring path's representation.
func Load(path string) (*cooccurrence.Partition, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // the batch output the flag names
	if err != nil {
		return nil, err
	}
	var sp sidecarPartition
	if err := json.Unmarshal(raw, &sp); err != nil {
		return nil, fmt.Errorf("partition %s: %w", path, err)
	}
	if len(sp.Blocks) == 0 {
		return nil, fmt.Errorf("partition %s: no block assignments", path)
	}

	blocks := make(map[cooccurrence.NodeID]cooccurrence.BlockID, len(sp.Blocks))
	for key, block := range sp.Blocks {
		field, value, ok := strings.Cut(key, nodeSeparator)
		if !ok {
			return nil, fmt.Errorf("partition %s: node key %q has no separator", path, key)
		}
		blocks[cooccurrence.NodeID{Field: event.FieldPath(field), Value: value}] =
			cooccurrence.BlockID(block)
	}

	degreeSums := make(map[cooccurrence.BlockID]float64, len(sp.DegreeSums))
	for block, d := range sp.DegreeSums {
		degreeSums[cooccurrence.BlockID(block)] = d
	}

	blockWeights := make(map[cooccurrence.BlockPair]float64, len(sp.BlockWeights))
	for key, w := range sp.BlockWeights {
		r, s, ok := strings.Cut(key, nodeSeparator)
		if !ok {
			return nil, fmt.Errorf("partition %s: block pair %q has no separator", path, key)
		}
		pair := cooccurrence.NewBlockPair(cooccurrence.BlockID(r), cooccurrence.BlockID(s))
		blockWeights[pair] = w
	}

	return &cooccurrence.Partition{
		Seed:          sp.Seed,
		GraphChecksum: sp.GraphChecksum,
		Resolution:    sp.Resolution,
		Blocks:        blocks,
		DegreeSums:    degreeSums,
		BlockWeights:  blockWeights,
		// The snapshot's own 2m, without which (14) would price live degrees against
		// frozen block statistics and inherit the ratio of the two graphs' scales.
		TotalDegree: cooccurrence.SumDegreeSums(degreeSums),
	}, nil
}
