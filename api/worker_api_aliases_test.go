package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api"
)

// TestConfiguredWorkerAliasMirrorsConnectors pins ADR-0203's public migration:
// configured Workers are the canonical resource, while /connectors remains a
// compatibility name over the same store and handlers.
func TestConfiguredWorkerAliasMirrorsConnectors(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/configured-workers", `{"name":"temis-prod","kind":"temis","endpoint":"https://temis.example.test","enabled":true}`, "application/json")
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
		if code != http.StatusOK || !strings.Contains(string(body), `"name":"temis-prod"`) {
			t.Fatalf("list via %s status=%d body=%s", path, code, body)
		}
	}

	// Worker names and kinds are deliberately immutable because deployed models
	// reference the logical name. Exercise an allowed compatibility mutation instead.
	code, body = doReq(t, ts, http.MethodPatch, "/api/v1/connectors/"+created.ID, `{"enabled":false}`, "application/json")
	if code != http.StatusOK || !strings.Contains(string(body), `"enabled":false`) {
		t.Fatalf("update via legacy worker path status=%d body=%s", code, body)
	}
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/configured-workers", "", "")
	if code != http.StatusOK || !strings.Contains(string(body), `"enabled":false`) {
		t.Fatalf("legacy update not visible via configured workers status=%d body=%s", code, body)
	}

	if code, body = doReq(t, ts, http.MethodDelete, "/api/v1/configured-workers/"+created.ID, "", ""); code != http.StatusNoContent {
		t.Fatalf("delete configured worker status=%d body=%s", code, body)
	}
	if _, body = doReq(t, ts, http.MethodGet, "/api/v1/connectors", "", ""); strings.Contains(string(body), created.ID) {
		t.Fatalf("deleted configured worker still visible through worker compatibility path: %s", body)
	}
}

// TestWorkerTypesExposeCanonicalRuntimeModes pins ADR-0208's migration boundary:
// /worker-types is a Worker Type catalog projection over the existing built-in
// capability registry, while /connector-kinds remains the legacy compatibility API.
func TestWorkerTypesExposeCanonicalRuntimeModes(t *testing.T) {
	ts := newTestServer(t)

	newCode, newBody := doReq(t, ts, http.MethodGet, "/api/v1/worker-types", "", "")
	oldCode, oldBody := doReq(t, ts, http.MethodGet, "/api/v1/connector-kinds", "", "")
	if newCode != http.StatusOK || oldCode != http.StatusOK {
		t.Fatalf("worker-types status=%d body=%s; connector-kinds status=%d body=%s", newCode, newBody, oldCode, oldBody)
	}
	if string(newBody) == string(oldBody) {
		t.Fatalf("worker-types still mirrors the legacy connector-kinds shape: %s", newBody)
	}

	var catalog struct {
		Kinds []map[string]any `json:"kinds"`
	}
	if err := json.Unmarshal(newBody, &catalog); err != nil {
		t.Fatalf("decode worker types: %v (%s)", err, newBody)
	}
	if len(catalog.Kinds) == 0 {
		t.Fatal("worker type catalog is empty")
	}

	seenEmbedded := false
	seenSupervised := false
	seenJiraMetadata := false
	for _, workerType := range catalog.Kinds {
		if _, legacy := workerType["workerOnly"]; legacy {
			t.Fatalf("worker type leaks legacy workerOnly compatibility flag: %v", workerType)
		}
		workerTypeID, ok := workerType["workerTypeId"].(string)
		if !ok || !strings.HasPrefix(workerTypeID, "atlas.") {
			t.Fatalf("worker type has no stable Atlas-namespaced identity: %v", workerType)
		}
		switch workerType["runtimeMode"] {
		case "atlas-embedded":
			seenEmbedded = true
		case "atlas-supervised":
			seenSupervised = true
		case "external":
			// External packages are a valid canonical mode even though this first
			// built-in projection does not require one to exist yet.
		default:
			t.Fatalf("worker type has missing or invalid runtimeMode: %v", workerType)
		}
		if workerType["id"] == "jira" {
			if workerType["workerTypeId"] != "atlas.jira" || workerType["version"] != "1.0.0" || workerType["title"] != "Atlassian Jira" || workerType["vendor"] != "Atlas" || workerType["origin"] != "built-in" {
				t.Fatalf("jira Worker Type does not expose its built-in package metadata: %v", workerType)
			}
			seenJiraMetadata = true
		}
	}
	if !seenEmbedded {
		t.Fatal("worker type catalog has no atlas-embedded built-in")
	}
	if !seenSupervised {
		t.Fatal("worker type catalog has no atlas-supervised built-in")
	}
	if !seenJiraMetadata {
		t.Fatal("worker type catalog has no jira built-in metadata")
	}
}

// TestWorkerTypesProjectOperationCapabilityClaims pins CDA3's read-only projection: a
// built-in Worker Type lists the operations whose table rows declare a technical
// capability, each claim marked candidate because no evidence is published
// (ADR-0480, ADR-0483), and a Worker Type that declares none carries no operations.
func TestWorkerTypesProjectOperationCapabilityClaims(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/worker-types", "", "")
	if code != http.StatusOK {
		t.Fatalf("worker types status=%d body=%s", code, body)
	}
	type claim struct {
		ID      string `json:"id"`
		Version string `json:"version"`
		Profile string `json:"profile"`
		State   string `json:"state"`
	}
	var catalog struct {
		Kinds []struct {
			ID         string `json:"id"`
			Operations []struct {
				ID           string  `json:"id"`
				Capabilities []claim `json:"capabilities"`
			} `json:"operations"`
		} `json:"kinds"`
	}
	if err := json.Unmarshal(body, &catalog); err != nil {
		t.Fatalf("decode worker types: %v (%s)", err, body)
	}
	want := map[string]claim{
		"entra/create-channel":         {ID: "io.atlas.collaboration.channel.create", Version: "0.1.0", Profile: "microsoft-teams", State: "candidate"},
		"teamsphone/ensure-call-queue": {ID: "io.atlas.telephony.call-queue.ensure", Version: "0.1.0", Profile: "microsoft-teams-phone", State: "candidate"},
	}
	for _, kind := range catalog.Kinds {
		if kind.ID == "jira" && kind.Operations != nil {
			t.Fatalf("jira declares no claims but lists operations: %+v", kind.Operations)
		}
		for _, op := range kind.Operations {
			w, ok := want[kind.ID+"/"+op.ID]
			if !ok {
				continue
			}
			if len(op.Capabilities) != 1 || op.Capabilities[0] != w {
				t.Fatalf("%s %s capabilities=%+v, want [%+v]", kind.ID, op.ID, op.Capabilities, w)
			}
			delete(want, kind.ID+"/"+op.ID)
		}
	}
	if len(want) != 0 {
		t.Fatalf("worker-types does not project %v: %s", want, body)
	}
	if strings.Contains(string(body), `"verified"`) {
		t.Fatalf("a claim reads as verified although no evidence is published: %s", body)
	}
}

func TestWorkerTypeRuntimeModeDoesNotFollowManualPlacement(t *testing.T) {
	ts := newTestServerWith(t, api.WithOffloadedConnectorKinds([]string{"jira"}))
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/worker-types", "", "")
	if code != http.StatusOK {
		t.Fatalf("worker types status=%d body=%s", code, body)
	}
	var catalog struct {
		Kinds []struct {
			ID          string `json:"id"`
			RuntimeMode string `json:"runtimeMode"`
			Placement   string `json:"placement"`
		} `json:"kinds"`
	}
	if err := json.Unmarshal(body, &catalog); err != nil {
		t.Fatalf("decode worker types: %v (%s)", err, body)
	}
	for _, workerType := range catalog.Kinds {
		if workerType.ID != "jira" {
			continue
		}
		if workerType.Placement != "worker" {
			t.Fatalf("jira placement=%q, want worker after explicit offload", workerType.Placement)
		}
		if workerType.RuntimeMode != "atlas-embedded" {
			t.Fatalf("jira runtimeMode=%q, want atlas-embedded: placement must not rewrite the Worker Type contract", workerType.RuntimeMode)
		}
		return
	}
	t.Fatal("worker type catalog has no jira entry")
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
