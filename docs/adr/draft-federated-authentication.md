# ADR-DRAFT: Federated authentication

- **Status:** Proposed
- **Date:** 2026-08-29
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0044](0044-user-management-and-authentication-boundary.md) gave Atlas
accounts and a login, and said in as many words which parts were built for what
comes after: a `Source` and an `ExternalID` on the user record, and an
authentication boundary where "tomorrow it can come from an OIDC/JWT bearer token
by changing only the middleware". [ADR-0209](0209-roles-per-endpoint-group.md)
then gave the product four roles, which is the thing external claims have to land
on. Both were written pointing here.

Measured against `main`:

| | |
|---|---|
| Ways to authenticate a **person** | 1 (local password) |
| Places a session is created | 1 (`handleLogin`, `api/users.go`) |
| Production reads of `User.Source` | 1 (the Console listing) |
| Production reads of `User.ExternalID` | **0** |
| OIDC, JWT or JWKS dependencies | 0 |

So the hooks are there and nothing has ever used them. That is the good news and
the whole risk in one line: the seam is a single function, and everything that
would make an external identity real — validating what the provider says, deciding
who gets an account, deciding what that account may do — does not exist yet.

What the absence costs is recorded, not hypothetical. Risk **R-03** (amber): local
passwords instead of eIAM, no MFA, no central password policy, and — the one an
auditor asks about first — **no automatic withdrawal when somebody leaves**. An
account here outlives its holder's employment until an administrator removes it by
hand. Open point **O-01** carries the same, and names what is missing: a relying-party
flow behind the same `*Principal` boundary, a role and group mapping from claims,
and documentation for running behind an authenticating proxy.

The question: **how does an identity from somewhere else become a principal here,
without a second authorization vocabulary, without trusting the network path, and
without Atlas growing a second half-built login it then has to maintain?**

## Decision drivers

- **One seam, not two.** A federated login must end where the local one ends — in
  `sessionStore.create`, with the same roles and groups snapshot — or every rule
  written since ADR-0195 has to be re-proved for a second kind of caller.
- **Trust nothing the network can set.** A header an upstream proxy sets is a
  credential anybody who reaches the port directly can forge. Whatever is chosen
  must be safe when somebody bypasses the proxy, because in a single-binary
  product somebody eventually does.
- **Claims land on the four roles that exist.** ADR-0209 made `admin`, `modeler`,
  `operator` and `user` a public contract precisely so a mapping has a target.
  Federation must not invent a fifth vocabulary.
- **Offboarding is the point.** MFA and password policy are the visible benefits;
  the one that closes a real hole is that a person who loses the account at the
  provider loses Atlas with it.
- **An installation must be able to keep working.** An operator who misconfigures
  the provider must not be locked out of their own instance, and an instance with
  no provider configured must behave exactly as it does today.

## Considered options

For **where the identity comes from**:

1. **OIDC relying party in Atlas.** Authorization Code with PKCE, provider
   discovery, ID-token validation against the provider's JWKS.
2. **A trusted header from an authenticating reverse proxy.** The proxy does the
   authentication; Atlas reads `X-Forwarded-User` or similar.
3. **SAML 2.0 relying party.** What eIAM classically speaks.
4. **LDAP bind.** Atlas already vendors `go-ldap` for the Active Directory
   connector, so a bind against the directory is the smallest possible change.

For **what happens to local accounts**:

- **a.** Coexistence: local login stays, and at least one local administrator is
  kept as a break-glass account.
- **b.** Exclusive: with a provider configured, local passwords stop working.
- **c.** Per account: `Source` decides which path a given account authenticates by.

For **who gets an account**:

- **i.** Just-in-time: a subject the provider vouches for gets an account on first
  login.
- **ii.** Pre-provisioned only: an unknown subject is refused.
- **iii.** Just-in-time behind the existing intake process (ADR-0122), so a human
  approves before the account exists.

For **what claims may grant**:

- **A.** A configured mapping from claim values to Atlas roles and groups.
- **B.** Convention: a `roles` claim carrying Atlas role names verbatim.
- **C.** Nothing. Every federated account starts at `user`, and roles are granted
  in Atlas.

For **ADR-0200's authorization-server half** (1,911 lines that let a hosted MCP
client obtain a token):

- **α.** Keep it. Only the human login federates.
- **β.** Retire it. The protected-resource metadata points at the external
  provider, which issues the tokens MCP clients present.

## Decision outcome

Proposed: **option 1 (OIDC relying party), (a) coexistence with a break-glass
local administrator, (i) just-in-time provisioning, (C) no role from a claim in the
first step, and (α) the authorization server stays.**

### The protocol

An OIDC relying party, because it is the only option on the list that is safe
without a trusted network path. Option 2 is not an authentication mechanism at all:
it is a decision to believe a header, and in a single binary that anyone can reach
on its own port that belief is exactly one misrouted request away from being a
public login. It stays in the record as an *operating* pattern with guardrails
written down — bind the server to loopback, require a shared secret alongside the
header, never on by default — because installations that already run such a proxy
exist and the ISDS record asks for that documentation. It does not become the
product's answer.

Option 3 is deferred rather than rejected: eIAM speaks SAML for its older
integrations, and if that is the target the work is a second front end onto the
same seam. Nothing here forecloses it, and the record says so out loud so that
choosing SAML later is a decision and not a rewrite. Option 4 is rejected as the
answer while it stays available as a convenience: an LDAP bind sends the person's
password through Atlas, which is the property federation exists to remove.

### The shape

Three endpoints and one seam:

- `GET /auth/oidc/start` builds the authorization request — `state`, `nonce`, a
  PKCE verifier — and redirects. Public, like `/oauth/authorize` is, and for the
  same reason: an unauthenticated browser has to be able to land there.
- `GET /auth/oidc/callback` exchanges the code, validates the ID token against the
  provider's JWKS, resolves the account, and creates the session.
- The login screen offers a "Sign in with …" button when a provider is configured,
  and keeps the password form.

The validation is the part that must not be hand-waved: issuer, audience,
expiry, `nonce`, signature against a cached JWKS that refreshes on an unknown key
id. That is the whole security of the flow, so it is one file with its own tests
and no shortcuts.

### The account

`Source` becomes `oidc` and `ExternalID` the provider's `sub` — the two fields
ADR-0044 put there, used at last. The subject is the identity, not the email
address: an email can be reassigned to a different person, a `sub` cannot.

Just-in-time provisioning, with the roles ADR-0209 gives a new account: `user`,
and nothing else, until an administrator says otherwise. That makes a first login
safe by construction — the worst case is somebody who can see their own task list —
and it is the same default a locally created account gets, so there is one rule to
explain rather than two. Option ii is the conservative-looking choice that is worse
in practice: it means an administrator hand-creates every account with a username
that must match a claim exactly, which is a mapping maintained by hand and wrong the
first time somebody's name changes. Option iii is right where an approval step is
genuinely wanted, and it is what the existing intake process (ADR-0122) already
does — an operator who wants it points registration at that process instead.

### What a claim may grant, and what it may not

In the first step: nothing. A federated login yields a `user`, and roles and group
membership are granted in Atlas. The claim mapping of option A is the second step,
configured explicitly, with the empty mapping meaning "no claim grants anything".

The order matters and it is the ADR-0209 order: the day the mapping ships, whoever
administers the provider's group memberships administers Atlas's roles. That is the
point of federation and also its sharpest edge, so it should be a thing an operator
turns on deliberately, on a screen, after the login itself is proven — not something
that arrives with it.

### The break-glass account

Local login stays. An instance whose provider is unreachable — a certificate
expired, a discovery document moved, a network path closed — must still be
administrable, and the lockout guard of ADR-0044 already refuses to leave an
instance without an enabled administrator. Federation does not get to take that
away.

### The authorization server stays

ADR-0200 named a future where an external provider issues the tokens MCP clients
present and Atlas keeps only the resource-server half. That future is still right,
and it is not this record: it requires the provider to issue tokens with Atlas as
the audience, which is a claim about the provider's configuration that a
self-hosted PoC cannot make and eIAM will not make casually. Federating the human
login is worth doing on its own, and the two halves are independent.

### Consequences

- **Positive:** R-03 moves from amber toward green for installations that federate;
  offboarding stops being a manual step; MFA and password policy become the identity
  provider's job, which is where they belong.
- **Positive:** the seam stays one function. A federated session is the same session
  a local login produces, so every rule since ADR-0195 keeps holding without a second
  proof.
- **Positive:** `Source` and `ExternalID` stop being decoration. The Console can say
  which accounts are external, and a later SAML or LDAP front end has a shape to fit.
- **Negative / trade-offs accepted:** Atlas takes on token validation, which is
  security-critical code it did not have. It is bounded and testable, but it is real,
  and getting it wrong is worse than not having it.
- **Negative:** a provider outage becomes an Atlas outage for federated accounts.
  The break-glass local administrator is the answer, and it is only an answer if
  somebody keeps that password.
- **Negative:** sessions remain in memory (ADR-0044, open point O-14), so a restart
  still signs everybody out. Federation makes that more visible, because the fix is
  now one redirect rather than a password prompt.
- **Follow-ups / risks to watch:** claim mapping to roles and groups as the second
  step; RP-initiated logout, so signing out of Atlas can end the provider session
  too; SAML if eIAM requires it; and the ADR-0200 authorization-server question,
  which this record deliberately leaves open rather than answering early.

## Pros and cons of the options

### 1 — OIDC relying party (proposed)
- Good: safe without trusting the network path; the protocol new identity providers
  actually offer; lands on the one seam that exists.
- Bad: token validation is security-critical code Atlas has to own; a new
  configuration surface an operator can get wrong.

### 2 — trusted proxy header
- Good: almost no code; matches installations that already run an authenticating
  proxy.
- Bad: a header is forgeable by anyone who reaches the port; the security of the
  whole product becomes a property of a deployment diagram.

### 3 — SAML relying party
- Good: what eIAM speaks today.
- Bad: XML signature handling is a larger and sharper surface than a JWT; nothing
  else in Atlas needs it.

### 4 — LDAP bind
- Good: the dependency is already vendored; no redirects, no browser flow.
- Bad: the password still travels through Atlas, which is the property federation
  exists to remove; no MFA; no session at the provider.

## Links

- the identity model and the boundary this fills in:
  [ADR-0044](0044-user-management-and-authentication-boundary.md)
- the roles claims will map onto, and why they are a public contract:
  [ADR-0209](0209-roles-per-endpoint-group.md)
- what a login is required for at all: [ADR-0195](0195-auth-on-by-default.md),
  [ADR-0199](0199-route-access-classes.md)
- the OAuth work this deliberately does not undo yet, and its own "federate later"
  option: [ADR-0200](0200-mcp-oauth-resource-server.md)
- the groups a group claim would map onto: [ADR-0180](0180-groups-as-members.md)
- the login hardening that stays for local accounts:
  [ADR-0197](0197-login-throttle-and-audit-log.md)
- the intake process an operator can put in front of provisioning:
  [ADR-0122](0122-protected-system-project-and-bootstrap-deployment.md)
