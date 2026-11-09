package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strings"
	"testing"
)

// TestEveryVariableEntryChecksDocumentReferences is the guard the document store's
// binding rests on (ADR-draft-a-file-is-a-document-not-a-variable).
//
// A variable that names a document is only as safe as the route it came in on: the
// route must refuse a reference the caller may not use, and bind the document to the
// instance once the command is durable, or refuse references outright where it cannot
// bind them. Nothing in the engine enforces this — a reference is plain JSON — so a
// route added later that forgets it would accept a reference to somebody else's upload,
// or leave a document bound to nothing until its time to live removes it under a
// running instance.
//
// So this reads the package's source: every function that turns a request's values
// into variables (parseStartVariables, startVarsFromMap) must also call the check or
// the refusal, or hand them to the start funnel that does. The few that build their
// variables themselves, from values no caller authored, are listed with the reason.
func TestEveryVariableEntryChecksDocumentReferences(t *testing.T) {
	parsers := map[string]bool{"parseStartVariables": true, "startVarsFromMap": true}
	checks := map[string]bool{
		"checkDocumentRefs":    true,
		"refuseDocumentRefs":   true,
		"startInstance":        true, // → startInstanceGuarded, which checks (asserted below)
		"startInstanceGuarded": true,
	}
	exempt := map[string]string{
		"parseStartVariables":         "the parser itself",
		"handleDecideApprovals":       "builds two fixed variables (genehmigt, begruendung) from a boolean and a text; nothing the caller sends can be an object",
		"handleCreateInstanceFromCSV": "builds rows, rowCount and fileName from a parsed CSV file; a CSV cell is text, never a JSON object",
		"selfStartBody":               "a parser only; its caller handleMyProcessStart hands what it parsed to startInstanceGuarded, which checks",
	}

	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var missing []string
	guardedCalls := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			calls := map[string]bool{}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if c, ok := n.(*ast.CallExpr); ok {
					switch f := c.Fun.(type) {
					case *ast.Ident:
						calls[f.Name] = true
					case *ast.SelectorExpr:
						calls[f.Sel.Name] = true
					}
				}
				return true
			})
			if fn.Name.Name == "startInstanceGuarded" {
				guardedCalls = calls
			}
			parses := false
			for p := range parsers {
				if calls[p] {
					parses = true
				}
			}
			if !parses {
				continue
			}
			if _, ok := exempt[fn.Name.Name]; ok {
				continue
			}
			checked := false
			for c := range checks {
				if calls[c] {
					checked = true
				}
			}
			if !checked {
				missing = append(missing, name+": "+fn.Name.Name)
			}
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("these functions let variable values in without checking the document references "+
			"they may carry — call checkDocumentRefs (and bindDocuments once the command is durable), "+
			"or refuseDocumentRefs where no instance can be bound:\n  %s", strings.Join(missing, "\n  "))
	}
	if !guardedCalls["checkDocumentRefs"] || !guardedCalls["bindDocuments"] {
		t.Errorf("startInstanceGuarded is counted as a check, so it must call checkDocumentRefs and bindDocuments")
	}
}
