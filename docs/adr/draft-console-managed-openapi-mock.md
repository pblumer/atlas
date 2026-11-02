# ADR-DRAFT: A mock API an author turns on in the Console

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-11-02
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0217](0217-openapi-mock-server.md) built the mock an author with a REST task
needs: `atlas mock-openapi --spec x.yaml` compiles an OpenAPI document and serves it,
faithfully enough to have been run against Stripe, DocuSign, Asana and DigitalOcean's
1141-file document. It closed the question of *what* to serve.

It left open the question of *who starts it*, and named the answer as a follow-up:
"a Console-managed mock — upload a document, Atlas serves it — if the standalone
process proves itself, which is the path the AD mockup took."

That path is worth repeating because of what it cost the first time. The AD mockup
began as `ATLAS_AD_MOCK=1` on the worker, and [ADR-0193](0193-ad-mock-in-the-console.md)
moved the switch into the Console on one finding: an environment variable means
**restart the server**, so "the switch that exists to make trying a process out cheap
costs an engine restart, and the person who most wants to flip it — a process author
part-way through drafting a joiner — is the person least placed to restart the server
everyone else is using." [ADR-0202](0202-atlas-manages-the-ad-mock-seed.md) then took
the *seed* off the host filesystem for the same class of reason: "the Console is
org-wide; a path belongs to one machine."

The OpenAPI mock is today exactly where the AD mockup was before both records. A
process author who wants to try their REST task needs a shell on a host, a copy of the
document placed on that host, a free port, and a URL their worker can reach. Every one
of those is a thing an author of a model does not have and should not need.

## Decision drivers

- **The flip must not cost a restart** ([ADR-0193](0193-ad-mock-in-the-console.md)).
  An author part-way through a draft is the user; a server restart is not available to
  them.
- **Atlas holds the document, not a path to it**
  ([ADR-0202](0202-atlas-manages-the-ad-mock-seed.md)). A filename typed into a
  browser is a promise about a filesystem the browser cannot see.
- **The model must not change between a mockup run and a real one**
  ([ADR-0181](0181-ad-connector-mock-mode.md), ADR-0217's first driver). Only the host
  in the task's URL may differ.
- **Refuse rather than improvise** (ADR-0217). A document that will not compile must be
  refused at upload, where the person who chose it is standing.
- **One place to look** ([ADR-0216](0216-mockups-are-one-view.md)). This must report
  into the Mockups view, not become a fifth page.
- **Nothing new on the engine's run loop** ([ADR-0164](0164-no-in-process-service-tasks.md)),
  which is ADR-0217's own driver and the one this record has to argue with rather than
  cite.

## Considered options

1. **Serve it from the engine's own HTTP surface**, from a document uploaded into
   Atlas and switched on per record.
2. **Supervise `atlas mock-openapi` as a child process**, the way `--supervise` runs
   workers, with Atlas writing the document into a directory it owns.
3. **Adopt an external mock server** — Mokapi, Prism, WireMock, Microcks — and have
   Atlas write its configuration and manage its lifecycle.
4. **Leave it standalone** and document the shell commands.

## Decision outcome

Chosen option: **"serve it from the engine's own HTTP surface"**, in package
`api/openapimocks`.

A record holds a name, the document Atlas was handed, and whether it is on. Uploading
compiles the document immediately and refuses it with the compiler's own message if it
will not compile; enabling it publishes it at `/mock-api/{name}/…` on the address Atlas
already listens on, with the document's own base path below that — so a document written
against `https://api.example.com/v1` mocks `/mock-api/petstore/v1/…` and every path
below it is the one the real API states. There is no port to choose, no file to place, no
process to start, and no restart — which is the whole point of the record.

**What changes in the task is its base URL, not only its host.** ADR-0217's driver, taken
from ADR-0181, is that the *model* must not change between a mockup run and a real one,
and it does not: the method, the headers, the authentication, the result mapping and
every path below the base are untouched. But the substitution an author makes is
`https://api.example.com/v1` → `http://atlas/mock-api/petstore/v1`, which is a base and
not a hostname. That is what any reverse-proxied mock looks like, and it is the price of
not allocating a port per mock; the alternative — a listener of its own per document — is
option 2, and its costs are below. The Console prints the exact base to paste, because an
author composing it by hand is the one place this could go wrong.

**Why this may live in the engine's process, when connector work may not.** ADR-0164
bans work that *can block the engine*: a call to someone else's host, on the single
writer's goroutine. A compiled mock is the opposite of that in every respect that
record cares about. `openapimock` turns the document into matchers and finished
response bytes at load (ADR-0217's "compiled, not interpreted"), so serving a call is a
path match and a write of bytes already in memory — no outbound I/O, no filesystem
read, no credential, and nothing durable. The handler never enters `runloop.Loop`: it
reads an atomic pointer to the compiled set, so a mock call cannot queue behind the
processor and the processor cannot queue behind a mock call. And the precedent is
already here and accepted — the SharePoint mockup (ADR-0484) serves from the engine's
own process on exactly this reasoning, which `handleSharePointMockOperation` states as
"it writes nothing durable and reaches nothing outside this process".

**The served route is unauthenticated, and the journal is not.** A mock must
authenticate nobody or the model would have to carry credentials it will not carry in
production, which is ADR-0181's rule. So the operation route is public, as the
standalone mock's whole port is. The journal is the part that is *not* synthetic: it
records request bodies, and those come from real instances of a real draft. ADR-0217
accepted that exposure because a standalone mock is bound where only its caller can
reach it; on the engine's address that reasoning does not carry, so it is not carried.
`/__mock/calls` and `/__mock/report` are **not** served on the public route — they are
404 there — and what a run did is read through the authenticated Mockups view instead.
This is stricter than the standalone mock, deliberately.

**A document that is off is inert.** The absence of a record and a stored "off" are the
same thing here, unlike ADR-0193's switch: there is no inherited environment variable
to override, so a record that is off compiles nothing and serves nothing, and the only
state is what an authenticated operator turned on.

**It reports in ADR-0216's envelope** under kind `openapi`, collected at read time
through `GET /api/v1/mocks` beside the SharePoint mockup — the same shape ADR-0217's
`GET /__mock/report` already produced with nowhere to post it. No `POST /api/v1/mocks`
and no report store are needed for a mock the server holds itself, so neither is built.

### Consequences

- **Positive:** an author with a REST task and a document gets a running mock without
  a shell, a host, a port or a restart, and the task's URL changes by its host alone.
  The journal stops being world-readable. Adding an API still costs no code.
- **Positive:** the standalone subcommand is untouched and stays the tested path for a
  mock that must live outside Atlas — on another host, or bound to a port a legacy
  client insists on.
- **Negative / trade-offs accepted:** the compiled documents sit in the engine's
  memory, and a 4.3 MB Stripe document is a real number rather than a rounding error;
  the bound is the upload budget and the operator's own choice to enable it. The mock
  still has no memory and still does not validate, exactly as ADR-0217 left it. And a
  public route on the engine's address is a new unauthenticated surface, mitigated by
  being off by default, serving only generated or document-stated data, and withholding
  the journal.
- **Follow-ups / risks to watch:** request validation remains ADR-0217's first
  follow-up and is untouched here. A per-record switch for whether the journal records
  bodies at all would make the withheld-journal decision unnecessary. The *rate* of
  calls on the public route is not bounded — what one call can cost is (the journal caps
  its entries and each recorded body), but nothing rate-limits a caller the way the
  public form routes limit theirs; that is acceptable for a dev aid serving bytes from
  memory and is the thing to revisit first if one of these is ever left on somewhere
  exposed. The API answers a *path* rather than an absolute URL, because the server does
  not know which of its names a caller used, so the Console composes the absolute one
  from the browser's origin — correct for a worker Atlas supervises on the same host, and
  something an author must adjust for a worker that reaches Atlas under another name. And
  if a mock ever needs to be reachable from outside the network Atlas sits in, that is
  the standalone process, not a flag here.

  The five operations are also MCP tools, which the repository's route/tool drift test
  required a decision on either way: an agent that authored a REST task standing up the
  API it calls is the same act as a person doing it in the Console, so omitting them
  would have needed a reason there is not one for.

## Pros and cons of the options

### Option 1 — serve it from the engine (chosen)
- Good: no port, no process, no file, no restart; one click, which is the requirement.
- Good: the journal can be kept behind the login, which the standalone mock cannot do.
- Bad: documents occupy the engine's memory, and the engine's address grows a public
  route.

### Option 2 — supervise `atlas mock-openapi`
- Good: keeps every byte of serving out of the engine, and reuses the supervisor.
- Bad: a port per mock to allocate and reach, a child to restart, and a document Atlas
  must write to disk for the child to read — three moving parts where option 1 has
  none, for a dev aid that cannot block the engine anyway.

### Option 3 — adopt an external mock server
- Good: Mokapi is Go rather than Node, so ADR-0217's objection to Prism (a Node runtime
  beside a single binary, ADR-0011) does not apply to it; it is MIT, and it mocks
  Kafka, MQTT, LDAP and SMTP as well as HTTP, which `openapimock` does not.
- Bad: it answers the question ADR-0217 already answered, and answers it outside the
  binary — a second artifact to ship, version and supervise, for the protocols where
  Atlas already has its own mockup (AD/LDAP via ADR-0181, mail via ADR-0150's preview
  provider, SQL via ADR-0221, SharePoint via ADR-0484). None of it reports into
  ADR-0216's view without an adapter per protocol.
- Bad: it does not deliver the click either. "Upload a document and Atlas serves it"
  is the missing piece, and it is missing with or without a third-party server behind
  it — so this option is cost without the benefit asked for.

### Option 4 — leave it standalone
- Good: nothing to build.
- Bad: it is ADR-0193's finding verbatim — the switch that exists to make trying a
  process out cheap is unavailable to the person trying it out.

## Links

- completes the Console-managed follow-up of [ADR-0217](0217-openapi-mock-server.md)
- follows [ADR-0193](0193-ad-mock-in-the-console.md) (the switch belongs in the Console) and [ADR-0202](0202-atlas-manages-the-ad-mock-seed.md) (Atlas holds the content, not a path)
- reports in the envelope of [ADR-0216](0216-mockups-are-one-view.md), beside [ADR-0484](0484-sharepoint-mock-mode.md)
- argues with [ADR-0164](0164-no-in-process-service-tasks.md) rather than citing it, on the precedent ADR-0484 set
- keeps [ADR-0181](0181-ad-connector-mock-mode.md)'s rule that the model does not change
- a new area service under [ADR-0147](0147-splitting-the-api-server-object.md)
