package infomodel

import (
	"bytes"
	"net/http"
	"testing"
)

// TestRenamingAnImportedModelKeepsItsContentAndReadsAsLists: an edit that sends only
// a name and documentation changes those two and nothing else — the classes it did
// not send stay — and a model whose import carried no associations or stores comes
// back with empty lists rather than null, which is what the canvas iterates.
func TestRenamingAnImportedModelKeepsItsContentAndReadsAsLists(t *testing.T) {
	fx := newFixture(t)
	doc := `<uml:Model xmlns:xmi="http://www.omg.org/spec/XMI/20131001" xmlns:uml="http://www.omg.org/spec/UML/20131001" name="Claims">
	  <packagedElement xmi:type="uml:Class" xmi:id="_c" name="Claim"/>
	</uml:Model>`
	created := requestJSON(t, fx.service.HandleImport, http.MethodPost, "/api/v1/infomodel/import",
		importBody("app-1", doc), http.StatusCreated)
	var imported ImportResponse
	decodeResponse(t, created, &imported)
	if imported.Model == nil {
		t.Fatalf("import stored nothing: %s", created.Body)
	}

	rec := fx.putModel(t, imported.Model.ID, map[string]any{
		"name": "  Claims handling ", "documentation": "  What an insurer settles.  ", "revision": 1,
	}, http.StatusOK)
	var got modelResponse
	decodeResponse(t, rec, &got)
	if got.Name != "Claims handling" || got.Documentation != "What an insurer settles." {
		t.Errorf("name/documentation = %q / %q, want both trimmed", got.Name, got.Documentation)
	}
	if len(got.Classes) != 1 || got.Classes[0].Name != "Claim" {
		t.Errorf("classes = %+v, want the imported class untouched", got.Classes)
	}
	for _, list := range []string{`"associations":[]`, `"stores":[]`} {
		if !bytes.Contains(rec.Body.Bytes(), []byte(list)) {
			t.Errorf("response does not carry %s: %s", list, rec.Body)
		}
	}
}

// TestADataStoreKeepsTheIDItWasGiven: a store saved again under the id the server
// minted for it is the same store, so a process bound to it stays bound.
func TestADataStoreKeepsTheIDItWasGiven(t *testing.T) {
	fx := newFixture(t)
	id := fx.create(t, "app-1", "Sales").ID
	m := storeModel()
	body := map[string]any{
		"classes": m.Classes, "associations": m.Associations,
		"stores": []DataStore{{ID: "new-store", Name: "Orders", Class: "Order", Worker: "clio-main", Mode: StoreModeRead}},
	}
	var first modelResponse
	decodeResponse(t, fx.putModel(t, id, body, http.StatusOK), &first)
	if len(first.Stores) != 1 {
		t.Fatalf("stores = %+v, want one", first.Stores)
	}

	body["classes"], body["associations"], body["stores"] = first.Classes, first.Associations, first.Stores
	var second modelResponse
	decodeResponse(t, fx.putModel(t, id, body, http.StatusOK), &second)
	if len(second.Stores) != 1 || second.Stores[0].ID != first.Stores[0].ID {
		t.Fatalf("store id %q became %+v on the second save, want it kept", first.Stores[0].ID, second.Stores)
	}
}
