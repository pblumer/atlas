# ADR-0099: An ArchiMate 3.2 enterprise-architecture view

- **Status:** Accepted
- **Date:** 2026-08-06
- **Deciders:** Atlas engine team

## Context and problem statement

Atlas is documented in depth *for implementers*: [`ARCHITECTURE.md`](../ARCHITECTURE.md)
plus the pillar deep-dives ([compiler](../architecture/compiler.md),
[processor](../architecture/processor.md), [data model](../architecture/data-model.md))
explain how the engine works, and the ADRs record why each decision was made. All of it
assumes the reader is comfortable in Go and in event-sourcing internals.

There was no single artifact aimed at the *other* audiences the project has to serve —
enterprise architects, evaluators deciding whether to adopt Atlas, and integrators
attaching external work. Those readers ask a different set of questions: who uses the
system, what capabilities it offers, how those capabilities rest on software and
technology, and *why* the system is shaped the way it is. Answering them by pointing at
package-level docs does not land.

The question this ADR settles: **should Atlas carry a dedicated, whole-system
architecture view for non-implementer stakeholders, and if so in what notation and
format — without creating a second source of truth that drifts from the code?**

## Decision drivers

- Communicate the whole system to stakeholders who do not read the Go source.
- Use a recognized, standard notation rather than ad-hoc boxes-and-lines that every
  reader must decode from scratch.
- Keep the artifact in-repo: versioned, reviewable in a pull request, diff-friendly, and
  rendered directly on GitHub.
- Minimize drift. The engine's behaviour is defined by the code and the deep-dives; any
  new artifact must stay subordinate to them, not compete with them.
- Diagrams must be reproducible from a text source, not hand-drawn binaries that rot.

## Considered options

1. **No dedicated view** — keep relying on `ARCHITECTURE.md` and the ADRs.
2. **An ad-hoc prose + custom-diagram overview** — a bespoke "big picture" page with
   home-grown box shapes and colours.
3. **An ArchiMate 3.2 layered view as a Markdown doc with generated SVG diagrams** —
   business, application, technology, and motivation layers, diagrams emitted by a
   checked-in script, standard ArchiMate colours and element icons.
4. **A formal ArchiMate model in a tool** (Archi `.archimate` / the Open Group Model
   Exchange File Format) as the primary source, exported to images for the repo.

## Decision outcome

Chosen option: **Option 3 — a hand-maintained ArchiMate 3.2 *layered view*** at
[`docs/architecture/enterprise-architecture.md`](../architecture/enterprise-architecture.md),
covering the business, application, technology, and motivation layers.

Key qualifications that make this safe to adopt:

- **It is a view, not the source of truth.** The document says so explicitly: where it
  disagrees with the deep-dives or the code, the deep-dives and the code win. It stays
  deliberately coarse-grained so it has little detail to drift on.
- **Standard notation.** ArchiMate's layer colours (business yellow, application blue,
  technology green, motivation purple) and element-type icons are used as-is, so the
  view is legible to anyone who knows the language and self-explanatory to those who
  don't (a notation cheat-sheet is included).
- **Reproducible diagrams.** The diagrams are generated from
  [`docs/architecture/diagrams/gen_diagrams.py`](../architecture/diagrams/gen_diagrams.py)
  into small, theme-aware SVGs committed alongside the doc — editing a diagram means
  editing the script, never hand-patching SVG. SVG was chosen over inline Mermaid and
  over PNG because it keeps an exact, designed layout while staying a diffable text file
  that adapts to GitHub's light/dark theme (see the diagrams
  [README](../architecture/diagrams/README.md)).
- **The motivation layer earns its place.** It maps the four design principles and the
  [six invariants](../architecture/invariants.md) onto the architecture, making the
  concern → decision → structure trace — the reason those invariants exist — explicit
  for a reader who would otherwise have to reverse-engineer it from the ADRs.

### Consequences

- **Positive:**
  - A single, standard-notation artifact answers the stakeholder-facing questions the
    implementer docs do not.
  - In-repo and diffable: the view is reviewed like code and versioned with it.
  - Diagrams are reproducible and theme-aware; no binary rot.
  - The motivation trace gives the invariants a visible rationale, reinforcing the
    "stop and flag it" rule when a change would break one.
- **Negative / trade-offs accepted:**
  - A second architecture artifact to keep roughly in sync — accepted, and mitigated by
    keeping it coarse-grained and explicitly subordinate to the deep-dives and code.
  - Readers unfamiliar with ArchiMate face a small learning curve (mitigated by the
    embedded cheat-sheet).
  - Regenerating the diagrams needs Python (and headless Chromium only if raster PNGs
    are ever wanted); the committed SVGs need neither to be viewed.
- **Follow-ups / risks to watch:**
  - Drift is the main risk. Keep the view high-level; when a change invalidates it,
    update the view in the same PR.
  - If a tool round-trip is ever needed, a formal `.archimate` / Open Exchange export
    (Option 4) can be added later as a generated artifact without changing this decision.

## Pros and cons of the options

### Option 1 — No dedicated view
- Good: nothing new to maintain; zero drift risk.
- Bad: leaves non-implementer stakeholders unserved; the "big picture" stays implicit
  and has to be re-explained ad hoc for every evaluation or onboarding.

### Option 2 — Ad-hoc prose + custom diagrams
- Good: full freedom; no notation to learn.
- Bad: every reader decodes bespoke shapes and colours from scratch; no shared
  vocabulary; tends toward decorative diagrams that drift silently.

### Option 3 — ArchiMate 3.2 layered view, Markdown + generated SVG (chosen)
- Good: standard, legible notation; in-repo, diffable, GitHub-rendered; reproducible
  theme-aware diagrams; motivation layer makes the "why" explicit.
- Bad: a second artifact to keep in sync; assumes some ArchiMate familiarity.

### Option 4 — Formal ArchiMate model in a tool
- Good: a true, tool-validated model; supports analysis and round-tripping.
- Bad: heavyweight for the payoff here; the source becomes a binary/tool file that does
  not review well in a PR and is easy to let rot; overkill while the view is meant to be
  a communication aid, not an analysis model.

## Links

- documents the decision behind [`docs/architecture/enterprise-architecture.md`](../architecture/enterprise-architecture.md)
  and its diagram sources in [`docs/architecture/diagrams/`](../architecture/diagrams/)
- relates to [`ARCHITECTURE.md`](../ARCHITECTURE.md) and the
  [invariants](../architecture/invariants.md) it visualizes
- the four design principles trace to ADR-0001, ADR-0002, ADR-0004, and ADR-0005
