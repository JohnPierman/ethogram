package objective

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Budgets is a validated set of per-day alert budgets, ascending and without duplicates.
//
// A budget is the operating point an objective is evaluated at: the number of alerts a day
// the configuration is allowed to emit. The set is held as a value object rather than as a
// bare slice so that the ordering [Utility.Best] relies on for its tie-break — earliest
// index is the smallest queue — is a property of the type instead of an assumption about
// the caller.
//
// It is ascending and deduplicated on construction, so a run's recorded budgets and the
// order its results are reported in cannot disagree, whatever order the operator wrote
// them.
type Budgets []int

// ParseBudgets reads a comma-separated list of per-day alert budgets, for instance
// "10,25,50,100". Whitespace around each entry is ignored.
//
// Every budget must be a positive integer: a budget of zero is an empty queue, which is
// already the reference [Utility.Score] measures against and does not need an operating
// point of its own.
func ParseBudgets(spec string) (Budgets, error) {
	fields := strings.Split(spec, ",")
	out := make(Budgets, 0, len(fields))
	for _, f := range fields {
		trimmed := strings.TrimSpace(f)
		if trimmed == "" {
			return nil, fmt.Errorf("objective: empty budget in %q", spec)
		}
		n, err := strconv.Atoi(trimmed)
		if err != nil {
			return nil, fmt.Errorf("objective: budget %q in %q: %w", trimmed, spec, err)
		}
		if n < 1 {
			return nil, fmt.Errorf("objective: budget %d in %q is not positive", n, spec)
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("objective: no budgets in %q", spec)
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// Max is the largest budget, which is the per-day alert retention a run must keep for
// every budget in the set to be answerable.
func (b Budgets) Max() int {
	if len(b) == 0 {
		return 0
	}
	return b[len(b)-1]
}

// String renders the set in the form ParseBudgets accepts, so a recorded value round-trips.
func (b Budgets) String() string {
	parts := make([]string, len(b))
	for i, n := range b {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, ",")
}
