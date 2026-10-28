// Package wikifake is a wikiwikibangbang that lives in a test: the routes Atlas's
// adapter reads, answering from pages a test writes, under an https test server.
//
// It implements the source's contract as its ADR-0120 and its item API state it —
// an unreadable item is a 404, a restriction and a hold are omitted when there is
// none, a protected passage is a secret mark over the placeholder for a reader who
// may not read it, the change feed names an item the caller cannot read as "" —
// so the tests that use it test Atlas against the contract rather than against
// whatever the fake happened to answer.
package wikifake

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Token is the read token the fake accepts.
const Token = "wiki-read-token-0123456789"

// Placeholder is what a reader who may not read a passage reads.
const Placeholder = "▒▒▒▒▒▒▒▒"

// Mark is one mark over a block's text, in runes.
type Mark struct {
	Type  string `json:"type"`
	Start int    `json:"start"`
	End   int    `json:"end"`
	Href  string `json:"href,omitempty"`
	Ref   string `json:"ref,omitempty"`
}

// Block is one block of a body.
type Block struct {
	Kind  string `json:"kind"`
	Text  string `json:"text,omitempty"`
	Marks []Mark `json:"marks,omitempty"`
	// Ref names an image's attachment or an embed's item; Level a heading's
	// level and Tone a panel's tone.
	Ref   string `json:"ref,omitempty"`
	Level int    `json:"level,omitempty"`
	Tone  string `json:"tone,omitempty"`
}

// Page is one item of the fake.
type Page struct {
	ID         string
	Key        string
	Space      string
	Parent     string
	Title      string
	State      string
	Presence   string
	Version    int
	Labels     []string
	Properties map[string]any
	Body       []Block
	// ReadRestriction and EditRestriction name who may read or edit.
	ReadRestriction []string
	EditRestriction []string
	Held            bool
	// Unreadable makes the page a 404 for the account, as a restriction that
	// does not name it would.
	Unreadable bool
	// OmitPresence leaves presence out of the answer, as a source that does
	// not report it would.
	OmitPresence bool
}

// Attachment is one file of a page.
type Attachment struct {
	ID, Name, Type string
	Data           []byte
}

// Grant is one space role of the account.
type Grant struct {
	Space string `json:"space"`
	Role  string `json:"role"`
	Via   string `json:"via"`
}

// Server is the fake.
type Server struct {
	TS *httptest.Server

	mu    sync.Mutex
	pages map[string]*Page
	order []string
	files map[string][]Attachment
	// The account's standing.
	Scope        string
	Admin        bool
	Erasure      bool
	System       []string
	Spaces       []Grant
	RolesMissing bool
	// Down makes every request answer 503, as a source in an outage.
	Down bool
	// Redirect sends every request to another address.
	Redirect string

	watched map[string]bool
	changes []change
	seq     int64
	bump    chan struct{}
	// Seen records every request, "METHOD path".
	Seen []string
	// Tokens records every bearer token presented.
	Tokens []string
}

type change struct {
	seq  int64
	item string
}

// New starts a fake whose account is a read-scope viewer of space SVC.
func New() *Server {
	s := &Server{pages: map[string]*Page{}, files: map[string][]Attachment{}, watched: map[string]bool{},
		Scope: "read", Spaces: []Grant{{Space: "SVC", Role: "viewer"}}, bump: make(chan struct{})}
	s.TS = httptest.NewTLSServer(http.HandlerFunc(s.serve))
	return s
}

// Close stops it.
func (s *Server) Close() { s.TS.Close() }

// URL is its base address.
func (s *Server) URL() string { return s.TS.URL }

// Host is its host and port.
func (s *Server) Host() string { return strings.TrimPrefix(s.TS.URL, "https://") }

// Put adds or replaces a page; a new one is the last of its siblings.
func (s *Server) Put(p Page) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p.Presence == "" && !p.OmitPresence {
		p.Presence = "live"
	}
	if p.Version == 0 {
		p.Version = 1
	}
	if _, ok := s.pages[p.ID]; !ok {
		s.order = append(s.order, p.ID)
	}
	cp := p
	s.pages[p.ID] = &cp
}

// Edit changes a page in place, bumps its version and reports the change in the
// feed of whoever watches it.
func (s *Server) Edit(id string, fn func(*Page)) {
	s.mu.Lock()
	p := s.pages[id]
	fn(p)
	p.Version++
	s.mu.Unlock()
	s.Touch(id)
}

// Attach adds a file to a page.
func (s *Server) Attach(pageID string, a Attachment) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files[pageID] = append(s.files[pageID], a)
}

// Touch writes a feed entry for a page if it is watched: its id when the account
// may read it, "" when it may not.
func (s *Server) Touch(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.watched[id] {
		return
	}
	item := id
	if p := s.pages[id]; p == nil || p.Unreadable {
		item = ""
	}
	s.append(item)
}

// TouchAccount writes the entry the source always lists: a change to the
// account's own roles, which names no item.
func (s *Server) TouchAccount() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.append("")
}

func (s *Server) append(item string) {
	s.seq++
	s.changes = append(s.changes, change{seq: s.seq, item: item})
	close(s.bump)
	s.bump = make(chan struct{})
}

// Watched lists the watched ids, sorted.
func (s *Server) Watched() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for id, on := range s.watched {
		if on {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out
}

// Set runs fn under the fake's lock, for changing its standing.
func (s *Server) Set(fn func(*Server)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(s)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.Seen = append(s.Seen, r.Method+" "+r.URL.Path)
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	s.Tokens = append(s.Tokens, tok)
	down, redirect := s.Down, s.Redirect
	s.mu.Unlock()
	if redirect != "" {
		http.Redirect(w, r, redirect+r.URL.Path, http.StatusFound)
		return
	}
	if down {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "down"})
		return
	}
	if tok != Token {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	path := r.URL.Path
	switch {
	case path == "/api/me" && r.Method == http.MethodGet:
		s.mu.Lock()
		writeJSON(w, 200, map[string]any{"user": map[string]string{"id": "u_atlas", "username": "atlas"},
			"via": "token", "scope": s.Scope, "admin": s.Admin, "erasure": s.Erasure})
		s.mu.Unlock()
	case path == "/api/me/roles" && r.Method == http.MethodGet:
		s.mu.Lock()
		missing := s.RolesMissing
		sys := append([]string{}, s.System...)
		spaces := append([]Grant{}, s.Spaces...)
		s.mu.Unlock()
		if missing {
			writeJSON(w, 404, map[string]string{"error": "not found"})
			return
		}
		writeJSON(w, 200, map[string]any{"system": sys, "spaces": spaces})
	case path == "/api/search":
		s.search(w, r)
	case path == "/api/changes":
		s.feed(w, r)
	case strings.HasPrefix(path, "/api/spaces/") && strings.HasSuffix(path, "/tree"):
		s.tree(w, r, strings.TrimSuffix(strings.TrimPrefix(path, "/api/spaces/"), "/tree"))
	case strings.HasPrefix(path, "/api/items/"):
		s.item(w, r, strings.Split(strings.TrimPrefix(path, "/api/items/"), "/"))
	default:
		writeJSON(w, 404, map[string]string{"error": "no route"})
	}
}

// find resolves an id or key to a readable page; nil is a 404.
func (s *Server) find(ref string) *Page {
	if p, ok := s.pages[ref]; ok {
		if p.Unreadable {
			return nil
		}
		return p
	}
	for _, p := range s.pages {
		if p.Key == ref && !p.Unreadable {
			return p
		}
	}
	return nil
}

func (s *Server) itemJSON(p *Page) map[string]any {
	body := []map[string]any{}
	for _, b := range p.Body {
		blk := map[string]any{"kind": b.Kind, "text": b.Text}
		if len(b.Marks) > 0 {
			blk["marks"] = b.Marks
		}
		if b.Ref != "" {
			blk["ref"] = b.Ref
		}
		if b.Level != 0 {
			blk["level"] = b.Level
		}
		if b.Tone != "" {
			blk["tone"] = b.Tone
		}
		body = append(body, blk)
	}
	labels := p.Labels
	if labels == nil {
		labels = []string{}
	}
	props := p.Properties
	if props == nil {
		props = map[string]any{}
	}
	j := map[string]any{"id": p.ID, "key": p.Key, "type": "page", "space": p.Space, "title": p.Title,
		"properties": props, "body": body, "version": p.Version, "labels": labels}
	if !p.OmitPresence {
		j["presence"] = p.Presence
	}
	if p.Parent != "" {
		j["parent"] = p.Parent
	}
	if p.State != "" {
		j["state"] = p.State
	}
	if len(p.ReadRestriction)+len(p.EditRestriction) > 0 {
		read, edit := p.ReadRestriction, p.EditRestriction
		if read == nil {
			read = []string{}
		}
		if edit == nil {
			edit = []string{}
		}
		j["restriction"] = map[string]any{"read": read, "edit": edit}
	}
	if p.Held {
		j["hold"] = map[string]any{"findings": []any{}, "savers": []string{"u_owner"}}
	}
	return j
}

func (s *Server) item(w http.ResponseWriter, r *http.Request, parts []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ref := parts[0]
	if len(parts) == 2 && parts[1] == "watch" {
		switch r.Method {
		case http.MethodPut:
			if s.find(ref) == nil {
				writeJSON(w, 404, map[string]string{"error": "not found"})
				return
			}
			s.watched[s.find(ref).ID] = true
		case http.MethodDelete:
			// By id, a watch ends even on a page the account can no longer read.
			if !s.watched[ref] {
				writeJSON(w, 404, map[string]string{"error": "not found"})
				return
			}
			delete(s.watched, ref)
		}
		writeJSON(w, 200, map[string]bool{"watching": r.Method == http.MethodPut})
		return
	}
	if r.Method != http.MethodGet {
		writeJSON(w, 403, map[string]string{"error": "a read token may not change content"})
		return
	}
	p := s.find(ref)
	if p == nil {
		writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	switch {
	case len(parts) == 1:
		writeJSON(w, 200, map[string]any{"item": s.itemJSON(p), "editable": false, "may": map[string]bool{}})
	case len(parts) == 2 && parts[1] == "attachments":
		list := []map[string]any{}
		for _, a := range s.files[p.ID] {
			list = append(list, map[string]any{"id": a.ID, "name": a.Name, "type": a.Type, "size": len(a.Data)})
		}
		writeJSON(w, 200, map[string]any{"attachments": list})
	case len(parts) == 3 && parts[1] == "attachments":
		for _, a := range s.files[p.ID] {
			if a.ID == parts[2] {
				w.Header().Set("Content-Type", a.Type)
				_, _ = w.Write(a.Data)
				return
			}
		}
		writeJSON(w, 404, map[string]string{"error": "not found"})
	default:
		writeJSON(w, 404, map[string]string{"error": "no route"})
	}
}

func (s *Server) tree(w http.ResponseWriter, r *http.Request, space string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := r.URL.Query()
	parent := q.Get("parent")
	if p := s.find(parent); p != nil {
		parent = p.ID
	} else {
		writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 {
		limit = 100
	}
	var all []map[string]any
	for _, id := range s.order {
		c := s.pages[id]
		if c.Parent == parent && c.Space == space && !c.Unreadable && c.Presence == "live" {
			all = append(all, map[string]any{"id": c.ID, "key": c.Key, "title": c.Title, "type": "page"})
		}
	}
	start := 0
	if after := q.Get("after"); after != "" {
		for i, it := range all {
			if it["id"] == after {
				start = i + 1
			}
		}
	}
	page := all[min(start, len(all)):min(start+limit, len(all))]
	res := map[string]any{"items": page, "total": map[string]any{"value": len(all), "exact": true}}
	if start+limit < len(all) {
		res["next"] = page[len(page)-1]["id"]
	}
	// A nil page slice is a non-nil interface value, so test the slice itself.
	if len(page) == 0 {
		res["items"] = []any{}
	}
	writeJSON(w, 200, res)
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := strings.ToLower(r.URL.Query().Get("q"))
	hits := []map[string]any{}
	for _, id := range s.order {
		p := s.pages[id]
		if !p.Unreadable && strings.Contains(strings.ToLower(p.Title), q) {
			hits = append(hits, map[string]any{"item": s.itemJSON(p), "score": 1, "stars": 5,
				"matched": []string{q}, "missing": []string{}})
		}
	}
	writeJSON(w, 200, map[string]any{"hits": hits})
}

func (s *Server) feed(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	s.mu.Lock()
	if q.Get("after") == "" {
		last := s.seq
		s.mu.Unlock()
		writeJSON(w, 200, map[string]any{"changes": []any{}, "last": last})
		return
	}
	after, _ := strconv.ParseInt(q.Get("after"), 10, 64)
	wait, _ := strconv.Atoi(q.Get("wait"))
	deadline := time.After(time.Duration(min(wait, 30)) * time.Second)
	for {
		var out []map[string]any
		for _, c := range s.changes {
			if c.seq > after {
				out = append(out, map[string]any{"seq": c.seq, "item": c.item, "at": time.Unix(0, 0).UTC().Format(time.RFC3339)})
			}
		}
		if len(out) > 0 || wait == 0 {
			last := s.seq
			s.mu.Unlock()
			if out == nil {
				out = []map[string]any{}
			}
			writeJSON(w, 200, map[string]any{"changes": out, "last": last})
			return
		}
		bump := s.bump
		s.mu.Unlock()
		select {
		case <-bump:
		case <-deadline:
			wait = 0
		case <-r.Context().Done():
			return
		}
		s.mu.Lock()
	}
}
