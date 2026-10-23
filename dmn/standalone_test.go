package dmn_test

import (
	"context"
	"strings"
	"testing"

	"github.com/pblumer/atlas/dmn"
)

// accessModel is a decision table of the shape an authorization policy embeds: flat
// inputs, a string effect and a reason code.
const accessModel = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" id="defs_access" name="Access" namespace="http://atlas/authz">
  <inputData id="id_op" name="operation"/>
  <inputData id="id_role" name="role"/>
  <decision id="access" name="access">
    <informationRequirement><requiredInput href="#id_op"/></informationRequirement>
    <informationRequirement><requiredInput href="#id_role"/></informationRequirement>
    <decisionTable id="dt" hitPolicy="FIRST">
      <input id="in1"><inputExpression id="ie1" typeRef="string"><text>operation</text></inputExpression></input>
      <input id="in2"><inputExpression id="ie2" typeRef="string"><text>role</text></inputExpression></input>
      <output id="out1" name="effect" typeRef="string"/>
      <output id="out2" name="reasonCode" typeRef="string"/>
      <rule id="r1"><inputEntry id="e11"><text>"delete-file"</text></inputEntry><inputEntry id="e12"><text>"manager"</text></inputEntry><outputEntry id="o11"><text>"Permit"</text></outputEntry><outputEntry id="o12"><text>"MANAGER_DELETE"</text></outputEntry></rule>
      <rule id="r2"><inputEntry id="e21"><text>"delete-file"</text></inputEntry><inputEntry id="e22"><text>-</text></inputEntry><outputEntry id="o21"><text>"Deny"</text></outputEntry><outputEntry id="o22"><text>"DELETE_NOT_ALLOWED"</text></outputEntry></rule>
    </decisionTable>
  </decision>
</definitions>`

func TestAStandaloneDecisionIsCompiledOnceAndEvaluatedMany(t *testing.T) {
	d, err := dmn.CompileStandalone(context.Background(), []byte(accessModel), "access")
	if err != nil {
		t.Fatalf("CompileStandalone: %v", err)
	}
	for _, tc := range []struct {
		role, effect, reason string
	}{
		{"manager", "Permit", "MANAGER_DELETE"},
		{"employee", "Deny", "DELETE_NOT_ALLOWED"},
	} {
		res, err := d.Evaluate(context.Background(), map[string]any{"operation": "delete-file", "role": tc.role})
		if err != nil {
			t.Fatalf("Evaluate(%s): %v", tc.role, err)
		}
		out, _ := res.(map[string]any)
		if out["effect"] != tc.effect || out["reasonCode"] != tc.reason {
			t.Errorf("Evaluate(%s) = %v, want effect %s reason %s", tc.role, res, tc.effect, tc.reason)
		}
	}
}

func TestAStandaloneDecisionRefusesAModelItCannotRun(t *testing.T) {
	if _, err := dmn.CompileStandalone(context.Background(), []byte("<definitions/>"), "access"); err == nil {
		t.Error("an empty model compiled")
	}
	_, err := dmn.CompileStandalone(context.Background(), []byte(accessModel), "missing")
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Errorf("a decision the model does not provide: err = %v", err)
	}
}

// A model that is not DMN at all, and one temis parses but reports errors in, are both
// refused at compilation — where a policy's author can still fix them — and each says
// which of the two it was.
func TestAStandaloneDecisionRefusesAModelThatDoesNotCompile(t *testing.T) {
	_, err := dmn.CompileStandalone(context.Background(), []byte(malformedModel), "access")
	if err == nil || !strings.Contains(err.Error(), "dmn: compile") {
		t.Errorf("malformed XML: err = %v, want a compile error", err)
	}
	_, err = dmn.CompileStandalone(context.Background(), []byte(erroneousModel), "Dec")
	if err == nil || !strings.Contains(err.Error(), "the model has errors") {
		t.Errorf("a model with diagnostics: err = %v, want its errors reported", err)
	}
}

// An evaluation that cannot run is an error, not a result: a value of the wrong type is
// refused rather than quietly missing every rule, a missing input is named, and a
// cancelled caller gets nothing.
func TestAStandaloneDecisionReportsAnEvaluationItCannotRun(t *testing.T) {
	d, err := dmn.CompileStandalone(context.Background(), []byte(accessModel), "access")
	if err != nil {
		t.Fatalf("CompileStandalone: %v", err)
	}
	for name, tc := range map[string]struct {
		ctx  func() context.Context
		in   map[string]any
		want string
	}{
		"an input of the wrong type": {
			ctx:  context.Background,
			in:   map[string]any{"operation": 42, "role": "manager"},
			want: `input "operation" expects string`,
		},
		"a missing input": {
			ctx:  context.Background,
			in:   map[string]any{"operation": "delete-file"},
			want: `missing required input "role"`,
		},
		"a cancelled caller": {
			ctx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			in:   map[string]any{"operation": "delete-file", "role": "manager"},
			want: "access",
		},
	} {
		res, err := d.Evaluate(tc.ctx(), tc.in)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: Evaluate = %v, %v; want an error naming %q", name, res, err, tc.want)
		}
		if res != nil {
			t.Errorf("%s: an evaluation that failed answered %v", name, res)
		}
	}
}
