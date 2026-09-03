package googlesheets_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	gs "github.com/pblumer/atlas/connector/googlesheets"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
)

// fakeReader stands in for the state store: one element instance and the variables its
// scope holds.
type fakeReader struct {
	ei   *model.ElementInstanceValue
	vars []model.VariableValue
}

func (f *fakeReader) GetElementInstance(uint64) (*model.ElementInstanceValue, bool, error) {
	return f.ei, f.ei != nil, nil
}

func (f *fakeReader) VariablesOfScope(scope uint64, fn func(*model.VariableValue) error) error {
	if f.ei == nil || scope != f.ei.ProcessInstanceKey {
		return nil
	}
	for i := range f.vars {
		if err := fn(&f.vars[i]); err != nil {
			return err
		}
	}
	return nil
}

// recordingClient captures what the worker resolved and answers with a canned result.
type recordingClient struct {
	reqs   []gs.Request
	result any
	files  []map[string]any
	err    error
}

func (r *recordingClient) Do(_ context.Context, req gs.Request) (any, error) {
	r.reqs = append(r.reqs, req)
	return r.result, r.err
}

// ListFiles is the inbound half of the interface; the outbound handler never calls it.
func (r *recordingClient) ListFiles(context.Context, gs.FileQuery) ([]map[string]any, error) {
	return r.files, r.err
}

// workerFixture compiles a one-task process and returns the pieces a handler needs.
func workerFixture(t *testing.T, inner string, vars ...model.VariableValue) (*fakeReader, gs.ProcessLookup) {
	t.Helper()
	bpmn := `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0">
  <bpmn:process id="p">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements>` + inner + `</bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
	cp, err := compiler.Parse(7, 1, strings.NewReader(bpmn))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	rd := &fakeReader{
		// FlowScopeKey is the process-instance scope: the chain the handler walks to
		// read the variables the task sees (ADR-0068) ends there.
		ei:   &model.ElementInstanceValue{ProcessInstanceKey: 500, ProcessDefKey: 7, ElementId: task, FlowScopeKey: 500},
		vars: vars,
	}
	return rd, func(uint64) *compiler.CompiledProcess { return cp }
}

func registered(client gs.Client) *gs.Registry {
	reg := gs.NewRegistry()
	reg.Register("acme", client)
	return reg
}

// TestHandlerResolvesAndWritesBack is the whole worker path: the FEEL values are
// evaluated over the variables the task sees, the rows are projected out of the shape
// the process held, and what Google returned lands in the result variable.
func TestHandlerResolvesAndWritesBack(t *testing.T) {
	rd, lookup := workerFixture(t,
		`<atlas:googleSheetsConnector connector="acme" operation="append-row" spreadsheet="=datei"
		    range="Anträge!A:C" values="=zeilen" columns="name,betrag" resultVariable="ergebnis"/>`,
		model.VariableValue{Name: "datei", Kind: model.VarString, Text: "https://docs.google.com/spreadsheets/d/1Bxi/edit"},
		model.VariableValue{Name: "zeilen", Kind: model.VarJSON, Text: `[{"name":"Anna","betrag":42},{"name":"Bo","betrag":7}]`},
	)
	client := &recordingClient{result: map[string]any{"updates": map[string]any{"updatedRows": 2}}}
	out, err := gs.Handler(rd, lookup, registered(client))(job.Job{Key: 9, ElementInstanceKey: 42})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if len(client.reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(client.reqs))
	}
	req := client.reqs[0]
	// The URL a person pasted is reduced to the id before it ever reaches Google.
	if req.Spreadsheet != "1Bxi" {
		t.Errorf("spreadsheet = %q, want the id extracted from the pasted URL", req.Spreadsheet)
	}
	if req.Operation != "append-row" || req.Range != "Anträge!A:C" {
		t.Errorf("request = %+v, want the resolved operation and range", req)
	}
	if len(req.Values) != 2 {
		t.Fatalf("values = %#v, want two rows", req.Values)
	}
	if req.Values[0][0] != "Anna" {
		t.Errorf("row 0 = %#v, want the columns in the authored order", req.Values[0])
	}
	if n, ok := req.Values[0][1].(json.Number); !ok || n.String() != "42" {
		t.Errorf("row 0 amount = %#v, want the JSON number 42 — a number written as text is not a number in the sheet", req.Values[0][1])
	}
	// The compiler's default reaches the call; the runtime interprets nothing (I5).
	if req.Input != gs.InputUser {
		t.Errorf("input = %q, want the compiled-in user default", req.Input)
	}
	if req.RequestID != "9" {
		t.Errorf("requestID = %q, want the job key", req.RequestID)
	}
	if len(out) != 1 || out[0].Name != "ergebnis" || out[0].Kind != model.VarJSON {
		t.Fatalf("outputs = %+v, want what Google returned in \"ergebnis\"", out)
	}
}

// TestHandlerProjectsAScalarRow: a FEEL list of scalars is one row, which is the shape
// a process holds when it has already assembled the cells.
func TestHandlerProjectsAScalarRow(t *testing.T) {
	rd, lookup := workerFixture(t,
		`<atlas:googleSheetsConnector connector="acme" operation="write-range" spreadsheet="1B"
		    range="A1:C1" values="=[name, betrag, true]" valueInput="raw"/>`,
		model.VariableValue{Name: "name", Kind: model.VarString, Text: "Anna"},
		model.VariableValue{Name: "betrag", Kind: model.VarNumber, Text: "42"},
	)
	client := &recordingClient{}
	if _, err := gs.Handler(rd, lookup, registered(client))(job.Job{Key: 1, ElementInstanceKey: 42}); err != nil {
		t.Fatalf("handler: %v", err)
	}
	req := client.reqs[0]
	if len(req.Values) != 1 || len(req.Values[0]) != 3 {
		t.Fatalf("values = %#v, want one row of three cells", req.Values)
	}
	if req.Values[0][2] != true {
		t.Errorf("cell 2 = %#v, want the boolean true", req.Values[0][2])
	}
	if req.Input != gs.InputRaw {
		t.Errorf("input = %q, want raw", req.Input)
	}
}

// TestHandlerFailsAValuesShapeItCannotWrite: the refusal belongs to the resolve step,
// where the message can name the fix, rather than to Google as a 400 on a request the
// client should never have built.
func TestHandlerFailsAValuesShapeItCannotWrite(t *testing.T) {
	rd, lookup := workerFixture(t,
		`<atlas:googleSheetsConnector connector="acme" operation="append-row" spreadsheet="1B" range="A:C" values="=zeilen"/>`,
		model.VariableValue{Name: "zeilen", Kind: model.VarJSON, Text: `[{"name":"Anna"}]`},
	)
	client := &recordingClient{}
	_, err := gs.Handler(rd, lookup, registered(client))(job.Job{Key: 1, ElementInstanceKey: 42})
	if err == nil {
		t.Fatal("a list of objects with no columns: want an error, got nil")
	}
	if !strings.Contains(err.Error(), "columns") {
		t.Errorf("error %q should name the fix", err)
	}
	if len(client.reqs) != 0 {
		t.Errorf("made %d calls on an unwritable shape; want none", len(client.reqs))
	}
}

// TestHandlerDiscardsWhenNoResultVariable: the two deletes answer with nothing a model
// keeps, and an operation whose answer the model discards must not invent a variable.
func TestHandlerDiscardsWhenNoResultVariable(t *testing.T) {
	rd, lookup := workerFixture(t,
		`<atlas:googleSheetsConnector connector="acme" operation="delete-sheet" spreadsheet="1B" sheet="Alt"/>`)
	client := &recordingClient{result: map[string]any{"replies": []any{}}}
	out, err := gs.Handler(rd, lookup, registered(client))(job.Job{Key: 1, ElementInstanceKey: 42})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("outputs = %+v, want none", out)
	}
	if client.reqs[0].Sheet != "Alt" {
		t.Errorf("sheet = %q, want the authored tab", client.reqs[0].Sheet)
	}
}

// TestHandlerUnknownWorkerSaysSo: the name in the model and the names the server
// holds have drifted, and that is the actionable half of the failure (ADR-0158).
func TestHandlerUnknownWorkerSaysSo(t *testing.T) {
	rd, lookup := workerFixture(t,
		`<atlas:googleSheetsConnector connector="woanders" operation="clear-range" spreadsheet="1B" range="A2:F"/>`)
	_, err := gs.Handler(rd, lookup, registered(&recordingClient{}))(job.Job{Key: 1, ElementInstanceKey: 42})
	if err == nil || !strings.Contains(err.Error(), "woanders") {
		t.Errorf("unknown Worker: want an error naming it, got %v", err)
	}
}

// TestHandlerOnAVanishedElementInstanceDoesNothing: the token has already moved on, so
// there is no task to perform and nothing to fail.
func TestHandlerOnAVanishedElementInstanceDoesNothing(t *testing.T) {
	rd, lookup := workerFixture(t,
		`<atlas:googleSheetsConnector connector="acme" operation="clear-range" spreadsheet="1B" range="A2:F"/>`)
	rd.ei = nil
	client := &recordingClient{}
	out, err := gs.Handler(rd, lookup, registered(client))(job.Job{Key: 1, ElementInstanceKey: 42})
	if err != nil || out != nil {
		t.Errorf("handler on a gone element instance = %v, %v; want nothing", out, err)
	}
	if len(client.reqs) != 0 {
		t.Errorf("made %d calls for an element instance that is gone", len(client.reqs))
	}
}

// TestHandlerWithoutACompiledProcess: a definition key nothing resolves is a failure
// the job must keep, not one it may complete over.
func TestHandlerWithoutACompiledProcess(t *testing.T) {
	rd, _ := workerFixture(t,
		`<atlas:googleSheetsConnector connector="acme" operation="clear-range" spreadsheet="1B" range="A2:F"/>`)
	none := func(uint64) *compiler.CompiledProcess { return nil }
	if _, err := gs.Handler(rd, none, registered(&recordingClient{}))(job.Job{Key: 1, ElementInstanceKey: 42}); err == nil {
		t.Error("no compiled process: want an error, got nil")
	}
}

// TestResolveWithoutDetail: the one nil the resolve step can be handed, named rather
// than dereferenced.
func TestResolveWithoutDetail(t *testing.T) {
	rd, lookup := workerFixture(t,
		`<atlas:googleSheetsConnector connector="acme" operation="clear-range" spreadsheet="1B" range="A2:F"/>`)
	if _, err := gs.Resolve(rd, lookup(7), nil, rd.ei, 42, 1); err == nil {
		t.Error("no detail: want an error, got nil")
	}
}

// TestHandlerUnevaluableExpressionResolvesEmpty: a FEEL expression that cannot be
// evaluated resolves to empty rather than failing the job, matching the engine's
// null-propagating contract — the rule the REST, SharePoint and Jira workers follow.
func TestHandlerUnevaluableExpressionResolvesEmpty(t *testing.T) {
	rd, lookup := workerFixture(t,
		`<atlas:googleSheetsConnector connector="acme" operation="clear-range" spreadsheet="=zahl + 1" range="A2:F"/>`,
		model.VariableValue{Name: "zahl", Kind: model.VarString, Text: "nicht zahl"},
	)
	client := &recordingClient{}
	if _, err := gs.Handler(rd, lookup, registered(client))(job.Job{Key: 1, ElementInstanceKey: 42}); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if client.reqs[0].Spreadsheet != "" {
		t.Errorf("spreadsheet = %q, want empty for an expression that cannot be evaluated", client.reqs[0].Spreadsheet)
	}
}
