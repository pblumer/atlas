# Connecting a system to atlas

What another system implements so that an atlas administrator can connect it with one
approval, choose what it may read, and let its people follow without a consent screen of
their own ([ADR-0512](../adr/0512-connected-systems.md)). Nothing here names a particular
system; the first to implement it is Orbithek (formerly wikiwikibangbang), and its
implementation is the reference.

Everything below is OAuth: the authorization code flow with PKCE
([RFC 6749](https://www.rfc-editor.org/rfc/rfc6749),
[RFC 7636](https://www.rfc-editor.org/rfc/rfc7636)), resource indicators
([RFC 8707](https://www.rfc-editor.org/rfc/rfc8707)), JWT client authentication
([RFC 7523](https://www.rfc-editor.org/rfc/rfc7523)), revocation
([RFC 7009](https://www.rfc-editor.org/rfc/rfc7009)) and the OAuth Client ID Metadata
Document draft as it stood in 2026-10. `<atlas>` is the engine's origin as its discovery
documents publish it (`/.well-known/oauth-authorization-server`); `<system>` is the
system's public origin.

## 1. Publish a document about yourself

The system's `client_id` is the https URL of a JSON document it publishes. Orbithek uses
`<system>/api/engines/client-metadata`; any path works.

```json
{
  "client_id": "<system>/api/engines/client-metadata",
  "client_name": "Example wiki",
  "client_uri": "<system>",
  "redirect_uris": ["<system>/api/engines/atlas/connect/callback"],
  "grant_types": ["authorization_code", "refresh_token"],
  "response_types": ["code"],
  "token_endpoint_auth_method": "private_key_jwt",
  "token_endpoint_auth_signing_alg": "EdDSA",
  "jwks": {"keys": [{"kty": "OKP", "crv": "Ed25519", "x": "<base64url>", "kid": "<kid>", "use": "sig", "alg": "EdDSA"}]}
}
```

What atlas checks when an authorization request names such a `client_id`:

- the URL is `https` (or `http` on a loopback host, for trying it out beside atlas), at
  most 120 characters, without credentials or a fragment;
- the document answers `200` with a JSON media type, at most 64 KiB, within 5 seconds,
  without a redirect — those are the budgets `ATLAS_LIMIT_CLIENT_METADATA` and
  `ATLAS_LIMIT_CLIENT_METADATA_SECONDS`;
- the host resolves to public addresses only, unless the operator set
  `ATLAS_CLIENT_METADATA_ALLOW_PRIVATE=on` (a system inside the same network needs it);
  no outbound proxy is used for this read;
- `client_id` equals the URL exactly; every `redirect_uris` entry has the scheme and
  host:port of the `client_id`, and the request's `redirect_uri` is one of them, compared
  whole;
- `token_endpoint_auth_method` is `private_key_jwt`, the document carries no
  `client_secret`, and `jwks` holds at least one Ed25519 key. P-256 keys (ES256) are read
  beside it. `jwks_uri` is not read.

atlas keeps the document for its `Cache-Control: max-age`, an hour at most, reads it
again for every approval, and once more when an assertion names a key it does not hold or
does not verify — so a key is rotated by changing the document. Reading the document
writes nothing in atlas; atlas remembers the system only once an approval is exchanged
for tokens.

## 2. Authenticate with a signed assertion

At `<atlas>/oauth/token` and `<atlas>/oauth/revoke` the system sends, beside the request's
own fields:

```
client_id=<the document's URL>
client_assertion_type=urn:ietf:params:oauth:client-assertion-type:jwt-bearer
client_assertion=<JWS compact serialisation>
```

The JWS header is `{"alg":"EdDSA","typ":"JWT","kid":"<kid>"}` (or `ES256` with a P-256
key). Its claims: `iss` and `sub` are the `client_id`; `aud` is the `token_endpoint` from
atlas's metadata — atlas also accepts its issuer URL, and at the revocation endpoint the
`revocation_endpoint`; `jti` is random and unique, at least 128 bits; `iat` is now; `exp`
is at most five minutes after `iat`. atlas allows its clock and the system's to differ by
a minute and accepts each `jti` once within its lifetime.

Every refusal — a wrong algorithm, an unknown key, a bad signature, a wrong `iss`, `sub`
or `aud`, an expired or replayed assertion — answers `401 {"error":"invalid_client"}`.
The reason is written to atlas's audit log (`auth.oauth_denied`), for the operator to
read.

## 3. Ask an administrator for the system grant

The administrator starts the connection in the system, which sends their browser to:

```
<atlas>/oauth/authorize?response_type=code&client_id=<url>&redirect_uri=<one of yours>
  &state=<random>&code_challenge=<S256>&code_challenge_method=S256
  &resource=<atlas>/system&scope=diagrams%20tasklist%20events
```

- The resource's protected-resource metadata is at
  `<atlas>/.well-known/oauth-protected-resource/system`.
- `scope` may name `diagrams`, `tasklist` and `events`; empty asks for all three.
  Anything else is answered by a redirect with `error=invalid_scope`.
- The consent screen shows the `client_id`'s host above `client_name`. Only a signed-in
  person holding the `admin` role may approve; anybody else is told an administrator has
  to connect the system. The administrator may untick capabilities, narrow each to
  projects, and choose whether people may connect without being asked (pre-consent).
- A new approval for the same client replaces the standing system grant.

The code is exchanged at the token endpoint as usual (§2 authentication, `grant_type=
authorization_code`, the `code_verifier`). The answer carries `access_token` (two hours),
`refresh_token` (rotated on every use) and `scope`, the capabilities that were granted —
possibly fewer than asked for.

### What the system grant reaches

The access token reaches the routes of its granted capabilities, each with its reach, and
`GET /api/v1/auth/me`:

| Capability | Routes | With a reach of projects |
|---|---|---|
| `diagrams` | `GET /api/v1/processes`, `…/{key}/xml`, `…/{key}/runtime/counts`, `GET /api/v1/architectures`, a release's `architecture.embed.svg` | as a `diagrams` API token with that reach |
| `tasklist` | `GET /api/v1/tasks`, `GET /api/v1/tasks/{key}` | the tasks of processes deployed from those projects; any other task by key is `404` |
| `events` | `GET /api/v1/events` | the task facts (`atlas.task.*`) of those projects and nothing else; without a reach, what an `events` token without reach reads |

It acts as the approving administrator, with the roles a token they minted would carry
— never `admin`. It ends when that person stops being an administrator or is disabled,
when an administrator disconnects the system, or when the system revokes it.

`GET /api/v1/auth/me` answers, beside the approver's account:

```json
"system": {
  "clientId": "<url>", "clientName": "Example wiki",
  "approvedBy": "patrick", "approvedAt": "2026-10-30T12:00:00Z",
  "capabilities": [{"scope": "tasklist", "projects": []}, {"scope": "events", "projects": ["p-1"]}],
  "preconsent": true
}
```

`GET /api/v1/tasks/{key}` tells a task that ended from one out of reach: open → `200`;
completed or cancelled while atlas's event feed still holds its end (its retention,
30 days by default) → `410 Gone` with `{"key":"…","ended":"completed"|"cancelled",
"endedAt":"<RFC 3339>"}`; unknown, outside the reach, or ended longer ago → `404`, after
which the system detaches its copy.

## 4. Connect each person

A person's grant is ADR-0510's: the same flow with `resource=<atlas>/tasks` and no
`scope`. It works that person's tasks as that person.

- With pre-consent standing, a person signed in to atlas (by password or single sign-on)
  who has not declined the system is approved without the consent page: the browser is
  redirected straight back with a code.
- With `prompt=none`, atlas never shows a page: not signed in →
  `redirect_uri?error=login_required&state=…`; signed in, but no pre-consent or the person
  declined → `error=consent_required`. A system can therefore try in the background and
  send the person through a sign-in only when it has to.
- A person who withdraws their grant (on atlas's Connected accounts or AI access page) has
  declined the system: pre-consent no longer applies to them until they approve it on the
  consent screen themselves.

## 5. Give a grant back

`POST <atlas>/oauth/revoke` with §2's authentication and `token=<access or refresh
token>` withdraws the whole grant — the system grant, or a person's. It answers `200`
whether or not the token was known, as RFC 7009 has it.

## Errors a system may receive

| Where | Answer | Meaning |
|---|---|---|
| consent screen | a message, no redirect | the document could not be read or did not pass, or the `redirect_uri` is not in it |
| redirect | `invalid_scope` | `scope` named something other than the three capabilities |
| redirect | `invalid_target` | `resource` is not one atlas publishes |
| redirect | `access_denied` | the person did not approve |
| redirect | `login_required`, `consent_required` | `prompt=none` could not be answered without a page |
| token, revoke | `401 invalid_client` | the assertion failed a check (§2) |
| token | `400 invalid_grant` | the code or refresh token is unknown, spent, revoked or another client's |
| token, revoke | `429 temporarily_unavailable` | the throttle per address (`ATLAS_LIMIT_TOKEN_ENDPOINT_*`) |
| API | `403` | the route is not one the grant's capabilities reach |
