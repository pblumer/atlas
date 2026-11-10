package csvimport

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
)

type corrDocs struct {
	files    map[string]string
	stored   map[string]string
	limit    int64
	storeErr error
}

func (d *corrDocs) ReadDocument(_ context.Context, id string) ([]byte, string, string, error) {
	text, ok := d.files[id]
	if !ok {
		return nil, "", "", errors.New("no such document")
	}
	return []byte(text), id + ".csv", "text/csv", nil
}

func (d *corrDocs) StoreDocument(_ context.Context, name, _ string, data []byte) (map[string]any, error) {
	if d.storeErr != nil {
		return nil, d.storeErr
	}
	if d.stored == nil {
		d.stored = map[string]string{}
	}
	d.stored[name] = string(data)
	return map[string]any{"$document": "doc_out", "name": name}, nil
}

func (d *corrDocs) DocumentLimit() int64 { return d.limit }

// The line numbers a check reports are the file's own lines: the header is line 1.
const staff = "personalnummer;name;mail;pensum\n100001;Anna;anna@x.ch;80\n100002;Beat;;100\n100003;Corinne;cori@gmail.com;0\n100004;Dominik;dom@x.ch;60\n"

func corrJob(corr []map[string]any, drop []int) Job {
	return Job{Operation: OperationApplyCorrections, SourceDocument: "staff", Delimiter: ";", Result: "korrigiert", Corrections: corr, Drop: drop}
}

func readBack(t *testing.T, text string) [][]string {
	t.Helper()
	r := csv.NewReader(strings.NewReader(strings.TrimPrefix(text, "\uFEFF")))
	r.Comma = ';'
	rows, err := r.ReadAll()
	if err != nil {
		t.Fatalf("corrected file is not CSV: %v\n%s", err, text)
	}
	return rows
}

// A correction replaces the fields it names on the line it names and keeps the rest;
// a dropped line is gone; the check's own fields are ignored; the source is untouched.
func TestApplyCorrectionsReplacesAndDrops(t *testing.T) {
	docs := &corrDocs{files: map[string]string{"staff": staff}}
	out, err := ApplyCorrections(context.Background(), corrJob([]map[string]any{
		{"_row": 3.0, "mail": "beat@x.ch", "_mailGueltig": false, "_valid": false},
		{"_row": 4.0, "mail": "corinne@x.ch", "pensum": 40.0, "name": "Corinne"},
	}, []int{5}), docs)
	if err != nil {
		t.Fatal(err)
	}
	res := out["korrigiert"].(map[string]any)
	if res["total"] != 4 || res["replaced"] != 2 || res["dropped"] != 1 || res["records"] != 3 {
		t.Fatalf("result = %v", res)
	}
	if docs.stored["staff.corrected.csv"] == "" || res["document"].(map[string]any)["$document"] != "doc_out" {
		t.Fatalf("stored = %v", docs.stored)
	}
	got := readBack(t, docs.stored["staff.corrected.csv"])
	want := "personalnummer;name;mail;pensum/100001;Anna;anna@x.ch;80/100002;Beat;beat@x.ch;100/100003;Corinne;corinne@x.ch;40"
	var lines []string
	for _, r := range got {
		lines = append(lines, strings.Join(r, ";"))
	}
	if strings.Join(lines, "/") != want {
		t.Fatalf("corrected file = %s", strings.Join(lines, "/"))
	}
}

// A corrected file — the failures document after a person changed it — works as the
// list does: cells as text, _row and _drop as written, the check's columns ignored. A
// source with a byte-order mark keeps it.
func TestApplyCorrectionsFromAFile(t *testing.T) {
	docs := &corrDocs{files: map[string]string{
		"staff": "\uFEFF" + staff,
		"fixed": "personalnummer;name;mail;pensum;_row;_valid;_mailGueltig;_drop\n100002;Beat;beat@x.ch;100;3;false;false;\n100003;Corinne;cori@gmail.com;0;4;false;true;x\n",
	}}
	j := corrJob(nil, nil)
	j.CorrectionsDocument, j.DocumentName = "fixed", "mitarbeitende-korrigiert.csv"
	out, err := ApplyCorrections(context.Background(), j, docs)
	if err != nil {
		t.Fatal(err)
	}
	if res := out["korrigiert"].(map[string]any); res["replaced"] != 1 || res["dropped"] != 1 {
		t.Fatalf("result = %v", res)
	}
	text := docs.stored["mitarbeitende-korrigiert.csv"]
	if !strings.HasPrefix(text, "\uFEFF") {
		t.Error("the byte-order mark of the source was dropped")
	}
	if rows := readBack(t, text); len(rows) != 4 || rows[2][2] != "beat@x.ch" {
		t.Fatalf("corrected file = %v", rows)
	}
}

// What would change the file unnoticed is refused, with the line it is about.
func TestApplyCorrectionsRefuses(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		j    Job
		docs DocumentStore
		want string
	}{
		"unknown line":   {corrJob([]map[string]any{{"_row": 9.0, "mail": "x"}}, nil), nil, "no record on line [9]"},
		"no _row":        {corrJob([]map[string]any{{"mail": "x"}}, nil), nil, "names no _row"},
		"bad _row":       {corrJob([]map[string]any{{"_row": "drei"}}, nil), nil, `_row "drei"`},
		"fraction":       {corrJob([]map[string]any{{"_row": 2.5}}, nil), nil, "not a line number"},
		"twice":          {corrJob([]map[string]any{{"_row": 2.0}, {"_row": 2.0}}, nil), nil, "corrected twice"},
		"new column":     {corrJob([]map[string]any{{"_row": 2.0, "telefon": "1"}}, nil), nil, `sets "telefon", which is not a column`},
		"drop corrected": {corrJob([]map[string]any{{"_row": 2.0, "mail": "x"}}, []int{2}), nil, "both corrected and dropped"},
		"no store":       {corrJob(nil, nil), DocumentStore(nil), "no document store"},
		"no source":      {Job{Operation: OperationApplyCorrections, Result: "r"}, nil, "holds none"},
		"no result":      {Job{Operation: OperationApplyCorrections, SourceDocument: "staff"}, nil, "needs a result variable"},
		"missing source": {Job{Operation: OperationApplyCorrections, SourceDocument: "gone", Result: "r"}, nil, "read the source gone"},
		"missing file":   {func() Job { j := corrJob(nil, nil); j.CorrectionsDocument = "gone"; return j }(), nil, "read the corrections gone"},
		"bad delimiter":  {func() Job { j := corrJob(nil, nil); j.Delimiter = "||"; return j }(), nil, "delimiter"},
	} {
		docs := tc.docs
		if docs == nil && name != "no store" {
			docs = &corrDocs{files: map[string]string{"staff": staff}}
		}
		if _, err := ApplyCorrections(ctx, tc.j, docs); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s = %v, want %q", name, err, tc.want)
		}
	}
	if _, err := ApplyCorrections(ctx, corrJob(nil, nil), &corrDocs{files: map[string]string{"staff": staff}, limit: 10}); err == nil || !strings.Contains(err.Error(), "10 bytes a document may be") {
		t.Errorf("past the limit = %v", err)
	}
	if _, err := ApplyCorrections(ctx, corrJob(nil, nil), &corrDocs{files: map[string]string{"staff": staff}, storeErr: errors.New("disk full")}); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Errorf("store refuses = %v", err)
	}
	if _, err := Run(corrJob(nil, nil)); err == nil || !strings.Contains(err.Error(), "no document store") {
		t.Errorf("Run = %v", err)
	}
}

func TestCorrectionHelpers(t *testing.T) {
	for _, v := range []any{true, "x", "Ja", "1", "yes"} {
		if !truthy(v) {
			t.Errorf("truthy(%v) = false", v)
		}
	}
	for _, v := range []any{false, "", "nein", nil, 1.0} {
		if truthy(v) {
			t.Errorf("truthy(%v) = true", v)
		}
	}
	for v, want := range map[any]string{nil: "", "a": "a", true: "true", 80.0: "80", 2.5: "2.5"} {
		if got := fieldText(v); got != want {
			t.Errorf("fieldText(%v) = %q", v, got)
		}
	}
	if got := fieldText([]any{1.0}); got != "[1]" {
		t.Errorf("fieldText(list) = %q", got)
	}
	if n, err := lineOf(3); err != nil || n != 3 {
		t.Errorf("lineOf(3) = %d, %v", n, err)
	}
	if _, err := lineOf([]int{1}); err == nil {
		t.Error("a list was taken for a line number")
	}
	for in, want := range map[string]string{"data.csv": "data.corrected.csv", "": "records.corrected.csv", "a/b.txt": "b.corrected.csv"} {
		if got := correctedName(in); got != want {
			t.Errorf("correctedName(%q) = %q", in, got)
		}
	}
}

// Through the in-process handler a compiled apply-corrections task reaches: the source
// and the corrections are resolved from the instance's variables, the corrected file
// is stored through the instance's store, and the job completes with its summary.
func TestTheHandlerAppliesCorrections(t *testing.T) {
	cp, task := csvTaskProcess(t, compiler.CsvConfig{
		Source: "datei", Result: "korrigiert", Delimiter: ";", HasHeader: true, Operation: OperationApplyCorrections,
		Corrections: "korrekturen", Drop: "weg", DocumentName: "neu.csv",
	})
	lookup := func(uint64) *compiler.CompiledProcess { return cp }
	vars := func(corr string) fakeVarStore {
		return fakeVarStore{
			ei: map[uint64]model.ElementInstanceValue{1: {ProcessDefKey: 7, ElementId: task}},
			vars: map[uint64][]model.VariableValue{1: {
				{Name: "datei", Kind: model.VarJSON, Text: `{"$document":"doc_staff","name":"staff.csv"}`},
				{Name: "korrekturen", Kind: model.VarJSON, Text: corr},
				{Name: "weg", Kind: model.VarJSON, Text: `[5]`},
			}},
		}
	}
	docs := &corrDocs{files: map[string]string{"doc_staff": staff, "doc_fixed": "personalnummer;mail;_row\n100002;beat@x.ch;3\n"}}
	stores := func(uint64) DocumentStore { return docs }
	for _, corr := range []string{`[{"_row":3,"mail":"beat@x.ch"}]`, `{"$document":"doc_fixed","name":"fixed.csv"}`} {
		docs.stored = nil
		out, err := HandlerWithStores(vars(corr), lookup, nil, stores)(job.Job{ElementInstanceKey: 1})
		if err != nil {
			t.Fatalf("%s: %v", corr, err)
		}
		var res map[string]any
		if len(out) != 1 || out[0].Name != "korrigiert" || json.Unmarshal([]byte(out[0].Text), &res) != nil || res["replaced"] != 1.0 || res["dropped"] != 1.0 {
			t.Fatalf("%s: outputs = %+v", corr, out)
		}
		if rows := readBack(t, docs.stored["neu.csv"]); len(rows) != 4 || rows[2][2] != "beat@x.ch" {
			t.Fatalf("%s: corrected file = %v", corr, rows)
		}
	}
	// Without a store, and with corrections that are neither a list nor a file.
	if _, err := HandlerWithStores(vars(`[]`), lookup, nil, nil)(job.Job{ElementInstanceKey: 1}); err == nil || !strings.Contains(err.Error(), "no document store") {
		t.Errorf("no store = %v", err)
	}
	if _, err := HandlerWithStores(vars(`"text"`), lookup, nil, stores)(job.Job{ElementInstanceKey: 1}); err == nil || !strings.Contains(err.Error(), "list of corrected records") {
		t.Errorf("text corrections = %v", err)
	}
	bad := vars(`[]`)
	bad.vars[1][0] = model.VariableValue{Name: "datei", Kind: model.VarString, Text: "a;b"}
	if _, err := HandlerWithStores(bad, lookup, nil, stores)(job.Job{ElementInstanceKey: 1}); err == nil || !strings.Contains(err.Error(), "holds no document") {
		t.Errorf("text source = %v", err)
	}
	bad = vars(`[]`)
	bad.vars[1][2] = model.VariableValue{Name: "weg", Kind: model.VarJSON, Text: `"drei"`}
	if _, err := HandlerWithStores(bad, lookup, nil, stores)(job.Job{ElementInstanceKey: 1}); err == nil || !strings.Contains(err.Error(), "list of line numbers") {
		t.Errorf("bad drop = %v", err)
	}
	bad = vars(`[]`)
	bad.vars[1] = bad.vars[1][:1]
	if _, err := HandlerWithStores(bad, lookup, nil, stores)(job.Job{ElementInstanceKey: 1}); err == nil || !strings.Contains(err.Error(), "corrections variable \"korrekturen\" is missing") {
		t.Errorf("no corrections = %v", err)
	}
}
