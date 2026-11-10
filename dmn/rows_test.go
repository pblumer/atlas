package dmn_test

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/dmn"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// fakeRowDocs is a document store with one file in it, which records what is stored.
type fakeRowDocs struct {
	files    map[string]string
	readErr  error
	storeErr error
	stored   []byte
	name     string
	readFor  uint64
	// all holds every document stored, by name; stored and name are the first, which
	// is the result document.
	all map[string][]byte
}

func (f *fakeRowDocs) ReadDocument(instance uint64, id string) ([]byte, error) {
	f.readFor = instance
	if f.readErr != nil {
		return nil, f.readErr
	}
	text, ok := f.files[id]
	if !ok {
		return nil, errors.New("no such document")
	}
	return []byte(text), nil
}

func (f *fakeRowDocs) StoreDocument(_ context.Context, name, contentType string, data []byte) (map[string]any, error) {
	if f.storeErr != nil {
		return nil, f.storeErr
	}
	if f.all == nil {
		f.all = map[string][]byte{}
	}
	f.all[name] = append([]byte(nil), data...)
	if f.name == "" {
		f.stored, f.name = append([]byte(nil), data...), name
	}
	return map[string]any{"$document": "doc_result", "name": name, "contentType": contentType}, nil
}

// rowsRun is one business rule task over a file, run through the engine and the
// in-process DMN worker.
type rowsRun struct {
	summary map[string]any
	evals   []*model.DecisionEvaluationValue
	message string // the incident's, when the run failed
}

const rowsCSV = "email,group,license\nada@x.io,users,PRO\nbob,users,PRO\ncy@x.io,ops,NONE\ndi@x.io,users,BASIC\n"

func runRows(t *testing.T, rowsAttrs, mappings string, source model.VariableValue, docs *fakeRowDocs, opts func(*dmn.RowsOptions)) rowsRun {
	t.Helper()
	dir := t.TempDir()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close(); log.Close() })

	bpmn := `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="p" isExecutable="true">
    <startEvent id="s"/>
    <businessRuleTask id="check">
      <extensionElements>
        <calledDecision decisionId="RowValid" resultVariable="pruefung" retries="1"/>
        <rowsFrom ` + rowsAttrs + `/>
        ` + mappings + `
      </extensionElements>
    </businessRuleTask>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="check"/>
    <sequenceFlow id="f2" sourceRef="check" targetRef="e"/>
  </process>
</definitions>`
	cp, err := compiler.Parse(dishDefKey, 1, strings.NewReader(bpmn))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	model2, err := os.ReadFile(filepath.Join("..", "examples", "pruefe-datensaetze.dmn"))
	if err != nil {
		t.Fatal(err)
	}
	reg := dmn.NewRegistry()
	if err := reg.Deploy(cp.Key, model2); err != nil {
		t.Fatal(err)
	}
	p := engine.New(1, log, store, &fixedClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatal(err)
	}
	o := dmn.RowsOptions{}
	if docs != nil {
		o.Docs = docs
	}
	if opts != nil {
		opts(&o)
	}
	runner := job.NewRunner(store, p)
	runner.HandleCompleting(compiler.DMNJobTypeIndex, func(rd state.Reader) job.CompletingHandler {
		return dmn.HandlerWithRows(store, func(uint64) *compiler.CompiledProcess { return cp }, reg, nil, o)
	})
	p.CreateInstance(cp.Key, source)
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}

	var run rowsRun
	if v := rootVar(t, store, model.NewKey(1, 1), "pruefung"); v != nil {
		if err := json.Unmarshal([]byte(v.Text), &run.summary); err != nil {
			t.Fatalf("summary %q: %v", v.Text, err)
		}
	}
	_ = store.EachDecisionEvaluation(func(_ uint64, _ int64, v *model.DecisionEvaluationValue) error {
		c := *v
		run.evals = append(run.evals, &c)
		return nil
	})
	_ = store.Incidents(func(_ uint64, v *model.IncidentValue) error {
		run.message = v.Message
		return nil
	})
	return run
}

func docVar(name string) model.VariableValue {
	return model.VariableValue{Name: "antrag", Kind: model.VarJSON, Text: `{"$document":"doc_src","name":"` + name + `","contentType":"text/csv"}`}
}

// resultRows reads a document the check wrote. Each one opens with a UTF-8 byte-order
// mark, which Excel needs to read the umlauts right.
func resultRows(t *testing.T, data []byte) [][]string {
	t.Helper()
	text, ok := strings.CutPrefix(string(data), "\uFEFF")
	if !ok {
		t.Fatalf("result document has no byte-order mark: %q", string(data[:min(len(data), 20)]))
	}
	rows, err := csv.NewReader(strings.NewReader(text)).ReadAll()
	if err != nil {
		t.Fatalf("result document: %v\n%s", err, data)
	}
	return rows
}

// A business rule task over a file evaluates the decision once per record, completes
// with a summary and a result document, and leaves one evaluation record — not one per
// record (ADR-0548).
func TestBusinessRuleTaskOverAFile(t *testing.T) {
	docs := &fakeRowDocs{files: map[string]string{"doc_src": rowsCSV}}
	run := runRows(t, `source="antrag" failureSample="200"`, "", docVar("antrag.csv"), docs, nil)
	if run.message != "" {
		t.Fatalf("incident: %s", run.message)
	}
	s := run.summary
	if s["total"] != 4.0 || s["valid"] != 2.0 || s["invalid"] != 2.0 || s["errors"] != 0.0 {
		t.Fatalf("summary = %v", s)
	}
	if s["result"].(map[string]any)["$document"] != "doc_result" || s["source"].(map[string]any)["$document"] != "doc_src" {
		t.Fatalf("references = %v / %v", s["result"], s["source"])
	}
	if d := s["decision"].(map[string]any); d["id"] != "RowValid" {
		t.Fatalf("decision = %v", d)
	}
	failures := s["failures"].([]any)
	if len(failures) != 2 {
		t.Fatalf("failures = %v", failures)
	}
	first := failures[0].(map[string]any)
	// The record's line, its fields and the decision's other outputs as reasons.
	if first["_row"] != 3.0 || first["email"] != "bob" || first["_emailOk"] != false || first["_groupOk"] != true {
		t.Fatalf("first failure = %v", first)
	}
	if docs.name != "antrag.result.csv" || docs.readFor != model.NewKey(1, 1) {
		t.Fatalf("stored %q, read for %d", docs.name, docs.readFor)
	}
	rows := resultRows(t, docs.stored)
	if strings.Join(rows[0], ",") != "email,group,license,_row,_valid,_emailOk,_groupOk,_licenseOk,_error" {
		t.Fatalf("header = %v", rows[0])
	}
	if strings.Join(rows[2], ",") != "bob,users,PRO,3,false,false,true,true," || len(rows) != 5 {
		t.Fatalf("result rows = %v", rows)
	}
	// The failures document is the result's layout, filtered to the records that
	// failed: the file a person downloads, corrects and uploads again (S2).
	if fd, _ := s["failuresDocument"].(map[string]any); fd["name"] != "antrag.failures.csv" {
		t.Fatalf("failures document = %v", s["failuresDocument"])
	}
	failed := resultRows(t, docs.all["antrag.failures.csv"])
	if len(failed) != 3 || strings.Join(failed[0], ",") != strings.Join(rows[0], ",")+",_drop" || failed[1][3] != "3" || failed[2][3] != "4" {
		t.Fatalf("failures document rows = %v", failed)
	}
	if len(run.evals) != 1 {
		t.Fatalf("evaluation records = %d, want one for the run", len(run.evals))
	}
	if e := run.evals[0]; !strings.Contains(e.InputsJSON, `"records":4`) || !strings.Contains(e.OutputsJSON, `"invalid":2`) || e.TraceJSON != "" {
		t.Fatalf("record = %+v", e)
	}
}

// Input mappings see the record as `row`, beside the instance's variables, and a file
// without a header row is read by the columns the task names. A sample smaller than
// the failures says the rest are in the document.
func TestBusinessRuleTaskOverAFileWithMappings(t *testing.T) {
	docs := &fakeRowDocs{files: map[string]string{"doc_src": "ADA@X.IO;users;pro\nbob;users;pro\n"}}
	mappings := `<ioMapping>
          <input source="=lower case(row.mail)" target="email"/>
          <input source="=row.grp" target="group"/>
          <input source="=upper case(row.lic)" target="license"/>
        </ioMapping>`
	run := runRows(t, `source="antrag" delimiter=";" hasHeader="false" columns="mail,grp,lic" failureSample="0"`, mappings, docVar("liste"), docs, nil)
	if run.message != "" {
		t.Fatalf("incident: %s", run.message)
	}
	if run.summary["valid"] != 1.0 || run.summary["invalid"] != 1.0 || run.summary["truncated"] != true || len(run.summary["failures"].([]any)) != 0 {
		t.Fatalf("summary = %v", run.summary)
	}
	rows := resultRows(t, []byte(strings.ReplaceAll(string(docs.stored), ";", ",")))
	if rows[0][0] != "mail" || rows[1][3] != "1" || rows[1][4] != "true" || docs.name != "liste.result.csv" {
		t.Fatalf("headerless result = %v as %q", rows, docs.name)
	}
}

// A verdict the decision does not give is an error on that record, counted and
// written, not a failure of the run.
func TestBusinessRuleTaskOverAFileRecordErrors(t *testing.T) {
	docs := &fakeRowDocs{files: map[string]string{"doc_src": rowsCSV}}
	run := runRows(t, `source="antrag" validOutput="nope"`, "", docVar("antrag.csv"), docs, func(o *dmn.RowsOptions) { o.MaxSummary = 400 })
	if run.message != "" {
		t.Fatalf("incident: %s", run.message)
	}
	if run.summary["errors"] != 4.0 || run.summary["truncated"] != true {
		t.Fatalf("summary = %v", run.summary)
	}
	rows := resultRows(t, docs.stored)
	if !strings.Contains(rows[1][len(rows[1])-1], `"nope" is not true or false`) {
		t.Fatalf("error column = %v", rows[1])
	}
}

// A file whose records all pass has no failures document: there is nothing to correct.
// Records the decision could not evaluate are failures too, and land in it.
func TestBusinessRuleTaskOverAFileFailuresDocument(t *testing.T) {
	docs := &fakeRowDocs{files: map[string]string{"doc_src": "email,group,license\nada@x.io,users,PRO\n"}}
	run := runRows(t, `source="antrag"`, "", docVar("antrag.csv"), docs, nil)
	if _, ok := run.summary["failuresDocument"]; ok || len(docs.all) != 1 {
		t.Fatalf("summary = %v, stored %d documents", run.summary, len(docs.all))
	}
	docs = &fakeRowDocs{files: map[string]string{"doc_src": rowsCSV}}
	run = runRows(t, `source="antrag" validOutput="nope"`, "", docVar("antrag.csv"), docs, nil)
	if failed := resultRows(t, docs.all["antrag.failures.csv"]); len(failed) != 5 {
		t.Fatalf("failures document = %v (summary %v)", failed, run.summary)
	}
}

// What stops a run is said in the incident, and no summary is written.
func TestBusinessRuleTaskOverAFileFails(t *testing.T) {
	good := func() *fakeRowDocs { return &fakeRowDocs{files: map[string]string{"doc_src": rowsCSV}} }
	for _, tc := range []struct {
		name   string
		source model.VariableValue
		docs   *fakeRowDocs
		want   string
	}{
		{"no store", docVar("a.csv"), nil, "no document store"},
		{"no variable", model.VariableValue{Name: "other", Kind: model.VarString, Text: "x"}, good(), `"antrag" is missing`},
		{"not a document", model.VariableValue{Name: "antrag", Kind: model.VarString, Text: "a,b"}, good(), "holds no document"},
		{"no document at the path", model.VariableValue{Name: "antrag", Kind: model.VarJSON, Text: `{"$document":"doc_src"}`}, good(), "holds no document"},
		{"unreadable", docVar("a.csv"), &fakeRowDocs{readErr: errors.New("gone")}, "gone"},
		{"not CSV for the layout", docVar("a.csv"), &fakeRowDocs{files: map[string]string{"doc_src": ""}}, "CSV is empty"},
		{"cannot store", docVar("a.csv"), &fakeRowDocs{files: map[string]string{"doc_src": rowsCSV}, storeErr: errors.New("disk full")}, "disk full"},
	} {
		attrs := `source="antrag"`
		if tc.name == "no document at the path" {
			attrs = `source="antrag.document"`
		}
		run := runRows(t, attrs, "", tc.source, tc.docs, nil)
		if run.summary != nil || !strings.Contains(run.message, tc.want) {
			t.Errorf("%s: summary=%v incident=%q, want %q", tc.name, run.summary, run.message, tc.want)
		}
	}
}

// The file can be named at a field inside a variable — where an AD search written as a
// document puts its reference (docs/planning/ad-search-as-document.md).
func TestBusinessRuleTaskOverAFileAtAPath(t *testing.T) {
	docs := &fakeRowDocs{files: map[string]string{"doc_src": rowsCSV}}
	source := model.VariableValue{Name: "benutzer", Kind: model.VarJSON, Text: `{"found":true,"count":4,"document":{"$document":"doc_src","name":"ad-search.csv"}}`}
	run := runRows(t, `source="benutzer.document"`, "", source, docs, nil)
	if run.message != "" || run.summary["total"] != 4.0 || docs.name != "ad-search.result.csv" {
		t.Fatalf("summary = %v, incident %q, stored %q", run.summary, run.message, docs.name)
	}
}
