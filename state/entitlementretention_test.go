package state

import (
	"testing"

	"github.com/pblumer/atlas/model"
)

// A right outlives the order that granted it.
//
// The instance behind an entitlement is eligible for retention deletion in ninety
// days; the access it produced can last years, and answering "who had this, and
// when" after the order is gone is the reason the inventory is engine state with
// a family of its own rather than something derived from order history.
//
// So the purge must not reach it. This is the amendment to ADR-0115 and ADR-0144
// that ADR-0312 calls non-optional, made
// checkable: the day somebody adds an entitlement prefix to the purge's list, or
// keys an entitlement by instance, this says so.
func TestPurgingAnInstanceLeavesTheRightItGrantedStanding(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	const defKey = uint64(9)
	piKey := model.NewKey(1, 1)

	tx := s.NewTransaction()
	must(t, tx.PutProcessInstanceHistory(piKey, &model.ProcessInstanceValue{
		ProcessDefKey: defKey, State: model.PICompleted, CompletedPosition: 5}))
	must(t, tx.PutEntitlement(&model.EntitlementValue{
		Principal: "usr_ada", ItemID: "laptop", VariantID: "14zoll",
		OrderID: "ord_1", Since: 1_700_000_000, Origin: model.OriginOrdered}))
	commit(t, tx)

	tx = s.NewTransaction()
	must(t, tx.PurgeInstanceHistory(piKey, defKey, 0))
	commit(t, tx)

	got, ok, err := s.Entitlement("usr_ada", "laptop")
	if err != nil {
		t.Fatalf("Entitlement: %v", err)
	}
	if !ok {
		t.Fatal("the right was deleted with the order that granted it; " +
			"an access record that cannot outlive its order is not a record")
	}
	// Whole, not merely present: a right that survives as a husk answers "who had
	// this" with nothing an auditor can use.
	want := model.EntitlementValue{
		Principal: "usr_ada", ItemID: "laptop", VariantID: "14zoll",
		OrderID: "ord_1", Since: 1_700_000_000, Origin: model.OriginOrdered}
	if *got != want {
		t.Errorf("= %+v, want %+v", *got, want)
	}
	if n, err := s.EntitlementCount(); err != nil || n != 1 {
		t.Errorf("inventory holds %d rights (err %v), want 1", n, err)
	}
}
