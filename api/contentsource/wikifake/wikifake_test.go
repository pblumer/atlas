package wikifake

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// The fake stands in for wikiwikibangbang in every content-source test, so those
// tests prove Atlas against the source's contract only while the fake keeps it.
// These hold the fake to that contract directly, request by request: what an
// unreadable item answers, what a read token may not do, how a watch ends, and how
// the change feed names what it may not name.

// call sends one request with the read token (or another) and decodes the answer.
func call(t *testing.T, s *Server, method, path, token string) (int, map[string]any, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, s.URL()+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := s.TS.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, out, raw
}

func newFake(t *testing.T) *Server {
	t.Helper()
	s := New()
	t.Cleanup(s.Close)
	s.Put(Page{ID: "it_root", Key: "SVC-1", Space: "SVC", Title: "Services"})
	s.Put(Page{ID: "it_a", Key: "SVC-2", Space: "SVC", Parent: "it_root", Title: "VPN",
		Body:   []Block{{Kind: "paragraph", Text: "Sicher", Marks: []Mark{{Type: "bold", Start: 0, End: 3}}}},
		Labels: []string{"vpn"}, Properties: map[string]any{"pictures": []string{"logo.png"}}, State: "published"})
	s.Put(Page{ID: "it_b", Key: "SVC-3", Space: "SVC", Parent: "it_root", Title: "Mail",
		ReadRestriction: []string{"u_atlas"}, Held: true})
	s.Put(Page{ID: "it_gone", Key: "SVC-4", Space: "SVC", Parent: "it_root", Title: "Gone", Unreadable: true})
	s.Put(Page{ID: "it_trash", Key: "SVC-5", Space: "SVC", Parent: "it_root", Title: "Trash", Presence: "trashed"})
	s.Put(Page{ID: "it_quiet", Key: "SVC-6", Space: "SVC", Title: "Quiet", OmitPresence: true})
	return s
}

func TestTheFakeAnswersOnlyItsReadToken(t *testing.T) {
	s := newFake(t)
	if code, _, _ := call(t, s, "GET", "/api/me", ""); code != http.StatusUnauthorized {
		t.Fatalf("no token = %d", code)
	}
	if code, _, _ := call(t, s, "GET", "/api/me", "wrong"); code != http.StatusUnauthorized {
		t.Fatalf("wrong token = %d", code)
	}
	code, me, _ := call(t, s, "GET", "/api/me", Token)
	if code != 200 || me["scope"] != "read" || me["admin"] != false || me["erasure"] != false {
		t.Fatalf("me = %d %v", code, me)
	}
	if got := s.Tokens[len(s.Tokens)-1]; got != Token {
		t.Fatalf("recorded token = %q", got)
	}
	if !strings.HasPrefix(s.Seen[0], "GET /api/me") {
		t.Fatalf("seen = %v", s.Seen)
	}
	if code, _, _ := call(t, s, "GET", "/api/nowhere", Token); code != 404 {
		t.Fatalf("unknown route = %d", code)
	}
}

func TestTheFakeReportsTheAccountsStanding(t *testing.T) {
	s := newFake(t)
	s.Set(func(s *Server) {
		s.Scope, s.Admin, s.System = "write", true, []string{"admin"}
		s.Spaces = append(s.Spaces, Grant{Space: "HR", Role: "admin", Via: "group"})
	})
	_, me, _ := call(t, s, "GET", "/api/me", Token)
	if me["scope"] != "write" || me["admin"] != true {
		t.Fatalf("me = %v", me)
	}
	_, roles, _ := call(t, s, "GET", "/api/me/roles", Token)
	if sys, _ := roles["system"].([]any); len(sys) != 1 || sys[0] != "admin" {
		t.Fatalf("system = %v", roles["system"])
	}
	if sp, _ := roles["spaces"].([]any); len(sp) != 2 {
		t.Fatalf("spaces = %v", roles["spaces"])
	}
	// A source without the roles query answers 404, which an adapter must read as
	// a fact it could not establish.
	s.Set(func(s *Server) { s.RolesMissing = true })
	if code, _, _ := call(t, s, "GET", "/api/me/roles", Token); code != 404 {
		t.Fatalf("missing roles = %d", code)
	}
}

func TestTheFakeCanBeDownOrRedirectAway(t *testing.T) {
	s := newFake(t)
	s.Set(func(s *Server) { s.Down = true })
	if code, _, _ := call(t, s, "GET", "/api/me", Token); code != http.StatusServiceUnavailable {
		t.Fatalf("down = %d", code)
	}
	s.Set(func(s *Server) { s.Down, s.Redirect = false, "https://elsewhere.example" })
	req, _ := http.NewRequest("GET", s.URL()+"/api/me", nil)
	client := s.TS.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusFound || res.Header.Get("Location") != "https://elsewhere.example/api/me" {
		t.Fatalf("redirect = %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	if !strings.HasPrefix(s.URL(), "https://") || !strings.Contains(s.URL(), s.Host()) {
		t.Fatalf("URL %q, host %q", s.URL(), s.Host())
	}
}

func TestAnItemAnswersItsFactsAndOmitsWhatItDoesNotHave(t *testing.T) {
	s := newFake(t)
	code, out, _ := call(t, s, "GET", "/api/items/SVC-2", Token)
	item, _ := out["item"].(map[string]any)
	if code != 200 || item["id"] != "it_a" || item["parent"] != "it_root" || item["state"] != "published" ||
		item["presence"] != "live" || item["version"] != float64(1) {
		t.Fatalf("by key = %d %v", code, out)
	}
	if _, has := item["restriction"]; has {
		t.Error("an unrestricted item carries a restriction")
	}
	if _, has := item["hold"]; has {
		t.Error("an item with no hold carries one")
	}
	body, _ := item["body"].([]any)
	if blk, _ := body[0].(map[string]any); blk["marks"] == nil {
		t.Errorf("marks lost: %v", body)
	}
	_, out, _ = call(t, s, "GET", "/api/items/it_b", Token)
	item, _ = out["item"].(map[string]any)
	r, _ := item["restriction"].(map[string]any)
	if r == nil || len(r["read"].([]any)) != 1 || len(r["edit"].([]any)) != 0 || item["hold"] == nil {
		t.Fatalf("restricted, held = %v", item)
	}
	if lbl, _ := item["labels"].([]any); lbl == nil {
		t.Error("labels are null rather than empty")
	}
	_, out, _ = call(t, s, "GET", "/api/items/it_quiet", Token)
	if _, has := out["item"].(map[string]any)["presence"]; has {
		t.Error("an item whose source reports no presence carries one")
	}
	// Unreadable is non-existent, by id and by key alike.
	for _, ref := range []string{"it_gone", "SVC-4", "nothing"} {
		if code, _, _ := call(t, s, "GET", "/api/items/"+ref, Token); code != 404 {
			t.Errorf("%s = %d", ref, code)
		}
	}
	if code, _, _ := call(t, s, "GET", "/api/items/it_a/unknown", Token); code != 404 {
		t.Errorf("unknown item route = %d", code)
	}
}

func TestAReadTokenMayNotChangeContent(t *testing.T) {
	s := newFake(t)
	for _, m := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		if code, _, _ := call(t, s, m, "/api/items/it_a", Token); code != http.StatusForbidden {
			t.Errorf("%s = %d", m, code)
		}
	}
}

func TestEditBumpsTheVersion(t *testing.T) {
	s := newFake(t)
	s.Edit("it_a", func(p *Page) { p.Title = "VPN neu" })
	_, out, _ := call(t, s, "GET", "/api/items/it_a", Token)
	if it := out["item"].(map[string]any); it["version"] != float64(2) || it["title"] != "VPN neu" {
		t.Fatalf("edited = %v", it)
	}
	// Put replaces a page and keeps its place among its siblings.
	s.Put(Page{ID: "it_a", Key: "SVC-2", Space: "SVC", Parent: "it_root", Title: "VPN", Version: 7})
	_, out, _ = call(t, s, "GET", "/api/items/it_a", Token)
	if it := out["item"].(map[string]any); it["version"] != float64(7) {
		t.Fatalf("replaced = %v", it)
	}
}

func TestAttachmentsAreListedAndServedAsTheyWere(t *testing.T) {
	s := newFake(t)
	s.Attach("it_a", Attachment{ID: "att_1", Name: "logo.png", Type: "image/png", Data: []byte("PNG")})
	_, out, _ := call(t, s, "GET", "/api/items/it_a/attachments", Token)
	list, _ := out["attachments"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["size"] != float64(3) {
		t.Fatalf("attachments = %v", out)
	}
	code, _, raw := call(t, s, "GET", "/api/items/it_a/attachments/att_1", Token)
	if code != 200 || string(raw) != "PNG" {
		t.Fatalf("file = %d %q", code, raw)
	}
	if code, _, _ := call(t, s, "GET", "/api/items/it_a/attachments/att_9", Token); code != 404 {
		t.Fatalf("missing file = %d", code)
	}
	_, out, _ = call(t, s, "GET", "/api/items/it_b/attachments", Token)
	if list, _ := out["attachments"].([]any); list == nil || len(list) != 0 {
		t.Fatalf("no attachments = %v", out)
	}
}

func TestATreeListsLiveReadableChildrenInPages(t *testing.T) {
	s := newFake(t)
	for _, id := range []string{"it_c", "it_d"} {
		s.Put(Page{ID: id, Key: id, Space: "SVC", Parent: "it_root", Title: id})
	}
	s.Put(Page{ID: "it_other", Space: "HR", Parent: "it_root", Title: "elsewhere"})
	ids := func(out map[string]any) []string {
		var r []string
		for _, it := range out["items"].([]any) {
			r = append(r, it.(map[string]any)["id"].(string))
		}
		return r
	}
	_, out, _ := call(t, s, "GET", "/api/spaces/SVC/tree?parent=SVC-1", Token)
	if got := strings.Join(ids(out), ","); got != "it_a,it_b,it_c,it_d" {
		t.Fatalf("children = %s (unreadable, trashed and another space's are left out)", got)
	}
	_, out, _ = call(t, s, "GET", "/api/spaces/SVC/tree?parent=it_root&limit=2", Token)
	if got := strings.Join(ids(out), ","); got != "it_a,it_b" || out["next"] != "it_b" {
		t.Fatalf("first page = %s next %v", got, out["next"])
	}
	_, out, _ = call(t, s, "GET", "/api/spaces/SVC/tree?parent=it_root&limit=2&after=it_b", Token)
	if got := strings.Join(ids(out), ","); got != "it_c,it_d" || out["next"] != nil {
		t.Fatalf("second page = %s next %v", got, out["next"])
	}
	_, out, _ = call(t, s, "GET", "/api/spaces/SVC/tree?parent=it_d", Token)
	if items, _ := out["items"].([]any); items == nil || len(items) != 0 {
		t.Fatalf("a leaf = %v", out)
	}
	if code, _, _ := call(t, s, "GET", "/api/spaces/SVC/tree?parent=it_gone", Token); code != 404 {
		t.Fatalf("tree under an unreadable page = %d", code)
	}
}

func TestSearchFindsOnlyReadablePages(t *testing.T) {
	s := newFake(t)
	_, out, _ := call(t, s, "GET", "/api/search?q=E", Token)
	var titles []string
	for _, h := range out["hits"].([]any) {
		titles = append(titles, h.(map[string]any)["item"].(map[string]any)["title"].(string))
	}
	// Case-insensitive, in the source's order, and never the unreadable "Gone".
	if got := strings.Join(titles, ","); got != "Services,Quiet" {
		t.Fatalf("hits = %s", got)
	}
	_, out, _ = call(t, s, "GET", "/api/search?q=zzz", Token)
	if hits, _ := out["hits"].([]any); hits == nil || len(hits) != 0 {
		t.Fatalf("no hits = %v", out)
	}
}

func TestAWatchEndsByIdEvenOnAPageNoLongerReadable(t *testing.T) {
	s := newFake(t)
	if code, out, _ := call(t, s, "PUT", "/api/items/SVC-2/watch", Token); code != 200 || out["watching"] != true {
		t.Fatalf("watch by key = %d %v", code, out)
	}
	call(t, s, "PUT", "/api/items/it_b/watch", Token)
	if code, _, _ := call(t, s, "PUT", "/api/items/it_gone/watch", Token); code != 404 {
		t.Fatalf("watching an unreadable page = %d", code)
	}
	if got := strings.Join(s.Watched(), ","); got != "it_a,it_b" {
		t.Fatalf("watched = %s", got)
	}
	s.Edit("it_b", func(p *Page) { p.Unreadable = true })
	if code, out, _ := call(t, s, "DELETE", "/api/items/it_b/watch", Token); code != 200 || out["watching"] != false {
		t.Fatalf("unwatch an unreadable page by id = %d %v", code, out)
	}
	if code, _, _ := call(t, s, "DELETE", "/api/items/it_b/watch", Token); code != 404 {
		t.Fatalf("unwatch twice = %d", code)
	}
	if got := strings.Join(s.Watched(), ","); got != "it_a" {
		t.Fatalf("watched = %s", got)
	}
}

func TestTheFeedNamesOnlyWhatTheAccountMayRead(t *testing.T) {
	s := newFake(t)
	call(t, s, "PUT", "/api/items/it_a/watch", Token)
	call(t, s, "PUT", "/api/items/it_b/watch", Token)
	_, out, _ := call(t, s, "GET", "/api/changes", Token)
	if out["last"] != float64(0) || len(out["changes"].([]any)) != 0 {
		t.Fatalf("position = %v", out)
	}
	s.Touch("it_root") // not watched: no entry
	s.Edit("it_a", func(p *Page) { p.Title = "VPN 2" })
	s.Edit("it_b", func(p *Page) { p.Unreadable = true })
	s.TouchAccount()
	_, out, _ = call(t, s, "GET", "/api/changes?after=0", Token)
	changes := out["changes"].([]any)
	if len(changes) != 3 || out["last"] != float64(3) {
		t.Fatalf("changes = %v", out)
	}
	items := []string{}
	for _, c := range changes {
		items = append(items, c.(map[string]any)["item"].(string))
	}
	if strings.Join(items, "|") != "it_a||" {
		t.Fatalf("items = %q: an unreadable page and the account's roles name no item", items)
	}
	_, out, _ = call(t, s, "GET", "/api/changes?after=3", Token)
	if c, _ := out["changes"].([]any); c == nil || len(c) != 0 {
		t.Fatalf("nothing new = %v", out)
	}
}

func TestTheFeedWaitsForAChangeOrItsDeadline(t *testing.T) {
	s := newFake(t)
	call(t, s, "PUT", "/api/items/it_a/watch", Token)
	got := make(chan map[string]any, 1)
	go func() {
		_, out, _ := call(t, s, "GET", "/api/changes?after=0&wait=10", Token)
		got <- out
	}()
	// Touch until the waiting request has seen an entry: one appended before the
	// request starts is answered at once, one after wakes it.
	deadline := time.After(10 * time.Second)
	s.Touch("it_a")
	select {
	case out := <-got:
		if len(out["changes"].([]any)) == 0 {
			t.Fatalf("woken with nothing: %v", out)
		}
	case <-deadline:
		t.Fatal("the feed never answered")
	}
	// A wait with no change ends at its deadline with an empty delivery.
	start := time.Now()
	_, out, _ := call(t, s, "GET", "/api/changes?after=1&wait=1", Token)
	if c, _ := out["changes"].([]any); len(c) != 0 || time.Since(start) < 900*time.Millisecond {
		t.Fatalf("deadline = %v after %v", out, time.Since(start))
	}
}

func TestTheFeedWakesAWaitingReader(t *testing.T) {
	s := newFake(t)
	call(t, s, "PUT", "/api/items/it_a/watch", Token)
	got := make(chan map[string]any, 1)
	go func() {
		_, out, _ := call(t, s, "GET", "/api/changes?after=0&wait=20", Token)
		got <- out
	}()
	// Wait until the request is in the fake, then change the page.
	for i := 0; i < 500; i++ {
		s.mu.Lock()
		n := len(s.Seen)
		s.mu.Unlock()
		if n >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	s.Edit("it_a", func(p *Page) { p.Title = "woken" })
	select {
	case out := <-got:
		if len(out["changes"].([]any)) != 1 {
			t.Fatalf("woken = %v", out)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("a change did not wake the waiting reader")
	}
}
