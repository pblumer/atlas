package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// ADR-0495 slice D: a release records its business layer — its processes as work, the
// roles their tasks and lanes name, the catalogue products a release process carries
// out — and its strategy layer — the capabilities it realises and the value streams
// that use them. A product is shown only to whoever may maintain its catalogue, and a
// capability's owner only by name and role.

// businessBPMN has a lane, a task offered to two candidate groups, one assigned to a
// person by name and one whose assignee is computed, and a task for the Worker events.
const businessBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:atlas="http://atlas/schema/1.0"
             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="defs">
  <process id="bestellung" name="Bestellung" isExecutable="true">
    <laneSet>
      <lane id="lane_v" name="Vertrieb"><flowNodeRef>Pruefen</flowNodeRef><flowNodeRef>Freigeben</flowNodeRef></lane>
    </laneSet>
    <startEvent id="Start"/>
    <userTask id="Pruefen"><extensionElements><zeebe:assignmentDefinition candidateGroups="vertrieb, backoffice"/></extensionElements></userTask>
    <userTask id="Freigeben"><extensionElements><zeebe:assignmentDefinition assignee="anna"/></extensionElements></userTask>
    <userTask id="Nachfassen"><extensionElements><zeebe:assignmentDefinition assignee="=initiator"/></extensionElements></userTask>
    <serviceTask id="Melden">
      <extensionElements><atlas:clioConnector connector="events" subject="bestellung/neu" eventType="BestellungEingegangen"/></extensionElements>
    </serviceTask>
    <endEvent id="End"/>
    <sequenceFlow id="f1" sourceRef="Start" targetRef="Pruefen"/>
    <sequenceFlow id="f2" sourceRef="Pruefen" targetRef="Freigeben"/>
    <sequenceFlow id="f3" sourceRef="Freigeben" targetRef="Nachfassen"/>
    <sequenceFlow id="f4" sourceRef="Nachfassen" targetRef="Melden"/>
    <sequenceFlow id="f5" sourceRef="Melden" targetRef="End"/>
  </process>
</definitions>`

type businessRead struct {
	Architecture struct {
		Business *struct {
			Processes []struct {
				ProcessID    string `json:"processId"`
				GroupTasks   int    `json:"groupTasks"`
				PersonTasks  int    `json:"personTasks"`
				RuntimeTasks int    `json:"runtimeTasks"`
			} `json:"processes"`
			Roles []struct {
				Name string `json:"name"`
				Kind string `json:"kind"`
			} `json:"roles"`
			Products []struct {
				ID       string `json:"id"`
				Name     string `json:"name"`
				Bindings []struct {
					Op        string `json:"op"`
					ProcessID string `json:"processId"`
				} `json:"bindings"`
			} `json:"products"`
			ProductsWithheld int `json:"productsWithheld"`
		} `json:"business"`
		Strategy *struct {
			Capabilities []struct {
				Key   string `json:"key"`
				Owner *struct {
					Name string `json:"name"`
					Role string `json:"role"`
				} `json:"owner"`
				RealizedBy []struct {
					Kind      string `json:"kind"`
					ProcessID string `json:"processId"`
					Worker    string `json:"worker"`
				} `json:"realizedBy"`
			} `json:"capabilities"`
			ValueStreams []struct {
				Key    string `json:"key"`
				Stages []struct {
					Key          string   `json:"key"`
					Capabilities []string `json:"capabilities"`
				} `json:"stages"`
			} `json:"valueStreams"`
		} `json:"strategy"`
	} `json:"architecture"`
}

func TestAReleaseRecordsItsBusinessAndStrategyLayers(t *testing.T) {
	ts := newServerOn(t, t.TempDir())
	admin := signedInClient(t, ts.URL)
	createUser(t, admin, ts.URL, "anna")
	anna := signInAs(t, ts.URL, "anna", "a-password-that-is-long")

	var app struct{ ID, Key string }
	if err := json.Unmarshal(mustCall(t, anna, http.MethodPost, ts.URL+"/api/v1/applications", `{"name":"Bestellwesen"}`, "application/json", http.StatusOK), &app); err != nil {
		t.Fatal(err)
	}
	if app.Key == "" {
		t.Fatal("a new application has no portable key")
	}

	// The register: one capability the process realises, one the Worker events
	// realises, one nothing of the release realises, and a value stream using two.
	for _, c := range []map[string]any{
		{"key": "bestellannahme", "name": "Bestellannahme", "state": "active",
			"owner":        map[string]any{"name": "Erika Muster", "role": "Leiterin Vertrieb", "contact": "erika@example.com", "username": "emuster"},
			"kpis":         []map[string]any{{"name": "Durchlaufzeit", "metric": "cycleTime", "goal": "< 1 Tag"}},
			"realizations": []map[string]any{{"kind": "process", "applicationKey": app.Key, "processId": "bestellung"}}},
		{"key": "benachrichtigung", "name": "Benachrichtigung", "state": "proposed",
			"realizations": []map[string]any{{"kind": "worker", "workerRef": "events"}}},
		{"key": "fremd", "name": "Fremd", "state": "active",
			"realizations": []map[string]any{{"kind": "process", "applicationKey": "anders", "processId": "bestellung"}}},
	} {
		body, _ := json.Marshal(c)
		mustCall(t, admin, http.MethodPost, ts.URL+"/api/v1/capabilities", string(body), "application/json", http.StatusCreated)
	}
	stream, _ := json.Marshal(map[string]any{"key": "order-to-cash", "name": "Order to Cash", "stages": []map[string]any{
		{"key": "bestellen", "name": "Bestellen", "capabilities": []string{"bestellannahme", "fremd"}},
		{"key": "liefern", "name": "Liefern"},
	}})
	mustCall(t, admin, http.MethodPost, ts.URL+"/api/v1/value-streams", string(stream), "application/json", http.StatusCreated)

	// A catalogue the administrator owns, offering a product the process provisions.
	var cat struct{ ID string }
	if err := json.Unmarshal(mustCall(t, admin, http.MethodPost, ts.URL+"/api/v1/catalogs",
		`{"rank":1,"languages":["de"],"texts":{"de":"Dienste"}}`, "application/json", http.StatusCreated), &cat); err != nil {
		t.Fatal(err)
	}
	mustCall(t, admin, http.MethodPost, ts.URL+"/api/v1/catalog-products",
		fmt.Sprintf(`{"id":"zugang","homeCatalog":%q,"state":"active","texts":{"de":"Zugang"},"approval":{"kind":"none"},"provisionProcess":"bestellung","deprovisionProcess":"abbestellung"}`, cat.ID),
		"application/json", http.StatusOK)
	mustCall(t, admin, http.MethodPatch, ts.URL+"/api/v1/catalogs/"+cat.ID, `{"items":["zugang"]}`, "application/json", http.StatusOK)
	mustCall(t, admin, http.MethodPost, ts.URL+"/api/v1/catalogs/"+cat.ID+"/releases", "", "", http.StatusCreated)

	mustCall(t, anna, http.MethodPost, ts.URL+"/api/v1/drafts?projectId="+app.ID, businessBPMN, "application/xml", http.StatusOK)
	mustCall(t, anna, http.MethodPost, ts.URL+"/api/v1/applications/"+app.ID+"/publish", `{}`, "application/json", http.StatusOK)

	base := ts.URL + "/api/v1/applications/" + app.ID + "/releases/1/architecture"
	var read businessRead
	if err := json.Unmarshal(mustCall(t, admin, http.MethodGet, base+"?layers=strategy,business", "", "", http.StatusOK), &read); err != nil {
		t.Fatal(err)
	}
	b, st := read.Architecture.Business, read.Architecture.Strategy
	if b == nil || st == nil {
		t.Fatalf("business = %v, strategy = %v, want both recorded", b, st)
	}
	if len(b.Processes) != 1 || b.Processes[0].GroupTasks != 1 || b.Processes[0].PersonTasks != 1 || b.Processes[0].RuntimeTasks != 1 {
		t.Errorf("processes = %+v, want one task each for a group, a person and decided at run time", b.Processes)
	}
	roles := []string{}
	for _, r := range b.Roles {
		roles = append(roles, r.Kind+":"+r.Name)
	}
	if strings.Join(roles, ",") != "candidateGroup:backoffice,candidateGroup:vertrieb,lane:Vertrieb" {
		t.Errorf("roles = %v, want both candidate groups and the lane", roles)
	}
	if len(b.Products) != 1 || b.Products[0].Name != "Zugang" || b.Products[0].Bindings[0].Op != "provision" || b.ProductsWithheld != 0 {
		t.Errorf("products = %+v (withheld %d), want Zugang provisioned by bestellung", b.Products, b.ProductsWithheld)
	}
	keys := []string{}
	for _, c := range st.Capabilities {
		keys = append(keys, c.Key+"/"+c.RealizedBy[0].Kind)
	}
	if strings.Join(keys, ",") != "benachrichtigung/worker,bestellannahme/process" {
		t.Errorf("capabilities = %v, want the two the release realises and not the other application's", keys)
	}
	if o := st.Capabilities[1].Owner; o == nil || o.Name != "Erika Muster" || o.Role != "Leiterin Vertrieb" {
		t.Errorf("owner = %+v, want name and role", o)
	}
	if len(st.ValueStreams) != 1 || len(st.ValueStreams[0].Stages) != 2 || strings.Join(st.ValueStreams[0].Stages[0].Capabilities, ",") != "bestellannahme" {
		t.Errorf("value streams = %+v, want order-to-cash with its two stages and only the release's capability", st.ValueStreams)
	}

	// The owner's contact and account, and the assignee's name, are never recorded.
	for _, suffix := range []string{"?layers=strategy,business", ".svg?layers=strategy,business", ".xml?layers=strategy,business"} {
		body := string(mustCall(t, admin, http.MethodGet, base+suffix, "", "", http.StatusOK))
		for _, leak := range []string{"erika@example.com", "emuster", `"anna"`, ">anna<"} {
			if strings.Contains(body, leak) {
				t.Errorf("architecture%s carries %q", suffix, leak)
			}
		}
	}
	svg := string(mustCall(t, admin, http.MethodGet, base+".svg?layers=strategy,business,application", "", "", http.StatusOK))
	for _, want := range []string{"Bestellannahme", "owner Erika Muster (Leiterin Vertrieb)", "[Bestellen] → Liefern", "Zugang", "lane · 1 process"} {
		if !strings.Contains(svg, want) {
			t.Errorf("the sketch lacks %q", want)
		}
	}

	// Anna owns the application but may not maintain the catalogue: the product is
	// withheld from her, and she is told so.
	var hers businessRead
	if err := json.Unmarshal(mustCall(t, anna, http.MethodGet, base+"?layers=business", "", "", http.StatusOK), &hers); err != nil {
		t.Fatal(err)
	}
	if hb := hers.Architecture.Business; hb == nil || len(hb.Products) != 0 || hb.ProductsWithheld != 1 {
		t.Errorf("anna's business layer = %+v, want the product withheld and counted", hb)
	}
	for _, suffix := range []string{".svg?layers=business", ".xml?layers=business"} {
		if body := string(mustCall(t, anna, http.MethodGet, base+suffix, "", "", http.StatusOK)); strings.Contains(body, "Zugang") {
			t.Errorf("architecture%s shows anna a product of a catalogue she may not maintain", suffix)
		}
	}
}
