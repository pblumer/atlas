package bench_test

import (
	"testing"

	"github.com/pblumer/atlas/bench"
	"github.com/pblumer/atlas/bench/scenarios"
)

// BenchmarkThroughput measures amortized per-instance cost when instances are
// folded through the batch cycle together — the group-commit path a busy
// deployment actually runs on (many commands per fsync). ns/op is the per-
// instance cost; invert it for instances/sec. Storage medium matters enormously
// here (an fsync on tmpfs is nearly free) and is reported by scripts/bench.sh —
// a throughput number without its medium is not comparable.
//
// The reported allocs/op is end-to-end (engine plus this harness), a coarse
// drift signal. The precise no-allocation-on-the-hot-path guard (invariant I1)
// lives in engine.BenchmarkInstanceLifecycle, which measures the processor path
// in isolation.
func BenchmarkThroughput(b *testing.B) {
	for _, s := range scenarios.All() {
		b.Run(s.Name, func(b *testing.B) {
			e, err := bench.Open(b.TempDir())
			if err != nil {
				b.Fatalf("Open: %v", err)
			}
			defer e.Close()
			cp, jobTypes := s.Compile(7)
			e.Proc.Deploy(cp)

			b.ReportAllocs()
			b.ResetTimer()
			if err := e.RunInstances(cp.Key, jobTypes, b.N, s.StartVars); err != nil {
				b.Fatalf("RunInstances: %v", err)
			}
			b.StopTimer()
		})
	}
}

// BenchmarkLatency measures a single instance driven to completion on its own,
// so its commands pay their own fsync instead of sharing one. This is the
// command-to-durable latency floor, and it is deliberately separate from
// throughput: because of group commit the two do not equal 1/each-other, and
// reporting only one of them would misrepresent the engine.
func BenchmarkLatency(b *testing.B) {
	for _, s := range scenarios.All() {
		b.Run(s.Name, func(b *testing.B) {
			e, err := bench.Open(b.TempDir())
			if err != nil {
				b.Fatalf("Open: %v", err)
			}
			defer e.Close()
			cp, jobTypes := s.Compile(7)
			e.Proc.Deploy(cp)

			b.ResetTimer()
			for range b.N {
				if err := e.RunInstances(cp.Key, jobTypes, 1, s.StartVars); err != nil {
					b.Fatalf("RunInstances: %v", err)
				}
			}
			b.StopTimer()
		})
	}
}
