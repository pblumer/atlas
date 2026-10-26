package contentsource

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pblumer/atlas/api/contentsource/wikifake"
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/runloop"
)

// The recheck runner against the fake source, with a catalogue that is a map.

type fakeBound struct {
	mu      sync.Mutex
	targets map[string][]Target
	results map[string]Result
	rounds  int
}

func (b *fakeBound) Referenced() ([]string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []string
	for s := range b.targets {
		out = append(out, s)
	}
	return out, nil
}

func (b *fakeBound) Targets(source string) ([]Target, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]Target(nil), b.targets[source]...), nil
}

func (b *fakeBound) Record(_ string, rs []Result) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rounds++
	for _, r := range rs {
		b.results[r.Item] = r
	}
	return nil
}

func (b *fakeBound) result(item string) (Result, int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.results[item], b.rounds
}

func registry(t *testing.T, f *wikifake.Server) *Registry {
	t.Helper()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	quit := make(chan struct{})
	loop := runloop.New(quit)
	go loop.Run()
	t.Cleanup(func() { close(quit) })
	reg := NewRegistry(loop, store, func(ref string) string {
		if ref == "wiki" {
			return wikifake.Token
		}
		return ""
	})
	reg.Transport = f.TS.Client().Transport
	loop.Do(func() { _ = store.Save(testSource(f)) })
	return reg
}

func bound(items map[string][]Check) *fakeBound {
	b := &fakeBound{targets: map[string][]Target{}, results: map[string]Result{}}
	for item, checks := range items {
		b.targets["wiki"] = append(b.targets["wiki"], Target{Item: item, Checks: checks})
	}
	return b
}

func TestASweepRecordsEveryProductAndWatchesEveryAncestor(t *testing.T) {
	f := wiki(t)
	b := bound(map[string][]Check{"vpn": {{ID: "it_de", Passages: []string{"sec_pw"}}, {ID: "it_c1"}}, "mail": {{ID: "it_fr"}}})
	r := NewRunner(registry(t, f), b, func() int64 { return 42 })
	if err := r.Sweep(ctx(t), "wiki"); err != nil {
		t.Fatal(err)
	}
	for _, item := range []string{"vpn", "mail"} {
		if res, _ := b.result(item); !res.Passed || res.At != 42 {
			t.Fatalf("%s: %+v", item, res)
		}
	}
	// Every bound page and every ancestor walked, as item watches.
	want := []string{"it_c1", "it_de", "it_fr", "it_root"}
	if got := f.Watched(); !reflect.DeepEqual(got, want) {
		t.Fatalf("watched at the source = %v, want %v", got, want)
	}
	if got := r.Watched("wiki"); !reflect.DeepEqual(got, want) {
		t.Fatalf("watched by the runner = %v", got)
	}

	// A product unlinked: the pages only it needed are no longer watched, by id.
	b.mu.Lock()
	b.targets["wiki"] = []Target{{Item: "mail", Checks: []Check{{ID: "it_fr"}}}}
	b.mu.Unlock()
	if err := r.Sweep(ctx(t), "wiki"); err != nil {
		t.Fatal(err)
	}
	if got := f.Watched(); !reflect.DeepEqual(got, []string{"it_fr", "it_root"}) {
		t.Fatalf("watched after the unlink = %v", got)
	}
	// Nothing bound at all: every watch ends.
	b.mu.Lock()
	b.targets["wiki"] = nil
	b.mu.Unlock()
	_ = r.Sweep(ctx(t), "wiki")
	if got := f.Watched(); len(got) != 0 {
		t.Fatalf("watched with nothing bound = %v", got)
	}
}

func TestAnUnreachableSourceFailsEveryProduct(t *testing.T) {
	f := wiki(t)
	b := bound(map[string][]Check{"vpn": {{ID: "it_de"}}, "mail": {{ID: "it_fr"}}})
	r := NewRunner(registry(t, f), b, func() int64 { return 1 })
	f.Set(func(s *wikifake.Server) { s.Down = true })
	_ = r.Sweep(ctx(t), "wiki")
	for _, item := range []string{"vpn", "mail"} {
		res, _ := b.result(item)
		if res.Passed || !strings.Contains(res.Reason, "cannot be reached") {
			t.Fatalf("%s: %+v — no tolerance for an unreachable source", item, res)
		}
	}
}

func TestAWrongStandingOrAMissingSourceFailsEveryProduct(t *testing.T) {
	f := wiki(t)
	b := bound(map[string][]Check{"vpn": {{ID: "it_de"}}})
	reg := registry(t, f)
	r := NewRunner(reg, b, func() int64 { return 1 })
	f.Set(func(s *wikifake.Server) { s.System = []string{"admin"} })
	_ = r.Sweep(ctx(t), "wiki")
	if res, _ := b.result("vpn"); res.Passed || !strings.Contains(res.Reason, "standing") {
		t.Fatalf("%+v", res)
	}
	reg.loop.Do(func() { _ = reg.store.Delete("wiki") })
	_ = r.Sweep(ctx(t), "wiki")
	if res, _ := b.result("vpn"); res.Passed || !strings.Contains(res.Reason, "no longer configured") {
		t.Fatalf("%+v", res)
	}
	// A source whose credential resolves to nothing cannot be opened.
	reg.loop.Do(func() {
		s := testSource(f)
		s.CredentialsRef = "missing"
		_ = reg.store.Save(s)
	})
	_ = r.Sweep(ctx(t), "wiki")
	if res, _ := b.result("vpn"); res.Passed || !strings.Contains(res.Reason, "could not be opened") {
		t.Fatalf("%+v", res)
	}
}

// The goroutine: a sweep at start, then the feed. A change to a watched page
// rechecks the products holding it; an entry naming nothing rechecks everything.
func TestTheFeedRechecksAtOnce(t *testing.T) {
	f := wiki(t)
	b := bound(map[string][]Check{"vpn": {{ID: "it_de", Passages: []string{"sec_pw"}}}, "mail": {{ID: "it_fr"}}})
	r := NewRunner(registry(t, f), b, func() int64 { return 7 })
	r.Wait = time.Second
	quit := make(chan struct{})
	done := make(chan struct{})
	go func() { r.Run(quit); close(done) }()
	defer func() {
		close(quit)
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("the runner did not stop")
		}
	}()
	waitFor(t, func() bool { return len(f.Watched()) == 3 })
	if _, rounds := b.result("vpn"); rounds < 1 {
		t.Fatal("no sweep at start")
	}

	// The root restricted: the feed names it, and both products hold it.
	f.Edit("it_root", func(p *wikifake.Page) { p.ReadRestriction = []string{"u_atlas"} })
	waitFor(t, func() bool { res, _ := b.result("mail"); return !res.Passed })
	if res, _ := b.result("vpn"); res.Passed {
		t.Fatalf("vpn holds the root too: %+v", res)
	}

	// Restriction lifted, then a page made unreadable: the feed names nothing,
	// and everything is rechecked.
	f.Edit("it_root", func(p *wikifake.Page) { p.ReadRestriction = nil })
	waitFor(t, func() bool { res, _ := b.result("mail"); return res.Passed })
	f.Edit("it_de", func(p *wikifake.Page) { p.Unreadable = true })
	waitFor(t, func() bool { res, _ := b.result("vpn"); return !res.Passed })
	_, before := b.result("vpn")
	f.TouchAccount()
	waitFor(t, func() bool { _, n := b.result("vpn"); return n > before })
}

func TestTheFeedFailingQuarantinesAndBacksOff(t *testing.T) {
	f := wiki(t)
	b := bound(map[string][]Check{"vpn": {{ID: "it_de", Passages: []string{"sec_pw"}}}})
	r := NewRunner(registry(t, f), b, func() int64 { return 7 })
	r.Wait, r.Backoff = time.Second, 10*time.Millisecond
	c, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	waitFor(t, func() bool { return true })
	go func() { r.followSource(c, "wiki"); close(done) }()
	waitFor(t, func() bool { res, _ := b.result("vpn"); return res.Passed })
	f.Set(func(s *wikifake.Server) { s.Down = true })
	f.TouchAccount()
	waitFor(t, func() bool { res, _ := b.result("vpn"); return !res.Passed })
	f.Set(func(s *wikifake.Server) { s.Down = false })
	waitFor(t, func() bool { res, _ := b.result("vpn"); return res.Passed })
	cancel()
	<-done
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The administrator's routes.
func TestTheConfigurationRoutes(t *testing.T) {
	f := wiki(t)
	reg := registry(t, f)
	changed := 0
	reg.OnChange(func() { changed++ })
	svc := NewService(reg, func() int64 { return 5 })
	svc.InUse = func(id string) ([]string, error) {
		if id == "wiki" {
			return []string{"vpn"}, nil
		}
		return nil, nil
	}
	call := func(h http.HandlerFunc, method, body string, id string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/x", strings.NewReader(body))
		req = req.WithContext(httpapi.WithPrincipal(req.Context(), &httpapi.Principal{UserID: "u1", Username: "root"}))
		if id != "" {
			req.SetPathValue("id", id)
		}
		rec := httptest.NewRecorder()
		h(rec, req)
		return rec
	}
	other := `{"id":"wiki2","name":"Zweit","kind":"wikiwikibangbang","baseURL":"https://w2.example.org/","credentialsRef":"w2","excludedLabels":["X"]}`
	if rec := call(svc.HandleCreate, "POST", other, ""); rec.Code != http.StatusCreated ||
		!strings.Contains(rec.Body.String(), `"baseURL":"https://w2.example.org"`) {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	if rec := call(svc.HandleCreate, "POST", other, ""); rec.Code != http.StatusConflict {
		t.Fatalf("second create = %d", rec.Code)
	}
	if rec := call(svc.HandleCreate, "POST", `{"id":"x","name":"x","kind":"wikiwikibangbang","baseURL":"http://x","credentialsRef":"r"}`, ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("http base = %d", rec.Code)
	}
	if rec := call(svc.HandleCreate, "POST", `{`, ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("broken body = %d", rec.Code)
	}
	if rec := call(svc.HandleList, "GET", "", ""); rec.Code != 200 || strings.Count(rec.Body.String(), `"id"`) != 2 {
		t.Fatalf("list = %s", rec.Body)
	}
	if rec := call(svc.HandleGet, "GET", "", "wiki2"); rec.Code != 200 {
		t.Fatalf("get = %d", rec.Code)
	}
	if rec := call(svc.HandleGet, "GET", "", "none"); rec.Code != 404 {
		t.Fatalf("get none = %d", rec.Code)
	}
	upd := `{"name":"Zweit neu","kind":"wikiwikibangbang","baseURL":"https://w2.example.org","credentialsRef":"w2","linkInShop":true}`
	if rec := call(svc.HandleUpdate, "PUT", upd, "wiki2"); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"linkInShop":true`) {
		t.Fatalf("update = %d %s", rec.Code, rec.Body)
	}
	if rec := call(svc.HandleUpdate, "PUT", upd, "none"); rec.Code != 404 {
		t.Fatalf("update none = %d", rec.Code)
	}
	if rec := call(svc.HandleUpdate, "PUT", `{"id":"other"}`, "wiki2"); rec.Code != 400 {
		t.Fatalf("rename = %d", rec.Code)
	}
	if rec := call(svc.HandleDelete, "DELETE", "", "wiki"); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "vpn") {
		t.Fatalf("delete in use = %d %s", rec.Code, rec.Body)
	}
	if rec := call(svc.HandleDelete, "DELETE", "", "wiki2"); rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", rec.Code)
	}
	if changed != 3 {
		t.Fatalf("the runner was told %d times, want 3", changed)
	}

	// The check: standing as the source answers it, never a token.
	rec := call(svc.HandleCheck, "POST", "", "wiki")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"reachable":true`) || strings.Contains(rec.Body.String(), wikifake.Token) {
		t.Fatalf("check = %s", rec.Body)
	}
	f.Set(func(s *wikifake.Server) { s.Spaces = append(s.Spaces, wikifake.Grant{Space: "IT", Role: "editor"}) })
	if rec := call(svc.HandleCheck, "POST", "", "wiki"); !strings.Contains(rec.Body.String(), "editor") {
		t.Fatalf("check with an editor grant = %s", rec.Body)
	}
	f.Set(func(s *wikifake.Server) { s.Down = true })
	if rec := call(svc.HandleCheck, "POST", "", "wiki"); !strings.Contains(rec.Body.String(), `"reachable":false`) &&
		!strings.Contains(rec.Body.String(), "503") {
		t.Fatalf("check down = %s", rec.Body)
	}
	if rec := call(svc.HandleCheck, "POST", "", "none"); rec.Code != 404 {
		t.Fatalf("check none = %d", rec.Code)
	}
	ok, err := reg.Configured()
	if err != nil || !ok {
		t.Fatal("Configured")
	}
}

// TestKickRechecksASourceInTheBackground: an unlink kicks a sweep that records
// the products without waiting for the interval, and repeated reloads coalesce
// into one wake-up instead of blocking the caller.
func TestKickRechecksASourceInTheBackground(t *testing.T) {
	f := wiki(t)
	b := bound(map[string][]Check{"mail": {{ID: "it_fr"}}})
	r := NewRunner(registry(t, f), b, func() int64 { return 9 })
	r.Kick("wiki")
	r.wg.Wait()
	if res, rounds := b.result("mail"); !res.Passed || res.At != 9 || rounds != 1 {
		t.Fatalf("after a kick: %+v in %d rounds", res, rounds)
	}
	r.Reload()
	r.Reload()
	if len(r.wake) != 1 {
		t.Fatalf("two reloads left %d wake-ups, want one", len(r.wake))
	}
}

// TestTheConfigurationRoutesRefuseWhatTheyCannotUse: an empty configuration is an
// empty list, a body that is not JSON or not a usable source is a 400, and a
// delete whose bindings cannot be read is refused rather than guessed.
func TestTheConfigurationRoutesRefuseWhatTheyCannotUse(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	quit := make(chan struct{})
	loop := runloop.New(quit)
	go loop.Run()
	t.Cleanup(func() { close(quit) })
	svc := NewService(NewRegistry(loop, store, func(string) string { return "" }), func() int64 { return 1 })
	svc.InUse = func(string) ([]string, error) { return nil, errors.New("bindings unreadable") }
	call := func(h http.HandlerFunc, method, body, id string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/x", strings.NewReader(body))
		req = req.WithContext(httpapi.WithPrincipal(req.Context(), &httpapi.Principal{UserID: "u1", Username: "root"}))
		if id != "" {
			req.SetPathValue("id", id)
		}
		rec := httptest.NewRecorder()
		h(rec, req)
		return rec
	}
	if rec := call(svc.HandleList, "GET", "", ""); rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("empty list = %d %q, want []", rec.Code, rec.Body)
	}
	if rec := call(svc.HandleUpdate, "PUT", `{`, "wiki"); rec.Code != http.StatusBadRequest {
		t.Fatalf("broken body = %d", rec.Code)
	}
	if rec := call(svc.HandleUpdate, "PUT", `{"name":"x","kind":"wikiwikibangbang","baseURL":"http://x","credentialsRef":"r"}`, "wiki"); rec.Code != http.StatusBadRequest {
		t.Fatalf("http base = %d", rec.Code)
	}
	if rec := call(svc.HandleDelete, "DELETE", "", "wiki"); rec.Code != http.StatusInternalServerError ||
		!strings.Contains(rec.Body.String(), "bindings unreadable") {
		t.Fatalf("delete with unreadable bindings = %d %s", rec.Code, rec.Body)
	}
}
