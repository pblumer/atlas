package order

import (
	"strings"
	"testing"
)

// Recording which instances work a position (ADR-draft-the-shop-shows-an-orders-open-tasks).

func twoPositionOrder() Order {
	return Order{ID: "ord_1", Orderer: "usr_1", Recipient: "usr_1", Lines: []Line{
		{ItemID: "vpn", Status: StatusPending},
		{ItemID: "phone", VariantID: "black", Status: StatusPending},
		{ItemID: "phone", VariantID: "silver", Status: StatusPending},
	}}
}

// TestRecordInstanceNotesItOnThePosition: appended in order, once per instance, on
// the named position and no other, and on a copy rather than on the order handed in.
func TestRecordInstanceNotesItOnThePosition(t *testing.T) {
	o := twoPositionOrder()
	got, err := RecordInstance(o, "vpn", LineInstance{Key: 7, ProcessID: "approve"})
	if err != nil {
		t.Fatalf("RecordInstance: %v", err)
	}
	got, err = RecordInstance(got, "vpn", LineInstance{Key: 9, ProcessID: "provision"})
	if err != nil {
		t.Fatalf("RecordInstance: %v", err)
	}
	// The same instance twice is recorded once: a caller that retries must not make
	// one instance look like two.
	got, err = RecordInstance(got, "vpn", LineInstance{Key: 7, ProcessID: "approve"})
	if err != nil {
		t.Fatalf("RecordInstance again: %v", err)
	}
	vpn := got.Lines[0].Instances
	if len(vpn) != 2 || vpn[0].Key != 7 || vpn[1].Key != 9 {
		t.Errorf("vpn instances = %+v, want 7 then 9", vpn)
	}
	if len(got.Lines[1].Instances) != 0 || len(got.Lines[2].Instances) != 0 {
		t.Error("an instance was recorded on a position it does not work")
	}
	if len(o.Lines[0].Instances) != 0 {
		t.Error("RecordInstance changed the order it was handed rather than a copy")
	}

	// A position of a product ordered in two shapes is named by its key.
	got, err = RecordInstance(got, "phone#silver", LineInstance{Key: 11})
	if err != nil || len(got.Lines[2].Instances) != 1 || len(got.Lines[1].Instances) != 0 {
		t.Errorf("phone#silver = %+v / %v, want the silver phone's position alone", got.Lines, err)
	}
}

// TestRecordInstanceRefusesAPositionItCannotName: a product the order carries
// twice does not name one position, and a product it does not carry names none.
func TestRecordInstanceRefusesAPositionItCannotName(t *testing.T) {
	o := twoPositionOrder()
	if _, err := RecordInstance(o, "phone", LineInstance{Key: 1}); err == nil ||
		!strings.Contains(err.Error(), "2 positions") {
		t.Errorf("ambiguous product = %v, want a refusal naming both positions", err)
	}
	if _, err := RecordInstance(o, "tablet", LineInstance{Key: 1}); err == nil {
		t.Error("a product the order does not carry was accepted")
	}
}

// TestServiceRecordInstanceSavesItOnTheOrder: through the service, onto the stored
// order; an order this server does not hold is not an error, only not found.
func TestServiceRecordInstanceSavesItOnTheOrder(t *testing.T) {
	svc := newService(t)
	if err := svc.store.Save(twoPositionOrder()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	found, err := svc.RecordInstance("ord_1", "vpn", LineInstance{Key: 42, ProcessID: "p"})
	if err != nil || !found {
		t.Fatalf("RecordInstance = %v, %v; want found", found, err)
	}
	stored, _, err := svc.store.Get("ord_1")
	if err != nil || len(stored.Lines[0].Instances) != 1 || stored.Lines[0].Instances[0].Key != 42 {
		t.Errorf("stored vpn instances = %+v (%v), want 42", stored.Lines[0].Instances, err)
	}

	found, err = svc.RecordInstance("ord_nobody", "vpn", LineInstance{Key: 1})
	if found || err != nil {
		t.Errorf("an unknown order = %v, %v; want not found and no error", found, err)
	}
	if _, err := svc.RecordInstance("ord_1", "tablet", LineInstance{Key: 1}); err == nil {
		t.Error("a position the order does not carry was accepted")
	}
}
