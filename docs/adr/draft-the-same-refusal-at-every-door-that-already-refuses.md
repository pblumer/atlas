# ADR-DRAFT: The same refusal at every door that already refuses

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-17
- **Deciders:** Patrick Blumer

## Context and problem statement

[ADR-0388](0388-a-call-that-can-only-be-null-is-refused-at-deploy.md) made a BPMN deploy
refuse a call this build can only ever answer with null — an unknown function name, or a
built-in called with an argument count its signature cannot take. It left the other FEEL
surfaces open, and gave a reason:

> The reasoning carries; what does not carry is the moment, since none of them has a deploy
> at which refusing is free.

**That reason is wrong.** Reading the code rather than reasoning about it, three of those
surfaces already have the moment, and all three already use it — each refuses an expression
that fails to compile, at the point somebody writes it:

| surface | already refuses | said nothing about |
| --- | --- | --- |
| `POST /api/v1/feel/validate` | `{"ok": false, "error": …}` on a parse error | a call that can only be null |
| an inbound worker's correlation key | `400 correlationKey is not a valid FEEL expression` | the same |
| playground rules | `rule N selects cases with …, which is not an expression` | the same |

Only this one class of fault walked through a gate that was already standing. The gap was
never about the moment; it was that `CheckCalls` had one caller.

One of the three is worse than silent. `/api/v1/feel/validate` exists to answer whether an
expression is valid, and it answered **`{"ok": true}`** for `is defined(x)`. Somebody who
asked exactly the right question was told the wrong answer, which is worse than never having
been asked — a "no" leaves you looking, a "yes" gives you a reason to stop.

The consequence differs by surface and is quietest where it matters most. A correlation key
that evaluates to null correlates an incoming message to nothing: events arrive, the sender
gets its 2xx, the worker reports healthy, and no instance is ever woken. There is nothing to
see anywhere.

## Decision drivers

- **The same fault by a different road is the same fault.** It should meet the same answer,
  in the shape that surface already uses for a syntax error, not a new one.
- **A route named `validate` must not certify.** No statement is a gap; a wrong statement is
  a defect.
- **The refusal must be where somebody can act on it.** For a correlation key that is
  registration: by the time an event arrives there is nobody to tell.
- **DMN's totality stays untouched**, as in ADR-0388. Nothing here changes evaluation, and
  `/api/v1/feel/evaluate` still reports the null the engine produces.
- **A check that cannot fire is not a guard, it is a claim.** A reader takes it to mean
  untrusted text reaches that line.

## Considered options

1. **Put `expr.CheckCallsError` in front of every remaining `CompileAuto`** in the tree.
2. **Put it in front of each surface that already refuses**, in that surface's own shape.
3. **Move the check into `expr.CompileAuto`** so no caller has to remember.
4. **Leave them as they are** and let the BPMN deploy be the only place that says so.

## Decision outcome

Chosen option: **"Put it in front of each surface that already refuses"** — with one surface
removed from the list on inspection, and one deliberately left out.

**Refused now**, each in the shape it already uses:

- `POST /api/v1/feel/validate` → `{"ok": false, "error": …}`, the message naming the standard
  equivalent where there is one.
- An inbound worker's correlation key, on create and on update → `400`.
- Playground rules, both `when` and `then` → the rule is refused where it is written.

**Task-folder rules are not refused at run time, because no such rule can be built.** ADR-0388's
open question named them, and the issue that became this record named them too. Both were
wrong about the shape of that surface: a folder rule's expression is *generated* from a
closed catalogue, and every value reaching it is a string literal this package escapes, an
integer bounded by `Validate`, or a duration held to a pattern. No text a caller sends
becomes a call. A guard in `Compile` could therefore never fire, and a guard that cannot fire
is worse than none: it tells the next reader that untrusted FEEL arrives there.

What is real on that surface is a property of the *catalogue* — that no operator generates a
call this build cannot make — and it is pinned where it can actually break, in the test that
already holds every advertised field/operator pair against the generator. An operator added
later that emitted such a call fails the build, which is the right place: a catalogue bug is
not something the person saving a folder can fix.

**The inbound bridge keeps compiling without the check**, also deliberately. It compiles a
correlation key that is already stored, and registration is where it was refused. Repeating
the check there would silently drop a subscription written before the gate existed — the
exact failure the gate is meant to prevent, arriving from the other side.

**`POST /api/v1/feel/evaluate` is untouched.** Evaluating is evaluating: if the expression
yields null, null is the honest answer, and it is the answer the engine really gives.
Reporting the fault *beside* that result may well be worth doing, but it is a different
decision from refusing and it is not folded in here.

### Consequences

- **Positive:** the surface that made a false statement now makes a true one, and the Modeler
  shows the message while the expression is being typed.
- **Positive:** a correlation key that could never have woken anything is refused at the only
  moment there is somebody to tell.
- **Positive:** the reading lives in one place. `expr.CheckCalls` has five callers and one
  rule; there is no second copy to drift.
- **Negative / trade-offs accepted:** an inbound subscription that exists today with such a
  key can no longer be *edited* without fixing the key. It never worked, but the refusal
  arrives when somebody changes something unrelated.
- **Negative:** the check is now in front of four more compiles, so a false refusal reaches
  four more places. It errs quiet for that reason — anything a reading could bind to a
  function is left alone — but the exposure is wider than it was.
- **Follow-ups / risks to watch:** whether `/feel/evaluate` should report the fault beside
  its null. And, as in ADR-0388, if the BPMN path ever gains a way to register extra
  functions, this check has to be told or it will refuse a call that works.

## Pros and cons of the options

### Option 1 — Every remaining `CompileAuto`
- Good: no surface can be forgotten.
- Bad: several of those call sites compile text the product generated, not text a person
  wrote. A refusal there has no author to tell and no shape to take.

### Option 2 — Each surface that already refuses
- Good: the answer shape is decided already; this adds a fault to an existing refusal rather
  than inventing a new one. And it puts the refusal where somebody can still act.
- Bad: the list is by inspection, so a surface added later has to be remembered.

### Option 3 — Inside `expr.CompileAuto`
- Good: one line, nothing to remember.
- Bad: the same defect ADR-0388 rejected it for — `expr` is what the DMN side compiles
  through, and DMN requires the call to stay executable.

### Option 4 — Leave them
- Good: nothing that works today stops working.
- Bad: it leaves `validate` certifying an expression that cannot work, which is the one
  behaviour in this class that actively misleads.

## Links

- extends [ADR-0388](0388-a-call-that-can-only-be-null-is-refused-at-deploy.md) — the refusal
  and the reading it uses; this record answers, and partly corrects, the question that one
  left open
- relates to [ADR-0008](0008-feel-expression-strategy.md) — compiling once, where it is
  written
- relates to [ADR-0015](0015-reuse-feel-engine.md) — the engine this leaves alone
