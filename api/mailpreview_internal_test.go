package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/mail"
)

// postConnector posts a create-connector body and returns the status and the stored
// record, so a test can assert what was *normalized into* the record and not merely
// that the request was accepted.
func postConnector(t *testing.T, srv *Server, body string) (int, connector) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/connectors", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var c connector
	_ = json.Unmarshal(rec.Body.Bytes(), &c)
	return rec.Code, c
}

// TestMailEndpointNormalizedOnCreate is the outage in test form: a submission host
// written without a port used to be stored as typed and to fail much later, deep in
// the send, as an incident on a parked token (ADR-0149). It is now completed at the
// boundary, and what cannot be completed is refused where it was typed.
func TestMailEndpointNormalizedOnCreate(t *testing.T) {
	srv, _ := newValidateServer(t)

	code, c := postConnector(t, srv, `{"name":"relay","kind":"mail","endpoint":"mx1.example.ch","sender":"bot@example.com"}`)
	if code != http.StatusOK {
		t.Fatalf("portless endpoint: status=%d, want 200", code)
	}
	if c.Endpoint != "mx1.example.ch:587" {
		t.Errorf("stored endpoint = %q, want the submission port filled in", c.Endpoint)
	}

	code, c = postConnector(t, srv, `{"name":"tls","kind":"mail","endpoint":"smtps://mx2.example.ch","sender":"bot@example.com"}`)
	if code != http.StatusOK || c.Endpoint != "mx2.example.ch:465" {
		t.Errorf("smtps endpoint: status=%d endpoint=%q, want 200 and the implicit-TLS port", code, c.Endpoint)
	}

	for name, body := range map[string]string{
		"no endpoint":     `{"name":"e1","kind":"mail","sender":"bot@example.com"}`,
		"mailbox as host": `{"name":"e2","kind":"mail","endpoint":"bot@example.com","sender":"bot@example.com"}`,
		"bad port":        `{"name":"e3","kind":"mail","endpoint":"mx.example.ch:nope","sender":"bot@example.com"}`,
	} {
		if code, _ := postConnector(t, srv, body); code != http.StatusBadRequest {
			t.Errorf("%s: status=%d, want 400", name, code)
		}
	}
}

// TestMailEndpointCheckedOnUpdate closes the other half of the same gap: a PATCH
// carries no kind and no provider, so it never reached the create validator — which
// is exactly the path an endpoint gets edited through.
func TestMailEndpointCheckedOnUpdate(t *testing.T) {
	srv, _ := newValidateServer(t)
	code, c := postConnector(t, srv, `{"name":"relay","kind":"mail","endpoint":"mx1.example.ch:587","sender":"bot@example.com"}`)
	if code != http.StatusOK {
		t.Fatalf("create: status=%d", code)
	}

	patch := func(body string) (int, connector) {
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/connectors/"+c.ID, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		var got connector
		_ = json.Unmarshal(rec.Body.Bytes(), &got)
		return rec.Code, got
	}

	code, got := patch(`{"endpoint":"mx9.example.ch"}`)
	if code != http.StatusOK || got.Endpoint != "mx9.example.ch:587" {
		t.Errorf("patch to a portless host: status=%d endpoint=%q, want 200 and :587", code, got.Endpoint)
	}
	if code, _ := patch(`{"endpoint":"bot@example.com"}`); code != http.StatusBadRequest {
		t.Errorf("patch to a mailbox: status=%d, want 400", code)
	}
	if code, _ := patch(`{"endpoint":""}`); code != http.StatusBadRequest {
		t.Errorf("patch to an empty endpoint: status=%d, want 400", code)
	}
	// The refused updates left the record as it was, endpoint included.
	recs, err := srv.connectors.LoadAll()
	if err != nil || len(recs) != 1 || recs[0].Endpoint != "mx9.example.ch:587" {
		t.Errorf("record after refused updates = %+v (err=%v), want the last good endpoint", recs, err)
	}
}

// TestPreviewConnectorNeedsNothingButASender: the point of the preview provider is
// that it asks for no host and no credential, and stores neither if one is sent.
func TestPreviewConnectorNeedsNothingButASender(t *testing.T) {
	srv, _ := newValidateServer(t)

	code, c := postConnector(t, srv, `{"name":"trial","kind":"mail","provider":"preview","sender":"bot@example.com"}`)
	if code != http.StatusOK {
		t.Fatalf("preview create: status=%d, want 200", code)
	}
	if c.Provider != mail.ProviderPreview || c.Endpoint != "" || c.CredentialsRef != "" {
		t.Errorf("preview record = %+v, want provider preview and no endpoint/credential", c)
	}
	// Configuration that cannot apply is dropped rather than stored as if it were used.
	code, c = postConnector(t, srv, `{"name":"trial2","kind":"mail","provider":"preview","sender":"bot@example.com","endpoint":"mx.example.ch:587","credentialsRef":"unused"}`)
	if code != http.StatusOK || c.Endpoint != "" || c.CredentialsRef != "" {
		t.Errorf("preview record = %+v (status=%d), want the dead configuration cleared", c, code)
	}
	if code, _ := postConnector(t, srv, `{"name":"trial3","kind":"mail","provider":"preview"}`); code != http.StatusBadRequest {
		t.Errorf("preview without a sender: status=%d, want 400", code)
	}
}

// TestPreviewConnectorDeliversIntoTheServerOutbox walks the whole seam the way the
// server does: a stored record becomes a client through buildMailClients, and what
// that client sends is readable over the outbox endpoint.
func TestPreviewConnectorDeliversIntoTheServerOutbox(t *testing.T) {
	srv, _ := newValidateServer(t)
	if code, _ := postConnector(t, srv, `{"name":"trial","kind":"mail","provider":"preview","sender":"bot@example.com"}`); code != http.StatusOK {
		t.Fatalf("preview create failed")
	}

	clients, err := srv.buildMailClients()
	if err != nil {
		t.Fatalf("buildMailClients: %v", err)
	}
	client, ok := clients["trial"]
	if !ok {
		t.Fatalf("preview connector produced no client (got %d clients)", len(clients))
	}
	if err := client.Send(context.Background(), mail.Message{To: []string{"her@example.com"}, Subject: "Hallo", Body: "Text"}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	get := func(path string) (int, struct {
		Messages []struct {
			Connector string `json:"connector"`
			Subject   string `json:"subject"`
			Raw       string `json:"raw"`
		} `json:"messages"`
		Truncated bool `json:"truncated"`
	}) {
		t.Helper()
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		var out struct {
			Messages []struct {
				Connector string `json:"connector"`
				Subject   string `json:"subject"`
				Raw       string `json:"raw"`
			} `json:"messages"`
			Truncated bool `json:"truncated"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}

	code, out := get("/api/v1/mail/outbox")
	if code != http.StatusOK || len(out.Messages) != 1 {
		t.Fatalf("outbox: status=%d messages=%d, want 200 and one message", code, len(out.Messages))
	}
	m := out.Messages[0]
	if m.Connector != "trial" || m.Subject != "Hallo" || !strings.Contains(m.Raw, "Subject: Hallo") {
		t.Errorf("outbox message = %+v, want the framed message from connector \"trial\"", m)
	}

	if code, _ := get("/api/v1/mail/outbox?limit=nope"); code != http.StatusBadRequest {
		t.Errorf("bad limit: status=%d, want 400", code)
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/v1/mail/outbox", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("clear outbox: status=%d, want 204", rec.Code)
	}
	if code, out := get("/api/v1/mail/outbox"); code != http.StatusOK || len(out.Messages) != 0 {
		t.Errorf("after clear: status=%d messages=%d, want 200 and none", code, len(out.Messages))
	}
}

// TestMisconfiguredMailConnectorIsSkippedNotFatal keeps the rebuild's posture: a
// record that cannot become a client parks its own tasks and leaves the others alone.
func TestMisconfiguredMailConnectorIsSkippedNotFatal(t *testing.T) {
	srv, _ := newValidateServer(t)
	// Saved straight to the store: this is the shape a record written before the
	// endpoint was validated at all still has on disk.
	_ = srv.connectors.Save(connector{ID: "1", Name: "legacy", Kind: "mail", Provider: "smtp", Endpoint: "mx1.example.ch", Sender: "a@x", Enabled: true, CreatedAt: 1})
	_ = srv.connectors.Save(connector{ID: "2", Name: "broken", Kind: "mail", Provider: "smtp", Endpoint: "bot@example.com", Sender: "b@x", Enabled: true, CreatedAt: 2})

	clients, err := srv.buildMailClients()
	if err != nil {
		t.Fatalf("buildMailClients: %v", err)
	}
	if _, ok := clients["legacy"]; !ok {
		t.Error("a stored portless endpoint did not heal into a client")
	}
	if _, ok := clients["broken"]; ok {
		t.Error("an unusable endpoint produced a client; want it skipped so its tasks park")
	}
}
