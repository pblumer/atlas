package catalog

import "testing"

// fakeShop is a per-operation lookup that also says which shop send tasks a process
// has.
type fakeShop struct {
	fakeEntryPoints
	shops map[string][]ShopOutcome
}

func (f fakeShop) ShopOutcomes(id string) []ShopOutcome { return f.shops[id] }

// TestEveryActionIsAnswered: a product that declares its actions publishes only when
// its process answers every change and service with a shop task stating it
// completed; a shop task naming an action the product does not declare, or the
// provision or the return, is refused; and a product that still carries the
// operation map is not held to it.
func TestEveryActionIsAnswered(t *testing.T) {
	it := actionItem("mailbox")
	starts := fakeEntryPoints{"mailbox-lifecycle": {messages: []string{
		"mailbox.provision", "mailbox.deprovision", "mailbox.storage.extend", "mailbox.password.reset"}}}
	answered := fakeShop{fakeEntryPoints: starts, shops: map[string][]ShopOutcome{"mailbox-lifecycle": {
		{Element: "Extended", Action: "storage-extend", Outcome: OutcomeCompleted},
		{Element: "ExtendFailed", Action: "storage-extend", Outcome: OutcomeFailed},
		{Element: "Reset", Action: "password-reset", Outcome: OutcomeCompleted},
	}}}
	if got := LifecycleProblems([]Item{it}, answered); len(got) != 0 {
		t.Fatalf("problems = %v, want none", got)
	}

	unanswered := fakeShop{fakeEntryPoints: starts, shops: map[string][]ShopOutcome{"mailbox-lifecycle": {
		{Element: "Extended", Action: "storage-extend", Outcome: OutcomeCompleted},
		{Element: "ResetFailed", Action: "password-reset", Outcome: OutcomeFailed},
	}}}
	contains(t, LifecycleProblems([]Item{it}, unanswered), "action password-reset is never answered")

	stray := fakeShop{fakeEntryPoints: starts, shops: map[string][]ShopOutcome{"mailbox-lifecycle": append(
		answered.shops["mailbox-lifecycle"],
		ShopOutcome{Element: "Archived", Action: "archive", Outcome: OutcomeCompleted},
		ShopOutcome{Element: "Provisioned", Action: "provision", Outcome: OutcomeCompleted},
	)}}
	got := LifecycleProblems([]Item{it}, stray)
	contains(t, got, "shop task Archived of mailbox-lifecycle answers action archive, which the product does not declare")
	contains(t, got, "shop task Provisioned answers the provision")

	legacy := lifecycleItem("laptop")
	legacy.Operations[OpChange] = "laptop.change"
	legacyStarts := fakeShop{fakeEntryPoints: fakeEntryPoints{"laptop-lifecycle": {messages: []string{
		"laptop.provision", "laptop.deprovision", "laptop.change"}}}}
	if got := LifecycleProblems([]Item{legacy}, legacyStarts); len(got) != 0 {
		t.Fatalf("an operation map held to the shop task: %v", got)
	}
}
