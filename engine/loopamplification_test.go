package engine_test

import (
	"io/fs"
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

// storeBytes is how much of the state store this run occupies. Unlike the log, which
// only ever grows, the store keeps the current value of each key — so what this
// measures is the churn the loop left behind: every superseded version of the
// collection that Pebble has yet to compact away.
func storeBytes(t *testing.T, dir string) int64 {
	t.Helper()
	var n int64
	if err := filepath.WalkDir(filepath.Join(dir, "state"), func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		n += fi.Size()
		return nil
	}); err != nil {
		t.Fatalf("WalkDir: %v", err)
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

// runLoop runs one instance of a loop over items iterations and reports what it wrote
// to the log, what it left in the state store, and how large its answer was.
func runLoop(t *testing.T, items int) (written, stored int64, result int) {
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
	return logBytes(t, dir), storeBytes(t, dir), len(got)
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
	small, _, smallResult := runLoop(t, 40)
	large, _, largeResult := runLoop(t, 80)

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

// TestTheConditionSeesTheCollectionAsItFills is the guard on the form itself. A loop's
// completion condition is evaluated over the body's scope chain, so it can read the
// collection *while it is being filled* — and that collection is now a stub with its
// elements held apart, assembled on every read. If the assembly were wrong, or skipped
// on the path FEEL takes, the condition would read nulls where results already are and
// the loop would run to the end of its list.
//
// It stops after the second round instead, and the promoted list shows exactly two
// filled slots: the rounds that ran, and nulls where the loop decided not to.
func TestTheConditionSeesTheCollectionAsItFills(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(1, "mi-condition", 1)
	start := b.AddStartEvent()
	setup := b.AddScriptTask(mustCompile(t, "[1,2,3,4]"), "items")
	work := b.AddScriptTask(mustCompile(t, "item * 10"), "result")
	b.SetMultiInstance(work, true /*sequential*/, "item", "results",
		mustCompile(t, "items"), nil, mustCompile(t, "result"),
		mustCompile(t, "results[1] != null and results[2] != null"))
	end := b.AddEndEvent()
	b.Connect(start, setup)
	b.Connect(setup, work)
	b.Connect(work, end)
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
	if got, want := varText(t, h.store, model.NewKey(1, 1), "results"), "[10,20,null,null]"; got != want {
		t.Errorf("results = %s, want %s — the condition did not read the collection as it filled", got, want)
	}
}

// TestALoopKeepsOneCopyOfItsCollection is the same guard for the state store, and it
// is a separate test because for a while the two answers differed. Naming the element
// in the log left the fold still putting the assembled collection back under one key
// each round, so the store went on absorbing bytes that grew with the square of the
// count even after the log had stopped: measured over these same four sizes, the log
// doubled while the store multiplied by 2.5, then 2.9, then 3.3.
//
// Holding the elements one key each is what makes a round cost one element here too
// (ADR-draft-a-collection-under-construction). The bound is the log test's, and for
// the same reason: what a round records besides its result is linear as well, so the
// shape is the claim and the constant is not.
func TestALoopKeepsOneCopyOfItsCollection(t *testing.T) {
	_, small, smallResult := runLoop(t, 40)
	_, large, largeResult := runLoop(t, 80)

	if largeResult <= smallResult {
		t.Fatalf("the larger loop produced %d bytes and the smaller %d — the fixture is not measuring what it thinks",
			largeResult, smallResult)
	}
	if ratio := float64(large) / float64(small); ratio > 2.6 {
		t.Errorf("doubling the iterations multiplied the state store by %.1f (%d → %d bytes), want about 2:\n"+
			"the whole collection is being stored once per element again",
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

// TestAnIterationResultTooLargeParksItsRound is where this change meets the budgets
// of ADR-0294. Each round's result is now measured on its own, against the budget for
// one variable — it *is* one business record — while the collection it joins has its
// own, larger ceiling. Refusing here rather than after assembling the list means the
// incident names the element that produced the value.
//
// And, as everywhere else, the refusal stops the round: an iteration that completed
// would take the incident with it and the loop would finish looking successful, minus
// one result.
func TestAnIterationResultTooLargeParksItsRound(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	// The round's own result is small and the *collected element* is large, so the
	// refusal happens where this test is aiming: at the write into the collection, not
	// at the script task's own write, which has its own guard and would otherwise stop
	// the round first and prove nothing about this path.
	b := compiler.NewBuilder(1, "mi-fat-element", 1)
	start := b.AddStartEvent()
	setup := b.AddScriptTask(mustCompile(t, "[1, 2, 3]"), "items")
	work := b.AddScriptTask(mustCompile(t, `"ok"`), "result")
	b.SetMultiInstance(work, false, "item", "results",
		mustCompile(t, "items"), nil, mustCompile(t, `"`+strings.Repeat("x", 200)+`"`), nil)
	end := b.AddEndEvent()
	b.Connect(start, setup)
	b.Connect(setup, work)
	b.Connect(work, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.SetMaxVariable(64) // above the round's own "ok", below the element it collects
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v — a refused result must not take the batch down", err)
	}

	incs := incidents(t, h.store)
	if len(incs) == 0 {
		t.Fatal("an oversized round result was collected without an incident")
	}
	for _, inc := range incs {
		if inc.Reason != model.IncidentVariableTooLarge {
			t.Errorf("incident reason = %v, want IncidentVariableTooLarge", inc.Reason)
		}
	}
	if pi, _ := counts(t, h.store); pi != 1 {
		t.Errorf("process instances = %d, want the instance still standing", pi)
	}
}
