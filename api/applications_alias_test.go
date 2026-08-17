package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The ADR-0034 "project" is being reframed as the ADR-0128 "process application":
// the HTTP surface gains /api/v1/applications routes bound to the very same
// handlers as /api/v1/projects, and the legacy /projects routes stay as
// deprecated aliases for one release so existing callers keep working. These
// tests pin that the two paths are interchangeable (same handlers, one store) and
// that the OpenAPI document advertises the applications surface and marks the
// projects surface deprecated.

// TestApplicationAliasMirrorsProjects drives the application lifecycle through the
// new path and proves it reads and writes the same underlying store as the
// project path — create via /applications, see it via /projects, and vice versa.
func TestApplicationAliasMirrorsProjects(t *testing.T) {
	ts := newTestServer(t)

	// Create through the new application surface.
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/applications", `{"name":"Onboarding"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("create application status=%d body=%s", code, body)
	}
	var app struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &app); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if app.ID == "" || app.Name != "Onboarding" {
		t.Fatalf("application = %+v, want a non-empty id and name Onboarding", app)
	}

	// It is visible through BOTH surfaces — one store behind two paths.
	for _, path := range []string{"/api/v1/applications", "/api/v1/projects"} {
		code, body = doReq(t, ts, http.MethodGet, path, "", "")
		if code != http.StatusOK || !strings.Contains(string(body), `"name":"Onboarding"`) {
			t.Fatalf("list via %s status=%d body=%s", path, code, body)
		}
	}

	// Mutations cross the alias too: rename via /applications, delete via /projects.
	code, body = doReq(t, ts, http.MethodPatch, "/api/v1/applications/"+app.ID, `{"name":"Onboarding v2"}`, "application/json")
	if code != http.StatusOK || !strings.Contains(string(body), `"name":"Onboarding v2"`) {
		t.Fatalf("rename via application path status=%d body=%s", code, body)
	}
	if code, _ := doReq(t, ts, http.MethodDelete, "/api/v1/projects/"+app.ID, "", ""); code != http.StatusNoContent {
		t.Fatalf("delete via project path status=%d", code)
	}
	if _, body := doReq(t, ts, http.MethodGet, "/api/v1/applications", "", ""); strings.Contains(string(body), app.ID) {
		t.Fatalf("deleted application still listed: %s", body)
	}
}

// TestApplicationDeployAliasExists proves the bundle-deploy route is reachable
// under the application path (the headline "publish application" action of
// ADR-0128). An empty application deploys nothing but returns 200.
func TestApplicationDeployAliasExists(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/applications", `{"name":"Empty"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("create application status=%d body=%s", code, body)
	}
	var app struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &app); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/applications/"+app.ID+"/deploy", "", "application/json"); code != http.StatusOK {
		t.Fatalf("deploy via application path status=%d body=%s", code, body)
	}
}

// TestOpenAPIAdvertisesApplicationsAndDeprecatesProjects checks the self-describing
// surface (ADR-0043) reflects the reframe: the applications paths are documented,
// and the legacy projects paths are marked deprecated so generated clients steer
// callers to the new names.
func TestOpenAPIAdvertisesApplicationsAndDeprecatesProjects(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/openapi.json", "", "")
	if code != http.StatusOK {
		t.Fatalf("openapi.json status=%d (docs enabled?) body=%s", code, body)
	}
	var doc struct {
		Paths map[string]map[string]struct {
			Deprecated bool `json:"deprecated"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("decode openapi: %v", err)
	}
	if _, ok := doc.Paths["/api/v1/applications"]; !ok {
		t.Errorf("openapi does not document /api/v1/applications")
	}
	if op, ok := doc.Paths["/api/v1/projects"]["post"]; !ok || !op.Deprecated {
		t.Errorf("POST /api/v1/projects should be documented and deprecated, got ok=%v deprecated=%v", ok, op.Deprecated)
	}
	if op, ok := doc.Paths["/api/v1/applications"]["post"]; !ok || op.Deprecated {
		t.Errorf("POST /api/v1/applications should be documented and NOT deprecated, got ok=%v deprecated=%v", ok, op.Deprecated)
	}
}
