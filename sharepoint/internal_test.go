package sharepoint

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/model"
)

// TestRegistry covers Register/Client/Replace, including the nil-map clear.
func TestRegistry(t *testing.T) {
	r := NewRegistry()
	r.Register("a", stubTokensClient{})
	if _, ok := r.Client("a"); !ok {
		t.Error("Client(a) after Register: want ok")
	}
	r.Replace(map[string]Client{"b": stubTokensClient{}})
	if _, ok := r.Client("a"); ok {
		t.Error("Client(a) after Replace: want gone")
	}
	if _, ok := r.Client("b"); !ok {
		t.Error("Client(b) after Replace: want ok")
	}
	r.Replace(nil)
	if _, ok := r.Client("b"); ok {
		t.Error("Client(b) after Replace(nil): want cleared")
	}
}

type stubTokensClient struct{}

func (stubTokensClient) CreateItem(context.Context, ItemRequest) (any, error) { return nil, nil }

// TestToExprKind covers every stored-variable-kind branch of the FEEL binding map.
func TestToExprKind(t *testing.T) {
	cases := map[model.VarKind]expr.ValueKind{
		model.VarBool:   expr.KindBool,
		model.VarNumber: expr.KindNumber,
		model.VarString: expr.KindString,
		model.VarJSON:   expr.KindJSON,
		model.VarNull:   expr.KindNull,
	}
	for in, want := range cases {
		if got := toExprKind(in); got != want {
			t.Errorf("toExprKind(%v) = %v, want %v", in, got, want)
		}
	}
}

// TestItemVariableKinds covers itemVariable/toVarKind across the created item's JSON
// shapes: a scalar stays a scalar, an object becomes a structured VarJSON, a null
// becomes VarNull.
func TestItemVariableKinds(t *testing.T) {
	cases := []struct {
		name string
		body any
		want model.VarKind
	}{
		{"object", map[string]any{"id": "1"}, model.VarJSON},
		{"string", "hello", model.VarString},
		{"number", json.Number("7"), model.VarNumber},
		{"bool", true, model.VarBool},
		{"null", nil, model.VarNull},
	}
	for _, c := range cases {
		got := itemVariable("created", c.body)
		if got.Name != "created" {
			t.Errorf("%s: name = %q, want created", c.name, got.Name)
		}
		if got.Kind != c.want {
			t.Errorf("%s: kind = %v, want %v", c.name, got.Kind, c.want)
		}
	}
}
