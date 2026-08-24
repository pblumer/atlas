package engine_test

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
)

// TestServiceTaskJobCarriesTheResolvedJobType pins the property the engine-wide
// job-type table exists for: once a process has been resolved at deploy time, the
// job a service task creates carries the *engine-wide* index, not the one interned
// locally. Without it two definitions write jobs under the same index for
// different job types, and a worker subscribing by type is handed the wrong work
// (ADR-0007).
func TestServiceTaskJobCarriesTheResolvedJobType(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(7, "orders", 1)
	start := b.AddStartEvent()
	st := b.AddServiceTask("send-email", 3)
	end := b.AddEndEvent()
	b.Connect(start, st)
	b.Connect(st, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	const resolved int32 = 4242
	local := cp.ServiceTask(cp.Node(st).Detail).JobType
	if local == resolved {
		t.Fatal("the local index happens to equal the resolved one; the test cannot tell them apart")
	}
	if err := cp.ResolveJobTypes(func(string) (int32, error) { return resolved, nil }); err != nil {
		t.Fatalf("ResolveJobTypes: %v", err)
	}

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	// The job is on the activatable index under the resolved type, and not under
	// the local one.
	for _, tc := range []struct {
		jobType int32
		want    int
		label   string
	}{
		{resolved, 1, "resolved"},
		{local, 0, "local"},
	} {
		got := 0
		if err := h.store.ActivatableJobs(tc.jobType, func(uint64) error { got++; return nil }); err != nil {
			t.Fatalf("ActivatableJobs(%s): %v", tc.label, err)
		}
		if got != tc.want {
			t.Errorf("%d job(s) activatable under the %s job type %d, want %d", got, tc.label, tc.jobType, tc.want)
		}
	}

	// And the stored job record agrees, so a restart replays the same identity.
	var found bool
	if err := h.store.ActivatableJobs(resolved, func(jobKey uint64) error {
		jv, ok, err := h.store.GetJob(jobKey)
		if err != nil || !ok {
			return err
		}
		found = true
		if jv.JobType != resolved {
			t.Errorf("stored JobValue.JobType = %d, want %d", jv.JobType, resolved)
		}
		return nil
	}); err != nil {
		t.Fatalf("ActivatableJobs: %v", err)
	}
	if !found {
		t.Fatal("no job found under the resolved job type")
	}
}
