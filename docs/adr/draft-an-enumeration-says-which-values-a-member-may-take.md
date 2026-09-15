# ADR-DRAFT: An «enumeration» says which values a member may take

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-15
- **Deciders:** Patrick Blumer

## Context and problem statement

Three questions an author answers while drawing a write arrow have the same shape —
*which of the things the model already declares do I mean?* — and until now only two of
them were asked that way:

| Question | Answered from | Since |
|---|---|---|
| Which class is this data object? | the information model | [ADR-0230](0230-process-information-model.md) |
| Which state does this write move it into? | the class's lifecycle | [ADR-0259](0259-data-object-lifecycle.md) |
| Which member does this write target? | the class's attributes | [ADR-0060](0060-field-level-data-object-writes.md) + issue #905 |
| **Which value does it write into that member?** | **nothing** | — |

The fourth is not always answerable: a value is a FEEL expression, and what
`=amount * 1.19` produces is not a fact of the model. But sometimes it is answerable
exactly. Where a member's type is an «enumeration», the model has already written down
the complete list of values that member may hold, one literal per line, in a box on the
same diagram. `= "aktive"` type-checks, deploys, runs, and writes a value that no
decision, no filter and no report will ever match — the same failure `customer.nmae` had
before the member picker, one level down.

The «enumeration» is also, on the canvas, an orphan. [ADR-0306](0306-a-lifecycle-may-take-its-states-from-an-enumeration.md)
drew a `«lifecycle»` edge from a class to the enumeration its *states* come from, and
that is the only line an enumeration ever gets. An enumeration that types an attribute —
which is what the ADR-0306 record left as its open question, and what a real model does —
sits unconnected next to the class that uses it, while the class compartment says
`status : Lebenszustand` and nothing joins the two. A reader cannot see the dependency,
and moving the enumeration away from the class costs nothing because nothing holds them
together.

## Decision drivers

- **The same question, asked the same way.** A closed set of values the model already
  declares should be offered, not remembered — exactly as a class, a state and a member
  already are.
- **A value that can never match is worth a word at deploy.** It is the `RuleDataUnknownState`
  case one level down: the string is what matching is done by, and nothing else.
- **Only where it is actually closed.** A computed value stays free text, and saying
  anything about it would be the false knowledge the derived model reports as a gap.
- **A class diagram shows dependencies.** An enumeration a class depends on is joined to
  it, the way the store and the lifecycle already are.
- **One line per pair.** A derived edge is routed straight, dock to dock, with no lane
  spreading — so a second derived edge between the same two boxes would be drawn exactly
  on top of the first.

## Considered options

For the **value**:

1. **Offer the literals in the write row, and warn at deploy about a constant that is not
   one.** The picker where the value is closed; free text everywhere else.
2. **Warn at deploy only.** No authoring help; the author still types the string and
   finds out afterwards.
3. **Refuse the deploy.** Treats a model that is merely behind its process as broken.
4. **Type-check every expression against the enumeration.** Needs a FEEL type system
   Atlas does not have, and would be wrong about every value computed at run time.

For the **edge**:

5. **One derived edge per (class, enumeration) pair**, labelled with the attributes that
   type by it.
6. **One edge per typed attribute.** Three attributes of the same enumeration means three
   lines on exactly the same pixels.
7. **Nothing; the compartment already says the type.** Which is what it does today, and
   is why the box floats.

## Decision outcome

Chosen: **option 1 for the value, option 5 for the edge.**

**The value.** The write row's FEEL field becomes a picker of the enumeration's literals
when the member it targets is typed by one, with an escape to a free expression that is
always there — because a value that is computed is a real thing to want, and a picker
that cannot be left is a picker that lies about what FEEL is. The stored form is an
ordinary FEEL string literal (`="aktiv"`), so nothing about the model, the compiler or
the engine changes: only the authoring.

At deploy, a write whose value is **constant** — an expression with no inputs, which is
the precise and complete test for "this is decided now rather than at run time" — is
evaluated and checked against the literals. The inputs are what decides, not what the
evaluation happens to return, and a conditional is why: FEEL is null-propagating, so most
expressions evaluate to null without their variables and would fall out of the check
anyway, but `if x then "approved" else "approvd"` with `x` unbound hands back a perfectly
concrete `"approvd"`. A check that simply evaluated everything would report the *else
branch* of a value the process decides at run time. Not one of them is a **warning**, worded like
`RuleDataUnknownState` and for the same reason: the model may simply be behind the
process, and refusing the deploy would make the two impossible to develop in either
order. An expression that reads any variable is not checked at all and never will be.

**The edge.** A `type-link` is drawn from a class to each «enumeration» that types one of
its attributes: derived, never authored, never deletable — the store's line and the
lifecycle's line, a third time. One per pair, labelled with the attribute names that
justify it (`status`, or `status, vorzustand`), so three attributes of one enumeration
are one line that says three things.

Where a `«lifecycle»` edge already joins the same pair, the type-link is **not** drawn.
Two derived edges between one pair are two straight dock-to-dock lines on the same
pixels: the diagram would say one line where there are two, and a click could only ever
reach whichever was drawn last. The lifecycle edge is the stronger statement of the two
and it stays; the attribute is legible in the compartment beneath it. This also answers
ADR-0306's open question in the affirmative and in the only way that draws: yes, the same
enumeration may type an attribute *and* seed the lifecycle, and the diagram says so once.

Only enumerations get the edge. An attribute typed by another **class** is what an
association is for, and the author draws that one deliberately — deriving a second line
beside it would double every association in the model. An enumeration is never the end of
an association, which is exactly why it is the one that floats.

### Consequences

- **Positive:** the last of the four questions is answered from the model. A status
  written from a picker cannot be spelled wrong, and one written from an expression is
  still possible.
- **Positive:** the enumeration is part of the diagram rather than beside it, and the
  dependency a reader has to know is drawn.
- **Negative:** the deploy check sees only constants. `= "aktive"` is caught;
  `= if x then "aktiv" else "aktive"` is not, and never will be without a type system.
  The picker is what makes that gap small in practice, not the check.
- **Negative:** where an enumeration both types an attribute and seeds a lifecycle, the
  drawing says «lifecycle» and stays quiet about the attribute. One line that is true is
  better than two lines that overlap, and the compartment is right there.
- **Follow-ups:** an enumeration-typed member in a *read* — an input association's
  transform — is not checked, and the same argument would apply. Nothing offers it either.
