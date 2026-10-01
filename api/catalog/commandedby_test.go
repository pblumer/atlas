package catalog

import "testing"

func commandedBy(id string, keys ...string) Item {
	it := item(id)
	it.CommandedBy = keys
	return it
}

// TestTheApplicationsThatMayCommandAProductAreAListOfKeys: a blank entry and a
// repeated one are refused, a key no application carries here is not — a catalogue
// moves between servers, and a key that matches nothing lets nothing through — and
// the list travels into the release, which is where a command task reads it.
func TestTheApplicationsThatMayCommandAProductAreAListOfKeys(t *testing.T) {
	contains(t, problemsOf(commandedBy("mailbox", "hr-leavers", " ")), "blank application")
	contains(t, problemsOf(commandedBy("mailbox", "hr-leavers", "hr-leavers")), "names application hr-leavers twice")
	if got := problemsOf(commandedBy("mailbox", "hr-leavers", "not-on-this-server")); len(got) != 0 {
		t.Fatalf("a list of keys did not publish: %v", got)
	}

	rel, problems := Publish(Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"}, Items: []string{"mailbox"}}},
		Items:    []Item{commandedBy("mailbox", "hr-leavers")},
	})
	if len(problems) != 0 || len(rel.Items) != 1 || len(rel.Items[0].CommandedBy) != 1 ||
		rel.Items[0].CommandedBy[0] != "hr-leavers" {
		t.Fatalf("the release carries %+v (%v); the list did not travel", rel.Items, problems)
	}
}
