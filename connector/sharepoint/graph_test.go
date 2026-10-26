package sharepoint

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubTokens is a TokenSource that returns a fixed token (or an error).
type stubTokens struct {
	tok string
	err error
}

func (s stubTokens) Token(context.Context) (string, error) { return s.tok, s.err }

// siteID is the Graph site id the fake answers every site lookup with.
const siteID = "contoso.sharepoint.com,1111,2222"

// seen is one request the fake Graph received.
type seen struct {
	method string
	path   string // escaped, as it went over the wire
	query  string
	header http.Header
	body   []byte
}

// fakeGraph is a Microsoft Graph endpoint that records every request and answers it
// with whatever the test's route function says.
type fakeGraph struct {
	srv      *httptest.Server
	requests []seen
	route    func(w http.ResponseWriter, r *http.Request, body []byte)
}

func newFakeGraph(t *testing.T, route func(w http.ResponseWriter, r *http.Request, body []byte)) (*fakeGraph, *GraphClient) {
	t.Helper()
	fg := &fakeGraph{route: route}
	fg.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		fg.requests = append(fg.requests, seen{method: r.Method, path: r.URL.EscapedPath(), query: r.URL.RawQuery, header: r.Header.Clone(), body: body})
		fg.route(w, r, body)
	}))
	t.Cleanup(fg.srv.Close)
	return fg, NewGraphClient(stubTokens{tok: "abc"}, fg.srv.URL)
}

// last is the most recent request.
func (fg *fakeGraph) last(t *testing.T) seen {
	t.Helper()
	if len(fg.requests) == 0 {
		t.Fatal("Graph received no request")
	}
	return fg.requests[len(fg.requests)-1]
}

func answer(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func jsonBody(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var m map[string]any
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("request body %q is not a JSON object: %v", raw, err)
	}
	return m
}

func do(t *testing.T, c *GraphClient, req Request) map[string]any {
	t.Helper()
	if req.Site == "" {
		req.Site = siteID
	}
	got, err := c.Do(context.Background(), req)
	if err != nil {
		t.Fatalf("%s: %v", req.Operation, err)
	}
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("%s answered %#v, want an object", req.Operation, got)
	}
	return m
}

// A Graph site id is used as it is, with its commas literal, as Graph's own examples
// write it. Create-item posts the columns under "fields", keeping each value's shape,
// and is what a request naming no operation does.
func TestCreateItem(t *testing.T) {
	fg, c := newFakeGraph(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		answer(w, http.StatusCreated, `{"id":"42","webUrl":"https://x","fields":{"Title":"Order shipped"}}`)
	})
	got := do(t, c, Request{List: "Incidents", Fields: map[string]any{
		"Title": "Order shipped", "Amount": json.Number("12.5"), "Urgent": true, "Tags": []any{"a", "b"},
	}})
	req := fg.last(t)
	if req.method != http.MethodPost || req.path != "/sites/"+siteID+"/lists/Incidents/items" {
		t.Errorf("request = %s %s, want POST to the list's items with the site id's commas literal", req.method, req.path)
	}
	if req.header.Get("Authorization") != "Bearer abc" {
		t.Errorf("Authorization = %q, want the bearer token", req.header.Get("Authorization"))
	}
	fields, _ := jsonBody(t, req.body)["fields"].(map[string]any)
	if fields["Title"] != "Order shipped" || fields["Amount"] != json.Number("12.5") || fields["Urgent"] != true {
		t.Errorf("fields = %#v, want each value in its own JSON shape", fields)
	}
	if tags, _ := fields["Tags"].([]any); len(tags) != 2 {
		t.Errorf("Tags = %#v, want the list a multi-choice column reads", fields["Tags"])
	}
	if got["id"] != "42" {
		t.Errorf("answer = %#v, want the created item", got)
	}
}

// A site named the way a person copies it — its URL, a host and path, the form this
// package's documentation once gave, or a bare host for the root site — is looked up
// once, and the id Graph answers with addresses everything after.
func TestSiteAddressing(t *testing.T) {
	for _, tc := range []struct {
		site, lookup string
	}{
		{"https://contoso.sharepoint.com/sites/hr/", "/sites/contoso.sharepoint.com:/sites/hr"},
		{"contoso.sharepoint.com:/sites/hr", "/sites/contoso.sharepoint.com:/sites/hr"},
		{"contoso.sharepoint.com/sites/Human Resources", "/sites/contoso.sharepoint.com:/sites/Human%20Resources"},
		{"contoso.sharepoint.com,/sites/ops", "/sites/contoso.sharepoint.com:/sites/ops"},
		{"contoso.sharepoint.com", "/sites/contoso.sharepoint.com"},
	} {
		t.Run(tc.site, func(t *testing.T) {
			fg, c := newFakeGraph(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
				if r.URL.RawQuery == "$select=id" {
					answer(w, http.StatusOK, `{"id":"`+siteID+`"}`)
					return
				}
				answer(w, http.StatusOK, `{"id":"7"}`)
			})
			for range 2 {
				do(t, c, Request{Operation: "get-item", Site: tc.site, List: "L", ItemID: "7"})
			}
			if len(fg.requests) != 3 {
				t.Fatalf("requests = %d, want one lookup and two reads (the id remembered)", len(fg.requests))
			}
			if fg.requests[0].path != tc.lookup {
				t.Errorf("lookup = %s, want %s", fg.requests[0].path, tc.lookup)
			}
			if want := "/sites/" + siteID + "/lists/L/items/7"; fg.requests[2].path != want {
				t.Errorf("read = %s, want %s", fg.requests[2].path, want)
			}
		})
	}
}

// A site that is not there says how a site is named, because the usual cause is a page
// or a list URL pasted where the site's was meant.
func TestSiteNotFound(t *testing.T) {
	_, c := newFakeGraph(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		answer(w, http.StatusNotFound, `{"error":{"code":"itemNotFound","message":"Requested site could not be found"}}`)
	})
	_, err := c.Do(context.Background(), Request{Operation: "get-item", Site: "https://contoso.sharepoint.com/sites/hr/Lists/X/AllItems.aspx", List: "L", ItemID: "1"})
	if err == nil || !strings.Contains(err.Error(), "no site at") || !strings.Contains(err.Error(), "not a page or a list") {
		t.Fatalf("err = %v, want it to say how a site is named", err)
	}
}

func TestGetItem(t *testing.T) {
	fg, c := newFakeGraph(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		answer(w, http.StatusOK, `{"id":"7","fields":{"Title":"x"}}`)
	})
	do(t, c, Request{Operation: "get-item", List: "Auftraege", ItemID: "7", Select: "Title, Status"})
	req := fg.last(t)
	if req.method != http.MethodGet || req.path != "/sites/"+siteID+"/lists/Auftraege/items/7" {
		t.Errorf("request = %s %s", req.method, req.path)
	}
	if req.query != "$expand=fields($select=Title,Status)" {
		t.Errorf("query = %q, want the selected columns expanded", req.query)
	}
	do(t, c, Request{Operation: "get-item", List: "Auftraege", ItemID: "7"})
	if q := fg.last(t).query; q != "$expand=fields" {
		t.Errorf("query = %q, want every column", q)
	}
}

func TestUpdateItem(t *testing.T) {
	fg, c := newFakeGraph(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		answer(w, http.StatusOK, `{"Status":"Erledigt"}`)
	})
	got := do(t, c, Request{Operation: "update-item", List: "Auftraege", ItemID: "7", Fields: map[string]any{"Status": "Erledigt", "Kosten": nil}})
	req := fg.last(t)
	if req.method != http.MethodPatch || req.path != "/sites/"+siteID+"/lists/Auftraege/items/7/fields" {
		t.Errorf("request = %s %s, want PATCH to the item's fields", req.method, req.path)
	}
	body := jsonBody(t, req.body)
	if body["Status"] != "Erledigt" {
		t.Errorf("body = %#v, want the columns themselves, not wrapped", body)
	}
	if v, present := body["Kosten"]; !present || v != nil {
		t.Errorf("Kosten = %#v (present %v), want null — which clears the column", v, present)
	}
	if got["Status"] != "Erledigt" {
		t.Errorf("answer = %#v", got)
	}
}

// A delete that finds nothing answers deleted=false rather than failing: a retry after a
// delete that succeeded finds exactly that.
func TestDeleteIsSafeToRepeat(t *testing.T) {
	status := http.StatusNoContent
	fg, c := newFakeGraph(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) { w.WriteHeader(status) })
	if got := do(t, c, Request{Operation: "delete-item", List: "L", ItemID: "7"}); got["deleted"] != true {
		t.Errorf("first delete = %#v, want deleted=true", got)
	}
	if req := fg.last(t); req.method != http.MethodDelete || req.path != "/sites/"+siteID+"/lists/L/items/7" {
		t.Errorf("request = %s %s", req.method, req.path)
	}
	status = http.StatusNotFound
	if got := do(t, c, Request{Operation: "delete-item", List: "L", ItemID: "7"}); got["deleted"] != false {
		t.Errorf("repeated delete = %#v, want deleted=false", got)
	}
	status = http.StatusNoContent
	got := do(t, c, Request{Operation: "delete-file", Path: "/Eingang/scan.pdf/"})
	if req := fg.last(t); req.path != "/sites/"+siteID+"/drive/root:/Eingang/scan.pdf" {
		t.Errorf("delete-file path = %s", req.path)
	}
	if got["deleted"] != true || got["path"] != "Eingang/scan.pdf" {
		t.Errorf("delete-file = %#v", got)
	}
	status = http.StatusForbidden
	if _, err := c.Do(context.Background(), Request{Operation: "delete-item", Site: siteID, List: "L", ItemID: "7"}); err == nil {
		t.Error("a refused delete completed")
	}
}

// A listing pages through Graph's nextLink, of which only the query travels: taking the
// whole link would let a variable choose where this worker sends its token.
func TestListItemsPages(t *testing.T) {
	fg, c := newFakeGraph(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		if strings.Contains(r.URL.RawQuery, "skiptoken") {
			answer(w, http.StatusOK, `{"value":[{"id":"3"}]}`)
			return
		}
		answer(w, http.StatusOK, `{"value":[{"id":"1"},{"id":"2"}],"@odata.nextLink":"https://elsewhere.example/v1.0/sites/x/lists/L/items?$skiptoken=Paged%3dTRUE%26p_ID%3d2&$top=2"}`)
	})
	first := do(t, c, Request{Operation: "list-items", List: "Auftraege", Filter: "fields/Status eq 'Offen & neu'", Select: "Title", MaxResults: 2})
	req := fg.last(t)
	if want := "$expand=fields($select=Title)&$top=2&$filter=fields/Status%20eq%20'Offen%20%26%20neu'"; req.query != want {
		t.Errorf("query = %q, want %q", req.query, want)
	}
	if req.header.Get("Prefer") != honorNonIndexed {
		t.Errorf("Prefer = %q, want the header that lets a filter reach an unindexed column", req.header.Get("Prefer"))
	}
	if first["hasMore"] != true || first["count"] != json.Number("2") {
		t.Errorf("first page = %#v", first)
	}
	token, _ := first["nextPageToken"].(string)
	if token != "$skiptoken=Paged%3dTRUE%26p_ID%3d2&$top=2" {
		t.Fatalf("nextPageToken = %q, want the nextLink's query alone", token)
	}
	second := do(t, c, Request{Operation: "list-items", List: "Auftraege", PageToken: token, MaxResults: 2})
	req = fg.last(t)
	if req.header.Get("Prefer") != "" {
		t.Errorf("Prefer = %q on a page whose token carries no filter", req.header.Get("Prefer"))
	}
	do(t, c, Request{Operation: "list-items", List: "Auftraege", PageToken: "%24filter=fields%2FStatus%20eq%20'x'&%24skiptoken=Paged%3dTRUE"})
	if fg.last(t).header.Get("Prefer") != honorNonIndexed {
		t.Error("a page whose token carries the filter was sent without the header that lets it run")
	}
	if !strings.HasPrefix(req.path, "/sites/"+siteID+"/lists/Auftraege/items") || req.query != token {
		t.Errorf("second page = %s?%s, want this worker's own address with the token's query", req.path, req.query)
	}
	if second["hasMore"] != false || second["nextPageToken"] != "" {
		t.Errorf("last page = %#v, want no more", second)
	}
	if _, err := c.Do(context.Background(), Request{Operation: "list-items", Site: siteID, List: "L", PageToken: "https://evil.example/#x"}); err == nil {
		t.Error("a page token that is not a query was followed")
	}
}

// An upload writes the bytes to the path, sets the columns given as the file's
// properties, and answers with the file — without the pre-authenticated download link
// Graph puts on it.
func TestUploadFile(t *testing.T) {
	fg, c := newFakeGraph(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		if r.Method == http.MethodPut {
			answer(w, http.StatusCreated, `{"id":"F1","name":"vertrag 1.pdf","webUrl":"https://x","@microsoft.graph.downloadUrl":"https://secret"}`)
			return
		}
		answer(w, http.StatusOK, `{"Kunde":"Ada"}`)
	})
	got := do(t, c, Request{Operation: "upload-file", List: "Verträge", Path: "2026/vertrag 1.pdf",
		Content: base64.StdEncoding.EncodeToString([]byte("%PDF-1.7")), Encoding: EncodingBase64,
		Fields: map[string]any{"Kunde": "Ada"}})
	put := fg.requests[0]
	if put.method != http.MethodPut || put.path != "/sites/"+siteID+"/lists/Vertr%C3%A4ge/drive/root:/2026/vertrag%201.pdf:/content" {
		t.Errorf("upload = %s %s, want PUT to the named library's path", put.method, put.path)
	}
	if string(put.body) != "%PDF-1.7" {
		t.Errorf("uploaded %q, want the decoded bytes", put.body)
	}
	patch := fg.requests[1]
	if patch.method != http.MethodPatch || patch.path != "/sites/"+siteID+"/lists/Vertr%C3%A4ge/drive/items/F1/listItem/fields" {
		t.Errorf("properties = %s %s, want PATCH to the file's list item", patch.method, patch.path)
	}
	if _, leaked := got[downloadURLKey]; leaked {
		t.Error("the pre-authenticated download link reached the answer")
	}
	li, _ := got["listItem"].(map[string]any)
	if f, _ := li["fields"].(map[string]any); f["Kunde"] != "Ada" {
		t.Errorf("answer = %#v, want the properties under listItem.fields", got)
	}

	// Without properties it is one call, into the site's default library.
	fg.requests = nil
	do(t, c, Request{Operation: "upload-file", Path: "notiz.txt", Content: "Hallo"})
	if len(fg.requests) != 1 || fg.requests[0].path != "/sites/"+siteID+"/drive/root:/notiz.txt:/content" || string(fg.requests[0].body) != "Hallo" {
		t.Errorf("plain upload = %+v", fg.requests)
	}
	if _, err := c.Do(context.Background(), Request{Operation: "upload-file", Site: siteID, Path: "x", Content: "not base64!", Encoding: EncodingBase64}); err == nil {
		t.Error("content that is not base64 was uploaded")
	}
}

func TestDownloadFile(t *testing.T) {
	body := []byte("Grüezi")
	fg, c := newFakeGraph(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write(body)
	})
	got := do(t, c, Request{Operation: "download-file", Path: "a/brief.txt"})
	if req := fg.last(t); req.path != "/sites/"+siteID+"/drive/root:/a/brief.txt:/content" {
		t.Errorf("path = %s", req.path)
	}
	if got["content"] != "Grüezi" || got["name"] != "brief.txt" || got["contentType"] != "text/plain" || got["size"] != json.Number("7") {
		t.Errorf("answer = %#v", got)
	}
	body = []byte{0xff, 0xfe, 0x00}
	if _, err := c.Do(context.Background(), Request{Operation: "download-file", Site: siteID, Path: "a.bin"}); err == nil || !strings.Contains(err.Error(), "base64") {
		t.Errorf("err = %v, want binary content refused as text with base64 named", err)
	}
	got = do(t, c, Request{Operation: "download-file", Path: "a.bin", Encoding: EncodingBase64})
	if got["content"] != base64.StdEncoding.EncodeToString(body) {
		t.Errorf("base64 content = %v", got["content"])
	}
	body = make([]byte, MaxContentBytes()+1)
	if _, err := c.Do(context.Background(), Request{Operation: "download-file", Site: siteID, Path: "gross.bin", Encoding: EncodingBase64}); err == nil || !strings.Contains(err.Error(), "webUrl") {
		t.Errorf("err = %v, want a file past the variable budget refused, pointing at its webUrl", err)
	}
}

func TestGetFile(t *testing.T) {
	status := http.StatusOK
	fg, c := newFakeGraph(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		answer(w, status, `{"id":"F1","name":"a.pdf","@microsoft.graph.downloadUrl":"https://secret","listItem":{"fields":{"Kunde":"Ada"}}}`)
	})
	got := do(t, c, Request{Operation: "get-file", Path: "a.pdf"})
	req := fg.last(t)
	if req.path != "/sites/"+siteID+"/drive/root:/a.pdf" || req.query != "$expand=listItem($expand=fields)" {
		t.Errorf("request = %s?%s, want the file with its library columns", req.path, req.query)
	}
	if got["exists"] != true || got[downloadURLKey] != nil {
		t.Errorf("answer = %#v, want exists and no download link", got)
	}
	status = http.StatusNotFound
	if got := do(t, c, Request{Operation: "get-file", Path: "a.pdf"}); got["exists"] != false || got["path"] != "a.pdf" {
		t.Errorf("missing file = %#v, want exists=false", got)
	}
}

func TestListFolder(t *testing.T) {
	fg, c := newFakeGraph(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		answer(w, http.StatusOK, `{"value":[{"id":"1","name":"a.pdf","@microsoft.graph.downloadUrl":"https://secret"}]}`)
	})
	got := do(t, c, Request{Operation: "list-folder"})
	if req := fg.last(t); req.path != "/sites/"+siteID+"/drive/root/children" || req.query != "$top=100" {
		t.Errorf("top level = %s?%s, want the root's children at the default page size", req.path, req.query)
	}
	items, _ := got["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)[downloadURLKey] != nil {
		t.Errorf("items = %#v, want the entry without its download link", items)
	}
	do(t, c, Request{Operation: "list-folder", List: "Akten", Path: "2026/Q1", MaxResults: 5})
	if req := fg.last(t); req.path != "/sites/"+siteID+"/lists/Akten/drive/root:/2026/Q1:/children" || req.query != "$top=5" {
		t.Errorf("folder = %s?%s", req.path, req.query)
	}
}

// create-folder makes every missing level and counts one that is there as made, so a
// retry after a create that succeeded answers with the folder instead of failing.
func TestCreateFolder(t *testing.T) {
	exists := map[string]bool{"Akten": true}
	fg, c := newFakeGraph(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
		if r.Method == http.MethodGet {
			answer(w, http.StatusOK, `{"id":"D2","name":"4711","folder":{"childCount":0}}`)
			return
		}
		var req struct {
			Name     string `json:"name"`
			Conflict string `json:"@microsoft.graph.conflictBehavior"`
		}
		_ = json.Unmarshal(body, &req)
		if req.Conflict != "fail" {
			answer(w, http.StatusBadRequest, `{}`)
			return
		}
		if exists[req.Name] {
			answer(w, http.StatusConflict, `{"error":{"code":"nameAlreadyExists"}}`)
			return
		}
		exists[req.Name] = true
		answer(w, http.StatusCreated, `{"id":"D2","name":"`+req.Name+`","folder":{"childCount":0}}`)
	})
	got := do(t, c, Request{Operation: "create-folder", Path: "Akten/4711"})
	if fg.requests[0].path != "/sites/"+siteID+"/drive/root/children" || fg.requests[1].path != "/sites/"+siteID+"/drive/root:/Akten:/children" {
		t.Errorf("requests = %s then %s, want each level created inside the one above", fg.requests[0].path, fg.requests[1].path)
	}
	if got["created"] != true || got["name"] != "4711" {
		t.Errorf("first create = %#v", got)
	}
	got = do(t, c, Request{Operation: "create-folder", Path: "Akten/4711"})
	if got["created"] != false || got["id"] != "D2" {
		t.Errorf("repeated create = %#v, want the existing folder, created=false", got)
	}
}

func TestCreateFolderOverAFile(t *testing.T) {
	_, c := newFakeGraph(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		if r.Method == http.MethodGet {
			answer(w, http.StatusOK, `{"id":"F","name":"x","file":{}}`)
			return
		}
		answer(w, http.StatusConflict, `{}`)
	})
	if _, err := c.Do(context.Background(), Request{Operation: "create-folder", Site: siteID, Path: "x"}); err == nil || !strings.Contains(err.Error(), "is a file") {
		t.Errorf("err = %v, want a file in the folder's place reported", err)
	}
}

// Graph's error envelope reaches the message, with what to do where there is something
// to do: a permission to grant, or the wait Graph asks for when it throttles.
func TestGraphFailures(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		header string
		want   []string
	}{
		{http.StatusForbidden, `{"error":{"code":"accessDenied","message":"Access denied"}}`, "", []string{"HTTP 403", "accessDenied: Access denied", "Sites.Selected"}},
		{http.StatusTooManyRequests, `{"error":{"code":"activityLimitReached"}}`, "17", []string{"HTTP 429", "asks for 17s"}},
		{http.StatusBadGateway, `upstream exploded`, "", []string{"HTTP 502", "upstream exploded"}},
	} {
		_, c := newFakeGraph(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) {
			if tc.header != "" {
				w.Header().Set("Retry-After", tc.header)
			}
			answer(w, tc.status, tc.body)
		})
		_, err := c.Do(context.Background(), Request{Operation: "get-item", Site: siteID, List: "L", ItemID: "1"})
		for _, want := range tc.want {
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("HTTP %d: err = %v, want it to contain %q", tc.status, err, want)
			}
		}
	}
}

// What a request is missing is refused before a call is made; the compiler has refused
// most of it already, so these are a FEEL value that resolved to nothing.
func TestRefusalsBeforeTheCall(t *testing.T) {
	fg, c := newFakeGraph(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) { answer(w, http.StatusOK, `{}`) })
	// A site named by its URL needs a lookup; none is made for a request that is
	// refused anyway.
	const byURL = "https://contoso.sharepoint.com/sites/hr"
	for name, req := range map[string]Request{
		"no site":         {Operation: "get-item", List: "L", ItemID: "1"},
		"no list":         {Operation: "create-item", Site: byURL},
		"no item id":      {Operation: "update-item", Site: byURL, List: "L"},
		"no path":         {Operation: "download-file", Site: byURL},
		"climbing path":   {Operation: "get-file", Site: byURL, Path: "a/../b"},
		"empty segment":   {Operation: "get-file", Site: byURL, Path: "a//b"},
		"unknown op":      {Operation: "share-file", Site: siteID},
		"content too big": {Operation: "upload-file", Site: siteID, Path: "a", Content: strings.Repeat("x", int(MaxContentBytes())+1)},
	} {
		if _, err := c.Do(context.Background(), req); err == nil {
			t.Errorf("%s: want a refusal", name)
		}
	}
	if len(fg.requests) != 0 {
		t.Errorf("Graph received %d requests for jobs that were refused", len(fg.requests))
	}
}

// An answer too large for a process variable is refused with what to change, rather
// than failing later at the variable.
func TestAnswerPastTheVariableBudget(t *testing.T) {
	_, c := newFakeGraph(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		answer(w, http.StatusOK, `{"value":["`+strings.Repeat("x", int(MaxContentBytes()))+`"]}`)
	})
	_, err := c.Do(context.Background(), Request{Operation: "list-items", Site: siteID, List: "L"})
	if err == nil || !strings.Contains(err.Error(), "maxResults") {
		t.Errorf("err = %v, want the oversized page refused with what to narrow", err)
	}
}

func TestTokenFailureMakesNoCall(t *testing.T) {
	c := NewGraphClient(stubTokens{err: context.DeadlineExceeded}, "http://127.0.0.1:0")
	if _, err := c.Do(context.Background(), Request{Site: siteID, List: "l"}); err == nil {
		t.Error("want an error when the token cannot be acquired")
	}
}

func TestGraphClientDefaultsBase(t *testing.T) {
	c := NewGraphClient(stubTokens{tok: "x"}, "")
	if c.baseURL != graphDefaultBase {
		t.Errorf("baseURL = %q, want the Graph default %q", c.baseURL, graphDefaultBase)
	}
}

// Create-item keeps its old answers: an empty body is no item, and a malformed one is
// an error rather than a variable holding nonsense.
func TestCreateItemAnswerShapes(t *testing.T) {
	body := ""
	_, c := newFakeGraph(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) { answer(w, http.StatusCreated, body) })
	if got, err := c.Do(context.Background(), Request{Site: siteID, List: "l"}); err != nil || got != nil {
		t.Errorf("empty body: got=%v err=%v, want nil,nil", got, err)
	}
	body = "not json"
	if _, err := c.Do(context.Background(), Request{Site: siteID, List: "l"}); err == nil {
		t.Error("want an error for a malformed Graph response body")
	}
	c = NewGraphClient(stubTokens{tok: "x"}, "http://127.0.0.1:0")
	if _, err := c.Do(context.Background(), Request{Site: siteID, List: "l"}); err == nil {
		t.Error("want an error for an unreachable Graph endpoint")
	}
}

// everyOperation is one well-formed request per operation, so a failure that every
// operation must treat the same way is checked against all of them.
var everyOperation = []Request{
	{Operation: "create-item", Site: siteID, List: "L", Fields: map[string]any{"Title": "x"}},
	{Operation: "get-item", Site: siteID, List: "L", ItemID: "1"},
	{Operation: "update-item", Site: siteID, List: "L", ItemID: "1", Fields: map[string]any{"Title": "x"}},
	{Operation: "delete-item", Site: siteID, List: "L", ItemID: "1"},
	{Operation: "list-items", Site: siteID, List: "L"},
	{Operation: "upload-file", Site: siteID, Path: "a.txt", Content: "x", Fields: map[string]any{"K": "v"}},
	{Operation: "download-file", Site: siteID, Path: "a.txt"},
	{Operation: "get-file", Site: siteID, Path: "a.txt"},
	{Operation: "list-folder", Site: siteID, Path: "a"},
	{Operation: "create-folder", Site: siteID, Path: "a"},
	{Operation: "delete-file", Site: siteID, Path: "a.txt"},
}

// TestEveryOperationIsCovered keeps the table above whole: an operation added to Ops
// and not to it would skip the failure checks below.
func TestEveryOperationIsCovered(t *testing.T) {
	if len(everyOperation) != len(Ops) {
		t.Fatalf("everyOperation has %d requests and Ops %d operations", len(everyOperation), len(Ops))
	}
}

// A server error fails every operation — none of them completes a token on work Graph
// refused — and the message names the operation that failed.
func TestEveryOperationFailsOnAServerError(t *testing.T) {
	_, c := newFakeGraph(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) {
		answer(w, http.StatusInternalServerError, `{"error":{"code":"generalException"}}`)
	})
	for _, req := range everyOperation {
		if _, err := c.Do(context.Background(), req); err == nil || !strings.Contains(err.Error(), req.Operation) {
			t.Errorf("%s: err = %v, want a failure naming the operation", req.Operation, err)
		}
	}
}

// An unreachable Graph fails every operation as a transport error.
func TestEveryOperationFailsWhenGraphIsUnreachable(t *testing.T) {
	c := NewGraphClient(stubTokens{tok: "x"}, "http://127.0.0.1:0")
	for _, req := range everyOperation {
		if _, err := c.Do(context.Background(), req); err == nil {
			t.Errorf("%s: completed against an unreachable Graph", req.Operation)
		}
	}
}

// An answer that is not JSON fails every operation that reads one, rather than landing
// in a variable as nonsense.
func TestEveryOperationRefusesAMalformedAnswer(t *testing.T) {
	_, c := newFakeGraph(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) { answer(w, http.StatusOK, `{"broken`) })
	for _, req := range everyOperation {
		switch req.Operation {
		case "delete-item", "delete-file", "download-file":
			continue // they read no JSON
		}
		if _, err := c.Do(context.Background(), req); err == nil {
			t.Errorf("%s: completed on a malformed answer", req.Operation)
		}
	}
}

// The second call an operation makes fails it too: the properties an upload sets, and
// the read create-folder makes of a folder that was already there.
func TestSecondCallFailures(t *testing.T) {
	_, c := newFakeGraph(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		switch r.Method {
		case http.MethodPut:
			answer(w, http.StatusCreated, `{"id":"F1"}`)
		case http.MethodPost:
			answer(w, http.StatusConflict, `{}`)
		default:
			answer(w, http.StatusForbidden, `{"error":{"code":"accessDenied"}}`)
		}
	})
	_, err := c.Do(context.Background(), Request{Operation: "upload-file", Site: siteID, Path: "a", Content: "x", Fields: map[string]any{"K": "v"}})
	if err == nil || !strings.Contains(err.Error(), "the file itself was stored") {
		t.Errorf("upload: err = %v, want the failed properties reported with the file stored", err)
	}
	if _, err := c.Do(context.Background(), Request{Operation: "create-folder", Site: siteID, Path: "a"}); err == nil {
		t.Error("create-folder completed although reading the existing folder failed")
	}
}

// Answers that are JSON but not what the operation needs are refused rather than passed
// on: an upload with no file, or one with no id to set properties through.
func TestUnexpectedAnswers(t *testing.T) {
	body := `null`
	_, c := newFakeGraph(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) { answer(w, http.StatusOK, body) })
	for _, req := range []Request{
		{Operation: "upload-file", Site: siteID, Path: "a", Content: "x"},
		{Operation: "get-file", Site: siteID, Path: "a"},
		{Operation: "create-folder", Site: siteID, Path: "a"},
	} {
		if _, err := c.Do(context.Background(), req); err == nil {
			t.Errorf("%s: completed on an answer with no file", req.Operation)
		}
	}
	body = `{"name":"a"}`
	if _, err := c.Do(context.Background(), Request{Operation: "upload-file", Site: siteID, Path: "a", Content: "x", Fields: map[string]any{"K": "v"}}); err == nil {
		t.Error("upload set properties through a file with no id")
	}
	body = `{"value":[],"@odata.nextLink":"https://graph.example/v1.0/items"}`
	if _, err := c.Do(context.Background(), Request{Operation: "list-folder", Site: siteID}); err == nil {
		t.Error("a next link with no query was taken for a page token")
	}
}

// A site lookup that fails for any reason but "not there", or answers with no id,
// fails the job before anything else is attempted.
func TestSiteLookupFailures(t *testing.T) {
	body, status := `{"error":{"code":"accessDenied"}}`, http.StatusForbidden
	_, c := newFakeGraph(t, func(w http.ResponseWriter, _ *http.Request, _ []byte) { answer(w, status, body) })
	req := Request{Operation: "get-item", Site: "contoso.sharepoint.com:/sites/hr", List: "L", ItemID: "1"}
	if _, err := c.Do(context.Background(), req); err == nil || !strings.Contains(err.Error(), "finding the site") {
		t.Errorf("err = %v, want the lookup named", err)
	}
	body, status = `{}`, http.StatusOK
	if _, err := c.Do(context.Background(), req); err == nil || !strings.Contains(err.Error(), "no site id") {
		t.Errorf("err = %v, want an answer without an id refused", err)
	}
	c = NewGraphClient(stubTokens{tok: "x"}, "http://127.0.0.1:0")
	if _, err := c.Do(context.Background(), req); err == nil {
		t.Error("a site was resolved against an unreachable Graph")
	}
}

// The cache of resolved sites is bounded: past its size it starts over rather than
// growing with every site a FEEL expression can compute.
func TestSiteCacheIsBounded(t *testing.T) {
	_, c := newFakeGraph(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		if r.URL.RawQuery == "$select=id" {
			answer(w, http.StatusOK, `{"id":"`+siteID+`"}`)
			return
		}
		answer(w, http.StatusOK, `{}`)
	})
	for i := range siteCacheSize + 1 {
		do(t, c, Request{Operation: "get-item", Site: "contoso.sharepoint.com:/sites/s" + strings.Repeat("x", i), List: "L", ItemID: "1"})
	}
	if n := len(c.sites); n != 1 {
		t.Errorf("cache holds %d sites after passing its size, want it started over", n)
	}
}
