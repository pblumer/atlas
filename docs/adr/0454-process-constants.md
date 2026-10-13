# ADR-0454: Process constants are seeded with every instance and written by nothing else

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-10-13
- **Deciders:** Atlas maintainers

## Context and problem statement

A process often needs values nobody enters: a domain suffix, a language code, the id of
an app registration a script signs in with. Until now a model had three places to put
one, and each was wrong in its own way.

- **A start variable** (`<atlas:startVariable>`, ADR-0028's typed start form). It is an
  *input*: the start surfaces ask for it, and since a start form no longer drops the
  declaration, they ask beneath the form. Its default is applied by those surfaces only,
  so an instance created through the API, a public link, a timer, a message, a signal or
  a call activity gets nothing — and a script reading it gets null, with nothing on any
  screen saying why. That is how this record started: a PowerShell task wrote
  `[null,null,null]` because three "start variables" nobody was ever asked for were never
  set.
- **An input mapping** on the task that needs the value (ADR-0068). It works on every
  path, but it lives on one task; a second task needing the same value repeats it, and
  the repetitions drift.
- **A FEEL script task** that writes the values after the start. It works on every path
  too, but it is a step in the diagram that is not a step of the process, and nothing
  stops a later task or a worker from overwriting what it wrote.

The question: **how does a model state a value that every instance of a process starts
with, that nobody is asked for, that every expression and every worker can read, and that
nothing in the run can change by accident?**

## Decision drivers

- **Every start path, one rule.** An instance started by hand, by the API, by a public
  link, by a timer, a message, a signal, a call activity or a fork must carry the same
  constants. A rule that holds on some paths is how the null above happened.
- **Readable like any variable.** FEEL conditions, mappings, scripts and Worker Types
  already read the scope chain. A constant that needs a second way of being read is a
  constant every one of them has to learn.
- **Nobody is asked.** A constant is not an input; no start surface renders it.
- **Read-only to the run.** A value that a worker's result or a message payload can
  replace is a default, not a constant.
- **The invariants.** The value is decided at deploy (I5), frozen into the creation
  events (I6), replayed by the one `applyToState` (I4), and costs a process that declares
  no constants nothing on the hot path (I1).
- **An escape hatch.** A constant that turns out wrong in a running instance must be
  correctable, and the correction must be on the record.

## Considered options

1. **Start variables with a "hidden" flag**, their default applied by the server on every
   start path.
2. **Constants resolved at read time** from the compiled process, as a lowest-priority
   scope below the instance's own — never stored per instance.
3. **Constants materialised at instance creation**: declared on the process, compiled at
   deploy, written as ordinary process-scope variables by the one function that creates
   every instance, and protected from every later write but an operator's.

## Decision outcome

Chosen option: **3 — constants are declared on the process and seeded with every
instance**, because it is the only option that holds on every start path while every
reader keeps reading variables the way it already does.

### The declaration

```xml
<bpmn:process id="ucc-collective-line">
  <bpmn:extensionElements>
    <atlas:constants>
      <atlas:constant name="DomainSuffix" type="string" value="contoso.ch"/>
      <atlas:constant name="MaxAgents" type="number" value="20"/>
      <atlas:constant name="Routing" type="json" value="{&quot;mode&quot;:&quot;round-robin&quot;}"/>
    </atlas:constants>
  </bpmn:extensionElements>
  …
```

- `type` is one of the four a start variable takes — `string`, `number`, `boolean`,
  `json` — and `value` is a literal of that type, not an expression. A constant that
  computes is a script task.
- Each pool of a collaboration declares its own, on the process it executes.
- The Modeler edits them in the process panel's Implement tab, beside the start
  variables, and lists them in the Variables panel and in FEEL completion as
  `constant`.

### The deploy

The compiler parses each value into the same canonical form any other variable takes
(`expr.FromJSON` → `expr.Classify`, the path a script's result takes), so the engine
writes a value it never has to parse (I5). The deploy is **refused** for:

- a constant without a name, a name declared twice, an unknown type, or a value that is
  not a literal of its type;
- a name that is also a declared start variable — it cannot be both asked for and fixed;
- a name declared personal or as the data subject (ADR-0314) — a value written into the
  model is in the clear in every copy of the model, which is the opposite of what that
  declaration promises;
- a model element that would write the name: a script, job-script, business-rule, Worker
  Type or mockup task's result variable, a multi-instance output collection or element
  variable, an ad-hoc result collection, and an input or output mapping whose target is
  the name — unless its source is the name itself (`=DomainSuffix`), which hands the
  value on unchanged, the way a call activity passes a constant into its child.

The refusal is where a mistake is cheapest. Unlike the start-variable declaration, which
had always been accepted as written and so must never start refusing a stored model, a
constant is new: no model that deployed before carries one.

### The run

`activateInstance` — the one function every creation and every fork comes through
(ADR-0389) — writes the constants as `VariableCreated` events into the instance scope
**before** the start variables. The values are the compiled ones, frozen into the events,
so replay re-applies exactly what the live run wrote (I4, I6).

Afterwards, a write of a constant's name into the instance root is **dropped** while the
constant is there: no event is written, and the writer is told the write happened, so
nothing parks. That one rule covers every path:

- a start variable or a message-start payload naming a constant — the constant was
  written first, so the caller's value is dropped;
- a worker's result, a completed task form, a correlated message's payload;
- a call activity returning its child's variables — the caller keeps its own constant
  even when the child has a constant of the same name with another value.

A write into an activity-local scope is not touched: shadowing a name inside one
activity changes nothing the instance holds, and the deploy already refuses the model's
own mappings onto the name.

**An operator's correction goes through** (ADR-0095). It is the escape hatch for a
constant that is wrong in an instance already running, and it is audited (ADR-0098), so
the instance's record says who changed the value it was started with. A delete is never
refused either, for the reason ADR-0294 gives for not refusing one.

A process that declares no constants pays one length check per variable write, the same
as one that declares nothing searchable.

### Consequences

- **Positive:** A value nobody enters is no longer an input the start surfaces ask for,
  and it is present on every start path, so the null that prompted this cannot happen
  through a constant. Every reader sees it as an ordinary variable — FEEL, mappings,
  scripts, Worker Types, the Variables panel, the instance search — and the Operations
  view shows it with every other value.
- **Negative / trade-offs accepted:**
  - Each instance stores its constants. That is the price of being readable everywhere
    without a second lookup, and it is also what makes the record say, years later,
    which value an instance ran with.
  - A dropped write is silent. It is the honest outcome for the cases that actually
    occur — a call activity echoing the caller's constant back, a worker returning its
    input — and a model that names a constant as a write target is refused at deploy,
    where the mistake is cheapest to see.
  - An instance keeps the constants of the version it was started on. Migrating it to a
    version with other constants (ADR-0162) does not rewrite them; an operator who needs
    the new value sets it, on the record.
  - A constant is not a secret. It is in the model, in the log, in every export. A
    credential belongs in the vault (ADR-0069), referenced by a Worker.
- **Follow-ups / risks to watch:**
  - **Values per installation.** A constant whose value differs between DEV, INT and
    PROD cannot be stated in the model without three models, which ADR-0371 rules out:
    the same BPMN must deploy everywhere. ADR-0453
    answers it: an application holds named values per installation, and a constant binds
    to one by name (`source="application"`). What this record fixed for it still holds —
    the value is resolved where the instance is created and frozen into the creation
    event, so it has to be something the engine reads while it processes the command, not
    something the API layer adds to an API start, which a timer or message start never
    passes through.
  - **Visibility.** A constant is visible wherever a variable is. Atlas has no
    per-variable visibility, and introducing one for constants alone would make two
    kinds of variable that read differently. If an installation needs to keep a value
    from a script's environment, that is a property of the value — a secret — and the
    vault already answers it.

## Pros and cons of the options

### Option 1 — start variables with a "hidden" flag
- Good: The smallest change: a flag on an existing declaration, and a server-side
  default.
- Bad: It makes one declaration mean two opposite things — an input somebody supplies,
  and a fixed value nobody may — and the API would still accept a caller's value for it,
  so the "constant" is a default under another name.
- Bad: The default would have to be applied on paths the API never sees: a timer, a
  message, a signal start. That is engine work either way, so the flag buys nothing over
  option 3 except the confusion.

### Option 2 — resolve at read time, store nothing per instance
- Good: No per-instance storage, and nothing a write could overwrite, because nothing is
  stored.
- Bad: Every reader of variables would need a second source: FEEL evaluation in the
  engine, the scope walk that hands a script its input, the variables API, the instance
  search, the exporter. Each of those is a place for the two sources to disagree.
- Bad: The record would no longer say which value an instance ran with; it would say
  which version it ran on, and the reader would have to find that version's model.

### Option 3 — materialise at creation, protect afterwards (chosen)
- Good: One writer, one rule, every path; readers unchanged.
- Good: The value an instance ran with is in its own record.
- Bad: Per-instance storage, and a silent drop for a conflicting write (see above).

## Links

- relates to [ADR-0028](0028-forms-and-the-tasks-app.md) (start forms and the start
  variables they now sit beside), [ADR-0068](0068-task-io-variable-mappings.md) (input and
  output mappings), [ADR-0076](0076-call-activities.md) (call activities and variable
  propagation), [ADR-0389](0389-forked-instance-migration.md) (forks come through the same
  creation)
- the operator's override is [ADR-0095](0095-external-variable-modification.md), audited
  per ADR-0098
- the refusal for personal data is ADR-0314's; the portability rule the follow-up must
  meet is [ADR-0371](0371-participant-binds-a-published-interface.md)'s
