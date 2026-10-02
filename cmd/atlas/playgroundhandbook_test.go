package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The handbook's test chapter (api/web/handbuch.html, #szenarien) teaches `atlas
// playground` as the command a pipeline runs: a table of its flags, and the exit
// status that turns a build red. Both are a copy of what this directory defines, and
// a copy drifts — a flag added here and never documented is a flag nobody finds, and
// a flag renamed here but not there is an instruction that fails on its first run in
// somebody's pipeline.
//
// So the table is read back and held to the flags runPlaygroundScenario defines, in
// both directions, and the status the chapter names for a run that missed its
// expectations is held to exitScenarioFailed.
const playgroundHandbook = "../../api/web/handbuch.html"

func TestTheHandbookDocumentsEveryPlaygroundFlag(t *testing.T) {
	defined := commandFlags(t, "playgroundrun.go", "runPlaygroundScenario")
	documented := documentedFlags(t, playgroundHandbook, "playground-flags")
	for name := range defined {
		if !documented[name] {
			t.Errorf("atlas playground defines --%s, but the handbook's flag table "+
				`(<table id="playground-flags">) does not list it`, name)
		}
	}
	for name := range documented {
		if !defined[name] {
			t.Errorf("the handbook's flag table lists --%s, which atlas playground does not define", name)
		}
	}
}

func TestTheHandbookNamesTheExitStatusOfAMissedScenario(t *testing.T) {
	page := readPage(t, playgroundHandbook)
	stated := regexp.MustCompile(`<code data-exit="failed">(\d+)</code>`).FindAllStringSubmatch(page, -1)
	// Once per language: a status stated in German only is half an instruction.
	if len(stated) < 2 {
		t.Fatalf("the handbook states the exit status of a missed scenario %d time(s); want it in both languages",
			len(stated))
	}
	for _, m := range stated {
		if m[1] != strconv.Itoa(exitScenarioFailed) {
			t.Errorf("the handbook says a missed expectation exits %s; the runner exits %d", m[1], exitScenarioFailed)
		}
	}
}

// commandFlags reads the flag names off a command's source — the first argument of
// every fs.String, fs.Bool, fs.Duration … call in fn, and the second of every fs.Var.
// Reading the source rather than running the function is deliberate: its flag set
// exits the process on -h, and a test that exits is a test that reports nothing.
func commandFlags(t *testing.T, file, fn string) map[string]bool {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	definers := map[string]int{"String": 0, "Bool": 0, "Duration": 0, "Int": 0, "Int64": 0,
		"Uint": 0, "Float64": 0, "Var": 1}
	flags := map[string]bool{}
	for _, decl := range parsed.Decls {
		f, ok := decl.(*ast.FuncDecl)
		if !ok || f.Name.Name != fn {
			continue
		}
		ast.Inspect(f.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			at, ok := definers[sel.Sel.Name]
			if !ok || len(call.Args) <= at {
				return true
			}
			if recv, ok := sel.X.(*ast.Ident); !ok || recv.Name != "fs" {
				return true
			}
			if lit, ok := call.Args[at].(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if name, err := strconv.Unquote(lit.Value); err == nil {
					flags[name] = true
				}
			}
			return true
		})
	}
	// Finding none means the flags moved out from under this reader, not that the
	// command has none; passing here would hold the table to nothing.
	if len(flags) == 0 {
		t.Fatalf("found no flags defined in %s — did they move to another function?", fn)
	}
	return flags
}

// documentedFlags reads the flags a page's table lists, one row each.
func documentedFlags(t *testing.T, page, tableID string) map[string]bool {
	t.Helper()
	content := readPage(t, page)
	i := strings.Index(content, `<table id="`+tableID+`">`)
	if i < 0 {
		t.Fatalf(`%s has no <table id="%s"> — the table this test guards is gone`, page, tableID)
	}
	table := content[i:]
	if end := strings.Index(table, "</table>"); end >= 0 {
		table = table[:end]
	}
	flags := map[string]bool{}
	for _, m := range regexp.MustCompile(`<tr><td><code>--([a-z-]+)</code></td>`).FindAllStringSubmatch(table, -1) {
		flags[m[1]] = true
	}
	return flags
}

func readPage(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}
