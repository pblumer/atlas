package model

import (
	"errors"
	"reflect"
	"testing"
)

func sampleHeader() RecordHeader {
	return RecordHeader{
		Position:    1024,
		SourcePos:   1000,
		Key:         NewKey(3, 77),
		Timestamp:   1_700_000_000_000_000_000,
		RecordType:  RecordEvent,
		ValueType:   VTElementInstance,
		Intent:      IntentActivated,
		PartitionId: 3,
	}
}

func TestRecordRoundTrip(t *testing.T) {
	tests := []struct {
		name   string
		vt     ValueType
		intent Intent
		value  Value
	}{
		{
			name:   "element instance",
			vt:     VTElementInstance,
			intent: IntentActivated,
			value: &ElementInstanceValue{
				ProcessInstanceKey: NewKey(3, 1),
				ProcessDefKey:      NewKey(3, 2),
				ElementId:          17,
				FlowScopeKey:       NewKey(3, 3),
				BpmnElementType:    5,
			},
		},
		{
			name:   "job",
			vt:     VTJob,
			intent: IntentJobCreated,
			value: &JobValue{
				ProcessInstanceKey: NewKey(1, 10),
				ElementInstanceKey: NewKey(1, 11),
				JobType:            42,
				Retries:            3,
				Deadline:           1_700_000_000,
			},
		},
		{
			name:   "number variable",
			vt:     VTVariable,
			intent: IntentVariableCreated,
			value: &VariableValue{
				ScopeKey: NewKey(1, 5),
				Name:     "answer",
				Kind:     VarNumber,
				Text:     "42",
			},
		},
		{
			name:   "string variable",
			vt:     VTVariable,
			intent: IntentVariableUpdated,
			value: &VariableValue{
				ScopeKey: NewKey(1, 5),
				Name:     "Season Label",
				Kind:     VarString,
				Text:     "Winter",
			},
		},
		{
			name:   "bool variable",
			vt:     VTVariable,
			intent: IntentVariableCreated,
			value: &VariableValue{
				ScopeKey: NewKey(1, 5),
				Name:     "ok",
				Kind:     VarBool,
				Bool:     true,
			},
		},
		{
			name:   "json variable",
			vt:     VTVariable,
			intent: IntentVariableCreated,
			value: &VariableValue{
				ScopeKey: NewKey(1, 5),
				Name:     "customer",
				Kind:     VarJSON,
				Text:     `{"id":7,"name":"acme","tags":["a","b"]}`,
			},
		},
		{
			name:   "timer with infinite repetitions",
			vt:     VTTimer,
			intent: IntentTimerCreated,
			value: &TimerValue{
				ProcessInstanceKey: NewKey(2, 20),
				ElementInstanceKey: NewKey(2, 21),
				TargetElementId:    8,
				DueDate:            1_700_000_123,
				Repetitions:        -1,
			},
		},
		{
			name:   "active process instance",
			vt:     VTProcessInstance,
			intent: IntentActivated,
			value: &ProcessInstanceValue{
				ProcessDefKey: NewKey(3, 2),
			},
		},
		{
			name:   "completed process instance (history)",
			vt:     VTProcessInstance,
			intent: IntentCompleted,
			value: &ProcessInstanceValue{
				ProcessDefKey: NewKey(3, 2),
				State:         PICompleted,
				CompletedAt:   1_700_000_000_000_000_000,
			},
		},
		{
			name:   "message subscription",
			vt:     VTMessageSubscription,
			intent: IntentSubscriptionCreated,
			value: &MessageSubscriptionValue{
				ProcessInstanceKey: NewKey(2, 30),
				ElementInstanceKey: NewKey(2, 31),
				MessageName:        "payment-received",
				CorrelationKey:     "order-42",
			},
		},
		{
			name:   "header only, no payload",
			vt:     VTSignal, // a value type without a payload codec yet
			intent: IntentActivating,
			value:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := sampleHeader()
			h.ValueType = tt.vt
			h.Intent = tt.intent
			in := Record{Header: h, Value: tt.value}

			buf := AppendRecord(nil, &in)

			got, err := ReadRecord(buf)
			if err != nil {
				t.Fatalf("ReadRecord: %v", err)
			}
			if !reflect.DeepEqual(got, in) {
				t.Errorf("round trip mismatch:\n got = %+v\nwant = %+v", got, in)
			}
		})
	}
}

func TestAppendRecordIsAppendOnly(t *testing.T) {
	// AppendRecord must extend the existing buffer, not overwrite a prefix.
	prefix := []byte{0xDE, 0xAD}
	// The value must match the header's ValueType (VTElementInstance); ReadRecord
	// decodes the payload as whatever the header declares.
	r := Record{Header: sampleHeader(), Value: &ElementInstanceValue{ElementId: 1}}
	buf := AppendRecord(prefix, &r)
	if buf[0] != 0xDE || buf[1] != 0xAD {
		t.Fatalf("prefix was clobbered: % x", buf[:2])
	}
	got, err := ReadRecord(buf[2:])
	if err != nil {
		t.Fatalf("ReadRecord: %v", err)
	}
	if got.Header.Position != r.Header.Position {
		t.Errorf("Position = %d, want %d", got.Header.Position, r.Header.Position)
	}
}

func TestReadRecordShortBuffer(t *testing.T) {
	// Truncated header.
	if _, err := ReadRecord(make([]byte, HeaderSize-1)); !errors.Is(err, ErrShortBuffer) {
		t.Errorf("short header: err = %v, want ErrShortBuffer", err)
	}

	// Full header but truncated payload.
	r := Record{Header: sampleHeader(), Value: &ElementInstanceValue{ElementId: 1}}
	buf := AppendRecord(nil, &r)
	if _, err := ReadRecord(buf[:HeaderSize+2]); !errors.Is(err, ErrShortBuffer) {
		t.Errorf("short payload: err = %v, want ErrShortBuffer", err)
	}
}

func TestReadRecordUnknownVersion(t *testing.T) {
	r := Record{Header: sampleHeader()}
	buf := AppendRecord(nil, &r)
	buf[0] = codecVersion + 1
	_, err := ReadRecord(buf)
	if !errors.Is(err, ErrUnknownVersion) {
		t.Errorf("err = %v, want ErrUnknownVersion", err)
	}
}

func TestEncodedSize(t *testing.T) {
	r := Record{Header: sampleHeader(), Value: &ElementInstanceValue{}}
	buf := AppendRecord(nil, &r)
	if want := HeaderSize + elementInstanceSize; len(buf) != want {
		t.Errorf("encoded size = %d, want %d", len(buf), want)
	}
}

// TestAppendRecordNoAlloc pins invariant I1: encoding an event into a reused
// buffer must not allocate. If this starts failing, something on the encode
// path began allocating per record.
func TestAppendRecordNoAlloc(t *testing.T) {
	r := Record{Header: sampleHeader(), Value: &ElementInstanceValue{
		ProcessInstanceKey: NewKey(3, 1),
		ElementId:          17,
		BpmnElementType:    5,
	}}
	buf := make([]byte, 0, 128) // pre-grown; reused across iterations

	allocs := testing.AllocsPerRun(1000, func() {
		buf = AppendRecord(buf[:0], &r)
	})
	if allocs != 0 {
		t.Errorf("AppendRecord allocated %v times per run, want 0", allocs)
	}
}
