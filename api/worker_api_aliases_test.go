package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api"
)

// TestConfiguredWorkerDTOProjectsTheConnectorStore pins ADR-0208 migration step 3:
// configured Workers have a canonical Worker-oriented representation while the
// deprecated connector API retains its historical shape over the same record.
func TestConfiguredWorkerDTOProjectsTheConnectorStore(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/configured-workers", `{"name":"temis-prod","workerTypeId":"atlas.temis","workerTypeVersion":"1.0.0","config":{"endpoint":"https://temis.example.test"},"credentialsRef":"temis-token","enabled":true}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("create configured worker status=%d body=%s", code, body)
	}
	var created map[string]any
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode configured worker: %v (%s)", err, body)
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("configured worker has empty id: %s", body)
	}
	if created["workerTypeId"] != "atlas.temis" || created["workerTypeVersion"] != "1.0.0" {
		t.Fatalf("configured worker has no canonical Worker Type identity: %v", created)
	}
	if _, legacy := created["kind"]; legacy {
		t.Fatalf("configured worker leaks legacy kind: %v", created)
	}
	config, ok := created["config"].(map[string]any)
	if !ok || config["endpoint"] != "https://temis.example.test" {
		t.Fatalf("configured worker does not separate configuration: %v", created)
	}
	if created["credentialsRef"] != "temis-token" {
		t.Fatalf("configured worker did not carry the credential reference: %v", created)
	}

	code, body = doReq(t, ts, http.MethodGet, "/api/v1/connectors", "", "")
	if code != http.StatusOK || !strings.Contains(string(body), `"kind":"temis"`) || !strings.Contains(string(body), `"endpoint":"https://temis.example.test"`) {
		t.Fatalf("canonical create not visible through connector compatibility API status=%d body=%s", code, body)
	}
	if strings.Contains(string(body), "workerTypeId") || strings.Contains(string(body), `"config"`) {
		t.Fatalf("legacy connector representation changed: %s", body)
	}

	code, body = doReq(t, ts, http.MethodPatch, "/api/v1/configured-workers/"+id, `{"config":{"endpoint":"https://temis-new.example.test"},"credentialsRef":"temis-token-2"}`, "application/json")
	if code != http.StatusOK || !strings.Contains(string(body), `"workerTypeId":"atlas.temis"`) || !strings.Contains(string(body), `"endpoint":"https://temis-new.example.test"`) {
		t.Fatalf("update configured worker status=%d body=%s", code, body)
	}
	_, body = doReq(t, ts, http.MethodGet, "/api/v1/connectors", "", "")
	if !strings.Contains(string(body), `"endpoint":"https://temis-new.example.test"`) || !strings.Contains(string(body), `"credentialsRef":"temis-token-2"`) {
		t.Fatalf("configured worker update not visible through connector compatibility API: %s", body)
	}

	// Connector names and kinds are deliberately immutable because deployed models
	// reference the logical name. Exercise an allowed compatibility mutation instead.
	code, body = doReq(t, ts, http.MethodPatch, "/api/v1/connectors/"+id, `{"enabled":false}`, "application/json")
	if code != http.StatusOK || !strings.Contains(string(body), `"enabled":false`) {
		t.Fatalf("update via legacy connector path status=%d body=%s", code, body)
	}
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/configured-workers", "", "")
	if code != http.StatusOK || !strings.Contains(string(body), `"enabled":false`) {
		t.Fatalf("legacy update not visible via configured workers status=%d body=%s", code, body)
	}

	if code, body = doReq(t, ts, http.MethodDelete, "/api/v1/configured-workers/"+id, "", ""); code != http.StatusNoContent {
		t.Fatalf("delete configured worker status=%d body=%s", code, body)
	}
	if _, body = doReq(t, ts, http.MethodGet, "/api/v1/connectors", "", ""); strings.Contains(string(body), id) {
		t.Fatalf("deleted configured worker still visible through connector compatibility path: %s", body)
	}
}

func TestConfiguredWorkerRejectsUnknownOrMismatchedWorkerType(t *testing.T) {
	ts := newTestServer(t)
	for _, body := range []string{
		`{"name":"unknown","workerTypeId":"example.unknown","workerTypeVersion":"1.0.0","config":{}}`,
		`{"name":"wrong-version","workerTypeId":"atlas.temis","workerTypeVersion":"2.0.0","config":{"endpoint":"https://temis.example.test"}}`,
		`{"name":"invalid-config","workerTypeId":"atlas.temis","workerTypeVersion":"1.0.0","config":{}}`,
	} {
		code, response := doReq(t, ts, http.MethodPost, "/api/v1/configured-workers", body, "application/json")
		if code != http.StatusBadRequest {
			t.Fatalf("invalid Worker Type accepted status=%d body=%s", code, response)
		}
	}

	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/configured-workers", "{", "application/json"); code != http.StatusBadRequest {
		t.Fatalf("malformed configured Worker create status=%d, want 400", code)
	}
	if code, _ := doReq(t, ts, http.MethodPatch, "/api/v1/configured-workers/missing", "{", "application/json"); code != http.StatusBadRequest {
		t.Fatalf("malformed configured Worker update status=%d, want 400", code)
	}
	if code, _ := doReq(t, ts, http.MethodPatch, "/api/v1/configured-workers/missing", `{"enabled":false}`, "application/json"); code != http.StatusNotFound {
		t.Fatalf("missing configured Worker update status=%d, want 404", code)
	}
}

func TestLegacyConnectorIsProjectedAsConfiguredWorker(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/connectors", `{"name":"mail-prod","kind":"mail","endpoint":"smtp.example.test:587","sender":"bot@example.test","credentialsRef":"smtp-token"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("create legacy connector status=%d body=%s", code, body)
	}
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/configured-workers", "", "")
	if code != http.StatusOK || !strings.Contains(string(body), `"workerTypeId":"atlas.mail"`) || !strings.Contains(string(body), `"workerTypeVersion":"1.0.0"`) {
		t.Fatalf("legacy connector not projected as configured worker status=%d body=%s", code, body)
	}
	if strings.Contains(string(body), `"kind":"mail"`) {
		t.Fatalf("configured worker list leaked the legacy kind field: %s", body)
	}
	if !strings.Contains(string(body), `"config":{"endpoint":"smtp.example.test:587"`) {
		t.Fatalf("configured worker list does not carry the endpoint inside config: %s", body)
	}
}

func TestConfiguredWorkerUsesWorkerTypeValidation(t *testing.T) {
	ts := newTestServer(t)
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{"missing endpoint", `{"name":"jira-no-endpoint","workerTypeId":"atlas.jira","workerTypeVersion":"1.0.0","config":{},"credentialsRef":"jira-token"}`, "endpoint"},
		{"missing credentials", `{"name":"jira-no-credentials","workerTypeId":"atlas.jira","workerTypeVersion":"1.0.0","config":{"endpoint":"https://jira.example.test"}}`, "credentialsRef"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, body := doReq(t, ts, http.MethodPost, "/api/v1/configured-workers", tc.body, "application/json")
			if code != http.StatusBadRequest || !strings.Contains(string(body), tc.want) {
				t.Fatalf("status=%d body=%s, want 400 naming %s", code, body, tc.want)
			}
		})
	}

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/configured-workers", `{"name":"jira-prod","workerTypeId":"atlas.jira","workerTypeVersion":"1.0.0","config":{"endpoint":"https://jira.example.test","provider":"smtp","sender":"wrong@example.test"},"credentialsRef":"jira-token"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("valid Jira configured Worker status=%d body=%s", code, body)
	}
	if !strings.Contains(string(body), `"workerTypeId":"atlas.jira"`) || strings.Contains(string(body), `"provider"`) || strings.Contains(string(body), `"sender"`) {
		t.Fatalf("Jira configured Worker did not apply kind normalization: %s", body)
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
		t.Fatalf("worker types status=%d body=%s; connector kinds status=%d body=%s", newCode, newBody, oldCode, oldBody)
	}
	if string(newBody) == string(oldBody) {
		t.Fatalf("worker types still mirror legacy connector kinds: %s", newBody)
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

// TestConfiguredWorkerPatchChangesOnlyTheKeysItNames pins the difference between an
// absent configuration key and an empty one. Translating a partial canonical patch
// into a full connector patch would move a Gmail Worker onto SMTP whenever somebody
// edited its endpoint — with the OAuth bundle reference still attached, and a 200 on
// the way out.
func TestConfiguredWorkerPatchChangesOnlyTheKeysItNames(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/configured-workers",
		`{"name":"mail-gmail","workerTypeId":"atlas.mail","workerTypeVersion":"1.0.0","config":{"provider":"gmail","sender":"bot@example.test"},"credentialsRef":"gmail-bundle"}`,
		"application/json")
	if code != http.StatusOK {
		t.Fatalf("create mail worker status=%d body=%s", code, body)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode configured worker: %v (%s)", err, body)
	}

	config := func(t *testing.T, raw []byte) map[string]any {
		t.Helper()
		var worker struct {
			Config map[string]any `json:"config"`
		}
		if err := json.Unmarshal(raw, &worker); err != nil {
			t.Fatalf("decode configured worker: %v (%s)", err, raw)
		}
		return worker.Config
	}

	code, body = doReq(t, ts, http.MethodPatch, "/api/v1/configured-workers/"+created.ID,
		`{"config":{"endpoint":"https://graph.example.test"}}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("endpoint-only patch status=%d body=%s", code, body)
	}
	if got := config(t, body); got["provider"] != "gmail" || got["sender"] != "bot@example.test" || got["endpoint"] != "https://graph.example.test" {
		t.Fatalf("endpoint-only patch changed keys it did not name: %v", got)
	}

	code, body = doReq(t, ts, http.MethodPatch, "/api/v1/configured-workers/"+created.ID,
		`{"config":{"sender":"ops@example.test"}}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("sender-only patch status=%d body=%s", code, body)
	}
	if got := config(t, body); got["provider"] != "gmail" || got["endpoint"] != "https://graph.example.test" || got["sender"] != "ops@example.test" {
		t.Fatalf("sender-only patch changed keys it did not name: %v", got)
	}

	code, body = doReq(t, ts, http.MethodPatch, "/api/v1/configured-workers/"+created.ID,
		`{"enabled":false}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("enabled-only patch status=%d body=%s", code, body)
	}
	if got := config(t, body); got["provider"] != "gmail" || got["endpoint"] != "https://graph.example.test" || got["sender"] != "ops@example.test" {
		t.Fatalf("a patch carrying no config changed the configuration: %v", got)
	}
	if !strings.Contains(string(body), `"credentialsRef":"gmail-bundle"`) {
		t.Fatalf("patch dropped the credential reference: %s", body)
	}
}

// TestConfiguredWorkerSealsASQLConnectionString pins that the canonical API can
// configure a SQL Worker Type at all. For those types the credential *is* the
// configuration, so a DTO with no connectionString could only name a vault key
// somebody had already written by other means.
func TestConfiguredWorkerSealsASQLConnectionString(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/configured-workers",
		`{"name":"pg-prod","workerTypeId":"atlas.postgres","workerTypeVersion":"1.0.0","config":{"connectionString":"postgres://atlas:s3cr3t@db.example.test:5432/atlas"}}`,
		"application/json")
	if code != http.StatusOK {
		t.Fatalf("create SQL worker status=%d body=%s", code, body)
	}
	if strings.Contains(string(body), "s3cr3t") || strings.Contains(string(body), "connectionString") {
		t.Fatalf("the response echoed the connection string: %s", body)
	}
	var created struct {
		CredentialsRef string `json:"credentialsRef"`
		Config         struct {
			Endpoint string `json:"endpoint"`
		} `json:"config"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode configured worker: %v (%s)", err, body)
	}
	if created.CredentialsRef == "" {
		t.Fatalf("sealed connection string left no credential reference: %s", body)
	}
	if created.Config.Endpoint != "atlas@db.example.test:5432/atlas" {
		t.Fatalf("SQL worker endpoint is not the redacted target: %s", body)
	}

	if _, body = doReq(t, ts, http.MethodGet, "/api/v1/configured-workers", "", ""); strings.Contains(string(body), "s3cr3t") {
		t.Fatalf("the configured Worker listing echoed the connection string: %s", body)
	}
}
