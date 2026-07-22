package model

import (
	"errors"
	"reflect"
	"testing"
)

// TestAppendValueRoundTrip covers AppendValue / DecodeValue / DecodeValueInto,
// which persist a payload without a record header (the state store's path).
func TestAppendValueRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		vt   ValueType
		v    Value
	}{
		{
			name: "element instance",
			vt:   VTElementInstance,
			v: &ElementInstanceValue{
				ProcessInstanceKey: NewKey(1, 1),
				ProcessDefKey:      NewKey(1, 2),
				ElementId:          3,
				FlowScopeKey:       NewKey(1, 4),
				BpmnElementType:    5,
			},
		},
		{
			name: "job",
			vt:   VTJob,
			v:    &JobValue{ProcessInstanceKey: NewKey(1, 1), ElementInstanceKey: NewKey(1, 2), JobType: 9, Retries: 2, Deadline: 123},
		},
		{
			name: "timer",
			vt:   VTTimer,
			v:    &TimerValue{ProcessInstanceKey: NewKey(1, 1), ElementInstanceKey: NewKey(1, 2), TargetElementId: 4, DueDate: 555, Repetitions: -1},
		},
		{
			name: "process instance (terminal history record)",
			vt:   VTProcessInstance,
			v:    &ProcessInstanceValue{ProcessDefKey: NewKey(2, 8), State: PICompleted, CompletedAt: 1_700_000_000_000_000_000},
		},
		{
			name: "variable",
			vt:   VTVariable,
			v:    &VariableValue{ScopeKey: NewKey(1, 1), Name: "x", Kind: VarNumber, Text: "42"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := AppendValue(nil, tt.v)

			// DecodeValue allocates a fresh value.
			got, err := DecodeValue(tt.vt, buf)
			if err != nil {
				t.Fatalf("DecodeValue: %v", err)
			}
			if !reflect.DeepEqual(got, tt.v) {
				t.Errorf("DecodeValue = %+v, want %+v", got, tt.v)
			}
			if got.ValueType() != tt.vt {
				t.Errorf("ValueType = %v, want %v", got.ValueType(), tt.vt)
			}

			// DecodeValueInto decodes into a caller-provided value of the same
			// concrete type.
			into := newValue(tt.vt)
			if err := DecodeValueInto(into, buf); err != nil {
				t.Fatalf("DecodeValueInto: %v", err)
			}
			if !reflect.DeepEqual(into, tt.v) {
				t.Errorf("DecodeValueInto = %+v, want %+v", into, tt.v)
			}
		})
	}
}

// TestDecodeValueNoPayloadType covers the branch where the value type has no
// payload codec.
func TestDecodeValueNoPayloadType(t *testing.T) {
	if _, err := DecodeValue(VTSignal, []byte{1, 2, 3}); err == nil {
		t.Errorf("DecodeValue(VTSignal) err = nil, want error")
	}
}

// TestDecodeValueShortBuffer covers the decode error propagation for each
// payload type on a truncated buffer.
func TestDecodeValueShortBuffer(t *testing.T) {
	for _, vt := range []ValueType{VTElementInstance, VTJob, VTTimer, VTProcessInstance, VTVariable} {
		if _, err := DecodeValue(vt, nil); !errors.Is(err, ErrShortBuffer) {
			t.Errorf("DecodeValue(%v, nil) err = %v, want ErrShortBuffer", vt, err)
		}
	}
}

// TestVariableDecodeErrors exercises every truncation guard in
// VariableValue.decode (and, transitively, readString's error returns).
func TestVariableDecodeErrors(t *testing.T) {
	full := AppendValue(nil, &VariableValue{ScopeKey: NewKey(1, 1), Name: "n", Kind: VarString, Text: "v"})
	tests := []struct {
		name string
		src  []byte
	}{
		{"short scope", full[:4]},
		{"truncated name length prefix", full[:10]}, // 8 scope + 2 of the 4-byte name length
		{"truncated name bytes", full[:12]},         // name length says 1 but no byte follows
		{"missing kind/bool", full[:13]},            // 8 + 4(len=1) + 1(name) = 13, no kind/bool
		{"truncated text", full[:15]},               // ...+kind+bool present, text length prefix truncated
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var v VariableValue
			if err := v.decode(tt.src); !errors.Is(err, ErrShortBuffer) {
				t.Errorf("decode(%d bytes) err = %v, want ErrShortBuffer", len(tt.src), err)
			}
		})
	}
}

// TestProcessInstanceStateString covers ProcessInstanceState.String() for each
// terminal state and the default (active / unknown) arm.
func TestProcessInstanceStateString(t *testing.T) {
	cases := []struct {
		s    ProcessInstanceState
		want string
	}{
		{PIActive, "active"},
		{PICompleted, "completed"},
		{PITerminated, "terminated"},
		{ProcessInstanceState(99), "active"}, // unknown value falls through to default
	}
	for _, c := range cases {
		if got := c.s.String(); got != c.want {
			t.Errorf("ProcessInstanceState(%d).String() = %q, want %q", c.s, got, c.want)
		}
	}
}

// TestNewValueNoPayload covers newValue's default branch for a value type with
// no payload struct.
func TestNewValueNoPayload(t *testing.T) {
	if v := newValue(VTMessage); v != nil {
		t.Errorf("newValue(VTMessage) = %v, want nil", v)
	}
}

// TestValueTypeMethods pins each payload's ValueType discriminator.
func TestValueTypeMethods(t *testing.T) {
	cases := []struct {
		v    Value
		want ValueType
	}{
		{(&ElementInstanceValue{}), VTElementInstance},
		{(&JobValue{}), VTJob},
		{(&TimerValue{}), VTTimer},
		{(&ProcessInstanceValue{}), VTProcessInstance},
		{(&VariableValue{}), VTVariable},
	}
	for _, c := range cases {
		if got := c.v.ValueType(); got != c.want {
			t.Errorf("ValueType = %v, want %v", got, c.want)
		}
	}
}

// TestStringersExhaustive calls String on every defined enum constant so each
// switch arm is exercised and no known value falls through to the "?" default.
func TestStringersExhaustive(t *testing.T) {
	recordTypes := []RecordType{RecordCommand, RecordEvent, RecordCommandRejection}
	for _, rt := range recordTypes {
		if s := rt.String(); s == "" || s == "RecordType(?)" {
			t.Errorf("RecordType(%d).String() = %q", rt, s)
		}
	}

	valueTypes := []ValueType{
		VTProcessInstance, VTElementInstance, VTJob, VTTimer, VTMessageSubscription,
		VTMessage, VTVariable, VTIncident, VTSignal, VTError, VTProcessDefinition,
	}
	for _, vt := range valueTypes {
		if s := vt.String(); s == "" || s == "ValueType(?)" {
			t.Errorf("ValueType(%d).String() = %q", vt, s)
		}
	}

	intents := []Intent{
		IntentActivating, IntentActivated, IntentCompleting, IntentCompleted,
		IntentTerminating, IntentTerminated, IntentSequenceFlowTaken,
		IntentJobCreated, IntentJobActivated, IntentJobCompleted, IntentJobFailed,
		IntentJobTimedOut, IntentTimerCreated, IntentTimerTriggered,
		IntentSubscriptionCreated, IntentSubscriptionCorrelated, IntentMessagePublished,
		IntentVariableCreated, IntentVariableUpdated, IntentIncidentCreated,
		IntentIncidentResolved,
	}
	for _, in := range intents {
		if s := in.String(); s == "" || s == "Intent(?)" {
			t.Errorf("Intent(%d).String() = %q", in, s)
		}
	}
}
