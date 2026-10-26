# ADR-0470: The line manager is a reference on the account

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-10-22
- **Deciders:** Atlas maintainers
- **Open question:** how Microsoft Graph's `/users/delta` delivers `manager` when it is
  selected. The documentation says a selected `manager` comes as a `manager@delta`
  annotation on the user; a community report says later delta pages carry it only when
  the manager itself changed; and `examples/rezertifizierung.bpmn` reads it as a plain
  `manager` object without having checked either. The mirror accepts both spellings and
  treats an absent one as "unchanged", which is right under every reading, but no run
  against a real tenant has confirmed which one Graph sends.
- **Question checked:** 2026-10

## Context and problem statement

A product whose approval rule is `superior` is approved by the orderer's line manager.
[ADR-0312](0312-portal-catalogue-order-inventory.md) put the lookup in the model: "who
somebody reports to is a question for a directory, and Atlas asks a directory through a
worker, from a model". The shipped model `atlas-genehmigung-vorgesetzter` therefore
searched the directory with `(userPrincipalName=<orderer>)` and assigned the task to
the `manager` attribute it read back.

That cannot work, and issue #1311 shows why. `orderer` is an Atlas principal id
(`usr_…`). No directory entry has one as its user principal name. And `manager` comes
back as a distinguished name in Active Directory, which is none of the spellings a task
can be held by: username, principal id, group id or group name (ADR-0042, ADR-0311).
The tests never saw it, because they answered the directory job with a fixed
`{"manager":"chefin"}` and never looked at the filter. Every `superior` approval
against a real directory stops at its first step.

Repairing it inside the model is not possible without breaking another rule. The model
would need a key the directory knows, and the keys Atlas holds for a person are the
username, the mail address and the directory object id. The first two are personal
data, and [ADR-0314](0314-portal-personal-data.md) forbids copying them into a
variable. The third exists only for accounts the Entra mirror maintains. And the way
back, from a distinguished name to an account, would put the manager's name into a
variable, because a DN carries it.

An approval model of an installation's own can do what the shipped one cannot, as long
as it is given something the directory knows. The Microsoft 365 example (#1312) reads,
from Entra ID, the manager of the person a position is for. It searches by the UPN its
order form asked for, which is sealed as personal data, and maps the manager to an
account by username. The shipped model is given principal ids only, and that case is
what this record is about.

The question: **where does Atlas learn who a person's line manager is, so that an
approval can be routed to them by principal id?**

## Decision drivers

- An approval must reach a person who can hold the task, or the order waits forever.
- Only references may enter a variable, an order or the log (ADR-0314).
- Atlas holds no directory credential. A directory is read by a worker, from a model,
  and the mirror already does exactly that (ADR-0332).
- Installations differ: some mirror Entra, some run Active Directory on their own
  premises, some keep their accounts in Atlas only, and demos use the mock directory.
  Each of them must be able to use the `superior` rule.
- The escalation chain already lives in `order.Escalate`, which takes a function that
  answers "whose superior is this?". It does not care where the answer comes from.

## Considered options

1. **Keep the lookup in the model**, and give it a directory key and a way back.
2. **A server-side directory lookup** at the moment the approval starts.
3. **A line-manager reference on the account**, filled by the mirror where one exists
   and by an administrator where it does not, read by the server when an approval
   starts and when it escalates.

## Decision outcome

Chosen option: **3, a reference on the account**.

Option 1 fails on ADR-0314 (see above), and on the installations without a mirror,
which would still have no key to search by. Option 2 is what ADR-0312 rejected, and
that objection still holds: it would be a directory configuration nobody set up and a
credential the server does not hold.

Option 3 does not run into that objection. The server does not read a directory. The
relation is reported to it in one of two ways:

- **by the mirror's own process** ([ADR-0332](0332-entra-directory-provisioning.md)), which
  already reads `/users/delta` through the Entra Worker and reports what changed;
- **by an administrator**, in the Console or through `PATCH /api/v1/users/{id}`.

What the server stores is a reference, one principal id pointing at another. That is
the shape ADR-0314 asks for.

### What the account holds

- `managerId` is the principal id of the person's line manager. It is the only field
  read at run time.
- `managerDirectoryId` is the directory object id of the manager, as the mirror last
  reported it. While it is set, the directory owns this account's line manager:
  - the mirror resolves it to `managerId` whenever the manager's account exists here;
  - an administrator may not edit `managerId` on such an account, which is refused
    with a 409 that says why.

Directory first, for the reason [ADR-0315](0315-portal-roles-and-responsibilities.md)
gives for the integration manager: a value somebody typed in Atlas must not quietly
keep authority that the directory took away months ago.

A manager the mirror names before it has read the manager's own account is held
on the record and resolved by a later run. The mirror already does this for group
memberships, and for the same reason: a delta read does not name an unchanged person a
second time.

### Where it is read

- **When an approval starts.** For a `superior` line, the server sets `approvalRef` to
  the orderer's line manager. The approval model then routes exactly as the `fixed` one
  does. When there is no usable manager, starting the approval is refused with a 409
  naming the orderer and the reason. The reasons are: no line manager on record; the
  directory names one with no account here yet; the manager's account is disabled. The
  fulfilment process reports the refusal as an incident, and resolving it starts the
  approval once the manager is set.
- **When an approval escalates.** `POST …/escalate` with `{"lineManager": true}` moves
  the approval to the current approver's line manager. The current approver may be
  spelled as a principal id or, after a reassignment, as a username. The `superior`
  field keeps its meaning, and a model that names the superior itself works as before.
  Asking for both is refused. No usable manager is the answer "nobody", and it stalls,
  as an empty `superior` always has.

The shipped model `atlas-genehmigung-vorgesetzter` no longer reads a directory. It no
longer needs an `ad` Worker called `verzeichnis` either. It still writes `vorgesetzter`,
because the event catalogue promises that variable to listeners of
`atlas.approval.requested`.

Whose manager approves is unchanged: the **orderer's**. When an operator orders for
somebody else (ADR-0349), that is the operator's manager. Whether it should be the
recipient's manager is a separate question, and this record does not answer it.

### Consequences

- **Positive:**
  - A `superior` approval reaches a person on every kind of installation.
  - No personal data enters a variable, and only principal ids are routed on.
  - The escalation chain moves one level up from the approver who holds the task now.
    The old model always asked about the first manager, so its second hop asked the
    same question again and stalled on the loop guard.
- **Negative / trade-offs accepted:**
  - Atlas now holds a relation that was settled as "not ours" in ADR-0312 and ADR-0349.
    The comments that cite that argument are rewritten to say what changed.
  - On an installation without a mirror, somebody has to maintain the relation in
    Atlas. A model that reads Active Directory can do it through the same `PATCH`, but
    none ships with Atlas.
  - Approval instances already running on the old model keep the old model and its
    directory steps; only approvals started after the upgrade use the new one.
- **Follow-ups / risks to watch:**
  - Confirm the `manager@delta` shape against a real tenant (the open question above).
  - Whether the recipient's manager, rather than the orderer's, should approve an order
    placed for somebody else.
  - Whether ADR-0349's gate could use this relation to let a line manager order for
    their own people. That gate is unchanged here.
  - An automatic feed from on-premises Active Directory.

## Pros and cons of the options

### Keep the lookup in the model
- Good: no new state on the account; the server stays out of who reports to whom.
- Bad: needs a personal key in a variable, or a mirror the installation may not have;
  the way back from a DN leaks a name; and the shipped model has been broken this way
  since it was written.

### A server-side directory lookup
- Good: always current.
- Bad: a directory configuration and a credential inside the server, which ADR-0312 and
  ADR-0332 both refuse.

### A reference on the account
- Good: references only; one place to read; works with a mirror, without one, and with
  the mock; and `order.Escalate` needs no change.
- Bad: the relation is only as fresh as the last mirror run or the last edit, and an
  installation without a mirror has to maintain it.

## Links

- amends [ADR-0312](0312-portal-catalogue-order-inventory.md): the superior is read from the account, not looked up by the model
- amends [ADR-0349](0349-ordering-for-others.md): its premise that Atlas cannot evaluate a manager relationship no longer holds; its decision is unchanged
- builds on [ADR-0332](0332-entra-directory-provisioning.md): the mirror reports the relation
- follows [ADR-0314](0314-portal-personal-data.md): a reference, not the content
- follows [ADR-0315](0315-portal-roles-and-responsibilities.md): the directory takes precedence over a value typed in Atlas
- fixes issue #1311
