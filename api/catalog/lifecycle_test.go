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
	if b := old.BindingFor(OpProvision); b.Process != old.ProvisionProcess || b.Triggered() {
		t.Errorf("two-process provision = %+v", b)
	}
	lc := lifecycleItem("vpn")
	if b := lc.BindingFor(OpProvision); b.Process != "vpn-lifecycle" || b.Message != "vpn.provision" {
		t.Errorf("lifecycle provision = %+v", b)
	}
	if b := lc.BindingFor(OpChange); b.Bound() {
		t.Errorf("lifecycle change without a start event = %+v, want unbound", b)
	}
}

type fakeShape struct {
	fakeEntryPoints
	catches map[string][]CatchPoint
	cycles  map[string][]string
}

func (f fakeShape) CatchPoints(id string) []CatchPoint { return f.catches[id] }
func (f fakeShape) WaitlessCycle(id string) []string   { return f.cycles[id] }

// TestAPerPositionBindingIsCheckedAgainstTheStrand: the later operations must be
// caught, keyed, and every cycle must wait; change may be a catch rather than a
// start; a lookup that cannot read the strand refuses rather than passes.
func TestAPerPositionBindingIsCheckedAgainstTheStrand(t *testing.T) {
	it := lifecycleItem("laptop")
	it.LifecycleForm = FormPerPosition
	it.Operations[OpChange] = "laptop.change"
	starts := fakeEntryPoints{"laptop-lifecycle": {messages: []string{"laptop.deprovision", "laptop.provision"}}}
	good := fakeShape{fakeEntryPoints: starts, catches: map[string][]CatchPoint{"laptop-lifecycle": {
		{Element: "Held", Message: "laptop.deprovision", Correlated: true},
		{Element: "Change", Message: "laptop.change", Correlated: true},
	}}}
	if got := LifecycleProblems([]Item{it}, good); len(got) != 0 {
		t.Fatalf("problems = %v, want none", got)
	}

	contains(t, LifecycleProblems([]Item{it}, starts), "cannot read what the process waits for")

	loose := good
	loose.catches = map[string][]CatchPoint{"laptop-lifecycle": {
		{Element: "Held", Message: "laptop.deprovision"},
		{Element: "Change", Message: "laptop.change", Correlated: true},
	}}
	contains(t, LifecycleProblems([]Item{it}, loose), "Held waits for laptop.deprovision without a correlation key")

	uncaught := good
	uncaught.catches = map[string][]CatchPoint{"laptop-lifecycle": {{Element: "Held", Message: "laptop.deprovision", Correlated: true}}}
	contains(t, LifecycleProblems([]Item{it}, uncaught), "names laptop.change, which laptop-lifecycle never waits for")

	circling := good
	circling.cycles = map[string][]string{"laptop-lifecycle": {"a", "b"}}
	contains(t, LifecycleProblems([]Item{it}, circling), "circles through a, b")

	it.LifecycleForm = ""
	contains(t, LifecycleProblems([]Item{it}, good), "names laptop.change, which is not a message start event")
}

// TestTheLifecycleFormIsOneOfTwo: an unknown form, and a form on an item with no
// lifecycle process, are refused by the catalogue alone.
func TestTheLifecycleFormIsOneOfTwo(t *testing.T) {
	odd := lifecycleItem("laptop")
	odd.LifecycleForm = "sometimes"
	contains(t, publishLifecycleItem(odd), "lifecycle form sometimes is not one")

	stray := item("laptop")
	stray.LifecycleForm = FormPerPosition
	contains(t, publishLifecycleItem(stray), "names a lifecycle form but binds no lifecycle process")

	if !(Item{LifecycleProcess: "p", LifecycleForm: FormPerPosition}).PerPosition() || (Item{LifecycleForm: FormPerPosition}).PerPosition() {
		t.Error("PerPosition does not require a lifecycle process and the per-position form")
	}
}
