package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api"
)

// Two models with user tasks, so a folder that names one process has something to
// exclude. The lane and the candidate group are what the editor's listboxes are
// filled from, so they are here rather than assumed.
const folderKundenBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="kunden-anfrage" name="Kundenanfrage" isExecutable="true">
    <laneSet>
      <lane id="lane_ks" name="Kundenservice"><flowNodeRef>sichten</flowNodeRef></lane>
    </laneSet>
    <startEvent id="start"/>
    <userTask id="sichten" name="Anfrage sichten">
      <extensionElements>
        <zeebe:assignmentDefinition candidateGroups="kundenservice"/>
        <zeebe:priorityDefinition priority="70"/>
      </extensionElements>
    </userTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="sichten"/>
    <sequenceFlow id="f2" sourceRef="sichten" targetRef="end"/>
  </process>
</definitions>`

const folderOnboardingBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="onboarding" name="Onboarding" isExecutable="true">
    <startEvent id="start"/>
    <userTask id="willkommen" name="Willkommen an Bord">
      <extensionElements>
        <zeebe:assignmentDefinition candidateGroups="personal"/>
      </extensionElements>
    </userTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="willkommen"/>
    <sequenceFlow id="f2" sourceRef="willkommen" targetRef="end"/>
  </process>
</definitions>`

// deployTasks deploys a model and parks n user tasks by starting n instances of
// it. It is deployAndStart with the instance bodies spelled for us, since every
// folder test starts the same empty instance several times over.
func deployTasks(t *testing.T, ts *httptest.Server, xml string, n int) {
	t.Helper()
	bodies := make([]string, n)
	for i := range bodies {
		bodies[i] = "{}"
	}
	deployAndStart(t, ts, xml, bodies...)
}

type folderJSON struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	FEEL     string `json:"feel"`
	Editable bool   `json:"editable"`
	Position int    `json:"position"`
}

func createFolder(t *testing.T, ts *httptest.Server, body string) folderJSON {
	t.Helper()
	code, out := doReq(t, ts, http.MethodPost, "/api/v1/task-folders", body, "application/json")
	if code != http.StatusOK {
		t.Fatalf("create folder: status=%d body=%s", code, out)
	}
	var f folderJSON
	if err := json.Unmarshal(out, &f); err != nil {
		t.Fatalf("decode folder: %v (%s)", err, out)
	}
	return f
}

func listTasks(t *testing.T, ts *httptest.Server, path string) ([]map[string]any, http.Header) {
	t.Helper()
	res, err := http.Get(ts.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d (%s)", path, res.StatusCode, data)
	}
	var tasks []map[string]any
	if err := json.Unmarshal(data, &tasks); err != nil {
		t.Fatalf("decode tasks: %v (%s)", err, data)
	}
	return tasks, res.Header
}

// TestFolderFiltersTheTaskList is the feature end to end over HTTP: a folder
// naming one process selects that process's open tasks and nothing else, and the
// unfiltered list still returns everything.
func TestFolderFiltersTheTaskList(t *testing.T) {
	ts := newTestServer(t)
	deployTasks(t, ts, folderKundenBPMN, 3)
	deployTasks(t, ts, folderOnboardingBPMN, 2)

	all, _ := listTasks(t, ts, "/api/v1/tasks")
	if len(all) != 5 {
		t.Fatalf("unfiltered list = %d tasks, want 5", len(all))
	}

	f := createFolder(t, ts, `{"name":"Kunden Anfragen","rule":{"match":"all","conditions":[`+
		`{"field":"process","op":"is","value":"kunden-anfrage"}]}}`)
	if f.FEEL != `processId = "kunden-anfrage"` {
		t.Errorf("generated FEEL = %q", f.FEEL)
	}

	got, _ := listTasks(t, ts, "/api/v1/tasks?folder="+f.ID)
	if len(got) != 3 {
		t.Fatalf("folder list = %d tasks, want 3", len(got))
	}
	for _, task := range got {
		if task["processId"] != "kunden-anfrage" {
			t.Errorf("folder list contains %v", task["processId"])
		}
	}
}

// TestFolderFiltersOnTaskMetadata covers the fields that are not the process: the
// candidate group, the lane, and the priority the model states.
func TestFolderFiltersOnTaskMetadata(t *testing.T) {
	ts := newTestServer(t)
	deployTasks(t, ts, folderKundenBPMN, 2)
	deployTasks(t, ts, folderOnboardingBPMN, 1)

	cases := map[string]struct {
		rule string
		want int
	}{
		"candidate group": {`{"field":"group","op":"is","value":"kundenservice"}`, 2},
		"lane":            {`{"field":"lane","op":"is","value":"Kundenservice"}`, 2},
		"lane path":       {`{"field":"lane","op":"under","value":"Kundenservice"}`, 2},
		"priority":        {`{"field":"priority","op":"atLeast","value":"70"}`, 2},
		"unassigned":      {`{"field":"assignee","op":"isEmpty"}`, 3},
		"has no form":     {`{"field":"form","op":"hasNot"}`, 3},
		"task name":       {`{"field":"taskName","op":"contains","value":"Anfrage"}`, 2},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := createFolder(t, ts, `{"name":"`+name+`","rule":{"match":"all","conditions":[`+tc.rule+`]}}`)
			got, _ := listTasks(t, ts, "/api/v1/tasks?folder="+f.ID)
			if len(got) != tc.want {
				t.Errorf("%s selected %d tasks, want %d", name, len(got), tc.want)
			}
		})
	}
}

// TestFolderListPagesLikeTheUnfilteredOne covers the cap and the cursor: a
// filtered page is truncated and resumable exactly as "All tasks" is.
func TestFolderListPagesLikeTheUnfilteredOne(t *testing.T) {
	ts := newTestServer(t)
	deployTasks(t, ts, folderKundenBPMN, 3)

	f := createFolder(t, ts, `{"name":"Alle Kunden","rule":{"match":"all","conditions":[`+
		`{"field":"process","op":"is","value":"kunden-anfrage"}]}}`)

	first, hdr := listTasks(t, ts, "/api/v1/tasks?limit=2&folder="+f.ID)
	if len(first) != 2 {
		t.Fatalf("first page = %d tasks, want 2", len(first))
	}
	if hdr.Get("X-Tasks-Truncated") != "true" {
		t.Fatalf("a capped folder page did not report truncation")
	}
	cursor := hdr.Get("X-Tasks-Next-Cursor")
	if cursor == "" {
		t.Fatal("a capped folder page handed back no cursor")
	}
	second, _ := listTasks(t, ts, "/api/v1/tasks?limit=2&before="+cursor+"&folder="+f.ID)
	if len(second) != 1 {
		t.Fatalf("second page = %d tasks, want the remaining 1", len(second))
	}
	if second[0]["key"] == first[0]["key"] || second[0]["key"] == first[1]["key"] {
		t.Error("the second page repeated a task from the first")
	}
}

// TestFolderCountsComeFromOneScan covers the sidebar badges over HTTP.
func TestFolderCountsComeFromOneScan(t *testing.T) {
	ts := newTestServer(t)
	deployTasks(t, ts, folderKundenBPMN, 3)
	deployTasks(t, ts, folderOnboardingBPMN, 2)

	kunden := createFolder(t, ts, `{"name":"Kunden","rule":{"match":"all","conditions":[`+
		`{"field":"process","op":"is","value":"kunden-anfrage"}]}}`)
	hoch := createFolder(t, ts, `{"name":"Hohe Priorität","rule":{"match":"all","conditions":[`+
		`{"field":"priority","op":"atLeast","value":"70"}]}}`)

	code, body := doReq(t, ts, http.MethodGet, "/api/v1/task-folders/counts", "", "")
	if code != http.StatusOK {
		t.Fatalf("counts = %d (%s)", code, body)
	}
	var counts struct {
		Folders   map[string]int `json:"folders"`
		Total     int            `json:"total"`
		Truncated bool           `json:"truncated"`
	}
	if err := json.Unmarshal(body, &counts); err != nil {
		t.Fatalf("decode counts: %v (%s)", err, body)
	}
	if counts.Total != 5 || counts.Truncated {
		t.Errorf("counts total = %d truncated = %v, want 5 and false", counts.Total, counts.Truncated)
	}
	if counts.Folders[kunden.ID] != 3 {
		t.Errorf("kunden count = %d, want 3", counts.Folders[kunden.ID])
	}
	if counts.Folders[hoch.ID] != 3 {
		t.Errorf("priority count = %d, want 3", counts.Folders[hoch.ID])
	}
}

// TestFolderFieldsAreFilledFromTheModels is the point of the whole design: the
// editor's listboxes are filled from what is deployed, so a person cannot build a
// folder around a process id that does not exist.
func TestFolderFieldsAreFilledFromTheModels(t *testing.T) {
	ts := newTestServer(t)
	deployTasks(t, ts, folderKundenBPMN, 1)
	deployTasks(t, ts, folderOnboardingBPMN, 1)

	code, body := doReq(t, ts, http.MethodGet, "/api/v1/task-folders/fields", "", "")
	if code != http.StatusOK {
		t.Fatalf("fields = %d (%s)", code, body)
	}
	var out struct {
		Fields []struct {
			ID  string `json:"id"`
			Ops []struct {
				ID    string `json:"id"`
				Value string `json:"value"`
			} `json:"ops"`
		} `json:"fields"`
		Options struct {
			Processes []struct{ Value, Label string } `json:"processes"`
			TaskNames []struct{ Value string }        `json:"taskNames"`
			Groups    []struct{ Value string }        `json:"groups"`
			Lanes     []struct{ Value string }        `json:"lanes"`
		} `json:"options"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode fields: %v (%s)", err, body)
	}
	if len(out.Fields) == 0 || len(out.Fields[0].Ops) == 0 {
		t.Fatalf("fields catalogue is empty: %s", body)
	}
	has := func(list []struct{ Value string }, want string) bool {
		for _, o := range list {
			if o.Value == want {
				return true
			}
		}
		return false
	}
	if len(out.Options.Processes) != 2 {
		t.Errorf("processes = %+v, want both deployed models", out.Options.Processes)
	}
	if !has(out.Options.TaskNames, "Anfrage sichten") || !has(out.Options.TaskNames, "Willkommen an Bord") {
		t.Errorf("task names = %+v", out.Options.TaskNames)
	}
	if !has(out.Options.Groups, "kundenservice") || !has(out.Options.Groups, "personal") {
		t.Errorf("candidate groups = %+v", out.Options.Groups)
	}
	if !has(out.Options.Lanes, "Kundenservice") {
		t.Errorf("lanes = %+v", out.Options.Lanes)
	}
}

// TestFolderPreviewCountsBeforeSaving covers the editor's live counter against a
// real task population.
func TestFolderPreviewCountsBeforeSaving(t *testing.T) {
	ts := newTestServer(t)
	deployTasks(t, ts, folderKundenBPMN, 2)
	deployTasks(t, ts, folderOnboardingBPMN, 3)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/task-folders/preview",
		`{"rule":{"match":"all","conditions":[{"field":"process","op":"is","value":"onboarding"}]}}`,
		"application/json")
	if code != http.StatusOK {
		t.Fatalf("preview = %d (%s)", code, body)
	}
	var out struct {
		OK      bool   `json:"ok"`
		FEEL    string `json:"feel"`
		Matched int    `json:"matched"`
		Total   int    `json:"total"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode preview: %v (%s)", err, body)
	}
	if !out.OK || out.Matched != 3 || out.Total != 5 {
		t.Errorf("preview = %+v, want 3 of 5", out)
	}
}

// TestUnknownFolderOnTheTaskListIs404 keeps a mistyped or deleted folder from
// silently degrading into "all tasks", which would show somebody work they were
// not asking for and look like the filter had simply stopped working.
func TestUnknownFolderOnTheTaskListIs404(t *testing.T) {
	ts := newTestServer(t)
	deployTasks(t, ts, folderKundenBPMN, 1)
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/tasks?folder=deadbeef", "", "")
	if code != http.StatusNotFound {
		t.Errorf("unknown folder = %d, want 404 (%s)", code, body)
	}
}

// TestFolderRoundTripOverHTTP covers create, list, update and delete on the real
// routes, including that a stored folder survives as a rule the editor can reopen.
func TestFolderRoundTripOverHTTP(t *testing.T) {
	ts := newTestServer(t)
	deployTasks(t, ts, folderKundenBPMN, 1)

	f := createFolder(t, ts, `{"name":"Kunden Anfragen","rule":{"match":"all","conditions":[`+
		`{"field":"process","op":"is","value":"kunden-anfrage"}]}}`)

	code, body := doReq(t, ts, http.MethodGet, "/api/v1/task-folders", "", "")
	if code != http.StatusOK || !strings.Contains(string(body), `"Kunden Anfragen"`) {
		t.Fatalf("list = %d body=%s", code, body)
	}
	var list []struct {
		ID   string `json:"id"`
		Rule struct {
			Match      string `json:"match"`
			Conditions []struct {
				Field string `json:"field"`
				Op    string `json:"op"`
				Value string `json:"value"`
			} `json:"conditions"`
		} `json:"rule"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list) != 1 || len(list[0].Rule.Conditions) != 1 ||
		list[0].Rule.Conditions[0].Field != "process" || list[0].Rule.Conditions[0].Value != "kunden-anfrage" {
		t.Fatalf("the stored rule did not come back as conditions: %+v", list)
	}

	code, body = doReq(t, ts, http.MethodPut, "/api/v1/task-folders/"+f.ID,
		`{"name":"Umbenannt","rule":{"match":"any","conditions":[`+
			`{"field":"priority","op":"atLeast","value":"70"},{"field":"due","op":"overdue"}]}}`,
		"application/json")
	if code != http.StatusOK {
		t.Fatalf("update = %d (%s)", code, body)
	}
	if !strings.Contains(string(body), `"Umbenannt"`) || !strings.Contains(string(body), "or (dueDate") {
		t.Errorf("updated folder = %s", body)
	}

	code, body = doReq(t, ts, http.MethodDelete, "/api/v1/task-folders/"+f.ID, "", "")
	if code != http.StatusOK {
		t.Fatalf("delete = %d (%s)", code, body)
	}
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/task-folders", "", "")
	if code != http.StatusOK || strings.TrimSpace(string(body)) != "[]" {
		t.Errorf("list after delete = %d %s", code, body)
	}
}

// TestFolderRejectsARuleTheCatalogueDoesNotDescribe keeps an unvalidated rule out
// of the store, where it would be compiled on every listing instead of once here.
func TestFolderRejectsARuleTheCatalogueDoesNotDescribe(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/task-folders",
		`{"name":"Kaputt","rule":{"match":"all","conditions":[{"field":"colour","op":"is","value":"rot"}]}}`,
		"application/json")
	if code != http.StatusBadRequest {
		t.Errorf("create with an unknown field = %d, want 400 (%s)", code, body)
	}
}

// TestFolderCountsReportAFloorAtTheScanBudget covers what happens at the bound:
// a population past the scan budget is answered with what the scan saw, flagged
// as truncated, rather than with a smaller number that reads like a total. The
// budget is lowered here rather than the population raised — the behaviour is the
// same and the test does not have to park twenty thousand tasks to reach it.
func TestFolderCountsReportAFloorAtTheScanBudget(t *testing.T) {
	restore := api.SetMaxFolderScanForTest(2)
	defer restore()

	ts := newTestServer(t)
	deployTasks(t, ts, folderKundenBPMN, 4)

	f := createFolder(t, ts, `{"name":"Alle","rule":{"match":"all","conditions":[`+
		`{"field":"process","op":"is","value":"kunden-anfrage"}]}}`)

	code, body := doReq(t, ts, http.MethodGet, "/api/v1/task-folders/counts", "", "")
	if code != http.StatusOK {
		t.Fatalf("counts = %d (%s)", code, body)
	}
	var counts struct {
		Folders   map[string]int `json:"folders"`
		Total     int            `json:"total"`
		Truncated bool           `json:"truncated"`
	}
	if err := json.Unmarshal(body, &counts); err != nil {
		t.Fatalf("decode counts: %v (%s)", err, body)
	}
	if !counts.Truncated {
		t.Error("a scan that hit its budget did not report itself as truncated")
	}
	if counts.Total != 2 || counts.Folders[f.ID] != 2 {
		t.Errorf("counts = %+v, want the two tasks the budget allowed", counts)
	}

	// The filtered listing hits the same bound and says so the same way, so a
	// client can tell "this is the whole folder" from "this is what we got to".
	res, err := http.Get(ts.URL + "/api/v1/tasks?folder=" + f.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	defer res.Body.Close()
	if res.Header.Get("X-Tasks-Truncated") != "true" {
		t.Error("a folder page cut short by the scan budget did not report truncation")
	}
	if res.Header.Get("X-Tasks-Next-Cursor") == "" {
		t.Error("a truncated folder page handed back no cursor to resume from")
	}
}

// instanceAgeBPMN parks a task on a process with no priority, lane or group, so a
// folder built on the instance's age has nothing else to accidentally match on.
const instanceAgeBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="langlaeufer" name="Langläufer" isExecutable="true">
    <startEvent id="s"/>
    <userTask id="warten" name="Warten"/>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="warten"/>
    <sequenceFlow id="f2" sourceRef="warten" targetRef="e"/>
  </process>
</definitions>`

// TestFolderFiltersOnInstanceAge covers the one field that is not on the task
// itself: how long the process instance carrying it has been running. It is the
// only condition that costs the scan a second store read, and the scan takes it
// only for a rule that asks — so this also exercises that branch.
func TestFolderFiltersOnInstanceAge(t *testing.T) {
	ts := newTestServer(t)
	deployTasks(t, ts, instanceAgeBPMN, 2)

	// Everything started in this test is seconds old, so "less than a day" holds
	// for both tasks and "more than a day" for neither.
	fresh := createFolder(t, ts, `{"name":"Frisch","rule":{"match":"all","conditions":[`+
		`{"field":"instanceAge","op":"newerThan","value":"1","unit":"d"}]}}`)
	if fresh.FEEL != `instanceCreatedAt > scanAt - duration("P1D")` {
		t.Errorf("generated FEEL = %q", fresh.FEEL)
	}
	got, _ := listTasks(t, ts, "/api/v1/tasks?folder="+fresh.ID)
	if len(got) != 2 {
		t.Errorf("newer-than-a-day selected %d tasks, want both", len(got))
	}

	stale := createFolder(t, ts, `{"name":"Liegengeblieben","rule":{"match":"all","conditions":[`+
		`{"field":"instanceAge","op":"olderThan","value":"1","unit":"d"}]}}`)
	got, _ = listTasks(t, ts, "/api/v1/tasks?folder="+stale.ID)
	if len(got) != 0 {
		t.Errorf("older-than-a-day selected %d tasks, want none", len(got))
	}

	// An hours-based bound generates the other duration form and reads the same way.
	hours := createFolder(t, ts, `{"name":"Letzte Stunde","rule":{"match":"all","conditions":[`+
		`{"field":"instanceAge","op":"newerThan","value":"1","unit":"h"}]}}`)
	if hours.FEEL != `instanceCreatedAt > scanAt - duration("PT1H")` {
		t.Errorf("hours FEEL = %q", hours.FEEL)
	}
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/task-folders/counts", "", "")
	if code != http.StatusOK {
		t.Fatalf("counts = %d (%s)", code, body)
	}
	var counts struct {
		Folders map[string]int `json:"folders"`
	}
	if err := json.Unmarshal(body, &counts); err != nil {
		t.Fatal(err)
	}
	if counts.Folders[fresh.ID] != 2 || counts.Folders[stale.ID] != 0 || counts.Folders[hours.ID] != 2 {
		t.Errorf("counts = %+v", counts.Folders)
	}
}
