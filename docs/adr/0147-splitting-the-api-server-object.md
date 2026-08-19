# ADR-0147: Splitting the api Server object, without weakening the single writer

- **Status:** Accepted (amended 2026-08-19: pilot area corrected, and the API-kernel prerequisite added after measuring it; accepted once the pilot shipped)
- **Date:** 2026-08-19
- **Deciders:** Atlas engine team

## Context and problem statement

After ADR-0019/0021 and everything layered on them, `api` is the largest package
in the repository: 18,118 non-test lines after the structural pass that moved
`layout`, `collab`, `vault`, `sidecar` and the CSV connector out of it. What
remains is one object:

```
type Server struct   — 260 lines, ~75 fields
methods on *Server   — 242, of which 150 are HTTP handlers
```

The fields are the entire server: engine handles (`proc`, `store`,
`deployments`), fifteen design-time stores, authentication (`sessions`, `vault`,
`internalToken`), the collaboration registry, the DMN resolver/validator/registry,
seven connector registries, the inbound bridge, the OpenSearch exporter, the
retention sweeper, checkpointing, metrics. Every one of the 150 handlers can
reach all of it.

The structural pass stopped here for a reason that is not stylistic. A handler is
`func (s *Server) handleX(w, r)`; a method can only live in the package that
declares its receiver's type. Moving handlers out is therefore not a file move at
all — it requires giving each one a narrower receiver, which is a change to how
the server is wired rather than to where its code sits. That is a different class
of change and deserves its own decision.

Two facts found while scoping it shape the answer, and both cut against the
"god object" reading of the struct:

1. **The coupling is shallow.** Counting which `Server` fields each file actually
   touches: only four (`projects`, `dmnrefs`, `users`, `deployments`) are used by
   more than six files. Most handler files touch four to eight. The struct is
   wide; the individual dependencies are not. `handlers.go` is the outlier at 17,
   and it is a grab bag rather than a subsystem.

2. **The two seams that matter are already single points.** `do` — the run-loop
   dispatch that enforces the single-writer invariant — is nine lines and depends
   on exactly two fields (`tasks`, `quit`). And every `/api/v1` route is
   registered from one table, `apiRoutes()`, which pairs each handler with its
   OpenAPI description and is guarded against drift by
   `TestOpenAPICoversEveryRoute` (ADR-0043).

So the question is not "how do we tame 242 methods" but "what does a handler hang
off instead of `*Server`, such that the single-writer invariant and the
route/OpenAPI single source of truth both survive the change".

## Decision drivers

- **Invariant 3 must come out stronger, not weaker.** Today `do` is the only door
  to processor and store state from a concurrent HTTP goroutine, and it holds
  because it is a private method on the one object handlers have. Any split that
  makes it possible for a handler to reach state *without* going through the run
  loop trades a structural guarantee for a convention, and would be a bad trade
  at any size of diff. Note the failure mode: such a bug does not fail a test, it
  corrupts state under concurrency.
- **The route table stays the single source of truth.** A served route that no
  OpenAPI operation describes, or the reverse, is exactly what ADR-0043 set out
  to make impossible.
- **Authentication must not be duplicated.** `principalFor` and `requireAdmin`
  are reachable from every handler today. Re-implementing them per area is how a
  scope check goes missing on one route.
- **Reviewable in slices.** 150 handlers in one diff is not reviewable, and a
  mistake at the run-loop boundary would be invisible inside it.
- **No behavior change.** Same routes, same status codes, same error strings.

## Considered options

1. **Leave it.** Accept the wide struct; keep adding to it.
2. **Per-area services in sub-packages**, each holding only the dependencies it
   uses, with the run-loop handle and the auth guard passed in as explicit
   collaborators. `Server` becomes the composition root that builds them and
   concatenates their route tables.
3. **Narrow interfaces on the existing methods** — keep every handler on
   `*Server`, but have each take an interface parameter describing what it needs.
4. **A dependency-injection framework** (wire, fx, or similar).

## Decision outcome

Chosen option: **"Per-area services in sub-packages"**, introduced one area at a
time, starting with a pilot that has to prove the pattern before the rest
follows.

The shape:

- **`do` becomes an explicit type**, not a method on the composition root — a
  small handle carrying the `tasks` and `quit` channels, with one exported method
  that runs a closure on the run-loop goroutine. Every service holds one. This is
  the load-bearing part of the decision and it *strengthens* invariant 3: today
  the seam is a private method that happens to be the only path; afterwards it is
  a named type that a service cannot do its work without, whose sole purpose is
  that guarantee, and which can be tested directly for it.
- **Each service owns its routes.** A service exposes `routes() []apiRoute` —
  handler and OpenAPI operation still paired in one literal — and `apiRoutes()`
  concatenates. The drift test keeps working unchanged, because it iterates the
  concatenation.
- **Authentication is one shared collaborator**, passed to every service, not
  re-implemented per area.
- **`Server` keeps ownership of the process lifecycle** — the run loop itself,
  startup, shutdown, the background sweepers. It stops being the receiver every
  handler hangs off.

**The pilot is process documentation** (`processdocs.go`): 12 methods, 9 of them
handlers, 9 `s.do` call sites, two stores no other area touches, one startup
hook and one unauthenticated public route (ADR-0029 share token). It shares
exactly one field with the rest of the server (`publicRate`).

Whether the remaining areas follow is a decision to take **after** the pilot, on
the evidence it produces, not now. If the pattern costs more indirection than it
removes, one converted area is a cheap thing to revert.

### Sequencing — what scoping the pilot revealed

An earlier draft of this ADR named projects/releases/promote as the pilot. That
was wrong, and measuring it is what showed why: `projects` is read by ten files,
which makes it one of the *most* entangled areas rather than one of the least.
Ranking every candidate by how many of its fields no other file touches puts
marketplace, process documentation and deploy tokens at the top; projects near
the bottom.

The same measurement turned up a prerequisite the first draft missed. A service
in its own package cannot reach the package-level helpers its handlers are
written against, and for process documentation — the *least* entangled real
candidate — that is still twelve identifiers: `writeJSON` / `writeError`,
`clientIP`, `principalFrom`, `newPublicToken`, the `deployment` type, and its own
store. The response helpers alone have **605 call sites across 34 files**.

So the work is three steps, not two:

1. **Give the single-writer boundary a name** — `api/runloop`. Done; it is what
   makes a service able to hold the invariant without inheriting it.
2. **Extract the API kernel** — the response helpers, `clientIP`, the principal
   accessor and the `apiRoute`/`apiOp` types — into a package that both `api` and
   any future service can import.
3. **Carve out the first service.**

Step 2 is wide (605 mechanical call sites) and delivers nothing visible on its
own: its entire payoff is unlocking step 3. That cost is not a reason to abandon
the direction, but it is a real number that was not visible when this decision
was first written down, and it belongs here rather than in a surprised commit
message. It also means step 3 cannot honestly be called cheap to revert once
step 2 has landed — step 2 is the commitment.

### Consequences

- **Positive:** the single-writer seam becomes a named, testable type instead of
  a private method whose discipline is a convention. A handler's dependencies
  become readable from its receiver rather than by grepping a 260-line struct.
  New areas get a place to live that is not "append to `Server`".
- **Positive:** the route/OpenAPI pairing survives untouched, because services
  contribute to the same table rather than registering with the mux themselves.
- **Negative / trade-offs accepted:** more indirection. A service that needs
  something another area owns has to be given it explicitly, and some handlers
  genuinely straddle areas (`projects.go` touches drafts, dmnrefs, releases and
  users). Those will need a judgement call each — the honest expectation is that
  a few handlers stay on `Server` rather than being forced into an area.
- **Negative:** during the transition the codebase has two shapes at once. That
  is the price of slices, and it is cheaper than an unreviewable single diff.
- **Follow-ups / risks to watch:** the real risk is a handler that ends up
  touching state outside `do` because the refactor made it possible. Every slice
  must be read specifically for that, and the run-loop handle should be the only
  way a service reaches the processor or a store. `handlers.go` (17 fields) is
  the hardest file and should be tackled last, not first — by then the areas
  around it will have drained much of it.

### What the pilot showed

All three steps have shipped: `api/runloop`, then `api/httpapi` (plus `api/token`,
which the pilot needed and public links already wanted), then `api/processdoc`.

Two things came out differently from the plan, both in the direction of less
machinery:

- **The route table did not need splitting.** The plan had each service exposing
  `routes() []apiRoute`, which meant moving the `apiRoute`/`apiOp` types and the
  OpenAPI schema helpers into the kernel. In the event the table stays whole in
  `api/openapi.go` and simply points at the service's exported handler methods.
  That is *more* faithful to ADR-0043 — the single source of truth stays literally
  a single table — and it kept the kernel to what handlers genuinely share.
- **The auth guard was not needed.** The area has no per-handler role check, so
  nothing forced the question of how `requireAdmin` travels. It is still open, and
  the next area that needs it decides it.

The payoff is concrete and was not available before. The area's behavior is now
tested against the service directly — no server, no mux, no route table — and
those tests cover 97.6% of the package. Seven tests that previously reached the
handlers' error branches by breaking a whole server's store were deleted as
duplicates at a worse altitude; one remains in `api`, the one that proves the
collaborators the server supplies are actually connected. Repo-wide coverage went
up, not down.

The cost is what was predicted: a wide, dull kernel commit, and three
collaborators the server now has to hand over explicitly.

**On whether the rest follows:** the pattern holds and the next area can use it.
It should stay opportunistic rather than becoming a migration — an area is worth
converting when it is being worked on anyway, and `handlers.go` (17 fields, and a
grab bag rather than a subsystem) should be last, by which point the areas around
it will have drained much of it.

## Pros and cons of the options

### Option 1 — leave it
- Good: no risk, no churn, no transitional inconsistency.
- Bad: the struct grows by a field per feature and the file count by a handler
  per route. It is already the reason the previous structural pass had to stop,
  and nothing about it improves on its own.

### Option 2 — per-area services (chosen)
- Good: makes each handler's real dependencies explicit; gives the single-writer
  seam a name; preserves the route table and its drift test; can be done and
  judged one area at a time.
- Bad: indirection; cross-area handlers need case-by-case decisions; two shapes
  coexist while it is in progress.

### Option 3 — narrow interfaces on existing methods
- Good: the smallest possible change; no packages move.
- Bad: it does not actually solve anything. The handlers stay in one package, so
  the file count and the compile unit are unchanged, and `Server` still has to
  satisfy every interface — the wide struct survives with extra ceremony on top.

### Option 4 — a DI framework
- Good: mechanical wiring, no hand-written composition root.
- Bad: a dependency and a code-generation step for a single binary with one
  composition root that is a few hundred lines of plain Go (ADR-0011). The
  problem here is which dependencies a handler *has*, and a framework does not
  answer that — it only automates writing them down once decided.

## Links

- builds on ADR-0002 (single-writer partition model) — the invariant this must
  not weaken
- builds on ADR-0043 (OpenAPI spec and embedded API explorer) — the route table
  that must stay a single source of truth
- relates to ADR-0011 (single-binary distribution) — why option 4 is refused
- relates to ADR-0044 (user management and authentication boundary) — the auth
  guard that becomes a shared collaborator
