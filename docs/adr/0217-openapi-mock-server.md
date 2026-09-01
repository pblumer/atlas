# ADR-0217: A mock REST API served from an OpenAPI document

- **Status:** Accepted
- **Date:** 2026-09-01
- **Deciders:** Atlas maintainers

## Context and problem statement

A REST connector task names the URL it calls, in the model
([ADR-0067](0067-service-task-connector-catalog.md)). That is the right place for it —
the endpoint is what the *process* integrates with, not something an operator
configures — and it means there is nowhere to point a draft except at the real API.

So an author with a REST task has three ways to try their process, and all three answer
a question they did not ask:

- **Call the real API.** It is production by definition, and frequently does not exist
  yet: the process and the service it calls are built in the same quarter, by different
  people.
- **Replace the task with a mockup** ([ADR-0120](0120-mockup-service-task.md)). The
  engine simulates the *task*, which is right while the integration is still an idea and
  useless once it exists: the URL, the headers, the result mapping are all gone from the
  element, so the run proves the flow around the task and nothing about the task.
- **Hand-write a stand-in**, the way `atlas mock-remedy` implements the three endpoints
  the Remedy client calls ([ADR-0106](0106-bmc-remedy-connector.md)). That works, and it
  is one Go package per API. It scales to the systems Atlas itself integrates with and
  not one step further — a REST task calls whatever the customer has.

What makes this worth solving generically is that the answer is usually already written
down. The API a process calls comes with an OpenAPI document far more often than not,
and that document states the paths, the statuses, the schemas, and — where somebody
took the trouble — the exact example responses.

## Decision drivers

- **The model must not change between a mockup run and a real one**
  ([ADR-0181](0181-ad-connector-mock-mode.md)). A model that behaves differently in the
  two is a model whose test proves nothing about production.
- **Refuse rather than improvise.** A mock that answers everything teaches a model to be
  wrong, and the lesson arrives in production.
- **The same document must produce the same bytes.** A demo is re-run in front of
  people, and a test asserts on a body.
- **Nothing new on the engine.** [ADR-0164](0164-no-in-process-service-tasks.md) moves
  connector work off the run loop; a mockup facility inside the engine would pull it
  back.
- **One place to look at what a mockup did**
  ([ADR-0216](0216-mockups-are-one-view.md)). Atlas already had
  three mockups reporting into three different places. A fourth shape is the thing that
  record exists to stop.

## Considered options

1. **A spec-driven mock server as a subcommand** — `atlas mock-openapi --spec x.yaml`,
   in the shape of `atlas mock-remedy` but reading what to serve from the document.
2. **Document an existing mock server** (Prism, WireMock, Microcks) and ship nothing.
3. **A `mock` provider per Worker Type**, the way the Jira mockup is designed.
4. **Extend the mockup service task** to return a canned body from the element.

## Decision outcome

Chosen option: **"a spec-driven mock server as a subcommand"**, in package
`connector/rest/openapimock`.

- **The document is compiled, not interpreted.** Every operation becomes a matcher and
  every response the exact bytes it will answer with, at load. It is the engine's habit
  ([ADR-0004](0004-compile-bpmn-to-indexed-graph.md)) applied where it buys something
  different: an unresolvable `$ref` or an unreadable example is an error in the
  terminal that started the mock, not a null in the middle of a demo.
- **The document wins.** A response body is the `example` where the document states one,
  the first of its named `examples` where it states those, and otherwise a value
  generated from the schema — every property, not only the required ones.
- **Generated values are deterministic and unmistakably synthetic**: the Unix epoch, the
  nil UUID, the ranges RFC 5737 and RFC 3849 reserve for documentation. Two runs of a
  demo produce the same output, and nothing the mock produced can later be read as
  though it had been real.
- **A caller can ask for a stated path** with `Prefer: code=404` or
  `Prefer: example=rex` (Prism's convention, RFC 7240's header). **A preference the
  document cannot honour is refused**, not ignored as the RFC allows: a test written
  against the 404 path that quietly receives a 200 passes, and the model it was judging
  is wrong in production instead.
- **The router is ours, not `net/http`'s.** A mux pattern expresses most OpenAPI
  templates, but the paths come from a file somebody else wrote: a document holding both
  `/pets/{id}` and `/pets/{petId}` panics a mux, and `/reports/{year}-{month}.csv`
  cannot be expressed at all. Sixty lines of matcher serve every template OpenAPI
  allows and take nothing down at startup.
- **It reports into the Mockups view, and adds no fourth place to look.**
  `GET /__mock/calls` is the journal — what was called, in what order, with which
  `X-Request-ID` (the job key) and body — and `GET /__mock/report` is that journal and
  the operation table inside the envelope
  [ADR-0216](0216-mockups-are-one-view.md) defines, under kind
  `openapi`. The endpoints sit under `/__mock/` rather than `/mock/` because a document
  is free to describe `/mock/anything`.
- **YAML is a direct dependency now** (`gopkg.in/yaml.v3`, already in the module graph).
  Most OpenAPI documents in the wild are YAML; a mock that reads only JSON would be a
  mock nobody points at their own API.

### What it deliberately does not do

It mocks; it does not validate. A request body is recorded and never checked against the
schema, no security scheme is enforced, and nothing is stateful — a POST followed by a
GET does not return what was posted, it returns what the document says a GET returns.

The distinction that matters here is between *refusing what it cannot serve* — which it
does, because a mock that improvises teaches a model to be wrong — and *checking what
the caller sent*, which is a second feature with its own error shapes. Request
validation is the first follow-up, not a gap this record is unaware of.

### Consequences

- **Positive:** any API with a document can be stood up in one command, with the model
  unchanged and the run reproducible. Adding an API costs nothing — no package, no
  registry entry, no server code.
- **Negative / trade-offs accepted:** it authenticates nobody, so the journal is
  readable by anyone who can reach the port — it is a dev aid holding invented data,
  bound where the worker calling it can reach it and no further, exactly as
  `atlas mock-remedy` is. The mock has no memory, so a process whose second
  call depends on its first sees a fixture, not its own write. A document with no
  examples and thin schemas produces a thin mock, which is the honest reflection of the
  document. And a mock that does not validate will accept a request the real API
  rejects — the failure it cannot save an author from.
- **Follow-ups / risks to watch:** request validation against the operation's
  parameters and body schema; posting the report once `POST /api/v1/mocks` exists (the
  envelope is already the one that route will take, so the reporter is small); a
  Console-managed mock — upload a document, Atlas serves it — if the standalone process
  proves itself, which is the path the AD mockup took
  ([ADR-0193](0193-ad-mock-in-the-console.md), [ADR-0202](0202-atlas-manages-the-ad-mock-seed.md)).

## Pros and cons of the options

### Option 1 — a spec-driven mock server as a subcommand
- Good: one command per API, no code per API, and the document is usually already there.
- Good: it lands in the shape the repository already has for a mock, and reports into
  the view the other mockups are moving to.
- Bad: a generic mock is faithful to a document, which is not always faithful to the API
  the document describes.

### Option 2 — document an existing mock server
- Good: nothing to build or maintain; Prism is better at this than a first cut.
- Bad: a Node runtime beside the single binary, for a project whose distribution is one
  file ([ADR-0011](0011-single-binary-distribution-and-web-ui.md)); and nothing that can
  report into the Console, which is where the operator trying the model is looking.

### Option 3 — a `mock` provider per Worker Type
- Good: exactly right for a type Atlas ships, where the mock can refuse what the real
  system refuses.
- Bad: the REST Worker Type has no fixed counterpart to mock — the API is whatever the
  model names. This option is not an alternative to option 1 so much as the other half
  of the story, and the Jira mockup is where it belongs.

### Option 4 — extend the mockup service task
- Good: no new process at all.
- Bad: it replaces the task, so the URL, the auth and the mapping are not exercised —
  the thing being tried is the flow, which ADR-0120 already covers.

## Links

- builds on [ADR-0181](0181-ad-connector-mock-mode.md) (a mock is faithful where being faithful is the point)
- relates to [ADR-0216](0216-mockups-are-one-view.md) (the envelope this mock reports in)
- relates to [ADR-0067](0067-service-task-connector-catalog.md) (the REST connector task this stands in for)
- relates to [ADR-0106](0106-bmc-remedy-connector.md) (the hand-written mock this generalizes)
- contrasts [ADR-0120](0120-mockup-service-task.md) (simulating the task rather than the API)
