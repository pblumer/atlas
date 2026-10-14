# ADR-0453: An application holds its values per installation, and a constant binds to one by name

- **Status:** Accepted (amended)
- **Implementation:** Landed
- **Date:** 2026-10-13
- **Deciders:** Atlas maintainers

## Context and problem statement

Process constants (ADR-0454) state a value every instance starts with
and nobody is asked for. They state it **in the model**. That is right for a value that
is the same wherever the process runs — a language code, a routing mode — and wrong for
the values that prompted them: the tenant an app registration lives in, the registration's
own id, a domain suffix. Those differ between DEV, INT and PROD, and the same BPMN must
deploy to all three (ADR-0371). A constant with the production tenant in it cannot be
tested on INT; three models with three tenants are three models to keep in step.

Nor are they a single process's business. Every process of an application that signs in
to the same tenant needs the same tenant id, and stating it once per process is how two
of them end up disagreeing.

The question: **where does a value live that belongs to an application, differs per
installation, and must reach every instance of that application's processes on every
start path — including the timer, message and signal starts that never pass through the
API?**

## Decision drivers

- **The model stays portable** (ADR-0371, ADR-0156: "a model should say *what*, not
  *where*"). The BPMN names the value it needs; the installation supplies it.
- **Every start path.** A value resolved at the API edge reaches an API start and misses a
  timer. It has to be resolved where every instance is created, which is the engine.
- **Replay is unaffected** (I4, I6). Whatever the value was when an instance started is
  what its log says, and recovery never asks for it again.
- **Operator configuration is not process history.** ADR-0105 settled this for call
  resolution overrides: per-server configuration is durable on a sidecar, pushed into the
  processor and owned by the run loop, and deliberately not event-sourced, because the
  log is the process's history and not the server's admin record. These values are the
  same kind of thing.
- **A missing value is visible before it is a null.** The failure this all started from
  is a script reading `$null` with nothing on any screen saying why.

## Considered options

1. **Application values on a sidecar, pushed into the processor; a constant binds to one
   by name** (`source="application"`).
2. **Event-source the values**: a command writes each value into the log, `applyToState`
   keeps them in the engine's state.
3. **Inject every application value into every instance** of the application's
   processes, without any declaration in the model.
4. **Resolve at the API edge**, adding the values to the start variables of an API start.

## Decision outcome

Chosen option: **1**, because it reaches every start path, keeps the model portable and
its dependencies visible, and follows the pattern ADR-0105 already established for
configuration the engine reads while it processes a command.

### Where the values live

Each application (ADR-0128) holds, **on each installation**, a set of named, typed
values — `{name, type, value}`, the type one of the four a constant takes. They are kept
in an `application-values` sidecar store, one record per application, and classified as
*instance* data (ADR-0282): a whole-installation snapshot carries them, the portable
design-time export does not, and promoting a release (ADR-0129) never ships them. That is
the point: DEV, INT and PROD each answer for themselves.

- `GET /api/v1/applications/{id}/values` returns the values this installation holds for
  the application, and what the application's deployed processes **require**: every
  constant bound to the application, with the processes that bind it, whether it has a
  fallback, and whether the value is missing or has another type than declared. Anyone
  who may view the application may read it — the values are identifiers, not secrets.
- `PUT /api/v1/applications/{id}/values` replaces the set. It is admin-gated, like a call
  override: it changes what every new instance of the application starts with.
- The Console's application page has a **Values** tab for both.

A value is parsed and canonicalised when it is saved — the same parser the compiler uses
for a constant — so a malformed one is refused at the door and the engine never parses.

### How a model binds

```xml
<atlas:constants>
  <atlas:constant name="TenantId" type="string" source="application"/>
  <atlas:constant name="Language" type="string" source="application" value="de-CH"/>
  <atlas:constant name="Routing"  type="string" value="round-robin"/>
</atlas:constants>
```

`source="application"` binds the constant to the application's value **of the same
name** on the installation where the instance starts. A `value` on a bound constant is its
**fallback**, used when the application holds none. Everything ADR-0454
decided about a constant still holds: it is seeded before the start variables, nothing in
the run replaces it, the model may not write it.

### How the engine resolves it

The processor holds two maps, both operator configuration in the sense of ADR-0105 —
pushed by the server layer at startup and on every change, owned by the run-loop goroutine
(I3), never in the log:

- which application each deployed definition is filed under (pushed on deploy, on a move
  between applications, and at startup), and
- the values each application holds (pushed when they are saved, when an application is
  deleted, and at startup).

`activateInstance` — the one function every creation and fork comes through — looks a
bound constant up in them and writes what it finds into the instance's `VariableCreated`
event. The value is then a fact of that instance (I6): recovery replays the event and
never consults the maps, and changing a value changes the instances started afterwards,
never the ones already running. A process deployed outside an application, or bound to a
value its application does not hold, gets the fallback; with no fallback, the constant is
not seeded.

### How a missing value is made visible

- **At deploy**, a bound constant without a value and without a fallback on this
  installation is a warning in the deploy response, beside the worker warnings — never a
  refusal, because a model is routinely deployed before its world exists (ADR-0158). So is
  a value whose type differs from the declared one, and a bound constant in a process
  deployed outside any application.
- **On the Values tab**, every required name is listed with its state.
- **An API start refuses** with `409 Conflict` — the Modeler's Deploy & run, the Live
  view's Start instance, the Tasks app's Start, a public link, a direct API call — naming
  the value and where to set it (a public link says only that the process is not fully
  configured on this installation). The person starting is there to read the answer.
- **A timer, message or signal start, and a call activity**, are not refused. The engine
  has nobody to answer and refusing would lose the trigger; the instance starts without
  the constant, which the deploy and the Values tab already said.

### Consequences

- **Positive:** One BPMN runs on DEV, INT and PROD with each installation's own tenant,
  app id and suffix, set once per application rather than once per process. The value an
  instance ran with is in its own record. An installation that is missing a value says so
  at deploy, on the application page, and on every start a person makes.
- **Negative / trade-offs accepted:**
  - A value is frozen when an instance starts. A value that changes while instances run —
    a certificate thumbprint at rotation — belongs where it is used: resolved on the worker
    host at run time, not in a constant. The record says so rather than pretend a constant
    can be both fixed and current.
  - A timer, message or signal start without a value starts without it. The engine cannot
    refuse those without losing the trigger; the warning and the Values tab are the
    defence there.
  - The binding is by name. A constant cannot rename an application value; the two names
    are one name, which is also what keeps them easy to read side by side.
  - Instance migration (ADR-0162) neither re-reads nor rewrites the values.
- **Follow-ups / risks to watch:**
  - Promotion could tell the operator, before it ships a release, which bound values the
    target installation does not hold. The target's own deploy warns today.
  - Installation-wide values, shared by every application, are not provided. Nothing has
    needed one that an application value does not cover.

## Pros and cons of the options

### Option 1 — sidecar plus processor maps, bound by name (chosen)
- Good: Every start path, because the engine resolves it; replay untouched, because the
  value is frozen into the event; the model portable and its dependency on the value
  visible in it.
- Good: The same shape as ADR-0105 — a reviewer already knows where to look.
- Bad: Two maps to keep in step with the deployment registry and the store. Both are
  pushed from the same places the registry changes.

### Option 2 — event-source the values
- Good: The engine's state would hold them and recovery would rebuild them.
- Bad: The log would carry the installation's configuration history beside its process
  history, which ADR-0105 rejected for the same reason, and it buys nothing: the value an
  instance used is already frozen into that instance's events.

### Option 3 — inject every application value into every instance
- Good: Nothing to declare.
- Bad: Every instance of every process would carry every value its application holds,
  whether it uses them or not, and nothing in a model would say which values it needs —
  so no deploy could warn that one is missing.

### Option 4 — resolve at the API edge
- Good: The smallest change.
- Bad: It misses every start the API never sees — the timer, message, signal and call
  activity starts — which is the null this was meant to end.

## Links

- extends ADR-0454, whose open question this answers
- follows [ADR-0105](0105-per-server-call-activity-target-overrides.md) for operator
  configuration the engine reads while processing a command
- the portability rule is [ADR-0371](0371-participant-binds-a-published-interface.md)'s;
  applications are [ADR-0128](0128-process-applications.md)'s, promotion ADR-0129's, and the
  store classes ADR-0282's

## Amendment (2026-10-14): an application's values are its constants, read as application.NAME

[ADR-0456](0456-constant-namespaces.md) keeps the decision above —
held per installation, pushed into the processor, bound by name, frozen into the creation
event — and changes how it is named and read:

- **Constants, not values.** What an application holds is called its constants:
  - the Console's tab is **Constants**;
  - the routes are `GET`/`PUT /api/v1/applications/{id}/constants` with the body
    `{"constants": [...]}`;
  - the MCP tool is `atlas_application_constants`;
  - the store is `application-constants`.

  No installation used the 0.12.0 names, so there are no aliases.
- **Read under a namespace.** A constant bound to the application is no longer a root
  variable of its own name. It is a member of the root variable `application`, a JSON
  object, read as `application.TENANT_ID` in FEEL and `$application.TENANT_ID` in
  PowerShell. That variable exists only in an instance of a process that declares an
  application constant, and is then a constant itself.
- **Names in capitals.** An application constant's name is written in capitals, as every
  constant's is (ADR-0454, amended), and `PUT` refuses any other.
- **Beside the installation's own.** A constant may also be held for every application,
  read as `server.NAME`, and both namespaces carry facts Atlas fills itself
  (`application.name`, `server.environment`). Both are that record's.

