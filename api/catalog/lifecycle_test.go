package catalog

import "testing"

func lifecycleItem(id string) Item {
	it := item(id)
	it.ProvisionProcess, it.DeprovisionProcess = "", ""
	it.LifecycleProcess = id + "-lifecycle"
	it.Operations = map[string]string{OpProvision: id + ".provision", OpDeprovision: id + ".deprovision"}
	return it
}

func publishLifecycleItem(it Item) []Problem {
	_, problems := Publish(Input{
		Catalogs: []Catalog{{ID: "cat", Rank: 1, Languages: []string{"de"}, Items: []string{it.ID}}},
		Items:    []Item{it},
	})
	return problems
}

// TestALifecycleBindingPublishes: one process with provision and deprovision named
// is a complete binding, with no provision or deprovision process beside it.
func TestALifecycleBindingPublishes(t *testing.T) {
	if problems := publishLifecycleItem(lifecycleItem("laptop")); len(problems) != 0 {
		t.Fatalf("problems = %v, want none", problems)
	}
}

// TestALifecycleBindingOwesItsOperations: each of the ways the catalogue alone can
// tell a lifecycle binding is incomplete or contradictory.
func TestALifecycleBindingOwesItsOperations(t *testing.T) {
	noDeprov := lifecycleItem("laptop")
	delete(noDeprov.Operations, OpDeprovision)
	contains(t, publishLifecycleItem(noDeprov), "names no start event for deprovision")

	both := lifecycleItem("laptop")
	both.ProvisionProcess = "prov-laptop"
	contains(t, publishLifecycleItem(both), "bind one form")

	typo := lifecycleItem("laptop")
	typo.Operations["deprovison"] = "x"
	contains(t, publishLifecycleItem(typo), "operation deprovison is not one a product offers")

	stray := item("laptop")
	stray.Operations = map[string]string{OpChange: "x"}
	contains(t, publishLifecycleItem(stray), "names operations but binds no lifecycle process")

	self := lifecycleItem("laptop")
	self.LifecycleProcess = FulfilmentProcess
	contains(t, publishLifecycleItem(self), "works the order itself")
}

type fakeEntryPoints map[string]struct {
	messages []string
	none     bool
}

func (f fakeEntryPoints) EntryPoints(id string) ([]string, bool, bool) {
	e, ok := f[id]
	return e.messages, e.none, ok
}

// TestLifecycleProblemsAskWhatIsDeployed: the three things only the engine can
// answer — deployed, the named start events exist, no none start.
func TestLifecycleProblemsAskWhatIsDeployed(t *testing.T) {
	it := lifecycleItem("laptop")
	good := fakeEntryPoints{"laptop-lifecycle": {messages: []string{"laptop.deprovision", "laptop.provision"}}}
	if got := LifecycleProblems([]Item{it}, good); len(got) != 0 {
		t.Fatalf("problems = %v, want none", got)
	}
	contains(t, LifecycleProblems([]Item{it}, fakeEntryPoints{}), "is not deployed")
	missing := fakeEntryPoints{"laptop-lifecycle": {messages: []string{"laptop.provision"}}}
	contains(t, LifecycleProblems([]Item{it}, missing), "names laptop.deprovision, which is not a message start event")
	withNone := fakeEntryPoints{"laptop-lifecycle": {messages: []string{"laptop.deprovision", "laptop.provision"}, none: true}}
	contains(t, LifecycleProblems([]Item{it}, withNone), "has a none start event")
	if got := LifecycleProblems([]Item{item("old")}, fakeEntryPoints{}); len(got) != 0 {
		t.Fatalf("a two-process item was checked against entry points: %v", got)
	}
}

// TestBindingForReadsEitherForm: the one question every starter asks — where does
// this operation start — has one answer per form.
func TestBindingForReadsEitherForm(t *testing.T) {
	old := item("vpn")
	if b := old.BindingFor(OpDeprovision); b.Process != "deprov-vpn" || b.Triggered() {
		t.Errorf("two-process deprovision = %+v", b)
	}
	if b := old.BindingFor(OpChange); b.Bound() {
		t.Errorf("two-process change = %+v, want unbound", b)
	}
	lc := lifecycleItem("vpn")
	if b := lc.BindingFor(OpProvision); b.Process != "vpn-lifecycle" || b.Message != "vpn.provision" {
		t.Errorf("lifecycle provision = %+v", b)
	}
	if b := lc.BindingFor(OpChange); b.Bound() {
		t.Errorf("lifecycle change without a start event = %+v, want unbound", b)
	}
}
