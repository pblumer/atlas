# ADR-0441: A position's answers reach its processes

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-10-02
- **Deciders:** Atlas maintainers

## Context and problem statement

A product can ask the orderer something when they order it: a configuration form
([ADR-0358](0358-order-line-configuration.md)), such as the licence plate a parking space is
for, or the cost centre a laptop is booked to. The answers are stored on the order line.
ADR-0358 named them as evidence that an approver reads and a provisioning process acts on.
In practice, three places did not hold that:

- **The approver did not see them.** The approval in the Console's inbox lists the product,
  the variant, the price, the people and the catalogue, but not the answers. The approval form
  `genehmigung` does not show them either. An approver deciding on a parking space could not
  see which car it was for.
- **The processes did not receive them.** The order starts a position's process with
  `orderId`, `itemId`, `positionId`, `recipient`, `orderer`, `variantId` and a few more, and
  without the answers. ADR-0358 recorded this as its follow-up: "if a model ever needs them at
  the moment a line starts, that is the next slice". A model that needed the plate had to call
  back into the order, which only the orderer and the recipient may read.
- **The one process that did get them had no need for them.** The fulfilment orchestration
  `atlas-auftrag-erfuellung` reads `GET /api/v1/orders/{id}/next`, which returned every ready
  line whole, answers included. It keeps that answer as its own variables (`bereit`,
  `position`), so the answers were written to its history in the clear: the event log, the
  checkpoints, an export and every backup. It starts processes and never reads a form.

Answers are often personal: a plate, a phone number, a private address.
[ADR-0314](0314-portal-personal-data.md) makes personal data erasable by sealing every value a
model declares personal under its data subject's own key. That only protects a value that
enters an instance through an edge that seals it.

## Decision drivers

- **The approver decides with the answers in view**, labelled as the form labels them.
- **The processes the product binds act on the answers** without calling back into the order.
- **Personal answers are erasable.** Where a model declares an answer personal, it is sealed
  like any other declared value.
- **Nothing gains answers it does not need.** A model that declares nothing does not hold
  someone's plate in the clear for years because the order handed it over.
- **The orderer cannot set what the order decides.** Answers are the orderer's input. An
  answer must never stand in for the recipient, the approver or any other variable the order
  sets.

## Considered options

1. **One variable per field, to the processes the product binds.** The approver reads the
   answers from the order.
2. **One variable `config`** holding every answer as an object.
3. **Display only.** The approver sees the answers, and processes keep reading the order
   themselves.

## Decision outcome

Chosen: **option 1.**

**Which processes receive the answers.** The order passes them when it **starts** a process
that the product binds:

- the provisioning process, or the lifecycle process at its provision message;
- a return or deprovisioning process;
- an action's process at its own start event;
- an approval model of the installation's own, bound by name.

Each answer is a variable of type string, named after its field's key. The answers are passed
as the order holds them at that moment, so an answer amended under
[ADR-0359](0359-amending-an-order-line.md) arrives amended.

**What does not receive them:**

- **A delivery to a running strand.** The per-position lifecycle instance has held the answers
  since it started. A delivery could not seal them either: the message path has no sealing
  edge, and the engine would park a declared value arriving there as an incident
  (ADR-0314).
- **Atlas's own approval models** (`atlas-genehmigung-fix`, `-rolle`, `-vorgesetzter`). They
  declare nothing personal, so the answers would sit in their history in the clear. Their
  approver reads the answers on the approval itself.
- **The fulfilment orchestration.** `GET /orders/{id}/next` no longer includes a line's
  answers or their amendments. Already written history keeps what it holds.

**Only what the form asks, never what the order sets.** An answer is passed only if:

- its key is a field of the product's form as the server holds it now, which shuts out keys
  the orderer added to the request;
- its key is none of `catalog.OrderVariables`: `orderId`, `itemId`, `positionId`, `variantId`,
  `recipient`, `orderer`, `provisionProcess`, `approvalRef`, `atlasApiBase`, `portalBaseUrl`,
  `commandId`, `reason`;
- its key is not already among the start's variables. An action's own input wins over an
  answer of the same name, because it is newer and was asked for this act.

A form the server no longer holds passes no answers, because there is no telling which keys it
asked.

**Publishing refuses a field named like the order's own variables**
(`catalog.OrderFormProblems`, in the single publish and in the document import). Such a field's
answer would never reach a process, and the product manager would look for it in vain. A form
the server does not hold is not refused here; publishing never asked whether one exists.

**Personal answers are declared by the model.** A process declares an answer personal as it
declares any variable:

```xml
<process id="proc_vd_parkplatz_zuteilen" atlas:personal="kennzeichen" atlas:dataSubject="recipient">
```

`recipient` is always among the start variables, so the start act seals `kennzeichen` under the
recipient's key. It is erased with that person (`DELETE /api/v1/personal-data/{subject}`). The
rules of ADR-0314 apply unchanged: conditions and mappings evaluated by the engine may not read
a declared value, while a task form and a connector see it.

**The approver reads the answers on the approval.** `GET /api/v1/approvals` carries:

- `answers` as `{key, label, value}`, in the form's order and with its labels, followed by any
  answer the form no longer asks for, under its key. The order is the record of what was
  answered.
- `amended` when the answers were corrected after the order was placed.

The Console's inbox shows them in the approval block. They are read from the order under the
approval's own gate, not from the process.

### Why not the others

**Option 2** puts everything in one place, so a model reads `config.kennzeichen` and nothing
can collide with the order's own names. But the personal declaration of ADR-0314 is per
variable:

- Declaring `config` seals every answer, including a cost centre that is not personal. A
  gateway branching on `config.kostenstelle` would then be refused, because the engine may not
  read a sealed value.
- Not declaring it leaves the plate in the clear.

A task holder's view of an instance is also per variable: they see the fields of their task's
form. A form can show `kennzeichen` but cannot pick one answer out of `config`, so the holder
would see all answers or none.

**Option 3** keeps every answer out of the event log, which is its real strength. But it leaves
the process that has to act on the answer without it. The process would need a REST call back
into an order that only the orderer and the recipient may read, which is exactly the
indirection ADR-0358 left as its follow-up.

### Consequences

- **Positive:**
  - An approver sees what they approve. A provisioning process acts on the plate without a
    round trip.
  - A personal answer is erasable where the model says it is personal.
  - The orchestration no longer copies answers into its history.
- **Negative / trade-offs accepted:**
  - A model that does not declare an answer personal keeps it in the clear in its history,
    which is ADR-0314's rule for every undeclared variable. The shop handbook says how to
    declare it.
  - Forms have no versions (ADR-0358). A field renamed after an order was placed is not
    passed, because the form no longer asks it. The approver still sees it, under its key.
  - The answers are strings, as the order stores them.
- **Follow-ups:**
  - **A per-position lifecycle strand started before this change** has no answers. Only its
    next start, after a return, carries them.

## Links

- [ADR-0358](0358-order-line-configuration.md): the configuration form and its answers on the
  line.
- [ADR-0359](0359-amending-an-order-line.md): amending the answers.
- [ADR-0314](0314-portal-personal-data.md): personal data, sealed under its subject's key.
- [ADR-0416](0416-the-shop-shows-an-orders-open-tasks.md): a task holder's view of an order,
  without its answers.
