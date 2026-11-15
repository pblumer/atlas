# ADR-DRAFT: A ceiling under every route bounds a request body no handler bounded

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-11-15
- **Deciders:** Atlas engine team
- **Open question:** whether one ceiling is enough. It is the largest budget of the installation, 1 GiB by default, because three routes legitimately take a body that large: the two restores and a Worker Type installation. For every other route that is far more than it should ever read. A ceiling per route class — the large-upload routes on their own, the rest at the largest ordinary budget, tens of megabytes — would be tighter, and needs each route to declare its class where it is mounted.
- **Question checked:** 2026-11

## Context and problem statement

[ADR-0291](0291-one-place-for-budgets.md) gave every bound on externally supplied
input a name in `limits.Limits`, and a completeness test that fails when a bound
appears without one. The test parses the calls that bound a read — `io.LimitReader`,
`io.CopyN`, `http.MaxBytesReader` — and checks the number each is given.

That rule can only see a bound that is written down. A handler that reads `r.Body`
with no bound at all contains none of those calls, so it is invisible to the test:
the audit of 2026-11-13 found nineteen such handlers in `api/` and its sub-packages,
between a hundred neighbours that were bounded. Each accepted a body of any size.
This is the same shape of finding as the one ADR-0291 answered — not a wrong
ceiling, a missing one — one level further down.

Two questions follow. How is a missing bound noticed? And what limits the damage on
the day one is missed anyway?

## Decision drivers

- a handler without a bound must fail the build, not wait for an audit
- no route that takes a large body today may be broken by the answer
- an operator who moves a budget must not have to move a second number with it
- the ceiling must not become the reason a handler's own budget is left out

## Considered options

1. A guard test only: forbid an unbounded body read in `api/`.
2. A guard test, and one ceiling under every route at the largest budget.
3. A guard test, and a ceiling per route class declared where the route is mounted.

## Decision outcome

Chosen option: **"a guard test, and one ceiling under every route"**, because the two
answer the two questions separately and neither is expensive.

- **The rule is the handler's budget.** Every handler reads its body through
  `httpapi.DecodeJSON` or `httpapi.ReadBody` with a budget from `limits.Limits`, or
  through an `io.LimitReader` / `http.MaxBytesReader` over one. A body past its
  budget is answered with `413`. `TestNoBodyIsReadWithoutABound` walks the
  production code of `api/` and fails for `json.NewDecoder(x.Body)` and
  `io.ReadAll(x.Body)`.
- **The ceiling is what stands behind the rule.** `Server.Handler` wraps everything
  it serves in `capBodies`, an `http.MaxBytesReader` at
  `Server.requestBodyCeiling()`: the largest byte budget of the installation, or
  the bound of an installed Worker Type where that is larger, plus one megabyte for
  multipart framing and for handlers that read one byte past their budget to tell
  "too large" from "exactly allowed". It is computed from the configured budgets,
  so it follows whichever an operator raises.

This does not reopen ADR-0291's rejected "one global maximum". That option proposed
one number *instead of* a budget per kind of input, and was rejected because a model
upload and a user record are not the same risk. Here the per-kind budgets stay the
rule and stay enforced; the ceiling is added underneath and is never what a correct
handler is refused by.

### Consequences

- **Positive:** a handler that forgets its bound reads a finite amount, and the build
  says so before it ships. The nineteen handlers are bounded, and the routes that
  carry variables (completing a job or a task) have a budget that fits a Worker's
  answer, `Payload` plus `Request`, not an ordinary command's.
- **Negative / trade-offs accepted:** the ceiling is coarse. At 1 GiB it bounds a
  forgotten handler's read without making that read harmless; it is a last line, and
  the guard test is what keeps it from being the only one. The guard is a text
  match on two call shapes: a body handed to another reader with no bound (a custom
  `Read` loop, a third-party decoder) passes it.
- **Follow-ups / risks to watch:** the open question above. Completions were
  unbounded before; a Worker whose answer is larger than `Payload` plus `Request`
  is now refused and needs `ATLAS_LIMIT_PAYLOAD` raised.

## Pros and cons of the options

### Option 1 — a guard test only
- Good: no new mechanism on the request path.
- Bad: a guard that matches text has gaps, and behind a gap there is nothing.

### Option 2 — a guard test and one ceiling (chosen)
- Good: every request is bounded whatever its handler does; the number follows the
  budgets; no route has to declare anything.
- Bad: one number for all routes is as loose as the largest legitimate upload.

### Option 3 — a ceiling per route class
- Good: tight where it matters: an ordinary route would be held to megabytes.
- Bad: every route has to be classified where it is mounted, and a wrong class
  breaks an upload that works today. Worth doing as its own change, with the route
  table as the place the class is declared.

## Links

- extends [ADR-0291](0291-one-place-for-budgets.md): the budgets it names are what
  the handlers read up to, and its completeness test is the other half of this rule
- relates to [ADR-0018](0018-test-driven-development.md): the guard is a test, and
  the bound on each route was written test-first
