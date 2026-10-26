package identity

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
)

func call(t *testing.T, h http.HandlerFunc, method, target, body string, p *httpapi.Principal, vals map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if p != nil {
		req = req.WithContext(httpapi.WithPrincipal(req.Context(), p))
	}
	for k, v := range vals {
		req.SetPathValue(k, v)
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func isAdmin(p *httpapi.Principal) bool { return p.HasRole("admin") }

func TestTheRoutesShowOnlyWhatTheCallerMayActOn(t *testing.T) {
	r := newRig(t)
	c, err := r.svc.Link(aliceLink())
	if err != nil {
		t.Fatal(err)
	}
	alice := &httpapi.Principal{UserID: "usr_alice", Roles: []string{"user"}, Session: true}
	bob := &httpapi.Principal{UserID: "usr_bob", Roles: []string{"user"}, Session: true}
	admin := &httpapi.Principal{UserID: "usr_admin", Roles: []string{"admin"}, Session: true}

	rec := call(t, r.svc.HandleMine, "GET", "/api/v1/me/connections", "", alice, nil)
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "rt-1") {
		t.Fatalf("mine: %d %s", rec.Code, rec.Body)
	}
	var v view
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil || len(v.Connections) != 1 {
		t.Fatalf("mine = %s", rec.Body)
	}
	if rec := call(t, r.svc.HandleMine, "GET", "/", "", nil, nil); rec.Code != 401 {
		t.Errorf("anonymous: %d", rec.Code)
	}
	if rec := call(t, r.svc.HandleOfUser, "GET", "/", "", admin, map[string]string{"id": "usr_alice"}); rec.Code != 200 ||
		!strings.Contains(rec.Body.String(), c.ID) {
		t.Errorf("admin view: %d %s", rec.Code, rec.Body)
	}

	// Bob may neither change nor revoke Alice's connection, and is told it does not exist.
	if rec := call(t, r.svc.HandleSetWorkers, "PUT", "/", `{"workers":["sp-prod"]}`, bob, map[string]string{"id": c.ID}); rec.Code != 404 {
		t.Errorf("bob set workers: %d", rec.Code)
	}
	if rec := call(t, r.svc.HandleRevoke(isAdmin), "POST", "/", `{}`, bob, map[string]string{"id": c.ID}); rec.Code != 404 {
		t.Errorf("bob revoked: %d", rec.Code)
	}
	// Allowing a Worker is the owner's consent, given in their own session: an
	// application Alice granted access to her account (an OAuth grant resolves to her
	// id, without a session) may not give it for her.
	aliceApp := &httpapi.Principal{UserID: "usr_alice", Roles: []string{"user"}}
	if rec := call(t, r.svc.HandleSetWorkers, "PUT", "/", `{"workers":["sp-prod"]}`, aliceApp, map[string]string{"id": c.ID}); rec.Code != 403 {
		t.Errorf("an application consented for alice: %d", rec.Code)
	}
	if rec := call(t, r.svc.HandleSetWorkers, "PUT", "/", `{"workers":["sp-prod"]}`, alice, map[string]string{"id": c.ID}); rec.Code != 200 {
		t.Errorf("alice set workers: %d %s", rec.Code, rec.Body)
	}
	if rec := call(t, r.svc.HandleSetWorkers, "PUT", "/", `{"workers":["ghost"]}`, alice, map[string]string{"id": c.ID}); rec.Code != 400 {
		t.Errorf("unknown worker: %d", rec.Code)
	}
	if rec := call(t, r.svc.HandleSetWorkers, "PUT", "/", `{`, alice, map[string]string{"id": c.ID}); rec.Code != 400 {
		t.Errorf("bad json: %d", rec.Code)
	}
	if rec := call(t, r.svc.HandleRevoke(isAdmin), "POST", "/", `{"reason":"offboarding"}`, admin, map[string]string{"id": c.ID}); rec.Code != 200 {
		t.Errorf("admin revoke: %d %s", rec.Code, rec.Body)
	}
	if rec := call(t, r.svc.HandleSetWorkers, "PUT", "/", `{"workers":[]}`, alice, map[string]string{"id": c.ID}); rec.Code != 409 {
		t.Errorf("revoked connection changed: %d", rec.Code)
	}
	if rec := call(t, r.svc.HandleRevoke(isAdmin), "POST", "/", ``, nil, map[string]string{"id": c.ID}); rec.Code != 401 {
		t.Errorf("anonymous revoke: %d", rec.Code)
	}
	if rec := call(t, r.svc.HandleSetWorkers, "PUT", "/", ``, nil, map[string]string{"id": c.ID}); rec.Code != 401 {
		t.Errorf("anonymous set: %d", rec.Code)
	}
	if rec := call(t, r.svc.HandleRevoke(isAdmin), "POST", "/", `{`, alice, map[string]string{"id": c.ID}); rec.Code != 400 {
		t.Errorf("bad json revoke: %d", rec.Code)
	}
}
