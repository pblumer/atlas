package api_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestJobsAreNotDrivenOnTheRunLoop is the regression guard for ADR-0150. Driving
// jobs dispatches onto the run loop (claim, then apply), so it must happen on a
// request's own goroutine — never from inside a do() turn, which is the loop
// goroutine itself. Two things break if it does: the drive deadlocks against the
// loop that would have to serve it, and — the reason the rule exists — a worker's
// outbound call would once again hold the single writer that serves every request.
//
// The check is static and transitive: it collects the functions in package api
// that reach Drive, directly or through another function in the package, and
// fails if any of them is called inside a do() closure. It parses to an AST
// rather than grepping, so prose like this comment never trips it.
func TestJobsAreNotDrivenOnTheRunLoop(t *testing.T) {
	fset := token.NewFileSet()
	files := map[string]*ast.File{}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(".", name), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		files[name] = f
	}
	if len(files) == 0 {
		t.Fatal("no source files scanned; the drift test would pass vacuously")
	}

	// Package-level functions, so a bare name() call can be told apart from a
	// method on some other object.
	plain := map[string]bool{}
	for _, f := range files {
		for _, decl := range f.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil {
				plain[fn.Name.Name] = true
			}
		}
	}

	// Every function that reaches Drive, to a fixed point over the package's own
	// call graph. Resolution is by name, narrowed to the two shapes package api
	// actually uses to call itself — s.method() and a package-level function — so a
	// same-named method on another object (token.New, sync.Once.Do) is not mistaken
	// for one of ours.
	drivers := map[string]bool{}
	for {
		grew := false
		for _, f := range files {
			for _, decl := range f.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil || drivers[fn.Name.Name] {
					continue
				}
				if via := reaches(fn.Body, drivers, plain); via != "" {
					drivers[fn.Name.Name] = true
					grew = true
				}
			}
		}
		if !grew {
			break
		}
	}

	if len(drivers) == 0 {
		t.Fatal("no function in package api reaches Drive; the scan found nothing to check")
	}

	var offenders []string
	for name, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || !isLoopDispatch(call) {
				return true
			}
			for _, arg := range call.Args {
				lit, ok := arg.(*ast.FuncLit)
				if !ok {
					continue
				}
				if via := reaches(lit.Body, drivers, plain); via != "" {
					pos := fset.Position(lit.Pos())
					offenders = append(offenders,
						fmt.Sprintf("%s:%d (via %s)", name, pos.Line, via))
				}
			}
			return true
		})
	}
	if len(offenders) > 0 {
		sort.Strings(offenders)
		t.Errorf("job driving inside a run-loop turn (ADR-0150): %s\n"+
			"Hoist the drive out of the do() closure: mutate on the loop, then drive from the caller's goroutine.",
			strings.Join(offenders, ", "))
	}
}

// reaches reports which call in a body gets to Drive — directly, or through a
// function already known to reach it — and "" when none does.
func reaches(body *ast.BlockStmt, drivers, plain map[string]bool) string {
	via := ""
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fn := call.Fun.(type) {
		case *ast.SelectorExpr:
			if fn.Sel.Name == "Drive" { // …jobRunner.Drive()
				via = fn.Sel.Name
				break
			}
			// s.helper(): a method on the server, which is how every handler in this
			// package reaches its own code. Anything else is another object's method.
			if recv, ok := fn.X.(*ast.Ident); ok && recv.Name == "s" && drivers[fn.Sel.Name] {
				via = fn.Sel.Name
			}
		case *ast.Ident: // helper() declared in this package
			if plain[fn.Name] && drivers[fn.Name] {
				via = fn.Name
			}
		}
		return via == ""
	})
	return via
}

// isLoopDispatch reports whether a call hands a closure to the single writer —
// s.do(...) or a service's loop.Do(...).
func isLoopDispatch(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	if sel.Sel.Name != "do" && sel.Sel.Name != "Do" {
		return false
	}
	// Only the run loop, not sync.Once.Do and friends: the receiver is the server
	// itself (s.do) or something named like the loop it holds.
	recv, ok := sel.X.(*ast.Ident)
	if !ok {
		return sel.Sel.Name == "do"
	}
	return sel.Sel.Name == "do" || recv.Name == "loop" || recv.Name == "runLoop"
}
