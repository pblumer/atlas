# ADR-0443: Publishing warns about answers in the clear

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-10-02
- **Deciders:** Atlas maintainers

## Context and problem statement

Since [ADR-0441](0441-a-position-s-answers-reach-its-processes.md), every answer of a
product's order form reaches the processes the product binds, as a variable of its own. A
process can declare an answer personal data ([ADR-0314](0314-portal-personal-data.md)).
The start act then seals it under the recipient's key, and it is erased with that person.
A process that does not declare an answer keeps it as given, in its history, in every
checkpoint, export and backup, for as long as those exist.

Nothing told anybody when that was about to happen. Three people are involved:

- the form author decides what is asked;
- the process author decides what is declared;
- the product manager binds the two together and publishes.

None of them saw the combination. An order form that asks for a phone number, bound to a process
that declares nothing, published without a word.

## Decision drivers

- **Said where the combination is made.** That is the publish, where the product manager binds a
  form and a process and makes them orderable.
- **Not in the way.** Whether an answer is personal data is a judgement Atlas cannot make. Many
  answers are not: a cost centre, a size, a vehicle type. A refusal would make every such form
  unpublishable until somebody declared a cost centre personal, which is false and has a cost:
  a declared value can no longer be read by a condition (ADR-0314).
- **Answerable.** A warning nobody can make go away teaches people to read past warnings. Every
  warning must have an action that ends it.

## Considered options

1. **Warn per answer, with a mark in the form for an answer that names nobody.**
2. **Warn per process when it declares nothing at all.**
3. **Refuse** an answer that reaches a process undeclared.

## Decision outcome

Chosen: **option 1.**

`catalog.AnswerWarnings` runs in the single publish (`POST /api/v1/catalogs/{id}/releases`) and
in a document import that publishes (ADR-0436). It looks at every product with an order form and
every process that receives its answers:

- provisioning and deprovisioning;
- the lifecycle process;
- an approval model of the installation's own.

It names each answer that the process's newest deployed version does not declare personal.

An answer is not warned about when:

- its field carries the custom property `personal = false`, which the form editor's
  *Custom properties* writes. This is the form author's word that the answer names nobody, and
  the one way to settle a warning about such a field.
- its key is one of the order's own variables, because those are never passed;
- the process is not deployed, because it is asked at the next publish.

**A warning does not refuse.** The release is made, and the answer carries the warnings beside
it:

- the publish answers `201` with the release and `warnings: [{item, message}]`;
- the import answers with `warnings: [{subject: "product:<id>", problem}]`.

The Console shows them in the publish report, which survives the reload every publish does.
`atlas import` prints them. The MCP publish tool passes the server's answer through.

The administration-services example marks the vehicle type `personal = false` and declares the
licence plate personal in both parking processes. Its test runs the same check over the example
and fails on any warning: an example is what people copy.

### Why not the others

**Option 2** is simpler and what was first proposed. It catches the process that declares
nothing, the case that is most likely an oversight. But it says nothing about a process that
declares the plate and forgets the phone number. And it can never be answered for a form whose
answers really name nobody: a cost-centre form bound to a process that declares nothing would
warn at every publish, forever. A warning per answer with a mark in the form is no harder to
build, and every warning it gives can be ended by the person it addresses.

**Option 3** would make the mistake impossible. But it would make Atlas decide what is personal
data, and it cannot know. Every form with a non-personal answer would need a mark before its
product could be published at all, including the ones published long before this check existed.
A product that is orderable today would stop being publishable on upgrade.

### Consequences

- **Positive:**
  - The person who binds a form to a process is told, at that moment, which answers would sit
    in the clear, and in which process.
  - Each warning names its remedy: declare the answer in the process, or mark the field.
- **Negative / trade-offs accepted:**
  - The mark is the form author's claim, and nothing checks it. A field marked
    `personal = false` that asks for a name is not warned about.
  - The warning looks at the newest deployed version of a process. An older version still
    running is not asked.
- **Follow-ups:** none planned.

## Links

- [ADR-0441](0441-a-position-s-answers-reach-its-processes.md):
  which processes receive the answers.
- [ADR-0314](0314-portal-personal-data.md): declaring and sealing personal data.
- [ADR-0436](0436-a-catalogue-is-imported-as-one-document.md): the document import.
