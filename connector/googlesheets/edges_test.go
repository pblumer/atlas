package googlesheets_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gs "github.com/pblumer/atlas/connector/googlesheets"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
)

// TestValuesKeepTheirFeelShape: a cell's JSON shape follows its FEEL value's kind, not
// the look of its text. Each kind is asserted because each is a different cell to
// Sheets — a number sent as "42" is text in the sheet and does not add up.
func TestValuesKeepTheirFeelShape(t *testing.T) {
	rd, lookup := workerFixture(t,
		`<atlas:googleSheetsConnector connector="acme" operation="append-row" spreadsheet="1B" range="A:E"
		    values="=[text, zahl, ja, fehlt]"/>`,
		model.VariableValue{Name: "text", Kind: model.VarString, Text: "Anna"},
		model.VariableValue{Name: "zahl", Kind: model.VarNumber, Text: "42"},
		model.VariableValue{Name: "ja", Kind: model.VarBool, Bool: true},
	)
	client := &recordingClient{}
	if _, err := gs.Handler(rd, lookup, registered(client))(job.Job{Key: 1, ElementInstanceKey: 42}); err != nil {
		t.Fatalf("handler: %v", err)
	}
	row := client.reqs[0].Values[0]
	if row[0] != "Anna" {
		t.Errorf("cell 0 = %#v, want the string", row[0])
	}
	if n, ok := row[1].(json.Number); !ok || n.String() != "42" {
		t.Errorf("cell 1 = %#v, want the JSON number", row[1])
	}
	if row[2] != true {
		t.Errorf("cell 2 = %#v, want the boolean", row[2])
	}
	// An unbound name is FEEL null, which is an empty cell rather than a missing one.
	if row[3] != nil {
		t.Errorf("cell 3 = %#v, want null for a variable that is not set", row[3])
	}
}

// TestWholeValueKinds: the values expression need not be a list at all. A scalar is
// one cell, which is what writing a status or an amount into a single cell means.
func TestWholeValueKinds(t *testing.T) {
	for name, tc := range map[string]struct {
		expr string
		vars []model.VariableValue
		want any
	}{
		"a number": {"=zahl", []model.VariableValue{{Name: "zahl", Kind: model.VarNumber, Text: "42"}}, json.Number("42")},
		"a bool":   {"=ja", []model.VariableValue{{Name: "ja", Kind: model.VarBool, Bool: true}}, true},
		"a string": {"=text", []model.VariableValue{{Name: "text", Kind: model.VarString, Text: "offen"}}, "offen"},
	} {
		rd, lookup := workerFixture(t,
			`<atlas:googleSheetsConnector connector="acme" operation="write-range" spreadsheet="1B" range="A1" values="`+tc.expr+`"/>`,
			tc.vars...)
		client := &recordingClient{}
		if _, err := gs.Handler(rd, lookup, registered(client))(job.Job{Key: 1, ElementInstanceKey: 42}); err != nil {
			t.Fatalf("%s: handler: %v", name, err)
		}
		if got := client.reqs[0].Values; len(got) != 1 || len(got[0]) != 1 || got[0][0] != tc.want {
			t.Errorf("%s: values = %#v, want one cell of %#v", name, got, tc.want)
		}
	}
}

// TestNullValuesFailRatherThanWriteNothing: an unset variable is a write with nothing
// in it, and a job that "succeeded" having written nothing is the failure nobody sees.
func TestNullValuesFailRatherThanWriteNothing(t *testing.T) {
	rd, lookup := workerFixture(t,
		`<atlas:googleSheetsConnector connector="acme" operation="write-range" spreadsheet="1B" range="A1" values="=fehlt"/>`)
	client := &recordingClient{}
	if _, err := gs.Handler(rd, lookup, registered(client))(job.Job{Key: 1, ElementInstanceKey: 42}); err == nil {
		t.Error("values resolving to null: want an error, got nil")
	}
	if len(client.reqs) != 0 {
		t.Errorf("made %d calls with nothing to write", len(client.reqs))
	}
}

// TestLiteralValuesAreNeverParsed: a literal is a literal. Text that happens to look
// like JSON is one cell of that text, which is the rule every other Worker Type follows.
func TestLiteralValuesAreNeverParsed(t *testing.T) {
	rd, lookup := workerFixture(t,
		`<atlas:googleSheetsConnector connector="acme" operation="write-range" spreadsheet="1B" range="A1" values="{nicht json}"/>`)
	client := &recordingClient{}
	if _, err := gs.Handler(rd, lookup, registered(client))(job.Job{Key: 1, ElementInstanceKey: 42}); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if got := client.reqs[0].Values; len(got) != 1 || got[0][0] != "{nicht json}" {
		t.Errorf("values = %#v, want one cell of the literal text", got)
	}
}

// TestProcessInstanceKeyBindsInAValue: the reserved name is what puts a traceable
// back-reference into a row, so an appended line can be found again from the instance.
func TestProcessInstanceKeyBindsInAValue(t *testing.T) {
	rd, lookup := workerFixture(t,
		`<atlas:googleSheetsConnector connector="acme" operation="append-row" spreadsheet="1B" range="A:B"
		    values="=[processInstanceKey, &quot;offen&quot;]"/>`)
	client := &recordingClient{}
	if _, err := gs.Handler(rd, lookup, registered(client))(job.Job{Key: 1, ElementInstanceKey: 42}); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if got := client.reqs[0].Values[0][0]; got != "500" {
		t.Errorf("cell 0 = %#v, want the process instance key", got)
	}
}

// TestResultVariableKeepsItsKind: what Google returns lands as the kind it is, so a
// gateway comparing a number compares a number.
func TestResultVariableKeepsItsKind(t *testing.T) {
	for name, tc := range map[string]struct {
		result any
		want   model.VarKind
	}{
		"a string":  {"fertig", model.VarString},
		"a number":  {json.Number("7"), model.VarNumber},
		"a bool":    {true, model.VarBool},
		"an object": {map[string]any{"spreadsheetId": "1B"}, model.VarJSON},
	} {
		rd, lookup := workerFixture(t,
			`<atlas:googleSheetsConnector connector="acme" operation="read-range" spreadsheet="1B" range="A1" resultVariable="r"/>`)
		out, err := gs.Handler(rd, lookup, registered(&recordingClient{result: tc.result}))(job.Job{Key: 1, ElementInstanceKey: 42})
		if err != nil {
			t.Fatalf("%s: handler: %v", name, err)
		}
		if len(out) != 1 || out[0].Kind != tc.want {
			t.Errorf("%s: result = %+v, want kind %v", name, out, tc.want)
		}
	}
}

// TestHandlerPropagatesAStoreError: a store that cannot answer is not an empty answer,
// and the job must stay pending rather than complete over it.
func TestHandlerPropagatesAStoreError(t *testing.T) {
	rd, lookup := workerFixture(t,
		`<atlas:googleSheetsConnector connector="acme" operation="clear-range" spreadsheet="1B" range="A2:F"/>`)
	failing := &failingReader{fakeReader: rd}
	if _, err := gs.Handler(failing, lookup, registered(&recordingClient{}))(job.Job{Key: 1, ElementInstanceKey: 42}); err == nil {
		t.Error("a store error: want it propagated, got nil")
	}
}

type failingReader struct{ *fakeReader }

func (f *failingReader) GetElementInstance(uint64) (*model.ElementInstanceValue, bool, error) {
	return nil, false, errors.New("store is unavailable")
}

// TestHandlerPropagatesTheClientError: a failed call leaves the job pending for a
// retry and then an incident (ADR-0061); it never completes the token.
func TestHandlerPropagatesTheClientError(t *testing.T) {
	rd, lookup := workerFixture(t,
		`<atlas:googleSheetsConnector connector="acme" operation="clear-range" spreadsheet="1B" range="A2:F"/>`)
	client := &recordingClient{err: errors.New("Google said no")}
	if _, err := gs.Handler(rd, lookup, registered(client))(job.Job{Key: 1, ElementInstanceKey: 42}); err == nil {
		t.Error("a client error: want it propagated, got nil")
	}
}

// TestWithHeaderNamesNonStringHeaderCells: a header row of years or ids is legal, and
// each still has to name a field.
func TestWithHeaderNamesNonStringHeaderCells(t *testing.T) {
	got := gs.WithHeader([][]any{
		{json.Number("2026"), nil, " name "},
		{"a", "b", "c"},
	})
	rec, _ := got[0].(map[string]any)
	if rec["2026"] != "a" {
		t.Errorf("record = %#v, want the numeric header naming its column", rec)
	}
	// A header cell with no text names no column, so its cells stay in no field rather
	// than in one called "".
	if _, ok := rec[""]; ok {
		t.Errorf("record = %#v, want no field for an unnamed column", rec)
	}
	if rec["name"] != "c" {
		t.Errorf("record = %#v, want the header trimmed", rec)
	}
}

// TestErrorBodyThatIsNotGooglesEnvelope: a proxy in front of Google answers HTML, and
// the whole body is more use than "unknown error".
func TestErrorBodyThatIsNotGooglesEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html>proxy error</html>"))
	}))
	defer srv.Close()
	c := gs.NewHTTPClient(gs.Account{Tokens: staticToken("tok"), SheetsBase: srv.URL, DriveBase: srv.URL})
	_, err := c.Do(context.Background(), gs.Request{Operation: "clear-range", Spreadsheet: "1B", Range: "A1"})
	if err == nil || !strings.Contains(err.Error(), "proxy error") {
		t.Errorf("a non-JSON error body: want it carried through, got %v", err)
	}
}

// TestEmptyAnswerIsNotAResult: Google answers some calls with no body, and a result
// variable must not be invented for one.
func TestEmptyAnswerIsNotAResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	c := gs.NewHTTPClient(gs.Account{Tokens: staticToken("tok"), SheetsBase: srv.URL, DriveBase: srv.URL})
	got, err := c.Do(context.Background(), gs.Request{Operation: "add-sheet", Spreadsheet: "1B", Sheet: "S"})
	if err != nil || got != nil {
		t.Errorf("an empty answer = %#v, %v; want nil, nil", got, err)
	}
}

// TestUndecodableAnswer: a 200 carrying something that is not JSON is a failure, not
// an empty result — completing a token over it would call work done that was not.
func TestUndecodableAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("nicht json"))
	}))
	defer srv.Close()
	c := gs.NewHTTPClient(gs.Account{Tokens: staticToken("tok"), SheetsBase: srv.URL, DriveBase: srv.URL})
	if _, err := c.Do(context.Background(), gs.Request{Operation: "read-range", Spreadsheet: "1B", Range: "A1"}); err == nil {
		t.Error("an undecodable 200: want an error, got nil")
	}
}

// TestMoveWithNoParentsStillAdds: a file Drive reports with no parents (a shared drive
// can) is added to the folder without a removeParents nobody can name.
func TestMoveWithNoParentsStillAdds(t *testing.T) {
	f := newFakeGoogle(t, map[string]string{
		"POST /v4/spreadsheets":    `{"spreadsheetId":"1B"}`,
		"GET /drive/v3/files/1B":   `{"id":"1B"}`,
		"PATCH /drive/v3/files/1B": `{"id":"1B"}`,
	})
	if _, err := f.client().Do(context.Background(), gs.Request{
		Operation: "create-spreadsheet", Title: "T", Folder: "fold",
	}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if q := f.calls[2].query; strings.Contains(q, "removeParents") {
		t.Errorf("move query = %q; want no removeParents when there are none", q)
	}
}

// TestMoveFailureFailsTheJob: the spreadsheet exists but is not where the model said.
// Failing is right — a file silently left where nobody looks for it is the failure
// that goes unnoticed.
func TestMoveFailureFailsTheJob(t *testing.T) {
	f := newFakeGoogle(t, map[string]string{
		"POST /v4/spreadsheets": `{"spreadsheetId":"1B"}`,
	})
	if _, err := f.client().Do(context.Background(), gs.Request{
		Operation: "create-spreadsheet", Title: "T", Folder: "fold",
	}); err == nil {
		t.Error("a failed move: want the job to fail, got nil")
	}
}

// TestDeleteSheetIgnoresEntriesWithoutProperties: a sheets array Google fills in
// differently than expected must not panic the worker.
func TestDeleteSheetIgnoresEntriesWithoutProperties(t *testing.T) {
	f := newFakeGoogle(t, map[string]string{
		"GET /v4/spreadsheets/1B":              `{"sheets":[{},{"properties":{"sheetId":3,"title":"Alt"}}]}`,
		"POST /v4/spreadsheets/1B:batchUpdate": `{"replies":[{}]}`,
	})
	if _, err := f.client().Do(context.Background(), gs.Request{
		Operation: "delete-sheet", Spreadsheet: "1B", Sheet: "Alt",
	}); err != nil {
		t.Fatalf("Do: %v", err)
	}
}

// TestRequestIDTravelsAsAHeader: the job key rides along as X-Request-ID. It buys
// tracing rather than idempotency — Google deduplicates nothing on it — but a
// duplicated row in a sheet is exactly the case somebody has to trace back to a job,
// so the header is a contract worth asserting rather than a nicety.
func TestRequestIDTravelsAsAHeader(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-Request-ID")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := gs.NewHTTPClient(gs.Account{Tokens: staticToken("tok"), SheetsBase: srv.URL, DriveBase: srv.URL})
	if _, err := c.Do(context.Background(), gs.Request{
		Operation: "clear-range", Spreadsheet: "1B", Range: "A1", RequestID: "4711",
	}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if got != "4711" {
		t.Errorf("X-Request-ID = %q; want the job key", got)
	}
}

// TestHandlerRefusesAnElementThatIsNotASheetsTask: the job and the element instance
// disagree about what this element is — a definition replaced under a pending job, say.
// Failing keeps the job for a retry; acting on whatever detail happened to sit at that
// index would perform a different task's call.
func TestHandlerRefusesAnElementThatIsNotASheetsTask(t *testing.T) {
	rd, lookup := workerFixture(t,
		`<atlas:googleSheetsConnector connector="acme" operation="clear-range" spreadsheet="1B" range="A2:F"/>`)
	cp := lookup(7)
	rd.ei.ElementId = cp.StartEvents()[0] // a start event carries no task detail
	client := &recordingClient{}
	if _, err := gs.Handler(rd, lookup, registered(client))(job.Job{Key: 1, ElementInstanceKey: 42}); err == nil {
		t.Error("an element that is not a Sheets task: want an error, got nil")
	}
	if len(client.reqs) != 0 {
		t.Errorf("made %d calls for an element that is not a Sheets task", len(client.reqs))
	}
}

// TestHandlerPropagatesAVariableReadError: the element instance is there but its scope
// cannot be read. That is not "no variables" — resolving the task against an empty
// scope would write a row of blanks and call it done.
func TestHandlerPropagatesAVariableReadError(t *testing.T) {
	rd, lookup := workerFixture(t,
		`<atlas:googleSheetsConnector connector="acme" operation="write-range" spreadsheet="1B" range="A1" values="=zeilen"/>`)
	client := &recordingClient{}
	_, err := gs.Handler(&unreadableScope{fakeReader: rd}, lookup, registered(client))(job.Job{Key: 1, ElementInstanceKey: 42})
	if err == nil {
		t.Error("a scope that cannot be read: want an error, got nil")
	}
	if len(client.reqs) != 0 {
		t.Errorf("made %d calls without having read the variables", len(client.reqs))
	}
}

// unreadableScope answers with the element instance but fails on its variables — the
// half of the store the resolve step needs second.
type unreadableScope struct{ *fakeReader }

func (u *unreadableScope) VariablesOfScope(uint64, func(*model.VariableValue) error) error {
	return errors.New("scope is unavailable")
}

// TestValuesWithNoVariableReferences: a FEEL value that names nothing still resolves.
// It is the shape a header row has — constants — and it must not depend on the
// binding step having anything to bind.
func TestValuesWithNoVariableReferences(t *testing.T) {
	rd, lookup := workerFixture(t,
		`<atlas:googleSheetsConnector connector="acme" operation="write-range" spreadsheet="1B" range="A1:B1"
		    values="=[[&quot;Name&quot;, &quot;Betrag&quot;]]"/>`)
	client := &recordingClient{}
	if _, err := gs.Handler(rd, lookup, registered(client))(job.Job{Key: 1, ElementInstanceKey: 42}); err != nil {
		t.Fatalf("handler: %v", err)
	}
	row := client.reqs[0].Values[0]
	if len(row) != 2 || row[0] != "Name" {
		t.Errorf("row = %#v; want the two constant cells", row)
	}
}

// TestAVariableExplicitlySetToNullIsAnEmptyCell: a variable that exists and holds null
// is not the same as one that was never set, but for a spreadsheet both are an empty
// cell — and the binding step has to carry the first through rather than drop it.
func TestAVariableExplicitlySetToNullIsAnEmptyCell(t *testing.T) {
	rd, lookup := workerFixture(t,
		`<atlas:googleSheetsConnector connector="acme" operation="append-row" spreadsheet="1B" range="A:B"
		    values="=[name, leer]"/>`,
		model.VariableValue{Name: "name", Kind: model.VarString, Text: "Anna"},
		model.VariableValue{Name: "leer", Kind: model.VarNull},
	)
	client := &recordingClient{}
	if _, err := gs.Handler(rd, lookup, registered(client))(job.Job{Key: 1, ElementInstanceKey: 42}); err != nil {
		t.Fatalf("handler: %v", err)
	}
	row := client.reqs[0].Values[0]
	if len(row) != 2 || row[0] != "Anna" || row[1] != nil {
		t.Errorf("row = %#v; want the name and an empty cell", row)
	}
}
