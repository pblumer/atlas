# ADR-0090: Bulk-terminate running instances — an explicit selection and a filtered scope

- **Status:** Accepted
- **Date:** 2026-07-30
- **Deciders:** Atlas engine team

## Context and problem statement

Before this decision an operator could terminate running instances two ways, and only
two:

- **`DELETE /api/v1/instances/{key}`** — one instance at a time, for a single stuck
  instance (e.g. one parked on a wait that will never complete).
- **`POST /api/v1/processes/{key}/cancel-instances`** — the per-definition drain: it
  terminates *every* running instance of one definition in bounded batches (repeat while
  `remaining=true`), the coarse "clear this flooded definition" path (ADR-0075 flood,
  survived by ADR-0080/0083).

Neither lets an operator terminate a **chosen subset**: the handful of rows they ticked
in the instances list, or "every running instance whose `customerType=Business`". Under
the reported `/employees` flood the operator often wants exactly that — surgically clear
a matching subset without draining the whole definition, or terminate the specific
instances they identified in the variable search. Doing it as a client-side loop of
single `DELETE`s is slow and non-atomic, and — critically — must not require scanning
the entire active set per key, which the flood makes expensive on the single-writer
loop.

## Decision drivers

- **Explicit selection *and* a filtered scope:** terminate a hand-picked key set, or a
  definition's instances matching a variable query — the two shapes the instances list
  and the variable search already produce.
- **Cheap for a hand-picked set, even under a flood:** verifying a ticked instance must
  not scan every active instance (I1-flavored: prefer point reads).
- **Bounded, atomic turn (I3, single writer):** all terminations for one call happen in
  a single run-loop turn, atomic with the read that selected them; the turn must be
  bounded so one call can't monopolize the loop.
- **Reuse, don't fork:** reuse the existing `CancelInstance` path and the instances-
  search matcher rather than inventing a second termination mechanism.

## Considered options

1. **Extend `cancel-instances`** with an optional `keys`/`q` filter on the existing
   per-definition endpoint.
2. **A new `POST /api/v1/instances/terminate`** with two mutually exclusive modes:
   an explicit key set, or a definition + optional query.
3. **Client-side loop** of `DELETE /instances/{key}` over the selected keys.

## Decision outcome

Chosen option: **"a new `POST /api/v1/instances/terminate` with two modes"**, because
the selection is not always scoped to one definition (an explicit key set can span
definitions, and reads most naturally as its own verb), and folding it into
`cancel-instances` — which is defined as *the whole definition* — would overload that
endpoint's contract. Body:

- **`{keys:[…]}` — explicit set.** Each key is verified with an O(1) point lookup
  (`ProcessInstance`), not a scan over the active set, so a hand-picked terminate stays
  cheap under a flood. Only a record in the active keyspace (`State == PIActive`) is
  terminable; a key found only in history — already finished, or never valid — is
  reported as `notFound` rather than failing the call. Duplicate keys collapse. The
  request-body limit bounds the batch (a few thousand keys), which keeps the turn
  bounded.
- **`{processDefKey, q?, limit?}` — filtered scope.** Scans the active instances of one
  definition and terminates those matching the optional variable query (the *same*
  matcher as `GET /api/v1/instances/search`; a blank query matches all of the
  definition's active instances), drained in bounded batches like `cancel-instances`
  (reports `remaining=true` when the per-call cap was hit → repeat).

The two modes are mutually exclusive (specifying both is a 400). All terminations for a
call run in one `s.do` turn — `CancelInstance` per selected instance, then one `Drive` —
so they are atomic with the scan/lookups that selected them. The response is
`{terminated, notFound, remaining, stats}`.

Single-instance `DELETE /instances/{key}` and the per-definition `cancel-instances`
drain **remain** for their narrower uses; the operations UI wires the new endpoint into
a **Select** mode over the live "All instances" list (per-card checkboxes, an "All
active" whole-version scope) and keeps a coarse per-process **"Terminate all running"**
on the overview. A confirmation modal scales friction to blast radius: above 50
instances it requires typing the exact count, so a large, irreversible terminate can't
be a single click.

### Consequences

- **Positive:** an operator can surgically clear a chosen subset — a ticked set or a
  variable-matched scope — without draining a whole definition; the explicit-keys path
  is O(selected) point reads, so it stays cheap even when the definition holds hundreds
  of thousands of instances; the per-call cap and single-turn execution keep the
  single-writer loop unblocked and the terminations atomic with the read.
- **Negative / trade-offs accepted:** three operator termination paths now coexist
  (single `DELETE`, this endpoint, and the per-definition `cancel-instances` drain);
  the explicit-keys batch is bounded by the request-body size rather than an explicit
  cap; filter mode still scans that definition's active set, exactly as the variable
  search does (no value index yet).
- **Follow-ups / risks to watch:** the overview's "Terminate all running" and the MCP
  bulk-cancel tool still call `cancel-instances`; they could migrate onto the unified
  terminate endpoint. The explicit-keys path relies on active-keyspace membership
  meaning `PIActive` — the same assumption the instances list encodes.

## Links

- builds on the same `CancelInstance` termination path as `DELETE /api/v1/instances/{key}`
  and the `POST /api/v1/processes/{key}/cancel-instances` drain; reuses the
  `GET /api/v1/instances/search` variable matcher for filter mode.
- complements the ADR-0075 flood family: ADR-0080/0083 (survive — O(1) runtime and
  summary), ADR-0082 (prevent runaway starts), ADR-0085 (prevent-standing via TTL); this
  is the operator's **drain-a-subset** surface alongside the whole-definition drain.
- honors I3 (one run-loop turn per call, single writer) and I2 (durable before visible —
  terminations commit through the normal event path, one `Drive`).
