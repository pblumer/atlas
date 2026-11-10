package csvimport

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func collect(t *testing.T, cfg Config, text string) ([]string, []Record, error) {
	t.Helper()
	var out []Record
	header, err := EachRow(cfg, strings.NewReader(text), func(r Record) error {
		r.Cells = append([]string(nil), r.Cells...) // the reader reuses the slice
		out = append(out, r)
		return nil
	})
	return header, out, err
}

// EachRow reads the rows ParseRows would, one at a time, with each record's line and
// cells as written.
func TestEachRowMatchesParseRows(t *testing.T) {
	text := "\ufeffemail;group;license;note\na@b.ch;users;PRO;\"two\nlines\"\nbob;ops\n"
	yes := true
	cfg := Config{Delimiter: ";", HasHeader: &yes, Columns: []Column{{Name: "mail", Header: "email"}, {Name: "group"}, {Name: "license"}}}
	header, recs, err := collect(t, cfg, text)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(header, []string{"email", "group", "license", "note"}) {
		t.Fatalf("header = %q", header)
	}
	want, err := ParseRows(cfg, []byte(text))
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != len(want) {
		t.Fatalf("records = %d, ParseRows = %d", len(recs), len(want))
	}
	for i := range recs {
		if !reflect.DeepEqual(recs[i].Row, want[i]) {
			t.Errorf("row %d = %v, ParseRows %v", i, recs[i].Row, want[i])
		}
	}
	// Lines are where a person finds the record: the quoted cell spans 2 and 3.
	if recs[0].Line != 2 || recs[1].Line != 4 {
		t.Errorf("lines = %d, %d", recs[0].Line, recs[1].Line)
	}
	// A short record keeps its own cells; its missing field is empty in the row.
	if len(recs[1].Cells) != 2 || recs[1].Row["license"] != "" {
		t.Errorf("short record = %q %v", recs[1].Cells, recs[1].Row)
	}
}

func TestEachRowWithoutHeaderAndDerived(t *testing.T) {
	no := false
	zero, one := 0, 1
	_, recs, err := collect(t, Config{HasHeader: &no, Columns: []Column{{Name: "a", Index: &zero}, {Name: "n", Index: &one, Type: "number"}}}, "x,1\ny,2\n")
	if err != nil || len(recs) != 2 || recs[1].Row["a"] != "y" || recs[1].Line != 2 {
		t.Fatalf("headerless = %+v, %v", recs, err)
	}
	// With a header and no layout, every named column is a field.
	header, recs, err := collect(t, Config{}, "a,b\n1,2\n")
	if err != nil || len(header) != 2 || recs[0].Row["b"] != "2" {
		t.Fatalf("derived = %q %+v, %v", header, recs, err)
	}
	// A header with no record is a file with nothing to check, not an error.
	if _, recs, err := collect(t, Config{}, "a,b\n"); err != nil || len(recs) != 0 {
		t.Fatalf("header only = %+v, %v", recs, err)
	}
}

func TestEachRowRefuses(t *testing.T) {
	yes, no := true, false
	stop := errors.New("stop")
	for _, tc := range []struct {
		name string
		cfg  Config
		text string
		fn   func(Record) error
		want string
	}{
		{"empty", Config{}, "", nil, "CSV is empty"},
		{"empty headerless", Config{HasHeader: &no, Columns: []Column{{Name: "a", Index: new(int)}}}, "", nil, "CSV is empty"},
		{"missing column", Config{HasHeader: &yes, Columns: []Column{{Name: "x"}}}, "a\n1\n", nil, "not found in CSV header"},
		{"blank header", Config{}, " , \n1,2\n", nil, "no usable column names"},
		{"bad layout", Config{HasHeader: &no}, "a\n", nil, "at least one column"},
		{"bad delimiter", Config{Delimiter: ";;"}, "a\n", nil, "single character"},
		{"caller stops", Config{}, "a\n1\n2\n", func(Record) error { return stop }, "stop"},
	} {
		fn := tc.fn
		if fn == nil {
			fn = func(Record) error { return nil }
		}
		_, err := EachRow(tc.cfg, strings.NewReader(tc.text), fn)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", tc.name, err, tc.want)
		}
	}
}
