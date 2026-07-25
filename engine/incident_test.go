package engine_test

import (
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// incidents reads every unresolved incident into a map keyed by element instance.
func incidents(t *testing.T, s *state.Store) map[uint64]model.IncidentValue {
	t.Helper()
	out := map[uint64]model.IncidentValue{}
	if err := s.Incidents(func(k uint64, v *model.IncidentValue) error { out[k] = *v; return nil }); err != nil {
		t.Fatalf("Incidents: %v", err)
	}
	return out
}

// startedJob deploys a linear service-task process, starts one instance, and
// returns the processor, its store harness, the job key parked on the task, and
// the job type.
func startedJob(t *testing.T, h *harness) (*engine.Processor, uint64, int32) {
	t.Helper()
	cp, jobType := linearProcess(t)
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.SetJobNotifier(func(int32) {})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	return p, singleActivatableJob(t, h.store, jobType), jobType
}

// TestJobFailWithRetriesRetries proves failing a job with retries left puts it
// back on the activatable index and raises no incident (ADR-0061).
func TestJobFailWithRetriesRetries(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	p, jobKey, jobType := startedJob(t, h)

	p.FailJob(jobKey, 2, "transient")
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if got := activatableJobs(t, h.store, jobType); len(got) != 1 || got[0] != jobKey {
		t.Fatalf("after retryable fail: activatable=%v, want the same job still activatable", got)
	}
	if n := len(incidents(t, h.store)); n != 0 {
		t.Fatalf("a retryable failure raised %d incidents, want 0", n)
	}
}

// TestJobFailExhaustedRaisesIncident proves failing a job with no retries left
// parks it off the activatable index and raises an incident on its element,
// leaving the token in place (ADR-0061).
func TestJobFailExhaustedRaisesIncident(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	p, jobKey, jobType := startedJob(t, h)

	p.FailJob(jobKey, 0, "boom")
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if got := activatableJobs(t, h.store, jobType); len(got) != 0 {
		t.Fatalf("an exhausted job is still activatable: %v", got)
	}
	inc := incidents(t, h.store)
	if len(inc) != 1 {
		t.Fatalf("exhausted failure raised %d incidents, want 1", len(inc))
	}
	for _, v := range inc {
		if v.JobKey != jobKey || v.Message != "boom" || v.RaisedAt == 0 {
			t.Fatalf("incident = %+v, want it to point at the job with the message and a raised-at time", v)
		}
	}
	// The token stays parked on the task: one instance, one element, still there.
	if pi, ei := counts(t, h.store); pi != 1 || ei != 1 {
		t.Fatalf("parked on incident: process=%d element=%d, want 1 and 1", pi, ei)
	}
}

// TestResolveIncidentResumes proves resolving an incident clears it, re-activates
// the job, and a worker can then complete it and finish the instance (ADR-0061).
func TestResolveIncidentResumes(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	p, jobKey, jobType := startedJob(t, h)

	p.FailJob(jobKey, 0, "boom")
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (fail): %v", err)
	}
	var elKey uint64
	for k := range incidents(t, h.store) {
		elKey = k
	}

	p.ResolveIncident(elKey, 3)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (resolve): %v", err)
	}
	if n := len(incidents(t, h.store)); n != 0 {
		t.Fatalf("after resolve: %d incidents remain, want 0", n)
	}
	resumed := singleActivatableJob(t, h.store, jobType)
	if resumed != jobKey {
		t.Fatalf("resumed job = %d, want the same job %d re-activated", resumed, jobKey)
	}

	p.CompleteJob(resumed)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (complete): %v", err)
	}
	if pi := activeProcs(t, h.store); pi != 0 {
		t.Fatalf("after completing the resumed job: active=%d, want 0 (finished)", pi)
	}
}

// TestIncidentRecovers proves a raised incident survives a restart: replaying the
// log into a fresh store restores the incident and the parked (non-activatable)
// job (ADR-0061, invariant I4).
func TestIncidentRecovers(t *testing.T) {
	dir := t.TempDir()
	cp, jobType := linearProcess(t)

	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, &manualClock{})
	p1.SetJobNotifier(func(int32) {})
	p1.Deploy(cp)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(cp.Key)
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	jobKey := singleActivatableJob(t, h1.store, jobType)
	p1.FailJob(jobKey, 0, "boom")
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (fail): %v", err)
	}
	h1.close(t)

	log2, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open 2: %v", err)
	}
	store2, err := state.Open(filepath.Join(dir, "state2"))
	if err != nil {
		t.Fatalf("state.Open 2: %v", err)
	}
	defer func() { _ = store2.Close(); _ = log2.Close() }()
	p2 := engine.New(1, log2, store2, &manualClock{})
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2 (replay): %v", err)
	}
	if n := len(incidents(t, store2)); n != 1 {
		t.Fatalf("after replay: %d incidents, want 1 (restored)", n)
	}
	if got := activatableJobs(t, store2, jobType); len(got) != 0 {
		t.Fatalf("after replay: parked job is activatable: %v", got)
	}
}

// TestFailAndResolveNoOpWhenAbsent proves failing a job that doesn't exist and
// resolving an incident that doesn't exist are both harmless no-ops (ADR-0061).
func TestFailAndResolveNoOpWhenAbsent(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	p := engine.New(1, h.log, h.store, &manualClock{})
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	p.FailJob(model.NewKey(1, 999), 0, "boom") // no such job
	p.ResolveIncident(model.NewKey(1, 888), 1) // no such incident
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if n := len(incidents(t, h.store)); n != 0 {
		t.Fatalf("no-op fail/resolve created %d incidents, want 0", n)
	}
}

// TestResolveIncidentWhoseJobVanished proves resolving an incident whose job has
// since disappeared (e.g. the job was removed by another path while the incident
// lingered) still clears the incident and resurrects no phantom job (ADR-0061).
func TestResolveIncidentWhoseJobVanished(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	p := engine.New(1, h.log, h.store, &manualClock{})
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	// Plant an incident pointing at a job key that has no job behind it.
	elKey := model.NewKey(1, 7)
	tx := h.store.NewTransaction()
	if err := tx.PutIncident(&model.IncidentValue{
		ProcessInstanceKey: model.NewKey(1, 1),
		ElementInstanceKey: elKey,
		JobKey:             model.NewKey(1, 99), // no such job
		Message:            "stale",
	}); err != nil {
		t.Fatalf("PutIncident: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	tx.Close()

	p.ResolveIncident(elKey, 3)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if n := len(incidents(t, h.store)); n != 0 {
		t.Fatalf("after resolving a job-less incident: %d incidents remain, want 0 (cleared)", n)
	}
}

// TestCancelInstanceClearsIncident proves canceling an instance removes the
// incident its element carried, leaving no orphan (ADR-0061).
func TestCancelInstanceClearsIncident(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	p, jobKey, _ := startedJob(t, h)

	p.FailJob(jobKey, 0, "boom")
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (fail): %v", err)
	}
	if len(incidents(t, h.store)) != 1 {
		t.Fatal("expected an incident before cancel")
	}

	p.CancelInstance(singleActiveInstance(t, h.store))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (cancel): %v", err)
	}
	if n := len(incidents(t, h.store)); n != 0 {
		t.Fatalf("cancel left %d orphan incidents, want 0", n)
	}
	if pi := activeProcs(t, h.store); pi != 0 {
		t.Fatalf("after cancel: active=%d, want 0", pi)
	}
}
