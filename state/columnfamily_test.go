package state

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Every key's first byte is its column family, so two families sharing a value share
// a key space: a range scan, a prefix delete or a retention sweep of one reaches the
// other's records. Nothing else notices — the two families are usually written in
// different files by different changes, and a merge of both raises no conflict. This
// is how cfAuthorization and cfEventLog both came to be 0x32. So the values are read
// from every source file of the package and held to be distinct.
func TestEveryColumnFamilyHasItsOwnValue(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	owner := map[int64]string{}
	found := 0
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			spec, ok := n.(*ast.ValueSpec)
			if !ok || spec.Type == nil {
				return true
			}
			if id, ok := spec.Type.(*ast.Ident); !ok || id.Name != "columnFamily" {
				return true
			}
			for i, name := range spec.Names {
				if i >= len(spec.Values) {
					continue
				}
				lit, ok := spec.Values[i].(*ast.BasicLit)
				if !ok {
					t.Errorf("%s: %s is not a literal; this guard reads literals", path, name.Name)
					continue
				}
				v, err := strconv.ParseInt(lit.Value, 0, 64)
				if err != nil {
					t.Fatalf("%s: %s = %s: %v", path, name.Name, lit.Value, err)
				}
				found++
				if other, dup := owner[v]; dup {
					t.Errorf("%s and %s are both column family %#02x: they share a key space", other, name.Name, v)
				}
				owner[v] = name.Name
			}
			return true
		})
	}
	if found < 50 {
		t.Fatalf("found only %d column families; the scan no longer matches how the package declares them", found)
	}
}
