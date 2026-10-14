package api

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/api/order"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// receipts is a read view that holds trigger receipts and nothing else.
type receipts struct {
	state.Reader
	held map[string]bool
}

func (r receipts) TriggerReceipt(source, triggerID string) (uint64, bool, error) {
	return 1, r.held[source+"|"+triggerID], nil
}

func (r receipts) GetElementInstance(uint64) (*model.ElementInstanceValue, bool, error) {
	return nil, false, nil
}

// shopServer is a server holding one order: a mailbox whose change was asked as c-1
// and recorded on the order, and whose reset was asked as c-2 and is known only by
// the trigger's receipt.
func shopServer(t *testing.T) *Server {
	t.Helper()
	store, err := order.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.Save(order.Order{ID: "ord_1", Orderer: "usr_a", Recipient: "usr_b", Lines: []order.Line{{
		ItemID: "mailbox", Status: order.StatusDone, LifecycleProcess: "mbx-strand", LifecycleForm: catalog.FormPerPosition,
		Actions: []catalog.Action{
			{Key: "provision", Message: "mbx.provision", Effect: catalog.EffectProvision},
			{Key: "change", Message: "mbx.change", Effect: catalog.EffectChange},
			{Key: "reset", Message: "mbx.reset", Effect: catalog.EffectService},
		},
		Instances: []order.LineInstance{
			{Key: 7, Operation: "provision", CommandID: "order:ord_1:mailbox:provision:1"},
			{Key: 8, Operation: "change", CommandID: "c-1"},
			{Key: 9, Operation: "audit", CommandID: "c-3"},
		},
	}}}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	return &Server{orderStore: store, now: func() int64 { return 1000 }}
}

func shopScope(orderID, position, command string) map[string]model.VariableValue {
	vars := map[string]model.VariableValue{}
	for name, text := range map[string]string{progressOrderVar: orderID, progressPositionVar: position, commandIDVar: command} {
		if text != "" {
			vars[name] = model.VariableValue{Name: name, Kind: model.VarString, Text: text}
		}
	}
	return vars
}

// TestAShopTaskStatesOnlyWhatItsInstanceCarriesOut: a shop task states how a command
// ended only for an instance an order act started, for the action that command asked
// for, and never for a provision or a return, which the line's report records. Each
// refusal is an incident that says what does not match.
func TestAShopTaskStatesOnlyWhatItsInstanceCarriesOut(t *testing.T) {
	s := shopServer(t)
	rd := personalReader{Reader: receipts{held: map[string]bool{
		orderTriggerSource + "|order:ord_1:mailbox:reset:c-2": true,
	}}}
	refused := []struct {
		name, action string
		scope        map[string]model.VariableValue
		want         string
	}{
		{"no command in scope", "change", shopScope("ord_1", "mailbox", ""), "carries no orderId, positionId and commandId"},
		{"a number for the order", "change", map[string]model.VariableValue{
			progressOrderVar:    {Kind: model.VarNumber, Text: "1"},
			progressPositionVar: {Kind: model.VarString, Text: "mailbox"},
			commandIDVar:        {Kind: model.VarString, Text: "c-1"},
		}, "carries no orderId"},
		{"an order that is not there", "change", shopScope("ord_9", "mailbox", "c-1"), "no order ord_9"},
		{"a position the order does not hold", "change", shopScope("ord_1", "vpn", "c-1"), "carries no line for vpn"},
		{"a command asked for another action", "reset", shopScope("ord_1", "mailbox", "c-1"), "took no command c-1 for reset"},
		{"a command never asked", "change", shopScope("ord_1", "mailbox", "c-9"), "took no command c-9"},
		{"an action the product does not declare", "audit", shopScope("ord_1", "mailbox", "c-3"), "declares no action audit"},
		{"a provision", "provision", shopScope("ord_1", "mailbox", "order:ord_1:mailbox:provision:1"), "line's report route"},
	}
	for _, c := range refused {
		t.Run(c.name, func(t *testing.T) {
			_, err := s.shopOutcome(rd, c.action, "completed", c.scope)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("shopOutcome = %v, want a refusal naming %q", err, c.want)
			}
		})
	}

	// The order's own record of the command answers for the change ...
	got, err := s.shopOutcome(rd, "change", "rejected", shopScope("ord_1", "mailbox", "c-1"))
	if err != nil {
		t.Fatalf("change: %v", err)
	}
	if got.Source != outcomeSourceShop || got.Outcome != "rejected" || got.Effect != catalog.EffectChange ||
		got.Principal != "usr_b" || got.ItemID != "mailbox" || got.At != 1000 || got.EventType == "" {
		t.Errorf("change stated %+v", got)
	}
	// ... and the trigger's receipt for the reset, which the order has not noted yet.
	got, err = s.shopOutcome(rd, "reset", "completed", shopScope("ord_1", "mailbox", "c-2"))
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if got.CommandID != "c-2" || got.Action != "reset" || !got.Valid() {
		t.Errorf("reset stated %+v", got)
	}
}

// TestAShopTaskWhoseElementIsGoneStatesNothing: the task finished or was cancelled
// between its claim and its handler; there is nothing to state and nothing to fail.
func TestAShopTaskWhoseElementIsGoneStatesNothing(t *testing.T) {
	s := shopServer(t)
	done, err := s.shopTaskHandler(receipts{})(job.Job{Key: 1, ElementInstanceKey: 2})
	if err != nil || done.Outcome != nil {
		t.Fatalf("handler = %+v, %v; want an empty completion", done, err)
	}
}
