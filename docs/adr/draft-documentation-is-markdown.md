# ADR-DRAFT: Documentation prose is Markdown, rendered by one closed renderer

- **Status:** Proposed
- **Date:** 2026-09-04
- **Deciders:** Atlas maintainers

## Context and problem statement

`<bpmn:documentation>` is the one field every element carries, and since ADR-0025 was
amended the compiler retains it, so the prose reaches people who never open the Modeler:
the assignee working a user task, the operator reading an instance replay, and — through
a shared public link (ADR-0143) — an auditor with no account at all.

Authors have been writing that prose as Markdown for as long as the field has existed.
The Modeler's documentation field declares itself Markdown to the Developer View
(ADR-0145), which highlights it as Markdown and offers a real editor for it. Every
*reading* surface then printed it as literal text: a heading arrived at the reader as a
hash, a checklist as a column of hyphens, and an emphasised "do not" as asterisks. The
one field meant to be read by non-modellers was the one field nobody rendered.

So the question is not whether the prose is Markdown — it is written that way already.
It is: **who turns it into markup, and what stops a modeller's prose from becoming
script in a caseworker's browser?** That second half is what makes this a decision
rather than a chore. Everywhere else in the console the author of a string and its
reader are the same person, or the string comes from the engine. Here a modeller writes
and a caseworker reads, and the exported document can be handed to readers outside the
organisation entirely.

## Decision drivers

- **Rendering markup is introducing an XSS surface.** Today `esc()` makes documentation
  inert at every reading surface. Whatever replaces it must be defensible in one sitting,
  because the blast radius is the console's own origin and a public PDF's audience.
- **The prose that already exists must not change meaning.** Every documentation text
  written before this decision was displayed as plain text under `white-space: pre-wrap`.
  CommonMark joins consecutive lines into one paragraph; applying it verbatim would
  silently reflow prose written years ago that nobody will re-read until it is wrong in
  front of an auditor.
- **Buildless (ADR-0012).** No npm in the Go build, no bundler, no runtime CDN. A
  dependency has to be a vendored, pre-built asset or it cannot exist here.
- **One meaning of the markup across surfaces (ADR-0243).** The same text is read in the
  Tasks app, in the replay and in the exported PDF. If each surface parses it, they will
  differ, and the difference will be found by the person comparing the PDF to the screen.

## Considered options

1. **Leave documentation as plain text** and tell authors to write plain prose.
2. **Vendor a Markdown library plus an HTML sanitizer** (marked + DOMPurify, or similar).
3. **A small in-house renderer for a defined subset**, escaping the source before it
   parses anything.

## Decision outcome

Chosen option: **Option 3 — a small in-house renderer** (`api/web/markdown.js`), because
it is the only one that makes the safety property a *structural* one rather than a
configured one.

The renderer escapes every character of the source first, then parses, then **builds
every tag itself**. No fragment of the source is ever passed through as markup, so raw
HTML in a documentation text renders as visible text and cannot become an element. That
leaves exactly one place where author-supplied text reaches an attribute — a link's
destination — and it is guarded by an allowlist (`safeHref`): http, https, mailto, and
destinations that stay on this origin. Everything else, `javascript:` and `data:`
included, loses its href and renders as its label. The whole security argument fits in
two paragraphs and is tested against a live DOM (`e2e/markdown.spec.mjs`), which is what
makes it reviewable.

Option 2 would have been faster to write and slower to trust: the pair is around a
hundred kilobytes of vendored code whose correctness we would depend on but not own, its
safety would rest on a sanitizer *configuration* (a blocklist by nature) rather than on
what the renderer can emit, and it drags in the full CommonMark surface — raw HTML
blocks, autolinks, entity references — for a field that needs headings, lists, code and
emphasis. This repository already owns its PDF writer (ADR-0143) and its syntax
highlighter (ADR-0145) for the same reason: a focused thing we understand is cheaper to
own than a general thing we do not.

### What is rendered, and what is not

Supported: headings, paragraphs, bullet and numbered lists (nested), block quotes,
fenced and inline code, bold, italic, strikethrough, links, thematic breaks.

Deliberately unsupported: **tables**, **images**, **raw HTML**. Each is a decision of its
own — a table has to survive the PDF exporter's line breaker, an image has to be embedded
or fetched from somewhere and would put a network reference into an artifact meant to be
readable offline — and leaving them out keeps the module something a reviewer can hold in
their head.

### Two deliberate departures from CommonMark

- **A soft line break becomes `<br>`.** This is the backward-compatibility decision named
  in the drivers, made once in the renderer rather than per surface.
- **No indented code blocks.** Four leading spaces is something people do in plain text;
  it must not turn a sentence into code.

Both exist for the same reason: the corpus this renders was written before it existed.

### What does not change

The storage does not move: `<bpmn:documentation>` stays a text field in the model, the
codec preserves it, the compiler interns it as design-time metadata, and the processor
never reads it. No value type, no event, no recovery path, no invariant is touched, and
no migration is needed — Markdown is text, and every existing documentation text is
already valid Markdown that renders as the paragraphs it always was.

BPMN's `textFormat` attribute (`<documentation textFormat="text/markdown">`) is
deliberately *not* branched on. Rendering conditionally would put two paths into every
reading surface to serve models Atlas did not author; a foreign model's plain prose
renders as plain prose anyway.

### Rollout

The Tasks app first, because that is where documentation reaches someone who never opens
the Modeler and is the only surface where it is an *instruction* rather than a note. The
instance replay's Details tab, the Panorama detail panel and the information model follow
on the same module and the same shared `.md` rules.

The exported document (ADR-0143) is last and is the only one with real work in it:
`pdf.js` carries Helvetica, Helvetica-Bold and Courier but no italic face, and its
`paragraph()` sets a whole line in one font. Block structure maps onto what it already
has (`heading`, `codeBlock`, indented `text`); inline emphasis needs run-based line
breaking — per-run widths and a font switch mid-line — which is a change to the writer,
not to this renderer.

### Consequences

- **Positive:** a work instruction reaches the person doing the work as the structure its
  author intended; one renderer means the PDF and the screen cannot disagree about what
  the markup means; the XSS surface is one allowlist function with tests against a real
  DOM; no new dependency, no build step, nothing added to the binary but one small module.
- **Negative / trade-offs accepted:** a subset renderer will meet Markdown it does not
  implement, and a table typed by an author renders as the pipes they typed. Two
  documented divergences from CommonMark mean "Markdown" here is Markdown-as-Atlas-reads-it,
  which has to stay written down. Rendering is client-side, so anything that consumes
  documentation over the API (the MCP tools, `GET /api/v1/tasks`) keeps getting the source
  text — correct for those readers, but it means "how it looks" lives only in the browser.
- **Follow-ups / risks to watch:** the remaining surfaces and the PDF are not done by this
  record. Tables are the most likely thing to be asked for next, and the PDF is what
  decides whether they are affordable. If a future surface needs documentation rendered
  server-side (an email, a server-rendered public page), this module is in the wrong
  language for it — that would be the moment to reconsider, not before.

## Pros and cons of the options

### Option 1 — leave it plain text
- Good: nothing to build, nothing to attack.
- Bad: the prose is already written as Markdown, so the markers are already on the
  screen. This option is not "no markup", it is "markup that renders badly".

### Option 2 — vendor a library plus a sanitizer
- Good: full CommonMark, well-trodden, someone else's maintenance.
- Bad: safety rests on a sanitizer's configuration rather than on what can be emitted;
  ~100 KB of vendored code we depend on but do not own; brings a raw-HTML surface we
  would then have to switch off.

### Option 3 — a small in-house renderer
- Good: the safety property is structural — escape first, build every tag, one allowlist
  for hrefs; the subset is chosen for this corpus; no dependency, no build step.
- Bad: we own the parser, including its edge cases; Markdown it does not implement is a
  feature request rather than a flag.

## Links

- amends [ADR-0025](0025-full-properties-panel.md) — the documentation field and its
  amendment that made the prose readable outside the Modeler
- relates to [ADR-0143](0143-process-documentation-export.md) — the exported document,
  the last surface to render this and the one whose PDF writer needs work for it
- relates to [ADR-0012](0012-web-ui-app-shell.md) — buildless, self-contained, no npm
- relates to [ADR-0145](0145-developer-view-for-code-fields.md) — where the field already declared itself
  Markdown
- relates to [ADR-0243](0243-shared-ui-primitives.md) — one shared part per shared idea
