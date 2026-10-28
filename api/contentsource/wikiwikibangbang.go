package contentsource

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The wikiwikibangbang adapter (§ 10, § 11).
//
// It speaks the source's HTTP API with the technical account's read token as a
// bearer token, and it is the only code in Atlas that knows that API's shapes —
// the rules read [Page] and [Standing], never the JSON below.
//
// Three properties are the adapter's own to keep:
//   - it follows no redirect to another host, so the token is never presented to
//     anybody but the configured source;
//   - every request has a deadline, so a source that stops answering is a source
//     that is unreachable, which quarantines, rather than a goroutine that hangs;
//   - the token appears in no error and no log line: errors name the path and the
//     status, and the request URL never carries the token.

// requestTimeout bounds one ordinary request.
const requestTimeout = 20 * time.Second

// maxResponse bounds a JSON answer. A page and its body fit in far less; a source
// that answers more is not answering the question asked.
const maxResponse = 8 << 20

// refShape is what an id or a key may look like on its way into a path: the
// characters wikiwikibangbang mints them from, and nothing a path could be
// confused by.
var refShape = regexp.MustCompile(`^[A-Za-z0-9._~-]{1,200}$`)

// WikiWikiBangBang is one configured wikiwikibangbang source.
type WikiWikiBangBang struct {
	base   *url.URL
	token  string
	client *http.Client
}

// NewWikiWikiBangBang builds the adapter for a source's base URL and token.
// transport is nil in production — the process-wide transport, which carries the
// outbound proxy (internal/sysproxy) — and a test server's in tests.
func NewWikiWikiBangBang(baseURL, token string, transport http.RoundTripper) (*WikiWikiBangBang, error) {
	if why := baseURLProblem(baseURL); why != "" {
		return nil, errors.New(why)
	}
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("no read token")
	}
	base, _ := url.Parse(strings.TrimRight(strings.TrimSpace(baseURL), "/"))
	if transport == nil {
		transport = http.DefaultTransport
	}
	w := &WikiWikiBangBang{base: base, token: token}
	w.client = &http.Client{
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Same host and https, or not at all. net/http drops the
			// Authorization header across hosts on its own; refusing the redirect
			// is the stronger statement — Atlas reads the source it was told to.
			if req.URL.Scheme != "https" || !strings.EqualFold(req.URL.Host, base.Host) {
				return fmt.Errorf("the source redirected to %s, which is not %s", req.URL.Host, base.Host)
			}
			if len(via) >= 5 {
				return errors.New("the source redirected too often")
			}
			return nil
		},
	}
	return w, nil
}

// statusError is a non-success answer, named by path and status only.
type statusError struct {
	path   string
	status int
}

func (e *statusError) Error() string {
	return fmt.Sprintf("the source answered %d to %s", e.status, e.path)
}

// do sends one request and decodes its JSON answer into into (when non-nil).
func (w *WikiWikiBangBang) do(ctx context.Context, method, path string, q url.Values, timeout time.Duration, into any) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	u := *w.base
	u.Path = strings.TrimRight(w.base.Path, "/") + path
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, method, u.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+w.token)
	req.Header.Set("Accept", "application/json")
	resp, err := w.client.Do(req)
	if err != nil {
		// A *url.Error carries the URL, which carries no token: the token is a
		// header. The path is named instead of the whole error where the error
		// is a deadline, so the sentence is about the source, not about Go.
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("the source did not answer %s within %s", path, timeout)
		}
		return fmt.Errorf("the source could not be reached for %s: %w", path, unwrapURL(err))
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		// Unreadable and non-existent are one answer at the source (its D10).
		return ErrUnreadable
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return &statusError{path: path, status: resp.StatusCode}
	}
	if into == nil {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		return fmt.Errorf("the source's answer to %s broke off: %w", path, err)
	}
	if len(body) > maxResponse {
		return fmt.Errorf("the source's answer to %s exceeds %d bytes", path, maxResponse)
	}
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("the source's answer to %s is not what it should be: %w", path, err)
	}
	return nil
}

// unwrapURL drops a *url.Error's method and URL, keeping what went wrong.
func unwrapURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

func (w *WikiWikiBangBang) get(ctx context.Context, path string, q url.Values, into any) error {
	return w.do(ctx, http.MethodGet, path, q, requestTimeout, into)
}

// Standing reads the account's standing from two routes: GET /api/me for the
// token's scope and the two system roles it reports as flags, and GET
// /api/me/roles for every grant, direct and through groups (§ 11). A source that
// does not answer the second has not proved the account is a viewer, and the
// fields stay unknown.
func (w *WikiWikiBangBang) Standing(ctx context.Context) (Standing, error) {
	var me struct {
		Scope   string `json:"scope"`
		Admin   *bool  `json:"admin"`
		Erasure *bool  `json:"erasure"`
	}
	if err := w.get(ctx, "/api/me", nil, &me); err != nil {
		return Standing{}, err
	}
	var roles struct {
		System *[]string `json:"system"`
		Spaces *[]struct {
			Space string `json:"space"`
			Role  string `json:"role"`
			Via   string `json:"via"`
		} `json:"spaces"`
	}
	if err := w.get(ctx, "/api/me/roles", nil, &roles); err != nil && !errors.Is(err, ErrUnreadable) {
		return Standing{}, err
	}
	st := Standing{Scope: me.Scope}
	if roles.System != nil && me.Admin != nil && me.Erasure != nil {
		st.SystemRolesKnown = true
		st.SystemRoles = append(st.SystemRoles, *roles.System...)
		if *me.Admin && !containsFold(st.SystemRoles, "admin") {
			st.SystemRoles = append(st.SystemRoles, "admin")
		}
		if *me.Erasure && !containsFold(st.SystemRoles, "erasure") {
			st.SystemRoles = append(st.SystemRoles, "erasure")
		}
	}
	if roles.Spaces != nil {
		st.SpacesKnown = true
		for _, g := range *roles.Spaces {
			st.Spaces = append(st.Spaces, SpaceGrant{Space: g.Space, Role: g.Role, Via: g.Via})
		}
	}
	return st, nil
}

func containsFold(list []string, s string) bool {
	for _, v := range list {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}

// itemJSON is the part of wikiwikibangbang's item the adapter reads.
type itemJSON struct {
	ID         string         `json:"id"`
	Key        string         `json:"key"`
	Space      string         `json:"space"`
	Parent     string         `json:"parent"`
	Title      string         `json:"title"`
	Properties map[string]any `json:"properties"`
	State      string         `json:"state"`
	Body       []struct {
		Kind    string `json:"kind"`
		Text    string `json:"text"`
		Ref     string `json:"ref"`
		Level   int    `json:"level"`
		Tone    string `json:"tone"`
		Checked bool   `json:"checked"`
		Align   string `json:"align"`
		Width   int    `json:"width"`
		Marks   []struct {
			Type  string `json:"type"`
			Start int    `json:"start"`
			End   int    `json:"end"`
			Ref   string `json:"ref"`
		} `json:"marks"`
	} `json:"body"`
	Version  int    `json:"version"`
	Presence string `json:"presence"`
	// Restriction and Hold are omitted when there is none: the source's own
	// statement that there is none, which is what makes their absence Absent.
	Restriction *struct {
		Read []string `json:"read"`
		Edit []string `json:"edit"`
	} `json:"restriction"`
	Hold   *json.RawMessage `json:"hold"`
	Labels []string         `json:"labels"`
}

// page maps the source's item onto the facts the rules read.
func (w *WikiWikiBangBang) page(it itemJSON) Page {
	p := Page{ID: it.ID, Key: it.Key, Title: it.Title, Space: it.Space, Parent: it.Parent,
		Version: it.Version, State: it.State, Labels: it.Labels, Properties: it.Properties,
		Presence: it.Presence, URL: w.address(nonEmpty(it.Key, it.ID))}
	// A restriction names who may read; an edit-only restriction narrows writing
	// and leaves reading open, so only the read list counts.
	p.ReadRestricted = FactOf(it.Restriction != nil && len(it.Restriction.Read) > 0)
	p.Held = FactOf(it.Hold != nil && string(*it.Hold) != "null")
	switch it.Presence {
	case "live":
		p.Withdrawn = Absent
	case "":
		p.Withdrawn = Unknown
	default:
		// trashed, purged — a redaction leaves a purged tombstone — and any
		// presence this adapter does not know, which is not live either.
		p.Withdrawn = Present
	}
	for _, b := range it.Body {
		blk := Block{Kind: blockKind(b.Kind), Source: b.Kind, Text: b.Text, Ref: b.Ref, Level: b.Level,
			Tone: b.Tone, Checked: b.Checked, Align: b.Align, Width: b.Width}
		for _, m := range b.Marks {
			kind := MarkOther
			switch m.Type {
			case "secret":
				kind = MarkPassage
			case "mention":
				kind = MarkMention
			}
			blk.Marks = append(blk.Marks, Mark{Kind: kind, Start: m.Start, End: m.End, Ref: m.Ref})
		}
		p.Body = append(p.Body, blk)
	}
	return p
}

// blockKind maps the source's block kinds. Only the ones § 7 takes are text;
// a quote, a panel, a table, code, a divider, an image and an embed are taken by
// the rich description (amendment of 2026-10-28); a video, a model, every computed
// list and a kind added to the source after this was written are not.
func blockKind(k string) BlockKind {
	switch k {
	case "quote":
		return BlockQuote
	case "code":
		return BlockCode
	case "panel":
		return BlockPanel
	case "table":
		return BlockTable
	case "divider":
		return BlockDivider
	case "image":
		return BlockImage
	case "embed":
		return BlockEmbed
	case "paragraph":
		return BlockParagraph
	case "heading":
		return BlockHeading
	case "bullet", "task":
		return BlockBullet
	case "ordered":
		return BlockOrdered
	}
	return BlockOther
}

// address is a page's address in the source's own interface.
func (w *WikiWikiBangBang) address(ref string) string {
	return strings.TrimRight(w.base.String(), "/") + "/#/item/" + url.PathEscape(ref)
}

// Search runs the source's full-text search (GET /api/search).
func (w *WikiWikiBangBang) Search(ctx context.Context, q string) ([]Hit, error) {
	var res struct {
		Hits []struct {
			Item itemJSON `json:"item"`
		} `json:"hits"`
	}
	if err := w.get(ctx, "/api/search", url.Values{"q": {q}, "limit": {"25"}}, &res); err != nil {
		return nil, err
	}
	out := []Hit{}
	for _, h := range res.Hits {
		out = append(out, Hit{ID: h.Item.ID, Key: h.Item.Key, Title: h.Item.Title, Space: h.Item.Space,
			State: h.Item.State, URL: w.address(nonEmpty(h.Item.Key, h.Item.ID))})
	}
	return out, nil
}

// Resolve turns an address (https://host/#/item/SVC-12), a key or an id into the
// page's id. The id is what a binding stores: a key changes when an item moves
// between spaces, an id never does.
func (w *WikiWikiBangBang) Resolve(ctx context.Context, input string) (string, error) {
	ref := strings.TrimSpace(input)
	if strings.Contains(ref, "://") {
		u, err := url.Parse(ref)
		if err != nil {
			return "", fmt.Errorf("%q is not an address", input)
		}
		if u.Scheme != "https" || !strings.EqualFold(u.Host, w.base.Host) {
			return "", fmt.Errorf("the address %s is not on %s, the source's host", u.Host, w.base.Host)
		}
		frag := strings.TrimPrefix(u.Fragment, "/")
		item, found := strings.CutPrefix(frag, "item/")
		if !found {
			return "", fmt.Errorf("the address does not name a page (want %s/#/item/KEY)", w.base.String())
		}
		ref, _, _ = strings.Cut(item, "/")
		ref, _, _ = strings.Cut(ref, "?")
	}
	if !refShape.MatchString(ref) {
		return "", fmt.Errorf("%q is neither an address, a key nor an id of a page", input)
	}
	var res struct {
		Item itemJSON `json:"item"`
	}
	if err := w.get(ctx, "/api/items/"+ref, nil, &res); err != nil {
		return "", err
	}
	if res.Item.ID == "" {
		return "", errors.New("the source answered a page without an id")
	}
	return res.Item.ID, nil
}

// Page reads one page (GET /api/items/{id}).
func (w *WikiWikiBangBang) Page(ctx context.Context, id string) (Page, error) {
	if !refShape.MatchString(id) {
		return Page{}, ErrUnreadable
	}
	var res struct {
		Item itemJSON `json:"item"`
	}
	if err := w.get(ctx, "/api/items/"+id, nil, &res); err != nil {
		return Page{}, err
	}
	if res.Item.ID == "" {
		return Page{}, errors.New("the source answered a page without an id")
	}
	return w.page(res.Item), nil
}

// Ancestors walks the parent chain with one GET per ancestor. The source answers
// a page's own restriction and not its ancestors', so the chain is the only way to
// learn what the page inherits.
func (w *WikiWikiBangBang) Ancestors(ctx context.Context, p Page) ([]Page, error) {
	return WalkAncestors(ctx, w.Page, p)
}

// Children lists a page's readable children in sibling order, following the
// tree's cursor (GET /api/spaces/{space}/tree?parent=).
func (w *WikiWikiBangBang) Children(ctx context.Context, p Page) ([]string, error) {
	if !refShape.MatchString(p.Space) {
		return nil, fmt.Errorf("the page's space %q cannot be listed", p.Space)
	}
	var out []string
	after := ""
	for range maxBelow/100 + 2 {
		q := url.Values{"parent": {p.ID}, "limit": {"100"}}
		if after != "" {
			q.Set("after", after)
		}
		var res struct {
			Items []struct {
				ID string `json:"id"`
			} `json:"items"`
			Next string `json:"next"`
		}
		if err := w.get(ctx, "/api/spaces/"+p.Space+"/tree", q, &res); err != nil {
			return nil, err
		}
		for _, it := range res.Items {
			out = append(out, it.ID)
		}
		if res.Next == "" {
			return out, nil
		}
		after = res.Next
	}
	return nil, errTooMany
}

// Attachment reads the newest file of a page by its name: the listing, then the
// download (GET /api/items/{id}/attachments/{attachment}).
func (w *WikiWikiBangBang) Attachment(ctx context.Context, pageID, file string, max int64) ([]byte, string, error) {
	if !refShape.MatchString(pageID) {
		return nil, "", ErrUnreadable
	}
	var list struct {
		Attachments []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"attachments"`
	}
	if err := w.get(ctx, "/api/items/"+pageID+"/attachments", nil, &list); err != nil {
		return nil, "", err
	}
	id, typ := "", ""
	// The listing is in the order the files were added, so the last one of a name
	// is its newest version.
	for _, a := range list.Attachments {
		if a.Name == file || a.ID == file {
			id, typ = a.ID, a.Type
		}
	}
	if id == "" || !refShape.MatchString(id) {
		return nil, "", ErrUnreadable
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	u := *w.base
	u.Path = strings.TrimRight(w.base.Path, "/") + "/api/items/" + pageID + "/attachments/" + id
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+w.token)
	resp, err := w.client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("the source could not be reached for an attachment: %w", unwrapURL(err))
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, "", ErrUnreadable
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return nil, "", &statusError{path: "an attachment", status: resp.StatusCode}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, "", fmt.Errorf("an attachment broke off: %w", err)
	}
	if int64(len(data)) > max {
		return nil, "", errTooLarge
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" && ct != "application/octet-stream" {
		typ = ct
	}
	return data, typ, nil
}

// Watch asks the source to report a page in the account's change feed. A
// read-scope token may watch (§ 11): watching changes no content.
func (w *WikiWikiBangBang) Watch(ctx context.Context, id string) error {
	if !refShape.MatchString(id) {
		return ErrUnreadable
	}
	return w.do(ctx, http.MethodPut, "/api/items/"+id+"/watch", nil, requestTimeout, nil)
}

// Unwatch ends the account's watch of a page by its id. By id it ends even the
// watch of a page the account can no longer read (its ADR-0120); by key it would
// not, which is why a binding keeps ids.
func (w *WikiWikiBangBang) Unwatch(ctx context.Context, id string) error {
	if !refShape.MatchString(id) {
		return ErrUnreadable
	}
	err := w.do(ctx, http.MethodDelete, "/api/items/"+id+"/watch", nil, requestTimeout, nil)
	if errors.Is(err, ErrUnreadable) {
		// No such watch any more: the state asked for holds.
		return nil
	}
	return err
}

// MaxWait is the longest the source holds a long poll (its ChangesWait budget).
const MaxWait = 30 * time.Second

type changesJSON struct {
	Changes []struct {
		Seq  int64  `json:"seq"`
		Item string `json:"item"`
	} `json:"changes"`
	Last *int64 `json:"last"`
}

// Head reads the feed's position: GET /api/changes without a cursor answers no
// changes and the head.
func (w *WikiWikiBangBang) Head(ctx context.Context) (int64, error) {
	var res changesJSON
	if err := w.get(ctx, "/api/changes", nil, &res); err != nil {
		return 0, err
	}
	if res.Last == nil {
		return 0, errors.New("the source's change feed answered no position")
	}
	return *res.Last, nil
}

// Changes long-polls the feed for changes after the cursor.
func (w *WikiWikiBangBang) Changes(ctx context.Context, after int64, wait time.Duration) (Changes, error) {
	wait = min(wait, MaxWait)
	q := url.Values{"after": {strconv.FormatInt(after, 10)}, "limit": {"1000"},
		"wait": {strconv.Itoa(int(wait / time.Second))}}
	var res changesJSON
	if err := w.do(ctx, http.MethodGet, "/api/changes", q, wait+requestTimeout, &res); err != nil {
		return Changes{}, err
	}
	if res.Last == nil {
		return Changes{}, errors.New("the source's change feed answered no position")
	}
	out := Changes{Last: *res.Last}
	for _, c := range res.Changes {
		out.Changes = append(out.Changes, Change{Seq: c.Seq, Item: c.Item})
	}
	return out, nil
}
