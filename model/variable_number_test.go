package model

import (
	"math"
	"strconv"
	"testing"
)

func TestVariableNumberUintReadsOnlyPositiveIntegers(t *testing.T) {
	enc := func(kind VarKind, text string) []byte {
		return AppendValue(nil, &VariableValue{ScopeKey: 7, Name: "loopCounter", Kind: kind, Text: text, Index: -1})
	}
	tests := []struct {
		name string
		raw  []byte
		want uint64
		ok   bool
	}{
		{"one", enc(VarNumber, "1"), 1, true},
		{"large", enc(VarNumber, "37421"), 37421, true},
		{"max", enc(VarNumber, strconv.FormatUint(math.MaxUint64, 10)), math.MaxUint64, true},
		{"overflow", enc(VarNumber, "18446744073709551616"), 0, false},
		{"zero", enc(VarNumber, "0"), 0, false},
		{"leading zero", enc(VarNumber, "01"), 0, false},
		{"negative", enc(VarNumber, "-1"), 0, false},
		{"fraction", enc(VarNumber, "1.5"), 0, false},
		{"exponent", enc(VarNumber, "1e3"), 0, false},
		{"empty", enc(VarNumber, ""), 0, false},
		{"string kind", enc(VarString, "3"), 0, false},
		{"too long", enc(VarNumber, "123456789012345678901"), 0, false},
		{"nil", nil, 0, false},
		{"short name", []byte{1, 2, 3, 4, 5, 6, 7, 8, 200, 0, 0, 0}, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := VariableNumberUint(tt.raw)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("VariableNumberUint = %d, %v; want %d, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
	// Truncations of a valid record must never read as a number.
	full := enc(VarNumber, "42")
	for i := 0; i < len(full); i++ {
		if _, ok := VariableNumberUint(full[:i]); ok && i < 8+4+len("loopCounter")+2+4+2 {
			t.Fatalf("truncated record of %d bytes accepted", i)
		}
	}
}

func TestVariableNumberUintDoesNotAllocate(t *testing.T) {
	raw := AppendValue(nil, &VariableValue{ScopeKey: 7, Name: "loopCounter", Kind: VarNumber, Text: "50000", Index: -1})
	if allocs := testing.AllocsPerRun(100, func() {
		if n, ok := VariableNumberUint(raw); !ok || n != 50000 {
			panic("wrong value")
		}
	}); allocs != 0 {
		t.Fatalf("VariableNumberUint allocated %.1f times", allocs)
	}
}
