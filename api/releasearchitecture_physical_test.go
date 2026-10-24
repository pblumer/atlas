package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api"
)

// ADR-0471 S2: the physical part of a release's snapshot — what the installation that
// recorded it had in place — and what each reader may see of it.

// physicalBPMN has a start form (so a public start link can be made for it), a task
// for a configured worker, and a service task with a job type an external worker
// leases.
const physicalBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="defs">
  <bpmn:process id="antrag" name="Antrag" isExecutable="true">
    <bpmn:startEvent id="Start">
      <bpmn:extensionElements><zeebe:formDefinition formId="antrag-form"/></bpmn:extensionElements>
    </bpmn:startEvent>
    <bpmn:serviceTask id="Notify">
      <bpmn:extensionElements><atlas:clioConnector connector="events" subject="antrag/neu" eventType="AntragEingegangen"/></bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:serviceTask id="Task_Adresse">
      <bpmn:extensionElements><zeebe:taskDefinition type="adresse-pruefen"/></bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="End"/>
    <bpmn:sequenceFlow id="f1" sourceRef="Start" targetRef="Notify"/>
    <bpmn:sequenceFlow id="f2" sourceRef="Notify" targetRef="Task_Adresse"/>
    <bpmn:sequenceFlow id="f3" sourceRef="Task_Adresse" targetRef="End"/>
  </bpmn:process>
</bpmn:definitions>`

type physicalPart struct {
	Node        string `json:"node"`
	Environment string `json:"environment"`
	ExternalURL *struct {
		Scheme string `json:"scheme"`
		Host   string `json:"host"`
	} `json:"externalUrl"`
	PublicStartLinks []struct {
		ProcessID string `json:"processId"`
		Links     int    `json:"links"`
	} `json:"publicStartLinks"`
	Workers []struct {
		Name         string `json:"name"`
		WorkerType   string `json:"workerType"`
		Placement    string `json:"placement"`
		ConfiguredID string `json:"configuredId"`
		Endpoint     *struct {
			Scheme   string `json:"scheme"`
			Host     string `json:"host"`
			Port     string `json:"port"`
			Withheld bool   `json:"withheld"`
		} `json:"endpoint"`
		ObservedBy        []string `json:"observedBy"`
		Observed          int      `json:"observed"`
		ObserversWithheld bool     `json:"observersWithheld"`
	} `json:"workers"`
	JobTypes []struct {
		JobType    string   `json:"jobType"`
		ObservedBy []string `json:"observedBy"`
		Observed   int      `json:"observed"`
	} `json:"jobTypes"`
}

// call is one request with a client against a base URL, returning status and body.
func call(t *testing.T, c *http.Client, method, url, body, contentType string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	res, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	return res.StatusCode, data
}

func mustCall(t *testing.T, c *http.Client, method, url, body, contentType string, want int) []byte {
	t.Helper()
	code, data := call(t, c, method, url, body, contentType)
	if code != want {
		t.Fatalf("%s %s = %d (%s), want %d", method, url, code, data, want)
	}
	return data
}

func readPhysical(t *testing.T, c *http.Client, base, appID, version string) (physicalPart, []byte) {
	t.Helper()
	raw := mustCall(t, c, http.MethodGet, base+"/api/v1/applications/"+appID+"/releases/"+version+"/architecture", "", "", http.StatusOK)
	var out struct {
		Architecture struct {
			Physical *physicalPart `json:"physical"`
		} `json:"architecture"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, raw)
	}
	if out.Architecture.Physical == nil {
		t.Fatalf("no physical part: %s", raw)
	}
	return *out.Architecture.Physical, raw
}

// setUpPhysical publishes the application twice around the facts the second release
// should record: a named installation, a configured worker whose endpoint carries a
// credential and a path, a public start link, and a worker instance that leased the
// job type and reported holding the worker name.
func setUpPhysical(t *testing.T, owner, admin *http.Client, base string) (appID string) {
	t.Helper()
	mustCall(t, admin, http.MethodPut, base+"/api/v1/node", `{"name":"prod-1","environment":"PROD"}`, "application/json", http.StatusOK)
	mustCall(t, owner, http.MethodPost, base+"/api/v1/configured-workers",
		`{"name":"events","kind":"clio","endpoint":"https://svc:geheim@clio.example.com:8443/hook?x=1","enabled":true}`, "application/json", http.StatusOK)

	var app struct{ ID string }
	if err := json.Unmarshal(mustCall(t, owner, http.MethodPost, base+"/api/v1/applications", `{"name":"Antraege"}`, "application/json", http.StatusOK), &app); err != nil {
		t.Fatal(err)
	}
	mustCall(t, owner, http.MethodPost, base+"/api/v1/drafts?projectId="+app.ID, physicalBPMN, "application/xml", http.StatusOK)
	mustCall(t, owner, http.MethodPost, base+"/api/v1/applications/"+app.ID+"/publish", `{}`, "application/json", http.StatusOK)

	mustCall(t, owner, http.MethodPost, base+"/api/v1/public-links", `{"processId":"antrag"}`, "application/json", http.StatusOK)
	mustCall(t, owner, http.MethodPost, base+"/api/v1/jobs/activate",
		`{"type":"adresse-pruefen","worker":"extern-1","connectors":["events"],"maxJobs":1,"waitMs":0,"leaseMs":1000}`, "application/json", http.StatusOK)

	mustCall(t, owner, http.MethodPost, base+"/api/v1/applications/"+app.ID+"/publish", `{}`, "application/json", http.StatusOK)
	return app.ID
}

func TestAReleaseRecordsThePhysicalPart(t *testing.T) {
	ts, stop := openServerAt(t, t.TempDir(), api.WithExternalURL("https://atlas.example.com/"))
	defer stop()
	c := ts.Client()
	app := setUpPhysical(t, c, c, ts.URL)

	p, raw := readPhysical(t, c, ts.URL, app, "2")
	if p.Node != "prod-1" || p.Environment != "PROD" {
		t.Errorf("installation = %q/%q, want prod-1/PROD", p.Node, p.Environment)
	}
	if p.ExternalURL == nil || p.ExternalURL.Scheme != "https" || p.ExternalURL.Host != "atlas.example.com" {
		t.Errorf("external URL = %+v", p.ExternalURL)
	}
	if len(p.PublicStartLinks) != 1 || p.PublicStartLinks[0].ProcessID != "antrag" || p.PublicStartLinks[0].Links != 1 {
		t.Errorf("public start links = %+v", p.PublicStartLinks)
	}
	if len(p.Workers) != 1 {
		t.Fatalf("workers = %+v", p.Workers)
	}
	w := p.Workers[0]
	if w.Name != "events" || w.WorkerType != "clio" || w.ConfiguredID == "" || (w.Placement != "engine" && w.Placement != "worker") {
		t.Errorf("worker = %+v", w)
	}
	if w.Endpoint == nil || w.Endpoint.Scheme != "https" || w.Endpoint.Host != "clio.example.com" || w.Endpoint.Port != "8443" {
		t.Errorf("endpoint = %+v, want scheme, host and port only", w.Endpoint)
	}
	if len(w.ObservedBy) != 1 || w.ObservedBy[0] != "extern-1" {
		t.Errorf("observed by = %v, want the instance that reported the name", w.ObservedBy)
	}
	if len(p.JobTypes) != 1 || p.JobTypes[0].JobType != "adresse-pruefen" || len(p.JobTypes[0].ObservedBy) != 1 {
		t.Errorf("job types = %+v", p.JobTypes)
	}
	for _, leaked := range []string{"geheim", "svc:", "/hook", "x=1"} {
		if strings.Contains(string(raw), leaked) {
			t.Errorf("the snapshot carries %q from the configured endpoint: %s", leaked, raw)
		}
	}

	_, svg := call(t, c, http.MethodGet, ts.URL+"/api/v1/applications/"+app+"/releases/2/architecture.svg", "", "")
	for _, want := range []string{"prod-1 (PROD)", "at https://atlas.example.com", "Public start form antrag",
		"→ https://clio.example.com:8443", "Job type adresse-pruefen", "seen: extern-1"} {
		if !strings.Contains(string(svg), want) {
			t.Errorf("SVG lacks %q", want)
		}
	}

	// The first release was recorded before any of it existed, and says so.
	first, _ := readPhysical(t, c, ts.URL, app, "1")
	if len(first.PublicStartLinks) != 0 || len(first.Workers[0].ObservedBy) != 0 {
		t.Errorf("v1 = %+v, want the moment it was recorded: no link and nobody seen yet", first)
	}
}

// TestEachReaderSeesWhatTheyMaySee: the owner of the worker sees its endpoint, and an
// operator the worker instances; a modeller the application is shared with sees that
// both exist and neither the endpoint nor the instances.
func TestEachReaderSeesWhatTheyMaySee(t *testing.T) {
	ts := newServerOn(t, t.TempDir())
	admin := signedInClient(t, ts.URL)
	createUser(t, admin, ts.URL, "anna")
	bertID := createUserWithRoles(t, admin, ts.URL, "bert", `["modeler","user"]`)
	anna := signInAs(t, ts.URL, "anna", "a-password-that-is-long")
	bert := signInAs(t, ts.URL, "bert", "a-password-that-is-long")

	app := setUpPhysical(t, anna, admin, ts.URL)
	mustCall(t, anna, http.MethodPut, ts.URL+"/api/v1/applications/"+app+"/members/"+bertID, `{"role":"viewer"}`, "application/json", http.StatusOK)

	mine, _ := readPhysical(t, anna, ts.URL, app, "2")
	if w := mine.Workers[0]; w.Endpoint == nil || w.Endpoint.Host != "clio.example.com" || len(w.ObservedBy) != 1 {
		t.Errorf("the worker's owner and operator sees %+v, want the endpoint and the instance", w)
	}

	theirs, _ := readPhysical(t, bert, ts.URL, app, "2")
	w := theirs.Workers[0]
	if w.Endpoint == nil || !w.Endpoint.Withheld || w.Endpoint.Host != "" {
		t.Errorf("a modeller without access to the worker sees endpoint %+v, want it withheld", w.Endpoint)
	}
	if w.ObservedBy != nil || !w.ObserversWithheld || w.Observed != 1 {
		t.Errorf("a non-operator sees observers %v (withheld=%v, count=%d), want the ids withheld and the count kept", w.ObservedBy, w.ObserversWithheld, w.Observed)
	}
	for _, leaked := range []string{"clio.example.com", "extern-1"} {
		for _, suffix := range []string{"", ".svg", ".xml"} {
			_, body := call(t, bert, http.MethodGet, ts.URL+"/api/v1/applications/"+app+"/releases/2/architecture"+suffix, "", "")
			if strings.Contains(string(body), leaked) {
				t.Errorf("architecture%s carries %q to a reader who may not see it", suffix, leaked)
			}
		}
	}
}

// reportedBPMN names two workers this server holds no configuration for: the
// workers that serve them keep their own (ADR-0168), and report where they connect.
const reportedBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="defs">
  <bpmn:process id="meldung" isExecutable="true">
    <bpmn:startEvent id="Start"/>
    <bpmn:serviceTask id="Fremd">
      <bpmn:extensionElements><atlas:clioConnector connector="fremd" subject="a" eventType="A"/></bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:serviceTask id="Zwei">
      <bpmn:extensionElements><atlas:clioConnector connector="zwei" subject="b" eventType="B"/></bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:serviceTask id="Melden">
      <bpmn:extensionElements><zeebe:taskDefinition type="melden"/></bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="End"/>
    <bpmn:sequenceFlow id="f1" sourceRef="Start" targetRef="Fremd"/>
    <bpmn:sequenceFlow id="f2" sourceRef="Fremd" targetRef="Zwei"/>
    <bpmn:sequenceFlow id="f3" sourceRef="Zwei" targetRef="Melden"/>
    <bpmn:sequenceFlow id="f4" sourceRef="Melden" targetRef="End"/>
  </bpmn:process>
</bpmn:definitions>`

// TestAReleaseRecordsTheEndpointsWorkersReport: a worker that keeps its own
// configuration reports where it connects, and the release records that — as
// reported, for operators only — and records two instances that disagree as
// disagreeing rather than picking one.
func TestAReleaseRecordsTheEndpointsWorkersReport(t *testing.T) {
	ts := newServerOn(t, t.TempDir())
	admin := signedInClient(t, ts.URL)
	createUser(t, admin, ts.URL, "anna")
	bertID := createUserWithRoles(t, admin, ts.URL, "bert", `["modeler","user"]`)
	anna := signInAs(t, ts.URL, "anna", "a-password-that-is-long")
	bert := signInAs(t, ts.URL, "bert", "a-password-that-is-long")

	var app struct{ ID string }
	if err := json.Unmarshal(mustCall(t, anna, http.MethodPost, ts.URL+"/api/v1/applications", `{"name":"Meldungen"}`, "application/json", http.StatusOK), &app); err != nil {
		t.Fatal(err)
	}
	mustCall(t, anna, http.MethodPost, ts.URL+"/api/v1/drafts?projectId="+app.ID, reportedBPMN, "application/xml", http.StatusOK)
	// v1 makes the job type known; the workers poll it; v2 records what they said.
	mustCall(t, anna, http.MethodPost, ts.URL+"/api/v1/applications/"+app.ID+"/publish", `{}`, "application/json", http.StatusOK)
	mustCall(t, anna, http.MethodPost, ts.URL+"/api/v1/jobs/activate",
		`{"type":"melden","worker":"w1","connectors":["fremd","zwei"],"endpoints":{"fremd":"https://u:p@fremd.example.com:9443/x","zwei":"zwei-a.example.com:25"},"waitMs":0}`,
		"application/json", http.StatusOK)
	mustCall(t, anna, http.MethodPost, ts.URL+"/api/v1/jobs/activate",
		`{"type":"melden","worker":"w2","connectors":["zwei"],"endpoints":{"zwei":"zwei-b.example.com:25"},"waitMs":0}`,
		"application/json", http.StatusOK)
	mustCall(t, anna, http.MethodPost, ts.URL+"/api/v1/applications/"+app.ID+"/publish", `{}`, "application/json", http.StatusOK)
	mustCall(t, anna, http.MethodPut, ts.URL+"/api/v1/applications/"+app.ID+"/members/"+bertID, `{"role":"viewer"}`, "application/json", http.StatusOK)

	type site struct {
		Name     string `json:"name"`
		Endpoint *struct {
			Scheme, Host, Port string
			Withheld           bool
		} `json:"endpoint"`
		EndpointReported bool `json:"endpointReported"`
		EndpointsDiffer  bool `json:"endpointsDiffer"`
	}
	read := func(c *http.Client) map[string]site {
		raw := mustCall(t, c, http.MethodGet, ts.URL+"/api/v1/applications/"+app.ID+"/releases/2/architecture", "", "", http.StatusOK)
		var out struct {
			Architecture struct {
				Physical struct {
					Workers []site `json:"workers"`
				} `json:"physical"`
			} `json:"architecture"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		byName := map[string]site{}
		for _, w := range out.Architecture.Physical.Workers {
			byName[w.Name] = w
		}
		return byName
	}

	operator := read(anna)
	if f := operator["fremd"]; f.Endpoint == nil || f.Endpoint.Host != "fremd.example.com" || f.Endpoint.Port != "9443" || !f.EndpointReported {
		t.Errorf("fremd = %+v, want the reported endpoint, reduced and marked as reported", f)
	}
	if z := operator["zwei"]; z.Endpoint != nil || !z.EndpointsDiffer {
		t.Errorf("zwei = %+v, want no endpoint and the disagreement recorded", z)
	}
	_, svg := call(t, anna, http.MethodGet, ts.URL+"/api/v1/applications/"+app.ID+"/releases/2/architecture.svg", "", "")
	for _, want := range []string{"→ https://fremd.example.com:9443 (reported)", "workers report different endpoints"} {
		if !strings.Contains(string(svg), want) {
			t.Errorf("SVG lacks %q", want)
		}
	}

	viewer := read(bert)
	if f := viewer["fremd"]; f.Endpoint == nil || !f.Endpoint.Withheld || f.Endpoint.Host != "" {
		t.Errorf("a non-operator reads fremd = %+v, want the reported endpoint withheld", f)
	}
}
