package model

import (
	"errors"
	"reflect"
	"testing"
)

// TestDataObjectValueRoundTrip encodes and decodes a DataObjectValue, covering
// both the DecodeValue (fresh) and DecodeValueInto (reused) paths and every
// VarKind the value can carry, including the structured VarJSON payload and a
// non-empty data state.
func TestDataObjectValueRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		v    *DataObjectValue
	}{
		{
			name: "unset with state",
			v:    &DataObjectValue{ScopeKey: NewKey(1, 1), Name: "order", State: "received", Kind: VarNull},
		},
		{
			name: "json value with state",
			v:    &DataObjectValue{ScopeKey: NewKey(2, 5), Name: "order", State: "approved", Kind: VarJSON, Text: `{"amount":42}`},
		},
		{
			name: "bool no state",
			v:    &DataObjectValue{ScopeKey: NewKey(1, 9), Name: "flag", Kind: VarBool, Bool: true},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			buf := AppendValue(nil, c.v)

			got, err := DecodeValue(VTDataObject, buf)
			if err != nil {
				t.Fatalf("DecodeValue: %v", err)
			}
			if !reflect.DeepEqual(got, c.v) {
				t.Errorf("DecodeValue = %+v, want %+v", got, c.v)
			}
			if got.ValueType() != VTDataObject {
				t.Errorf("ValueType = %v, want %v", got.ValueType(), VTDataObject)
			}

			into := newValue(VTDataObject)
			if err := DecodeValueInto(into, buf); err != nil {
				t.Fatalf("DecodeValueInto: %v", err)
			}
			if !reflect.DeepEqual(into, c.v) {
				t.Errorf("DecodeValueInto = %+v, want %+v", into, c.v)
			}
		})
	}
}

// TestDataObjectDecodeErrors exercises every truncation guard in
// DataObjectValue.decode: the fixed scope prefix and each length-prefixed string,
// plus the kind/bool bytes between the name/state and the text.
func TestDataObjectDecodeErrors(t *testing.T) {
	full := AppendValue(nil, &DataObjectValue{ScopeKey: NewKey(1, 1), Name: "n", State: "s", Kind: VarString, Text: "v"})
	tests := []struct {
		name string
		src  []byte
	}{
		{"short scope", full[:4]},
		{"truncated name length prefix", full[:10]},  // 8 scope + 2 of the 4-byte name length
		{"truncated name bytes", full[:12]},          // name length says 1 but no byte follows
		{"truncated state length prefix", full[:15]}, // 8 + 4 + 1(name) + 2 of state length
		{"truncated state bytes", full[:17]},         // state length says 1 but no byte follows
		{"missing kind/bool", full[:18]},             // through the state string, no kind/bool
		{"truncated text", full[:20]},                // kind+bool present, text length prefix truncated
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var v DataObjectValue
			if err := v.decode(tt.src); !errors.Is(err, ErrShortBuffer) {
				t.Errorf("decode(%d bytes) err = %v, want ErrShortBuffer", len(tt.src), err)
			}
		})
	}
}
