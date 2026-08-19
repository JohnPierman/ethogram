package objective_test

import (
	"slices"
	"testing"

	"github.com/JohnPierman/ethogram/domain/objective"
)

func TestParseBudgets(t *testing.T) {
	for _, tc := range []struct {
		spec string
		want []int
	}{
		{"25", []int{25}},
		{"10,25,50,100", []int{10, 25, 50, 100}},
		{" 10 , 25 ", []int{10, 25}},
		// Ascending and deduplicated on construction, whatever order it was written in.
		{"100,10,50,25", []int{10, 25, 50, 100}},
		{"25,25,10", []int{10, 25}},
	} {
		got, err := objective.ParseBudgets(tc.spec)
		if err != nil {
			t.Errorf("ParseBudgets(%q): %v", tc.spec, err)
			continue
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("ParseBudgets(%q) = %v, want %v", tc.spec, got, tc.want)
		}
	}
}

func TestParseBudgetsRejectsNonsense(t *testing.T) {
	for _, spec := range []string{"", " ", "0", "-5", "10,", ",10", "10,,25", "ten", "1.5", "10 25"} {
		if got, err := objective.ParseBudgets(spec); err == nil {
			t.Errorf("ParseBudgets(%q) = %v, want an error", spec, got)
		}
	}
}

// TestBudgetsMaxIsTheRetentionRequirement: a run keeps only its top-k alerts per day, so
// the largest budget is the smallest retention that leaves every budget answerable.
func TestBudgetsMaxIsTheRetentionRequirement(t *testing.T) {
	b, err := objective.ParseBudgets("10,25,50,100")
	if err != nil {
		t.Fatal(err)
	}
	if got := b.Max(); got != 100 {
		t.Errorf("Max = %d, want 100", got)
	}
	if got := objective.Budgets(nil).Max(); got != 0 {
		t.Errorf("Max of an empty set = %d, want 0", got)
	}
}

// TestBudgetsRoundTrip: the value is recorded in a run's provenance, so its rendered form
// has to be one ParseBudgets accepts, or a recorded run cannot be reproduced from it.
func TestBudgetsRoundTrip(t *testing.T) {
	original, err := objective.ParseBudgets("100,10,25,50")
	if err != nil {
		t.Fatal(err)
	}
	again, err := objective.ParseBudgets(original.String())
	if err != nil {
		t.Fatalf("re-parsing %q: %v", original.String(), err)
	}
	if !slices.Equal(original, again) {
		t.Errorf("round trip: %v -> %q -> %v", original, original.String(), again)
	}
}
