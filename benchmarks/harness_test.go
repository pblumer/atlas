package benchmarks

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// benchClock is a monotonic counter clock. Benchmarks read time into events on
// every batch; a counter avoids a real time syscall skewing the per-op allocation
// and latency figures while still giving each event a distinct, ordered stamp
// (invariant I4 — time is a value read into events, never regenerated on replay).
type benchClock struct{ t int64 }

func (c *benchClock) Now() int64 { c.t++; return c.t }

// durableEngine builds a processor over a real segmented WAL and a real Pebble
// state store in a fresh temp dir — the *durable* profile: every batch commit is
// made durable with a group-commit fsync, exactly as the running server does. It
// returns the processor, the store (for job and applied-position queries), and the
// WAL directory (for on-disk storage-growth measurement). Cleanup is registered on
// b, so callers do not close anything themselves.
func durableEngine(b *testing.B) (*engine.Processor, *state.Store, string) {
	b.Helper()
	dir := b.TempDir()
	walDir := filepath.Join(dir, "wal")
	log, err := wal.Open(wal.Options{Dir: walDir})
	if err != nil {
		b.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		b.Fatalf("state.Open: %v", err)
	}
	b.Cleanup(func() { _ = store.Close(); _ = log.Close() })
	return engine.New(1, log, store, &benchClock{}), store, walDir
}

// deploy registers a compiled process and recovers the (empty) engine so it is
// ready to run instances. Recovery on a fresh store is a no-op beyond priming the
// key generator; it mirrors the server's start-up order (deploy then recover).
func deploy(b *testing.B, p *engine.Processor, cp *compiler.CompiledProcess) {
	b.Helper()
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		b.Fatalf("Recover: %v", err)
	}
}

// mustCompile compiles a FEEL expression or fails the benchmark.
func mustCompile(b *testing.B, text string) *expr.Compiled {
	b.Helper()
	e, err := expr.CompileAuto(text)
	if err != nil {
		b.Fatalf("expr.CompileAuto(%q): %v", text, err)
	}
	return e
}

// --- Workloads (built with the public compiler builder, not BPMN XML) ---------

// linearSelfCompleting is workload #1: a minimal linear process that runs to
// completion with no external worker — Start → Script → End. The script writes one
// variable via in-engine FEEL, so a single CreateInstance drives straight through
// to a finished instance.
func linearSelfCompleting(b *testing.B) *compiler.CompiledProcess {
	b.Helper()
	bld := compiler.NewBuilder(1, "bench-linear", 1)
	start := bld.AddStartEvent()
	task := bld.AddScriptTask(mustCompile(b, `"done"`), "status")
	end := bld.AddEndEvent()
	bld.Connect(start, task)
	bld.Connect(task, end)
	cp, err := bld.Build()
	if err != nil {
		b.Fatalf("Build linear: %v", err)
	}
	return cp
}

// serviceTaskWorkload is workload #2: Start → ServiceTask → End. The token parks at
// the service task on an activatable job; a worker (the benchmark) completes it and
// the instance finishes. Returns the process and its interned job type.
func serviceTaskWorkload(b *testing.B) (*compiler.CompiledProcess, int32) {
	b.Helper()
	bld := compiler.NewBuilder(1, "bench-service", 1)
	start := bld.AddStartEvent()
	task := bld.AddServiceTask("bench-work", 3)
	end := bld.AddEndEvent()
	bld.Connect(start, task)
	bld.Connect(task, end)
	cp, err := bld.Build()
	if err != nil {
		b.Fatalf("Build service: %v", err)
	}
	return cp, cp.ServiceTask(cp.Node(task).Detail).JobType
}

// variableGatewayWorkload is workload #3: a mixed process reading a start variable
// and routing on it — Start → XOR(amount > 100 → "high" | default → "low") → End.
// Each branch is a script task that writes the chosen path, so the instance
// self-completes; the start variable decides which branch runs.
func variableGatewayWorkload(b *testing.B) *compiler.CompiledProcess {
	b.Helper()
	bld := compiler.NewBuilder(1, "bench-router", 1)
	start := bld.AddStartEvent()
	gw := bld.AddExclusiveGateway()
	high := bld.AddScriptTask(mustCompile(b, `"high"`), "path")
	low := bld.AddScriptTask(mustCompile(b, `"low"`), "path")
	endHigh := bld.AddEndEvent()
	endLow := bld.AddEndEvent()
	bld.Connect(start, gw)
	fHigh := bld.Connect(gw, high)
	bld.SetFlowCondition(fHigh, mustCompile(b, "amount > 100"))
	fLow := bld.Connect(gw, low)
	bld.SetFlowDefault(fLow)
	bld.Connect(high, endHigh)
	bld.Connect(low, endLow)
	cp, err := bld.Build()
	if err != nil {
		b.Fatalf("Build router: %v", err)
	}
	return cp
}

// --- Run helpers --------------------------------------------------------------

// runToIdle creates one instance and drives the batch cycle until the engine is
// idle. For a self-completing workload this runs the instance to a finished state.
func runToIdle(b *testing.B, p *engine.Processor, defKey uint64, vars ...model.VariableValue) {
	p.CreateInstance(defKey, vars...)
	if err := p.RunUntilIdle(); err != nil {
		b.Fatalf("RunUntilIdle: %v", err)
	}
}

// completeOneJob finds the single activatable job of the given type and completes
// it, then drains the follow-up batch — the "external worker" half of workload #2.
func completeOneJob(b *testing.B, p *engine.Processor, store *state.Store, jobType int32) {
	var jobKey uint64
	if err := store.ActivatableJobs(jobType, func(k uint64) error { jobKey = k; return nil }); err != nil {
		b.Fatalf("ActivatableJobs: %v", err)
	}
	if jobKey == 0 {
		b.Fatal("no activatable job to complete")
	}
	p.CompleteJob(jobKey)
	if err := p.RunUntilIdle(); err != nil {
		b.Fatalf("RunUntilIdle after complete: %v", err)
	}
}

// --- Metric reporting ---------------------------------------------------------

// reportEngineMetrics adds per-op engine-wide metrics to the benchmark result:
//
//   - events/op — durable event records applied per iteration, from the monotonic
//     applied log position (a record's Position is its sequence number). Multiply
//     by the ops/sec that `ns/op` implies to get events/sec.
//   - walB/op   — on-disk WAL bytes grown per iteration (total segment bytes since
//     the start position, divided by N). WAL segments grow by append, so this is
//     real storage growth, not preallocated capacity.
//
// Combined with `-benchmem` (allocs/op, B/op) and `ns/op`, this covers the
// engine-side figures programme B asks for. It is called after b.StopTimer so the
// store read and directory walk do not count toward the measured time.
func reportEngineMetrics(b *testing.B, store *state.Store, walDir string, startPos uint64) {
	b.Helper()
	endPos, err := store.LastAppliedPosition()
	if err != nil {
		b.Fatalf("LastAppliedPosition: %v", err)
	}
	n := float64(b.N)
	b.ReportMetric(float64(endPos-startPos)/n, "events/op")
	b.ReportMetric(float64(walDirBytes(b, walDir))/n, "walB/op")
}

// appliedPosition snapshots the durable applied log position, used as the baseline
// before the measured loop so reported event counts exclude deploy/recovery.
func appliedPosition(b *testing.B, store *state.Store) uint64 {
	b.Helper()
	pos, err := store.LastAppliedPosition()
	if err != nil {
		b.Fatalf("LastAppliedPosition: %v", err)
	}
	return pos
}

// walDirBytes sums the sizes of every file in the WAL directory — the durable
// storage the workload produced.
func walDirBytes(b *testing.B, walDir string) int64 {
	b.Helper()
	entries, err := os.ReadDir(walDir)
	if err != nil {
		b.Fatalf("ReadDir(%s): %v", walDir, err)
	}
	var total int64
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			b.Fatalf("stat %s: %v", e.Name(), err)
		}
		total += info.Size()
	}
	return total
}
