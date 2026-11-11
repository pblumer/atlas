package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// freigabeTaskBPMN parks on a task offered to the group intmgr, bound to the form
// freigabe-form.
const freigabeTaskBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="freigabe" name="Freigabe" isExecutable="true">
    <startEvent id="start"/>
    <userTask id="approve" name="Freigeben">
      <extensionElements>
        <zeebe:assignmentDefinition candidateGroups="intmgr"/>
        <zeebe:formDefinition formId="freigabe-form"/>
      </extensionElements>
    </userTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="approve"/>
    <sequenceFlow id="f2" sourceRef="approve" targetRef="end"/>
  </process>
</definitions>`

const freigabeFormV1 = `{"type":"default","components":[` +
	`{"type":"number","key":"betrag","label":"Betrag","disabled":true},` +
	`{"type":"checkbox","key":"genehmigt","label":"Genehmigt"},` +
	`{"type":"textfield","key":"kommentar","label":"Kommentar"}]}`

const freigabeFormV2 = `{"type":"default","components":[` +
	`{"type":"checkbox","key":"genehmigt","label":"Genehmigt"},` +
	`{"type":"textfield","key":"neu","label":"Neu"}]}`

// completedView is the part of GET /api/v1/tasks/completed/{key} the test reads.
type completedView struct {
	Task struct {
		Key          string `json:"key"`
		Name         string `json:"name"`
		ProcessID    string `json:"processId"`
		FormID       string `json:"formId"`
		FormRevision string `json:"formRevision"`
		CompletedBy  string `json:"completedBy"`
	} `json:"task"`
	Form *struct {
		ID       string          `json:"id"`
		Revision string          `json:"revision"`
		Schema   json.RawMessage `json:"schema"`
	} `json:"form"`
	FormMissing bool           `json:"formMissing"`
	Values      map[string]any `json:"values"`
	Answered    []string       `json:"answered"`
}

// ADR-0554: the person who completed a task lists it and reads it again, in the
// revision of the form they answered through, with the values the form binds as they
// stood once it was answered. Nobody else does, not even an administrator, and the open
// task's own route keeps answering 404.
func TestACompletedTaskIsReadableToItsCompleterInTheFormItWasAnsweredIn(t *testing.T) {
	ts, admin, ids, clients := intmgrServer(t)
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/forms",
		`{"id":"freigabe-form","name":"Freigabe","schema":`+freigabeFormV1+`}`); code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("save form: %d (%s)", code, b)
	}
	code, body := cReq(t, admin, ts, "POST", "/api/v1/deployments", freigabeTaskBPMN)
	if code != http.StatusOK {
		t.Fatalf("deploy: %d (%s)", code, body)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &deploy); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	if code, b := cReq(t, admin, ts, "POST", fmt.Sprintf("/api/v1/processes/%d/instances", deploy.Key),
		`{"variables":{"betrag":100,"geheim":"nicht im Formular"}}`); code != http.StatusOK {
		t.Fatalf("start: %d (%s)", code, b)
	}
	var key uint64
	for k := range taskKeysSeenBy(t, ts, clients["imke"], "") {
		key = k
	}
	if key == 0 {
		t.Fatal("imke is shown no task")
	}

	// The browser names the revision it rendered.
	code, body = cReq(t, clients["imke"], ts, "GET", "/api/v1/forms/freigabe-form", "")
	if code != http.StatusOK {
		t.Fatalf("get form: %d (%s)", code, body)
	}
	var shown struct {
		Revision string `json:"revision"`
	}
	if err := json.Unmarshal(body, &shown); err != nil || !strings.HasPrefix(shown.Revision, "sha256:") {
		t.Fatalf("form revision = %q, %v", shown.Revision, err)
	}
	if code, b := cReq(t, clients["imke"], ts, "POST", fmt.Sprintf("/api/v1/tasks/%d/complete", key),
		`{"formRevision":"`+shown.Revision+`","variables":{"genehmigt":true,"kommentar":"passt"}}`); code != http.StatusOK {
		t.Fatalf("imke complete: %d (%s)", code, b)
	}
	// The form changes afterwards; the answer is still shown in the one it was given in.
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/forms",
		`{"id":"freigabe-form","name":"Freigabe","schema":`+freigabeFormV2+`}`); code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("change form: %d (%s)", code, b)
	}

	// The list.
	list := func(who string) []string {
		t.Helper()
		code, body := cReq(t, clients[who], ts, "GET", "/api/v1/tasks/completed", "")
		if code != http.StatusOK {
			t.Fatalf("%s list completed: %d (%s)", who, code, body)
		}
		var page struct {
			Items []struct {
				Key  string `json:"key"`
				Name string `json:"name"`
			} `json:"items"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			t.Fatalf("decode list: %v (%s)", err, body)
		}
		var keys []string
		for _, it := range page.Items {
			keys = append(keys, it.Key+":"+it.Name)
		}
		return keys
	}
	want := fmt.Sprintf("%d:Freigeben", key)
	if got := list("imke"); len(got) != 1 || got[0] != want {
		t.Fatalf("imke's completed tasks = %v, want [%s]", got, want)
	}
	// Narrowed by process and by period; a process or a period without it lists nothing.
	for query, n := range map[string]int{
		"?process=freigabe&from=2000-01-01&to=2200-01-01": 1,
		"?process=other":           0,
		"?to=2000-01-01T00:00:00Z": 0,
	} {
		code, body := cReq(t, clients["imke"], ts, "GET", "/api/v1/tasks/completed"+query, "")
		var page struct {
			Items []json.RawMessage `json:"items"`
		}
		if code != http.StatusOK || json.Unmarshal(body, &page) != nil || len(page.Items) != n {
			t.Errorf("%s = %d, %d items (%s); want %d", query, code, len(page.Items), body, n)
		}
	}
	if code, _ := cReq(t, clients["imke"], ts, "GET", "/api/v1/tasks/completed/x", ""); code != http.StatusBadRequest {
		t.Errorf("a key that is no number = %d, want 400", code)
	}
	for _, who := range []string{"mallory", "otto"} {
		if got := list(who); len(got) != 0 {
			t.Errorf("%s lists %v, completed nothing", who, got)
		}
	}

	// The view.
	path := fmt.Sprintf("/api/v1/tasks/completed/%d", key)
	code, body = cReq(t, clients["imke"], ts, "GET", path, "")
	if code != http.StatusOK {
		t.Fatalf("imke view: %d (%s)", code, body)
	}
	var v completedView
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("decode view: %v (%s)", err, body)
	}
	if v.Task.CompletedBy != ids["imke"] || v.Task.ProcessID != "freigabe" || v.Task.FormID != "freigabe-form" || v.Task.FormRevision != shown.Revision {
		t.Errorf("task = %+v", v.Task)
	}
	if v.Form == nil || v.Form.Revision != shown.Revision || !strings.Contains(string(v.Form.Schema), `"kommentar"`) || strings.Contains(string(v.Form.Schema), `"neu"`) {
		t.Fatalf("form = %+v, want the revision answered through", v.Form)
	}
	if v.Values["genehmigt"] != true || v.Values["kommentar"] != "passt" || v.Values["betrag"] != float64(100) {
		t.Errorf("values = %v", v.Values)
	}
	for _, hidden := range []string{"geheim", "completedBy"} {
		if _, ok := v.Values[hidden]; ok {
			t.Errorf("values show %q, which the form does not bind", hidden)
		}
	}
	if !slices.Equal(v.Answered, []string{"genehmigt", "kommentar"}) {
		t.Errorf("answered = %v, want the two fields the completion wrote", v.Answered)
	}

	// Nobody else reads it, and the open task's route is unchanged.
	for _, c := range []*http.Client{clients["mallory"], clients["otto"], admin} {
		if code, _ := cReq(t, c, ts, "GET", path, ""); code != http.StatusNotFound {
			t.Errorf("another account reads the completed task: %d", code)
		}
	}
	if code, _ := cReq(t, clients["imke"], ts, "GET", fmt.Sprintf("/api/v1/tasks/%d", key), ""); code != http.StatusNotFound {
		t.Errorf("GET /api/v1/tasks/{key} for a completed task = %d, want 404 as before", code)
	}

	// Deleting the form leaves the answer, without a form.
	if code, b := cReq(t, admin, ts, "DELETE", "/api/v1/forms/freigabe-form", ""); code != http.StatusNoContent && code != http.StatusOK {
		t.Fatalf("delete form: %d (%s)", code, b)
	}
	code, body = cReq(t, clients["imke"], ts, "GET", path, "")
	if code != http.StatusOK {
		t.Fatalf("view after delete: %d (%s)", code, body)
	}
	v = completedView{}
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("decode view: %v", err)
	}
	if v.Form != nil || !v.FormMissing {
		t.Errorf("after the form was deleted: form = %v, formMissing = %v", v.Form, v.FormMissing)
	}
	if _, ok := v.Values["betrag"]; ok || v.Values["kommentar"] != "passt" {
		t.Errorf("without its form the view shows the answer only: %v", v.Values)
	}
}

// A page of the list continues after its cursor and states when it is the last.
func TestTheCompletedListPages(t *testing.T) {
	ts, admin, _, clients := intmgrServer(t)
	for i := 0; i < 3; i++ {
		startTask(t, ts, admin, groupTaskBPMN)
	}
	for k := range taskKeysSeenBy(t, ts, clients["imke"], "") {
		if code, b := cReq(t, clients["imke"], ts, "POST", fmt.Sprintf("/api/v1/tasks/%d/complete", k), `{"variables":{"genehmigt":true}}`); code != http.StatusOK {
			t.Fatalf("complete: %d (%s)", code, b)
		}
	}
	seen := map[string]bool{}
	cursor := ""
	for pages := 0; pages < 5; pages++ {
		code, body := cReq(t, clients["imke"], ts, "GET", "/api/v1/tasks/completed?limit=2"+cursor, "")
		if code != http.StatusOK {
			t.Fatalf("list: %d (%s)", code, body)
		}
		var page struct {
			Items []struct {
				Key string `json:"key"`
			} `json:"items"`
			NextCursor string `json:"nextCursor"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			t.Fatal(err)
		}
		for _, it := range page.Items {
			if seen[it.Key] {
				t.Fatalf("task %s listed twice", it.Key)
			}
			seen[it.Key] = true
		}
		if page.NextCursor == "" {
			break
		}
		cursor = "&cursor=" + page.NextCursor
	}
	if len(seen) != 3 {
		t.Fatalf("paged through %d tasks, want 3", len(seen))
	}
	for _, bad := range []string{"?limit=0", "?limit=201", "?cursor=x", "?from=gestern", "?to=2999-01-01", "?to=gestern"} {
		if code, _ := cReq(t, clients["imke"], ts, "GET", "/api/v1/tasks/completed"+bad, ""); code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", bad, code)
		}
	}
}

// twoStepBPMN asks a first task, then parks on a second, so the instance lives on after
// the first was answered and its variables can change after it.
const twoStepBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="zweistufig" isExecutable="true">
    <startEvent id="start"/>
    <userTask id="first" name="Erfassen">
      <extensionElements>
        <zeebe:assignmentDefinition candidateGroups="intmgr"/>
        <zeebe:formDefinition formId="erfassen-form"/>
      </extensionElements>
    </userTask>
    <userTask id="second" name="Prüfen"/>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="first"/>
    <sequenceFlow id="f2" sourceRef="first" targetRef="second"/>
    <sequenceFlow id="f3" sourceRef="second" targetRef="end"/>
  </process>
</definitions>`

// A completed task shows its variables as they stood once it was answered, not as a
// later step or an operator changed them (ADR-0554 §3); a key nobody completed is 404.
func TestACompletedTaskShowsTheValuesAtItsAnswer(t *testing.T) {
	ts, admin, _, clients := intmgrServer(t)
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/forms",
		`{"id":"erfassen-form","name":"Erfassen","schema":{"components":[{"type":"textfield","key":"betrag","label":"Betrag"}]}}`); code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("save form: %d (%s)", code, b)
	}
	startTask(t, ts, admin, twoStepBPMN)
	var key uint64
	for k := range taskKeysSeenBy(t, ts, clients["imke"], "") {
		key = k
	}
	code, body := cReq(t, admin, ts, "GET", fmt.Sprintf("/api/v1/tasks/%d", key), "")
	var tk struct {
		ProcessInstanceKey uint64 `json:"processInstanceKey"`
	}
	if code != http.StatusOK || json.Unmarshal(body, &tk) != nil {
		t.Fatalf("get task: %d (%s)", code, body)
	}
	if code, b := cReq(t, clients["imke"], ts, "POST", fmt.Sprintf("/api/v1/tasks/%d/complete", key), `{"variables":{"betrag":"100"}}`); code != http.StatusOK {
		t.Fatalf("complete: %d (%s)", code, b)
	}
	if code, b := cReq(t, admin, ts, "POST", fmt.Sprintf("/api/v1/instances/%d/variables", tk.ProcessInstanceKey), `{"variables":{"betrag":"999"}}`); code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("operator change: %d (%s)", code, b)
	}
	code, body = cReq(t, clients["imke"], ts, "GET", fmt.Sprintf("/api/v1/tasks/completed/%d", key), "")
	var v completedView
	if code != http.StatusOK || json.Unmarshal(body, &v) != nil {
		t.Fatalf("view: %d (%s)", code, body)
	}
	if v.Values["betrag"] != "100" {
		t.Errorf("betrag = %v, want the value at the answer, 100", v.Values["betrag"])
	}
	if code, _ := cReq(t, clients["imke"], ts, "GET", "/api/v1/tasks/completed/987654321", ""); code != http.StatusNotFound {
		t.Errorf("a key nobody completed = %d, want 404", code)
	}
}
