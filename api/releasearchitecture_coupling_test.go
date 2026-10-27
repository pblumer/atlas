package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// ADR-0495 slice E: a release records who in other applications is at the other end of
// the messages and signals it throws and receives, and another application — called or
// coupled — is shown only to a reader who may see it.

// mahnwesenBPMN throws the message the order process starts on, starts on the message
// it throws, and catches the signal it broadcasts.
const mahnwesenBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" id="defsB">
  <message id="M_b" name="bestellt"/>
  <message id="M_v" name="versandt"/>
  <signal id="S_s" name="storno"/>
  <process id="mahnlauf" isExecutable="true">
    <startEvent id="Versandt"><messageEventDefinition messageRef="M_v"/></startEvent>
    <sendTask id="Bestellen" messageRef="M_b"/>
    <intermediateCatchEvent id="Storniert"><signalEventDefinition signalRef="S_s"/></intermediateCatchEvent>
    <endEvent id="E"/>
    <sequenceFlow id="f1" sourceRef="Versandt" targetRef="Bestellen"/>
    <sequenceFlow id="f2" sourceRef="Bestellen" targetRef="Storniert"/>
    <sequenceFlow id="f3" sourceRef="Storniert" targetRef="E"/>
  </process>
</definitions>`

const mahnstufeBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" id="defsC">
  <process id="mahnstufe" isExecutable="true">
    <startEvent id="s"/><endEvent id="e"/><sequenceFlow id="f" sourceRef="s" targetRef="e"/>
  </process>
</definitions>`

const bestellungBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="defsA">
  <message id="M_b" name="bestellt"/>
  <message id="M_v" name="versandt"/>
  <signal id="S_s" name="storno"/>
  <process id="bestellung" isExecutable="true">
    <startEvent id="Start"><messageEventDefinition messageRef="M_b"/></startEvent>
    <sendTask id="Versenden" messageRef="M_v"/>
    <intermediateThrowEvent id="Stornieren"><signalEventDefinition signalRef="S_s"/></intermediateThrowEvent>
    <callActivity id="Mahnen"><extensionElements><zeebe:calledElement processId="mahnstufe"/></extensionElements></callActivity>
    <endEvent id="End"/>
    <sequenceFlow id="f1" sourceRef="Start" targetRef="Versenden"/>
    <sequenceFlow id="f2" sourceRef="Versenden" targetRef="Stornieren"/>
    <sequenceFlow id="f3" sourceRef="Stornieren" targetRef="Mahnen"/>
    <sequenceFlow id="f4" sourceRef="Mahnen" targetRef="End"/>
  </process>
</definitions>`

type couplingRead struct {
	Architecture struct {
		Dependencies []struct {
			Kind              string `json:"kind"`
			Name              string `json:"name"`
			TargetApplication string `json:"targetApplication"`
			TargetWithheld    bool   `json:"targetWithheld"`
		} `json:"dependencies"`
		Coupling *struct {
			Names []struct {
				Kind      string `json:"kind"`
				Name      string `json:"name"`
				Direction string `json:"direction"`
			} `json:"names"`
			Peers []struct {
				Kind        string `json:"kind"`
				Name        string `json:"name"`
				Direction   string `json:"direction"`
				Application string `json:"application"`
				ProcessID   string `json:"processId"`
				Withheld    bool   `json:"withheld"`
			} `json:"peers"`
			PeersWithheld int `json:"peersWithheld"`
		} `json:"coupling"`
	} `json:"architecture"`
}

func TestAReleaseRecordsWhoElseIsAtTheOtherEndOfItsNames(t *testing.T) {
	ts := newServerOn(t, t.TempDir())
	admin := signedInClient(t, ts.URL)
	createUser(t, admin, ts.URL, "anna")
	anna := signInAs(t, ts.URL, "anna", "a-password-that-is-long")

	// The administrator's application, which anna may not see.
	var other struct{ ID string }
	if err := json.Unmarshal(mustCall(t, admin, http.MethodPost, ts.URL+"/api/v1/applications", `{"name":"Mahnwesen"}`, "application/json", http.StatusOK), &other); err != nil {
		t.Fatal(err)
	}
	mustCall(t, admin, http.MethodPost, ts.URL+"/api/v1/drafts?projectId="+other.ID, mahnwesenBPMN, "application/xml", http.StatusOK)
	mustCall(t, admin, http.MethodPost, ts.URL+"/api/v1/drafts?projectId="+other.ID, mahnstufeBPMN, "application/xml", http.StatusOK)
	mustCall(t, admin, http.MethodPost, ts.URL+"/api/v1/applications/"+other.ID+"/publish", `{}`, "application/json", http.StatusOK)

	var app struct{ ID string }
	if err := json.Unmarshal(mustCall(t, anna, http.MethodPost, ts.URL+"/api/v1/applications", `{"name":"Bestellwesen"}`, "application/json", http.StatusOK), &app); err != nil {
		t.Fatal(err)
	}
	mustCall(t, anna, http.MethodPost, ts.URL+"/api/v1/drafts?projectId="+app.ID, bestellungBPMN, "application/xml", http.StatusOK)
	mustCall(t, anna, http.MethodPost, ts.URL+"/api/v1/applications/"+app.ID+"/publish", `{}`, "application/json", http.StatusOK)

	base := ts.URL + "/api/v1/applications/" + app.ID + "/releases/1/architecture"
	var whole couplingRead
	if err := json.Unmarshal(mustCall(t, admin, http.MethodGet, base, "", "", http.StatusOK), &whole); err != nil {
		t.Fatal(err)
	}
	c := whole.Architecture.Coupling
	if c == nil {
		t.Fatal("no coupling recorded")
	}
	names := []string{}
	for _, n := range c.Names {
		names = append(names, n.Direction+":"+n.Kind+":"+n.Name)
	}
	if strings.Join(names, ",") != "out:message:versandt,out:signal:storno" {
		t.Errorf("names = %v, want the message and the signal the release throws", names)
	}
	peers := []string{}
	for _, p := range c.Peers {
		peers = append(peers, p.Direction+":"+p.Kind+":"+p.Name+"@"+p.Application+"/"+p.ProcessID)
	}
	if strings.Join(peers, ",") != "in:message:bestellt@Mahnwesen/mahnlauf,out:message:versandt@Mahnwesen/mahnlauf,out:signal:storno@Mahnwesen/mahnlauf" {
		t.Errorf("peers = %v, want mahnlauf at the other end of all three names", peers)
	}
	svg := string(mustCall(t, admin, http.MethodGet, base+".svg", "", "", http.StatusOK))
	for _, want := range []string{"← thrown by Mahnwesen", "Sends message versandt", "→ received by Mahnwesen", "Broadcasts signal storno", "in Mahnwesen · 1 element"} {
		if !strings.Contains(svg, want) {
			t.Errorf("the administrator's sketch lacks %q", want)
		}
	}

	// Anna may not see Mahnwesen: she learns that another application is there, called
	// and coupled, and nothing of its name or processes.
	var hers couplingRead
	if err := json.Unmarshal(mustCall(t, anna, http.MethodGet, base, "", "", http.StatusOK), &hers); err != nil {
		t.Fatal(err)
	}
	if hc := hers.Architecture.Coupling; hc == nil || hc.PeersWithheld != 3 {
		t.Errorf("anna's coupling = %+v, want three peers withheld", hc)
	}
	for _, d := range hers.Architecture.Dependencies {
		if d.Kind == "call" && (!d.TargetWithheld || d.TargetApplication != "") {
			t.Errorf("anna's call = %+v, want the called application withheld", d)
		}
	}
	for _, suffix := range []string{"", ".svg", ".xml"} {
		body := string(mustCall(t, anna, http.MethodGet, base+suffix, "", "", http.StatusOK))
		for _, leak := range []string{"Mahnwesen", "mahnlauf", other.ID} {
			if strings.Contains(body, leak) {
				t.Errorf("architecture%s shows anna %q", suffix, leak)
			}
		}
	}
	if svg := string(mustCall(t, anna, http.MethodGet, base+".svg", "", "", http.StatusOK)); !strings.Contains(svg, "in another application · 1 element") ||
		!strings.Contains(svg, "← thrown by another application") {
		t.Error("anna's sketch does not say another application is there")
	}
}
