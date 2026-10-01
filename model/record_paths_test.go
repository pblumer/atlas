package model

import "testing"

// TestEveryDefinedTagHasItsOwnName walks the whole defined range rather than a list
// somebody maintains by hand — the list in TestStringersExhaustive had already fallen
// behind the enum it checks. A tag that printed as "?" would turn a timeline row or a
// log line into a number nobody can read, and two tags with one name would make two
// different facts look the same.
func TestEveryDefinedTagHasItsOwnName(t *testing.T) {
	seen := map[string]ValueType{}
	for vt := VTProcessInstance; vt <= VTActionOutcome; vt++ {
		name := vt.String()
		if name == "ValueType(?)" {
			t.Errorf("ValueType(%d) has no name", vt)
		} else if other, dup := seen[name]; dup {
			t.Errorf("ValueType(%d) and ValueType(%d) are both %q", other, vt, name)
		}
		seen[name] = vt
	}
	if got := (VTActionOutcome + 1).String(); got != "ValueType(?)" {
		t.Errorf("a tag past the last one = %q, want the guard label", got)
	}

	seenIntent := map[string]Intent{}
	for in := IntentActivating; in <= IntentActionCompleted; in++ {
		name := in.String()
		if name == "Intent(?)" {
			t.Errorf("Intent(%d) has no name", in)
		} else if other, dup := seenIntent[name]; dup {
			t.Errorf("Intent(%d) and Intent(%d) are both %q", other, in, name)
		}
		seenIntent[name] = in
	}

	for kind, want := range map[OperatorActionKind]string{
		OperatorActionForkedTo:   "forkedTo",
		OperatorActionForkedFrom: "forkedFrom",
	} {
		if got := kind.String(); got != want {
			t.Errorf("OperatorActionKind(%d) = %q, want %q", kind, got, want)
		}
	}
}

// TestAnIndexRemovalRoundTrips: Indexed=false is the record that takes a variable out
// of the value index, and it must come back as false rather than as a missing byte.
func TestAnIndexRemovalRoundTrips(t *testing.T) {
	for _, indexed := range []bool{false, true} {
		in := &VariableIndexValue{ProcessInstanceKey: 42, Name: "customerId", Indexed: indexed}
		v, err := DecodeValue(VTVariableIndex, AppendValue(nil, in))
		if err != nil {
			t.Fatalf("DecodeValue(indexed=%v): %v", indexed, err)
		}
		if got := v.(*VariableIndexValue); *got != *in {
			t.Fatalf("round trip = %+v, want %+v", got, in)
		}
	}
}
