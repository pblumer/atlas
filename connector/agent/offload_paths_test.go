package agent

import (
	"reflect"
	"strings"
	"testing"

	"github.com/pblumer/atlas/model"
)

// What a leased round's payload may and may not carry. RoundFromPayload is the worker's
// half of the contract ResolveJobPayload writes, and a payload a worker cannot read
// must fail the round loudly — a round put to a model with no tools offers it nothing
// to choose, and a container would wait on an answer that cannot come.

// TestARoundsToolsSurviveJSONWithTheirParameters: the shape a round has after the
// wire, with a parameter that is not an object skipped rather than read as an empty
// one.
func TestARoundsToolsSurviveJSONWithTheirParameters(t *testing.T) {
	r, err := RoundFromPayload(map[string]any{
		"round": float64(2),
		"tools": []any{map[string]any{
			"name": "approve", "description": "Approve the request",
			"params": []any{
				map[string]any{"name": "reason", "type": "string", "description": "Why", "required": true},
				"not a parameter",
			},
		}},
	})
	if err != nil {
		t.Fatalf("RoundFromPayload: %v", err)
	}
	want := []Tool{{Name: "approve", Description: "Approve the request",
		Params: []Param{{Name: "reason", Type: "string", Description: "Why", Required: true}}}}
	if !reflect.DeepEqual(r.Tools, want) {
		t.Fatalf("tools = %+v, want %+v", r.Tools, want)
	}
}

// TestARoundWhoseToolsCannotBeReadIsRefused names each way the list can be unusable.
func TestARoundWhoseToolsCannotBeReadIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name  string
		tools any
		want  string
	}{
		{"an empty list as built", []Tool{}, "payload carries no tools"},
		{"an empty list off the wire", []any{}, "payload carries no tools"},
		{"a tool that is not an object", []any{"approve"}, "a tool in the payload is not an object"},
		{"a tool with no name", []any{map[string]any{"description": "x"}}, "a tool in the payload has no name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := RoundFromPayload(map[string]any{"round": 1, "tools": tc.tools})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("RoundFromPayload = %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

// TestAnEmptyContextTravelsAsNone: an agent given no context gets no context section,
// whichever shape the empty map arrived in.
func TestAnEmptyContextTravelsAsNone(t *testing.T) {
	tools := []Tool{{Name: "approve"}}
	for _, ctx := range []any{map[string]string{}, map[string]any{}} {
		r, err := RoundFromPayload(map[string]any{"round": 1, "tools": tools, "context": ctx})
		if err != nil {
			t.Fatalf("RoundFromPayload: %v", err)
		}
		if r.Context != nil {
			t.Errorf("context %T{} = %v, want none", ctx, r.Context)
		}
	}
}

// TestAFalseContextValueReadsAsFalse: Classify hands a boolean back in its bool slot
// with empty text, and a prompt that rendered that text would tell the model nothing.
func TestAFalseContextValueReadsAsFalse(t *testing.T) {
	if got := contextText(model.VariableValue{Kind: model.VarBool, Bool: false}); got != "false" {
		t.Fatalf("contextText(false) = %q, want \"false\"", got)
	}
}
