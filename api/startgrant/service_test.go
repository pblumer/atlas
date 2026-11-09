package startgrant

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/runloop"
	"github.com/pblumer/atlas/limits"
)

// fixture is a service over a real store and a real run loop, with the directory
// and the deployment registry stood in by maps the test controls.
type fixture struct {
	t        *testing.T
	dir      string
	store    *Store
	loop     *runloop.Loop
	svc      *Service
	auth     bool
	clock    int64
	users    map[string]Account
	groups   map[string][]string // group id -> member user ids
	apps     map[string]bool     // application id -> protected
	process  map[string]string   // process id -> application its newest version is filed under
	blockers map[string][]string // process id -> blockers
	dirErr   error
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "start-grants")
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	quit := make(chan struct{})
	loop := runloop.New(quit)
	done := make(chan struct{})
	go func() { loop.Run(); close(done) }()
	t.Cleanup(func() { close(quit); <-done })
	f := &fixture{
		t: t, dir: dir, store: store, loop: loop, auth: true, clock: 1762100000,
		users: map[string]Account{
			"usr_alice": {SelfService: true},
			"usr_bob":   {SelfService: true},
			"usr_off":   {SelfService: true, Disabled: true},
			"usr_ops":   {},
		},
		groups:   map[string][]string{"grp_field": {"usr_bob"}, "grp_empty": nil},
		apps:     map[string]bool{"app_field": false, "app_other": false, "app_system": true},
		process:  map[string]string{"safety": "app_field", "audit": "app_other", "sysproc": "app_system"},
		blockers: map[string][]string{},
	}
	f.svc = f.service(store)
	return f
}

func (f *fixture) service(store *Store) *Service {
	dir := Directory{
		User: func(id string) (Account, bool, error) {
			if f.dirErr != nil {
				return Account{}, false, f.dirErr
			}
			a, ok := f.users[id]
			return a, ok, nil
		},
		Group: func(id string) (bool, error) {
			if f.dirErr != nil {
				return false, f.dirErr
			}
			_, ok := f.groups[id]
			return ok, nil
		},
		GroupsOf: func(userID string) ([]string, error) {
			var out []string
			for g, members := range f.groups {
				for _, m := range members {
					if m == userID {
						out = append(out, g)
					}
				}
			}
			return out, nil
		},
	}
	resolve := func(app, proc string) (Target, error) {
		if f.dirErr != nil {
			return Target{}, f.dirErr
		}
		protected, found := f.apps[app]
		t := Target{ApplicationFound: found, ApplicationProtected: protected}
		if proc == "" {
			return t, nil
		}
		owner, deployed := f.process[proc]
		t.ProcessInApplication = found && deployed && owner == app
		if deployed {
			t.DefinitionKey, t.Version = 4242, 3
		}
		t.Blockers = f.blockers[proc]
		if !t.ProcessInApplication {
			t.Blockers = append(t.Blockers, BlockOtherApplication)
		}
		return t, nil
	}
	return NewService(f.loop, store, dir, resolve, func() bool { return f.auth }, func() int64 { return f.clock })
}

var (
	adminSession = &httpapi.Principal{UserID: "usr_admin", Username: "root", Roles: []string{"admin"}, Session: true}
	adminViaApp  = &httpapi.Principal{UserID: "usr_admin", Username: "root", Roles: []string{"admin"}}
)

type reply struct {
	code int
	body map[string]any
	raw  string
}

func (f *fixture) call(h http.HandlerFunc, method, target, body string, p *httpapi.Principal, path ...string) reply {
	f.t.Helper()
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	for i := 0; i+1 < len(path); i += 2 {
		r.SetPathValue(path[i], path[i+1])
	}
	if p != nil {
		r = r.WithContext(httpapi.WithPrincipal(r.Context(), p))
	}
	w := httptest.NewRecorder()
	h(w, r)
	out := reply{code: w.Code, raw: w.Body.String()}
	_ = json.Unmarshal(w.Body.Bytes(), &out.body)
	return out
}

func (f *fixture) put(app, proc, body string, p *httpapi.Principal) reply {
	return f.call(f.svc.HandlePut, http.MethodPut, "/x", body, p, "id", app, "processId", proc)
}

func (f *fixture) del(app, proc, query string, p *httpapi.Principal) reply {
	return f.call(f.svc.HandleDelete, http.MethodDelete, "/x?"+query, "", p, "id", app, "processId", proc)
}

func (f *fixture) get(app, proc string) reply {
	return f.call(f.svc.HandleGet, http.MethodGet, "/x", "", adminSession, "id", app, "processId", proc)
}

func ids(v any) []string {
	out := []string{}
	list, _ := v.([]any)
	for _, x := range list {
		s, _ := x.(string)
		out = append(out, s)
	}
	return out
}

func TestServiceRefusesWithoutAuthentication(t *testing.T) {
	f := newFixture(t)
	f.auth = false
	routes := []func() reply{
		func() reply {
			return f.call(f.svc.HandleList, http.MethodGet, "/x", "", nil, "id", "app_field")
		},
		func() reply { return f.get("app_field", "safety") },
		func() reply {
			return f.put("app_field", "safety", `{"userIds":["usr_alice"],"expectedRevision":0}`, nil)
		},
		func() reply { return f.del("app_field", "safety", "expectedRevision=0", nil) },
		func() reply {
			return f.call(f.svc.HandleHistory, http.MethodGet, "/x", "", nil, "id", "app_field", "processId", "safety")
		},
		func() reply {
			return f.call(f.svc.HandlePreview, http.MethodGet, "/x?userId=usr_alice", "", nil, "id", "app_field", "processId", "safety")
		},
	}
	for i, call := range routes {
		if got := call(); got.code != http.StatusForbidden {
			t.Errorf("route %d without authentication = %d %s, want 403", i, got.code, got.raw)
		}
	}
	if _, ok := f.store.Current("app_field", "safety"); ok {
		t.Fatal("an open server wrote a grant")
	}
}

func TestServiceNeedsAPrincipalAndAPersonsOwnSessionToWrite(t *testing.T) {
	f := newFixture(t)
	if got := f.get("app_field", "safety"); got.code != http.StatusOK {
		t.Fatalf("admin read = %d", got.code)
	}
	if got := f.call(f.svc.HandleGet, http.MethodGet, "/x", "", nil, "id", "app_field", "processId", "safety"); got.code != http.StatusUnauthorized {
		t.Fatalf("no principal = %d, want 401", got.code)
	}
	body := `{"userIds":["usr_alice"],"expectedRevision":0}`
	if got := f.put("app_field", "safety", body, nil); got.code != http.StatusUnauthorized {
		t.Fatalf("anonymous write = %d, want 401", got.code)
	}
	// An application a person granted access to acts for them but is not them.
	if got := f.put("app_field", "safety", body, adminViaApp); got.code != http.StatusForbidden {
		t.Fatalf("write through a delegated credential = %d, want 403", got.code)
	}
	if got := f.del("app_field", "safety", "expectedRevision=0", adminViaApp); got.code != http.StatusForbidden {
		t.Fatalf("revoke through a delegated credential = %d, want 403", got.code)
	}
	if _, ok := f.store.Current("app_field", "safety"); ok {
		t.Fatal("a refused write was stored")
	}
}

func TestServiceGrantIsAuditedWithTheServersActorAndTime(t *testing.T) {
	f := newFixture(t)
	got := f.put("app_field", "safety", `{"applicationId":"app_field","processId":"safety","userIds":["usr_alice"],"groupIds":["grp_field"],"expectedRevision":0}`, adminSession)
	if got.code != http.StatusOK {
		t.Fatalf("grant = %d %s", got.code, got.raw)
	}
	if got.body["changed"] != true || got.body["granted"] != true || got.body["revision"] != float64(1) {
		t.Fatalf("grant answer = %v", got.body)
	}
	if got.body["changedBy"] != "usr_admin" || got.body["changedAt"] != float64(f.clock) {
		t.Fatalf("audit fields = %v, want the server's actor and time", got.body)
	}
	if !f.store.Allowed("app_field", "safety", Principal{UserID: "usr_alice"}) ||
		!f.store.Allowed("app_field", "safety", Principal{UserID: "usr_bob", GroupIDs: []string{"grp_field"}}) {
		t.Fatal("grant not effective for its audience")
	}
	// The client may not state the audit fields.
	for _, forged := range []string{
		`{"userIds":["usr_alice"],"expectedRevision":1,"actorId":"usr_mallory"}`,
		`{"userIds":["usr_alice"],"expectedRevision":1,"at":1}`,
		`{"userIds":["usr_alice"],"expectedRevision":1,"revision":9}`,
	} {
		if r := f.put("app_field", "safety", forged, adminSession); r.code != http.StatusBadRequest {
			t.Errorf("forged audit field %s = %d, want 400", forged, r.code)
		}
	}
	h := f.call(f.svc.HandleHistory, http.MethodGet, "/x", "", adminSession, "id", "app_field", "processId", "safety")
	items, _ := h.body["items"].([]any)
	if h.code != http.StatusOK || len(items) != 1 {
		t.Fatalf("history = %d %s", h.code, h.raw)
	}
	first, _ := items[0].(map[string]any)
	if first["actorId"] != "usr_admin" || first["action"] != ActionGrant || first["at"] != float64(f.clock) {
		t.Fatalf("history entry = %v", first)
	}
}

func TestServiceRefusesMalformedGrants(t *testing.T) {
	f := newFixture(t)
	cases := map[string]struct {
		body string
		code int
	}{
		"no revision":        {`{"userIds":["usr_alice"]}`, http.StatusBadRequest},
		"empty audience":     {`{"userIds":[],"groupIds":[],"expectedRevision":0}`, http.StatusBadRequest},
		"duplicate user":     {`{"userIds":["usr_alice","usr_alice"],"expectedRevision":0}`, http.StatusBadRequest},
		"duplicate group":    {`{"groupIds":["grp_field","grp_field"],"expectedRevision":0}`, http.StatusBadRequest},
		"padded id":          {`{"userIds":[" usr_alice"],"expectedRevision":0}`, http.StatusBadRequest},
		"empty id":           {`{"userIds":[""],"expectedRevision":0}`, http.StatusBadRequest},
		"other application":  {`{"applicationId":"app_other","userIds":["usr_alice"],"expectedRevision":0}`, http.StatusBadRequest},
		"other process":      {`{"processId":"audit","userIds":["usr_alice"],"expectedRevision":0}`, http.StatusBadRequest},
		"not json":           {`{"userIds":`, http.StatusBadRequest},
		"trailing data":      {`{"userIds":["usr_alice"],"expectedRevision":0} {}`, http.StatusBadRequest},
		"a name, not an id":  {`{"userIds":["alice"],"expectedRevision":0}`, http.StatusBadRequest},
		"unknown group":      {`{"groupIds":["field-inspectors"],"expectedRevision":0}`, http.StatusBadRequest},
		"negative revision":  {`{"userIds":["usr_alice"],"expectedRevision":-1}`, http.StatusBadRequest},
		"oversized body":     {`{"userIds":["` + strings.Repeat("x", int(limits.Default().Request)) + `"],"expectedRevision":0}`, http.StatusRequestEntityTooLarge},
		"too many ids":       {audienceOf(maxAudience + 1), http.StatusBadRequest},
		"unknown field":      {`{"userIds":["usr_alice"],"expectedRevision":0,"role":"admin"}`, http.StatusBadRequest},
		"string for an list": {`{"userIds":"usr_alice","expectedRevision":0}`, http.StatusBadRequest},
	}
	for name, c := range cases {
		if got := f.put("app_field", "safety", c.body, adminSession); got.code != c.code {
			t.Errorf("%s = %d %s, want %d", name, got.code, got.raw, c.code)
		}
	}
	if bad := f.put(" app_field", "safety", `{"userIds":["usr_alice"],"expectedRevision":0}`, adminSession); bad.code != http.StatusBadRequest {
		t.Errorf("padded application id = %d, want 400", bad.code)
	}
	if _, ok := f.store.Current("app_field", "safety"); ok {
		t.Fatal("a refused grant was stored")
	}
}

func audienceOf(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = `"usr_` + strings.Repeat("a", i%7+1) + string(rune('a'+i%26)) + string(rune('a'+i/26%26)) + `"`
	}
	return `{"userIds":[` + strings.Join(parts, ",") + `],"expectedRevision":0}`
}

func TestServiceChecksApplicationProcessAndIdentities(t *testing.T) {
	f := newFixture(t)
	cases := map[string]struct {
		app, proc, body string
		code            int
	}{
		"unknown application":            {"app_gone", "safety", `{"userIds":["usr_alice"],"expectedRevision":0}`, http.StatusNotFound},
		"protected system application":   {"app_system", "sysproc", `{"userIds":["usr_alice"],"expectedRevision":0}`, http.StatusConflict},
		"process of another application": {"app_field", "audit", `{"userIds":["usr_alice"],"expectedRevision":0}`, http.StatusNotFound},
		"process not deployed":           {"app_field", "nothing", `{"userIds":["usr_alice"],"expectedRevision":0}`, http.StatusNotFound},
		"unknown user":                   {"app_field", "safety", `{"userIds":["usr_alice","usr_ghost"],"expectedRevision":0}`, http.StatusBadRequest},
		"unknown group":                  {"app_field", "safety", `{"groupIds":["grp_ghost"],"expectedRevision":0}`, http.StatusBadRequest},
	}
	for name, c := range cases {
		got := f.put(c.app, c.proc, c.body, adminSession)
		if got.code != c.code {
			t.Errorf("%s = %d %s, want %d", name, got.code, got.raw, c.code)
		}
		if _, ok := f.store.Current(c.app, c.proc); ok {
			t.Errorf("%s: refused grant was stored", name)
		}
	}
	if got := f.put("app_field", "safety", `{"userIds":["usr_ghost"],"groupIds":["grp_ghost"],"expectedRevision":0}`, adminSession); !strings.Contains(got.raw, "usr_ghost") || !strings.Contains(got.raw, "grp_ghost") {
		t.Errorf("refusal %s should name every unknown id", got.raw)
	}
	f.dirErr = errors.New("directory unreadable")
	if got := f.put("app_field", "safety", `{"userIds":["usr_alice"],"expectedRevision":0}`, adminSession); got.code != http.StatusInternalServerError {
		t.Errorf("unreadable directory = %d, want 500", got.code)
	}
	f.dirErr = nil
	f.users["usr_flaky"] = Account{}
	f.svc.dir.User = func(id string) (Account, bool, error) {
		if id == "usr_flaky" {
			return Account{}, false, errors.New("user store unreadable")
		}
		a, ok := f.users[id]
		return a, ok, nil
	}
	if got := f.put("app_field", "safety", `{"userIds":["usr_flaky"],"expectedRevision":0}`, adminSession); got.code != http.StatusInternalServerError {
		t.Errorf("unreadable account = %d, want 500", got.code)
	}
	f.svc.dir.Group = func(string) (bool, error) { return false, errors.New("group store unreadable") }
	if got := f.put("app_field", "safety", `{"groupIds":["grp_field"],"expectedRevision":0}`, adminSession); got.code != http.StatusInternalServerError {
		t.Errorf("unreadable group = %d, want 500", got.code)
	}
}

func TestServiceRevisionsDecideCompetingAdministrators(t *testing.T) {
	f := newFixture(t)
	if got := f.put("app_field", "safety", `{"userIds":["usr_alice"],"expectedRevision":0}`, adminSession); got.code != http.StatusOK {
		t.Fatalf("first grant = %d", got.code)
	}
	// Two administrators both read revision 1. The loop serializes their writes: the
	// first wins, the second is told it is stale and changes nothing.
	other := &httpapi.Principal{UserID: "usr_admin2", Roles: []string{"admin"}, Session: true}
	if got := f.put("app_field", "safety", `{"groupIds":["grp_field"],"expectedRevision":1}`, adminSession); got.code != http.StatusOK || got.body["revision"] != float64(2) {
		t.Fatalf("first competitor = %d %s", got.code, got.raw)
	}
	stale := f.put("app_field", "safety", `{"userIds":["usr_bob"],"expectedRevision":1}`, other)
	if stale.code != http.StatusConflict || stale.body["currentRevision"] != float64(2) {
		t.Fatalf("stale competitor = %d %s, want 409 with currentRevision 2", stale.code, stale.raw)
	}
	if staleDel := f.del("app_field", "safety", "expectedRevision=1", other); staleDel.code != http.StatusConflict {
		t.Fatalf("stale revoke = %d, want 409", staleDel.code)
	}
	cur, _ := f.store.Current("app_field", "safety")
	if cur.Revision != 2 || !reflect.DeepEqual(cur.Grant.GroupIDs, []string{"grp_field"}) || len(cur.Grant.UserIDs) != 0 {
		t.Fatalf("stale writers changed the grant: %+v", cur)
	}
	// Granting exactly what is granted writes nothing; order does not matter.
	if again := f.put("app_field", "safety", `{"groupIds":["grp_field"],"expectedRevision":2}`, adminSession); again.code != http.StatusOK || again.body["changed"] != false {
		t.Fatalf("repeated grant = %d %s, want 200 unchanged", again.code, again.raw)
	}
	if len(f.store.History("app_field", "safety")) != 2 {
		t.Fatal("a repeated grant wrote an audit entry")
	}
	sorted := f.put("app_field", "safety", `{"userIds":["usr_bob","usr_alice"],"expectedRevision":2}`, adminSession)
	if sorted.code != http.StatusOK || !reflect.DeepEqual(ids(sorted.body["userIds"]), []string{"usr_alice", "usr_bob"}) {
		t.Fatalf("audience not stored in canonical order: %d %s", sorted.code, sorted.raw)
	}
}

func TestServiceRevocationIsDurableIdempotentAndAlwaysPossible(t *testing.T) {
	f := newFixture(t)
	if got := f.del("app_field", "safety", "", adminSession); got.code != http.StatusBadRequest {
		t.Fatalf("revoke without revision = %d, want 400", got.code)
	}
	// Nothing granted: revoking is a no-op for an application that exists, 404 for one
	// that does not.
	if got := f.del("app_field", "safety", "expectedRevision=0", adminSession); got.code != http.StatusOK || got.body["changed"] != false {
		t.Fatalf("revoke of nothing = %d %s", got.code, got.raw)
	}
	if got := f.del("app_gone", "safety", "expectedRevision=0", adminSession); got.code != http.StatusNotFound {
		t.Fatalf("revoke in unknown application = %d, want 404", got.code)
	}
	if got := f.put("app_field", "safety", `{"userIds":["usr_alice"],"expectedRevision":0}`, adminSession); got.code != http.StatusOK {
		t.Fatalf("grant = %d", got.code)
	}
	// The application is deleted and the process gone; the right must still be
	// revocable.
	delete(f.apps, "app_field")
	delete(f.process, "safety")
	got := f.del("app_field", "safety", "expectedRevision=1", adminSession)
	if got.code != http.StatusOK || got.body["changed"] != true || got.body["granted"] != false || got.body["revision"] != float64(2) {
		t.Fatalf("revoke = %d %s", got.code, got.raw)
	}
	if f.store.Allowed("app_field", "safety", Principal{UserID: "usr_alice"}) {
		t.Fatal("revocation not effective for the next check")
	}
	again := f.del("app_field", "safety", "expectedRevision=1", adminSession)
	if again.code != http.StatusOK || again.body["changed"] != false || len(f.store.History("app_field", "safety")) != 2 {
		t.Fatalf("repeated revoke = %d %s, want 200 unchanged with no new audit entry", again.code, again.raw)
	}
	// A restart reads the revocation back: nothing resurrects.
	restarted, err := NewStore(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.Allowed("app_field", "safety", Principal{UserID: "usr_alice"}) {
		t.Fatal("revoked grant resurrected after restart")
	}
	svc := f.service(restarted)
	g := f.call(svc.HandleGet, http.MethodGet, "/x", "", adminSession, "id", "app_field", "processId", "safety")
	if g.code != http.StatusOK || g.body["revision"] != float64(2) || g.body["granted"] != false {
		t.Fatalf("recovered grant = %d %s", g.code, g.raw)
	}
	h := f.call(svc.HandleHistory, http.MethodGet, "/x", "", adminSession, "id", "app_field", "processId", "safety")
	items, _ := h.body["items"].([]any)
	if len(items) != 2 || items[0].(map[string]any)["action"] != ActionRevoke || items[1].(map[string]any)["action"] != ActionGrant {
		t.Fatalf("recovered history not newest first: %s", h.raw)
	}
}

func TestServiceReadsDefaultToDeny(t *testing.T) {
	f := newFixture(t)
	got := f.get("app_field", "safety")
	if got.code != http.StatusOK || got.body["revision"] != float64(0) || got.body["granted"] != false || len(ids(got.body["userIds"])) != 0 {
		t.Fatalf("never-granted scope = %d %s", got.code, got.raw)
	}
	if got := f.get("app_gone", "safety"); got.code != http.StatusNotFound {
		t.Fatalf("unknown application = %d, want 404", got.code)
	}
	if got := f.call(f.svc.HandleHistory, http.MethodGet, "/x", "", adminSession, "id", "app_gone", "processId", "safety"); got.code != http.StatusNotFound {
		t.Fatalf("history of unknown application = %d, want 404", got.code)
	}
	if got := f.call(f.svc.HandleHistory, http.MethodGet, "/x", "", adminSession, "id", "app_field", "processId", "safety"); got.code != http.StatusOK || got.body["total"] != float64(0) {
		t.Fatalf("empty history = %d %s", got.code, got.raw)
	}
	if got := f.call(f.svc.HandleGet, http.MethodGet, "/x", "", adminSession, "id", "app_field", "processId", "bad\nid"); got.code != http.StatusBadRequest {
		t.Fatalf("invalid process id = %d, want 400", got.code)
	}
}

func TestServiceListsOneApplicationsGrants(t *testing.T) {
	f := newFixture(t)
	f.process["second"] = "app_field"
	for _, p := range []string{"second", "safety"} {
		if got := f.put("app_field", p, `{"userIds":["usr_alice"],"expectedRevision":0}`, adminSession); got.code != http.StatusOK {
			t.Fatalf("grant %s = %d", p, got.code)
		}
	}
	if got := f.put("app_other", "audit", `{"userIds":["usr_alice"],"expectedRevision":0}`, adminSession); got.code != http.StatusOK {
		t.Fatalf("grant elsewhere = %d", got.code)
	}
	if got := f.del("app_field", "second", "expectedRevision=1", adminSession); got.code != http.StatusOK {
		t.Fatalf("revoke = %d", got.code)
	}
	l := f.call(f.svc.HandleList, http.MethodGet, "/x", "", adminSession, "id", "app_field")
	items, _ := l.body["items"].([]any)
	if l.code != http.StatusOK || len(items) != 2 {
		t.Fatalf("list = %d %s", l.code, l.raw)
	}
	a, b := items[0].(map[string]any), items[1].(map[string]any)
	if a["processId"] != "safety" || a["granted"] != true || b["processId"] != "second" || b["granted"] != false {
		t.Fatalf("list not by process id, or wrong state: %s", l.raw)
	}
	if strings.Contains(l.raw, "audit") {
		t.Fatalf("list leaked another application's grant: %s", l.raw)
	}
	if got := f.call(f.svc.HandleList, http.MethodGet, "/x", "", adminSession, "id", "app_gone"); got.code != http.StatusNotFound {
		t.Fatalf("list of unknown application = %d, want 404", got.code)
	}
	if got := f.call(f.svc.HandleList, http.MethodGet, "/x", "", adminSession, "id", ""); got.code != http.StatusBadRequest {
		t.Fatalf("list without an id = %d, want 400", got.code)
	}
	f.dirErr = errors.New("projects unreadable")
	if got := f.call(f.svc.HandleList, http.MethodGet, "/x", "", adminSession, "id", "app_field"); got.code != http.StatusInternalServerError {
		t.Fatalf("list with unreadable registry = %d, want 500", got.code)
	}
	if got := f.get("app_field", "safety"); got.code != http.StatusInternalServerError {
		t.Fatalf("get with unreadable registry = %d, want 500", got.code)
	}
	if got := f.call(f.svc.HandleHistory, http.MethodGet, "/x", "", adminSession, "id", "app_field", "processId", "safety"); got.code != http.StatusInternalServerError {
		t.Fatalf("history with unreadable registry = %d, want 500", got.code)
	}
	if got := f.del("app_field", "second", "expectedRevision=2", adminSession); got.code != http.StatusInternalServerError {
		t.Fatalf("revoke of revoked with unreadable registry = %d, want 500", got.code)
	}
	if got := f.call(f.svc.HandlePreview, http.MethodGet, "/x?userId=usr_alice", "", adminSession, "id", "app_field", "processId", "safety"); got.code != http.StatusInternalServerError {
		t.Fatalf("preview with unreadable registry = %d, want 500", got.code)
	}
}

func (f *fixture) preview(proc, query string) reply {
	return f.call(f.svc.HandlePreview, http.MethodGet, "/x?"+query, "", adminSession, "id", "app_field", "processId", proc)
}

func TestServicePreviewExplainsWhoWouldBeOffered(t *testing.T) {
	f := newFixture(t)
	if got := f.put("app_field", "safety", `{"userIds":["usr_alice","usr_off","usr_ops"],"groupIds":["grp_field"],"expectedRevision":0}`, adminSession); got.code != http.StatusOK {
		t.Fatalf("grant = %d %s", got.code, got.raw)
	}
	direct := f.preview("safety", "userId=usr_alice")
	if direct.code != http.StatusOK || direct.body["wouldBeOffered"] != true || !reflect.DeepEqual(ids(direct.body["matchedBy"]), []string{"user"}) {
		t.Fatalf("direct grant preview = %d %s", direct.code, direct.raw)
	}
	if direct.body["definitionKey"] != float64(4242) || direct.body["version"] != float64(3) {
		t.Fatalf("preview does not name the version that would start: %s", direct.raw)
	}
	viaGroup := f.preview("safety", "userId=usr_bob")
	if viaGroup.body["wouldBeOffered"] != true || !reflect.DeepEqual(ids(viaGroup.body["matchedBy"]), []string{"group:grp_field"}) {
		t.Fatalf("group grant preview = %s", viaGroup.raw)
	}
	// Leaving the group takes the right away; the directory is read now, not at grant time.
	f.groups["grp_field"] = nil
	if left := f.preview("safety", "userId=usr_bob"); left.body["granted"] != false || left.body["wouldBeOffered"] != false {
		t.Fatalf("preview after leaving the group = %s", left.raw)
	}
	off := f.preview("safety", "userId=usr_off")
	if off.body["granted"] != true || off.body["wouldBeOffered"] != false || !reflect.DeepEqual(ids(off.body["accountIssues"]), []string{"disabled"}) {
		t.Fatalf("disabled account preview = %s", off.raw)
	}
	ops := f.preview("safety", "userId=usr_ops")
	if ops.body["wouldBeOffered"] != false || !reflect.DeepEqual(ids(ops.body["accountIssues"]), []string{"missing-user-role"}) {
		t.Fatalf("account without the user role = %s", ops.raw)
	}
	group := f.preview("safety", "groupId=grp_field")
	if group.body["granted"] != true || group.body["wouldBeOffered"] != true || group.body["subjectKind"] != "group" {
		t.Fatalf("group preview = %s", group.raw)
	}
	if other := f.preview("safety", "groupId=grp_empty"); other.body["granted"] != false {
		t.Fatalf("ungranted group preview = %s", other.raw)
	}
	f.blockers["safety"] = []string{BlockInactive}
	blocked := f.preview("safety", "userId=usr_alice")
	if blocked.body["granted"] != true || blocked.body["startable"] != false || blocked.body["wouldBeOffered"] != false ||
		!reflect.DeepEqual(ids(blocked.body["blockers"]), []string{BlockInactive}) {
		t.Fatalf("blocked process preview = %s", blocked.raw)
	}
	for q, code := range map[string]int{
		"":                                   http.StatusBadRequest,
		"userId=usr_alice&groupId=grp_field": http.StatusBadRequest,
		"userId=%20usr_alice":                http.StatusBadRequest,
		"userId=usr_ghost":                   http.StatusNotFound,
		"groupId=grp_ghost":                  http.StatusNotFound,
	} {
		if got := f.preview("safety", q); got.code != code {
			t.Errorf("preview ?%s = %d, want %d", q, got.code, code)
		}
	}
	if got := f.call(f.svc.HandlePreview, http.MethodGet, "/x?userId=usr_alice", "", adminSession, "id", "app_gone", "processId", "safety"); got.code != http.StatusNotFound {
		t.Errorf("preview in unknown application = %d, want 404", got.code)
	}
	f.svc.dir.GroupsOf = func(string) ([]string, error) { return nil, errors.New("groups unreadable") }
	if got := f.preview("safety", "userId=usr_alice"); got.code != http.StatusInternalServerError {
		t.Errorf("preview with unreadable groups = %d, want 500", got.code)
	}
	f.svc.dir.User = func(string) (Account, bool, error) { return Account{}, false, errors.New("users unreadable") }
	if got := f.preview("safety", "userId=usr_alice"); got.code != http.StatusInternalServerError {
		t.Errorf("preview with unreadable users = %d, want 500", got.code)
	}
	f.svc.dir.Group = func(string) (bool, error) { return false, errors.New("groups unreadable") }
	if got := f.preview("safety", "groupId=grp_field"); got.code != http.StatusInternalServerError {
		t.Errorf("group preview with unreadable groups = %d, want 500", got.code)
	}
}

// TestServiceFailedWriteGrantsNothingAndSurvivesNoRestart is the persistence-failure
// case: a write whose fsync outcome is unknown is answered as a failure, nothing it
// tried to grant becomes effective, and the area refuses everything until a restart
// reads back what actually reached the disk.
func TestServiceFailedWriteGrantsNothingAndSurvivesNoRestart(t *testing.T) {
	f := newFixture(t)
	if err := os.RemoveAll(f.dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.dir, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := f.put("app_field", "safety", `{"userIds":["usr_alice"],"expectedRevision":0}`, adminSession)
	if got.code != http.StatusInternalServerError {
		t.Fatalf("failed write = %d %s, want 500", got.code, got.raw)
	}
	if f.store.Allowed("app_field", "safety", Principal{UserID: "usr_alice"}) {
		t.Fatal("a failed write became effective")
	}
	for name, r := range map[string]reply{
		"get":     f.get("app_field", "safety"),
		"put":     f.put("app_field", "safety", `{"userIds":["usr_alice"],"expectedRevision":0}`, adminSession),
		"delete":  f.del("app_field", "safety", "expectedRevision=0", adminSession),
		"list":    f.call(f.svc.HandleList, http.MethodGet, "/x", "", adminSession, "id", "app_field"),
		"history": f.call(f.svc.HandleHistory, http.MethodGet, "/x", "", adminSession, "id", "app_field", "processId", "safety"),
		"preview": f.preview("safety", "userId=usr_alice"),
	} {
		if r.code != http.StatusServiceUnavailable {
			t.Errorf("%s after an uncertain write = %d, want 503", name, r.code)
		}
	}
	// Restore the directory and restart: nothing the failed write attempted is there.
	if err := os.Remove(f.dir); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewStore(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := restarted.Current("app_field", "safety"); ok {
		t.Fatal("a failed write is visible after restart")
	}
	if got := f.call(f.service(restarted).HandlePut, http.MethodPut, "/x", `{"userIds":["usr_alice"],"expectedRevision":0}`, adminSession, "id", "app_field", "processId", "safety"); got.code != http.StatusOK {
		t.Fatalf("grant after recovery = %d %s", got.code, got.raw)
	}
}

func TestWriteOutcomeMapsStoreErrors(t *testing.T) {
	if code, _ := writeOutcome(ErrUncertainWrite); code != http.StatusServiceUnavailable {
		t.Fatalf("uncertain = %d", code)
	}
	if code, _ := writeOutcome(ErrStaleRevision); code != http.StatusConflict {
		t.Fatalf("stale = %d", code)
	}
	if code, _ := writeOutcome(nil); code != 0 {
		t.Fatalf("nil = %d", code)
	}
	w := httptest.NewRecorder()
	(&Service{}).answerWrite(w, http.StatusConflict, "explicit", 0, MutationView{})
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "explicit") {
		t.Fatalf("explicit conflict = %d %s", w.Code, w.Body.String())
	}
}

// TestServiceReadsSayWhetherTheProcessWouldBeOffered: the two grant reads carry the
// blockers of the process's newest version, by the same rule the preview and the
// self-service routes apply, so an administrator's table can say "offered" or why
// not without asking the preview once per subject (MFE1A).
func TestServiceReadsSayWhetherTheProcessWouldBeOffered(t *testing.T) {
	f := newFixture(t)
	never := f.get("app_field", "safety")
	if never.code != http.StatusOK || never.body["blockers"] == nil || len(ids(never.body["blockers"])) != 0 ||
		never.body["definitionKey"] != float64(4242) || never.body["version"] != float64(3) {
		t.Fatalf("never-granted eligible scope = %d %s, want blockers [] and the newest version", never.code, never.raw)
	}
	f.blockers["safety"] = []string{BlockInactive}
	if got := f.get("app_field", "safety"); !reflect.DeepEqual(ids(got.body["blockers"]), []string{BlockInactive}) {
		t.Fatalf("deactivated = %s", got.raw)
	}
	if got := f.put("app_field", "safety", `{"userIds":["usr_alice"],"expectedRevision":0}`, adminSession); got.code != http.StatusOK {
		t.Fatalf("grant = %d %s", got.code, got.raw)
	}
	// A grant whose process moved to another application keeps its record and says why
	// it offers nothing.
	f.process["safety"] = "app_other"
	got := f.get("app_field", "safety")
	if got.body["granted"] != true || !reflect.DeepEqual(ids(got.body["blockers"]), []string{BlockInactive, BlockOtherApplication}) {
		t.Fatalf("granted, process elsewhere = %s", got.raw)
	}
	delete(f.process, "safety")
	if got := f.get("app_field", "safety"); got.body["definitionKey"] != nil || got.body["version"] != nil {
		t.Fatalf("undeployed process names a version: %s", got.raw)
	}
}

func TestServiceListSaysPerProcessWhetherItWouldBeOffered(t *testing.T) {
	f := newFixture(t)
	f.process["second"] = "app_field"
	for _, p := range []string{"safety", "second"} {
		if got := f.put("app_field", p, `{"groupIds":["grp_field"],"expectedRevision":0}`, adminSession); got.code != http.StatusOK {
			t.Fatalf("grant %s = %d", p, got.code)
		}
	}
	f.blockers["second"] = []string{BlockNotExecutable}
	l := f.call(f.svc.HandleList, http.MethodGet, "/x", "", adminSession, "id", "app_field")
	items, _ := l.body["items"].([]any)
	if l.code != http.StatusOK || len(items) != 2 {
		t.Fatalf("list = %d %s", l.code, l.raw)
	}
	a, b := items[0].(map[string]any), items[1].(map[string]any)
	if a["processId"] != "safety" || len(ids(a["blockers"])) != 0 || a["blockers"] == nil || a["definitionKey"] != float64(4242) {
		t.Fatalf("eligible item = %v", a)
	}
	if b["processId"] != "second" || !reflect.DeepEqual(ids(b["blockers"]), []string{BlockNotExecutable}) {
		t.Fatalf("blocked item = %v", b)
	}
	// Writes answer the scope as it now stands and nothing about offering it: a
	// write's answer is not the table.
	w := f.put("app_field", "safety", `{"groupIds":["grp_field"],"userIds":["usr_bob"],"expectedRevision":1}`, adminSession)
	if _, has := w.body["blockers"]; has || w.code != http.StatusOK {
		t.Fatalf("write answer carries blockers: %d %s", w.code, w.raw)
	}
	// The resolver failing for one process fails the list rather than leaving a row
	// that looks offered.
	resolve := f.svc.resolve
	f.svc.resolve = func(app, proc string) (Target, error) {
		if proc == "second" {
			return Target{}, errors.New("registry unreadable")
		}
		return resolve(app, proc)
	}
	if got := f.call(f.svc.HandleList, http.MethodGet, "/x", "", adminSession, "id", "app_field"); got.code != http.StatusInternalServerError {
		t.Fatalf("list with an unreadable process = %d, want 500", got.code)
	}
}
