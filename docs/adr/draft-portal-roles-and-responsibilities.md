# ADR-DRAFT: Who runs the portal — a role for the operation, a catalogue for the object

- **Status:** Accepted
- **Implementation:** Not started
- **Date:** 2026-09-11
- **Deciders:** Atlas maintainers
- **Open question:** Whether aggregate figures are enough for a product manager to
  diagnose a failing product. This record gives the role counts, durations and failure
  rates but no individual order, so every provisioning failure needs an operator. No
  portal has run long enough for anybody to say whether that holds or becomes the
  first boundary somebody asks to widen.
- **Question checked:** 2026-09

## Context and problem statement

The portal needs administering: somebody defines products, assembles catalogues,
decides what needs approval and publishes a release. That person is not an
administrator of the instance, and must not have to be.

Atlas authorizes on two axes and they answer different questions.
[ADR-0209](0209-roles-per-endpoint-group.md) gives every route the role it requires —
`admin`, `modeler`, `operator`, `user` — read once at the boundary.
[ADR-0071](0071-sharing-scopes.md) gives an object an owner, a visibility and a member
list, with [ADR-0180](0180-groups-as-members.md) allowing a group to be a member and
[ADR-0278](0278-object-authorization.md) insisting the check happens wherever the
action does. ADR-0209 states the relationship plainly: the sharing scopes "say *which
object* a person may touch, never *which kind of operation* they may perform at all".

A catalogue needs both answered, and the request that prompted this record named the
first in the vocabulary of the second — a *group* called product manager. In Atlas a
group carries no authority at all; it is a named set of users that a scope can grant a
role to. So the request decomposes: the authority is a role, the "which catalogue" is
a scope, and the two are not interchangeable.

Three further roles are in play and are easily confused with each other, which is why
this record names all of them in one place:

| Who | What they are | Which axis |
|---|---|---|
| Product manager | maintains products and catalogues | a role, plus catalogue scope |
| Integration manager | orders for *the users assigned to them* | a relation, not a role |
| Approver | the superior, or a fixed person, per product | derived from product and orderer |

## Decision drivers

- One place to read what a credential can reach (ADR-0209's declaration rule).
- Nobody administering a catalogue should need instance administration.
- Ten catalogues for different customer groups means one product manager must not be
  able to edit another's catalogue.
- Publishing a catalogue is not deploying code, and must not require a role that can.
- A new role must not arrive switched on for accounts that predate it.
- A product can appear in several catalogues, so "who may change it" cannot be
  answered by "whoever opens the catalogue it is in".

## Considered options

1. **Scopes only** — no new role; whoever is editor of a catalogue maintains it.
2. **Admin only** — catalogue administration is instance administration.
3. **A fifth role plus catalogue scopes.**

## Decision outcome

Chosen option: **a fifth role, `productmanager`, plus the catalogue as a scope-bearing
object.**

Option 2 makes every catalogue edit an administrative act and puts ten customer
catalogues through one queue. Option 1 is the serious contender and fails on one
specific thing: a catalogue that does not exist yet has no scope to carry a
permission, so nothing can say who may *create* one. Creation needs the role; every
act after it needs the scope. That is the division ADR-0278 already drew for projects
and deployments, and this follows it rather than inventing a shape.

### The role

`productmanager`, spelled like the other four: one lowercase word naming the person,
as `modeler` and `operator` do. It reaches catalogue and product routes and nothing
else. `admin` reaches them too, as it reaches everything — the one superset, for the
reason ADR-0209 gives.

**It is never granted by an upgrade.** `legacyRoles()` is `{modeler, operator, user}`,
the set that describes what a pre-role-model account could already do, and
`productmanager` must not be added to it. Adding a new authority to that list would
hand catalogue control to every existing account on the day an operator installs the
update — a widening nobody asked for, arriving silently, which is the exact failure
ADR-0209's upgrade rule exists to avoid. Narrowing is an operator's deliberate act;
granting a new role is too.

### The object: the catalogue is the scope, a product has a home

A `Catalog` carries the ADR-0071 shape — `ownerId`, `visibility`, `members` of
`{ref, role}` where a ref may be a user or a group and the role is viewer or editor.
Nothing new is invented; it is the same three fields on a different object.

A `CatalogItem` is referenced by catalogues, not owned by them — it appears in several
([ADR-draft-portal-catalogue-order-inventory](draft-portal-catalogue-order-inventory.md)),
so scope cannot be inherited from "the catalogue it is in". Two ways out were
available: give every item its own member list, which is the per-artifact ACL ADR-0071
deliberately refused because it becomes unmanageable; or give each item **one home
catalogue** whose scope governs editing it. This record takes the second.

An item is edited only through its home catalogue. Another catalogue may include it,
which requires viewer on the home catalogue and grants nothing further. So a change to
a shared product happens in one place, by one responsible party, and a catalogue
cannot alter the behaviour of a product another catalogue depends on. What it *can*
do is decline to carry the new version: each catalogue's release freezes the item
versions it contains, so an including catalogue moves when it publishes, not when
somebody else edits.

### A product manager is not a modeller

Binding a product to its provisioning and deprovisioning processes means choosing from
processes already deployed — never deploying one. `modeler` includes deploy, and
deploy is code execution (ADR-0278, risk R-09 in `docs/compliance/isds-konzept.md`).
Granting it to catalogue maintenance would make the catalogue a path to running code,
which is precisely the escalation this record must not open.

So the binding reads through a **narrow route of its own**, returning process id, name
and version and nothing else, rather than opening the existing process listing to a
fifth role. Least authority, and it also keeps ADR-0209's admin allowlist test
(`TestAdminRoutesAreExactlyTheAllowlist`) answering a question about one line.

### Publishing: the product manager releases, and this is a known risk

A product manager publishes their own catalogue releases. No second party.

This record states the consequence rather than burying it, because a record that hides
a weakness is worth nothing to whoever reads it next. The catalogue is the **only**
place that decides what may be provisioned without approval; everything downstream
executes what it says. A product manager can therefore define a product that
provisions a privileged group membership, set its approval rule to none, place it in a
catalogue they can order from, and order it — and every component behaves correctly
while it happens, because from the engine's point of view nothing is wrong.

Separating authoring from release would close this for almost nothing: applications
already have the shape ([ADR-0128](0128-process-applications.md)). It was considered
and deliberately not taken, to keep catalogue maintenance free of a second person.

Two compensations follow from that choice, and they are part of the decision, not
suggestions:

- **Every change to an approval rule is audited** — who, when, from what to what —
  through the existing audit trail rather than a portal-specific log.
- **A catalogue always answers "which products require no approval"**, as a standing
  list, not a report somebody has to think to run. The escalation stays possible; it
  stops being invisible.

Should an operator later want the second party, the mechanism is the release, and it
is a scope role, not a redesign.

### What a product manager may see

Aggregate figures for their products: counts, durations, abandonment and failure
rates, plus failures with technical detail. Not individual orders.

This costs less than it looks, because
[ADR-draft-portal-personal-data](draft-portal-personal-data.md) keeps personal data out
of orders entirely — an order names a principal id. The boundary is therefore about
who may be *correlated with what they ordered*, which is the disclosure worth guarding,
rather than about names in a payload.

The price is stated in the open question above: diagnosis of a single failing order
goes through `operator`.

### The integration manager is a relation

Ordering on behalf of another principal is one operation, so it is one route, reachable
by `user`. Which principals a given orderer may act for is the object axis, checked
where the action happens (ADR-0278). An orderer with no assignment may order only for
themselves — the empty relation is the safe default, not an unrestricted one.

The assignment is resolved from the directory where it exists and from Atlas groups
where it does not, with the directory taking precedence. Two sources need a third rule:
where they disagree, the portal shows the conflict. Silently preferring one is how a
stale group quietly keeps authority somebody removed in the directory months ago.

Where an order's approver resolves to the orderer, the approval step does not run and
the record says **"ordered by the approver"** rather than showing an approval that
never happened. The check belongs in the approval process as a gateway, so it is
visible in the diagram, the replay and the trail — not in the portal, where it would
be a condition nobody can see.

### What this record does not decide

The theme of a catalogue stays with `admin`, as the instance brand does today
([ADR-0113](0113-org-wide-ui-theme.md)); making it per-catalogue is a separate record.
Self-registration is approved by a process, not by this role
([ADR-draft-portal-catalogue-order-inventory](draft-portal-catalogue-order-inventory.md)
places it), and which role that process assigns work to is that process's business.

### Consequences

- **Positive:** Catalogue administration needs neither `admin` nor `modeler`. Ten
  catalogues have ten independent responsibilities with one mechanism. A shared product
  has exactly one party who may change it. The new role reaches nobody until somebody
  grants it.
- **Negative / trade-offs accepted:** The catalogue holds every approval requirement and
  one role can change them unreviewed — accepted, audited and surfaced as above. A fifth
  role and a new scope-bearing object are both things a reviewer must learn. A product
  manager cannot diagnose a failed order alone.
- **Follow-ups / risks to watch:** Whether the aggregate boundary holds (open question).
  Whether "one home catalogue" survives a reorganisation in which a shared product
  should change hands — transferring a home is an operation this record has not
  specified. Session role revocation ([ADR-0281](0281-session-role-revocation.md))
  applies to the new role as to the others and needs no separate treatment, but it does
  need a test saying so.

## Pros and cons of the options

### Scopes only
- Good: no fifth role, one mechanism, nothing new for a reviewer.
- Bad: cannot express who may create the first catalogue. Conflates "may do this kind of
  thing" with "may touch this object", which ADR-0209 separated on evidence.

### Admin only
- Good: nothing to build; the authority already exists.
- Bad: ten customer catalogues through one administrative queue, and catalogue
  maintenance carries backup, restore, credentials and accounts with it.

### A fifth role plus catalogue scopes
- Good: creation is answerable, per-catalogue responsibility is answerable, and both use
  shapes already in the tree.
- Bad: the most moving parts. Two axes to hold in mind when reviewing any portal handler.

## Links

- extends [ADR-0209](0209-roles-per-endpoint-group.md) — a fifth role, for new routes only, never granted by upgrade
- extends [ADR-0071](0071-sharing-scopes.md) and [ADR-0180](0180-groups-as-members.md) — the catalogue carries the scope shape; groups may be members
- follows [ADR-0278](0278-object-authorization.md) — the object check happens where the action does
- deliberately does not grant [ADR-0199](0199-route-access-classes.md) public reach to any portal route
- relies on [ADR-draft-portal-personal-data](draft-portal-personal-data.md) — orders hold ids, which is what makes an aggregate boundary meaningful
- governs the catalogue defined in [ADR-draft-portal-catalogue-order-inventory](draft-portal-catalogue-order-inventory.md)
- theme ownership stays with [ADR-0113](0113-org-wide-ui-theme.md) until a separate record moves it
