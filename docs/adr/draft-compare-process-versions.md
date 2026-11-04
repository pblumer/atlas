# ADR-DRAFT: Comparing two versions of a process, element by element

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-11-04
- **Deciders:** Atlas maintainers

## Context and problem statement

Every deploy mints an immutable version of a process ([ADR-0019](0019-durable-deployments.md)),
so a process with a history has many, and "what changed between v6 and v7" is a question
that comes up all the time:

- before migrating running instances onto a newer version
  ([ADR-0162](0162-process-instance-migration.md)), where the operator needs to know what the
  instances will meet;
- when a run behaves differently from last week's, and the first suspect is a new version;
- in a review of what a publish or a deploy by an agent actually changed.

Atlas could say whether two models are the same, and where they first part:
`layout.Difference`, behind the draft comparison and the Modeler's version chip
([ADR-0452](0452-modeler-shows-its-version.md)). A first difference is one line of an
answer whose other lines the reader cannot see, and after a reordering it is not even the
most important one. Short of that, the answer was to fetch two XML documents and read them
side by side, where the diagram, the formatting and the attribute order differ in every
place the model does not.

The request was to compare two process versions in Operations and in the Modeler.

## Decision drivers

- **The comparison is the server's**, for the reason ADR-0452 gave: "the same model" must
  not depend on how a document was written, and the server already has a canonical reading
  of a document for that (`semanticDigest`: the diagram stripped, attributes at their
  schema default dropped, whitespace and comments ignored).
- **An inserted task must not make every later element read as changed.** Lining two
  documents up by position does exactly that.
- **A changed condition or script is a change.** It is often *the* change, and nothing on a
  drawing shows it.
- **One answer for every surface**: Operations, the Modeler, and an agent over MCP read the
  same list.

## Considered options

1. **A list of changes by element, computed on the server**, matched by element id, each
   changed element with the fields it states differently before and after; shown as a list
   in Operations and the Modeler.
2. **A textual diff of the two XML documents**, canonicalised first.
3. **Two diagrams side by side, the changed elements marked**, without a list.

## Decision outcome

Chosen option: **1**, and the diagram of option 3 as a later step that builds on it.

**What is compared.** `layout.Changes(a, b)` parses both documents with the diagram
stripped. An element is a BPMN element with an id that stands on its own: a child of the
root `<definitions>` (a process, message, signal, error) or of a container (process,
sub-process, ad-hoc sub-process, transaction, collaboration, lane set, lane). Everything
else inside an element belongs to it and is compared as one of its *fields*, named by a
path: `name`, `conditionExpression`, `messageEventDefinition@messageRef`,
`extensionElements/zeebe:taskDefinition@retries`,
`extensionElements/zeebe:ioMapping/zeebe:input[total]@source`. Elements are matched by id,
so a moved element is the same element, and one that changed its kind keeps its id in
bpmn-js and is reported as a changed `type`; one moved into another container as a changed
`parent`.

**Repeated parts are named the same way in both documents.** Several input mappings, headers
or lane references are told apart by an attribute every one of them carries distinctly —
`target`, then `key`, then `name` — by their text when that is short and distinct (a lane's
`flowNodeRef`), and by position otherwise. The scheme is settled from both documents at
once: settled from one, the name of the one input left after another was removed would
change from `input[total]` to `input`, and the list would report a removed and an added
input where one was removed.

**What is not compared**, each because the compiler does not read it: the
`<incoming>`/`<outgoing>` hints bpmn-js writes beside every flow node (a rewired flow is
reported once, on the flow); the ids of parts, such as the id bpmn-js gives an event
definition; and `xsi:type`, whose value spells a type with a prefix local to its document.
The root's own attributes are the document's metadata, as for the digest. So two models
whose digests differ can have an empty list; the answer then carries `sameModel: false` and
the first difference, and the surfaces say that the models differ only in what the list
leaves out, never "no changes".

**The surface.**

- `GET /api/v1/processes/{key}/comparison[?with=]` answers what changed from the version
  `with` names — by default the highest version number below `key` — to `key`: both sides
  (key, version, tag, deploy time, deployer), `sameModel`, `difference`, `changes` and
  `counts`. Both must be versions of the same process; a version of another process is a
  400, an unknown key a 404, and a first version without `with` a 404 that says there is
  nothing earlier. The documents are parsed after the run loop's turn. Readable like the
  version's XML.
- `GET /api/v1/drafts/{id}/comparison` carries the same `changes` and `counts`, from the
  version to the draft, beside the first difference it already had.
- MCP `atlas_compare_versions` (`key`, `with?`).
- Operations: *Versionen vergleichen…* in the ⋯ menu of a process row with more than one
  version, and a button in the live view of a version, comparing it with the one before.
- Modeler: the version chip's panel offers the comparison with another version on a
  version, and the full list of differences on a draft or version whose draft differs.
- Migration: the migration plan answers `at`, the element ids an instance has live
  element instances on (the process itself left out, a multi-instance body and its
  iterations once), and `changes`/`counts` from the instance's version to the target —
  parsed after the run loop's turn, and only on the plan endpoint, never once per
  instance of a batch; a batch refusal carries `at` alone. The migration dialogs lead
  with the changed and removed elements instances stand on — for one instance its
  positions, for a batch the source version's live token counts per element
  (`GET /api/v1/processes/{key}/runtime/counts`) — and fold the whole comparison below.
  A change at an element an instance stands on is the one it meets first; a removed one
  is where it is refused unless mapped.

### Consequences

- **Positive:** the question "what changed" has an answer in one request, on every
  surface — and before a migration, at the places the instances stand — that a formatting difference cannot pollute, and the same reading backs the
  chip's "≠" and its list.
- **Negative / trade-offs accepted:** matching by id means an element whose id was changed
  reads as removed and added. Field paths are technical (`extensionElements/zeebe:…`); they
  say exactly what changed, and a person who models in Atlas meets the same names in the
  properties panel and the XML view. A collaboration is deployed as one document, so the
  comparison of one pool's versions lists the other pools' changes too. Values are returned
  whole, a long script on both sides included.
- **Follow-ups / risks to watch:** the diagram — two versions side by side, the changed
  elements marked, a click in the list selecting the element — builds on this list.
  The migration dialogs name only the elements instances stand on; a change *ahead* of a
  token — a rewired flow it has yet to take — is in the folded list, not singled out.
  Element types are shown as the document spells them (`userTask`), not translated.

## Pros and cons of the options

### A textual diff of the canonicalised XML
- Good: cheap; the canonical walk exists.
- Bad: line-based, so a reordering is a change; it names lines, not elements, and a reader
  still has to work out which task a hunk belongs to.

### Two diagrams side by side, without a list
- Good: shows where in the process a change sits.
- Bad: does not show what changed in a condition, a mapping or a script — often the change
  that matters — and ADR-0310's caution applies in part: a drawing implies, a list can say
  which side each value is from.

## Links

- relates to [ADR-0452](0452-modeler-shows-its-version.md),
  [ADR-0251](0251-adjust-a-deployed-diagram.md),
  [ADR-0162](0162-process-instance-migration.md),
  [ADR-0310](0310-read-the-difference-between-what-is-built-and-what-is-planned.md)
