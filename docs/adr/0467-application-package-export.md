# ADR-0467: An application and its shop move as a package, and an import asks before it replaces

- **Status:** Accepted (amended 2026-10-20: an import remembers, per artifact, the version the last import brought, and asks only about what both sides moved — see "The plan remembers the last import"; constants are imported through `POST /api/v1/applications/{id}/constants/import`)
- **Implementation:** Landed
- **Date:** 2026-10-20
- **Deciders:** Atlas maintainers

## Context and problem statement

An application built on a test installation, and the shop bound to it, are meant to reach
production. The parts existed one by one:

- the application's source travels as a tree ([ADR-0134](0134-git-backed-applications.md));
- a catalogue travels as a document ([ADR-0451](0451-catalogue-export.md));
- `atlas import` installs a directory of both ([ADR-0437](0437-a-package-is-imported-from-the-command-line.md)),
  and named the export that would close the round trip as its follow-up.

Putting them together showed five gaps:

1. **Nothing wrote a package.** A package was assembled by hand from two downloads.
2. **Decision models did not travel.** The source tree carried a decision as a reference to
   a model handle. Since the decision editor and the model upload write models into the
   server's own folder ([ADR-0321](0321-decision-drafts.md)), that handle exists only on the
   server that wrote it. The application imported elsewhere and then refused to
   publish: "1 DMN reference(s) unresolved".
3. **A catalogue document carried the server's ids.** Its audiences, members, approvers and
   eligible groups are principal ids, minted per server and meaningless on the next.
4. **An import replaced work without asking.** The source import wrote every artifact it
   carried over whatever the target held under the same id. The catalogue import wrote every
   catalogue and product again. A process edited on production, a product renamed there, was
   gone after the next import, and nobody had been told it would be.
5. **An import was never a no-op.** Every run saved every draft anew, moved every catalogue's
   revision on, published a new application release ([ADR-0437](0437-a-package-is-imported-from-the-command-line.md)
   accepted this as a cost) and released every catalogue again.

The application's constants were a sixth question. [ADR-0453](0453-application-values-per-installation.md)
keeps their values per installation, and its export carries none. A target therefore starts
without a value for any of them, and nothing says which.

## Decision drivers

- **Idempotent.** The same package imported twice changes nothing the second time: no second
  application, no write, no release.
- **Nothing replaced unasked.** What differs on the target is work done there. It is replaced
  only when a person decided so, artifact by artifact.
- **No browser needed, and none excluded.** A pipeline imports without anybody to ask; a person
  at a terminal or in the Console is asked.
- **One door per store.** The server gains no route that writes a store some other way. What
  it gains is a plan on the routes it has.
- **What belongs to the installation stays there**, unless a person chooses otherwise.

## Considered options

For writing the package:

1. **A CLI command over the existing routes:** `atlas export`.
2. **A server route that answers the whole package** as one archive.

For a changed artifact on import:

1. **Plan, then explicit decisions:** each changed artifact is overwritten or kept as the caller
   says, and one left undecided stops the import before anything is written.
2. **Keep the target's version** silently and import the rest.
3. **Overwrite**, and list what was overwritten.
4. **Merge** the two versions.

## Decision outcome

Chosen: **`atlas export`** (option 1), and **plan, then explicit decisions** (option 1).

### The package

`atlas export --application KEY [--catalog ID]... DIR` writes the directory `atlas import`
reads:

| File | Content |
|---|---|
| `atlas.json`, `processes/`, `forms/`, `decisions/` | the application's source tree, as `GET /api/v1/applications/{id}/source` answers it |
| `konstanten.json` | the application's constants, as this server holds them |
| `katalog.json` | the catalogues, with every product they maintain or offer |
| `fragen.json` | one question per group and person the catalogues name |
| `antworten.json` | the answers, as this server names them |

- **Principals become questions.** Each group or person the catalogues name — audience,
  member, approver, eligible group — is replaced by a `{{placeholder}}`. Its name is the
  principal's name, so the same principal is the same question in every export. The answer is
  the name, which `atlas import` finds in the target's directory (ADR-0437). An id the
  directory does not know is a question without an answer, and the export says so.
- **Text that looks like a placeholder** is asked as a question answered with itself, so it
  arrives as it was instead of stopping the import.
- **A process a product is bound to travels once.** It travels in the application when the
  application holds it. Otherwise it travels in `katalog.json`, which the catalogue import
  deploys first (ADR-0451).
- **What cannot travel is named** with what it will do on the target:
  - a form outside the application;
  - a process this server deploys nowhere;
  - a product whose home catalogue the package leaves out.
- **`publish`** is set only when every exported catalogue has a release here. A shop still
  being built is not published on the target for what is unfinished here.
- **A directory an earlier export wrote is updated in place.** The files the earlier manifest
  named and the application no longer has are removed. A README beside them stays, and so does
  a question reworded by hand. A directory holding anything else is refused before the server
  is asked anything.

The command uses `GET …/source`, `GET /api/v1/catalogs/{id}/document?processes=true`,
`GET /api/v1/catalogs/{id}/releases`, `GET /api/v1/principals` and
`GET /api/v1/applications/{id}/constants`, under the authority each already asks.

### Decision models in the source tree

A model the server keeps in its own model folder travels as `decisions/<handle>.dmn`, once
however many of its decisions the application references. The manifest's decision entry
names it under `path`. This refines ADR-0134, which reasoned that a reference names a
temis-owned model Atlas must not copy. That still holds where a remote temis service serves
the models: there the reference travels alone, and the import names the models it did not
write (`modelsSkipped`).

- **The format version** becomes 2 for a tree that carries a model. A tree without one is
  still written as version 1, which every reader reads whole.
- **On import**, each model is compiled before anything is written.
- **A handle the target holds with other content** is replaced only when decided, and is
  refused while a decision outside the application resolves it. Replacing it would change that
  decision without its author knowing. Keeping the target's model is always a way through.
- **Bytes are carried verbatim**, so an unchanged model compares equal on re-import.

### The plan and the decisions

Both import routes compare every artifact they carry with what the server holds, and class
it **new**, **unchanged** or **changed**:

- **The source import** (`POST /api/v1/applications/source`) compares the application's name,
  every process, form and decision, and every decision model.
- **The catalogue import** (`POST /api/v1/catalogs/import`) compares every catalogue and
  product, and every process the document deploys against the newest deployed version.

The decisions follow from the class:

- **New is written; unchanged is not written at all.**
- **Changed is written only when `?overwrite=` names it, and left when `?keep=` names it.**
  Refs are `kind:id` (`process:onboarding`, `product:laptop`). `*` stands for every changed
  artifact the other list does not name.
- **A changed artifact named in neither list stops the import with 409**, the undecided refs
  and the plan, before anything is written. `?dryRun=true` answers the plan alone.
- **Decisions are taken again where the writes happen.** A record changed between the plan and
  the import is refused rather than written on the strength of a decision about what it was.
  The CLI and the Console therefore name every decided ref explicitly, never with `*`.
- **A publishing catalogue import releases a catalogue only** when it has no release yet, or
  when the import changed it or a product it offers and the release would differ from its
  newest.
- **Importing products `?into=` a catalogue** offers them there without asking. That is what the
  importer named.

The shared vocabulary lives in `api/importplan`.

`atlas import` shows the plan before its first write and settles what the flags leave open:

- **At a terminal**, it asks one question per changed artifact. The default, Enter, keeps the
  server's version.
- **In a pipeline**, it stops and names each artifact, with `--overwrite` and `--keep` as the
  answer. ADR-0437 set interactive prompting aside because a pipeline would hang; asking only
  when standard input is a terminal keeps that property.
- **`--dry-run`** shows the plan and writes nothing.
- **The application is published only** when the import changed it or it was never published
  here.

The Console's source-tree import and its catalogue imports show the same plan as a dialog: one
box per changed artifact, unticked meaning keep.

### Constants travel as a suggestion

`konstanten.json` carries the values the exporting server holds. `atlas import` treats them like
any other artifact, against what the application holds on the target:

- **A constant the target lacks is set.**
- **One it holds with another value is changed**, and is decided. INT and PROD rightly differ,
  and the default at a terminal keeps the target's value.
- **One the target holds and the package does not name** is not touched.

Setting a constant is an administrator's, and a token never carries that role. A refused
write therefore does not fail the import; it names what an administrator still has to set.
At the end the import reports every constant the published application reads that the target
does not hold.

This departs from ADR-0453's "the portable design-time export does not" carry values. It does
so in the package, not in the source tree. The tree stays design source. The package is the
unit that crosses installations, and a person decides each value that would replace one.

### The plan remembers the last import (amended 2026-10-20)

As first built, the plan compared the import with the server and nothing else. That could not
tell a process edited on production from one a newer package had moved on: both read as
changed. Every update of a package therefore asked about every artifact it touched, and a
pipeline had to pass `--overwrite all` — which also replaced the work the plan was meant to
protect.

Each import now remembers, per artifact, the hash of the version it brought — its **base** —
in a store of this installation (`import-baselines`, one record per application and one for
the catalogue; class instance, since it describes this installation's history and means
nothing elsewhere). With three versions the plan has two more classes:

| Status | Server | Import | Without a word |
|---|---|---|---|
| **behind** | as the last import left it | newer | taken |
| **ahead** | edited here | as the last import brought it | kept |
| **changed** | edited here | newer | asked |

- **Changed is also every difference where no base exists.** That covers an artifact never
  imported, and the first import after this change. That import asks once, as before; the
  bases exist afterwards.
- **A named ref wins either way.** `?keep=` holds back a behind artifact, and `?overwrite=` puts
  the import's version back over an ahead one.
- **`*` decides only what is changed.** `overwrite=*` never replaces work done only here, and
  `keep=*` never holds back what only the import moved.
- **Every artifact an import settled becomes the base**, whether written or kept. A changed
  artifact kept against this version is then ahead of it, and is asked about again only when
  a later import brings it changed.
- **Hashes cover the parts each kind is compared by.** Name and newline-terminated content for
  a process or form; name and handle for a decision; bytes for a model; the record without
  revision, times, owner and theme for a catalogue or product; the model for a process a
  document deploys; type and value for a constant. A hand-written file that ends without a
  newline no longer reads as changed on every import.
- **Models stay guarded when behind.** A model the last import wrote and a newer tree brings is
  still refused while a decision outside the application resolves its handle.
- **Constants are imported through their own route**, `POST /api/v1/applications/{id}/constants/import`,
  with the same plan, decisions and base. This makes the INT/PROD case quiet: a value an
  administrator set here while the package suggests what it suggested before is ahead, and
  kept without a question.
  - **The route is a modeler's** with an editor's role on the application, because comparing
    and remembering are not setting.
  - **A value is written only for an administrator.** Anyone else gets `notSet`, and nothing is
    written or remembered for them. The suggestion is therefore offered again rather than taken
    as settled.
  - **MCP leaves the route out**, as it leaves out the PUT it complements.
- **What the clients send.**
  - `atlas import` lists behind and ahead in its plan, asks only about changed, and names only
    what the operator decided. An artifact edited between plan and import is then refused
    rather than replaced.
  - The Console dialog lists three groups — changed on both sides (unticked), newer in the file
    (ticked), changed here only (unticked) — and names behind and ahead only where a box was
    moved.

Considered and set aside:

- **Storing the base on each record** (a field on drafts, forms, catalogues and products) would
  need no store. But every writer that rebuilds a record would have to carry the field, a
  model file has no record to carry it, and an export would have to strip it.
- **Storing whole base versions** would allow a merge, which this record refuses. A hash
  answers the only question asked: is this the version the last import brought?

### Why not the others

**A server route for the whole package** would serve the Console without a terminal, which is
its strongest point. But it would duplicate three exports in one more format. It would also
take the principal and constant handling onto the server, where the catalogue package has no
business with the directory. A later Console button can call the same routes the command does.

**Keeping the target's version silently** is safe for the target and wrong for the person:
the import "succeeds", and production quietly differs from the package nobody looked at again.

**Overwriting with a listing** is what the imports did, and what made them unsafe to repeat.

**Merging** BPMN, a form or a catalogue record can produce something that parses and is
silently wrong. ADR-0134 refused to merge for that reason, and it holds here.

### Consequences

- **Positive:**
  - An application with its forms, processes, decision models, constants and shop moves from
    one installation to another with one command on each side.
  - The package is reviewable in git and comes back unchanged from the target.
  - An import is idempotent and repeatable from a pipeline.
  - Work done on the target is never replaced without a decision naming it.
- **Negative / trade-offs accepted:**
  - **Every import first asks for the plan.** That is two requests and one more turn of the
    run loop per import.
  - **The bases are this installation's.** A restored snapshot carries them. A design-time
    restore onto another installation does not, so its first import asks about every
    difference, as an import without bases always does.
  - **A script that relied on an import replacing changed artifacts** now gets 409 until it
    passes `overwrite=*`. That includes the Console's earlier behaviour and the shop
    handbook's installer for a catalogue.
  - **The shop handbook's browser installer** still writes forms and drafts one by one,
    matches the application by name, and shows an undecided catalogue import as a list of
    problems with no way to choose. It installs examples into fresh servers, where nothing
    differs.
  - **Not carried:** information models, Playground scenarios, Panorama models, process
    documentation, Workers, server constants, and a catalogue's theme, logo and pictures.
    Each either belongs to the installation or needs an idempotent import it does not have
    yet. Information models are the likeliest next step: they are design source, and their
    import mints a new id on every run.
- **Follow-ups:**
  - Carry information models, once their import can find what an earlier one created.
  - Let the shop handbook's installer use the plan, or `atlas import`'s steps.
  - A Console button that writes a package, if a person without a terminal needs one.

## Links

- [ADR-0134](0134-git-backed-applications.md): the source tree, which now carries the models
  Atlas keeps.
- [ADR-0436](0436-a-catalogue-is-imported-as-one-document.md) and
  [ADR-0451](0451-catalogue-export.md): the catalogue document and its export.
- [ADR-0437](0437-a-package-is-imported-from-the-command-line.md): the package and
  `atlas import`, whose "every run publishes" this removes.
- [ADR-0453](0453-application-values-per-installation.md): constants per installation, which
  now travel as a suggestion.
- [ADR-0321](0321-decision-drafts.md): decision models written into the server's own folder
  under a handle.
