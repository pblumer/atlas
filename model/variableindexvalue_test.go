package model

import "testing"

// Every record type answers with its own tag, and the tag is what DecodeValue
// dispatches on: a type returning another's would decode one record's bytes as a
// different record, silently. VariableIndexValue's answer had no test.
func TestVariableIndexValueAnswersWithItsOwnTag(t *testing.T) {
	if got := (&VariableIndexValue{}).ValueType(); got != VTVariableIndex {
		t.Errorf("ValueType() = %v, want VTVariableIndex — the tag decode dispatches on", got)
	}
}
