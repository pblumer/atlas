package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestConfiguredWorkerAliasMirrorsConnectors pins ADR-0203's public migration:
// configured Workers are the canonical resource, while /connectors remains a
// compatibility alias over the same store and handlers.
func TestConfiguredWorkerAliasMirrorsConnectors(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/configured-workers", `{"name":"jira-prod","kind":"jira","endpoint":"https://jira.example.test","enabled":true}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("create configured worker status=%d body=%s", code, body)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode configured worker: %v (%s)", err, body)
	}
	if created.ID == "" {
		t.Fatalf("configured worker has empty id: %s", body)
	}

	for _, path := range []string{"/api/v1/configured-workers", "/api/v1/connectors"} {
		code, body = doReq(t, ts, http.MethodGet, path, "", "")
		if code != http.StatusOK || !strings.Contains(string(body), `"name":"jira-prod"`) {
			t.Fatalf("list via %s status=%d body=%s", path, code, body)
		}
	}

	code, body = doReq(t, ts, http.MethodPatch, "/api/v1/connectors/"+created.ID, `{"name":"jira-prod-renamed"}`, "application/json")
	if code != http.StatusOK || !strings.Contains(string(body), `"name":"jira-prod-renamed"`) {
		t.Fatalf("update via legacy connector path status=%d body=%s", code, body)
	}
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/configured-workers", "", "")
	if code != http.StatusOK || !strings.Contains(string(body), `"name":"jira-prod-renamed"`) {
		t.Fatalf("legacy update not visible via configured workers status=%d body=%s", code, body)
	}

	if code, body = doReq(t, ts, http.MethodDelete, "/api/v1/configured-workers/"+created.ID, "", ""); code != http.StatusNoContent {
		t.Fatalf("delete configured worker status=%d body=%s", code, body)
	}
	if _, body = doReq(t, ts, http.MethodGet, "/api/v1/connectors", "", ""); strings.Contains(string(body), created.ID) {
		t.Fatalf("deleted configured worker still visible through connector alias: %s", body)
	}
}

// TestWorkerTypesAliasMirrorsConnectorKinds proves the Worker Catalog API is a
// terminology alias over the same server capability data, not a second catalog.
func TestWorkerTypesAliasMirrorsConnectorKinds(t *testing.T) {
	ts := newTestServer(t)

	newCode, newBody := doReq(t, ts, http.MethodGet, "/api/v1/worker-types", "", "")
	oldCode, oldBody := doReq(t, ts, http.MethodGet, "/api/v1/connector-kinds", "", "")
	if newCode != http.StatusOK || oldCode != http.StatusOK {
		t.Fatalf("worker types status=%d body=%s; connector kinds status=%d body=%s", newCode, newBody, oldCode, oldBody)
	}
	if string(newBody) != string(oldBody) {
		t.Fatalf("worker types and connector kinds diverged:\nworker-types: %s\nconnector-kinds: %s", newBody, oldBody)
	}
}

// TestOpenAPIAdvertisesWorkerAliasesAndDeprecatesConnectorCore checks that new
// clients are led to ADR-0203 terminology while old clients can discover their
// compatibility surface is deprecated.
func TestOpenAPIAdvertisesWorkerAliasesAndDeprecatesConnectorCore(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/openapi.json", "", "")
	if code != http.StatusOK {
		t.Fatalf("openapi.json status=%d body=%s", code, body)
	}
	var doc struct {
		Paths map[string]map[string]struct {
			Deprecated bool `json:"deprecated"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("decode openapi: %v", err)
	}

	for _, tc := range []struct {
		path       string
		method     string
		deprecated bool
	}{
		{"/api/v1/configured-workers", "get", false},
		{"/api/v1/configured-workers", "post", false},
		{"/api/v1/configured-workers/{id}", "patch", false},
		{"/api/v1/configured-workers/{id}", "delete", false},
		{"/api/v1/worker-types", "get", false},
		{"/api/v1/connectors", "get", true},
		{"/api/v1/connectors", "post", true},
		{"/api/v1/connectors/{id}", "patch", true},
		{"/api/v1/connectors/{id}", "delete", true},
		{"/api/v1/connector-kinds", "get", true},
	} {
		op, ok := doc.Paths[tc.path][tc.method]
		if !ok {
			t.Errorf("OpenAPI missing %s %s", strings.ToUpper(tc.method), tc.path)
			continue
		}
		if op.Deprecated != tc.deprecated {
			t.Errorf("%s %s deprecated=%v, want %v", strings.ToUpper(tc.method), tc.path, op.Deprecated, tc.deprecated)
		}
	}

	if _, ok := doc.Paths["/api/v1/workers"]; !ok {
		t.Error("runtime /api/v1/workers route disappeared during configured-worker migration")
	}
}
