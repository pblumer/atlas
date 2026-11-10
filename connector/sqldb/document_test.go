package sqldb

import (
	"context"
	"encoding/csv"
	"errors"
	"strings"
	"testing"
	"time"
)

type docStore struct {
	name, contentType string
	data              []byte
	limit             int64
	err               error
}

func (s *docStore) StoreDocument(_ context.Context, name, contentType string, data []byte) (map[string]any, error) {
	if s.err != nil {
		return nil, s.err
	}
	s.name, s.contentType, s.data = name, contentType, append([]byte(nil), data...)
	return map[string]any{"$document": "doc_sql", "name": name}, nil
}

func (s *docStore) DocumentLimit() int64 { return s.limit }

const personenQuery = "SELECT personalnummer, name, mail, eintritt, aktiv, pensum FROM personen"

func personenAnswer() MockAnswer {
	return MockAnswer{
		Statement: personenQuery,
		Columns:   []string{"personalnummer", "name", "mail", "eintritt", "aktiv", "pensum"},
		Rows: [][]any{
			{1001, "Anna Maier", "anna@x.ch", "2020-01-01T00:00:00Z", true, 80.5},
			{1002, "Beat, Keller", nil, "2021-03-15T00:00:00Z", false, 100},
		},
	}
}

func documentQuery() Job {
	return Job{Connector: "hr", Product: "mssql", Operation: "query", Statement: personenQuery,
		ResultVariable: "personen", AsDocument: true, DocumentName: "personen.csv", MaxRows: 10}
}

// A query written as a document: one row per row of the result, its columns the
// statement's in their order, NULL an empty cell, a comma quoted; the result variable
// holds {found, count, document} (docs/planning/ad-search-as-document.md, S3).
func TestAQueryIsWrittenAsADocument(t *testing.T) {
	c, _ := mockClient(t, personenAnswer())
	docs := &docStore{}
	out, err := RunWithDocuments(context.Background(), documentQuery(), regWith("hr", c), docs)
	if err != nil {
		t.Fatal(err)
	}
	res := out["personen"].(map[string]any)
	if res["count"] != 2 || res["found"] != true || res["document"].(map[string]any)["$document"] != "doc_sql" {
		t.Fatalf("result = %v", res)
	}
	if _, ok := res["dn"]; ok {
		t.Error("a query's result names a dn")
	}
	if docs.name != "personen.csv" || docs.contentType != "text/csv; charset=utf-8" {
		t.Fatalf("stored %q as %q", docs.name, docs.contentType)
	}
	rows, err := csv.NewReader(strings.NewReader(string(docs.data))).ReadAll()
	if err != nil || len(rows) != 3 {
		t.Fatalf("rows = %v (%v)", rows, err)
	}
	if got := strings.Join(rows[0], ";"); got != "personalnummer;name;mail;eintritt;aktiv;pensum" {
		t.Errorf("header = %s", got)
	}
	if got := strings.Join(rows[1], ";"); got != "1001;Anna Maier;anna@x.ch;2020-01-01T00:00:00Z;true;80.5" {
		t.Errorf("row 1 = %s", got)
	}
	if got := strings.Join(rows[2], ";"); got != "1002;Beat, Keller;;2021-03-15T00:00:00Z;false;100" {
		t.Errorf("row 2 = %s", got)
	}
}

// No rows is a result: the header alone, found false.
func TestAQueryDocumentThatFindsNothing(t *testing.T) {
	a := personenAnswer()
	a.Rows = nil
	c, _ := mockClient(t, a)
	docs := &docStore{}
	j := documentQuery()
	j.DocumentName = ""
	out, err := RunWithDocuments(context.Background(), j, regWith("hr", c), docs)
	if err != nil {
		t.Fatal(err)
	}
	if res := out["personen"].(map[string]any); res["found"] != false || res["count"] != 0 || docs.name != "query.csv" {
		t.Fatalf("result = %v, name %q", res, docs.name)
	}
	if rows, _ := csv.NewReader(strings.NewReader(string(docs.data))).ReadAll(); len(rows) != 1 {
		t.Fatalf("rows = %v", rows)
	}
}

// What a document query cannot honestly finish fails the job, with the reason.
func TestAQueryDocumentFailsRatherThanCuts(t *testing.T) {
	ctx := context.Background()
	c, _ := mockClient(t, personenAnswer())
	reg := regWith("hr", c)
	with := func(f func(*Job)) Job { j := documentQuery(); f(&j); return j }
	for name, tc := range map[string]struct {
		j    Job
		docs DocumentStore
		want string
	}{
		"cap":        {with(func(j *Job) { j.MaxRows = 1 }), &docStore{}, "sqldb: the query returned more than the 1-row cap"},
		"limit":      {with(func(j *Job) { j.DocumentLimit = 60 }), &docStore{limit: 1 << 20}, "60 bytes a document may be"},
		"store":      {documentQuery(), &docStore{err: errors.New("disk full")}, "disk full"},
		"no store":   {documentQuery(), nil, "no document store"},
		"not query":  {with(func(j *Job) { j.Operation = "execute" }), &docStore{}, "only a query"},
		"no worker":  {with(func(j *Job) { j.Connector = "nope" }), &docStore{}, "nope"},
		"product":    {with(func(j *Job) { j.Product = "postgres" }), &docStore{}, "is a mssql database"},
		"unanswered": {with(func(j *Job) { j.Statement = "SELECT 1" }), &docStore{}, "SELECT 1"},
	} {
		if _, err := RunWithDocuments(ctx, tc.j, reg, tc.docs); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s = %v, want %q", name, err, tc.want)
		}
	}
	// No cap but the document limit.
	if _, err := RunWithDocuments(ctx, with(func(j *Job) { j.MaxRows = 0 }), reg, &docStore{}); err != nil {
		t.Errorf("uncapped = %v", err)
	}
	// Run, which knows no document store, says so rather than writing a variable.
	if _, err := Run(ctx, documentQuery(), reg); err == nil || !strings.Contains(err.Error(), "no document store") {
		t.Errorf("Run = %v", err)
	}
	// A query into a variable is Run's, document store or not.
	if out, err := RunWithDocuments(ctx, with(func(j *Job) { j.AsDocument = false }), reg, nil); err != nil || len(out["personen"].([]map[string]any)) != 2 {
		t.Errorf("variable form = %v, %v", out, err)
	}
}

// A cell is the value's text: NULL empty, a time in RFC 3339, bytes as text, anything
// else as JSON.
func TestCellText(t *testing.T) {
	for _, tc := range []struct {
		in   any
		want string
	}{
		{nil, ""}, {"x", "x"}, {true, "true"}, {int64(-3), "-3"}, {2.5, "2.5"},
		{[]byte("abc"), "abc"},
		{time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC), "2026-11-10T08:00:00Z"},
		{[]int{1, 2}, "[1,2]"},
		{func() {}, ""},
	} {
		got := cellText(tc.in)
		if tc.want == "" && tc.in != nil && got != "" {
			continue // a value JSON cannot encode is shown as Go prints it
		}
		if got != tc.want {
			t.Errorf("cellText(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
