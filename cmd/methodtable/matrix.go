package main

// The per-mechanism table, emitted as data rather than only as Markdown.
//
// The robust-allocation analysis reads exactly the rectangle this command already builds:
// one row per method, one column per attack mechanism, at a stated budget. Re-deriving it
// there would mean a second reader of the same run file, and two readers of one measurement
// kept in agreement by memory is how a table and the analysis of that table come to disagree.
// So the matrix is written once, here, and `cmd/robust` consumes it.
//
// The counts are the counts the Markdown table renders. Nothing is normalised on the way
// out: whether a mechanism's count should be divided by what was planted depends on the
// question being asked of it, and that decision belongs to the consumer.

import (
	"encoding/json"
	"fmt"
	"os"
)

// matrixRow is one method's detections by mechanism, at one budget.
type matrixRow struct {
	Name  string `json:"name"`
	Group string `json:"group"`
	// Caught is keyed by mechanism name, and holds only mechanisms the method reached.
	Caught map[string]int `json:"caught"`
	Alerts int            `json:"alerts"`
	// Permitted is what the budget allows over the whole window. A method spending more
	// than this bought its detections rather than ranked better, and the consumer needs to
	// be able to see that rather than compare the counts as though they were matched.
	Permitted int `json:"permitted"`
	// Measured distinguishes "this method detected nothing" from "no run covers this
	// method at this budget". They must not both render as zero.
	Measured bool   `json:"measured"`
	Note     string `json:"note,omitempty"`
}

// matrixBudget is the whole rectangle at one budget.
type matrixBudget struct {
	Budget     int            `json:"budget"`
	Permitted  int            `json:"permitted"`
	Mechanisms []string       `json:"mechanisms"`
	Planted    map[string]int `json:"planted"`
	Rows       []matrixRow    `json:"rows"`
}

// matrixDoc is the emitted file.
type matrixDoc struct {
	SchemaVersion int            `json:"schema_version"`
	Kind          string         `json:"kind"`
	Run           map[string]any `json:"run"`
	Note          string         `json:"note"`
	Budgets       []matrixBudget `json:"budgets"`
}

// matrix returns the table as data. It is taken from the same rows the Markdown renders, so
// the two cannot disagree about a count.
func (t *table) matrix() matrixBudget {
	out := matrixBudget{
		Budget:     t.budget,
		Permitted:  t.permitted,
		Mechanisms: append([]string(nil), t.types...),
		Planted:    map[string]int{},
		Rows:       make([]matrixRow, 0, len(t.rows)),
	}
	for _, kind := range t.types {
		if n, ok := t.planted[kind]; ok {
			out.Planted[kind] = n
		}
	}
	for _, r := range t.rows {
		caught := map[string]int{}
		for kind, n := range r.caught {
			if n != 0 {
				caught[kind] = n
			}
		}
		out.Rows = append(out.Rows, matrixRow{
			Name:      r.name,
			Group:     r.group,
			Caught:    caught,
			Alerts:    r.alerts,
			Permitted: r.permitted,
			Measured:  r.measured,
			Note:      r.note,
		})
	}
	return out
}

// writeMatrix records the matrices for every budget rendered, with the run that produced
// them, so a consumer can state its provenance without being told it separately.
func writeMatrix(path, runID string, corpus string, budgets []matrixBudget) error {
	doc := matrixDoc{
		SchemaVersion: 1,
		Kind:          "method-matrix",
		Run:           map[string]any{"run_id": runID, "corpus": corpus},
		Note: "one row per method, one column per attack mechanism, at each budget the run " +
			"measured. Counts, not rates: dividing by the planted total is the consumer's " +
			"decision because the mechanisms have very different planted totals.",
		Budgets: budgets,
	}
	blob, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding the matrix: %w", err)
	}
	return os.WriteFile(path, append(blob, '\n'), 0o644)
}
