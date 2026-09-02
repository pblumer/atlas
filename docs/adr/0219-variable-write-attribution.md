# ADR-0219: Variable write attribution

- **Status:** Accepted
- **Date:** 2026-09-01
- **Deciders:** Atlas maintainers

## Context and problem statement

The Operations replay hangs a small in/out card under the selected element (ADR-0161):
what it was handed, and what it produced. The first half is a recorded fact — an
activity's `zeebe:ioMapping` inputs live in its own scope and are read straight off the
log (ADR-0068). The second half was not recorded anywhere. ADR-0159 added
`variablesAfter` (the variable fold at the element's *completion* position) beside
`variables` (the fold at its activation), and both the card's *out* section and the
Variables tab's "what this element contributed" marks inferred the answer as the
difference between the two — while ADR-0159 said, in its own words, that this "is not
attribution".

On a single branch the inference is right often enough to pass for the real thing. On
a fork it is wrong, and visibly so. A parallel gateway splitting into two tasks means
both are open at the same time, so each one's completion snapshot already contains
whatever the *other* branch wrote:

```
fork → "erstelle ein Ticket"  (writes newTicket)
     → "alle Tickets holen"   (writes tickets)
```

Selecting either task showed **both** `newTicket` and `tickets` under *out*. Reported
by an operator as: "why is `newTicket` shown here — that variable is produced by the
other task?" — and the answer was that nothing in the instance's history said which
element wrote it, so both diffs contained both writes. The same set is what the
Variables tab marks as the element's own contribution, so both surfaces told the same
wrong story.

No fold of the two snapshots can fix this: the writes of two concurrent branches are
interleaved in one instance-wide sequence, and "which element wrote this" is simply not
derivable from what the log holds. Nor is it recoverable from log positions — the
processor drains up to a batch's worth of commands per cycle and defers followups to the
next batch, so another branch's records legitimately sit between a task's own write and
its completion.

## Decision drivers

- **Say what happened, don't infer it.** The console asks a question about one element;
  the engine either records the answer or the surface must not claim one.
- **Invariant integrity.** Attribution must be a fact frozen into the event and rebuilt
  by replay (I4/I6), never re-derived, and must not add allocation to the command path
  (I1).
- **One stamping point.** A value copied out of one scope and rewritten into another —
  a call activity promoting its child's result, a loop promoting its body's — must not
  carry the producer of the write it was copied from. Attribution that has to be
  remembered at twenty call sites is attribution that will be wrong at one of them.
- **Existing history stays readable.** Instances already on disk have no attribution;
  reading them must not fail, and must not silently report "wrote nothing".
- **Worth new persistence.** ADR-0161 built the card as a pure read-side change — "these
  values are already on the log". That is precisely what stopped being true: the value
  the card needs is not on the log at all, so either the log carries it or the card keeps
  guessing.

## Considered options

1. **A `ProducerKey` on `VariableValue`** — the element instance whose processing wrote
   the value, stamped at command time and appended to the record.
2. **A sidecar attribution event per write**, as ADR-0098 did for operator overrides.
3. **Infer it in the API from log positions** — attribute a change to the element
   instance whose lifecycle record is nearest it.
4. **Show nothing** — drop the *out* section, leaving only the recorded input side.

## Decision outcome

Chosen option: **a `ProducerKey` on `VariableValue`**.

```go
type VariableValue struct {
	ScopeKey uint64
	Name     string
	Kind     VarKind
	Bool     bool
	Text     string
	ProducerKey uint64 // element instance whose processing wrote this; 0 = none
}
```

- It is an **appended field** in the codec, the shape ADR-0009 already uses for
  `JobValue.LeaseEpoch` and friends: a record written before it ends after `Text` and
  decodes to 0, so every instance already on disk still reads.
- It is stamped in **one place**, `ProcessingContext.AppendVariableEvent`, from a
  `producer` field on the context. `Processor.processOne` sets that field from the
  command: an element command names its element instance in `Key`, so every write an
  element's own behavior makes — io-mappings, a script's result, a mockup's result, a
  loop's promoted output — is attributed with no lookup and no allocation. The four
  handlers that write on behalf of a *different* element set it themselves:
  `handleJobCompleted` (a worker's result belongs to the task whose job it was, and the
  command names the job), `correlateMessage` and `broadcastSignal` (a payload belongs to
  the catch event it arrived on), and `resumeCaller` (a child instance's result is the
  call activity's output). Because the stamp overwrites unconditionally, a value copied
  between scopes can never inherit a stale producer.
- `0` means **no element wrote it**: the instance's start variables, an operator's
  override (ADR-0098 already records who did that one), or a record from before this
  decision.
- The timeline gains a per-step **`writes`** — the last value each element instance
  wrote, per (scope, name) — and the response a **`variableAttribution`** flag, true
  when the instance's history carries any producer at all. `variables` and
  `variablesAfter` are unchanged: they are snapshots of the whole instance, and on a
  fork they legitimately contain the sibling branch's work.
- The console's *out* section and the Variables tab's "what this element contributed"
  marks both read `writes` when the flag is set. For an instance recorded before this
  they keep the old inference and hang the reason on the section as a tooltip, so a
  pre-attribution replay says why a neighbour's variable may be listed instead of
  quietly reporting the wrong thing — or, worse, "wrote nothing".

### Consequences

- **Positive.** The question the card asks is now answered by the log rather than
  guessed from it, on forks, joins, loops, subprocesses and call activities alike. A
  pass-through element (a gateway, an event) correctly claims nothing, so the card stops
  appearing on elements that produce nothing. Attribution is replay-derived like every
  other fact, and costs one `uint64` per variable event and no allocation on the command
  path.
- **Negative / trade-offs accepted.**
  - **The core variable record grows.** ADR-0098 deliberately kept `VariableValue`
    untouched and put the operator's identity in a sidecar event. The trade-off inverts
    here: an actor is empty on virtually every write, while a producer is present on
    virtually every write, so a sidecar would double the event count on the path that
    writes variables — the opposite of what that ADR was protecting. Eight bytes on the
    record is the cheaper half.
  - **`writes` is a write list, not a change list.** An element that rewrites a value
    with the value it already had now appears under *out*; the old diff hid that. It
    did write it, and hiding it made a task that plainly ran look inert.
  - **History predating this decision keeps the old inference.** It is marked, not
    fixed: the log cannot be back-filled with a fact it never carried.
- **Follow-ups / risks to watch.** The producer is not yet surfaced outside the
  timeline — the instance variables endpoint could name the element that last wrote each
  value, which is the same fact asked from the other direction. A variable written by a
  behavior that neither an element command nor one of the four named handlers drives
  would silently attribute to nobody; the tests cover the paths that exist today, and a
  new one has to state its producer like the rest.

## Pros and cons of the options

### Option 1 — `ProducerKey` on `VariableValue` (chosen)
- Good: exact for every topology; one stamping point; appended field so old records
  still read; no allocation on the command path.
- Bad: touches the codec of the most-written value type; eight bytes per variable event.

### Option 2 — sidecar attribution event
- Good: leaves `VariableValue` and its codec untouched, exactly as ADR-0098 chose.
- Bad: one extra event per variable write — and unlike an override, a variable write is
  the common case, not a rare manual one. Doubling the record count of the variable path
  to carry eight bytes is the wrong trade.

### Option 3 — infer from log positions in the API
- Good: no engine or format change; works for history already on disk.
- Bad: not sound. Commands from other branches are processed between an element's write
  and its completion record, so the nearest lifecycle record is not reliably the writer.
  It would replace a wrong answer with a differently wrong answer, and one much harder
  to explain.

### Option 4 — show nothing
- Good: honest, and free.
- Bad: throws away the most useful thing the card says. "What did this task produce" is
  the question an operator opens the replay to answer.

## Links

- extends ADR-0161 (the element in/out card, whose *out* side this redefines)
- extends ADR-0159 (`variablesAfter`, and its note that the diff is not attribution)
- relates to ADR-0048 (per-step variable snapshots), ADR-0068 (io mappings and scopes),
  ADR-0098 (operator override audit), ADR-0009 (record serialization, appended fields)
