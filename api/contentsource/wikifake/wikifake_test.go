package wikifake_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/api/contentsource/wikifake"
)

// call sends one request to the fake with the given bearer token and decodes a
// JSON answer into out when out is non-nil. It returns the status and raw body.
func call(t *testing.T, f *wikifake.Server, method, path, token string, out any) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, f.URL()+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := f.TS.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			t.Fatalf("%s %s: decode %q: %v", method, path, body, err)
		}
	}
	return res.StatusCode, body
}

func get(t *testing.T, f *wikifake.Server, path string, out any) int {
	t.Helper()
	status, _ := call(t, f, http.MethodGet, path, wikifake.Token, out)
	return status
}

// seed is a small space: a root with two live children, one archived child, one
// the account may not read, and a page in another space.
func seed(t *testing.T) *wikifake.Server {
	t.Helper()
	f := wikifake.New()
	t.Cleanup(f.Close)
	f.Put(wikifake.Page{ID: "it_root", Key: "SVC-1", Space: "SVC", Title: "Services"})
	f.Put(wikifake.Page{ID: "it_vpn", Key: "SVC-2", Space: "SVC", Parent: "it_root", Title: "VPN access",
		Labels: []string{"network"}, Properties: map[string]any{"owner": "net"},
		Body: []wikifake.Block{{Kind: "paragraph", Text: "Ask the desk.",
			Marks: []wikifake.Mark{{Type: "secret", Start: 0, End: 3}}}},
		ReadRestriction: []string{"u_atlas"}, Held: true, State: "approved"})
	f.Put(wikifake.Page{ID: "it_mail", Key: "SVC-3", Space: "SVC", Parent: "it_root", Title: "Mailbox"})
	f.Put(wikifake.Page{ID: "it_old", Key: "SVC-4", Space: "SVC", Parent: "it_root", Title: "Fax", Presence: "archived"})
	f.Put(wikifake.Page{ID: "it_hidden", Key: "SVC-5", Space: "SVC", Parent: "it_root", Title: "VPN admin", Unreadable: true})
	f.Put(wikifake.Page{ID: "it_other", Key: "OPS-1", Space: "OPS", Parent: "it_root", Title: "Runbook"})
	return f
}

// TestTheFakeRefusesWhatTheSourceRefuses: no token, a wrong token, an outage and
// a redirect each answer before any route is read, as the source's front door does.
func TestTheFakeRefusesWhatTheSourceRefuses(t *testing.T) {
	f := seed(t)
	if status, _ := call(t, f, http.MethodGet, "/api/me", "", nil); status != http.StatusUnauthorized {
		t.Errorf("no token: status %d, want 401", status)
	}
	if status, _ := call(t, f, http.MethodGet, "/api/me", "wrong", nil); status != http.StatusUnauthorized {
		t.Errorf("wrong token: status %d, want 401", status)
	}
	if got := f.Tokens; !reflect.DeepEqual(got, []string{"", "wrong"}) {
		t.Errorf("Tokens = %q, want both presented tokens recorded", got)
	}
	f.Set(func(s *wikifake.Server) { s.Down = true })
	if status := get(t, f, "/api/me", nil); status != http.StatusServiceUnavailable {
		t.Errorf("down: status %d, want 503", status)
	}
	f.Set(func(s *wikifake.Server) { s.Down = false; s.Redirect = "https://elsewhere.example" })
	status, _ := call(t, f, http.MethodGet, "/api/me", wikifake.Token, nil)
	if status != http.StatusFound {
		t.Errorf("redirect: status %d, want 302", status)
	}
	f.Set(func(s *wikifake.Server) { s.Redirect = "" })
	if status := get(t, f, "/api/nowhere", nil); status != http.StatusNotFound {
		t.Errorf("unknown route: status %d, want 404", status)
	}
	if !strings.HasPrefix(f.URL(), "https://") || !strings.HasSuffix(f.URL(), f.Host()) {
		t.Errorf("URL %q and Host %q disagree", f.URL(), f.Host())
	}
	if len(f.Seen) != 5 || f.Seen[0] != "GET /api/me" {
		t.Errorf("Seen = %q, want every request recorded in order", f.Seen)
	}
}

func TestTheAccountReportsItsStanding(t *testing.T) {
	f := seed(t)
	var me struct {
		User  map[string]string `json:"user"`
		Scope string            `json:"scope"`
		Admin bool              `json:"admin"`
	}
	if status := get(t, f, "/api/me", &me); status != 200 || me.User["id"] != "u_atlas" || me.Scope != "read" || me.Admin {
		t.Errorf("me = %d %+v, want the read-scope viewer New starts with", status, me)
	}
	f.Set(func(s *wikifake.Server) { s.System = []string{"auditor"} })
	var roles struct {
		System []string         `json:"system"`
		Spaces []wikifake.Grant `json:"spaces"`
	}
	if status := get(t, f, "/api/me/roles", &roles); status != 200 ||
		!reflect.DeepEqual(roles.System, []string{"auditor"}) || len(roles.Spaces) != 1 || roles.Spaces[0].Space != "SVC" {
		t.Errorf("roles = %d %+v", status, roles)
	}
	f.Set(func(s *wikifake.Server) { s.RolesMissing = true })
	if status := get(t, f, "/api/me/roles", nil); status != http.StatusNotFound {
		t.Errorf("roles missing: status %d, want 404", status)
	}
}

type itemAnswer struct {
	Item     map[string]any `json:"item"`
	Editable bool           `json:"editable"`
}

// TestAnItemCarriesOnlyWhatItHas: restriction, hold, parent and state appear only
// when the page has them, an unreadable page is a 404 by id and by key, and a
// read token may not change anything.
func TestAnItemCarriesOnlyWhatItHas(t *testing.T) {
	f := seed(t)
	var full itemAnswer
	if status := get(t, f, "/api/items/SVC-2", &full); status != 200 {
		t.Fatalf("by key: status %d", status)
	}
	it := full.Item
	if it["id"] != "it_vpn" || it["parent"] != "it_root" || it["state"] != "approved" || it["presence"] != "live" || full.Editable {
		t.Errorf("item = %v", it)
	}
	if it["version"] != float64(1) {
		t.Errorf("version = %v, want the default 1", it["version"])
	}
	restriction, _ := it["restriction"].(map[string]any)
	if !reflect.DeepEqual(restriction["read"], []any{"u_atlas"}) || !reflect.DeepEqual(restriction["edit"], []any{}) {
		t.Errorf("restriction = %v, want read named and edit empty", it["restriction"])
	}
	if _, ok := it["hold"]; !ok {
		t.Error("a held page reports its hold")
	}
	body, _ := it["body"].([]any)
	if len(body) != 1 || body[0].(map[string]any)["marks"] == nil {
		t.Errorf("body = %v, want the block with its mark", it["body"])
	}

	var bare itemAnswer
	get(t, f, "/api/items/it_mail", &bare)
	for _, key := range []string{"restriction", "hold", "state"} {
		if _, ok := bare.Item[key]; ok {
			t.Errorf("a plain page carries %q: %v", key, bare.Item)
		}
	}
	if !reflect.DeepEqual(bare.Item["labels"], []any{}) || !reflect.DeepEqual(bare.Item["properties"], map[string]any{}) {
		t.Errorf("labels/properties = %v / %v, want empty, not null", bare.Item["labels"], bare.Item["properties"])
	}

	f.Put(wikifake.Page{ID: "it_quiet", Key: "SVC-6", Space: "SVC", Title: "Quiet", OmitPresence: true})
	var quiet itemAnswer
	get(t, f, "/api/items/it_quiet", &quiet)
	if _, ok := quiet.Item["presence"]; ok {
		t.Errorf("OmitPresence still reports presence: %v", quiet.Item)
	}

	for _, ref := range []string{"it_hidden", "SVC-5", "missing"} {
		if status := get(t, f, "/api/items/"+ref, nil); status != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", ref, status)
		}
	}
	if status, _ := call(t, f, http.MethodPost, "/api/items/it_mail", wikifake.Token, nil); status != http.StatusForbidden {
		t.Errorf("POST with a read token: status %d, want 403", status)
	}
	if status := get(t, f, "/api/items/it_mail/history", nil); status != http.StatusNotFound {
		t.Errorf("unknown item route: status %d, want 404", status)
	}
}

func TestAttachmentsAreListedAndServed(t *testing.T) {
	f := seed(t)
	f.Attach("it_vpn", wikifake.Attachment{ID: "at_1", Name: "guide.pdf", Type: "application/pdf", Data: []byte("%PDF-1")})
	var list struct {
		Attachments []map[string]any `json:"attachments"`
	}
	if status := get(t, f, "/api/items/it_vpn/attachments", &list); status != 200 ||
		len(list.Attachments) != 1 || list.Attachments[0]["name"] != "guide.pdf" || list.Attachments[0]["size"] != float64(6) {
		t.Errorf("attachments = %d %v", status, list.Attachments)
	}
	status, data := call(t, f, http.MethodGet, "/api/items/it_vpn/attachments/at_1", wikifake.Token, nil)
	if status != 200 || string(data) != "%PDF-1" {
		t.Errorf("file = %d %q, want the attached bytes", status, data)
	}
	if status := get(t, f, "/api/items/it_vpn/attachments/at_2", nil); status != http.StatusNotFound {
		t.Errorf("missing file: status %d, want 404", status)
	}
}

// TestTheTreeListsLiveReadableChildrenInPages: only live, readable pages of the
// asked space, in sibling order, with a cursor until the last page.
func TestTheTreeListsLiveReadableChildrenInPages(t *testing.T) {
	f := seed(t)
	type tree struct {
		Items []map[string]any `json:"items"`
		Next  string           `json:"next"`
		Total map[string]any   `json:"total"`
	}
	var first tree
	if status := get(t, f, "/api/spaces/SVC/tree?parent=SVC-1&limit=1", &first); status != 200 {
		t.Fatalf("tree: status %d", status)
	}
	if len(first.Items) != 1 || first.Items[0]["id"] != "it_vpn" || first.Next != "it_vpn" || first.Total["value"] != float64(2) {
		t.Errorf("first page = %+v, want it_vpn and a cursor over two children", first)
	}
	var second tree
	get(t, f, "/api/spaces/SVC/tree?parent=it_root&limit=1&after=it_vpn", &second)
	if len(second.Items) != 1 || second.Items[0]["id"] != "it_mail" || second.Next != "" {
		t.Errorf("second page = %+v, want it_mail and no cursor", second)
	}
	var all tree
	get(t, f, "/api/spaces/SVC/tree?parent=it_root", &all)
	if len(all.Items) != 2 {
		t.Errorf("default limit = %+v, want both live readable children", all.Items)
	}
	var none tree
	get(t, f, "/api/spaces/SVC/tree?parent=it_mail", &none)
	if none.Items == nil || len(none.Items) != 0 {
		t.Errorf("a leaf lists %v, want an empty list, not null", none.Items)
	}
	if status := get(t, f, "/api/spaces/SVC/tree?parent=it_hidden", nil); status != http.StatusNotFound {
		t.Errorf("unreadable parent: status %d, want 404", status)
	}
}

func TestSearchMatchesReadableTitles(t *testing.T) {
	f := seed(t)
	var res struct {
		Hits []struct {
			Item map[string]any `json:"item"`
		} `json:"hits"`
	}
	if status := get(t, f, "/api/search?q=vpn", &res); status != 200 {
		t.Fatalf("search: status %d", status)
	}
	if len(res.Hits) != 1 || res.Hits[0].Item["id"] != "it_vpn" {
		t.Errorf("hits = %v, want VPN access and not the unreadable VPN admin", res.Hits)
	}
}

func TestWatchesStartOnReadablePagesAndEndById(t *testing.T) {
	f := seed(t)
	var ans map[string]bool
	if status, _ := call(t, f, http.MethodPut, "/api/items/SVC-3/watch", wikifake.Token, &ans); status != 200 || !ans["watching"] {
		t.Errorf("watch by key = %d %v", status, ans)
	}
	call(t, f, http.MethodPut, "/api/items/it_vpn/watch", wikifake.Token, nil)
	if got := f.Watched(); !reflect.DeepEqual(got, []string{"it_mail", "it_vpn"}) {
		t.Errorf("Watched = %q, want the ids, sorted", got)
	}
	if status, _ := call(t, f, http.MethodPut, "/api/items/it_hidden/watch", wikifake.Token, nil); status != http.StatusNotFound {
		t.Errorf("watch unreadable: status %d, want 404", status)
	}
	f.Edit("it_vpn", func(p *wikifake.Page) { p.Unreadable = true })
	if status, _ := call(t, f, http.MethodDelete, "/api/items/it_vpn/watch", wikifake.Token, &ans); status != 200 || ans["watching"] {
		t.Errorf("unwatch a page that became unreadable = %d %v, want it ended", status, ans)
	}
	if status, _ := call(t, f, http.MethodDelete, "/api/items/it_vpn/watch", wikifake.Token, nil); status != http.StatusNotFound {
		t.Errorf("unwatch twice: status %d, want 404", status)
	}
}

type feedAnswer struct {
	Changes []struct {
		Seq  int64  `json:"seq"`
		Item string `json:"item"`
	} `json:"changes"`
	Last int64 `json:"last"`
}

// TestTheFeedNamesOnlyWhatTheAccountMayRead: a watched readable page is named, a
// watched unreadable one and an account change are "", an unwatched page writes
// nothing, and Edit bumps the version as it writes its entry.
func TestTheFeedNamesOnlyWhatTheAccountMayRead(t *testing.T) {
	f := seed(t)
	var start feedAnswer
	get(t, f, "/api/changes", &start)
	if start.Last != 0 || start.Changes == nil || len(start.Changes) != 0 {
		t.Fatalf("start = %+v, want no changes and last 0", start)
	}
	call(t, f, http.MethodPut, "/api/items/it_mail/watch", wikifake.Token, nil)
	call(t, f, http.MethodPut, "/api/items/it_vpn/watch", wikifake.Token, nil)
	f.Touch("it_root") // not watched: no entry
	f.Edit("it_mail", func(p *wikifake.Page) { p.Title = "Mailbox (new)" })
	f.Edit("it_vpn", func(p *wikifake.Page) { p.Unreadable = true })
	f.TouchAccount()

	var got feedAnswer
	get(t, f, "/api/changes?after=0", &got)
	items := []string{}
	for _, c := range got.Changes {
		items = append(items, c.Item)
	}
	if !reflect.DeepEqual(items, []string{"it_mail", "", ""}) || got.Last != 3 {
		t.Errorf("feed = %q last %d, want it_mail then two unnamed entries", items, got.Last)
	}
	var item itemAnswer
	get(t, f, "/api/items/it_mail", &item)
	if item.Item["version"] != float64(2) || item.Item["title"] != "Mailbox (new)" {
		t.Errorf("edited item = %v, want version 2 with the new title", item.Item)
	}
	var later feedAnswer
	get(t, f, "/api/changes?after=3", &later)
	if later.Changes == nil || len(later.Changes) != 0 || later.Last != 3 {
		t.Errorf("caught-up feed = %+v, want an empty list", later)
	}
}

// TestTheFeedWaitsForAChange: a long poll returns as soon as an entry is written,
// returns empty once its wait runs out, and ends when the caller gives up.
func TestTheFeedWaitsForAChange(t *testing.T) {
	f := seed(t)
	call(t, f, http.MethodPut, "/api/items/it_mail/watch", wikifake.Token, nil)
	go func() {
		time.Sleep(50 * time.Millisecond)
		f.Touch("it_mail")
	}()
	var woke feedAnswer
	get(t, f, "/api/changes?after=0&wait=10", &woke)
	if len(woke.Changes) != 1 || woke.Changes[0].Item != "it_mail" {
		t.Errorf("woken feed = %+v, want the entry written while it waited", woke)
	}

	began := time.Now()
	var empty feedAnswer
	get(t, f, "/api/changes?after=1&wait=1", &empty)
	if len(empty.Changes) != 0 || time.Since(began) < 900*time.Millisecond {
		t.Errorf("timed-out feed = %+v after %v, want empty after its wait", empty, time.Since(began))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, f.URL()+"/api/changes?after=1&wait=10", nil)
	req.Header.Set("Authorization", "Bearer "+wikifake.Token)
	if res, err := f.TS.Client().Do(req); err == nil {
		res.Body.Close()
		t.Error("a poll the caller abandoned answered anyway")
	}
}
