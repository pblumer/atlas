# ADR-0493: A diagrams scope for systems that show processes elsewhere

- **Status:** Proposed
- **Implementation:** Partial
- **Date:** 2026-10-26
- **Deciders:** requested 2026-10-26 by the maintainer, who wants wikiwikibangbang (github.com/dbuchs7/wikiwikibangbang) to show Atlas processes in its pages in their newest version, with their live counts when its administrator allows them, and asked for a narrow token on the Atlas side rather than a `full` one; proposed for the maintainers' acceptance
- **Open question:** `GET /api/v1/processes/{key}/runtime` answers the overlay's counts *and* a page of incident details — the message, the instance and job keys, the Worker. A system that shows counts only (the wiki drops the rest) still receives them, so a leaked `diagrams` token reads incident messages, which can carry business data. Should the scope reach a counts-only form of the overlay instead (a query parameter the handler honours for this scope, or a route of its own), so that its reach matches what such a system needs?
- **Question checked:** 2026-10

## Context and problem statement

A wiki that documents business processes wants to draw them as Atlas runs them: the newest deployed version of a process, read whenever its page is shown, and, where its administrator allows it, the overlay of where the instances are. It reads three routes, all of role `any`: `GET /api/v1/processes` (to find the newest version of a process id), `GET /api/v1/processes/{key}/xml` and `GET /api/v1/processes/{key}/runtime`.

The only mintable scope that reaches them is `full` (ADR-0194): everything a signed-in person without `admin` reaches, which for an administrator's token includes deploying, starting and cancelling instances, reading variables and moving definitions. The wiki holds its credential on another server, in its environment, for as long as the integration lives. "What else could this do if it leaked" has a long answer.

Deployed definitions are readable by every signed-in caller (ADR-0447 §5, `definitionReadable`), so the scope does not decide *which* processes such a system sees; it decides what else the credential can do.

## Decision drivers

- A long-lived credential on another host reaches what its machine needs and nothing else (ADR-0194).
- The reach of a scope is short enough to read in one glance.
- No new route and no change to what the three routes answer.

## Considered options

1. **A new confined scope `diagrams`** whose allowlist is the three reads.
2. **A `full` token of an account without roles**: roles refuse the writes that ask for `modeler`, `operator` or `admin`.
3. **Widen the `landscape` scope** with the three reads.

## Decision outcome

Chosen option: **1**.

- `apiScopeDiagrams = "diagrams"` is mintable. Its allowlist is `GET /api/v1/processes`, `GET /api/v1/processes/{key}/xml` and `GET /api/v1/processes/{key}/runtime`; every other request, the writes on the same paths included, is refused with 403.
- It carries its minter's roles, as `status` and `landscape` do: every route it reaches asks for the role `any`, so roles cannot widen what the allowlist admits. It is not added to `scopeRoles`, because an empty role set is read as a legacy token's.
- The Console offers it on the API token page between `status` and `events`; the handbook's table of scopes names it, in both languages, with the runtime answer's incident messages said plainly.

### Consequences

- **Positive:** a leaked `diagrams` token reads models and runtime overlays; it cannot deploy, start, cancel, move, redraw or read an instance's variables.
- **Negative / trade-offs accepted:** the runtime overlay comes whole, incident details included (the open question). Every deployed definition is readable, as for any signed-in caller.
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
