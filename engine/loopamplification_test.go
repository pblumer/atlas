package engine_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
)

// logBytes is how much this run wrote to the write-ahead log.
func logBytes(t *testing.T, dir string) int64 {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, "wal"))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var n int64
	for _, e := range entries {
		fi, err := e.Info()
		if err != nil {
			t.Fatalf("Info: %v", err)
		}
		n += fi.Size()
	}
	return n
}

// bigResultLoop is a multi-instance activity whose every iteration produces a result of
// about two hundred bytes, so the collection — and therefore any rewrite of it — is
// what dominates what the run records.
func bigResultLoop(t *testing.T, items int) *compiler.CompiledProcess {
	t.Helper()
	list := "[" + strings.TrimSuffix(strings.Repeat("1,", items), ",") + "]"
	b := compiler.NewBuilder(1, "mi-amplification", 1)
	start := b.AddStartEvent()
	setup := b.AddScriptTask(mustCompile(t, list), "items")
	work := b.AddScriptTask(mustCompile(t, `"`+strings.Repeat("x", 200)+`"`), "result")
	b.SetMultiInstance(work, false, "item", "results",
		mustCompile(t, "items"), nil, mustCompile(t, "result"), nil)
	end := b.AddEndEvent()
	b.Connect(start, setup)
	b.Connect(setup, work)
	b.Connect(work, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp
}

// runLoop runs one instance of a loop over items iterations and reports what it
// wrote to the log and how large its answer was.
func runLoop(t *testing.T, items int) (written int64, result int) {
	t.Helper()
	dir := t.TempDir()
	h := openHarness(t, dir)
	cp := bigResultLoop(t, items)
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	got := varText(t, h.store, model.NewKey(1, 1), "results")
	h.close(t)
	return logBytes(t, dir), len(got)
}

// TestALoopRecordsOneResultPerRound is the regression guard for the write
// amplification. A multi-instance activity collected each round's result by writing
// the *whole* collection back — read the list, set one element, serialise it again —
// so the bytes it recorded grew with the square of the iteration count. A hundred
// thousand results of a kilobyte each cost a hundred gigabytes of log to record a
// hundred megabytes of answer, and every intermediate version of the list was durable.
//
// The measurement is the test, because the defect was invisible to every functional
// assertion: the loop always produced the right answer. What was wrong was the cost of
// producing it.
//
// Doubling the iterations must roughly double what is written — not quadruple it. The
// bound is loose (2.6 rather than 2.0) because each round records more than its result:
// element instances, a loop counter, a job. Those are linear too, and the point is the
// shape, not the constant. Against the old code this ratio was 3.3 and climbing.
func TestALoopRecordsOneResultPerRound(t *testing.T) {
	small, smallResult := runLoop(t, 40)
	large, largeResult := runLoop(t, 80)

	if largeResult <= smallResult {
		t.Fatalf("the larger loop produced %d bytes and the smaller %d — the fixture is not measuring what it thinks",
			largeResult, smallResult)
	}
	if ratio := float64(large) / float64(small); ratio > 2.6 {
		t.Errorf("doubling the iterations multiplied the log by %.1f (%d → %d bytes), want about 2:\n"+
			"the collection is being recorded once per round again, and the cost is back to growing with the square of the count",
			ratio, small, large)
	}
}

// TestTheCollectedAnswerIsUnchanged: the cheaper record must produce the same list.
// The amplification fix moved where the collection is assembled — from the behaviour,
// which wrote the whole list, into the fold, which sets one element — and that is
// exactly the kind of move that is easy to get subtly wrong in the order or the
// indices.
func TestTheCollectedAnswerIsUnchanged(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp := miCollectProcess(t, "[1, 2, 3]", false)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if got := varText(t, h.store, model.NewKey(1, 1), "results"); got != "[10,20,30]" {
		t.Errorf("results = %q, want [10,20,30] — input order, one element per round", got)
	}
}
