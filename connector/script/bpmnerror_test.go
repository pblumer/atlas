package script_test

import (
	"context"
	"errors"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/script"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// scriptBoundaryProcess: Start → PowerShell script task [error boundary catching code]
// → End, the boundary routed to an end of its own.
func scriptBoundaryProcess(t *testing.T, code string) (*compiler.CompiledProcess, int32) {
	t.Helper()
	b := compiler.NewBuilder(scriptDefKey, "resource-accounts", 1)
	start := b.AddStartEvent()
	sj := b.AddScriptJobTask(compiler.PwshJobType, "powershell", `Write-Error -ErrorId 'UPN_EXISTS' -Message 'taken'`, "ResourceAccounts", 3)
	boundary := b.AddBoundaryErrorEvent(sj, code)
	done, recovered := b.AddEndEvent(), b.AddEndEvent()
	b.Connect(start, sj)
	b.Connect(sj, done)
	b.Connect(boundary, recovered)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, cp.ScriptJobTask(cp.Node(sj).Detail).JobType
}

func driveScript(t *testing.T, cp *compiler.CompiledProcess, jobType int32, fx *fakeExec) *state.Store {
	t.Helper()
	store, log := openStore(t)
	p := engine.New(1, log, store, &fixedClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	runner := job.NewRunner(store, p)
	runner.HandleWithOutput(jobType, func(rd state.Reader) job.OutputHandler { return script.Handler(store, lookupOf(cp), fx) })
	p.CreateInstance(cp.Key)
	if err := runner.Drive(); err != nil {
		t.Fatalf("Drive: %v", err)
	}
	return store
}

func incidentCount(t *testing.T, s *state.Store) int {
	t.Helper()
	n := 0
	if err := s.Incidents(func(uint64, *model.IncidentValue) error { n++; return nil }); err != nil {
		t.Fatalf("Incidents: %v", err)
	}
	return n
}

// TestAScriptsCaughtCodeTakesTheErrorBoundary is the case this exists for: a script
// fails with a code an error boundary on its task catches, and the instance takes the
// boundary's path — once, with no retry spent and no incident raised
// (ADR-0461).
func TestAScriptsCaughtCodeTakesTheErrorBoundary(t *testing.T) {
	cp, jobType := scriptBoundaryProcess(t, "UPN_EXISTS")
	fx := &fakeExec{err: &script.CodedError{Code: "UPN_EXISTS", Message: "taken", Err: errors.New("powershell: exit status 1")}}
	store := driveScript(t, cp, jobType, fx)
	if fx.calls != 1 {
		t.Errorf("script ran %d times, want once: a BPMN error spends no retry", fx.calls)
	}
	if pi, ei := active(t, store); pi != 0 || ei != 0 {
		t.Errorf("after the throw: process=%d element=%d, want 0 and 0 (the boundary's end)", pi, ei)
	}
	if n := incidentCount(t, store); n != 0 {
		t.Errorf("incidents = %d, want 0", n)
	}
}

// TestAScriptsUncaughtCodeIsAFailure: a code the process does not catch is an ordinary
// failure. An identifier on an error is not a statement about the model, and turning
// every one of them into a throw would skip the retries a transient fault is owed.
func TestAScriptsUncaughtCodeIsAFailure(t *testing.T) {
	cp, jobType := scriptBoundaryProcess(t, "UPN_EXISTS")
	fx := &fakeExec{err: &script.CodedError{Code: "CommandNotFoundException", Message: "no such command", Err: errors.New("powershell: exit status 1")}}
	store := driveScript(t, cp, jobType, fx)
	if fx.calls != 3 {
		t.Errorf("script ran %d times, want 3: every attempt the task grants", fx.calls)
	}
	if pi, _ := active(t, store); pi != 1 {
		t.Errorf("active instances = %d, want 1 (parked on the incident)", pi)
	}
	if n := incidentCount(t, store); n != 1 {
		t.Errorf("incidents = %d, want 1", n)
	}
}

// TestRunThrowsOnlyACaughtCode is the decision on its own, shared by the in-process
// handler and an out-of-process worker: [script.Run] turns a coded failure into a
// [job.BPMNError] when the job lists the code among the ones its process catches, and
// leaves every other failure as it was, message and all.
func TestRunThrowsOnlyACaughtCode(t *testing.T) {
	coded := &script.CodedError{Code: "UPN_EXISTS", Message: "taken", Err: errors.New("exit status 1: taken")}
	j := script.Job{Source: "x", ErrorCodes: []string{"LICENSE_MISSING", "UPN_EXISTS"}}

	_, err := script.Run(context.Background(), j, &fakeExec{err: coded})
	var be *job.BPMNError
	if !errors.As(err, &be) || be.Code != "UPN_EXISTS" || be.Message != "taken" {
		t.Fatalf("Run error = %v, want BPMN error UPN_EXISTS: taken", err)
	}

	j.ErrorCodes = []string{"LICENSE_MISSING"}
	_, err = script.Run(context.Background(), j, &fakeExec{err: coded})
	if errors.As(err, &be) {
		t.Fatalf("Run error = %v, want a plain failure for a code the process does not catch", err)
	}
	if err == nil || err.Error() != "script: exit status 1: taken" {
		t.Errorf("Run error = %v, want the interpreter's own message", err)
	}

	_, err = script.Run(context.Background(), script.Job{Source: "x", ErrorCodes: []string{"UPN_EXISTS"}}, &fakeExec{err: errors.New("exit status 1")})
	if err == nil || errors.As(err, &be) {
		t.Errorf("Run error = %v, want a plain failure for an error without a code", err)
	}
}
