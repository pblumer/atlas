# ADR-DRAFT: A connector task's input mappings are its outbound payload

- **Status:** Proposed
- **Date:** 2026-08-21
- **Deciders:** Atlas engine team

## Context and problem statement

Two related things were unresolved about what a connector task sends.

**What a worker can see.** ADR-0068 gave activities `zeebe:ioMapping` inputs: on
activation the engine evaluates each source and writes it into an **activity-local**
variable scope, keyed by the element-instance key, which the activity and its worker
then read *up the scope chain* — locals first, then each enclosing scope to the
process root. The engine binds its own FEEL that way (`bindInputsChain`), and the
script, DMN and CSV workers were moved onto it. Every other worker
(`connector/rest`, `mail`, `clio`, `scim`, `soap`, `ldap`, `ad`, `sharepoint`,
`remedy`, `webscrape`, and the user-provisioning connector in `api`) kept reading a
single flat scope — `ei.ProcessInstanceKey` — and so could not see its own task's
input mappings at all. ADR-0068's implementation note listed this as remaining work.

The consequence was not a subtlety. A clio write task whose payload came from input
mappings appended events with an **empty body**, because the mapped values were in a
scope the worker never read and the process scope held nothing. A REST task's FEEL
url could not reference a mapped local. A SCIM task naming a body variable an input
mapping had built failed with "body variable is not set on the instance". Each is the
same defect wearing a different connector's clothes.

**What a task sends.** Three connectors put a *variable scope* on the wire: the clio
write body, the REST request body for methods that carry one, and the SCIM
create/replace/patch body when the task names no body variable. Each sends every
variable it read. ADR-0036 had planned better — its decision names "a **payload
mapping** (which process variables form the event body)" as part of a write task's
compiled detail — and the code that shipped carried a comment saying the whole scope
was the payload "until output mappings exist (Milestone 1)". ADR-0067 repeated the
interim for REST, ADR-0152/0153 for SCIM.

So fixing the read raises the question the interim deferred. Once a worker resolves
up the chain, a mapped task's body would be "locals *plus* everything inherited" —
which fixes the empty body but still cannot *restrict* what leaves the process. For
an outbound body that matters: events and API payloads are retained, replayed,
projected, and increasingly schema-checked (clio validates against a registered event
schema; a SCIM endpoint rejects unknown attributes). A body that silently carries
every scratch and internal variable of an instance leaks process state into systems
outside the engine, and breaks the moment a schema exists.

## Decision drivers

- **A model should state what leaves it.** An outbound payload's shape is part of the
  contract with its consumers, not an accident of which variables the instance
  happens to hold at that moment.
- **The already-stated intent.** ADR-0036's payload mapping, and ADR-0067/0152's
  copies of the same interim note, were always waiting for the mechanism ADR-0068
  delivered.
- **One rule, every connector.** Ten workers with the same defect should not get ten
  slightly different fixes, nor ten copies of the same scope-chain walk.
- **No new concept.** I/O mappings already exist, are already in the properties panel,
  already compile at deploy (I5) and freeze their values into variable events (I6). A
  per-connector payload syntax beside them would be a parallel mechanism to maintain.
- **Backwards compatibility.** Models that send the instance's variables today, and
  the flat processes that are the common case, must keep working unchanged.

## Considered options

1. **Scope-chain read only.** Move every worker onto the ADR-0068 chain read and stop
   there. Fixes what a worker can see; input mappings *add to* a payload but cannot
   restrict it.
2. **Scope-chain read, and input mappings are the outbound payload (chosen).** Every
   worker resolves up the chain; additionally, where a connector's payload *is* a
   variable scope, a task's input mappings — when it has any — are exactly that
   payload.
3. **A dedicated payload field per connector kind.** A FEEL expression (or named
   variable) on each task that evaluates to the body.

## Decision outcome

Chosen: **option 2.**

**Everywhere.** A connector worker reads the variables its task sees by walking the
scope chain from its element instance: the activity-local scope (its input-mapped
locals) first, then each enclosing scope to the process root, nearest winning. This
is one shared implementation — `state.VisibleVariables` / `VisibleVariablesMap`,
taking the `state.Reader` a handler already holds — rather than a copy per package,
and `Store.VisibleVariablesOfScope` (form prefill) is now a name for it. A task with
no mappings in a flat process reads exactly the process scope, as before. The
reserved `processInstanceKey` builtin keeps binding to the *process instance's* key,
which is no longer the scope being read.

**Where a payload is a variable scope** — the clio event body, the REST request body,
the SCIM body when no body variable is named — a task's input mappings, if it has
any, *are* the payload: the body is exactly its activity-local scope, which at that
point holds the mapped values and nothing else (a job's result is written there only
on completion). With no input mappings the body stays every variable the task sees.

Connectors whose fields are individually authored (mail, SOAP, SharePoint, Remedy,
LDAP, AD, web scrape, user provisioning) have no scope-shaped payload; for them this
record is the chain read alone, and their FEEL fields — and the variables they name,
such as LDAP's and AD's entry variable — now resolve mapped locals.

### Consequences

- **Positive:** input mappings work on every connector task, which is what the
  properties panel has been offering all along. A modelled payload has a modelled
  shape: rename, reshape and restrict what a process publishes, with the mechanism
  that is already compiled and replay-safe. Internal and scratch variables stop
  leaking into external systems, and a registered clio event schema or a strict SCIM
  endpoint becomes satisfiable from a model. One scope-chain implementation replaces
  what was becoming a copy per package.
- **Negative / trade-offs accepted:** an outbound body deliberately departs from
  ADR-0068's "locals plus inherited" reading — on a mapped task the FEEL fields still
  resolve up the chain while the body does not. Adding a first input mapping to an
  existing clio/REST/SCIM task becomes a breaking change to that task's payload,
  which is the point but must be visible in the panel (the affected hints say so). A
  model wanting both a mapped value and an inherited one in the body must map both.
  Each worker now walks a chain rather than reading one scope — bounded by scope
  depth, off the processor path, and the walk stops at the local scope for a mapped
  body.
- **Follow-ups / risks to watch:** the three exported `Resolve` functions (REST,
  mail, web scrape) now take the element instance and its key rather than a scope
  key, which the offload path in `api/handlers.go` passes; a fourth kind added later
  should follow the same shape rather than reintroducing a scope argument. Whether
  the message throw's whole-scope publish (ADR-0035) should follow this rule is
  deliberately left open — it is an internal correlation payload, not an external
  contract.

## Pros and cons of the options

### Option 1 — scope-chain read only
- Good: literally ADR-0068; one rule for every worker; smallest change.
- Bad: an outbound payload still cannot be restricted, so internal state keeps
  reaching external systems and a schema-checked endpoint stays unsatisfiable;
  mappings can only add.

### Option 2 — chain read plus mappings-as-payload (chosen)
- Good: delivers the payload mapping ADR-0036/0067/0152 all deferred, with no new
  syntax; the model states what leaves it; backwards compatible for mapping-free
  tasks; fixes the empty body and the invisible locals in one rule.
- Bad: a deliberate, documented departure from ADR-0068's inheritance rule for
  outbound bodies; adding a mapping changes an existing task's payload.

### Option 3 — a dedicated payload field per kind
- Good: the payload is visibly one field; could express a non-object body.
- Bad: a second payload mechanism beside I/O mappings, per connector kind, each with
  its own compile, freeze and replay story; duplicates the mapping editor; ADR-0036's
  own wording points at mappings, and Camunda-shaped models already reach for
  `zeebe:ioMapping`.

## Links

- delivers the payload mapping ADR-0036 named and ADR-0067 (REST) and
  ADR-0152/0153 (SCIM) repeated as an interim
- completes ADR-0068 (task I/O mappings and activity-local scopes) for the connector
  workers, and deliberately narrows its inheritance rule for outbound bodies
- relates to ADR-0168 (connector work on a worker): the `Resolve`/`Run` split keeps
  one definition of a resolved task, so the in-process and offloaded paths change
  together
- honors I5 (mappings compile at deploy) and I6 (mapped values are frozen into
  variable events, so replay re-applies rather than re-evaluates them)
- relates to ADR-0035 (a message throw publishes its instance's variables), which
  keeps the whole-scope rule
