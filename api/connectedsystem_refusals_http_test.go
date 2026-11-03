package api_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestAnAssertionIsRefusedForEveryFlawItCanHave (ADR-0512 §1): a token request of a
// client named by its document is refused unless its assertion is exactly what the
// contract says — the right kind, for this server, by the client itself, signed with a
// key of its document, short-lived and used once; a key the system rotated in since is
// found by reading the document again.
func TestAnAssertionIsRefusedForEveryFlawItCanHave(t *testing.T) {
	ts, admin := connectedAtlas(t)
	sys := newFakeSystem(t)
	tok := sys.connect(t, admin, ts.URL, nil, false)
	refresh := tok["refresh_token"].(string)
	aud := ts.URL + "/oauth/token"

	// renew posts a renewal of the system's grant with the form's credentials.
	renew := func(form url.Values) (int, map[string]any) {
		form.Set("grant_type", "refresh_token")
		form.Set("refresh_token", refresh)
		status, body := postToken(t, ts.URL, form)
		if status == http.StatusOK {
			refresh = body["refresh_token"].(string)
		}
		return status, body
	}
	signed := func(header, claims map[string]any, key ed25519.PrivateKey) string {
		enc := base64.RawURLEncoding.EncodeToString
		h, _ := json.Marshal(header)
		c, _ := json.Marshal(claims)
		input := enc(h) + "." + enc(c)
		return input + "." + enc(ed25519.Sign(key, []byte(input)))
	}
	withAssertion := func(a string) url.Values {
		return url.Values{"client_id": {sys.id}, "client_assertion_type": {"urn:ietf:params:oauth:client-assertion-type:jwt-bearer"}, "client_assertion": {a}}
	}
	claims := func(mutate func(map[string]any)) string { return sys.assertion(aud, mutate) }
	_, stranger, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Now()

	for name, form := range map[string]url.Values{
		"no assertion type": {"client_id": {sys.id}, "client_assertion": {claims(nil)}},
		"a secret as well":  func() url.Values { f := withAssertion(claims(nil)); f.Set("client_secret", "x"); return f }(),
		"a registered id":   {"client_id": {"some-client"}, "client_assertion_type": {"urn:ietf:params:oauth:client-assertion-type:jwt-bearer"}, "client_assertion": {claims(nil)}},
		"two segments":      withAssertion("a.b"),
		"a header not b64":  withAssertion("%%%.e30.e30"),
		"a header not JSON": withAssertion(base64.RawURLEncoding.EncodeToString([]byte("nope")) + ".e30.e30"),
		"HS256":             withAssertion(signed(map[string]any{"alg": "HS256", "kid": "k1"}, map[string]any{"iss": sys.id}, stranger)),
		"a foreign key":     withAssertion(signed(map[string]any{"alg": "EdDSA", "kid": "k1"}, map[string]any{"iss": sys.id, "sub": sys.id, "aud": aud, "jti": "x", "exp": now.Add(time.Minute).Unix()}, stranger)),
		"another issuer":    withAssertion(claims(func(c map[string]any) { c["iss"] = "https://elsewhere.example/doc" })),
		"another audience":  withAssertion(claims(func(c map[string]any) { c["aud"] = "https://elsewhere.example/token" })),
		"no expiry":         withAssertion(claims(func(c map[string]any) { delete(c, "exp") })),
		"expired":           withAssertion(claims(func(c map[string]any) { c["exp"] = now.Add(-time.Hour).Unix() })),
		"issued later":      withAssertion(claims(func(c map[string]any) { c["iat"] = now.Add(time.Hour).Unix() })),
		"not yet valid":     withAssertion(claims(func(c map[string]any) { c["nbf"] = now.Add(time.Hour).Unix() })),
		"too long":          withAssertion(claims(func(c map[string]any) { c["exp"] = now.Add(time.Hour).Unix() })),
		"too long, no iat":  withAssertion(claims(func(c map[string]any) { delete(c, "iat"); c["exp"] = now.Add(time.Hour).Unix() })),
		"no jti":            withAssertion(claims(func(c map[string]any) { delete(c, "jti") })),
	} {
		if status, body := renew(form); status != http.StatusUnauthorized || body["error"] != "invalid_client" {
			t.Errorf("%s: %d %v, want 401 invalid_client", name, status, body)
		}
	}

	// The client named only by the assertion's issuer, once; the same assertion again is
	// a replay.
	a := claims(nil)
	form := url.Values{"client_assertion_type": {"urn:ietf:params:oauth:client-assertion-type:jwt-bearer"}, "client_assertion": {a}}
	if status, body := renew(form); status != http.StatusOK {
		t.Fatalf("a renewal naming the client by the issuer = %d %v", status, body)
	}
	if status, _ := renew(withAssertion(a)); status != http.StatusUnauthorized {
		t.Errorf("a replayed assertion = %d, want 401", status)
	}
	// A rotated key is learnt by reading the document again.
	sys.rotate()
	if status, body := renew(withAssertion(claims(nil))); status != http.StatusOK {
		t.Errorf("after the key rotated = %d %v, want the document read again", status, body)
	}
}

// TestTheConnectedSystemRoutesRefuseWhatTheyCannotDo: the routes of Connected systems
// and Connected accounts answer a body they cannot read, a system they do not know and
// a pre-consent they cannot turn on, and a person withdraws and declines a system.
func TestTheConnectedSystemRoutesRefuseWhatTheyCannotDo(t *testing.T) {
	ts, admin := connectedAtlas(t)
	sys := newFakeSystem(t)
	sys.connect(t, admin, ts.URL, nil, true)
	id := connectedSystems(t, admin, ts.URL)[0]["id"].(string)

	putJSONBody(t, admin, ts.URL+"/api/v1/connected-systems/"+id+"/preconsent", `not json`, http.StatusBadRequest)
	putJSONBody(t, admin, ts.URL+"/api/v1/connected-systems/"+id+"/preconsent", `{}`, http.StatusBadRequest)
	putJSONBody(t, admin, ts.URL+"/api/v1/connected-systems/nope/preconsent", `{"preconsent":false}`, http.StatusNotFound)

	createUserWithRoles(t, admin, ts.URL, "alice", `["user"]`)
	alice := signInAs(t, ts.URL, "alice", "a-password-that-is-long")
	silent := sys.authorizeQuery(ts.URL+"/tasks", "")
	silent.Set("prompt", "none")
	_, to := visit(t, alice, ts.URL, silent)
	if !strings.Contains(to, "code=") {
		t.Fatalf("a silent connection under pre-consent → %q", to)
	}
	sys.exchange(t, ts.URL, to)

	if status, _ := postJSONAs(t, alice, ts.URL+"/api/v1/me/connected-systems/withdraw", map[string]string{"clientId": "https://nowhere.example/doc"}); status != http.StatusNotFound {
		t.Errorf("withdrawing from an unknown system = %d, want 404", status)
	}
	resp, err := alice.Post(ts.URL+"/api/v1/me/connected-systems/withdraw", "application/json", strings.NewReader("not json"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("withdrawing with a body that is no JSON = %d, want 400", resp.StatusCode)
	}
	if status, body := postJSONAs(t, alice, ts.URL+"/api/v1/me/connected-systems/withdraw", map[string]string{"clientId": sys.id}); status != http.StatusNoContent {
		t.Fatalf("withdrawing = %d %s", status, body)
	}
	// Declined: pre-consent no longer applies to alice.
	if _, to := visit(t, alice, ts.URL, silent); !strings.Contains(to, "consent_required") {
		t.Errorf("after declining → %q, want consent_required", to)
	}
	if mine := mySystems(t, alice, ts.URL); len(mine) == 0 {
		t.Errorf("alice's systems after declining = %v, want the system listed", mine)
	}
}

// TestSeveralSystemsAreListedInOrder: Connected systems lists the connected before the
// disconnected, and each person's Connected accounts every system they are connected
// to or that is pre-approved for them.
func TestSeveralSystemsAreListedInOrder(t *testing.T) {
	ts, admin := connectedAtlas(t)
	first, second, third := newFakeSystem(t), newFakeSystem(t), newFakeSystem(t)
	for _, s := range []*fakeSystem{first, second, third} {
		s.connect(t, admin, ts.URL, nil, true)
	}
	createUserWithRoles(t, admin, ts.URL, "alice", `["user"]`)
	alice := signInAs(t, ts.URL, "alice", "a-password-that-is-long")
	for _, s := range []*fakeSystem{first, second} {
		silent := s.authorizeQuery(ts.URL+"/tasks", "")
		silent.Set("prompt", "none")
		_, to := visit(t, alice, ts.URL, silent)
		s.exchange(t, ts.URL, to)
	}
	rows := connectedSystems(t, admin, ts.URL)
	if len(rows) != 3 {
		t.Fatalf("connected systems = %v, want three", rows)
	}
	deleteAt(t, admin, ts.URL+"/api/v1/connected-systems/"+rows[0]["id"].(string), http.StatusNoContent)
	rows = connectedSystems(t, admin, ts.URL)
	if rows[0]["connected"] != true || rows[1]["connected"] != true || rows[2]["connected"] != false {
		t.Errorf("rows = %v, want the connected first", rows)
	}
	if rows[0]["clientId"].(string) > rows[1]["clientId"].(string) && rows[0]["host"] == rows[1]["host"] {
		t.Errorf("connected rows are not in order: %v", rows)
	}
	mine := mySystems(t, alice, ts.URL)
	if len(mine) < 2 {
		t.Fatalf("alice's systems = %v, want at least the two she is connected to", mine)
	}
	for i := 1; i < len(mine); i++ {
		if mine[i-1]["clientId"].(string) > mine[i]["clientId"].(string) {
			t.Errorf("alice's systems are not in order: %v", mine)
		}
	}
}

// TestEveryWayASystemGrantEndsIsSaid: a system that gives its grant back, an approver
// whose account is disabled, and a client an administrator removes on the AI access
// page each end the system grant, and Connected systems says how.
func TestEveryWayASystemGrantEndsIsSaid(t *testing.T) {
	ts, admin := connectedAtlas(t)
	gives, approved, removed := newFakeSystem(t), newFakeSystem(t), newFakeSystem(t)

	// The system gives its grant back (RFC 7009); approving again shows the current
	// grant on the consent screen first.
	tok := gives.connect(t, admin, ts.URL, nil, false)
	_, ctx := csGetJSON(t, admin, ts.URL+"/api/v1/oauth/authorize-context?"+gives.authorizeQuery(ts.URL+"/system", "").Encode())
	if sys, _ := ctx["system"].(map[string]any); sys == nil || sys["current"] == nil {
		t.Errorf("the consent screen of a connected system = %v, want the current grant", ctx["system"])
	}
	resp, err := http.PostForm(ts.URL+"/oauth/revoke", gives.auth(url.Values{"token": {tok["refresh_token"].(string)}}, ts.URL+"/oauth/token"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("revoke = %d", resp.StatusCode)
	}

	// The approver's account is disabled.
	id := createUserWithRoles(t, admin, ts.URL, "ada", `["admin","user"]`)
	ada := signInAs(t, ts.URL, "ada", "a-password-that-is-long")
	approved.connect(t, ada, ts.URL, nil, false)
	patchUser(t, admin, ts.URL, id, `{"disabled":true}`)

	// The client is removed on the AI access page, with other systems' grants left.
	removed.connect(t, admin, ts.URL, nil, false)
	for _, c := range listClients(t, admin, ts.URL) {
		if c["id"] == removed.id {
			deleteAt(t, admin, ts.URL+"/api/v1/oauth-clients/"+url.PathEscape(removed.id), http.StatusNoContent)
		}
	}

	why := map[string]string{}
	for _, r := range connectedSystems(t, admin, ts.URL) {
		reason, _ := r["disconnectReason"].(string)
		why[r["clientId"].(string)] = reason
	}
	if !strings.Contains(why[gives.id], "gave its grant back") {
		t.Errorf("a system that gave its grant back: %q", why[gives.id])
	}
	if !strings.Contains(why[approved.id], "disabled or deleted") {
		t.Errorf("a system whose approver was disabled: %q", why[approved.id])
	}
	if reason, listed := why[removed.id]; listed && reason == "" {
		t.Errorf("a removed client is still listed as connected")
	}
}

// TestPromptNoneNeverShowsAPage: a system grant is never approved without its page, a
// request the page would refuse is answered at the redirect URI when no page may be
// shown, and declining a system twice is declining it once.
func TestPromptNoneNeverShowsAPage(t *testing.T) {
	ts, admin := connectedAtlas(t)
	sys := newFakeSystem(t)
	sys.connect(t, admin, ts.URL, nil, true)

	system := sys.authorizeQuery(ts.URL+"/system", "diagrams")
	system.Set("prompt", "none")
	if _, to := visit(t, admin, ts.URL, system); !strings.Contains(to, "consent_required") {
		t.Errorf("a silent request for a system grant → %q, want consent_required", to)
	}
	wrong := sys.authorizeQuery(ts.URL+"/system", "full")
	wrong.Set("prompt", "none")
	if _, to := visit(t, admin, ts.URL, wrong); !strings.Contains(to, "error=") || !strings.HasPrefix(to, sys.redirect) {
		t.Errorf("a silent request the page would refuse → %q, want an error at the redirect URI", to)
	}

	createUserWithRoles(t, admin, ts.URL, "alice", `["user"]`)
	alice := signInAs(t, ts.URL, "alice", "a-password-that-is-long")
	for range 2 {
		if status, body := postJSONAs(t, alice, ts.URL+"/api/v1/me/connected-systems/withdraw", map[string]string{"clientId": sys.id}); status != http.StatusNoContent {
			t.Fatalf("declining = %d %s", status, body)
		}
	}
	silent := sys.authorizeQuery(ts.URL+"/tasks", "")
	silent.Set("prompt", "none")
	if _, to := visit(t, alice, ts.URL, silent); !strings.Contains(to, "consent_required") {
		t.Errorf("after declining twice → %q, want consent_required", to)
	}
}
