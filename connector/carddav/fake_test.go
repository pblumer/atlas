package carddav

import (
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
)

// fakeDAV is an in-memory CardDAV server (RFC 4918, RFC 6352, RFC 6764) as the
// operations meet one: a well-known redirect, a principal, an address book home, address
// books, PROPFIND at depth 0 and 1, addressbook-multiget, and GET, PUT and DELETE with
// ETags and the If-Match and If-None-Match preconditions. Every request needs the
// configured Basic credential.
//
// It is not a reference implementation. It answers what the servers this Worker is
// written for answer, as their documentation and RFC 6352 describe them; the record's
// open question is the part only a real server can settle.
type fakeDAV struct {
	t   *testing.T
	srv *httptest.Server

	mu         sync.Mutex
	user, pass string
	principal  string
	home       string
	// homeURL, when set, is where the principal says the home is — another server, as
	// iCloud names a pNN-contacts host.
	homeURL string
	books   map[string]*fakeBook // collection path → book
	seq     int

	// requests are "METHOD path" in order.
	requests []string
	// beforePut runs before a PUT is applied; it may change the store, as another client
	// writing in between would.
	beforePut func(path string)
	// failPut answers the next PUTs to a path with this status instead of applying them.
	failPut map[string]int
	// weakETags makes every ETag weak, as some servers answer.
	weakETags bool
	// plainRoot makes the server's root answer 404, so only /.well-known/carddav says
	// where the account is.
	plainRoot bool
}

type fakeBook struct {
	name  string
	cards map[string]*fakeCard // member name (escaped) → card
}

type fakeCard struct {
	data string
	etag string
}

const (
	fakeUser = "anna@firma.ch"
	fakePass = "app-passwort"
)

func newFakeDAV(t *testing.T) *fakeDAV {
	f := &fakeDAV{
		t: t, user: fakeUser, pass: fakePass,
		principal: "/dav/principals/anna/",
		home:      "/dav/addressbooks/anna/",
		books:     map[string]*fakeBook{},
		failPut:   map[string]int{},
	}
	f.books["/dav/addressbooks/anna/kontakte/"] = &fakeBook{name: "Kontakte", cards: map[string]*fakeCard{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

// bundle is a credential bundle for the fake, in a group form.
func (f *fakeDAV) bundle(groups string) string {
	if groups == "" {
		return fmt.Sprintf(`{"username":%q,"password":%q}`, f.user, f.pass)
	}
	return fmt.Sprintf(`{"username":%q,"password":%q,"groups":%q}`, f.user, f.pass, groups)
}

func (f *fakeDAV) book() *fakeBook { return f.books["/dav/addressbooks/anna/kontakte/"] }

// put stores a card under a member name, as another client would have.
func (f *fakeDAV) put(name, data string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seq++
	f.book().cards[name] = &fakeCard{data: data, etag: fmt.Sprintf(`"e%d"`, f.seq)}
}

// card is a stored card, or "".
func (f *fakeDAV) card(name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c, ok := f.book().cards[name]; ok {
		return c.data
	}
	return ""
}

func (f *fakeDAV) count(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.requests {
		if strings.HasPrefix(r, method+" ") {
			n++
		}
	}
	return n
}

func (f *fakeDAV) etag(n int) string {
	if f.weakETags {
		return fmt.Sprintf(`W/"e%d"`, n)
	}
	return fmt.Sprintf(`"e%d"`, n)
}

func (f *fakeDAV) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.Method+" "+r.URL.EscapedPath())
	if r.URL.Path == "/.well-known/carddav" {
		http.Redirect(w, r, "/dav/", http.StatusMovedPermanently)
		return
	}
	if u, p, ok := r.BasicAuth(); !ok || u != f.user || p != f.pass {
		w.Header().Set("WWW-Authenticate", `Basic realm="dav"`)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	body, _ := io.ReadAll(r.Body)
	path := r.URL.Path
	switch r.Method {
	case "PROPFIND":
		f.propfind(w, path, r.Header.Get("Depth"))
	case "REPORT":
		f.report(w, path, body)
	case http.MethodGet:
		book, name := f.member(path)
		c := f.lookup(book, name)
		if c == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/vcard; charset=utf-8")
		w.Header().Set("ETag", c.etag)
		_, _ = w.Write([]byte(c.data))
	case http.MethodPut:
		f.doPut(w, r, path, body)
	case http.MethodDelete:
		book, name := f.member(path)
		if f.lookup(book, name) == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		delete(book.cards, name)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (f *fakeDAV) doPut(w http.ResponseWriter, r *http.Request, path string, body []byte) {
	if f.beforePut != nil {
		hook := f.beforePut
		f.beforePut = nil
		f.mu.Unlock()
		hook(path)
		f.mu.Lock()
	}
	if code, ok := f.failPut[path]; ok {
		delete(f.failPut, path)
		w.WriteHeader(code)
		return
	}
	book, name := f.member(path)
	if book == nil {
		w.WriteHeader(http.StatusConflict)
		return
	}
	cur := book.cards[name]
	if r.Header.Get("If-None-Match") == "*" && cur != nil {
		w.WriteHeader(http.StatusPreconditionFailed)
		return
	}
	if m := r.Header.Get("If-Match"); m != "" && (cur == nil || m != cur.etag || strings.HasPrefix(m, "W/")) {
		w.WriteHeader(http.StatusPreconditionFailed)
		return
	}
	if _, err := ParseCard(body); err != nil {
		w.WriteHeader(http.StatusUnsupportedMediaType)
		return
	}
	f.seq++
	book.cards[name] = &fakeCard{data: string(body), etag: f.etag(f.seq)}
	w.Header().Set("ETag", f.etag(f.seq))
	if cur == nil {
		w.WriteHeader(http.StatusCreated)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// member splits a card's path into its book and its member name.
func (f *fakeDAV) member(path string) (*fakeBook, string) {
	i := strings.LastIndex(path, "/")
	book := f.books[path[:i+1]]
	if book == nil {
		return nil, ""
	}
	return book, url.PathEscape(path[i+1:])
}

func (f *fakeDAV) lookup(book *fakeBook, name string) *fakeCard {
	if book == nil {
		return nil
	}
	return book.cards[name]
}

type fakeResp struct {
	href  string
	props string
}

func (f *fakeDAV) multistatus(w http.ResponseWriter, rs []fakeResp) {
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusMultiStatus)
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?><d:multistatus xmlns:d="DAV:" xmlns:card="urn:ietf:params:xml:ns:carddav">`)
	for _, r := range rs {
		b.WriteString("<d:response><d:href>" + html.EscapeString(r.href) + "</d:href>")
		b.WriteString("<d:propstat><d:prop>" + r.props + "</d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat>")
		// What a server does not have it answers in a propstat of its own.
		b.WriteString("<d:propstat><d:prop><d:quota-used-bytes/></d:prop><d:status>HTTP/1.1 404 Not Found</d:status></d:propstat>")
		b.WriteString("</d:response>")
	}
	b.WriteString("</d:multistatus>")
	_, _ = w.Write([]byte(b.String()))
}

func (f *fakeDAV) propfind(w http.ResponseWriter, path, depth string) {
	principal := `<d:current-user-principal><d:href>` + f.principal + `</d:href></d:current-user-principal>`
	switch {
	case path == "/" && f.plainRoot:
		w.WriteHeader(http.StatusNotFound)
	case path == "/" || path == "/dav/":
		f.multistatus(w, []fakeResp{{path, `<d:resourcetype><d:collection/></d:resourcetype>` + principal}})
	case path == f.principal:
		home := f.home
		if f.homeURL != "" {
			home = f.homeURL
		}
		f.multistatus(w, []fakeResp{{path, `<d:resourcetype><d:principal/></d:resourcetype><card:addressbook-home-set><d:href>` + home + `</d:href></card:addressbook-home-set>` + principal}})
	case path == f.home:
		rs := []fakeResp{{path, `<d:resourcetype><d:collection/></d:resourcetype>` + principal}}
		if depth == "1" {
			var paths []string
			for p := range f.books {
				paths = append(paths, p)
			}
			sort.Strings(paths)
			for _, p := range paths {
				rs = append(rs, fakeResp{p, `<d:resourcetype><d:collection/><card:addressbook/></d:resourcetype><d:displayname>` + f.books[p].name + `</d:displayname>`})
			}
			// A calendar in the same home is not an address book.
			rs = append(rs, fakeResp{f.home + "kalender/", `<d:resourcetype><d:collection/><cal:calendar xmlns:cal="urn:ietf:params:xml:ns:caldav"/></d:resourcetype>`})
		}
		f.multistatus(w, rs)
	default:
		book := f.books[path]
		if book == nil && !strings.HasSuffix(path, "/") {
			// A collection asked for without its trailing slash, which servers accept.
			book = f.books[path+"/"]
		}
		if book == nil {
			if b, name := f.member(path); f.lookup(b, name) != nil {
				f.multistatus(w, []fakeResp{{path, `<d:resourcetype/><d:getetag>` + b.cards[name].etag + `</d:getetag>`}})
				return
			}
			w.WriteHeader(http.StatusNotFound)
			return
		}
		rs := []fakeResp{{path, `<d:resourcetype><d:collection/><card:addressbook/></d:resourcetype><d:displayname>` + book.name + `</d:displayname>` + principal}}
		if depth == "1" {
			var names []string
			for n := range book.cards {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, n := range names {
				rs = append(rs, fakeResp{path + n, `<d:resourcetype/><d:getetag>` + html.EscapeString(book.cards[n].etag) + `</d:getetag><d:getcontenttype>text/vcard; charset=utf-8</d:getcontenttype>`})
			}
		}
		f.multistatus(w, rs)
	}
}

func (f *fakeDAV) report(w http.ResponseWriter, path string, body []byte) {
	book := f.books[path]
	if book == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	var q struct {
		XMLName xml.Name
		Hrefs   []string `xml:"DAV: href"`
	}
	if err := xml.Unmarshal(body, &q); err != nil || q.XMLName.Local != "addressbook-multiget" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var rs []fakeResp
	for _, h := range q.Hrefs {
		u, _ := url.Parse(h)
		b, name := f.member(u.Path)
		c := f.lookup(b, name)
		if c == nil {
			continue
		}
		rs = append(rs, fakeResp{h, `<d:getetag>` + html.EscapeString(c.etag) + `</d:getetag><card:address-data>` + html.EscapeString(c.data) + `</card:address-data>`})
	}
	f.multistatus(w, rs)
}
