package engine_test

import (
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// BenchmarkInstanceLifecycle runs a full Start → ServiceTask → End instance
// (create, run to the waiting job, complete it, finish) and reports allocations
// per instance. It is the I1 harness: watch allocs/op as the engine evolves.
func BenchmarkInstanceLifecycle(b *testing.B) {
	dir := b.TempDir()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		b.Fatalf("wal.Open: %v", err)
	}
	defer log.Close()
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		b.Fatalf("state.Open: %v", err)
	}
	defer store.Close()

	cp, jobType := linearProcess(b)
	p := engine.New(1, log, store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		b.Fatalf("Recover: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		p.CreateInstance(cp.Key)
		if err := p.RunUntilIdle(); err != nil {
			b.Fatalf("RunUntilIdle: %v", err)
		}
		var jobKey uint64
		if err := store.ActivatableJobs(jobType, func(k uint64) error { jobKey = k; return nil }); err != nil {
			b.Fatalf("ActivatableJobs: %v", err)
		}
		p.CompleteJob(jobKey)
		if err := p.RunUntilIdle(); err != nil {
			b.Fatalf("RunUntilIdle: %v", err)
		}
	}
}
