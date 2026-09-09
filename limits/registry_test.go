package limits_test

import (
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// ceilingCalls are the calls that bound how much external input this process will
// hold, and which argument carries the bound. Each one is a resource budget whether
// or not anybody called it that.
var ceilingCalls = map[string]int{
	"io.LimitReader":      1, // (r, n)
	"io.CopyN":            2, // (dst, src, n)
	"http.MaxBytesReader": 2, // (w, r, n)
}

// classified are the ceilings that are deliberately *not* installation budgets, each
// with the reason. This is the other half of the completeness rule: the test does not
// ask whether the list of budgets is right, it asks whether anything is unclassified —
// so an exemption is a decision somebody wrote down, not a silence.
//
// Keyed by "<path>:<expression as it is printed>".
var classified = map[string]string{
	"mcp/http.go:maxLine": "JSON-RPC framing, shared with the stdio transport where there is no HTTP body at " +
		"all; it belongs to the protocol's own constants rather than to an operator's memory policy",
	"cmd/atlas/playgroundrun.go:64 << 20": "the CLI reading an answer from a server it was pointed at — a " +
		"client's own ceiling, not this installation's",
	"connector/rest/openapimock/server.go:maxRecordedBody": "a mock server that exists for tests",
	"connector/remedy/mock/server.go:1 << 20":              "a mock server that exists for tests",
}

// TestNoCeilingWithoutAName is what makes this package more than a file of constants.
// The audit of 2026-09-07 did not find a wrong ceiling; it found a missing one, in
// among twenty that were present, because nothing said what the set was supposed to
// be. A list alone would go stale the same way. So the rule is checked rather than
// written down: every bound on externally supplied input reads its number from
// [limits.Limits] — through a service's configured budgets, or through a parameter a
// caller resolved from one — or it is named above as something else, with the reason.
func TestNoCeilingWithoutAName(t *testing.T) {
	const root = ".."
	fset := token.NewFileSet()
	files := goFiles(t, root)

	// Package-level names are what separates a budget from a constant beside the
	// call. `limit` in `io.LimitReader(r, limit+1)` is whatever the caller passed,
	// so the ceiling is named one frame up; `maxLine` is a number this file chose,
	// and that is precisely the shape the audit found a hole in.
	pkgConsts := map[string]map[string]bool{}
	parsed := map[string]*ast.File{}
	for _, path := range files {
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", path, err)
		}
		parsed[path] = f
		dir := filepath.Dir(path)
		if pkgConsts[dir] == nil {
			pkgConsts[dir] = map[string]bool{}
		}
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || (gd.Tok != token.CONST && gd.Tok != token.VAR) {
				continue
			}
			for _, spec := range gd.Specs {
				if vs, ok := spec.(*ast.ValueSpec); ok {
					for _, name := range vs.Names {
						pkgConsts[dir][name.Name] = true
					}
				}
			}
		}
	}

	found := 0
	for _, path := range files {
		rel := filepath.ToSlash(strings.TrimPrefix(filepath.Clean(path), "../"))
		dir := filepath.Dir(path)
		ast.Inspect(parsed[path], func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			idx, ok := ceilingCalls[render(fset, call.Fun)]
			if !ok || idx >= len(call.Args) {
				return true
			}
			found++
			expr := render(fset, call.Args[idx])
			if named(expr, pkgConsts[dir]) {
				return true
			}
			if _, ok := classified[rel+":"+expr]; ok {
				return true
			}
			t.Errorf("%s:%d bounds input at %s, which is a ceiling with no name.\n"+
				"Give it a budget in limits.Limits, or classify it in this test's `classified` "+
				"table with the reason it is not one.", rel, fset.Position(call.Pos()).Line, expr)
			return true
		})
	}

	// A rule that checks nothing reports the same "ok" as a rule everything satisfies.
	// This repository bounds input in dozens of places; far fewer than that means the
	// walk stopped early or the call names above went stale, and a green result here
	// would mean nothing. The first draft of this test walked no files at all and
	// passed.
	if found < 40 {
		t.Errorf("only %d bounded reads found; the walk or ceilingCalls is broken", found)
	}
}

// named reports whether a ceiling expression takes its number from the registry: the
// registry itself, a component's budgets accessor, or a name that is not this
// package's own — which makes it a parameter or a field, resolved by whoever called.
//
// `.budgets().` is the canonical form. Reading the field directly is not accepted,
// and deliberately: a Server or Service built as a struct literal has the zero
// Limits, which is every ceiling at zero, and the accessor is what defaults it.
func named(expr string, pkgLevel map[string]bool) bool {
	// Package-qualified, so it must start the expression: `limits.Default().X` is the
	// registry, while `s.limits.X` is the field behind the accessor and is exactly
	// what this rule has to keep rejecting.
	if strings.HasPrefix(expr, "limits.") || strings.Contains(expr, ".budgets().") {
		return true
	}
	root := strings.TrimSpace(expr)
	if i := strings.IndexAny(root, "+- ("); i > 0 {
		root = strings.TrimSpace(root[:i])
	}
	if root == "" || !isIdent(root) {
		return false // a bare literal is never a named budget
	}
	return !pkgLevel[root]
}

func isIdent(s string) bool {
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
		case i > 0 && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}

// goFiles lists the non-test Go sources in the tree, skipping what is not this
// repository's own code.
func goFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir():
			// The root itself is named "..", which is why this asks about the path and
			// not only about the entry's name: skipping every dot-prefixed name would
			// skip the tree before entering it, and the test would then pass by walking
			// nothing at all. It did, on the first draft, and looked exactly like success.
			if path == root {
				return nil
			}
			if name := d.Name(); name == "node_modules" || name == "testdata" || strings.HasPrefix(name, ".") {
				return fs.SkipDir
			}
			return nil
		case strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go"):
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the tree: %v", err)
	}
	return out
}

func render(fset *token.FileSet, n ast.Node) string {
	var b strings.Builder
	_ = printer.Fprint(&b, fset, n)
	return b.String()
}
