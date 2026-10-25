# ADR-0474: Readable ids for new BPMN elements

- **Status:** Accepted (amended 2026-10-23: a named element's id follows its name)
- **Implementation:** Landed
- **Date:** 2026-10-23
- **Deciders:** Atlas maintainers

## Context and problem statement

An element drawn in the Modeler is named by bpmn-js: `Activity_0jmndkk`, `Gateway_1x0aq2p`,
`Flow_0n2c6ux`. A new diagram's process was named by the Modeler itself:
`Process_<base-36 timestamp + random>`. Until an author names the element, that id is what
the canvas, the properties panel, the breadcrumb, the Problems panel and every message
about the element show — and it can neither be said aloud nor told apart from its
neighbour at a glance. People asked for ids they can read.

The random ids had one real virtue: a collision was practically impossible, which matters
twice. Within a document a BPMN id must be unique. And a process id is the identity
deployments and instances are grouped by (ADR-0019, ADR-0021): a new diagram that happened
to reuse a process id already deployed would become a silent new version of somebody
else's process.

## Decision drivers

- An id an author can read, say and recognise.
- No duplicate within a diagram, ever.
- No process id the installation already holds, as far as the Modeler can know.
- Valid in every tool: an XML name, ASCII, one token per segment.

## Considered options

1. Keep bpmn-js's random ids and show the type instead of the id where no name is set.
2. `<type>_<yyyymmdd>_<researcher>` minted through bpmn-js's id registry, with checks.
3. `<type>_<yyyymmdd>_<researcher>_<random>`.

## Decision outcome

Chosen option: **2, `<type>_<yyyymmdd>_<researcher>`** — `activity_20261023_heisenberg` —
because it is the form asked for, and the collisions a shorter id invites can be checked
rather than left to chance:

- **One door.** The Modeler replaces `nextPrefixed` on the moddle's id registry
  (`api/web/elementids.js`, installed in `mountEditor`). bpmn-js mints every id through it,
  so palette, context pad, replace, copy-paste and Atlas's own code that adds a message,
  signal, error or escalation all get the readable form. The type is bpmn-js's own
  prefix in lowercase (`activity`, `event`, `gateway`, `flow`, `participant`, `process`,
  `dataobjectreference`, …). The diagram interchange (`BPMNShape_…`) keeps bpmn-js's
  scheme; nobody reads it.
- **Date.** The author's local calendar day, `yyyymmdd`.
- **Researcher.** Drawn at random from a fixed pool of 173 names in lowercase ASCII
  (`schroedinger`, `alkhwarizmi`), each once until the pool is used up.
- **Within a diagram.** A candidate is refused while the id registry (which bpmn-js fills
  on every import and keeps current through renames) or the element registry holds it.
  When every name is taken for a type on a day, a counter follows the name
  (`heisenberg2`, `heisenberg3`, …), so the supply never ends and never repeats.
- **Across the installation, for process ids.** When the Modeler opens, it loads the
  process ids of the drafts the author can see and of every deployed process; a process
  id among them is refused, for a new diagram and for a pool added to any diagram. A new
  diagram's process id is in addition put to the availability check
  (`GET /api/v1/drafts/{id}/availability`), which also counts drafts the author cannot
  see, and another one is drawn while it answers *taken*.

Ids already in a diagram are never rewritten.

### Consequences

- **Positive:** an element without a name is still recognisable; two new diagrams no longer
  look alike in a list; an id can be dictated.
- **Negative / trade-offs accepted:** across the installation the guarantee is a check, not
  a reservation. Two authors who open a new diagram in the same moment can draw the same
  free process id; the second draft save is refused with 409 (ADR-0222), so the duplicate
  cannot be stored, but the author has to choose another id. A pool's process id is checked
  only against what was loaded when the Modeler opened. Element ids other than process ids
  are unique per diagram only, which is the scope BPMN requires.
- **Follow-ups / risks to watch:** DMN elements (decisions, input and output clauses) keep
  dmn-js's ids; extending the scheme to the decision editor would go through its own moddle.
  A server-side mint that reserves a process id would close the remaining race.

> **Amendment (2026-10-23): a named element's id follows its name.** Naming an element
> "Microsoft Teams bestellen" — in the properties panel or on the canvas — renames its id
> to `activity_microsoft_teams_bestellen`, so the author is spared typing the id as well.
> The type segment is the one above; the name segment is the name in lowercase ASCII,
> words joined by underscores (umlauts spelled out, `ä` → `ae`, `ß` → `ss`; other accents
> dropped; at most 60 characters, cut at a word). When another element of the diagram
> holds the id, `_01`, `_02`, … follows, so two elements with the same name never share
> an id; a different type tells them apart without a counter. The rename runs inside the
> naming command (`postExecuted` of `element.updateProperties` / `element.updateLabel`),
> so one undo takes back name and id together.
>
> The id follows the name only while nobody chose it and nothing outside the diagram can
> know it:
>
> - **Only an automatic id moves** — the readable placeholder above, bpmn-js's random
>   default (`Activity_0abc123`), or the id the element's previous name produced. An id
>   the author typed stays, and so does one they changed after the name produced it.
> - **A deployed id stays.** Instances, their migration between versions, capability and
>   landscape bindings know an element by its id. When the Modeler opens a diagram it
>   reads the element ids of the deployed versions of its processes (the 25 newest); an
>   id among them is never renamed. Until they have arrived — or when a version could not
>   be read, or there are more — every id the diagram was opened with is kept, and only
>   elements drawn in the session follow their names.
> - **A process, a collaboration and the definitions keep their ids** whatever their
>   name: a process id is the identity drafts and deployments are filed under.
>
> A name without a letter or digit (`???`, an empty one) leaves the id as it is. A peer in
> a live session (ADR-0140) holding the old id's lock releases it with its next selection.

## Pros and cons of the options

### Option 1 — keep random ids, show the type
- Good: no collision risk; no change to stored models.
- Bad: the id the author copies, searches for and reads in messages stays unreadable.

### Option 2 — `<type>_<yyyymmdd>_<researcher>`
- Good: the form asked for; readable; a valid XML name.
- Bad: a small name space per type and day, which is why every id is checked and a counter
  takes over.

### Option 3 — with a random suffix
- Good: collisions practically impossible again.
- Bad: brings back the unreadable part the change exists to remove.

## Links

- relates to [ADR-0013](0013-embed-bpmn-js-modeler.md) (embedded bpmn-js modeler),
  [ADR-0019](0019-durable-deployments.md) (durable deployments),
  [ADR-0021](0021-diagram-drafts.md) (diagram drafts),
  [ADR-0222](0222-artifact-id-renames.md) (an artifact's id is its identity; a collision is refused)
