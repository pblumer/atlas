package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/dmn"
)

// TestDmnRefGraphEndpoint covers the read-only viewer's endpoint: a reference to a
// valid model returns its requirements graph; an unknown reference is a 404.
func TestDmnRefGraphEndpoint(t *testing.T) {
	srv, _ := newValidateServer(t) // seeds dmn-models/dish.dmn
	h := srv.Handler()
	do := func(method, path, body string) (int, []byte) {
		var req *http.Request
		if body != "" {
			req = httptest.NewRequest(method, path, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
		} else {
			req = httptest.NewRequest(method, path, nil)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.Bytes()
	}

	if code, _ := do(http.MethodGet, "/api/v1/dmnrefs/nope/graph", ""); code != http.StatusNotFound {
		t.Fatalf("graph of unknown ref = %d, want 404", code)
	}

	_, b := do(http.MethodPost, "/api/v1/dmnrefs", `{"name":"Dish","modelRef":"dish"}`)
	var ref struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(b, &ref); err != nil || ref.ID == "" {
		t.Fatalf("create ref: %v (%s)", err, b)
	}

	code, b := do(http.MethodGet, "/api/v1/dmnrefs/"+ref.ID+"/graph", "")
	if code != http.StatusOK {
		t.Fatalf("graph: %d %s", code, b)
	}
	var g dmn.ModelGraph
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !g.Valid || len(g.Nodes) == 0 {
		t.Fatalf("graph = %+v, want a valid graph with nodes", g)
	}
}

// TestDmnRefGraphResolveError covers the endpoint's infrastructure-error branch: a
// reference whose model source is unreadable (a directory where a file is expected)
// is a 500, not a normal empty graph.
func TestDmnRefGraphResolveError(t *testing.T) {
	srv, dir := newValidateServer(t)
	// A directory named "busy.dmn" makes DirResolver.Resolve("busy") fail with a
	// non-not-found error.
	if err := os.MkdirAll(filepath.Join(dir, "dmn-models", "busy.dmn"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	h := srv.Handler()
	do := func(method, path, body string) (int, []byte) {
		var req *http.Request
		if body != "" {
			req = httptest.NewRequest(method, path, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
		} else {
			req = httptest.NewRequest(method, path, nil)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.Bytes()
	}
	_, b := do(http.MethodPost, "/api/v1/dmnrefs", `{"name":"Busy","modelRef":"busy"}`)
	var ref struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(b, &ref); err != nil {
		t.Fatalf("create ref: %v", err)
	}
	if code, _ := do(http.MethodGet, "/api/v1/dmnrefs/"+ref.ID+"/graph", ""); code != http.StatusInternalServerError {
		t.Fatalf("graph with an unreadable model = %d, want 500", code)
	}
}
