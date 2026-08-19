package marginal_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/JohnPierman/ethogram/domain/detector"
	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/marginal"
)

// The cardinality ceiling is both a statement about resolution and the thing that makes
// Detector IV affordable at population scope, so it is tested as behaviour rather than
// left to the constant's documentation.

func TestAboveTheCardinalityCeilingTheDetectorAbstains(t *testing.T) {
	reg := warmRegistry(false)
	d, repo := newWiredDetector(reg)
	ctx := context.Background()

	// Fill the destination field past the ceiling. Each distinct host is committed
	// once, so the marginal is wide and flat: exactly the shape in which membership of
	// the tail stops distinguishing anything.
	at := event.Timestamp(1) * event.Second
	for i := range marginal.MaxCardinality + 1 {
		if err := repo.SaveCategorical(ctx, src, fDstComputer,
			fmt.Sprintf("HOST-%05d", i), at); err != nil {
			t.Fatalf("SaveCategorical: %v", err)
		}
	}

	k, err := repo.Cardinality(ctx, src, fDstComputer)
	if err != nil {
		t.Fatalf("Cardinality: %v", err)
	}
	if k != marginal.MaxCardinality+1 {
		t.Fatalf("cardinality = %d, want %d", k, marginal.MaxCardinality+1)
	}

	e := mkEvent(entityU66, at+event.Second, map[event.FieldPath]event.Value{
		fAuthType:    event.NewValue("Kerberos"),
		fDstComputer: event.NewValue("HOST-00007"),
		fSuccess:     event.NewValue("Success"),
	}, 1)

	verdicts, _, err := d.Score(ctx, e)
	if err != nil {
		t.Fatalf("Score: %v", err)
	}

	var found bool
	for _, v := range verdicts {
		if len(v.Target().Fields) != 1 || v.Target().Fields[0] != fDstComputer {
			continue
		}
		found = true
		if v.Status() == detector.StatusEvaluated {
			t.Error("the detector answered for a field above the ceiling")
		}
		// The reason must name the numbers, so a reader of the verdict card can see
		// why the abstention happened without consulting the source.
		reason := v.Reason()
		for _, want := range []string{"distinct values", "ceiling"} {
			if !strings.Contains(reason, want) {
				t.Errorf("abstention reason omits %q: %s", want, reason)
			}
		}
		if got := v.Evidence().Stats["K"]; got != float64(marginal.MaxCardinality+1) {
			t.Errorf("evidence K = %v, want %d", got, marginal.MaxCardinality+1)
		}
		if got := v.Evidence().Stats["max_cardinality"]; got != float64(marginal.MaxCardinality) {
			t.Errorf("evidence max_cardinality = %v, want %d", got, marginal.MaxCardinality)
		}
	}
	if !found {
		t.Fatal("no verdict was produced for the wide field at all")
	}
}

func TestBelowTheCeilingTheDetectorStillAnswers(t *testing.T) {
	// The ceiling must not silence the narrow fields it was never about; the
	// low-cardinality protocol field is where a population marginal is informative.
	reg := warmRegistry(false)
	d, repo := newWiredDetector(reg)
	ctx := context.Background()

	at := event.Timestamp(1) * event.Second
	for i := range 400 {
		v := "Kerberos"
		if i%40 == 0 {
			v = "NTLM"
		}
		if err := repo.SaveCategorical(ctx, src, fAuthType, v, at); err != nil {
			t.Fatalf("SaveCategorical: %v", err)
		}
	}

	e := mkEvent(entityU66, at+event.Second, map[event.FieldPath]event.Value{
		fAuthType:    event.NewValue("NTLM"),
		fDstComputer: event.NewValue("C700"),
		fSuccess:     event.NewValue("Success"),
	}, 1)

	verdicts, _, err := d.Score(ctx, e)
	if err != nil {
		t.Fatalf("Score: %v", err)
	}
	for _, v := range verdicts {
		if len(v.Target().Fields) != 1 || v.Target().Fields[0] != fAuthType {
			continue
		}
		if v.Status() != detector.StatusEvaluated {
			t.Fatalf("the narrow field abstained: %s", v.Reason())
		}
		// NTLM holds 10 of 400 observations, so it is the minority value and its tail
		// mass must be well below one.
		p, ok := v.PValue()
		if !ok {
			t.Fatal("an evaluated verdict carried no p-value")
		}
		if p >= 1 || p <= 0 {
			t.Errorf("p = %v, want a proper tail mass in (0,1)", p)
		}
		return
	}
	t.Fatal("no verdict for the narrow field")
}

func TestCardinalityIsZeroForAnUnseenField(t *testing.T) {
	// A cold start is not an error, and must not be reported as a wide field.
	_, repo := newWiredDetector(warmRegistry(false))
	k, err := repo.Cardinality(context.Background(), src, fDstComputer)
	if err != nil {
		t.Fatalf("Cardinality: %v", err)
	}
	if k != 0 {
		t.Errorf("cardinality of an unseen field = %d, want 0", k)
	}
}
