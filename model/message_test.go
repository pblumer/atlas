package model

import (
	"reflect"
	"testing"
)

// TestABufferedMessageRoundTrips: everything a buffered message carries survives the
// log — its name, key, id, expiry and payload — and a record cut short is refused
// rather than read as a message with no payload (ADR-0370).
func TestABufferedMessageRoundTrips(t *testing.T) {
	in := &MessageValue{
		MessageName:    "order",
		CorrelationKey: "42",
		MessageId:      "m-1",
		ExpiresAt:      1 << 50,
		BufferedAt:     1 << 49,
		Variables: []VariableValue{
			{Name: "amount", Kind: VarNumber, Text: "12.50", Index: -1},
			{Name: "note", Kind: VarString, Text: "early", ProducerKey: 9, Index: -1},
		},
	}
	raw := AppendValue(nil, in)
	v, err := DecodeValue(VTMessage, raw)
	if err != nil {
		t.Fatalf("DecodeValue: %v", err)
	}
	if got := v.(*MessageValue); !reflect.DeepEqual(got, in) {
		t.Fatalf("round trip = %+v, want %+v", got, in)
	}
	// The payload's last variable cut short is refused; a record that ends after the
	// payload is one written before BufferedAt and reads it as zero.
	for _, n := range []int{0, 3, len(raw) - 9} {
		if _, err := DecodeValue(VTMessage, raw[:n]); err == nil {
			t.Fatalf("a message cut to %d of %d bytes decoded", n, len(raw))
		}
	}

	older, err := DecodeValue(VTMessage, raw[:len(raw)-8])
	if err != nil {
		t.Fatalf("DecodeValue (without BufferedAt): %v", err)
	}
	if got := older.(*MessageValue); got.BufferedAt != 0 || len(got.Variables) != 2 {
		t.Fatalf("a record without BufferedAt = %+v, want it read as zero with its payload", got)
	}

	// Without a payload — what a consumed, expired or id-only record carries.
	bare := &MessageValue{MessageName: "order", ExpiresAt: 5}
	v, err = DecodeValue(VTMessage, AppendValue(nil, bare))
	if err != nil {
		t.Fatalf("DecodeValue (bare): %v", err)
	}
	if got := v.(*MessageValue); !reflect.DeepEqual(got, bare) {
		t.Fatalf("bare round trip = %+v, want %+v", got, bare)
	}
}
