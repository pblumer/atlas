package api

import (
	"testing"

	"github.com/pblumer/atlas/model"
)

// TestParseStartVariables covers every branch of parseStartVariables: the empty
// and whitespace-only bodies, malformed JSON, an empty variables map, each
// supported scalar kind, and an unsupported (non-scalar) value.
func TestParseStartVariables(t *testing.T) {
	t.Run("empty body", func(t *testing.T) {
		got, err := parseStartVariables(nil)
		if err != nil || got != nil {
			t.Fatalf("parseStartVariables(nil) = (%v, %v), want (nil, nil)", got, err)
		}
	})

	t.Run("whitespace only", func(t *testing.T) {
		got, err := parseStartVariables([]byte("   \n\t "))
		if err != nil || got != nil {
			t.Fatalf("got (%v, %v), want (nil, nil)", got, err)
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		_, err := parseStartVariables([]byte(`{not json`))
		if err == nil {
			t.Fatal("expected an error for malformed JSON")
		}
	})

	t.Run("no variables key", func(t *testing.T) {
		got, err := parseStartVariables([]byte(`{"other": 1}`))
		if err != nil || got != nil {
			t.Fatalf("got (%v, %v), want (nil, nil)", got, err)
		}
	})

	t.Run("empty variables map", func(t *testing.T) {
		got, err := parseStartVariables([]byte(`{"variables": {}}`))
		if err != nil || got != nil {
			t.Fatalf("got (%v, %v), want (nil, nil)", got, err)
		}
	})

	t.Run("all scalar kinds", func(t *testing.T) {
		got, err := parseStartVariables([]byte(`{"variables": {"b": true, "n": 12.50, "s": "hi", "z": null}}`))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		byName := map[string]model.VariableValue{}
		for _, v := range got {
			byName[v.Name] = v
		}
		if v := byName["b"]; v.Kind != model.VarBool || !v.Bool {
			t.Errorf("b = %+v, want bool true", v)
		}
		if v := byName["n"]; v.Kind != model.VarNumber || v.Text != "12.50" {
			t.Errorf("n = %+v, want number with exact text 12.50", v)
		}
		if v := byName["s"]; v.Kind != model.VarString || v.Text != "hi" {
			t.Errorf("s = %+v, want string hi", v)
		}
		if v := byName["z"]; v.Kind != model.VarNull {
			t.Errorf("z = %+v, want null", v)
		}
	})

	t.Run("false bool", func(t *testing.T) {
		got, err := parseStartVariables([]byte(`{"variables": {"b": false}}`))
		if err != nil || len(got) != 1 || got[0].Kind != model.VarBool || got[0].Bool {
			t.Fatalf("got (%+v, %v), want a single false bool", got, err)
		}
	})

	t.Run("unsupported non-scalar", func(t *testing.T) {
		_, err := parseStartVariables([]byte(`{"variables": {"x": [1, 2, 3]}}`))
		if err == nil {
			t.Fatal("expected an error for a non-scalar value")
		}
	})
}

// TestToVariableView covers all four rendering branches, including the true and
// false bool forms and the null default.
func TestToVariableView(t *testing.T) {
	cases := []struct {
		name      string
		in        model.VariableValue
		wantKind  string
		wantValue string
	}{
		{"bool true", model.VariableValue{Name: "b", Kind: model.VarBool, Bool: true}, "boolean", "true"},
		{"bool false", model.VariableValue{Name: "b", Kind: model.VarBool, Bool: false}, "boolean", "false"},
		{"number", model.VariableValue{Name: "n", Kind: model.VarNumber, Text: "42"}, "number", "42"},
		{"string", model.VariableValue{Name: "s", Kind: model.VarString, Text: "hi"}, "string", "hi"},
		{"null", model.VariableValue{Name: "z", Kind: model.VarNull}, "null", "null"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := toVariableView(&tc.in)
			if got.Name != tc.in.Name || got.Kind != tc.wantKind || got.Value != tc.wantValue {
				t.Fatalf("toVariableView(%+v) = %+v, want kind=%q value=%q", tc.in, got, tc.wantKind, tc.wantValue)
			}
		})
	}
}
