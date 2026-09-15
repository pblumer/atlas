# ADR-0332: Accounts and groups are pulled from Entra by delta query, and the first run writes nothing

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-13
- **Deciders:** Atlas maintainers

## Context and problem statement

Atlas creates an account in exactly one situation: somebody signs in through OIDC for
the first time (`api/oidclogin.go`). Everything else — the roles a route asks for
(ADR-0209), the group memberships a project scope resolves against (ADR-0180), the
task assignments and the audit trail — hangs off a record that does not exist until
its owner turns up.

On the day an installation goes live that is an almost empty user store, and the next
slice of work makes it a blocker rather than an inconvenience. The inventory slice has
to attribute a permission it finds in AD or Entra to an *Atlas account*, because a
permission holds a principal id. Against an empty store almost every line it found
would be unattributable.

So the accounts have to exist before anybody signs in. The question is how they get
there.

The obvious answer is the one Entra administrators already know: **inbound SCIM**.
Entra's provisioning service pushes users and groups at an application, and the
administrator gets a provisioning blade with status, quarantine and retries. But Atlas
runs internally here, and whether it can be published outward is unresolved — which
makes the obvious answer the one that cannot be built.

What can be built is already in the tree. `connector/entra` has supported
`delta-users` and `delta-groups` since ADR-0172: Graph change-tracking queries that
return `{value, deltaLink}`, carry deletions through as `@removed` rather than
filtering them out, and resume from a cursor. Atlas can read outbound and
incrementally, from a model, with no network opening and no second service provider.

## Decision drivers

- **The inventory slice is blocked on it.** Whatever is built has to leave a stable
  identifier on an account that the next slice can match a found permission against.
- **No inbound path.** The network question has to disappear rather than be deferred.
- **A first run touches everybody.** An empty cursor enumerates the whole tenant
  against an empty user store. A defect in the merge or the role logic does not hurt
  one person on that run, it hurts all of them.
- **One mechanism, not two.** An account must not be able to get into the user store
  by a second route that no model can see.
- **A leaver must lose access quickly.** The interval is the delay with which somebody
  who has left keeps an account and its grants, and it is the only axis on which
  pulling is worse than being pushed.

## Considered options

1. **Inbound SCIM**, with Entra's provisioning service pushing at Atlas.
2. **Inbound SCIM through Microsoft's on-premises provisioning agent**, which reaches
   an application inside a network without publishing it.
3. **Outbound delta queries from a scheduled BPMN process**, reporting to an API route
   that writes.
4. **Outbound delta queries into a new operation on the user-provisioning worker**
   (ADR-0123) rather than an API route.

## Decision outcome

Chosen option: **3 — pull, on a schedule, from a model, into one API route — and the
first run writes nothing.**

### Why pulled and not pushed

This is the paragraph that has to survive, because in a year somebody will build
inbound SCIM beside this and it will be nobody's fault if the argument is not written
down.

**The strongest form of the case for SCIM**: it is what Entra administrators know. The
provisioning blade shows per-user status, quarantines a failing target, retries on its
own schedule, and Entra owns the cadence — so the answer to "why has this person not
appeared" is on a screen Microsoft maintains rather than in an Atlas incident. Option 2
even removes the network objection: the provisioning agent is designed for exactly
this, an application inside a network that Entra cannot reach.

It was still not chosen, for three reasons:

- **The network question disappears entirely rather than being answered.** Option 1
  needs a published endpoint; option 2 needs an agent installed, registered and
  maintained, and an argument about what that agent may reach. Option 3 needs the
  Entra Worker that this installation already has.
- **The same query carries the next slice.** `delta-users` returns an account's licence
  assignments in the same read, so the inventory is another step in this process rather
  than a second integration.
- **The state stays in one place.** With SCIM, Entra holds the watermark and Atlas
  holds the accounts; reconciling them means asking Microsoft. Here the cursor and the
  accounts are written by the same run, and a report says what that run decided.

The cost accepted is the one that is real: **latency**. A leaver keeps their Atlas
account for up to the interval. That is why the interval is an hour, and why an hour
is defensible: Entra's own incremental provisioning cycle runs about every 40 minutes
and is not configurable, so a pushed integration would not have been meaningfully
faster. (Verified against Microsoft's documentation, 2026-09; it is a documented
figure rather than a guarantee, and an installation that needs less can change its
copy of the model.)

### Why an API route and not the user-provisioning worker

**The strongest form of the case for the worker** (option 4) is genuinely strong, and
it is the option that would have been *safer*: ADR-0123 already decided that a process
may manage Atlas logins, through an in-process `atlas:userConnector` gated to the
protected system project and to an operator's explicit opt-in. There is no credential
in that path, no route, no scope question, and therefore no "unauthenticated caller"
case to close at all.

It was not chosen because the shape does not fit:

- A `userConnector` task provisions **one** account. This slice's unit is a batch, and
  the three properties the batch needs — one decision shared by both modes, a report
  over the whole set, and a compare-and-swap on the cursor — cannot be expressed one
  job at a time. Six thousand accounts would be six thousand jobs and six thousand
  run-loop turns, with no point at which anything holds the whole decision.
- The process-calls-a-route shape is the one this part of the tree already uses: the
  portal's fulfilment model reports a provisioned line the same way.
- The next slice writes portal state (permissions with `model.OriginLegacy`), which is
  only reachable over HTTP.

### The dry run, and the four properties it rests on

The first run enumerates the whole tenant against an empty user store. It writes
nothing until a person has read what it would do.

1. **One code path, with the write suppressed at the end.** `decideDirectorySync`
   produces a `directoryPlan` holding every record it *would* write, complete;
   `applyDirectoryPlan` saves that plan and contains no rule at all. The reporting mode
   calls the first and not the second. A preview computed by code of its own is a
   claim about what another piece of code would do, true until the day the two diverge
   — and that day is invisible, because the report is read, believed and applied.
   `TestDryRunAndApplyDecideTheSame` holds both modes against one input and compares
   what they decided; `TestAReportPredictsWhatTheRealRunDoes` does it end to end over
   HTTP.

2. **The safe state is the zero value.** The mode travels as `apply bool`, never
   `dryRun bool`. An omitted JSON field decodes to the zero value, so the message that
   forgot to say what it wanted reports instead of provisioning. This direction is not
   a preference: one way round, the mistake produces a report nobody acts on; the other
   way round it produces a directory nobody asked for.

3. **The cursor does not move.** A reporting run that stored the returned `deltaLink`
   would destroy exactly the changes it just examined — the next real run would resume
   past them, find nothing, and leave the store empty with nothing reporting it. This
   is the most dangerous single trap in the slice, so it has a test that does the thing
   that would expose it: report, then apply the same batch, and require the real run to
   still see the whole tenant (`TestAReportingRunDoesNotMoveTheCursor`). A second test
   fingerprints the accounts, the groups and the sync state on disk and requires a
   reporting run to leave every byte alone.

4. **The report is for people.** Counts for the expected — created, updated, disabled —
   and whole lines for the notable: merges, disables, refusals, memberships that could
   not be resolved, a merge onto an account holding more than the default role. The
   lines are bounded by a named budget (`limits.DirectoryReport`); what does not fit is
   counted, never dropped in silence.

**Who flips the switch.** The model carries the mode, and an operator edits their copy
and redeploys — the same principle by which ADR-0051 puts the schedule in the model and
ADR-0122 has an installation own its copy of a platform process. The counter-argument
is real: a switch somebody has to flip is a switch somebody forgets, and a deployment
that ran dry for a month while nobody noticed is worse than one that wrote too eagerly
on day one. So the mode is made **loud** rather than automatic: every report states
`applied: false` with a sentence saying why, and both the report and the state route
carry `everApplied`, which stays false until a run has actually written. A mirror left
in reporting mode does not look like a mirror with nothing to do.

### The decisions that had to be made along the way

**Where the cursor lives.** In a one-record sidecar store (`directory-sync`), classified
in the store registry so a backup carries it. It is not in the model, because it has to
survive restarts and redeployments; it is not in the event log, because it is operating
state rather than engine state. A lost cursor costs one full enumeration and loses
nothing, and that asymmetry is what lets the answer be this small.

**Which identifier an account carries.** The Entra object id, in a new `DirectoryID`
field — and in `ExternalID` with `Source: "entra"` for accounts this mirror creates.
The second field is not redundancy. An ID token's `sub` from Entra is *pairwise*:
derived from the user and the application, so two applications signing in the same
person receive different subjects, and the subject is therefore never the directory
object id — that is `oid`. (Verified against Microsoft's documentation, 2026-09.) An
account created by a federated login holds that pairwise subject in `ExternalID` and
must keep holding it, or the next sign-in stops finding it and creates the duplicate
this whole rule exists to avoid. So one record carries both identities: the subject it
signs in with, and the object id the mirror follows it by.

**Duplicate accounts.** Since the identifiers cannot be joined, the address is the only
bridge there is, and it is a weaker one — an address can be reassigned. The result is
still a **merge** and never a second account: permissions, assignments and the audit
trail hang off one record, and a duplicate leaves the person holding one and signing in
as the other. Every merge is an individual line in the report, and a merge onto an
account that holds more than the default role gets a second line saying so. Where an
address is already held by an account mirroring a *different* object, nothing is
guessed: it is refused and reported.

**Leaving means disabling.** `@removed` and `accountEnabled: false` are the two
signals, and both set `Disabled`. Deleting a record that permissions hang off turns
every reference to it into a dangling one; that is a harder problem than a disabled
account nobody cleans up. The same holds for a group the directory dropped: its
membership is emptied — which is the revoking direction — and the record is kept,
because grants were made through it.

**Roles.** A created account gets exactly `[RoleUser]`, from a literal. There is no
mapping and no attribute a tenant administrator could set that reaches that line, and
`oidcMapping` is deliberately *not* reused: it maps the claims of a token, and a delta
read carries directory attributes rather than claims, so reusing it would mean
inventing a claim source. An account that already exists keeps the roles it has, in
both directions — widening them here would be the mirror granting authority, narrowing
them would be the mirror taking an administrator's away because a directory that knows
nothing about Atlas said nothing about it. The impossibility is a test
(`TestNothingInAMessageCanGrantARole`), not a comment. And the last enabled
administrator is never disabled, by the same guard the administration API applies: an
instance whose every administrator has left the tenant is one nobody can repair.

**Groups, and the order things arrive in.** A mirrored group keeps two lists:
`ExternalMembers`, the directory's own object ids whether or not they resolve, and
`Members`, the subset that resolves to an Atlas account — recomputed on every run,
for every mirrored group, including the ones the message did not mention. A membership
can arrive before its account, and a change-tracking read never mentions it again, so
translating on arrival and discarding what does not translate would lose it for good.
Holding the id costs nothing and resolves itself on the run that reads the account.
Members that are not people — nested groups, service principals, devices — are dropped
and named once, because an id that can never resolve would otherwise be a line in every
report forever.

**Idempotency.** The job protocol delivers at least once. The state carries a
`Revision`, the state route hands it out, and the message pins itself to it with
`fromRevision`; a write happens only while that revision is still current. A repeat
delivery is decided and reported and writes nothing, and says "repeat" rather than
reading like somebody left the mirror in reporting mode.

### The security frame

- **No unauthenticated path, in any mode.** Both routes refuse outright when the server
  runs without authentication — the only routes in Atlas that do. An open installation
  is a deliberate single-user mode everywhere else; here an unauthenticated caller could
  create an account and sign in as it the moment authentication was switched on, which
  is not a smaller version of the feature but a different one.
- **The credential is not a user account.** A new confined scope, `directory`, reaches
  these two patterns and nothing else (ADR-0194). A provisioning token cannot deploy,
  cannot start an instance and cannot read the log. It is **not** made an
  administrator: `TestTokenRolesNeverIncludeAdmin` still holds, and the confinement is
  the allowlist.
- **Disabling is not only a record.** A session that is already open is not re-checked
  against the user store on every request, and an OAuth grant can stand for months
  (ADR-0200), so writing `disabled: true` stops nothing by itself. A run that writes
  therefore also ends the person's live sessions and revokes their standing grants —
  what the administration API has always done — and pushes every mirrored group
  membership it changed into the sessions that are already open (ADR-0185), because a
  session carries the group ids it was opened with and nothing on the access path
  re-reads the group store. Without that half, a mirror would be a *quieter* way to
  disable somebody than the button that says so, and the gap would last until the
  session expired. It runs after the run-loop turn and never inside it: revoking grants
  dispatches onto the loop, which from inside a turn is a deadlock rather than a slow
  path.
- **Everything is audited**, including a run that wrote nothing: somebody read a whole
  tenant out of a directory, which is an event even when nothing changed here. Accounts
  are recorded under the existing `auth.user_created` / `auth.user_updated`, so an alert
  written against those keeps working whoever made the change. The Graph cursors appear
  in no audit line and no error message — they are not credentials, but a URL naming a
  tenant, shipped to wherever the logs go, is a disclosure nobody chose.

### Consequences

- **Positive:** the inventory slice is unblocked, with a stable identifier to attribute
  a found permission to. No network opening, no second service provider, no second way
  into the user store that no model can see. The interval, like every other schedule
  here, is a property of an installation's copy of a model rather than of the source.
- **Negative / trade-offs accepted:**
  - A leaver keeps their account for up to an hour.
  - The decision and the write happen in **one run-loop turn**, which is what makes the
    plan describe the state it is written against — and which stalls process execution
    for the length of the write. `limits.DirectoryObjects` (2000) is what bounds that,
    and a message above it is refused *whole* rather than truncated, because a short
    change set recorded as a complete one loses the rest for good.
  - The change set travels through a process variable, so `limits.Variable` bounds how
    large a tenant one read may carry. A narrow `$select` and `maxUsers` on the task are
    the levers; beyond that the budget is raised, or the batch is chunked across calls,
    which is the follow-up below.
  - For a mirrored group the directory decides the membership, so a member added here by
    hand is removed again on the next run. This is reported by name every time it
    happens, but it is a real change in what a group record means.
  - A mirrored account or group deleted by hand through the administration API is not
    recreated: the change-tracking read will not mention the object again. The recovery
    is deliberate and cheap — delete the `directory-sync` state record, and the next run
    enumerates the whole tenant from scratch (with `apply` still deciding whether it
    writes). That asymmetry, a lost cursor costing one full read and nothing else, is
    what the whole design leans on.
  - The routes name `RoleOperator`, because a token can never hold `RoleAdmin` and a
    scheduled process has to be able to call them. An operator with a browser session
    can therefore post a hand-written change set. What that reaches is bounded — it can
    create accounts holding only the default role, it cannot disable the last
    administrator, and it touches only mirrored groups — but it does include the
    membership of a mirrored group, so **a project scope granted to a mirrored group is
    a grant delegated to the directory and to the operator role**. An installation that
    is not willing to delegate it should not attach scopes to mirrored groups. Naming
    `RoleAdmin` instead would make the route unreachable by any machine credential,
    which is the same as not building the feature.
- **Follow-ups / risks to watch:**
  - The inventory slice adds licence assignments from the same `delta-users` read and AD
    group memberships from `connector/ad`, writing permissions with `model.OriginLegacy`.
    It needs a mapping from an AD group and a licence SKU onto a catalogue item first.
  - Chunking a batch across several calls, so a tenant larger than the object budget is
    a longer run rather than a refusal.
  - The model lives under `examples/` and not in the embedded system bundle
    (ADR-0122), deliberately: a timer-started process in that bundle would arm on every
    installation and raise an incident every hour on the ones with no Entra tenant. The
    bundle's processes are inert until something starts them; a scheduled one is not.
    If the bundle ever grows a way to ship a process unarmed, this belongs in it.

## Pros and cons of the options

### Option 1 — inbound SCIM
- Good: the interface Entra administrators know; Entra owns the schedule, the retries
  and the quarantine; per-user status on a screen Microsoft maintains.
- Bad: needs Atlas published outward, which is unresolved here; a second provisioning
  surface with its own authentication; the watermark lives at Microsoft, so
  reconciliation means asking them; it does not carry the inventory slice.

### Option 2 — SCIM through the on-premises provisioning agent
- Good: keeps option 1's operational story without publishing Atlas.
- Bad: an agent to install, register and maintain, and a fresh argument about what it
  may reach; still a second provisioning surface; still does not carry the inventory.

### Option 3 — outbound delta into an API route (chosen)
- Good: no network opening; the credential stays in the worker; the same query carries
  the inventory; the cursor and the accounts are written by one run; the schedule is a
  property of the model.
- Bad: leaver latency bounded by the interval; the most powerful route in Atlas now
  exists and has to be confined on three axes; the batch is bounded by a run-loop turn.

### Option 4 — outbound delta into the user-provisioning worker
- Good: no credential and no route at all; already the decided place for a process to
  manage Atlas logins (ADR-0123); gated to system processes and to an opt-in.
- Bad: a `userConnector` task is one account, and this slice's unit is a batch — the
  shared decision, the whole-set report and the cursor compare-and-swap have nowhere to
  live; it is also coupled to an opt-in that turns on create/set-password/disable for
  every system process.

## Links

- builds on [ADR-0172](0172-entra-id-connector.md) — the Entra Worker, `delta-users`,
  `delta-groups`, and the cursor that travels through the process
- extends [ADR-0044](0044-user-management-and-authentication-boundary.md) — the user store, and `Source` stored so
  external identities could coexist later without a migration
- relates to [ADR-0210](0210-federated-authentication.md) — federated login, and the pairwise subject
  that is why `DirectoryID` is a second field
- relates to [ADR-0209](0209-roles-per-endpoint-group.md) — the role each route names
- relates to [ADR-0180](0180-groups-as-members.md) — groups as scope members, and
  why a mirrored group's membership is a grant
- relates to [ADR-0194](0194-api-tokens.md) — API tokens and the scope allowlist
- relates to [ADR-0051](0051-timer-start-events.md) — schedules in the model, not the source
- relates to [ADR-0123](0123-sanctioned-user-provisioning-for-system-processes.md) — the per-account worker
  this deliberately does not extend
- relates to [ADR-0122](0122-protected-system-project-and-bootstrap-deployment.md) — why this model ships as an example
  rather than in the embedded bundle
