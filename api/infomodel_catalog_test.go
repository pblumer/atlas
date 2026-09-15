package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The class catalogue and the where-used reading, end to end
// (ADR-0338).
//
// api/infomodel holds the reading itself and tests its rules there. What those tests
// cannot see is the join: whether the models the service owns and the processes only
// the server can reach are actually read against each other on one turn, whether the
// route was registered at all, and whether an application somebody may not see stays
// out of a list that spans applications by design.

// catalogModel is one business object and the enumeration it is typed with — the pair
// that makes the point, because the enumeration is used by no process at all and would
// read as unused to anything that looked only at processes.
const catalogModel = `{
  "classes":[
    {"id":"c1","name":"Order","stereotype":"businessObject","identity":["id"],
     "attributes":[{"name":"id","type":"string","multiplicity":"1"},
                   {"name":"total","type":"number","multiplicity":"0..1"},
                   {"name":"status","type":"OrderStatus","multiplicity":"1"}],
     "lifecycle":{"statesFrom":"OrderStatus",
       "states":[{"name":"received","initial":true},{"name":"approved"},{"name":"shipped","final":true}],
       "transitions":[{"id":"t1","from":"received","to":"approved"},
                      {"id":"t2","from":"approved","to":"shipped"}]}},
    {"id":"c2","name":"OrderStatus","stereotype":"enumeration",
     "literals":["received","approved","shipped"]}
  ],
  "associations":[],
  "stores":[{"id":"s1","name":"Order archive","class":"Order","worker":"clio","mode":"read"}]}`

// catalogBPMN touches that Order every way a process can: it declares one in a state,
// writes a member of it, reads it into a variable, moves its state, and names the
// store it is archived in.
const catalogBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <itemDefinition id="ItemDefinition_Order" structureRef="Order"/>
  <dataStore id="Store_archive" name="Order archive"/>
  <process id="order-to-cash" name="Order to cash" isExecutable="true">
    <dataObject id="DO_order" name="order" itemSubjectRef="ItemDefinition_Order"><dataState name="received"/></dataObject>
    <dataObjectReference id="Ref_order" name="order" dataObjectRef="DO_order"/>
    <dataObjectReference id="Ref_approved" name="order" dataObjectRef="DO_order"><dataState name="approved"/></dataObjectReference>
    <dataStoreReference id="Ref_archive" name="Order archive" dataStoreRef="Store_archive"/>
    <startEvent id="start"/>
    <task id="capture">
      <dataOutputAssociation id="out1"><targetRef>Ref_order</targetRef>
        <assignment><to>total</to><from>= 42</from></assignment></dataOutputAssociation>
    </task>
    <task id="approve">
      <dataInputAssociation id="in1"><sourceRef>Ref_order</sourceRef>
        <assignment><to>orderUnderReview</to></assignment></dataInputAssociation>
      <dataOutputAssociation id="out2"><targetRef>Ref_approved</targetRef>
        <assignment><from>= {id: &quot;ORD-1&quot;}</from></assignment></dataOutputAssociation>
    </task>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="capture"/>
    <sequenceFlow id="f2" sourceRef="capture" targetRef="approve"/>
    <sequenceFlow id="f3" sourceRef="approve" targetRef="end"/>
  </process>
</definitions>`

type catalogRowResp struct {
	ModelID         string   `json:"modelId"`
	ModelName       string   `json:"modelName"`
	ApplicationID   string   `json:"applicationId"`
	ApplicationName string   `json:"applicationName"`
	Name            string   `json:"name"`
	Stereotype      string   `json:"stereotype"`
	Members         int      `json:"members"`
	Identity        []string `json:"identity"`
	States          int      `json:"states"`
	Usage           struct {
		Processes  int      `json:"processes"`
		Uses       int      `json:"uses"`
		Reads      int      `json:"reads"`
		Writes     int      `json:"writes"`
		Attributes []string `json:"attributes"`
		States     []string `json:"states"`
		Stores     []string `json:"stores"`
		ModelUses  int      `json:"modelUses"`
	} `json:"usage"`
}

type usageResp struct {
	Class struct {
		Name       string `json:"name"`
		Attributes []struct {
			Name string `json:"name"`
		} `json:"attributes"`
	} `json:"class"`
	ApplicationName string `json:"applicationName"`
	Processes       []struct {
		ProcessID   string `json:"processId"`
		ProcessName string `json:"processName"`
		Version     int32  `json:"version"`
		Kind        string `json:"kind"`
		Object      string `json:"object"`
		ElementID   string `json:"elementId"`
		Attribute   string `json:"attribute"`
		State       string `json:"state"`
		Variable    string `json:"variable"`
		Store       string `json:"store"`
	} `json:"processes"`
	Model []struct {
		Kind  string `json:"kind"`
		Class string `json:"class"`
		Name  string `json:"name"`
	} `json:"model"`
}

// catalogFixture creates the application, stores catalogModel in it and deploys
// catalogBPMN under it, returning the application and model ids.
func catalogFixture(t *testing.T, ts *httptest.Server) (appID, modelID string) {
	t.Helper()
	appID = lifecycleApplication(t, ts, catalogModel)
	if code, b := doReq(t, ts, http.MethodPost, "/api/v1/deployments?projectId="+appID, catalogBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, b)
	}
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/infomodel/models?applicationId="+appID, "", "")
	if code != http.StatusOK {
		t.Fatalf("list models: status=%d body=%s", code, body)
	}
	var models []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &models); err != nil || len(models) != 1 {
		t.Fatalf("decode models: %v (%s)", err, body)
	}
	return appID, models[0].ID
}

func fetchCatalog(t *testing.T, ts *httptest.Server, query string) []catalogRowResp {
	t.Helper()
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/infomodel/classes"+query, "", "")
	if code != http.StatusOK {
		t.Fatalf("catalogue: status=%d body=%s", code, body)
	}
	var rows []catalogRowResp
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("decode catalogue: %v (%s)", err, body)
	}
	return rows
}

// TestClassCatalogJoinsTheModelsToTheProcesses is the whole point of serving this
// from the server rather than from the information-model service: one side is the
// stored models, the other is what the application deploys, and neither half is the
// answer on its own.
func TestClassCatalogJoinsTheModelsToTheProcesses(t *testing.T) {
	ts := newTestServer(t)
	appID, _ := catalogFixture(t, ts)

	byName := map[string]catalogRowResp{}
	for _, row := range fetchCatalog(t, ts, "") {
		byName[row.Name] = row
	}
	order, ok := byName["Order"]
	if !ok {
		t.Fatalf("no Order in the catalogue; got %v", byName)
	}
	if order.ApplicationID != appID || order.ApplicationName != "Sales" || order.ModelName != "Sales data" {
		t.Errorf("Order's row does not say where it lives: %+v", order)
	}
	if order.Stereotype != "businessObject" || order.Members != 3 || order.States != 3 {
		t.Errorf("Order: stereotype=%q members=%d states=%d", order.Stereotype, order.Members, order.States)
	}
	if len(order.Identity) != 1 || order.Identity[0] != "id" {
		t.Errorf("Order's business key = %v, want [id]", order.Identity)
	}
	// One process, and every kind of use it makes of the class.
	if order.Usage.Processes != 1 || order.Usage.Reads != 1 || order.Usage.Writes != 2 || order.Usage.Uses != 5 {
		t.Errorf("Order usage = %+v, want 1 process, 1 read, 2 writes, 5 uses", order.Usage)
	}
	if len(order.Usage.Attributes) != 1 || order.Usage.Attributes[0] != "total" {
		t.Errorf("members written = %v, want [total]", order.Usage.Attributes)
	}
	// "shipped" is declared and nothing writes it: the summary says what processes
	// *reach*, which is rarely all of what the class declares.
	if got := fmt.Sprint(order.Usage.States); got != "[approved received]" {
		t.Errorf("states reached = %v, want [approved received]", order.Usage.States)
	}
	if len(order.Usage.Stores) != 1 || order.Usage.Stores[0] != "Order archive" {
		t.Errorf("stores = %v, want [Order archive]", order.Usage.Stores)
	}

	// The enumeration is used by no process and is not therefore unused: it types a
	// member and it is where a lifecycle takes its states from.
	status, ok := byName["OrderStatus"]
	if !ok {
		t.Fatal("no OrderStatus in the catalogue — an enumeration is part of the vocabulary")
	}
	if status.Stereotype != "enumeration" || status.Members != 3 {
		t.Errorf("OrderStatus: stereotype=%q members=%d, want enumeration with 3 literals", status.Stereotype, status.Members)
	}
	if status.Usage.Processes != 0 || status.Usage.ModelUses != 2 {
		t.Errorf("OrderStatus usage = %+v, want no process use and 2 model uses", status.Usage)
	}
}

// TestClassCatalogNarrowsToOneApplication, for the person working in one.
func TestClassCatalogNarrowsToOneApplication(t *testing.T) {
	ts := newTestServer(t)
	appID, _ := catalogFixture(t, ts)

	if rows := fetchCatalog(t, ts, "?applicationId="+appID); len(rows) != 2 {
		t.Errorf("filtered catalogue has %d rows, want the two classes of that model", len(rows))
	}
	if rows := fetchCatalog(t, ts, "?applicationId=nosuchapp"); len(rows) != 0 {
		t.Errorf("an unknown application returned %d rows", len(rows))
	}
}

// TestClassUsageNamesTheElementAndHow is the detail view's question: not only which
// processes, but which element, which member, which state.
func TestClassUsageNamesTheElementAndHow(t *testing.T) {
	ts := newTestServer(t)
	_, modelID := catalogFixture(t, ts)

	code, body := doReq(t, ts, http.MethodGet,
		"/api/v1/infomodel/models/"+modelID+"/usage?class=Order", "", "")
	if code != http.StatusOK {
		t.Fatalf("status=%d body=%s", code, body)
	}
	var u usageResp
	if err := json.Unmarshal(body, &u); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if u.Class.Name != "Order" || len(u.Class.Attributes) != 3 || u.ApplicationName != "Sales" {
		t.Errorf("the class is not carried whole: %+v", u.Class)
	}

	// Each use, read as a row of the detail table.
	seen := map[string]string{}
	for _, p := range u.Processes {
		if p.ProcessID != "order-to-cash" || p.ProcessName != "Order to cash" || p.Version != 1 {
			t.Errorf("a use does not name its process: %+v", p)
		}
		switch {
		case p.Kind == "declare":
			seen["declare"] = p.Object + "/" + p.State
		case p.Kind == "read":
			seen["read"] = p.ElementID + "/" + p.Variable
		case p.Kind == "write" && p.Attribute != "":
			seen["member-write"] = p.ElementID + "/" + p.Attribute
		case p.Kind == "write":
			seen["state-write"] = p.ElementID + "/" + p.State
		case p.Kind == "store":
			seen["store"] = p.ElementID + "/" + p.Store
		}
	}
	for use, want := range map[string]string{
		"declare":      "order/received",
		"read":         "approve/orderUnderReview",
		"member-write": "capture/total",
		"state-write":  "approve/approved",
		"store":        "Ref_archive/Order archive",
	} {
		if seen[use] != want {
			t.Errorf("%s = %q, want %q (all: %+v)", use, seen[use], want, u.Processes)
		}
	}

	// And the model's own use of it: the store that holds it.
	stored := false
	for _, m := range u.Model {
		if m.Kind == "store" && m.Name == "Order archive" {
			stored = true
		}
	}
	if !stored {
		t.Errorf("the store holding Order is not among its model uses: %+v", u.Model)
	}
}

// TestClassUsageOfAnEnumerationIsTheModelsOwnUse — the case a process-only reading
// would report as unused, and the reason the two kinds are read together.
func TestClassUsageOfAnEnumerationIsTheModelsOwnUse(t *testing.T) {
	ts := newTestServer(t)
	_, modelID := catalogFixture(t, ts)

	code, body := doReq(t, ts, http.MethodGet,
		"/api/v1/infomodel/models/"+modelID+"/usage?class=OrderStatus", "", "")
	if code != http.StatusOK {
		t.Fatalf("status=%d body=%s", code, body)
	}
	var u usageResp
	if err := json.Unmarshal(body, &u); err != nil {
		t.Fatalf("decode: %v", err)
	}
	kinds := map[string]string{}
	for _, m := range u.Model {
		kinds[m.Kind] = m.Class + "." + m.Name
	}
	if kinds["attribute"] != "Order.status" {
		t.Errorf("attribute use = %q, want Order.status", kinds["attribute"])
	}
	if _, ok := kinds["states"]; !ok {
		t.Errorf("the lifecycle taking its states from this enumeration is not a use of it: %+v", u.Model)
	}
}

// TestClassUsageRefusesWhatItCannotAnswer: an empty usage for a class nobody models
// would read as a fact about a class that exists.
func TestClassUsageRefusesWhatItCannotAnswer(t *testing.T) {
	ts := newTestServer(t)
	_, modelID := catalogFixture(t, ts)

	for _, tc := range []struct {
		name, path string
		want       int
	}{
		{"no class named", "/api/v1/infomodel/models/" + modelID + "/usage", http.StatusBadRequest},
		{"unknown class", "/api/v1/infomodel/models/" + modelID + "/usage?class=Invoice", http.StatusNotFound},
		{"unknown model", "/api/v1/infomodel/models/deadbeef/usage?class=Order", http.StatusNotFound},
	} {
		if code, body := doReq(t, ts, http.MethodGet, tc.path, "", ""); code != tc.want {
			t.Errorf("%s: status=%d want %d (%s)", tc.name, code, tc.want, body)
		}
	}
}

// TestClassCatalogHidesWhatTheCallerMayNotSee. The list spans applications by design,
// which is exactly why the scope has to hold here: one modeller's private application
// must not become visible through a list of its classes.
func TestClassCatalogHidesWhatTheCallerMayNotSee(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	clients := twoUsers(t, ts, admin, "mia", "noah")
	mia, noah := clients[0], clients[1]
	for _, name := range []string{"mia", "noah"} {
		id := userID(t, ts, admin, name)
		if code, b := cReq(t, admin, ts, "PATCH", "/api/v1/users/"+id,
			`{"roles":["user","modeler"]}`); code != http.StatusOK {
			t.Fatalf("grant modeler to %s: %d (%s)", name, code, b)
		}
	}

	code, body := cReq(t, mia, ts, "POST", "/api/v1/applications", `{"name":"Mia's sales"}`)
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create application: %d (%s)", code, body)
	}
	var app struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &app); err != nil {
		t.Fatalf("decode application: %v", err)
	}
	code, body = cReq(t, mia, ts, "POST", "/api/v1/infomodel/models",
		fmt.Sprintf(`{"applicationId":%q,"name":"Mia's data"}`, app.ID))
	if code != http.StatusCreated {
		t.Fatalf("create model: %d (%s)", code, body)
	}
	var m struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("decode model: %v", err)
	}
	if code, b := cReq(t, mia, ts, "PUT", "/api/v1/infomodel/models/"+m.ID, catalogModel); code != http.StatusOK {
		t.Fatalf("save model: %d (%s)", code, b)
	}

	if code, b := cReq(t, mia, ts, "GET", "/api/v1/infomodel/classes", ""); code != http.StatusOK {
		t.Fatalf("mia's own catalogue: %d (%s)", code, b)
	} else {
		var rows []catalogRowResp
		if err := json.Unmarshal(b, &rows); err != nil || len(rows) != 2 {
			t.Fatalf("mia sees %d rows of her own model: %v (%s)", len(rows), err, b)
		}
	}
	code, body = cReq(t, noah, ts, "GET", "/api/v1/infomodel/classes", "")
	if code != http.StatusOK {
		t.Fatalf("noah's catalogue: %d (%s)", code, body)
	}
	var rows []catalogRowResp
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if len(rows) != 0 {
		t.Errorf("noah sees %d classes of an application he has no access to: %+v", len(rows), rows)
	}
	// And the usage of one of them is refused the same way the model itself is —
	// not found, so its existence never leaks through a different refusal.
	if code, b := cReq(t, noah, ts, "GET", "/api/v1/infomodel/models/"+m.ID+"/usage?class=Order", ""); code != http.StatusNotFound {
		t.Errorf("usage of a hidden model: status=%d want 404 (%s)", code, b)
	}
}
