package sharepoint

import (
	"context"
	"encoding/json"
	"fmt"
	"mime"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The mockup provider (ADR-0484).
//
// A SharePoint Worker whose provider is [MockProvider] reaches no tenant: its operations
// run against a [MockSite] in the memory of the Atlas server that holds the Worker
// record. That is the AD and SQL mockups' idea (ADR-0181/0221), placed where the preview
// mail provider places its outbox (ADR-0150): in the server, whether the SharePoint jobs
// run in the engine or in a worker it supervises. A worker in mockup mode forwards every
// operation there ([RemoteMock]), so the site survives the worker being restarted — which
// a supervised worker is, whenever a Worker record changes — and the Console can show it.
//
// # Faithful where a model depends on it
//
// A mockup that accepts what SharePoint refuses teaches a model to be wrong. So this one
// answers in the shapes the Graph client answers in — an item with its columns under
// fields, a page with items, hasMore and nextPageToken, a file with webUrl and its
// properties under listItem.fields — and refuses what the site would refuse: an item or
// a file that is not there, a list item created in a document library, a file where a
// folder is asked for, a filter on a column the list does not have. It maps a missing
// thing to the same answer the Graph client gives for SharePoint's 404: deleted=false on
// a delete, exists=false on get-file, a failed task everywhere else.
//
// # Where it is more forgiving, and says so
//
// A list or a document library exists from the first time a task writes to it. A real
// site has its lists before a process runs, made by a person; the mockup has nobody to
// make them, so a process creates them by using them, and a task that reads a list
// nobody has written to is refused as SharePoint refuses a list it does not have. A
// list's columns are likewise the ones written to it: there is no schema to break, so a
// column name with a typo is created on write and found out on the first filter or read
// that names the right one. The filter language is a stated subset of OData (see
// [parseMockFilter]) and refuses rather than guesses at the rest.

// MockProvider is the provider of a SharePoint Worker record that is a mockup.
const MockProvider = "mock"

// mockJournalSize is how many operations a mockup remembers for the Console to show:
// enough for several runs of a process, few enough that a loop gone wrong cannot grow
// it without bound. The oldest go first.
const mockJournalSize = 200

// mockContentBudget is the most file content one mockup holds, across every library of
// every site it has. The content lives in the server's memory, and a process uploading
// in a loop would otherwise make it hold whatever the loop produced.
const mockContentBudget int64 = 64 << 20

// mockViewRows is how many items or files of one list the Console is shown; the count
// beside it says how many there are.
const mockViewRows = 200

// mockDefaultLibrary is the library a file operation uses when the task names none: the
// one every SharePoint site is created with.
const mockDefaultLibrary = "Documents"

// MockSites holds the mockup of every SharePoint Worker configured as one, by Worker
// name. It outlives the client registry, which is rebuilt on every change to the
// Worker store: a rebuild re-binds the clients and must not throw away what a run put
// into the site.
type MockSites struct {
	mu    sync.Mutex
	now   func() time.Time
	sites map[string]*MockSite
}

// NewMockSites creates the holder. now stamps what the mockups record; nil is
// time.Now.
func NewMockSites(now func() time.Time) *MockSites {
	if now == nil {
		now = time.Now
	}
	return &MockSites{now: now, sites: map[string]*MockSite{}}
}

// Site returns the Worker's mockup, creating it empty the first time.
func (s *MockSites) Site(worker string) *MockSite {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.sites[worker]
	if !ok {
		m = s.newSite(worker)
		s.sites[worker] = m
	}
	return m
}

// Lookup returns the Worker's mockup when it has one.
func (s *MockSites) Lookup(worker string) (*MockSite, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.sites[worker]
	return m, ok
}

// Keep drops every mockup whose Worker is not among workers: one that was deleted, or
// switched to Graph.
func (s *MockSites) Keep(workers ...string) {
	keep := make(map[string]bool, len(workers))
	for _, w := range workers {
		keep[w] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for name := range s.sites {
		if !keep[name] {
			delete(s.sites, name)
		}
	}
}

// Clear empties a Worker's mockup and forgets what it was asked: the way an operator
// starts the next trial run from nothing. It reports whether the Worker has one.
//
// The site is emptied in place rather than replaced, because the engine's registry
// holds this very site as the Worker's client; a fresh one would leave the engine
// writing where nobody looks.
func (s *MockSites) Clear(worker string) bool {
	m, ok := s.Lookup(worker)
	if !ok {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.webs = map[string]*mockWeb{}
	m.journal, m.seq, m.stored = nil, 0, 0
	return true
}

func (s *MockSites) newSite(worker string) *MockSite {
	return &MockSite{worker: worker, now: s.now, budget: mockContentBudget, webs: map[string]*mockWeb{}}
}

// Snapshot is every mockup's content, by Worker name, for the Console.
func (s *MockSites) Snapshot() []MockSiteSnapshot {
	s.mu.Lock()
	sites := make([]*MockSite, 0, len(s.sites))
	for _, m := range s.sites {
		sites = append(sites, m)
	}
	s.mu.Unlock()
	sort.Slice(sites, func(i, j int) bool { return sites[i].worker < sites[j].worker })
	out := make([]MockSiteSnapshot, 0, len(sites))
	for _, m := range sites {
		out = append(out, m.Snapshot())
	}
	return out
}

// MockSite is one Worker's mockup: every site its tasks have addressed, with their lists
// and libraries, and the operations it was asked. It implements [Client].
type MockSite struct {
	mu      sync.Mutex
	worker  string
	now     func() time.Time
	webs    map[string]*mockWeb
	journal []MockOperation
	seq     int64
	stored  int64 // bytes of file content held, against budget
	budget  int64
	fileSeq int
}

type mockWeb struct {
	address string // the site as first named, normalized
	url     string // what its webUrls start with
	lists   map[string]*mockList
}

type mockList struct {
	title   string
	library bool
	columns map[string]bool
	items   map[int]*mockItem
	nextID  int
	files   map[string]*mockFile // by lower-case path: SharePoint paths ignore case
}

type mockItem struct {
	id                int
	fields            map[string]any
	created, modified string
	version           int
}

type mockFile struct {
	id                string
	path              string
	folder            bool
	content           []byte
	fields            map[string]any
	created, modified string
	version           int
}

// MockOperation is one operation a mockup was asked, as the Console lists it.
type MockOperation struct {
	Seq       int64  `json:"seq"`
	At        string `json:"at"`
	Site      string `json:"site"`
	Operation string `json:"operation"`
	Target    string `json:"target"`
	Error     string `json:"error,omitempty"`
}

// Do performs one operation against the mockup and records it.
func (m *MockSite) Do(_ context.Context, req Request) (any, error) {
	p, err := prepare(req)
	m.mu.Lock()
	defer m.mu.Unlock()
	var answer any
	if err == nil {
		answer, err = m.do(p, req)
		m.prune()
	}
	m.record(p.op, req, err)
	return answer, err
}

// prune forgets a site no task has written to, so a read that addressed a site the
// mockup does not have leaves nothing behind in the Console.
func (m *MockSite) prune() {
	for address, w := range m.webs {
		if len(w.lists) == 0 {
			delete(m.webs, address)
		}
	}
}

func (m *MockSite) do(p prepared, req Request) (any, error) {
	web := m.web(req.Site)
	if p.spec.NeedsList {
		switch p.op {
		case OpCreateItem:
			return m.createItem(web, p.op, req)
		case "list-items":
			return m.listItems(web, p.op, req)
		case "get-item":
			return m.getItem(web, p.op, req)
		case "update-item":
			return m.updateItem(web, p.op, req)
		default:
			return m.deleteItem(web, p.op, req)
		}
	}
	switch p.op {
	case "upload-file":
		return m.uploadFile(web, p.op, p.path, req)
	case "download-file":
		return m.downloadFile(web, p.op, p.path, req)
	case "get-file":
		return m.getFile(web, p.path, req)
	case "list-folder":
		return m.listFolder(web, p.op, p.path, req)
	case "create-folder":
		return m.createFolder(web, p.op, p.path, req)
	default:
		return m.deleteFile(web, p.path, req)
	}
}

// web is the site a task names, made the first time it is named. Every form the Graph
// client accepts reaches the same site: its URL, host:/path, host/path.
func (m *MockSite) web(ref string) *mockWeb {
	ref = strings.TrimSpace(ref)
	host, p, isID := parseSite(ref)
	address := strings.ToLower(host + p)
	siteURL := "https://" + host + escapeSegments(p)
	if isID {
		address = strings.ToLower(ref)
		siteURL = "https://" + strings.TrimSpace(strings.Split(ref, ",")[0])
	}
	w, ok := m.webs[address]
	if !ok {
		w = &mockWeb{address: address, url: siteURL, lists: map[string]*mockList{}}
		m.webs[address] = w
	}
	return w
}

func refuse(op, format string, args ...any) error {
	return fmt.Errorf("sharepoint: %s: %s (mockup)", op, fmt.Sprintf(format, args...))
}

// list finds a list or library by title. A missing one is made only when the operation
// writes, and only of the kind that operation writes to.
func (web *mockWeb) list(op, title string, library, create bool) (*mockList, error) {
	title = strings.TrimSpace(title)
	if library && title == "" {
		// The site's default library, which every site has from the start: a task that
		// names no library reads from it without anybody having written to it first.
		title, create = mockDefaultLibrary, true
	}
	l, ok := web.lists[strings.ToLower(title)]
	switch {
	case !ok && !create:
		if library {
			return nil, refuse(op, "there is no document library %q in this site", title)
		}
		return nil, refuse(op, "there is no list %q in this site", title)
	case !ok:
		l = &mockList{title: title, library: library, columns: map[string]bool{}, items: map[int]*mockItem{}, files: map[string]*mockFile{}}
		web.lists[strings.ToLower(title)] = l
	case library && !l.library:
		return nil, refuse(op, "%q is a list, not a document library", title)
	case !library && l.library:
		return nil, refuse(op, "%q is a document library: its entries are files, which upload-file, get-file and list-folder reach — the mockup keeps no list items beside them", title)
	}
	return l, nil
}

func (m *MockSite) stamp() string { return m.now().UTC().Format(time.RFC3339) }

func (m *MockSite) createItem(web *mockWeb, op string, req Request) (any, error) {
	l, err := web.list(op, req.List, false, true)
	if err != nil {
		return nil, err
	}
	l.nextID++
	now := m.stamp()
	it := &mockItem{id: l.nextID, fields: map[string]any{}, created: now, modified: now, version: 1}
	l.setFields(it.fields, req.Fields)
	l.items[it.id] = it
	return web.itemAnswer(l, it, ""), nil
}

// itemID reads a list item id, which SharePoint numbers from 1.
func itemID(op string, req Request) (int, error) {
	raw := strings.TrimSpace(req.ItemID)
	id, err := strconv.Atoi(raw)
	if err != nil || id < 1 {
		return 0, refuse(op, "%q is not a list item id; SharePoint numbers its items from 1", raw)
	}
	return id, nil
}

func (m *MockSite) item(web *mockWeb, op string, req Request) (*mockList, *mockItem, error) {
	id, err := itemID(op, req)
	if err != nil {
		return nil, nil, err
	}
	l, err := web.list(op, req.List, false, false)
	if err != nil {
		return nil, nil, err
	}
	it, ok := l.items[id]
	if !ok {
		return nil, nil, refuse(op, "there is no item %d in %q", id, l.title)
	}
	return l, it, nil
}

func (m *MockSite) getItem(web *mockWeb, op string, req Request) (any, error) {
	l, it, err := m.item(web, op, req)
	if err != nil {
		return nil, err
	}
	return web.itemAnswer(l, it, req.Select), nil
}

func (m *MockSite) updateItem(web *mockWeb, op string, req Request) (any, error) {
	l, it, err := m.item(web, op, req)
	if err != nil {
		return nil, err
	}
	l.setFields(it.fields, req.Fields)
	it.modified = m.stamp()
	it.version++
	return fieldsWithID(it), nil
}

// deleteItem answers deleted=false for a list or an item that is not there — SharePoint's
// 404, as the Graph client answers it. A malformed id and a document library are faults
// in the model, there as here.
func (m *MockSite) deleteItem(web *mockWeb, op string, req Request) (any, error) {
	id, err := itemID(op, req)
	if err != nil {
		return nil, err
	}
	gone := map[string]any{"deleted": false}
	l, ok := web.lists[strings.ToLower(strings.TrimSpace(req.List))]
	switch {
	case !ok:
		return gone, nil
	case l.library:
		_, err := web.list(op, req.List, false, false)
		return nil, err
	}
	if _, ok := l.items[id]; !ok {
		return gone, nil
	}
	delete(l.items, id)
	return map[string]any{"deleted": true}, nil
}

func (m *MockSite) listItems(web *mockWeb, op string, req Request) (any, error) {
	l, err := web.list(op, req.List, false, false)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	if tok := strings.TrimSpace(req.PageToken); tok != "" {
		parsed, err := url.ParseQuery(tok)
		if err != nil || parsed.Get("$skiptoken") == "" {
			return nil, refuse(op, "pageToken %q is not one a page answered with; pass nextPageToken back unchanged", tok)
		}
		q = parsed
	} else {
		q.Set("$top", strconv.Itoa(int(pageSize(req.MaxResults))))
		if f := strings.TrimSpace(req.Filter); f != "" {
			q.Set("$filter", f)
		}
		if s := strings.TrimSpace(req.Select); s != "" {
			q.Set("$select", s)
		}
	}
	match, err := parseMockFilter(op, q.Get("$filter"), l)
	if err != nil {
		return nil, err
	}
	top, _ := strconv.Atoi(q.Get("$top"))
	if top <= 0 {
		top = int(DefaultMaxResults)
	}
	after, _ := strconv.Atoi(q.Get("$skiptoken"))
	ids := make([]int, 0, len(l.items))
	for id, it := range l.items {
		if id > after && match(it) {
			ids = append(ids, id)
		}
	}
	sort.Ints(ids)
	items := []any{}
	for _, id := range ids {
		if len(items) == top {
			break
		}
		items = append(items, web.itemAnswer(l, l.items[id], q.Get("$select")))
	}
	token := ""
	if len(ids) > len(items) {
		q.Set("$skiptoken", strconv.Itoa(ids[len(items)-1]))
		token = q.Encode()
	}
	return pageAnswer(items, token), nil
}

// libraryName is the document library a file operation names; "" is the site's
// default one, which [mockWeb.list] resolves.
func libraryName(req Request) string { return strings.TrimSpace(req.List) }

func (m *MockSite) uploadFile(web *mockWeb, op string, p cleanedPath, req Request) (any, error) {
	data, err := decodeContent(op, req)
	if err != nil {
		return nil, err
	}
	l, err := web.list(op, libraryName(req), true, true)
	if err != nil {
		return nil, err
	}
	key := strings.ToLower(p.clean)
	old, exists := l.files[key]
	if exists && old.folder {
		return nil, refuse(op, "%q is a folder", p.clean)
	}
	grow := int64(len(data))
	if exists {
		grow -= int64(len(old.content))
	}
	if m.stored+grow > m.budget {
		return nil, refuse(op, "the mockup holds at most %d MiB of file content, and this upload would pass it — delete what earlier runs left, or empty the site in Operations › Diagnostics › Mockups", m.budget>>20)
	}
	if err := m.makeParents(l, op, p.clean); err != nil {
		return nil, err
	}
	now := m.stamp()
	f := old
	if !exists {
		f = &mockFile{id: m.newFileID(), path: p.clean, fields: map[string]any{}, created: now}
		l.files[key] = f
	}
	f.content = data
	f.modified = now
	f.version++
	m.stored += grow
	answer := web.driveItem(l, f)
	if len(req.Fields) > 0 {
		l.setFields(f.fields, req.Fields)
		answer["listItem"] = map[string]any{"fields": fieldsOf(f.fields)}
	}
	return answer, nil
}

// makeParents creates the folders above a path, as an upload by path does in SharePoint.
func (m *MockSite) makeParents(l *mockList, op, clean string) error {
	segments := strings.Split(clean, "/")
	for i := 1; i < len(segments); i++ {
		parent := strings.Join(segments[:i], "/")
		f, ok := l.files[strings.ToLower(parent)]
		switch {
		case ok && !f.folder:
			return refuse(op, "%q is a file, not a folder", parent)
		case !ok:
			now := m.stamp()
			l.files[strings.ToLower(parent)] = &mockFile{id: m.newFileID(), path: parent, folder: true, fields: map[string]any{}, created: now, modified: now, version: 1}
		}
	}
	return nil
}

func (m *MockSite) newFileID() string {
	m.fileSeq++
	return fmt.Sprintf("01MOCK%06d", m.fileSeq)
}

func (m *MockSite) downloadFile(web *mockWeb, op string, p cleanedPath, req Request) (any, error) {
	l, err := web.list(op, libraryName(req), true, false)
	if err != nil {
		return nil, err
	}
	f, ok := l.files[strings.ToLower(p.clean)]
	if !ok || f.folder {
		return nil, refuse(op, "there is no file %q in %q", p.clean, l.title)
	}
	content, err := encodeContent(op, p.clean, req, f.content)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"path":        f.path,
		"name":        path.Base(f.path),
		"content":     content,
		"encoding":    contentEncoding(req),
		"contentType": mimeType(f.path),
		"size":        json.Number(strconv.Itoa(len(f.content))),
	}, nil
}

func (m *MockSite) getFile(web *mockWeb, p cleanedPath, req Request) (any, error) {
	missing := map[string]any{"exists": false, "path": p.clean}
	l, err := web.list("get-file", libraryName(req), true, false)
	if err != nil {
		return missing, nil // SharePoint's 404, which get-file answers as exists=false
	}
	f, ok := l.files[strings.ToLower(p.clean)]
	if !ok {
		return missing, nil
	}
	answer := web.driveItem(l, f)
	answer["listItem"] = map[string]any{"fields": fieldsOf(f.fields)}
	answer["exists"] = true
	return answer, nil
}

func (m *MockSite) listFolder(web *mockWeb, op string, p cleanedPath, req Request) (any, error) {
	l, err := web.list(op, libraryName(req), true, false)
	if err != nil {
		return nil, err
	}
	if p.clean != "" {
		f, ok := l.files[strings.ToLower(p.clean)]
		switch {
		case !ok:
			return nil, refuse(op, "there is no folder %q in %q", p.clean, l.title)
		case !f.folder:
			return nil, refuse(op, "%q is a file, not a folder", p.clean)
		}
	}
	q := url.Values{}
	if tok := strings.TrimSpace(req.PageToken); tok != "" {
		parsed, err := url.ParseQuery(tok)
		if err != nil || parsed.Get("$skiptoken") == "" {
			return nil, refuse(op, "pageToken %q is not one a page answered with; pass nextPageToken back unchanged", tok)
		}
		q = parsed
	} else {
		q.Set("$top", strconv.Itoa(int(pageSize(req.MaxResults))))
	}
	top, _ := strconv.Atoi(q.Get("$top"))
	if top <= 0 {
		top = int(DefaultMaxResults)
	}
	after := strings.ToLower(q.Get("$skiptoken"))
	var children []*mockFile
	for _, f := range l.files {
		if parentOf(f.path) == strings.ToLower(p.clean) && strings.ToLower(path.Base(f.path)) > after {
			children = append(children, f)
		}
	}
	sort.Slice(children, func(i, j int) bool {
		return strings.ToLower(path.Base(children[i].path)) < strings.ToLower(path.Base(children[j].path))
	})
	items := []any{}
	for _, f := range children {
		if len(items) == top {
			break
		}
		items = append(items, web.driveItem(l, f))
	}
	token := ""
	if len(children) > len(items) {
		// A name sorts after "" so the first page's empty cursor is never confused with it;
		// the cursor is the last name this page answered with, as Graph's is opaque.
		q.Set("$skiptoken", strings.ToLower(path.Base(children[len(items)-1].path)))
		token = q.Encode()
	}
	return pageAnswer(items, token), nil
}

func (m *MockSite) createFolder(web *mockWeb, op string, p cleanedPath, req Request) (any, error) {
	l, err := web.list(op, libraryName(req), true, true)
	if err != nil {
		return nil, err
	}
	if err := m.makeParents(l, op, p.clean); err != nil {
		return nil, err
	}
	key := strings.ToLower(p.clean)
	f, exists := l.files[key]
	if exists && !f.folder {
		return nil, refuse(op, "%q is a file, not a folder", p.clean)
	}
	if !exists {
		now := m.stamp()
		f = &mockFile{id: m.newFileID(), path: p.clean, folder: true, fields: map[string]any{}, created: now, modified: now, version: 1}
		l.files[key] = f
	}
	answer := web.driveItem(l, f)
	answer["created"] = !exists
	return answer, nil
}

func (m *MockSite) deleteFile(web *mockWeb, p cleanedPath, req Request) (any, error) {
	answer := map[string]any{"deleted": false, "path": p.clean}
	l, err := web.list("delete-file", libraryName(req), true, false)
	if err != nil {
		return answer, nil // SharePoint's 404, which a delete answers as deleted=false
	}
	key := strings.ToLower(p.clean)
	if _, ok := l.files[key]; !ok {
		return answer, nil
	}
	// A folder goes with everything inside it, as it does in SharePoint.
	for k, f := range l.files {
		if k == key || strings.HasPrefix(k, key+"/") {
			m.stored -= int64(len(f.content))
			delete(l.files, k)
		}
	}
	answer["deleted"] = true
	return answer, nil
}

// setFields writes column values, learning the column names; a null clears a column, as
// Graph's does.
func (l *mockList) setFields(into map[string]any, values map[string]any) {
	for name, v := range values {
		l.columns[name] = true
		if v == nil {
			delete(into, name)
			continue
		}
		into[name] = cloneValue(v)
	}
}

// itemAnswer is a list item in Graph's shape: its id, its link, its times, and its
// columns under fields — all of them, or the ones a task selected.
func (web *mockWeb) itemAnswer(l *mockList, it *mockItem, sel string) map[string]any {
	id := strconv.Itoa(it.id)
	var fields map[string]any
	if cols := selectedColumns(sel); len(cols) > 0 {
		fields = map[string]any{}
		for _, c := range cols {
			if v, ok := it.fields[c]; ok {
				fields[c] = cloneValue(v)
			}
		}
	} else {
		fields = fieldsWithID(it)
	}
	return map[string]any{
		"id":                   id,
		"webUrl":               web.url + "/Lists/" + url.PathEscape(l.title) + "/DispForm.aspx?ID=" + id,
		"createdDateTime":      it.created,
		"lastModifiedDateTime": it.modified,
		"eTag":                 fmt.Sprintf("\"%s,%d\"", id, it.version),
		"fields":               fields,
	}
}

// driveItem is a file or folder in Graph's shape.
func (web *mockWeb) driveItem(l *mockList, f *mockFile) map[string]any {
	parent := ""
	if i := strings.LastIndex(f.path, "/"); i >= 0 {
		parent = "/" + f.path[:i]
	}
	m := map[string]any{
		"id":                   f.id,
		"name":                 path.Base(f.path),
		"webUrl":               web.url + "/" + url.PathEscape(l.title) + "/" + escapeSegments(f.path),
		"createdDateTime":      f.created,
		"lastModifiedDateTime": f.modified,
		"eTag":                 fmt.Sprintf("\"{%s},%d\"", f.id, f.version),
		"parentReference":      map[string]any{"path": "/drive/root:" + parent},
	}
	if f.folder {
		n := 0
		for _, g := range l.files {
			if parentOf(g.path) == strings.ToLower(f.path) {
				n++
			}
		}
		m["folder"] = map[string]any{"childCount": json.Number(strconv.Itoa(n))}
	} else {
		m["file"] = map[string]any{"mimeType": mimeType(f.path)}
		m["size"] = json.Number(strconv.Itoa(len(f.content)))
	}
	return m
}

func pageAnswer(items []any, token string) map[string]any {
	return map[string]any{
		"items":         items,
		"count":         json.Number(strconv.Itoa(len(items))),
		"hasMore":       token != "",
		"nextPageToken": token,
	}
}

func fieldsWithID(it *mockItem) map[string]any {
	out := fieldsOf(it.fields)
	out["id"] = strconv.Itoa(it.id)
	return out
}

func fieldsOf(fields map[string]any) map[string]any {
	out := make(map[string]any, len(fields)+1)
	for k, v := range fields {
		out[k] = cloneValue(v)
	}
	return out
}

// cloneValue copies a JSON value, so nothing a caller holds can change what the mockup
// keeps, and nothing the mockup keeps changes under a caller.
func cloneValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			out[k] = cloneValue(x)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = cloneValue(x)
		}
		return out
	default:
		return v
	}
}

func selectedColumns(sel string) []string {
	var cols []string
	for _, c := range strings.Split(sel, ",") {
		if c = strings.TrimSpace(c); c != "" {
			cols = append(cols, c)
		}
	}
	return cols
}

// parentOf is the lower-case path of the folder a path is in; "" is the library's top
// level.
func parentOf(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return strings.ToLower(p[:i])
	}
	return ""
}

func mimeType(p string) string {
	if t := mime.TypeByExtension(path.Ext(p)); t != "" {
		return t
	}
	return "application/octet-stream"
}

// record appends one operation to the journal, dropping the oldest past its size.
func (m *MockSite) record(op string, req Request, err error) {
	if op == "" {
		op = req.Operation // prepare refused it; the journal shows what was asked
	}
	if op == "" {
		op = OpCreateItem
	}
	target := strings.TrimSpace(req.List)
	switch {
	case strings.TrimSpace(req.ItemID) != "":
		target += " #" + strings.TrimSpace(req.ItemID)
	case strings.TrimSpace(req.Path) != "":
		if target == "" {
			target = mockDefaultLibrary
		}
		target += "/" + strings.Trim(strings.TrimSpace(req.Path), "/")
	}
	m.seq++
	e := MockOperation{Seq: m.seq, At: m.stamp(), Site: strings.TrimSpace(req.Site), Operation: op, Target: target}
	if err != nil {
		e.Error = err.Error()
	}
	m.journal = append(m.journal, e)
	if len(m.journal) > mockJournalSize {
		m.journal = append([]MockOperation(nil), m.journal[len(m.journal)-mockJournalSize:]...)
	}
}

// MockSiteSnapshot is one Worker's mockup as the Console shows it.
type MockSiteSnapshot struct {
	Worker      string            `json:"worker"`
	StoredBytes int64             `json:"storedBytes"`
	Sites       []MockWebSnapshot `json:"sites"`
	Journal     []MockOperation   `json:"journal"`
}

// MockWebSnapshot is one site of a mockup.
type MockWebSnapshot struct {
	Address string             `json:"address"`
	URL     string             `json:"url"`
	Lists   []MockListSnapshot `json:"lists"`
}

// MockListSnapshot is one list or library: its columns and its first entries, with how
// many it holds.
type MockListSnapshot struct {
	Title   string             `json:"title"`
	Library bool               `json:"library"`
	Columns []string           `json:"columns"`
	Held    int                `json:"held"`
	Items   []map[string]any   `json:"items,omitempty"`
	Files   []MockFileSnapshot `json:"files,omitempty"`
}

// MockFileSnapshot is one file or folder of a library.
type MockFileSnapshot struct {
	Path   string         `json:"path"`
	Folder bool           `json:"folder"`
	Size   int            `json:"size"`
	Fields map[string]any `json:"fields,omitempty"`
}

// Snapshot is this mockup's content now.
func (m *MockSite) Snapshot() MockSiteSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := MockSiteSnapshot{Worker: m.worker, StoredBytes: m.stored, Sites: []MockWebSnapshot{}, Journal: append([]MockOperation{}, m.journal...)}
	addresses := make([]string, 0, len(m.webs))
	for a := range m.webs {
		addresses = append(addresses, a)
	}
	sort.Strings(addresses)
	for _, a := range addresses {
		web := m.webs[a]
		ws := MockWebSnapshot{Address: web.address, URL: web.url, Lists: []MockListSnapshot{}}
		titles := make([]string, 0, len(web.lists))
		for t := range web.lists {
			titles = append(titles, t)
		}
		sort.Strings(titles)
		for _, t := range titles {
			ws.Lists = append(ws.Lists, web.lists[t].snapshot())
		}
		out.Sites = append(out.Sites, ws)
	}
	return out
}

func (l *mockList) snapshot() MockListSnapshot {
	s := MockListSnapshot{Title: l.title, Library: l.library, Columns: []string{}}
	for c := range l.columns {
		s.Columns = append(s.Columns, c)
	}
	sort.Strings(s.Columns)
	if l.library {
		paths := make([]string, 0, len(l.files))
		for k := range l.files {
			paths = append(paths, k)
		}
		sort.Strings(paths)
		s.Held = len(paths)
		for _, k := range paths {
			if len(s.Files) == mockViewRows {
				break
			}
			f := l.files[k]
			fs := MockFileSnapshot{Path: f.path, Folder: f.folder, Size: len(f.content)}
			if len(f.fields) > 0 {
				fs.Fields = fieldsOf(f.fields)
			}
			s.Files = append(s.Files, fs)
		}
		return s
	}
	ids := make([]int, 0, len(l.items))
	for id := range l.items {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	s.Held = len(ids)
	for _, id := range ids {
		if len(s.Items) == mockViewRows {
			break
		}
		s.Items = append(s.Items, fieldsWithID(l.items[id]))
	}
	return s
}
