package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/catalog"
)

// Where the orderer is asked, and what the two pages owe the field
// (ADR-0358).
//
// A product declares one Atlas form and the order line carries the answers. Three
// seams hold that together, and each is a place where one side can quietly stop
// matching the other: the wire name, the runtime that renders the form, and the
// list the product manager chooses the form from.

// TestThePortalAsksForTheFieldTheReleaseCarries.
//
// Derived from the marshalled item rather than from a tag somebody read once, so
// renaming the Go field, the JSON tag or the portal's reader fails here instead of
// leaving the basket silently asking nothing.
func TestThePortalAsksForTheFieldTheReleaseCarries(t *testing.T) {
	marker := "atlas-config-form-marker"
	raw, err := json.Marshal(catalog.Item{ConfigForm: marker})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	key := ""
	for k, v := range out {
		if v == marker {
			key = k
		}
	}
	if key == "" {
		t.Fatalf("no field in %s carries the marker; the fixture has gone stale", raw)
	}

	src := readWeb(t, "portal.js")
	if !strings.Contains(src, "it."+key) {
		t.Errorf("the release spells a product's form %q and portal.js never reads "+
			"it.%s, so a product that asks for a cost centre is ordered without one "+
			"and nobody is told", key, key)
	}
}

// TestTheBasketRendersTheFormWithAtlasOwnRuntime.
//
// Not a hand-rolled renderer. Which questions there are, which are required and
// what counts as valid are the form's own statements, and a second implementation
// of those rules would be wrong the first time somebody edits a form. The portal
// therefore uses the same runtime the Tasks app and the incident repair use.
func TestTheBasketRendersTheFormWithAtlasOwnRuntime(t *testing.T) {
	src := readWeb(t, "portal.js")
	if !strings.Contains(src, "./formviewer.js") {
		t.Error("portal.js does not load the shared form runtime. Rendering the fields " +
			"itself would be a second copy of every rule a form states about itself")
	}
	if !strings.Contains(src, "form.submit()") {
		t.Error("portal.js never asks the form whether it is valid, so an incomplete " +
			"form is sent and the refusal arrives after the order was attempted")
	}
	// And what is typed survives a redraw. The basket is rebuilt whenever anything
	// on it changes, a rendered form does not survive its container being replaced,
	// and a person who added a second product would find the first one's form empty.
	// The call site inside render(), and not the name: harvest() appears in its own
	// declaration and again in order(), so looking for the bare name stays green
	// after the one call that matters has gone — which is what breaking it showed.
	if !strings.Contains(src, "harvest();\n  paint(root,") {
		t.Error("render() does not capture what was typed before it replaces the page. " +
			"A rendered form goes with its container, so adding a second product would " +
			"empty the first one's form with no sign that anything was lost")
	}
}

// TestTheProductEditorOffersFormsThatExist.
//
// The same rule the process bindings follow, and for the same reason: a product
// whose form does not exist is an order somebody cannot get past, found by the
// person ordering rather than by the person who bound it. A select list cannot
// make that mistake; a text field can.
func TestTheProductEditorOffersFormsThatExist(t *testing.T) {
	src := readWeb(t, "catalog-admin.js")
	if !strings.Contains(src, `api("GET", "/api/v1/forms")`) {
		t.Error("the product editor does not read the form list, so the field is either " +
			"absent or free text — and free text binds a form nobody wrote")
	}
	if !strings.Contains(src, `name="configForm"`) {
		t.Fatal("the product editor has no configForm control; a product cannot be " +
			"given a form through the screen that exists to fill the catalogue")
	}
	if !strings.Contains(src, "configForm: f.get(\"configForm\")") {
		t.Error("the product editor renders the control and does not send it, so the " +
			"choice is lost on save")
	}
	// A product already bound to a form that has since been deleted must still say
	// so rather than silently reading as "none" and being saved that way.
	if !strings.Contains(src, "no such form") {
		t.Error("a product bound to a form that no longer exists shows as unbound, and " +
			"opening the editor and saving would quietly clear the binding")
	}
}
