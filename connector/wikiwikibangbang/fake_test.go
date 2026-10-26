package wikiwikibangbang

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakeWiki is an in-memory wikiwikibangbang, answering the routes this Worker Type
// calls the way the wiki answered them when walked at commit 928fec0: the same status
// codes, the same {"error": …} envelope, 404 for an item that does not exist or may not
// be read, a unique property refused with 400, a transition refused from a state it does
// not leave, a body replaced whole, labels normalised, and cursor paging.
type fakeWiki struct {
	t     *testing.T
	token string
	srv   *httptest.Server

	mu       sync.Mutex
	me       fakeMe
	items    []*fakeItem
	comments map[string][]map[string]any
	// busy marks items a live session holds, whose body commit is refused with 409.
	busy map[string]bool
	// calls counts requests by "METHOD path", so a test can say what was not done.
	calls map[string]int
	// beforeBody runs once, as the next body commit arrives and before it is applied, so
	// a test can change the page in the window between an append's read and its write.
	beforeBody func(it *fakeItem)
	pageSize   int
}

type fakeMe struct {
	ID, Username   string
	Admin, Erasure bool
	Scope          string
}

type fakeItem struct {
	ID, Key, Type, Space, Parent, Title, State string
	Properties                                 map[string]any
	Labels                                     []string
	Body                                       []any
	Version                                    int
}

// The fake's types: a task with a workflow and a unique source-id, and a page.
var fakeTypes = []map[string]any{
	{"name": "page", "version": 1, "body": true, "fields": []any{}},
	{"name": "task", "version": 1, "body": true, "fields": []any{
		map[string]any{"name": "assignee", "kind": "user"},
		map[string]any{"name": "due", "kind": "date"},
		map[string]any{"name": "source-id", "kind": "text", "unique": true},
		map[string]any{"name": "points", "kind": "number"},
	}, "workflow": map[string]any{
		"initial": "open", "states": []any{"open", "in-progress", "done"},
		"transitions": []any{
			map[string]any{"name": "start", "from": []any{"open"}, "to": "in-progress"},
			map[string]any{"name": "finish", "from": []any{"open", "in-progress"}, "to": "done"},
			map[string]any{"name": "reopen", "from": []any{"done"}, "to": "open"},
		},
	}},
}

func newFakeWiki(t *testing.T) *fakeWiki {
	t.Helper()
	f := &fakeWiki{
		t: t, token: "wwbb_test-token",
		me:       fakeMe{ID: "8af20b46b102eefc02c562e60e2b095c", Username: "atlas-bot", Scope: "full"},
		comments: map[string][]map[string]any{},
		busy:     map[string]bool{},
		calls:    map[string]int{},
		pageSize: 100,
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

// client is a client of this fake, built the way a Worker builds one.
func (f *fakeWiki) client(t *testing.T) *Client {
	t.Helper()
	c, err := NewClient(f.srv.URL, `{"token":"`+f.token+`"}`, Options{HTTPClient: f.srv.Client()})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

func (f *fakeWiki) count(call string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[call]
}

func (f *fakeWiki) find(ref string) *fakeItem {
	for _, it := range f.items {
		if it.ID == ref || it.Key == ref {
			return it
		}
	}
	return nil
}

func (f *fakeWiki) add(space, typ, title string, props map[string]any, body []any) *fakeItem {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.addLocked(space, typ, "", title, props, body)
}

func (f *fakeWiki) addLocked(space, typ, parent, title string, props map[string]any, body []any) *fakeItem {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	n := 1
	for _, it := range f.items {
		if it.Space == space {
			n++
		}
	}
	if props == nil {
		props = map[string]any{}
	}
	if body == nil {
		body = []any{}
	}
	it := &fakeItem{ID: hex.EncodeToString(b), Key: space + "-" + strconv.Itoa(n), Type: typ, Space: space, Parent: parent,
		Title: title, Properties: props, Labels: []string{}, Body: body, Version: 1}
	if typ == "task" {
		it.State = "open"
	}
	f.items = append(f.items, it)
	return it
}

func (it *fakeItem) json() map[string]any {
	out := map[string]any{"id": it.ID, "key": it.Key, "type": it.Type, "typeVersion": 1, "space": it.Space,
		"title": it.Title, "properties": it.Properties, "body": it.Body, "version": it.Version,
		"presence": "live", "labels": it.Labels}
	if it.Parent != "" {
		out["parent"] = it.Parent
	}
	if it.State != "" {
		out["state"] = it.State
	}
	return out
}

func (f *fakeWiki) reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (f *fakeWiki) fail(w http.ResponseWriter, status int, msg string) {
	f.reply(w, status, map[string]any{"error": msg})
}

func (f *fakeWiki) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[r.Method+" "+r.URL.Path]++
	if r.Header.Get("Authorization") != "Bearer "+f.token {
		f.fail(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if r.Method != http.MethodGet && f.me.Scope == "read" {
		f.fail(w, http.StatusForbidden, "this token may only read")
		return
	}
	var body map[string]any
	if r.Body != nil && (r.Method == http.MethodPost || r.Method == http.MethodPut) {
		dec := json.NewDecoder(r.Body)
		dec.UseNumber()
		if err := dec.Decode(&body); err != nil && err != io.EOF {
			f.fail(w, http.StatusBadRequest, "invalid JSON")
			return
		}
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/")
	parts := strings.Split(path, "/")
	switch {
	case r.Method == http.MethodGet && path == "me":
		f.reply(w, 200, map[string]any{"admin": f.me.Admin, "erasure": f.me.Erasure, "scope": f.me.Scope,
			"user": map[string]any{"id": f.me.ID, "username": f.me.Username}, "via": "token"})
	case r.Method == http.MethodGet && path == "types":
		f.reply(w, 200, map[string]any{"types": fakeTypes})
	case r.Method == http.MethodGet && path == "items":
		f.list(w, r)
	case r.Method == http.MethodPost && path == "items":
		f.create(w, body)
	case len(parts) >= 2 && parts[0] == "items":
		it := f.find(parts[1])
		if it == nil {
			f.fail(w, http.StatusNotFound, fmt.Sprintf("not found: key %q", parts[1]))
			return
		}
		f.onItem(w, r, it, parts[2:], body)
	default:
		f.fail(w, http.StatusNotFound, "no such route")
	}
}

func (f *fakeWiki) create(w http.ResponseWriter, body map[string]any) {
	space, _ := body["space"].(string)
	typ, _ := body["type"].(string)
	title, _ := body["title"].(string)
	if space != "OPS" && space != "DOC" {
		f.fail(w, http.StatusForbidden, fmt.Sprintf("forbidden: %s may not create in %s", f.me.ID, space))
		return
	}
	if typ != "task" && typ != "page" {
		f.fail(w, http.StatusBadRequest, fmt.Sprintf("validation failed: no type %q", typ))
		return
	}
	if strings.TrimSpace(title) == "" {
		f.fail(w, http.StatusBadRequest, "validation failed: a title is required")
		return
	}
	props, _ := body["properties"].(map[string]any)
	for name, v := range props {
		if typ == "page" {
			f.fail(w, http.StatusBadRequest, fmt.Sprintf("validation failed: type %q has no field %q", typ, name))
			return
		}
		if name == "source-id" {
			for _, other := range f.items {
				if other.Properties["source-id"] == v {
					f.fail(w, http.StatusBadRequest, fmt.Sprintf("validation failed: field \"source-id\": %v is already used by %s", v, other.Key))
					return
				}
			}
		}
	}
	parent, _ := body["parent"].(string)
	if parent != "" && (len(parent) != 32 || f.find(parent) == nil) {
		f.fail(w, http.StatusBadRequest, fmt.Sprintf("validation failed: parent %q is not an item id", parent))
		return
	}
	var blocks []any
	if b, ok := body["body"].([]any); ok {
		blocks = b
	}
	it := f.addLocked(space, typ, parent, title, props, blocks)
	f.reply(w, http.StatusCreated, map[string]any{"item": it.json()})
}

func (f *fakeWiki) onItem(w http.ResponseWriter, r *http.Request, it *fakeItem, rest []string, body map[string]any) {
	route := r.Method + " " + strings.Join(rest, "/")
	switch {
	case route == "GET ":
		f.reply(w, 200, map[string]any{"item": it.json(), "editable": true})
	case route == "PUT title":
		it.Title, _ = body["title"].(string)
		it.Version++
		f.reply(w, 200, map[string]any{"item": it.json()})
	case r.Method == http.MethodPut && len(rest) == 2 && rest[0] == "properties":
		if it.Type != "task" {
			f.fail(w, http.StatusBadRequest, fmt.Sprintf("validation failed: type %q has no field %q", it.Type, rest[1]))
			return
		}
		if body["value"] == nil {
			delete(it.Properties, rest[1])
		} else {
			it.Properties[rest[1]] = body["value"]
		}
		it.Version++
		f.reply(w, 200, map[string]any{"item": it.json()})
	case route == "PUT labels":
		raw, _ := body["labels"].([]any)
		it.Labels = []string{}
		for _, l := range raw {
			s, _ := l.(string)
			it.Labels = append(it.Labels, strings.ReplaceAll(strings.ToLower(s), " ", "-"))
		}
		sort.Strings(it.Labels)
		it.Version++
		f.reply(w, 200, map[string]any{"item": it.json()})
	case route == "POST transitions":
		name, _ := body["name"].(string)
		for _, tr := range fakeTypes[1]["workflow"].(map[string]any)["transitions"].([]any) {
			tr := tr.(map[string]any)
			if tr["name"] != name || it.Type != "task" {
				continue
			}
			for _, from := range tr["from"].([]any) {
				if from == it.State {
					it.State = tr["to"].(string)
					it.Version++
					f.reply(w, 200, map[string]any{"item": it.json()})
					return
				}
			}
			f.fail(w, http.StatusBadRequest, fmt.Sprintf("validation failed: transition %q is not allowed from state %q", name, it.State))
			return
		}
		f.fail(w, http.StatusBadRequest, fmt.Sprintf("validation failed: type %q has no transition %q", it.Type, name))
	case route == "PUT body":
		if hook := f.beforeBody; hook != nil {
			f.beforeBody = nil
			hook(it)
		}
		if f.busy[it.ID] {
			f.fail(w, http.StatusConflict, "the body is being edited in a live session; join it instead")
			return
		}
		blocks, _ := body["blocks"].([]any)
		it.Body = blocks
		it.Version++
		f.reply(w, 200, map[string]any{"item": it.json()})
	case route == "POST comments":
		text, _ := body["text"].(string)
		c := map[string]any{"id": fmt.Sprintf("c%d", len(f.comments[it.ID])+1), "author": f.me.ID,
			"created": "2026-10-26T19:29:00Z", "text": text}
		f.comments[it.ID] = append(f.comments[it.ID], c)
		it.Version++
		f.reply(w, http.StatusCreated, map[string]any{"comment": c})
	case route == "GET comments":
		all := f.comments[it.ID]
		page, next := f.pageOf(len(all), r.URL.Query().Get("after"), func(i int) string { return all[i]["id"].(string) })
		out := []any{}
		for _, i := range page {
			out = append(out, all[i])
		}
		res := map[string]any{"comments": out, "total": map[string]any{"exact": true, "value": len(all)}}
		if next != "" {
			res["next"] = next
		}
		f.reply(w, 200, res)
	case route == "GET export/md":
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		fmt.Fprintf(w, "# %s\n\n- **Key:** %s\n", it.Title, it.Key)
	default:
		f.fail(w, http.StatusNotFound, "no such route")
	}
}

// list answers GET /api/items: by space, type and where, or by a query. The fake's
// query language knows three sentences — every item, the tasks, and the open tasks —
// and refuses every other the way the wiki refuses a malformed query.
func (f *fakeWiki) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var match func(it *fakeItem) bool
	if query := q.Get("query"); query != "" {
		switch query {
		case "ORDER BY key":
			match = func(*fakeItem) bool { return true }
		case "type = task":
			match = func(it *fakeItem) bool { return it.Type == "task" }
		case "type = task AND state != done":
			match = func(it *fakeItem) bool { return it.Type == "task" && it.State != "done" }
		default:
			f.reply(w, http.StatusBadRequest, map[string]any{"at": 6, "error": "at character 7: expected a value after ="})
			return
		}
	} else {
		field, value, _ := strings.Cut(q.Get("where"), "=")
		match = func(it *fakeItem) bool {
			if s := q.Get("space"); s != "" && it.Space != s {
				return false
			}
			if t := q.Get("type"); t != "" && it.Type != t {
				return false
			}
			if field != "" && fmt.Sprint(it.Properties[field]) != value {
				return false
			}
			return true
		}
	}
	var hits []*fakeItem
	for _, it := range f.items {
		if match(it) {
			hits = append(hits, it)
		}
	}
	limit := f.pageSize
	if l, err := strconv.Atoi(q.Get("limit")); err == nil && l > 0 && l < limit {
		limit = l
	}
	start := 0
	if after := q.Get("after"); after != "" {
		for i, it := range hits {
			if it.ID == after {
				start = i + 1
			}
		}
	}
	end := min(start+limit, len(hits))
	out := []any{}
	for _, it := range hits[start:end] {
		out = append(out, it.json())
	}
	res := map[string]any{"items": out, "total": map[string]any{"exact": true, "value": len(hits)}}
	if end < len(hits) {
		res["next"] = hits[end-1].ID
	}
	f.reply(w, 200, res)
}

// pageOf pages n entries by cursor, the way the wiki pages comments.
func (f *fakeWiki) pageOf(n int, after string, id func(int) string) ([]int, string) {
	start := 0
	if after != "" {
		for i := 0; i < n; i++ {
			if id(i) == after {
				start = i + 1
			}
		}
	}
	end := min(start+f.pageSize, n)
	var page []int
	for i := start; i < end; i++ {
		page = append(page, i)
	}
	next := ""
	if end < n {
		next = id(end - 1)
	}
	return page, next
}
