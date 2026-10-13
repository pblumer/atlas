# ADR-0455: A start variable's default is what an instance starts with when its start did not supply it

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-10-13
- **Deciders:** Atlas maintainers

## Context and problem statement

A process declares the values it starts with as start variables
(`<atlas:startVariable name type default required/>` inside `<atlas:startForm>`, the typed
start form of [ADR-0028](0028-forms-and-the-tasks-app.md)). A declaration may carry a
default, and until now that default meant one thing only: the value the start surfaces
prefill. The Modeler's *Deploy & run*, the Live view's *Start instance* and the Tasks app
put it in the field; whoever pressed Start sent it, or cleared it and sent nothing.

Nothing else read it. The engine never looked at the declaration, so an instance started
through the API, MCP, a public link, a CSV upload, a timer, a message, a signal or a call
activity without the variable simply did not have it. A FEEL condition read null, a
PowerShell script read `$null` — or failed under `Set-StrictMode` — and nothing on any
screen said why. The same model behaved one way when started by hand and another when
started by anything else.

The question: **what does a declared default mean for an instance started without the
variable?**

## Decision drivers

- **One rule on every start path.** A default that holds on some paths and not on others
  is the defect, not a detail of it: the process that works under *Deploy & run* fails
  under its own timer.
- **The word means what it says.** To anyone who writes one, a default is the value used
  when none is given. A field labelled "default" that is only a prefill invites exactly
  the null above.
- **No accepted model stops loading.** A stored definition is compiled again on every
  restart, and the declaration has always been accepted as written. A malformed default
  cannot become a refusal.
- **Parsed once (I5), recorded as facts (I6).** The engine must seed a default without
  parsing it, and the value it seeds must be an event like any other variable's, so
  recovery replays it rather than recomputing it.
- **Personal data stays sealed** ([ADR-0314](0314-portal-personal-data.md)). A value
  stated in the model is in the clear in every copy of it.
- **What a start says, it says.** A start that names a variable — even as null — made a
  choice, and a default must not overrule it.

## Considered options

1. **Keep it a prefill, and say so.** Rename the field "suggestion" and leave the engine
   alone.
2. **Apply it in the API.** The instance-creation endpoints fill in what the body lacks
   before the command is built.
3. **Apply it where an instance is created** — `activateInstance`, the one path every
   start goes through — for every start, and not for a fork.

## Decision outcome

Chosen option: **3, apply it where an instance is created**, because it is the only one
that gives every start path the same rule, and it costs one loop over a declaration the
compiler already holds.

### The rule

- **Supplied means named.** A declared start variable the start's variables do not name
  takes its default. One they name keeps the value they gave, null included.
- **Every start, no fork.** The API, MCP, a public link, a CSV upload, a timer, a message,
  a signal and a call activity all create an instance through `activateInstance`, and all
  get the defaults. A fork ([ADR-0389](0389-forked-instance-migration.md)) comes through
  the same function but is not a start: it continues an instance with the values that
  instance had reached, and a default the target version declares for a variable the
  predecessor never had would be a change nobody made. A fork is the creation that names
  a predecessor, and that is the test.
- **After the start's own variables, after the constants.** Constants
  ([ADR-0454](0454-process-constants.md)) are written first, then the
  variables the start supplied, then the defaults of the ones it did not. A start
  variable cannot share a name with a constant — the deploy refuses that — so the order
  decides nothing about values; it decides only the order of the events.
- **The start correlation key is computed from what arrived.** A singleton message start
  evaluates its correlation expression over the message's variables before the instance
  exists ([ADR-0035](0035-message-start-events.md)), so a default does not take part in it.

### The deploy

- **Parsed once.** The compiler reads the default into the canonical (kind, bool, text)
  form a variable is stored in, by the same literal rule a constant's value follows: a
  string as written, `true`/`false`, a JSON number, any JSON value. The engine seeds that
  form and parses nothing (I5). The authored text stays beside it for the start forms to
  prefill.
- **Empty is none.** The attribute cannot tell "absent" from "empty", and the start forms
  read an empty field as one not filled in.
- **Malformed is withheld, not refused.** A default that is not a literal of its type —
  `number` with `many` — is not seeded, and the deploy warns with the reason. The model
  still deploys and still loads after a restart.
- **Personal data is withheld.** A default on a variable declared personal would arrive in
  the clear, and the engine refuses such a write by parking the instance it was starting.
  A default on the data subject would make every instance started without one the same
  person's, so one erasure would destroy the data of all of them. Both are withheld, with
  a deploy warning; the start form still prefills them, and what a person types over them
  is enciphered on its way in like any other value.

### The start forms

They are unchanged: they prefill the default and send what the field holds. An optional
field the person empties is omitted, as before — and now means "not supplied", so the
engine seeds the default. The Modeler says so on the field where a default is typed.

### Consequences

- **Positive:** a model behaves the same however it is started. The null that started
  [ADR-0454](0454-process-constants.md) cannot recur for a declared
  start variable with a default. A default is visible as a variable on the instance's
  timeline, written at creation, like everything else it started with.
- **Negative / trade-offs accepted:**
  - **Existing models change behaviour.** An instance started over the API or by a
    trigger now carries variables it did not carry before. A model that tested for their
    absence — a gateway on `x = null`, a script on `$null` — takes the other branch. That
    is the defect being fixed, but it is a change in a release, and the changelog says so.
  - **An empty string cannot be sent through a start form for a variable whose default is
    not empty.** Emptying the field means "not supplied". A start over the API can still
    send `""`.
  - **`required` is still enforced by the start forms only.** A start over the API that
    leaves out a required variable without a default is not refused; the variable is
    missing, as before.
- **Follow-ups / risks to watch:**
  - **Refusing a start that lacks a required value.** For the API and MCP the answer is a
    400 naming the missing variables. For a timer, a message or a signal there is no
    caller to refuse — an incident, a skipped start and a start with null are the
    choices, and that decision is its own record.
  - **The listing could say a default is withheld**, so the start forms stop prefilling a
    value the engine would not seed. Today the deploy warning is the only place it shows.

## Pros and cons of the options

### Option 1 — keep it a prefill, and say so
- Good: no engine change; the event log holds exactly what each start supplied.
- Bad: the API, MCP and every trigger keep starting instances without the value, and
  nothing tells them. Renaming the field explains the null without preventing it.

### Option 2 — apply it in the API
- Good: small, and the HTTP starts — the commonest — are covered.
- Bad: timers, messages, signals and call activities create instances inside the engine
  and never pass through the API, so the rule would again hold on some paths only.

### Option 3 — apply it where an instance is created (chosen)
- Good: one place, every start; the default is an ordinary variable event, replayed on
  recovery; the fork is excluded by the one field that defines it.
- Bad: a behaviour change for models already running, and a default can no longer be
  "cleared" through a start form.

## Links

- extends [ADR-0028](0028-forms-and-the-tasks-app.md) (the typed start form and its declaration)
- relates to [ADR-0454](0454-process-constants.md) (values nobody is asked for)
- relates to [ADR-0389](0389-forked-instance-migration.md) (a fork continues an instance)
- relates to [ADR-0314](0314-portal-personal-data.md) (personal data is never in the clear)
- relates to [ADR-0035](0035-message-start-events.md), [ADR-0076](0076-call-activities.md), [ADR-0084](0084-csv-batch-validation.md) (the start paths that now carry defaults)
