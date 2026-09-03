# ADR-0231: Structured HTML extraction, richer feed entries, and a fetch that survives the real web

- **Status:** Proposed
- **Date:** 2026-09-03
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0118](0118-web-scraping-connector.md) shipped web scraping as *CSS selector →
JSON array of strings*. [ADR-0190](0190-webscrape-feed-extraction.md) added explicit
`rss` and `atom` formats whose result is an array of
`{title, link, description, published}` objects. Both slices are in production use.

Three gaps show up the moment a model scrapes something real:

**1. HTML extraction cannot express a record.** A page listing articles carries a
title *and* a link *and* a date per item. Today a task returns either each match's
text or one attribute of each match — never both, and never grouped. Getting title
and link means two tasks over the same page returning two parallel arrays that a
following script has to zip back together by index, which is exactly the trap
ADR-0190 rejected for feeds (its option 5). It is also fragile: an item missing its
link silently shifts every later pairing by one. In practice authors avoid HTML
entirely and look for a feed, which is why the first thing a new user does is switch
`format` to `rss`.

**2. Feed entries drop most of what a feed carries.** Four fields is enough to
display a headline and not enough to *process* one. A recurring scrape has no stable
identity to deduplicate on across runs (`guid`/`id`), no author, no categories to
route on, and no image. `description` arrives as the publisher wrote it — usually
with HTML markup and entities inside — so a model that puts it in a mail body or a
user task renders tags at the reader.

**3. The fetch assumes a friendlier web than exists.** Three concrete defects, each
observed in the code rather than inferred:

- **Encoding.** `extractRSS`/`extractAtom` decode with a plain `xml.Decoder`, whose
  `CharsetReader` is nil. A feed that declares `<?xml version="1.0"
  encoding="ISO-8859-1"?>` — still common in German-language feeds — fails outright
  with `xml: encoding "ISO-8859-1" declared but Decoder.CharsetReader is nil`, and
  the job parks on an incident. HTML has the quieter half of the same bug:
  `goquery.NewDocumentFromReader` assumes UTF-8, so a Latin-1 page yields mojibake in
  every extracted string, and nothing fails.
- **Identity.** The connector sends Go's default `User-Agent: Go-http-client/2.0`. A
  large share of sites answer that with 403 or a challenge page, which reaches the
  author as `unexpected status 403` with no hint that the request was refused for
  what it said it was.
- **RSS flavors.** `rssDocument` pins its root to `<rss>`, so an RSS 1.0 / RDF feed
  (`<rdf:RDF>` root, items as siblings of the channel) fails on a format the author
  correctly selected as `rss`.

A fourth, latent: no bound on the response body. In-process connector workers run on
the run-loop goroutine (see `connector/nettimeout`), so an endless or enormous
response is the same class of hazard as an unbounded timeout — the reason that
package exists.

The execution locus does not change. [ADR-0168](0168-connector-work-on-a-worker.md)
keeps network reach and document parsing on the worker; the engine compiles
structural intent and resolves authored values (I1, I2, I4, I5).

## Decision drivers

- **Preserve existing models exactly.** Every task authored under ADR-0118 or
  ADR-0190 must compile unchanged and return the same value it returns today.
- **One item is one object.** A scraped record should arrive assembled, not as
  parallel arrays a script re-zips by index.
- **Compile structural intent, do not infer it at runtime (I5).** Which fields an
  item has, and how a value is post-processed, is model intent and knowable at
  deploy time.
- **Never invent data.** A field a source omits is empty, not guessed; a timestamp
  is passed through as published rather than reformatted (ADR-0190's rule, kept).
- **Reject misleading combinations at deploy.** A field list under `format="rss"`,
  or an item attribute beside a field list, must fail in the Modeler rather than be
  ignored at runtime.
- **A defect in the fetch is a defect, not a knob.** Encoding, feed flavor and body
  bounds are correctness; they get fixed for every task rather than exposed as
  settings an author has to know to switch on.

## Considered options

### Option 1 — extend the existing connector: item fields, more feed fields, a corrected fetch (chosen)

`<atlas:webscrapeConnector>` gains optional `<atlas:scrapeField>` children. With
none, HTML behaves exactly as today. With fields, `selector` becomes the *item*
selector and each match yields one object whose keys are the authored field names,
each read by an optional field-relative selector and optional attribute. Feed entries
gain `guid`, `author`, `categories` and `image`. Charset detection, a declared
User-Agent, RSS 1.0/RDF, and a body cap are fixed in the client for every task.

### Option 2 — a template or mini-language in the selector field

For example `.card { title: h2, link: a@href }`. It avoids new XML, but invents a
parser and an error-message surface of its own inside a field that today means CSS,
and the Modeler could no longer offer per-field inputs. Rejected — the same reasoning
ADR-0190 used against sentinel selector values.

### Option 3 — return every match as its full outer HTML and let a script parse it

Honest and tiny, but it moves HTML parsing into a FEEL script or a script task, i.e.
onto a path that has neither a parser nor the page's base URL. Rejected.

### Option 4 — a separate "structured scrape" Worker Type

A second kind duplicates the model-authored GET, retries, result mapping, worker
lifecycle and catalog wiring for what is a different extraction strategy behind the
same read-only fetch. Rejected for the same reason ADR-0190 rejected a separate feed
kind.

### Option 5 — auto-detect encoding *and* feed flavor from the response

Detecting the *charset* is not the runtime interpretation I5 warns about: the
document states its own encoding, and reading it as declared is decoding, not
choosing a contract — the result shape is identical either way. Feed *flavor* (RSS
2.0 vs RSS 1.0/RDF) is the same: both produce the same entry objects under the
author's `format="rss"`. Accepting a document's declared encoding and its RSS flavor
is therefore adopted; sniffing which *format* (html/rss/atom) a task means stays
rejected, exactly as ADR-0190 decided.

## Decision outcome

Chosen: **option 1 — extend the existing connector.**

### Model contract

```xml
<atlas:webscrapeConnector url="https://example.com/news" selector="article.card"
                          absoluteLinks="true" maxItems="20" resultVariable="artikel">
  <atlas:scrapeField name="titel"  selector="h2"/>
  <atlas:scrapeField name="link"   selector="a" attribute="href"/>
  <atlas:scrapeField name="datum"  selector="time" attribute="datetime"/>
</atlas:webscrapeConnector>
```

- **`<atlas:scrapeField name selector attribute>`** — zero or more, HTML only.
  `name` is required and unique within the task; it is the object key. `selector` is
  a CSS selector evaluated *within* the matched item, and is optional — empty means
  the item element itself. `attribute` is optional — empty means the element's text.
  All three are structural literals: a field list is the result's shape, and a shape
  that varies per instance is not a shape.
- **`selector`** keeps its literal-or-FEEL behavior. With no fields it selects the
  values to extract (today's meaning); with fields it selects the items.
- **`attribute` on the connector** stays the no-fields attribute read, and is
  rejected together with a field list — with fields, each field states its own.
- **`absoluteLinks="true|false"`** — HTML only, default false. When true, values read
  from an `href` or `src` attribute are resolved against the document's final URL
  (after redirects). Off by default because a model authored before this record may
  already concatenate a base itself.
- **`plainText="true|false"`** — feed modes only, default false. When true, a feed
  entry's `description` has its markup removed and its entities decoded.
- **`maxItems`** keeps its meaning: the first N items (matches or entries) in
  document order.

Validation is mode-specific, and rejects at deploy: fields under `rss`/`atom`;
`absoluteLinks` under `rss`/`atom`; `plainText` under `html`; a field with no name;
two fields with the same name; `attribute` on the connector beside a field list.

### Result contract

| Task | Result |
|---|---|
| HTML, no fields | `["…", "…"]` — unchanged from ADR-0118 |
| HTML, with fields | `[{"<name>": "…", …}, …]` — one object per item, every authored field always present |
| RSS/Atom | `[{title, link, description, published, guid, author, categories, image}, …]` |

Every key is always serialized. A field whose selector matches nothing, or whose
attribute is absent, is `""` — the item still counts toward `maxItems`, which bounds
*items*, not successful reads (ADR-0190's rule for attributes, extended). `categories`
is always a list, empty when the source names none.

Adding keys to the feed entry is additive for a FEEL model — `entry.title` keeps
working — but it is a visible change to the value a process variable holds, which is
why it is recorded here rather than shipped as a fix.

### Fetch

Applies to every scrape, with nothing to author:

- The response is decoded through its declared or sniffed charset (BOM,
  `Content-Type`, XML declaration, HTML `<meta>`), so a Latin-1 page or feed reads
  correctly instead of failing or arriving as mojibake.
- Requests send `User-Agent: Atlas-Webscrape/1.0 (+https://github.com/pblumer/atlas)`
  — an honest identity a site operator can allow or block, rather than a default that
  reads as a script. It carries no Atlas version: the server's version lives in
  `api`, which the connector packages cannot import, and a string that drifts out of
  date is worse than one that never claimed to be current.
- `format="rss"` accepts RSS 2.0/0.9x (`<rss><channel><item>`) and RSS 1.0/RDF
  (`<rdf:RDF><item>`).
- The body is read through a 32 MiB cap. A document over it fails the job with a
  message naming the cap rather than being truncated into a half-parsed result.
- A feed parse that finds an HTML document says so and names the Format setting,
  instead of reporting an XML syntax error at line 1.

## Consequences

- **Positive:** the common scraping task — a list of records off a page — is one
  task and one variable. Feeds carry an identity to deduplicate on. Three classes of
  page that failed or silently corrupted now work.
- **Negative / trade-offs accepted:** the connector's model surface grows by one
  child element and two flags, and the feed entry gains four keys — a bigger contract
  to keep stable. `absoluteLinks` and `plainText` are opt-in *because* the old
  behavior must be preserved, so two defaults are the historical ones rather than the
  better ones.
- **Follow-ups / risks to watch:** a declared User-Agent can be blocked by name where
  the anonymous default was tolerated; an authored `userAgent` is deliberately not in
  this slice (a REST task can already send arbitrary headers). Per-field FEEL
  selectors, pagination, and conditional polling (ETag/If-Modified-Since across runs)
  remain out of scope — the last would be the trigger to revisit ADR-0190's option 4,
  a separate feed kind.

## Links

- extends [ADR-0118](0118-web-scraping-connector.md) and
  [ADR-0190](0190-webscrape-feed-extraction.md)
- execution locus unchanged: [ADR-0168](0168-connector-work-on-a-worker.md)
- outbound call budget: [ADR-0149](0149-bounded-connector-call-budget.md)
