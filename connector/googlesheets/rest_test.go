package googlesheets_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	gs "github.com/pblumer/atlas/connector/googlesheets"
)

// staticToken is a TokenSource that hands out one token, so a client test exercises
// the API calls rather than the OAuth flow (which oauth2's own tests cover).
type staticToken string

func (t staticToken) Token(context.Context) (string, error) { return string(t), nil }

// call records one request the fake Google received, reduced to what the assertions
// are about.
type call struct {
	method string
	path   string
	query  string
	body   map[string]any
}

// fakeGoogle serves canned answers and records what it was asked. routes maps
// "METHOD /path" to the JSON body to answer with; anything unrouted is a 404, which
// is what makes a wrong URL a test failure rather than a silent pass.
type fakeGoogle struct {
	t      *testing.T
	routes map[string]string
	calls  []call
	srv    *httptest.Server
}

func newFakeGoogle(t *testing.T, routes map[string]string) *fakeGoogle {
	t.Helper()
	f := &fakeGoogle{t: t, routes: routes}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &body)
		}
		f.calls = append(f.calls, call{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery, body: body})
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("Authorization = %q; want the bearer token", got)
		}
		answer, ok := f.routes[r.Method+" "+r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"message":"no route"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(answer))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGoogle) client() *gs.HTTPClient {
	return gs.NewHTTPClient(gs.Account{Tokens: staticToken("tok"), SheetsBase: f.srv.URL, DriveBase: f.srv.URL})
}

const sheetID = "1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgvE2upms"

// TestCreateSpreadsheet: Sheets creates the file and names its first tab in the one
// request, which is why this is not a Drive files.create.
func TestCreateSpreadsheet(t *testing.T) {
	f := newFakeGoogle(t, map[string]string{
		"POST /v4/spreadsheets": `{"spreadsheetId":"` + sheetID + `","spreadsheetUrl":"https://docs.google.com/x"}`,
	})
	got, err := f.client().Do(context.Background(), gs.Request{
		Operation: "create-spreadsheet", Title: "Anträge 2026", Sheet: "Eingang",
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	obj, _ := got.(map[string]any)
	if obj["spreadsheetId"] != sheetID {
		t.Errorf("result = %#v; want the created spreadsheet", got)
	}
	if len(f.calls) != 1 {
		t.Fatalf("made %d calls; want 1 (no folder was named)", len(f.calls))
	}
	props, _ := f.calls[0].body["properties"].(map[string]any)
	if props["title"] != "Anträge 2026" {
		t.Errorf("request properties = %#v; want the authored title", props)
	}
	sheets, _ := f.calls[0].body["sheets"].([]any)
	if len(sheets) != 1 {
		t.Fatalf("request names %d sheets; want the one authored tab", len(sheets))
	}
	first, _ := sheets[0].(map[string]any)
	sp, _ := first["properties"].(map[string]any)
	if sp["title"] != "Eingang" {
		t.Errorf("first sheet = %#v; want the authored tab title", sp)
	}
}

// TestCreateSpreadsheetMovesIntoFolder: Drive owns folders, so a created file is moved
// by removing the parents it was born with and adding the authored one. Reading the
// current parents first is what makes the move a move rather than a second home.
func TestCreateSpreadsheetMovesIntoFolder(t *testing.T) {
	f := newFakeGoogle(t, map[string]string{
		"POST /v4/spreadsheets":          `{"spreadsheetId":"` + sheetID + `"}`,
		"GET /drive/v3/files/" + sheetID: `{"id":"` + sheetID + `","parents":["rootid"]}`,
		"PATCH /drive/v3/files/" + sheetID: `{"id":"` + sheetID + `","parents":["fold"],` +
			`"webViewLink":"https://drive.google.com/x"}`,
	})
	got, err := f.client().Do(context.Background(), gs.Request{
		Operation: "create-spreadsheet", Title: "T", Folder: "https://drive.google.com/drive/folders/fold",
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	obj, _ := got.(map[string]any)
	if obj["spreadsheetId"] != sheetID {
		t.Errorf("result = %#v; want the created spreadsheet, not the Drive file", got)
	}
	if obj["webViewLink"] != "https://drive.google.com/x" {
		t.Errorf("result = %#v; want the moved file's link folded in", got)
	}
	if len(f.calls) != 3 {
		t.Fatalf("made %d calls; want create, read parents, move", len(f.calls))
	}
	if q := f.calls[2].query; !strings.Contains(q, "addParents=fold") || !strings.Contains(q, "removeParents=rootid") {
		t.Errorf("move query = %q; want the folder added and the old parent removed", q)
	}
}

// TestAddSheet is one batchUpdate request, which is the only way Sheets adds a tab.
func TestAddSheet(t *testing.T) {
	f := newFakeGoogle(t, map[string]string{
		"POST /v4/spreadsheets/" + sheetID + ":batchUpdate": `{"replies":[{"addSheet":{"properties":{"sheetId":7,"title":"Neu"}}}]}`,
	})
	if _, err := f.client().Do(context.Background(), gs.Request{
		Operation: "add-sheet", Spreadsheet: sheetID, Sheet: "Neu",
	}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	reqs, _ := f.calls[0].body["requests"].([]any)
	if len(reqs) != 1 {
		t.Fatalf("batchUpdate carries %d requests; want 1", len(reqs))
	}
	first, _ := reqs[0].(map[string]any)
	add, _ := first["addSheet"].(map[string]any)
	props, _ := add["properties"].(map[string]any)
	if props["title"] != "Neu" {
		t.Errorf("addSheet = %#v; want the authored title", add)
	}
}

// TestReadRangeAnswersRows: the result variable receives the rows themselves, not
// Sheets' envelope. A model that read a range wants the values.
func TestReadRangeAnswersRows(t *testing.T) {
	f := newFakeGoogle(t, map[string]string{
		"GET /v4/spreadsheets/" + sheetID + "/values/Tabelle1!A1:B2": `{"range":"Tabelle1!A1:B2","majorDimension":"ROWS","values":[["name","amount"],["Anna","42"]]}`,
	})
	got, err := f.client().Do(context.Background(), gs.Request{
		Operation: "read-range", Spreadsheet: sheetID, Range: "Tabelle1!A1:B2",
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	want := []any{[]any{"name", "amount"}, []any{"Anna", "42"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("read = %#v; want %#v", got, want)
	}
}

// TestReadRangeWithHeader keys the rows by the first row, which is the shape a
// multi-instance subprocess iterates.
func TestReadRangeWithHeader(t *testing.T) {
	f := newFakeGoogle(t, map[string]string{
		"GET /v4/spreadsheets/" + sheetID + "/values/A:B": `{"values":[["name","amount"],["Anna","42"]]}`,
	})
	got, err := f.client().Do(context.Background(), gs.Request{
		Operation: "read-range", Spreadsheet: sheetID, Range: "A:B", Header: true,
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	want := []any{map[string]any{"name": "Anna", "amount": "42"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("read = %#v; want %#v", got, want)
	}
}

// TestReadEmptyRangeAnswersEmptyList: Sheets omits `values` entirely for an empty
// range. Answering with an empty list rather than null is what keeps `count(rows) = 0`
// working in the model that reads it.
func TestReadEmptyRangeAnswersEmptyList(t *testing.T) {
	f := newFakeGoogle(t, map[string]string{
		"GET /v4/spreadsheets/" + sheetID + "/values/A:B": `{"range":"A:B"}`,
	})
	got, err := f.client().Do(context.Background(), gs.Request{
		Operation: "read-range", Spreadsheet: sheetID, Range: "A:B",
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if !reflect.DeepEqual(got, []any{}) {
		t.Errorf("read of an empty range = %#v; want an empty list", got)
	}
}

// TestWriteRange sends the rows and the value-input mode Google names.
func TestWriteRange(t *testing.T) {
	f := newFakeGoogle(t, map[string]string{
		"PUT /v4/spreadsheets/" + sheetID + "/values/A1:B1": `{"updatedCells":2}`,
	})
	if _, err := f.client().Do(context.Background(), gs.Request{
		Operation: "write-range", Spreadsheet: sheetID, Range: "A1:B1",
		Values: [][]any{{"Anna", json.Number("42")}}, Input: gs.InputRaw,
	}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if q := f.calls[0].query; !strings.Contains(q, "valueInputOption=RAW") {
		t.Errorf("query = %q; want RAW", q)
	}
	rows, _ := f.calls[0].body["values"].([]any)
	if len(rows) != 1 {
		t.Fatalf("sent %d rows; want 1", len(rows))
	}
}

// TestAppendRowUsesInsertRows: without insertDataOption Sheets overwrites whatever
// happens to sit below the table, which is not what "append" means.
func TestAppendRowUsesInsertRows(t *testing.T) {
	f := newFakeGoogle(t, map[string]string{
		"POST /v4/spreadsheets/" + sheetID + "/values/A:C:append": `{"updates":{"updatedRows":1}}`,
	})
	if _, err := f.client().Do(context.Background(), gs.Request{
		Operation: "append-row", Spreadsheet: sheetID, Range: "A:C", Values: [][]any{{"x"}},
	}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	q := f.calls[0].query
	if !strings.Contains(q, "insertDataOption=INSERT_ROWS") {
		t.Errorf("query = %q; want INSERT_ROWS", q)
	}
	if !strings.Contains(q, "valueInputOption=USER_ENTERED") {
		t.Errorf("query = %q; want the USER_ENTERED default", q)
	}
}

// TestClearRange empties the cells and keeps the sheet.
func TestClearRange(t *testing.T) {
	f := newFakeGoogle(t, map[string]string{
		"POST /v4/spreadsheets/" + sheetID + "/values/A2:F:clear": `{"clearedRange":"A2:F100"}`,
	})
	if _, err := f.client().Do(context.Background(), gs.Request{
		Operation: "clear-range", Spreadsheet: sheetID, Range: "A2:F",
	}); err != nil {
		t.Fatalf("Do: %v", err)
	}
}

// TestDeleteSheetResolvesTitleToID: Sheets deletes a tab by numeric id, and a model
// tied to a numeric id is tied to one spreadsheet's internals — so the title is
// resolved first, the way the Jira Worker Type resolves a transition name.
func TestDeleteSheetResolvesTitleToID(t *testing.T) {
	f := newFakeGoogle(t, map[string]string{
		"GET /v4/spreadsheets/" + sheetID:                   `{"sheets":[{"properties":{"sheetId":0,"title":"Tabelle1"}},{"properties":{"sheetId":13,"title":"Alt"}}]}`,
		"POST /v4/spreadsheets/" + sheetID + ":batchUpdate": `{"replies":[{}]}`,
	})
	if _, err := f.client().Do(context.Background(), gs.Request{
		Operation: "delete-sheet", Spreadsheet: sheetID, Sheet: "Alt",
	}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	reqs, _ := f.calls[1].body["requests"].([]any)
	first, _ := reqs[0].(map[string]any)
	del, _ := first["deleteSheet"].(map[string]any)
	if del["sheetId"] != float64(13) {
		t.Errorf("deleteSheet = %#v; want the resolved sheetId 13", del)
	}
}

// TestDeleteSheetUnknownTitle names what is actually there, because the usual cause is
// a typo or a renamed tab and the list is the answer.
func TestDeleteSheetUnknownTitle(t *testing.T) {
	f := newFakeGoogle(t, map[string]string{
		"GET /v4/spreadsheets/" + sheetID: `{"sheets":[{"properties":{"sheetId":0,"title":"Tabelle1"}}]}`,
	})
	_, err := f.client().Do(context.Background(), gs.Request{
		Operation: "delete-sheet", Spreadsheet: sheetID, Sheet: "Fehlt",
	})
	if err == nil {
		t.Fatal("deleting an unknown tab: want an error, got nil")
	}
	if !strings.Contains(err.Error(), "Tabelle1") {
		t.Errorf("error %q should name the sheets that do exist", err)
	}
}

// TestDeleteSpreadsheetTrashes: an unrecoverable purge is not offered, so this is a
// Drive update setting trashed rather than a files.delete.
func TestDeleteSpreadsheetTrashes(t *testing.T) {
	f := newFakeGoogle(t, map[string]string{
		"PATCH /drive/v3/files/" + sheetID: `{"id":"` + sheetID + `","trashed":true}`,
	})
	if _, err := f.client().Do(context.Background(), gs.Request{
		Operation: "delete-spreadsheet", Spreadsheet: sheetID,
	}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if f.calls[0].method != http.MethodPatch {
		t.Errorf("method = %s; want PATCH (a trash, not a delete)", f.calls[0].method)
	}
	if f.calls[0].body["trashed"] != true {
		t.Errorf("body = %#v; want trashed:true", f.calls[0].body)
	}
}

// TestErrorsCarryGooglesMessage: a 403 for a missing scope is the failure an operator
// will actually hit, and Google's own text is the part that says which scope.
func TestErrorsCarryGooglesMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":403,"message":"Request had insufficient authentication scopes."}}`))
	}))
	defer srv.Close()
	c := gs.NewHTTPClient(gs.Account{Tokens: staticToken("tok"), SheetsBase: srv.URL, DriveBase: srv.URL})
	_, err := c.Do(context.Background(), gs.Request{Operation: "read-range", Spreadsheet: sheetID, Range: "A1"})
	if err == nil {
		t.Fatal("a 403: want an error, got nil")
	}
	if !strings.Contains(err.Error(), "insufficient authentication scopes") {
		t.Errorf("error %q should carry Google's own message", err)
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("error %q should carry the status", err)
	}
}

// TestUnknownOperationLists what this package does implement — the same courtesy the
// compiler already extends at deploy.
func TestUnknownOperation(t *testing.T) {
	c := gs.NewHTTPClient(gs.Account{Tokens: staticToken("tok")})
	_, err := c.Do(context.Background(), gs.Request{Operation: "pivot"})
	if err == nil || !strings.Contains(err.Error(), "read-range") {
		t.Errorf("unknown operation: want an error listing the operations, got %v", err)
	}
}

// TestTokenFailureIsNotACall: a credential that cannot mint a token must fail before
// any request, so a broken Worker never reaches Google at all.
func TestTokenFailureIsNotACall(t *testing.T) {
	f := newFakeGoogle(t, map[string]string{})
	c := gs.NewHTTPClient(gs.Account{Tokens: failingToken{}, SheetsBase: f.srv.URL, DriveBase: f.srv.URL})
	if _, err := c.Do(context.Background(), gs.Request{Operation: "read-range", Spreadsheet: sheetID, Range: "A1"}); err == nil {
		t.Fatal("want the token error, got nil")
	}
	if len(f.calls) != 0 {
		t.Errorf("made %d calls with no token; want none", len(f.calls))
	}
}

type failingToken struct{}

func (failingToken) Token(context.Context) (string, error) {
	return "", io.ErrUnexpectedEOF
}
