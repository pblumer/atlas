package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/pblumer/atlas/eventcatalog"
)

// taskFeedBPMN parks on one user task offered to a group, due in an hour.
const taskFeedBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" targetNamespace="http://atlas.test">
  <process id="urlaub" isExecutable="true">
    <startEvent id="start"/>
    <userTask id="genehmigen" name="Urlaub genehmigen">
      <extensionElements>
        <zeebe:assignmentDefinition candidateGroups="hr"/>
        <zeebe:taskSchedule dueDate="PT1H"/>
      </extensionElements>
    </userTask>
    <endEvent id="ende"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="genehmigen"/>
    <sequenceFlow id="f2" sourceRef="genehmigen" targetRef="ende"/>
  </process>
</definitions>`

// openTaskOnFeed deploys taskFeedBPMN, starts it and answers the definition and the
// task it parks on.
func openTaskOnFeed(t *testing.T, srv *Server) (defKey, taskKey uint64) {
	t.Helper()
	code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments", taskFeedBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: %d %s", code, raw)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(raw, &dep); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	code, raw = serveInternal(t, srv, http.MethodPost, "/api/v1/processes/"+strconv.FormatUint(dep.Key, 10)+"/instances", `{}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("start: %d %s", code, raw)
	}
	code, raw = serveInternal(t, srv, http.MethodGet, "/api/v1/tasks", "", "")
	if code != http.StatusOK {
		t.Fatalf("tasks: %d %s", code, raw)
	}
	var page struct {
		Items []struct {
			Key uint64 `json:"key"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &page); err != nil || len(page.Items) != 1 {
		t.Fatalf("tasks = %s, want one", raw)
	}
	return dep.Key, page.Items[0].Key
}

// TestAUserTasksLifeLeavesOnTheFeedAsFactsOfTheEngine
// (ADR-0510 §1): a task opened, taken on, given back and
// completed is atlas.task.created, .assigned, .unassigned and .completed on the feed,
// from the engine's source, under the subject tasks/{key}, naming the task, its
// instance, its element and process, the groups it is offered to and when it is due —
// and whether somebody holds it, never who. A reader narrowed to catalogues is given
// none of them.
func TestAUserTasksLifeLeavesOnTheFeedAsFactsOfTheEngine(t *testing.T) {
	srv := newServerWithOptions(t, WithExternalURL("https://atlas.example"))
	defKey, taskKey := openTaskOnFeed(t, srv)
	for _, step := range []struct{ path, body string }{
		{fmt.Sprintf("/api/v1/tasks/%d/claim", taskKey), `{"assignee":"anna.muster"}`},
		{fmt.Sprintf("/api/v1/tasks/%d/unclaim", taskKey), ``},
		{fmt.Sprintf("/api/v1/tasks/%d/complete", taskKey), `{"variables":{"genehmigt":true}}`},
	} {
		if code, raw := serveInternal(t, srv, http.MethodPost, step.path, step.body, "application/json"); code != http.StatusOK {
			t.Fatalf("POST %s = %d %s", step.path, code, raw)
		}
	}

	code, page, _ := feedPage(t, srv, "")
	if code != http.StatusOK {
		t.Fatalf("feed = %d", code)
	}
	var types []string
	for _, ev := range page.Events {
		types = append(types, ev.Type)
	}
	want := []string{eventcatalog.TaskCreated, eventcatalog.TaskAssigned, eventcatalog.TaskUnassigned, eventcatalog.TaskCompleted}
	if strings.Join(types, " ") != strings.Join(want, " ") {
		t.Fatalf("the feed's types = %v, want %v", types, want)
	}
	for i, ev := range page.Events {
		if ev.Source != "https://atlas.example/engine" || ev.Subject != "tasks/"+strconv.FormatUint(taskKey, 10) {
			t.Errorf("%s's envelope = source %s subject %s", ev.Type, ev.Source, ev.Subject)
		}
		data := ev.Data.(map[string]any)
		for name, want := range map[string]any{
			"taskKey": float64(taskKey), "processDefKey": float64(defKey), "processId": "urlaub", "version": float64(1),
			"elementId": "genehmigen", "name": "Urlaub genehmigen", "candidateGroups": "hr",
		} {
			if data[name] != want {
				t.Errorf("%s data %s = %v, want %v", ev.Type, name, data[name], want)
			}
		}
		if data["dueDate"] == nil || data["createdAt"] == nil || data["processInstanceKey"] == nil || data["elementInstanceKey"] == nil {
			t.Errorf("%s data lacks a key or a time: %v", ev.Type, data)
		}
		if got, want := data["assigned"], i == 1; got != want {
			t.Errorf("%s assigned = %v, want %v", ev.Type, got, want)
		}
	}
	raw, _ := json.Marshal(page)
	if strings.Contains(string(raw), "anna.muster") || strings.Contains(string(raw), "genehmigt") {
		t.Fatalf("the feed carries who holds the task or what was submitted: %s", raw)
	}

	view, nodeID, part, defs, err := srv.feedSnapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	narrowed, err := srv.readFeedPage(view, defs, part, nodeID, 0, 100, map[string]bool{"cat-it": true})
	view.Close()
	if err != nil || len(narrowed.Events) != 0 || narrowed.Next != page.Next {
		t.Fatalf("a narrowed reader = %+v %v, want nothing and the cursor at %s", narrowed, err, page.Next)
	}
}

// TestACancelledInstancesTaskLeavesAsCancelled: a task whose instance is cancelled is
// atlas.task.cancelled — what a copy elsewhere closes its card by — and its process
// is still named while the definition is deployed.
func TestACancelledInstancesTaskLeavesAsCancelled(t *testing.T) {
	srv := newServerWithOptions(t)
	_, taskKey := openTaskOnFeed(t, srv)
	code, raw := serveInternal(t, srv, http.MethodGet, fmt.Sprintf("/api/v1/tasks/%d", taskKey), "", "")
	var task struct {
		ProcessInstanceKey uint64 `json:"processInstanceKey"`
	}
	if code != http.StatusOK || json.Unmarshal(raw, &task) != nil {
		t.Fatalf("task: %d %s", code, raw)
	}
	if code, raw := serveInternal(t, srv, http.MethodDelete, fmt.Sprintf("/api/v1/instances/%d", task.ProcessInstanceKey), "", ""); code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("cancel: %d %s", code, raw)
	}
	_, page, _ := feedPage(t, srv, "")
	cancelled := eventsOfType(page, eventcatalog.TaskCancelled)
	if len(cancelled) != 1 || len(eventsOfType(page, eventcatalog.TaskCompleted)) != 0 {
		t.Fatalf("the feed = %+v, want the task cancelled and not completed", page.Events)
	}
	if d := cancelled[0].Data.(map[string]any); d["taskKey"] != float64(taskKey) || d["elementId"] != "genehmigen" {
		t.Errorf("the cancellation's data = %v", d)
	}
}
