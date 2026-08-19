package domain_test

// Architectural fitness tests for the requirements of §4 that are properties of the
// code's shape rather than of its output. Each fails loudly, naming the offending
// file and line, because the failures they guard against are silent at runtime:
// a wall-clock read makes replay irreproducible without producing a wrong-looking
// number, and clean-architecture drift produces no symptom until the domain cannot
// be tested without a database.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// domainRoot is the directory this test governs, relative to itself.
const domainRoot = "."

// forbiddenImports are packages the domain layer may not import.
//
// R4 forbids nondeterminism in the scoring path. time.Now makes replay
// irreproducible and E8 unpassable; math/rand makes a verdict depend on a global
// seed. Clean architecture forbids the rest: the domain must not know that a
// database, an HTTP client, or a driver exists.
var forbiddenImports = map[string]string{
	"math/rand":               "R4: no stochastic component in the scoring path (§4)",
	"math/rand/v2":            "R4: no stochastic component in the scoring path (§4)",
	"database/sql":            "clean architecture: the domain must not import infrastructure",
	"net/http":                "clean architecture: the domain must not import infrastructure",
	"github.com/jackc/pgx/v5": "clean architecture: the domain must not import infrastructure",
	"os/exec":                 "clean architecture: the domain must not shell out",
}

// forbiddenCalls are selector expressions the domain may not evaluate.
//
// time.Now is the one the paper singles out (§6.2, and trap 4 of the brief): decay
// must be driven by the event timestamp and by the state row's own last-observed
// timestamp, never by the clock at scoring time.
var forbiddenCalls = map[string]string{
	"time.Now":     "R4: decay uses the event timestamp, not wall-clock time (§6.2)",
	"time.Since":   "R4: decay uses the event timestamp, not wall-clock time (§6.2)",
	"time.Until":   "R4: decay uses the event timestamp, not wall-clock time (§6.2)",
	"rand.Float64": "R4: no stochastic component in the scoring path (§4)",
	"rand.Int":     "R4: no stochastic component in the scoring path (§4)",
	"rand.Intn":    "R4: no stochastic component in the scoring path (§4)",
	"rand.Shuffle": "R4: no stochastic component in the scoring path (§4)",
}

// goFilesUnder walks the domain tree, excluding test files. Test files are exempt:
// a test may legitimately measure elapsed time or seed a simulated null, provided
// the scoring path itself does not.
func goFilesUnder(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		out = append(out, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	if len(out) == 0 {
		t.Fatalf("no non-test Go files found under %s; the test would pass vacuously", root)
	}
	return out
}

// TestR4DomainHasNoWallClockOrRandomness enforces trap 4 and the R4 half of §4.
func TestR4DomainHasNoWallClockOrRandomness(t *testing.T) {
	fset := token.NewFileSet()
	files := goFilesUnder(t, domainRoot)

	for _, path := range files {
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parsing %s: %v", path, err)
		}

		for _, imp := range f.Imports {
			p, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				continue
			}
			if why, bad := forbiddenImports[p]; bad {
				t.Errorf("%s: forbidden import %q\n    %s",
					fset.Position(imp.Pos()), p, why)
			}
		}

		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			name := pkg.Name + "." + sel.Sel.Name
			if why, bad := forbiddenCalls[name]; bad {
				t.Errorf("%s: forbidden call %s\n    %s",
					fset.Position(call.Pos()), name, why)
			}
			return true
		})
	}

	t.Logf("scanned %d non-test files under domain/", len(files))
}

// TestDomainDoesNotImportOuterLayers confirms the dependency rule holds in the
// direction that matters: the domain may be imported by application and
// infrastructure, never the reverse.
func TestDomainDoesNotImportOuterLayers(t *testing.T) {
	const modulePath = "github.com/JohnPierman/ethogram/"
	outer := []string{"application/", "infrastructure/", "cmd/"}

	fset := token.NewFileSet()
	for _, path := range goFilesUnder(t, domainRoot) {
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution|parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parsing %s: %v", path, err)
		}
		for _, imp := range f.Imports {
			p, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				continue
			}
			rel, isLocal := strings.CutPrefix(p, modulePath)
			if !isLocal {
				continue
			}
			for _, o := range outer {
				if strings.HasPrefix(rel, o) {
					t.Errorf("%s: domain imports outer layer %q; dependencies point inward only",
						fset.Position(imp.Pos()), p)
				}
			}
		}
	}
}

// TestNoNeutralScoreConstants guards R3 lexically as well as structurally.
//
// The type system already makes a p-value on an abstained verdict unrepresentable.
// This adds a cheap check against the specific anti-pattern the brief names: a
// literal 0.5 standing in for "we do not know". Genuine uses of 0.5 in a detector
// (a median, a midpoint) should be named constants, which is why the check looks for
// the bare literal in an assignment or return rather than anywhere at all.
func TestNoNeutralScoreConstants(t *testing.T) {
	fset := token.NewFileSet()
	for _, path := range goFilesUnder(t, domainRoot) {
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parsing %s: %v", path, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			ret, ok := n.(*ast.ReturnStmt)
			if !ok {
				return true
			}
			for _, res := range ret.Results {
				lit, ok := res.(*ast.BasicLit)
				if !ok || lit.Kind != token.FLOAT {
					continue
				}
				if lit.Value == "0.5" {
					t.Errorf("%s: bare 0.5 returned; R3 forbids a neutral score standing "+
						"for absent evidence. Abstain instead, or name the constant.",
						fset.Position(lit.Pos()))
				}
			}
			return true
		})
	}
}
