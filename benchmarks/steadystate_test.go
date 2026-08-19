package benchmarks

import (
	"testing"

	"github.com/pblumer/atlas/model"
)

// These benchmarks measure steady-state throughput of the pure engine under the
// durable profile (real WAL fsync per batch). Each op is one full instance:
// created, run through the batch cycle, and — for the service-task workload —
// completed by an in-process worker. Read `ns/op` as the per-instance cost
// (instances/sec = 1e9 / ns_per_op); `events/op` and `walB/op` are reported by the
// harness, and `-benchmem` adds allocs/op and B/op. See README.md.

// BenchmarkLinearSelfCompleting is workload #1: a minimal linear process that runs
// straight to completion with no external worker (Start → Script → End).
func BenchmarkLinearSelfCompleting(b *testing.B) {
	p, store, walDir := durableEngine(b)
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

// BenchmarkServiceTaskLifecycle is workload #2: create, activate, and complete a
// service task (Start → ServiceTask → End). This is the create → job → complete
// path a real external worker drives.
func BenchmarkServiceTaskLifecycle(b *testing.B) {
	p, store, walDir := durableEngine(b)
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

// BenchmarkVariableGatewayRouting is workload #3: a mixed process that reads a start
// variable and routes on an exclusive gateway (amount > 100). The variable sends it
// down the "high" branch each iteration; both branches self-complete via a script.
func BenchmarkVariableGatewayRouting(b *testing.B) {
	p, store, walDir := durableEngine(b)
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
