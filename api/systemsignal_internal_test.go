package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// intakeListenerBPMN is what an installation deploys to hear about intake requests
// (ADR-0431): a signal start on
// "atlas.user.requested" and one Discord message. It is the recipe
// examples/benutzerverwaltung/README.md documents, so the recipe is known to deploy.
const intakeListenerBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             xmlns:atlas="http://atlas/schema/1.0"
             id="defs_aufnahme_discord" targetNamespace="http://atlas/examples">
  <signal id="sig_user_requested" name="atlas.user.requested"/>
  <process id="proc_aufnahme_discord" name="Aufnahme-Antrag nach Discord melden" isExecutable="true">
    <startEvent id="start" name="Aufnahme beantragt">
      <outgoing>f_start_melden</outgoing>
      <signalEventDefinition signalRef="sig_user_requested"/>
    </startEvent>
    <serviceTask id="melden" name="In Discord melden">
      <extensionElements>
        <atlas:discordConnector connector="discord" operation="send-message"
          channel="123456789012345678"
          content="=&quot;Neuer Aufnahme-Antrag von &quot; + vorname + &quot; &quot; + nachname + &quot; (&quot; + benutzername + &quot;). Die Freigabe wartet in den Aufgaben.&quot;"
          resultVariable="gesendet">
          <atlas:discordField name="allowed_mentions" value="={parse: []}"/>
        </atlas:discordConnector>
      </extensionElements>
      <incoming>f_start_melden</incoming>
      <outgoing>f_melden_ende</outgoing>
    </serviceTask>
    <endEvent id="ende" name="Gemeldet">
      <incoming>f_melden_ende</incoming>
    </endEvent>
    <sequenceFlow id="f_start_melden" sourceRef="start" targetRef="melden"/>
    <sequenceFlow id="f_melden_ende" sourceRef="melden" targetRef="ende"/>
  </process>
</definitions>`

// TestSystemIntakeAnnouncesTheRequestAsASignal drives the intake process with a
// listener deployed beside it. The request must start exactly one listener instance,
// carrying what the requester typed and the proposed username, while the intake
// itself still waits at "Antrag freigeben" — the notice must not hold up the request.
// The approval that follows must not announce anything again: the throw sits before
// the step where initialpasswort comes into being, and no listener may ever see it.
func TestSystemIntakeAnnouncesTheRequestAsASignal(t *testing.T) {
	srv, _ := newSystemServer(t, WithSystemProcesses())
	h := srv.Handler()

	do := func(method, path, body, contentType string) (int, []byte) {
		t.Helper()
		var req *http.Request
		if body != "" {
			req = httptest.NewRequest(method, path, strings.NewReader(body))
			req.Header.Set("Content-Type", contentType)
		} else {
			req = httptest.NewRequest(method, path, nil)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.Bytes()
	}

	code, raw := do(http.MethodPost, "/api/v1/deployments", intakeListenerBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy listener: %d %s", code, raw)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(raw, &dep); err != nil || dep.Key == 0 {
		t.Fatalf("decode listener deployment: %v (%s)", err, raw)
	}

	var intakeKey uint64
	for _, r := range listProcesses(t, h) {
		if r.ProcessID == "proc_benutzer_aufnahme" {
			intakeKey = r.Key
		}
	}
	if intakeKey == 0 {
		t.Fatal("intake process not deployed")
	}

	type listenerRow struct {
		Key       uint64 `json:"key"`
		Variables []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"variables"`
	}
	listeners := func() []listenerRow {
		t.Helper()
		code, raw := do(http.MethodGet, "/api/v1/instances?process="+strconv.FormatUint(dep.Key, 10), "", "")
		if code != http.StatusOK {
			t.Fatalf("list listener instances: %d %s", code, raw)
		}
		var rows []listenerRow
		if err := json.Unmarshal(listRows(t, raw), &rows); err != nil {
			t.Fatalf("decode listener instances: %v (%s)", err, raw)
		}
		return rows
	}

	startInstance(t, h, intakeKey,
		`{"vorname":"Neuer","nachname":"Kollege","email":"neuer@example.org","abteilung":"Informatik","begruendung":"Projektmitarbeit"}`)

	rows := listeners()
	if len(rows) != 1 {
		t.Fatalf("one intake request started %d listener instances, want exactly 1", len(rows))
	}
	got := map[string]string{}
	for _, v := range rows[0].Variables {
		got[v.Name] = v.Value
	}
	for name, want := range map[string]string{
		"vorname":      "Neuer",
		"nachname":     "Kollege",
		"email":        "neuer@example.org",
		"abteilung":    "Informatik",
		"begruendung":  "Projektmitarbeit",
		"benutzername": "neuer.kollege",
	} {
		if !strings.Contains(got[name], want) {
			t.Errorf("listener variable %s = %q, want it to carry %q", name, got[name], want)
		}
	}
	for _, name := range []string{"initialpasswort", "rolle", "entscheidung"} {
		if _, leaked := got[name]; leaked {
			t.Errorf("listener received %s; the signal must be thrown before the approval decides it", name)
		}
	}

	// The intake did not wait on the notice: its approval task is open.
	code, raw = do(http.MethodGet, "/api/v1/tasks", "", "")
	if code != http.StatusOK {
		t.Fatalf("list tasks: %d %s", code, raw)
	}
	var tasks []struct {
		Key       uint64 `json:"key"`
		ProcessID string `json:"processId"`
		ElementID string `json:"elementId"`
	}
	if err := json.Unmarshal(listRows(t, raw), &tasks); err != nil {
		t.Fatalf("decode tasks: %v (%s)", err, raw)
	}
	var taskKey uint64
	for _, it := range tasks {
		if it.ProcessID == "proc_benutzer_aufnahme" && it.ElementID == "freigabe" {
			taskKey = it.Key
		}
	}
	if taskKey == 0 {
		t.Fatalf("intake is not waiting at freigabe after the signal: %s", raw)
	}

	// Approving announces nothing further.
	code, raw = do(http.MethodPost, "/api/v1/tasks/"+strconv.FormatUint(taskKey, 10)+"/complete",
		`{"variables":{"rolle":"user","benutzername":"neuer.kollege","entscheidung":"anlegen","initialpasswort":"willkommen1"}}`,
		"application/json")
	if code != http.StatusOK {
		t.Fatalf("complete freigabe task: %d %s", code, raw)
	}
	if n := len(listeners()); n != 1 {
		t.Fatalf("after the approval there are %d listener instances, want still 1", n)
	}
}
