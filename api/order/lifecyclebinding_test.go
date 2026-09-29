package order

import (
	"testing"

	"github.com/pblumer/atlas/api/catalog"
)

// A line's frozen binding in either form (ADR-0425), asked the way every starter
// asks it: where does this operation start.
func TestALineAnswersWhereEachOperationStarts(t *testing.T) {
	old := Line{ItemID: "vpn", ProvisionProcess: "p", DeprovisionProcess: "d"}
	if b := old.BindingFor(catalog.OpDeprovision); b.Process != "d" || b.Triggered() {
		t.Errorf("two-process deprovision = %+v", b)
	}
	lc := Line{ItemID: "vpn", LifecycleProcess: "vpn-lc",
		Operations: map[string]string{catalog.OpProvision: "vpn.p", catalog.OpDeprovision: "vpn.d"}}
	if b := lc.BindingFor(catalog.OpProvision); b.Process != "vpn-lc" || b.Message != "vpn.p" {
		t.Errorf("lifecycle provision = %+v", b)
	}

	o := Order{ID: "o1", Lines: []Line{lc}}
	if b := ReturnBindingOf(o, "vpn"); b.Message != "vpn.d" {
		t.Errorf("ReturnBindingOf = %+v, want the deprovision start", b)
	}
	if ReturnProcessOf(o, "vpn") != "vpn-lc" {
		t.Error("ReturnProcessOf does not name the lifecycle process")
	}
	if b := ReturnBindingOf(o, "nothing"); b.Bound() {
		t.Errorf("an unknown position = %+v, want unbound", b)
	}
}

// TestStartsOfCountsOneOperation: the attempt a new start is counts only the
// instances of the same operation.
func TestStartsOfCountsOneOperation(t *testing.T) {
	l := Line{Instances: []LineInstance{
		{Key: 1, Operation: catalog.OpProvision},
		{Key: 2, Operation: catalog.OpDeprovision},
		{Key: 3, Operation: catalog.OpDeprovision},
		{Key: 4},
	}}
	if got := l.StartsOf(catalog.OpDeprovision); got != 2 {
		t.Errorf("StartsOf(deprovision) = %d, want 2", got)
	}
	if got := l.StartsOf(catalog.OpChange); got != 0 {
		t.Errorf("StartsOf(change) = %d, want 0", got)
	}
}

// TestALifecycleLineIsReturnable: a held line whose product binds a lifecycle process
// can be given back — its deprovision start is its revocation.
func TestALifecycleLineIsReturnable(t *testing.T) {
	o := Order{ID: "o1", Lines: []Line{{ItemID: "vpn", Status: StatusDone, LifecycleProcess: "vpn-lc",
		Operations: map[string]string{catalog.OpDeprovision: "vpn.d"}}}}
	if err := Returnable(o, "vpn"); err != nil {
		t.Fatalf("Returnable: %v", err)
	}
	o.Lines[0].Operations = map[string]string{catalog.OpProvision: "vpn.p"}
	if err := Returnable(o, "vpn"); err == nil {
		t.Fatal("a line with no deprovision start was returnable")
	}
}
