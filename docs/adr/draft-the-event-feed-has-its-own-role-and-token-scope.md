# ADR-DRAFT: The event feed has its own role and its own token scope

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-10-02
- **Deciders:** Atlas maintainers

## Context and problem statement

ADR-0429 §5 made the catalogue's facts leave Atlas as a feed: every action outcome, every
right granted and every right revoked, as CloudEvents at `GET /api/v1/events`. It required
`operator` in the first cut and named "a role scoped to the feed alone (ADR-0209) and scoped
tokens (ADR-0194)" as the follow-up. The feed's reader is, in nearly every case, a machine on
another host — a CMDB, a billing system — holding a credential for as long as the integration
runs.

What that machine could be given before this record:

- **An API token's roles were always its minter's.** `tokenRoles` gives an administrator's
  token the whole non-admin set, `modeler` + `operator` + `user`, whatever its scope
  (ADR-0209, "Credentials that are not sessions"). Minting is admin-only, so that is every
  token.
- **The `full` scope reaches everything a non-admin reaches.** A `full` token holding
  `operator` could read the feed — and deploy a model (code execution), start, cancel and
  terminate instances, and read every instance's variables.
- **No confined scope named the feed.** The narrow scopes (`worker`, `metrics`, `status`,
  `directory`, `inventory`, `landscape`) each name their own routes; none named
  `/api/v1/events`.

So the only credential a CMDB could hold to read who holds what was one that could also
deploy code. Rows of the feed name people by id only, but the feed as a whole is a map of
every right every person holds across every catalogue — not something an operator role, built
for starting and repairing instances, implies.

## Decision drivers

- **A leaked feed credential leaks the feed and nothing else.** It must not deploy, start or
  read instances, or act on orders.
- **Both locks name the same door.** ADR-0209 enforces two checks on every machine
  credential — the scope says which routes, the role says which kind of act. A credential
  that passes one only because the other is broad is confined by one lock, not two.
- **No new authorization mechanism.** The route table names one role per route
  (`apiOp.role`), and scopes are fail-closed allowlists (ADR-0194). Whatever is built uses
  those, not a third thing.
- **Nothing that works today stops working.** Existing accounts, existing tokens and the
  legacy upgrade keep exactly what they hold.

## Considered options

1. **A role `feedreader`, the route requires it, and a mintable scope `events` whose token
   carries that role and no other.**
2. **The route stays `operator`; add only a scope `events`.** The token keeps its minter's
   roles, the scope confines it to the feed.
3. **Let a route name several roles** (`operator` *or* `feedreader`), so operators keep the
   feed and a feed reader gets it too.
4. **Tokens carry an explicit list of roles chosen at mint**, in place of scopes — the merger
   ADR-0194 names as a follow-up.

## Decision outcome

Chosen: **option 1.**

**The role.** `feedreader` (`api/userstore.go`) reads the event feed and nothing else. It is a
route role and a grantable role. It is offered in the Console's account form and in the
sign-on mapping editor, so a person who integrates a CMDB can be given it to look at the feed
by hand, and a directory-backed installation can hold it past the next sign-in. Like
`productmanager` it is **never** carried over by the legacy upgrade: an upgrade that handed
every existing account the map of who holds what would be widening, the one direction
ADR-0209 does not take.

**The route.** `GET /api/v1/events` requires `feedreader`. An administrator reaches it as it
reaches everything (ADR-0209). An `operator` no longer does, unless also given `feedreader`.
The feed shipped in the same unreleased version, so no installed integration loses it.

**The scope.** `events` (`api/apitokenscope.go`) is mintable and reaches exactly
`GET /api/v1/events`. An `events` token **carries `feedreader` and no other role**, whoever
mints it (`scopeRoles`, `tokenRolesFor` in `api/routeroles.go`). The scope says the feed is
the only route, the role says reading the feed is the only act, and both checks name the
feed. The rule that a token is never more than its minter still holds: a minter who is not
an administrator and does not hold `feedreader` is refused with 403. A `reach` is refused on
an `events` token, because the feed is one stream and a reach nothing reads would be a
narrowing the holder believes in and nothing enforces.

**The other scopes are unchanged.** A scope absent from `scopeRoles` carries the minter's
roles, as before. In particular a `full` token does **not** read the feed, because its
minter's non-admin set does not include `feedreader`. That is deliberate: a CI job's token is
not a CMDB's.

**The listing shows a token's roles.** `GET /api/v1/api-tokens` now answers each token's
`roles` beside its `scope` and `reach`, so an administrator can see that an `events` token
holds `feedreader` and nothing more.

**MCP.** The feed stays outside MCP (ADR-0429 §5): it is a machine-to-machine feed with a
cursor the consumer keeps. Token minting and user administration are admin routes and were
never MCP tools.

### Why not the others

**Option 2** confines the route but not the act. The token would still hold `modeler` and
`operator`, and the day a pattern is added to the `events` allowlist by mistake, or a scope
check regresses, the token deploys. ADR-0209 has two locks so that one failing is not the
whole of the protection. Option 2 has one lock.

**Option 3** is the strongest argument against option 1: an operator who debugs an
integration wants to look at the feed without a second role, and "operator *or* feedreader"
would let them. But the route table's single role is what makes every route's authority
readable in one line and testable by walking the table (`TestEveryRouteDeclaresAKnownRole`).
Several roles per route is a change to the access model for all routes, made for one.
Granting the operator `feedreader` costs one checkbox and says in the account what it does.
The multi-role checks that exist all live inside handlers and all have the form "operator or
admin", which is the admin superset the boundary already applies.

**Option 4** is where ADR-0194 says scopes and roles should end up, and this record does not
close that door. But a mint-time role list for every token makes every administrator design
an authorization model at the moment of minting — the objection ADR-0194 raised against
choosing routes at mint ("a permission system in disguise"). A scope that fixes its roles is
a reviewed, named set, which is what makes it safe to hand out.

### Consequences

- **Positive:** A CMDB or billing system can hold a credential that reads the feed and can do
  nothing else. It is bounded in time, revocable, attributable in the audit trail, and both
  of its checks name the feed. An operator role no longer implies the map of every person's
  rights. Token listings show what each token may do, not only where.
- **Negative / trade-offs accepted:**
  - An operator who used the feed in the unreleased first cut needs `feedreader` as well.
  - A `full` token cannot read the feed. An integration that wanted one credential for
    everything needs two.
  - The feed is not narrowed by catalogue. An `events` token reads every catalogue's facts.
- **Follow-ups / risks to watch:**
  - **Narrowing by catalogue.** A billing system for one catalogue would want only that
    catalogue's rows, and the subject already carries the order position to filter on. That
    is a reach of catalogues, not of projects, and it needs the feed rows to carry their
    catalogue.
  - **Push delivery.** The Worker that pushes the feed (ADR-0429 §5, prepared) should run
    under this role.
  - **The role-carrying scope** is the first step toward the merger ADR-0194 names. If a
    second scope needs roles of its own, `scopeRoles` is where it goes, and this record is the
    precedent.

## Links

- [ADR-0429](0429-product-actions-are-commands-with-published-outcomes.md) §5 — the feed,
  and the follow-up this record builds.
- [ADR-0209](0209-roles-per-endpoint-group.md) — roles per route, and what a token carries.
- [ADR-0194](0194-api-tokens.md) — API tokens and scopes as fail-closed allowlists.
- [ADR-0315](0315-portal-roles-and-responsibilities.md) — `productmanager`, the precedent for
  a granted role the upgrade never carries over.
