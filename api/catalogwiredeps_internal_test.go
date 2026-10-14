package api

import (
	"regexp"
	"strings"
	"testing"
)

// What the catalogue screen hands its event handlers.
//
// api/web/catalog-admin.js splits in two: viewCatalogDetail draws the page and
// takes everything the page needs from the shell, and wire() attaches the
// handlers and is given a bag of its own. A name wire() uses but was not given
// is a ReferenceError — and not at load, which review would catch, but on the
// click that reaches the line.
//
// The product save is where that lands hardest, and it did: the picture step was
// handed apiBytes, which wire() was never given, so pressing Save wrote the
// record and then threw "apiBytes is not defined". savePicture catches its own
// failures, but the bag it is called with is evaluated by the caller, before that
// try block exists — so the throw escaped to the outer catch and took the step
// that tells the catalogue to offer the product. The product was stored and
// offered by nothing, which the code right there calls "the likeliest way to lose
// work here".
//
// Two guards, because it took two mistakes: a name that was not handed over, and
// an ordering in which the optional step stood in front of the one that matters.
//
// This is the second time the same shape reached a user: `list is not a function`
// was a DOM element shadowing a helper inside this same function. Both are a name
// resolving to the wrong thing, or to nothing, deep inside a handler. So the
// guard is written for the class and not for the instance.

// TestWireIsGivenEveryDependencyItUses.
//
// Whatever wire()'s body reaches for, its parameter list has to declare — and its
// caller has to pass. Three places, and a name is only usable if all three agree.
func TestWireIsGivenEveryDependencyItUses(t *testing.T) {
	src := readWeb(t, "catalog-admin.js")
	body := webRegion(t, src, "function wire({", "\n}")

	// The destructuring pattern wire() opens with.
	params := regexp.MustCompile(`function wire\(\{([^}]*)\}`).FindStringSubmatch(src)
	if params == nil {
		t.Fatal("wire() no longer opens with a destructured bag; this guard reads that pattern")
	}
	declared := params[1]

	// And what the caller hands over.
	callsite := regexp.MustCompile(`\n  wire\(\{([^}]*)\}`).FindStringSubmatch(src)
	if callsite == nil {
		t.Fatal("viewCatalogDetail no longer calls wire() with a destructured bag")
	}
	passed := callsite[1]

	// What the shell owns and wire() cannot invent.
	for _, name := range []string{"api", "apiBytes", "toast", "view"} {
		// Used means called or handed on, not merely named in a comment.
		used := strings.Contains(body, name+"(") || strings.Contains(body, name+",") ||
			strings.Contains(body, name+" }")
		if !used {
			continue
		}
		if !strings.Contains(declared, name) {
			t.Errorf("wire() uses %q and is not given it, so the handler that reaches "+
				"that line throws a ReferenceError on the click rather than at load",
				name)
		}
		if !strings.Contains(passed, name) {
			t.Errorf("wire() is called without %q, so the name is declared and "+
				"undefined — which fails the same way and reads the same to whoever "+
				"pressed the button", name)
		}
	}
}

// TestSavingAProductOffersItBeforeAnythingElseCanThrow.
//
// The ordering is what turns a failed picture into a lost product. Write the
// record, then tell the catalogue to offer it, and only then upload the picture:
// anything that throws after that point costs a picture, which is one upload and
// is visibly missing. Anything that throws before the offering costs a product
// that is stored and offered by nothing — invisible on every screen, and reported
// in words about something else.
//
// Guarded as an ordering rather than as a fix to one name: the next dependency
// added to the picture step would reintroduce it exactly.
func TestSavingAProductOffersItBeforeAnythingElseCanThrow(t *testing.T) {
	body := webRegion(t, readWeb(t, "catalog-admin.js"),
		`editor.querySelector(".product-form").addEventListener("submit"`, "\n    });")
	save := strings.Index(body, `api("POST", "/api/v1/catalog-products"`)
	picture := strings.Index(body, "savePicture(")
	offer := strings.Index(body, "patchList({ items:")
	if save < 0 || picture < 0 || offer < 0 {
		t.Fatal("the product save no longer writes the record, the picture and the " +
			"catalogue's offering; this guard reads those three steps")
	}
	if !(save < offer) {
		t.Error("the catalogue is told to offer a product before the product is " +
			"written, so a refused product leaves the catalogue offering nothing")
	}
	if !(offer < picture) {
		t.Error("the picture is uploaded before the catalogue is told to offer the " +
			"product, so whatever the picture step throws leaves the product stored " +
			"and offered by nothing — reported in words about the picture")
	}
}
