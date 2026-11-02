package engine_test

import (
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// BenchmarkMultiInstanceIterations runs instances of a parallel multi-instance
// script task with 100 iterations each and reports time and allocations per
// instance. It is the I1/I4 harness for the multi-instance history projection
// (ADR-0517): watch allocs/op when changing what applyToState records per
// iteration.
func BenchmarkMultiInstanceIterations(b *testing.B) {
	dir := b.TempDir()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		b.Fatal(err)
	}
	defer log.Close()
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()
	bld := compiler.NewBuilder(1, "mi-bench", 1)
	start := bld.AddStartEvent()
	work := bld.AddScriptTask(mustCompile(b, "loopCounter * 2"), "r")
	bld.SetMultiInstance(work, false, "", "", nil, mustCompile(b, "100"), nil, nil)
	end := bld.AddEndEvent()
	bld.Connect(start, work)
	bld.Connect(work, end)
	cp, err := bld.Build()
	if err != nil {
		b.Fatal(err)
	}
	p := engine.New(1, log, store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		p.CreateInstance(cp.Key)
		if err := p.RunUntilIdle(); err != nil {
			b.Fatal(err)
		}
	}
}
