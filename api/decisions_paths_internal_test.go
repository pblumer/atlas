package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// decisionsPathsList reads the decision catalog, optionally for one application.
func decisionsPathsList(t *testing.T, srv *Server, query string) []decisionCatalogItem {
	t.Helper()
	code, body := serveInternal(t, srv, http.MethodGet, "/api/v1/decisions"+query, "", "")
	if code != http.StatusOK {
		t.Fatalf("decisions: %d (%s)", code, body)
	}
	var out []decisionCatalogItem
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	return out
}

// TestDecisionsCatalogShowsOneApplicationAndSurvivesABrokenModel: the picker asks
// for one application's decisions, and another application's must not be offered.
// A reference whose model cannot be read costs that model's decisions and nothing
// else — the validate endpoint is where that failure is reported, per reference.
func TestDecisionsCatalogShowsOneApplicationAndSurvivesABrokenModel(t *testing.T) {
	srv, _ := newValidateServer(t)
	dir := dmnModelListPathsDir(t, srv)
	// "odd" resolves to a folder where its file should be: unreadable, not invalid.
	if err := os.MkdirAll(filepath.Join(dir, "odd.dmn", "inside"), 0o755); err != nil {
		t.Fatal(err)
	}
	var err error
	srv.do(func() {
		for _, ref := range []dmnRef{
			{ID: "r-dish", Name: "Dish", ModelRef: "dish", ProjectID: "p-a"},
			{ID: "r-odd", Name: "Odd", ModelRef: "odd", ProjectID: "p-a"},
			{ID: "r-other", Name: "Other", ModelRef: "dish-copy", ProjectID: "p-b"},
		} {
			if err = srv.dmnrefs.Save(ref); err != nil {
				return
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}

	got := decisionsPathsList(t, srv, "?projectId=p-a")
	if len(got) == 0 {
		t.Fatal("the readable model's decisions are missing")
	}
	for _, d := range got {
		if d.ModelRef != "dish" {
			t.Errorf("listed %+v, want only the readable model of application p-a", d)
		}
	}
}

// TestDecisionsFailsWhenTheReferencesCannotBeRead: the catalog is built from the
// references, and an empty catalog from a store nobody could read would tell an
// author the application has no decisions to call.
func TestDecisionsFailsWhenTheReferencesCannotBeRead(t *testing.T) {
	srv := newServerForErrors(t)
	approvalsPathsDirAsFile(t, srv.dmnrefs.Dir())
	code, body := serveInternal(t, srv, http.MethodGet, "/api/v1/decisions", "", "")
	if code != http.StatusInternalServerError || !strings.Contains(string(body), "list dmn references") {
		t.Errorf("unreadable references: %d (%s), want 500", code, body)
	}
}

// TestDecisionsGraphFailsOnAReferenceItCannotRead: the graph viewer must say the
// reference is broken, not that it does not exist — a 404 would send the author
// looking for a reference that is sitting right there.
func TestDecisionsGraphFailsOnAReferenceItCannotRead(t *testing.T) {
	srv := newServerForErrors(t)
	corrupt(t, srv.dmnrefs.Dir(), "ref-1")
	code, body := serveInternal(t, srv, http.MethodGet, "/api/v1/dmnrefs/ref-1/graph", "", "")
	if code != http.StatusInternalServerError || !strings.Contains(string(body), "read dmn reference") {
		t.Errorf("unreadable reference: %d (%s), want 500", code, body)
	}
}
