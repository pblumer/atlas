package model

import (
	"errors"
	"reflect"
	"testing"
)

// TestDecisionEvaluationValueRoundTrip encodes and decodes a
// DecisionEvaluationValue, covering both the DecodeValue (fresh) and
// DecodeValueInto (reused) paths, an evaluation with a trace and one without.
func TestDecisionEvaluationValueRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		v    *DecisionEvaluationValue
	}{
		{
			name: "with trace",
			v: &DecisionEvaluationValue{
				ProcessInstanceKey: NewKey(1, 1),
				ElementInstanceKey: NewKey(1, 7),
				ProcessDefKey:      NewKey(0, 3),
				ElementId:          4,
				DecisionId:         "dish",
				InputsJSON:         `{"season":"Fall","guests":8}`,
				OutputsJSON:        `{"dish":"Spareribs"}`,
				TraceJSON:          `{"tables":[{"hitPolicy":"U","matched":[2]}]}`,
			},
		},
		{
			name: "no trace",
			v: &DecisionEvaluationValue{
				ProcessInstanceKey: NewKey(2, 5),
				ElementInstanceKey: NewKey(2, 9),
				ProcessDefKey:      NewKey(0, 1),
				ElementId:          0,
				DecisionId:         "greeting",
				InputsJSON:         `{"name":"Patrick"}`,
				OutputsJSON:        `{"message":"Hello Patrick"}`,
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			buf := AppendValue(nil, c.v)

			got, err := DecodeValue(VTDecisionEvaluation, buf)
			if err != nil {
				t.Fatalf("DecodeValue: %v", err)
			}
			if !reflect.DeepEqual(got, c.v) {
				t.Errorf("DecodeValue = %+v, want %+v", got, c.v)
			}
			if got.ValueType() != VTDecisionEvaluation {
				t.Errorf("ValueType = %v, want %v", got.ValueType(), VTDecisionEvaluation)
			}

			into := newValue(VTDecisionEvaluation)
			if err := DecodeValueInto(into, buf); err != nil {
				t.Fatalf("DecodeValueInto: %v", err)
			}
			if !reflect.DeepEqual(into, c.v) {
				t.Errorf("DecodeValueInto = %+v, want %+v", into, c.v)
			}
		})
	}
}

// TestDecisionEvaluationDecodeErrors exercises every truncation guard in
// DecisionEvaluationValue.decode: the 28-byte fixed prefix (three keys plus the
// element id) and each of the four length-prefixed JSON strings.
func TestDecisionEvaluationDecodeErrors(t *testing.T) {
	full := AppendValue(nil, &DecisionEvaluationValue{
		ProcessInstanceKey: NewKey(1, 1),
		ElementInstanceKey: NewKey(1, 2),
		ProcessDefKey:      NewKey(0, 1),
		ElementId:          1,
		DecisionId:         "d",
		InputsJSON:         "i",
		OutputsJSON:        "o",
		TraceJSON:          "t",
	})
	tests := []struct {
		name string
		src  []byte
	}{
		{"short prefix", full[:4]},
		{"truncated decisionId length prefix", full[:30]}, // 28 prefix + 2 of the 4-byte length
		{"truncated decisionId bytes", full[:32]},         // length says 1 but no byte follows
		{"truncated inputs length prefix", full[:35]},     // 28 + 4 + 1(id) + 2 of inputs length
		{"truncated inputs bytes", full[:37]},
		{"truncated outputs length prefix", full[:40]},
		{"truncated outputs bytes", full[:42]},
		{"truncated trace length prefix", full[:45]},
		{"truncated trace bytes", full[:47]},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var v DecisionEvaluationValue
			if err := v.decode(tt.src); !errors.Is(err, ErrShortBuffer) {
				t.Errorf("decode(%d bytes) err = %v, want ErrShortBuffer", len(tt.src), err)
			}
		})
	}
}
