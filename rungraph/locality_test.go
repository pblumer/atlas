package rungraph

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// W1's acceptance criterion: the ordinal locality ADR-0404 §10 rests on, measured on
// state the engine actually wrote rather than on keys a test invented.
//
// The claim is §10's: *"assign them in key order and a component's nodes are contiguous
// in ordinal space, so its adjacency lists are sequential."* Everything §10 concludes
// about where the CSR may live depends on it — the same section measured a scattered
// read under a memory cap, without MADV_RANDOM, at 74× the cost of a sequential one. So
// if the claim fails, the mmap dial is not an operator's choice at all.
//
// It has to be measured on engine-written state because the property is about how the
// engine mints keys, not about the CSR builder. Keys are `[16 bit partition][48 bit
// counter]` and the counter increases, so *if* an instance's element instances are minted
// close together in time, they are close together in key space, and therefore close in
// ordinal space. Synthetic keys would assume exactly what is in question. What a test can
// honestly bound is the volume: this drives thousands of instances, not the hundred
// million the record projects, so what it establishes is that the mechanism holds where
// the engine's interleaving is real — and it would catch the interleaving that breaks it.

// engineFixture is the smallest real engine: a WAL on the test's temp disk, a Pebble
// store beside it, and a processor over both. Deliberately not a shared helper with
// benchmarks/harness_test.go — that one takes a *testing.B, and a copy of thirteen lines
// is cheaper than a package whose only purpose is to be shared by two callers.
func engineFixture(t *testing.T) (*engine.Processor, *state.Store) {
	t.Helper()
	p, store, _ := engineFixtureIn(t)
	return p, store
}

// engineFixtureIn is engineFixture plus the WAL directory, for a test that tails the same
// log the processor is writing.
func engineFixtureIn(t *testing.T) (*engine.Processor, *state.Store, string) {
	t.Helper()
	dir := t.TempDir()
	walDir := filepath.Join(dir, "wal")
	log, err := wal.Open(wal.Options{Dir: walDir})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close(); _ = log.Close() })
	return engine.New(1, log, store, engine.SystemClock{}), store, walDir
}

// parkingWorkload is a process that leaves a *shape* behind rather than a single node.
//
// Start → SubProcess( Start → ServiceTask ) with an interrupting timer boundary on the
// subprocess. Each instance therefore parks with several live element instances joined by
// the two reference kinds that matter here: the inner nodes carry FlowScopeKey pointing at
// the subprocess instance, and the boundary event carries AttachedToKey pointing at the
// same. A flat Start → ServiceTask → End would park one element with no edges at all, and
// a locality measurement over components of one node is arithmetic rather than evidence.
func parkingWorkload(t *testing.T) *compiler.CompiledProcess {
	t.Helper()
	bld := compiler.NewBuilder(1, "rungraph-locality", 1)
	start := bld.AddStartEvent()
	sub := bld.AddSubProcess()
	bld.Connect(start, sub)

	bld.PushScope(sub)
	innerStart := bld.AddStartEvent()
	work := bld.AddServiceTask("rungraph-work", 3)
	innerEnd := bld.AddEndEvent()
	bld.Connect(innerStart, work)
	bld.Connect(work, innerEnd)
	bld.PopScope()

	// An interrupting timer far enough out that it never fires during the test: what is
	// wanted is the armed element instance and its AttachedToKey edge, not the interrupt.
	bld.AddBoundaryTimerEvent(sub, true, int64(24)*3600*1_000_000_000)

	end := bld.AddEndEvent()
	bld.Connect(sub, end)

	cp, err := bld.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp
}

// TestOrdinalLocalityOnEngineWrittenState is the measurement W1 owes, and it found that
// §10's claim is conditional in a way the record does not say.
//
// The claim holds perfectly under sequential arrival and fails completely under a burst,
// and the failure scales with how many instances are in flight together. See the comment
// on the results below — the numbers are logged on every run, so a change in the engine's
// batching shows up here rather than in a mystery slowdown of a walk.
func TestOrdinalLocalityOnEngineWrittenState(t *testing.T) {
	// concurrency is how many instances are created before the batch cycle is driven.
	// 1 is arrival spread over time — an installation accumulating 10 000 a day. The
	// larger values are a burst: a bulk import, a message storm, a backlog drained after
	// an outage.
	for _, concurrency := range []int{1, 8, 64, 512} {
		t.Run(fmt.Sprintf("concurrency=%d", concurrency), func(t *testing.T) {
			p, store := engineFixture(t)
			cp := parkingWorkload(t)
			p.Deploy(cp)
			if err := p.Recover(); err != nil {
				t.Fatalf("Recover: %v", err)
			}

			const instances = 1024
			for created := 0; created < instances; created += concurrency {
				for range min(concurrency, instances-created) {
					p.CreateInstance(cp.Key)
				}
				if err := p.RunUntilIdle(); err != nil {
					t.Fatalf("RunUntilIdle: %v", err)
				}
			}

			// The consistent view ADR-0404 §4 seeds from, taken and closed around the
			// build the way the record's discipline requires (ADR-0239).
			view := store.ReadView()
			defer view.Close()

			g, err := Build(view)
			if err != nil {
				t.Fatalf("Build from a real ReadView: %v", err)
			}
			if g.Ordinals.Len() == 0 {
				t.Fatal("the engine parked nothing; this would measure the builder against an empty store")
			}

			span, components := g.Locality()
			nodes := g.Ordinals.Len()
			t.Logf("%d instances at concurrency %d → %d nodes, %d edges, %d components (%.2f nodes each, degree %.2f)",
				instances, concurrency, nodes, g.Edges, components,
				float64(nodes)/float64(components), 2*float64(g.Edges)/float64(nodes))
			t.Logf("  ORDINAL SPAN %.2f", span)

			// The forest topology every conclusion in ADR-0404 rests on. One giant
			// component would mean the run graph is not what the record says it is, and
			// that would matter far more than the locality number.
			if components < instances/2 {
				t.Errorf("components = %d for %d instances; the run graph is supposed to be a forest of per-instance components, not a connected mass",
					components, instances)
			}
			if g.Edges == 0 {
				t.Error("no edges: the scope and boundary references this workload creates were not turned into edges")
			}

			// What is asserted is the law the measurement found, not a hope and not a
			// constant. The engine mints an instance's k element instances across k
			// batch phases, and with c instances in flight each phase lays down c keys
			// before the next phase begins — so one component's k nodes end up one
			// "phase stride" apart and its ordinal span is
			//
			//	((k-1)·c + 1) / k
			//
			// which is 1.0 at c=1 and grows linearly with concurrency. Pinning the law
			// rather than a number is what makes this test catch a change in the engine's
			// batching, which is the thing that would silently move §10's ground.
			k := float64(nodes) / float64(components)
			predicted := ((k-1)*float64(concurrency) + 1) / k
			if span < predicted*0.9 || span > predicted*1.1 {
				t.Errorf("ordinal span = %.2f at concurrency %d with %.2f nodes per component; the law ((k-1)·c+1)/k predicts %.2f.\n"+
					"Either the engine's batching changed or the ordinal assignment did, and ADR-0404 §10's premise is a function of exactly this.",
					span, concurrency, k, predicted)
			}
			// And sequential arrival must be exactly 1.0, because that is the case §10's
			// premise is stated for: keys minted in order, one component contiguous.
			if concurrency == 1 && span != 1 {
				t.Errorf("ordinal span = %.4f at sequential arrival, want exactly 1.0", span)
			}
		})
	}
}
