package cooccurrence_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/JohnPierman/ethogram/domain/cooccurrence"
	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/registry"
)

const fByteCount = event.FieldPath("flows.byte_count")

// warmRegistryWithBytes settles fByteCount as continuous beside two categorical fields.
func warmRegistryWithBytes() *registry.Registry {
	reg := registry.New(registry.DefaultPolicy())
	for i := range warmEvents {
		reg.ObserveEvent(mkEvent("warm", event.Timestamp(i+1)*event.Second, map[event.FieldPath]event.Value{
			fAuthType:    event.NewValue([]string{"Negotiate", "Kerberos", "NTLM"}[i%3]),
			fDstComputer: event.NewValue(fmt.Sprintf("C%d", 700+i%11)),
			fByteCount:   event.NewValue(fmt.Sprintf("%d", 40_000_000+i*7919)),
		}, int64(i)))
	}
	return reg
}

// TestContinuousEntersTheGraphAsABand is the §8.2 claim, restated as a test.
//
// The specification admits numeric fields to the graph through bins. Before this
// existed they were withheld altogether, because the node is the value and a node per
// measurement is a node per event: every one a singleton, and the block structure the
// detector depends on dissolves. The band is the bin.
func TestContinuousEntersTheGraphAsABand(t *testing.T) {
	ctx := context.Background()
	reg := warmRegistryWithBytes()
	if kind, _ := reg.KindOf(src, fByteCount); kind != registry.KindContinuous {
		t.Fatalf("fixture: fByteCount settled as %s, want continuous", kind)
	}
	d, graph := newWiredDetector(reg)

	e := mkEvent(entityU66, event.Hour, map[event.FieldPath]event.Value{
		fAuthType:  event.NewValue("Kerberos"),
		fByteCount: event.NewValue("1500"),
	}, 1)
	_, obs, err := d.Score(ctx, e)
	if err != nil {
		t.Fatal(err)
	}
	if err := obs.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	if !graph.HasNode(cooccurrence.NodeID{Field: fByteCount, Value: registry.Band(1500)}) {
		t.Errorf("no band node for the measurement; the field did not enter the graph")
	}
	if graph.HasNode(cooccurrence.NodeID{Field: fByteCount, Value: "1500"}) {
		t.Error("the raw measurement became a node: unbinned admission is the failure §8.2 prevents")
	}
}

// TestContinuousDoesNotDissolveTheGraph: the node count must grow with the number of
// bands, not with the number of measurements. This is the guard against restoring the
// behaviour the field was withheld to avoid.
func TestContinuousDoesNotDissolveTheGraph(t *testing.T) {
	ctx := context.Background()
	reg := warmRegistryWithBytes()
	d, graph := newWiredDetector(reg)

	const events = 500
	for i := range events {
		e := mkEvent(entityU66, event.Timestamp(i+1)*event.Minute, map[event.FieldPath]event.Value{
			fAuthType:  event.NewValue("Kerberos"),
			fByteCount: event.NewValue(fmt.Sprintf("%d", 1+i*7919)),
		}, int64(i))
		_, obs, err := d.Score(ctx, e)
		if err != nil {
			t.Fatal(err)
		}
		if err := obs.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}

	// One node for Kerberos plus one per occupied band.
	if got := graph.Nodes(); got > 32 {
		t.Fatalf("%d events over %d distinct measurements produced %d nodes; "+
			"the graph is a set of singletons again", events, events, got)
	}
}

// TestUnmeasurableContinuousContributesNoNode: a sentinel is not a band, and admitting
// it as one would make "the value was missing" co-occur with everything.
func TestUnmeasurableContinuousContributesNoNode(t *testing.T) {
	ctx := context.Background()
	reg := warmRegistryWithBytes()
	d, graph := newWiredDetector(reg)

	e := mkEvent(entityU66, event.Hour, map[event.FieldPath]event.Value{
		fAuthType:    event.NewValue("Kerberos"),
		fDstComputer: event.NewValue("C625"),
		fByteCount:   event.NewValue("unknown"),
	}, 1)
	_, obs, err := d.Score(ctx, e)
	if err != nil {
		t.Fatal(err)
	}
	if err := obs.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	if graph.HasNode(cooccurrence.NodeID{Field: fByteCount, Value: "unknown"}) {
		t.Error("a sentinel became a graph node")
	}
	if got := graph.Nodes(); got != 2 {
		t.Errorf("nodes = %d, want 2 — the two categorical values only", got)
	}
}
