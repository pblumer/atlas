package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// ADR-0493, as amended for ADR-0495's sketches: a wiki holding a diagrams token picks
// a release from the architecture list and reads its sketch in the view every reader
// gets alike, whoever minted the token — never the architecture routes, which answer
// by who asks.

// auftragBPMN hands work to a configured Worker, calls into another application and
// throws a message that application receives.
const auftragBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" xmlns:atlas="http://atlas/schema/1.0" id="defsW">
  <message id="M_v" name="versandt"/>
  <process id="auftrag" isExecutable="true">
    <startEvent id="Start"/>
    <serviceTask id="Melden">
      <extensionElements><atlas:clioConnector connector="events" subject="auftrag/neu" eventType="AuftragEingegangen"/></extensionElements>
    </serviceTask>
    <sendTask id="Versenden" messageRef="M_v"/>
    <callActivity id="Mahnen"><extensionElements><zeebe:calledElement processId="mahnstufe"/></extensionElements></callActivity>
    <endEvent id="End"/>
    <sequenceFlow id="f1" sourceRef="Start" targetRef="Melden"/>
    <sequenceFlow id="f2" sourceRef="Melden" targetRef="Versenden"/>
    <sequenceFlow id="f3" sourceRef="Versenden" targetRef="Mahnen"/>
    <sequenceFlow id="f4" sourceRef="Mahnen" targetRef="End"/>
  </process>
</definitions>`

func TestAWikiReadsTheSketchAsEverybodyMaySeeIt(t *testing.T) {
	ts := newServerOn(t, t.TempDir())
	admin := signedInClient(t, ts.URL)
	mustCall(t, admin, http.MethodPost, ts.URL+"/api/v1/connectors",
		`{"name":"events","kind":"clio","endpoint":"https://clio.example.com:8443/api","credentialsRef":"events_cred"}`, "application/json", http.StatusOK)

	var other struct{ ID string }
	if err := json.Unmarshal(mustCall(t, admin, http.MethodPost, ts.URL+"/api/v1/applications", `{"name":"Mahnwesen"}`, "application/json", http.StatusOK), &other); err != nil {
		t.Fatal(err)
	}
	mustCall(t, admin, http.MethodPost, ts.URL+"/api/v1/drafts?projectId="+other.ID, mahnwesenBPMN, "application/xml", http.StatusOK)
	mustCall(t, admin, http.MethodPost, ts.URL+"/api/v1/drafts?projectId="+other.ID, mahnstufeBPMN, "application/xml", http.StatusOK)
	mustCall(t, admin, http.MethodPost, ts.URL+"/api/v1/applications/"+other.ID+"/publish", `{}`, "application/json", http.StatusOK)

	var app struct{ ID string }
	if err := json.Unmarshal(mustCall(t, admin, http.MethodPost, ts.URL+"/api/v1/applications", `{"name":"Auftragswesen"}`, "application/json", http.StatusOK), &app); err != nil {
		t.Fatal(err)
	}
	mustCall(t, admin, http.MethodPost, ts.URL+"/api/v1/drafts?projectId="+app.ID, auftragBPMN, "application/xml", http.StatusOK)
	mustCall(t, admin, http.MethodPost, ts.URL+"/api/v1/applications/"+app.ID+"/publish", `{}`, "application/json", http.StatusOK)
	mustCall(t, admin, http.MethodPost, ts.URL+"/api/v1/applications/"+app.ID+"/publish", `{}`, "application/json", http.StatusOK)
	// An application with no release has nothing to draw, and is not offered.
	mustCall(t, admin, http.MethodPost, ts.URL+"/api/v1/applications", `{"name":"Leer"}`, "application/json", http.StatusOK)

	// The token reaches the applications its reach names (ADR-0410): the wiki shows
	// the sketches its administrator chose, and a token naming none sees none.
	mintToken := func(body string) string {
		t.Helper()
		var minted struct{ Token string }
		if err := json.Unmarshal(mustCall(t, admin, http.MethodPost, ts.URL+"/api/v1/api-tokens", body, "application/json", http.StatusOK), &minted); err != nil {
			t.Fatal(err)
		}
		return minted.Token
	}
	token := mintToken(`{"name":"wiki","scope":"diagrams","expiresInDays":365,"reach":["` + app.ID + `"]}`)
	unreached := mintToken(`{"name":"wiki-processes","scope":"diagrams","expiresInDays":365}`)
	read := func(path string, want int) []byte {
		t.Helper()
		return readAs(t, ts.URL, token, path, want)
	}
	if body := readAs(t, ts.URL, unreached, "/api/v1/architectures", http.StatusOK); string(body) != `{"applications":[]}`+"\n" {
		t.Errorf("a token naming no reach lists %s, want nothing", body)
	}

	raw := read("/api/v1/architectures", http.StatusOK)
	var list struct {
		Applications []struct {
			ApplicationID string `json:"applicationId"`
			Application   string `json:"application"`
			Releases      []struct {
				Version  int32    `json:"version"`
				TakenAt  int64    `json:"takenAt"`
				Recorded []string `json:"recorded"`
			} `json:"releases"`
		} `json:"applications"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, a := range list.Applications {
		names = append(names, a.Application)
	}
	if strings.Join(names, ",") != "Auftragswesen" {
		t.Fatalf("applications = %v, want the one its reach names", names)
	}
	if rels := list.Applications[0].Releases; len(rels) != 2 || rels[0].Version != 2 || rels[1].Version != 1 || rels[0].TakenAt == 0 ||
		strings.Join(rels[0].Recorded, ",") != "strategy,business,application,data,technology" {
		t.Errorf("releases = %+v, want v2 then v1 with their moment and layers", rels)
	}
	if strings.Contains(string(raw), "publishedBy") || strings.Contains(string(raw), "note") {
		t.Errorf("the list says who published a release: %s", raw)
	}

	// The administrator who minted the token sees the Worker's endpoint and the other
	// application; the page the wiki shows to everybody does not.
	base := "/api/v1/applications/" + app.ID + "/releases/2/architecture"
	if own := string(mustCall(t, admin, http.MethodGet, ts.URL+base+".svg", "", "", http.StatusOK)); !strings.Contains(own, "clio.example.com") || !strings.Contains(own, "Mahnwesen") {
		t.Fatal("the administrator's own sketch lacks the endpoint or the other application")
	}
	svg := string(read(base+".embed.svg?layers=application,technology", http.StatusOK))
	for _, leak := range []string{"clio.example.com", "Mahnwesen", "mahnlauf", other.ID, "credential configured"} {
		if strings.Contains(svg, leak) {
			t.Errorf("the embedded sketch shows %q", leak)
		}
	}
	for _, want := range []string{"Auftragswesen", "events", "in another application", "→ received by another application", "endpoint withheld from this reader"} {
		if !strings.Contains(svg, want) {
			t.Errorf("the embedded sketch lacks %q", want)
		}
	}
	// Everybody gets the same picture: the administrator's own session too.
	if mine := string(mustCall(t, admin, http.MethodGet, ts.URL+base+".embed.svg?layers=application,technology", "", "", http.StatusOK)); mine != svg {
		t.Error("the embedded sketch depends on who asks")
	}

	// The routes that answer by who asks stay out of the scope's reach.
	for _, path := range []string{base, base + ".svg", base + ".xml", "/api/v1/applications/" + app.ID + "/releases/2/mesh", "/api/v1/applications/" + app.ID + "/releases"} {
		read(path, http.StatusForbidden)
	}
	read("/api/v1/applications/"+app.ID+"/releases/9/architecture.embed.svg", http.StatusNotFound)
	if code := readStatus(t, ts.URL, token, "/api/v1/applications/"+other.ID+"/releases/1/architecture.embed.svg"); code == http.StatusOK {
		t.Error("the token reads the sketch of an application its reach does not name")
	}
}

// readAs reads path with a bearer token and insists on the status.
func readAs(t *testing.T, base, token, path string, want int) []byte {
	t.Helper()
	code, body := tokenGet(t, base, token, path)
	if code != want {
		t.Fatalf("token on GET %s = %d, want %d; body = %.200s", path, code, want, body)
	}
	return body
}

func readStatus(t *testing.T, base, token, path string) int {
	t.Helper()
	code, _ := tokenGet(t, base, token, path)
	return code
}

func tokenGet(t *testing.T, base, token, path string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, base+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return res.StatusCode, body
}
