package order

import "testing"

// Two phones in two colours are two positions, and the order has to be able to
// tell them apart.
//
// A line was identified by its item id, which held for exactly as long as one
// product could appear in an order once. It cannot survive the catalogue's own
// rule that the same service pulled in twice in different variants is a conflict
// the orderer resolves (ADR-0312): resolving it by
// keeping both is the case the identity could not express. Two lines with the
// same id collapse in every map the order builds — the status map, the ready
// lookup — and a provisioning outcome reported for one would land on whichever
// came first.
//
// So a line is identified by what distinguishes it: its product, and the shape of
// it that was ordered. A line with no variant keeps the product's id exactly, so
// every order ever placed and every process that reports against one is unchanged.

func twoColours() Order {
	return Order{
		ID: "ord_1", Lines: []Line{
			{ItemID: "paket", Status: StatusPending},
			{ItemID: "phone", VariantID: "black", Status: StatusPending},
			{ItemID: "phone", VariantID: "silver", Status: StatusPending},
		},
		Waves: [][]string{{"paket", "phone"}},
	}
}

// TestALineWithNoVariantIsStillNamedByItsProduct.
//
// The compatibility half, and the reason the key is derived rather than minted:
// nothing stored changes, no order is migrated, and a fulfilment process built
// against `/lines/{itemId}` keeps working.
func TestALineWithNoVariantIsStillNamedByItsProduct(t *testing.T) {
	l := Line{ItemID: "vpn"}
	if l.Key() != "vpn" {
		t.Errorf("a line with no variant is called %q, want \"vpn\" — every caller "+
			"that names a product would have to be changed for nothing", l.Key())
	}
}

// TestTwoVariantsOfOneProductAreTwoPositions.
func TestTwoVariantsOfOneProductAreTwoPositions(t *testing.T) {
	o := twoColours()
	ready := Ready(o)
	seen := map[string]bool{}
	for _, l := range ready {
		seen[l.Key()] = true
	}
	for _, want := range []string{"paket", "phone#black", "phone#silver"} {
		if !seen[want] {
			t.Errorf("%q is not ready, so one of two phones is never provisioned "+
				"(ready: %v)", want, seen)
		}
	}
	if len(ready) != 3 {
		t.Errorf("the order offers %d positions to work on, want 3", len(ready))
	}
}

// TestAnOutcomeLandsOnThePositionItNames.
//
// The half that matters most: a black phone that failed must not mark the silver
// one failed, and the two are told apart by nothing else.
func TestAnOutcomeLandsOnThePositionItNames(t *testing.T) {
	o, err := Apply(twoColours(), "phone#silver", StatusDone, 1)
	if err != nil {
		t.Fatalf("recording the silver phone: %v", err)
	}
	for _, l := range o.Lines {
		switch l.Key() {
		case "phone#silver":
			if l.Status != StatusDone {
				t.Errorf("the position that was reported is %s, want done", l.Status)
			}
		case "phone#black":
			if l.Status != StatusPending {
				t.Errorf("reporting one phone moved the other to %s", l.Status)
			}
		}
	}
}
