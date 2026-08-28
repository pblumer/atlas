# ADR-DRAFT: Roles per endpoint group

- **Status:** Accepted
- **Date:** 2026-08-28
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0195](0195-auth-on-by-default.md) made a login the default and
[ADR-0199](0199-route-access-classes.md) made every route declare whether it needs
one. Both answer *whether* a caller is somebody. Neither answers *what that
somebody may do*, and Atlas enforces exactly one role: `admin`.

Measured against the route table on `main`:

| | |
|---|---|
| API routes under `/api/v1` | 199 |
| Gated on `admin` | 53 |
| **Reachable by any signed-in account** | **146** |

(Of those 53, 51 are admin outright; two ask a finer question inside the handler —
installing a repository package needs admin only when the package carries code, and
the AD-mockup read returns the seed to an administrator and the switch to everyone.
Both keep their in-handler check, because a route-level role cannot express either.)

Among those 146: `POST /api/v1/deployments`, `POST /api/v1/scripts/run`,
`DELETE /api/v1/instances/{key}`, `POST /api/v1/instances/terminate`,
`POST /api/v1/processes/{key}/cancel-instances`, and every instance's variables.
Deploying a model is code execution (risk R-09 in the ISDS record), so "every
account may deploy" is the wrong default for anything but a workshop.

The sharing work is a different axis and does not cover this. ADR-0071 and
[ADR-0205](0205-connector-ownership-and-event-delivery.md) answer *which object*
a person may touch — this project, that connector. They do not answer *which kind
of operation* a person may perform at all, which is why an account with no
project of its own can still deploy a model and cancel somebody's instances.

There is a second gap, and it is the one that decides how much work this is. An
**API token carries no roles at all** (`api/auth.go`, the token branch of
`principalFor`): its principal has a `Scope` and an empty `Roles`. Today that
reads as "not an admin, everything else", which is why tokens work. Under a rule
that asks each route for a role, a role-less principal reaches *nothing* — so
every worker, CI job and stdio MCP adapter stops the day this lands, unless the
record decides what roles a token carries.

The question: **how does Atlas express what a signed-in identity may do, across
199 routes, without a second authorization vocabulary, without breaking every
installation on upgrade, and without leaving a route that silently defaults to
open?**

## Decision drivers

- **The list must be readable.** ADR-0199's property is the one worth keeping: the
  reach of a credential is provable by reading one list and running one test, not
  by auditing 199 handlers. Anything that scatters the decision across handler
  bodies repeats the mistake `requireAdmin` already made 52 times.
- **Fail closed, by construction.** A route that declares nothing must not be
  reachable. "We forgot to annotate it" has to be a failing test, not an open door.
- **One vocabulary, two axes.** A role says *what kind of operation*; a sharing
  scope says *which object*. Both must pass. Neither may be reimplemented in terms
  of the other.
- **Every credential shape, or none.** A session, an API token, a deploy token and
  an OAuth grant all become a `*Principal` in one function. A rule that only bites
  on sessions is a rule with three ways around it.
- **MCP for free.** ADR-0196 makes a tool call run as its caller, so a role rule
  applied at the boundary covers `/mcp` with no MCP-specific work. That is worth
  asserting with a test rather than assuming.
- **An upgrade is not an outage.** Whatever the new default, an installation that
  upgrades must not find that nobody can deploy until an administrator intervenes.

## Considered options

For **where the role is declared**:

1. **In the route table** (`apiOp` gains a required role), beside the summary and
   tag that already live there — the single source of truth ADR-0043 established.
2. **A per-role allowlist of patterns**, resolved through an `http.ServeMux`, like
   `apiScopeAllowed` does for token scopes and `deployAgentAllowed` before it.
3. **In the handlers**, as `requireRole(...)` calls — what `requireAdmin` does now.
4. **By tag**: each OpenAPI tag maps to a role.

For **the roles themselves**:

- **a.** Four: `admin`, `modeler` (authoring *and* deploy), `operator` (runtime
  control), `user` (tasks and reading).
- **b.** Five: the same, with `deployer` split out of `modeler`.
- **c.** Two: `admin` and `user`, with everything else decided by sharing scopes.

For **what an existing account gets on upgrade**:

- **i.** Existing accounts are migrated to keep exactly what they can do today
  (`modeler` + `operator` + `user`); new accounts default to `user`.
- **ii.** Nothing is migrated: the new roles are additive, and `user` keeps meaning
  what it means now until an operator narrows it.
- **iii.** A hard cut: `user` means tasks only, and every account is re-granted by
  hand.

## Decision outcome

Chosen: **option 1 for the declaration, (a) four roles, and (i) for the upgrade.**

### The declaration

Each entry in the route table names the role it requires. The boundary reads it;
no handler asks again. A route that names none does not compile past the test that
walks the table — the same shape as `wantPublicRoutes`, and for the same reason:
opening a route becomes a diff a reviewer sees.

Option 2 was the near miss. The pattern allowlist is proven here and would work,
but it puts the answer in a second file that has to be kept in step with the route
table by hand — and `TestEveryPublicAPIRouteEntryIsRegistered` exists precisely
because that kind of list drifts into naming routes that no longer exist. With the
role on the route, drift is impossible: there is one line.

Option 3 is what exists and what this replaces: 52 call sites, each of which can
be forgotten, and none of which a test can enumerate. Option 4 is rejected on the
evidence — the tags do not group by authority. `System` holds `GET /api/v1/info`
next to `POST /api/v1/restore/full`; `Incidents` holds the worker job endpoints;
`Processes` holds a read next to `DELETE /api/v1/processes/{key}`.

### The roles

Four. Only `admin` is a superset — it reaches every route, because an instance
where the administrator cannot reach an endpoint to fix it on the day its usual
holder is unreachable is not administered. The other three overlap in what they
read, but none contains another:

| Role | May |
|---|---|
| `admin` | everything, including accounts, credentials, secrets, backup and restore |
| `modeler` | author drafts, forms and decisions; validate; **deploy** |
| `operator` | start, cancel, terminate and repair instances; read runtime data |
| `user` | work on tasks, read what they are given, manage their own credentials |

Migrating a population of instances onto another definition, rewriting a running
instance's variables and reading a worker's job history stay with `admin` — not
because an operator could not be trusted with them, but because they were
admin-only before this and no route may become reachable by somebody who could not
reach it yesterday. Widening is the one direction this record does not take.

They are a list, not a lattice: an account carries several, and the check is "does
this principal hold the role this route names". That is the shape ADR-0044 already
chose for the field, and it keeps `modeler` from having to imply `operator` merely
to let a modeller start a test instance — they get both roles, deliberately.

Deploy sits inside `modeler` (option a), not in a role of its own (option b),
because the split that matters first is "not everybody may deploy", and one role
buys that. Splitting `deployer` out later costs one constant and the routes that
name it, because the role is declared per route — which is the point of option 1.
Option c is rejected: sharing scopes govern objects, and 146 routes are not about
an object at all.

### The upgrade

Existing accounts are migrated to `modeler` + `operator` + `user` — exactly what
they can do today — and new accounts default to `user`. Tightening is then an
operator's deliberate act on a screen, with nothing broken in the meantime.

The migration runs once, at startup, and each account carries a marker
(`rolesUpgradedAt`) saying its roles were written under this model. Without one, an
account an operator narrows would be widened straight back on the next restart —
the upgrade would be a standing policy rather than a one-off, and every decision
made on the screen would last until the next deployment. The marker sits on the
record rather than instance-wide because it describes one account and has to travel
with it: a full snapshot carries accounts and settings together, but a design-time
backup carries neither, and an instance-wide flag restored without the accounts it
describes would silently skip them.

This is the opposite of what ADR-0205 chose for ownerless connectors, and the
difference is the point. There, the current behaviour was a **hole**: an ordinary
account could delete somebody else's connector, and a measure that exempted every
existing installation would have closed nothing. Here the current behaviour is a
**documented, accepted risk** (R-04, amber) in a product whose installations are
running work today. Turning every account into a task worker on upgrade would stop
that work, and an upgrade that stops work is one nobody applies — which protects
nobody.

### Credentials that are not sessions

- **API token**: carries the roles of the account that minted it, snapshotted at
  mint time, and never `admin` — a machine that administers accounts is not a case
  Atlas has, and a leaked credential that could would be a much worse leak. Minting
  is itself an administrator's act, so in practice the token gets the whole non-admin
  set, which is exactly what an API token could reach the day before this shipped;
  the rule is written for the minter's roles so that it stays true if a narrower
  account is ever allowed to mint. Both halves are then enforced — the scope says
  which routes, the roles say which kinds of operation. A token minted before this
  reads as `modeler` + `operator` + `user`, by the same rule as the accounts.
- **Deploy token**: a publisher, so it carries `modeler` — publishing a bundle is
  the same act a person performs by deploying one, and a credential whose role said
  otherwise would be lying about what it does. It keeps `deploy-agent` beside it,
  which is what ADR-0071 reads to decide what a peer sees of a project, and its
  ADR-0129 allowlist — two routes — stays the narrower answer of the two.
- **The server's own internal token**: the credential Atlas hands the processes it
  supervises. It carries the legacy set for the same reason an existing account
  does: a supervised worker that stops leasing jobs on upgrade is precisely the
  outage this record set out not to cause.
- **OAuth grant**: already snapshots the person's roles (ADR-0200) and already has
  the maintenance that keeps them honest — so the upgrade joins that maintenance and
  rewrites the snapshot on a standing grant when it widens the account behind it.
  Skipping that would leave somebody's connector able to do less than they can, for
  a change they never made.
- **Auth off**: no principal, no roles, nothing enforced — as with every rule since
  ADR-0195.

### Consequences

- **Positive:** "who may deploy" becomes an answerable question, and R-04 moves
  from amber toward green. O-02 gets its first real slice.
- **Positive:** the reach of a role is provable by reading the route table, and a
  new route without one fails the build.
- **Positive:** MCP inherits it, because a tool call already runs as its caller.
- **Negative / trade-offs accepted:** 199 routes each need a decision made once, by
  hand, and a wrong one is a support call rather than a security incident. The
  inventory test makes the set reviewable; it cannot make each choice right.
- **Negative:** an upgrade grants three roles to every existing account, so nothing
  is safer until an operator narrows it. Named here rather than discovered later.
- **Negative:** one route in the runtime group stays open to every signed-in
  identity — reading a single instance's variables. A task form is prefilled from
  the variables of the instance its task belongs to, so the operator role the rest
  of that group carries would have handed a task worker an empty form. The rule that
  belongs there is the other axis — may you see *this instance* — which is the open
  work O-02 already names, not something a role per endpoint group can express.
- **Positive:** the Console's account screen grants the four roles by name, each
  with what it lets the person do, and its navigation offers only the apps and
  screens the signed-in person's roles reach — so a narrowed account meets a shorter
  menu rather than a wall of 403s. Shipping the rule without either would have made
  it an API-only capability, which ADR-0200 already taught is no capability at all.
- **Follow-ups / risks to watch:** the navigation is a courtesy, not a boundary —
  the server refuses regardless, and a screen reached by typing its URL will simply
  fail its calls. Federation (O-01) maps external claims onto exactly these
  roles, so the names are a public contract from the day they ship. And `deployer`
  as a fifth role should be a one-line change when somebody asks — if it is not,
  this record chose wrong.

## Pros and cons of the options

### 1 — role in the route table (chosen)
- Good: one line per route, next to what the route is; drift impossible; the whole
  policy is one readable table.
- Bad: 199 decisions to make by hand, once.

### 2 — per-role pattern allowlists
- Good: reuses a mechanism already proven twice here.
- Bad: a second list to keep in step with the route table, which is the failure the
  existing entry-registration test exists to catch.

### 3 — role checks in handlers
- Good: nothing new to build.
- Bad: what exists, 52 times; unenumerable, and one forgotten call is an open route.

### 4 — role per OpenAPI tag
- Good: 30 decisions instead of 199.
- Bad: the tags demonstrably do not group by authority.

## Links

- the layer below, which decides whether a route needs a principal at all:
  [ADR-0199](0199-route-access-classes.md)
- the identity and the roles field this fills in:
  [ADR-0044](0044-user-management-and-authentication-boundary.md)
- the other axis, which governs objects rather than operations:
  [ADR-0071](0071-sharing-scopes.md), [ADR-0180](0180-groups-as-members.md),
  [ADR-0205](0205-connector-ownership-and-event-delivery.md)
- the single source of truth this annotates: [ADR-0043](0043-openapi-spec-and-embedded-api-explorer.md)
- the credentials that must not fall over: [ADR-0194](0194-api-tokens.md),
  [ADR-0129](0129-remote-deployment-targets.md), [ADR-0200](0200-mcp-oauth-resource-server.md)
- why it reaches MCP without MCP-specific work: [ADR-0196](0196-authenticated-mcp-transport.md)
