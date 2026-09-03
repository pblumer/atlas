package model

import (
	"encoding/binary"
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
				TokenID:            NewKey(3, 4),
				ParentTokenID:      NewKey(3, 5),
				SourceFlowId:       9,
				MultiInstance:      2,            // an inner multi-instance iteration (ADR-0077)
				EventGatewayKey:    NewKey(3, 6), // armed by an event-based gateway (ADR-0110)
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
				RetryDueDate:       1_700_000_030_000_000_000, // backing off after a failure (ADR-0111)
			},
		},
		{
			name:   "job with assignee",
			vt:     VTJob,
			intent: IntentJobAssigned,
			value: &JobValue{
				ProcessInstanceKey: NewKey(1, 10),
				ElementInstanceKey: NewKey(1, 11),
				JobType:            1,
				Retries:            3,
				Assignee:           "alice",
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
			name:   "retry-backoff timer carries a job key",
			vt:     VTTimer,
			intent: IntentTimerCreated,
			value: &TimerValue{
				// A retry-backoff timer re-activates its job when due (ADR-0111): no element.
				ProcessInstanceKey: NewKey(2, 20),
				DueDate:            1_700_000_500,
				JobKey:             NewKey(2, 22),
			},
		},
		{
			name:   "start timer carries a process definition key",
			vt:     VTTimer,
			intent: IntentTimerCreated,
			value: &TimerValue{
				// A start timer has no owning instance/element; it names the
				// definition it instantiates on fire (ADR-0051).
				TargetElementId: 3,
				DueDate:         1_700_000_456,
				Repetitions:     0,
				ProcessDefKey:   NewKey(1, 7),
			},
		},
		{
			name:   "incident carries its job and message",
			vt:     VTIncident,
			intent: IntentIncidentCreated,
			value: &IncidentValue{
				ProcessInstanceKey: NewKey(1, 5),
				ElementInstanceKey: NewKey(1, 6),
				JobKey:             NewKey(1, 7),
				ElementId:          4,
				RaisedAt:           1_700_000_999,
				Message:            "worker: connection refused",
			},
		},
		{
			name:   "active process instance",
			vt:     VTProcessInstance,
			intent: IntentActivated,
			value: &ProcessInstanceValue{
				ProcessDefKey: NewKey(3, 2),
				CreatedAt:     1_700_000_000_000_000_000,
			},
		},
		{
			name:   "message-start process instance carries its correlation key",
			vt:     VTProcessInstance,
			intent: IntentActivated,
			value: &ProcessInstanceValue{
				ProcessDefKey:  NewKey(3, 2),
				CreatedAt:      1_700_000_000_000_000_000,
				CorrelationKey: "order-42",
			},
		},
		{
			name:   "completed process instance (history)",
			vt:     VTProcessInstance,
			intent: IntentCompleted,
			value: &ProcessInstanceValue{
				ProcessDefKey:     NewKey(3, 2),
				State:             PICompleted,
				CompletedAt:       1_700_000_000_000_000_000,
				CreatedAt:         1_699_999_999_000_000_000,
				CorrelationKey:    "order-42",
				CompletedPosition: 4_242,
			},
		},
		{
			name:   "finished process instance scheduled for a history purge",
			vt:     VTProcessInstance,
			intent: IntentCompleted,
			value: &ProcessInstanceValue{
				ProcessDefKey:     NewKey(3, 2),
				State:             PICompleted,
				CompletedAt:       1_700_000_000_000_000_000,
				CreatedAt:         1_699_999_999_000_000_000,
				CompletedPosition: 4_242,
				PurgeDueDate:      1_700_604_800_000_000_000,
			},
		},
		{
			name:   "child process instance with a TTL expiry due date",
			vt:     VTProcessInstance,
			intent: IntentActivated,
			value: &ProcessInstanceValue{
				ProcessDefKey:            NewKey(3, 2),
				CreatedAt:                1_699_999_999_000_000_000,
				ParentElementInstanceKey: NewKey(2, 7),
				ExpiryDueDate:            1_700_000_600_000_000_000,
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
			name:   "inbound delivery high-water",
			vt:     VTInboundDelivery,
			intent: IntentInboundDeliveryApplied,
			value: &InboundDeliveryValue{
				SourceID:  "clio:orders-clio:/orders",
				SourceSeq: 1_700_000_042,
			},
		},
		{
			name:   "header only, no payload",
			vt:     VTError, // a value type without a payload codec yet (VTSignal gained one in ADR-0088)
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

func TestProcessInstanceDecodeLegacy(t *testing.T) {
	// A record written before CreatedAt/CorrelationKey were appended is just the
	// legacy fixed layout (ProcessDefKey, State, CompletedAt). It must still decode,
	// leaving the newer fields at their zero values (ADR-0017).
	legacy := make([]byte, processInstanceLegacySize)
	binary.LittleEndian.PutUint64(legacy[0:], NewKey(3, 2))
	legacy[8] = byte(PICompleted)
	binary.LittleEndian.PutUint64(legacy[9:], uint64(int64(1_700_000_000_000_000_000)))

	var v ProcessInstanceValue
	if err := v.decode(legacy); err != nil {
		t.Fatalf("decode legacy: %v", err)
	}
	want := ProcessInstanceValue{
		ProcessDefKey: NewKey(3, 2),
		State:         PICompleted,
		CompletedAt:   1_700_000_000_000_000_000,
	}
	if !reflect.DeepEqual(v, want) {
		t.Errorf("legacy decode = %+v, want %+v", v, want)
	}
}

func TestElementInstanceDecodeLegacy(t *testing.T) {
	// A record written before MultiInstance was appended is just the prior fixed
	// layout (through SourceFlowId). It must still decode, leaving MultiInstance at 0
	// (ADR-0017/0077).
	full := (&ElementInstanceValue{
		ProcessInstanceKey: NewKey(3, 1),
		ProcessDefKey:      NewKey(3, 2),
		ElementId:          17,
		FlowScopeKey:       NewKey(3, 3),
		BpmnElementType:    5,
		TokenID:            NewKey(3, 4),
		ParentTokenID:      NewKey(3, 5),
		SourceFlowId:       9,
		MultiInstance:      2,
	}).encode(nil)
	legacy := full[:elementInstanceSize] // drop the trailing MultiInstance byte

	var v ElementInstanceValue
	if err := v.decode(legacy); err != nil {
		t.Fatalf("decode legacy: %v", err)
	}
	if v.MultiInstance != 0 {
		t.Errorf("legacy MultiInstance = %d, want 0 (absent → default)", v.MultiInstance)
	}
	if v.SourceFlowId != 9 || v.TokenID != NewKey(3, 4) {
		t.Errorf("legacy decode lost prior fields: %+v", v)
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
	if want := HeaderSize + elementInstanceEGSize; len(buf) != want {
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

// TestVariableProducerRoundTrip covers the write-attribution field: the element
// instance that produced a variable rides on the record, so history can answer "which
// task wrote this" without inferring it from a diff of two snapshots
// (ADR-0219).
func TestVariableProducerRoundTrip(t *testing.T) {
	producer := NewKey(1, 42)
	buf := AppendValue(nil, &VariableValue{
		ScopeKey: NewKey(1, 5), Name: "newTicket", Kind: VarString, Text: "PAT-9", ProducerKey: producer,
	})
	got, err := DecodeValue(VTVariable, buf)
	if err != nil {
		t.Fatalf("DecodeValue: %v", err)
	}
	v := got.(*VariableValue)
	if v.ProducerKey != producer {
		t.Errorf("ProducerKey = %d, want %d", v.ProducerKey, producer)
	}
	if v.Name != "newTicket" || v.Text != "PAT-9" || v.Kind != VarString {
		t.Errorf("value = %+v, want the name/kind/text unchanged by the appended field", v)
	}
}

// TestVariableProducerAppendCompatible pins the on-disk compatibility of that field:
// a record written before it exists ends after the text, and must decode as "no
// element is known to have written this" rather than failing the read of history that
// is already on disk. It also pins the reset, since DecodeValueInto reuses the value.
func TestVariableProducerAppendCompatible(t *testing.T) {
	full := AppendValue(nil, &VariableValue{
		ScopeKey: NewKey(1, 5), Name: "tickets", Kind: VarNumber, Text: "4", ProducerKey: NewKey(1, 7),
	})
	legacy := full[:len(full)-8] // exactly what the old encoder would have written

	v := VariableValue{ProducerKey: NewKey(1, 99)} // reused: carries someone else's producer
	if err := DecodeValueInto(&v, legacy); err != nil {
		t.Fatalf("DecodeValueInto(legacy): %v", err)
	}
	if v.ProducerKey != 0 {
		t.Errorf("ProducerKey = %d, want 0 — a pre-attribution record names no producer", v.ProducerKey)
	}
	if v.Name != "tickets" || v.Text != "4" || v.Kind != VarNumber {
		t.Errorf("value = %+v, want the older record's fields read back intact", v)
	}
}

// TestVariableIndexedRoundTrip covers the value-index flag. The fold cannot ask a
// compiled process whether a name is searchable — it holds the record and nothing
// else — so the answer is decided at command time and frozen into the event, and
// replay indexes exactly what the live write indexed (I6), the same discipline the
// producer key rides under.
func TestVariableIndexedRoundTrip(t *testing.T) {
	buf := AppendValue(nil, &VariableValue{
		ScopeKey: NewKey(1, 5), Name: "identityId", Kind: VarString, Text: "MT-1998",
		ProducerKey: NewKey(1, 42), Indexed: true,
	})
	got, err := DecodeValue(VTVariable, buf)
	if err != nil {
		t.Fatalf("DecodeValue: %v", err)
	}
	v := got.(*VariableValue)
	if !v.Indexed {
		t.Error("Indexed = false, want the flag the event was written with")
	}
	if v.ProducerKey != NewKey(1, 42) || v.Name != "identityId" || v.Text != "MT-1998" {
		t.Errorf("value = %+v, want the earlier fields unchanged by the appended flag", v)
	}
}

// TestVariableIndexedAppendCompatible pins its on-disk compatibility, and with it the
// one honest limitation of the index: a record written before the flag existed reads
// back as not indexed. Such a variable is findable by a content walk, never by a
// seek, until it is written again — which is why the index is seeded once from the
// declarations at startup rather than assumed complete.
func TestVariableIndexedAppendCompatible(t *testing.T) {
	full := AppendValue(nil, &VariableValue{
		ScopeKey: NewKey(1, 5), Name: "identityId", Kind: VarString, Text: "MT-1998",
		ProducerKey: NewKey(1, 7), Indexed: true,
	})
	legacy := full[:len(full)-1] // exactly what the pre-index encoder would have written

	v := VariableValue{Indexed: true} // reused: carries someone else's flag
	if err := DecodeValueInto(&v, legacy); err != nil {
		t.Fatalf("DecodeValueInto(legacy): %v", err)
	}
	if v.Indexed {
		t.Error("Indexed = true, want false — a pre-index record indexed nothing")
	}
	if v.ProducerKey != NewKey(1, 7) || v.Name != "identityId" || v.Text != "MT-1998" {
		t.Errorf("value = %+v, want the older record's fields read back intact", v)
	}
}

// TestVariableIndexText pins what the value index can hold. It is an ordered
// key-value index, so it answers equality and prefix over a byte string and nothing
// else: the scalars go in under their canonical text, and a structured value stays
// out — nobody searches for a JSON blob by its exact encoding, and putting one in
// would make the index mostly blobs. A NUL byte would break the terminator the exact
// match relies on, and an unbounded value would let one write dominate the index.
func TestVariableIndexText(t *testing.T) {
	long := ""
	for len(long) <= MaxIndexedValueBytes {
		long += "x"
	}
	for _, tc := range []struct {
		name   string
		v      VariableValue
		want   string
		wantOK bool
	}{
		{"string", VariableValue{Kind: VarString, Text: "MT-1998"}, "MT-1998", true},
		{"number", VariableValue{Kind: VarNumber, Text: "1998"}, "1998", true},
		{"true", VariableValue{Kind: VarBool, Bool: true}, "true", true},
		{"false", VariableValue{Kind: VarBool, Bool: false}, "false", true},
		{"empty-string", VariableValue{Kind: VarString, Text: ""}, "", true},
		{"null", VariableValue{Kind: VarNull}, "", false},
		{"json", VariableValue{Kind: VarJSON, Text: `{"a":1}`}, "", false},
		{"nul-byte", VariableValue{Kind: VarString, Text: "MT\x001998"}, "", false},
		{"too-long", VariableValue{Kind: VarString, Text: long}, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := tc.v.IndexText()
			if ok != tc.wantOK || (ok && got != tc.want) {
				t.Errorf("IndexText() = (%q, %v), want (%q, %v)", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}
