package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/pblumer/atlas/logging"
)

// The security audit trail, asserted where it is actually produced.
//
// What Atlas wrote down about itself was strong on the business side and silent
// on the security side: who signed in, who failed to, who changed an account or a
// credential appeared nowhere, and the ISDS concept's answer to R-13 had to be
// "the reverse proxy supplies it" — an answer about somebody else's software.

// syncBuffer is a log sink safe to write from handler goroutines and read from
// the test one.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// captureLog points the process logger at a buffer in JSON form — the shape an
// operator ships to a SIEM — and restores stderr afterwards.
func captureLog(t *testing.T) *syncBuffer {
	t.Helper()
	sink := &syncBuffer{}
	if err := logging.Setup(sink, logging.FormatJSON); err != nil {
		t.Fatalf("logging.Setup: %v", err)
	}
	t.Cleanup(func() { _ = logging.Setup(os.Stderr, logging.DefaultFormat) })
	return sink
}

// auditLines returns the captured records carrying the given event name.
func auditLines(t *testing.T, sink *syncBuffer, event string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(sink.String(), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue // a line from something that does not log through logging
		}
		if rec["event"] == event {
			out = append(out, rec)
		}
	}
	return out
}

// requireOneLine fails unless exactly one record carries the event, and returns it.
func requireOneLine(t *testing.T, sink *syncBuffer, event string) map[string]any {
	t.Helper()
	lines := auditLines(t, sink, event)
	if len(lines) != 1 {
		t.Fatalf("want exactly one %q record, got %d\n--- log ---\n%s", event, len(lines), sink.String())
	}
	return lines[0]
}

// TestAuditTrailRecordsTheAccountLifecycle walks one session end to end and holds
// the trail to what an audit asks for: who did it, to what, and from where.
func TestAuditTrailRecordsTheAccountLifecycle(t *testing.T) {
	sink := captureLog(t)
	ts, _ := newAuthServer(t, "root", "rootpassword")

	admin := newClient(t)
	if code := login(t, admin, ts, "root", "rootpassword"); code != http.StatusOK {
		t.Fatalf("login: got %d", code)
	}
	line := requireOneLine(t, sink, "auth.login")
	if line["username"] != "root" {
		t.Errorf("auth.login username = %v, want root", line["username"])
	}
	if line["client_ip"] == nil || line["client_ip"] == "" {
		t.Error("auth.login carries no client_ip")
	}
	if line["user_id"] == nil {
		t.Error("auth.login carries no user_id — a username can be reassigned, an id cannot")
	}

	// An administration action names the administrator, not only the subject.
	code, body := cReq(t, admin, ts, "POST", "/api/v1/users",
		`{"username":"alice","password":"alicepassword","roles":["user"]}`)
	if code != http.StatusCreated {
		t.Fatalf("create alice: got %d (%s)", code, body)
	}
	created := requireOneLine(t, sink, "auth.user_created")
	if created["actor"] != "root" {
		t.Errorf("auth.user_created actor = %v, want root — a change to an account must name who made it", created["actor"])
	}
	if created["username"] != "alice" {
		t.Errorf("auth.user_created username = %v, want alice", created["username"])
	}

	// A failed login says why, in the log only.
	if code, _ := cReq(t, newClient(t), ts, "POST", "/api/v1/auth/login",
		`{"username":"alice","password":"not-alices-password"}`); code != http.StatusUnauthorized {
		t.Fatalf("failed login: got %d, want 401", code)
	}
	failed := requireOneLine(t, sink, "auth.login_failed")
	if failed["reason"] != "wrong password" {
		t.Errorf("auth.login_failed reason = %v, want the wrong-password reason", failed["reason"])
	}
	if failed["username"] != "alice" {
		t.Errorf("auth.login_failed username = %v, want alice", failed["username"])
	}

	// An authorization refusal is recorded; being asked to log in is not.
	alice := newClient(t)
	if code := login(t, alice, ts, "alice", "alicepassword"); code != http.StatusOK {
		t.Fatalf("alice login: got %d", code)
	}
	if code, _ := cReq(t, alice, ts, "GET", "/api/v1/users", ""); code != http.StatusForbidden {
		t.Fatalf("alice listing users: got %d, want 403", code)
	}
	denied := requireOneLine(t, sink, "auth.denied")
	if denied["actor"] != "alice" {
		t.Errorf("auth.denied actor = %v, want alice", denied["actor"])
	}
	if denied["path"] != "/api/v1/users" {
		t.Errorf("auth.denied path = %v, want the path that was refused", denied["path"])
	}

	// Logging out is recorded, and names who — which is why it is emitted before
	// the cookie is cleared.
	if code, _ := cReq(t, alice, ts, "POST", "/api/v1/auth/logout", ""); code != http.StatusOK {
		t.Fatalf("logout: got %d", code)
	}
	if out := requireOneLine(t, sink, "auth.logout"); out["actor"] != "alice" {
		t.Errorf("auth.logout actor = %v, want alice", out["actor"])
	}
}

// TestAuditTrailNeverCarriesASecret is the standing guard on everything above. An
// attribute is exactly what a log shipper extracts, indexes and keeps, so a
// password or a hash reaching one is a credential in a SIEM with an extra step.
func TestAuditTrailNeverCarriesASecret(t *testing.T) {
	sink := captureLog(t)
	ts, _ := newAuthServer(t, "root", "hunter2-the-admin-password")

	admin := newClient(t)
	if code := login(t, admin, ts, "root", "hunter2-the-admin-password"); code != http.StatusOK {
		t.Fatalf("login: got %d", code)
	}
	if code, body := cReq(t, admin, ts, "POST", "/api/v1/users",
		`{"username":"alice","password":"correct-horse-battery-staple","roles":["user"]}`); code != http.StatusCreated {
		t.Fatalf("create alice: got %d (%s)", code, body)
	}
	if code, _ := cReq(t, admin, ts, "PUT", "/api/v1/users/nonexistent/password",
		`{"password":"another-secret-value"}`); code != http.StatusNotFound {
		t.Fatalf("set password on a missing user: got %d, want 404", code)
	}
	// A failed login, so the wrong password has passed through the handler too.
	cReq(t, newClient(t), ts, "POST", "/api/v1/auth/login",
		`{"username":"alice","password":"a-guessed-password"}`)

	// Mint a machine credential: the secret is returned to the caller exactly once
	// and must not also be sitting in the log.
	code, body := cReq(t, admin, ts, "POST", "/api/v1/deploy-tokens", `{"name":"a peer"}`)
	if code != http.StatusOK {
		t.Fatalf("mint deploy token: got %d (%s)", code, body)
	}
	var minted struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &minted); err != nil || minted.Token == "" {
		t.Fatalf("no token in %s (%v)", body, err)
	}
	if len(auditLines(t, sink, "auth.token_minted")) != 1 {
		t.Error("minting a deploy token was not recorded")
	}

	log := sink.String()
	for _, secret := range []string{
		"hunter2-the-admin-password",
		"correct-horse-battery-staple",
		"another-secret-value",
		"a-guessed-password",
		minted.Token,
		"$2a$", // a bcrypt digest prefix: a hash in a log is a credential in a log
	} {
		if strings.Contains(log, secret) {
			t.Errorf("the log contains a secret (%q)", secret)
		}
	}
}
