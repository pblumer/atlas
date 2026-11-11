package atlasreport

import (
	"context"
	"errors"
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
)

type fakeVarStore struct {
	vars map[uint64][]model.VariableValue
}

func (f fakeVarStore) VariablesOfScope(scope uint64, fn func(*model.VariableValue) error) error {
	for i := range f.vars[scope] {
		v := f.vars[scope][i]
		if err := fn(&v); err != nil {
			return err
		}
	}
	return nil
}

func (fakeVarStore) GetElementInstance(uint64) (*model.ElementInstanceValue, bool, error) {
	return nil, false, nil
}

func compileTask(t *testing.T, attrs string) (*compiler.CompiledProcess, *compiler.ConnectorTaskDetail) {
	t.Helper()
	bpmn := `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:atlas="http://atlas/schema/1.0" id="d">
  <bpmn:error id="err" errorCode="ATLASREPORT_NOT_FOUND"/>
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements><atlas:atlasReportConnector ` + attrs + `/></bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:boundaryEvent id="b" attachedToRef="t"><bpmn:errorEventDefinition errorRef="err"/></bpmn:boundaryEvent>
    <bpmn:endEvent id="e"/>
    <bpmn:endEvent id="e2"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
    <bpmn:sequenceFlow id="f3" sourceRef="b" targetRef="e2"/>
  </bpmn:process>
</bpmn:definitions>`
	cp, err := compiler.Parse(1, 1, strings.NewReader(bpmn))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	node := cp.Node(cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target)
	return cp, cp.ConnectorTask(node.Detail)
}

func input(json string) string { return `input="` + html.EscapeString(json) + `"` }

// The engine's half evaluates the worker name and the input against the scope, and the
// job carries the codes the process catches.
func TestResolveEvaluatesTheWorkerAndTheInput(t *testing.T) {
	cp, d := compileTask(t, `connector="=welcher" operation="process-statistics" resultVariable="bericht" `+
		input(`{"process": "=prozess", "to": "=bis", "buckets": "=anzahl", "bucket": "week", "language": "en", "title": "=titel"}`))
	store := fakeVarStore{vars: map[uint64][]model.VariableValue{10: {
		{Name: "welcher", Kind: model.VarString, Text: "zentrale"},
		{Name: "prozess", Kind: model.VarString, Text: "bestellung"},
		{Name: "bis", Kind: model.VarString, Text: "2026-11-01"},
		{Name: "anzahl", Kind: model.VarNumber, Text: "4"},
		{Name: "aktiv", Kind: model.VarBool, Bool: true},
	}}}
	j, err := Resolve(store, cp, d, 10)
	if err != nil {
		t.Fatal(err)
	}
	if j.Connector != "zentrale" || j.Process != "bestellung" || j.To != "2026-11-01" || j.Buckets != 4 ||
		j.Bucket != "week" || j.Language != "en" || j.Title != "" || j.ResultVariable != "bericht" {
		t.Errorf("job = %#v", j)
	}
	if len(j.ErrorCodes) != 1 || j.ErrorCodes[0] != CodeNotFound {
		t.Errorf("error codes = %v", j.ErrorCodes)
	}

	if _, err := Resolve(fakeVarStore{}, cp, d, 10); err == nil || !strings.Contains(err.Error(), "empty name") {
		t.Errorf("an unset worker variable: %v", err)
	}
	if _, err := Resolve(store, nil, d, 10); err == nil {
		t.Error("no compiled process was accepted")
	}

	// An operation that needs nothing compiles without an input, and resolves.
	cp, d = compileTask(t, `connector="atlas" operation="instances-summary" resultVariable="bericht"`)
	if j, err := Resolve(fakeVarStore{}, cp, d, 10); err != nil || j.Connector != "atlas" || j.Operation != "instances-summary" {
		t.Errorf("no input: %#v %v", j, err)
	}
	// One that needs a key is refused when its =expression made nothing.
	cp, d = compileTask(t, `connector="atlas" operation="process-statistics" resultVariable="bericht" `+input(`{"process": "=prozess"}`))
	if _, err := Resolve(fakeVarStore{}, cp, d, 10); err == nil || !strings.Contains(err.Error(), "process") {
		t.Errorf("a missing required value: %v", err)
	}
}

func TestResolveRefusesValuesADeployCouldNotSee(t *testing.T) {
	for name, in := range map[string]string{
		"bucket without buckets": `{"buckets": "=unset", "bucket": "week"}`,
		"a fraction":             `{"buckets": "=n", "bucket": "week"}`,
		"a bad bucket":           `{"buckets": 2, "bucket": "=b"}`,
		"a bad moment":           `{"to": "=b"}`,
		"a bad language":         `{"language": "=b"}`,
	} {
		t.Run(name, func(t *testing.T) {
			cp, d := compileTask(t, `connector="atlas" operation="process-statistics" resultVariable="r" `+
				input(strings.Replace(in, "{", `{"process": "p", `, 1)))
			store := fakeVarStore{vars: map[uint64][]model.VariableValue{10: {
				{Name: "b", Kind: model.VarString, Text: "gestern"},
				{Name: "n", Kind: model.VarNumber, Text: "2.5"},
			}}}
			if _, err := Resolve(store, cp, d, 10); err == nil {
				t.Error("accepted")
			}
		})
	}
}

// What a server answers besides a report is said as the job's failure, and a refusal or
// a 404 only becomes a BPMN error where the process catches it.
func TestTheClientSaysWhatTheServerAnswered(t *testing.T) {
	status, body := http.StatusOK, `[]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	c, err := NewClient(srv.URL+"/", `{"token":"atl"}`)
	if err != nil {
		t.Fatal(err)
	}
	reg := NewRegistry()
	reg.Register("atlas", c)
	j := Job{Connector: "atlas", Operation: "instances-summary", ResultVariable: "r"}

	for _, tc := range []struct {
		status    int
		body      string
		want      string
		kind      errKind
		errorCode string
	}{
		{http.StatusInternalServerError, strings.Repeat("x", 300), "answered 500", kindUnreachable, ""},
		{http.StatusNotFound, "no such route", "answered 404", kindNotFound, CodeNotFound},
		{http.StatusForbidden, "", "refused (403)", kindRefused, CodeRefused},
		{http.StatusOK, "not json", "not the expected JSON", 0, ""},
	} {
		status, body = tc.status, tc.body
		_, err := Run(context.Background(), j, reg)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%d: %v, want %q", tc.status, err, tc.want)
			continue
		}
		var ce *callError
		if tc.kind != 0 && (!errors.As(err, &ce) || ce.kind != tc.kind) {
			t.Errorf("%d: kind of %v", tc.status, err)
		}
		if tc.status == http.StatusInternalServerError && !strings.Contains(err.Error(), "…") {
			t.Errorf("a long answer was not clipped: %v", err)
		}
		if tc.errorCode != "" {
			caught := j
			caught.ErrorCodes = []string{tc.errorCode}
			if _, err := Run(context.Background(), caught, reg); err == nil || !strings.Contains(err.Error(), tc.errorCode) {
				t.Errorf("%d caught: %v", tc.status, err)
			}
		}
	}

	status, body = http.StatusOK, strings.Repeat(" ", maxAnswer+1)
	if _, err := Run(context.Background(), j, reg); err == nil || !strings.Contains(err.Error(), "more than") {
		t.Errorf("an oversized answer: %v", err)
	}

	srv.Close()
	if _, err := Run(context.Background(), j, reg); err == nil {
		t.Error("an unreachable server was not a failure")
	}
	if _, err := Run(context.Background(), Job{Connector: "andere", Operation: "instances-summary"}, reg); err == nil {
		t.Error("an unknown Worker was accepted")
	}
	if _, err := Run(context.Background(), Job{Connector: "atlas", Operation: "kaffee"}, reg); err == nil {
		t.Error("an unknown operation was accepted")
	}
	var none *Registry
	if _, ok := none.Get("atlas"); ok {
		t.Error("a nil registry holds a client")
	}
}

func TestAPeriodIsChecked(t *testing.T) {
	for _, j := range []Job{
		{To: "morgen"},
		{From: "gestern"},
		{From: "2026-11-02", To: "2026-11-01"},
	} {
		if _, _, err := period(j, now); err == nil {
			t.Errorf("period %+v was accepted", j)
		}
	}
}

func TestReportCheckHoldsTheContract(t *testing.T) {
	one := func(d Dataset) Report {
		return Report{Schema: Schema, Datasets: map[string]Dataset{"d": d}}
	}
	many := make([]string, MaxCategories+1)
	for _, r := range []Report{
		{Schema: "other/1", Datasets: map[string]Dataset{"d": valueSet("count", 1)}},
		{Schema: Schema},
		one(Dataset{Unit: "count", Categories: many, Series: []Series{{Name: "s", Values: make([]*float64, len(many))}}}),
		one(Dataset{Unit: "count", Categories: []string{"a"}}),
		one(Dataset{Unit: "count", Categories: []string{"a"}, Series: []Series{{Name: "s"}}}),
	} {
		if err := r.Check(); err == nil {
			t.Errorf("report %+v passed the check", r)
		}
	}
}
