package engine_test

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
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

// elementTrail is every retained element write of an instance, as "scope:index=value"
// in change order per scope. The body's key is one the run generated, so every key it
// could have generated is asked.
func elementTrail(t *testing.T, s *state.Store, pi uint64) []string {
	t.Helper()
	var out []string
	for seq := uint64(1); seq <= 200; seq++ {
		scope := model.NewKey(1, seq)
		if err := s.VariableElementHistory(pi, scope, func(_ int64, _ uint64, v *model.VariableValue) error {
			out = append(out, fmt.Sprintf("%d:%d=%s", seq, v.Index, v.Text))
			return nil
		}); err != nil {
			t.Fatalf("VariableElementHistory: %v", err)
		}
	}
	return out
}

// TestEachRoundsElementIsRetainedAndRecovers: a round's result joins the body's
// collection as one element (ADR-0296), and the element is retained so the replay can
// show the collection as each round left it (ADR-draft-collection-by-round). One row
// per round, the element alone — and replaying the log into an empty store rebuilds
// the identical rows, because the fold writes them from the event and the batch's own
// state (I4).
func TestEachRoundsElementIsRetainedAndRecovers(t *testing.T) {
	dir := t.TempDir()
	cp := miCollectProcess(t, "[1, 2, 3]", true)
	clock := &manualClock{}

	h := openHarness(t, dir)
	p := engine.New(1, h.log, h.store, clock)
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	pi := model.NewKey(1, 1)
	live := elementTrail(t, h.store, pi)
	if len(live) != 3 {
		t.Fatalf("retained elements = %v, want one per round", live)
	}
	for i, want := range []string{"=10", "=20", "=30"} {
		if !strings.HasSuffix(live[i], fmt.Sprintf(":%d%s", i, want)) {
			t.Errorf("element %d = %q, want index %d%s", i, live[i], i, want)
		}
	}
	h.close(t)

	log2, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store2, err := state.Open(filepath.Join(dir, "state2"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	defer func() {
		_ = store2.Close()
		_ = log2.Close()
	}()
	p2 := engine.New(1, log2, store2, clock)
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover (replay): %v", err)
	}
	if replayed := elementTrail(t, store2, pi); !reflect.DeepEqual(replayed, live) {
		t.Errorf("replayed elements = %v, want %v", replayed, live)
	}
}
