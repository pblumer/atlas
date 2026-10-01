package api

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
)

// TestConnectorsOutboxHonoursALimit: the outbox view polls, and a limit is how a
// small panel asks for the newest few without paying for the whole buffer. A page
// cut short says so.
func TestConnectorsOutboxHonoursALimit(t *testing.T) {
	srv := newServerForErrors(t)
	for _, subject := range []string{"first", "second"} {
		if code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/mail/outbox",
			`{"connector":"preview","subject":"`+subject+`"}`, "application/json"); code != http.StatusNoContent {
			t.Fatalf("deliver %s: %d (%s)", subject, code, body)
		}
	}
	code, body := serveInternal(t, srv, http.MethodGet, "/api/v1/mail/outbox?limit=1", "", "")
	if code != http.StatusOK {
		t.Fatalf("outbox: %d (%s)", code, body)
	}
	var got struct {
		Messages  []json.RawMessage `json:"messages"`
		Truncated bool              `json:"truncated"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if len(got.Messages) != 1 || !got.Truncated {
		t.Errorf("outbox = %d message(s), truncated=%v; want one and the cut said", len(got.Messages), got.Truncated)
	}
}

// TestConnectorsWillNotTakeAConnectionStringWithoutAVault: a pasted connection string
// is a credential, and the record stores only a vault reference to it. With no vault
// there is nowhere safe to put it, so the worker is refused — never stored with the
// secret in it, and never stored pointing at nothing.
func TestConnectorsWillNotTakeAConnectionStringWithoutAVault(t *testing.T) {
	srv := newServerWithoutVault(t)
	code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/connectors",
		`{"name":"warehouse","kind":"postgres","connectionString":"postgres://u:secret@db.example/w"}`, "application/json")
	if code != http.StatusServiceUnavailable || !strings.Contains(string(body), "vault not configured") {
		t.Fatalf("create without a vault: %d (%s), want 503", code, body)
	}
	if strings.Contains(string(body), "secret") {
		t.Errorf("the refusal echoed the credential: %s", body)
	}
	if entries, err := os.ReadDir(srv.connectors.Dir()); err != nil || len(entries) != 0 {
		t.Errorf("worker store after the refusal = %v (%v), want empty", entries, err)
	}
}

// TestConnectorsCheckReadsItsRequestBeforeDialling: the check sends real mail when
// asked to, so a body that does not parse is refused, and one that names no kind is
// a mail check — held to what a mail worker needs before anything is dialled.
func TestConnectorsCheckReadsItsRequestBeforeDialling(t *testing.T) {
	srv := newServerForErrors(t)
	if code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/connectors/test", `{"kind":`, "application/json"); code != http.StatusBadRequest {
		t.Errorf("malformed check: %d (%s), want 400", code, body)
	}
	code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/connectors/test", `{"provider":"smtp"}`, "application/json")
	if code != http.StatusBadRequest {
		t.Errorf("a mail check with nothing to dial: %d (%s), want 400 from the mail rules", code, body)
	}
}

// TestConnectorsUpdateChangesTheModelAnAgentAsks: which model an agent worker asks is
// the setting an operator changes most often, and it is not a secret — so it is
// updated in place, trimmed, and echoed back.
func TestConnectorsUpdateChangesTheModelAnAgentAsks(t *testing.T) {
	srv := newServerForErrors(t)
	code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/connectors",
		`{"name":"llm","kind":"agent","credentialsRef":"agent-key"}`, "application/json")
	if code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("create agent worker: %d (%s)", code, body)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &created); err != nil || created.ID == "" {
		t.Fatalf("decode: %v (%s)", err, body)
	}

	code, body = serveInternal(t, srv, http.MethodPatch, "/api/v1/connectors/"+created.ID, `{"model":"  claude-x  "}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("update: %d (%s)", code, body)
	}
	var stored connector
	var err error
	srv.do(func() { stored, _, err = srv.connectors.Get(created.ID) })
	if err != nil || stored.Model != "claude-x" {
		t.Errorf("stored model = %q (%v), want claude-x", stored.Model, err)
	}
	if !strings.Contains(string(body), `"model":"claude-x"`) {
		t.Errorf("update response = %s, want the new model echoed", body)
	}
}
