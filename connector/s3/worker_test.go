package s3_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/s3"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
)

// The handler's half of ADR-0168 for an object store: the task's values are
// resolved against the variables the task sees, the named Worker's client performs
// the operation, and what the store answered is written back as the result variable.

type s3Reader struct {
	ei      *model.ElementInstanceValue
	vars    []model.VariableValue
	err     error
	varsErr error
}

func (f *s3Reader) GetElementInstance(uint64) (*model.ElementInstanceValue, bool, error) {
	if f.err != nil {
		return nil, false, f.err
	}
	return f.ei, f.ei != nil, nil
}

func (f *s3Reader) VariablesOfScope(scope uint64, fn func(*model.VariableValue) error) error {
	if f.varsErr != nil {
		return f.varsErr
	}
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

type s3Recorder struct {
	reqs   []s3.Request
	result any
}

func (r *s3Recorder) Do(_ context.Context, req s3.Request) (any, error) {
	r.reqs = append(r.reqs, req)
	return r.result, nil
}

func s3Fixture(t *testing.T, connector string, vars ...model.VariableValue) (*s3Reader, s3.ProcessLookup) {
	t.Helper()
	bpmn := `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0">
  <bpmn:process id="p">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements>` + connector + `</bpmn:extensionElements></bpmn:serviceTask>
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
	rd := &s3Reader{
		ei:   &model.ElementInstanceValue{ProcessInstanceKey: 500, ProcessDefKey: 7, ElementId: task, FlowScopeKey: 500},
		vars: vars,
	}
	return rd, func(uint64) *compiler.CompiledProcess { return cp }
}

const s3Put = `<atlas:s3Connector connector="archiv" operation="put-object" bucket="rechnungen"
    key="=&quot;faelle/&quot; + vorgang + &quot;/antrag.txt&quot;" content="=inhalt" contentType="text/plain"
    resultVariable="ablage">
  <atlas:s3Meta name="fall" value="=vorgang"/>
  <atlas:s3Meta name="instanz" value="=processInstanceKey"/>
  <atlas:s3Meta name="dringend" value="=dringend"/>
  <atlas:s3Meta name="betrag" value="=betrag"/>
  <atlas:s3Meta name="daten" value="=daten"/>
  <atlas:s3Meta name="leer" value="=leer"/>
  <atlas:s3Meta name="fehlt" value="=gibtEsNicht"/>
  <atlas:s3Meta name="klasse" value="STANDARD_IA"/>
</atlas:s3Connector>`

func TestS3HandlerResolvesTheTaskAndWritesTheAnswerBack(t *testing.T) {
	rd, lookup := s3Fixture(t, s3Put,
		model.VariableValue{Name: "vorgang", Kind: model.VarString, Text: "4711"},
		model.VariableValue{Name: "inhalt", Kind: model.VarString, Text: "Antrag"},
		model.VariableValue{Name: "dringend", Kind: model.VarBool, Bool: true},
		model.VariableValue{Name: "betrag", Kind: model.VarNumber, Text: "12.5"},
		model.VariableValue{Name: "daten", Kind: model.VarJSON, Text: `{"a":1}`},
		model.VariableValue{Name: "leer", Kind: model.VarNull},
	)
	c := &s3Recorder{result: map[string]any{"etag": "abc", "versionId": nil}}
	reg := s3.NewRegistry()
	reg.Register("archiv", c)

	out, err := s3.Handler(rd, lookup, reg)(job.Job{Key: 9, ElementInstanceKey: 42})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if len(c.reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(c.reqs))
	}
	req := c.reqs[0]
	if req.Operation != "put-object" || req.Bucket != "rechnungen" || req.Key != "faelle/4711/antrag.txt" ||
		req.Content != "Antrag" || req.ContentType != "text/plain" {
		t.Errorf("request = %+v, want the resolved values", req)
	}
	want := map[string]string{"fall": "4711", "instanz": "500", "betrag": "12.5", "leer": "", "fehlt": "", "klasse": "STANDARD_IA"}
	for k, v := range want {
		if req.Metadata[k] != v {
			t.Errorf("metadata %s = %q, want %q", k, req.Metadata[k], v)
		}
	}
	if len(out) != 1 || out[0].Name != "ablage" || out[0].Kind != model.VarJSON {
		t.Fatalf("outputs = %+v, want the store's answer in \"ablage\"", out)
	}
}

// Each kind of answer keeps its kind as a variable; a delete answers with nothing
// and writes nothing.
func TestS3HandlerKeepsTheAnswersKind(t *testing.T) {
	for _, tc := range []struct {
		result any
		kind   model.VarKind
	}{
		{"https://example.test/signed", model.VarString},
		{true, model.VarBool},
		{float64(3), model.VarNumber},
		{[]any{"a"}, model.VarJSON},
	} {
		rd, lookup := s3Fixture(t, `<atlas:s3Connector connector="archiv" operation="head-object" bucket="b" key="k" resultVariable="r"/>`)
		reg := s3.NewRegistry()
		reg.Register("archiv", &s3Recorder{result: tc.result})
		out, err := s3.Handler(rd, lookup, reg)(job.Job{Key: 1, ElementInstanceKey: 42})
		if err != nil || len(out) != 1 || out[0].Kind != tc.kind {
			t.Errorf("result %#v = %+v, %v, want kind %v", tc.result, out, err, tc.kind)
		}
	}

	for _, task := range []string{
		`<atlas:s3Connector connector="archiv" operation="delete-object" bucket="b" key="k"/>`,
		`<atlas:s3Connector connector="archiv" operation="head-object" bucket="b" key="k" resultVariable="r"/>`,
	} {
		rd, lookup := s3Fixture(t, task)
		reg := s3.NewRegistry()
		reg.Register("archiv", &s3Recorder{})
		if out, err := s3.Handler(rd, lookup, reg)(job.Job{Key: 1, ElementInstanceKey: 42}); err != nil || out != nil {
			t.Errorf("an answer of nothing = %+v, %v, want nothing written", out, err)
		}
	}
}

// The handler's failures are not swallowed, and a task already gone is nothing to do.
func TestS3HandlerReportsWhatItCannotDo(t *testing.T) {
	task := `<atlas:s3Connector connector="archiv" operation="head-object" bucket="b" key="=k" resultVariable="r"/>`
	run := func(rd *s3Reader, lookup s3.ProcessLookup, reg *s3.Registry) ([]model.VariableValue, error) {
		return s3.Handler(rd, lookup, reg)(job.Job{Key: 1, ElementInstanceKey: 42})
	}

	rd, lookup := s3Fixture(t, task)
	if _, err := run(rd, lookup, s3.NewRegistry()); err == nil || !strings.Contains(err.Error(), "archiv") {
		t.Errorf("unconfigured Worker = %v, want it named", err)
	}

	rd, lookup = s3Fixture(t, task)
	rd.ei = nil
	if out, err := run(rd, lookup, s3.NewRegistry()); err != nil || out != nil {
		t.Errorf("gone element = %+v, %v", out, err)
	}

	rd, _ = s3Fixture(t, task)
	if _, err := run(rd, func(uint64) *compiler.CompiledProcess { return nil }, s3.NewRegistry()); err == nil {
		t.Error("a missing compiled process was not reported")
	}

	rd, lookup = s3Fixture(t, task)
	rd.err = errors.New("store down")
	if _, err := run(rd, lookup, s3.NewRegistry()); err == nil {
		t.Error("a failed element read was swallowed")
	}

	rd, lookup = s3Fixture(t, task)
	rd.varsErr = errors.New("vars down")
	if _, err := run(rd, lookup, s3.NewRegistry()); err == nil {
		t.Error("a failed variable read was swallowed")
	}

	if _, err := s3.Resolve(nil, nil, nil, nil, 0, 0); err == nil {
		t.Error("a task without detail resolved")
	}
}
