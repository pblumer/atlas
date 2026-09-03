package infomodel

import (
	"net/http"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/token"
)

func importBody(app, document string) map[string]any {
	return map[string]any{"applicationId": app, "document": document}
}

// TestImportStoresAModelAndItsAccount walks the endpoint the Data area's Import
// button drives: a UML tool's XMI becomes a model of this application's, and the
// answer says what the import did to it.
func TestImportStoresAModelAndItsAccount(t *testing.T) {
	fx := newFixture(t)
	rec := requestJSON(t, fx.service.HandleImport, http.MethodPost, "/api/v1/infomodel/import",
		importBody("app-1", salesXMI), http.StatusCreated)

	var got ImportResponse
	decodeResponse(t, rec, &got)
	if got.Format != ImportFormatXMI {
		t.Errorf("format = %q, want xmi", got.Format)
	}
	if got.Model == nil || got.Model.Classes != 6 {
		t.Fatalf("stored = %#v, want the six classes the document models", got.Model)
	}
	if got.Model.Name != "Sales" {
		t.Errorf("name = %q, want the document's own", got.Model.Name)
	}
	if !got.Validation.Valid {
		t.Errorf("an imported model arrived with findings: %v", got.Validation.Findings)
	}
	if len(got.Notes) == 0 {
		t.Error("a foreign document imported without a single note; the account is the point")
	}

	stored, exists, err := fx.store.Get(got.Model.ID)
	if err != nil || !exists {
		t.Fatalf("Get(%q) = %v, %v", got.Model.ID, exists, err)
	}
	if stored.Revision != 1 || stored.ApplicationID != "app-1" {
		t.Errorf("stored = %#v, want revision 1 in app-1", stored)
	}
	// Every id on disk has to be one this server issued, whatever the document called
	// its elements — the same rule the canvas's local handles go through.
	for _, c := range stored.Classes {
		if !token.IsHex(c.ID) {
			t.Errorf("class %s kept the document's id %q", c.Name, c.ID)
		}
	}
	for _, a := range stored.Associations {
		if !token.IsHex(a.ID) {
			t.Errorf("association %q kept the document's id", a.ID)
		}
		if _, ok := stored.ClassByID(a.From.ClassID); !ok {
			t.Errorf("association %q points at a class that is not in the model", a.ID)
		}
	}
}

func TestImportDryRunStoresNothing(t *testing.T) {
	fx := newFixture(t)
	body := importBody("app-1", salesXMI)
	body["dryRun"] = true
	body["name"] = "Sales vocabulary"
	rec := requestJSON(t, fx.service.HandleImport, http.MethodPost, "/api/v1/infomodel/import",
		body, http.StatusOK)

	var got ImportResponse
	decodeResponse(t, rec, &got)
	if got.Model != nil {
		t.Errorf("a dry run reported a stored model: %#v", got.Model)
	}
	if got.Preview == nil || len(got.Preview.Classes) != 6 {
		t.Fatalf("preview = %#v, want the six classes it would create", got.Preview)
	}
	if got.Preview.Name != "Sales vocabulary" {
		t.Errorf("name = %q, want the requested override", got.Preview.Name)
	}
	models, err := fx.store.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(models) != 0 {
		t.Errorf("a dry run stored %d models", len(models))
	}
}

func TestImportJSONIntoAnotherApplication(t *testing.T) {
	fx := newFixture(t)
	exported := `{"name":"Sales","classes":[
		{"id":"c1","name":"Order","stereotype":"businessObject","identity":["id"],
		 "attributes":[{"name":"id","type":"string","multiplicity":"1"}]}]}`
	rec := requestJSON(t, fx.service.HandleImport, http.MethodPost, "/api/v1/infomodel/import",
		importBody("app-1", exported), http.StatusCreated)

	var got ImportResponse
	decodeResponse(t, rec, &got)
	if got.Format != ImportFormatJSON || got.Model == nil || got.Model.Classes != 1 {
		t.Fatalf("got = %#v", got)
	}
	stored, _, _ := fx.store.Get(got.Model.ID)
	order, ok := stored.ClassByName("Order")
	if !ok || len(order.Identity) != 1 {
		t.Fatalf("Order = %#v, want its business key", order)
	}
}

func TestImportRefusals(t *testing.T) {
	cases := []struct {
		name   string
		body   map[string]any
		status int
	}{
		{"no application", map[string]any{"document": salesXMI}, http.StatusBadRequest},
		{"no document", map[string]any{"applicationId": "app-1"}, http.StatusBadRequest},
		{"blank document", importBody("app-1", "   "), http.StatusBadRequest},
		{"unreadable document", importBody("app-1", "Order: id, total"), http.StatusBadRequest},
		{"empty model", importBody("app-1", `{"classes":[]}`), http.StatusBadRequest},
		{"unknown application", importBody("nope", salesXMI), http.StatusBadRequest},
		{"application not visible", importBody("hidden", salesXMI), http.StatusNotFound},
		{"read-only access", importBody("viewer", salesXMI), http.StatusForbidden},
		{"protected application", importBody("protected", salesXMI), http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := newFixture(t)
			requestJSON(t, fx.service.HandleImport, http.MethodPost, "/api/v1/infomodel/import", tc.body, tc.status)
			models, err := fx.store.LoadAll()
			if err != nil {
				t.Fatalf("LoadAll: %v", err)
			}
			if len(models) != 0 {
				t.Errorf("a refused import stored %d models", len(models))
			}
		})
	}
}

// TestImportReportsABrokenIDGenerator covers the branch where the server cannot mint
// an id at all: the import must fail loudly rather than store elements the canvas
// could never address.
func TestImportReportsABrokenIDGenerator(t *testing.T) {
	fx := newFixture(t)
	fx.idErr = errIDCollision
	requestJSON(t, fx.service.HandleImport, http.MethodPost, "/api/v1/infomodel/import",
		importBody("app-1", salesXMI), http.StatusInternalServerError)
}

func TestImportReportsABrokenAccessResolver(t *testing.T) {
	fx := newFixture(t)
	fx.accessErr = errIDCollision
	requestJSON(t, fx.service.HandleImport, http.MethodPost, "/api/v1/infomodel/import",
		importBody("app-1", salesXMI), http.StatusInternalServerError)
}

func TestImportRefusesAnUnreadableBody(t *testing.T) {
	fx := newFixture(t)
	request(t, fx.service.HandleImport, http.MethodPost, "/api/v1/infomodel/import",
		strings.NewReader("{not json"), http.StatusBadRequest)
}

// TestImportOverridesTheDocumentsOwnNaming: a UML tool names a model after the file
// it lives in as often as after the business, so both fields may be overridden.
func TestImportOverridesTheDocumentsOwnNaming(t *testing.T) {
	fx := newFixture(t)
	body := importBody("app-1", salesXMI)
	body["name"] = "Order vocabulary"
	body["documentation"] = "Imported from the architecture team's UML model."
	rec := requestJSON(t, fx.service.HandleImport, http.MethodPost, "/api/v1/infomodel/import",
		body, http.StatusCreated)
	var got ImportResponse
	decodeResponse(t, rec, &got)
	stored, _, _ := fx.store.Get(got.Model.ID)
	if stored.Name != "Order vocabulary" || stored.Documentation == "" {
		t.Errorf("stored = %q / %q, want the requested naming", stored.Name, stored.Documentation)
	}
}

// TestImportNamesAnUnnamedDocument: XMI from a tool that never named its model would
// otherwise arrive as a library row with no title.
func TestImportNamesAnUnnamedDocument(t *testing.T) {
	fx := newFixture(t)
	doc := `<uml:Model xmlns:xmi="http://www.omg.org/spec/XMI/20131001" xmlns:uml="http://www.omg.org/spec/UML/20131001">
	  <packagedElement xmi:type="uml:Class" xmi:id="_c" name="Claim"/>
	</uml:Model>`
	rec := requestJSON(t, fx.service.HandleImport, http.MethodPost, "/api/v1/infomodel/import",
		importBody("app-1", doc), http.StatusCreated)
	var got ImportResponse
	decodeResponse(t, rec, &got)
	if got.Model == nil || got.Model.Name != "Imported model" {
		t.Errorf("name = %#v, want a stated fallback", got.Model)
	}
}

// TestImportDryRunReportsABrokenIDGenerator: a preview mints ids too, so that what it
// shows is what would be stored. If the server cannot mint one, saying so beats
// showing a preview whose elements the canvas could not address.
func TestImportDryRunReportsABrokenIDGenerator(t *testing.T) {
	fx := newFixture(t)
	fx.idErr = errIDCollision
	body := importBody("app-1", salesXMI)
	body["dryRun"] = true
	requestJSON(t, fx.service.HandleImport, http.MethodPost, "/api/v1/infomodel/import",
		body, http.StatusInternalServerError)
}
