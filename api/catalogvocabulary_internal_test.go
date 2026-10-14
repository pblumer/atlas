package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The catalogue screen's vocabularies against the server's own.
//
// catalog-admin.js spells the states, the approval kinds and the edge kinds itself,
// and says why: they are part of the screen's shape, and a kind the server does not
// know would be refused on save. The comment above them also said they were "pinned
// by a test against the Go source so the two cannot drift".
//
// **No such test existed.** That is not a missing nicety, it is why the drift went
// unnoticed: `EdgeExcludes` was added to the record for separation of duties
// (ADR-0342), frozen into releases in both directions by Publish, read by the
// conflicts surface — and never reached the Console, which offered three kinds of
// four. An incompatibility could not be declared there, and one that existed
// appeared in no table and could not be removed, because the only remove button is
// on a row that is drawn.
//
// So this is that test, derived from the constants rather than from a list somebody
// keeps beside them: adding a value to one of the three types makes it fail until
// the screen carries it. A list written out here in Go would have exactly the
// weakness that let this happen.

// TestTheConsoleSpellsEveryVocabularyTheServerDoes.
func TestTheConsoleSpellsEveryVocabularyTheServerDoes(t *testing.T) {
	src := readWeb(t, "catalog-admin.js")
	for _, v := range []struct{ goType, jsList, what string }{
		{"State", "STATES", "whether a product may be published"},
		{"ApprovalKind", "APPROVAL_KINDS", "who decides an order line"},
		{"EdgeKind", "EDGE_KINDS", "how two products relate"},
	} {
		server := constantsOfType(t, v.goType)
		screen := idsOfJSList(t, src, v.jsList)
		if diff := missing(server, screen); len(diff) > 0 {
			t.Errorf("the server knows %s values the Console does not spell: %v.\n"+
				"%s is %s — a value only one side knows is a value nobody can author "+
				"on the screen, and one that exists shows up nowhere.",
				v.goType, diff, v.jsList, v.what)
		}
		if diff := missing(screen, server); len(diff) > 0 {
			t.Errorf("the Console spells %s values the server does not know: %v. "+
				"Choosing one would be refused on save, with the screen having offered it.",
				v.goType, diff)
		}
	}
}

// TestTheConsoleCanAuthorEveryEdgeKindAndShowIt.
//
// Spelling a kind is not offering it. The vocabulary is used to *read* an edge;
// what a person can write comes from PAIRWISE_KINDS and the assemble kit, and what
// they can see comes from the sections edgeTable draws. A kind in none of them is
// exactly the state EdgeExcludes was in — known, stored, enforced, and unreachable.
func TestTheConsoleCanAuthorEveryEdgeKindAndShowIt(t *testing.T) {
	src := readWeb(t, "catalog-admin.js")

	// What the kit owns, derived where the file derives it.
	structure := idsOfJSList(t, src, "STRUCTURE_CHOICES")
	kit := map[string]bool{}
	for _, id := range structure {
		if id != "none" {
			kit[id] = true
		}
	}
	if len(kit) == 0 {
		t.Fatal("the assemble kit offers no structural answer; this guard has lost its subject")
	}

	// And what the pairwise form writes: everything else, which is how the file
	// states it. Read as the rule rather than as a list, so the two cannot disagree.
	pairwise := webRegion(t, src, "const PAIRWISE_KINDS =", "\n")
	if !strings.Contains(pairwise, "!STRUCTURE_IDS.includes(k.id)") {
		t.Error("the pairwise form no longer takes every kind the kit does not own, so " +
			"a kind added to the vocabulary can end up authorable by nothing")
	}

	// Every kind is shown somewhere. edgeTable draws one section per question, and a
	// kind drawn in none of them cannot be removed either — the remove button only
	// exists on a row.
	table := webRegion(t, src, "function edgeTable(", "\n}")
	for _, kind := range constantsOfType(t, "EdgeKind") {
		if kit[kind] {
			continue // structure is drawn by the kit, product by product
		}
		if !strings.Contains(table, `"`+kind+`"`) {
			t.Errorf("edgeTable draws no section for %q, so an edge of that kind is "+
				"invisible in the Console and cannot be removed there", kind)
		}
	}
}

// constantsOfType returns the values of every constant declared with that type in
// the catalogue package — the server's own vocabulary, read off the declarations
// rather than copied.
func constantsOfType(t *testing.T, typeName string) []string {
	t.Helper()
	path := filepath.Join("catalog", "catalog.go")
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var out []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			ident, ok := vs.Type.(*ast.Ident)
			if !ok || ident.Name != typeName {
				continue
			}
			for _, v := range vs.Values {
				lit, ok := v.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				value, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatalf("unquote %s: %v", lit.Value, err)
				}
				out = append(out, value)
			}
		}
	}
	if len(out) == 0 {
		t.Fatalf("no constants of type %s in %s; this guard has lost its subject",
			typeName, path)
	}
	sort.Strings(out)
	return out
}

// idsOfJSList returns the `id:` values of one of the screen's vocabulary arrays.
func idsOfJSList(t *testing.T, src, name string) []string {
	t.Helper()
	body := webRegion(t, src, "const "+name+" = [", "\n];")
	ids := regexp.MustCompile(`id:\s*"([^"]+)"`).FindAllStringSubmatch(body, -1)
	if len(ids) == 0 {
		t.Fatalf("%s carries no entries; this guard has lost its subject", name)
	}
	var out []string
	for _, m := range ids {
		out = append(out, m[1])
	}
	sort.Strings(out)
	return out
}

// missing returns the values of a that b does not carry.
func missing(a, b []string) []string {
	have := make(map[string]bool, len(b))
	for _, v := range b {
		have[v] = true
	}
	var out []string
	for _, v := range a {
		if !have[v] {
			out = append(out, v)
		}
	}
	return out
}
