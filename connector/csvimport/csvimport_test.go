package csvimport

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func ptrInt(i int) *int    { return &i }
func ptrBool(b bool) *bool { return &b }

// num is a shorthand for the json.Number a numeric cell coerces to, so a test's
// want map reads naturally.
func num(s string) json.Number { return json.Number(s) }

func TestParseCSVRows(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		data string
		want []map[string]any
	}{
		{
			name: "header mapping, default comma, default string type",
			cfg: Config{Columns: []Column{
				{Name: "email"}, {Name: "group"}, {Name: "license"},
			}},
			data: "email,group,license\nada@x.io,admin,pro\nbob@y.io,ops,basic\n",
			want: []map[string]any{
				{"email": "ada@x.io", "group": "admin", "license": "pro"},
				{"email": "bob@y.io", "group": "ops", "license": "basic"},
			},
		},
		{
			name: "custom header names and type coercion",
			cfg: Config{Columns: []Column{
				{Name: "email", Header: "E-Mail"},
				{Name: "seats", Header: "Sitze", Type: "integer"},
				{Name: "score", Header: "Punkte", Type: "number"},
				{Name: "active", Header: "Aktiv", Type: "boolean"},
			}},
			data: "E-Mail,Sitze,Punkte,Aktiv\nada@x.io,3,4.5,ja\n",
			want: []map[string]any{
				{"email": "ada@x.io", "seats": num("3"), "score": num("4.5"), "active": true},
			},
		},
		{
			name: "semicolon delimiter (Excel/German export)",
			cfg: Config{
				Delimiter: ";",
				Columns:   []Column{{Name: "email"}, {Name: "group"}},
			},
			data: "email;group\nada@x.io;admin\n",
			want: []map[string]any{{"email": "ada@x.io", "group": "admin"}},
		},
		{
			name: "headerless with explicit index",
			cfg: Config{
				HasHeader: ptrBool(false),
				Columns: []Column{
					{Name: "email", Index: ptrInt(0)},
					{Name: "license", Index: ptrInt(2)},
				},
			},
			data: "ada@x.io,ignored,pro\nbob@y.io,ignored,basic\n",
			want: []map[string]any{
				{"email": "ada@x.io", "license": "pro"},
				{"email": "bob@y.io", "license": "basic"},
			},
		},
		{
			name: "lenient coercion keeps a malformed cell as its raw string",
			cfg: Config{Columns: []Column{
				{Name: "email"}, {Name: "seats", Type: "integer"},
			}},
			data: "email,seats\nada@x.io,not-a-number\n",
			want: []map[string]any{{"email": "ada@x.io", "seats": "not-a-number"}},
		},
		{
			name: "short row yields empty cells, extra columns are ignored",
			cfg: Config{Columns: []Column{
				{Name: "email"}, {Name: "group"}, {Name: "license"},
			}},
			data: "email,group,license\nada@x.io\n",
			want: []map[string]any{{"email": "ada@x.io", "group": "", "license": ""}},
		},
		{
			name: "quoted field with embedded delimiter and newline",
			cfg: Config{Columns: []Column{
				{Name: "email"}, {Name: "note"},
			}},
			data: "email,note\nada@x.io,\"a, b\nc\"\n",
			want: []map[string]any{{"email": "ada@x.io", "note": "a, b\nc"}},
		},
		{
			name: "leading UTF-8 BOM is stripped before the header row",
			cfg: Config{Columns: []Column{
				{Name: "email"}, {Name: "group"},
			}},
			data: "\ufeffemail,group\nada@x.io,admin\n",
			want: []map[string]any{{"email": "ada@x.io", "group": "admin"}},
		},
		{
			name: "header row only yields zero rows (non-nil)",
			cfg: Config{Columns: []Column{
				{Name: "email"}, {Name: "group"},
			}},
			data: "email,group\n",
			want: []map[string]any{},
		},
		{
			name: "boolean false tokens and lenient fallback",
			cfg: Config{Columns: []Column{
				{Name: "a", Type: "boolean"}, {Name: "b", Type: "boolean"}, {Name: "c", Type: "boolean"},
			}},
			data: "a,b,c\nnein,0,maybe\n",
			want: []map[string]any{{"a": false, "b": false, "c": "maybe"}},
		},
		{
			name: "boolean true tokens",
			cfg: Config{Columns: []Column{
				{Name: "a", Type: "boolean"}, {Name: "b", Type: "boolean"}, {Name: "c", Type: "boolean"},
				{Name: "d", Type: "boolean"}, {Name: "e", Type: "boolean"},
			}},
			data: "a,b,c,d,e\ntrue,1,yes,wahr,X\n",
			want: []map[string]any{{"a": true, "b": true, "c": true, "d": true, "e": true}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseRows(tc.cfg, []byte(tc.data))
			if err != nil {
				t.Fatalf("ParseRows: unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("rows =\n  %#v\nwant\n  %#v", got, tc.want)
			}
		})
	}
}

func TestParseCSVRowsErrors(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		data string
		want string // substring the error must contain
	}{
		{
			name: "no columns and no header row",
			cfg:  Config{Columns: nil, HasHeader: csvBoolPtr(false)},
			data: "ada@x.io\n",
			want: "at least one column",
		},
		{
			name: "column with empty name",
			cfg:  Config{Columns: []Column{{Name: ""}}},
			data: "email\nada@x.io\n",
			want: "column name is required",
		},
		{
			name: "duplicate column name",
			cfg: Config{Columns: []Column{
				{Name: "email"}, {Name: "email"},
			}},
			data: "email\nada@x.io\n",
			want: "duplicate column name",
		},
		{
			name: "headerless column without index",
			cfg: Config{
				HasHeader: ptrBool(false),
				Columns:   []Column{{Name: "email"}},
			},
			data: "ada@x.io\n",
			want: "needs an index",
		},
		{
			name: "headerless negative index",
			cfg: Config{
				HasHeader: ptrBool(false),
				Columns:   []Column{{Name: "email", Index: ptrInt(-1)}},
			},
			data: "ada@x.io\n",
			want: "index must be >= 0",
		},
		{
			name: "multi-rune delimiter",
			cfg: Config{
				Delimiter: ";;",
				Columns:   []Column{{Name: "email"}},
			},
			data: "email\nada@x.io\n",
			want: "delimiter",
		},
		{
			name: "configured header absent from the file",
			cfg: Config{Columns: []Column{
				{Name: "email"}, {Name: "group", Header: "Gruppe"},
			}},
			data: "email,team\nada@x.io,admin\n",
			want: "not found in CSV header",
		},
		{
			name: "unknown column type",
			cfg: Config{Columns: []Column{
				{Name: "email", Type: "date"},
			}},
			data: "email\nada@x.io\n",
			want: "unsupported column type",
		},
		{
			name: "empty file (no header row)",
			cfg:  Config{Columns: []Column{{Name: "email"}}},
			data: "",
			want: "empty",
		},
		{
			name: "header row with only blank names, no columns to derive",
			cfg:  Config{Columns: nil},
			data: " , \nx,y\n",
			want: "no usable column names",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseRows(tc.cfg, []byte(tc.data))
			if err == nil {
				t.Fatalf("ParseRows: expected error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tc.want)
			}
		})
	}
}

// csvBoolPtr returns a pointer to b, for setting the optional HasHeader field in a
// test Config literal.
func csvBoolPtr(b bool) *bool { return &b }

// TestParseCSVRowsDeriveColumns covers ADR-0139's header-derivation mode: with a
// header row and no explicit column layout, every distinct non-blank header cell
// becomes a string field, and duplicate/blank header cells are skipped.
func TestParseCSVRowsDeriveColumns(t *testing.T) {
	rows, err := ParseRows(Config{Columns: nil}, []byte("email,group,license\nada@x.io,users,PRO\nbob@x.io,ops,BASIC\n"))
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	first := rows[0]
	if len(first) != 3 || first["email"] != "ada@x.io" || first["group"] != "users" || first["license"] != "PRO" {
		t.Fatalf("row[0] = %v, want the three derived string columns", first)
	}

	// A duplicate header cell is taken once (first occurrence wins); a blank one is
	// dropped. Here "email" repeats and one cell is blank.
	rows, err = ParseRows(Config{Columns: nil}, []byte("email, ,email\na, b, c\n"))
	if err != nil {
		t.Fatalf("derive dup/blank: %v", err)
	}
	if len(rows) != 1 || len(rows[0]) != 1 || rows[0]["email"] != "a" {
		t.Fatalf("row = %v, want a single email column bound to the first occurrence", rows[0])
	}
}
