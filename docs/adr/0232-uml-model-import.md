# ADR-0232: Importing a UML class diagram — reading what somebody else drew

- **Status:** Proposed
- **Date:** 2026-09-03
- **Deciders:** Patrick Blumer

## Context and problem statement

[ADR-0230](0230-process-information-model.md) gave Atlas an information model: a
declared subset of the UML class diagram, owned by a process application, that a BPMN
data object's `itemSubjectRef` resolves against. It also drew a line under the
question of interchange, in its own consequences:

> **XMI is an export, not an interchange.** Round-tripping a foreign UML tool's XMI
> back into the model is out of scope and stays out until it is tested.

That line is the right default for a projection and the wrong one for a first
encounter with Atlas. The order in which this work actually happens is:

1. Somebody models the business objects — in Enterprise Architect, in Papyrus, in
   Visual Paradigm, in whatever the architecture team already uses. Often years
   before a process is drawn.
2. Somebody else models a process in Atlas and needs to say what an `order` *is*.

Today step 2 begins by retyping step 1 by hand, class by class and attribute by
attribute. That is slow, and slow is the least of it: what gets lost in a retyping is
never the class names. It is the **business key** — the one fact BPMN has no
equivalent for, the fact every cross-process capability in ADR-0230 rests on, and the
one a person transcribing forty attributes silently drops.

The same gap exists in the other direction and is not about UML at all: a model
authored in one Atlas application cannot be moved to another, or to another
installation, without the same retyping. `GET /api/v1/infomodel/models/{id}` already
hands out the whole document; nothing reads one back.

So: **does Atlas read a class diagram it did not author, from which documents, and
what does it do with everything the subset does not have a place for?**

## Decision drivers

- **The subset is the contract, and it does not move.** Whatever is read, what gets
  stored is a model `Validate` accepts. ADR-0230's store guarantee — every model on
  disk is one the subset accepts, so a deploy resolving `itemSubjectRef` never meets a
  half-model — must hold for an imported model exactly as for a drawn one.
- **Say what was lost, element by element.** This is the ADR-0211 projection
  discipline pointed the other way. A lossy read that does not report its losses is
  worse than no read at all, because the gaps are invisible until a deploy resolves
  against a class that quietly lost its key.
- **A real document must survive.** Refusing a document because it contains an
  interface, an operation or an n-ary association would refuse every real model. The
  useful answer is *most of it, and here is the rest*.
- **The business key survives or the import is pointless.** Everything cross-process
  in ADR-0230 rests on identity.
- **No new authority.** An import is an ordinary write through the application scope
  (ADR-0071/0128), on the run loop (I3), with server-issued ids.
- **Buildless and CDN-free** (ADR-0012): no XMI library in the browser and none in the
  server either.

## Considered options

### What documents are read

1. **Atlas's own JSON only.** Model portability between applications and
   installations, no UML interchange.
2. **JSON and XMI 2.5.1** — the native document, and what a UML tool exports.
3. **JSON, XMI, and a textual notation** (PlantUML, Mermaid class syntax).

**Option 2 is selected.** Option 1 solves the smaller half of the problem and leaves
the retyping exactly where it is. Option 3 adds a third parser for notations that have
no standard beyond their own renderer's grammar; the loss they would report is a
property of a tool rather than of a standard, and a text notation is easy to add later
against demand rather than against a guess.

### What happens to what the subset does not author

1. **Refuse the document**, naming the first thing that does not fit.
2. **Import what fits and report the rest**, element by element, distinguishing what
   is gone from what arrived saying something slightly different.
3. **Import what fits, silently.**

**Option 2 is selected.** Option 1 refuses every real document: a UML model of a
business carries operations, interfaces and visibility as a matter of course, and none
of them are a reason to reject the classes beside them. Option 3 is the failure mode
this whole record is about — an unreported loss is discovered by a deploy warning
weeks later, against a class the modeler believes they imported.

The report distinguishes three levels, because a reader acts on them differently:
**dropped** (not in the model), **adjusted** (in the model, saying something slightly
different — a widened multiplicity, an unresolvable type read as text), and **info**
(nothing was lost; this is worth knowing — a flattened package, a generated layout).

### Where the reading happens

1. **In the server**, as a service of `api/infomodel`.
2. **In the browser**, parsing the file before posting a model.

**Option 1 is selected**, for the reason ADR-0230 already served the relationship
matrix rather than duplicating it: the subset's rules are the server's, and a browser
that re-implemented them would drift. It also means the MCP tools and `curl` get the
same import the Data area gets, which the browser option could not offer.

### Import into an existing model, or always a new one

1. **Always create a new model.**
2. **Merge into an existing model**, reconciling by class name.

**Option 1 is selected** for now. A merge has to answer what happens to a class that
exists on both sides with different attributes, and that question is a modeling
decision — the answer is different for "the architecture team re-exported" and for
"another department's model joins ours". Creating a new model leaves the existing one
untouched and the decision with the person, who can copy across on the canvas. A merge
is a follow-up with its own record if it is asked for.

## Decision outcome

Chosen option: **read JSON and XMI 2.5.1 into a new, application-owned model, through
the same subset the canvas writes through, and report every element the subset would
not take.**

`POST /api/v1/infomodel/import` takes the document as text with the owning
`applicationId`, detects the format from the document (a UML tool writes `.uml`,
`.xmi` and `.xml` for the same file, so the extension says nothing), and answers with
the notes, the validation verdict, and either the stored model or — with `dryRun` — the
model it *would* store. Preview and import are one route with a flag, because a
preview that is produced by a second code path is a preview that can drift from what
it promises.

What the reader does with a foreign document:

- `uml:Class` → «businessObject»; `uml:DataType` → «valueType» (two values with equal
  contents are the same value, which is what a data type says); `uml:Enumeration` →
  «enumeration» with its literals. An applied stereotype of one of the three names
  overrides the metaclass.
- An `ownedAttribute` that is an association end (it names an `association`) belongs to
  the association, not to the class — keeping both would state the relationship twice.
- Multiplicity is read from `lowerValue`/`upperValue` or the `lower`/`upper`
  attributes, and mapped onto the four the subset has by keeping the two facts it
  keeps: whether a value is required, and whether there can be more than one. `0..5`
  becomes `0..*`, and says so.
- `isID` becomes the business key. An identifier whose multiplicity the document never
  states is read as **required**, not optional: the document does say something about
  that member — it identifies the instance — and reading it as optional would drop the
  key, which is the most valuable fact in the file.
- A composite or shared end is the **whole**, so the relationship runs from it to the
  part — that is what the diamond marks.
- Packages are flattened (a class is named once per model, not once per package), and
  a document with no geometry — XMI keeps the picture in a file of its own — is laid
  out on a grid, stated as such.
- Everything else — interfaces, operations, n-ary associations, association classes,
  visibility, abstractness — is dropped with a note naming the element.

Everything that survives that reading then goes through a sanitizer that enforces
every rule `validation.go` checks, by removing or adjusting rather than refusing: a
duplicate class name, an association to an enumeration, a generalization that closes a
cycle, a store over a class with no key. The handler validates the result anyway
before storing it — the store's guarantee must not rest on one function having thought
of everything.

### Consequences

- **Positive:** a model drawn in a UML tool starts working in Atlas in one step, with
  its business keys intact. A model moves between applications and installations
  through the document the API already hands out. The MCP surface gets the same
  import, so an agent can bring a vocabulary in before authoring against it. And the
  loss report is a teaching surface: it is the first place a modeler reads what this
  build's subset actually is, against their own model rather than against an
  abstract table.
- **Negative / trade-offs accepted:**
  - **It is still not a round trip.** ADR-0230's sentence stands as a statement about
    *fidelity*: what goes out as XMI and comes back is not guaranteed to be what left,
    because the subset is smaller than UML in both directions. What this record adds is
    that the difference is *reported*, not that it is gone.
  - **XMI dialects vary, and no reader covers all of them.** The reader is generic — a
    node walk that recognizes what it knows — rather than bound to one tool's output,
    and a construct it does not recognize is a note rather than a failure. Some
    documents will import less than their author expects; the report is what tells
    them so.
  - **An import always creates a new model.** Two imports of the same file are two
    models, and the second is not a merge.
  - **A grid is not the diagram somebody drew.** Layout is lost with XMI and stated as
    lost.
- **Follow-ups / risks to watch.** Merging into an existing model (above). An XMI
  *export* — ADR-0230 named it and it does not exist yet — which would make the pair
  symmetric and is the natural next slice. A textual notation (PlantUML) if it is
  asked for. Neither changes anything decided here.

## Pros and cons of the options

### Option A — refuse anything outside the subset
- Good: nothing is ever imported that the author did not intend; no report to read.
- Bad: refuses essentially every real UML document, over elements that are not what
  the import is for.

### Option B — import what fits and report the rest (chosen)
- Good: works on real documents; the loss is stated per element, so it can be checked
  and fixed on the canvas; the report doubles as the clearest statement of the subset.
- Bad: a person who does not read the report has a model that is quietly smaller than
  the one they exported.

### Option C — import silently
- Good: the shortest path from file to model.
- Bad: exactly the failure this record exists to prevent — a class that lost its
  business key is discovered by a deploy warning weeks later.

## Links

- extends [ADR-0230](0230-process-information-model.md) — the information model, its
  subset, and the sentence this record revisits
- follows the projection discipline of ADR-0211 and ADR-0189 §2 (a declared subset;
  refusals distinguish *out of subset* from *the notation says no*)
- reuses the application sharing scope of ADR-0071 / [ADR-0128](0128-process-applications.md)
- a service under `api/`, per [ADR-0147](0147-splitting-the-api-server-object.md)
