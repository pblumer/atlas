package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// ADR-0495 slice B: a release records what else its Workers' configuration says and
// the inbound subscriptions that feed its ways in, and each reader sees of it what
// they may see of the Worker.

// inboundBPMN starts on a message an inbound subscription publishes, and hands work to
// the clio Worker that subscription reads through.
const inboundBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0" id="defs">
  <bpmn:message id="Msg_Order" name="order.created"/>
  <bpmn:process id="bestellung" name="Bestellung" isExecutable="true">
    <bpmn:startEvent id="Start"><bpmn:messageEventDefinition messageRef="Msg_Order"/></bpmn:startEvent>
    <bpmn:serviceTask id="Notify">
      <bpmn:extensionElements><atlas:clioConnector connector="events" subject="bestellung/neu" eventType="BestellungEingegangen"/></bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="End"/>
    <bpmn:sequenceFlow id="f1" sourceRef="Start" targetRef="Notify"/>
    <bpmn:sequenceFlow id="f2" sourceRef="Notify" targetRef="End"/>
  </bpmn:process>
</bpmn:definitions>`

type inboundRead struct {
	Architecture struct {
		Physical struct {
			Workers []struct {
				Name           string `json:"name"`
				DetailWithheld bool   `json:"detailWithheld"`
			} `json:"workers"`
			Inbound []struct {
				MessageName string `json:"messageName"`
				Worker      string `json:"worker"`
				Subject     string `json:"subject"`
				Recursive   bool   `json:"recursive"`
				Withheld    bool   `json:"withheld"`
				Endpoint    *struct {
					Host string `json:"host"`
				} `json:"endpoint"`
			} `json:"inbound"`
		} `json:"physical"`
	} `json:"architecture"`
}

func TestAReleaseRecordsTheInboundSubscriptionThatFeedsIt(t *testing.T) {
	ts := newServerOn(t, t.TempDir())
	admin := signedInClient(t, ts.URL)
	createUser(t, admin, ts.URL, "anna")
	bertID := createUserWithRoles(t, admin, ts.URL, "bert", `["modeler","user"]`)
	anna := signInAs(t, ts.URL, "anna", "a-password-that-is-long")
	bert := signInAs(t, ts.URL, "bert", "a-password-that-is-long")

	var worker struct{ ID string }
	if err := json.Unmarshal(mustCall(t, anna, http.MethodPost, ts.URL+"/api/v1/configured-workers",
		`{"name":"events","kind":"clio","endpoint":"https://svc:geheim@clio.example.com:8443/hook","enabled":true}`, "application/json", http.StatusOK), &worker); err != nil {
		t.Fatal(err)
	}
	mustCall(t, anna, http.MethodPost, ts.URL+"/api/v1/connectors/"+worker.ID+"/inbound-subscriptions",
		`{"watchedSubject":"orders/new","recursive":true,"messageName":"order.created","enabled":true}`, "application/json", http.StatusOK)
	// A disabled subscription feeds nothing, and a subscription for a message the
	// release does not receive is not the release's.
	mustCall(t, anna, http.MethodPost, ts.URL+"/api/v1/connectors/"+worker.ID+"/inbound-subscriptions",
		`{"watchedSubject":"orders/old","messageName":"order.created","enabled":false}`, "application/json", http.StatusOK)
	mustCall(t, anna, http.MethodPost, ts.URL+"/api/v1/connectors/"+worker.ID+"/inbound-subscriptions",
		`{"watchedSubject":"invoices/new","messageName":"invoice.created","enabled":true}`, "application/json", http.StatusOK)

	var app struct{ ID string }
	if err := json.Unmarshal(mustCall(t, anna, http.MethodPost, ts.URL+"/api/v1/applications", `{"name":"Bestellwesen"}`, "application/json", http.StatusOK), &app); err != nil {
		t.Fatal(err)
	}
	mustCall(t, anna, http.MethodPost, ts.URL+"/api/v1/drafts?projectId="+app.ID, inboundBPMN, "application/xml", http.StatusOK)
	mustCall(t, anna, http.MethodPost, ts.URL+"/api/v1/applications/"+app.ID+"/publish", `{}`, "application/json", http.StatusOK)
	mustCall(t, anna, http.MethodPut, ts.URL+"/api/v1/applications/"+app.ID+"/members/"+bertID, `{"role":"viewer"}`, "application/json", http.StatusOK)

	base := ts.URL + "/api/v1/applications/" + app.ID + "/releases/1/architecture"
	var mine inboundRead
	if err := json.Unmarshal(mustCall(t, anna, http.MethodGet, base, "", "", http.StatusOK), &mine); err != nil {
		t.Fatal(err)
	}
	in := mine.Architecture.Physical.Inbound
	if len(in) != 1 || in[0].MessageName != "order.created" || in[0].Worker != "events" || in[0].Subject != "orders/new" ||
		!in[0].Recursive || in[0].Endpoint == nil || in[0].Endpoint.Host != "clio.example.com" {
		t.Fatalf("inbound = %+v, want the one enabled subscription feeding order.created", in)
	}
	svg := string(mustCall(t, anna, http.MethodGet, base+".svg", "", "", http.StatusOK))
	if !strings.Contains(svg, "← events · orders/new/…") {
		t.Error("the owner's sketch does not say where order.created comes from")
	}

	// A reader the application is shared with, who may not see the Worker, learns
	// that a subscription feeds the message and not what it watches or where.
	var theirs inboundRead
	raw := mustCall(t, bert, http.MethodGet, base, "", "", http.StatusOK)
	if err := json.Unmarshal(raw, &theirs); err != nil {
		t.Fatal(err)
	}
	if in := theirs.Architecture.Physical.Inbound; len(in) != 1 || !in[0].Withheld || in[0].Subject != "" || in[0].Endpoint != nil {
		t.Errorf("inbound for a reader without the Worker = %+v, want it withheld", in)
	}
	for _, suffix := range []string{"", ".svg", ".xml"} {
		body := string(mustCall(t, bert, http.MethodGet, base+suffix, "", "", http.StatusOK))
		for _, leak := range []string{"orders/new", "clio.example.com", "geheim"} {
			if strings.Contains(body, leak) {
				t.Errorf("architecture%s carries %q to a reader who may not see the Worker", suffix, leak)
			}
		}
	}
}
