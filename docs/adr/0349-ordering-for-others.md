# ADR-0349: Whose name an order may carry is a question Atlas must ask, and a hierarchy it must not invent

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-15
- **Deciders:** Atlas maintainers
- **Open question:** Whether a line manager should be able to order for the people
  they manage without the operator role. The mockups treat that as the ordinary
  case and it plainly is one — but Atlas cannot answer what "manage" means, and
  this record refuses to guess. The answer needs a directory relationship the
  engine can read, which is the same gap the escalation path works around by
  having the caller name a superior.
- **Question checked:** 2026-09

## Context and problem statement

`HandlePlace` took the recipient straight out of the request body:

```go
recipient := req.Recipient
if recipient == "" {
    recipient = p.UserID
}
```

Nothing was asked about it. Any account that could reach a catalogue could place
an order in anybody's name — which puts an approval in that person's manager's
inbox, a line in their record, and, once approved, a provisioning run against a
target system.

It stayed harmless only because nothing exercised it: the portal never sent a
recipient, and no modelled process places an order at all — every `recipient` in
a shipped BPMN file *reads* the one the order already carries and passes it down
to provisioning.

**The mockups end that.** They make ordering for somebody else a first-class
screen ("Bestellen für: Person suchen"), and the portal now carries the field. A
latent hole with no caller becomes an open path with a button.

### Why the eligibility check does not cover it

They look adjacent and refuse different things. Product eligibility
(ADR-0347) asks whether *this person* may have *this
product*, and would wave through an order placed in a colleague's name for
something the colleague is perfectly entitled to.

What is wrong in that case is not the product. It is the name on the order.

## Decision

A caller may name themselves as recipient, as before. Naming anybody else needs
the **operator** role (or admin, which carries it).

### Why a role and not a manager relationship

Because Atlas cannot evaluate a manager relationship, and this is settled
elsewhere rather than open. The escalation path has the *caller* name the
superior, for a reason it states plainly: a directory lookup belongs to a
modelled process and not to the engine. The recertification record says the same
about who reviews whom.

An engine that gated on a hierarchy it had to invent would be deciding who may
act in whose name from a guess. The operator role is the strictest gate this
server can actually express, and it is the honest one until something can answer
the other question.

### Why the strictest available rule rather than the most convenient

Nothing is broken by it: no shipped model places an order, and the portal has
never sent a foreign recipient before today. So the choice is between closing the
door now and widening it deliberately later (M7 in the measures catalogue), or
leaving it open and narrowing it once somebody has come to rely on it. The first
costs a role grant today; the second costs a removal of something people use.

## Consequences

**The portal's "Bestellen für" works for operators and refuses everybody else.**
That is the intended state until a manager relationship exists, and the refusal
says which role is missing rather than only that the request failed — somebody
told "forbidden" about an order they believe is routine files a ticket.

**Single-user mode is unaffected.** With enforcement off there is nobody to be,
exactly as everywhere else in this server.

**It does not reach an order already placed.** Like every other gate here it
decides what may be *created*. An order somebody placed in a colleague's name
before this stands, and is visible where it always was.
