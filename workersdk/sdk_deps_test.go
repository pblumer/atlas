package workersdk

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const modulePath = "github.com/pblumer/atlas"

// sdkMayImport are the packages of this module the SDK still depends on. Both are
// leaves — they import nothing of atlas themselves, which the test below also checks —
// and both are debts to pay before workersdk becomes a module of its own
// (ADR-0535, migration step 1): the log event
// names, and the lookup query and page a form lookup carries. Adding to this list is a
// decision against that record, not a fix for a failing test.
var sdkMayImport = map[string]bool{
	modulePath + "/logging":          true,
	modulePath + "/connector/lookup": true,
}

// TestTheSDKImportsNothingOfTheEngine holds workersdk to what makes it an SDK: a Worker
// Type built outside this repository depends on it, so it must not pull in the engine —
// job, state, compiler, and through state the storage engine. It reads the imports of
// every non-test file of this package and, transitively, of every atlas package it
// reaches, and fails on any atlas package outside sdkMayImport.
func TestTheSDKImportsNothingOfTheEngine(t *testing.T) {
	seen := map[string]bool{}
	var visit func(dir, pkg string)
	visit = func(dir, pkg string) {
		if seen[pkg] {
			return
		}
		seen[pkg] = true
		for _, imp := range atlasImports(t, dir) {
			if !sdkMayImport[imp] {
				t.Errorf("%s imports %s; workersdk and what it reaches may import only %s",
					pkg, imp, strings.Join(sortedKeys(sdkMayImport), ", "))
				continue
			}
			visit(filepath.Join("..", strings.TrimPrefix(imp, modulePath+"/")), imp)
		}
	}
	visit(".", modulePath+"/workersdk")
}

// atlasImports are the atlas packages the non-test files in dir import.
func atlasImports(t *testing.T, dir string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatalf("glob %s: %v", dir, err)
	}
	if len(files) == 0 {
		t.Fatalf("no Go files in %s", dir)
	}
	var out []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		for _, imp := range parsed.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if path == modulePath || strings.HasPrefix(path, modulePath+"/") {
				out = append(out, path)
			}
		}
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
