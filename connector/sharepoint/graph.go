package sharepoint

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/pblumer/atlas/connector/nettimeout"
	"github.com/pblumer/atlas/limits"
)

// graphDefaultBase is the Microsoft Graph v1.0 API base a worker uses when it
// authors no endpoint override.
const graphDefaultBase = "https://graph.microsoft.com/v1.0"

// siteCacheSize bounds how many resolved site addresses one client remembers. A site
// is usually one of a handful a process works in; the bound only matters when a FEEL
// expression computes a different site for every token, and then forgetting them all
// at once is cheaper than keeping an order to evict in.
const siteCacheSize = 256

// downloadURLKey is the property on every file Graph describes that holds a
// pre-authenticated download link. It is removed from every answer: it is a bearer
// capability valid for about an hour, nobody asked for it, and a process variable is
// kept in the event log for as long as the installation keeps history. The file's
// webUrl, which needs the reader's own sign-in, stays.
const downloadURLKey = "@microsoft.graph.downloadUrl"

// honorNonIndexed is the Prefer header list-items sends with a filter. Without it Graph
// refuses a filter on any column the list has not indexed, which in a list of a few
// hundred items is every column an author thinks of; with it the query runs, and only
// a list past SharePoint's 5000-item view threshold can still refuse it — which is what
// indexing the column is for.
const honorNonIndexed = "HonorNonIndexedQueriesWarningMayFailRandomly"

// GraphClient performs SharePoint operations through the Microsoft Graph API
// (ADR-0141, amended), with a bearer token from its TokenSource, and returns what Graph
// answered as decoded JSON.
//
// It remembers which Graph site id a site URL or path resolved to, so a task naming
// its site the way a person copies it from the browser costs one lookup per site
// rather than one per job. It is safe for concurrent use.
type GraphClient struct {
	http    *http.Client
	tokens  TokenSource
	baseURL string

	mu    sync.Mutex
	sites map[string]string
}

// NewGraphClient builds a SharePoint Graph client. baseURL defaults to the Graph
// v1.0 API when empty.
func NewGraphClient(tokens TokenSource, baseURL string) *GraphClient {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = graphDefaultBase
	}
	return &GraphClient{http: nettimeout.HTTPClient(), tokens: tokens, baseURL: strings.TrimRight(baseURL, "/")}
}

// Do performs one operation. Every failure — a missing address, a transport error, a
// non-2xx answer Graph gave for a reason the operation cannot absorb — is returned so
// the job stays pending and is retried, then raises an incident (ADR-0061), rather than
// completing a token on work that did not happen.
func (c *GraphClient) Do(ctx context.Context, req Request) (any, error) {
	p, err := prepare(req)
	if err != nil {
		return nil, err
	}
	op, spec, path := p.op, p.spec, p.path
	site, err := c.siteBase(ctx, op, req.Site)
	if err != nil {
		return nil, err
	}
	if spec.NeedsList {
		list := site + "/lists/" + url.PathEscape(strings.TrimSpace(req.List))
		item := list + "/items/" + url.PathEscape(strings.TrimSpace(req.ItemID))
		switch op {
		case OpCreateItem:
			return c.createItem(ctx, op, list, req)
		case "list-items":
			return c.listItems(ctx, op, list, req)
		case "get-item":
			return c.getItem(ctx, op, item, req)
		case "update-item":
			return c.updateItem(ctx, op, item, req)
		default:
			return c.remove(ctx, op, item, "")
		}
	}
	drive := driveBase(site, req.List)
	switch op {
	case "upload-file":
		return c.uploadFile(ctx, op, drive, path, req)
	case "download-file":
		return c.downloadFile(ctx, op, drive, path, req)
	case "get-file":
		return c.getFile(ctx, op, drive, path)
	case "list-folder":
		return c.listFolder(ctx, op, drive, path, req)
	case "create-folder":
		return c.createFolder(ctx, op, drive, path)
	default:
		return c.remove(ctx, op, drive+"/root:/"+path.escaped, path.clean)
	}
}

// createItem creates a list item from its column values, which Graph takes under a
// "fields" object, and answers with the item as Graph returned it.
func (c *GraphClient) createItem(ctx context.Context, op, list string, req Request) (any, error) {
	fields := req.Fields
	if fields == nil {
		fields = map[string]any{}
	}
	r, err := c.sendJSON(ctx, op, http.MethodPost, list+"/items", map[string]any{"fields": fields}, "")
	if err != nil {
		return nil, err
	}
	if !r.ok() {
		return nil, graphFailure(op, http.MethodPost, r)
	}
	return decodeAnswer(op, r)
}

// getItem reads one list item with its column values — all of them, or the ones the
// task selected.
func (c *GraphClient) getItem(ctx context.Context, op, item string, req Request) (any, error) {
	r, err := c.send(ctx, op, http.MethodGet, item+"?$expand="+odataEscape(expandFields(req.Select)), nil, "", "")
	if err != nil {
		return nil, err
	}
	if !r.ok() {
		return nil, graphFailure(op, http.MethodGet, r)
	}
	return decodeAnswer(op, r)
}

// updateItem sets the given columns of one list item and leaves the rest as they are.
// Graph answers with the item's column values after the change.
func (c *GraphClient) updateItem(ctx context.Context, op, item string, req Request) (any, error) {
	r, err := c.sendJSON(ctx, op, http.MethodPatch, item+"/fields", req.Fields, "")
	if err != nil {
		return nil, err
	}
	if !r.ok() {
		return nil, graphFailure(op, http.MethodPatch, r)
	}
	return decodeAnswer(op, r)
}

// listItems reads one page of a list's items, filtered and narrowed as the task says.
func (c *GraphClient) listItems(ctx context.Context, op, list string, req Request) (any, error) {
	query := "$expand=" + odataEscape(expandFields(req.Select)) + "&$top=" + strconv.Itoa(int(pageSize(req.MaxResults)))
	if f := strings.TrimSpace(req.Filter); f != "" {
		query += "&$filter=" + odataEscape(f)
	}
	query, err := resumeAt(op, query, req.PageToken)
	if err != nil {
		return nil, err
	}
	// The header follows the filter wherever it is: authored on this task, or carried
	// in the page token of a listing that was filtered when it began.
	prefer := ""
	if strings.Contains(query, "$filter=") || strings.Contains(query, "%24filter=") {
		prefer = honorNonIndexed
	}
	r, err := c.send(ctx, op, http.MethodGet, list+"/items?"+query, nil, "", prefer)
	if err != nil {
		return nil, err
	}
	if !r.ok() {
		return nil, graphFailure(op, http.MethodGet, r)
	}
	return page(op, r)
}

// uploadFile writes a document to a path in the library — replacing the file that is
// there, which a library with versioning keeps as its previous version — and, when the
// task gives column values, sets them as the file's properties.
func (c *GraphClient) uploadFile(ctx context.Context, op, drive string, path cleanedPath, req Request) (any, error) {
	data, err := decodeContent(op, req)
	if err != nil {
		return nil, err
	}
	r, err := c.send(ctx, op, http.MethodPut, drive+"/root:/"+path.escaped+":/content", data, "application/octet-stream", "")
	if err != nil {
		return nil, err
	}
	if !r.ok() {
		return nil, graphFailure(op, http.MethodPut, r)
	}
	answer, err := decodeAnswer(op, r)
	if err != nil {
		return nil, err
	}
	file, _ := answer.(map[string]any)
	if file == nil {
		return nil, fmt.Errorf("sharepoint: %s of %q was accepted, but Graph answered with no file", op, path.clean)
	}
	delete(file, downloadURLKey)
	if len(req.Fields) == 0 {
		return file, nil
	}
	// The properties are the file's list item, reached through the file Graph just
	// described. A second call, and a separate failure: the file is stored by now, and
	// the retry this error causes stores the same bytes again before it sets them.
	id, _ := file["id"].(string)
	if id == "" {
		return nil, fmt.Errorf("sharepoint: %s stored %q, but Graph's answer carries no id to set its properties through", op, path.clean)
	}
	fr, err := c.sendJSON(ctx, op, http.MethodPatch, drive+"/items/"+url.PathEscape(id)+"/listItem/fields", req.Fields, "")
	if err != nil {
		return nil, err
	}
	if !fr.ok() {
		return nil, fmt.Errorf("%w (the file itself was stored)", graphFailure(op, http.MethodPatch, fr))
	}
	fields, err := decodeAnswer(op, fr)
	if err != nil {
		return nil, err
	}
	// Under listItem.fields, where get-file puts a file's properties too, so a model reads
	// them from the same place whichever operation answered.
	file["listItem"] = map[string]any{"fields": fields}
	return file, nil
}

// downloadFile reads a document's content into the answer, as text or base64. Graph
// answers the content request with a redirect to a pre-authenticated location, which
// the HTTP client follows without the bearer token — it does not carry an Authorization
// header to another host.
func (c *GraphClient) downloadFile(ctx context.Context, op, drive string, path cleanedPath, req Request) (any, error) {
	r, err := c.send(ctx, op, http.MethodGet, drive+"/root:/"+path.escaped+":/content", nil, "", "")
	if err != nil {
		return nil, err
	}
	if !r.ok() {
		return nil, graphFailure(op, http.MethodGet, r)
	}
	if int64(len(r.body)) > MaxContentBytes() {
		return nil, fmt.Errorf("sharepoint: %s: %q is larger than %d bytes, which is all a process variable holds; "+
			"hand a person the file's webUrl (get-file answers with it) instead of its content", op, path.clean, MaxContentBytes())
	}
	content, err := encodeContent(op, path.clean, req, r.body)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"path":        path.clean,
		"name":        path.name(),
		"content":     content,
		"encoding":    contentEncoding(req),
		"contentType": r.header.Get("Content-Type"),
		"size":        json.Number(strconv.Itoa(len(r.body))),
	}, nil
}

// getFile reads a file's or folder's properties, with its library columns, and says
// whether it is there at all: a path that is not answers exists=false rather than
// failing, because that is the question this operation is asked.
func (c *GraphClient) getFile(ctx context.Context, op, drive string, path cleanedPath) (any, error) {
	r, err := c.send(ctx, op, http.MethodGet, drive+"/root:/"+path.escaped+"?$expand="+odataEscape("listItem($expand=fields)"), nil, "", "")
	if err != nil {
		return nil, err
	}
	if r.status == http.StatusNotFound {
		return map[string]any{"exists": false, "path": path.clean}, nil
	}
	if !r.ok() {
		return nil, graphFailure(op, http.MethodGet, r)
	}
	answer, err := decodeAnswer(op, r)
	if err != nil {
		return nil, err
	}
	file, _ := answer.(map[string]any)
	if file == nil {
		return nil, fmt.Errorf("sharepoint: %s of %q answered with no file", op, path.clean)
	}
	delete(file, downloadURLKey)
	file["exists"] = true
	return file, nil
}

// listFolder reads one page of what a folder holds — the library's top level when the
// task names no path.
func (c *GraphClient) listFolder(ctx context.Context, op, drive string, path cleanedPath, req Request) (any, error) {
	query, err := resumeAt(op, "$top="+strconv.Itoa(int(pageSize(req.MaxResults))), req.PageToken)
	if err != nil {
		return nil, err
	}
	r, err := c.send(ctx, op, http.MethodGet, childrenOf(drive, path.escaped)+"?"+query, nil, "", "")
	if err != nil {
		return nil, err
	}
	if !r.ok() {
		return nil, graphFailure(op, http.MethodGet, r)
	}
	return page(op, r)
}

// createFolder makes a folder and every missing folder above it, one level at a time,
// and treats a level that is already there as made. That is what makes it safe to
// repeat: a retry after a create that succeeded finds every level present and answers
// with the folder, created=false.
func (c *GraphClient) createFolder(ctx context.Context, op, drive string, path cleanedPath) (any, error) {
	segments := strings.Split(path.escaped, "/")
	names := strings.Split(path.clean, "/")
	var made any
	for i := range segments {
		body := map[string]any{
			"name":                              names[i],
			"folder":                            map[string]any{},
			"@microsoft.graph.conflictBehavior": "fail",
		}
		r, err := c.sendJSON(ctx, op, http.MethodPost, childrenOf(drive, strings.Join(segments[:i], "/")), body, "")
		if err != nil {
			return nil, err
		}
		switch {
		case r.status == http.StatusConflict:
			made = nil // already there; the next level goes inside it
		case r.ok():
			if made, err = decodeAnswer(op, r); err != nil {
				return nil, err
			}
		default:
			return nil, graphFailure(op, http.MethodPost, r)
		}
	}
	created := made != nil
	if !created {
		r, err := c.send(ctx, op, http.MethodGet, drive+"/root:/"+path.escaped, nil, "", "")
		if err != nil {
			return nil, err
		}
		if !r.ok() {
			return nil, graphFailure(op, http.MethodGet, r)
		}
		if made, err = decodeAnswer(op, r); err != nil {
			return nil, err
		}
	}
	folder, _ := made.(map[string]any)
	if folder == nil {
		return nil, fmt.Errorf("sharepoint: %s of %q answered with no folder", op, path.clean)
	}
	if _, isFolder := folder["folder"]; !isFolder {
		return nil, fmt.Errorf("sharepoint: %s: %q is a file, not a folder", op, path.clean)
	}
	delete(folder, downloadURLKey)
	folder["created"] = created
	return folder, nil
}

// remove deletes a list item or a file. Something that is not there answers
// deleted=false instead of failing: at-least-once delivery means a retry after a delete
// that succeeded finds exactly that, and an incident for it would report a failure that
// did not happen. A deleted file or folder goes to the site's recycle bin, and a folder
// takes everything inside it along.
func (c *GraphClient) remove(ctx context.Context, op, endpoint, path string) (any, error) {
	r, err := c.send(ctx, op, http.MethodDelete, endpoint, nil, "", "")
	if err != nil {
		return nil, err
	}
	answer := map[string]any{"deleted": true}
	switch {
	case r.status == http.StatusNotFound:
		answer["deleted"] = false
	case !r.ok():
		return nil, graphFailure(op, http.MethodDelete, r)
	}
	if path != "" {
		answer["path"] = path
	}
	return answer, nil
}

// prepared is a request that has passed the checks every client makes before it does
// anything: the operation it names, what that operation is, and its path made safe.
type prepared struct {
	op   string
	spec Op
	path cleanedPath
}

// prepare refuses what a request is missing, before anything is called — a site lookup
// included. The compiler has refused most of it already; what is left is a FEEL value
// that resolved to nothing. Both clients start here, so the mockup refuses exactly the
// requests the real site would never be sent.
func prepare(req Request) (prepared, error) {
	op := req.Operation
	if op == "" {
		op = OpCreateItem
	}
	spec, ok := Ops[op]
	if !ok {
		return prepared{}, fmt.Errorf("sharepoint: unknown operation %q (want %s)", req.Operation, strings.Join(OpNames(), ", "))
	}
	if strings.TrimSpace(req.Site) == "" {
		return prepared{}, fmt.Errorf("sharepoint: %s has no site", op)
	}
	if spec.NeedsList && strings.TrimSpace(req.List) == "" {
		return prepared{}, fmt.Errorf("sharepoint: %s has no list", op)
	}
	if spec.NeedsItemID && strings.TrimSpace(req.ItemID) == "" {
		return prepared{}, fmt.Errorf("sharepoint: %s has no itemId", op)
	}
	p := prepared{op: op, spec: spec}
	if !spec.NeedsList {
		path, err := cleanPath(op, req.Path, spec.TakesPath && !spec.NeedsPath)
		if err != nil {
			return prepared{}, err
		}
		p.path = path
	}
	return p, nil
}

// graphResponse is one answer, read under the variable budget.
type graphResponse struct {
	status int
	header http.Header
	body   []byte
}

func (r graphResponse) ok() bool { return r.status/100 == 2 }

// sendJSON encodes a payload and sends it as the request body.
func (c *GraphClient) sendJSON(ctx context.Context, op, method, endpoint string, payload any, prefer string) (graphResponse, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return graphResponse{}, fmt.Errorf("sharepoint: encode %s request: %w", op, err)
	}
	return c.send(ctx, op, method, endpoint, body, "application/json", prefer)
}

// send performs one authenticated request and reads its answer. The body is read under
// the process variable's budget plus one byte, so an answer too large to land anywhere
// is recognised as too large rather than held in full first: the cap belongs at the
// read, because a server answering with a gigabyte would otherwise put a gigabyte in
// this process's memory before anything refused it.
func (c *GraphClient) send(ctx context.Context, op, method, endpoint string, body []byte, contentType, prefer string) (graphResponse, error) {
	tok, err := c.tokens.Token(ctx)
	if err != nil {
		return graphResponse{}, err
	}
	var payload io.Reader
	if body != nil {
		payload = bytes.NewReader(body)
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, endpoint, payload)
	if err != nil {
		return graphResponse{}, fmt.Errorf("sharepoint: build %s request: %w", op, err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+tok)
	httpReq.Header.Set("Accept", "application/json")
	if contentType != "" {
		httpReq.Header.Set("Content-Type", contentType)
	}
	if prefer != "" {
		httpReq.Header.Set("Prefer", prefer)
	}
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return graphResponse{}, fmt.Errorf("sharepoint: %s: %w", op, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limits.Default().Variable+1))
	if err != nil {
		return graphResponse{}, fmt.Errorf("sharepoint: read %s response: %w", op, err)
	}
	return graphResponse{status: resp.StatusCode, header: resp.Header, body: raw}, nil
}

// decodeAnswer decodes a JSON answer, keeping numbers exact through the variable
// round-trip. An answer larger than a process variable holds is refused here, with what
// to change, rather than failing later at the variable with a message about bytes.
func decodeAnswer(op string, r graphResponse) (any, error) {
	if int64(len(r.body)) > MaxContentBytes() {
		return nil, fmt.Errorf("sharepoint: %s answered with more than %d bytes, which is more than a process variable holds; "+
			"narrow it with select or a smaller maxResults", op, MaxContentBytes())
	}
	if len(bytes.TrimSpace(r.body)) == 0 {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(r.body))
	dec.UseNumber()
	var out any
	if err := dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("sharepoint: decode %s response: %w", op, err)
	}
	return out, nil
}

// page turns one page of a Graph collection into a listing's answer: the entries, how
// many there are, and — when Graph has more — the token that resumes after them.
//
// The token is the query of Graph's nextLink and nothing else. Taking the whole link
// would let a variable decide which host this worker sends its bearer token to; taking
// only the query keeps the address the worker's own.
func page(op string, r graphResponse) (any, error) {
	answer, err := decodeAnswer(op, r)
	if err != nil {
		return nil, err
	}
	m, _ := answer.(map[string]any)
	items, _ := m["value"].([]any)
	if items == nil {
		items = []any{}
	}
	for _, it := range items {
		if entry, ok := it.(map[string]any); ok {
			delete(entry, downloadURLKey)
		}
	}
	token := ""
	if next, _ := m["@odata.nextLink"].(string); next != "" {
		u, err := url.Parse(next)
		if err != nil || u.RawQuery == "" {
			return nil, fmt.Errorf("sharepoint: %s: Graph's link to the next page cannot be followed: %q", op, next)
		}
		token = u.RawQuery
	}
	return map[string]any{
		"items":         items,
		"count":         json.Number(strconv.Itoa(len(items))),
		"hasMore":       token != "",
		"nextPageToken": token,
	}, nil
}

// resumeAt replaces a listing's first-page query with the token an earlier page
// answered with, when the task carries one. The token already holds the filter, the
// selection and the page size it was asked with, so nothing is added to it. It is
// refused when it is not a query, because then it did not come from a page.
func resumeAt(op, first, token string) (string, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return first, nil
	}
	if _, err := url.ParseQuery(token); err != nil || !strings.Contains(token, "=") || strings.ContainsAny(token, "#?") {
		return "", fmt.Errorf("sharepoint: %s: pageToken %q is not one a page answered with; pass nextPageToken back unchanged", op, token)
	}
	return token, nil
}

// pageSize applies the default to a listing that was handed none — which only a job
// from a builder that skipped the compiler can be.
func pageSize(n int32) int32 {
	if n <= 0 {
		return DefaultMaxResults
	}
	return n
}

// graphFailure turns a non-2xx answer into the error a job fails with: the operation,
// the status, Graph's own error code and message, and — where one helps — what to do
// about it.
func graphFailure(op, method string, r graphResponse) error {
	msg := fmt.Sprintf("sharepoint: %s (%s) returned HTTP %d", op, method, r.status)
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	switch {
	case json.Unmarshal(r.body, &envelope) == nil && (envelope.Error.Code != "" || envelope.Error.Message != ""):
		msg += " (" + strings.TrimSpace(envelope.Error.Code+": "+envelope.Error.Message) + ")"
	case len(bytes.TrimSpace(r.body)) > 0:
		quote := bytes.TrimSpace(r.body)
		if int64(len(quote)) > limits.Default().ErrorBody {
			quote = quote[:limits.Default().ErrorBody]
		}
		msg += ": " + string(quote)
	}
	switch r.status {
	case http.StatusUnauthorized, http.StatusForbidden:
		msg += "; check that the app registration holds the Graph permission this needs — Sites.ReadWrite.All, or Sites.Selected with a grant on this site"
	case http.StatusTooManyRequests, http.StatusServiceUnavailable:
		if after := strings.TrimSpace(r.header.Get("Retry-After")); after != "" {
			msg += "; Graph is throttling and asks for " + after + "s before the next attempt"
		}
	}
	return errors.New(msg)
}

// siteBase answers the Graph address of the site a task names.
func (c *GraphClient) siteBase(ctx context.Context, op, raw string) (string, error) {
	id, err := c.siteID(ctx, op, raw)
	if err != nil {
		return "", err
	}
	return c.baseURL + "/sites/" + id, nil
}

// siteID answers the escaped Graph site id of a site reference. A Graph site id is used
// as it is; a URL, a host and path, or a bare host is looked up once and remembered,
// because Graph addresses everything beneath a site by its id.
func (c *GraphClient) siteID(ctx context.Context, op, raw string) (string, error) {
	ref := strings.TrimSpace(raw) // prepare has refused an empty one
	host, path, isID := parseSite(ref)
	if isID {
		return escapeSiteID(ref), nil
	}
	c.mu.Lock()
	id, known := c.sites[ref]
	c.mu.Unlock()
	if known {
		return id, nil
	}
	endpoint := c.baseURL + "/sites/" + url.PathEscape(host)
	if path != "" {
		endpoint += ":" + escapeSegments(path)
	}
	r, err := c.send(ctx, op, http.MethodGet, endpoint+"?$select=id", nil, "", "")
	if err != nil {
		return "", err
	}
	if r.status == http.StatusNotFound {
		return "", fmt.Errorf("sharepoint: %s: there is no site at %q — name the site itself (https://contoso.sharepoint.com/sites/hr), not a page or a list in it", op, ref)
	}
	if !r.ok() {
		return "", graphFailure(op+" (finding the site)", http.MethodGet, r)
	}
	var site struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(r.body, &site); err != nil || site.ID == "" {
		return "", fmt.Errorf("sharepoint: %s: looking up the site %q answered with no site id", op, ref)
	}
	id = escapeSiteID(site.ID)
	c.mu.Lock()
	if c.sites == nil || len(c.sites) >= siteCacheSize {
		c.sites = map[string]string{}
	}
	c.sites[ref] = id
	c.mu.Unlock()
	return id, nil
}

// parseSite reads a site reference: the site's URL, a host and server-relative path
// ("host:/sites/hr", or "host/sites/hr" as people also write it), a bare host for the
// tenant's root site, or a Graph site id "host,siteId,webId" — the one form used as it
// is. "host,/sites/path" is read as a host and a path: it is the form this package's
// documentation once gave, and it is not an id.
func parseSite(ref string) (host, path string, isID bool) {
	if strings.Contains(ref, "://") {
		if u, err := url.Parse(ref); err == nil && u.Host != "" {
			return u.Host, strings.TrimRight(u.Path, "/"), false
		}
	}
	if i := strings.Index(ref, ","); i >= 0 {
		if strings.HasPrefix(ref[i+1:], "/") {
			return ref[:i], strings.TrimRight(ref[i+1:], "/"), false
		}
		return "", "", true
	}
	if i := strings.Index(ref, ":/"); i >= 0 {
		return ref[:i], strings.TrimRight(ref[i+1:], "/"), false
	}
	if i := strings.Index(ref, "/"); i >= 0 {
		return ref[:i], strings.TrimRight(ref[i:], "/"), false
	}
	return ref, "", false
}

// escapeSiteID escapes each part of a composite site id and keeps the commas between
// them literal, as Graph's own examples write it.
func escapeSiteID(id string) string {
	parts := strings.Split(id, ",")
	for i, p := range parts {
		parts[i] = url.PathEscape(strings.TrimSpace(p))
	}
	return strings.Join(parts, ",")
}

// escapeSegments escapes each segment of a slash-separated path and keeps the slashes.
func escapeSegments(path string) string {
	segments := strings.Split(path, "/")
	for i, s := range segments {
		segments[i] = url.PathEscape(s)
	}
	return strings.Join(segments, "/")
}

// driveBase answers the Graph address of the document library a file operation acts
// in: the named library — which SharePoint keeps as a list, so it is addressed like one
// — or the site's default library when the task names none.
func driveBase(site, library string) string {
	library = strings.TrimSpace(library)
	if library == "" {
		return site + "/drive"
	}
	return site + "/lists/" + url.PathEscape(library) + "/drive"
}

// childrenOf answers the address of what a folder holds; the empty path is the
// library's top level.
func childrenOf(drive, escapedPath string) string {
	if escapedPath == "" {
		return drive + "/root/children"
	}
	return drive + "/root:/" + escapedPath + ":/children"
}

// cleanedPath is a file or folder path inside a library, as the task authored it with
// the slashes around it removed, and escaped for a URL.
type cleanedPath struct {
	clean   string
	escaped string
}

// name is the path's last segment: the file's or the folder's own name.
func (p cleanedPath) name() string {
	return p.clean[strings.LastIndex(p.clean, "/")+1:]
}

// cleanPath reads an authored path. A segment that is empty, "." or ".." is refused
// rather than resolved: a path is an address an author wrote, and one that climbs or
// skips is far more likely a FEEL expression that produced the wrong text than a place
// somebody meant.
func cleanPath(op, raw string, optional bool) (cleanedPath, error) {
	clean := strings.Trim(strings.TrimSpace(raw), "/")
	if clean == "" {
		if optional {
			return cleanedPath{}, nil
		}
		return cleanedPath{}, fmt.Errorf("sharepoint: %s has no path", op)
	}
	segments := strings.Split(clean, "/")
	for i, s := range segments {
		if s == "" || s == "." || s == ".." {
			return cleanedPath{}, fmt.Errorf("sharepoint: %s: path %q has an empty, \".\" or \"..\" segment; name the file from the library's top level", op, raw)
		}
		segments[i] = url.PathEscape(s)
	}
	return cleanedPath{clean: clean, escaped: strings.Join(segments, "/")}, nil
}

// expandFields is the $expand value that brings an item's column values along — all of
// them, or the ones a task selected.
func expandFields(sel string) string {
	var cols []string
	for _, c := range strings.Split(sel, ",") {
		if c = strings.TrimSpace(c); c != "" {
			cols = append(cols, c)
		}
	}
	if len(cols) == 0 {
		return "fields"
	}
	return "fields($select=" + strings.Join(cols, ",") + ")"
}

// odataEscape escapes a query value for Graph. It keeps the characters OData's own
// syntax is written in — $ ' ( ) , * / : @ = — literal, as Graph's documentation writes
// them, and escapes the rest, so a filter's spaces and an & inside a quoted value cannot
// end the parameter early.
func odataEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', ch >= '0' && ch <= '9',
			strings.IndexByte("-._~$'(),*/:@=", ch) >= 0:
			b.WriteByte(ch)
		default:
			fmt.Fprintf(&b, "%%%02X", ch)
		}
	}
	return b.String()
}

// decodeContent turns the model's authored content into the bytes to upload, and
// refuses anything past the cap before a request is built.
func decodeContent(op string, req Request) ([]byte, error) {
	raw := []byte(req.Content)
	if contentEncoding(req) == EncodingBase64 {
		// Whitespace is what a base64 value picks up on its way through a form, a mail
		// or a FEEL concatenation; rejecting it would fail a document that is intact.
		decoded, err := base64.StdEncoding.DecodeString(stripWhitespace(req.Content))
		if err != nil {
			return nil, fmt.Errorf("sharepoint: %s was told the content is base64, and it is not: %w", op, err)
		}
		raw = decoded
	}
	if int64(len(raw)) > MaxContentBytes() {
		return nil, fmt.Errorf("sharepoint: %s was given %d bytes and the limit is %d — a process variable holds no more", op, len(raw), MaxContentBytes())
	}
	return raw, nil
}

// encodeContent renders downloaded bytes as the task asked for them. Text that is not
// valid UTF-8 is refused rather than passed on: it would land in the variable with
// every invalid byte replaced, which is a document that looks intact and is not.
func encodeContent(op, path string, req Request, raw []byte) (string, error) {
	if contentEncoding(req) == EncodingBase64 {
		return base64.StdEncoding.EncodeToString(raw), nil
	}
	if !utf8.Valid(raw) {
		return "", fmt.Errorf("sharepoint: %s: %q is not text; download it with encoding base64", op, path)
	}
	return string(raw), nil
}

// contentEncoding reads the task's encoding, treating anything unset as text. The
// compiler has already applied the default and refused an unknown value.
func contentEncoding(req Request) string {
	if strings.EqualFold(strings.TrimSpace(req.Encoding), EncodingBase64) {
		return EncodingBase64
	}
	return EncodingText
}

// stripWhitespace removes the line breaks and spaces a base64 value collects in transit.
func stripWhitespace(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\n', '\r':
			return -1
		}
		return r
	}, s)
}
