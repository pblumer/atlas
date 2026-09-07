package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Filing a *deployment* into an application (ADR-0034). A deployment carries its own
// project id, stamped at deploy time from the ?projectId= the editor sends or, absent
// that, inherited from the matching draft at that moment. Nothing could change it
// afterwards: moving the draft moves the draft, and a deployment made before its
// application existed — or through the raw API — stayed Ungrouped for good, visible
// on the Modeler home and on the Starmap as a process belonging to nothing.
//
// These tests pin what the move does and, more importantly, what it must not: it is
// a filing change, so the version, the model and everything running must come
// through it untouched.

// deployedProcessResp mirrors one row of GET /api/v1/processes. Declared here rather
// than imported so the test pins the wire contract a browser reads.
type deployedProcessResp struct {
	Key       uint64 `json:"key"`
	ProcessID string `json:"processId"`
	Version   int32  `json:"version"`
	ProjectID string `json:"projectId"`
	Active    bool   `json:"active"`
}

func listProcesses(t *testing.T, ts *httptest.Server) []deployedProcessResp {
	t.Helper()
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/processes", "", "")
	if code != http.StatusOK {
		t.Fatalf("list processes status = %d, body = %s", code, body)
	}
	var list []deployedProcessResp
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decode processes: %v (%s)", err, body)
	}
	return list
}

// deployProcess deploys one BPMN model and returns the key of the first process in it.
func deployProcess(t *testing.T, ts *httptest.Server, xml string) uint64 {
	t.Helper()
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", xml, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy status = %d, body = %s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deploy: %v (%s)", err, body)
	}
	return dep.Key
}

// makeApplication creates an application and returns its id.
func makeApplication(t *testing.T, ts *httptest.Server, name string) string {
	t.Helper()
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/applications",
		fmt.Sprintf(`{"name":%q}`, name), "application/json")
	if code != http.StatusOK {
		t.Fatalf("create application status = %d, body = %s", code, body)
	}
	var app struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &app); err != nil {
		t.Fatalf("decode application: %v (%s)", err, body)
	}
	return app.ID
}

func moveProcess(t *testing.T, ts *httptest.Server, key uint64, appID string) (int, []byte) {
	t.Helper()
	return doReq(t, ts, http.MethodPatch, fmt.Sprintf("/api/v1/processes/%d", key),
		fmt.Sprintf(`{"projectId":%q}`, appID), "application/json")
}

// TestMoveProcessFilesEveryVersionOfIt is the whole point of addressing this by key
// and acting on the process id: filing belongs to the definition, not to one version
// of it. Moving v2 and leaving v1 behind would show one process in two folders on the
// Modeler home, and would leave the Starmap — which reads the current version — saying
// it was fixed while the history disagreed.
func TestMoveProcessFilesEveryVersionOfIt(t *testing.T) {
	ts := newTestServer(t)
	deployProcess(t, ts, idBPMN("invoice"))
	second := deployProcess(t, ts, idBPMN("invoice"))
	app := makeApplication(t, ts, "Billing")

	code, body := moveProcess(t, ts, second, app)
	if code != http.StatusOK {
		t.Fatalf("move status = %d, body = %s", code, body)
	}

	var moved int
	for _, p := range listProcesses(t, ts) {
		if p.ProcessID != "invoice" {
			continue
		}
		moved++
		if p.ProjectID != app {
			t.Errorf("v%d projectId = %q, want %q — every version follows", p.Version, p.ProjectID, app)
		}
	}
	if moved != 2 {
		t.Fatalf("saw %d versions of invoice, want 2", moved)
	}
}

// TestMoveProcessLeavesTheDefinitionAlone: this is a filing change and nothing else.
// A move that bumped a version, deactivated a process or disturbed its model would be
// a deploy wearing a smaller name, and an operator would have no reason to trust it on
// a running system.
func TestMoveProcessLeavesTheDefinitionAlone(t *testing.T) {
	ts := newTestServer(t)
	key := deployProcess(t, ts, idBPMN("invoice"))
	app := makeApplication(t, ts, "Billing")

	_, beforeXML := doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/processes/%d/xml", key), "", "")

	if code, body := moveProcess(t, ts, key, app); code != http.StatusOK {
		t.Fatalf("move status = %d, body = %s", code, body)
	}

	list := listProcesses(t, ts)
	if len(list) != 1 {
		t.Fatalf("processes = %d, want 1 — a move deploys nothing", len(list))
	}
	if list[0].Key != key || list[0].Version != 1 {
		t.Errorf("key/version = %d/v%d, want %d/v1 — a move is not a deploy",
			list[0].Key, list[0].Version, key)
	}
	if !list[0].Active {
		t.Error("the process was deactivated by being filed")
	}
	_, afterXML := doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/processes/%d/xml", key), "", "")
	if string(beforeXML) != string(afterXML) {
		t.Error("the model changed — a move must not touch the deployed XML")
	}
}

// TestMoveProcessBackToUngrouped: the empty id is a destination, not a missing field.
// It is how a process leaves an application without a second endpoint, and it is what
// a draft's move already means.
func TestMoveProcessBackToUngrouped(t *testing.T) {
	ts := newTestServer(t)
	key := deployProcess(t, ts, idBPMN("invoice"))
	app := makeApplication(t, ts, "Billing")

	if code, body := moveProcess(t, ts, key, app); code != http.StatusOK {
		t.Fatalf("move in status = %d, body = %s", code, body)
	}
	if code, body := moveProcess(t, ts, key, ""); code != http.StatusOK {
		t.Fatalf("move out status = %d, body = %s", code, body)
	}

	if got := listProcesses(t, ts)[0].ProjectID; got != "" {
		t.Errorf("projectId = %q, want empty — Ungrouped is a place a process can go", got)
	}
}

// TestMoveProcessIsIdempotent: filing something where it already is changes nothing
// and is not an error. A script re-running over an estate must not have to remember
// what it already did.
func TestMoveProcessIsIdempotent(t *testing.T) {
	ts := newTestServer(t)
	key := deployProcess(t, ts, idBPMN("invoice"))
	app := makeApplication(t, ts, "Billing")

	for i := range 2 {
		if code, body := moveProcess(t, ts, key, app); code != http.StatusOK {
			t.Fatalf("move %d status = %d, body = %s", i+1, code, body)
		}
	}
	if got := listProcesses(t, ts)[0].ProjectID; got != app {
		t.Errorf("projectId = %q, want %q", got, app)
	}
}

// TestMoveProcessRefusesAnUnknownTarget: a project id nobody has is a typo, and
// accepting it would file the process into a folder that does not exist — which reads
// on the Modeler home exactly like Ungrouped, so the caller would think it worked.
func TestMoveProcessRefusesAnUnknownTarget(t *testing.T) {
	ts := newTestServer(t)
	key := deployProcess(t, ts, idBPMN("invoice"))

	if code, _ := moveProcess(t, ts, key, "no-such-application"); code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for an application that does not exist", code)
	}
	if got := listProcesses(t, ts)[0].ProjectID; got != "" {
		t.Errorf("projectId = %q — a refused move must change nothing", got)
	}
}

// TestMoveProcessRefusesAnUnknownKey: there is nothing to file.
func TestMoveProcessRefusesAnUnknownKey(t *testing.T) {
	ts := newTestServer(t)
	app := makeApplication(t, ts, "Billing")

	if code, _ := moveProcess(t, ts, 999999, app); code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for a key nothing is deployed under", code)
	}
}

// TestMoveProcessRefusesTheProtectedApplication: the platform-managed application's
// contents are Atlas's own (ADR-0122). Nothing a caller does may add to it, and the
// draft move already refuses this — a second door into the same room would be the
// bug that makes the first check pointless.
func TestMoveProcessRefusesTheProtectedApplication(t *testing.T) {
	ts := newTestServer(t)
	key := deployProcess(t, ts, idBPMN("invoice"))

	if code, _ := moveProcess(t, ts, key, "system"); code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for the protected system application", code)
	}
	if got := listProcesses(t, ts)[0].ProjectID; got != "" {
		t.Errorf("projectId = %q — a refused move must change nothing", got)
	}
}

// TestMoveProcessCarriesTheWholeCollaboration: a collaboration's pools are one
// drawing, deployed together and filed together, and the process list shows them as
// one row. Moving the row has to move the drawing — otherwise a pool nobody can
// address separately is left behind in another folder.
func TestMoveProcessCarriesTheWholeCollaboration(t *testing.T) {
	ts := newTestServer(t)
	key := deployProcess(t, ts, collabBPMN)
	app := makeApplication(t, ts, "Trading")

	if code, body := moveProcess(t, ts, key, app); code != http.StatusOK {
		t.Fatalf("move status = %d, body = %s", code, body)
	}

	seen := map[string]string{}
	for _, p := range listProcesses(t, ts) {
		seen[p.ProcessID] = p.ProjectID
	}
	for _, pid := range []string{"buyer", "seller"} {
		if seen[pid] != app {
			t.Errorf("%s projectId = %q, want %q — the pools are one drawing", pid, seen[pid], app)
		}
	}
}

// TestMoveProcessSurvivesARestart: the filing is on disk, not only in the registry.
// A move that lived in memory would look right until the next restart put every
// process back where it was, which is the worst way for this to fail — silently, and
// long after anybody was watching.
func TestMoveProcessSurvivesARestart(t *testing.T) {
	dir := t.TempDir()

	first := boot(t, dir)
	key := deployProcess(t, first.ts, idBPMN("invoice"))
	app := makeApplication(t, first.ts, "Billing")
	if code, body := moveProcess(t, first.ts, key, app); code != http.StatusOK {
		t.Fatalf("move status = %d, body = %s", code, body)
	}
	first.shutdown()

	second := boot(t, dir)
	defer second.shutdown()

	list := listProcesses(t, second.ts)
	if len(list) != 1 || list[0].ProjectID != app {
		t.Errorf("after restart: %+v, want one process filed under %q", list, app)
	}
}
