package mcp_test

import (
	"net/http"
	"testing"
)

// TestImportInformationModelForwardsItsOptions: the optional fields override what the
// document carries, so each must reach the server when given and stay absent when not
// — an empty name would otherwise replace the document's own. dryRun is forwarded
// only as true: false is the server's default and saying it changes nothing.
func TestImportInformationModelForwardsItsOptions(t *testing.T) {
	rec := newAPIRecorder(t, http.StatusOK, `{"notes":[]}`)

	text, isErr, c := rec.call(t, "atlas_import_information_model", map[string]any{"applicationId": "app-1"})
	wantRefusal(t, "atlas_import_information_model", text, isErr, c, "argument: document")

	_, isErr, c = rec.call(t, "atlas_import_information_model", map[string]any{
		"applicationId": "app-1", "document": "{}", "format": "json", "name": "Orders",
		"documentation": "what an order is", "dryRun": true,
	})
	if isErr {
		t.Fatal("atlas_import_information_model reported an error")
	}
	wantCall(t, "atlas_import_information_model", c, http.MethodPost, "/api/v1/infomodel/import", "")
	body := bodyObject(t, c)
	for k, want := range map[string]any{
		"applicationId": "app-1", "document": "{}", "format": "json", "name": "Orders",
		"documentation": "what an order is", "dryRun": true,
	} {
		if body[k] != want {
			t.Errorf("import body %s = %v, want %v", k, body[k], want)
		}
	}

	_, _, c = rec.call(t, "atlas_import_information_model",
		map[string]any{"applicationId": "app-1", "document": "{}", "dryRun": false})
	body = bodyObject(t, c)
	for _, k := range []string{"format", "name", "documentation", "dryRun"} {
		if _, has := body[k]; has {
			t.Errorf("import body = %v, want no %s when it was not asked for", body, k)
		}
	}
}

// TestSaveInformationModelForwardsDocumentation: documentation is one of the fields a
// whole-document write may change on its own.
func TestSaveInformationModelForwardsDocumentation(t *testing.T) {
	rec := newAPIRecorder(t, http.StatusOK, `{"id":"m1"}`)
	_, isErr, c := rec.call(t, "atlas_save_information_model",
		map[string]any{"id": "m1", "documentation": "customers and their orders"})
	if isErr {
		t.Fatal("atlas_save_information_model reported an error")
	}
	wantCall(t, "atlas_save_information_model", c, http.MethodPut, "/api/v1/infomodel/models/m1", "")
	if body := bodyObject(t, c); body["documentation"] != "customers and their orders" {
		t.Fatalf("save body = %v, want the documentation", body)
	}
}

// TestValidateInformationModelSendsTheDocument: validation judges exactly what it is
// given, stores included, and needs no model id.
func TestValidateInformationModelSendsTheDocument(t *testing.T) {
	rec := newAPIRecorder(t, http.StatusOK, `{"valid":true}`)
	stores := []any{map[string]any{"name": "CRM", "classId": "c1"}}
	_, isErr, c := rec.call(t, "atlas_validate_information_model",
		map[string]any{"classes": []any{}, "stores": stores})
	if isErr {
		t.Fatal("atlas_validate_information_model reported an error")
	}
	wantCall(t, "atlas_validate_information_model", c, http.MethodPost, "/api/v1/infomodel/validate", "")
	body := bodyObject(t, c)
	if got, _ := body["stores"].([]any); len(got) != 1 {
		t.Fatalf("validate body stores = %v, want the one store", body["stores"])
	}
	if _, has := body["associations"]; has {
		t.Fatalf("validate body = %v, want no associations when none were given", body)
	}
}

// TestClassUsageNeedsTheClass: usage is per class, so a missing class name is refused
// rather than answered for the whole model.
func TestClassUsageNeedsTheClass(t *testing.T) {
	rec := newAPIRecorder(t, http.StatusOK, `{}`)
	text, isErr, c := rec.call(t, "atlas_class_usage", map[string]any{"id": "m1"})
	wantRefusal(t, "atlas_class_usage", text, isErr, c, "argument: class")
}

// TestModelDifferenceAndDerivedReadTheApplication: both reads are per application,
// with the id query-escaped since it is free text.
func TestModelDifferenceAndDerivedReadTheApplication(t *testing.T) {
	rec := newAPIRecorder(t, http.StatusOK, `{}`)
	for _, tc := range []struct{ tool, path string }{
		{"atlas_model_difference", "/api/v1/infomodel/difference"},
		{"atlas_derived_information_model", "/api/v1/infomodel/derived"},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			_, isErr, c := rec.call(t, tc.tool, map[string]any{"applicationId": "app 1"})
			if isErr {
				t.Fatalf("%s reported an error", tc.tool)
			}
			wantCall(t, tc.tool, c, http.MethodGet, tc.path, "applicationId=app+1")
		})
	}
}
