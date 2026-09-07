# ADR-DRAFT: Token lost between the batch that completes an element and the batch that activates its successor

- **Status:** Draft
- **Date:** 2026-09-07
- **Deciders:** Atlas engine team

## Context and problem statement

A September 2026 identity-lifecycle test on server01 lost **1,850 tokens** out of
149,889. The deficit is stable and shows up in two independent measures:

```
49,963 active instances x 3 tokens = 149,889 expected
                        actual     = 148,039        (-1,850)

s_aktivieren  47,210 activations, 0 tokens parked, 0 terminated
rt_aktiv      45,360 activations                   (-1,850)
```

Both elements' own books balance — every token leaving `rt_erfasst` reached
`s_aktivieren`, and every activation of `rt_aktiv` is accounted for by its parked
tokens plus its outflow. The loss sits on a single sequence flow, between a
script task and the receive task after it.

A replay timeline of an affected instance (`MT-36000`) shows the shape exactly:

```
rt_erfasst    09-04 07:59:54 -> 09-07 12:58:50   consumed its message
s_aktivieren  09-07 12:58:52 -> 09-07 12:59:24   Completed
                                                  nothing after it
open element instances: c_service, c_mutation     the lane token is gone
```

`s_aktivieren` reached `Completed`. `rt_aktiv` was never activated. No incident
was raised, the instance is still active, and it can never receive
`identitaet-austritt` again — it is a zombie that keeps its parallel event hub
running forever. The 32 seconds the script task took is the signature of a
saturated engine: the loss happened during a bulk publish of 9,000 messages
fired while the previous burst was still draining, and the proxy returned 502s
in the same window.

## What actually happens

`completeAndTakeFlows` writes the predecessor's `Completed` into `batchRecords` —
durable, fsynced with the batch — and hands the successor's activation to
`activateElement`, which appends an `IntentActivating` **command** to
`p.followups`. Followups are merged into the queue by `advanceQueue` *after* the
commit, so they are processed by the **next** batch, behind its own fsync.

Between those two batches the token exists only as an in-memory command. And
`Command`'s own doc comment states the rule that makes this fatal:

> Commands are processed but never persisted (only the events they produce are);
> on recovery they are not replayed (invariant I6).

`applyToState` deletes the completed element instance when it applies
`Completed`, so after a crash there is no state left that says a flow was
pending. Nothing re-derives it. The token is simply gone.

This contradicts the guarantee ADR-0108 states for cancellation, which the engine
relies on generally:

> commands are not replayed (only events are). **Every waiting point is a durable
> event-derived state** ... A crash mid-cancellation recovers to the same waiting
> state and continues.

The window between `Completed` and the successor's `Activated` is not a waiting
point, and it is not durable. Every sequence flow in every model passes through
it.

## Reproduction

Deterministic, three batches, no load needed. `engine/zz_handoff_internal_test.go`:

```go
package engine

import (
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

type stepClock struct{ t int64 }

func (c *stepClock) Now() int64 { c.t++; return c.t }

// handoff: start -> catchA("go") -> catchB("never") -> end.
// catchA completing hands the token to catchB, exactly like s_aktivieren -> rt_aktiv.
func handoffProcess(t *testing.T, key uint64) (*compiler.CompiledProcess, int32) {
	t.Helper()
	b := compiler.NewBuilder(key, "handoff", 1)
	st := b.AddStartEvent()
	a := b.AddMessageCatchEvent("go", nil)
	bb := b.AddMessageCatchEvent("never", nil)
	end := b.AddEndEvent()
	b.Connect(st, a)
	b.Connect(a, bb)
	b.Connect(bb, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, bb
}

func TestTokenSurvivesCrashBetweenCompletedAndActivating(t *testing.T) {
	dir := t.TempDir()
	const defKey = 42
	cp, catchB := handoffProcess(t, defKey)

	log1, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store1, err := state.Open(filepath.Join(dir, "state1"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	p1 := New(1, log1, store1, &stepClock{})
	p1.Deploy(cp)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(defKey)
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	p1.PublishMessage("go", "")
	// Run batch by batch and stop *before* the batch that would activate catchB.
	stopped := false
	for i := 0; i < 20 && len(p1.queue) > 0; i++ {
		c := p1.queue[0]
		if c.ValueType == model.VTElementInstance && c.Intent == model.IntentActivating && c.Value.element.ElementId == catchB {
			stopped = true
			break
		}
		if err := p1.processBatch(); err != nil {
			t.Fatalf("processBatch: %v", err)
		}
	}
	if !stopped {
		t.Fatalf("never reached the pending activation of catchB (queue len %d)", len(p1.queue))
	}
	// Crash: the log holds every committed event; the pending command dies with the process.
	_ = store1.Close()
	_ = log1.Close()

	log2, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open 2: %v", err)
	}
	store2, err := state.Open(filepath.Join(dir, "state1"))
	if err != nil {
		t.Fatalf("state.Open 2: %v", err)
	}
	defer func() { _ = store2.Close(); _ = log2.Close() }()
	p2 := New(1, log2, store2, &stepClock{})
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2: %v", err)
	}

	found := map[int32]int{}
	if err := store2.ActiveElementInstances(func(k uint64, v *model.ElementInstanceValue) error {
		found[v.ElementId]++
		return nil
	}); err != nil {
		t.Fatalf("ActiveElementInstances: %v", err)
	}
	if found[catchB] == 0 {
		t.Fatalf("token lost: catchB (%d) is not active after recovery; active: %v", catchB, found)
	}
}
```

Output:

```
batch 0: head vt=Message         intent=MessagePublished element=0
batch 1: head vt=ElementInstance intent=Completing      element=1
batch 2: head vt=ElementInstance intent=Activating      element=2   <- crash here
active element instances after recovery: map[]
--- FAIL: token lost
```

It fails identically whether recovery replays into a fresh store or reopens the
store the crashed process left behind, so the failure is not an artefact of how
the test simulates the crash.

The test is deliberately **not** in the tree: it documents a defect that has no
fix yet, and a repository does not carry a red test or a skipped one. It belongs
with the change that fixes it.

## What is not the cause

The error paths inside `processBatch` were the first suspicion and they are not
it. A failing `log.Sync` or `tx.Commit` returns *without* calling `advanceQueue`,
so the consumed commands stay in the queue and are processed again. That risks
duplicate events, not lost ones.

## Options

1. **Keep the completing element instance alive until its successor commits.**
   Do not delete on `Completed`; delete when the successor's `Activated` is
   applied. Recovery then finds an element instance in a completed state and
   re-drives `takeOutgoingFlows`. This restores the ADR-0108 property — every
   point a token can rest at is durable event-derived state — and leaves I6
   untouched, at the cost of one extra live record per in-flight transition and a
   recovery pass that re-drives them.

2. **Emit the successor's `Activated` in the same batch as the predecessor's
   `Completed`.** No window at all. It means draining followups within the batch
   rather than after it, which changes what `maxBatchSize` bounds and how the
   group commit is reasoned about (ADR-0005).

3. **Persist the pending followups with the batch and re-queue them on
   recovery** — a durable outbox. Simple, but it makes commands durable, which is
   exactly what I6 says they are not.

Option 1 looks right: it is the smallest change that restores the stated
guarantee, and it keeps the distinction between events and commands intact.

## Consequences

Until this is fixed, any crash or hard restart under load silently drops every
token that is mid-transition at that instant, with no incident and no trace other
than an instance that stops moving. On server01 that was 1,850 identities that
can no longer be given an exit date.

The 1,850 affected instances on server01 are left as they are, as evidence.
