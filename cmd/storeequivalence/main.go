// Command storeequivalence compares two replay results that should be identical, and exits
// non-zero if they are not (#48).
//
// # What it is for
//
// Every file in results/ was produced with the in-memory stores, so the framework had no
// recorded measurement that the persistent path produces the same numbers end to end -- only
// per-operation equivalence tests over synthetic observations. Those are strong evidence and
// a different claim: a replay folds state across millions of events, and an equivalence that
// holds per call can still be broken by a transaction boundary, a partial write, or a field
// added without a column, which has happened twice.
//
// # Why byte equality is the right bar
//
// Nothing in the scoring path is stochastic (R4). Two runs over the same corpus prefix with
// the same flags must therefore agree exactly, and anything less than exact agreement is a
// defect rather than noise. This tool does not compare with a tolerance, and adding one would
// be the wrong repair for any failure it reports.
//
// # What is masked, and why each one has to be
//
// Only values that cannot be equal between two separate runs, and the store's own name:
//
//   - the run id, which names the store by construction, so the two runs can be told apart;
//   - wall-clock timings, memory figures and rates, which are properties of the machine;
//   - the `store` provenance block, which exists to say which store was used.
//
// Nothing else. In particular the final store SIZES are compared rather than masked: a store
// size is deterministic, and two runs producing identical scores from different-sized stores
// would be a defect this comparison would otherwise miss.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"reflect"
	"sort"
	"strings"
)

// masked are the JSON paths whose values may legitimately differ. Paths are matched as
// prefixes of the slash-separated location, so masking a subtree needs one entry.
var masked = []string{
	"/run/run_id",
	"/run/started_at",
	"/run/finished_at",
	"/run/git_sha",
	"/runtime/wall_seconds",
	"/runtime/events_per_sec",
	"/runtime/heap_alloc_mb",
	"/runtime/heap_sys_mb",
	"/store",
}

func isMasked(path string) bool {
	for _, m := range masked {
		if path == m || strings.HasPrefix(path, m+"/") {
			return true
		}
	}
	return false
}

// difference is one disagreement between the two documents.
type difference struct {
	Path  string
	Kind  string
	First any
	Other any
}

func (d difference) String() string {
	switch d.Kind {
	case "only-in-first", "only-in-second":
		return fmt.Sprintf("%-16s %s", d.Kind, d.Path)
	case "length":
		return fmt.Sprintf("%-16s %s: %v against %v", d.Kind, d.Path, d.First, d.Other)
	default:
		return fmt.Sprintf("%-16s %s: %v against %v", d.Kind, d.Path, d.First, d.Other)
	}
}

// compare walks the two documents in a fixed key order and collects every disagreement.
//
// Every difference is collected rather than stopping at the first: a schema gap shows up as
// many differences at once, and their shape is what says which field is missing.
func compare(first, other any, path string, out *[]difference) {
	if isMasked(path) {
		return
	}

	switch a := first.(type) {
	case map[string]any:
		b, ok := other.(map[string]any)
		if !ok {
			*out = append(*out, difference{path, "type", typeOf(first), typeOf(other)})
			return
		}
		keys := map[string]bool{}
		for k := range a {
			keys[k] = true
		}
		for k := range b {
			keys[k] = true
		}
		ordered := make([]string, 0, len(keys))
		for k := range keys {
			ordered = append(ordered, k)
		}
		sort.Strings(ordered)
		for _, k := range ordered {
			child := path + "/" + k
			if isMasked(child) {
				continue
			}
			av, inA := a[k]
			bv, inB := b[k]
			switch {
			case !inA:
				*out = append(*out, difference{Path: child, Kind: "only-in-second"})
			case !inB:
				*out = append(*out, difference{Path: child, Kind: "only-in-first"})
			default:
				compare(av, bv, child, out)
			}
		}
	case []any:
		b, ok := other.([]any)
		if !ok {
			*out = append(*out, difference{path, "type", typeOf(first), typeOf(other)})
			return
		}
		if len(a) != len(b) {
			*out = append(*out, difference{path, "length", len(a), len(b)})
			return
		}
		for i := range a {
			compare(a[i], b[i], fmt.Sprintf("%s[%d]", path, i), out)
		}
	default:
		if !reflect.DeepEqual(first, other) {
			*out = append(*out, difference{path, "value", first, other})
		}
	}
}

func typeOf(v any) string {
	if v == nil {
		return "null"
	}
	return reflect.TypeOf(v).String()
}

func load(path string) (map[string]any, error) {
	body, err := os.ReadFile(path) //nolint:gosec // the path the flag names
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return doc, nil
}

func main() {
	log.SetFlags(0)
	first := flag.String("first", "", "the first result file (required)")
	second := flag.String("second", "", "the second result file (required)")
	limit := flag.Int("limit", 40, "how many differences to print")
	flag.Parse()

	if *first == "" || *second == "" {
		log.Fatal("both -first and -second are required")
	}

	a, err := load(*first)
	if err != nil {
		log.Fatal(err)
	}
	b, err := load(*second)
	if err != nil {
		log.Fatal(err)
	}

	var differences []difference
	compare(a, b, "", &differences)

	// The claim the comparison supports is worth stating with its own numbers, so a reader
	// of the output does not have to look them up.
	scored := "unknown"
	if run, ok := a["run"].(map[string]any); ok {
		if events, ok := run["events_scored"]; ok {
			scored = fmt.Sprint(events)
		}
	}
	if corpus, ok := a["corpus"].(map[string]any); ok {
		if events, ok := corpus["events_scored"]; ok {
			scored = fmt.Sprint(events)
		}
	}

	if len(differences) == 0 {
		fmt.Printf("the two stores produced identical output over %s scored events\n", scored)
		fmt.Printf("compared: %s\n          %s\n", *first, *second)
		return
	}

	fmt.Printf("%d difference(s) over %s scored events:\n", len(differences), scored)
	for i, d := range differences {
		if i >= *limit {
			fmt.Printf("  ... and %d more\n", len(differences)-*limit)
			break
		}
		fmt.Printf("  %s\n", d)
	}
	fmt.Println("\nNothing in the scoring path is stochastic (R4), so this is a defect " +
		"rather than noise. Do not add a tolerance.")
	os.Exit(1)
}
