# ADR-DRAFT: Add explicit RSS and Atom extraction to the web-scraping connector

- **Status:** Proposed
- **Date:** 2026-08-26
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0118](0118-web-scraping-connector.md) deliberately shipped the first
web-scraping extraction mode as **CSS selector -> JSON array of strings**. A task
fetches a model-authored URL, applies a CSS selector to static HTML, optionally reads
one HTML attribute from every match, and writes the resulting strings to a process
variable.

That shape works for pages whose HTML is the source of truth. News sites and other
publishers often expose the same information through RSS 2.0 or Atom feeds instead.
Those feeds already carry structured entries -- typically a title, canonical link,
description/summary, and publication timestamp -- and extracting those fields through
independent CSS-selector tasks would throw that structure away and couple the model to
XML presentation details.

The requested next slice is therefore not merely another selector. It changes the
connector's model contract and, for feed modes, the element type of the result array:
from strings to structured objects. Per the repository's architecture rules this must
be an explicit decision rather than a hidden extension of ADR-0118.

The execution locus does **not** change. [ADR-0168](0168-connector-work-on-a-worker.md)
puts connector network work and parsing on a worker. RSS/Atom parsing must stay there,
after durability and away from the single-writer processor. The engine only compiles
structural choices and carries resolved task data to the worker (I1, I2, I4, I5).

No explicit Roadmap item currently names RSS/Atom extraction. This is a follow-up to
the already implemented ADR-0118 vertical slice rather than a new execution-engine
milestone.

## Decision drivers

- **Preserve existing models exactly.** A web-scrape task authored before this change
  must compile and return the same `[]string` result as today.
- **Compile structural intent, do not infer it at runtime.** Whether a task means HTML,
  RSS, or Atom is model intent and is knowable at deployment (I5).
- **Keep network and document parsing off the processor.** XML parsing belongs in the
  web-scrape worker, never in `applyToState` or the single-writer hot path (I1/I2/I4).
- **Make structured feeds structured.** One feed entry should arrive as one object,
  not as four unrelated arrays that a following script has to zip back together.
- **Reject misleading combinations.** A model that selects RSS while also authoring
  an HTML CSS selector should fail at deploy rather than silently ignore a field.
- **Offer a deterministic bound.** Authors need a simple way to take the first N feed
  entries without a following script task; the absent setting must keep today's
  unlimited HTML behavior.
- **Keep the connector catalog small.** RSS and Atom are alternate representations of
  the same read-only HTTP fetch/extract concern, not a new integration credential or
  side-effect model.

## Considered options

### Option 1 -- explicit format on the existing web-scrape connector (chosen)

Add a structural `format` attribute with `html`, `rss`, and `atom`. `html` remains the
default. Add an optional non-negative `maxItems` bound. HTML keeps selector/attribute
extraction; RSS and Atom map entries into objects.

This changes only the extraction strategy behind the existing read-only connector and
keeps its job type, retry behavior, worker placement, and result-variable contract.

### Option 2 -- auto-detect HTML, RSS, or Atom from Content-Type or the document root

This makes models shorter, but it moves a deployment-time choice into runtime content
inspection. Servers frequently publish feeds with generic `application/xml` or even
incorrect content types, redirects can change representation, and one URL returning a
different root after a deployment would silently change the process variable's shape.
That is exactly the kind of runtime interpretation I5 asks Atlas to avoid.

Rejected.

### Option 3 -- encode feed extraction through selector/attribute conventions

For example, special selector values such as `rss:item` or sentinel attributes could
switch the parser. This preserves the XML extension shape but creates an undocumented
mini-language in fields that currently mean CSS and HTML attributes. Validation and the
Modeler would become less clear than an explicit format.

Rejected.

### Option 4 -- introduce a separate RSS/Atom connector kind

A dedicated `io.atlas.feed` job type would make the result type explicit, but would
duplicate the existing model-authored GET, retries, result mapping, worker lifecycle,
and catalog wiring for a parser choice that needs no credential and has the same
read-only failure semantics.

Rejected for this slice. A separate kind should be reconsidered only if feeds later
acquire materially different lifecycle semantics such as conditional polling,
subscription state, ETag checkpoints, or deduplication across process runs.

### Option 5 -- compose several current web-scrape tasks

RSS/Atom XML could in theory be treated as markup and separate selectors used for
`title`, `link`, `description`, and `published`. That returns parallel arrays, handles
Atom links poorly because the value lives in an `href` attribute, and forces every
process to reconstruct entry identity itself.

Rejected as the first-class product behavior; it remains possible for unusual XML.

## Decision outcome

Chosen: **option 1 -- explicit `format` on the existing web-scrape connector**.

### Model contract

`<atlas:webscrapeConnector>` gains two structural literal attributes:

- `format="html|rss|atom"` -- optional, default `html`;
- `maxItems="N"` -- optional non-negative integer; `0` or absent means unlimited.

`format` and `maxItems` are structural deployment data, not FEEL expressions. The URL
continues to support the existing literal-or-FEEL toggle. For HTML, the selector also
keeps its existing literal-or-FEEL behavior.

Validation is mode-specific:

- `html`: `selector` is required; `attribute` remains optional;
- `rss` / `atom`: `selector` and `attribute` are invalid and deployment fails if
  either is present;
- every mode requires `url` and `resultVariable` as today;
- unknown formats, non-numeric `maxItems`, and negative `maxItems` fail at deploy.

`maxItems` applies after extraction in document order. In HTML mode this means the
first N selector matches; in RSS/Atom it means the first N feed entries. With no bound,
existing HTML behavior is byte-for-byte compatible at the model boundary.

The compiled detail receives dedicated fields for the new semantics (for example
`ScrapeFormat` and `ScrapeMaxItems`). Existing generic fields such as `Method` or
`Limit` are **not** overloaded: doing so would save two integers at the cost of hidden
cross-connector coupling in an immutable structure whose purpose is to make runtime
semantics obvious.

### Feed result shape

RSS and Atom write a JSON array whose entries have the stable shape:

```json
[
  {
    "title": "Example headline",
    "link": "https://example.com/article",
    "description": "Short summary",
    "published": "2026-08-26T08:30:00Z"
  }
]
```

All four keys are always present. A source that omits a field yields an empty string;
Atlas does not invent content or timestamps.

RSS 2.0 mapping for the first slice:

- `title` <- `<item><title>`
- `link` <- `<item><link>`
- `description` <- `<item><description>`
- `published` <- `<item><pubDate>`

Atom mapping:

- `title` <- `<entry><title>`
- `link` <- the `href` of the first link whose `rel` is absent or `alternate`;
- `description` <- `<summary>`, falling back to `<content>`;
- `published` <- `<published>`, falling back to `<updated>`.

The connector preserves the publisher-provided textual values after surrounding
whitespace is trimmed. It does not parse, reformat, or regenerate publication dates in
this slice; doing so would turn a source value into an Atlas interpretation and would
also make malformed-but-useful feeds unnecessarily fail.

### Worker and HTTP behavior

The existing `io.atlas.webscrape` job type and worker path remain. The resolved work
item carries the explicit format and bound together with the already resolved URL and
HTML selector where relevant.

The HTTP client advertises representations appropriate to the authored format. Feed
modes may accept `application/rss+xml`, `application/atom+xml`, `application/xml`, and
`text/xml`; this is content negotiation only, not format auto-detection. The authored
format decides which parser runs.

HTML continues through `goquery`/`cascadia`. RSS and Atom use Go's XML decoding in the
worker package. A malformed document or a document that cannot be decoded as the
authored feed format fails the job, preserving the existing retry/incident behavior.

No network access, XML parsing, current-time lookup, or result construction enters
`applyToState`. A successful completion carries the resulting variable value through
the existing durable job-completion path; recovery re-applies the persisted result and
does not refetch the feed.

### Modeler behavior

The Web Scraping Connector properties panel gains a **Format** choice with HTML as the
default and an optional **Max items** field.

- HTML shows URL, Selector, Attribute, Result variable, Retries, and Max items.
- RSS/Atom show URL, Format, Max items, Result variable, and Retries; Selector and
  Attribute are hidden because the compiler rejects them in those modes.

The Modeler authors only engine-supported fields; it does not perform feed discovery or
preview execution in this slice.

### Tests and recovery

Implementation is not complete until tests cover at least:

1. compiler compatibility for an existing HTML model with no `format`;
2. compiler validation for every mode, invalid combinations, and `maxItems` bounds;
3. RSS and Atom fixture extraction including Atom fallback fields and alternate links;
4. HTML and feed truncation through `maxItems`;
5. worker output showing `[]string` for HTML and the structured JSON array for feeds;
6. resolved/offloaded job payloads carrying format and bound deterministically;
7. a recovery test that executes a feed task, persists the completion/result, restarts,
   replays the WAL, and compares live and replay state without refetching the source;
8. Modeler authoring/round-trip tests for the new properties.

The repository Definition of Done remains the final gate: `go build ./...`,
`go test ./...`, `go test -race ./...`, `go vet ./...`, and a clean `gofmt -l .`.

## Consequences

- **Positive:** RSS/Atom becomes a first-class structured input with one task and no
  user-authored XML parser or array-joining script.
- **Positive:** existing HTML models and their `[]string` result remain unchanged.
- **Positive:** explicit format makes result shape visible in the model and keeps
  parser selection compile-time rather than content-driven.
- **Positive:** no new job type, engine behavior, credential model, or processor path is
  introduced; ADR-0118 and ADR-0168's durability boundaries stay intact.
- **Positive:** `maxItems` gives both HTML and feeds a simple deterministic cap without
  adding pagination or polling semantics.
- **Negative / trade-off accepted:** the element type of `resultVariable` depends on
  the explicitly authored format (`string` for HTML entries, object for feed entries).
  Atlas has no static process-variable schema today, so this is documented and visible
  in the Modeler rather than type-checked downstream.
- **Negative:** the first RSS mapping intentionally ignores extension namespaces such
  as Dublin Core and Media RSS, and Atom content types are returned as source text.
  Supporting provider-specific metadata is a separate extraction-shape decision.
- **Negative:** `maxItems=0` is unlimited to preserve compatibility. A future response
  byte limit or default cap may still be warranted for hostile or unexpectedly large
  documents; that is an HTTP-resource policy, not a feed-format semantic.
- **Follow-ups / risks to watch:** conditional HTTP requests (`ETag`/
  `If-Modified-Since`), feed discovery from HTML `<link rel="alternate">`, richer
  namespaced fields, explicit response-size limits, and stateful polling/deduplication.
  Any stateful subscription behavior would be enough of a semantic change to revisit a
  dedicated feed connector rather than extending this read-once scrape task.

## Links

- amends [ADR-0118](0118-web-scraping-connector.md) by adding explicit extraction
  formats while preserving its HTML selector mode
- follows [ADR-0168](0168-connector-work-on-a-worker.md): resolved connector work and
  all network/document parsing stay on the worker
- follows [ADR-0170](0170-adr-numbers-assigned-at-merge.md): this record remains a
  draft and is numbered only after merge
- constrained by `docs/architecture/invariants.md`, especially I1/I2/I4/I5
