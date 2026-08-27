# ADR-0194: API tokens — a credential a machine can actually be given

- **Status:** Proposed
- **Date:** 2026-08-26
- **Deciders:** Atlas maintainers

## Context and problem statement

Under `--auth`, Atlas accepted exactly two kinds of credential: a session cookie,
and the internal service token of ADR-0049. A deploy token (ADR-0129) is a third,
confined to two operations and issued for one purpose.

The internal token is minted from CSPRNG output at startup, kept in memory, and
**served over no endpoint** — deliberately, because its holder was the server's
own in-process MCP adapter. `workerTokenEnv` later handed it to the workers the
server spawns itself, which is the same argument: a supervised worker is this
process's own child on this host, not a third party.

That left a gap nobody could fall into while authentication was opt-in, and which
became the default path the moment it was not
(ADR-0195). **A machine that is not this server's child has
no credential it can hold.** Concretely:

- `atlas worker --server …` on another host — the ordinary way to run a connector
  in the target system's network zone (ADR-0168) — cannot authenticate.
- `atlas mcp --server … --token …` gained its flag in
  ADR-0196, and there is no value an operator can put
  in it.
- A CI job that deploys, a Prometheus scraper, any script: the same.

Worse, the workaround the code appears to offer does not exist.
`workerTokenEnv` reads:

> An operator who set `ATLAS_TOKEN` themselves keeps it: they have chosen an
> identity for their workers, and silently replacing it would undo that choice.

The supervisor honours that — it stops injecting, and the child inherits the
operator's value through `os.Environ()`. But `principalFor` compares a bearer only
against `s.internalToken`, which is only ever assigned from `randomHex(32)`. So
the value is honoured on the way out and refused on the way in. Verified against
the built binary: a server started with `ATLAS_TOKEN=x` answers `401` to
`Authorization: Bearer x`, identically to presenting nothing — and its supervised
workers, now holding `x`, are refused at every poll. Setting the variable breaks
the workers it appears to configure.

The question: **what credential does an administrator give a machine, and what may
that machine then do?**

## Decision drivers

- **Issuable and revocable.** A credential an operator cannot obtain is not a
  credential; one they cannot withdraw is a liability.
- **Bounded by default in time and reach.** The finding this whole line of work
  answers is "a credential that can do everything". Repeating that shape for
  machines would be answering it badly.
- **Never an admin.** A machine that administers accounts is not a case Atlas has.
- **One confinement mechanism, not a second one.** A parallel allowlist introduced
  one change after the first is exactly the drift a review catches.
- **Reuse what is proven.** Deploy tokens already got the storage right.
- **Do not make this a permission system.** Roles per endpoint group is a larger
  piece of work; a scope here must answer "what does this kind of machine need".

## Considered options

For **the credential**:

1. **Accept an operator-set `ATLAS_TOKEN`** as a shared secret — make the code's
   apparent promise real.
2. **A durable, admin-minted token store**, following the deploy-token pattern.
3. Let a machine hold a *user account* and log in like a person.

For **what it may reach**:

A. Nothing — unscoped, whatever a signed-in non-admin reaches.
B. **Named scopes, as fail-closed allowlists**, in the mechanism deploy tokens and
   the route access classes already use.
C. Per-route permissions chosen at mint time.

## Decision outcome

Chosen: **option 2 with option B.**

`apiToken` is the deploy-token record with two fields added, and everything else
deliberately identical because those details are what make a machine credential
safe to hand out: the secret is never stored (only its SHA-256, returned exactly
once at mint), SHA-256 rather than bcrypt (32 bytes of CSPRNG output has nothing
to brute-force, and bcrypt would add ~100ms to *every* authenticated request
rather than to a login), and revocation is deletion (no disabled flag to reason
about, nothing to resurrect by flipping back). The prefix `atlasat_` is distinct
from `atlasdt_` so a secret scanner and a human can each tell what they are
holding.

The two added fields are why this credential is general where that one is narrow.
**`expiresAt`**: checked on every resolution, and an expired token is refused
exactly like an unknown one while its record stays listed, so an operator can see
what needs reissuing. Zero means never, which is allowed — a worker that runs for
a year is a real case — but the mint API takes a lifetime, so "never" is something
somebody chose rather than the shape of the request. **`scope`**: what it may
reach.

Two scopes are mintable. `full` reaches everything a signed-in non-admin reaches,
which is the honest scope for a CI job or an MCP adapter driving a surface nobody
can enumerate in advance — and it is broad, which is why it must be named rather
than being what you get by omitting a field. `worker` reaches the four operations
`atlas worker` actually performs: lease a batch by type, settle each job either
way, and post a framed preview message back to this server's outbox (ADR-0150).
That scope is what earns the mechanism — a worker is a long-lived credential on
another host, often in another network zone, and its whole job is four calls.

**Enforcement is unified rather than added to.** ADR-0129's `deployAgentAllowed`
was a fail-closed allowlist resolved through an `http.ServeMux`, with a comment
arguing that a credential's reach must be provable by reading one short list.
Adding a second such mechanism beside it would have made that argument false, so
the deploy allowlist becomes one scope among the others — `deploy`, not mintable
here because the credential that carries it has its own store — and `withAuth`
has one check for every scoped credential there is. `RoleDeployAgent` stays,
because project visibility reads it (`api/scopes.go`); what moved is the reach
check, not the identity.

A token resolves to a principal with the token's name as its username, a
`system:token:<id>` user id so a trail says which token acted, **no roles at all**,
and its scope. No roles means `requireAdmin` refuses it, so even a `full` token
stops short of user administration — the same reasoning ADR-0049 applied to
`system:mcp`, now with an identity per machine instead of one shared by all of
them.

Verified end to end against the built binary: an administrator mints a
`worker`-scoped token; `atlas worker --server … --token …` on that token leases
its job types and exits cleanly, where the same command without one fails with
"authentication required"; and that token answers `403` on `/api/v1/processes` and
`/api/v1/deployments`, naming its scope.

Option 1 is rejected: a shared secret in an environment variable has no identity,
no expiry, no revocation short of restarting the server and reconfiguring every
holder, and no way to tell two machines apart in a log. It would also have to be
accepted as *unscoped*, since there is nothing on it to scope by. The honest fix
for the promise in that comment is to delete the promise, which this record does.
Option 3 is rejected because a machine with an account is an account somebody must
remember is not a person — it shows up in user listings, in assignee pickers, in
the admin count that guards against lockout — and because a password is the wrong
credential for a process.

Option A is rejected for the reason the whole exercise exists. Option C is
rejected as a permission system in disguise: choosing routes at mint time makes
every operator design an authorization model, badly, and the result cannot be
reviewed as a set the way a named scope can.

### Consequences

- **Positive:** a worker, an MCP adapter or a CI job on any host can hold a
  credential an administrator issued, bounded in time and reach, revocable
  immediately, attributable in the audit trail (`auth.token_minted`,
  `auth.token_revoked`, and the actor on every request it makes). The confinement
  mechanism is now one thing for deploy tokens and API tokens alike, so what any
  machine credential can reach is one file. The misleading `ATLAS_TOKEN` promise
  is gone.
- **Negative / trade-offs accepted:** `full` is broad — it is a real reduction only
  in *time* and *revocability*, not in reach, and an operator who gives a CI job a
  `full` token has given it what a user has. Naming it is the mitigation, not a
  fix. There is no `lastUsedAt`: a durable write per authenticated request is
  exactly the wrong trade on a single-writer loop, and an in-memory value that
  resets on restart would be half an answer, so an operator deciding whether to
  revoke has the audit trail and not a column. Tokens are one more thing to rotate.
  The internal token remains for the server's own children — a fourth credential
  in a codebase that now has one general one — because it is ephemeral, never
  leaves the host, and is therefore strictly better than a durable secret for that
  case.
- **Follow-ups / risks to watch:** deploy tokens should fold into this store
  outright, which means a record migration and belongs in its own change now that
  the enforcement is already shared. A `metrics` scope makes `/metrics` gateable
  without a second listener, which is the remaining piece of the access work. A
  failed *token* authentication — a bad or expired bearer rather than a bad
  password — is not yet an audit event and should be. And `full` deserves to be
  split once roles per endpoint group exist: at that point a token should be able
  to carry a role rather than a scope, and these two ideas should merge.

## Pros and cons of the options

### The credential

**1 — accept an operator-set `ATLAS_TOKEN`**
- Good: smallest possible change; makes an existing comment true.
- Bad: no identity, no expiry, no revocation, no way to tell two holders apart;
  necessarily unscoped; a secret in an environment variable is a secret in every
  process listing and every container spec.

**2 — a durable admin-minted store (chosen)**
- Good: identity, expiry, revocation and scope per credential; reuses storage that
  is already proven and reviewed; the secret is never stored.
- Bad: one more store, one more thing to rotate; three token kinds until deploy
  tokens fold in.

**3 — a machine holds a user account**
- Good: no new concept at all.
- Bad: a machine appears in user listings, assignee pickers and the last-admin
  guard; a password is the wrong credential for a process; nothing bounds it.

### What it may reach

**A — unscoped**
- Good: nothing to design.
- Bad: hands a machine everything a person has, which is the finding this work
  exists to answer.

**B — named scopes as fail-closed allowlists (chosen)**
- Good: reviewable as a set; the same mechanism as the deploy allowlist and the
  route access classes, so there is one thing to understand; an unknown scope
  reaches nothing.
- Bad: a coarse instrument — `full` is genuinely broad, and a new kind of machine
  needs a code change rather than a configuration one. (That is deliberate: the
  alternative is every operator inventing an authorization model.)

**C — per-route permissions at mint time**
- Good: maximally precise.
- Bad: a permission system without the design work a permission system needs;
  unreviewable, since every token is its own policy.

## Links

- generalizes the storage and one-time-secret discipline of
  [ADR-0129](0129-remote-deployment-targets.md), and absorbs its allowlist into
  the shared scope mechanism
- narrows the reach of [ADR-0049](0049-internal-service-auth-for-mcp.md)'s internal
  token to what it was designed for: this server's own children
- makes `atlas mcp --token` of ADR-0196 usable, and
  `atlas worker --token` usable from another host
- required by ADR-0195, which turned "a remote machine cannot
  authenticate" from an edge case into the default
- uses the allowlist shape of ADR-0199
- the product-side concept this implements:
  [`docs/compliance/zugriffsschutz-konzept.md`](../compliance/zugriffsschutz-konzept.md), measure M3
