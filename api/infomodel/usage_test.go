package infomodel

import (
	"reflect"
	"testing"

	"github.com/pblumer/atlas/compiler"
)

// usageModel is orderModel with the two things a usage reading has to see beyond the
// classes themselves: a store that holds one of them, and a lifecycle that takes its
// states from the enumeration next to it (ADR-0306).
func usageModel() Model {
	m := orderModel()
	m.UpdatedAt = 1700
	m.Stores = []DataStore{{
		ID: "s1", Name: "Order archive", Class: "Order",
		Worker: "clio", Mode: StoreModeRead,
	}}
	for i := range m.Classes {
		if m.Classes[i].Name != "Order" {
			continue
		}
		m.Classes[i].Lifecycle = &Lifecycle{
			StatesFrom: "OrderStatus",
			States: []LifecycleState{
				{Name: "draft", Initial: true},
				{Name: "approved"},
				{Name: "rejected", Final: true},
			},
			Transitions: []LifecycleTransition{
				{ID: "t1", From: "draft", To: "approved"},
				{ID: "t2", From: "draft", To: "rejected"},
			},
		}
	}
	return m
}

// usageProcess touches an Order every way a process can: it declares one, writes a
// member of it, reads it into a variable, moves its state, and names the store it is
// archived in. It also carries an untyped object, which resolves to no class at all.
func usageProcess(t *testing.T) Process {
	t.Helper()
	b := compiler.NewBuilder(7, "order-to-cash", 3)
	start := b.AddStartEvent()
	capture := b.AddTask()
	approve := b.AddTask()
	end := b.AddEndEvent()
	b.Connect(start, capture)
	b.Connect(capture, approve)
	b.Connect(approve, end)
	b.SetElementBpmnId(capture, "Capture")
	b.SetElementBpmnId(approve, "Approve")
	b.AddDataObject("order", "Order", "draft", false)
	b.AddDataObject("note", "", "", false)
	b.AddDataOutputAssociation(capture, "order", mustExpr(t, "payload"), "", "id")
	b.AddDataInputAssociation(approve, "order", "orderUnderReview", nil)
	b.AddDataOutputAssociation(approve, "order", nil, "approved", "")
	b.AddDataStore("Order archive", "Archive")
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return Process{Compiled: cp, Name: "Order to cash", Version: 3}
}

func usageOf(t *testing.T, class string, procs ...Process) Usage {
	t.Helper()
	u, ok := UsageOf([]Model{usageModel()}, "m1", class, procs)
	if !ok {
		t.Fatalf("UsageOf(%q) found no such class", class)
	}
	return u
}

// TestUsageOfReadsEveryUseAProcessMakes is the question the whole reading exists
// for: not only *that* a class is used, but where and how — the element, the member,
// the state, the variable.
func TestUsageOfReadsEveryUseAProcessMakes(t *testing.T) {
	u := usageOf(t, "Order", usageProcess(t))

	want := []ProcessUse{
		{ProcessDefinitionKey: 7, ProcessID: "order-to-cash", ProcessName: "Order to cash", Version: 3,
			Kind: ProcessUseDeclare, Object: "order", State: "draft"},
		{ProcessDefinitionKey: 7, ProcessID: "order-to-cash", ProcessName: "Order to cash", Version: 3,
			Kind: ProcessUseWrite, Object: "order", ElementID: "Capture", Attribute: "id", WritesValue: true},
		{ProcessDefinitionKey: 7, ProcessID: "order-to-cash", ProcessName: "Order to cash", Version: 3,
			Kind: ProcessUseRead, Object: "order", ElementID: "Approve", Variable: "orderUnderReview"},
		{ProcessDefinitionKey: 7, ProcessID: "order-to-cash", ProcessName: "Order to cash", Version: 3,
			Kind: ProcessUseWrite, Object: "order", ElementID: "Approve", State: "approved"},
		{ProcessDefinitionKey: 7, ProcessID: "order-to-cash", ProcessName: "Order to cash", Version: 3,
			Kind: ProcessUseStore, Store: "Order archive", ElementID: "Archive"},
	}
	if !reflect.DeepEqual(u.Processes, want) {
		t.Errorf("process uses:\n got %+v\nwant %+v", u.Processes, want)
	}
}

// TestUsageSummaryCountsTheKindsApart is what a reader sees before the rows: how
// many processes, how they use it, which members and states they actually touch.
func TestUsageSummaryCountsTheKindsApart(t *testing.T) {
	got := usageOf(t, "Order", usageProcess(t)).Summary
	want := UsageSummary{
		Processes: 1, Uses: 5, Reads: 1, Writes: 2,
		Attributes: []string{"id"},
		// "draft" counts: every instance is created in the state the declaration
		// seeds, so it is reached as surely as any write reaches one.
		States:    []string{"approved", "draft"},
		Stores:    []string{"Order archive"},
		ModelUses: 3,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("summary:\n got %+v\nwant %+v", got, want)
	}
}

// TestUsageOfIgnoresAnUntypedDataObject: an object with no itemSubjectRef names no
// class, and guessing one from its name would report an inference as a fact.
func TestUsageOfIgnoresAnUntypedDataObject(t *testing.T) {
	for _, u := range usageOf(t, "Order", usageProcess(t)).Processes {
		if u.Object == "note" {
			t.Errorf("the untyped object %q was attributed to Order: %+v", u.Object, u)
		}
	}
}

// TestUsageOfReadsTheModelItself is the other half of "where used": an enumeration is
// typically used by no process at all — it is used by the class that is typed with it,
// and by the lifecycle that takes its states from it.
func TestUsageOfReadsTheModelItself(t *testing.T) {
	u := usageOf(t, "OrderStatus")
	want := []ModelUse{
		{ModelID: "m1", ModelName: "Sales", Kind: ModelUseAttribute, Class: "Order", Name: "status", Detail: "multiplicity 1"},
		{ModelID: "m1", ModelName: "Sales", Kind: ModelUseStates, Class: "Order"},
	}
	if !reflect.DeepEqual(u.Model, want) {
		t.Errorf("model uses of OrderStatus:\n got %+v\nwant %+v", u.Model, want)
	}
	if u.Summary.Processes != 0 || u.Summary.Uses != 0 {
		t.Errorf("an enumeration no process declares must show no process use: %+v", u.Summary)
	}
}

// TestUsageOfReadsAssociationsAndStores: both ends of an association are a use of the
// class at the other end, and a store is a use of the class it holds.
func TestUsageOfReadsAssociationsAndStores(t *testing.T) {
	want := []ModelUse{
		{ModelID: "m1", ModelName: "Sales", Kind: ModelUseAssociation, Class: "Customer",
			Name: "places", Detail: "association · Customer 1 → Order 0..*"},
		{ModelID: "m1", ModelName: "Sales", Kind: ModelUseAssociation, Class: "OrderLine",
			Detail: "composition · Order 1 → OrderLine 1..*"},
		{ModelID: "m1", ModelName: "Sales", Kind: ModelUseStore, Name: "Order archive", Detail: "read · clio"},
	}
	if got := usageOf(t, "Order").Model; !reflect.DeepEqual(got, want) {
		t.Errorf("model uses of Order:\n got %+v\nwant %+v", got, want)
	}
}

// TestUsageOfRefusesAClassThatIsNotThere: a name nobody models has no usage, and
// saying "used nowhere" about it would read as a fact about a class that exists.
func TestUsageOfRefusesAClassThatIsNotThere(t *testing.T) {
	if _, ok := UsageOf([]Model{usageModel()}, "m1", "Invoice", nil); ok {
		t.Error("UsageOf answered for a class the model does not declare")
	}
	if _, ok := UsageOf([]Model{usageModel()}, "nope", "Order", nil); ok {
		t.Error("UsageOf answered for a model that is not there")
	}
}

// TestUsageOfCarriesTheClassItself, so the detail view needs one call rather than two
// that can come to disagree.
func TestUsageOfCarriesTheClassItself(t *testing.T) {
	u := usageOf(t, "Order")
	if u.Class.Name != "Order" || len(u.Class.Attributes) != 4 {
		t.Errorf("class not carried whole: %+v", u.Class)
	}
	if u.ModelID != "m1" || u.ModelName != "Sales" || u.ApplicationID != "app" {
		t.Errorf("model and application not stated: %+v", u)
	}
}

// TestCatalogListsEveryClassOfEveryKind — the overview is of the vocabulary, not of
// the business objects alone: a value type and an enumeration are things somebody
// maintains too, and leaving them out would hide exactly the ones with no process use.
func TestCatalogListsEveryClassOfEveryKind(t *testing.T) {
	entries := Catalog([]Model{usageModel()}, func(string) []Process { return nil })

	var names []string
	for _, e := range entries {
		names = append(names, e.Name)
	}
	// Sorted by name, so the list opens the same way twice.
	want := []string{"Address", "Customer", "Order", "OrderLine", "OrderStatus"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("catalogue = %v, want %v", names, want)
	}

	byName := map[string]CatalogEntry{}
	for _, e := range entries {
		byName[e.Name] = e
	}
	// An enumeration has literals where every other stereotype has attributes, and
	// both are its members — one column, honestly filled, rather than two half-empty.
	for _, tc := range []struct {
		class      string
		stereotype string
		members    int
		states     int
		identity   []string
	}{
		{"Order", StereotypeBusinessObject, 4, 3, []string{"id"}},
		{"Customer", StereotypeBusinessObject, 2, 0, []string{"number"}},
		{"Address", StereotypeValueType, 1, 0, nil},
		{"OrderStatus", StereotypeEnumeration, 3, 0, nil},
	} {
		e := byName[tc.class]
		if e.Stereotype != tc.stereotype || e.Members != tc.members || e.States != tc.states {
			t.Errorf("%s: stereotype=%q members=%d states=%d, want %q/%d/%d",
				tc.class, e.Stereotype, e.Members, e.States, tc.stereotype, tc.members, tc.states)
		}
		if !reflect.DeepEqual(e.Identity, tc.identity) {
			t.Errorf("%s: identity = %v, want %v", tc.class, e.Identity, tc.identity)
		}
		if e.ModelID != "m1" || e.ModelName != "Sales" || e.ApplicationID != "app" || e.UpdatedAt != 1700 {
			t.Errorf("%s: model not stated on the row: %+v", tc.class, e)
		}
	}
}

// TestCatalogCountsUsageWithoutWalkingPerClass: the counts on the list are the same
// ones the detail view adds up, so a row that says "used by one process" opens on one.
func TestCatalogCountsUsageWithoutWalkingPerClass(t *testing.T) {
	proc := usageProcess(t)
	walks := 0
	entries := Catalog([]Model{usageModel()}, func(applicationID string) []Process {
		walks++
		if applicationID != "app" {
			t.Errorf("processes asked for %q, want the model's own application", applicationID)
		}
		return []Process{proc}
	})
	if walks != 1 {
		t.Errorf("the application's processes were gathered %d times, want once for all its classes", walks)
	}
	for _, e := range entries {
		switch e.Name {
		case "Order":
			if e.Usage.Processes != 1 || e.Usage.Uses != 5 {
				t.Errorf("Order usage = %+v, want 1 process and 5 uses", e.Usage)
			}
		case "OrderStatus":
			if e.Usage.Processes != 0 || e.Usage.ModelUses != 2 {
				t.Errorf("OrderStatus usage = %+v, want no process use and 2 model uses", e.Usage)
			}
		}
	}
}

// TestCatalogSpansApplications: the point of the list is that it is one list. Two
// applications' models sit in it together, each read against its own processes.
func TestCatalogSpansApplications(t *testing.T) {
	other := Model{
		ID: "m2", ApplicationID: "hr", Name: "People", UpdatedAt: 1800,
		Classes: []Class{{ID: "c9", Name: "Applicant", Stereotype: StereotypeBusinessObject,
			Identity: []string{"id"}, Attributes: []Attribute{{Name: "id", Type: TypeString, Multiplicity: MultOne}}}},
	}
	asked := map[string]bool{}
	entries := Catalog([]Model{usageModel(), other}, func(applicationID string) []Process {
		asked[applicationID] = true
		return nil
	})
	if len(entries) != 6 {
		t.Fatalf("catalogue has %d entries, want the classes of both models", len(entries))
	}
	if !asked["app"] || !asked["hr"] {
		t.Errorf("processes were not gathered per application: %v", asked)
	}
	for _, e := range entries {
		if e.Name == "Applicant" && e.ApplicationID != "hr" {
			t.Errorf("Applicant landed in application %q", e.ApplicationID)
		}
	}
}

// TestUsageOfSeesAClassUsedByASecondModelOfTheSameApplication — an application's
// models share one namespace (a data object's type resolves against all of them), so
// an attribute in a second model typed with this class is a use of it.
func TestUsageOfSeesAClassUsedByASecondModelOfTheSameApplication(t *testing.T) {
	second := Model{
		ID: "m2", ApplicationID: "app", Name: "Fulfilment",
		Classes: []Class{{ID: "d1", Name: "Shipment", Stereotype: StereotypeBusinessObject,
			Identity: []string{"id"}, Attributes: []Attribute{
				{Name: "id", Type: TypeString, Multiplicity: MultOne},
				{Name: "for", Type: "Order", Multiplicity: MultOne},
			}}},
	}
	u, ok := UsageOf([]Model{usageModel(), second}, "m1", "Order", nil)
	if !ok {
		t.Fatal("UsageOf found no Order")
	}
	found := false
	for _, m := range u.Model {
		if m.ModelID == "m2" && m.Class == "Shipment" && m.Name == "for" {
			found = true
		}
	}
	if !found {
		t.Errorf("a use in a second model of the same application was not seen: %+v", u.Model)
	}
}

// TestUsageOfDoesNotJoinAnAssociationFromAnotherModel: an association names its ends
// by a class id, and that id is scoped to the model holding it. Two models in one
// application may carry the same handle — an imported document's ids are remapped, but
// nothing in the type says so — and reading an association of the wrong model would
// report a relationship this class does not have.
func TestUsageOfDoesNotJoinAnAssociationFromAnotherModel(t *testing.T) {
	// The same ids as usageModel's Customer and Order, in a different document.
	twin := Model{
		ID: "m2", ApplicationID: "app", Name: "Fulfilment",
		Classes: []Class{
			{ID: "c1", Name: "Courier", Stereotype: StereotypeBusinessObject, Identity: []string{"id"},
				Attributes: []Attribute{{Name: "id", Type: TypeString, Multiplicity: MultOne}}},
			{ID: "c2", Name: "Shipment", Stereotype: StereotypeBusinessObject, Identity: []string{"id"},
				Attributes: []Attribute{{Name: "id", Type: TypeString, Multiplicity: MultOne}}},
		},
		Associations: []Association{{ID: "b1", Kind: KindAssociation, Name: "carries",
			From: End{ClassID: "c1", Role: "courier", Multiplicity: MultOne},
			To:   End{ClassID: "c2", Role: "shipments", Multiplicity: MultMany}}},
	}
	u, ok := UsageOf([]Model{usageModel(), twin}, "m1", "Order", nil)
	if !ok {
		t.Fatal("UsageOf found no Order")
	}
	for _, m := range u.Model {
		if m.ModelID == "m2" {
			t.Errorf("an association of another model was read as a use of Order: %+v", m)
		}
	}
}
