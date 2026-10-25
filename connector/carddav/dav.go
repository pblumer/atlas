package carddav

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pblumer/atlas/connector/clientreg"
	"github.com/pblumer/atlas/limits"
)

// Client speaks WebDAV and CardDAV (RFC 4918, RFC 6352) to one configured Worker: one
// address book, one identity. Its methods are the requests the operations are built
// from; the operations, not the client, decide what a model sees.
type Client struct {
	endpoint *url.URL
	username string
	password string
	// groups is the Worker's group form, catalog.GroupsCategories or GroupsVCards.
	groups string
	http   *http.Client
	now    func() time.Time

	// collection is the address book, found on the first call and kept: discovery is
	// three or four requests, and the address book does not move between jobs.
	mu         sync.Mutex
	collection *url.URL
}

// Registry resolves a Worker name to the address book behind it. It is the worker's own
// map: the engine never builds one for this kind.
type Registry = clientreg.Registry[*Client]

// NewRegistry creates an empty registry.
func NewRegistry() *Registry { return clientreg.New[*Client]() }

// errAnswerTooLarge is an answer above the installation's Payload budget
// (limits.Payload, 8 MiB by default). It is refused rather than read short, because a
// short listing is a wrong one; a batch of cards that meets it is asked for again in
// halves (book.fetch).
var errAnswerTooLarge = errors.New("the answer is larger than the installation's payload budget")

// maxRedirects bounds the hops one request follows — a server's discovery redirect, a
// load balancer's — before it is refused as a loop.
const maxRedirects = 5

// DAVError is a server's refusal: the request it answered and the HTTP status. It never
// carries the URL, whose path names the account (Nextcloud's
// /addressbooks/users/<name>/), nor the body, which can quote a contact.
type DAVError struct {
	Call string
	HTTP int
}

func (e *DAVError) Error() string {
	switch e.HTTP {
	case http.StatusUnauthorized:
		return fmt.Sprintf("carddav: %s: the server refused the credential (HTTP 401); check the username, and use an app-specific password where the provider issues one (iCloud, Fastmail, Nextcloud with two-factor sign-in, mailbox.org)", e.Call)
	case http.StatusForbidden:
		return fmt.Sprintf("carddav: %s: the server refused the request (HTTP 403); the account may not read or write this address book", e.Call)
	}
	return fmt.Sprintf("carddav: %s returned HTTP %d %s", e.Call, e.HTTP, http.StatusText(e.HTTP))
}

// isStatus reports whether err is the server answering with one of the statuses.
func isStatus(err error, statuses ...int) bool {
	var de *DAVError
	if !errors.As(err, &de) {
		return false
	}
	for _, s := range statuses {
		if de.HTTP == s {
			return true
		}
	}
	return false
}

// reply is one answer, read whole within the installation's payload budget.
type reply struct {
	status int
	header http.Header
	body   []byte
	// url is where the answer came from, after redirects: what an href in it is
	// relative to.
	url *url.URL
}

// do performs one request with the Worker's credential, following redirects itself for
// every method — a PROPFIND stays a PROPFIND — and never from https to http. The
// credential goes along to another host a redirect names: that is the server the
// credential was configured for answering an authenticated request, so following it
// trusts nobody the credential did not already reach.
//
// call names the request for an error message — never the URL.
func (c *Client) do(ctx context.Context, call, method string, u *url.URL, hdr http.Header, body []byte) (*reply, error) {
	cur := u
	for hop := 0; ; hop++ {
		var rd io.Reader
		if body != nil {
			rd = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, cur.String(), rd)
		if err != nil {
			return nil, fmt.Errorf("carddav: build %s request: %w", call, err)
		}
		for k, vs := range hdr {
			for _, v := range vs {
				req.Header.Add(k, v)
			}
		}
		req.SetBasicAuth(c.username, c.password)
		req.Header.Set("User-Agent", "atlas-carddav-worker")
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, fmt.Errorf("carddav: %s: the server could not be reached: %w", call, unwrapURLError(err))
		}
		budget := limits.Default().Payload
		raw, err := io.ReadAll(io.LimitReader(resp.Body, limits.Default().Payload+1))
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("carddav: %s: reading the answer: %w", call, unwrapURLError(err))
		}
		if int64(len(raw)) > budget {
			return nil, fmt.Errorf("carddav: %s: the server answered with more than %d MiB: %w", call, budget>>20, errAnswerTooLarge)
		}
		switch resp.StatusCode {
		case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
			loc := resp.Header.Get("Location")
			if loc == "" {
				return &reply{status: resp.StatusCode, header: resp.Header, body: raw, url: cur}, nil
			}
			if hop == maxRedirects {
				return nil, fmt.Errorf("carddav: %s: the server redirected more than %d times", call, maxRedirects)
			}
			next, err := cur.Parse(loc)
			if err != nil {
				return nil, fmt.Errorf("carddav: %s: the server redirected to an address that is not a URL", call)
			}
			if err := c.reachable(cur, next); err != nil {
				return nil, fmt.Errorf("carddav: %s: %w", call, err)
			}
			cur = next
			continue
		}
		return &reply{status: resp.StatusCode, header: resp.Header, body: raw, url: cur}, nil
	}
}

// reachable is the rule for every address a server sends this client to — a redirect,
// a principal, a home on another host: https, or http only from http on a loopback
// host. A downgrade would send the credential in the clear.
func (c *Client) reachable(from, to *url.URL) error {
	switch {
	case to.Scheme == "https":
		return nil
	case to.Scheme == "http" && from.Scheme == "http" && loopback(to.Hostname()):
		return nil
	}
	return fmt.Errorf("the server sent the client to a non-https address, which would expose the credential; refused")
}

// unwrapURLError drops the *url.Error wrapper, whose message repeats the request URL.
func unwrapURLError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// The XML a server answers PROPFIND and REPORT with (RFC 4918 §14.16).
type multistatus struct {
	Responses []davResponse `xml:"DAV: response"`
}

type davResponse struct {
	Hrefs     []string   `xml:"DAV: href"`
	Status    string     `xml:"DAV: status"`
	Propstats []propstat `xml:"DAV: propstat"`
}

type propstat struct {
	Prop   davProp `xml:"DAV: prop"`
	Status string  `xml:"DAV: status"`
}

type davProp struct {
	ResourceType         *resourceType `xml:"DAV: resourcetype"`
	DisplayName          string        `xml:"DAV: displayname"`
	ETag                 string        `xml:"DAV: getetag"`
	ContentType          string        `xml:"DAV: getcontenttype"`
	CurrentUserPrincipal *hrefList     `xml:"DAV: current-user-principal"`
	HomeSet              *hrefList     `xml:"urn:ietf:params:xml:ns:carddav addressbook-home-set"`
	AddressData          *string       `xml:"urn:ietf:params:xml:ns:carddav address-data"`
}

type resourceType struct {
	Collection  *struct{} `xml:"DAV: collection"`
	AddressBook *struct{} `xml:"urn:ietf:params:xml:ns:carddav addressbook"`
}

type hrefList struct {
	Hrefs []string `xml:"DAV: href"`
}

// okProp merges the properties a response reports with a 2xx status: a server answers
// the properties it does not have in a propstat of their own, with 404.
func (r davResponse) okProp() davProp {
	var out davProp
	for _, ps := range r.Propstats {
		if !statusOK(ps.Status) {
			continue
		}
		p := ps.Prop
		if p.ResourceType != nil {
			out.ResourceType = p.ResourceType
		}
		if p.DisplayName != "" {
			out.DisplayName = p.DisplayName
		}
		if p.ETag != "" {
			out.ETag = p.ETag
		}
		if p.ContentType != "" {
			out.ContentType = p.ContentType
		}
		if p.CurrentUserPrincipal != nil {
			out.CurrentUserPrincipal = p.CurrentUserPrincipal
		}
		if p.HomeSet != nil {
			out.HomeSet = p.HomeSet
		}
		if p.AddressData != nil {
			out.AddressData = p.AddressData
		}
	}
	return out
}

// statusOK reads a status line ("HTTP/1.1 200 OK"); no status at all is a success, as
// it is for a response element that carries its properties in propstats.
func statusOK(line string) bool {
	f := strings.Fields(line)
	return len(f) < 2 || strings.HasPrefix(f[1], "2")
}

func (r davResponse) isAddressBook() bool {
	p := r.okProp()
	return p.ResourceType != nil && p.ResourceType.AddressBook != nil
}

func (r davResponse) isCollection() bool {
	p := r.okProp()
	return p.ResourceType != nil && (p.ResourceType.Collection != nil || p.ResourceType.AddressBook != nil)
}

// href is the response's first href, resolved against the URL the answer came from.
func (r davResponse) href(base *url.URL) (*url.URL, bool) {
	if len(r.Hrefs) == 0 {
		return nil, false
	}
	u, err := base.Parse(strings.TrimSpace(r.Hrefs[0]))
	return u, err == nil
}

// propfind asks for properties of a resource (Depth 0) or of it and its members (1).
func (c *Client) propfind(ctx context.Context, call string, u *url.URL, depth string, props string) (*multistatus, *url.URL, error) {
	body := `<?xml version="1.0" encoding="utf-8"?>` +
		`<d:propfind xmlns:d="DAV:" xmlns:card="urn:ietf:params:xml:ns:carddav"><d:prop>` + props + `</d:prop></d:propfind>`
	hdr := http.Header{"Depth": {depth}, "Content-Type": {"application/xml; charset=utf-8"}}
	rep, err := c.do(ctx, call, "PROPFIND", u, hdr, []byte(body))
	if err != nil {
		return nil, nil, err
	}
	if rep.status != http.StatusMultiStatus && rep.status/100 != 2 {
		return nil, rep.url, &DAVError{Call: call, HTTP: rep.status}
	}
	var ms multistatus
	if err := xml.Unmarshal(rep.body, &ms); err != nil {
		return nil, rep.url, fmt.Errorf("carddav: %s: the answer is not a WebDAV multistatus", call)
	}
	return &ms, rep.url, nil
}

// responseFor finds the response describing u itself among a multistatus's.
func responseFor(ms *multistatus, base, u *url.URL) (davResponse, bool) {
	for _, r := range ms.Responses {
		if h, ok := r.href(base); ok && samePath(h, u) {
			return r, true
		}
	}
	// A server that answers a Depth 0 request with one response under another spelling
	// of the same URL still answered for it.
	if len(ms.Responses) == 1 {
		return ms.Responses[0], true
	}
	return davResponse{}, false
}

// samePath compares two URLs by host and decoded path, ignoring a trailing slash.
func samePath(a, b *url.URL) bool {
	return strings.EqualFold(a.Host, b.Host) &&
		strings.TrimSuffix(a.Path, "/") == strings.TrimSuffix(b.Path, "/")
}

const discoverProps = `<d:resourcetype/><d:displayname/><d:current-user-principal/><card:addressbook-home-set/>`

// addressBook is the Worker's address book: the endpoint itself when it is one, or the
// one the server names for the account (RFC 6352 §7, RFC 6764 §6).
func (c *Client) addressBook(ctx context.Context) (*url.URL, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.collection != nil {
		return c.collection, nil
	}
	coll, err := c.discover(ctx)
	if err != nil {
		return nil, err
	}
	c.collection = coll
	return coll, nil
}

func (c *Client) discover(ctx context.Context) (*url.URL, error) {
	ms, base, err := c.propfind(ctx, "PROPFIND (finding the address book)", c.endpoint, "0", discoverProps)
	root := c.endpoint.Path == "/" || c.endpoint.Path == ""
	if err != nil && (!root || !isStatus(err, http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusNotImplemented)) {
		// Only a server's root may say nothing and leave the question to
		// /.well-known/carddav; anywhere else the endpoint is what the operator meant.
		return nil, err
	}
	var home, principal *url.URL
	if err == nil {
		// base, not the endpoint: a Depth 0 answer describes the resource the request
		// ended at, after any redirect.
		if r, ok := responseFor(ms, base, base); ok {
			if r.isAddressBook() {
				return collectionURL(base), nil
			}
			home, principal = hrefOf(r.okProp().HomeSet, base), hrefOf(r.okProp().CurrentUserPrincipal, base)
		}
	}
	if home == nil && principal == nil && root {
		// The server's root says nothing about the account: ask where its CardDAV
		// service is (RFC 6764 §5).
		wk := *c.endpoint
		wk.Path = "/.well-known/carddav"
		ms, base, err = c.propfind(ctx, "PROPFIND (.well-known/carddav)", &wk, "0", discoverProps)
		if err != nil {
			return nil, fmt.Errorf("%w; set the Worker's endpoint to the address book's URL", err)
		}
		if r, ok := responseFor(ms, base, base); ok {
			if r.isAddressBook() {
				return collectionURL(base), nil
			}
			home, principal = hrefOf(r.okProp().HomeSet, base), hrefOf(r.okProp().CurrentUserPrincipal, base)
		}
	}
	if home == nil && principal != nil {
		if err := c.reachable(c.endpoint, principal); err != nil {
			return nil, fmt.Errorf("carddav: finding the address book: %w", err)
		}
		ms, base, err := c.propfind(ctx, "PROPFIND (the account's principal)", principal, "0", `<card:addressbook-home-set/>`)
		if err != nil {
			return nil, err
		}
		if r, ok := responseFor(ms, base, base); ok {
			home = hrefOf(r.okProp().HomeSet, base)
		}
	}
	if home == nil {
		return nil, fmt.Errorf("carddav: the server did not say where this account's address books are (no addressbook-home-set); set the Worker's endpoint to the address book's URL")
	}
	if err := c.reachable(c.endpoint, home); err != nil {
		return nil, fmt.Errorf("carddav: finding the address book: %w", err)
	}
	return c.pickAddressBook(ctx, home)
}

// generatedBook reports whether an address book is one Nextcloud generates and keeps
// itself — "Recently contacted", the system address book — rather than one a person
// keeps contacts in. Its URI says so, by Nextcloud's own convention.
func generatedBook(u *url.URL) bool {
	seg := lastSegment(u)
	return strings.HasPrefix(seg, "z-app-generated--") || strings.HasPrefix(seg, "z-server-generated--")
}

// pickAddressBook lists the address books in an account's home and takes the one there
// is. More than one is refused with their names, rather than guessed at: a process
// writing customers into the wrong address book is a mistake nobody would see.
func (c *Client) pickAddressBook(ctx context.Context, home *url.URL) (*url.URL, error) {
	ms, base, err := c.propfind(ctx, "PROPFIND (the account's address books)", home, "1", `<d:resourcetype/><d:displayname/>`)
	if err != nil {
		return nil, err
	}
	type book struct {
		u    *url.URL
		name string
	}
	var books []book
	for _, r := range ms.Responses {
		u, ok := r.href(base)
		if !ok || !r.isAddressBook() || samePath(u, base) || generatedBook(u) {
			continue
		}
		books = append(books, book{collectionURL(u), r.okProp().DisplayName})
	}
	switch len(books) {
	case 1:
		return books[0].u, nil
	case 0:
		return nil, fmt.Errorf("carddav: the account has no address book; create one at the provider, or set the Worker's endpoint to the address book's URL")
	}
	sort.Slice(books, func(i, j int) bool { return books[i].u.Path < books[j].u.Path })
	var names []string
	for i, b := range books {
		if i == 10 {
			names = append(names, "…")
			break
		}
		label := b.u.EscapedPath()
		if b.name != "" {
			label = fmt.Sprintf("%q at %s", b.name, label)
		}
		names = append(names, label)
	}
	return nil, fmt.Errorf("carddav: the account has %d address books (%s); set the Worker's endpoint to the URL of the one this Worker uses", len(books), strings.Join(names, ", "))
}

// hrefOf is the first href of a property, resolved; nil for none.
func hrefOf(h *hrefList, base *url.URL) *url.URL {
	if h == nil || len(h.Hrefs) == 0 {
		return nil
	}
	u, err := base.Parse(strings.TrimSpace(h.Hrefs[0]))
	if err != nil {
		return nil
	}
	return u
}

// collectionURL is a collection's URL with the trailing slash a member's name is
// appended to.
func collectionURL(u *url.URL) *url.URL {
	out := *u
	out.RawQuery, out.Fragment = "", ""
	if !strings.HasSuffix(out.Path, "/") {
		out.Path += "/"
		if out.RawPath != "" {
			out.RawPath += "/"
		}
	}
	return &out
}

// lastSegment is a URL's last path segment as the server spells it (still escaped),
// without a trailing slash.
func lastSegment(u *url.URL) string {
	p := strings.TrimSuffix(u.EscapedPath(), "/")
	return p[strings.LastIndex(p, "/")+1:]
}

// memberURL is the URL of a contact in the address book, from its id: the id is one
// escaped path segment (catalog.CheckContactID has refused anything else), appended to
// the collection's path.
func memberURL(coll *url.URL, id string) (*url.URL, error) {
	decoded, err := url.PathUnescape(id)
	if err != nil {
		return nil, fmt.Errorf("carddav: %q is not a contact id", clip(id))
	}
	u := *coll
	u.Path = coll.Path + decoded
	u.RawPath = coll.EscapedPath() + id
	return &u, nil
}

// member is one resource of the address book as its listing reports it.
type member struct {
	id   string
	url  *url.URL
	etag string
}

// isMemberOf reports whether u is a direct member of the collection: on its host,
// under its path, one segment deep. A server that lists anything else in an address
// book's listing is not describing a contact.
func isMemberOf(coll, u *url.URL) bool {
	if !strings.EqualFold(u.Host, coll.Host) || u.Scheme != coll.Scheme || !strings.HasPrefix(u.Path, coll.Path) {
		return false
	}
	rest := u.Path[len(coll.Path):]
	return rest != "" && !strings.Contains(rest, "/")
}

// vcardType reports whether a listed resource's content type is a vCard's — or unknown,
// which some servers leave it.
func vcardType(ct string) bool {
	ct = strings.ToLower(strings.TrimSpace(ct))
	return ct == "" || strings.HasPrefix(ct, "text/vcard") || strings.HasPrefix(ct, "text/x-vcard") || strings.HasPrefix(ct, "text/directory")
}

// members lists the address book's contacts and group cards, failing above limit.
//
// Membership is judged against the collection the listing came from, after any
// redirect: judged against the URL asked for, a moved collection's members would all
// fail the test, and the address book would read as empty — the one answer a find must
// never give wrongly.
func (c *Client) members(ctx context.Context, coll *url.URL, limit int) ([]member, error) {
	ms, base, err := c.propfind(ctx, "PROPFIND (listing the address book)", coll, "1", `<d:getetag/><d:getcontenttype/><d:resourcetype/>`)
	if err != nil {
		return nil, err
	}
	listed := collectionURL(base)
	var out []member
	for _, r := range ms.Responses {
		u, ok := r.href(base)
		if !ok || r.isCollection() || !isMemberOf(listed, u) || !statusOK(r.Status) {
			continue
		}
		p := r.okProp()
		if !vcardType(p.ContentType) {
			continue
		}
		if len(out) == limit {
			return nil, errTooBig
		}
		out = append(out, member{id: lastSegment(u), url: u, etag: p.ETag})
	}
	return out, nil
}

// errTooBig is a listing above the most a find or a listing reads.
var errTooBig = errors.New("carddav: the address book holds more contacts than a find or a listing reads")

// fetched is one card a multiget answered with.
type fetched struct {
	data string
	etag string
}

// multigetBatch is how many cards one addressbook-multiget asks for: few enough that a
// batch of cards with photos usually stays inside the payload budget, and a batch that
// does not is halved rather than failed.
const multigetBatch = 50

// multiget reads the cards of the members named, keyed by decoded path. A member a
// server no longer has (deleted since the listing) is absent from the answer, and
// absent from the result.
func (c *Client) multiget(ctx context.Context, coll *url.URL, ms []member) (map[string]fetched, error) {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?><card:addressbook-multiget xmlns:d="DAV:" xmlns:card="urn:ietf:params:xml:ns:carddav"><d:prop><d:getetag/><card:address-data/></d:prop>`)
	for _, m := range ms {
		b.WriteString("<d:href>")
		_ = xml.EscapeText(&b, []byte(m.url.EscapedPath()))
		b.WriteString("</d:href>")
	}
	b.WriteString("</card:addressbook-multiget>")
	hdr := http.Header{"Depth": {"1"}, "Content-Type": {"application/xml; charset=utf-8"}}
	rep, err := c.do(ctx, "REPORT addressbook-multiget", "REPORT", coll, hdr, []byte(b.String()))
	if err != nil {
		return nil, err
	}
	if rep.status != http.StatusMultiStatus {
		return nil, &DAVError{Call: "REPORT addressbook-multiget", HTTP: rep.status}
	}
	var res multistatus
	if err := xml.Unmarshal(rep.body, &res); err != nil {
		return nil, fmt.Errorf("carddav: REPORT addressbook-multiget: the answer is not a WebDAV multistatus")
	}
	out := map[string]fetched{}
	for _, r := range res.Responses {
		u, ok := r.href(rep.url)
		if !ok || !statusOK(r.Status) {
			continue
		}
		p := r.okProp()
		if p.AddressData == nil {
			continue
		}
		out[u.Path] = fetched{data: *p.AddressData, etag: p.ETag}
	}
	return out, nil
}

// getCard reads one contact and the ETag it is written under.
func (c *Client) getCard(ctx context.Context, call string, u *url.URL) (*Card, string, error) {
	rep, err := c.do(ctx, call, http.MethodGet, u, http.Header{"Accept": {"text/vcard, text/x-vcard;q=0.5"}}, nil)
	if err != nil {
		return nil, "", err
	}
	if rep.status/100 != 2 {
		return nil, "", &DAVError{Call: call, HTTP: rep.status}
	}
	card, err := ParseCard(rep.body)
	if err != nil {
		return nil, "", fmt.Errorf("carddav: %s: %w", call, err)
	}
	return card, rep.header.Get("ETag"), nil
}

// putCard writes a card. ifMatch writes it only over the version that ETag names;
// create writes it only where nothing is yet (If-None-Match: *). A weak ETag is never
// sent as a precondition: If-Match compares strongly (RFC 7232 §3.1), so it would
// refuse every write, and the update would fail on a contact nobody else touched.
func (c *Client) putCard(ctx context.Context, call string, u *url.URL, card *Card, ifMatch string, create bool) error {
	hdr := http.Header{"Content-Type": {"text/vcard; charset=utf-8"}}
	switch {
	case create:
		hdr.Set("If-None-Match", "*")
	case ifMatch != "" && !strings.HasPrefix(ifMatch, "W/"):
		hdr.Set("If-Match", ifMatch)
	}
	rep, err := c.do(ctx, call, http.MethodPut, u, hdr, card.Bytes())
	if err != nil {
		return err
	}
	if rep.status/100 != 2 {
		if strings.Contains(string(rep.body), "no-uid-conflict") {
			return fmt.Errorf("carddav: %s: the address book already holds another contact with this card's UID", call)
		}
		return &DAVError{Call: call, HTTP: rep.status}
	}
	return nil
}

// deleteCard removes one contact.
func (c *Client) deleteCard(ctx context.Context, u *url.URL) error {
	rep, err := c.do(ctx, "DELETE contact", http.MethodDelete, u, nil, nil)
	if err != nil {
		return err
	}
	if rep.status/100 != 2 {
		return &DAVError{Call: "DELETE contact", HTTP: rep.status}
	}
	return nil
}
