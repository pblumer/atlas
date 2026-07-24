package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// newDocsServer wires the same real engine stack as newTestServer but with the
// API explorer enabled, so the gated routes are reachable.
func newDocsServer(t *testing.T) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	proc := engine.New(1, log, store, nil)
	if err := proc.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	srv, err := api.New(proc, store, dir, api.WithDocs())
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ts.Close()
		srv.Close()
		_ = store.Close()
		_ = log.Close()
	})
	return ts
}

// TestDocsDisabledByDefault: without WithDocs, neither the spec nor the explorer
// is served — the catch-all file server 404s them (ADR-0043 gate).
func TestDocsDisabledByDefault(t *testing.T) {
	ts := newTestServer(t)
	for _, path := range []string{"/api/v1/openapi.json", "/api/docs"} {
		status, _ := doReq(t, ts, http.MethodGet, path, "", "")
		if status != http.StatusNotFound {
			t.Errorf("GET %s with docs off: status %d, want 404", path, status)
		}
	}
}

// TestOpenAPIServedWhenEnabled: the spec endpoint returns a valid OpenAPI 3.1
// document describing the live surface, as JSON.
func TestOpenAPIServedWhenEnabled(t *testing.T) {
	ts := newDocsServer(t)
	status, body := doReq(t, ts, http.MethodGet, "/api/v1/openapi.json", "", "")
	if status != http.StatusOK {
		t.Fatalf("GET /api/v1/openapi.json: status %d, want 200\n%s", status, body)
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("openapi.json is not valid JSON: %v", err)
	}
	if doc["openapi"] != "3.1.0" {
		t.Errorf("openapi = %v, want 3.1.0", doc["openapi"])
	}
	paths, ok := doc["paths"].(map[string]any)
	if !ok || paths["/api/v1/deployments"] == nil {
		t.Errorf("spec does not document the deployments endpoint: %v", doc["paths"])
	}
}

// TestExplorerServedWhenEnabled: the explorer shell is HTML that loads the
// vendored Scalar asset and points it at the same-origin spec.
func TestExplorerServedWhenEnabled(t *testing.T) {
	ts := newDocsServer(t)
	for _, path := range []string{"/api/docs", "/api/docs/"} {
		status, body := doReq(t, ts, http.MethodGet, path, "", "")
		if status != http.StatusOK {
			t.Fatalf("GET %s: status %d, want 200", path, status)
		}
		html := string(body)
		if !strings.Contains(html, "/vendor/scalar/standalone.js") {
			t.Errorf("GET %s: explorer does not reference the vendored Scalar asset", path)
		}
		if !strings.Contains(html, "/api/v1/openapi.json") {
			t.Errorf("GET %s: explorer does not point at the OpenAPI document", path)
		}
	}
}

// TestVendoredScalarAssetEmbedded: the standalone build is embedded and served,
// so the explorer works offline (no CDN).
func TestVendoredScalarAssetEmbedded(t *testing.T) {
	ts := newDocsServer(t)
	status, body := doReq(t, ts, http.MethodGet, "/vendor/scalar/standalone.js", "", "")
	if status != http.StatusOK {
		t.Fatalf("GET /vendor/scalar/standalone.js: status %d, want 200", status)
	}
	if !strings.Contains(string(body), "createApiReference") {
		t.Errorf("vendored asset does not look like the Scalar standalone build")
	}
}

// TestExplorerCanReachLiveAPI is the point of the feature: a request issued the
// way the explorer's "Try it out" would (same-origin against /api/v1) hits the
// real engine. Deploy then read stats through the documented surface.
func TestExplorerCanReachLiveAPI(t *testing.T) {
	ts := newDocsServer(t)
	status, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", sampleBPMN, "application/xml")
	if status != http.StatusOK {
		t.Fatalf("deploy through documented surface: status %d\n%s", status, body)
	}
	status, body = doReq(t, ts, http.MethodGet, "/api/v1/stats", "", "")
	if status != http.StatusOK {
		t.Fatalf("stats: status %d\n%s", status, body)
	}
}
