package remedymock_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/remedy"
	remedymock "github.com/pblumer/atlas/connector/remedy/mock"
)

// TestRoundTripThroughRealClient is the whole point of the mock: the real Remedy
// connector client (login → create → logout) talks to it unmodified and gets a
// generated entry id back, and the mock records exactly what the client sent.
func TestRoundTripThroughRealClient(t *testing.T) {
	mock := remedymock.New()
	srv := httptest.NewServer(mock.Handler())
	defer srv.Close()

	client := remedy.NewHTTPClient(remedy.Connector{BaseURL: srv.URL, Username: "svc", Password: "pw"})
	res, err := client.CreateEntry(context.Background(), remedy.Entry{
		Form:      "HPD:IncidentInterface_Create",
		Values:    map[string]any{"Description": "Disk full", "Impact": "2-Significant"},
		RequestID: "42",
	})
	if err != nil {
		t.Fatalf("CreateEntry: %v", err)
	}
	if res.EntryID != "INC000000000001" {
		t.Errorf("EntryID = %q, want the first generated incident id", res.EntryID)
	}
	entries := mock.Entries()
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	e := entries[0]
	if e.ID != "INC000000000001" || e.Form != "HPD:IncidentInterface_Create" {
		t.Errorf("entry id/form = %q / %q", e.ID, e.Form)
	}
	if e.Values["Description"] != "Disk full" || e.Values["Impact"] != "2-Significant" {
		t.Errorf("entry values = %v, want the two fields the client sent", e.Values)
	}
	if e.RequestID != "42" {
		t.Errorf("entry requestId = %q, want the client's X-Request-ID", e.RequestID)
	}
}

// TestReplayCreatesDuplicate proves the mock is faithful to at-least-once semantics:
// there is no idempotency de-duplication, so a replayed create (same X-Request-ID)
// makes a second entry.
func TestReplayCreatesDuplicate(t *testing.T) {
	mock := remedymock.New()
	srv := httptest.NewServer(mock.Handler())
	defer srv.Close()
	client := remedy.NewHTTPClient(remedy.Connector{BaseURL: srv.URL, Username: "u", Password: "p"})
	for i := 0; i < 2; i++ {
		if _, err := client.CreateEntry(context.Background(), remedy.Entry{Form: "F", RequestID: "same"}); err != nil {
			t.Fatalf("CreateEntry %d: %v", i, err)
		}
	}
	if got := mock.Entries(); len(got) != 2 || got[0].ID == got[1].ID {
		t.Errorf("entries = %+v, want two entries with distinct ids", got)
	}
}

// TestConfiguredCredentials proves WithCredentials enforces an exact match: the right
// login succeeds, a wrong one fails the client's create at the login step.
func TestConfiguredCredentials(t *testing.T) {
	mock := remedymock.New(remedymock.WithCredentials("svc", "s3cret"))
	srv := httptest.NewServer(mock.Handler())
	defer srv.Close()

	ok := remedy.NewHTTPClient(remedy.Connector{BaseURL: srv.URL, Username: "svc", Password: "s3cret"})
	if _, err := ok.CreateEntry(context.Background(), remedy.Entry{Form: "F"}); err != nil {
		t.Fatalf("create with correct credentials: %v", err)
	}
	bad := remedy.NewHTTPClient(remedy.Connector{BaseURL: srv.URL, Username: "svc", Password: "wrong"})
	if _, err := bad.CreateEntry(context.Background(), remedy.Entry{Form: "F"}); err == nil {
		t.Error("create with wrong credentials: want an error, got nil")
	}
}

// TestAcceptAnyRejectsEmpty proves the zero-config accept-any mode still requires a
// non-empty username and password.
func TestAcceptAnyRejectsEmpty(t *testing.T) {
	mock := remedymock.New()
	srv := httptest.NewServer(mock.Handler())
	defer srv.Close()
	empty := remedy.NewHTTPClient(remedy.Connector{BaseURL: srv.URL}) // no username/password
	if _, err := empty.CreateEntry(context.Background(), remedy.Entry{Form: "F"}); err == nil {
		t.Error("create with empty credentials: want an error, got nil")
	}
}

// TestIDPrefix proves WithIDPrefix drives the generated id shape.
func TestIDPrefix(t *testing.T) {
	mock := remedymock.New(remedymock.WithIDPrefix("CRQ"))
	srv := httptest.NewServer(mock.Handler())
	defer srv.Close()
	client := remedy.NewHTTPClient(remedy.Connector{BaseURL: srv.URL, Username: "u", Password: "p"})
	res, err := client.CreateEntry(context.Background(), remedy.Entry{Form: "CHG:Infrastructure Change"})
	if err != nil {
		t.Fatalf("CreateEntry: %v", err)
	}
	if !strings.HasPrefix(res.EntryID, "CRQ") {
		t.Errorf("EntryID = %q, want the CRQ prefix", res.EntryID)
	}
}

// login logs in through the handler and returns the issued token, so the direct-handler
// tests below can reach the create path.
func login(t *testing.T, h http.Handler) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/jwt/login", strings.NewReader("username=u&password=p"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login: status %d, want 200", rec.Code)
	}
	token := strings.TrimSpace(rec.Body.String())
	if token == "" {
		t.Fatal("login returned an empty token")
	}
	return token
}

// TestCreateRejectsMissingToken proves the create endpoint refuses a request with no
// (or an unissued) AR-JWT token.
func TestCreateRejectsMissingToken(t *testing.T) {
	h := remedymock.New().Handler()
	req := httptest.NewRequest(http.MethodPost, "/api/arsys/v1/entry/F", strings.NewReader(`{"values":{}}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("create without a token: status %d, want 401", rec.Code)
	}
	// An unissued token is rejected too.
	req = httptest.NewRequest(http.MethodPost, "/api/arsys/v1/entry/F", strings.NewReader(`{"values":{}}`))
	req.Header.Set("Authorization", "AR-JWT not-a-real-token")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("create with a bogus token: status %d, want 401", rec.Code)
	}
}

// TestCreateRejectsBadJSON proves a malformed body is a 400 (a valid token in hand).
func TestCreateRejectsBadJSON(t *testing.T) {
	mock := remedymock.New()
	h := mock.Handler()
	token := login(t, h)
	req := httptest.NewRequest(http.MethodPost, "/api/arsys/v1/entry/F", strings.NewReader(`{not json`))
	req.Header.Set("Authorization", "AR-JWT "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("create with bad JSON: status %d, want 400", rec.Code)
	}
}

// TestLogoutInvalidatesToken proves a logged-out token no longer authorizes a create.
func TestLogoutInvalidatesToken(t *testing.T) {
	h := remedymock.New().Handler()
	token := login(t, h)
	// Log out.
	req := httptest.NewRequest(http.MethodPost, "/api/jwt/logout", nil)
	req.Header.Set("Authorization", "AR-JWT "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("logout: status %d, want 204", rec.Code)
	}
	// The create now fails with the released token.
	req = httptest.NewRequest(http.MethodPost, "/api/arsys/v1/entry/F", strings.NewReader(`{"values":{}}`))
	req.Header.Set("Authorization", "AR-JWT "+token)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("create after logout: status %d, want 401", rec.Code)
	}
}

// TestEntriesEndpoint proves the inspection endpoint returns the created entries as
// JSON.
func TestEntriesEndpoint(t *testing.T) {
	mock := remedymock.New()
	srv := httptest.NewServer(mock.Handler())
	defer srv.Close()
	client := remedy.NewHTTPClient(remedy.Connector{BaseURL: srv.URL, Username: "u", Password: "p"})
	if _, err := client.CreateEntry(context.Background(), remedy.Entry{Form: "HPD:Help Desk", Values: map[string]any{"Summary": "hi"}}); err != nil {
		t.Fatalf("CreateEntry: %v", err)
	}
	resp, err := http.Get(srv.URL + "/mock/entries")
	if err != nil {
		t.Fatalf("GET /mock/entries: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var got []remedymock.Entry
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode entries: %v (body %s)", err, body)
	}
	if len(got) != 1 || got[0].Form != "HPD:Help Desk" || got[0].Values["Summary"] != "hi" {
		t.Errorf("entries = %+v, want the one created entry", got)
	}
}

// TestMissingForm proves an empty form path segment is a 400 (with a valid token).
func TestMissingForm(t *testing.T) {
	mock := remedymock.New()
	h := mock.Handler()
	token := login(t, h)
	req := httptest.NewRequest(http.MethodPost, "/api/arsys/v1/entry/", strings.NewReader(`{"values":{}}`))
	req.Header.Set("Authorization", "AR-JWT "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("create with an empty form: status %d, want 400", rec.Code)
	}
}
