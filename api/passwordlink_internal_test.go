package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/connector/mail"
)

// A password is set by its owner, through a one-time link
// (ADR-0526). These tests drive the shipped system
// processes end to end on a server with a preview mail Worker and an external URL.

const passwordTestOrigin = "https://atlas.example.ch"

// passwordServer is a server running the system processes with user provisioning, an
// external URL and a preview mail Worker named "mail" — what registration and recovery
// need.
func passwordServer(t *testing.T, opts ...Option) *Server {
	t.Helper()
	srv, _ := newSystemServer(t, append([]Option{WithSystemProcesses(), WithUserProvisioning(), WithExternalURL(passwordTestOrigin)}, opts...)...)
	code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/configured-workers",
		`{"name":"mail","kind":"mail","provider":"preview","sender":"atlas@example.ch"}`, "application/json")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create the mail worker: %d %s", code, raw)
	}
	return srv
}

// bearer serves one request carrying an Authorization header.
func bearer(t *testing.T, srv *Server, method, path, token, body string) (int, []byte) {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, r)
	return rec.Code, rec.Body.Bytes()
}

// mailsTo is every outbox message addressed to addr, oldest first.
func mailsTo(srv *Server, addr string) []mail.OutboxMessage {
	msgs, _ := srv.mailOutbox.Messages(0)
	var out []mail.OutboxMessage
	for i := len(msgs) - 1; i >= 0; i-- {
		if len(msgs[i].To) == 1 && msgs[i].To[0] == addr {
			out = append(out, msgs[i])
		}
	}
	return out
}

// tokenAfter returns the token that follows prefix in text.
func tokenAfter(t *testing.T, text, prefix string) string {
	t.Helper()
	i := strings.Index(text, prefix)
	if i < 0 {
		t.Fatalf("no %q in %q", prefix, text)
	}
	return strings.Fields(text[i+len(prefix):])[0]
}

// startIntake starts proc_benutzer_aufnahme for Neuer Kollege.
func startIntake(t *testing.T, srv *Server) {
	t.Helper()
	var key uint64
	for _, r := range listProcesses(t, srv.Handler()) {
		if r.ProcessID == "proc_benutzer_aufnahme" {
			key = r.Key
		}
	}
	if key == 0 {
		t.Fatal("intake process not deployed")
	}
	startInstance(t, srv.Handler(), key, `{"vorname":"Neuer","nachname":"Kollege","email":"neuer@example.org","abteilung":"Informatik","begruendung":"Projektmitarbeit"}`)
}

// confirmIntake answers the confirmation task through the link the confirmation mail
// carries, as the requester does.
func confirmIntake(t *testing.T, srv *Server) {
	t.Helper()
	msgs := mailsTo(srv, "neuer@example.org")
	if len(msgs) != 1 {
		t.Fatalf("after the start the requester has %d mail(s), want the one confirmation", len(msgs))
	}
	tok := tokenAfter(t, msgs[0].Body, passwordTestOrigin+answerPagePath+"#")
	code, raw := bearer(t, srv, http.MethodGet, answerPagePath+"/task", tok, "")
	if code != http.StatusOK {
		t.Fatalf("open the confirmation: %d %s", code, raw)
	}
	var got answerResp
	if err := json.Unmarshal(raw, &got); err != nil || got.Step == nil {
		t.Fatalf("decode the confirmation step: %v (%s)", err, raw)
	}
	code, raw = bearer(t, srv, http.MethodPost, fmt.Sprintf("%s/task/%d/complete", answerPagePath, got.Step.TaskKey), tok, `{"variables":{}}`)
	if code != http.StatusOK {
		t.Fatalf("confirm: %d %s", code, raw)
	}
}

// openTask returns the key of the open task at elementID of the intake, or 0.
func openTask(t *testing.T, srv *Server, elementID string) uint64 {
	t.Helper()
	code, raw := serveInternal(t, srv, http.MethodGet, "/api/v1/tasks", "", "")
	if code != http.StatusOK {
		t.Fatalf("list tasks: %d %s", code, raw)
	}
	var tasks []struct {
		Key       uint64 `json:"key"`
		ProcessID string `json:"processId"`
		ElementID string `json:"elementId"`
	}
	if err := json.Unmarshal(listRows(t, raw), &tasks); err != nil {
		t.Fatalf("decode tasks: %v (%s)", err, raw)
	}
	for _, it := range tasks {
		if it.ProcessID == "proc_benutzer_aufnahme" && it.ElementID == elementID {
			return it.Key
		}
	}
	return 0
}

// approveIntake completes "Antrag freigeben" with the admin's decision.
func approveIntake(t *testing.T, srv *Server) {
	t.Helper()
	key := openTask(t, srv, "freigabe")
	if key == 0 {
		t.Fatal("the intake does not wait at freigabe")
	}
	code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/tasks/"+strconv.FormatUint(key, 10)+"/complete",
		`{"variables":{"rolle":"user","benutzername":"neuer.kollege","entscheidung":"anlegen"}}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("complete freigabe: %d %s", code, raw)
	}
}

// TestRegistrationNeverHandlesAPassword drives the intake from the request to the
// first sign-in: the address is confirmed through its link before anybody approves,
// the approved account has no password, its owner is mailed an activation link —
// which the preview outbox withholds — and sets the password through it. At no step
// does a password appear in a variable.
func TestRegistrationNeverHandlesAPassword(t *testing.T) {
	srv := passwordServer(t)
	startIntake(t, srv)

	if k := openTask(t, srv, "freigabe"); k != 0 {
		t.Fatal("the approval waits before the address was confirmed")
	}
	confirmIntake(t, srv)
	approveIntake(t, srv)

	u, ok, err := srv.users.byUsername("neuer.kollege")
	if err != nil || !ok {
		t.Fatalf("the intake did not provision the account (ok=%v err=%v)", ok, err)
	}
	if u.PasswordHash != "" {
		t.Fatal("the provisioned account carries a password; nobody but its owner may choose one")
	}

	// The activation mail went to the account's address, and the preview kept no link.
	msgs := mailsTo(srv, "neuer@example.org")
	if len(msgs) != 2 {
		t.Fatalf("the requester has %d mails, want the confirmation and the activation", len(msgs))
	}
	act := msgs[1]
	if strings.Contains(act.Body, passwordPagePath+"#") || strings.Contains(act.Raw, passwordPagePath+"#") {
		t.Fatalf("the preview outbox kept the password link: %q", act.Body)
	}
	if !strings.Contains(act.Body, mail.WithheldText) {
		t.Fatalf("the activation mail does not say its link was withheld: %q", act.Body)
	}

	// No variable of any instance carries a password or a link.
	code, raw := serveInternal(t, srv, http.MethodGet, "/api/v1/instances?limit=50", "", "")
	if code != http.StatusOK {
		t.Fatalf("list instances: %d %s", code, raw)
	}
	for _, needle := range []string{"passwort", "password", passwordPagePath} {
		if strings.Contains(strings.ToLower(string(raw)), needle) {
			t.Errorf("an instance carries %q: %s", needle, raw)
		}
	}

	// The owner sets the password through the link the server would have mailed.
	tok, _, err := srv.passwordToken(u, time.Now())
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	code, raw = bearer(t, srv, http.MethodGet, passwordPagePath+"/link", tok, "")
	if code != http.StatusOK || !strings.Contains(string(raw), `"purpose":"activate"`) {
		t.Fatalf("open the activation link: %d %s", code, raw)
	}
	code, raw = bearer(t, srv, http.MethodPost, passwordPagePath+"/link", tok, `{"password":"meinGeheimnis1"}`)
	if code != http.StatusOK {
		t.Fatalf("set the password: %d %s", code, raw)
	}
	u, _, _ = srv.users.byUsername("neuer.kollege")
	if !checkPassword(u.PasswordHash, "meinGeheimnis1") {
		t.Fatal("the password set through the link does not verify")
	}
	// One time: the same link opens nothing any more.
	if code, _ := bearer(t, srv, http.MethodPost, passwordPagePath+"/link", tok, `{"password":"nochEinmal22"}`); code != http.StatusNotFound {
		t.Fatalf("a used link set a password again: %d", code)
	}
}

// TestPasswordLinkRefusals: every link that should open nothing answers 404 alike —
// a forged MAC, an expired one, one minted before the address changed, one of a
// disabled account — and a link scanner's GET changes nothing.
func TestPasswordLinkRefusals(t *testing.T) {
	srv := passwordServer(t)
	now := time.Now()
	u := User{ID: "usr_0123456789abcdef01234567", Username: "erika", Email: "erika@example.org", Source: SourceLocal, Roles: []string{RoleUser}}
	if err := srv.users.Save(u); err != nil {
		t.Fatal(err)
	}
	good, _, err := srv.passwordToken(u, now)
	if err != nil {
		t.Fatal(err)
	}
	// The scanner opens it twice; nothing changes.
	for range 2 {
		if code, _ := bearer(t, srv, http.MethodGet, passwordPagePath+"/link", good, ""); code != http.StatusOK {
			t.Fatalf("a good link does not open: %d", code)
		}
	}
	if cur, _, _ := srv.users.Get(u.ID); cur.PasswordHash != "" {
		t.Fatal("opening the link changed the account")
	}

	parts := strings.Split(good, ".")
	forged := parts[0] + "." + parts[1] + "." + strings.Repeat("0", len(parts[2]))
	expired, _, _ := srv.passwordToken(u, now.Add(-activationLinkTTL-time.Minute))
	for name, tok := range map[string]string{
		"forged mac": forged, "expired": expired, "garbage": "x.y.z", "empty": "",
	} {
		if code, _ := bearer(t, srv, http.MethodGet, passwordPagePath+"/link", tok, ""); code != http.StatusNotFound {
			t.Errorf("%s: GET = %d, want 404", name, code)
		}
	}

	// An address corrected after the mail kills the link sent to the old one.
	u.Email = "erika.neu@example.org"
	if err := srv.users.Save(u); err != nil {
		t.Fatal(err)
	}
	if code, _ := bearer(t, srv, http.MethodGet, passwordPagePath+"/link", good, ""); code != http.StatusNotFound {
		t.Errorf("a link minted for the old address still opens: %d", code)
	}
	// A disabled account opens nothing, and mints nothing.
	u.Disabled = true
	_ = srv.users.Save(u)
	fresh, _, err := srv.passwordToken(u, now)
	if err == nil || fresh != "" {
		t.Error("a disabled account was minted a link")
	}

	// A password that breaks the rule is refused before the link is spent.
	u.Disabled = false
	_ = srv.users.Save(u)
	tok, _, _ := srv.passwordToken(u, now)
	if code, raw := bearer(t, srv, http.MethodPost, passwordPagePath+"/link", tok, `{"password":"kurz"}`); code != http.StatusBadRequest {
		t.Errorf("a short password: %d %s, want 400", code, raw)
	}
	if code, _ := bearer(t, srv, http.MethodPost, passwordPagePath+"/link", tok, `{"password":"erika"}`); code != http.StatusBadRequest {
		t.Errorf("the username as password: %d, want 400", code)
	}
	if code, _ := bearer(t, srv, http.MethodGet, passwordPagePath+"/link", tok, ""); code != http.StatusOK {
		t.Error("a refused password spent the link")
	}
}

// TestForgotPasswordAnswersAlike: the form answers 202 with the same body whether the
// input names an account or not, and mails only an enabled local account with an
// address — once, however often it is asked within the cooldown.
func TestForgotPasswordAnswersAlike(t *testing.T) {
	srv := passwordServer(t)
	for _, u := range []User{
		{ID: "usr_a00000000000000000000001", Username: "anna", Email: "anna@example.org", Source: SourceLocal, PasswordHash: mustHash(t, "altesPasswort1")},
		{ID: "usr_a00000000000000000000002", Username: "gesperrt", Email: "gesperrt@example.org", Source: SourceLocal, Disabled: true},
		{ID: "usr_a00000000000000000000003", Username: "extern", Email: "extern@example.org", Source: SourceOIDC},
		{ID: "usr_a00000000000000000000004", Username: "ohneadresse", Source: SourceLocal},
	} {
		if err := srv.users.Save(u); err != nil {
			t.Fatal(err)
		}
	}
	// The per-address budget is its own test below; here every answer is compared.
	srv.recoveryRate = newRateLimiter(100, 1)
	var bodies []string
	for _, login := range []string{"anna", "niemand", "gesperrt", "extern", "ohneadresse", "anna@example.org", "anna"} {
		code, raw := bearer(t, srv, http.MethodPost, passwordPagePath+"/forgot", "", `{"login":"`+login+`"}`)
		if code != http.StatusAccepted {
			t.Fatalf("forgot %q: %d %s", login, code, raw)
		}
		bodies = append(bodies, string(raw))
	}
	for _, b := range bodies[1:] {
		if b != bodies[0] {
			t.Fatalf("the answers differ: %q vs %q", b, bodies[0])
		}
	}
	if got := len(mailsTo(srv, "anna@example.org")); got != 1 {
		t.Errorf("anna was mailed %d times, want once (the cooldown)", got)
	}
	for _, addr := range []string{"gesperrt@example.org", "extern@example.org"} {
		if got := len(mailsTo(srv, addr)); got != 0 {
			t.Errorf("%s was mailed %d times, want never", addr, got)
		}
	}
	msg := mailsTo(srv, "anna@example.org")[0]
	if !strings.Contains(msg.Subject, mail.WithheldText) || !strings.Contains(msg.Body, mail.WithheldText) {
		t.Errorf("the recovery mail = %q / %q", msg.Subject, msg.Body)
	}
	// Asking changed nothing: the old password still works until a new one is set.
	if u, _, _ := srv.users.byUsername("anna"); !checkPassword(u.PasswordHash, "altesPasswort1") {
		t.Error("asking for a link changed the password")
	}

	code, raw := serveInternal(t, srv, http.MethodGet, "/api/v1/auth/password-recovery", "", "")
	if code != http.StatusOK || !strings.Contains(string(raw), `"enabled":true`) {
		t.Errorf("password-recovery = %d %s, want enabled", code, raw)
	}
}

// TestForgotPasswordIsThrottledPerAddress: the form is a way to make the server send
// mail, so one address gets a burst and then waits.
func TestForgotPasswordIsThrottledPerAddress(t *testing.T) {
	srv := passwordServer(t)
	throttled := false
	for range 10 {
		if code, _ := bearer(t, srv, http.MethodPost, passwordPagePath+"/forgot", "", `{"login":"niemand"}`); code == http.StatusTooManyRequests {
			throttled = true
			break
		}
	}
	if !throttled {
		t.Fatal("ten requests from one address in a row were all accepted")
	}
}

// TestPasswordLinkMailGoesToTheOwnerOnly: the server refuses to mint a link for a mail
// that goes anywhere but to the account's own address, or from a process outside the
// system project.
func TestPasswordLinkMailGoesToTheOwnerOnly(t *testing.T) {
	srv := passwordServer(t)
	u := User{ID: "usr_b00000000000000000000001", Username: "bob", Email: "Bob@Example.org", Source: SourceLocal}
	if err := srv.users.Save(u); err != nil {
		t.Fatal(err)
	}
	d := mailDirectory{srv}
	var sysKey uint64
	srv.do(func() {
		if dep := srv.latestDeploymentOf(passwordLinkProcessID); dep != nil {
			sysKey = dep.Key
		}
	})
	if sysKey == 0 {
		t.Fatal("the system process proc_passwort_link is not deployed")
	}
	if _, err := d.PasswordLink(sysKey, passwordLinkProcessID, "bob", []string{"bob@example.org"}, nil, nil); err != nil {
		t.Errorf("to the owner: %v", err)
	}
	for name, c := range map[string]struct {
		key         uint64
		proc        string
		to, cc, bcc []string
	}{
		"another address":      {sysKey, passwordLinkProcessID, []string{"mallory@example.org"}, nil, nil},
		"a second recipient":   {sysKey, passwordLinkProcessID, []string{"bob@example.org", "mallory@example.org"}, nil, nil},
		"a copy":               {sysKey, passwordLinkProcessID, []string{"bob@example.org"}, []string{"mallory@example.org"}, nil},
		"a blind copy":         {sysKey, passwordLinkProcessID, []string{"bob@example.org"}, nil, []string{"mallory@example.org"}},
		"not a system process": {0, "tenant_proc", []string{"bob@example.org"}, nil, nil},
		// The id alone is not enough: a definition that is not the system project's
		// mints nothing, whatever it calls its process.
		"a system process id elsewhere": {sysKey + 1_000_000, passwordLinkProcessID, []string{"bob@example.org"}, nil, nil},
	} {
		if _, err := d.PasswordLink(c.key, c.proc, "bob", c.to, c.cc, c.bcc); err == nil {
			t.Errorf("%s: a link was minted", name)
		}
	}
}

// TestChangeOwnPassword: a signed-in person changes their password with the current
// one; their other sessions end, the one they changed it in stays.
func TestChangeOwnPassword(t *testing.T) {
	srv, _ := newSystemServer(t, WithAuth())
	u := User{ID: "usr_c00000000000000000000001", Username: "carla", Source: SourceLocal, Roles: []string{RoleUser}, PasswordHash: mustHash(t, "altesPasswort1")}
	if err := srv.users.Save(u); err != nil {
		t.Fatal(err)
	}
	here, err := srv.sessions.create(u, nil)
	if err != nil {
		t.Fatal(err)
	}
	elsewhere, _ := srv.sessions.create(u, nil)

	change := func(body string) (int, []byte) {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: here})
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, r)
		return rec.Code, rec.Body.Bytes()
	}
	if code, raw := change(`{"currentPassword":"falsch","newPassword":"neuesPasswort2"}`); code != http.StatusBadRequest {
		t.Fatalf("a wrong current password: %d %s, want 400", code, raw)
	}
	if code, raw := change(`{"currentPassword":"altesPasswort1","newPassword":"kurz"}`); code != http.StatusBadRequest {
		t.Fatalf("a short new password: %d %s, want 400", code, raw)
	}
	if code, raw := change(`{"currentPassword":"altesPasswort1","newPassword":"neuesPasswort2"}`); code != http.StatusOK {
		t.Fatalf("change: %d %s", code, raw)
	}
	cur, _, _ := srv.users.Get(u.ID)
	if !checkPassword(cur.PasswordHash, "neuesPasswort2") {
		t.Fatal("the new password does not verify")
	}
	if _, ok := srv.sessions.lookup(here); !ok {
		t.Error("the session the password was changed in ended")
	}
	if _, ok := srv.sessions.lookup(elsewhere); ok {
		t.Error("another session of the account survived the change")
	}
}

// TestAdminHandsOverALinkNotAPassword: an administrator takes the link for an account
// awaiting activation; it opens on the page, and the account shows as awaiting
// activation until it is used.
func TestAdminHandsOverALinkNotAPassword(t *testing.T) {
	srv := passwordServer(t)
	code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/users",
		`{"username":"dora","email":"dora@example.org","roles":["user"]}`, "application/json")
	if code != http.StatusCreated || !strings.Contains(string(raw), `"awaitingActivation":true`) {
		t.Fatalf("create without a password: %d %s", code, raw)
	}
	var created publicUser
	_ = json.Unmarshal(raw, &created)

	code, raw = serveInternal(t, srv, http.MethodPost, "/api/v1/users/"+created.ID+"/password-link", `{}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("take the link: %d %s", code, raw)
	}
	var link struct {
		URL, Path string
	}
	_ = json.Unmarshal(raw, &link)
	if !strings.HasPrefix(link.URL, passwordTestOrigin+passwordPagePath+"#") {
		t.Fatalf("link = %+v", link)
	}
	tok := strings.TrimPrefix(link.Path, passwordPagePath+"#")
	if code, _ := bearer(t, srv, http.MethodPost, passwordPagePath+"/link", tok, `{"password":"doraGeheim11"}`); code != http.StatusOK {
		t.Fatalf("use the handed-over link: %d", code)
	}

	// Sending goes through the system process, and the preview keeps no link.
	code, raw = serveInternal(t, srv, http.MethodPost, "/api/v1/users/"+created.ID+"/password-link", `{"send":true}`, "application/json")
	if code != http.StatusAccepted {
		t.Fatalf("send the link: %d %s", code, raw)
	}
	msgs := mailsTo(srv, "dora@example.org")
	if len(msgs) != 1 || !strings.Contains(msgs[0].Body, mail.WithheldText) {
		t.Fatalf("the invitation = %+v", msgs)
	}
}

// TestPasswordLinkMailIsLeasedOnlyByTheServersOwnWorker: a mail job that carries a
// password link is recognised as one, and a lease by a credential the server did not
// issue to its own workers — an operator's session — is not the server's own worker,
// so the activation scan leaves the job for the worker the server runs.
func TestPasswordLinkMailIsLeasedOnlyByTheServersOwnWorker(t *testing.T) {
	// No mail Worker: the job parks where a lease would find it.
	srv, _ := newSystemServer(t, WithSystemProcesses(), WithUserProvisioning(), WithExternalURL(passwordTestOrigin), WithAuth())
	u := User{ID: "usr_d00000000000000000000001", Username: "admin2", Email: "admin2@example.org", Source: SourceLocal, Roles: []string{RoleAdmin}}
	if err := srv.users.Save(u); err != nil {
		t.Fatal(err)
	}
	inst, err := srv.startPasswordLinkProcess(u, passwordAnlassForgot)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	var mailJob uint64
	srv.do(func() {
		_ = srv.store.ElementInstancesOfProcess(inst, func(elKey uint64) error {
			if k, ok, err := srv.store.JobOfElement(elKey); err == nil && ok {
				mailJob = k
			}
			return nil
		})
	})
	if mailJob == 0 {
		t.Fatal("the password-link process has no mail job")
	}
	var carries bool
	srv.do(func() { carries = srv.carriesPasswordLink(mailJob) })
	if !carries {
		t.Fatal("the mail job of proc_passwort_link is not recognised as carrying a password link")
	}

	r := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/activate", nil)
	op := &httpapi.Principal{UserID: "usr_op", Username: "op", Roles: []string{RoleOperator}, Session: true}
	if srv.ownWorker(r.WithContext(httpapi.WithPrincipal(r.Context(), op))) {
		t.Fatal("an operator's session counts as the server's own worker")
	}
	own := &httpapi.Principal{UserID: servicePrincipalName}
	if !srv.ownWorker(r.WithContext(httpapi.WithPrincipal(r.Context(), own))) {
		t.Fatal("the server's own worker credential is not recognised")
	}
}

func mustHash(t *testing.T, pw string) string {
	t.Helper()
	h, err := hashPassword(pw)
	if err != nil {
		t.Fatal(err)
	}
	return h
}
