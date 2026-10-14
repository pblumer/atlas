package engine_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
)

// A job that becomes available must wake the workers long-polling its type
// (ADR-0157 step 2b). The engine does that by registering a post-fsync notification
// beside the event that puts the job on the activatable index — and a path that
// forgets it breaks nothing a test of the job itself can see: the job is on the index,
// it is pullable, and a worker that asks finds it. Only a worker that was already
// waiting sleeps out its poll. That is how a retryable failure went without a wake-up
// until #1140.
//
// Two guards keep the next path from doing the same:
//
//   - wakeChecker, at run time: after every batch, a job that is on the index and was
//     not before must have had its type notified by that batch. It is attached to a
//     scenario that drives every path the engine has for putting a job there, and it
//     is proven able to fail.
//   - TestEveryEventThatReopensAJobIsBesideAWakeUp, in the source: every job event
//     that can put a job on the index is emitted by a function that also notifies. A
//     new such event, or a new function emitting one, fails it until it notifies or
//     says here why it need not.

// wakeChecker is a Metrics that, at each committed batch, compares the activatable
// index with the one after the previous batch and holds the batch to having notified
// every job type that gained a job.
//
// The processor reports BatchCommitted before it sends that batch's notifications, so
// a batch's debt is settled at the next BatchCommitted, or by settle after the run.
type wakeChecker struct {
	t     *testing.T
	store *state.Store
	// open is the activatable index after the last committed batch: job key → type.
	open map[uint64]int32
	// owed are the types the last committed batch put a job on the index for.
	owed map[int32][]uint64
	// told are the types notified since the last committed batch.
	told map[int32]bool
	// silent collects every batch that owed a wake-up it did not send.
	silent []string
}

func newWakeChecker(t *testing.T, store *state.Store) *wakeChecker {
	return &wakeChecker{t: t, store: store, open: map[uint64]int32{}, owed: map[int32][]uint64{}, told: map[int32]bool{}}
}

// attach makes the checker the processor's metrics and its job notifier.
func (w *wakeChecker) attach(p *engine.Processor) {
	p.SetMetrics(w)
	p.SetJobNotifier(func(jobType int32) { w.told[jobType] = true })
}

func (w *wakeChecker) BatchCommitted(engine.BatchStats) {
	w.settle()
	now := w.index()
	for key, jobType := range now {
		if _, was := w.open[key]; !was {
			w.owed[jobType] = append(w.owed[jobType], key)
		}
	}
	w.open = now
}

func (w *wakeChecker) SyncFailed()   {}
func (w *wakeChecker) CommitFailed() {}

// settle holds the last committed batch to its debt and starts a fresh one.
func (w *wakeChecker) settle() {
	for jobType, keys := range w.owed {
		if !w.told[jobType] {
			w.silent = append(w.silent, fmt.Sprintf("jobs %v of type %d became available and no worker waiting for the type was told", keys, jobType))
		}
	}
	w.owed = map[int32][]uint64{}
	w.told = map[int32]bool{}
}

// index reads the activatable index: every pullable job and its type.
func (w *wakeChecker) index() map[uint64]int32 {
	out := map[uint64]int32{}
	if err := w.store.AllActivatableJobs(func(key uint64) error {
		job, ok, err := w.store.GetJob(key)
		if err != nil {
			return err
		}
		if ok {
			out[key] = job.JobType
		}
		return nil
	}); err != nil {
		w.t.Fatalf("read the activatable index: %v", err)
	}
	return out
}

// run processes what is queued and settles the last batch.
func (w *wakeChecker) run(p *engine.Processor, what string) {
	w.t.Helper()
	if err := p.RunUntilIdle(); err != nil {
		w.t.Fatalf("%s: %v", what, err)
	}
	w.settle()
}

// tick fires the timers due on the clock and settles the last batch.
func (w *wakeChecker) tick(p *engine.Processor, what string) {
	w.t.Helper()
	if err := p.TickTimers(); err != nil {
		w.t.Fatalf("%s: %v", what, err)
	}
	w.run(p, what)
}

// driveEveryReopeningPath takes one job through every way the engine has of putting a
// job on the activatable index: creation, a retryable fail, a backoff elapsing, a
// lease running out, and an incident resolved. It reports what the checker saw.
func driveEveryReopeningPath(t *testing.T, silenceNotifier bool) []string {
	t.Helper()
	h := openHarness(t, t.TempDir())
	t.Cleanup(func() { h.close(t) })
	clk := &fixedClock{t: 1_000}
	cp, jobType := linearProcess(t)
	p := engine.New(1, h.log, h.store, clk)
	w := newWakeChecker(t, h.store)
	w.attach(p)
	if silenceNotifier {
		// What a path that forgets to notify looks like from outside.
		p.SetJobNotifier(func(int32) {})
	}
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	// Creation.
	p.CreateInstance(cp.Key)
	w.run(p, "create the instance")
	job := singleActivatableJob(t, h.store, jobType)

	// A retryable fail with no backoff hands the job straight back.
	p.ActivateJob(job, "worker-1", 60e9)
	w.run(p, "lease")
	p.FailJob(job, 3, "transient", 0)
	w.run(p, "fail with retries left")

	// A fail with a backoff holds it; the backoff elapsing hands it back.
	p.ActivateJob(job, "worker-1", 60e9)
	w.run(p, "lease")
	p.FailJob(job, 2, "transient", 5e9)
	w.run(p, "fail with a backoff")
	clk.t += 6e9
	w.tick(p, "backoff elapses")

	// A lease nobody reports on runs out and hands it back.
	p.ActivateJob(job, "worker-1", 10e9)
	w.run(p, "lease")
	clk.t += 11e9
	w.tick(p, "lease runs out")

	// An exhausting fail raises an incident; resolving it hands the job back.
	p.ActivateJob(job, "worker-1", 60e9)
	w.run(p, "lease")
	p.FailJob(job, 0, "broken", 0)
	w.run(p, "exhausting fail")
	if got := activatableJobs(t, h.store, jobType); len(got) != 0 {
		t.Fatalf("activatable after an exhausting fail = %v, want none", got)
	}
	ei, ok, err := h.store.GetJob(job)
	if err != nil || !ok {
		t.Fatalf("GetJob: %v, ok=%v", err, ok)
	}
	p.ResolveIncident(ei.ElementInstanceKey, 1)
	w.run(p, "resolve the incident")

	// The scenario is only worth its verdict if every step reached the index.
	if got := activatableJobs(t, h.store, jobType); len(got) != 1 || got[0] != job {
		t.Fatalf("activatable after the incident was resolved = %v, want the job back", got)
	}
	return w.silent
}

// TestAJobThatBecomesAvailableWakesItsWorkers holds every path that puts a job on the
// activatable index to notifying the job's type in the same batch.
func TestAJobThatBecomesAvailableWakesItsWorkers(t *testing.T) {
	if silent := driveEveryReopeningPath(t, false); len(silent) != 0 {
		t.Fatalf("a job became available without waking the workers waiting for its type:\n  %s",
			strings.Join(silent, "\n  "))
	}
}

// TestTheWakeCheckerCatchesASilentPath is the checker's own test: with the engine's
// notifications thrown away, every step that put the job on the index must be reported.
// A guard that cannot fail proves nothing.
func TestTheWakeCheckerCatchesASilentPath(t *testing.T) {
	// Creation, the retryable fail, the elapsed backoff, the expired lease and the
	// resolved incident.
	if silent := driveEveryReopeningPath(t, true); len(silent) != 5 {
		t.Fatalf("with no notifications the checker reported %d silent batches, want 5:\n  %s",
			len(silent), strings.Join(silent, "\n  "))
	}
}

// reopenExempt are the job intents applyToState re-puts that never move a job onto the
// activatable index, and why. Anything else it re-puts can, and is held to a wake-up.
var reopenExempt = map[string]string{
	"IntentJobActivated": "a lease takes the job off the index",
	"IntentJobAssigned":  "an assignment changes a user task's claimant and neither sets nor clears a hold",
}

// TestEveryEventThatReopensAJobIsBesideAWakeUp is the source-level half of the guard.
//
// The intents that can put a job on the index are read from applyToState itself — the
// case that calls tx.PutJob — so a new one appears here without anybody listing it.
// Every function in the engine that emits one of them through AppendJobEvent must also
// call NotifyJobAvailable. A function that notifies only on some of its paths passes:
// the run-time checker above is what holds the paths themselves.
func TestEveryEventThatReopensAJobIsBesideAWakeUp(t *testing.T) {
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var parsed []*ast.File
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		parsed = append(parsed, f)
	}

	reopening := putJobIntents(t, parsed)
	if len(reopening) == 0 {
		t.Fatal("found no job intent that applyToState re-puts with tx.PutJob — the guard is reading the wrong thing")
	}
	for intent := range reopenExempt {
		delete(reopening, intent)
	}

	var offenders []string
	emitters := 0
	for _, f := range parsed {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			var emits []string
			notifies := false
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch calledName(call) {
				case "NotifyJobAvailable":
					notifies = true
				case "AppendJobEvent":
					if len(call.Args) >= 2 {
						if intent := selectorName(call.Args[1]); reopening[intent] {
							emits = append(emits, intent)
						}
					}
				}
				return true
			})
			if len(emits) == 0 {
				continue
			}
			emitters++
			if !notifies {
				offenders = append(offenders, fmt.Sprintf("%s (%s) emits %s and never calls NotifyJobAvailable",
					fn.Name.Name, fset.Position(fn.Pos()), strings.Join(emits, ", ")))
			}
		}
	}
	if emitters == 0 {
		t.Fatal("found no function emitting a reopening job event — the guard is reading the wrong thing")
	}
	sort.Strings(offenders)
	if len(offenders) > 0 {
		t.Fatalf("a job event that can put a job on the activatable index is emitted without waking the workers waiting for it — notify, or record in reopenExempt why it cannot reopen a job:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

// putJobIntents returns the job intents of the applyToState case that calls tx.PutJob.
func putJobIntents(t *testing.T, files []*ast.File) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			clause, ok := n.(*ast.CaseClause)
			if !ok {
				return true
			}
			putsJob := false
			for _, stmt := range clause.Body {
				ast.Inspect(stmt, func(m ast.Node) bool {
					if call, ok := m.(*ast.CallExpr); ok && calledName(call) == "PutJob" {
						putsJob = true
					}
					return !putsJob
				})
			}
			if !putsJob {
				return true
			}
			for _, expr := range clause.List {
				if name := selectorName(expr); strings.HasPrefix(name, "IntentJob") {
					out[name] = true
				}
			}
			return true
		})
	}
	return out
}

// calledName is the method or function name a call expression calls.
func calledName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.SelectorExpr:
		return fn.Sel.Name
	case *ast.Ident:
		return fn.Name
	}
	return ""
}

// selectorName is the selected name of pkg.Name, or "".
func selectorName(expr ast.Expr) string {
	if sel, ok := expr.(*ast.SelectorExpr); ok {
		return sel.Sel.Name
	}
	return ""
}
