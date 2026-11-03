package engine_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cockroachdb/pebble"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

func TestStateKnowsTheTriggerElementType(t *testing.T) {
	if state.EventSubTriggerType != uint8(compiler.TypeEventSubProcessStart) {
		t.Fatalf("state indexes type %d as a trigger, the compiler arms type %d", state.EventSubTriggerType, compiler.TypeEventSubProcessStart)
	}
}

// triggeredMIProcess is a parallel multi-instance subprocess over n iterations,
// each waiting on a service task, with a non-interrupting signal event subprocess
// armed in every iteration — the scope whose triggers must be disarmed when the
// iteration completes.
func triggeredMIProcess(t testing.TB, n string) (*compiler.CompiledProcess, int32) {
	t.Helper()
	b := compiler.NewBuilder(1, "mi-triggers", 1)
	start := b.AddStartEvent()
	sub := b.AddSubProcess()
	b.SetMultiInstance(sub, false, "", "", nil, mustCompile(t, n), nil, nil)
	b.PushScope(sub)
	is := b.AddStartEvent()
	work := b.AddServiceTask("mi-trigger-work", 3)
	ie := b.AddEndEvent()
	b.Connect(is, work)
	b.Connect(work, ie)
	handler := b.AddSubProcess()
	b.PushScope(handler)
	hs := b.AddStartEvent()
	he := b.AddEndEvent()
	b.Connect(hs, he)
	b.PopScope()
	b.SetEventSubProcess(handler, compiler.EventSubProcessDetail{StartNode: hs, Kind: compiler.BoundarySignal, SignalName: "never-sent"})
	b.PopScope()
	end := b.AddEndEvent()
	b.Connect(start, sub)
	b.Connect(sub, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	return cp, cp.ServiceTask(cp.Node(work).Detail).JobType
}

func liveTriggers(t *testing.T, s *state.Store) int {
	t.Helper()
	n := 0
	if err := s.ActiveElementInstances(func(_ uint64, v *model.ElementInstanceValue) error {
		if v.BpmnElementType == uint8(compiler.TypeEventSubProcessStart) {
			n++
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return n
}

// finish completes every iteration's job one by one and checks that each
// completed iteration disarmed its own trigger and no other, and that the
// instance ends with nothing left alive.
func finish(t *testing.T, p *engine.Processor, s *state.Store, jobType int32, n int) {
	t.Helper()
	for left := n; left > 0; left-- {
		if got := liveTriggers(t, s); got != left {
			t.Fatalf("%d iterations running, %d triggers armed", left, got)
		}
		p.CompleteJob(activatableJobs(t, s, jobType)[0])
		miRun(t, p)
	}
	if got, err := s.ActiveElementInstanceCount(); err != nil || got != 0 {
		t.Fatalf("%d element instances alive after the instance completed (err %v)", got, err)
	}
}

// Each iteration's trigger is disarmed when that iteration completes, from the
// scope-trigger index — live, after an independent WAL replay, and on a store that
// armed its triggers before the index existed (#1494).
func TestIterationTriggersAreDisarmedFromTheScopeIndex(t *testing.T) {
	cp, jobType := triggeredMIProcess(t, "3")
	start := func(t *testing.T, h *harness) *engine.Processor {
		p := engine.New(1, h.log, h.store, &manualClock{})
		p.Deploy(cp)
		if err := p.Recover(); err != nil {
			t.Fatal(err)
		}
		return p
	}
	t.Run("live", func(t *testing.T) {
		h := openHarness(t, t.TempDir())
		defer h.close(t)
		p := start(t, h)
		p.CreateInstance(cp.Key)
		miRun(t, p)
		finish(t, p, h.store, jobType, 3)
	})
	t.Run("after WAL replay", func(t *testing.T) {
		dir := t.TempDir()
		h := openHarness(t, dir)
		p := start(t, h)
		p.CreateInstance(cp.Key)
		miRun(t, p)
		h.close(t)
		fresh := miReplayInto(t, dir, cp)
		h2 := openHarness(t, dir)
		_ = h2.store.Close()
		h2.store = fresh
		defer func() { _ = h2.log.Close() }()
		finish(t, start(t, h2), fresh, jobType, 3)
	})
	// edit changes the closed store of a started instance directly.
	edit := func(t *testing.T, dir string, fn func(db *pebble.DB) error) {
		db, err := pebble.Open(filepath.Join(dir, "state"), &pebble.Options{Merger: &pebble.Merger{
			Name: "atlas.counter.sum.v1",
			Merge: func(_, v []byte) (pebble.ValueMerger, error) {
				m := &sumMerger{}
				return m, m.MergeNewer(v)
			},
		}})
		if err != nil {
			t.Fatal(err)
		}
		if err := fn(db); err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
	started := func(t *testing.T) string {
		dir := t.TempDir()
		h := openHarness(t, dir)
		p := start(t, h)
		p.CreateInstance(cp.Key)
		miRun(t, p)
		h.close(t)
		return dir
	}
	t.Run("store armed before the index", func(t *testing.T) {
		dir := started(t)
		edit(t, dir, func(db *pebble.DB) error {
			if err := db.DeleteRange([]byte{0x47}, []byte{0x48}, pebble.Sync); err != nil {
				return err
			}
			return db.Delete(append([]byte{0x00}, "scope_trigger_index_v1"...), pebble.Sync)
		})
		h2 := openHarness(t, dir)
		defer h2.close(t)
		finish(t, start(t, h2), h2.store, jobType, 3)
	})
	// A corrupt entry under the root scope stops the processor when the instance
	// completes, rather than completing it with a trigger it could not read.
	t.Run("corrupt index entry", func(t *testing.T) {
		dir := started(t)
		pi := model.NewKey(1, 1)
		edit(t, dir, func(db *pebble.DB) error {
			k := []byte{0x47, byte(pi >> 56), byte(pi >> 48), byte(pi >> 40), byte(pi >> 32), byte(pi >> 24), byte(pi >> 16), byte(pi >> 8), byte(pi), 1}
			return db.Set(k, nil, pebble.Sync)
		})
		h2 := openHarness(t, dir)
		defer h2.close(t)
		p := start(t, h2)
		var err error
		for range 3 {
			jobs := activatableJobs(t, h2.store, jobType)
			if len(jobs) == 0 {
				break
			}
			p.CompleteJob(jobs[0])
			if err = p.RunUntilIdle(); err != nil {
				break
			}
		}
		if err == nil || !strings.Contains(err.Error(), "scope trigger") {
			t.Fatalf("the instance completed over a corrupt scope-trigger entry (err %v)", err)
		}
	})
}

// Completing N parallel subprocess iterations is linear in N: each completion
// reads its own scope's triggers, not every live element instance. Quadratic
// growth multiplies the time by 16 from 2,000 to 8,000 iterations (measured
// before the index: 0.23 s at 250, 1.5 s at 500, 41 s at 1,000); this allows 10.
func TestParallelSubprocessIterationsCompleteInLinearTime(t *testing.T) {
	if testing.Short() || os.Getenv("ATLAS_SKIP_SCALE") != "" {
		t.Skip("scale test")
	}
	run := func(n string) time.Duration {
		h := openHarness(t, t.TempDir())
		defer h.close(t)
		b := compiler.NewBuilder(1, "mi-scale", 1)
		start := b.AddStartEvent()
		sub := b.AddSubProcess()
		b.SetMultiInstance(sub, false, "", "", nil, mustCompile(t, n), nil, nil)
		b.PushScope(sub)
		is := b.AddStartEvent()
		ie := b.AddEndEvent()
		b.Connect(is, ie)
		b.PopScope()
		end := b.AddEndEvent()
		b.Connect(start, sub)
		b.Connect(sub, end)
		cp, err := b.Build()
		if err != nil {
			t.Fatal(err)
		}
		p := engine.New(1, h.log, h.store, &manualClock{})
		p.Deploy(cp)
		if err := p.Recover(); err != nil {
			t.Fatal(err)
		}
		t0 := time.Now()
		p.CreateInstance(cp.Key)
		miRun(t, p)
		took := time.Since(t0)
		if got, err := h.store.ActiveElementInstanceCount(); err != nil || got != 0 {
			t.Fatalf("%s iterations: %d element instances alive (err %v)", n, got, err)
		}
		return took
	}
	small, large := run("2000"), run("8000")
	t.Logf("2,000 iterations: %v; 8,000 iterations: %v (×%.1f)", small, large, float64(large)/float64(small))
	if large > 10*small {
		t.Fatalf("8,000 iterations took %v, %.1f times 2,000 (%v): completion is not linear", large, float64(large)/float64(small), small)
	}
}
