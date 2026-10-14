package order

import (
	"reflect"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/catalog"
)

// TestAReturnBlockedTwiceNamesBothDependents: every held line that still needs this
// one is in the way, and the refusal lists them all, sorted, so the next attempt is
// not a surprise about the second.
func TestAReturnBlockedTwiceNamesBothDependents(t *testing.T) {
	o := aHeldOrder()
	o.Requires["phone"] = []string{"account"}
	o.Lines = append(o.Lines, Line{ItemID: "phone", Status: StatusDone, DeprovisionProcess: "phone-einziehen"})

	err := Returnable(o, "account")
	if err == nil {
		t.Fatal("the account was given back under two products that still need it")
	}
	if !strings.Contains(err.Error(), "[laptop phone], which are still held") {
		t.Fatalf("Returnable = %v, want both dependents named, in order", err)
	}
}

// TestReturningRefusesWhatCannotGoBack: marking a line as returning is the step that
// starts a revocation, so it carries the same refusal and leaves the order untouched.
func TestReturningRefusesWhatCannotGoBack(t *testing.T) {
	o := aHeldOrder()
	got, err := Returning(o, "account", 100, "usr_ada")
	if err == nil || !strings.Contains(err.Error(), "laptop") {
		t.Fatalf("Returning the needed account = %v, want the refusal naming the laptop", err)
	}
	if !reflect.DeepEqual(got, o) {
		t.Fatalf("a refused return changed the order: %+v", got)
	}
}

// TestNoReturnProcessForALineTheOrderDoesNotCarry: asked about a position that is not
// there, the answer is no process, never some other line's.
func TestNoReturnProcessForALineTheOrderDoesNotCarry(t *testing.T) {
	o := aHeldOrder()
	if got := ReturnBindingOf(o, "printer"); got != (catalog.Binding{}) {
		t.Fatalf("ReturnBindingOf(printer) = %+v, want the zero binding", got)
	}
	if got := ReturnProcessOf(o, "printer"); got != "" {
		t.Fatalf("ReturnProcessOf(printer) = %q, want none", got)
	}
	if got := ReturnProcessOf(o, "laptop"); got != "laptop-einziehen" {
		t.Fatalf("ReturnProcessOf(laptop) = %q, want the process the order froze", got)
	}
}
