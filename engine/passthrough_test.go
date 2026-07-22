package engine_test

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
)

// TestPassThroughTaskRunsToCompletion executes Start → Task → End with an
// undefined (pass-through) task: it has no work, so the instance flows straight
// through to completion with no external worker.
func TestPassThroughTaskRunsToCompletion(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(defKey, "passthrough", 1)
	start := b.AddStartEvent()
	task := b.AddTask()
	end := b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
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
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after run: process=%d element=%d, want 0 and 0 (task passed through)", pi, ei)
	}
}
