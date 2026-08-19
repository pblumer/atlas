package logging

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestNothingLogsAroundTheCatalogue is the guard that keeps the contract from eroding one
// convenient call at a time.
//
// The catalogue is only an API if every operational line goes through it. A single
// surviving log.Printf is a line no alert can match and no shipper can parse, and it is
// invisible in review precisely because it looks like every log call anyone has ever
// written. So the rule is checked rather than asked for: outside this package, no direct
// emission through the standard logger or slog.
//
// It matches on function names rather than resolving types, which is what keeps it free
// of a type-checking dependency (ADR-0010). That is sound here because the names below
// share nothing with the *wal.Log methods that also read as `log.Something` — Append,
// Sync, Close, Replay — so an identifier that happens to be named log cannot be mistaken
// for the standard library.
func TestNothingLogsAroundTheCatalogue(t *testing.T) {
	emitters := map[string]map[string]bool{
		"log": {
			"Print": true, "Printf": true, "Println": true,
			"Fatal": true, "Fatalf": true, "Fatalln": true,
			"Panic": true, "Panicf": true, "Panicln": true,
		},
		"slog": {
			"Debug": true, "Info": true, "Warn": true, "Error": true, "Log": true,
			"DebugContext": true, "InfoContext": true, "WarnContext": true,
			"ErrorContext": true, "LogAttrs": true,
		},
	}

	root := ".."
	fset := token.NewFileSet()
	var offenders []string

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "logging", "testdata", "node_modules", "coverage", ".git":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return perr
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
			if emitters[pkg.Name][sel.Sel.Name] {
				offenders = append(offenders,
					fset.Position(call.Pos()).String()+": "+pkg.Name+"."+sel.Sel.Name)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(offenders) > 0 {
		t.Errorf("%d log call(s) bypass the event catalogue — every operational line needs a "+
			"registered event name so an operator can alert on it (ADR-0142):\n\t%s",
			len(offenders), strings.Join(offenders, "\n\t"))
	}
}
