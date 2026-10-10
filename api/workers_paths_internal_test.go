package api

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// The Workers view (ADR-0157, ADR-0168), for what the main tests leave out: a
// worker name several processes wait on, two names in one answer, the registry's
// no-op reports, and a view that cannot count its incidents.

// workersPathsModel is a one-step mail model naming a worker, parameterised on
// the process id as well, so several processes can reference one name.
const workersPathsModel = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0" id="defs">
  <bpmn:process id="%s" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:mailConnector connector="%s" to="=customer" subject="Hello" body="Hi."/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`

// TestWorkersAnUnservedNameNamesEveryProcessWaitingOnIt. Two processes waiting on
// one unserved name are both listed, in a stable order, and each unserved name is
// its own row — so an operator can see the whole of what one missing worker stops.
func TestWorkersAnUnservedNameNamesEveryProcessWaitingOnIt(t *testing.T) {
	srv, _ := newValidateServer(t, WithOffloadedConnectorKinds([]string{"mail"}))
	for _, m := range [][2]string{{"remind", "office365"}, {"notify", "office365"}, {"alert", "gmail"}} {
		code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments",
			fmt.Sprintf(workersPathsModel, m[0], m[1]), "application/xml")
		if code != http.StatusOK && code != http.StatusCreated {
			t.Fatalf("deploy %s: %d %s", m[0], code, raw)
		}
	}

	got := coverage(t, srv)
	if len(got.Unserved) != 2 || got.Unserved[0].Name != "gmail" || got.Unserved[1].Name != "office365" {
		t.Fatalf("unserved = %+v, want gmail then office365", got.Unserved)
	}
	procs := got.Unserved[1].Processes
	if len(procs) != 2 || procs[0].ProcessID != "notify" || procs[1].ProcessID != "remind" {
		t.Errorf("office365 is waited on by %+v, want notify and remind in that order", procs)
	}
}

// TestWorkersRegistryIgnoresEmptyReports. A poll naming no job type, and a report
// of no worker holding nothing, describe nobody; recording them would put an
// empty row on the view.
func TestWorkersRegistryIgnoresEmptyReports(t *testing.T) {
	r := newWorkerRegistry(nil)
	r.holdsConnectors("", nil)
	r.polls("worker-1", "   ")
	if _, ok := r.byName[""]; ok {
		t.Error("an empty connector report created a nameless worker")
	}
	if st, ok := r.byName["worker-1"]; ok {
		t.Errorf("a poll for no job type recorded %+v", st)
	}
}

// TestWorkersAViewThatCannotCountIncidentsSaysSo. Queue depths without incident
// counts would show a failing queue as merely slow.
func TestWorkersAViewThatCannotCountIncidentsSaysSo(t *testing.T) {
	srv, closeSrv := newOffLoopServer(t)
	closeSrv()
	code, body := recertifyHTTPPathsCall(t, srv.Handler(), http.MethodGet, "/api/v1/workers", nil)
	if code != http.StatusInternalServerError || !strings.Contains(body, "count incidents") {
		t.Errorf("workers = %d %s, want 500", code, body)
	}
}
