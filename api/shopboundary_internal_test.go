package api

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The boundary between tshopper and the engine (ADR-0515, api/shopcontract).
//
// Phase A draws it inside one binary, so nothing stops a server file from reaching
// into the catalogue or the order package directly — which is how the shop came to
// be woven through thirty-seven files of this package. These two guards make the
// boundary a fact rather than a wish: the shop's report service reaches the engine
// through the contract only, and the number of server files that import the shop's
// packages directly may fall and never rise.

// shopImportersCeiling is how many non-test files of package api import api/catalog
// or api/order. Lower it when a file stops; never raise it — a new route of the
// shop belongs in the shop's own package, reaching the engine through
// api/shopcontract.
const shopImportersCeiling = 37

func shopImporters(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fset, f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for _, imp := range parsed.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			if p == "github.com/pblumer/atlas/api/catalog" || p == "github.com/pblumer/atlas/api/order" {
				out = append(out, f)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

func TestServerFilesReachingIntoTheShopOnlyBecomeFewer(t *testing.T) {
	got := shopImporters(t)
	if len(got) > shopImportersCeiling {
		t.Fatalf("%d files of package api import api/catalog or api/order, above the ceiling of %d: "+
			"put the new code in the shop's own package and reach the engine through api/shopcontract "+
			"(ADR-0515)\n%s", len(got), shopImportersCeiling, strings.Join(got, "\n"))
	}
	if len(got) < shopImportersCeiling {
		t.Errorf("%d files import the shop's packages now, below the ceiling of %d: lower "+
			"shopImportersCeiling to %d so the progress is kept", len(got), shopImportersCeiling, len(got))
	}
}

func TestTheShopsSideReachesTheEngineOnlyThroughTheContract(t *testing.T) {
	forbidden := []string{"github.com/pblumer/atlas/engine", "github.com/pblumer/atlas/state",
		"github.com/pblumer/atlas/model", "github.com/pblumer/atlas/api/runloop"}
	for _, dir := range []string{"servicereport", "shopcontract"} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		if len(files) == 0 {
			t.Fatalf("no files in %s", dir)
		}
		fset := token.NewFileSet()
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			src, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := parser.ParseFile(fset, f, src, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			for _, imp := range parsed.Imports {
				p := strings.Trim(imp.Path.Value, `"`)
				for _, no := range forbidden {
					if p == no || strings.HasPrefix(p, no+"/") {
						t.Errorf("%s imports %s; the shop reaches the engine through api/shopcontract only", f, p)
					}
				}
			}
		}
	}
}
