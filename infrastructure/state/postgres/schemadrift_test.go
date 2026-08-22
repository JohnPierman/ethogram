package postgres

import (
	"os"
	"reflect"
	"strings"
	"testing"
	"unicode"

	"github.com/JohnPierman/ethogram/domain/timing"
	"github.com/JohnPierman/ethogram/domain/volume"
)

// This file carries no build tag on purpose. The equivalence test that proves the Postgres
// store is a drop-in replacement needs a database and so sits behind `integration`, which
// means it does not run in CI -- and #33 is what that costs: three fields of volume.State had
// no column for as long as nobody ran Postgres by hand, and the equivalence test could not
// have caught it anyway, because it compared a hand-written list of fields naming only the
// ones already persisted.
//
// The guards below need no database.

// snakeCase converts a Go field name to the column convention used throughout this schema.
func snakeCase(name string) string {
	var b strings.Builder
	for i, r := range name {
		if i > 0 && unicode.IsUpper(r) {
			b.WriteByte('_')
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// storeSQL is every non-test Go file in this package concatenated.
func storeSQL(t *testing.T) string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	var b strings.Builder
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		body, readErr := os.ReadFile(name)
		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}
		b.Write(body)
	}
	return b.String()
}

// statementsFor returns every backtick-quoted SQL literal in this package that names the given
// table, split into the three places a column has to appear for a field to survive a restart:
// the DDL that creates or alters the table, the SELECT that reads it, and the INSERT that
// writes it.
//
// Splitting them is the whole point, and the first version of this guard did not. Checking one
// concatenation of the package finds a column name that appears ANYWHERE -- so deleting three
// columns from the schema while the queries still named them passed, which was verified by
// deleting them. A guard that cannot fail on the bug it was written for is worse than no guard,
// because it is also an assurance.
func statementsFor(text, table string) (ddl, selects, inserts []string) {
	for _, lit := range strings.Split(text, "`") {
		if !strings.Contains(lit, table) {
			continue
		}
		upper := strings.ToUpper(lit)
		switch {
		case strings.Contains(upper, "CREATE TABLE"), strings.Contains(upper, "ALTER TABLE"):
			ddl = append(ddl, lit)
		case strings.Contains(upper, "SELECT"):
			selects = append(selects, lit)
		case strings.Contains(upper, "INSERT INTO"):
			inserts = append(inserts, lit)
		}
	}
	return ddl, selects, inserts
}

// mentions reports whether any of the statements names the column as a whole SQL identifier.
//
// Token-aware rather than substring, because this schema has columns named `c`, `s` and `w`:
// a substring test for "c" matches every statement ever written, and would report a missing
// column as present.
func mentions(statements []string, column string) bool {
	for _, stmt := range statements {
		for _, tok := range strings.FieldsFunc(stmt, func(r rune) bool {
			return r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r)
		}) {
			if tok == column {
				return true
			}
		}
	}
	return false
}

// TestEveryPersistedFieldHasAColumn is the regression guard for #33.
//
// It is a source-text check, which is unusual and is the point: the property it protects is a
// correspondence between a Go struct and SQL string literals, and nothing in the type system
// relates the two. A schema is one of the few places where "the compiler would have caught it"
// is simply false.
//
// Only the structs this package actually persists are checked. What it does NOT do is decide
// which arms ought to be persisted -- TestPersistedArmsAreTheRecordedSet does that.
func TestEveryPersistedFieldHasAColumn(t *testing.T) {
	text := storeSQL(t)

	for _, tc := range []struct {
		name  string
		table string
		typ   reflect.Type
		// spelled names the columns whose spelling is not snakeCase of the field.
		spelled map[string]string
		// flattened names the columns a composite field is spread across, so the guard
		// insists on all of them rather than accepting the parent being absent. This is the
		// escape hatch that could defeat the check, so every entry carries a reason.
		flattened map[string][]string
	}{
		{
			name:  "volume.State",
			table: "volume_state",
			typ:   reflect.TypeOf(volume.State{}),
			spelled: map[string]string{
				"LastSeen": "last_seen_us", // the column carries microseconds
			},
			flattened: map[string][]string{
				// The Gamma posterior is two float columns rather than a composite type.
				"Rate": {"a", "b"},
			},
		},
		{
			name:  "timing.State",
			table: "timing_state",
			typ:   reflect.TypeOf(timing.State{}),
			spelled: map[string]string{
				"LastSeen": "last_seen_us",
				// snakeCase would ask for log_u_sum_sq; the column is log_u_sumsq. The
				// mapping is explicit rather than clever for exactly this reason -- a guard
				// that guesses names produces false alarms, and a false alarm here costs
				// somebody the afternoon that a real one would have saved them.
				"LogUSumSq": "log_u_sumsq",
			},
			flattened: map[string][]string{
				// The Fourier moments are three columns: two float arrays and the weight.
				"Moments": {"c", "s", "w"},
			},
		},
	} {
		ddl, selects, inserts := statementsFor(text, tc.table)
		if len(ddl) == 0 || len(selects) == 0 || len(inserts) == 0 {
			t.Fatalf("%s: found %d DDL, %d SELECT and %d INSERT statements naming %q;"+
				" the guard cannot check a table it cannot find",
				tc.name, len(ddl), len(selects), len(inserts), tc.table)
		}

		check := func(fieldName, column string) {
			// All three, separately. A column in the DDL but not the INSERT is written
			// never; in the INSERT but not the SELECT is read never; in neither is #33.
			for _, region := range []struct {
				what string
				in   []string
			}{
				{"the schema", ddl},
				{"the SELECT", selects},
				{"the INSERT", inserts},
			} {
				if !mentions(region.in, column) {
					t.Errorf("%s.%s: %q is absent from %s, so the field does not survive"+
						" a restart (#33)", tc.name, fieldName, column, region.what)
				}
			}
		}

		for i := 0; i < tc.typ.NumField(); i++ {
			field := tc.typ.Field(i)
			if !field.IsExported() {
				continue
			}
			if parts, ok := tc.flattened[field.Name]; ok {
				for _, p := range parts {
					check(field.Name, p)
				}
				continue
			}
			column := snakeCase(field.Name)
			if override, ok := tc.spelled[field.Name]; ok {
				column = override
			}
			check(field.Name, column)
		}
	}
}

// TestPersistedArmsAreTheRecordedSet records which arms this store can carry across a restart,
// and it is a short list.
//
// Four of the seven arms have no Postgres store at all -- no table, no accessor, nothing --
// so a Postgres-backed deployment does not lose part of their state the way #33 described,
// it has none of it. That costs no recorded measurement, because every run in `results/`
// uses the memory stores; it is asserted so the gap is a decision with a date on it rather
// than something a reader has to discover by grepping for a table that is not there.
//
// Whoever adds a store will find this test failing and update the list, which is the point.
func TestPersistedArmsAreTheRecordedSet(t *testing.T) {
	text := storeSQL(t)

	for table, wantPresent := range map[string]bool{
		"novelty_value_count": true, // serves novelty and, per-entity, pairing
		"timing_state":        true,
		"volume_state":        true,
		"graph_node":          true, // co-occurrence
		"graph_edge":          true,
		"noveltyrate_state":   false,
		"drift_state":         false,
		"marginal_state":      false,
	} {
		got := strings.Contains(text, table)
		if got == wantPresent {
			continue
		}
		if wantPresent {
			t.Errorf("table %q has gone from this store's schema", table)
		} else {
			t.Errorf("table %q now exists: this arm is persisted, so add it to"+
				" TestEveryPersistedFieldHasAColumn and drop it from this list", table)
		}
	}
}
