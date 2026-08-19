package marginal_test

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/JohnPierman/ethogram/domain/detector"
	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/marginal"
	"github.com/JohnPierman/ethogram/domain/registry"
)

// fStatus is a discrete field: four numeric codes, each recurring constantly.
const fStatus = event.FieldPath("http.status_code")

var statusCodes = []string{"200", "301", "404", "500"}

// warmRegistryWithStatus settles fStatus as discrete beside a categorical field.
func warmRegistryWithStatus() *registry.Registry {
	reg := registry.New(registry.DefaultPolicy())
	for i := range warmEvents {
		reg.ObserveEvent(mkEvent("warm", event.Timestamp(i+1)*event.Second, map[event.FieldPath]event.Value{
			fAuthType: event.NewValue([]string{"Negotiate", "Kerberos", "NTLM"}[i%3]),
			fStatus:   event.NewValue(statusCodes[i%len(statusCodes)]),
		}, int64(i)))
	}
	return reg
}

// TestDiscreteFieldIsCountedNotSketched. A numeric field used to mean one thing to this
// detector — a quantile sketch — whatever its cardinality. For four recurring codes that
// discards the exact counts in favour of an interpolated tail over four centroids, and
// asks a two-sided question ("is this value extreme?") of a value set that has no order
// worth speaking of: 404 is not "further out" than 301.
//
// Equations (4) and (5) over the codes themselves answer the question that field can
// actually support — is this code rare in the population — and answer it exactly.
func TestDiscreteFieldIsCountedNotSketched(t *testing.T) {
	ctx := context.Background()
	reg := warmRegistryWithStatus()
	if kind, _ := reg.KindOf(src, fStatus); kind != registry.KindDiscrete {
		t.Fatalf("fixture: fStatus settled as %s, want discrete", kind)
	}
	d, repo := newWiredDetector(reg)

	for i := range 300 {
		e := mkEvent(event.EntityID(fmt.Sprintf("P%03d@DOM1", i%100)),
			event.Timestamp(i+1)*event.Second,
			map[event.FieldPath]event.Value{fStatus: event.NewValue(statusCodes[i%len(statusCodes)])},
			int64(i))
		feed(t, ctx, d, e)
	}

	rows, err := repo.FindCategorical(ctx, src, fStatus, 400*event.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != len(statusCodes) {
		t.Errorf("categorical marginal holds %d values, want %d — the codes themselves",
			len(rows), len(statusCodes))
	}

	if _, ok, findErr := repo.FindNumeric(ctx, src, fStatus, 400*event.Second); findErr != nil {
		t.Fatal(findErr)
	} else if ok {
		t.Error("a discrete field accumulated a quantile sketch; its counts are exact and " +
			"a sketch over them only loses resolution")
	}

	e := mkEvent(entityU66, 400*event.Second,
		map[event.FieldPath]event.Value{fStatus: event.NewValue("200")}, 400)
	verdicts, _, err := d.Score(ctx, e)
	if err != nil {
		t.Fatal(err)
	}
	v, found := verdictFor(verdicts, fStatus)
	if !found {
		t.Fatal("a discrete field produced no verdict")
	}
	if eqs := v.Evidence().Equations; !slices.Contains(eqs, 4) || !slices.Contains(eqs, 5) {
		t.Errorf("evidence cites equations %v, want (4) and (5) — the counted estimator", eqs)
	}
}

// TestDiscreteRarityIsMeasuredExactly: the payoff of counting rather than sketching. A
// code seen on one event in three hundred must score far below a code seen on a third of
// them, and both against the exact counts.
func TestDiscreteRarityIsMeasuredExactly(t *testing.T) {
	ctx := context.Background()
	reg := warmRegistryWithStatus()
	d, _ := newWiredDetector(reg)

	// 299 events of 200, and a single 500.
	for i := range 299 {
		e := mkEvent(event.EntityID(fmt.Sprintf("P%03d@DOM1", i%100)),
			event.Timestamp(i+1)*event.Second,
			map[event.FieldPath]event.Value{fStatus: event.NewValue("200")}, int64(i))
		feed(t, ctx, d, e)
	}
	rare := mkEvent("P999@DOM1", 300*event.Second,
		map[event.FieldPath]event.Value{fStatus: event.NewValue("500")}, 300)
	feed(t, ctx, d, rare)

	pCommon := mustPValue(t, scoreStatus(ctx, t, d, "200"), fStatus)
	pRare := mustPValue(t, scoreStatus(ctx, t, d, "500"), fStatus)

	if pRare >= pCommon {
		t.Errorf("the rare code scored P = %v against the common code's %v; "+
			"counting must separate them", pRare, pCommon)
	}
}

// scoreStatus scores one event carrying only fStatus, without committing it.
func scoreStatus(ctx context.Context, t *testing.T, d *marginal.Detector, text string) detector.Verdicts {
	t.Helper()
	e := mkEvent(entityU66, 400*event.Second,
		map[event.FieldPath]event.Value{fStatus: event.NewValue(text)}, 400)
	verdicts, _, err := d.Score(ctx, e)
	if err != nil {
		t.Fatal(err)
	}
	return verdicts
}
