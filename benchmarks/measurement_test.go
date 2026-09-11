package benchmarks

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// Measuring a business capability's declared KPIs and SLAs (ADR-0305) means reading
// what the engine already recorded. The record's open question is whether that is
// computable at the instance volumes this is aimed at *without* the OpenSearch
// exporter (ADR-0114), which not every installation runs — and it has to be answered
// by measurement rather than by argument, which is what this file is.
//
// The four candidate readings sit on three different data shapes, and the shapes are
// what decides the answer:
//
//   - **Outcome distribution** — how often each distinctly named end event fired —
//     reads ElementVisitTotals, a maintained per-element counter (ADR-0080). Its work
//     is O(elements): the same handful of keys whether the definition has run a
//     hundred instances or a million.
//   - **Cancellation rate** reads ElementTerminationTotals, the same shape.
//   - **Cycle time** needs each instance's own CreatedAt and CompletedAt, which the
//     instance record carries. The finished-by-definition index is ordered by
//     completion time, so a window is a bounded range scan and the walk already
//     yields the record — the timestamps cost no extra read at all.
//   - **Per-phase duration** needs the order of elements *within* an instance, which
//     lives in the step trail (ADR-0046) under a prefix per instance. One extra scan
//     per instance, on top of the walk.
//
// So three of the four are cheap for structural reasons and one is not, and the
// question is what "not" costs. Each benchmark below populates a fixed instance
// population in setup (the timer is off for it) and measures one whole reading over
// that population, so ns/op is the cost of answering once. Comparing the same
// reading across populations is the answer: a flat line is O(elements), a line that
// tracks the population is O(instances).

// measurementPopulations are the sizes every reading is measured at. They span two
// orders of magnitude, which is enough to tell a flat line from a linear one without
// making the smoke run in CI (`-benchtime=1x`, which still pays the setup) slow: the
// whole file populates about 11k self-completing instances.
//
// A larger population is a deliberate local run, not a committed default: set
// ATLAS_BENCH_POPULATIONS to a comma-separated list to override. The override exists
// because the interesting question here is where a curve *goes*, and answering it by
// editing this line would mean the run that produced a published number is not the
// code that was committed. See README.md — a number here is specific to one machine
// and one commit.
var measurementPopulations = populationsFromEnv()

func populationsFromEnv() []int {
	raw := strings.TrimSpace(os.Getenv("ATLAS_BENCH_POPULATIONS"))
	if raw == "" {
		return []int{100, 1_000, 10_000}
	}
	var out []int
	for _, field := range strings.Split(raw, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(field))
		if err != nil || n <= 0 {
			// A malformed override is not a reason to silently measure the default:
			// the whole point of the variable is that somebody is chasing a specific
			// size, and answering a different question would be worse than not
			// answering. panic is the only signal available before a *testing.B exists.
			panic("ATLAS_BENCH_POPULATIONS: " + raw + " is not a comma-separated list of positive integers")
		}
		out = append(out, n)
	}
	return out
}

// populated runs n instances of cp to completion and returns the store holding them.
// Every instance is finished, which is what a KPI reads: an in-flight case has no
// cycle time yet.
func populated(b *testing.B, n int) (*state.Store, *compiler.CompiledProcess) {
	b.Helper()
	cp := linearSelfCompleting(b)
	p, store, _ := durableEngine(b)
	deploy(b, p, cp)
	for range n {
		p.CreateInstance(cp.Key)
	}
	if err := p.RunUntilIdle(); err != nil {
		b.Fatalf("RunUntilIdle (populate %d): %v", n, err)
	}
	return store, cp
}

// forEachPopulation runs read once per op against a population of each size, naming
// the sub-benchmark after the size so `benchstat` lines up the comparison.
func forEachPopulation(b *testing.B, read func(b *testing.B, store *state.Store, cp *compiler.CompiledProcess)) {
	for _, n := range measurementPopulations {
		b.Run(fmt.Sprintf("instances=%d", n), func(b *testing.B) {
			store, cp := populated(b, n)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				read(b, store, cp)
			}
		})
	}
}

// BenchmarkMeasurementOutcomeDistribution reads the per-element visit counters — the
// reading behind "how many cases ended each way". If the counters do what ADR-0080
// says, this is flat across the populations: the work is the number of elements the
// definition has, and that does not change when more instances run.
func BenchmarkMeasurementOutcomeDistribution(b *testing.B) {
	forEachPopulation(b, func(b *testing.B, store *state.Store, cp *compiler.CompiledProcess) {
		var total int64
		if err := store.ElementVisitTotals(cp.Key, func(_ int32, count int64) error {
			total += count
			return nil
		}); err != nil {
			b.Fatalf("ElementVisitTotals: %v", err)
		}
		if total == 0 {
			b.Fatal("visit totals are empty; the population did not run")
		}
	})
}

// BenchmarkMeasurementCancellationRate reads the termination counters, the same shape
// as the visit counters and the other half of "a token was here and left".
func BenchmarkMeasurementCancellationRate(b *testing.B) {
	forEachPopulation(b, func(b *testing.B, store *state.Store, cp *compiler.CompiledProcess) {
		if err := store.ElementTerminationTotals(cp.Key, func(int32, int64) error {
			return nil
		}); err != nil {
			b.Fatalf("ElementTerminationTotals: %v", err)
		}
	})
}

// BenchmarkMeasurementCycleTime walks every finished instance of the definition and
// folds its CreatedAt/CompletedAt into a mean — the reading behind "how long a case
// takes end to end".
//
// The walk is over the whole population rather than a window on purpose: a window is
// the same scan with an earlier stop, so the unbounded case is the upper bound on
// what any window costs, and it is the number the open question is really about.
func BenchmarkMeasurementCycleTime(b *testing.B) {
	forEachPopulation(b, func(b *testing.B, store *state.Store, cp *compiler.CompiledProcess) {
		var sum, n int64
		if err := store.FinishedInstancesOfDefDesc(cp.Key, 0, 0, func(_ uint64, v *model.ProcessInstanceValue) error {
			if v.CompletedAt > v.CreatedAt {
				sum += v.CompletedAt - v.CreatedAt
				n++
			}
			return nil
		}); err != nil {
			b.Fatalf("FinishedInstancesOfDefDesc: %v", err)
		}
		if n == 0 {
			b.Fatal("no finished instance carried a cycle time; the population did not complete")
		}
	})
}

// BenchmarkMeasurementPhaseDuration is the expensive one, and the reason this file
// exists. It is the same walk as the cycle time, plus one step-trail scan per
// instance — the only way to know *when within a case* a phase boundary was passed.
//
// Its cost against BenchmarkMeasurementCycleTime at the same population is the whole
// marginal price of offering per-phase duration, isolated: same walk, same instances,
// one extra prefix scan each.
func BenchmarkMeasurementPhaseDuration(b *testing.B) {
	forEachPopulation(b, func(b *testing.B, store *state.Store, cp *compiler.CompiledProcess) {
		var steps int64
		if err := store.FinishedInstancesOfDefDesc(cp.Key, 0, 0, func(piKey uint64, _ *model.ProcessInstanceValue) error {
			return store.ElementStepHistory(piKey, func(int64, uint64, int32) error {
				steps++
				return nil
			})
		}); err != nil {
			b.Fatalf("phase duration walk: %v", err)
		}
		if steps == 0 {
			b.Fatal("no step was recorded; the population left no trail to phase")
		}
	})
}

// The population is not the only axis, and the second one is the one that decides
// whether per-phase duration is affordable.
//
// Cycle time reads two fields off each instance record, so its cost per instance is
// the same whether the process has three elements or thirty. Per-phase duration reads
// the instance's step trail, which holds one entry per element a token activated — so
// its cost per instance is proportional to the *length of the process*.
//
// Every benchmark above runs the three-element linear workload, which is the shortest
// process this harness can build and therefore the most flattering case per-phase
// duration will ever get. Reporting its 100k number without this axis would be
// answering an easier question than the one that was asked.

// steppedWorkload builds Start → n script tasks in a row → End, so an instance leaves
// n+2 entries in its step trail. n is what a real process's length varies.
func steppedWorkload(b *testing.B, n int) *compiler.CompiledProcess {
	b.Helper()
	bld := compiler.NewBuilder(1, "bench-stepped", 1)
	prev := bld.AddStartEvent()
	for i := range n {
		task := bld.AddScriptTask(mustCompile(b, `"done"`), fmt.Sprintf("step%d", i))
		bld.Connect(prev, task)
		prev = task
	}
	end := bld.AddEndEvent()
	bld.Connect(prev, end)
	cp, err := bld.Build()
	if err != nil {
		b.Fatalf("Build stepped(%d): %v", n, err)
	}
	return cp
}

// measurementStepCounts are the process lengths the length axis is measured at, at one
// fixed population. Three is the linear workload above; thirty is an ordinary
// end-to-end process, not a pathological one.
var measurementStepCounts = []int{1, 10, 30}

// populatedStepped is [populated] over a process of a chosen length.
func populatedStepped(b *testing.B, instances, steps int) (*state.Store, *compiler.CompiledProcess) {
	b.Helper()
	cp := steppedWorkload(b, steps)
	p, store, _ := durableEngine(b)
	deploy(b, p, cp)
	for range instances {
		p.CreateInstance(cp.Key)
	}
	if err := p.RunUntilIdle(); err != nil {
		b.Fatalf("RunUntilIdle (populate %d×%d): %v", instances, steps, err)
	}
	return store, cp
}

// measurementLengthPopulation is the fixed population the length axis is measured at.
// One size, three lengths: the question here is the slope in the other direction, and
// the population axis is already answered above.
const measurementLengthPopulation = 10_000

// BenchmarkMeasurementCycleTimeByLength is the control. Cycle time reads the instance
// record, so a longer process must not move it — and if it does, the comparison below
// is measuring something other than trail length.
func BenchmarkMeasurementCycleTimeByLength(b *testing.B) {
	forEachLength(b, func(b *testing.B, store *state.Store, cp *compiler.CompiledProcess) {
		if err := store.FinishedInstancesOfDefDesc(cp.Key, 0, 0, func(uint64, *model.ProcessInstanceValue) error {
			return nil
		}); err != nil {
			b.Fatalf("FinishedInstancesOfDefDesc: %v", err)
		}
	})
}

// BenchmarkMeasurementPhaseDurationByLength is the question. If this tracks the
// process's length while the control above stays flat, then per-phase duration costs
// instances × elements, and its price on a real process is not the price the
// three-element workload reported.
func BenchmarkMeasurementPhaseDurationByLength(b *testing.B) {
	forEachLength(b, func(b *testing.B, store *state.Store, cp *compiler.CompiledProcess) {
		if err := store.FinishedInstancesOfDefDesc(cp.Key, 0, 0, func(piKey uint64, _ *model.ProcessInstanceValue) error {
			return store.ElementStepHistory(piKey, func(int64, uint64, int32) error { return nil })
		}); err != nil {
			b.Fatalf("phase duration walk: %v", err)
		}
	})
}

func forEachLength(b *testing.B, read func(b *testing.B, store *state.Store, cp *compiler.CompiledProcess)) {
	for _, steps := range measurementStepCounts {
		b.Run(fmt.Sprintf("steps=%d", steps), func(b *testing.B) {
			store, cp := populatedStepped(b, measurementLengthPopulation, steps)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				read(b, store, cp)
			}
		})
	}
}
