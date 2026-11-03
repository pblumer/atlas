package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// ordererBPMN is a process that orders the tool for the account in `who` from catalogue
// cat, keeping the order's id in `auftrag`. With catch, a refused order is caught by an
// error boundary and the instance ends at "Abgelehnt" instead of raising an incident.
func ordererBPMN(id, cat string, catch bool) string {
	boundary := ""
	if catch {
		boundary = `<boundaryEvent id="Refused" attachedToRef="Order"><errorEventDefinition errorRef="err"/></boundaryEvent>
    <endEvent id="Abgelehnt"/>
    <sequenceFlow id="f3" sourceRef="Refused" targetRef="Abgelehnt"/>`
	}
	return `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:atlas="http://atlas/schema/1.0">
  <error id="err" errorCode="SHOP_ORDER_REFUSED"/>
  <process id="` + id + `" isExecutable="true">
    <startEvent id="S"/>
    <sendTask id="Order"><extensionElements><atlas:shopTask mode="order" catalog="` + cat +
		`" product="tool" recipient="= who" resultVariable="auftrag" retries="1"/></extensionElements></sendTask>
    <userTask id="Halt"/>
    <endEvent id="E"/>
    ` + boundary + `
    <sequenceFlow id="f1" sourceRef="S" targetRef="Order"/>
    <sequenceFlow id="f2" sourceRef="Order" targetRef="Halt"/>
    <sequenceFlow id="f4" sourceRef="Halt" targetRef="E"/>
  </process>
</definitions>`
}

// TestAProcessOrdersInTheNameOfItsApplication (ADR-draft-process-places-an-order): a
// process orders a product for somebody only when the product's newest release lists
// its application; the order then names the application as its orderer, carries the
// recipient, starts like a person's order, and its id lands in the instance. A refusal
// is an incident, or the model's path when it catches SHOP_ORDER_REFUSED.
func TestAProcessOrdersInTheNameOfItsApplication(t *testing.T) {
	ts, _ := newAuthServerWith(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	ritaID := createUserWithRoles(t, admin, ts.URL, "rita", `["user"]`)
	if code, b := cReqTyped(t, admin, ts, "POST", "/api/v1/deployments", "application/xml", actingStrandBPMN); code != http.StatusOK {
		t.Fatalf("deploy strand: %d (%s)", code, b)
	}
	// A catalogue nobody is in the audience of: the tool is ordered by the process only.
	code, body := cReq(t, admin, ts, "POST", "/api/v1/catalogs", `{"rank":1,"languages":["de"],"texts":{"de":"HR"}}`)
	if code != http.StatusCreated {
		t.Fatalf("catalogue: %d (%s)", code, body)
	}
	cat := idOf(t, body)
	appID, appKey := anApplication(t, admin, ts, "HR Eintritte")
	publish := func(orderableBy string) {
		t.Helper()
		if code, b := cReq(t, admin, ts, "POST", "/api/v1/catalog-products",
			`{"id":"tool","homeCatalog":"`+cat+`","state":"active","texts":{"de":"Werkzeug"},`+
				`"approval":{"kind":"none"},"lifecycleProcess":"tool-strand","lifecycleForm":"per-position",`+
				`"actions":`+toolActions(`["customer","operator"]`)+`,"orderableBy":`+orderableBy+`}`); code != http.StatusOK {
			t.Fatalf("save: %d (%s)", code, b)
		}
		if code, b := cReq(t, admin, ts, "PATCH", "/api/v1/catalogs/"+cat, `{"items":["tool"]}`); code != http.StatusOK {
			t.Fatalf("offer: %d (%s)", code, b)
		}
		if code, b := cReq(t, admin, ts, "POST", "/api/v1/catalogs/"+cat+"/releases", ""); code != http.StatusCreated {
			t.Fatalf("publish: %d (%s)", code, b)
		}
	}
	publish(`[]`)
	for id, catch := range map[string]bool{"joiner": false, "joiner-caught": true} {
		if code, b := cReqTyped(t, admin, ts, "POST", "/api/v1/deployments?projectId="+appID, "application/xml",
			ordererBPMN(id, cat, catch)); code != http.StatusOK {
			t.Fatalf("deploy %s: %d (%s)", id, code, b)
		}
	}
	start := func(id, who string) uint64 {
		t.Helper()
		code, b := cReq(t, admin, ts, "POST", "/api/v1/instances", `{"processId":"`+id+`","variables":{"who":"`+who+`"}}`)
		var started struct {
			InstanceKey uint64 `json:"instanceKey"`
		}
		if code != http.StatusOK || json.Unmarshal(b, &started) != nil {
			t.Fatalf("start %s: %d (%s)", id, code, b)
		}
		return started.InstanceKey
	}
	awaitIncident := func(id, want string) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			for _, m := range commandIncidents(t, admin, ts, id) {
				if strings.Contains(m, want) {
					return
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s raised %q, want an incident naming %q", id, commandIncidents(t, admin, ts, id), want)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	awaitVariable := func(key uint64, name string) string {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			vars := lookupVariables(t, admin, ts, key)
			if v, ok := vars[name].(string); ok && v != "" {
				return v
			}
			if time.Now().After(deadline) {
				t.Fatalf("instance %d never got %s: %v", key, name, vars)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	// Not listed: the order is refused, as an incident or as the model's own path.
	start("joiner", ritaID)
	awaitIncident("joiner", `does not list application "`+appKey+`"`)
	caught := start("joiner-caught", ritaID)
	awaitInstanceState(t, admin, ts, caught, "COMPLETED")

	// Listed: the order is placed in the application's name, for rita.
	publish(`["` + appKey + `"]`)
	key := start("joiner", ritaID)
	orderID := awaitVariable(key, "auftrag")
	// Read as rita: an order is its orderer's and its recipient's, and the
	// application signs in as nobody, so the recipient is who sees it.
	rita := signInAs(t, ts.URL, "rita", "a-password-that-is-long")
	code, body = cReq(t, rita, ts, "GET", "/api/v1/orders/"+orderID, "")
	var got struct {
		Orderer, Recipient string
		Lines              []struct{ ItemID string }
	}
	if code != http.StatusOK || json.Unmarshal(body, &got) != nil {
		t.Fatalf("read order: %d (%s)", code, body)
	}
	if got.Orderer != "system:application:"+appKey || got.Recipient != ritaID || len(got.Lines) != 1 || got.Lines[0].ItemID != "tool" {
		t.Errorf("order = %+v, want the tool for rita ordered by the application", got)
	}

	// Nobody by that id: refused with the order's own reason.
	start("joiner", "usr_nobody")
	awaitIncident("joiner", "usr_nobody")
}
