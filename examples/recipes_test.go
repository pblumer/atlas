package examples

import (
	"html"
	"os"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
)

// The handbook's recipe chapter (api/web/handbuch.html, "Rezepte") ships 28 small
// BPMN models as escaped XML inside the page, each with a "▶ Ausprobieren" button
// that deploys and starts exactly that XML against the reader's own server. They
// are the most-copied models Atlas has: a reader who wants a boundary timer opens
// the recipe, reads the XML and pastes it into their own process.
//
// Nothing compiled them. They live only in the page, so shippedModels() — which
// walks .bpmn files on disk — never saw them, and neither did any other test. A
// compiler rule added afterwards could therefore turn a recipe into a model the
// deploy gate refuses, with the page happily teaching it and the button failing
// in the reader's face. That is not hypothetical: variable.dotted-target landed
// after the ioMapping recipe was written and rejected it.
//
// These tests give the recipes the same floor every shipped model has: they must
// compile, and they must contain the process their button starts.

const recipeCardOpen = `<div class="recipe" id="`

// recipe is one card: the anchor a reader links to, the process the run button
// starts, and the model it deploys.
type recipe struct {
	id   string // the card's element id, e.g. "rz-iomapping"
	proc string // data-proc: the BPMN process id the run button starts
	xml  string // the model, unescaped back from the <pre><code> block
}

// TestHandbookRecipesCompile deploys every recipe through the real compiler, the
// same ParseAll the deployment endpoint uses — including its validation gate, which
// is the half that a page-only model can silently fall out of.
func TestHandbookRecipesCompile(t *testing.T) {
	for _, r := range handbookRecipes(t) {
		t.Run(r.id, func(t *testing.T) {
			if _, err := compiler.ParseAll(1, 1, strings.NewReader(r.xml)); err != nil {
				t.Errorf("the recipe does not deploy — its ▶ button fails for every reader who presses it: %v", err)
			}
		})
	}
}

// TestHandbookRecipesStartTheProcessTheyName checks each card's data-proc against
// the model it carries. runRecipe() looks the started definition up by that id and
// falls back to the first one deployed, so a stale data-proc does not fail loudly —
// it quietly starts a different process than the card is about.
func TestHandbookRecipesStartTheProcessTheyName(t *testing.T) {
	for _, r := range handbookRecipes(t) {
		t.Run(r.id, func(t *testing.T) {
			deployables, err := compiler.ParseAll(1, 1, strings.NewReader(r.xml))
			if err != nil {
				t.Skipf("does not compile — TestHandbookRecipesCompile reports it: %v", err)
			}
			var ids []string
			for _, d := range deployables {
				if d.Process.ProcessId() == r.proc {
					return
				}
				ids = append(ids, d.Process.ProcessId())
			}
			t.Errorf("the card starts data-proc=%q, but its model deploys %v — the button starts "+
				"whichever process comes first instead of the one the card explains", r.proc, ids)
		})
	}
}

// handbookRecipes reads the cards out of the page. It is deliberately literal about
// the markup it expects: a card that stops matching is a card that stopped being
// tested, so the parse fails loudly rather than returning a shorter list.
func handbookRecipes(t *testing.T) []recipe {
	t.Helper()
	page, err := os.ReadFile(handbookPath)
	if err != nil {
		t.Fatalf("read %s: %v", handbookPath, err)
	}
	// Bounded to the recipe chapter on purpose. The workshop chapter styles its two
	// diagram cards with the same class, but they carry no XML of their own — they
	// render from the embedded application block, which examples/handbook_test.go
	// generates from the files here and guards separately.
	rest := after(t, string(page), `<section id="rezepte">`, "the page")
	rest = until(t, rest, "</section>", "the rezepte section")

	var out []recipe
	for {
		i := strings.Index(rest, recipeCardOpen)
		if i < 0 {
			break
		}
		rest = rest[i+len(recipeCardOpen):]
		id := until(t, rest, `"`, "a recipe card's id")

		// The card ends where the next one begins; everything found past that
		// boundary belongs to the following recipe, not this one.
		card := rest
		if next := strings.Index(card, recipeCardOpen); next >= 0 {
			card = card[:next]
		}

		proc := after(t, card, `data-proc="`, id)
		proc = until(t, proc, `"`, "the data-proc of "+id)

		body := after(t, card, "<pre><code>", id)
		out = append(out, recipe{
			id:   id,
			proc: proc,
			xml:  html.UnescapeString(until(t, body, "</code></pre>", "the XML block of "+id)),
		})
	}
	if len(out) == 0 {
		t.Fatalf("%s has no recipe cards — the markup changed, so nothing here is testing anything", handbookPath)
	}
	return out
}

func after(t *testing.T, s, marker, what string) string {
	t.Helper()
	i := strings.Index(s, marker)
	if i < 0 {
		t.Fatalf("recipe %s has no %s — every card needs one", what, marker)
	}
	return s[i+len(marker):]
}

func until(t *testing.T, s, marker, what string) string {
	t.Helper()
	i := strings.Index(s, marker)
	if i < 0 {
		t.Fatalf("%s is not terminated by %s", what, marker)
	}
	return s[:i]
}
