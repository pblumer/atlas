package api_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pblumer/atlas/api"
)

// A connected system, end to end (ADR-0512).
//
// The system here is a test server publishing a client ID metadata document on
// loopback — which the atlas under test may read because it is started with private
// addresses allowed — and signing its token requests with an Ed25519 key, as the
// contract with the first such system (Orbithek) has it. What these hold is the
// record's promise: one approval by an administrator, no secret, a reach the
// approver chose, people who follow without a page of their own, and every way it
// ends.

// fakeSystem is a connected system: its document, its key, and the assertions it signs.
type fakeSystem struct {
	ts       *httptest.Server
	id       string
	redirect string

	mu    sync.Mutex
	priv  ed25519.PrivateKey
	kid   string
	reads int
	seq   int
}

func newFakeSystem(t *testing.T) *fakeSystem {
	t.Helper()
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	f := &fakeSystem{priv: priv, kid: "k1"}
	f.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/engines/client-metadata" {
			http.NotFound(w, r)
			return
		}
		f.mu.Lock()
		f.reads++
		pub := f.priv.Public().(ed25519.PublicKey)
		kid := f.kid
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "max-age=300")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"client_id":                       f.id,
			"client_name":                     "Example wiki",
			"client_uri":                      f.ts.URL,
			"redirect_uris":                   []string{f.redirect},
			"grant_types":                     []string{"authorization_code", "refresh_token"},
			"response_types":                  []string{"code"},
			"token_endpoint_auth_method":      "private_key_jwt",
			"token_endpoint_auth_signing_alg": "EdDSA",
			"jwks": map[string]any{"keys": []any{map[string]any{
				"kty": "OKP", "crv": "Ed25519", "x": base64.RawURLEncoding.EncodeToString(pub),
				"kid": kid, "use": "sig", "alg": "EdDSA",
			}}},
		})
	}))
	f.id = f.ts.URL + "/api/engines/client-metadata"
	f.redirect = f.ts.URL + "/api/engines/atlas/connect/callback"
	t.Cleanup(f.ts.Close)
	return f
}

// rotate replaces the system's key, as changing its document does.
func (f *fakeSystem) rotate() {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	f.mu.Lock()
	f.priv, f.kid = priv, "k2"
	f.mu.Unlock()
}

// assertion signs a fresh client assertion for aud; mutate may change its claims.
func (f *fakeSystem) assertion(aud string, mutate func(map[string]any)) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	now := time.Now()
	claims := map[string]any{"iss": f.id, "sub": f.id, "aud": aud,
		"jti": fmt.Sprintf("jti-%d-%d", now.UnixNano(), f.seq), "iat": now.Unix(), "exp": now.Add(2 * time.Minute).Unix()}
	if mutate != nil {
		mutate(claims)
	}
	enc := base64.RawURLEncoding.EncodeToString
	h, _ := json.Marshal(map[string]any{"alg": "EdDSA", "typ": "JWT", "kid": f.kid})
	c, _ := json.Marshal(claims)
	input := enc(h) + "." + enc(c)
	return input + "." + enc(ed25519.Sign(f.priv, []byte(input)))
}

// auth is the client authentication of a form posted to atlas's token or revocation
// endpoint, for aud.
func (f *fakeSystem) auth(form url.Values, aud string) url.Values {
	form.Set("client_id", f.id)
	form.Set("client_assertion_type", "urn:ietf:params:oauth:client-assertion-type:jwt-bearer")
	form.Set("client_assertion", f.assertion(aud, nil))
	return form
}

func connectedAtlas(t *testing.T) (*httptest.Server, *http.Client) {
	t.Helper()
	ts, _ := newAuthServerWith(t, "root", "rootpassword", api.WithClientMetadataPrivateAddresses())
	return ts, signedInClient(t, ts.URL)
}

// authorizeQuery is an authorization request of the system for a resource.
func (f *fakeSystem) authorizeQuery(resource, scope string) url.Values {
	q := url.Values{
		"client_id": {f.id}, "redirect_uri": {f.redirect}, "response_type": {"code"},
		"code_challenge": {testChallenge()}, "code_challenge_method": {"S256"},
		"resource": {resource}, "state": {"xyz"},
	}
	if scope != "" {
		q.Set("scope", scope)
	}
	return q
}

func csGetJSON(t *testing.T, c *http.Client, u string) (int, map[string]any) {
	t.Helper()
	resp, err := c.Get(u)
	if err != nil {
		t.Fatalf("GET %s: %v", u, err)
	}
	defer resp.Body.Close()
	var out map[string]any
	data, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(data, &out)
	return resp.StatusCode, out
}

func postJSONAs(t *testing.T, c *http.Client, u string, body any) (int, []byte) {
	t.Helper()
	b, _ := json.Marshal(body)
	resp, err := c.Post(u, "application/json", strings.NewReader(string(b)))
	if err != nil {
		t.Fatalf("POST %s: %v", u, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}

// approveSystem posts an administrator's decision on a system grant and answers the
// status and where the browser goes.
func (f *fakeSystem) approveSystem(t *testing.T, c *http.Client, base, scope string, caps any, preconsent bool) (int, string) {
	t.Helper()
	body := map[string]any{
		"clientId": f.id, "redirectUri": f.redirect, "state": "xyz", "codeChallenge": testChallenge(),
		"resource": base + "/system", "scope": scope, "approve": true, "preconsent": preconsent,
	}
	if caps != nil {
		body["capabilities"] = caps
	}
	code, data := postJSONAs(t, c, base+"/api/v1/oauth/authorize", body)
	var out struct{ Redirect string }
	_ = json.Unmarshal(data, &out)
	if code != http.StatusOK {
		return code, string(data)
	}
	return code, out.Redirect
}

// exchange spends a code with the system's assertion and answers the token response.
func (f *fakeSystem) exchange(t *testing.T, base, redirect string) map[string]any {
	t.Helper()
	status, tok := postToken(t, base, f.auth(url.Values{
		"grant_type": {"authorization_code"}, "code": {codeFrom(t, redirect)},
		"redirect_uri": {f.redirect}, "code_verifier": {testVerifier},
	}, base+"/oauth/token"))
	if status != http.StatusOK {
		t.Fatalf("exchange = %d: %v", status, tok)
	}
	return tok
}

// connect walks the system grant through with the given capabilities.
func (f *fakeSystem) connect(t *testing.T, admin *http.Client, base string, caps any, preconsent bool) map[string]any {
	t.Helper()
	code, redirect := f.approveSystem(t, admin, base, "diagrams tasklist events", caps, preconsent)
	if code != http.StatusOK {
		t.Fatalf("approve the system = %d: %s", code, redirect)
	}
	return f.exchange(t, base, redirect)
}

func listClients(t *testing.T, admin *http.Client, base string) []map[string]any {
	t.Helper()
	resp, err := admin.Get(base + "/api/v1/oauth-clients")
	if err != nil {
		t.Fatalf("list clients: %v", err)
	}
	defer resp.Body.Close()
	var out []map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out
}

func clientListed(clients []map[string]any, id string) map[string]any {
	for _, c := range clients {
		if c["id"] == id {
			return c
		}
	}
	return nil
}

// TestAConnectedSystemIsReadFromItsDocumentAndRememberedOnceApproved: the consent
// screen is told the host above the name, nothing is saved for the request or the
// approval, and the exchange — authenticated by an assertion, not a secret — saves the
// client in the turn that saves its grant. Renewal and revocation take the assertion
// too; a replayed one, one made out to another audience, and a secret are refused.
func TestAConnectedSystemIsReadFromItsDocumentAndRememberedOnceApproved(t *testing.T) {
	ts, admin := connectedAtlas(t)
	sys := newFakeSystem(t)

	code, ctx := csGetJSON(t, admin, ts.URL+"/api/v1/oauth/authorize-context?"+sys.authorizeQuery(ts.URL+"/system", "").Encode())
	if code != http.StatusOK || ctx["error"] != nil {
		t.Fatalf("authorize-context = %d %v", code, ctx)
	}
	host := strings.TrimPrefix(sys.ts.URL, "http://")
	if ctx["clientHost"] != host || ctx["clientName"] != "Example wiki" || ctx["metadataDocument"] != true {
		t.Errorf("context = %v, want the host %s above the claimed name", ctx, host)
	}
	system, _ := ctx["system"].(map[string]any)
	if system == nil || system["mayApprove"] != true || fmt.Sprint(system["scopes"]) != "[diagrams tasklist events]" {
		t.Errorf("context's system = %v, want all three capabilities asked for an administrator", system)
	}
	if clientListed(listClients(t, admin, ts.URL), sys.id) != nil {
		t.Fatal("a client record exists for a request nobody approved")
	}

	status, redirect := sys.approveSystem(t, admin, ts.URL, "", nil, true)
	if status != http.StatusOK {
		t.Fatalf("approve = %d %s", status, redirect)
	}
	if clientListed(listClients(t, admin, ts.URL), sys.id) != nil {
		t.Fatal("the approval saved the client before its grant")
	}
	// A secret is not how this client authenticates.
	if status, _ := postToken(t, ts.URL, url.Values{"grant_type": {"authorization_code"}, "code": {"x"},
		"client_id": {sys.id}, "client_secret": {"atlascs_guess"}}); status != http.StatusUnauthorized {
		t.Errorf("a secret for a metadata client = %d, want 401", status)
	}
	// An assertion made out to another audience is refused.
	bad := url.Values{"grant_type": {"authorization_code"}, "code": {codeFrom(t, redirect)},
		"redirect_uri": {sys.redirect}, "code_verifier": {testVerifier}, "client_id": {sys.id},
		"client_assertion_type": {"urn:ietf:params:oauth:client-assertion-type:jwt-bearer"},
		"client_assertion":      {sys.assertion("https://elsewhere.example.org/oauth/token", nil)}}
	if status, out := postToken(t, ts.URL, bad); status != http.StatusUnauthorized || out["error"] != "invalid_client" {
		t.Errorf("an assertion to another audience = %d %v, want 401 invalid_client", status, out)
	}

	tok := sys.exchange(t, ts.URL, redirect)
	if tok["scope"] != "diagrams tasklist events" {
		t.Errorf("token scope = %v, want the three capabilities", tok["scope"])
	}
	rec := clientListed(listClients(t, admin, ts.URL), sys.id)
	if rec == nil || rec["metadata"] != true || rec["name"] != "Example wiki" {
		t.Fatalf("after the exchange the client is %v, want it saved as a metadata client", rec)
	}

	// Renewal with a fresh assertion; the same assertion twice is a replay.
	refreshForm := sys.auth(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tok["refresh_token"].(string)}}, ts.URL+"/oauth/token")
	status, renewed := postToken(t, ts.URL, refreshForm)
	if status != http.StatusOK {
		t.Fatalf("refresh = %d %v", status, renewed)
	}
	refreshForm.Set("refresh_token", renewed["refresh_token"].(string))
	if status, _ := postToken(t, ts.URL, refreshForm); status != http.StatusUnauthorized {
		t.Errorf("a replayed assertion = %d, want 401", status)
	}

	// A rotated key is picked up by reading the document again.
	sys.rotate()
	status, renewed2 := postToken(t, ts.URL, sys.auth(url.Values{"grant_type": {"refresh_token"},
		"refresh_token": {renewed["refresh_token"].(string)}}, ts.URL+"/oauth/token"))
	if status != http.StatusOK {
		t.Fatalf("refresh after a key rotation = %d %v", status, renewed2)
	}

	// Revocation with an assertion made out to the revocation endpoint ends the grant.
	access := renewed2["access_token"].(string)
	if code := bearerGet(t, ts.URL, "/api/v1/auth/me", access); code != http.StatusOK {
		t.Fatalf("auth/me before revoking = %d", code)
	}
	form := sys.auth(url.Values{"token": {access}}, ts.URL+"/oauth/revoke")
	if code, body := postRevoke(t, ts.URL, form); code != http.StatusOK {
		t.Fatalf("revoke = %d %s", code, body)
	}
	if code := bearerGet(t, ts.URL, "/api/v1/auth/me", access); code != http.StatusUnauthorized {
		t.Errorf("auth/me after revoking = %d, want 401", code)
	}
}

// TestADocumentOnAPrivateAddressIsNotReadByDefault: without the operator's setting a
// loopback document is refused, and the refusal is shown, never redirected.
func TestADocumentOnAPrivateAddressIsNotReadByDefault(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := signedInClient(t, ts.URL)
	sys := newFakeSystem(t)
	_, ctx := csGetJSON(t, admin, ts.URL+"/api/v1/oauth/authorize-context?"+sys.authorizeQuery(ts.URL+"/system", "").Encode())
	if msg, _ := ctx["error"].(string); !strings.Contains(msg, "ATLAS_CLIENT_METADATA_ALLOW_PRIVATE") || ctx["redirect"] != nil {
		t.Errorf("context = %v, want the private-address refusal and no redirect", ctx)
	}
}

// TestASystemAsksOnlyForTheNarrowScopes: full, worker or anything else is refused for
// the system resource as invalid_scope, back to the system.
func TestASystemAsksOnlyForTheNarrowScopes(t *testing.T) {
	ts, admin := connectedAtlas(t)
	sys := newFakeSystem(t)
	for _, scope := range []string{"full", "tasklist worker", "tasks"} {
		_, ctx := csGetJSON(t, admin, ts.URL+"/api/v1/oauth/authorize-context?"+sys.authorizeQuery(ts.URL+"/system", scope).Encode())
		if r, _ := ctx["redirect"].(string); !strings.Contains(r, "error=invalid_scope") {
			t.Errorf("scope %q: context = %v, want an invalid_scope redirect", scope, ctx)
		}
	}
	// Approving a capability the system did not ask for is refused too.
	if code, body := sys.approveSystem(t, admin, ts.URL, "tasklist", []map[string]any{{"scope": "events"}}, false); code != http.StatusBadRequest {
		t.Errorf("approving an unasked capability = %d %s, want 400", code, body)
	}
	if code, body := sys.approveSystem(t, admin, ts.URL, "tasklist", []map[string]any{}, false); code != http.StatusBadRequest {
		t.Errorf("approving no capability = %d %s, want 400", code, body)
	}
	if code, body := sys.approveSystem(t, admin, ts.URL, "", []map[string]any{{"scope": "tasklist", "projects": []string{"nope"}}}, false); code != http.StatusBadRequest {
		t.Errorf("a reach of a project that does not exist = %d %s, want 400", code, body)
	}
}

// TestOnlyAnAdministratorConnectsASystem: anybody else is told so on the screen and
// refused when they try.
func TestOnlyAnAdministratorConnectsASystem(t *testing.T) {
	ts, admin := connectedAtlas(t)
	sys := newFakeSystem(t)
	createUserWithRoles(t, admin, ts.URL, "olga", `["operator","user"]`)
	olga := signInAs(t, ts.URL, "olga", "a-password-that-is-long")
	_, ctx := csGetJSON(t, olga, ts.URL+"/api/v1/oauth/authorize-context?"+sys.authorizeQuery(ts.URL+"/system", "").Encode())
	if system, _ := ctx["system"].(map[string]any); system == nil || system["mayApprove"] != false || system["projects"] != nil {
		t.Errorf("an operator's context = %v, want mayApprove false and no projects", ctx["system"])
	}
	if code, body := sys.approveSystem(t, olga, ts.URL, "", nil, true); code != http.StatusForbidden {
		t.Errorf("an operator approving a system = %d %s, want 403", code, body)
	}
	// Declining stays open to anybody: it grants nothing.
	body := map[string]any{"clientId": sys.id, "redirectUri": sys.redirect, "state": "xyz", "codeChallenge": testChallenge(),
		"resource": ts.URL + "/system", "approve": false}
	if code, data := postJSONAs(t, olga, ts.URL+"/api/v1/oauth/authorize", body); code != http.StatusOK || !strings.Contains(string(data), "access_denied") {
		t.Errorf("an operator declining = %d %s", code, data)
	}
}

func bearerJSON(t *testing.T, base, method, path, token string) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest(method, base+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}

func projectTask(t *testing.T, admin *http.Client, ts *httptest.Server, project, processID string) uint64 {
	t.Helper()
	bpmn := strings.ReplaceAll(openTaskBPMN, `id="open-task"`, `id="`+processID+`"`)
	def := deployIntoProject(t, admin, ts, bpmn, project)
	code, body := cReq(t, admin, ts, "POST", fmt.Sprintf("/api/v1/processes/%d/instances", def), `{}`)
	if code != http.StatusOK {
		t.Fatalf("start = %d %s", code, body)
	}
	code, body = cReq(t, admin, ts, "GET", "/api/v1/tasks", "")
	var tasks []struct {
		Key       uint64 `json:"key"`
		ProcessID string `json:"processId"`
	}
	if code != http.StatusOK || json.Unmarshal(listRows(t, body), &tasks) != nil {
		t.Fatalf("tasks = %d %s", code, body)
	}
	for _, tr := range tasks {
		if tr.ProcessID == processID {
			return tr.Key
		}
	}
	t.Fatalf("no task of %s in %s", processID, body)
	return 0
}

// TestASystemGrantReachesItsCapabilitiesWithTheirReach: a token of a system grant is
// confined to the routes of the capabilities the administrator kept, each with the
// reach they chose — the tasks of one project, the task facts of that project and
// nothing else of the feed — plus who-am-I, which says what was approved. A task that
// ended is told from one out of reach.
func TestASystemGrantReachesItsCapabilitiesWithTheirReach(t *testing.T) {
	ts, admin := connectedAtlas(t)
	sys := newFakeSystem(t)
	inside := createProjectAs(t, admin, ts.URL, "Inside")
	outside := createProjectAs(t, admin, ts.URL, "Outside")
	in := projectTask(t, admin, ts, inside, "inside-task")
	out := projectTask(t, admin, ts, outside, "outside-task")

	tok := sys.connect(t, admin, ts.URL, []map[string]any{
		{"scope": "tasklist", "projects": []string{inside}},
		{"scope": "events", "projects": []string{inside}},
	}, false)
	if tok["scope"] != "tasklist events" {
		t.Errorf("scope = %v, want the two kept", tok["scope"])
	}
	access := tok["access_token"].(string)

	code, body := bearerJSON(t, ts.URL, "GET", "/api/v1/tasks", access)
	var tasks []struct {
		Key uint64 `json:"key"`
	}
	if code != http.StatusOK || json.Unmarshal(listRows(t, body), &tasks) != nil || len(tasks) != 1 || tasks[0].Key != in {
		t.Fatalf("the system lists %d %s, want the inside task %d alone", code, body, in)
	}
	if code, _ := bearerJSON(t, ts.URL, "GET", fmt.Sprintf("/api/v1/tasks/%d", in), access); code != http.StatusOK {
		t.Errorf("the inside task by key = %d, want 200", code)
	}
	if code, _ := bearerJSON(t, ts.URL, "GET", fmt.Sprintf("/api/v1/tasks/%d", out), access); code != http.StatusNotFound {
		t.Errorf("the outside task by key = %d, want 404", code)
	}
	// diagrams was unticked; the product surface was never on offer.
	for _, r := range []struct{ method, path string }{
		{"GET", "/api/v1/processes"}, {"GET", "/api/v1/instances"}, {"GET", "/api/v1/users"},
		{"POST", fmt.Sprintf("/api/v1/tasks/%d/claim", in)},
	} {
		if code, _ := bearerJSON(t, ts.URL, r.method, r.path, access); code != http.StatusForbidden {
			t.Errorf("%s %s = %d, want 403", r.method, r.path, code)
		}
	}

	// The feed carries the inside process's task facts and nothing else.
	code, body = bearerJSON(t, ts.URL, "GET", "/api/v1/events", access)
	var page struct {
		Events []struct {
			Type string         `json:"type"`
			Data map[string]any `json:"data"`
		} `json:"events"`
	}
	if code != http.StatusOK || json.Unmarshal(body, &page) != nil || len(page.Events) == 0 {
		t.Fatalf("events = %d %s", code, body)
	}
	for _, e := range page.Events {
		if !strings.HasPrefix(e.Type, "atlas.task.") || e.Data["processId"] != "inside-task" {
			t.Errorf("the system read %s about %v, want only the inside task's facts", e.Type, e.Data["processId"])
		}
	}

	// Who it is, and what was approved.
	code, body = bearerJSON(t, ts.URL, "GET", "/api/v1/auth/me", access)
	var me struct {
		System struct {
			ClientID     string `json:"clientId"`
			ClientName   string `json:"clientName"`
			ApprovedBy   string `json:"approvedBy"`
			ApprovedAt   string `json:"approvedAt"`
			Capabilities []struct {
				Scope    string   `json:"scope"`
				Projects []string `json:"projects"`
			} `json:"capabilities"`
			Preconsent *bool `json:"preconsent"`
		} `json:"system"`
	}
	if code != http.StatusOK || json.Unmarshal(body, &me) != nil {
		t.Fatalf("auth/me = %d %s", code, body)
	}
	s := me.System
	if s.ClientID != sys.id || s.ClientName != "Example wiki" || s.ApprovedBy != "root" || s.Preconsent == nil || *s.Preconsent ||
		len(s.Capabilities) != 2 || s.Capabilities[0].Scope != "tasklist" || len(s.Capabilities[0].Projects) != 1 {
		t.Errorf("auth/me's system = %+v", s)
	}
	if _, err := time.Parse(time.RFC3339, s.ApprovedAt); err != nil {
		t.Errorf("approvedAt %q is not RFC 3339", s.ApprovedAt)
	}

	// Ended, told from out of reach: complete both tasks as the administrator.
	for _, k := range []uint64{in, out} {
		if code, body := cReq(t, admin, ts, "POST", fmt.Sprintf("/api/v1/tasks/%d/complete", k), `{"variables":{}}`); code != http.StatusOK && code != http.StatusNoContent {
			t.Fatalf("complete %d = %d %s", k, code, body)
		}
	}
	code, body = bearerJSON(t, ts.URL, "GET", fmt.Sprintf("/api/v1/tasks/%d", in), access)
	var gone struct{ Key, Ended, EndedAt string }
	if code != http.StatusGone || json.Unmarshal(body, &gone) != nil || gone.Key != fmt.Sprint(in) || gone.Ended != "completed" {
		t.Fatalf("the ended inside task = %d %s, want 410 completed", code, body)
	}
	if _, err := time.Parse(time.RFC3339, gone.EndedAt); err != nil {
		t.Errorf("endedAt %q is not RFC 3339", gone.EndedAt)
	}
	if code, _ := bearerJSON(t, ts.URL, "GET", fmt.Sprintf("/api/v1/tasks/%d", out), access); code != http.StatusNotFound {
		t.Errorf("the ended outside task = %d, want 404", code)
	}
	if code, _ := bearerJSON(t, ts.URL, "GET", "/api/v1/tasks/999999999", access); code != http.StatusNotFound {
		t.Errorf("an unknown task = %d, want 404", code)
	}
	// A person reading an ended task is answered as before.
	if code, _ := cReq(t, admin, ts, "GET", fmt.Sprintf("/api/v1/tasks/%d", in), ""); code != http.StatusNotFound {
		t.Errorf("the administrator's own read of the ended task = %d, want 404 as before", code)
	}
}

// TestADiagramsCapabilityReadsAsATokenWithThatReach: a project reach applies as a
// diagrams token's does — the process list names the application of a process inside
// the reach and withholds the name of one outside it.
func TestADiagramsCapabilityReadsAsATokenWithThatReach(t *testing.T) {
	ts, admin := connectedAtlas(t)
	sys := newFakeSystem(t)
	inside := createProjectAs(t, admin, ts.URL, "Inside")
	outside := createProjectAs(t, admin, ts.URL, "Outside")
	projectTask(t, admin, ts, inside, "inside-proc")
	projectTask(t, admin, ts, outside, "outside-proc")
	tok := sys.connect(t, admin, ts.URL, []map[string]any{{"scope": "diagrams", "projects": []string{inside}}}, false)
	code, body := bearerJSON(t, ts.URL, "GET", "/api/v1/processes", tok["access_token"].(string))
	var procs []struct {
		ProcessID       string `json:"processId"`
		ApplicationName string `json:"applicationName"`
	}
	if code != http.StatusOK || json.Unmarshal(body, &procs) != nil {
		t.Fatalf("processes = %d %s", code, body)
	}
	for _, p := range procs {
		if want := map[string]string{"inside-proc": "Inside", "outside-proc": ""}[p.ProcessID]; p.ApplicationName != want {
			t.Errorf("%s is shown under application %q, want %q", p.ProcessID, p.ApplicationName, want)
		}
	}
	if code, _ := bearerJSON(t, ts.URL, "GET", "/api/v1/tasks", tok["access_token"].(string)); code != http.StatusForbidden {
		t.Errorf("tasks without the tasklist capability = %d, want 403", code)
	}
}

// TestANewApprovalReplacesTheSystemGrant: one system grant per client.
func TestANewApprovalReplacesTheSystemGrant(t *testing.T) {
	ts, admin := connectedAtlas(t)
	sys := newFakeSystem(t)
	first := sys.connect(t, admin, ts.URL, nil, false)["access_token"].(string)
	second := sys.connect(t, admin, ts.URL, []map[string]any{{"scope": "tasklist"}}, true)["access_token"].(string)
	if code := bearerGet(t, ts.URL, "/api/v1/auth/me", first); code != http.StatusUnauthorized {
		t.Errorf("the replaced grant's token = %d, want 401", code)
	}
	if code := bearerGet(t, ts.URL, "/api/v1/auth/me", second); code != http.StatusOK {
		t.Errorf("the new grant's token = %d, want 200", code)
	}
	rows := connectedSystems(t, admin, ts.URL)
	if len(rows) != 1 || rows[0]["connected"] != true || rows[0]["preconsent"] != true {
		t.Errorf("connected systems = %v, want one row", rows)
	}
}

func connectedSystems(t *testing.T, admin *http.Client, base string) []map[string]any {
	t.Helper()
	resp, err := admin.Get(base + "/api/v1/connected-systems")
	if err != nil {
		t.Fatalf("connected systems: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("connected systems = %d", resp.StatusCode)
	}
	var out []map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out
}

// noRedirects is a person's browser that shows where it is sent instead of going.
func csNoRedirects(c *http.Client) *http.Client {
	out := *c
	out.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &out
}

// visit sends a person's browser to the authorization endpoint and answers the status
// and the redirect target, if any.
func visit(t *testing.T, c *http.Client, base string, q url.Values) (int, string) {
	t.Helper()
	resp, err := csNoRedirects(c).Get(base + "/oauth/authorize?" + q.Encode())
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	resp.Body.Close()
	return resp.StatusCode, resp.Header.Get("Location")
}

// TestPreconsentConnectsAPersonWithoutThePage (ADR-0512 §3): with pre-consent a signed-in
// person's request for their tasks is approved without the page; prompt=none answers
// login_required and consent_required rather than a page; withdrawing declines the
// system, and approving on the screen oneself clears that.
func TestPreconsentConnectsAPersonWithoutThePage(t *testing.T) {
	ts, admin := connectedAtlas(t)
	sys := newFakeSystem(t)
	tasks := sys.authorizeQuery(ts.URL+"/tasks", "")
	silent := sys.authorizeQuery(ts.URL+"/tasks", "")
	silent.Set("prompt", "none")
	createUserWithRoles(t, admin, ts.URL, "alice", `["user"]`)
	alice := signInAs(t, ts.URL, "alice", "a-password-that-is-long")
	anonymous := &http.Client{}

	// Before the system is connected: the page, and consent_required when silent.
	if code, _ := visit(t, alice, ts.URL, tasks); code != http.StatusOK {
		t.Errorf("before pre-consent the request = %d, want the page", code)
	}
	if _, to := visit(t, alice, ts.URL, silent); !strings.Contains(to, "error=consent_required") || !strings.HasPrefix(to, sys.redirect) {
		t.Errorf("silent before pre-consent → %q, want consent_required", to)
	}

	systemTok := sys.connect(t, admin, ts.URL, []map[string]any{{"scope": "tasklist"}}, true)
	if code, _ := visit(t, anonymous, ts.URL, tasks); code != http.StatusOK {
		t.Errorf("not signed in, the request = %d, want the page to sign in on", code)
	}
	if _, to := visit(t, anonymous, ts.URL, silent); !strings.Contains(to, "error=login_required") || !strings.Contains(to, "state=xyz") {
		t.Errorf("silent and not signed in → %q, want login_required with the state", to)
	}
	code, to := visit(t, alice, ts.URL, silent)
	if code != http.StatusFound || !strings.Contains(to, "code=") {
		t.Fatalf("silent and pre-consented → %d %q, want a code", code, to)
	}
	tok := sys.exchange(t, ts.URL, to)
	personal := tok["access_token"].(string)
	code, body := bearerJSON(t, ts.URL, "GET", "/api/v1/auth/me", personal)
	if code != http.StatusOK || !strings.Contains(string(body), `"alice"`) || strings.Contains(string(body), `"system"`) {
		t.Fatalf("the person's token is %d %s, want alice's own grant", code, body)
	}

	// Alice sees the system, pre-approved for her, and withdraws.
	mine := mySystems(t, alice, ts.URL)
	if len(mine) != 1 || mine[0]["preApproved"] != true || len(mine[0]["grants"].([]any)) != 1 {
		t.Fatalf("alice's connected systems = %v", mine)
	}
	grantID := mine[0]["grants"].([]any)[0].(string)
	deleteAt(t, alice, ts.URL+"/api/v1/oauth-grants/"+grantID, http.StatusNoContent)
	if code := bearerGet(t, ts.URL, "/api/v1/auth/me", personal); code != http.StatusUnauthorized {
		t.Errorf("the withdrawn token = %d", code)
	}
	if mine := mySystems(t, alice, ts.URL); mine[0]["declined"] != true || mine[0]["preApproved"] != false {
		t.Errorf("after withdrawing alice's systems = %v, want declined", mine)
	}
	if code, _ := visit(t, alice, ts.URL, tasks); code != http.StatusOK {
		t.Errorf("after declining the request = %d, want the page", code)
	}
	if _, to := visit(t, alice, ts.URL, silent); !strings.Contains(to, "error=consent_required") {
		t.Errorf("silent after declining → %q, want consent_required", to)
	}

	// Approving on the screen herself clears the mark.
	approval := map[string]any{"clientId": sys.id, "redirectUri": sys.redirect, "state": "xyz",
		"codeChallenge": testChallenge(), "resource": ts.URL + "/tasks", "approve": true}
	if code, data := postJSONAs(t, alice, ts.URL+"/api/v1/oauth/authorize", approval); code != http.StatusOK {
		t.Fatalf("alice approving = %d %s", code, data)
	}
	if code, to := visit(t, alice, ts.URL, silent); code != http.StatusFound || !strings.Contains(to, "code=") {
		t.Errorf("after approving herself → %d %q, want pre-consent again", code, to)
	}

	// Withdrawing from Connected accounts declines too, with or without a grant.
	if code, data := postJSONAs(t, alice, ts.URL+"/api/v1/me/connected-systems/withdraw", map[string]any{"clientId": sys.id}); code != http.StatusNoContent {
		t.Fatalf("withdraw = %d %s", code, data)
	}
	if _, to := visit(t, alice, ts.URL, silent); !strings.Contains(to, "consent_required") {
		t.Errorf("after withdrawing on Connected accounts → %q, want consent_required", to)
	}

	// Pre-consent off: nobody is connected without being asked.
	createUserWithRoles(t, admin, ts.URL, "bob", `["user"]`)
	bob := signInAs(t, ts.URL, "bob", "a-password-that-is-long")
	if code, _ := visit(t, bob, ts.URL, silent); code != http.StatusFound {
		t.Fatalf("bob before pre-consent is turned off = %d", code)
	}
	rows := connectedSystems(t, admin, ts.URL)
	id := rows[0]["id"].(string)
	putJSONBody(t, admin, ts.URL+"/api/v1/connected-systems/"+id+"/preconsent", `{"preconsent":true}`, http.StatusBadRequest)
	putJSONBody(t, admin, ts.URL+"/api/v1/connected-systems/"+id+"/preconsent", `{"preconsent":false}`, http.StatusNoContent)
	if _, to := visit(t, bob, ts.URL, silent); !strings.Contains(to, "consent_required") {
		t.Errorf("with pre-consent off → %q, want consent_required", to)
	}
	if code := bearerGet(t, ts.URL, "/api/v1/auth/me", systemTok["access_token"].(string)); code != http.StatusOK {
		t.Errorf("turning pre-consent off ended the system grant (%d)", code)
	}
}

func mySystems(t *testing.T, c *http.Client, base string) []map[string]any {
	t.Helper()
	resp, err := c.Get(base + "/api/v1/me/connected-systems")
	if err != nil {
		t.Fatalf("my systems: %v", err)
	}
	defer resp.Body.Close()
	var out []map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out
}

func putJSONBody(t *testing.T, c *http.Client, u, body string, want int) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPut, u, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("PUT %s: %v", u, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("PUT %s = %d %s, want %d", u, resp.StatusCode, data, want)
	}
}

// TestDisconnectingASystemEndsItAndOptionallyItsPeople: the system grant goes, and with
// people=withdraw every person's grant of the client goes with it; the row stays and
// says why.
func TestDisconnectingASystemEndsItAndOptionallyItsPeople(t *testing.T) {
	ts, admin := connectedAtlas(t)
	sys := newFakeSystem(t)
	systemTok := sys.connect(t, admin, ts.URL, nil, true)["access_token"].(string)
	createUserWithRoles(t, admin, ts.URL, "alice", `["user"]`)
	alice := signInAs(t, ts.URL, "alice", "a-password-that-is-long")
	silent := sys.authorizeQuery(ts.URL+"/tasks", "")
	silent.Set("prompt", "none")
	_, to := visit(t, alice, ts.URL, silent)
	personal := sys.exchange(t, ts.URL, to)["access_token"].(string)

	rows := connectedSystems(t, admin, ts.URL)
	if len(rows) != 1 || rows[0]["people"] != float64(1) || rows[0]["approvedBy"] != "root" || rows[0]["lastUsed"] == nil {
		t.Fatalf("connected systems = %v, want one system with one person", rows)
	}
	createUserWithRoles(t, admin, ts.URL, "olga", `["operator","user"]`)
	olga := signInAs(t, ts.URL, "olga", "a-password-that-is-long")
	deleteAt(t, olga, ts.URL+"/api/v1/connected-systems/"+rows[0]["id"].(string), http.StatusForbidden)

	deleteAt(t, admin, ts.URL+"/api/v1/connected-systems/"+rows[0]["id"].(string)+"?people=withdraw", http.StatusNoContent)
	for name, tok := range map[string]string{"system": systemTok, "alice": personal} {
		if code := bearerGet(t, ts.URL, "/api/v1/auth/me", tok); code != http.StatusUnauthorized {
			t.Errorf("%s's token after the disconnect = %d, want 401", name, code)
		}
	}
	rows = connectedSystems(t, admin, ts.URL)
	if len(rows) != 1 || rows[0]["connected"] != false || rows[0]["disconnectReason"] == nil {
		t.Errorf("after the disconnect the row = %v, want it disconnected with a reason", rows)
	}
	deleteAt(t, admin, ts.URL+"/api/v1/connected-systems/nope", http.StatusNotFound)
	// Pre-consent ended with the grant.
	if _, to := visit(t, alice, ts.URL, silent); !strings.Contains(to, "consent_required") {
		t.Errorf("after the disconnect → %q, want consent_required", to)
	}
	// Approving it again is one approval, and the row is connected again.
	sys.connect(t, admin, ts.URL, nil, false)
	if rows := connectedSystems(t, admin, ts.URL); rows[0]["connected"] != true || rows[0]["disconnectReason"] != nil {
		t.Errorf("after reconnecting the row = %v", rows)
	}
}

// TestAnApproverWhoIsNoLongerAnAdministratorEndsTheSystemGrant: the grant stands on an
// approval only an administrator could give.
func TestAnApproverWhoIsNoLongerAnAdministratorEndsTheSystemGrant(t *testing.T) {
	ts, admin := connectedAtlas(t)
	sys := newFakeSystem(t)
	id := createUserWithRoles(t, admin, ts.URL, "ada", `["admin","user"]`)
	ada := signInAs(t, ts.URL, "ada", "a-password-that-is-long")
	tok := sys.connect(t, ada, ts.URL, nil, false)["access_token"].(string)
	if code := bearerGet(t, ts.URL, "/api/v1/auth/me", tok); code != http.StatusOK {
		t.Fatalf("auth/me = %d", code)
	}
	patchUser(t, admin, ts.URL, id, `{"roles":["user"]}`)
	if code := bearerGet(t, ts.URL, "/api/v1/auth/me", tok); code != http.StatusUnauthorized {
		t.Errorf("after ada lost admin the system token = %d, want 401", code)
	}
	rows := connectedSystems(t, admin, ts.URL)
	if len(rows) != 1 || rows[0]["connected"] != false || !strings.Contains(fmt.Sprint(rows[0]["disconnectReason"]), "no longer an administrator") {
		t.Errorf("rows = %v, want the system shown as disconnected because of its approver", rows)
	}
}

// TestTheSystemResourceIsPublished: its protected-resource metadata, and the
// authorization server's support for metadata documents and private_key_jwt.
func TestTheSystemResourceIsPublished(t *testing.T) {
	ts, admin := connectedAtlas(t)
	_, doc := csGetJSON(t, admin, ts.URL+"/.well-known/oauth-protected-resource/system")
	if doc["resource"] != ts.URL+"/system" {
		t.Errorf("system resource metadata = %v", doc)
	}
	_, as := csGetJSON(t, admin, ts.URL+"/.well-known/oauth-authorization-server")
	if as["client_id_metadata_document_supported"] != true || !strings.Contains(fmt.Sprint(as["token_endpoint_auth_methods_supported"]), "private_key_jwt") {
		t.Errorf("authorization-server metadata = %v", as)
	}
}
