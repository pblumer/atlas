# ADR-DRAFT: A rule the compiler gains later is a gate on deploy, whatever stage it lives in

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-17
- **Deciders:** Patrick Blumer
- **Open question:** what keeps the next rule on the right side of this line. The split is
  a property of *where* a refusal is raised, and nothing in the tree states that for a rule
  that does not exist yet — a new compile-stage refusal will be fatal on reload again unless
  its author knows to think about it. A generic test cannot decide it either, because some
  compile failures genuinely have nothing to bring back. Left as a stated risk rather than
  guessed at; the nearest real answer is probably an upgrade preflight that compiles the
  stored deployment store under the new binary before the old container is stopped.
- **Question checked:** 2026-09

## Context and problem statement

ADR-0177 decided that reloading a stored definition skips the deploy-time validation gate: a
definition in the deployment store passed the gate of the day it was deployed and its
instances have been running under it since, so a rule added afterwards must be able to tell
the operator about it without being able to stop the server. It drew the line at "is there
something to bring back" — a record that does not decode, names no such process, or holds an
expression that will not compile is still fatal, because nothing can run it.

ADR-0388 then added a rule: a FEEL call to a name this build does not have compiles to a
constant null, silently, and is refused at deploy. The rule is right, and the reasoning for
it is sound. But it was implemented in `compiler.compileFEEL` — the one door every one of a
model's expressions goes through, which runs while the process is still being *built*,
several stages before validation.

That placement put it on the wrong side of ADR-0177's line without anybody deciding to. The
reload's escape hatch in `parseNamed` is written against exactly one shape:

```go
var ve *ValidationError
if !gated && errors.As(err, &ve) && ve.Process != nil {
    return ve.Process, ve.Problems, nil
}
```

Only stage 5 produces that, because only stage 5 runs on a process that already exists. A
refusal from the build stage arrives as a plain error, `loadDeployments` treats it as fatal
as ADR-0019 decided, and the process exits.

This is not hypothetical either. A production server was upgraded to a build carrying the
new rule. One stored deployment — a probe model whose script task called `get keys(...)`, a
function this build does not have — refused to compile. The server exited during startup,
the supervisor restarted it, and it exited again, with every other definition and roughly
fifty thousand running instances unreachable behind the one record. The deployment JSON on
disk was intact; the only thing wrong with it was a rule that did not exist when it was
written. That is precisely the outage ADR-0177 exists to prevent, arriving through a door it
did not cover.

The question: does the reload split belong to the *validation stage*, or to the *kind of
rule* — one the compiler gained after the definition was stored?

## Decision drivers

- **An upgrade must not be an outage.** ADR-0177's first driver, unchanged. It does not
  become less true because a rule was implemented in an earlier compile stage.
- **A gate still has to gate.** The refusal must keep its full force at deploy, where the
  author is watching. ADR-0388's whole value is that the silence stops.
- **The definition that comes back must be the one that ran.** Not a repaired one, not a
  partial one: the expression has to compile to the same constant null it has always
  evaluated to, or the reload would change behaviour on a restart.
- **Drift must stay visible.** A model that today's compiler refuses is still a model
  somebody has to fix.
- **The line must be cheap to hold.** A fix that threads a mode through half the compiler by
  hand is a fix that rots at the first new call site.

## Considered options

1. **Leave it, and quarantine the record operationally.** The deployment role can move the
   offending JSON out of the store and let the server boot without it.
2. **Move the rule into stage 5**, so it produces a `ValidationError` and the existing
   reload hatch covers it.
3. **Carry the decision through the compile as a gate value**: a deploy refuses, a reload
   compiles and reports.
4. **Widen the reload hatch** to catch any compile error and start without the definition.

## Decision outcome

Chosen option: **"Carry the decision through the compile as a gate value" (option 3)**.

`feelGate` is threaded from the parse entry point to `compileFEEL`, which becomes a method on
it. `Parse`, `ParseAll`, `ParseNamed` and the dry run use the shared strict gate and behave
exactly as before — the refusal, its message, and the element and field the call sites wrap
around it are untouched. `ReloadNamed` uses a tolerant gate: the expression compiles the way
the build that stored it compiled it, and the fault is recorded as a `feel.null-call`
Problem, returned beside the process. `loadDeployments` already logs exactly that, as
`event=deployment.reloaded_with_problems`, so the drift surfaces where operators already
look, and the server serves.

The general rule this records, beyond the one fix: **a rule the compiler gains after a
definition was stored is a gate on deploying that definition, never a condition for loading
it — and that holds regardless of which compile stage the rule is implemented in.** ADR-0177
stated the principle in terms of stage 5 because stage 5 was where the rules were. The
principle was never about the stage.

The line ADR-0177 drew does not move: a record that yields no compiled process at all is
still fatal. A call that can only be null is not that case, and the distinction is exactly
the one ADR-0177 named — *the engine compiles it*, to the constant null it has always
returned, so there is something to bring back and it is the thing that has been running.

The threading is deliberate rather than clever. The gate reaches `compileFEEL` through the
`Builder` the compile already carries everywhere, and through four helpers that gained a
parameter; the compiler enforces that every call site has one, so a future expression site
cannot quietly miss the decision the way an enumeration of expression fields would.

### Consequences

- **Positive:** a server holding a definition with such a call boots, keeps every other
  definition and every running instance, and names the model to fix in its log. The rule
  keeps its full force at deploy, with the same message and the same element anchor.
- **Positive:** the reload's report is now a list a caller can act on uniformly — a stage-5
  Problem and a null-call Problem arrive the same way.
- **Negative / trade-offs accepted:** the same asymmetry ADR-0177 already accepted, one rule
  wider. A definition running on a current server may contain a call today's compiler
  refuses; it is caught at its next deploy, not before. And it is a call that evaluates to
  null, so a gateway reading it takes its default flow — the very defect ADR-0388 was written
  about — *on a definition that has been doing that since it was deployed*. The reload does
  not make that worse; it also does not fix it, and the warning is the only thing pointing at
  it until somebody redeploys the model.
- **Negative:** the `feel.null-call` Problem has no element anchor. The element and field
  live in the call sites' error wrapping, which a tolerated fault never produces, so the
  Problem quotes the expression instead. ADR-0388 accepted the same limit for the refusal.
- **Follow-ups / risks to watch:** the open question above — nothing in the tree tells the
  author of the *next* compile-stage rule which side of the line it belongs on.

## Pros and cons of the options

### Option 1 — Leave it, quarantine operationally
- Good: no code change; the deployment role already has the move-the-record path, and it
  recovers a down server without touching instance state.
- Bad: it is a remedy, not a fix. It runs after the outage, by hand, on a server that is
  already down; the definition it removes is one whose instances then cannot advance (which
  is the objection ADR-0177 raised against quarantine in the first place); and the next
  stored model with the same fault repeats it, one record at a time.

### Option 2 — Move the rule into stage 5
- Good: the existing hatch covers it with no new mechanism, and the finding would gain a
  real element anchor in the Problems panel.
- Bad: stage 5 validates a *linearized graph*, and there is no path from that graph back to
  every FEEL expression a model carries without enumerating the fifty-odd fields that hold
  one. An enumeration is silently incomplete the first time a field is added, which turns a
  rule against silence into a silent rule.

### Option 3 — A gate value carried through the compile (chosen)
- Good: one decision, taken at the entry point, where the caller already knows whether this
  is a deploy or a reload; the deploy path is byte-for-byte unchanged; the compiler proves
  every call site was considered.
- Bad: a parameter threaded through four helpers and their callers — a wide, mechanical diff,
  and one more thing for a new expression site to pass.

### Option 4 — Widen the hatch to any compile error
- Good: one line, and no server ever fails to start on a stored definition.
- Bad: it erases the line ADR-0019 and ADR-0177 both drew. A record that yields no runnable
  process would be dropped silently, and the failure would move to the first instance that
  tried to advance — with no definition anywhere to explain why.

## Links

- refines [ADR-0177](0177-reload-skips-the-deploy-gate.md) — the split this generalizes from
  stage 5 to any rule the compiler gained later
- amends [ADR-0388](0388-a-call-that-can-only-be-null-is-refused-at-deploy.md) — the rule
  whose placement put it on the wrong side of that split
- relates to [ADR-0019](0019-durable-deployments.md) — the fatal-on-reload line that still
  holds for a record with nothing to bring back
- relates to [ADR-0026](0026-problems-panel-and-versioned-validation.md) — the Problem shape
  the reload reports through
