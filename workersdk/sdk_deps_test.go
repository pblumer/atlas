package workersdk

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const modulePath = "github.com/pblumer/atlas"

// sdkPath is this package's import path; its own subpackages (workersdk/lookup) are the
// SDK too and may be imported.
const sdkPath = modulePath + "/workersdk"

// TestTheSDKImportsNothingOfAtlas holds workersdk to what makes it an SDK: a Worker
// Type built outside this repository depends on it, so it must pull in nothing of
// atlas — not the engine (job, state, compiler, and through state the storage engine),
// and not the AGPL packages around it either, since the SDK is to be licensed apart
// from them (ADR-0535, ADR-0536). It reads the
// imports of every non-test file of this package and, transitively, of its
// subpackages, and fails on any atlas package outside workersdk.
func TestTheSDKImportsNothingOfAtlas(t *testing.T) {
	seen := map[string]bool{}
	var visit func(dir, pkg string)
	visit = func(dir, pkg string) {
		if seen[pkg] {
			return
		}
		seen[pkg] = true
		for _, imp := range atlasImports(t, dir) {
			if imp != sdkPath && !strings.HasPrefix(imp, sdkPath+"/") {
				t.Errorf("%s imports %s; workersdk may import nothing of atlas outside itself", pkg, imp)
				continue
			}
			visit(filepath.Join(dir, strings.TrimPrefix(strings.TrimPrefix(imp, sdkPath), "/")), imp)
		}
	}
	visit(".", sdkPath)
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
