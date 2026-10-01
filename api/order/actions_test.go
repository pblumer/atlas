package order

import (
	"reflect"
	"testing"

	"github.com/pblumer/atlas/api/catalog"
)

// mailboxActions is a lifecycle product that declares its actions (ADR-0429).
func mailboxActions() catalog.Item {
	return catalog.Item{ID: "mailbox", LifecycleProcess: "mailbox-lc", LifecycleForm: catalog.FormPerPosition,
		Actions: []catalog.Action{
			{Key: catalog.ActionProvision, Message: "mailbox.p", Effect: catalog.EffectProvision},
			{Key: catalog.ActionDeprovision, Message: "mailbox.d", Effect: catalog.EffectDeprovision,
				Triggers: []string{catalog.TriggerCustomer}},
			{Key: "storage-extend", Message: "mailbox.extend", Effect: catalog.EffectChange,
				Triggers: []string{catalog.TriggerCustomer}, Labels: map[string]string{"de": "Mehr Speicher"}},
		}}
}

// TestALineFreezesItsProductsActions: what an order may later ask of a position is
// what the release said when it was placed (ADR-0312), and the line owns its copy — a
// catalogue edited afterwards reaches into no order.
func TestALineFreezesItsProductsActions(t *testing.T) {
	it := mailboxActions()
	lines := linesFor(catalog.Release{Items: []catalog.Item{it}}, []string{"mailbox"}, nil, nil, nil)
	if len(lines) != 1 {
		t.Fatalf("lines = %d, want 1", len(lines))
	}
	l := lines[0]
	if !reflect.DeepEqual(l.Actions, it.Actions) {
		t.Fatalf("line actions = %+v, want %+v", l.Actions, it.Actions)
	}
	it.Actions[2].Labels["de"] = "geändert"
	it.Actions[2].Triggers[0] = catalog.TriggerOperator
	if l.Actions[2].Labels["de"] != "Mehr Speicher" || l.Actions[2].Triggers[0] != catalog.TriggerCustomer {
		t.Error("the line shares its actions with the release it was placed against")
	}
}

// TestALineAnswersItsActionsByKey: a line asked where an action starts reads the
// actions it froze — provision and deprovision by effect, the rest by key.
func TestALineAnswersItsActionsByKey(t *testing.T) {
	it := mailboxActions()
	l := Line{ItemID: "mailbox", LifecycleProcess: it.LifecycleProcess, LifecycleForm: it.LifecycleForm,
		Actions: catalog.CopyActions(it.Actions)}
	for op, want := range map[string]string{
		catalog.OpProvision: "mailbox.p", catalog.OpDeprovision: "mailbox.d", "storage-extend": "mailbox.extend",
	} {
		if b := l.BindingFor(op); b.Process != "mailbox-lc" || b.Message != want {
			t.Errorf("BindingFor(%s) = %+v, want %s", op, b, want)
		}
	}
	if b := l.BindingFor(catalog.OpChange); b.Bound() {
		t.Errorf("a change nobody declared is bound: %+v", b)
	}
	if a, ok := l.ActionNamed("storage-extend"); !ok || a.Effect != catalog.EffectChange {
		t.Errorf("ActionNamed(storage-extend) = %+v, %v", a, ok)
	}
}

// TestRebindCarriesTheActions: a line moved to a product's lifecycle process takes the
// actions that product declares, as it takes the operation map of one that still
// carries it (ADR-0427).
func TestRebindCarriesTheActions(t *testing.T) {
	o := Order{ID: "o1", Lines: []Line{
		{ItemID: "mailbox", Status: StatusDone, ProvisionProcess: "p", DeprovisionProcess: "d"},
	}}
	it := mailboxActions()
	next, moved := Rebind(o, it, "pm", "one process now", 7)
	if moved != 1 {
		t.Fatalf("moved = %d, want 1", moved)
	}
	l := next.Lines[0]
	if !reflect.DeepEqual(l.Actions, it.Actions) || l.Operations != nil {
		t.Fatalf("rebound line actions = %+v, operations = %+v", l.Actions, l.Operations)
	}
	it.Actions[2].Labels["de"] = "geändert"
	if l.Actions[2].Labels["de"] != "Mehr Speicher" {
		t.Error("the rebound line shares its actions with the catalogue")
	}
}
