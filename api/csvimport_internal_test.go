package api

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
		cfg  csvConfig
		data string
		want []map[string]any
	}{
		{
			name: "header mapping, default comma, default string type",
			cfg: csvConfig{Columns: []csvColumn{
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
			cfg: csvConfig{Columns: []csvColumn{
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
			cfg: csvConfig{
				Delimiter: ";",
				Columns:   []csvColumn{{Name: "email"}, {Name: "group"}},
			},
			data: "email;group\nada@x.io;admin\n",
			want: []map[string]any{{"email": "ada@x.io", "group": "admin"}},
		},
		{
			name: "headerless with explicit index",
			cfg: csvConfig{
				HasHeader: ptrBool(false),
				Columns: []csvColumn{
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
			cfg: csvConfig{Columns: []csvColumn{
				{Name: "email"}, {Name: "seats", Type: "integer"},
			}},
			data: "email,seats\nada@x.io,not-a-number\n",
			want: []map[string]any{{"email": "ada@x.io", "seats": "not-a-number"}},
		},
		{
			name: "short row yields empty cells, extra columns are ignored",
			cfg: csvConfig{Columns: []csvColumn{
				{Name: "email"}, {Name: "group"}, {Name: "license"},
			}},
			data: "email,group,license\nada@x.io\n",
			want: []map[string]any{{"email": "ada@x.io", "group": "", "license": ""}},
		},
		{
			name: "quoted field with embedded delimiter and newline",
			cfg: csvConfig{Columns: []csvColumn{
				{Name: "email"}, {Name: "note"},
			}},
			data: "email,note\nada@x.io,\"a, b\nc\"\n",
			want: []map[string]any{{"email": "ada@x.io", "note": "a, b\nc"}},
		},
		{
			name: "leading UTF-8 BOM is stripped before the header row",
			cfg: csvConfig{Columns: []csvColumn{
				{Name: "email"}, {Name: "group"},
			}},
			data: "\ufeffemail,group\nada@x.io,admin\n",
			want: []map[string]any{{"email": "ada@x.io", "group": "admin"}},
		},
		{
			name: "header row only yields zero rows (non-nil)",
			cfg: csvConfig{Columns: []csvColumn{
				{Name: "email"}, {Name: "group"},
			}},
			data: "email,group\n",
			want: []map[string]any{},
		},
		{
			name: "boolean false tokens and lenient fallback",
			cfg: csvConfig{Columns: []csvColumn{
				{Name: "a", Type: "boolean"}, {Name: "b", Type: "boolean"}, {Name: "c", Type: "boolean"},
			}},
			data: "a,b,c\nnein,0,maybe\n",
			want: []map[string]any{{"a": false, "b": false, "c": "maybe"}},
		},
		{
			name: "boolean true tokens",
			cfg: csvConfig{Columns: []csvColumn{
				{Name: "a", Type: "boolean"}, {Name: "b", Type: "boolean"}, {Name: "c", Type: "boolean"},
				{Name: "d", Type: "boolean"}, {Name: "e", Type: "boolean"},
			}},
			data: "a,b,c,d,e\ntrue,1,yes,wahr,X\n",
			want: []map[string]any{{"a": true, "b": true, "c": true, "d": true, "e": true}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseCSVRows(tc.cfg, []byte(tc.data))
			if err != nil {
				t.Fatalf("parseCSVRows: unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("rows =\n  %#v\nwant\n  %#v", got, tc.want)
			}
		})
	}
}

// TestStartVarsFromMap documents the shared map→VariableValues contract the CSV
// upload path and the JSON start body both rely on (ADR-0084).
func TestStartVarsFromMap(t *testing.T) {
	if vs, err := startVarsFromMap(nil); err != nil || vs != nil {
		t.Fatalf("empty map: got %v, %v; want nil, nil", vs, err)
	}
	// A raw int is outside the encoding/json-with-UseNumber shape and is rejected.
	if _, err := startVarsFromMap(map[string]any{"x": 42}); err == nil ||
		!strings.Contains(err.Error(), "unsupported value type") {
		t.Fatalf("raw int: want unsupported value type error, got %v", err)
	}
	// Every supported kind converts, including a null and a nested array.
	vs, err := startVarsFromMap(map[string]any{
		"n": num("3"), "s": "hi", "b": true, "z": nil,
		"j": []any{map[string]any{"k": "v"}},
	})
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if len(vs) != 5 {
		t.Fatalf("converted %d variables, want 5", len(vs))
	}
}

func TestParseCSVRowsErrors(t *testing.T) {
	tests := []struct {
		name string
		cfg  csvConfig
		data string
		want string // substring the error must contain
	}{
		{
			name: "no columns configured",
			cfg:  csvConfig{Columns: nil},
			data: "email\nada@x.io\n",
			want: "at least one column",
		},
		{
			name: "column with empty name",
			cfg:  csvConfig{Columns: []csvColumn{{Name: ""}}},
			data: "email\nada@x.io\n",
			want: "column name is required",
		},
		{
			name: "duplicate column name",
			cfg: csvConfig{Columns: []csvColumn{
				{Name: "email"}, {Name: "email"},
			}},
			data: "email\nada@x.io\n",
			want: "duplicate column name",
		},
		{
			name: "headerless column without index",
			cfg: csvConfig{
				HasHeader: ptrBool(false),
				Columns:   []csvColumn{{Name: "email"}},
			},
			data: "ada@x.io\n",
			want: "needs an index",
		},
		{
			name: "headerless negative index",
			cfg: csvConfig{
				HasHeader: ptrBool(false),
				Columns:   []csvColumn{{Name: "email", Index: ptrInt(-1)}},
			},
			data: "ada@x.io\n",
			want: "index must be >= 0",
		},
		{
			name: "multi-rune delimiter",
			cfg: csvConfig{
				Delimiter: ";;",
				Columns:   []csvColumn{{Name: "email"}},
			},
			data: "email\nada@x.io\n",
			want: "delimiter",
		},
		{
			name: "configured header absent from the file",
			cfg: csvConfig{Columns: []csvColumn{
				{Name: "email"}, {Name: "group", Header: "Gruppe"},
			}},
			data: "email,team\nada@x.io,admin\n",
			want: "not found in CSV header",
		},
		{
			name: "unknown column type",
			cfg: csvConfig{Columns: []csvColumn{
				{Name: "email", Type: "date"},
			}},
			data: "email\nada@x.io\n",
			want: "unsupported column type",
		},
		{
			name: "empty file (no header row)",
			cfg:  csvConfig{Columns: []csvColumn{{Name: "email"}}},
			data: "",
			want: "empty",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseCSVRows(tc.cfg, []byte(tc.data))
			if err == nil {
				t.Fatalf("parseCSVRows: expected error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tc.want)
			}
		})
	}
}
