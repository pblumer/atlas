# ADR-0307: The milestone event compiles

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-11
- **Deciders:** Atlas maintainers

## Context and problem statement

The business-architecture method ([ADR-0305](0305-business-capabilities-and-value-streams.md))
measures a value stream by the points a case passes through. Some of those points are
activities, and an activity is recorded anyway: Atlas writes an element event for every
activation and completion, so "when did this case reach underwriting" is already
answerable wherever a task sits there.

Some of them are not. *Identity verification started* is a point on the path, not work
somebody does — the work is what follows it. BPMN's marker for exactly this is a **none
intermediate throw event**: an intermediate throw event carrying no event definition. It
throws nothing, waits for nothing, and its whole product is the fact that a token reached
it.

Atlas refused it:

```
compiler: intermediate throw event "m": only message, signal, compensation,
escalation, and link events are supported yet
```

Refused at deploy, not at author time. `UNSUPPORTED_TYPES` in the Modeler lists the
elements bpmn-js can draw that the engine cannot run, and this one was never in it, so the
Modeler drew a milestone, validated it, and let the author find out at Deploy.

The refusal was not a decision anybody made about milestones. The throw-event branch
handled the four definitions that carry semantics — message, signal, compensation,
escalation — plus link, and everything that fell through was an error, because at the time
everything that fell through *was* one.

## Decision drivers

- The element has no execution semantics, so the implementation is a pass-through node,
  which the compiler already has two of (link throw, link catch).
- Recording is what it is for. Whatever it compiles to must write its own element events,
  or the element is indistinguishable from not having drawn it.
- A refusal that becomes an acceptance must not widen silently. "No definition this
  compiler implements" and "no definition at all" were one state and both were refused;
  if the second one starts compiling, the first must not come along with it.
- `applyToState` is untouched. This is a compile-time and dispatch-table change; nothing
  about recovery, batching or the hot path moves.

## Considered options

1. **A pass-through node of its own type.** A new `TypeNoneThrowEvent`, dispatched to the
   existing `passThroughBehavior`.
2. **Reuse `TypeTask`,** which is already a pass-through for an undefined task. No new
   type, no new dispatch entry.
3. **Reuse `TypeLinkThrowEvent`,** the nearest existing throw that is a pass-through.
4. **Leave it refused and document the substitutes** — an embedded subprocess per phase,
   or the nearest named task — which is what
   [`business-architecture.md`](../architecture/business-architecture.md) said to do.

## Decision outcome

Chosen option: **"a pass-through node of its own type"**.

Options 2 and 3 are cheaper by exactly one enum constant and one dispatch entry, and both
buy that by making the compiled process lie about what the model says. Everything that
reads a compiled node back reads its type: the Operations overlay, the step-by-step
replay, the process documentation, a migration plan matching elements across versions. A
milestone stored as a task would be drawn and described as a task, and a milestone stored
as a link throw would be a link throw with no link name — which the link resolution pass
would then refuse. The type is the model's own statement about what the element is, and
there is no reason to keep a second, wrong one.

Option 4 is what this record overturns. The substitutes it named are real and stay real —
a phase boundary is usually better modelled as a subprocess, because a phase has a
duration and a milestone does not — but they are advice about modelling, and the
dedicated marker earns its place precisely where no task and no phase boundary sits.

### What it compiles to, and why that is not nothing

`AddNoneThrowEvent` adds a node with no detail, and `passThroughBehavior` completes it on
activation and takes its outgoing flow. That is the same execution as having drawn no
element at all.

The difference is the record. Activation and completion write element events under the
element's own id, so the per-definition visit counters
([ADR-0080](0080-runtime-aggregate-counters.md)) count it, the instance's step trail
([ADR-0046](0046-single-process-step-replay.md)) carries it in order, and the Operations
overlay lights it up. That is the entire point of the element, and it comes from the
event stream rather than from any behaviour the node has.

### Unlike a link throw, it keeps its edge

A link throw's outgoing flow is synthetic: `connectScope` wires it to the catch of the
same name and the XML draws no edge from it. A none throw is a point on the path, so it
keeps the sequence flow the model drew. Nothing in the compile needs to say so — it is
what happens when no pass rewrites its edges — but it is the thing a reader coming from
the link events expects to be true and it is worth stating once.

### An unmatched event definition is refused by suffix, not by a list

This is the part that makes the change safe, and it is a decision rather than a detail.

Go's `encoding/xml` drops child elements no field declares. Once "nothing matched" compiles
to a none throw, `<intermediateThrowEvent><timerEventDefinition/></intermediateThrowEvent>`
would compile to a pass-through: a model asking to wait five minutes, silently running
straight through. A timer on a throw event is invalid BPMN, but "invalid, and so nobody
will write it" is not a property this compiler can rely on, and a wrong answer is worse
than a refusal.

So the parsed event captures every unmatched child for its name alone, and a child whose
local name ends in `EventDefinition` is refused, naming it.

The alternative was to enumerate the definitions that are wrong here — timer, conditional,
error, cancel, terminate. That list is correct today and goes stale the first time BPMN or
this compiler grows one more, and it goes stale silently, in the direction of accepting
something. The suffix needs no maintenance: a definition none of the five fields matched
is, by construction, one this compiler does not implement on a throw event, whatever it is
called.

The cost is that the rule keys off a naming convention. It is BPMN's own and it is
universal in the specification, so the risk is an element that *ends* in `EventDefinition`
and is legitimately not one. None exists; if one ever does, it is a field on the struct
like the other five.

## Consequences

- **Good:** the method's milestone marker works, and a Modeler round trip that draws one
  now deploys instead of failing.
- **Good:** an intermediate throw event carrying a definition the engine does not
  implement is now refused by name — `<timerEventDefinition> is not supported here` — where
  it previously produced a list of what *is* supported and left the author to diff it.
- **Neutral:** the Modeler needed no change. The element was never in `UNSUPPORTED_TYPES`,
  and the token simulation already treated a definition-less throw as a pass-through
  marker, so both were already describing the behaviour this record makes true.
- **Bad, and accepted:** a milestone is free to draw and free to run, so a model can
  accumulate them until the overlay is a wall of circles. Nothing here rations them, and
  nothing should: the judgement of whether a point is worth naming belongs to whoever is
  measuring the value stream.
- **Not done:** no conformance scenario. The suite covers a core set and the elements
  landed since — link, escalation, conditional, ad-hoc — carry none either; adding one
  only here would be inconsistent rather than thorough. Replay equivalence for the element
  is asserted directly in `engine/nonethrow_test.go`.
