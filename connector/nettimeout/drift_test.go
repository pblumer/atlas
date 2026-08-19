package nettimeout_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// scannedTrees are the packages whose code runs on the run-loop goroutine as a
// job worker (every connector) or is called by one (the DMN model resolver).
// These are exactly the places where an unbounded outbound call can stall the
// engine's single writer.
var scannedTrees = []string{"connector", "dmn"}

// TestNoUnboundedHTTPClientInConnectors is the regression guard for the outage
// this package exists to prevent: a connector using http.DefaultClient, whose
// zero Timeout means "wait forever". Because connector workers run ON the
// run-loop goroutine, one hung host then parks the whole engine — the API keeps
// answering /info while every request that touches the loop hangs.
//
// A new connector must therefore build its client from nettimeout (or set its
// own non-zero Timeout). This test walks the real source, so it fails when the
// hazard is reintroduced rather than when someone remembers to look.
//
// It parses to an AST rather than grepping, so prose mentioning
// http.DefaultClient (like this package's own doc comment) never trips it.
func TestNoUnboundedHTTPClientInConnectors(t *testing.T) {
	var offenders []string
	for _, tree := range scannedTrees {
		root := filepath.Join("..", "..", tree)
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			fset := token.NewFileSet()
			f, perr := parser.ParseFile(fset, path, nil, 0) // no comments parsed
			if perr != nil {
				return perr
			}
			ast.Inspect(f, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "DefaultClient" {
					return true
				}
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == "http" {
					offenders = append(offenders, fset.Position(sel.Pos()).String())
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	if len(offenders) > 0 {
		sort.Strings(offenders)
		t.Fatalf("http.DefaultClient (no timeout) used in %d place(s) that run on the run-loop goroutine:\n\t%s\n\n"+
			"An unbounded outbound call lets one hung host stall the whole engine. "+
			"Build the client with nettimeout.HTTPClient() instead.",
			len(offenders), strings.Join(offenders, "\n\t"))
	}
}
