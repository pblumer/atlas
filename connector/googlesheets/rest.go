package googlesheets

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/pblumer/atlas/connector/nettimeout"
)

// The two API bases this connector speaks. Sheets owns cells; Drive owns the file the
// cells live in — which is why creating one in a folder and trashing one are Drive
// calls even though the object is a spreadsheet
// (ADR-draft-google-sheets-worker).
const (
	sheetsDefaultBase = "https://sheets.googleapis.com"
	driveDefaultBase  = "https://www.googleapis.com"
)

// driveFileFields is what a Drive call is asked to return. Drive answers with the id
// alone unless asked, and the link is the field a process most often wants to put in a
// mail or a task description.
const driveFileFields = "id,name,parents,webViewLink,mimeType"

// Connector is the server-side configuration of one Google account: a token source and
// the two API bases (empty means Google's own, overridden only by tests and by an
// operator behind a proxy). The credential itself lives behind Tokens and is never
// persisted (I6).
type Connector struct {
	Tokens     TokenSource
	SheetsBase string
	DriveBase  string
}

// HTTPClient calls the real Sheets and Drive APIs. It is stateless beyond its cached
// token source, so it is safe for concurrent use by the worker.
type HTTPClient struct {
	conn Connector
	http *http.Client
}

// NewHTTPClient builds a Google client for a configured connector, bounded by the
// shared connector call budget (ADR-0149). The worker may run on the run-loop
// goroutine, so an unbounded call would let a hung Google stall the whole engine; see
// the nettimeout package doc.
func NewHTTPClient(conn Connector) *HTTPClient {
	conn.SheetsBase = base(conn.SheetsBase, sheetsDefaultBase)
	conn.DriveBase = base(conn.DriveBase, driveDefaultBase)
	return &HTTPClient{conn: conn, http: nettimeout.HTTPClient()}
}

func base(configured, dflt string) string {
	if strings.TrimSpace(configured) == "" {
		return dflt
	}
	return strings.TrimRight(strings.TrimSpace(configured), "/")
}

// Do performs one operation. Every failure — a transport error, a non-2xx status, an
// operation nothing implements — is returned so the job stays pending and is retried,
// then raises an incident (ADR-0061), rather than completing a token on work that did
// not happen.
func (c *HTTPClient) Do(ctx context.Context, req Request) (any, error) {
	req.Spreadsheet = SpreadsheetID(req.Spreadsheet)
	req.Folder = FolderID(req.Folder)
	switch req.Operation {
	case "create-spreadsheet":
		return c.createSpreadsheet(ctx, req)
	case "add-sheet":
		return c.batchUpdate(ctx, req, map[string]any{
			"addSheet": map[string]any{"properties": map[string]any{"title": req.Sheet}},
		})
	case "read-range":
		return c.readRange(ctx, req)
	case "write-range":
		return c.call(ctx, http.MethodPut, c.valuesURL(req, "", url.Values{
			"valueInputOption": {valueInputOption(req.Input)},
		}), map[string]any{"values": req.Values}, req)
	case "append-row":
		return c.call(ctx, http.MethodPost, c.valuesURL(req, ":append", url.Values{
			"valueInputOption": {valueInputOption(req.Input)},
			// Without this Sheets overwrites whatever sits below the table instead of
			// making room, which is not what "append" means anywhere else.
			"insertDataOption": {"INSERT_ROWS"},
		}), map[string]any{"values": req.Values}, req)
	case "clear-range":
		return c.call(ctx, http.MethodPost, c.valuesURL(req, ":clear", nil), map[string]any{}, req)
	case "delete-sheet":
		return c.deleteSheet(ctx, req)
	case "delete-spreadsheet":
		// A trash, not a purge: files.delete is permanent and the mistake is
		// unrecoverable, where an owner can restore what a process trashed.
		return c.call(ctx, http.MethodPatch, c.driveURL(req.Spreadsheet, nil), map[string]any{"trashed": true}, req)
	default:
		return nil, fmt.Errorf("googlesheets: unknown operation %q (want %s)", req.Operation, strings.Join(OpNames(), ", "))
	}
}

// valueInputOption maps the authored mode to Google's own name. The compiler has
// already applied the default, so an empty value here can only be a caller that
// bypassed it — and USER_ENTERED is the answer that makes a written date a date.
func valueInputOption(input string) string {
	if input == InputRaw {
		return "RAW"
	}
	return "USER_ENTERED"
}

// createSpreadsheet creates the file and, when the task names a folder, moves it
// there. Sheets creates it because only Sheets can name the first tab in the same
// request; Drive moves it because only Drive knows about folders.
func (c *HTTPClient) createSpreadsheet(ctx context.Context, req Request) (any, error) {
	body := map[string]any{"properties": map[string]any{"title": req.Title}}
	if strings.TrimSpace(req.Sheet) != "" {
		body["sheets"] = []any{map[string]any{"properties": map[string]any{"title": req.Sheet}}}
	}
	created, err := c.call(ctx, http.MethodPost, c.conn.SheetsBase+"/v4/spreadsheets", body, req)
	if err != nil {
		return nil, err
	}
	obj, _ := created.(map[string]any)
	id, _ := obj["spreadsheetId"].(string)
	if req.Folder == "" || id == "" {
		return created, nil
	}
	moved, err := c.move(ctx, id, req)
	if err != nil {
		// The spreadsheet exists; only the move failed. Failing the job is still right
		// — a retry re-creates it, which is a duplicate, but silently leaving the file
		// where nobody looks for it is the failure that goes unnoticed.
		return nil, err
	}
	// One answer, not two: the model asked for a spreadsheet, and the Drive fields
	// (the link above all) are the useful half of what the move returned.
	if file, ok := moved.(map[string]any); ok {
		for _, k := range []string{"webViewLink", "parents", "name"} {
			if v, ok := file[k]; ok {
				obj[k] = v
			}
		}
	}
	return obj, nil
}

// move puts a newly created file into the authored folder. The current parents are
// read first and removed, so the file *moves* rather than acquiring a second home —
// Drive's parents are a list, and adding one without removing the others leaves the
// file in the credential's root as well.
func (c *HTTPClient) move(ctx context.Context, id string, req Request) (any, error) {
	current, err := c.call(ctx, http.MethodGet, c.driveURL(id, url.Values{"fields": {"id,parents"}}), nil, req)
	if err != nil {
		return nil, err
	}
	q := url.Values{"addParents": {req.Folder}, "fields": {driveFileFields}}
	if obj, _ := current.(map[string]any); obj != nil {
		if parents, _ := obj["parents"].([]any); len(parents) > 0 {
			old := make([]string, 0, len(parents))
			for _, p := range parents {
				if s, ok := p.(string); ok && s != req.Folder {
					old = append(old, s)
				}
			}
			if len(old) > 0 {
				q.Set("removeParents", strings.Join(old, ","))
			}
		}
	}
	return c.call(ctx, http.MethodPatch, c.driveURL(id, q), map[string]any{}, req)
}

// readRange answers with the rows themselves rather than Sheets' envelope: a model
// that read a range wants the values, and `range`/`majorDimension` are what it already
// authored. With Header the rows are keyed by the first row (see [WithHeader]).
func (c *HTTPClient) readRange(ctx context.Context, req Request) (any, error) {
	got, err := c.call(ctx, http.MethodGet, c.valuesURL(req, "", nil), nil, req)
	if err != nil {
		return nil, err
	}
	obj, _ := got.(map[string]any)
	raw, _ := obj["values"].([]any)
	rows := make([][]any, 0, len(raw))
	for _, r := range raw {
		cells, _ := r.([]any)
		rows = append(rows, cells)
	}
	if req.Header {
		return WithHeader(rows), nil
	}
	// Sheets omits `values` entirely for an empty range. An empty list rather than a
	// null is what keeps count() and a multi-instance loop working over the answer.
	out := make([]any, len(rows))
	for i, r := range rows {
		out[i] = r
	}
	return out, nil
}

// deleteSheet removes one tab. Sheets deletes by numeric sheetId, so the authored
// title is resolved against the spreadsheet's own tabs first — a model tied to a
// numeric id would be tied to one spreadsheet's internals, and the id is not visible
// anywhere a person looks.
func (c *HTTPClient) deleteSheet(ctx context.Context, req Request) (any, error) {
	meta, err := c.call(ctx, http.MethodGet, c.sheetURL(req.Spreadsheet, url.Values{
		"fields": {"sheets.properties(sheetId,title)"},
	}), nil, req)
	if err != nil {
		return nil, err
	}
	id, titles := findSheet(meta, req.Sheet)
	if id == nil {
		return nil, fmt.Errorf("googlesheets: spreadsheet %s has no sheet named %q (it has %s)",
			req.Spreadsheet, req.Sheet, strings.Join(titles, ", "))
	}
	return c.batchUpdate(ctx, req, map[string]any{
		"deleteSheet": map[string]any{"sheetId": id},
	})
}

// findSheet looks a tab up by title in a spreadsheet's metadata, returning its id and
// — for the error when there is none — every title that does exist.
func findSheet(meta any, title string) (any, []string) {
	obj, _ := meta.(map[string]any)
	sheets, _ := obj["sheets"].([]any)
	titles := make([]string, 0, len(sheets))
	for _, s := range sheets {
		sheet, _ := s.(map[string]any)
		props, _ := sheet["properties"].(map[string]any)
		if props == nil {
			continue
		}
		have, _ := props["title"].(string)
		titles = append(titles, have)
		if have == title {
			return props["sheetId"], titles
		}
	}
	return nil, titles
}

// batchUpdate wraps one request in the envelope every structural change to a
// spreadsheet travels in.
func (c *HTTPClient) batchUpdate(ctx context.Context, req Request, request map[string]any) (any, error) {
	return c.call(ctx, http.MethodPost, c.sheetURL(req.Spreadsheet, nil)+":batchUpdate",
		map[string]any{"requests": []any{request}}, req)
}

// sheetURL, valuesURL and driveURL build the three URL shapes this connector uses. The
// range is path-escaped: A1 notation carries '!' and ':' which Google reads, and a
// sheet title carries whatever a person typed.
func (c *HTTPClient) sheetURL(id string, q url.Values) string {
	return c.conn.SheetsBase + "/v4/spreadsheets/" + url.PathEscape(id) + query(q)
}

func (c *HTTPClient) valuesURL(req Request, verb string, q url.Values) string {
	return c.conn.SheetsBase + "/v4/spreadsheets/" + url.PathEscape(req.Spreadsheet) +
		"/values/" + url.PathEscape(req.Range) + verb + query(q)
}

func (c *HTTPClient) driveURL(id string, q url.Values) string {
	return c.conn.DriveBase + "/drive/v3/files/" + url.PathEscape(id) + query(q)
}

func query(q url.Values) string {
	if len(q) == 0 {
		return ""
	}
	return "?" + q.Encode()
}

// call performs one authenticated request and decodes the JSON answer. A non-2xx
// status is an error carrying Google's own message — for the failure an operator will
// actually hit, a 403 for a scope the credential was not granted, that text is the
// part that says which scope.
func (c *HTTPClient) call(ctx context.Context, method, endpoint string, payload any, req Request) (any, error) {
	tok, err := c.conn.Tokens.Token(ctx)
	if err != nil {
		return nil, err
	}
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("googlesheets: encode %s request: %w", req.Operation, err)
		}
		body = bytes.NewReader(raw)
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, fmt.Errorf("googlesheets: build %s request: %w", req.Operation, err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+tok)
	httpReq.Header.Set("Accept", "application/json")
	if payload != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	if req.RequestID != "" {
		httpReq.Header.Set("X-Request-ID", req.RequestID)
	}
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("googlesheets: %s: %w", req.Operation, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("googlesheets: %s returned HTTP %d: %s", req.Operation, resp.StatusCode, googleMessage(raw))
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber() // keep ids and cell numbers exact through the variable round-trip
	var out any
	if err := dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("googlesheets: decode %s response: %w", req.Operation, err)
	}
	return out, nil
}

// googleMessage pulls the human-readable half out of Google's error envelope, falling
// back to the raw body when it is not one — an HTML error page from a proxy is still
// more useful whole than replaced by "unknown error".
func googleMessage(raw []byte) string {
	var env struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err == nil && env.Error.Message != "" {
		return env.Error.Message
	}
	return strings.TrimSpace(string(raw))
}
