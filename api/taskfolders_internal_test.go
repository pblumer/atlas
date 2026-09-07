package api

import (
	"net/http/httptest"
	"testing"

	"github.com/pblumer/atlas/api/taskfolder"
)

// The projection between a task row and what a folder rule sees. It is small and
// pure, and it is the one place a mistake would be invisible: a rule would simply
// stop matching, and the folder would look like it was never right.

// TestTaskTitleOf covers the fallback a task authored without a name relies on.
// Filtering on "Aufgabe" has to find such a task by the id it does have, rather
// than treating it as a task with no name at all.
func TestTaskTitleOf(t *testing.T) {
	if got := taskTitleOf(taskResp{Name: "Anfrage sichten", ElementID: "sichten"}); got != "Anfrage sichten" {
		t.Errorf("taskTitleOf = %q, want the element's name", got)
	}
	if got := taskTitleOf(taskResp{ElementID: "sichten"}); got != "sichten" {
		t.Errorf("taskTitleOf(unnamed) = %q, want the BPMN id", got)
	}
	if got := taskTitleOf(taskResp{}); got != "" {
		t.Errorf("taskTitleOf(nothing) = %q, want empty", got)
	}
}

// TestTaskPriorityOf covers the model default. A row that carries no priority
// must land in a "priority is at least 50" folder, because 50 is what the model
// means by saying nothing (ADR-0091) — and the inbox already reads it that way.
func TestTaskPriorityOf(t *testing.T) {
	if got := taskPriorityOf(taskResp{Priority: 70}); got != 70 {
		t.Errorf("taskPriorityOf(70) = %d", got)
	}
	if got := taskPriorityOf(taskResp{}); got != 50 {
		t.Errorf("taskPriorityOf(absent) = %d, want the model default 50", got)
	}
	if got := taskPriorityOf(taskResp{Priority: -3}); got != 50 {
		t.Errorf("taskPriorityOf(negative) = %d, want the model default 50", got)
	}
}

// TestFolderTaskProjection pins what a rule can see, field by field. Every one of
// these is a condition somebody can build, so a dropped field is a folder that
// silently selects nothing.
func TestFolderTaskProjection(t *testing.T) {
	row := taskResp{
		ProcessID: "kunden-anfrage", Name: "Anfrage sichten", ElementID: "sichten",
		Assignee: "patrick", CandidateGroups: "kundenservice",
		Lane: "Team Lead", LanePath: []string{"Kundenservice", "Team Lead"},
		Priority: 70, DueDate: 1_700_000_000_000, FormID: "anfrage-form",
	}
	got := folderTask(row, "Kundenanfrage")
	want := taskfolder.Task{
		ProcessID: "kunden-anfrage", ProcessName: "Kundenanfrage",
		TaskName: "Anfrage sichten", ElementID: "sichten",
		Assignee: "patrick", CandidateGroups: "kundenservice",
		Lane: "Team Lead", LanePath: []string{"Kundenservice", "Team Lead"},
		Priority: 70, DueDate: 1_700_000_000_000, HasForm: true,
	}
	if got.ProcessID != want.ProcessID || got.ProcessName != want.ProcessName ||
		got.TaskName != want.TaskName || got.ElementID != want.ElementID ||
		got.Assignee != want.Assignee || got.CandidateGroups != want.CandidateGroups ||
		got.Lane != want.Lane || got.Priority != want.Priority ||
		got.DueDate != want.DueDate || !got.HasForm {
		t.Errorf("folderTask = %+v\n            want %+v", got, want)
	}
	if len(got.LanePath) != 2 || got.LanePath[0] != "Kundenservice" {
		t.Errorf("lane path = %v, want the outermost-to-leaf path", got.LanePath)
	}
	// A task with no bound form is the "Formular · nicht vorhanden" case.
	if folderTask(taskResp{}, "").HasForm {
		t.Error("a task with no form id reports that it has a form")
	}
}

// TestDefsMetaMisses covers the lookup answering for a definition that is not in
// the snapshot it was given — a deployment removed between the snapshot and the
// scan. It must miss cleanly rather than hand back a zero deployment that would
// enrich every task with an empty process id.
func TestDefsMetaMisses(t *testing.T) {
	lookup := defsMeta(defIndex{})
	if _, _, _, ok := lookup(42); ok {
		t.Error("an unknown definition key resolved")
	}
	lookup = defsMeta(defIndex{7: {ProcessID: "kunden-anfrage", Name: "Kundenanfrage"}})
	id, name, cp, ok := lookup(7)
	if !ok || id != "kunden-anfrage" || name != "Kundenanfrage" || cp != nil {
		t.Errorf("lookup(7) = %q %q %v %v", id, name, cp, ok)
	}
}

// TestFolderQuery covers the selector's parsing: whitespace is not a folder, so a
// blank one falls through to the unfiltered listing rather than 404ing.
func TestFolderQuery(t *testing.T) {
	cases := map[string]string{
		"/api/v1/tasks":               "",
		"/api/v1/tasks?folder=":       "",
		"/api/v1/tasks?folder=%20%20": "",
		"/api/v1/tasks?folder=abc123": "abc123",
		"/api/v1/tasks?folder=+abc+":  "abc",
	}
	for path, want := range cases {
		if got := folderQuery(httptest.NewRequest("GET", path, nil)); got != want {
			t.Errorf("folderQuery(%q) = %q, want %q", path, got, want)
		}
	}
}

// TestDeploymentMetaMisses covers the loop-side lookup's miss, which is what
// keeps a task whose definition has been removed from being enriched against a
// zero-valued deployment.
func TestDeploymentMetaMisses(t *testing.T) {
	s := &Server{deployments: map[uint64]*deployment{}}
	if _, _, _, ok := s.deploymentMeta(1); ok {
		t.Error("an unknown definition key resolved on the loop side")
	}
	s.deployments[1] = &deployment{ProcessID: "p", Name: "P"}
	if id, name, _, ok := s.deploymentMeta(1); !ok || id != "p" || name != "P" {
		t.Errorf("deploymentMeta(1) = %q %q %v", id, name, ok)
	}
}
