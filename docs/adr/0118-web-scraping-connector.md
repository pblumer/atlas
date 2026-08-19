# ADR-0118: A web-scraping connector — model-authored URL + CSS selector extraction

- **Status:** Accepted
- **Date:** 2026-08-12
- **Deciders:** Atlas engine team

## Context and problem statement

Atlas processes routinely need a value that lives on a **web page** rather than behind
a clean JSON API — a price, a status line, a list of headlines, the rows of an HTML
table. A user asked for a first-class **web-scraping connector**: a modeled step that
fetches a page and extracts the elements matching a CSS selector, so a process can pull
structured data off a site without a hand-rolled script task and without the author
writing HTML-parsing code.

Atlas already runs several connector *kinds* through the job path, discriminated by a
reserved job type a `TypeConnectorTask` carries (ADR-0036/0067): clio, HTTP REST, mail,
CSV, SharePoint, and Remedy. The service-task connector **catalog** (ADR-0067) was built
precisely so that "the next connector kind" is a data entry plus a worker, not a bespoke
subsystem. A web-scrape connector is the next kind.

Two shaping questions:

1. **Where does the target live?** A scrape targets an ad-hoc, per-task **URL** with no
   credentials and no operator-managed instance — exactly the REST situation (ADR-0067),
   not the clio/mail/Remedy one. The URL and the selector belong in the model.
2. **What does the task return, and how is it extracted?** The chosen shape is a **CSS
   selector that matches many elements**, yielding a **JSON array** — the natural fit for
   "a list of headlines / links / table cells". Each match contributes its text content,
   or the value of a named HTML **attribute** (e.g. `href`) when the task authors one.

## Decision drivers

- **Reuse the proven seam.** The connector-via-job pattern (ADR-0036/0007) gives crash
  recovery, non-blocking execution, and dependency isolation for free: the outbound fetch
  and the HTML parse run only in the worker, post-fsync, off the single writer, never in
  `applyToState` (I1/I2/I4, ADR-0005/0007). No engine change, and no HTML dependency
  anywhere near the hot path.
- **Extensibility is the point (ADR-0067).** Adding web-scrape should be one catalog
  entry + one moddle type + one compiler branch + one worker + one server registration —
  additive at every layer, colliding with nothing.
- **Model-authored, like REST (ADR-0067).** The value of a scrape is the ad-hoc URL and
  selector; there is no shared endpoint or secret to manage, so nothing is
  server-registered. This keeps a scrape task as light to author as a REST GET.
- **A real HTML parser, off the hot path.** CSS-selector extraction wants a proper
  parser; `goquery` (over `golang.org/x/net/html`) is the Go standard. It is pulled in
  only by the `webscrape` worker package, so the engine core stays dependency-free (I1).

## Considered options

**A. A dedicated web-scrape connector kind, model-authored (chosen).** A
`<serviceTask>` bearing an `<atlas:webscrapeConnector url selector attribute
resultVariable>` extension carries the reserved `io.atlas.webscrape` job type; an
in-process worker fetches the page, applies the selector with `goquery`, and writes the
extracted values into the result variable as a JSON array. The URL and selector live in
the model, like REST; there is no registry and no credential.

**B. Just use the generic REST connector.** Rejected as the primary answer: a REST GET
returns the *raw HTML* (or fails to decode it as JSON) with no way to extract the
elements the author actually wants. Every scrape would then need a following script task
that parses HTML by hand — the very work the connector exists to remove. REST remains
available for genuine JSON APIs.

**C. A general script task (PowerShell/Python/JS) that scrapes.** Rejected as the
first-class answer: it works, but pushes HTML parsing, dependency management, and the
fetch into user-authored script — unportable, harder to review, and outside the modeled,
searchable connector catalog. The purpose-built kind hides the parser behind two model
attributes.

**D. Inline fetch from a behavior.** Rejected on sight (as in ADR-0036): network I/O and
HTML parsing on the single writer, tempts a call inside `applyToState`; violates
I1/I4/ADR-0007.

## Decision outcome

Chosen: **option A**, the dedicated model-authored `webscrape` connector kind, first
extraction mode **selector → list**.

- **Model-authored:** the **URL** and the **CSS selector** (each literal or a FEEL
  expression over the instance's variables — the fx toggle, ADR-0067), an optional
  **attribute** name (a structural literal; omit to extract each match's text content),
  and a required **result variable** the extracted values are written back into as a
  JSON array (the output-mapping path, ADR-0014/0066). Compiled into the shared
  `ConnectorTaskDetail` (new `ScrapeSelector`/`ScrapeAttribute`, reusing `Url`/`ResultVar`)
  as deploy-time data (I5).
- **Worker:** the `webscrape` package (`Client`/`HTTPClient`/`Handler`) performs a plain
  HTTP `GET`, parses the response with `goquery`, and returns the text (or the named
  attribute) of every element matching the selector. The selector is compiled with
  `cascadia` first, so an invalid selector surfaces as a job error (incident) rather than
  a silent empty result. Registered under the reserved `WebScrapeJobTypeIndex` via
  `HandleWithOutput`, so one worker serves every deployed process.

Delivery keeps the ADR-0036 job-path guarantees: at-least-once, recovery inherited from
the job protocol, no engine change. A scrape is a **read-only GET** — an at-least-once
replay simply refetches, with no duplicate-side-effect concern, so no idempotency key is
needed.

### Consequences

- **Positive:** a process extracts a list of values from a web page at a modeled point,
  authored end-to-end in the Modeler (a searchable "Web Scraping Connector" catalog
  entry) with just a URL and a selector, and executed off the processor loop with
  recovery; models stay portable and dependency-free (the HTML parser lives only in the
  worker); adding the *next* extraction mode (single value, or named-selector → object)
  is additive on the same framework, exactly as clio grew write → query → read.
- **Negative / trade-offs accepted:** only static server-rendered HTML is scraped — a
  JavaScript-rendered SPA yields nothing without a headless browser (out of scope); one
  extraction mode to start (selector → array of strings), so a task that wants a
  structured object per row composes a following script task for now; a match's text is
  trimmed and an attribute-less match is skipped, which is convenient but lossy; no
  configurable timeout or custom request headers yet (a scrape uses `http.DefaultClient`
  with a default `Accept`); a process reaching a scrape task parks until the worker and
  the target site are reachable — the same failure mode as any connector task.
- **Follow-ups / risks to watch:** a headless-browser fetch mode for JS-rendered pages;
  additional extraction modes (single value; a map of named selectors → per-row object);
  a configurable timeout, custom headers, and a polite user-agent / rate limiting;
  optional pagination; typed coercion of extracted text.

## Links

- realizes the intent of ADR-0067 (service-task connector catalog) for a new kind, and
  mirrors ADR-0067 (REST connector) for the model-authored, credential-free shape
- reuses ADR-0036 (connector-via-job) / ADR-0007 (job worker protocol) wholesale — no
  engine change; honors I1/I2/I4/I5 and ADR-0005
- output mapping mirrors ADR-0014/0066
