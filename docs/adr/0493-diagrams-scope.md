# ADR-0493: A diagrams scope for systems that show processes elsewhere

- **Status:** Proposed (amended 2026-10-26: the scope reaches the overlay's counts, not its incident details; amended 2026-10-27: it reaches a release's architecture sketch as everybody may see it)
- **Implementation:** Partial
- **Date:** 2026-10-26
- **Deciders:** requested 2026-10-26 by the maintainer, who wants wikiwikibangbang (github.com/dbuchs7/wikiwikibangbang) to show Atlas processes in its pages in their newest version, with their live counts when its administrator allows them, and asked for a narrow token on the Atlas side rather than a `full` one; proposed for the maintainers' acceptance. The amendment was asked for by the maintainer on the same day, answering the record's open question.

> **Amendment (2026-10-27) — the scope reaches a release's architecture sketch, as
> everybody may see it.** The maintainer asked for the sketch of ADR-0471 and ADR-0495 in
> the same wiki, inserted into a page as its processes and decisions are. The scope
> gains two reads, both role `any`:
>
> - `GET /api/v1/architectures` lists, per application the caller may view, its releases
>   that carry a snapshot, newest first: version, release id, when it was published and
>   taken, and the layers it records. Not who published a release, nor its note. It is
>   what a picker offers, and how a page that follows the newest release finds it.
> - `GET /api/v1/applications/{id}/releases/{version}/architecture.embed.svg` answers
>   the sketch read as `archsketch.Snapshot.Minimal` reads it, **whoever asks**: a
>   Worker keeps its name and Worker Type and loses its endpoints and the rest of its
>   configuration, instances and catalogue products are counted, another application —
>   called or coupled — is "another application", a feed endpoint is withheld, and the
>   owner of a capability or value stream is withheld, name and role. The release's own
>   application keeps its name. `?layers=` as on the architecture routes.
>
> Why a route of its own, and why minimal for everyone: the architecture routes answer by
> who asks (ADR-0495 §5), and a token carries its minter's roles, so a token an
> administrator minted would hand the wiki an administrator's view, which the wiki then
> shows to every person who can open the page. Reading the caller's scope in the handler
> to narrow it would break the rule this record keeps (the reach of a scope is readable
> in `api/apitokenscope.go` alone; handlers never read a scope). So the scope names a
> route whose answer is the same for every caller, and the architecture routes, the
> release history and the release lens stay out of its reach.
>
> **Which applications** a token sees is its reach (ADR-0410): a machine principal is no
> member of anything, so a `diagrams` token sees the sketches of the applications its
> reach names, and a token minted without a reach — every token minted for processes
> before this amendment — lists none. Processes are unaffected: every signed-in caller
> reads every deployed definition (ADR-0447 §5).

> **Amendment (2026-10-26) — the scope reaches the counts of the overlay, not the
> overlay.** As first written, the scope reached `GET /api/v1/processes/{key}/runtime`,
> which answers the overlay's counts *and* a page of incident details: the message, the
> instance, element-instance and job keys, the Worker and its repair form. A system that
> shows counts only still received them, so a leaked `diagrams` token read incident
> messages, which can carry business data, and could isolate one instance with
> `?instance=`. The record left that open; the maintainer answered it.
>
> - `GET /api/v1/processes/{key}/runtime/counts` answers the overlay's numbers and
>   nothing else: `instances`, `finished`, `tokens`, `elements` (per element: `tokens`,
>   `visits`, `incidents`, `terminated`, with its id and type), `incidentTotal` and
>   `incidentCountsExact`. They come from the same reading as the overlay, so the two
>   cannot disagree. The fields are copied one by one, so a field added to the overlay
>   later reaches this answer only by a decision. It is the whole definition's: an
>   `?instance=` filter is refused (400), not ignored, because isolating an instance is
>   reading it.
> - The scope's allowlist names that route instead of `/runtime`, which a `diagrams`
>   token is now refused (403) — with or without `?instance=`.
> - Of the two forms the open question named, a route of its own was chosen over a
>   query parameter the handler honours for this scope. The allowlist matches patterns,
>   not queries, so the parameter would have needed the handler to know the caller's
>   scope, and the reach of a `diagrams` token would no longer be readable in
>   `api/apitokenscope.go` alone — the property ADR-0194 asks of every scope.
> - The new route is role `any`, like the overlay. It is not an MCP tool: an agent holds
>   no `diagrams` credential and reads the whole overlay through `atlas_process_runtime`.
>
> The decision driver "no new route" below gave way to that property: one read route
> that answers a subset of an existing one is the price of a reach that is a list.
> The consumer reads the counts route; until it does, a wiki holding a `diagrams` token
> is refused the operations view, not handed the messages.

## Context and problem statement

A wiki that documents business processes wants to draw them as Atlas runs them: the newest deployed version of a process, read whenever its page is shown, and, where its administrator allows it, the overlay of where the instances are. It reads three routes, all of role `any`: `GET /api/v1/processes` (to find the newest version of a process id), `GET /api/v1/processes/{key}/xml` and `GET /api/v1/processes/{key}/runtime`.

The only mintable scope that reaches them is `full` (ADR-0194): everything a signed-in person without `admin` reaches, which for an administrator's token includes deploying, starting and cancelling instances, reading variables and moving definitions. The wiki holds its credential on another server, in its environment, for as long as the integration lives. "What else could this do if it leaked" has a long answer.

Deployed definitions are readable by every signed-in caller (ADR-0447 §5, `definitionReadable`), so the scope does not decide *which* processes such a system sees; it decides what else the credential can do.

## Decision drivers

- A long-lived credential on another host reaches what its machine needs and nothing else (ADR-0194).
- The reach of a scope is short enough to read in one glance.
- No new route and no change to what the three routes answer. (Amended: one new read route, the overlay's counts; see the amendment.)

## Considered options

1. **A new confined scope `diagrams`** whose allowlist is the three reads.
2. **A `full` token of an account without roles**: roles refuse the writes that ask for `modeler`, `operator` or `admin`.
3. **Widen the `landscape` scope** with the three reads.

## Decision outcome

Chosen option: **1**.

- `apiScopeDiagrams = "diagrams"` is mintable. Its allowlist is `GET /api/v1/processes`, `GET /api/v1/processes/{key}/xml` and `GET /api/v1/processes/{key}/runtime`; every other request, the writes on the same paths included, is refused with 403. (Amended: `GET /api/v1/processes/{key}/runtime/counts` instead of `/runtime`.)
- It carries its minter's roles, as `status` and `landscape` do: every route it reaches asks for the role `any`, so roles cannot widen what the allowlist admits. It is not added to `scopeRoles`, because an empty role set is read as a legacy token's.
- The Console offers it on the API token page between `status` and `events`; the handbook's table of scopes names it, in both languages, with the runtime answer's incident messages said plainly. (Amended: the handbook now says the scope reads counts and never an incident's message.)

### Consequences

- **Positive:** a leaked `diagrams` token reads models and runtime overlays; it cannot deploy, start, cancel, move, redraw or read an instance's variables. (Amended: it reads the overlays' counts, never an incident's message, key or Worker, and cannot isolate an instance.)
- **Negative / trade-offs accepted:** the runtime overlay comes whole, incident details included (the open question). Every deployed definition is readable, as for any signed-in caller. (Amended: the overlay no longer comes whole; one more read route, the counts, is the trade-off.)
- **Follow-ups / risks to watch:** if definitions become object-gated (ADR-0447 §5), the scope narrows with them, since the routes do the gating. A new read such a system needs is a change to this record, not an addition on the side.

## Pros and cons of the options

### Option 2: a `full` token of an account without roles

Strongest form: no new scope at all; the role check already refuses deploying (`modeler`), operating instances (`operator`) and administration, so what remains is the role-`any` surface.

- Good: nothing to build.
- Bad: the role-`any` surface is large and grows with every release — instance listings and their variables, drafts, applications, the call-activity table — and nobody decides, route by route, that a wiki may read them. It also depends on an account being kept without roles, which is a promise outside the credential.

### Option 3: widen `landscape`

Strongest form: `landscape` is already the scope another system holds to read this server's picture of itself; models are part of that picture.

- Good: no new name to learn.
- Bad: `landscape` must state the applications it may see (ADR-0410) and is meant for another Atlas; a wiki is neither, and a scope whose name no longer describes its contents is one nobody can reason about (ADR-0343).

## Links

- relates to ADR-0194 (scopes), ADR-0209 (route roles), ADR-0447 (definition reads)
- the consuming side: wikiwikibangbang ADR-0129 (BPMN diagrams in the text, processes from a process engine)
