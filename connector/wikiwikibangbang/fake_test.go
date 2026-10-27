package wikiwikibangbang

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeWiki is an in-memory wikiwikibangbang, answering the routes this Worker Type
// calls the way the wiki answered them when walked at commit 928fec0: the same status
// codes, the same {"error": …} envelope, 404 for an item that does not exist or may not
// be read, a unique property refused with 400, a transition refused from a state it does
// not leave, a body replaced whole, labels normalised, and cursor paging.
//
// It keeps the wiki's change feed (its ADR-0121) the way the wiki keeps it: one log of
// every change, an entry listed only while the account watched the item's space at the
// time, a watch that is itself a change but never listed, and "last" as the cursor to
// continue from.
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

	// log is the change feed, watches the spans each space was watched in, and clock the
	// time every change, every stateSince and every Date header is taken from.
	log     []fakeChange
	watches map[string][]fakeSpan
	clock   time.Time
	// queries are the queries the listing was asked, in order.
	queries []string
	// widen answers a watch's question with every finished task, of any space and asked
	// for or not, as a wiki that misread it would.
	widen bool
}

// fakeChange is one change of the log. A watch is one too, and is never listed.
type fakeChange struct {
	Seq   int
	Item  string
	Space string
	At    time.Time
	Watch bool
}

// fakeSpan is the time a watch held: after the change from, before the change to (0
// while it holds).
type fakeSpan struct{ from, to int }

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
	// StateSince is when a task reached its state.
	StateSince time.Time
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
		watches:  map[string][]fakeSpan{},
		clock:    time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC),
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
		Title: title, Properties: props, Labels: []string{}, Body: body}
	if typ == "task" {
		it.State, it.StateSince = "open", f.clock
	}
	f.items = append(f.items, it)
	f.touch(it)
	return it
}

// touch records a change of an item: its version moves on and the log gains an entry.
func (f *fakeWiki) touch(it *fakeItem) {
	it.Version++
	f.log = append(f.log, fakeChange{Seq: len(f.log) + 1, Item: it.ID, Space: it.Space, At: f.clock})
}

// tick moves the fake's clock on.
func (f *fakeWiki) tick(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clock = f.clock.Add(d)
}

// setState moves a task to a state, as a person in the wiki's UI would.
func (f *fakeWiki) setState(it *fakeItem, state string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	it.State, it.StateSince = state, f.clock
	f.touch(it)
}

// edit changes an item's title, as a person in the wiki's UI would.
func (f *fakeWiki) edit(it *fakeItem, title string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	it.Title = title
	f.touch(it)
}

// watch starts or ends the account's watch of a space, as the wiki records one.
func (f *fakeWiki) watch(space string, on bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.watchLocked(space, on)
}

func (f *fakeWiki) watchLocked(space string, on bool) {
	spans := f.watches[space]
	holds := len(spans) > 0 && spans[len(spans)-1].to == 0
	if holds == on {
		// The wiki records only a real change.
		return
	}
	seq := len(f.log) + 1
	f.log = append(f.log, fakeChange{Seq: seq, Space: space, At: f.clock, Watch: true})
	if on {
		f.watches[space] = append(spans, fakeSpan{from: seq})
	} else {
		spans[len(spans)-1].to = seq
	}
}

// spaceEvent records a change of a whole space — a rename — which the feed lists with
// no item.
func (f *fakeWiki) spaceEvent(space string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log = append(f.log, fakeChange{Seq: len(f.log) + 1, Space: space, At: f.clock})
}

// restore cuts the log back to its first n changes, as a wiki restored from a backup
// would be.
func (f *fakeWiki) restore(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.log = f.log[:n]
}

// lastQuery is the last query the listing was asked.
func (f *fakeWiki) lastQuery() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.queries) == 0 {
		return ""
	}
	return f.queries[len(f.queries)-1]
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
	if !it.StateSince.IsZero() {
		out["stateSince"] = it.StateSince
	}
	return out
}

func (f *fakeWiki) reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Date", f.clock.Format(http.TimeFormat))
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
	// Watching is the one write a token of scope read may make.
	watchRoute := strings.HasPrefix(r.URL.Path, "/api/spaces/") && strings.HasSuffix(r.URL.Path, "/watch")
	if r.Method != http.MethodGet && f.me.Scope == "read" && !watchRoute {
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
	case r.Method == http.MethodGet && path == "changes":
		f.changes(w, r)
	case r.Method == http.MethodPut && watchRoute && len(parts) == 3:
		if parts[1] != "OPS" && parts[1] != "DOC" {
			f.fail(w, http.StatusNotFound, fmt.Sprintf("not found: space %q", parts[1]))
			return
		}
		f.watchLocked(parts[1], true)
		f.reply(w, 200, map[string]any{"watching": true})
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
		f.touch(it)
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
		f.touch(it)
		f.reply(w, 200, map[string]any{"item": it.json()})
	case route == "PUT labels":
		raw, _ := body["labels"].([]any)
		it.Labels = []string{}
		for _, l := range raw {
			s, _ := l.(string)
			it.Labels = append(it.Labels, strings.ReplaceAll(strings.ToLower(s), " ", "-"))
		}
		sort.Strings(it.Labels)
		f.touch(it)
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
					it.StateSince = f.clock
					f.touch(it)
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
		f.touch(it)
		f.reply(w, 200, map[string]any{"item": it.json()})
	case route == "POST comments":
		text, _ := body["text"].(string)
		c := map[string]any{"id": fmt.Sprintf("c%d", len(f.comments[it.ID])+1), "author": f.me.ID,
			"created": "2026-10-26T19:29:00Z", "text": text}
		f.comments[it.ID] = append(f.comments[it.ID], c)
		f.touch(it)
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

// fakeSentences are the sentences of the wiki's query language the fake knows.
var fakeSentences = map[string]func(it *fakeItem) bool{
	"ORDER BY key":                  func(*fakeItem) bool { return true },
	"type = task":                   func(it *fakeItem) bool { return it.Type == "task" },
	"type = task AND state != done": func(it *fakeItem) bool { return it.Type == "task" && it.State != "done" },
	`label = "atlas"`:               func(it *fakeItem) bool { return slices.Contains(it.Labels, "atlas") },
}

// fakeFinishedQuery is the query a watch asks: the finished items of a space among the
// ones it names, narrowed by a sentence of the watch's own.
var fakeFinishedQuery = regexp.MustCompile(`^(?:\((.+)\) AND )?space = "([A-Z0-9]+)" AND finished = true AND id IN \(([^)]*)\)$`)

// sentence is the fake's reading of a query: one of the sentences it knows, or a watch's.
func (f *fakeWiki) sentence(query string) (func(it *fakeItem) bool, bool) {
	if match, ok := fakeSentences[query]; ok {
		return match, true
	}
	m := fakeFinishedQuery.FindStringSubmatch(query)
	if m == nil {
		return nil, false
	}
	narrow := func(*fakeItem) bool { return true }
	if m[1] != "" {
		var ok bool
		if narrow, ok = fakeSentences[m[1]]; !ok {
			return nil, false
		}
	}
	ids := map[string]bool{}
	for _, raw := range strings.Split(m[3], ",") {
		id, err := strconv.Unquote(strings.TrimSpace(raw))
		if err != nil {
			return nil, false
		}
		ids[id] = true
	}
	return func(it *fakeItem) bool {
		// A task's last state is its finished one, as the wiki's workflow decides.
		finished := narrow(it) && it.Type == "task" && it.State == "done"
		return finished && (f.widen || it.Space == m[2] && ids[it.ID])
	}, true
}

// list answers GET /api/items: by space, type and where, or by a query. The fake's
// query language knows a handful of sentences and a watch's question, and refuses
// every other the way the wiki refuses a malformed query.
func (f *fakeWiki) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var match func(it *fakeItem) bool
	if query := q.Get("query"); query != "" {
		f.queries = append(f.queries, query)
		var ok bool
		if match, ok = f.sentence(query); !ok {
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

// changes answers GET /api/changes the way the wiki does: without after, no changes
// and the newest; with it, the changes after it on a space the account watched at the
// time, oldest first, at most limit, and last the cursor to continue from — the last
// entry's when the page is full, the newest change otherwise.
func (f *fakeWiki) changes(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	head := len(f.log)
	if q.Get("after") == "" {
		f.reply(w, 200, map[string]any{"changes": []any{}, "last": head})
		return
	}
	after, err := strconv.Atoi(q.Get("after"))
	if err != nil || after < 0 {
		f.fail(w, http.StatusBadRequest, "after must be 0 to 9223372036854775807")
		return
	}
	limit := 100
	if l := q.Get("limit"); l != "" {
		if limit, err = strconv.Atoi(l); err != nil || limit < 1 || limit > 1000 {
			f.fail(w, http.StatusBadRequest, "limit must be 1 to 1000")
			return
		}
	}
	out := []any{}
	last := head
	for _, c := range f.log[min(after, head):] {
		if c.Watch || !f.watchedAt(c.Space, c.Seq) {
			continue
		}
		if len(out) == limit {
			last = out[len(out)-1].(map[string]any)["seq"].(int)
			break
		}
		out = append(out, map[string]any{"seq": c.Seq, "item": c.Item, "at": c.At})
	}
	f.reply(w, 200, map[string]any{"changes": out, "last": last})
}

// watchedAt reports whether the account watched a space at the time of a change.
func (f *fakeWiki) watchedAt(space string, seq int) bool {
	for _, sp := range f.watches[space] {
		if sp.from < seq && (sp.to == 0 || seq < sp.to) {
			return true
		}
	}
	return false
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
