# ADR-DRAFT: A process places an order in the name of its application

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-11-03
- **Deciders:** Atlas maintainers

## Context and problem statement

A person who joins the organisation needs an account, a licence with a mailbox, the
memberships of their organisational unit and, often, a phone and a device — the
"starter package". Every one of these is a product in the catalogue, with its own
provisioning, its own return and its own place in the person's inventory. What is
missing is who orders them.

In the shop a person orders for themselves, or an operator for somebody else
(ADR-0349). For a joiner neither fits: the person does not exist yet when HR knows
they will start, and nobody should have to remember to order the package by hand —
HR's record is the authority. [ADR-0349](0349-ordering-for-others.md) recorded the
state of things plainly: *no modelled process places an order at all.* There was no
way for one to: an API token carries no groups, so it reaches no catalogue, and the
shop's order route asks for a caller in a catalogue's audience.

[ADR-0429](0429-product-actions-are-commands-with-published-outcomes.md) §10 had already answered the sibling
question for an existing right. A shop send task in mode `command` lets a process —
an HR leaver process — ask a held position for an action, in the name of the
process's application, when the product lists that application in `commandedBy`. The
leaver half of a joiner/leaver lifecycle was therefore modellable; the joiner half was
not.

The question this record answers: **how may a process order a product for somebody,
without a credential in the model, without a back door around the checks a person's
order meets, and so that taking the permission away stops it at once?**

## Decision drivers

- **No credential in a model** (ADR-0041/0067). The permission is the product's
  statement about an application, not a token a process presents.
- **The same checks as a person's order.** Eligibility (ADR-0347), conflicts
  (ADR-0342), the orderable window (ADR-0397), answers and variants (ADR-0358), and the
  product's approval rule. A process is not a way around any of them.
- **Withdrawal is immediate**, as for `commandedBy`: the list is read from the newest
  release when the task orders.
- **A retry orders once.** A job is delivered at least once; an order placed twice is
  two provisionings in a target system.
- **The order says who ordered it.** An application, not a person and not a token.

## Considered options

1. **A REST call to `POST /api/v1/orders` with an API token** that a catalogue grants
   ordering to (a new member type "token").
2. **A new shop send task mode `order`**, admitted by a product field `orderableBy`
   naming applications, the sibling of `commandedBy`.
3. **An "order" DMN or system process in the protected system project** (ADR-0122)
   that any application's process asks by message.

## Decision outcome

Chosen: **option 2.**

### Why not the others, at their strongest

**Option 1** needs the least engine code: the route exists, the REST worker exists,
and a catalogue member list exists. Its cost is that the permission becomes a
long-lived secret in the vault that any process holding it can use for any product the
catalogue offers, and the order is then the token's act — a machine account, not the
automation the operator decided to trust. Revoking it means rotating a secret rather
than unticking an application.

**Option 3** keeps every order inside one trusted model. It moves the question rather
than answering it: the system process would still need to know which application may
order what, which is exactly the list this record puts on the product.

### What a model says

```xml
<sendTask id="st_paket" name="Eintrittspaket bestellen">
  <extensionElements>
    <atlas:shopTask mode="order" catalog="cat-m365-hr" product="m365-eintritt"
                    recipient="=konto.id" answers="=antworten" resultVariable="auftrag"/>
  </extensionElements>
</sendTask>
```

The catalogue and the product are literals, checked at deploy (I5): a task names
exactly what it may order. The recipient — the account id of the person — and the
answers to the products' forms, `{product: {field: value}}`, are literal-or-FEEL
values evaluated when the task runs; like the command task's order and position they
are evaluated at the server's edge, where ADR-0314 lets a declared personal variable be
read in plaintext. The task states no action and no outcome: the order's own
fulfilment says how it ended. The order's id lands in `resultVariable`.

### What the server does

The task rides the command task's job type (`io.atlas.shop.command`), served in-process
off the run loop. For an `order` task the handler

1. resolves the process's application by its portable key (ADR-0134); a process in no
   application orders nothing;
2. reads the newest release of the named catalogue and refuses unless it offers the
   product and the product's `orderableBy` lists the application;
3. places the order through **the same code as the shop's** (`order.place`): what the
   recipient holds and which groups they are in, eligibility, conflicts, the window,
   answers, variants, the durable write, and only then the fulfilment's start (I2);
4. names the order's orderer `system:application:<key>`, a principal nobody signs in
   as;
5. derives the order id from the job key, which survives retries: a retry finds the
   order its first attempt placed and returns it without starting the fulfilment again
   (an order stored but not started is what the fulfilment repair is for).

A refusal that a retry will not change — the product does not list the application,
the recipient has no account, the basket breaks a rule — is thrown as the BPMN error
`SHOP_ORDER_REFUSED` when the process catches that code, so the model can route it to
a person; uncaught it is a failure with retries and then an incident, as for every
other task (ADR-0461's rule). A server error is always a failure.

### What the catalogue says

`orderableBy` is a product field beside `commandedBy`, with the same rules at publish
(no blank entry, no repeat, a key nobody here carries is not refused because a
catalogue moves between servers), frozen into a release, and edited in the Console with
the same picker. Empty — the default — means no process may order the product.

A product a process orders is normally offered in a catalogue that reaches nobody (a
catalogue without groups reaches nobody, fail-closed): the package is the HR process's
to order, not a shelf in anybody's shop. Its approval rule is the catalogue's choice;
for a package HR's record authorises, `none` is the honest one, and anything optional
on top is ordered by a person in the shop under its own rule.

### Consequences

- **Positive:** a joiner/leaver lifecycle is modellable end to end — the joiner
  process orders the package in its application's name, the leaver process returns it
  with a command task, and both are lists on the product an operator can read and
  change.
- **Negative / trade-offs accepted:** the orderer of such an order is not a person, so
  nobody receives the orderer's notices and nobody may cancel it as its orderer; an
  operator acts on it. The recipient must have an Atlas account before the order is
  placed — for a joiner, the directory synchronisation (ADR-0332) has to have run.
- **Follow-ups:** variants in the task (an order of a product with variants is refused
  today with the order's own "names no variant" reason); several products in one task.

## Links

- the sibling of ADR-0429 §10 (`commandedBy`, mode `command`)
- amends the observation of ADR-0349 that no modelled process places an order
- relies on ADR-0332 for the joiner's account and on ADR-0134 for application keys
