# ADR-DRAFT: A follow-up command is not durable, so a crash between two batches loses or strands a token

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

Deterministic, no load needed. `engine/zz_window_internal_test.go` drives a fresh
engine to a chosen crash point, drops it, and recovers from the log alone. Both
variants fail:

```
=== TestCrashBeforeActivatingLosesTheToken
    A: active element instances after recovery: map[]
    A: TOKEN LOST - catchB not active

=== TestCrashBeforeCompletingStrandsTheToken
    B: before publishing again: map[1:1]
    B: after publishing "go" again: map[1:1]
    B: INSTANCE STUCK - the message is consumed, the token sits on catchA, nothing moves it
```

Variant A fails identically whether recovery replays into a fresh store or
reopens the one the crashed process left, so the failure is not an artefact of how
the test simulates the crash.

The tests are deliberately **not** in the tree: they document a defect that has no
fix yet, and a repository carries neither a red test nor a skipped one. They
belong with the change that fixes it.

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

type winClock struct{ t int64 }

func (c *winClock) Now() int64 { c.t++; return c.t }

func winProcess(t *testing.T, key uint64) (*compiler.CompiledProcess, int32, int32) {
	t.Helper()
	b := compiler.NewBuilder(key, "window", 1)
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
	return cp, a, bb
}

// runUntilHead runs batches until the queue head matches want, then stops without
// processing it — the crash point.
func runUntilHead(t *testing.T, p *Processor, intent model.Intent, element int32) {
	t.Helper()
	for i := 0; i < 20 && len(p.queue) > 0; i++ {
		c := p.queue[0]
		if c.ValueType == model.VTElementInstance && c.Intent == intent && c.Value.element.ElementId == element {
			return
		}
		if err := p.processBatch(); err != nil {
			t.Fatalf("processBatch: %v", err)
		}
	}
	t.Fatalf("never reached %v of element %d (queue len %d)", intent, element, len(p.queue))
}

func activeByElement(t *testing.T, s *state.Store) map[int32]int {
	t.Helper()
	found := map[int32]int{}
	if err := s.ActiveElementInstances(func(k uint64, v *model.ElementInstanceValue) error {
		found[v.ElementId]++
		return nil
	}); err != nil {
		t.Fatalf("ActiveElementInstances: %v", err)
	}
	return found
}

// crashAt drives a fresh engine to the given crash point, then returns a recovered
// processor and its store, rebuilt from the log alone.
func crashAt(t *testing.T, intent model.Intent, element int32) (*Processor, *state.Store, int32, int32) {
	t.Helper()
	dir := t.TempDir()
	const defKey = 42
	cp, catchA, catchB := winProcess(t, defKey)

	log1, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store1, err := state.Open(filepath.Join(dir, "state1"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	p1 := New(1, log1, store1, &winClock{})
	p1.Deploy(cp)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(defKey)
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	p1.PublishMessage("go", "")
	runUntilHead(t, p1, intent, element)
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
	t.Cleanup(func() { _ = store2.Close(); _ = log2.Close() })
	p2 := New(1, log2, store2, &winClock{})
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2: %v", err)
	}
	return p2, store2, catchA, catchB
}

// A: crash between the predecessor's Completed and the successor's Activating.
func TestCrashBeforeActivatingLosesTheToken(t *testing.T) {
	_, store2, _, catchB := crashAt(t, model.IntentActivating, 2)
	found := activeByElement(t, store2)
	t.Logf("A: aktive Element-Instanzen nach Recovery: %v", found)
	if found[catchB] == 0 {
		t.Fatalf("A: TOKEN VERLOREN — catchB nicht aktiv; aktiv: %v", found)
	}
}

// B: crash between the correlation (which durably deletes the subscription) and the
// catch event's Completing. The token is still there, but nothing can move it again.
func TestCrashBeforeCompletingStrandsTheToken(t *testing.T) {
	p2, store2, catchA, catchB := crashAt(t, model.IntentCompleting, 1)
	t.Logf("B: vor dem zweiten Publish: %v", activeByElement(t, store2))
	p2.PublishMessage("go", "")
	if err := p2.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 2: %v", err)
	}
	found := activeByElement(t, store2)
	t.Logf("B: nach erneutem Publish von \"go\": %v (catchA=%d catchB=%d)", found, catchA, catchB)
	if found[catchB] == 0 {
		t.Fatalf("B: INSTANZ STECKT — die Nachricht ist verbraucht, der Token steht auf catchA und nichts bewegt ihn; aktiv: %v", found)
	}
}
```

## The defect is wider than one sequence flow

The first draft of this record scoped the hole to the handoff between a completing
element and its successor. Reading the command path shows that is one instance of
a class. Every in-flight transition is scheduled the same way —
`AppendElementCommand` appends to `p.followups`, and nothing in `followups` is
durable:

| caller | what the next batch owes |
|---|---|
| `activateElement` | the successor of a taken sequence flow |
| `armBoundaryEvents` | a host activity's boundary events |
| `seedMultiInstance` | a multi-instance body's iterations |
| `correlateMessage` | the `Completing` of the catch the message hit |
| job completion, timer fired, scope drained | the `Completing` of the waiting element |

So the window is not "between `Completed` and `Activated`". It is "between any
batch and the follow-ups it scheduled".

A second reproduction makes the consequence concrete, and it is worse than the
first. Crashing one batch earlier — between the correlation and the catch event's
`Completing` — leaves this:

```
B: active element instances after recovery: map[1:1]     token still on catchA
B: after publishing "go" again:             map[1:1]     nothing moves
```

`IntentSubscriptionCorrelated` deletes the message subscription in the batch that
correlates, durably. The `Completing` it scheduled dies with the process. The
token is still on the catch event, but its subscription is gone, so no further
publish can ever move it. The instance is stuck forever.

That variant is the dangerous one for operations: the token still exists, so every
counter balances. The arithmetic that exposed the lost tokens on server01 —
instances times tokens against the element totals — would never have shown it.

## What is not the cause

The error paths inside `processBatch` were the first suspicion and they are not
it. A failing `log.Sync` or `tx.Commit` returns *without* calling `advanceQueue`,
so the consumed commands stay in the queue and are processed again. That risks
duplicate events, not lost ones.

## Options

Option 1 below was chosen before the second reproduction existed. It fixes the
sequence-flow handoff and nothing else, so it is left here for the record but it
is no longer a candidate on its own.

1. **Keep the completing element instance alive until its successor commits.**
   Restores the handoff, but only the handoff. It does nothing for a boundary event
   that was never armed, a multi-instance body whose iterations were never seeded,
   or the stranded catch event above — each would need its own bespoke pending
   record. Generalised over every follow-up kind, it *becomes* option 3 with extra
   steps.

2. **Drain follow-ups inside the batch.** Phase 1 keeps consuming until no
   follow-ups remain, so one batch spans the whole chain from an external command
   to the next durable resting point — a parked element instance, an open job, an
   armed timer or subscription — and one fsync covers it. A crash then either
   loses the whole chain (and with it the external command, which was never
   acknowledged) or none of it. This is the "a record batch ends at a wait state"
   design, and it likely *reduces* fsyncs.

   It is not complete on its own. A chain that does not reach a wait state within
   the batch cap has to commit and carry the rest, which reopens the same window
   for that case. `maxBatchSize` today bounds commands consumed, not follow-ups
   produced; a 5,000-iteration multi-instance fan-out is one batch's worth of
   follow-ups.

3. **Persist the pending follow-ups with the batch and re-queue them on
   recovery** — a durable outbox, one record per batch rather than one per
   transition. This is the only option that closes the window for every follow-up
   kind, including the overflow case option 2 leaves open.

   It is also the one that touches an invariant. I6 says commands are never
   persisted and never replayed. The outbox can be read as persisting an *effect*
   the committed batch owes rather than a command a client submitted — but that is
   a reading, and the invariant deserves to be amended explicitly rather than
   quietly reinterpreted.

**Recommendation:** option 3, optionally with option 2 layered on as an
optimisation once the correctness hole is closed. Option 3 needs a decision on I6
first, which is why this record stops here rather than carrying a patch.

## Consequences

Until this is fixed, any crash or hard restart under load silently drops every
token that is mid-transition at that instant, and strands every instance whose
trigger was consumed in the batch before. Neither raises an incident. On server01
the first kind cost 1,850 identities that can no longer be given an exit date; the
second kind leaves no arithmetic trace at all, so how many instances it has
stranded there is not known.

The 1,850 affected instances on server01 are left as they are, as evidence.
