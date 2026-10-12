package api

import (
	"bufio"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A mail Worker with no credential reference is an internal relay that admits this
// server by its network address. The Console's check used to present the sender with
// an empty password anyway, and came back with the relay's "535 5.7.8 authentication
// failed" — so the relay could not be configured at all. These tests hold the two
// halves of the fix: no reference means no login, and a reference that resolves to
// nothing says so instead of quietly becoming no login.

// startLoginRefusingRelay serves just enough SMTP for a check: it offers AUTH and
// refuses every login, as the relay that prompted this did. It reports whether a
// login was attempted.
func startLoginRefusingRelay(t *testing.T) (addr string, sawAuth *atomic.Bool) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	sawAuth = &atomic.Bool{}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer func() { _ = conn.Close() }()
				_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
				r := bufio.NewReader(conn)
				say := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }
				say("220 relay ESMTP")
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					switch cmd := strings.ToUpper(strings.TrimSpace(line)); {
					case strings.HasPrefix(cmd, "EHLO"):
						say("250-relay greets you")
						say("250 AUTH PLAIN LOGIN")
					case strings.HasPrefix(cmd, "AUTH"):
						sawAuth.Store(true)
						say("535 5.7.8 Error: authentication failed: ")
					case cmd == "QUIT":
						say("221 bye")
						return
					default:
						say("250 ok")
					}
				}
			}(conn)
		}
	}()
	return ln.Addr().String(), sawAuth
}

// checkMailWorker posts a worker check and decodes its verdict.
func checkMailWorker(t *testing.T, srv *Server, body string) (ok bool, detail string) {
	t.Helper()
	code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/connectors/test", body, "application/json")
	if code != http.StatusOK {
		t.Fatalf("check: %d (%s)", code, raw)
	}
	var got struct {
		OK     bool   `json:"ok"`
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, raw)
	}
	return got.OK, got.Detail
}

// TestCheckingAMailWorkerWithoutACredentialDoesNotLogIn is the report: the check of a
// Worker with no credential reference reaches the relay without logging in, and says
// that it did, so nobody reads "authenticated" into a check that presented nothing.
func TestCheckingAMailWorkerWithoutACredentialDoesNotLogIn(t *testing.T) {
	srv := newServerForErrors(t)
	relay, sawAuth := startLoginRefusingRelay(t)

	ok, detail := checkMailWorker(t, srv,
		`{"kind":"mail","provider":"smtp","name":"relay","endpoint":"`+relay+`","sender":"bot@example.com"}`)
	if !ok {
		t.Fatalf("check failed: %s — a Worker with no credential still tried to log in", detail)
	}
	if sawAuth.Load() {
		t.Error("the check sent AUTH although the Worker has no credential to present")
	}
	if !strings.Contains(detail, "without logging in") {
		t.Errorf("detail = %q, want it to say the check did not log in", detail)
	}
}

// TestCheckingAMailWorkerNamesACredentialReferenceThatResolvesToNothing: somebody
// meant this Worker to log in, so a reference that resolves to nothing — a typo, or
// a secret never stored — is named, with both places it was looked for, before
// anything is dialled. It must not become a check that quietly does not log in.
func TestCheckingAMailWorkerNamesACredentialReferenceThatResolvesToNothing(t *testing.T) {
	srv := newServerForErrors(t)
	relay, sawAuth := startLoginRefusingRelay(t)

	ok, detail := checkMailWorker(t, srv,
		`{"kind":"mail","provider":"smtp","name":"relay","endpoint":"`+relay+`","sender":"bot@example.com","credentialsRef":"smtp_pw_typo"}`)
	if ok {
		t.Fatalf("check passed with a credential reference that resolves to nothing: %s", detail)
	}
	for _, want := range []string{`"smtp_pw_typo"`, "ATLAS_CONNECTOR_SMTP_PW_TYPO_TOKEN"} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail = %q, want it to name %s", detail, want)
		}
	}
	if sawAuth.Load() {
		t.Error("the check dialled and logged in with a credential it never found")
	}
}

// TestAMailWorkerWhoseCredentialResolvesToNothingIsNotBuilt: the same rule where
// tasks are served. A Worker without a reference is built and sends without logging
// in; one whose reference resolves to nothing is left out with the reason its parked
// tasks report, rather than built as a relay nobody configured.
func TestAMailWorkerWhoseCredentialResolvesToNothingIsNotBuilt(t *testing.T) {
	srv, _ := newValidateServer(t)
	for _, c := range []connector{
		{ID: "1", Name: "relay", Kind: connectorKindMail, Provider: "smtp", Endpoint: "relay.example.ch:25", Sender: "bot@example.ch", Enabled: true, CreatedAt: 1},
		{ID: "2", Name: "typo", Kind: connectorKindMail, Provider: "smtp", Endpoint: "mx.example.ch:587", Sender: "bot@example.ch", CredentialsRef: "smtp_pw_typo", Enabled: true, CreatedAt: 2},
	} {
		if err := srv.connectors.Save(c); err != nil {
			t.Fatalf("Save(%s): %v", c.Name, err)
		}
	}
	clients, problems, err := srv.buildMailClients()
	if err != nil {
		t.Fatalf("buildMailClients: %v", err)
	}
	if _, ok := clients["relay"]; !ok {
		t.Errorf("the relay Worker was not built; problem: %q", problems["relay"])
	}
	if _, ok := clients["typo"]; ok {
		t.Error("a Worker whose credential reference resolves to nothing was built")
	}
	if why := problems["typo"]; !strings.Contains(why, `"smtp_pw_typo"`) {
		t.Errorf("problem = %q, want it to name the reference", why)
	}
}

// TestASupervisedWorkerIsNotHandedAMailWorkerWhoseCredentialResolvesToNothing: a
// Worker Instance builds its client from what it is handed, and a missing SECRET
// there now means "do not log in". So a Worker whose reference resolves to nothing is
// not handed over at all, as an unusable Jira or Remedy Worker is not — while a relay
// configured without a reference is, with no SECRET variable.
func TestASupervisedWorkerIsNotHandedAMailWorkerWhoseCredentialResolvesToNothing(t *testing.T) {
	srv, _ := newValidateServer(t, WithSupervisedWorkers("http://s", nil, nil))
	for _, c := range []connector{
		{ID: "1", Name: "relay", Kind: connectorKindMail, Provider: "smtp", Endpoint: "relay.example.ch:25", Sender: "bot@example.ch", Enabled: true, CreatedAt: 1},
		{ID: "2", Name: "typo", Kind: connectorKindMail, Provider: "smtp", Endpoint: "mx.example.ch:587", Sender: "bot@example.ch", CredentialsRef: "smtp_pw_typo", Enabled: true, CreatedAt: 2},
	} {
		if err := srv.connectors.Save(c); err != nil {
			t.Fatalf("Save(%s): %v", c.Name, err)
		}
	}
	env := envOf(t, srv.mailWorkerEnv())
	if got := env["ATLAS_MAIL_CONNECTORS"]; got != "relay" {
		t.Errorf("ATLAS_MAIL_CONNECTORS = %q, want only the relay", got)
	}
	if _, ok := env["ATLAS_MAIL_RELAY_SECRET"]; ok {
		t.Error("the relay was handed a SECRET variable although it has no credential")
	}
	for name := range env {
		if strings.HasPrefix(name, "ATLAS_MAIL_TYPO_") {
			t.Errorf("the Worker whose credential resolves to nothing was handed %s", name)
		}
	}
}
