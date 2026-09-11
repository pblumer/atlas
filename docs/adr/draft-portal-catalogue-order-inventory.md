# ADR-DRAFT: Catalogue, order, inventory — three models, not one

- **Status:** Accepted
- **Implementation:** Partial
- **Date:** 2026-09-11
- **Deciders:** Atlas maintainers
- **Open question:** Whether a column family holding millions of entitlements stays
  within a workable checkpoint. The inventory is engine state by this record, so it
  is written into every checkpoint ([ADR-0131](0131-engine-recovery-checkpoints-and-wal-compaction.md)),
  and nobody has measured what that costs at the scale this record sizes for.
- **Question checked:** 2026-09

## Context and problem statement

Atlas executes provisioning today. A model can create an account in Active Directory
or Entra, assign a licence, add a group member, open a Jira issue, call a REST or SOAP
endpoint — durably, with incidents, retries and replay. What it has no notion of is
the thing a person actually arrives with: **a catalogue to order from, and a record of
what they already have.**

The request that prompted this record asks for a self-service portal with several
product catalogues, structured from an ArchiMate model or a SAP system, themed per
customer group, gated by approval, and fulfilled in dependency order. The part that
looks hardest — orchestrating provisioning across systems, in order, surviving a
crash — is the part that already exists. The parts that do not exist are the catalogue
and the inventory.

Both of those look like the same thing: a list of services attached to a person. That
resemblance is the trap this record exists to close. A first cut would store one list
and let the order be a flag on it, because at the moment somebody orders, the catalogue
entry, the order line and the entitlement all say "Alice, VPN access". They diverge
immediately afterwards, and they diverge along three axes that cannot be reconciled in
one structure:

- **Lifetime.** A catalogue entry is withdrawn and replaced. An order completes in
  days. An entitlement outlives both by years, and the instance that created it is
  eligible for retention deletion long before the entitlement ends
  ([ADR-0115](0115-history-retention-hard-delete.md), [ADR-0144](0144-per-definition-history-ttl.md)).
- **Truth.** A catalogue says what *may* be ordered. An order says what *was asked
  for*, and must not change while an approval is pending. An entitlement asserts what
  *is true in another system* — an assertion Atlas cannot guarantee, because target
  systems are changed outside Atlas.
- **Authority.** A catalogue is authored. An order is placed. An entitlement is
  observed or derived. Nobody edits an entitlement directly, and the day somebody can,
  the record of who was granted what stops being evidence.

The question this record answers: **what does the portal store, where does each part
live, and which of Atlas's existing mechanisms does each part reuse?**

Everything about who may see a catalogue, who approves, how personal data is retained
and how a catalogue is themed is deliberately *not* here. Those are companion records,
listed under Links.

## Decision drivers

- The engine must not learn about catalogues. Ordering is design-time data and
  runtime orchestration; neither is a new execution model.
- Work that can happen when a catalogue is published must not happen when somebody
  orders (I5, [ADR-0008](0008-feel-expression-strategy.md)).
- An order must be immune to catalogue edits made while it is being approved.
- A query that grows with the population must not hold the single writer
  (I3, [ADR-0239](0239-off-loop-queries.md), [ADR-0080](0080-runtime-aggregate-counters.md)).
- The inventory must survive the process instance that created it, and must be
  durable for as long as the evidence is needed.
- No second provisioning path. Everything that changes a target system goes through a
  modelled process, so it is visible in the diagram, the replay and the audit trail.
- Single binary ([ADR-0011](0011-single-binary-distribution-and-web-ui.md)): no
  external database, whatever the volume.

## Considered options

1. **One model.** A single "service assignment" record carrying a state — offered,
   ordered, approved, provisioned, revoked.
2. **Two models.** Catalogue separate; order and inventory merged, the inventory being
   "the orders that succeeded".
3. **Three models,** with separate storage and separate lifetimes.

## Decision outcome

Chosen option: **three models** — a versioned design-time catalogue, an order that is
an ordinary process instance, and an inventory that is engine state in its own column
family.

Option 1 fails on lifetime alone: retention would delete the evidence that somebody
holds a privilege, or the privilege would keep a completed instance alive forever.
Option 2 is the near miss and deserves its refutation in full, because "the inventory
is just the successful orders" is true on the first day and false ever after. An
entitlement can exist that no order produced — it predates the portal, or an
administrator created it directly, and the reconciliation below adopts it. An order can
succeed and the entitlement later cease, because somebody removed it in the target
system. The moment either happens, "the orders that succeeded" is a wrong answer to
"what does this person have", and it is wrong silently.

### The catalogue — design-time, versioned, published

A `CatalogItem` is a product or a service; the distinction is its position, not its
type, so the structure nests to arbitrary depth. Items are linked by a `CatalogEdge`
of one of **three** kinds, and the split matters more than it looks — they belong to
two graphs answering two different questions:

- **Structure** — a **composition** (integral, always included, not deselectable) or
  an **aggregation** (optional, separately orderable). These are the two ArchiMate
  relationships the source model already distinguishes, and they say what belongs to
  what.
- **Precedence** — **requires**, saying one service cannot be provisioned before
  another is. This is the edge the fulfilment order is computed over, and the only one
  whose direction means "after".

**The import reads the model in the architect's own vocabulary.** A Product or a
Business Service is something offered; a composition is a part that comes with the
whole, an aggregation one offered beside it. That is the same distinction the catalogue
makes, which is the whole reason to read a model rather than retype a list of names.
Everything arrives as a **draft**, so importing makes nothing orderable — publishing
refuses a draft, and that is the safeguard, not a nuisance. A product already stored is
left exactly as it is: the model is where a catalogue starts, not something it follows,
and a second import after somebody bound processes and activated a product must not undo
that work. What the import could not take, and what it deliberately left alone, it names
— a silent drop is the failure mode of every importer, and an architect who modelled
something and cannot find it concludes the import is broken.

Writing this record, the two were conflated: it named the structural edges and then
spoke of "the dependency graph" as though that were the same thing. It is not, and
reading them as one graph refuses ordinary catalogues — a workplace that *contains* an
account and cannot be provisioned *before* one exists would be a cycle. Both graphs
must be acyclic; they are checked separately.

An item may have several parents: the same service legitimately appears in several
products.

Each item carries its orderable window (`orderableFrom`, `orderableUntil`), a
withdrawal state, multilingual texts, its variant definition, an approval rule, a
`multipleAllowed` flag, and bindings to two processes: one to provision, one to
deprovision. **A service without a deprovisioning process cannot be published.** A
catalogue that can only grant is not a lifecycle, and the day somebody needs to revoke
at scale is the wrong day to discover the process was never written.

A `Catalog` is a named set of items plus a theme, a group assignment and a **rank**.
The rank resolves the one-catalogue-per-user rule against directory group membership,
which is many-to-many; ranks must be unique, and publishing refuses a tie rather than
falling back to an id nobody chose.

A `CatalogRelease` is a frozen, published version, following the shape applications
already have ([ADR-0128](0128-process-applications.md)). **Publishing is where the work
happens**, and this is I5 applied to a catalogue:

- both graphs are checked for cycles, and the precedence graph is resolved into
  **waves** — rounds in which nothing depends on anything else in the same round, and
  everything it does depend on has already run. Computed once, never derived per
  order, and deterministic: a schedule that reordered between two publishes of one
  input would make a diff of two releases unreadable and fulfil the same order
  differently twice;
- every binding is resolved — a process that no longer exists fails the publish;
- every item has both processes, and translations for every declared language;
- ranks are unique.

A catalogue that interprets a dependency graph at order time is an interpreter on the
runtime path, and a cycle in it is a modelling error that must surface when somebody
publishes, not as an incident for whoever orders at 23:00.

**Nothing is ever deleted from a catalogue** — items are withdrawn. An order placed
years ago and an entitlement still held both resolve through the item, and a store that
can delete one of those is a store that can orphan evidence.

### The order — an ordinary process instance

An `Order` names exactly one `CatalogRelease`. That is the whole of the snapshot rule:
the scope of what was ordered cannot shift because somebody edited the catalogue while
an approval was pending.

An `OrderLine` per position carries its own status, its own approval and its resolved
variant, because a single order can require several independent approvals.

**A line's status distinguishes three ways of not being provisioned**, and merging
them would break two things at once. *Failed* is a defect: something broke, an
operator repairs it, it retries. *Rejected* is a decision: nothing is broken, somebody
said no, and there is nothing to repair — filing it as a failure raises incidents
nobody can close and reports a decision as a malfunction. *Blocked* is a consequence:
the line was never attempted because something it requires failed or was rejected, and
it carries the **root** cause rather than the intermediate blocked line between, since
that one is blocked for the same reason and naming it makes a reader walk the chain.
A fourth, *skipped*, is a line the recipient already holds; it satisfies its dependents
exactly as a provisioned one does, or the inventory would block a line for the reason
that it was unnecessary.

The same rule reaches the failure itself, not only the lines behind it: **an order
carrying an open incident has not finished.** A failed line has an outcome of its own —
which is why propagation leaves it alone — but it can still provision once somebody
repairs it, so it is not terminal and the order waits. Only abandonment ends it. The
first implementation conflated the two questions in one predicate and reported such an
order as settled, telling the orderer the result was final while an operator was
working on it.

*Blocked* is therefore **derived, never stored**, and recomputed on every pass. That is
what makes a repair effective: an operator who fixes the incident behind a failed
precondition releases the line that was waiting on it, with nobody rewriting a status
by hand. And it is what decides how long an order lives — **an order stays open while
any blockage can still be repaired.** A failure is an incident somebody can fix, after
which the line runs after all; a rejection will not change, so waiting on one is
waiting for nothing. One cause that will not lift settles the line whatever happens to
the rest. Settling an order while an incident behind it is being worked would tell the
orderer their line is never coming, at the moment somebody is fixing the reason it has
not.

For that to close rather than run forever, a failure has to be able to **stop** being
repairable, and the deadline that decides it sits on the **incident**, not on the
order. The incident is the thing actually stuck; a deadline on the order would settle
work that was about to succeed, and one on the order's own clock would have to guess at
what the incident is doing. What that deadline does is **escalate**: it makes
the incident visible and tells somebody. It never abandons anything itself. A system
that closed orders because nobody was in the incident queue over the holidays would
tell an orderer their line is never coming for a reason that was actually short
staffing, and would write "the system decided" into a record kept forever.

Giving up is therefore a decision with an author. A line whose incident a **person**
gave up on becomes *abandoned*, which settles like a failure and lifts like nothing: it
is a failure nobody will repair, and among a blocked line's causes it counts exactly as
a rejection. The rule is structural rather than a review note — there is one transition
into the status and it cannot be called without naming the principal who decided, so an
automated caller has no call to make, and the same rule is re-checked where a line
arrives as JSON. An incident already carries `RaisedAt`, frozen into its event, so the
deadline needs no new state to measure against. A generic
fulfilment process works the release's waves: every line in a wave starts its
provisioning process, and the next wave begins when the current one settles.

**Not as a call activity, and that took building it to find out.** `<zeebe:calledElement
processId=…>` is a static attribute: the called process is fixed when the model is
authored, and the operator-level override (ADR-0105) redirects one process id to another
for the whole server rather than per instance. An order's line knows its process only at
runtime, from the release. So the fulfilment process starts each line's provisioning
process through the API instead — the same REST Worker it already uses to ask which
lines are ready, and to report what came back. The cost is that engine-level parent and
child are not related, so the order is what ties them together; it already does, since
every line carries its process and its outcome.

**Nothing polls, and nothing waits on a child.** Placing an order publishes a message
that starts the fulfilment process; a line's own provisioning process reports its result
as its last step, and that report publishes a second message the fulfilment process is
parked on. So there is exactly one reason to ask what may start next — a settled line —
and asking on a timer would be asking at every moment except that one. The two message
names are constants in `api/order` rather than strings in the model, because a name
nobody publishes is a process that waits forever and fails silently.

**Approval sits before provisioning and outside the product's own process.** The rule
belongs to the catalogue, so the fulfilment process starts the approval process of the
line's kind, and that one starts the provisioning process if somebody agreed. Were the
approval inside the product process, every one of them would have to model it, and
changing a rule would be a change to every process that carries it. An agreement needs
no message back: the line goes on to be provisioned and its outcome arrives the ordinary
way. Only a refusal is reported, because otherwise it would be recorded nowhere — and it
is reported through its own call, since the ordinary one deliberately refuses a
rejection.

**One of the three shipped approval kinds cannot name who decided.** Atlas does not
record which principal completed a user task, so an approval assigned to a *person* —
the fixed approver, the superior read from the directory — names them because the task
went to exactly them, while an approval assigned to a *group* can only name the group.
For evidence kept forever that is too little, and it is not fixable in a model: it needs
the engine to record the completer. Until it does, the group variant says what it does
not know, in its own documentation, rather than writing the group into a field that
reads like a person.

That puts one obligation on every provisioning process: its last step reports the
outcome. The catalogue cannot check it — a release proves the process is deployed, not
what it does — so it is a convention, stated here and in the fulfilment process's own
documentation. A process that does not report leaves its line running and the order open
until somebody looks. When a line fails, every line that does not depend on it continues; dependent
lines stop and raise an incident. Partial fulfilment is the intended behaviour, not a
degraded mode.

**The schedule is waves rather than a sequence precisely because of that failure
rule.** A flat topological list answers "what before what" and nothing else — it has
already discarded the reason each item sits where it does, so a failure in the middle
of it stops everything after, including branches that never depended on the failure.
The first implementation of this record built the list, and the gap surfaced when the
fulfilment process was designed against it. An item's wave is one past the *latest* of
its preconditions, so "everything this needs has already run" is true at every wave
boundary.

Waves alone are still not enough, and the same design pass found why. A wave is the
right unit to **run** in and the wrong unit to **start on**: at a wave boundary the
only fact available is "the previous wave is done", which cannot tell a line whose
precondition failed from one whose precondition succeeded beside it. A wave-wide
barrier therefore either starts a line whose precondition is missing or holds one
whose preconditions are all present — both wrong. So a release carries **both**: the
waves, which are what a person reads and what the fulfilment process parallelises
over, and each line's direct preconditions, which are what decides who a failure
takes with it. The release answers that question itself, so the orchestrator never
reconsults the catalogue.

Two resolutions happen in the basket, before the order exists, and both are shown to
the person rather than decided for them: a service the ordering user **already holds**
is marked as held and skipped, and the same service pulled in twice in **different
variants** is a conflict the orderer resolves. Where the service arrived by composition
it cannot be dropped, so only the variant is choosable; by aggregation, the optional
position can be removed.

### What reaches the person who ordered

Three moments, and they are the three where knowing changes what the orderer does: a
line was **rejected**, a line was **abandoned**, and the order **settled** — the closing
message carrying what came and what did not.

Nothing else. A failure and a blockage are addressed to whoever can act on them: an
incident escalates to an operator, and telling the orderer that provisioning threw an
error gives them a worry, no action, and a fresh message on every retry. A blocked line
is a consequence whose cause was already notified.

A notice is owed for a **transition**, never for a state, because propagation runs after
every settled line and a function reporting what is true rather than what changed would
send the same message on each pass.

Two consequences for the model. A rejection carries **who decided, when and why**, in
the same shape abandonment already had: a refusal kept forever without an author is a
decision nobody made, and one without words is the message that produces a phone call
instead of an understanding. And nothing in the order model sends anything — delivery is
a mail task in the fulfilment process, which puts the side effect after fsync where it
belongs (I2) and leaves the channel, the wording and the language to the model.

The orderer is told, not the recipient. They are frequently the same person; where they
are not, it is the orderer who is waiting and who can act — and a recipient onboarding
next month may have no mailbox to write to yet.

### An approval nobody answers

The quieter half of the same problem. A stuck incident stands out in an operations
view; an unattended approval task looks exactly like every other task in an inbox, and
the order waits on it with no failure to escalate and nothing to notice.

A deadline on an approval therefore **reminds, then escalates to a deputy, and never
decides.** Silence is not a refusal. Recording one would put a decision nobody made
into a record kept forever — the same thing abandonment is guarded against on the
incident path, and guarded the same way: the transition into a refusal takes the
principal who decided, so a clock has nothing to pass and no call to make. The only
thing a deadline can do to an assignment is move it.

It escalates **upwards**: the deputy is the approver's superior, read from the
directory. That is the lookup the `superior` approval rule already performs, so no
second list has to be kept current for the day it is finally needed — the failure mode
of every deputy register.

An assignment keeps **who it started with** alongside who holds it now and every hop
between, because a decision eventually taken by a third superior reads very differently
from one taken by the line manager it was meant for. Each hop records **who moved it**,
or nobody when a deadline did: that absence is what separates an automatic move from
somebody's decision to intervene.

Escalating upwards ends somewhere — nobody is above the top, and a directory can loop.
Both are the same operational case, there is nowhere new to go, so both raise one
distinguishable error and the assignment is **stalled**: still with whoever last held
it, since stalling reports a fact rather than taking the task away, but now visible.
That visibility is the point. An approval nobody can escalate and nobody is looking at
is exactly how an order waits forever, and it is the quiet version of the incident
problem this record already solved loudly.

Visibility with no way to act on it is only a quieter kind of stuck, so a person can
**reassign** a stalled approval, which clears the stall and puts the deadline back on
the normal path from the new holder. A reassignment may go to somebody who already held
it: the loop guard exists to stop a *clock* cycling an approval between two colleagues,
and a person sending it back to the original approver knows something the guard does
not.

What it will not do is give the approval to whoever is making the call. Taking a stuck
approval for yourself and approving it is the one move that turns the escalation path
into its own bypass, and it needs no role at all — only access to an approval that has
stalled, which is by definition one nobody is watching. The chain records it, but
afterwards, and nobody reads escalation histories routinely. This sits in the model
rather than in a role because it is not a question of authority: whoever genuinely needs
the approval can still be given it, by somebody else, and that second person is the
whole difference.

### The inventory — engine state, its own column family

An `Entitlement` records that a principal holds a service, in a variant, since when,
from which order, against which target, in which state. It lives in a new column family
in the state store, written through the log and rebuilt by `applyToState` like every
other engine fact (I4, [ADR-0001](0001-event-sourcing-and-log-structured-state.md)).
Pebble carries millions of keys; the volume is not the reason to put it elsewhere, and
there is nowhere else to put it that does not break
[ADR-0011](0011-single-binary-distribution-and-web-ui.md).

Three things follow and none of them is optional:

- **Retention must exempt this column family.** The instance that produced an
  entitlement is eligible for deletion long before the entitlement ends. This is an
  explicit amendment to [ADR-0115](0115-history-retention-hard-delete.md) and
  [ADR-0144](0144-per-definition-history-ttl.md), not an oversight to be discovered.
- **Every inventory query runs off the loop.** They grow with the population, which is
  precisely what [ADR-0239](0239-off-loop-queries.md) removed from the single writer.
  The catalogue's "already held" marking reads only the asking user's entitlements —
  bounded per user, but it still takes the read view rather than the loop.
- **An entitlement stores a principal reference, never personal data.** Names,
  addresses and superiors resolve from the account. This is what keeps `applyToState`
  free of any key access, and it is what makes the personal-data record's erasure
  mechanism possible at all — an append-only log cannot forget what it was given in
  the clear.

Entitlements carry their **origin**: `ordered` when an order produced it, `adopted`
when reconciliation found it in a target system and somebody accepted it, `legacy` when
it came from the initial load at commissioning. The distinction is the difference
between evidence and an assumption. On the first day the inventory is empty and reality
is full; without `legacy`, the first reconciliation reports every existing privilege in
the estate as a discrepancy, and each of those carries an executable "remove in the
target system". A field that costs nothing now costs a migration of an append-only
column family later.

### Reconciliation — report with an action, never act alone

Atlas holds the intended state, reconciles against the target systems, and records
**transitions** rather than samples — the shape `api/panorama/drift.go` already uses,
for the reason given there: nobody reads a graph of "healthy, healthy, healthy". Unlike
Panorama's journal this one is durable, because it is evidence rather than a reading
surface.

A discrepancy is never acted on automatically. It carries two offered actions:
**remove in the target system**, which runs the service's deprovisioning process — not a
direct worker call, or there would be a second provisioning path outside the model and
outside the audit trail — and **adopt into the inventory**, which writes an entitlement
with origin `adopted`.

Atlas does not enforce. A system that silently removes privileges it did not grant locks
people out on its first bad reconciliation, and the value here is in naming the
discrepancy, which no other system in the estate can do.

### Consequences

- **Positive:** The catalogue is data and the fulfilment is a model, so a new service is
  authored, not coded. Dependency order, cycle freedom and binding integrity are proven
  at publish time. An order cannot change under an approver. Evidence outlives the
  instance. Nothing reaches a target system except through a process.
- **Negative / trade-offs accepted:** Three stores instead of one, and a rule per store
  about what may write it. Retention gains an exemption, which is a thing an operator
  must understand rather than infer. The basket carries two resolution steps that a
  simpler design would decide silently. Publishing can fail for reasons an author must
  then fix — which is the point, but it is friction at authoring time.
- **Follow-ups / risks to watch:** The checkpoint cost in the open question above, to be
  measured before the inventory is built rather than after. The initial load at
  commissioning is a deliverable, not a script somebody writes on the day. Whether a
  service can be held more than once is per-service data here; if that turns out to be
  per-variant, it is a migration.

## Pros and cons of the options

### One model
- Good: one store, one set of handlers, no question about which record is authoritative.
- Bad: one lifetime for three lifetimes. Either retention deletes evidence of a held
  privilege, or a completed instance is kept alive to preserve it. Editing the catalogue
  edits pending orders.

### Two models (order and inventory merged)
- Good: the inventory needs no separate write path — it is the successful orders.
- Bad: cannot represent an entitlement no order produced (pre-existing, or created
  directly in the target system), nor one that ceased outside Atlas. Both are the normal
  case, not the exception, and both fail silently.

### Three models
- Good: each store has one lifetime, one notion of truth and one writer. Reuses
  applications for versioning, the engine for orders, the state store for the inventory.
- Bad: the most machinery. Three stores must agree about what a service *is*, which is
  what the catalogue item id is for and what a review must check.

## Implementation

`api/catalog` carries the catalogue model, the ArchiMate import and `Publish` — the validation, the wave
schedule and the preconditions described above, with `Release.Blocked` answering which
lines a failure stops. `api/web/portal.html` and `portal.js` are the visitor's half: the catalogue they are
the audience for, what each product is made of, and their own orders with a status per
line. It computes an order's standing from its lines rather than reading a stored one,
for the same reason the server derives it — the two cannot then disagree. The brand a
visitor sees is their catalogue's, under its own record
([ADR-draft-portal-theme-per-catalogue](draft-portal-theme-per-catalogue.md)).

`api/order` carries the order model, the propagation and the orchestrator's two
questions:
`Next` and `Ready` say which lines may start — `Ready` with the process and variant an
orchestrator needs — `Apply` records what came back, `Propagate` marks what a settled
outcome stopped, `Derive` reads an order's own standing off its lines rather than
storing it, `Abandon` and `Reject` are the transitions into the two
decided outcomes, `Line.Valid` holds their rules at the persistence boundary, `Notices` reports what a change owes the orderer, and
`Assign`, `Escalate`, `Stall` and `Reassign` move an unanswered
approval along, make it visible when it can go no further, and let a person restart it
— without anything there ever deciding it. The inventory is not built yet, which is why
this record reads `Partial`.

**The deadline that drives them is a boundary timer on the approval task**, because a
deadline is a modelled fact: an installation changes P3D and P7D by editing its copy of
the approval, not by changing Go. Both timers are **non-interrupting**, and that is the
whole of "a clock never decides" expressed as a shape — an interrupting one would close
the task whose completion it is chasing, and whatever its path then did would be the
answer. A test refuses an interrupting deadline on an approval, and a second refuses any
deadline branch reaching the task that records a refusal.

`POST /orders/{id}/lines/{item}/escalate` is one hop, because one call is one elapsed
deadline. **The caller names the superior.** Who somebody reports to is a question for a
directory, and Atlas asks a directory through a worker, from a model; a server-side path
to one would be a configuration nobody set up and a credential the server does not hold.
What Atlas decides is whether the hop may happen — the loop guard, the already-held
guard, the end of the chain — and that stays in `Escalate`, pure. An empty superior is
not an error but the answer "nobody", and it stalls.

The assignment is written **when something first moves an approval**, not when the order
is placed: at placement the approver of a `superior` line is not known, and until
something moves it the live task's assignee is the whole truth. What outlives the task
is the history, and that is what is stored. It is written **before** the task is
reassigned (I2): a recorded hop with a task that did not move is repairable and visible;
a moved task with no record explaining it is not.

Two deviations from the design above, both deliberate. **Only the `superior` variant
escalates up a chain.** That variant *is* the line, so a step up it is the same
mechanism one step further, and it already requires the directory. A `fixed` approver is
a standing responsibility rather than a position in a line — handing their queue to
their own line manager is not obviously right, and the answer for a stuck one is that a
person reassigns it. A `role` approver is a group, which has no superior at all. Both
therefore remind and then **stall**, which is the same visibility by a shorter road; an
installation that wants the chain for its `fixed` approvals adds the lookup to its copy
of the model, exactly as the `superior` variant has it.

And **stalling is visible through `GET /api/v1/approvals/stalled`**. A stall records a
fact, and a fact nobody queries is not visible — which is the failure this whole
mechanism exists to prevent, one level up.

### Taking an order back

The likeliest support call a self-service portal receives is somebody who ordered the
wrong thing a minute ago. Until `POST /orders/{id}/cancel` the only answer was to
telephone the approver and ask them to refuse it — which files a decision nobody made,
in a record kept for years, in the place a reader goes to find out whether that
colleague's laptop was turned down.

A withdrawal is therefore its own line status and not a reuse of rejection, for the
reason the three failure statuses are three: they differ in who has to act. A rejection
is somebody refusing a request that was made; a cancellation is the request being taken
back, and nobody has to act on it at all. It carries an author and a moment like every
other way a line settles unprovisioned, and — unlike a rejection — no reason, because the
person a cancellation is explained to is the person who made it.

**What it takes back is exactly what has not happened.** A running line is with a
provisioning process now, which is a conversation with a system Atlas does not control;
stopping it halfway is not withdrawal but a half-provisioned account nobody owns. A
failed one has a decision of its own waiting — somebody gives up on it — and filing that
as a change of mind would record a repair nobody finished as one. A provisioned one is
held by the recipient, and undoing it is **deprovisioning**: it runs the process the
order froze for exactly that purpose, and it belongs with the inventory rather than
here. So the answer names both halves, because "your order is cancelled" when a laptop
is already on its way is the sentence that produces the second support call.

Two things follow that are not the order's own record. A cancelled line's **approval
instance is cancelled with it**: left standing it is a task asking somebody to decide a
request that no longer exists, and eventually they do. And the **orchestrator is woken**,
or it waits for a line that will never start.

A withdrawn line is a root cause like a refused one — anything requiring it is waiting
for nothing, and the block does not lift — and an order withdrawn in full reports
`cancelled` rather than `unfulfilled`. "Not fulfilled" is what an order says when it
tried and did not manage; telling somebody that about their own cancellation invites
them to ask why it failed, which is the call this was supposed to replace.

### Giving back what was granted

Every line has carried a `DeprovisionProcess` since orders existed, frozen at placement
so that revoking a grant uses the rules that were in force when it was made. Nothing
ever ran it. `POST /orders/{id}/lines/{item}/return` does.

A return is **not** a cancellation, and the two are separate statuses for a reason an
access record cares about: a line that was provisioned and given back is a different
fact from one that never was. A record saying "cancelled" where somebody held a laptop
for three weeks has lost three weeks, and an audit that cannot tell the two apart cannot
answer who had access when — which is the question such a record exists to answer.
`returning` sits between them and is deliberately **not settled**: something is in flight
against a target system, and an order reporting itself finished while an account is
half-deleted would be guessing at the one thing it is least entitled to guess at.

**It is refused while something still held requires it.** That is the precedence graph
read backwards, from the same `Requires` the waves were computed from. Provisioning
ordered the account before the laptop that needs it; giving back runs the other way, and
an account revoked under a laptop still using it leaves the laptop working until
somebody notices — or not working, for a reason nobody connects to this. The refusal
names what is in the way, because "cannot return" without it is an instruction to guess.

A return is **not** something a cancellation does on its own. Withdrawing takes back what
has not happened; revoking an access somebody has used for three weeks is a different
act with a different risk, and one click doing both deletes accounts on a mis-click. For
the same reason the page asks before it starts one.

Reporting it goes through the same endpoint a provisioning reports through, and only a
line already `returning` may be reported `returned` or `returnFailed` — otherwise a
provisioning worker could take a line somebody holds and record it as given back with
nothing having run.

**A revocation that runs and fails is its own status**, because the two plausible
alternatives both lose something. Read as `failed`, a reader concludes nobody has it —
and the precedence guard would then let the account underneath be revoked out from under
something very much still there. Fallen back to `done`, the fact that a revocation was
attempted and lost is gone, and the next person to look sees an ordinary held line with
no sign anything went wrong. So `returnFailed`: still **held**, not **settled**, and
asking for the return again is the ordinary repair.

**"Held" is wider than "provisioned", and that is the correction this cost.** The first
cut asked only whether a line was `done`, so a laptop whose revocation had merely been
*asked for* did not stop the account underneath from being revoked — the precise harm
the guard exists to prevent, written into the guard. A revocation that has been
requested has not happened: until it confirms, the access is there, and one that failed
is plainer still. Both count as held. What may be *returned* is the narrower question,
and asks separately — a return already under way must not be asked for twice, because
two revocations racing against one target system is how a half-deleted account happens.

Two things this deliberately is not. It is not the **inventory**: "what you hold, and
giving it back" is the recipient's question, and the recipient cannot see an order at
all. This is the *orderer* revoking what they asked for, which is why it takes the same
right the withdrawal takes. And there is no **return of a whole order**: that is the wave
schedule reversed, and the per-line guard already makes a caller do it in the only order
that is safe.

**Which process decides a line is resolved when fulfilment asks, and was wrong at
first.** The model built an approval's process id by concatenating the catalogue's
kind onto a prefix — `"atlas-genehmigung-" + "fixed"` — and the three approval
processes this binary ships are named in German. Not one of the three ids existed, so
no approval could ever have started; the model parsed, the processes compiled, and
every test passed, because a mapping that lives inside a string expression has nowhere
to be checked. `Line.ApprovalProcess` is that mapping as a table, `/next` carries the
answer, and a test walks every `ApprovalKind` against the processes the binary
actually deploys.

It is deliberately *not* frozen into the order, unlike everything else the order
copies. What the catalogue promised is frozen — the product, the variant, the rule it
is approved under — because a catalogue edit must not change a pending order. Which
model implements that rule is this installation's wiring, and freezing it would mean
an operator who redeploys an approval process breaks every order already waiting on
one.

## Links

- builds on [ADR-0128](0128-process-applications.md) — versioned, publishable bundles
- builds on [ADR-0028](0028-forms-and-the-tasks-app.md) and [ADR-0029](0029-public-process-start-links.md) — forms, the Tasks app and public start
- builds on [ADR-0189](0189-panorama-architecture-modeling-and-live-overlays.md) — the ArchiMate binding mechanism the import reuses
- amends [ADR-0115](0115-history-retention-hard-delete.md) and [ADR-0144](0144-per-definition-history-ttl.md) — the inventory is exempt from retention
- constrained by [ADR-0239](0239-off-loop-queries.md) and [ADR-0080](0080-runtime-aggregate-counters.md) — inventory queries run off the loop
- constrained by [ADR-0011](0011-single-binary-distribution-and-web-ui.md) — no external database
- companion records, to be written: personal data and erasure in the portal; portal
  roles and responsibilities, amending [ADR-0209](0209-roles-per-endpoint-group.md),
  [ADR-0180](0180-groups-as-members.md) and [ADR-0278](0278-object-authorization.md); a
  theme per catalogue, amending [ADR-0113](0113-org-wide-ui-theme.md) and
  [ADR-0263](0263-form-runtime-brand-theming.md); portal language following the browser,
  an exception to [ADR-0267](0267-console-speaks-german-first.md)
