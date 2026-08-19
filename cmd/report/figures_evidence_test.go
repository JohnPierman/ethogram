package main

// Evidence-figure tests: F1 and F8 from a control fixture, F2 from a replay
// fixture. Like every test here they render exclusively from fixtures written into
// t.TempDir(), never from the repository's results/, and never into docs/.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func controlFixture() map[string]any {
	hours := []float64{0, 3, 6, 9, 12, 15, 18, 21}
	density := []float64{0.30, 0.20, 0.05, 0.01, 0.02, 0.01, 0.05, 0.25}
	pCircular := []float64{1, 0.5, 0.1, 0.001, 0.01, 0.001, 0.1, 0.6}
	pCells := []float64{0.8, 0.4, 0.2, 0.05, 0.06, 0.05, 0.2, 0.5}
	curve := make([]any, 0, len(hours))
	for i, h := range hours {
		curve = append(curve, map[string]any{
			"hour": h, "density": density[i],
			"p_circular": pCircular[i], "p_cells": pCells[i],
		})
	}
	return map[string]any{
		"schema_version": "1",
		"kind":           "control",
		"hypothesis":     []string{"E9"},
		"run": map[string]any{
			"run_id": "control-fixture-001", "started_at": "2026-01-04T00:00:00Z",
			"finished_at": "2026-01-04T00:00:01Z",
		},
		"corpus":              map[string]any{"coverage": map[string]any{"kind": "control"}},
		"provenance_complete": true,
		"parameters": map[string]any{
			"bandwidth_hours": 2.25, "kappa": 3.5, "H": 7, "grid": 64,
		},
		"results": map[string]any{
			"curve": curve,
			"probes": map[string]any{
				"circular": map[string]any{
					"p_23_30": 0.4321, "p_00_30": 0.55, "p_12_00": 0.0123,
				},
				"cells_168": map[string]any{
					"p_23_30": 0.9, "p_00_30": 0.8, "p_12_00": 0.7,
				},
			},
			"acceptance": map[string]any{
				"circular_passes": true, "cells_show_defect": true,
				"criteria": "23:30 and 00:30 unremarkable (P >= 0.20), 12:00 unusual (P <= 0.02)",
			},
		},
	}
}

// cooccurrenceReplayFixture extends replayFixture with the co-occurrence p-value
// histogram F2 needs; replayFixture already carries runtime graph counts.
func cooccurrenceReplayFixture() map[string]any {
	doc := replayFixture()
	counts := make([]int, 240)
	counts[3], counts[120], counts[239] = 1, 9, 90
	hists := doc["results"].(map[string]any)["p_histograms"].(map[string]any)
	hists["cooccurrence"] = map[string]any{"counts": counts, "under_1e_12": 7}
	return doc
}

// 1. A control fixture with a curve, probes, and parameters produces F1 and F8:
// manifest entries carry the fixture's run_id, both figures render inline in the
// HTML, F1's subtitle carries the fixture's bandwidth, and no coordinate is NaN
// or infinite.
func TestControlFixtureEvidenceFigures(t *testing.T) {
	resultsDir, figuresDir, outPath := renderDirs(t)
	writeFixture(t, resultsDir, "control.json", controlFixture())
	html := renderAll(t, resultsDir, figuresDir, outPath)

	manifest := readManifest(t, figuresDir)
	for _, name := range []string{"f1-circular-density.svg", "f8-wraparound-control.svg"} {
		raw, err := os.ReadFile(filepath.Join(figuresDir, name))
		if err != nil {
			t.Fatalf("figure %s not written: %v", name, err)
		}
		if manifest[name] != "control-fixture-001" {
			t.Errorf("manifest[%s] = %q, want control-fixture-001", name, manifest[name])
		}
		for _, bad := range []string{"NaN", "Inf"} {
			if strings.Contains(string(raw), bad) {
				t.Errorf("%s contains %s coordinates", name, bad)
			}
		}
	}
	for _, want := range []string{
		"<svg",
		"Fitted circular activity density, with the scored event marked",
		"The §12.5 wraparound control",
		"bandwidth 2.25 h",
		"23:30 and 00:30 unremarkable (P &gt;= 0.20), 12:00 unusual (P &lt;= 0.02)",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("report missing %q", want)
		}
	}
}

// 2. F1's level-set label prints the fixture's p_23_30 to four decimal places.
func TestF1LevelSetLabel(t *testing.T) {
	resultsDir, figuresDir, outPath := renderDirs(t)
	writeFixture(t, resultsDir, "control.json", controlFixture())
	renderAll(t, resultsDir, figuresDir, outPath)

	raw, err := os.ReadFile(filepath.Join(figuresDir, "f1-circular-density.svg"))
	if err != nil {
		t.Fatalf("F1 not written: %v", err)
	}
	if !strings.Contains(string(raw), "level set for the 23:30 probe: P = 0.4321") {
		t.Error("F1 level-set label missing the probe's p-value at 4 dp")
	}
}

// 3a. A replay carrying the co-occurrence histogram and the measured graph counts
// produces the F2 summary with those exact numbers and the partition string.
func TestF2CooccurrenceSummary(t *testing.T) {
	resultsDir, figuresDir, outPath := renderDirs(t)
	writeFixture(t, resultsDir, "replay.json", cooccurrenceReplayFixture())
	renderAll(t, resultsDir, figuresDir, outPath)

	raw, err := os.ReadFile(filepath.Join(figuresDir, "f2-cooccurrence-summary.svg"))
	if err != nil {
		t.Fatalf("F2 not written: %v", err)
	}
	for _, want := range []string{">210</text>", ">978</text>", "partition: leiden"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("F2 missing %q", want)
		}
	}
	if got := readManifest(t, figuresDir)["f2-cooccurrence-summary.svg"]; got != "replay-fixture-001" {
		t.Errorf("manifest run for F2 = %q, want replay-fixture-001", got)
	}
}

// 3b. Without runtime.graph_nodes/graph_edges there is no measured graph size, so
// no F2 exists even though the histogram does.
func TestF2AbsentWithoutGraphCounts(t *testing.T) {
	resultsDir, figuresDir, outPath := renderDirs(t)
	doc := cooccurrenceReplayFixture()
	rt := doc["runtime"].(map[string]any)
	delete(rt, "graph_nodes")
	delete(rt, "graph_edges")
	writeFixture(t, resultsDir, "replay.json", doc)
	renderAll(t, resultsDir, figuresDir, outPath)

	if _, err := os.Stat(filepath.Join(figuresDir, "f2-cooccurrence-summary.svg")); err == nil {
		t.Error("F2 must not exist without runtime.graph_nodes/graph_edges")
	}
}

// 4. Rendering twice from the same fixtures yields byte-identical F1, F2, and F8.
func TestEvidenceFiguresDeterministic(t *testing.T) {
	resultsDir, _, _ := renderDirs(t)
	writeFixture(t, resultsDir, "control.json", controlFixture())
	writeFixture(t, resultsDir, "replay.json", cooccurrenceReplayFixture())

	tmp := t.TempDir()
	figsA, outA := filepath.Join(tmp, "figs-a"), filepath.Join(tmp, "a.html")
	figsB, outB := filepath.Join(tmp, "figs-b"), filepath.Join(tmp, "b.html")
	renderAll(t, resultsDir, figsA, outA)
	renderAll(t, resultsDir, figsB, outB)

	for _, name := range []string{"f1-circular-density.svg",
		"f2-cooccurrence-summary.svg", "f8-wraparound-control.svg"} {
		a, err := os.ReadFile(filepath.Join(figsA, name))
		if err != nil {
			t.Fatalf("first render missing %s: %v", name, err)
		}
		b, err := os.ReadFile(filepath.Join(figsB, name))
		if err != nil {
			t.Fatalf("second render missing %s: %v", name, err)
		}
		if !bytes.Equal(a, b) {
			t.Errorf("%s differs between renders", name)
		}
	}
}

// 5. With no backing data none of F1, F2, or F8 is written and all three stay on
// the report's pending list.
func TestEvidenceFiguresPendingWithoutData(t *testing.T) {
	resultsDir, figuresDir, outPath := renderDirs(t)
	html := renderAll(t, resultsDir, figuresDir, outPath)

	for _, prefix := range []string{"f1-", "f2-", "f8-"} {
		matches, err := filepath.Glob(filepath.Join(figuresDir, prefix+"*.svg"))
		if err != nil {
			t.Fatal(err)
		}
		if len(matches) != 0 {
			t.Errorf("no %s figure may exist without data, got %v", prefix, matches)
		}
	}
	for _, want := range []string{"F1 circular-density evidence",
		"F2 co-occurrence graph", "F8 wraparound render"} {
		if !strings.Contains(html, want) {
			t.Errorf("pending list missing %q", want)
		}
	}
}
