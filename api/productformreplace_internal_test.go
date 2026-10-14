package api

import (
	"strings"
	"testing"
)

// Saving a product replaces it: the record that arrives is the record that is
// stored (ADR-0376). That is the right shape for a
// form which renders every field and posts every field back — and the catalogue's
// product form does not render every field.
//
// It had no control for variants, for the orderable window, for the search
// keywords, for the groups eligible to receive the product, or for the ceiling on
// how long the right may last. It built its body out of the controls it did have,
// so saving a price cleared all five, and moved the creation date to today. Nothing
// said so: the save succeeded and the page reloaded looking correct, because the
// fields it dropped are the ones it never shows.
//
// Four of the five are rendered now and one is not — the orderable window, which
// nothing enforces and which a control would therefore misrepresent. So the rule
// this file holds is unchanged and is not a transitional one: the form carries what
// it does not render, which means starting from the stored record rather than from
// an empty object. Every field added to a product from here on arrives on that
// side of the rule first.

// productFormBody returns the body of productBody, which is where the record a
// save posts is assembled. It reads that and not whatever else the file says about
// products.
//
// It used to read wireProductForm, where this was a block inside an event listener.
// The assembly moved out to be provable in a browser (e2e/catalog-product-body)
// rather than only by reading it here; what has to hold of it did not change.
func productFormBody(t *testing.T) string {
	t.Helper()
	return webRegion(t, readWeb(t, "catalog-admin.js"), "export function productBody(", "\n}")
}

// productFormHandler returns the body of wireProductForm: the save's own
// surroundings — the toasts, the picture, the conflict.
func productFormHandler(t *testing.T) string {
	t.Helper()
	src := readWeb(t, "catalog-admin.js")
	start := strings.Index(src, "function wireProductForm(")
	if start < 0 {
		t.Fatal("catalog-admin.js has no wireProductForm(); if the form moved, this " +
			"test now passes vacuously and says so instead")
	}
	end := strings.Index(src[start:], "\n  }\n")
	if end < 0 {
		t.Fatal("wireProductForm() has no recognisable end; the extraction above is stale")
	}
	return src[start : start+end]
}

// TestTheProductFormKeepsTheFieldsItDoesNotRender is the defect itself: the body
// must be seeded from the product being edited, so a field with no control on the
// form survives a save.
func TestTheProductFormKeepsTheFieldsItDoesNotRender(t *testing.T) {
	// The stored product has to reach the assembly at all: it is the only record the
	// page holds that carries a field the form has no control for.
	if h := productFormHandler(t); !strings.Contains(h, "byID[pid]") {
		t.Error("the submit path does not hand the stored product to the body, so " +
			"every field the form does not render — the orderable window today — is " +
			"cleared by a save")
	}

	b := productFormBody(t)
	// And the seed has to be spread *under* the form's own fields, or the form would
	// not be able to change anything.
	seed := strings.Index(b, "...was")
	id := strings.Index(b, "id: productID")
	if seed < 0 || id < 0 || seed > id {
		t.Error("the stored product is not spread underneath the form's own fields, " +
			"so either nothing is carried over or the form cannot change what it renders")
	}
}

// TestTheProductFormKeepsTextsInLanguagesItIsNotShowing: the same defect one level
// down, and the one that loses a translation. The form renders a text box per
// language *the catalogue* declares, but a product is shared between catalogues
// and may carry a text for a language this one does not offer. Rebuilding texts
// from the boxes alone drops it.
func TestTheProductFormKeepsTextsInLanguagesItIsNotShowing(t *testing.T) {
	h := productFormBody(t)
	if !strings.Contains(h, "was.texts") {
		t.Error("the product form rebuilds texts from its own boxes, so a text in a " +
			"language this catalogue does not declare is dropped when somebody else's " +
			"catalogue saves the product")
	}
	// Emptying a box that *is* rendered still has to clear that text, or a text
	// could never be removed.
	if !strings.Contains(h, "delete texts[") {
		t.Error("clearing a rendered text box does not remove the text, so a text can " +
			"be added and never taken away")
	}
}

// TestTheProductFormReportsAConcurrentEditToAPerson: carrying the revision is what
// turns a silent overwrite into a refusal, and the refusal is written for an API
// caller — it asks for the revision that was read. A person has no revision to
// state; they have a page to reload, and the form has to say that instead.
func TestTheProductFormReportsAConcurrentEditToAPerson(t *testing.T) {
	h := productFormHandler(t)
	if !strings.Contains(h, "409") {
		t.Error("the product form does not recognise a conflict, so a concurrent edit " +
			"is reported with a message asking a person for a revision number")
	}
}
