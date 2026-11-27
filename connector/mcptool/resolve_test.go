package mcptool

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
)

// scopeStore is the two reads a task is resolved from: one element instance under one
// process instance, whose variables are the process's.
type scopeStore struct {
	vars []model.VariableValue
	fail error
}

const (
	testPI = uint64(1)
	testEI = uint64(10)
)

func (s scopeStore) VariablesOfScope(scope uint64, fn func(*model.VariableValue) error) error {
	if s.fail != nil {
		return s.fail
	}
	if scope != testPI {
		return nil
	}
	for i := range s.vars {
		if err := fn(&s.vars[i]); err != nil {
			return err
		}
	}
	return nil
}

func (s scopeStore) GetElementInstance(key uint64) (*model.ElementInstanceValue, bool, error) {
	if key != testEI {
		return nil, false, nil
	}
	return &model.ElementInstanceValue{ProcessInstanceKey: testPI, FlowScopeKey: testPI}, true, nil
}

func compiledTask(t *testing.T, attrs string) (*compiler.CompiledProcess, *compiler.ConnectorTaskDetail) {
	t.Helper()
	cp, err := compiler.Parse(1, 1, strings.NewReader(`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:atlas="http://atlas/schema/1.0">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/><endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="t"/><sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
	    <boundaryEvent id="b" attachedToRef="t"><errorEventDefinition errorRef="err"/></boundaryEvent>
	    <sequenceFlow id="f3" sourceRef="b" targetRef="e"/>
	    <serviceTask id="t"><extensionElements><atlas:mcpConnector `+attrs+`/></extensionElements></serviceTask>
	  </process>
	  <error id="err" errorCode="MCP_TOOL_ERROR"/>
	</definitions>`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	for id := int32(0); id < int32(cp.NodeCount()); id++ {
		if cp.ElementBpmnId(id) == "t" {
			return cp, cp.ConnectorTask(cp.Node(id).Detail)
		}
	}
	t.Fatal("the task is not in the compiled process")
	return nil, nil
}

// A task is resolved into what a worker needs and nothing else: the Worker's name, the
// tool, the arguments with their expressions evaluated against the variables the task
// sees, and the codes the process catches (ADR-0568).
func TestATaskIsResolvedAgainstTheVariablesItSees(t *testing.T) {
	cp, detail := compiledTask(t, `connector="=server" tool="search_issues" resultVariable="treffer" timeout="PT30S"
	  input='{"query":"=meldung","dringend":"=eilt","anzahl":"=limit","instanz":"=processInstanceKey","fest":7,"fehlt":"=gibtsnicht","kopf":"=daten.kopf"}'`)
	store := scopeStore{vars: []model.VariableValue{
		{Name: "server", Kind: model.VarString, Text: " monitoring "},
		{Name: "meldung", Kind: model.VarString, Text: "Portal langsam"},
		{Name: "eilt", Kind: model.VarBool, Bool: true},
		{Name: "limit", Kind: model.VarNumber, Text: "5"},
		{Name: "daten", Kind: model.VarJSON, Text: `{"kopf":"A"}`},
		{Name: "leer", Kind: model.VarNull},
	}}
	j, err := Resolve(store, cp, detail, testEI)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if j.Connector != "monitoring" || j.Tool != "search_issues" || j.ResultVariable != "treffer" || j.TimeoutMs != 30000 || j.Refused != "" {
		t.Errorf("job = %+v", j)
	}
	if len(j.ErrorCodes) != 1 || j.ErrorCodes[0] != CodeToolError {
		t.Errorf("error codes = %v, want the one the process catches", j.ErrorCodes)
	}
	got, _ := json.Marshal(j.Arguments)
	want := `{"anzahl":5,"dringend":true,"fehlt":null,"fest":7,"instanz":"1","kopf":"A","query":"Portal langsam"}`
	if string(got) != want {
		t.Errorf("arguments = %s\nwant        %s", got, want)
	}

	// A task that names its Worker outright and takes no arguments.
	cp, detail = compiledTask(t, `connector="monitoring" tool="ping"`)
	j, err = Resolve(scopeStore{}, cp, detail, testEI)
	if err != nil || j.Connector != "monitoring" || j.Arguments != nil || j.TimeoutMs != 0 || j.ResultVariable != "" {
		t.Errorf("job = %+v, %v", j, err)
	}
}

func TestATaskThatCannotBeResolvedSaysWhy(t *testing.T) {
	cp, detail := compiledTask(t, `connector="=server" tool="x"`)
	if _, err := Resolve(scopeStore{}, cp, detail, testEI); err == nil || !strings.Contains(err.Error(), "empty name") {
		t.Errorf("a Worker expression that names nobody: %v", err)
	}
	if _, err := Resolve(scopeStore{fail: errors.New("store closed")}, cp, detail, testEI); err == nil || !strings.Contains(err.Error(), "read variables") {
		t.Errorf("a store that cannot be read: %v", err)
	}
	if _, err := Resolve(scopeStore{}, cp, nil, testEI); err == nil {
		t.Error("a task with no detail was resolved")
	}
	if _, err := Resolve(scopeStore{}, nil, detail, testEI); err == nil {
		t.Error("a task with no process was resolved")
	}
}

// A server's list is read directly too — it is what a Worker's list of permitted tools
// will be filled in from — and paged through to its end.
func TestAModernServersToolsAreListed(t *testing.T) {
	pages := 0
	f := newFake(t, func(_ *fakeServer, w http.ResponseWriter, r *http.Request, req rpcIn) {
		pages++
		if req.Params["cursor"] == nil {
			rpcResult(w, req.ID, `{"tools":[{"name":"a","annotations":{"readOnlyHint":true}}],"nextCursor":"p2"}`)
			return
		}
		rpcResult(w, req.ID, `{"tools":[{"name":"b"}]}`)
	})
	c := &Client{Endpoint: f.srv.URL}
	tools, err := c.ListTools(context.Background())
	if err != nil || len(tools) != 2 || tools[0].Name != "a" || tools[1].Name != "b" || pages != 2 {
		t.Fatalf("ListTools = %+v, %v after %d pages", tools, err, pages)
	}
	if h := tools[0].Annotations.ReadOnlyHint; h == nil || !*h {
		t.Errorf("the server's hint was not read: %+v", tools[0].Annotations)
	}
	if got := f.calls("tools/list")[1].Params["cursor"]; got != "p2" {
		t.Errorf("the second page was asked for with cursor %v", got)
	}
}

// What can go wrong while a server is being listed or a session opened is an error that
// says so, never a silent empty list.
func TestListingAndSessionFailuresAreErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		handle func(w http.ResponseWriter, r *http.Request, req rpcIn)
		want   string
	}{
		"a modern refusal": {func(w http.ResponseWriter, _ *http.Request, req rpcIn) {
			rpcFail(w, 400, req.ID, codeUnsupportedVersion, "UnsupportedProtocolVersion")
		}, "error -32022"},
		"a list that is no list": {func(w http.ResponseWriter, _ *http.Request, req rpcIn) { rpcResult(w, req.ID, `"x"`) }, "decode the tool list"},
		"a server error":         {func(w http.ResponseWriter, _ *http.Request, _ rpcIn) { w.WriteHeader(500) }, "answered 500"},
		"initialize refused": {func(w http.ResponseWriter, _ *http.Request, req rpcIn) {
			rpcFail(w, 400, req.ID, -32000, "no")
		}, "error -32000"},
		"initialize answers nonsense": {func(w http.ResponseWriter, _ *http.Request, req rpcIn) {
			if req.Method == "initialize" {
				rpcResult(w, req.ID, `"x"`)
				return
			}
			rpcFail(w, 400, req.ID, -32000, "no session")
		}, "decode the initialize result"},
		"a legacy list that fails": {func(w http.ResponseWriter, _ *http.Request, req rpcIn) {
			switch {
			case req.Method == "initialize":
				rpcResult(w, req.ID, `{"protocolVersion":"2025-11-25"}`)
			case req.Method == "tools/list" && req.Params["_meta"] != nil:
				rpcFail(w, 400, req.ID, -32000, "no session")
			case req.Method == "tools/list":
				rpcFail(w, 500, req.ID, -32603, "kaputt")
			default:
				w.WriteHeader(http.StatusAccepted)
			}
		}, "error -32603: kaputt"},
		"a legacy list that is no list": {func(w http.ResponseWriter, _ *http.Request, req rpcIn) {
			switch {
			case req.Method == "initialize":
				rpcResult(w, req.ID, `{"protocolVersion":"2025-11-25"}`)
			case req.Method == "tools/list" && req.Params["_meta"] != nil:
				rpcFail(w, 400, req.ID, -32000, "no session")
			case req.Method == "tools/list":
				rpcResult(w, req.ID, `7`)
			default:
				w.WriteHeader(http.StatusAccepted)
			}
		}, "decode the tool list"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFake(t, func(_ *fakeServer, w http.ResponseWriter, r *http.Request, req rpcIn) { tc.handle(w, r, req) })
			if _, err := (&Client{Endpoint: f.srv.URL}).ListTools(context.Background()); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want one naming %q", err, tc.want)
			}
		})
	}
	// A legacy server whose call fails after the session opened, and one that assigns no
	// session id at all — which a stateless server may do, and which needs no ending.
	f := newFake(t, func(_ *fakeServer, w http.ResponseWriter, r *http.Request, req rpcIn) {
		switch {
		case req.Method == "initialize":
			rpcResult(w, req.ID, `{"protocolVersion":"2025-03-26"}`)
		case req.Params["_meta"] != nil:
			rpcFail(w, 400, req.ID, -32000, "legacy")
		case req.Method == "tools/call":
			rpcFail(w, 400, req.ID, -32602, "Unknown tool")
		case req.Method == "tools/list":
			rpcResult(w, req.ID, `{"tools":[]}`)
		default:
			w.WriteHeader(http.StatusAccepted)
		}
	})
	c := &Client{Endpoint: f.srv.URL}
	if _, err := c.CallTool(context.Background(), "nope", nil); err == nil || !strings.Contains(err.Error(), "Unknown tool") {
		t.Errorf("a legacy call that is refused: %v", err)
	}
	f.mu.Lock()
	for _, r := range f.seen {
		if r.HTTP == http.MethodDelete {
			t.Error("a session that was never given an id was ended")
		}
	}
	f.mu.Unlock()
}
