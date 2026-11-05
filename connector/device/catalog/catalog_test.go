package catalog

import (
	"slices"
	"strings"
	"testing"
)

func TestOperationsAreListedSorted(t *testing.T) {
	want := []string{OpDigitalRead, OpDigitalWrite, OpPulse}
	if got := Names(); !slices.Equal(got, want) {
		t.Fatalf("Names() = %v, want %v", got, want)
	}
}

func TestEveryOperationOnlyUsesKnownAttributes(t *testing.T) {
	for _, name := range Names() {
		op, ok := Lookup(name)
		if !ok || op.Name != name {
			t.Fatalf("Lookup(%q) = %+v, %v", name, op, ok)
		}
		for _, a := range append(slices.Clone(op.Needs), op.Takes...) {
			if !slices.Contains(Attributes, a) {
				t.Errorf("%s uses %q, which Attributes does not list", name, a)
			}
		}
	}
	if _, ok := Lookup("digital.toggle"); ok {
		t.Fatal("an unknown operation was found")
	}
}

func TestOnlyThePulseIsDeduplicated(t *testing.T) {
	for _, name := range Names() {
		op, _ := Lookup(name)
		if op.Idempotent == (name == OpPulse) {
			t.Errorf("%s: Idempotent = %v", name, op.Idempotent)
		}
	}
}

func TestNeedsAndUses(t *testing.T) {
	pulse, _ := Lookup(OpPulse)
	if !pulse.NeedsAttr(AttrDuration) || pulse.NeedsAttr(AttrOn) {
		t.Fatal("a pulse needs a duration and may take a state")
	}
	if !pulse.Uses(AttrOn) || pulse.Uses(AttrLine) {
		t.Fatal("a pulse uses on, not line")
	}
	read, _ := Lookup(OpDigitalRead)
	if !read.Returns {
		t.Fatal("a read returns a value")
	}
}

func TestCheckDuration(t *testing.T) {
	for _, ok := range []int64{1, 500, MaxPulse.Milliseconds()} {
		if err := CheckDuration(ok); err != nil {
			t.Errorf("CheckDuration(%d) = %v", ok, err)
		}
	}
	for _, bad := range []int64{0, -5, MaxPulse.Milliseconds() + 1} {
		if err := CheckDuration(bad); err == nil {
			t.Errorf("CheckDuration(%d) accepted", bad)
		}
	}
	if err := CheckDuration(MaxPulse.Milliseconds() + 1); !strings.Contains(err.Error(), "timer") {
		t.Errorf("a too-long pulse should say how to model it: %v", err)
	}
}

func TestCheckName(t *testing.T) {
	for _, ok := range []string{"relais", "tuer-nord", "r1", strings.Repeat("a", 32)} {
		if err := CheckName(ok); err != nil {
			t.Errorf("CheckName(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "Relais", "1relais", "relais_1", "rel ais", strings.Repeat("a", 33), "../x"} {
		if err := CheckName(bad); err == nil {
			t.Errorf("CheckName(%q) accepted", bad)
		}
	}
}
