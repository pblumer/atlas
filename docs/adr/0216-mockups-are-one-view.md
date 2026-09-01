# ADR-0216: Mockups are one view, not one per kind

- **Status:** Proposed
- **Date:** 2026-09-01
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0213](0213-ad-mock-directory-in-the-console.md) gave the Active Directory mockup a
view: Operations › Mock directory, one card per worker, a containment tree per LDAP
URL. It answers the question that prompted it — *I added a user, where is it?* — and it
answers it only for AD.

The route is `/api/v1/ad/mock-directory`, the payload is forests and DNs, the store is
`ad.MockView`, and the Console renderer knows what a DN tree looks like. Nothing of that
serves the next mockup. Meanwhile the same question is waiting to be asked of every
other integration a process touches: *the ticket my process created — is it there? what
does it say?* And a whole page spent on one connector is hard to justify when the same
operator has a Jira task and a Postgres task in the same model.

Atlas already has three mockups and they live in three different places:

- **mail preview** (ADR-0150) — a *provider* on a connector record, needing no
  credential, delivering into an in-server outbox with its own Operations view.
- **AD mockup** (ADR-0181/0193) — an org-wide *switch*, because an AD task names its own
  URL rather than a connector record, held in a worker's memory and reported back.
- **the Remedy mock server** (`atlas mock-remedy`) — a separate process an operator runs
  and points a connector at.

Three shapes, three answers to "where do I look". A fourth mockup would be a fourth.

## Decision drivers

- **One place to look.** An operator trying a model out wants to see what it did, not to
  learn which of four views this particular connector reports into.
- **The shapes are genuinely different.** A forest is a tree of DNs; an issue tracker is
  records with fields; a database is tables with rows. Flattening them into one generic
  rendering makes every one of them worse.
- **Where a mock lives differs, and must not leak into the view.** AD is worker-only
  (ADR-0206), so its mock is in another process and has to be reported. Jira runs *in
  the engine* (its handler is registered on the server), so its mock is right there and
  needs no transport at all. Postgres is worker-only again.
- **A mock earns its keep by refusing what the real system refuses** (ADR-0181). That is
  the expensive part of a new mockup, and it is the part a view cannot help with.
- **Workers and servers are deployed separately.** A worker running an older binary must
  keep reporting somewhere the current server accepts.

## Considered options

1. **One view, kind-agnostic transport, per-kind renderers.** A single store of "mock
   reports" keyed by kind and reporter; the Console picks a renderer by kind and falls
   back to a generic one.
2. **One view, one universal payload.** Every mock reports sections of key/value rows;
   one renderer draws them all.
3. **A view per kind.** AD keeps its page, Jira gets its own, Postgres gets a third.
4. **Leave it at AD.** Other mockups keep answering through logs and job results.

## Decision outcome

Chosen option: **"one view, kind-agnostic transport, per-kind renderers"**.

The server stores an envelope it fully understands and a payload it does not:

```go
type MockReport struct {
    Kind    string // "ad", "jira", "postgres" — the Worker Type this mocks
    Source  string // the worker id that holds it, or "engine" for an in-process mock
    Target  string // which configured thing is mocked: a connector name, or "" org-wide
    At      int64  // stamped on arrival, never by the reporter
    Summary string // the one line a folded card shows
    Data    json.RawMessage // the kind's own shape
}
```

- `POST /api/v1/mocks` accepts one report (worker token scope, as the outbox and the AD
  report do today). `GET /api/v1/mocks` serves them all, admin-gated, for the reason the
  AD seed's *content* is admin-gated (ADR-0202): invented data shaped like real data.
- **An in-process mock posts nothing.** The engine holds it already, so `GET` collects it
  from the registry at read time — no transport, no staleness, no second copy. That is
  the asymmetry this design exists to hide: Jira and mail are in the engine, AD and
  Postgres are in a worker, and the Console sees one list either way.
- **The server never interprets `Data`.** It bounds it and serves it back. A kind's shape
  is between the thing that produces it and the renderer that draws it, which is what
  keeps adding a mockup out of the server.
- **The Console keeps a small map** `kind → renderer`, with a generic renderer (a titled
  list of key/value tables) for a kind it does not know. A new mockup is therefore
  *visible* the day it reports, and *well drawn* when somebody writes its twenty lines.
- Operations › **Mock directory** becomes Operations › **Mockups**, cards folded the way
  they already fold. AD's renderer moves across unchanged.
- **Mail keeps its own view.** A preview message is a rendered HTML body and its RFC 5322
  source; that is not a card in a list, and cramming it in would make the outbox worse.
  Mockups links to it and says how many messages are waiting, so the "what is simulated
  right now" question still has one answer.

### Jira is the second mocker, and it is the reason to do this now

Designed against the real connector rather than in the abstract:

- `jira.Client` is one method — `Do(ctx, Request) (any, error)` — so a mock is a drop-in
  implementation, exactly as `ad.Dialer` was.
- It is switched on **per connector record**, as a provider of its own (`mock`), the way
  the preview mail provider is: a Jira connector names one instance, so mocking belongs
  to that record and not to an org-wide switch. AD's switch is org-wide only because an
  AD task names its own URL and there is no record to hang it on.
- It must refuse what Jira refuses, or it teaches a model to be wrong: an unknown project
  or issue type on create, an unknown key on read, a transition that is not available
  from the issue's current status, a create with no summary, an assignee nobody knows.
  JQL gets the mock filter's rule — support a stated subset, and **refuse** what is
  outside it rather than quietly matching everything.
- What it never does: pretend to be a workflow engine. A transition table small enough to
  state (To Do → In Progress → Done, plus a reopen) is a mockup; anything more is Jira.
- Its card: one section per project, issues as records (key, type, status, assignee,
  summary), and the operation journal underneath — the same journal AD's card carries,
  because "what did the run actually do" is the half a state view cannot answer.

### Consequences

- **Positive:** One page answers "what did my mockup run do" for every kind. A new mockup
  costs a client and a renderer, not a route, a store and a view. AD's page stops being a
  page about one connector.
- **Negative / trade-offs accepted:** The payload is opaque to the server, so a
  malformed report shows as a broken card rather than being refused at the boundary — the
  price of not teaching the server every kind's shape. The envelope is not opaque, so who
  reported, when, and how much is still guarded. A generic renderer will look plainer
  than a written one; that is the honest signal that nobody has written one yet.
- **Compatibility:** `/api/v1/ad/mock-directory` stays as an alias that files a report
  under kind `ad`, because a worker on an older binary posts there and a supervised
  worker is restarted by the server, not with it. It is removed a release later, not in
  this change.
- **Follow-ups / risks to watch:** The real cost of the next mockup is the client that
  refuses correctly, not the card — the Jira mock is most of this work, and the Postgres
  one (constraints, types, a sane subset of SQL) is more. Do not let the view's existence
  become an argument for a mock that accepts everything.

## Pros and cons of the options

### Option 1 — kind-agnostic transport, per-kind renderers
- Good: one view, and each kind still drawn in its own shape.
- Good: the server learns nothing per kind; a mockup ships without touching it.
- Bad: an opaque payload cannot be validated at the boundary.

### Option 2 — one universal payload
- Good: one renderer, nothing to write per kind.
- Bad: a DN tree flattened into key/value rows is worse than what AD has today, and the
  first thing anybody would ask for is a tree — arriving at option 1 by a longer road.

### Option 3 — a view per kind
- Good: every view is exactly right for its kind.
- Bad: the nav grows one entry per mockable connector, and the operator has to know which
  page a connector reports into before they can look.

### Option 4 — leave it at AD
- Good: nothing to build.
- Bad: the question AD's view answers is not an AD question. Every other mockup keeps
  answering it with a log line, which is where this started.

## Links

- extends [ADR-0213](0213-ad-mock-directory-in-the-console.md) (the AD mock directory in the Console)
- follows [ADR-0150](0150-preview-mail-provider-and-visible-incidents.md) (a mockup as a provider, reporting into the server's own view)
- builds on [ADR-0181](0181-ad-connector-mock-mode.md) (a mock is faithful where being faithful is the point)
- relates to [ADR-0168](0168-connector-work-on-a-worker.md) (why a worker reports rather than being asked)
- relates to [ADR-0201](0201-jira-connector.md) (the Jira connector this mocks)
