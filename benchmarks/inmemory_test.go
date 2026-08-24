package benchmarks

import (
	"os"
	"testing"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// The in-memory profile runs the same engine-level workloads as the steady-state
// suite, but with the WAL and state store on a RAM-backed tmpfs instead of real
// disk. The state store already commits with pebble.NoSync, so the only durability
// cost is the WAL's per-batch fsync; on tmpfs that fsync hits RAM and returns almost
// immediately. Comparing an in-memory benchmark to its durable twin therefore splits
// the per-instance cost into engine CPU (what remains here) and disk-fsync latency
// (the difference). It is a measurement profile, not a durability mode: nothing here
// is a claim that Atlas runs without fsync in production.

// inMemoryEngine is engineOverDir on a fresh RAM-backed directory. If no tmpfs mount
// is available (a non-Linux host, or /dev/shm missing), the benchmark is skipped
// rather than silently falling back to disk — a disk run would mislabel the profile.
func inMemoryEngine(b *testing.B) (*engine.Processor, *state.Store, string) {
	b.Helper()
	for _, base := range []string{"/dev/shm", os.Getenv("ATLAS_BENCH_TMPFS")} {
		if base == "" {
			continue
		}
		if info, err := os.Stat(base); err != nil || !info.IsDir() {
			continue
		}
		dir, err := os.MkdirTemp(base, "atlas-bench-inmem-*")
		if err != nil {
			continue
		}
		b.Cleanup(func() { _ = os.RemoveAll(dir) })
		return engineOverDir(b, dir)
	}
	b.Skip("no tmpfs mount for the in-memory profile (set ATLAS_BENCH_TMPFS to a RAM-backed dir)")
	return nil, nil, "" // unreachable; b.Skip stops the benchmark
}

// BenchmarkInMemoryLinearSelfCompleting is workload #1 on the in-memory profile — the
// RAM-backed twin of BenchmarkLinearSelfCompleting.
func BenchmarkInMemoryLinearSelfCompleting(b *testing.B) {
	p, store, walDir := inMemoryEngine(b)
	cp := linearSelfCompleting(b)
	deploy(b, p, cp)
	startPos := appliedPosition(b, store)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		runToIdle(b, p, cp.Key)
	}
	b.StopTimer()
	reportEngineMetrics(b, store, walDir, startPos)
}

// BenchmarkInMemoryServiceTaskLifecycle is workload #2 on the in-memory profile — the
// RAM-backed twin of BenchmarkServiceTaskLifecycle.
func BenchmarkInMemoryServiceTaskLifecycle(b *testing.B) {
	p, store, walDir := inMemoryEngine(b)
	cp, jobType := serviceTaskWorkload(b)
	deploy(b, p, cp)
	startPos := appliedPosition(b, store)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		runToIdle(b, p, cp.Key)
		completeOneJob(b, p, store, jobType)
	}
	b.StopTimer()
	reportEngineMetrics(b, store, walDir, startPos)
}

// BenchmarkInMemoryVariableGatewayRouting is workload #3 on the in-memory profile —
// the RAM-backed twin of BenchmarkVariableGatewayRouting.
func BenchmarkInMemoryVariableGatewayRouting(b *testing.B) {
	p, store, walDir := inMemoryEngine(b)
	cp := variableGatewayWorkload(b)
	deploy(b, p, cp)
	startPos := appliedPosition(b, store)
	amount := model.VariableValue{Name: "amount", Kind: model.VarNumber, Text: "150"}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		runToIdle(b, p, cp.Key, amount)
	}
	b.StopTimer()
	reportEngineMetrics(b, store, walDir, startPos)
}
