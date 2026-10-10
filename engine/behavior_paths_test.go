package engine_test

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
)

// The two assignment refusals TestAnUnresolvableAssigneeParksInsteadOfOpeningTheTask
// and TestAnAssignmentThatIsNotANameIsRefused leave out: a name that is blank, and a
// candidate-group expression that is not a name.

// parkedOnAssignment runs one instance of an assignment process with the given
// variables and returns the incident it parked with, after checking no job opened.
func parkedOnAssignment(t *testing.T, assignee, groups string, vars ...model.VariableValue) model.IncidentValue {
	t.Helper()
	h := openHarness(t, t.TempDir())
	t.Cleanup(func() { h.close(t) })

	cp, jobType, _ := assignmentProcess(t, assignee, groups)
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key, vars...)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if n := len(activatableJobs(t, h.store, jobType)); n != 0 {
		t.Fatalf("%d job(s) opened for a task whose assignment could not be resolved", n)
	}
	_, inc := oneIncident(t, h)
	return inc
}

// TestABlankAssigneeParks: a variable that holds only spaces names nobody, and a
// task addressed to nobody is work anybody may complete — so it parks like an
// absent one does, saying why.
func TestABlankAssigneeParks(t *testing.T) {
	inc := parkedOnAssignment(t, "=approvalRef", "",
		model.VariableValue{Name: "approvalRef", Kind: model.VarString, Text: "   "})
	if !strings.Contains(inc.Message, "addressed to nobody") {
		t.Errorf("incident = %q, want it to say the task would be addressed to nobody", inc.Message)
	}
}

// TestCandidateGroupsThatAreNotANameAreRefused: the groups decide who may claim the
// task, so a number stringified into one is refused like a number assignee is, and
// the incident says which half of the assignment it was.
func TestCandidateGroupsThatAreNotANameAreRefused(t *testing.T) {
	inc := parkedOnAssignment(t, "alice", "=kostenstelle",
		model.VariableValue{Name: "kostenstelle", Kind: model.VarNumber, Text: "4711"})
	if !strings.Contains(inc.Message, "candidateGroups") {
		t.Errorf("incident = %q, want it to name candidateGroups", inc.Message)
	}
}
