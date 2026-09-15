# ADR-DRAFT: The inventory is taken before it is enforced, and what it records says Atlas did not grant it

- **Status:** Accepted
- **Implementation:** Partial
- **Date:** 2026-09-15
- **Deciders:** Atlas maintainers
- **Open question:** Whether a mapping per product scales to an estate whose rights are
  expressed as hundreds of groups nobody has named. The join works; what is unmeasured is
  how much of a real directory is *modellable* at all, and the `unmapped` roll-up this
  record introduces is the instrument for finding out — but nobody has run it against a
  real tenant yet.
- **Question checked:** 2026-09

## Context and problem statement

[ADR-0312](0312-portal-catalogue-order-inventory.md) put three models in place and named
what each is for. Two of them are built. The third — the inventory — has a store, a
write path from an order, and a read route, and it is **empty**, because the only thing
that has ever written to it is an order the portal itself fulfilled.

That is the whole problem, and it is worth stating in the order it actually bites:

1. On commissioning day the inventory is empty and reality is full. Everybody already
   has a laptop, a VPN account, a mailbox and eleven group memberships.
2. The next slice after this one is reconciliation: compare what Atlas believes against
   what the target systems hold, and report the differences.
3. Run against an empty inventory, reconciliation reports **every privilege in the
   estate** as a discrepancy. ADR-0312 already says what each discrepancy carries: an
   offered "remove it in the target system", which runs the deprovisioning process.
4. So the first reconciliation of a freshly commissioned portal is a list of several
   thousand executable actions whose effect, if anybody accepted them in bulk, is to
   deprovision the company.

`model.OriginLegacy` was added in ADR-0312 for exactly this, with a comment saying so,
and has had no writer since. This record is the writer.

The question: **how do the rights that already exist get into the inventory, and what
stops that process from being a second, unsupervised way to write evidence that is kept
for years?**

## Decision drivers

- Nothing may reach a target system except through a modelled process (ADR-0312). A
  load *reads* rather than writes, but a load that bypassed the model would be the first
  crack in that rule and would be cited as precedent by the second one.
- The load's output must be **checkable by a person**, because its output is evidence,
  and evidence nobody could audit is an assumption with better formatting.
- It must never downgrade knowledge Atlas actually has.
- It must be safe to re-run. A commissioning load is run, read, corrected and run again;
  one that were destructive on repeat would be run exactly once, badly.
- Every ceiling on external input reads a named budget, and a scan that grows with the
  population does not hold the single writer (I3, [ADR-0239](0239-off-loop-queries.md)).

## Considered options

1. **A script.** Somebody writes a one-off importer against the directory and the API on
   the day, runs it, and deletes it.
2. **Derive it from what Atlas already mirrors.** The Entra mirror
   ([ADR-0332](0332-entra-directory-provisioning.md)) already holds groups and their
   members. Walk them, match against the catalogue, write entitlements. No new
   endpoint at all.
3. **A generic observation endpoint**, fed by a modelled process, joined inside Atlas
   against a mapping each product declares.

## Decision outcome

Chosen option: **three** — a `POST /api/v1/inventory-load` that takes observations in the
target system's own vocabulary, resolves both halves inside Atlas, reports what it
decided, and writes only when asked.

Option 1 is what ADR-0312 already refused in one line — "the initial load at
commissioning is a deliverable, not a script somebody writes on the day" — and the reason
is not tidiness. The script's judgement calls are the interesting part: which account a
mail address belongs to, what a group means, what to do about the four hundred people it
could not match. A script makes all of them, leaves no record of any of them, and is
deleted.

**Option 2 deserves its refutation in full, because it is the cheaper option and it is
nearly right.** Atlas does hold Entra's groups and their memberships already; deriving
the inventory from them needs no endpoint, no worker and no process. Three things kill
it, and only the third is fatal:

- It covers only what the mirror reads. Licences are not group memberships, on-premises
  AD is not Entra, and SAP roles are neither. The two sources this installation actually
  named are AD *and* licence management, so the cheap option covers half of the stated
  requirement on day one.
- It makes the mirror's scope load-bearing for something else. Widening what the mirror
  reads would then be a change to the inventory's meaning, which is not a coupling
  anybody would choose deliberately.
- **It has no moment at which a person decides.** The mirror runs hourly and unattended.
  An inventory derived from it is a derived view that appears without anybody having read
  it — and the one thing this record exists to guarantee is that the first entry of
  several thousand permanent records into an evidence store is an act somebody performed,
  having looked at what it would do.

### The join is data, and that is the whole design

A target system reports a *subject* and a *right*. An entitlement holds a principal id
and a catalogue item id. Neither matches without a lookup, and where that lookup lives is
the only real decision in this record.

It could live in the worker: the process that reads AD knows that `CN=VPN-Users` is the
VPN service, because the provisioning process for VPN is what adds people to it. Then
Atlas receives `(principal, itemId)` and simply records it. Less machinery, and it keeps
target-system vocabulary out of Atlas entirely.

It is rejected because of what it does to the report. The load's output is read by a
person who has to decide whether to apply it. With the join in the worker, that report
says *"Alice holds VPN access"* — and there is nothing in it to check. With the join
here, it says *Alice is in `CN=VPN-Users`, and the catalogue says that group is VPN
access*, which is two facts a reader can disagree with. A report that cannot be
disagreed with is a report nobody is really reading.

So `catalog.Item` gains `Targets []TargetRef{System, Ref}`: what this product is called
out there. It is **not** a duplicate of the provisioning process's knowledge, though they
overlap. The process *acts*. This is a *claim*, and a claim is the thing reconciliation
can later test — which is the same reason it will still be needed after this slice.

Empty is the ordinary state, and deliberately so: a product nothing outside Atlas grants
declares nothing, and no load will ever name it. Requiring a reference would put a field
on every product whose author has no answer for it, which is how fields come to be filled
in with anything.

Publishing refuses **two products claiming one reference**. A right found under it could
be attributed to either, so a load attributes it to neither — the right stays out of the
inventory, and the reconciliation this whole exercise exists to make meaningful reports
it as a discrepancy regardless. The hole is invisible without the refusal. Publish sees
one catalogue's items, so the load checks again across the whole store, where a clash
between two separately valid catalogues is visible at all.

### The safe state is the zero value, for the second time

The field is `apply`, not `dryRun`, for the reason the directory mirror already argues: a
JSON field that is absent decodes to the zero value, so **the message that forgot to say
what it wanted writes nothing**. Spelled the other way the same omission enters an entire
target system's membership list into a store kept for years.

The report and the write are one decision and two functions. `decideInventoryLoad`
produces a complete plan; `applyInventoryPlan` writes it and re-decides nothing. A
preview with its own implementation agrees with the real run until the day it stops, and
the day it stops is invisible — the report is read, believed, and applied.

### Four rules, each of which is a way to destroy or invent evidence

- **Never downgrade.** `GrantEntitlement` replaces. A legacy grant written over an
  `ordered` right discards the order that produced it and the approval behind it, with
  nothing failing and nothing logging. This is the single most destructive thing the load
  could do and the reason it reads the inventory at all.
- **Legacy over legacy is unchanged, not rewritten.** `Since` is the only thing that says
  how long somebody has held something. A load that moved it would make the estate look
  freshly acquired every time anybody checked — the act of verifying would destroy what
  was being verified.
- **A draft product is never attributed; a withdrawn one still is.** They look
  symmetrical and are opposites. A draft may never be published, so evidence must not
  point at it. A withdrawal stops a product being *ordered* and says nothing about who
  holds it, which is why ADR-0312 has no deleted state.
- **The load only ever adds.** A right a batch does not mention is never revoked. A batch
  is one system's partial answer — a page of an export, one system of five — and treating
  silence as evidence of removal would revoke rights because a read stopped early.
  Revocation is reconciliation's, with a person deciding.

### What is not here, and why

**No cursor and no revision.** The directory mirror pins itself to a revision because a
delta read is destructive on repeat: advancing past a change set that was never written
loses it. A load carries absolute facts, so a batch delivered twice decides "already
recorded" the second time and writes nothing. It is **idempotent by construction**, which
is also why pages of one export need no ordering between them. Copying the mirror's
revision guard would have been cargo-culting its shape without its problem.

**No refusal when authentication is off.** The directory routes refuse outright in an
open installation, because a caller there could create an account and sign in as it. That
argument does not transfer: a load creates no identity and grants access to nothing. It
records a statement *about* people who already exist. In an open installation every route
is open — publishing a catalogue, placing an order — and singling this one out would look
careful while protecting nothing, in exactly the single-user mode where somebody tries it
first. The honest residual cost is stated in the source: a caller who can reach this can
record rights nobody holds, and the portal will then refuse to order those products for
them. That is a denial, not an escalation, and it is why the scope exists.

### Consequences

- **Positive:** `OriginLegacy` has a writer, so reconciliation can be built against a
  populated inventory rather than against an empty one. The `unmapped` roll-up produces a
  list nothing in the estate could produce before: what is granted that nobody decided to
  offer. The join is data, so the same mapping serves reconciliation.
- **Negative / trade-offs accepted:** A catalogue maintainer now has a field that
  requires knowing what a product is called in AD, and getting it wrong is silent until a
  load reports the right as unmodelled. A load's decision, its inventory reads and its
  writes all happen in one run-loop turn, so a batch is time the single writer is not
  executing processes — bounded by `InventoryObservations` and refused whole above it.
  Subjects that resolve to nobody are a list somebody has to work through, and on a first
  load it can be long.
- **Follow-ups / risks to watch:** Reconciliation and adoption, which are what the
  inventory is for. Whether `Since` recorded as the load's own moment turns out to be
  read as the day a person acquired the right, despite the origin beside it saying
  otherwise — if it is, the field needs splitting rather than explaining. And the open
  question above: how much of a real directory is modellable at all.

## Pros and cons of the options

### A script
- Good: nothing to build, nothing to maintain afterwards.
- Bad: its judgement calls are the substance and it records none of them. It is run once,
  by one person, and cannot be re-run, reviewed or explained a year later when somebody
  asks why four hundred people hold something.

### Derived from the Entra mirror
- Good: no endpoint, no worker, no process; the data is already here.
- Bad: covers only what the mirror reads, couples the mirror's scope to the inventory's
  meaning, and — decisively — has no moment at which a person decides. It would enter
  thousands of permanent records as a side effect of an hourly job.

### A generic observation endpoint with the join in Atlas
- Good: one mechanism for every target system, present and future. The report is
  checkable, because both halves of every attribution are in it. The mapping is the same
  one reconciliation needs.
- Bad: the most machinery, and a new field on every product that somebody has to fill in
  correctly. The join can be wrong in a way that is quiet until a load says so.

## Implementation

`api/inventoryload.go` decides; `api/inventoryloadapply.go` writes and renders;
`api/inventoryload_http.go` is the pair of routes; `api/inventoryloadstore.go` keeps one
record per target system answering *has a load ever been applied here* — which the
inventory itself cannot answer, since a system loaded and found empty looks exactly like
one nobody ever pointed at.

`catalog.TargetRef` and `Item.Targets` are the join, `checkTargets` the publish-time
refusal, and `examples/bestandsaufnahme.bpmn` the modelled process that reads a system
and reports what it found. That process has **no timer**: a commissioning load is an act
somebody performs, not a schedule, and a start event on a clock would be the quiet
opposite of everything above.

What keeps this record at `Partial` is that reconciliation and adoption — the two things
a populated inventory exists for — are still not built.

## Links

- [ADR-0312](0312-portal-catalogue-order-inventory.md) — the three models, the origins,
  and the paragraph this record implements.
- [ADR-0239](0239-off-loop-queries.md) — why the batch is bounded by a budget rather than
  by the population.
- [ADR-0332](0332-entra-directory-provisioning.md) — the accounts a load resolves its subjects
  against, and the two-function split this record reuses.
- [ADR-0194](0194-api-tokens.md) — the scope that confines the credential a load carries.
