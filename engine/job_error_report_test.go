package engine_test

import (
	"testing"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// A worker's message rides with its error. Nothing reads it while a handler catches
// the error, but when nothing does it is what the operator reads on the incident,
// after the code.
func TestAnUncaughtErrorsIncidentCarriesTheWorkersMessage(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, jobType := uncaughtWorkerErrorProcess(t, 84)
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	p.ThrowJobErrorWithMessage(singleActivatableJob(t, h.store, jobType), "CARD_DECLINED", "issuer said no")
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (throw): %v", err)
	}
	if got := onlyIncident(t, h.store).Message; got != "unhandled error 'CARD_DECLINED': issuer said no" {
		t.Errorf("incident message = %q, want the code and the worker's message", got)
	}
	if n := len(operatorActions(t, h.store, model.NewKey(1, 1))); n != 0 {
		t.Errorf("a worker's throw left %d operator actions, want none: it is the protocol, not an intervention", n)
	}
}

// An operator throwing the error by hand forces the error flow the engine did not take
// on its own, so it is recorded like a completion by hand (ADR-0159): who, why, and the
// code — and the throw itself runs exactly as a worker's does. The record is a persisted
// fact, so a replay rebuilds it (I4/I6).
func TestAThrowByHandIsRecordedWithWhoWhyAndWhichCode(t *testing.T) {
	dir := t.TempDir()
	h := openHarness(t, dir)
	cp, jobType, _, recEnd := workerErrorProcess(t, 85, "WITHDRAWN")
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	job := singleActivatableJob(t, h.store, jobType)
	p.ThrowJobErrorManually(job, "WITHDRAWN", "", "alice", "the customer withdrew by phone")
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (throw by hand): %v", err)
	}
	if v := elementVisits(t, h.store, cp.Key)[recEnd]; v != 1 {
		t.Errorf("recovery end visits = %d, want 1 (the boundary caught the thrown error)", v)
	}

	piKey := model.NewKey(1, 1)
	want := func(s *state.Store, what string) {
		t.Helper()
		got := operatorActions(t, s, piKey)
		if len(got) != 1 {
			t.Fatalf("%s: operator actions = %d, want 1", what, len(got))
		}
		a := got[0]
		if a.Kind != model.OperatorActionThrowJobError || a.Actor != "alice" ||
			a.Reason != "the customer withdrew by phone" || a.ErrorCode != "WITHDRAWN" ||
			a.JobKey != job || a.ProcessInstanceKey != piKey || a.ElementInstanceKey == 0 {
			t.Errorf("%s: operator action = %+v, want alice's throw of WITHDRAWN on job %d", what, a, job)
		}
	}
	want(h.store, "live")
	h.close(t)

	_, replayedStore := replayed(t, dir, cp)
	want(replayedStore, "after replay")
}
