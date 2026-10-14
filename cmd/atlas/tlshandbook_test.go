package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The handbook's TLS chapter (api/web/handbuch.html, #tls) tells an operator which
// flag to set on which command, and under which environment variable a service
// wrapper can set it instead: a table of every --tls-* flag. That table is a copy
// of what this directory defines, and a copy drifts — a flag documented on a
// command that does not have it is an instruction that fails on the first start,
// and one renamed here but not there sends somebody to a variable nothing reads.
//
// So the table is read back and held to the flags the three commands define, in
// both directions: the flag, its environment variable, and the commands it is on.
const tlsHandbook = "../../api/web/handbuch.html"

// tlsFlagCommands is where TLS flags are defined, by the command the handbook
// names them under.
var tlsFlagCommands = map[string]string{
	"runServe":  "serve",
	"runWorker": "worker",
	"runMCPOn":  "mcp",
}

// tlsFlag is one --tls-* flag: the environment variable that is its default, and
// the commands that define it, sorted.
type tlsFlag struct {
	env      string
	commands []string
}

func TestTheHandbookDocumentsEveryTLSFlag(t *testing.T) {
	defined := definedTLSFlags(t)
	documented := documentedTLSFlags(t)
	for name, def := range defined {
		doc, ok := documented[name]
		if !ok {
			t.Errorf("atlas %s defines --%s, but the handbook's TLS table "+
				`(<table id="tls-flags">) does not list it`, strings.Join(def.commands, ", "), name)
			continue
		}
		if doc.env != def.env {
			t.Errorf("the handbook says --%s is read from %s; the flag's default is os.Getenv(%q)",
				name, doc.env, def.env)
		}
		if strings.Join(doc.commands, ",") != strings.Join(def.commands, ",") {
			t.Errorf("the handbook lists --%s on %v; it is defined on %v", name, doc.commands, def.commands)
		}
	}
	for name := range documented {
		if _, ok := defined[name]; !ok {
			t.Errorf("the handbook's TLS table lists --%s, which no atlas command defines", name)
		}
	}
}

// definedTLSFlags reads the --tls-* flags off the source of the commands that
// define them — the first argument of every fs.String, fs.Bool … call whose name
// starts with "tls-", and the os.Getenv its default is read from. Reading the
// source rather than running the commands is deliberate: their flag sets exit the
// process on -h, and runServe starts a server.
func definedTLSFlags(t *testing.T) map[string]tlsFlag {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}
	definers := map[string]bool{"String": true, "Bool": true, "Duration": true, "Int": true}
	flags := map[string]tlsFlag{}
	seen := map[string]bool{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		command, ok := tlsFlagCommands[fn.Name.Name]
		if !ok {
			continue
		}
		seen[fn.Name.Name] = true
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) < 2 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !definers[sel.Sel.Name] {
				return true
			}
			if recv, ok := sel.X.(*ast.Ident); !ok || recv.Name != "fs" {
				return true
			}
			name, ok := stringLiteral(call.Args[0])
			if !ok || !strings.HasPrefix(name, "tls-") {
				return true
			}
			f := flags[name]
			if env, ok := getenvArg(call.Args[1]); ok {
				f.env = env
			}
			f.commands = append(f.commands, command)
			sort.Strings(f.commands)
			flags[name] = f
			return true
		})
	}
	for fn := range tlsFlagCommands {
		if !seen[fn] {
			t.Fatalf("main.go has no func %s — the TLS flags this test reads moved", fn)
		}
	}
	// Finding none means the flags moved out from under this reader, not that
	// atlas has none; passing here would hold the table to nothing.
	if len(flags) == 0 {
		t.Fatal("found no --tls-* flags in runServe, runWorker or runMCPOn — did they move to another function?")
	}
	return flags
}

// getenvArg returns NAME for an expression os.Getenv("NAME").
func getenvArg(e ast.Expr) (string, bool) {
	call, ok := e.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return "", false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Getenv" {
		return "", false
	}
	if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "os" {
		return "", false
	}
	return stringLiteral(call.Args[0])
}

func stringLiteral(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	return s, err == nil
}

// documentedTLSFlags reads the rows of <table id="tls-flags">: the flag, its
// environment variable, and a cell naming the commands, one <code> each.
func documentedTLSFlags(t *testing.T) map[string]tlsFlag {
	t.Helper()
	raw, err := os.ReadFile(tlsHandbook)
	if err != nil {
		t.Fatalf("read %s: %v", tlsHandbook, err)
	}
	page := string(raw)
	i := strings.Index(page, `<table id="tls-flags">`)
	if i < 0 {
		t.Fatalf(`%s has no <table id="tls-flags"> — the table this test guards is gone`, tlsHandbook)
	}
	table := page[i:]
	if end := strings.Index(table, "</table>"); end >= 0 {
		table = table[:end]
	}
	row := regexp.MustCompile(`<tr><td><code>--([a-z-]+)</code></td><td><code>([A-Z_]+)</code></td><td>(.*?)</td>`)
	command := regexp.MustCompile(`<code>([a-z]+)</code>`)
	flags := map[string]tlsFlag{}
	for _, m := range row.FindAllStringSubmatch(table, -1) {
		f := tlsFlag{env: m[2]}
		for _, c := range command.FindAllStringSubmatch(m[3], -1) {
			f.commands = append(f.commands, c[1])
		}
		sort.Strings(f.commands)
		flags[m[1]] = f
	}
	if len(flags) == 0 {
		t.Fatal(`found no rows in <table id="tls-flags"> — the row format this test reads changed`)
	}
	return flags
}
