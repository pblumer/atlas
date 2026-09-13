package engine_test

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
)

// A user task's assignment may be an expression, and the value it evaluates to is
// frozen into the job like the due date beside it
// (ADR-0318).
//
// Before this, an `assignee="=approvalRef"` was stored verbatim: the task was
// assigned to the eight characters `=approvalRef` and no person held it. Three of
// the portal's own approval models are written that way, so no approval Atlas
// shipped could reach an approver at all.

// assignmentProcess builds a one-task process whose assignment attributes are
// whatever the caller writes, compiled the way the parser compiles them.
func assignmentProcess(t testing.TB, assignee, groups string) (*compiler.CompiledProcess, int32, int32) {
	t.Helper()
	a, err := compiler.Assign("decide", "assignee", assignee)
	if err != nil {
		t.Fatalf("assignee %q: %v", assignee, err)
	}
	g, err := compiler.Assign("decide", "candidateGroups", groups)
	if err != nil {
		t.Fatalf("candidateGroups %q: %v", groups, err)
	}
	b := compiler.NewBuilder(defKey, "assignment", 1)
	start := b.AddStartEvent()
	task := b.AddUserTask("Decide", a, g, "", 50, 0, 3)
	end := b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, cp.UserTask(cp.Node(task).Detail).JobType, task
}

func TestAnAssignmentExpressionNamesThePersonItEvaluatesTo(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	cp, jobType, _ := assignmentProcess(t, "=approvalRef", "=freigabeGruppe")
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key,
		model.VariableValue{Name: "approvalRef", Kind: model.VarString, Text: "alice"},
		model.VariableValue{Name: "freigabeGruppe", Kind: model.VarString, Text: "einkauf"})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	jobKey := singleActivatableJob(t, h.store, jobType)
	jv, ok, err := h.store.GetJob(jobKey)
	if err != nil || !ok {
		t.Fatalf("GetJob: ok=%v err=%v", ok, err)
	}
	if jv.Assignee != "alice" {
		t.Errorf("assignee = %q, want alice — the expression was not evaluated", jv.Assignee)
	}
	if jv.CandidateGroups != "einkauf" {
		t.Errorf("candidateGroups = %q, want einkauf", jv.CandidateGroups)
	}
}

// TestALiteralAssignmentStillWorks: the vocabulary's rule is that a leading "="
// makes an expression and everything else is a name. The overwhelming majority of
// models are the second kind and must be untouched by this.
func TestALiteralAssignmentStillWorks(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	cp, jobType, _ := assignmentProcess(t, "editor", "reviewers")
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	jv, ok, err := h.store.GetJob(singleActivatableJob(t, h.store, jobType))
	if err != nil || !ok {
		t.Fatalf("GetJob: ok=%v err=%v", ok, err)
	}
	if jv.Assignee != "editor" || jv.CandidateGroups != "reviewers" {
		t.Errorf("assignment = %q / %q, want editor / reviewers", jv.Assignee, jv.CandidateGroups)
	}
}

// TestAnUnresolvableAssigneeParksInsteadOfOpeningTheTask is the case that decides
// how safe this is.
//
// An expression over a variable that is not there evaluates to null. Writing that
// through as an empty assignee would leave the task addressed to nobody — which,
// under the rule that decides who may act on a task, is *open work anybody may
// complete*. An approval whose approver could not be resolved would become
// everybody's approval. So it parks with an incident and creates no job at all.
func TestAnUnresolvableAssigneeParksInsteadOfOpeningTheTask(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	cp, jobType, _ := assignmentProcess(t, "=approvalRef", "")
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key) // no approvalRef
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	if n := len(activatableJobs(t, h.store, jobType)); n != 0 {
		t.Fatalf("%d job(s) created for a task nobody could be addressed with; "+
			"an unaddressed task is work anybody may complete", n)
	}
	elKey, inc := oneIncident(t, h)

	// And resolving it is a genuine retry: with the variable in place the task
	// activates and reaches the person it was always meant for.
	p.SetVariables(inc.ProcessInstanceKey, inc.ProcessInstanceKey, "operator",
		model.VariableValue{Name: "approvalRef", Kind: model.VarString, Text: "alice"})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after fixing the data): %v", err)
	}
	p.ResolveIncident(elKey, 3)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after resolve): %v", err)
	}
	jv, ok, err := h.store.GetJob(singleActivatableJob(t, h.store, jobType))
	if err != nil || !ok {
		t.Fatalf("GetJob after resolve: ok=%v err=%v", ok, err)
	}
	if jv.Assignee != "alice" {
		t.Errorf("assignee after resolve = %q, want alice", jv.Assignee)
	}
}

// TestAnAssignmentThatIsNotANameIsRefused: "who is this for" has one shape. A
// number coerced into an assignee is a task addressed to somebody who does not
// exist, decided silently, at the one gate that says who may act on it.
func TestAnAssignmentThatIsNotANameIsRefused(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	cp, jobType, _ := assignmentProcess(t, "=kostenstelle", "")
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key,
		model.VariableValue{Name: "kostenstelle", Kind: model.VarNumber, Text: "4711"})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if n := len(activatableJobs(t, h.store, jobType)); n != 0 {
		t.Fatalf("%d job(s) created from a number", n)
	}
	if _, inc := oneIncident(t, h); inc.Message == "" {
		t.Error("the incident says nothing about what went wrong")
	}
}

// TestAnEmptyExpressionIsACompileError: a model that writes "=" and nothing else
// said it meant an expression. Deploying it and discovering that at activation
// would be the same defect this record exists to end, one step later.
func TestAnEmptyExpressionIsACompileError(t *testing.T) {
	if _, err := compiler.Assign("decide", "assignee", "= "); err == nil {
		t.Error("an empty FEEL expression compiled")
	}
	if _, err := compiler.Assign("decide", "assignee", "=1 +"); err == nil {
		t.Error("a malformed FEEL expression compiled")
	}
	a, err := compiler.Assign("decide", "assignee", "")
	if err != nil || a.Expr != nil || a.Literal != "" {
		t.Errorf("an absent assignment became %+v (%v); it is not an expression", a, err)
	}
}
