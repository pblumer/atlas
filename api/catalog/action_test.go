package catalog

import (
	"reflect"
	"testing"
)

// actionItem is a per-operation lifecycle product that declares its actions rather than
// an operation map: the two the order needs, a change the customer asks for and a
// service an operator runs (ADR-0429).
func actionItem(id string) Item {
	it := item(id)
	it.ProvisionProcess, it.DeprovisionProcess = "", ""
	it.LifecycleProcess = id + "-lifecycle"
	it.Actions = []Action{
		{Key: ActionProvision, Message: id + ".provision", Effect: EffectProvision},
		{Key: ActionDeprovision, Message: id + ".deprovision", Effect: EffectDeprovision,
			Triggers: []string{TriggerCustomer, TriggerOperator}},
		{Key: "storage-extend", Message: id + ".storage.extend", Effect: EffectChange,
			Triggers: []string{TriggerCustomer}, Labels: map[string]string{"de": "Mehr Speicher"},
			Outcomes: map[string]string{OutcomeCompleted: id + ".storage.extended"}},
		{Key: "password-reset", Message: id + ".password.reset", Effect: EffectService,
			Triggers: []string{TriggerOperator}, Labels: map[string]string{"de": "Passwort zurücksetzen"}},
	}
	return it
}

// TestActionsPublish: a product declaring its actions in full is a complete binding.
func TestActionsPublish(t *testing.T) {
	if problems := publishLifecycleItem(actionItem("mailbox")); len(problems) != 0 {
		t.Fatalf("problems = %v, want none", problems)
	}
}

// TestActionsOweTheirShape: every rule the catalogue can check alone, one failing case
// each, so a rule that stops being enforced is a test that stops failing.
func TestActionsOweTheirShape(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Item)
		want   string
	}{
		{"operations beside actions", func(it *Item) {
			it.Operations = map[string]string{OpProvision: "x"}
		}, "carries an operation map and actions"},
		{"actions on the two-process form", func(it *Item) {
			it.LifecycleProcess, it.ProvisionProcess, it.DeprovisionProcess = "", "p", "d"
		}, "names actions but binds no lifecycle process"},
		{"no provision", func(it *Item) { it.Actions = it.Actions[1:] }, "declares no action of effect provision"},
		{"two deprovisions", func(it *Item) {
			it.Actions = append(it.Actions, Action{Key: "give-back", Message: "m", Effect: EffectDeprovision,
				Triggers: []string{TriggerCustomer}})
		}, "declares more than one action of effect deprovision"},
		{"provision under another key", func(it *Item) { it.Actions[0].Key = "order" }, "an action of effect provision is keyed provision"},
		{"a reserved key on another effect", func(it *Item) { it.Actions[2].Key = ActionDeprovision }, "key deprovision is reserved"},
		{"a key that is not a key", func(it *Item) { it.Actions[2].Key = "Storage Extend" }, "key \"Storage Extend\" is not"},
		{"a key used twice", func(it *Item) { it.Actions[3].Key = "storage-extend" }, "key storage-extend is used twice"},
		{"no message", func(it *Item) { it.Actions[2].Message = " " }, "action storage-extend names no message"},
		{"a message used twice", func(it *Item) { it.Actions[3].Message = it.Actions[2].Message }, "message mailbox.storage.extend is used by two actions"},
		{"an unknown effect", func(it *Item) { it.Actions[2].Effect = "upgrade" }, "effect upgrade is not one"},
		{"a triggered provision", func(it *Item) { it.Actions[0].Triggers = []string{TriggerCustomer} }, "the order starts it"},
		{"a system deprovision", func(it *Item) { it.Actions[1].Triggers = []string{TriggerSystem} }, "deprovision is triggered by customer or operator"},
		{"a change nobody triggers", func(it *Item) { it.Actions[2].Triggers = nil }, "action storage-extend names no trigger"},
		{"an unknown trigger", func(it *Item) { it.Actions[3].Triggers = []string{"robot"} }, "trigger robot is not one"},
		{"an unknown outcome", func(it *Item) { it.Actions[2].Outcomes = map[string]string{"done": "x"} }, "outcome done is not one"},
		{"an outcome with no event type", func(it *Item) { it.Actions[2].Outcomes = map[string]string{OutcomeFailed: " "} }, "outcome failed names no event type"},
		{"a blank form", func(it *Item) { it.Actions[2].Form = "  " }, "names a blank form"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			it := actionItem("mailbox")
			c.mutate(&it)
			contains(t, publishLifecycleItem(it), c.want)
		})
	}
}

// TestActionListReadsTheOperationMap: a product that still carries the operation map is
// read as the actions it means, so every consumer asks one question of either shape.
func TestActionListReadsTheOperationMap(t *testing.T) {
	legacy := lifecycleItem("laptop")
	legacy.Operations[OpChange] = "laptop.change"
	got := legacy.ActionList()
	want := []Action{
		{Key: ActionProvision, Message: "laptop.provision", Effect: EffectProvision},
		{Key: ActionDeprovision, Message: "laptop.deprovision", Effect: EffectDeprovision,
			Triggers: []string{TriggerCustomer, TriggerOperator}},
		{Key: ActionChange, Message: "laptop.change", Effect: EffectChange,
			Triggers: []string{TriggerCustomer, TriggerOperator}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ActionList = %+v\nwant %+v", got, want)
	}
	declared := actionItem("mailbox")
	if got := declared.ActionList(); !reflect.DeepEqual(got, declared.Actions) {
		t.Fatalf("declared actions read as %+v", got)
	}
	if got := item("vpn").ActionList(); got != nil {
		t.Fatalf("a two-process product has actions %+v, want none", got)
	}
}

// TestBindingForReadsActions: where an operation starts is the same answer whether the
// product says it in the old map or as actions, and an action of any other key is
// addressable by that key.
func TestBindingForReadsActions(t *testing.T) {
	legacy := lifecycleItem("mailbox")
	legacy.Operations[OpChange] = "mailbox.change"
	declared := legacy
	declared.Operations = nil
	declared.Actions = legacy.ActionList()
	for _, op := range []string{OpProvision, OpDeprovision, OpChange} {
		if a, b := legacy.BindingFor(op), declared.BindingFor(op); a != b {
			t.Errorf("BindingFor(%s): operations %+v, actions %+v", op, a, b)
		}
	}
	it := actionItem("mailbox")
	if b := it.BindingFor("storage-extend"); b.Process != "mailbox-lifecycle" || b.Message != "mailbox.storage.extend" {
		t.Errorf("BindingFor(storage-extend) = %+v", b)
	}
	if b := it.BindingFor("upgrade"); b.Bound() {
		t.Errorf("an action nobody declared is bound: %+v", b)
	}
}

// TestLifecycleProblemsCheckEveryAction: against the deployed process, every action —
// not only the three operations — is a start in the per-operation form, and every
// change and service action is a keyed catch in the per-position form.
func TestLifecycleProblemsCheckEveryAction(t *testing.T) {
	it := actionItem("mailbox")
	starts := fakeEntryPoints{"mailbox-lifecycle": {messages: []string{
		"mailbox.provision", "mailbox.deprovision", "mailbox.storage.extend", "mailbox.password.reset"}}}
	if got := LifecycleProblems([]Item{it}, starts); len(got) != 0 {
		t.Fatalf("per-operation problems = %v, want none", got)
	}
	partial := fakeEntryPoints{"mailbox-lifecycle": {messages: []string{"mailbox.provision", "mailbox.deprovision"}}}
	contains(t, LifecycleProblems([]Item{it}, partial), "action password-reset names mailbox.password.reset, which is not a message start event")

	it.LifecycleForm = FormPerPosition
	strand := fakeShape{fakeEntryPoints: partial, catches: map[string][]CatchPoint{"mailbox-lifecycle": {
		{Element: "Held", Message: "mailbox.deprovision", Correlated: true},
		{Element: "Extend", Message: "mailbox.storage.extend", Correlated: true},
		{Element: "Reset", Message: "mailbox.password.reset", Correlated: true},
	}}}
	if got := LifecycleProblems([]Item{it}, strand); len(got) != 0 {
		t.Fatalf("per-position problems = %v, want none", got)
	}
	uncaught := strand
	uncaught.catches = map[string][]CatchPoint{"mailbox-lifecycle": {
		{Element: "Held", Message: "mailbox.deprovision", Correlated: true},
		{Element: "Extend", Message: "mailbox.storage.extend", Correlated: true},
	}}
	contains(t, LifecycleProblems([]Item{it}, uncaught), "action password-reset names mailbox.password.reset, which mailbox-lifecycle never waits for")
}

// TestActionLabelsAreTranslationGaps: a change or service action somebody presses is a
// button, and a button with no words in a declared language is a reported gap — never a
// refusal (ADR-0429 §10, decision 7). The two the order runs have their own words.
func TestActionLabelsAreTranslationGaps(t *testing.T) {
	it := actionItem("mailbox")
	got := TranslationGaps(Input{Catalogs: []Catalog{{ID: "cat", Languages: []string{"de", "en"}, Items: []string{it.ID}}}, Items: []Item{it}})
	contains(t, got, "no label for the action storage-extend in en")
	contains(t, got, "no label for the action password-reset in en")
	for _, p := range got {
		if p.Message == "no label for the action deprovision in en" || p.Message == "no label for the action provision in en" {
			t.Errorf("the order's own action reported as a gap: %v", p)
		}
	}
	unlabelled := actionItem("mailbox")
	unlabelled.Actions[2].Labels = nil
	contains(t, TranslationGaps(Input{Catalogs: []Catalog{{ID: "cat", Languages: []string{"de"}, Items: []string{it.ID}}}, Items: []Item{unlabelled}}),
		"action storage-extend has no label in any language")
	if problems := publishLifecycleItem(unlabelled); len(problems) != 0 {
		t.Fatalf("a missing label refused the publish: %v", problems)
	}
}

type fakeWatched struct {
	fakeEntryPoints
	watched map[string]bool
}

func (f fakeWatched) WatchedMessages() map[string]bool { return f.watched }

// TestAWatchedMessageIsNoAction: a name an inbound watch publishes would let a Worker's
// event drive the lifecycle around the order (ADR-0425 §8, ADR-0429 §1), so publishing
// refuses an action that uses it — whichever of the two came first.
func TestAWatchedMessageIsNoAction(t *testing.T) {
	it := actionItem("mailbox")
	look := fakeWatched{
		fakeEntryPoints: fakeEntryPoints{"mailbox-lifecycle": {messages: []string{
			"mailbox.provision", "mailbox.deprovision", "mailbox.storage.extend", "mailbox.password.reset"}}},
		watched: map[string]bool{"mailbox.password.reset": true},
	}
	contains(t, LifecycleProblems([]Item{it}, look), "action password-reset names mailbox.password.reset, which an inbound watch publishes")
	look.watched = map[string]bool{"jira.ticket.created": true}
	if got := LifecycleProblems([]Item{it}, look); len(got) != 0 {
		t.Fatalf("problems = %v, want none", got)
	}
}

// TestActionsAreFoundByKeyAndByMessage: the two questions the order and the refusals ask
// of a product — which action is this key, and which action owns this message — have
// one answer in either shape, and none for a product with no lifecycle process.
func TestActionsAreFoundByKeyAndByMessage(t *testing.T) {
	it := actionItem("mailbox")
	if a, ok := it.ActionNamed("storage-extend"); !ok || a.Message != "mailbox.storage.extend" {
		t.Errorf("ActionNamed(storage-extend) = %+v, %v", a, ok)
	}
	if _, ok := it.ActionNamed("upgrade"); ok {
		t.Error("an action nobody declared was found")
	}
	legacy := lifecycleItem("laptop")
	legacy.Operations[OpChange] = "laptop.change"
	if a, ok := legacy.ActionNamed(ActionChange); !ok || a.Effect != EffectChange {
		t.Errorf("the operation map's change read as %+v, %v", a, ok)
	}

	for msg, want := range map[string]string{
		"mailbox.password.reset": "password-reset",
		" mailbox.provision ":    ActionProvision,
	} {
		if key, ok := it.OwnsMessage(msg); !ok || key != want {
			t.Errorf("OwnsMessage(%q) = %q, %v, want %q", msg, key, ok, want)
		}
	}
	for _, msg := range []string{"", "  ", "jira.ticket.created"} {
		if key, ok := it.OwnsMessage(msg); ok {
			t.Errorf("OwnsMessage(%q) = %q, want no owner", msg, key)
		}
	}
	if key, ok := legacy.OwnsMessage("laptop.deprovision"); !ok || key != ActionDeprovision {
		t.Errorf("the operation map's deprovision is owned by %q, %v", key, ok)
	}
	if _, ok := item("vpn").OwnsMessage("vpn.provision"); ok {
		t.Error("a two-process product owns a message")
	}
}
