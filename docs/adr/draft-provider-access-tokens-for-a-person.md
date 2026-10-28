# ADR-DRAFT: Atlas accepts access tokens its sign-in provider issued for it, when another application acts for a person

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-10-28
- **Deciders:** Atlas maintainers (requested 2026-10-28 by the maintainer: a wiki page starts a
  process as its reader and lists the instances that reader started, with Atlas deciding by its
  own check who the reader is)
- **Open question:** whether Entra ID access tokens for a custom API carry what this record reads.
  The record assumes that a token another application requests for Atlas's API carries `oid`,
  `azp` and `scp`, and names the tenant's v2.0 issuer when the API is registered for v2.0 tokens.
  Microsoft's documentation says so; no tenant has been tried. A token that differs is refused, so
  a wrong assumption fails closed.
- **Question checked:** 2026-10

## Context and problem statement

The starter grant ([ADR-draft-starters-start-processes-and-follow-their-instances](draft-starters-start-processes-and-follow-their-instances.md))
lets a person start an application's processes and follow the instances they started. Its routes
answer the caller's own: `GET /api/v1/me/startable-processes`, the two start routes,
`GET /api/v1/me/started-instances` and `GET /api/v1/instances/{key}/progress`
([ADR-draft-every-user-task-is-addressed](draft-every-user-task-is-addressed.md)). A wiki
(dbuchs7/wikiwikibangbang, its ADR-0151) wants to offer them on its pages: a start form that starts
the process as the reader, and the reader's instances below it.

For that, Atlas must know which person is calling when the call comes from another application.
What `principalFor` accepts today (`api/auth.go`):

- the internal token of supervised workers;
- deploy tokens of peer servers ([ADR-0129](0129-remote-deployment-targets.md));
- API tokens ([ADR-0194](0194-api-tokens.md)), which act as a machine with the roles of whoever minted
  them, so every start through one records the token, not a person;
- Atlas's own OAuth access tokens ([ADR-0200](0200-mcp-oauth-resource-server.md)), which carry a
  person, but under the scope `mcp`: they reach the MCP transport and nothing else;
- session cookies.

None of them lets another application present a person whom Atlas verifies itself.

Atlas already trusts one OpenID provider for sign-in ([ADR-0210](0210-federated-authentication.md)):
- It verifies ID tokens with its own small verifier: RS256 only, keys from the provider's key set,
  issuer, audience and time checked (`api/oidctoken.go`).
- It links an account by `(oidc, sub)`, and applies the operator's claim mapping to roles and groups
  at each login (`api/oidcmapping.go`).
- Entra ID's `sub` is pairwise per application. Another application therefore receives a different
  `sub` for the same person. The stable identifier is `oid`, which the directory mirror stores as
  `DirectoryID` ([ADR-0332](0332-entra-directory-provisioning.md), verified against Microsoft's
  documentation 2026-09). A federated login does not record `oid` today.

The question is how Atlas lets another application act for a person on a narrow set of routes:
- the person's identity verified by Atlas and not asserted by the application;
- no credential store on either side;
- nothing reachable beyond what starting and following one's own instances needs.

## Decision drivers

- The provider vouches for the person and Atlas checks everything itself. A name an application
  asserts is never believed.
- An ID token is never a bearer credential, and a token issued to an application acting for itself
  (no person) is not one either.
- The reach is the starter's and nothing more, confined by the scope mechanism every other
  credential uses (ADR-0194).
- Only applications an administrator lists may act for people.
- `principalFor` reads in-memory indexes only, never the user store.
- A disabled account is refused at once, not when a token expires.
- Inert until configured, as ADR-0210 is.

## Considered options

1. **Atlas as a resource server for its sign-in provider's access tokens**, verified like ID tokens,
   confined to a new scope, accepted only from listed applications.
2. **Token exchange at Atlas** (RFC 8693): a listed application presents the provider's token to
   Atlas's authorization server and receives an Atlas access token for the person.
3. **Widen Atlas's own OAuth grants** (ADR-0200) to the REST routes: the application becomes an Atlas
   OAuth client that every person approves once.
4. **An API token plus a header naming the person**, trusted from listed applications.
5. **Introspection at the provider** on every request, for opaque tokens.

## Decision outcome

Chosen option: **1**, because:
- the person's identity comes from the provider Atlas already trusts for sign-in, checked by code
  Atlas already has;
- nothing is stored on either side;
- the provider bounds each token's life;
- the scope mechanism confines what the token reaches, exactly as it confines every other credential.

### Settings

- A new admin setting, `federatedAccess`, kept with the OIDC mapping. It is **off by default**.
  - `audience`: what the provider writes into `aud` for Atlas's API, e.g. its application ID URI or
    client ID.
  - `scope`: the delegated scope a token must carry, e.g. `start`.
  - `clients`: the client IDs of the applications that may act for people.
  - `userClaim`: `sub` or `oid`, the claim that names the person.
- The issuer is the configured OIDC issuer (ADR-0210), exactly. For Entra ID, the API's registration
  must issue v2.0 access tokens; a v1.0 token names another issuer and is refused (the open question).
- Writing the setting is refused when a client or the audience is empty, when `scope` is empty, or
  when no provider is configured. As for the mapping, the refusal comes at write time, not on every
  request.

### Checking a token

A bearer that matches no Atlas credential is checked as a provider access token, in this order. The
first failure refuses it with 401 and `error="invalid_token"`; the reason goes to the log, not the
answer.

1. **Signature:** a JWS signed with RS256 by a key of the provider's key set, with the same verifier
   and key cache as sign-in.
   - A key ID that is not held triggers at most one refresh of the key set per minute.
   - Today a refresh happens only at a sign-in, which is throttled
     ([ADR-0197](0197-login-throttle-and-audit-log.md)); a bearer on every request is not.
2. **Issuer and audience:** `iss` equals the issuer; `aud` names the configured audience.
3. **Time:** `exp`, `nbf` and `iat` within the sign-in's clock skew.
4. **A delegated scope:** the configured scope is among the space-separated values of `scp` or
   `scope`.
   - An ID token carries neither claim, so it is refused here even when its audience is Atlas's own
     client ID (with Entra ID, sign-in and API often share one registration).
   - A token an application obtained for itself, with no person, carries application roles and no
     delegated scope, and is refused too.
5. **A listed application:** `azp` (Entra ID v2.0, OpenID Connect) or `client_id` (RFC 9068) names a
   listed client.
6. **A known person:** the claim `userClaim` names resolves to an enabled account.
   - `sub` matches the `ExternalID` of an `oidc` account.
   - `oid` matches the lower-cased `DirectoryID`.
   - **A token never creates an account.** Accounts come from a sign-in or from the mirror, as today.
   - A person with no account here is refused, and the log says they have never signed in.

### Recording `oid` at sign-in

With `userClaim` set to `oid`, a federated login writes the ID token's `oid`, lower-cased, into an
empty `DirectoryID`. A person who signed in once is then found by the tokens of another application.
This is the value ADR-0332's mirror would write for the same person, so a later mirror run joins on
it exactly instead of by address. A `DirectoryID` already set is never overwritten.

### The principal

- `principalFor` resolves the person through an in-memory index from claim value to user ID,
  username, roles, groups and whether the account is enabled.
  - It is built at start from the user store.
  - It is maintained at every federated login, and at the call sites that already keep sessions and
    OAuth grants current: disabling or deleting an account, and changing its roles or groups
    (`revokeUserGrants`, `setUserGrantRoles`, `setGrantGroupMembership`, `dropGroupFromGrants`;
    [ADR-0281](0281-session-role-revocation.md)).
- The principal is the person's own: their roles and groups as Atlas holds them now. The token's
  claims grant nothing. The claim mapping stays a sign-in matter (ADR-0210).
- Its scope is a new, non-mintable `apiScopeStarter`. It reaches:
  - the two start routes;
  - `GET /api/v1/me/startable-processes` and `GET /api/v1/me/started-instances`;
  - `GET /api/v1/instances/{key}/progress` and `GET /api/v1/instances/{key}/variables`;
  - `GET /api/v1/forms/{id}`;
  - `GET /api/v1/processes/{key}/xml`.

  Nothing else. Each route's role and object question then apply as for the person's own session:
  only what the starter grant allows is started, only their own instances are listed, and only the
  start form's fields are read (ADR-0275).
- Each request through such a token is logged with the person and the client (`azp`), and counted per
  client. The instance's `StartedBy` is the person, as for a start from Atlas's own Start view.

### Consequences

- **Positive:**
  - Another application acts for a person on exactly the starter's routes. Atlas checks the person
    itself, against the provider it already trusts.
  - The audit names the person, not a machine.
  - No credential store on either side.
- **Positive:** a disabled account and a client taken off the list are refused at once. A role or
  group change applies on the next request, as for sessions.
- **Negative / trade-offs accepted:**
  - Atlas cannot revoke one token. Each lives until its expiry at the provider (about an hour for
    Entra ID by default), within the narrow scope.
  - A listed application can act, within that scope, for any person whose token it holds. That is
    what delegation is; the list, the scope and the provider's consent bound it.
- **Negative:**
  - Only providers that issue JWT access tokens for a custom API qualify.
  - Opaque tokens are refused.
  - As far as this record knows, Google's sign-in issues access tokens only for Google's own APIs
    (an assumption, not checked).
- **Negative:** one more in-memory index to keep current, at the same call sites as sessions and
  grants.
- **Follow-ups / risks to watch:**
  - Option 2, if per-token revocation or lifetimes Atlas sets become necessary.
  - Option 3, for installations whose provider issues no tokens for Atlas.
  - Protected resource metadata (RFC 9728) for this audience, so a client can discover the provider.
  - A second scope only when a second kind of delegated use turns up (ADR-0194's rule for scopes).

## Pros and cons of the options

### Option 2: token exchange at Atlas

Strongest form: Atlas checks the provider's token once and then issues its own token. That token has
an audience that is only Atlas, a lifetime Atlas sets, and Atlas can revoke it, much as its OAuth
grants are revoked today.

- Good: revocation and lifetime under Atlas's control; the provider's token travels once.
- Bad:
  - A token store and a new grant type on the authorization server, for a gain the account switch and
    the client list already give for the cases that matter.
  - The other application still needs the provider's token first.

  A good next step if this record's revocation window proves too wide.

### Option 3: widen Atlas's own OAuth grants

Strongest form: works with any provider and with local accounts; Atlas issues, refreshes and revokes
everything; each person approves the application on Atlas's consent screen.

- Good: independent of the provider; revocable per person.
- Bad:
  - The application must keep a refresh token per person to be useful, a store the wiki declined in
    its ADR-0129.
  - Every person meets a second consent screen.
  - ADR-0200 deliberately bound these grants to the MCP transport; widening them is a decision about
    that record.

### Option 4: an API token and a header naming the person

Strongest form: no token per person, no provider involvement, one relation between two servers an
administrator sets up.

- Good: simplest to operate.
- Bad: Atlas would believe whatever name the application sends. A compromised application starts
  processes as anybody, and `StartedBy` would be only as true as the application. This is what both
  sides set out to avoid.

### Option 5: introspection at the provider

Strongest form: works with opaque tokens; the provider answers whether a token is still valid, so a
revoked token stops at once.

- Good: immediate revocation at the provider.
- Bad:
  - A call to somebody else's service on every request, on the handler path.
  - Entra ID offers no introspection for its access tokens.
  - Atlas would depend on the provider's availability for every start.

## Links

- extends [ADR-0210](0210-federated-authentication.md) (the provider Atlas trusts for sign-in) and [ADR-0194](0194-api-tokens.md) (scopes confine every credential)
- relates to [ADR-0199](0199-route-access-classes.md), [ADR-0200](0200-mcp-oauth-resource-server.md), [ADR-0209](0209-roles-per-endpoint-group.md), [ADR-0275](0275-instance-visibility.md), [ADR-0281](0281-session-role-revocation.md), [ADR-0332](0332-entra-directory-provisioning.md), [ADR-0475](0475-identity-aware-worker-execution.md)
- serves [ADR-draft-starters-start-processes-and-follow-their-instances](draft-starters-start-processes-and-follow-their-instances.md) and [ADR-draft-every-user-task-is-addressed](draft-every-user-task-is-addressed.md)
