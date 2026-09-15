# ADR-DRAFT: Reconciliation reads absence as a finding, and only inside a scope somebody promised was complete

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-15
- **Deciders:** Atlas maintainers
- **Open question:** Whether the whole-inventory walk this performs stays workable as the
  entitlement family grows. It is measured here only in the sense that it is now the one
  place doing it, off the loop, once per run — nobody has run it against a million rows.
  The fix if it hurts is a by-item index, named below, and it is the wrong trade until
  somebody has the number.
- **Question checked:** 2026-09

## Context and problem statement

The inventory asserts something Atlas cannot guarantee. [ADR-0312](0312-portal-catalogue-order-inventory.md)
says so in as many words: an entitlement "asserts what *is true in another system* — an
assertion Atlas cannot guarantee, because target systems are changed outside Atlas."

So it decays. An administrator removes a group membership in a hurry; a script tidies up;
a merger moves people wholesale. Every one of those makes the inventory wrong, silently,
and an inventory nobody checks is a list of things that were once true. The
commissioning load ([ADR-0333](0333-inventory-commissioning-load.md)) filled it; nothing
since has asked whether it is still right.

**The question this record answers: how does Atlas compare what it believes against what
a target system holds, without the comparison itself becoming the thing that locks people
out?**

## Decision drivers

- Atlas must not enforce. A system that silently removes privileges it did not grant
  locks a company out on its first bad reading, and the reading is the part Atlas is
  least entitled to be confident about — it is one worker's answer about somebody else's
  system.
- Nothing reaches a target system except through a modelled process (ADR-0312).
- A journal of samples is a journal nobody reads. `api/panorama/drift.go` already argues
  this and stores transitions instead; the same shape applies, durably.
- The comparison must not hold the single writer (I3, [ADR-0239](0239-off-loop-queries.md)).
- Every ceiling on external input reads a named budget — including, here, a ceiling on a
  *store*, which is new.

## The one hard problem: silence

A commissioning load reports what it **found** and never what it did not, and that is
load-bearing: a batch is one system's partial answer, and treating its silence as
evidence of removal would revoke rights because a paginated read stopped early.

Reconciliation is the exact opposite by necessity. **Absence is the finding.** A right
Atlas records and the target system does not report is precisely what this exists to
surface — and that makes the same silence dangerous in exactly the place the load was
safe. A worker that returned half a group, reported as the whole group, is a report that
everybody in the other half has lost their access.

### Considered options

1. **Whole-system runs.** One run covers everything a system holds, so absence inside it
   is meaningful.
2. **Scope by subject.** "This is everything Alice holds in system X."
3. **Scope by reference.** "These are the complete memberships of these groups."

**Chosen: three, and then two beside it.** A run declares what it read *completely* —
`refs`, the references, or `subjects`, the people — and the comparison happens only inside
that scope. Outside it nothing is concluded: an entitlement no promise covers is not
missing, it is **unexamined**, and a reconciliation that could not tell those apart would
report the whole inventory as wrong on its first partial read.

Option 1 fails on arithmetic. A whole system does not fit in one message, so it pages —
and a paged run is a run whose parts are each incomplete, which is the problem restated
rather than solved.

Option 3 was built first, because it matches how target systems actually export and what
the catalogue already declares: `catalog.TargetRef` is the reference, and a group listing
is exactly one complete scope.

Option 2 is now built beside it, and the two are **not alternatives**. They are the same
promise about the two axes of the same table, and one run may make both. What separated
them was never the comparison; it was that they need different readings from the caller.
See *The other axis* below.

**A scope is required and has no default** — at least one of the two, and a run naming
neither is refused. The only candidates for a default are "nothing", which is useless, and
"everything", which is a guess that turns a truncated read into a report that the estate
has lost its access.

**The promise is unverifiable, and that is stated rather than papered over.** Atlas
cannot check that a caller read a group whole. There is one place where the temptation to
add a heuristic is strong — a reading carrying *no observations at all* — and it is
refused: a special case for zero protects against one shape of a broken reading and not
against a worker that returned half, and a protection that does not generalise is a
comfort blanket that makes the contract less clear. What is special about zero is the
**prior**, not the logic, so the findings stand and the report says out loud that an empty
answer is far more often a failed read than an emptied estate. Nothing acts on a finding
without a person, and that sentence is what the person needs.

## The other axis: scope by subject

`refs` cannot answer the question an offboarding asks. *Is this person out of everything?*
is not a statement about a group, and it cannot be assembled from statements about groups:
you would have to reconcile every group in the system and observe the person's absence
from all of them, which is option 1 wearing a disguise — with the same arithmetic against
it.

So `subjects` names the people whose holdings this run read **whole**, in the target
system's vocabulary, exactly as an observation's subject is named. It is the same
unverifiable promise as `refs`, about the other axis.

**A pair is examined when either promise covers it**, and they compose: a run may say
"these two groups whole, and everything Ada holds". That is one predicate,
`reconcilePlan.examined(principal, item)`, and everything downstream asks it rather than
asking about an item — including the closing of findings, which is where getting this
wrong would be expensive.

Two things about the subject axis are decisions rather than mechanics:

- **A subject-scoped run is confined to the system it names.** Without that, a run that
  read everything Ada holds in Active Directory would report her Jira rights as missing —
  true of nothing, and a finding that invites somebody to revoke a correct record. So the
  scope covers only items a product declares a target reference for in *this* system.
- **A subject that resolves to no account is never a clean result.** It is reported, and
  it is deliberately *not* taken into scope, because the alternative is an offboarding
  reading the absence of an account as the absence of access. A misspelled object id would
  otherwise come back as "they are out of everything" — the most dangerous true-looking
  answer this endpoint can produce.

The empty answer that follows a clean leaver gets **its own sentence in the report**, and
that is not decoration. A verification that returns nothing looks exactly like a run that
did nothing, and an offboarding file needs the difference in words.

This axis buys no arithmetic. A subject-scoped run still walks the entitlement family
whole, for the reason in *The population-sized read* below — the question changed, the key
order did not.

## Two directions, and they are not symmetrical

- **Unmanaged** — the target system grants it, Atlas has no record. Somebody has access
  nobody here decided to give them. This is the direction people expect.
- **Missing** — Atlas records it, the target system does not. Atlas is asserting
  something untrue and will keep asserting it until somebody looks. **This is the one
  that corrupts the evidence**, because an inventory wrong in this direction answers "who
  had access when" with a confident falsehood.

A missing finding carries the entitlement's **origin**, because it decides how alarming it
is: an `ordered` right that vanished is a provisioning that came undone; a `legacy` one is
quite possibly a group somebody tidied up years ago.

## Transitions, not samples

A run over an unchanged disagreement writes nothing but a moved last-seen moment. A
disagreement that goes away **closes**. That is the shape `api/panorama/drift.go` argues
for, here made durable because it is evidence rather than a reading surface.

Closing is the half that is easy to get wrong: a run closes only findings it was entitled
to conclude anything about — same system, and an item the scope covered. A run that closed
every finding it did not happen to see would report a whole estate as repaired the first
time somebody reconciled one group.

A record keeps `Episodes` and how the previous one ended. A membership somebody keeps
re-adding is itself a finding, and a journal of one record per identity hides it perfectly
without that.

### Why a sidecar rather than engine state

The inventory is engine state because a *process* writes it: an order grants a right,
through the log, and it must survive the retention deletion of the instance that produced
it. Nothing in a process writes a discrepancy. It is produced by a comparison somebody
runs, it is never replayed, and `applyToState` has no business with it — so it belongs
with the other durable records that are not the engine's.

The cost, stated rather than discovered: a discrepancy is not in the event log and cannot
be reconstructed from it. What it records is a judgement about two states at one moment,
and neither of those states is the engine's to replay.

## Three actions, and why not four

Nothing is acted on automatically. Each is a separate call, about one finding, by a
person:

| Action | For | What it does |
|---|---|---|
| **adopt** | unmanaged | Writes an entitlement with origin `adopted` — the first writer that origin has had since ADR-0312 named it |
| **deprovision** | unmanaged | Runs the product's deprovisioning process. Never a direct worker call |
| **revoke** | missing | Removes the record Atlas could not substantiate |

`adopted` rather than `legacy`: both mean "Atlas did not grant this", and they differ in
who said so. Legacy is what a commissioning load found before anybody was watching;
adopted is a right that appeared afterwards and a person accepted. An audit that could not
tell them apart could not tell a pre-existing estate from privileges that grew under
Atlas's nose.

**The fourth — re-provisioning a missing right — is deliberately absent.** Granting
something is ordering it, ordering already exists, and it carries the approval rule the
catalogue declares. An action here that started a provisioning process would be a second
granting path with no approval in it: the thing this whole portal is built to not have.

**Deprovisioning uses the catalogue as it stands now**, and that is a weaker guarantee
than an order's return has. A returned order line revokes by the release it was ordered
against, frozen when it was placed, so a grant is undone by the rules in force when it was
made. A right nobody ordered has no such release. There is nothing else to use, and
refusing to deprovision anything unmanaged would leave the one case this exists for
unreachable.

### The credential split

The comparison joins `apiScopeInventory`: a scheduled process may run it unattended,
because it writes no entitlement and reaches no target system. **The three actions are in
no confined scope at all.** Each either changes what Atlas asserts about somebody's access
or takes access away, and neither belongs behind a credential a model carries.

## The population-sized read

"What does Alice hold" is a prefix scan. **"Who holds VPN access" is not**, because the
principal comes first in the entitlement key — and it has to, for the reason
`entitlementPrefix` gives. So reconciliation walks the whole family, once per run, off the
loop through a read view.

This is ADR-0312's open question arriving: *"whether reconciliation against the target
systems can be run over the whole estate without the comparison becoming a
population-sized job... nothing yet reads the inventory whole, and the one place that will
is the one nobody has built."* It is built, it is a population-sized job, and it is off the
loop where population-sized jobs go. A by-item index would remove the walk and cost a
second column family every write has to keep in step; that is the right trade the day
somebody measures this walk hurting, and the wrong one before.

### Consequences

- **Positive:** `model.OriginAdopted` has a writer. The inventory can be checked rather
  than trusted, in both directions. The journal answers "what is wrong now" and "how long
  has it been wrong", which nothing in the estate could answer before.
- **Negative / trade-offs accepted:** The soundness of every finding rests on a promise
  Atlas cannot verify. The comparison reads the whole inventory. A finding is a statement
  about one moment, and the inventory can move between the snapshot and the action taken
  on it — the action is idempotent enough to survive that, but the finding is not a lock.
  `ReconcileJournal` is a ceiling on a store rather than on a message, which is a new
  shape here and one an operator has to understand rather than infer.
- **Follow-ups / risks to watch:** The open question above. A subject-scoped run does not
  reduce the walk, so the leaver check makes the same question more pressing rather than
  less — an offboarding is run per person, which is a great many more runs than a nightly
  group sweep.

The screen this record originally named as a gap is built: **Operations →
Reconciliation**. It belongs there rather than under Catalogue because maintaining a
catalogue is authoring and acting on a finding is repair, which is what the routes
themselves say — all three are the operator's.

Two things about it are decisions rather than styling. The three actions are **not
guarded alike**: adopt and revoke are recoverable, because the next comparison finds the
truth again either way, so they are one click; deprovisioning runs a process that takes
access away in a real system and Atlas cannot undo it, so it asks and is styled as
destructive. A confirmation on every action is a confirmation nobody reads by the third
one. And its contextual help points at the examples chapter rather than at Operations:
the operations chapter is about incidents — a token that is stuck — and telling a reader
that a finding is a malfunction is the one thing this record takes pains to say it is
not.

## Implementation

`api/reconcile.go` compares and concludes; `api/reconcileapply.go` folds a run into the
journal and renders the report; `api/reconcilestore.go` is the durable journal;
`api/reconcile_http.go` the two routes and `api/reconcileactions.go` the three acts.
`state.queries.Entitlements` is the whole-family walk, added here and used only here.
`examples/abgleich.bpmn` is the nightly modelled process and
`examples/austrittspruefung.bpmn` the leaver check — the second one reads the whole tenant
and filters to one person, because no operation the Entra worker offers lists one person's
memberships. That is expensive and it is honest: the promise is "everything this person
holds", and a full enumeration keeps it.

## Links

- [ADR-0312](0312-portal-catalogue-order-inventory.md) — the three models, the origins,
  the reconciliation section this implements, and the open question it answers.
- [ADR-0333](0333-inventory-commissioning-load.md) — the inventory this compares against, and the
  opposite treatment of silence.
- [ADR-0239](0239-off-loop-queries.md) — why the whole-inventory walk runs off the loop.
- [ADR-0194](0194-api-tokens.md) — the scope the comparison joins and the actions do not.
