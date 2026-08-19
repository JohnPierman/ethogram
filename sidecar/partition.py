"""Offline Leiden partition of the co-occurrence graph (whitepaper §8.2).

Partitioning is a scheduled batch computation, never in the scoring path; that
separation is what preserves R4 and with it R6. This script is therefore allowed to be
Python: it runs between scoring runs, under a fixed seed, and persists its output with
the seed and the checksum of the input graph, so a scoring run can state exactly which
partition it conditioned on.

Input:  a graph TSV exported by the replay engine, one edge per line:
        field_a <TAB> value_a <TAB> field_b <TAB> value_b <TAB> weight
Output: a partition JSON consumed by the Go scoring path:
        blocks (node -> block), degree_sums (D_r), block_weights (m_rs under the
        Karrer-Newman convention: weight internal to a block counts from both
        endpoints), plus provenance (seed, input checksum, library versions).

Blocks are field-local, per §8.2 ("nodes are partitioned within each field"): Leiden
runs on the whole k-partite graph, and a node's block is the pair (field, community),
so values of different fields never share a block while the community structure that
spans fields is retained.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import sys

import igraph
import leidenalg

NODE_SEP = "\x1f"  # unit separator; cannot occur in a field path or value


def read_edges(path: str) -> tuple[list[tuple[str, str]], list[float], str]:
    """Read the edge TSV; return (edges by node key, weights, sha256 of the bytes)."""
    edges: list[tuple[str, str]] = []
    weights: list[float] = []
    digest = hashlib.sha256()
    with open(path, "rb") as fh:
        for raw in fh:
            digest.update(raw)
            line = raw.decode("utf-8").rstrip("\n")
            if not line:
                continue
            parts = line.split("\t")
            if len(parts) != 5:
                raise SystemExit(f"malformed edge line: {line!r}")
            fa, va, fb, vb, w = parts
            edges.append((fa + NODE_SEP + va, fb + NODE_SEP + vb))
            weights.append(float(w))
    return edges, weights, digest.hexdigest()


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--graph", required=True, help="edge TSV exported by the replay engine")
    ap.add_argument("--out", required=True, help="partition JSON path")
    ap.add_argument("--seed", type=int, default=42, help="Leiden RNG seed (recorded)")
    ap.add_argument("--resolution", type=float, default=1.0,
                    help="RBConfiguration resolution parameter (recorded; §8.5 notes "
                         "the resolution limit means no scale is correct for all structure)")
    args = ap.parse_args()

    edges, weights, checksum = read_edges(args.graph)
    if not edges:
        raise SystemExit("empty graph; refusing to write a partition of nothing")

    g = igraph.Graph.TupleList(edges, weights=False)
    g.es["weight"] = weights

    part = leidenalg.find_partition(
        g,
        leidenalg.RBConfigurationVertexPartition,
        weights="weight",
        resolution_parameter=args.resolution,
        seed=args.seed,
        n_iterations=-1,  # run to convergence: deterministic under the fixed seed
    )

    # Field-local blocks: (field, community).
    blocks: dict[str, str] = {}
    for vertex, community in zip(g.vs, part.membership):
        field = vertex["name"].split(NODE_SEP, 1)[0]
        blocks[vertex["name"]] = f"{field}:{community}"

    # D_r and m_rs from the same weighted graph, m_rs under the Karrer-Newman
    # convention: an edge internal to block r contributes twice to m_rr (§8.3; this is
    # the convention that makes the single-block collapse land on k_i k_j / 2m, review
    # item B1).
    degree_sums: dict[str, float] = {}
    strengths = g.strength(weights="weight")
    for vertex, strength in zip(g.vs, strengths):
        block = blocks[vertex["name"]]
        degree_sums[block] = degree_sums.get(block, 0.0) + strength

    block_weights: dict[str, float] = {}
    for edge in g.es:
        ra = blocks[g.vs[edge.source]["name"]]
        rb = blocks[g.vs[edge.target]["name"]]
        w = float(edge["weight"])
        if ra == rb:
            key = ra + NODE_SEP + rb
            block_weights[key] = block_weights.get(key, 0.0) + 2.0 * w
        else:
            key = (ra + NODE_SEP + rb) if ra <= rb else (rb + NODE_SEP + ra)
            block_weights[key] = block_weights.get(key, 0.0) + w

    out = {
        "algorithm": "leiden-rbconfiguration",
        "seed": args.seed,
        "resolution": args.resolution,
        "graph_checksum": checksum,
        "nodes": len(g.vs),
        "edges": len(g.es),
        "communities": len(set(part.membership)),
        "blocks_field_local": len(set(blocks.values())),
        "versions": {
            "python": sys.version.split()[0],
            "leidenalg": leidenalg.version,
            "igraph": igraph.__version__,
        },
        "blocks": blocks,
        "degree_sums": degree_sums,
        "block_weights": block_weights,
    }
    with open(args.out, "w", encoding="utf-8") as fh:
        json.dump(out, fh, indent=1)
    print(f"partition: {len(g.vs)} nodes, {len(g.es)} edges -> "
          f"{out['blocks_field_local']} field-local blocks "
          f"({out['communities']} communities), seed {args.seed}, "
          f"checksum {checksum[:12]}", file=sys.stderr)


if __name__ == "__main__":
    main()
