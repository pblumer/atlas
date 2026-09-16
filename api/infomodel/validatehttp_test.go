package infomodel

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Serving the verdict on a document nobody has saved
// (ADR-draft-the-verdict-is-served-not-duplicated).
//
// The canvas could only ever show the findings of the *last save*, so every edit that
// broke the model was silent until somebody pressed Save — and the refusal then named
// something they had stopped thinking about. These tests hold the route that closes
// that: it judges the document it is handed, stores nothing, and reads nothing.

// TestValidateJudgesTheDocumentItIsHanded is the whole route: a model that is fine
// comes back valid, and a model that is not comes back with the same findings a save
// would have refused it with.
func TestValidateJudgesTheDocumentItIsHanded(t *testing.T) {
	fx := newFixture(t)

	clean := requestJSON(t, fx.service.HandleValidate, http.MethodPost, "/api/v1/infomodel/validate",
		classesBody(), http.StatusOK)
	var ok ValidationResult
	decodeResponse(t, clean, &ok)
	if !ok.Valid || len(ok.Findings) != 0 {
		t.Fatalf("a well-formed document = %+v, want valid and silent", ok)
	}

	// The case that prompted this: a store naming a class the document does not
	// declare. It was refused on save and said nothing before it.
	broken := classesBody()
	broken["stores"] = []map[string]any{{
		"id": "st1", "name": "Orders", "class": "Auftrag", "mode": StoreModeRead,
	}}
	rec := requestJSON(t, fx.service.HandleValidate, http.MethodPost, "/api/v1/infomodel/validate",
		broken, http.StatusOK)
	var res ValidationResult
	decodeResponse(t, rec, &res)
	if res.Valid {
		t.Fatalf("a store naming no class was called valid: %+v", res)
	}
	if len(res.Findings) != 1 || res.Findings[0].Code != CodeStoreUnknownClass {
		t.Fatalf("findings = %+v, want one %s", res.Findings, CodeStoreUnknownClass)
	}
	// The finding carries what the panel marks the drawing with.
	if res.Findings[0].StoreID == "" {
		t.Error("the finding names no store, so nothing on the canvas could be marked with it")
	}
}

// TestValidateIsARefusalNotAnError: an invalid model is a 200 carrying findings, not a
// 400. The canvas asks this on every edit, and a document mid-edit is *expected* to be
// invalid — answering that with an error would make the normal case look like a fault.
func TestValidateIsARefusalNotAnError(t *testing.T) {
	fx := newFixture(t)
	body := map[string]any{"classes": []map[string]any{
		{"id": "c1", "name": "Order", "stereotype": StereotypeBusinessObject,
			"attributes": []map[string]any{{"name": "total", "type": "Nothing", "multiplicity": MultOne}}},
	}}
	rec := requestJSON(t, fx.service.HandleValidate, http.MethodPost, "/api/v1/infomodel/validate",
		body, http.StatusOK)
	var res ValidationResult
	decodeResponse(t, rec, &res)
	if res.Valid || len(res.Findings) == 0 {
		t.Fatalf("an unresolved attribute type = %+v, want findings", res)
	}
}

// TestValidateStoresNothing is the property the route rests on: it is asked on every
// keystroke, so it must not be a write. Nothing exists after it that did not before.
func TestValidateStoresNothing(t *testing.T) {
	fx := newFixture(t)
	before, err := fx.store.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	requestJSON(t, fx.service.HandleValidate, http.MethodPost, "/api/v1/infomodel/validate",
		classesBody(), http.StatusOK)
	after, err := fx.store.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(after) != len(before) {
		t.Errorf("validating stored something: %d models before, %d after", len(before), len(after))
	}
}

// TestValidateRefusesWhatItCannotRead — a body that is not a model at all is the one
// case that is a bad request rather than a finding.
func TestValidateRefusesWhatItCannotRead(t *testing.T) {
	fx := newFixture(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/infomodel/validate",
		strings.NewReader("not json at all"))
	req.Header.Set("Content-Type", "application/json")
	invoke(t, fx.service.HandleValidate, req, http.StatusBadRequest)
}
