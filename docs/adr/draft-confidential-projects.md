# ADR-DRAFT: Confidential projects — an instance is its project's to see, operators included

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-10-02
- **Deciders:** Atlas maintainers
- **Open question:** the cost of the rule on a server with thousands of deployed
  definitions has not been measured. The veil is one pass over the deployment map on
  the run loop per request, and only for a caller who is not a member of every marked
  project — cheap at the sizes Atlas is run at today, and unproven at ten times that.
- **Question checked:** 2026-10

## Context and problem statement

[ADR-0275](0275-instance-visibility.md) answered "who may read a running instance's
data" with the project: a member of the project the definition was deployed from reads
it, a task holder reads the fields of their form, anybody else reads nothing. It left
one population out on purpose. Operators and admins kept reading everything, because
they could already list every instance and the per-instance endpoint was not the place
to narrow that.

On a single team's server that is the right answer. On a *shared* installation it is
the gap. "Operator" is a role on the server, not a relationship to the work: the
operator of team A lists team B's instances, opens their timeline, reads their
variables, cancels them, resolves their incidents and completes their jobs. The
request that forces this record came out of the mailbox Worker
([ADR-0438](0438-mailbox-worker.md)): a process that reads a person's mail carries that
mail in its variables, and on a shared server every operator could read it there even
though nobody but the mailbox's owner could use the Worker.

The question: how does a team keep the instances of its work to itself on a server it
shares, without taking the operator role away from the people who run everybody else's?

## Decision drivers

- **Nothing changes for anybody who does not ask.** A server whose operators run every
  team's incidents is a legitimate way to run Atlas. Whatever closes the gap must be
  opted into, per project, by the people whose data it is.
- **One predicate, asked everywhere.** An instance is reachable through lists, counts,
  search, a dozen per-instance views, bulk actions, the job protocol and the task
  inbox. A rule each of those re-derives is a rule one of them gets wrong.
- **The worker protocol keeps running.** A worker serving a confidential process must
  still lease its jobs, or the mark stops the process it was meant to protect.
- **Fail closed.** A deleted definition, a server restart, a principal the request
  could not resolve: each must leave the instances hidden, not exposed.
- **No engine invariant moves.** This is a question about who may see what through
  the API. It must not reach the log, `applyToState` or the processor.
- **Cost nothing where unused.** Every instance read goes through the check; on a
  server with no marked project it must cost no turn on the run loop.

## Considered options

1. **Confidential projects:** an owner marks a project; its instances are visible to
   whoever the project grants viewer or better, and to nobody else whatever their role.
2. **Operators per project by default:** the operator role reaches only the projects
   one is a member of, everywhere.
3. **Encrypt instance data per project:** variables at rest under a per-project key.
4. **A separate installation per team:** the answer before this record.

## Decision outcome

Chosen option: **1, confidential projects**. It closes the gap for exactly the
projects whose owners ask, with the membership model teams already maintain, and it
leaves every other project and every existing installation as it was.

### The rule

A project carries `confidential` (owner only, as every other access change). Its
instances are hidden from a caller when the project grants them less than viewer — the
same `effectiveRole` every artifact route asks. So the owner, a member (directly or
through a group, [ADR-0180](0180-groups-as-members.md)), an admin, and a machine
credential whose reach names the project
([ADR-0410](0410-a-peer-credential-carries-the-reach-a-membership-cannot-give-it.md))
see them; nobody else does, the operator role included.

The rule is one value, the **veil**: the set of definition keys whose instances a
caller may not see, computed on the run loop per request from an in-memory index of the
marked projects and the deployment map. The index is fed by the project store itself
(a change hook on every save and delete), and seeded at startup before the server
answers anything. With no project marked the check is a lock-free emptiness test and
no turn on the loop.

### Where it is asked

- **Lists and counts:** the instance list (both halves, one definition, one element;
  an exact total stays exact by subtracting the hidden definitions' counters), the
  instance summary, the instance search (live rows and archive rows alike), the
  incident list and summary, the cross-instance data objects, a decision's
  evaluations, the task inbox, folders and their counts.
- **One instance:** every `/instances/{key}/…` read, `/processes/{key}/runtime` and
  `/collaborations/{key}/runtime`. The route table declares the check beside the role
  (`apiOp.veil`), the mount puts it in front of the handler, and
  `TestEveryInstanceRouteIsVeiled` fails the build for a keyed route that forgets it.
  A hidden key answers 404 with the route's own "no such" message.
- **Actions:** cancel, bulk terminate (by key and by definition), drain a definition,
  resolve one or many incidents, complete, fail and lease a job, claim, release and
  complete a task.
- **Variables:** an operator who is not a member falls through to ADR-0275's task
  holder rule: a task they hold still opens its form's fields.

### What it deliberately leaves alone

- **The worker protocol, for machines.** A job route called with a machine credential
  (the server's own service identity, an API token) is not veiled. A worker is part of
  the process's execution, and its credential is one an admin issued. A *person* on the
  same routes is veiled: leasing by type skips the hidden jobs, a job by key answers 404.
- **Tasks addressed to somebody.** A task the model offers to a person or a group
  stays theirs, member or not — that is the project's own decision about who works on
  it. What changes is open work: an unaddressed task of a confidential project is the
  members' open work, not every account's, and an operator does not hold every task.
- **The definition.** It is still listed, its model still readable, an operator may
  still start it and switch it active or inactive. Its instances are what the mark
  covers.
- **Existence and aggregates.** A hidden key answers 404 where a missing key answers
  404, but a route that answers a missing key with an empty 200 now answers a hidden
  one with 404, so a determined caller can tell "hidden" from "absent". The server-wide
  counts (`/stats`, the Workers view) still include hidden instances. Neither discloses
  a byte of an instance's data, and volume was never what the mark protects.
- **Admins, backups, logs.** An admin reads everything; a backup holds everything; the
  server log is the admin's. The mark is a visibility rule in the API, not encryption.
- **The OpenSearch export.** The exporter mirrors the log, and with it every
  confidential instance, into an index this rule does not reach. Marking a project on
  a server that exports answers with a warning saying so, and the handbook says it
  again. Archive rows of a definition this server no longer knows at all are shown —
  they cannot be placed.
- **Call activities.** The mark follows the definition, not the call chain. A child
  process deployed from another project is that project's, whatever data its parent
  handed it.
- **The order and catalogue domain.** Orders, approvals, stalled approvals and the
  event feed have their own ownership rules ([ADR-0429](0429-product-actions-are-commands-with-published-outcomes.md)).
- **The preview outbox.** It is a development aid, in memory, and holds every preview
  mail for every operator.

### Keeping it closed

- **Deleting a definition** of a marked project records its key on the project
  (`retiredDefinitions`) before the deployment goes. Its finished instances outlive it
  in the store, and without that record they would have no project left to hide them.
- **Deleting a marked project** is refused (409). Removing the mark first is one more
  step, and it is the step the audit trail records.
- **Moving a definition out** of a marked project is the owner's, not an editor's:
  the instances leave the confidentiality with it.
- **A protected system project** cannot be marked; the existing guard refuses every
  change to one.
- **Marking and unmarking** are grant-audit events (`confidential`,
  [ADR-0184](0184-grant-audit-log.md)).

### Consequences

- **Positive:** a team on a shared server keeps its instances — data and actions — to
  its members with one switch, and the switch costs nothing on a server nobody flipped
  it on. The rule is one value asked in one way, and the route table enforces that the
  keyed routes ask it.
- **Negative / trade-offs accepted:** an operator who is not a member can no longer
  help with a confidential project's incidents; the team, or an admin, has to. That is
  the point, and it is also a cost a team pays for marking. A non-member can tell a
  hidden key from an absent one on some routes.
- **Follow-ups / risks to watch:** per-project filtering in the OpenSearch exporter;
  encryption at rest for teams whose threat model includes the admin; the open
  question's measurement; a confidential badge in the Console's application list.

## Pros and cons of the options

### 1. Confidential projects
- Good: opt-in, per project, by its owner; reuses membership, `effectiveRole`, the
  grant audit; one predicate.
- Good: touches no engine invariant — design-time sidecar data and API checks only.
- Bad: every route that can reach an instance has to ask, which is why the route table
  carries it and a test reads the table.

### 2. Operators per project by default
- Good: no switch to forget.
- Bad: changes what the operator role means on every existing installation, and breaks
  the shared operations desk that is a legitimate way to run Atlas. Steelmanned, it is
  the cleaner end state — "a role is never a relationship" — but it is a migration
  every installation pays for whether it has the problem or not.

### 3. Encrypt instance data per project
- Good: the only option that holds against an admin, a backup and the exporter.
- Bad: FEEL evaluates variables, the search indexes them, the timeline shows them —
  each would need the key, and key custody is a product of its own. Out of proportion
  for the problem asked, and not excluded as a follow-up for a team whose threat model
  includes the admin.

### 4. A separate installation per team
- Good: complete isolation.
- Bad: exactly what the request rules out — the shared installation is the case.

## Links

- extends [ADR-0275](0275-instance-visibility.md) (instance visibility) to operators
- builds on [ADR-0071](0071-sharing-scopes.md) (sharing scopes) and
  [ADR-0180](0180-groups-as-members.md) (groups as members)
- relates to [ADR-0438](0438-mailbox-worker.md) (the mailbox Worker that prompted it),
  [ADR-0114](0114-opensearch-event-exporter.md) (the exporter it does not cover),
  [ADR-0184](0184-grant-audit-log.md) (the audit it writes),
  [ADR-0209](0209-roles-per-endpoint-group.md) (the route table it extends)
