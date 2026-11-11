package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The interaction graph of ADR-0559: two processes deployed from two files, linked
// only by message names, seen around one of them.

// orderBPMN is a secretariat's order process, deployed from a file of its own under
// the pool "Sekretariat". It records a note nobody receives, releases the order to
// shipping, and waits for shipping to report back.
const orderBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <collaboration id="C">
    <participant id="P_order" name="Sekretariat" processRef="bestellung"/>
  </collaboration>
  <message id="Msg_note" name="protokoll"/>
  <message id="Msg_release" name="freigabe">
    <extensionElements><zeebe:subscription correlationKey="= orderId"/></extensionElements>
  </message>
  <message id="Msg_shipped" name="versandt">
    <extensionElements><zeebe:subscription correlationKey="= orderId"/></extensionElements>
  </message>
  <process id="bestellung" isExecutable="true">
    <startEvent id="start"/>
    <intermediateThrowEvent id="note"><messageEventDefinition messageRef="Msg_note"/></intermediateThrowEvent>
    <intermediateThrowEvent id="release"><messageEventDefinition messageRef="Msg_release"/></intermediateThrowEvent>
    <intermediateCatchEvent id="wait"><messageEventDefinition messageRef="Msg_shipped"/></intermediateCatchEvent>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="note"/>
    <sequenceFlow id="f2" sourceRef="note" targetRef="release"/>
    <sequenceFlow id="f3" sourceRef="release" targetRef="wait"/>
    <sequenceFlow id="f4" sourceRef="wait" targetRef="end"/>
  </process>
</definitions>`

// shippingBPMN is started by the release and reports back, correlated on the order.
const shippingBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <message id="Msg_release" name="freigabe">
    <extensionElements><zeebe:subscription correlationKey="= orderId"/></extensionElements>
  </message>
  <message id="Msg_shipped" name="versandt">
    <extensionElements><zeebe:subscription correlationKey="= orderId"/></extensionElements>
  </message>
  <process id="versand" name="Versand" isExecutable="true">
    <startEvent id="s"><messageEventDefinition messageRef="Msg_release"/></startEvent>
    <intermediateThrowEvent id="report"><messageEventDefinition messageRef="Msg_shipped"/></intermediateThrowEvent>
    <endEvent id="e"/>
    <sequenceFlow id="g1" sourceRef="s" targetRef="report"/>
    <sequenceFlow id="g2" sourceRef="report" targetRef="e"/>
  </process>
</definitions>`

type graphParticipant struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Center    bool   `json:"center"`
	Name      string `json:"name"`
	ProcessID string `json:"processId"`
	ProjectID string `json:"projectId"`
}

type graphObserved struct {
	Received int    `json:"received"`
	Started  int    `json:"started"`
	LastAt   string `json:"lastAt"`
}

type graphExchange struct {
	From     string         `json:"from"`
	To       string         `json:"to"`
	Message  string         `json:"message"`
	Possible bool           `json:"possible"`
	Observed *graphObserved `json:"observed"`
}

type interactionGraph struct {
	ProcessID    string             `json:"processId"`
	Participants []graphParticipant `json:"participants"`
	Exchanges    []graphExchange    `json:"exchanges"`
	Unreceived   []struct {
		From    string `json:"from"`
		Message string `json:"message"`
		Count   int    `json:"count"`
	} `json:"unreceived"`
	Ports []struct {
		Direction string   `json:"direction"`
		Message   string   `json:"message"`
		Elements  []string `json:"elements"`
	} `json:"ports"`
	Observed struct {
		Rows    int  `json:"rows"`
		Partial bool `json:"partial"`
	} `json:"observed"`
	Retention string `json:"retention"`
}

func readGraph(t *testing.T, c *http.Client, ts *httptest.Server, query string) interactionGraph {
	t.Helper()
	code, body := cReq(t, c, ts, "GET", "/api/v1/process-interactions"+query, "")
	var g interactionGraph
	if code != http.StatusOK || json.Unmarshal(body, &g) != nil {
		t.Fatalf("process interactions%s = %d %s", query, code, body)
	}
	return g
}

func exchangeIndex(g interactionGraph) map[string]graphExchange {
	out := map[string]graphExchange{}
	for _, e := range g.Exchanges {
		out[e.From+" -> "+e.To+" : "+e.Message] = e
	}
	return out
}

// TestTheInteractionGraphShowsWhatEachReaderMayRead: an administrator sees the
// order and the shipping process deployed from two files as one exchange, possible
// and observed both ways, with the note nobody received; a member of the order's
// project sees the same possible exchanges, but the arrivals they may not read come
// from, and go to, outside their view.
func TestTheInteractionGraphShowsWhatEachReaderMayRead(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	admin := newClient(t)
	login(t, admin, ts, "admin", "password1")
	memberID := createUserWithRoles(t, admin, ts.URL, "member", `["user"]`)

	code, body := cReq(t, admin, ts, "POST", "/api/v1/projects", `{"name":"Sekretariat"}`)
	var proj struct {
		ID string `json:"id"`
	}
	if (code != http.StatusCreated && code != http.StatusOK) || json.Unmarshal(body, &proj) != nil {
		t.Fatalf("create project = %d: %s", code, body)
	}
	if code, body := cReq(t, admin, ts, "PUT", "/api/v1/projects/"+proj.ID+"/members/"+memberID,
		`{"role":"viewer"}`); code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("add project member = %d: %s", code, body)
	}
	order := deployIntoProject(t, admin, ts, orderBPMN, proj.ID)
	deployIntoProject(t, admin, ts, shippingBPMN, "")
	if code, body := cReq(t, admin, ts, "POST", fmt.Sprintf("/api/v1/processes/%d/instances", order),
		`{"variables":{"orderId":"A-1"}}`); code != http.StatusOK {
		t.Fatalf("start order = %d: %s", code, body)
	}

	g := readGraph(t, admin, ts, "?processId=bestellung")
	if len(g.Participants) != 2 {
		t.Fatalf("participants = %+v, want the order and the shipping process", g.Participants)
	}
	center, shipping := g.Participants[0], g.Participants[1]
	if !center.Center || center.ID != "process:bestellung" || center.Name != "Sekretariat" || center.ProjectID != proj.ID {
		t.Errorf("center = %+v, want the order process, named after its pool", center)
	}
	if shipping.ID != "process:versand" || shipping.Kind != "process" || shipping.Name != "Versand" {
		t.Errorf("shipping = %+v", shipping)
	}
	ex := exchangeIndex(g)
	release := ex["process:bestellung -> process:versand : freigabe"]
	if !release.Possible || release.Observed == nil || release.Observed.Received != 1 || release.Observed.Started != 1 {
		t.Errorf("release = %+v (%+v), want possible, and observed once starting the shipping instance", release, release.Observed)
	}
	report := ex["process:versand -> process:bestellung : versandt"]
	if !report.Possible || report.Observed == nil || report.Observed.Received != 1 || report.Observed.Started != 0 {
		t.Errorf("report = %+v (%+v), want possible, and observed once into the waiting order", report, report.Observed)
	}
	if len(g.Unreceived) != 1 || g.Unreceived[0].Message != "protokoll" || g.Unreceived[0].Count != 1 {
		t.Errorf("unreceived = %+v, want the note nobody received", g.Unreceived)
	}
	if len(g.Ports) != 3 || g.Ports[0].Direction != "out" || g.Ports[2].Direction != "in" || g.Ports[2].Message != "versandt" {
		t.Errorf("ports = %+v, want two outgoing names and the one it waits for", g.Ports)
	}
	if g.Observed.Partial || g.Observed.Rows == 0 || g.Retention != "720h0m0s" {
		t.Errorf("observed = %+v, retention %q", g.Observed, g.Retention)
	}

	member := signInAs(t, ts.URL, "member", "a-password-that-is-long")
	mg := readGraph(t, member, ts, "?processId=bestellung")
	mex := exchangeIndex(mg)
	if e := mex["process:bestellung -> process:versand : freigabe"]; !e.Possible || e.Observed != nil {
		t.Errorf("member's release to shipping = %+v, want possible and nothing they may read observed", e)
	}
	if e := mex["process:bestellung -> outside-view : freigabe"]; e.Observed == nil || e.Observed.Received != 1 || e.Observed.Started != 1 {
		t.Errorf("member's release outside the view = %+v", e)
	}
	if e := mex["outside-view -> process:bestellung : versandt"]; e.Observed == nil || e.Observed.Received != 1 {
		t.Errorf("member's arrival from outside the view = %+v", e)
	}
	for _, p := range mg.Participants {
		if p.Kind == "outside-view" && p.Name != "" {
			t.Errorf("the participant outside the view is named: %+v", p)
		}
	}

	// A window that ends before anything happened observes nothing, and keeps what
	// is possible.
	empty := readGraph(t, admin, ts, "?processId=bestellung&window=1ns")
	for _, e := range empty.Exchanges {
		if e.Observed != nil {
			t.Errorf("within a nanosecond %+v was observed", e)
		}
	}
	if len(empty.Exchanges) != 2 || len(empty.Unreceived) != 0 {
		t.Errorf("a window of a nanosecond = %+v / %+v, want the two possible exchanges alone", empty.Exchanges, empty.Unreceived)
	}

	for q, want := range map[string]int{
		"":                                  http.StatusBadRequest,
		"?processId=bestellung&window=soon": http.StatusBadRequest,
		"?processId=bestellung&window=-1h":  http.StatusBadRequest,
		"?processId=nowhere":                http.StatusNotFound,
	} {
		if code, body := cReq(t, admin, ts, "GET", "/api/v1/process-interactions"+q, ""); code != want {
			t.Errorf("GET /api/v1/process-interactions%s = %d %s, want %d", q, code, body, want)
		}
	}
}

type replayEvent struct {
	Position       string `json:"position"`
	Kind           string `json:"kind"`
	From           string `json:"from"`
	To             string `json:"to"`
	Message        string `json:"message"`
	Count          int    `json:"count"`
	Started        int    `json:"started"`
	CorrelationKey string `json:"correlationKey"`
}

type replayPage struct {
	Events []replayEvent `json:"events"`
	Next   string        `json:"next"`
	Latest string        `json:"latest"`
	More   bool          `json:"more"`
}

func readReplay(t *testing.T, c *http.Client, ts *httptest.Server, query string) replayPage {
	t.Helper()
	code, body := cReq(t, c, ts, "GET", "/api/v1/process-interactions/replay"+query, "")
	var p replayPage
	if code != http.StatusOK || json.Unmarshal(body, &p) != nil {
		t.Fatalf("replay%s = %d %s", query, code, body)
	}
	return p
}

func eventKinds(p replayPage) []string {
	out := make([]string, len(p.Events))
	for i, e := range p.Events {
		out[i] = e.Kind + " " + e.From + " -> " + e.To + " " + e.Message
	}
	return out
}

// TestTheReplayListsWhatTheGraphCounts: the order's note nobody received, the release
// that started shipping and shipping's report, oldest first, with the order's key; a
// second order arrives after the cursor; a member sees the same history through their
// own rows; the cursors are checked.
func TestTheReplayListsWhatTheGraphCounts(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	admin := newClient(t)
	login(t, admin, ts, "admin", "password1")
	memberID := createUserWithRoles(t, admin, ts.URL, "member", `["user"]`)
	code, body := cReq(t, admin, ts, "POST", "/api/v1/projects", `{"name":"Sekretariat"}`)
	var proj struct {
		ID string `json:"id"`
	}
	if (code != http.StatusCreated && code != http.StatusOK) || json.Unmarshal(body, &proj) != nil {
		t.Fatalf("create project = %d: %s", code, body)
	}
	if code, body := cReq(t, admin, ts, "PUT", "/api/v1/projects/"+proj.ID+"/members/"+memberID,
		`{"role":"viewer"}`); code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("add project member = %d: %s", code, body)
	}
	order := deployIntoProject(t, admin, ts, orderBPMN, proj.ID)
	deployIntoProject(t, admin, ts, shippingBPMN, "")
	start := func(id string) {
		t.Helper()
		if code, body := cReq(t, admin, ts, "POST", fmt.Sprintf("/api/v1/processes/%d/instances", order),
			`{"variables":{"orderId":"`+id+`"}}`); code != http.StatusOK {
			t.Fatalf("start order = %d: %s", code, body)
		}
	}
	start("A-1")

	p := readReplay(t, admin, ts, "?processId=bestellung")
	want := []string{
		"unreceived process:bestellung ->  protokoll",
		"started process:bestellung -> process:versand freigabe",
		"received process:versand -> process:bestellung versandt",
	}
	if got := eventKinds(p); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("replay = %v, want %v", got, want)
	}
	if p.More || p.Events[1].CorrelationKey != "A-1" {
		t.Errorf("page = %+v, want the whole history, keyed by the order", p)
	}

	start("A-2")
	newer := readReplay(t, admin, ts, "?processId=bestellung&after="+p.Latest)
	if got := eventKinds(newer); len(got) != 3 || newer.Events[0].CorrelationKey != "" || newer.Events[1].CorrelationKey != "A-2" {
		t.Errorf("after the latest = %v (%+v)", got, newer.Events)
	}
	older := readReplay(t, admin, ts, "?processId=bestellung&limit=1")
	if len(older.Events) != 1 || !older.More {
		t.Fatalf("one event = %+v", older)
	}
	if back := readReplay(t, admin, ts, "?processId=bestellung&limit=1&before="+older.Next); len(back.Events) != 1 ||
		back.Events[0].Position == older.Events[0].Position {
		t.Errorf("the page before = %+v, after %+v", back, older)
	}

	member := signInAs(t, ts.URL, "member", "a-password-that-is-long")
	mp := readReplay(t, member, ts, "?processId=bestellung")
	var outside, fromOutside int
	for _, e := range mp.Events {
		switch {
		case e.Kind == "outside" && e.To == "outside-view" && e.Message == "freigabe":
			outside++
			if e.Count != 1 || e.Started != 1 {
				t.Errorf("outside event = %+v", e)
			}
		case e.From == "outside-view" && e.Message == "versandt":
			fromOutside++
		case e.To == "process:versand" || e.From == "process:versand":
			t.Errorf("the member reads a row of shipping: %+v", e)
		}
	}
	if outside != 2 || fromOutside != 2 {
		t.Errorf("member's replay = %v, want both orders' release and report through their own rows", eventKinds(mp))
	}

	for q, want := range map[string]int{
		"?processId=bestellung&before=x":         http.StatusBadRequest,
		"?processId=bestellung&before=1&after=1": http.StatusBadRequest,
		"?processId=bestellung&limit=2001":       http.StatusBadRequest,
		"?processId=bestellung&limit=0":          http.StatusBadRequest,
		"?processId=nowhere":                     http.StatusNotFound,
		"?window=1h":                             http.StatusBadRequest,
	} {
		if code, body := cReq(t, admin, ts, "GET", "/api/v1/process-interactions/replay"+q, ""); code != want {
			t.Errorf("GET replay%s = %d %s, want %d", q, code, body, want)
		}
	}
}
