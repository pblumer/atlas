# ADR-DRAFT: The orderable window is enforced when an order is placed

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-18
- **Deciders:** catalogue/portal

## Context and problem statement

A catalogue item has carried an orderable window since the catalogue was designed:
[ADR-0312](0312-portal-catalogue-order-inventory.md) lists `orderableFrom` /
`orderableUntil` among what each item holds, and `catalog.Item.Lifecycle` has
stored it ever since, in the server's own Unix nanoseconds with zero meaning
unbounded on that side.

Nothing ever read it. Publishing checked that the window did not end before it
began — a sanity check on the pair, not on the present — and that was the only
reference to the field in the tree. No reader anywhere asked whether *today* was
inside it, so a product with a window was orderable exactly like a product without
one. The Console did not offer a control for it either, which is how it stayed
unnoticed: nobody could fill it in from the screen, so nobody found out it did
nothing.

That is worse than an absent feature. The field is in the HTTP API, in the MCP tool
schema and in ADR-0312's description of what an item is, so an integrator can set
it, watch it persist, watch it travel into a release — and get no behaviour at all.
A promise kept by nobody is the shape of defect that is discovered by the person
who relied on it.

## Decision drivers

- A field that is stored and documented either does something or is removed.
- The catalogue's surface is an HTTP API that agents drive; the portal is one
  client of it.
- The window's stated purpose is publishing a catalogue **ahead** of the date it
  opens, so the product has to be visible before it is orderable.
- Every other rule about a basket — the approval, the ceiling, eligibility, the
  price — is read from the release at placement. A sixth rule read somewhere else
  would be a sixth place to look.

## Considered options

1. **Enforce at order placement, against the release.**
2. Enforce in the portal only — hide or disable what is outside its window.
3. Enforce at publish — refuse to publish a catalogue whose windows have passed.
4. Remove the field.

## Decision

**Option 1.** `closedIn` refuses a placement carrying an item outside its window,
beside the conflict and eligibility gates, inside the same run-loop turn, read from
the same release. The refusal is **403**, names the item and the date, and says
which side of the window the moment fell on.

Three things follow from where it sits, and each was the alternative to a defect:

- **The portal keeps the window too, and is explicitly the courtesy half.** A
  basket that cannot be submitted is a control that fails; filling one to be
  refused at the end teaches somebody the page is broken. The row is shown with a
  disabled control and the date, not hidden — hiding it removes the one thing
  somebody wants to know, which is when it opens, and that is the case the field
  exists for.
- **One reading of the clock per placement.** The instant checked against the
  window is the instant the order records as its creation. Read twice, an order
  placed across a boundary could be refused for a window that had already opened at
  the moment the order says it was created: a contradiction inside one record, of
  the kind that surfaces once a year and cannot be reproduced.
- **Both ends are inclusive, and the Console stores the end of the last day.**
  Somebody who writes 31.10. means the product is orderable on the 31st. Storing
  midnight would close it when the 30th ended — a day early, every time, with
  nothing about it looking like a defect.

An integral part outside its window closes the product carrying it, and the refusal
says so. That is the same distinction eligibility draws and it is there for the
same reason: a part is never deselectable, so refusing one by name leaves somebody
looking for a checkbox that does not exist.

## Consequences

- **Positive:** the field does what the record, the API and ADR-0312 say it does; a
  seasonal or launch-dated product can be published in advance; the refusal names a
  date, so nobody has to ask an administrator what "not yet" means.
- **Negative / trade-offs accepted:** a window is now load-bearing, so a wrong date
  stops orders — which is the point, and why the Console states that both ends are
  inclusive and that the dates are the server's own (UTC). A window that matters to
  the hour is not expressible; days are what the field is authored in.
- **Not covered:** the window governs **ordering** and nothing else. A right
  already held when the window closes keeps running; expiry is `maxDays`
  ([ADR-0344](0344-time-bounded-entitlements.md)), which is a different question.
- **Risks to watch:** an installation that filled the field in while it did nothing
  now has products that refuse orders. That is the correction, not a regression,
  but it arrives without warning — which is why the release note says so plainly.

## Pros and cons of the options

### Option 1 — enforce at placement, against the release

- Good: one gate, where the other five rules about a basket already are; every
  caller passes it, not only the screen; the refusal is readable and dated.
- Bad: a rule that was inert becomes load-bearing on upgrade.

### Option 2 — the portal only

- Good: no behaviour change for anything but the screen.
- Bad: a rule enforced where it is displayed is a rule every other caller walks
  past — and the catalogue is an API first. It would leave the field still doing
  nothing for agents, which is most of what this record is about.

### Option 3 — refuse at publish

- Good: the problem surfaces for whoever publishes.
- Bad: it answers a different question. A window is about *when an order is placed*,
  and a publish happens once; a catalogue published inside the window would stay
  orderable forever after it closed.

### Option 4 — remove the field

- Good: honest, and smaller than enforcing it.
- Bad: it is in ADR-0312, the HTTP API and the MCP schema, and the need is real —
  a seasonal offer and a product with a launch date are ordinary. Removing it would
  answer the defect by deleting the requirement.
