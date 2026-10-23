package swisscomnatelgo

import (
	"context"
	"errors"
	"testing"
)

func TestMockLifecycle(t *testing.T) {
	m := NewMock()
	ctx := context.Background()
	created, err := m.Do(ctx, Request{Operation: Activate, Number: "+41791234567", IdempotencyKey: "activation-1"})
	if err != nil || created.Subscription == nil {
		t.Fatalf("activate: %+v %v", created, err)
	}
	repeat, err := m.Do(ctx, Request{Operation: Activate, Number: "+41791234567", IdempotencyKey: "activation-1"})
	if err != nil || repeat.Subscription.ID != created.Subscription.ID {
		t.Fatalf("idempotency: %+v %v", repeat, err)
	}
	id := created.Subscription.ID
	updated, err := m.Do(ctx, Request{Operation: ConfigureRoaming, SubscriptionID: id, Roaming: map[string]bool{"EU": true}})
	if err != nil || !updated.Subscription.Roaming["EU"] {
		t.Fatalf("roaming: %+v %v", updated, err)
	}
	sim, err := m.Do(ctx, Request{Operation: OrderSIM, SubscriptionID: id, SIMType: "esim"})
	if err != nil || sim.SIM == nil {
		t.Fatalf("sim: %+v %v", sim, err)
	}
	active, err := m.Do(ctx, Request{Operation: ActivateSIM, SimID: sim.SIM.ID})
	if err != nil || active.SIM.Status != "active" {
		t.Fatalf("sim activate: %+v %v", active, err)
	}
	puk, err := m.Do(ctx, Request{Operation: GetPUK, SimID: sim.SIM.ID})
	if err != nil || puk.PUK != "00000000" {
		t.Fatalf("mock PUK: %+v %v", puk, err)
	}
	inv, err := m.Do(ctx, Request{Operation: ListInventory})
	if err != nil || inv.Count != 1 {
		t.Fatalf("inventory: %+v %v", inv, err)
	}
	order, err := m.Do(ctx, Request{Operation: GetOrder, SubscriptionID: created.OrderID})
	if err != nil || order.OrderID != created.OrderID {
		t.Fatalf("order: %+v %v", order, err)
	}
}

func TestInvalidAndLiveFailClosed(t *testing.T) {
	m := NewMock()
	_, err := m.Do(context.Background(), Request{Operation: Activate, Number: "123"})
	if !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	_, err = (Live{}).Do(context.Background(), Request{Operation: ListInventory})
	if !errors.Is(err, ErrUnsupportedLive) {
		t.Fatal(err)
	}
}
