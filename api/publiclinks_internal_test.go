package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func newPublicLinks(t *testing.T) *publicLinkStore {
	t.Helper()
	s, err := newPublicLinkStore(filepath.Join(t.TempDir(), "public-links"))
	if err != nil {
		t.Fatalf("newPublicLinkStore: %v", err)
	}
	return s
}

// TestNewPublicLinkStoreError covers the MkdirAll failure branch: a file where
// the directory should be.
func TestNewPublicLinkStoreError(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "public-links")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	if _, err := newPublicLinkStore(blocker); err == nil {
		t.Fatal("newPublicLinkStore over a file: want an error")
	}
}

// TestPublicLinkStoreRoundTrip saves links and reads them back newest-first, and
// fetches one by token.
func TestPublicLinkStoreRoundTrip(t *testing.T) {
	s := newPublicLinks(t)
	for _, l := range []publicLink{
		{Token: "aa", ProcessID: "p1", FormID: "f1", CreatedAt: 100},
		{Token: "bb", ProcessID: "p2", FormID: "f2", CreatedAt: 300},
		{Token: "cc", ProcessID: "p3", FormID: "f3", CreatedAt: 200},
	} {
		if err := s.Save(l); err != nil {
			t.Fatalf("save %s: %v", l.Token, err)
		}
	}
	got, err := s.LoadAll()
	if err != nil {
		t.Fatalf("loadAll: %v", err)
	}
	want := []string{"bb", "cc", "aa"} // CreatedAt descending
	if len(got) != len(want) {
		t.Fatalf("loadAll returned %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Token != w {
			t.Errorf("position %d = %q, want %q", i, got[i].Token, w)
		}
	}
	rec, ok, err := s.Get("bb")
	if err != nil || !ok || rec.ProcessID != "p2" {
		t.Fatalf("get bb = (%+v, %v, %v), want the saved record", rec, ok, err)
	}
}

// TestPublicLinkStoreGetMisses covers get's non-error miss branches: an empty
// token, a non-hex token (path-traversal guard), and an absent record.
func TestPublicLinkStoreGetMisses(t *testing.T) {
	s := newPublicLinks(t)
	for _, tok := range []string{"", "../etc", "nothex!", "abcd"} {
		rec, ok, err := s.Get(tok)
		if ok || err != nil {
			t.Errorf("get(%q) = (%+v, %v, %v), want a clean miss", tok, rec, ok, err)
		}
	}
}

// TestPublicLinkStoreGetCorrupt covers get's decode-error branch.
func TestPublicLinkStoreGetCorrupt(t *testing.T) {
	s := newPublicLinks(t)
	if err := os.WriteFile(s.FileFor("abcd"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("plant corrupt: %v", err)
	}
	if _, _, err := s.Get("abcd"); err == nil {
		t.Fatal("get of a corrupt record: want an error")
	}
}

// TestPublicLinkStoreGetReadError covers get's non-IsNotExist read error: a
// directory sits where the record file should be.
func TestPublicLinkStoreGetReadError(t *testing.T) {
	s := newPublicLinks(t)
	if err := os.Mkdir(s.FileFor("abcd"), 0o755); err != nil {
		t.Fatalf("mkdir record path: %v", err)
	}
	if _, _, err := s.Get("abcd"); err == nil {
		t.Fatal("get over a directory: want an error")
	}
}

// TestPublicLinkStoreDelete covers delete's happy path, its non-hex no-op guard,
// and its non-IsNotExist error branch (a non-empty directory can't be removed).
func TestPublicLinkStoreDelete(t *testing.T) {
	s := newPublicLinks(t)
	if err := s.Save(publicLink{Token: "abcd", ProcessID: "p"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := s.Delete("abcd"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok, _ := s.Get("abcd"); ok {
		t.Fatal("record still present after delete")
	}
	// Deleting an absent (and a non-hex) token is a no-op, not an error.
	if err := s.Delete("abcd"); err != nil {
		t.Errorf("delete absent: %v", err)
	}
	if err := s.Delete("nothex!"); err != nil {
		t.Errorf("delete non-hex: %v", err)
	}
	// A non-empty directory at the record path makes os.Remove fail.
	dirPath := s.FileFor("dddd")
	if err := os.Mkdir(dirPath, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dirPath, "child"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write child: %v", err)
	}
	if err := s.Delete("dddd"); err == nil {
		t.Fatal("delete of a non-empty directory: want an error")
	}
}

// TestPublicLinkStoreSaveError covers save's failure branch: a directory at the
// atomic-write temp path makes the write fail.
func TestPublicLinkStoreSaveError(t *testing.T) {
	s := newPublicLinks(t)
	tmp := s.FileFor("abcd") + ".tmp"
	if err := os.Mkdir(tmp, 0o755); err != nil {
		t.Fatalf("mkdir temp path: %v", err)
	}
	if err := s.Save(publicLink{Token: "abcd"}); err == nil {
		t.Fatal("save with a blocked temp path: want an error")
	}
}

// TestPublicLinkStoreLoadAllSkipsAndErrors covers loadAll skipping foreign files
// (non-.json, sub-directories, non-hex names) and its decode-error branch.
func TestPublicLinkStoreLoadAllSkipsAndErrors(t *testing.T) {
	s := newPublicLinks(t)
	if err := s.Save(publicLink{Token: "abcd", CreatedAt: 1}); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Foreign entries that loadAll must ignore.
	if err := os.WriteFile(filepath.Join(s.Dir(), "note.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write note: %v", err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir(), "nothex.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write nothex: %v", err)
	}
	if err := os.Mkdir(filepath.Join(s.Dir(), "sub.json"), 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	got, err := s.LoadAll()
	if err != nil {
		t.Fatalf("loadAll: %v", err)
	}
	if len(got) != 1 || got[0].Token != "abcd" {
		t.Fatalf("loadAll = %+v, want just the one hex record", got)
	}
	// A hex-named record that is a dangling symlink makes loadAll's read fail
	// (the entry isn't a directory, so it isn't skipped, but the read errors).
	if err := os.Symlink(filepath.Join(s.Dir(), "missing-target"), s.FileFor("dead")); err != nil {
		t.Fatalf("symlink hex record: %v", err)
	}
	if _, err := s.LoadAll(); err == nil {
		t.Fatal("loadAll over an unreadable record: want an error")
	}
	if err := os.Remove(s.FileFor("dead")); err != nil {
		t.Fatalf("cleanup symlink: %v", err)
	}
	// A corrupt hex record makes loadAll fail.
	if err := os.WriteFile(s.FileFor("beef"), []byte("{nope"), 0o644); err != nil {
		t.Fatalf("plant corrupt: %v", err)
	}
	if _, err := s.LoadAll(); err == nil {
		t.Fatal("loadAll with a corrupt record: want an error")
	}
}

// TestPublicLinkStoreLoadAllDirError covers loadAll's ReadDir error branch: the
// store directory is removed out from under it.
func TestPublicLinkStoreLoadAllDirError(t *testing.T) {
	s := newPublicLinks(t)
	if err := os.RemoveAll(s.Dir()); err != nil {
		t.Fatalf("remove dir: %v", err)
	}
	if _, err := s.LoadAll(); err == nil {
		t.Fatal("loadAll over a missing directory: want an error")
	}
}

func TestIsHexToken(t *testing.T) {
	cases := map[string]bool{
		"":       false,
		"abc":    false, // odd length
		"zz":     false, // not hex
		"ab":     true,
		"00ff":   true,
		"../etc": false,
	}
	for in, want := range cases {
		if got := isHexToken(in); got != want {
			t.Errorf("isHexToken(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestNewPublicTokenUnique(t *testing.T) {
	a, err := newPublicToken()
	if err != nil {
		t.Fatalf("newPublicToken: %v", err)
	}
	b, err := newPublicToken()
	if err != nil {
		t.Fatalf("newPublicToken: %v", err)
	}
	if a == b || !isHexToken(a) || len(a) != 64 {
		t.Fatalf("tokens not unique 64-char hex: %q %q", a, b)
	}
}

// TestRateLimiterAllow drives the token bucket with an injected clock: a fresh
// key starts full, a burst drains it to a deny, and time refills it.
func TestRateLimiterAllow(t *testing.T) {
	now := time.Unix(1000, 0)
	l := newRateLimiter(2, 1) // capacity 2, 1 token/sec
	l.now = func() time.Time { return now }

	if !l.allow("ip") || !l.allow("ip") {
		t.Fatal("first two requests should pass (bucket starts full)")
	}
	if l.allow("ip") {
		t.Fatal("third request should be denied (bucket empty)")
	}
	// A different key is independent and starts full.
	if !l.allow("other") {
		t.Fatal("a fresh key should start full")
	}
	// After 1 second one token refills.
	now = now.Add(time.Second)
	if !l.allow("ip") {
		t.Fatal("after a 1s refill the request should pass")
	}
	if l.allow("ip") {
		t.Fatal("only one token should have refilled")
	}
	// Refill is capped at capacity: a long idle doesn't over-fill.
	now = now.Add(time.Hour)
	if !l.allow("ip") || !l.allow("ip") {
		t.Fatal("after a long idle the bucket should be full again")
	}
	if l.allow("ip") {
		t.Fatal("refill must be capped at capacity")
	}
}

// TestRateLimiterEviction covers the map-bounding branch: past the cap the
// bookkeeping is dropped and every key restarts full (fail-open).
func TestRateLimiterEviction(t *testing.T) {
	l := newRateLimiter(1, 1)
	// Fill the map past its 10000-key bound.
	for i := 0; i < 10001; i++ {
		l.allow("k" + strconv.Itoa(i))
	}
	if len(l.tokens) > 10001 {
		t.Fatalf("map grew unbounded: %d keys", len(l.tokens))
	}
	// The very next call triggers (or has triggered) the reset and is allowed.
	if !l.allow("fresh") {
		t.Fatal("after eviction a key should start full and be allowed")
	}
}

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "203.0.113.7:54321"
	if got := clientIP(r); got != "203.0.113.7" {
		t.Errorf("clientIP host = %q, want 203.0.113.7", got)
	}
	// A RemoteAddr without a port falls back to the whole string.
	r.RemoteAddr = "unixsocket"
	if got := clientIP(r); got != "unixsocket" {
		t.Errorf("clientIP fallback = %q, want unixsocket", got)
	}
}

// TestPublicLinkHandlerStoreErrors covers the 500 branches of the public-link
// handlers by corrupting the link store the running server owns (white-box).
func TestPublicLinkHandlerStoreErrors(t *testing.T) {
	srv := newServerForErrors(t)
	h := srv.Handler()

	do := func(method, path, body string) int {
		var r *http.Request
		if body == "" {
			r = httptest.NewRequest(method, path, nil)
		} else {
			r = httptest.NewRequest(method, path, strings.NewReader(body))
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Code
	}

	// A corrupt hex-named record makes loadAll (list) fail.
	if err := os.WriteFile(srv.publicLinks.FileFor("beef"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("plant corrupt record: %v", err)
	}
	if code := do(http.MethodGet, "/api/v1/public-links", ""); code != http.StatusInternalServerError {
		t.Errorf("list with corrupt record: %d, want 500", code)
	}
	// The public schema/start endpoints read the link store by token: a corrupt
	// record surfaces a 500.
	if code := do(http.MethodGet, "/public/forms/beef/schema", ""); code != http.StatusInternalServerError {
		t.Errorf("public schema over corrupt record: %d, want 500", code)
	}
	if code := do(http.MethodPost, "/public/forms/beef/start", `{}`); code != http.StatusInternalServerError {
		t.Errorf("public start over corrupt record: %d, want 500", code)
	}
	if err := os.Remove(srv.publicLinks.FileFor("beef")); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	// A non-empty directory at a record path makes revoke's remove fail.
	dirPath := srv.publicLinks.FileFor("dddd")
	if err := os.Mkdir(dirPath, 0o755); err != nil {
		t.Fatalf("mkdir record path: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dirPath, "child"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write child: %v", err)
	}
	if code := do(http.MethodDelete, "/api/v1/public-links/dddd", ""); code != http.StatusInternalServerError {
		t.Errorf("revoke over a non-empty directory: %d, want 500", code)
	}

	// A malformed start body (not the {variables:{...}} shape parseStartVariables
	// expects) is a 400 before any store access.
	if code := do(http.MethodPost, "/public/forms/abcd/start", `{"variables":[]}`); code != http.StatusBadRequest {
		t.Errorf("malformed start body: %d, want 400", code)
	}
}

// TestPublicFormSchemaBranches covers handlePublicFormSchema and
// handlePublicFormStart branches that need a planted link whose process is not
// deployed: the best-effort process-name skip, a missing form (404), a corrupt
// form (500), and a valid token whose process is gone (404 on start).
func TestPublicFormSchemaBranches(t *testing.T) {
	srv := newServerForErrors(t)
	h := srv.Handler()
	do := func(method, path, body string) int {
		var r *http.Request
		if body == "" {
			r = httptest.NewRequest(method, path, nil)
		} else {
			r = httptest.NewRequest(method, path, strings.NewReader(body))
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Code
	}

	// A link whose process is not deployed but whose form exists: schema serves
	// with an empty process name (the deployment lookup is best-effort).
	if err := srv.forms.Save(form{ID: "pubform", Schema: `{"type":"default"}`}); err != nil {
		t.Fatalf("save form: %v", err)
	}
	if err := srv.publicLinks.Save(publicLink{Token: "aaaa", ProcessID: "ghost", FormID: "pubform", CreatedAt: 1}); err != nil {
		t.Fatalf("save link: %v", err)
	}
	if code := do(http.MethodGet, "/public/forms/aaaa/schema", ""); code != http.StatusOK {
		t.Errorf("schema for an undeployed process: %d, want 200", code)
	}
	// A valid token whose process is gone is a 404 on start (nothing to start).
	if code := do(http.MethodPost, "/public/forms/aaaa/start", `{"variables":{"x":1}}`); code != http.StatusNotFound {
		t.Errorf("start for an undeployed process: %d, want 404", code)
	}

	// A corrupt form record makes the schema read fail (500).
	if err := os.WriteFile(srv.forms.FileFor("pubform"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("corrupt form: %v", err)
	}
	if code := do(http.MethodGet, "/public/forms/aaaa/schema", ""); code != http.StatusInternalServerError {
		t.Errorf("schema over a corrupt form: %d, want 500", code)
	}
	// A missing form (link points at a form that no longer exists) is a 404.
	if err := os.Remove(srv.forms.FileFor("pubform")); err != nil {
		t.Fatalf("remove form: %v", err)
	}
	if code := do(http.MethodGet, "/public/forms/aaaa/schema", ""); code != http.StatusNotFound {
		t.Errorf("schema for a missing form: %d, want 404", code)
	}
}

// startFormBPMNInternal mirrors the api_test start-form fixture for white-box
// tests that need a deployed process whose start event binds a form.
const startFormBPMNInternal = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="onboard" isExecutable="true" name="Onboarding">
    <startEvent id="s">
      <extensionElements><zeebe:formDefinition formId="onboarding-form"/></extensionElements>
    </startEvent>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="e"/>
  </process>
</definitions>`

// TestCreatePublicLinkStoreError covers handleCreatePublicLink's store-failure
// arm (500): the process is deployed with a start form, but the link store's
// directory is gone, so loadAll/save fail.
func TestCreatePublicLinkStoreError(t *testing.T) {
	srv := newServerForErrors(t)
	h := srv.Handler()
	do := func(method, path, body, ct string) int {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if ct != "" {
			r.Header.Set("Content-Type", ct)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Code
	}

	if err := srv.forms.Save(form{ID: "onboarding-form", Schema: `{"type":"default"}`}); err != nil {
		t.Fatalf("save form: %v", err)
	}
	// A decoy process deployed first makes the deployment lookup iterate past a
	// non-matching entry before finding onboard.
	decoy := `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="decoy" isExecutable="true"><startEvent id="s"/><endEvent id="e"/><sequenceFlow id="f" sourceRef="s" targetRef="e"/></process></definitions>`
	if code := do(http.MethodPost, "/api/v1/deployments", decoy, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy decoy: %d", code)
	}
	if code := do(http.MethodPost, "/api/v1/deployments", startFormBPMNInternal, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy: %d", code)
	}
	// Remove the link store's directory so the durable read/write fails.
	if err := os.RemoveAll(srv.publicLinks.Dir()); err != nil {
		t.Fatalf("remove link dir: %v", err)
	}
	if code := do(http.MethodPost, "/api/v1/public-links", `{"processId":"onboard"}`, "application/json"); code != http.StatusInternalServerError {
		t.Errorf("create with a broken store: %d, want 500", code)
	}
}

// TestPublicLinkBodyReadError covers the io.ReadAll failure branch (400) of the
// create and public-start handlers via a request body that errors on read.
func TestPublicLinkBodyReadError(t *testing.T) {
	srv := newServerForErrors(t)
	h := srv.Handler()
	send := func(method, path string) int {
		r := httptest.NewRequest(method, path, errReader{})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Code
	}
	if code := send(http.MethodPost, "/api/v1/public-links"); code != http.StatusBadRequest {
		t.Errorf("create with an unreadable body: %d, want 400", code)
	}
	if code := send(http.MethodPost, "/public/forms/abcd/start"); code != http.StatusBadRequest {
		t.Errorf("public start with an unreadable body: %d, want 400", code)
	}
}

// TestPublicRateLimited covers the 429 branch of the public endpoints: a
// saturated limiter denies before any work.
func TestPublicRateLimited(t *testing.T) {
	srv := newServerForErrors(t)
	srv.publicRate = newRateLimiter(0, 0) // no tokens, no refill: always deny
	h := srv.Handler()

	for _, path := range []string{"/public/forms/abcd", "/public/forms/abcd/schema"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != http.StatusTooManyRequests {
			t.Errorf("GET %s under a saturated limiter: %d, want 429", path, rec.Code)
		}
	}
	r := httptest.NewRequest(http.MethodPost, "/public/forms/abcd/start", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("POST start under a saturated limiter: %d, want 429", rec.Code)
	}
}
