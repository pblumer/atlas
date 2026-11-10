package csvdoc_test

import (
	"context"
	"encoding/csv"
	"errors"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/csvdoc"
	"github.com/pblumer/atlas/limits"
)

type store struct {
	limit int64
	err   error
	data  []byte
	ct    string
}

func (s *store) StoreDocument(_ context.Context, name, ct string, data []byte) (map[string]any, error) {
	if s.err != nil {
		return nil, s.err
	}
	s.data, s.ct = data, ct
	return map[string]any{"$document": "doc_1", "name": name}, nil
}

func (s *store) DocumentLimit() int64 { return s.limit }

type plain struct{}

func (plain) StoreDocument(context.Context, string, string, []byte) (map[string]any, error) {
	return nil, nil
}

// A cell with several values splits back into exactly them: the separator and the
// escape inside a value are escaped, one plain value is written as it is.
func TestJoinValuesEscapes(t *testing.T) {
	for _, tc := range []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{"a"}, "a"},
		{[]string{"a", "b"}, "a|b"},
		{[]string{`A|B`, `C\D`}, `A\|B|C\\D`},
		{[]string{`x|y`}, `x\|y`},
	} {
		if got := csvdoc.JoinValues(tc.in); got != tc.want {
			t.Errorf("JoinValues(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The job's limit wins, then the store's, then the default budget.
func TestLimitPrecedence(t *testing.T) {
	if got := csvdoc.Limit(700, &store{limit: 500}); got != 700 {
		t.Errorf("job limit = %d", got)
	}
	if got := csvdoc.Limit(0, &store{limit: 500}); got != 500 {
		t.Errorf("store limit = %d", got)
	}
	if got := csvdoc.Limit(0, plain{}); got != limits.Default().Document {
		t.Errorf("default = %d", got)
	}
	if got := csvdoc.Limit(0, &store{}); got != limits.Default().Document {
		t.Errorf("a store reporting 0 = %d", got)
	}
}

// A row past either bound is refused with the source's name and the number that was
// passed; a document within them is stored as CSV.
func TestWriterBoundsAndStore(t *testing.T) {
	w := csvdoc.NewWriter([]string{"id", "name"}, csvdoc.Bounds{What: "sqldb: the query", Limit: 1 << 20, Max: 2})
	for _, r := range [][]string{{"1", "Ada"}, {"2", "Grace, Hopper"}} {
		if err := w.Write(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Write([]string{"3", "x"}); err == nil || !strings.Contains(err.Error(), "sqldb: the query returned more than the 2-row cap") {
		t.Errorf("past the cap = %v", err)
	}
	s := &store{}
	w = csvdoc.NewWriter([]string{"id"}, csvdoc.Bounds{What: "q", Limit: 1 << 20})
	_ = w.Write([]string{"1"})
	ref, err := w.Store(context.Background(), s, "x.csv")
	if err != nil || ref["$document"] != "doc_1" || s.ct != csvdoc.ContentType || w.Rows() != 1 {
		t.Fatalf("store = %v, %v, %q", ref, err, s.ct)
	}
	if rows, err := csv.NewReader(strings.NewReader(string(s.data))).ReadAll(); err != nil || len(rows) != 2 {
		t.Fatalf("stored %q (%v)", s.data, err)
	}
	if _, err := w.Store(context.Background(), &store{err: errors.New("disk full")}, "x.csv"); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Errorf("store refuses = %v", err)
	}
	w = csvdoc.NewWriter([]string{"id"}, csvdoc.Bounds{What: "ldap: the search", Unit: "entry", Units: "entries", Limit: 10})
	if err := w.Write([]string{"0123456789"}); err == nil || !strings.Contains(err.Error(), "10 bytes a document may be (ATLAS_LIMIT_DOCUMENT) after 1 entries") {
		t.Errorf("past the limit = %v", err)
	}
	if err := w.Write([]string{"again"}); err == nil {
		t.Error("a writer that refused once accepted another row")
	}
}

// An entry's row is its DN, then the attributes asked for, matched without regard to
// case; one the entry lacks is an empty cell.
func TestEntryRow(t *testing.T) {
	got := csvdoc.EntryRow("cn=a", map[string][]string{"MAIL": {"a@x"}, "memberOf": {"g1", "g2"}}, []string{"mail", "telephoneNumber", "memberof"}, nil)
	if strings.Join(got, ";") != "cn=a;a@x;;g1|g2" {
		t.Fatalf("row = %q", got)
	}
	if s := csvdoc.Summary(0, nil); s["found"] != false || s["count"] != 0 {
		t.Fatalf("summary = %v", s)
	}
}
