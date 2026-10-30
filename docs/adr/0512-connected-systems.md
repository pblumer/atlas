# ADR-0512: Connected systems — an administrator connects another system with one approval, chooses what it may read, and its people follow without a consent screen of their own

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-10-30
- **Deciders:** requested 2026-10-30 by the maintainer. Connecting wikiwikibangbang to Atlas as ADR-0510 and the wiki's ADR-0151 built it took five credentials kept in three places, and the maintainer asked for it to be simpler. They decided: (1) an administrator who manages an Atlas and another system connects the two with a few clicks; (2) the procedure is general — any system may use it, not only the wiki — although no second system is in view today; (3) every person keeps a grant of their own and Atlas keeps deciding who may work a task, while the administrator approves once for all of them (pre-consent); (4) the administrator who connects a system decides what it may read. Proposed for the maintainers' acceptance.
- **Open question:** three points. (1) **The way a system names itself is an IETF draft.** §1 follows the OAuth Client ID Metadata Document draft as it stood in 2026-10; before building, the fields and rules used here are checked against the then current version. (2) **What the people are told.** Pre-consent lets a system read a person's tasks without that person having approved it; §4 lists such systems to each person and lets them withdraw. Whether a notice beyond that is owed — a mail, a first-use banner — is a question of the applicable data protection law, which this record does not settle. (3) **The other direction.** A process that writes into the connected system (ADR-0498) still needs a credential set up by hand; §6 records the shape a follow-up could take, not a decision.
- **Question checked:** 2026-10

## Context and problem statement

What connecting wikiwikibangbang to an Atlas takes today:

| What | Where it is made | Where it goes |
|---|---|---|
| an API token of scope `diagrams` (ADR-0493), for processes, forms and sketches | Console → API tokens | the wiki's environment |
| an API token of scope `tasklist` (ADR-0510 §2), minted by an administrator so it lists every open task | Console → API tokens | the wiki's environment |
| an API token of scope `events` without a reach (ADR-0510 §1), for the task facts | Console → API tokens | the wiki's environment |
| an OAuth client with the wiki's redirect URI copied by hand (ADR-0200) | Console → AI access → Another application | client ID and secret into the wiki's environment |
| each person's grant for `…/tasks` (ADR-0510 §3) | each person, on the consent screen | the wiki, one per person |

An operator who keeps the wiki's environment in configuration management adds a vault and a deployment to that list. Every value is copied by a person from one screen into another file, and nothing checks that it arrived under the right name.

Two mechanisms Atlas already has are close to a shorter path and are not it:

- **Self-registration (RFC 7591, ADR-0200 step 2)** removes the hand-made client, but it is off unless an operator opens it, and `oauthregister.go` says why: it is the one unauthenticated endpoint that writes durable state, and opening it makes "an application is asking for access" stop implying that anybody vetted it. It also does nothing for the three tokens.
- **A grant (ADR-0200)** already carries a person, snapshots their roles and is kept current — a disabled account revokes its grants, a role change rewrites them. But a grant reaches what its resource names (the server, `/mcp`, `/tasks`); none names the narrow, mintable scopes a system needs to read on its own.

The question: how does an administrator connect another system to Atlas in one step, choosing what it may read, without a secret copied by hand, without opening an endpoint to strangers, and without Atlas giving up the decision over what each person may do?

## Decision drivers

- **One approval, nothing copied.** An administrator who manages both systems starts the connection in the other system and approves it in Atlas. No token, client ID, secret or redirect URI is copied by a person, and nothing has to go into an environment file.
- **Atlas alone decides who works a task.** A person's work still runs on that person's own grant (ADR-0196, ADR-0510 §3); a connected system never acts for a person it names.
- **The approver decides what a system reads, narrower than what they could mint,** and the choice is visible and changeable in one place (ADR-0194: a credential reaches what its holder needs).
- **No durable state for strangers.** Nothing a request from an unknown system can trigger writes a record before an administrator has approved it.
- **Standards where they exist.** The parts any system has to implement are OAuth: authorization code with PKCE, the metadata documents of RFC 8414 and RFC 9728, revocation by RFC 7009. What is Atlas's own is the consent screen and the Console page.
- **People can see and withdraw** what was approved for them.
- **General.** Nothing in Atlas names the wiki; the wiki is the first system to implement the procedure.

## Considered options

1. **Connected systems:** a system names itself by an address of its own (a client ID metadata document); an administrator approves a *system grant* for a fourth resource, `…/system`, choosing its capabilities and reach on the consent screen, and may pre-consent its people's task grants.
2. **Open self-registration** (RFC 7591) and keep the tokens as they are.
3. **A pairing code:** an administrator mints one connection token in Atlas and pastes it into the other system, which uses it to fetch the rest.
4. **The system acts for its people:** one system credential, the system names the person it acts for (ADR-0510 option 4, ADR-0475).
5. **The system asks for its reach** with Rich Authorization Requests (RFC 9396).
6. **Nothing new:** document the five steps better.

## Decision outcome

Chosen option: **1, "connected systems"**, because it is the only option that removes every hand-copied value, keeps each person's work on their own grant, and writes nothing before an administrator approved it; option 5's question — how narrow — is answered by the approver on the consent screen instead of by the system's request.

### 1. A system names itself by its address

A connected system's `client_id` is an `https` URL of its own, the address of a JSON document it publishes (the OAuth Client ID Metadata Document draft). The document carries:

- `client_id` — equal to the document's own URL;
- `client_name` — shown on the consent screen beside the URL's host;
- `redirect_uris` — each on the origin of `client_id`, compared whole, as `oauthClient.RedirectURIs` is today;
- `jwks_uri` or `jwks`, and `token_endpoint_auth_method: "private_key_jwt"` — the system authenticates at the token endpoint with a signed assertion (RFC 7523) instead of a secret, so **no shared secret exists** to copy, leak or rotate by hand. Atlas accepts EdDSA (Ed25519, RFC 8037) and ES256 assertions, each used once within its lifetime. A key is rotated by changing the document.

Atlas reads the document when an authorization request names such a `client_id`:

- **Fetching is bounded.** `https` only, a public address unless an operator allows private ones (a new setting, `ATLAS_CLIENT_METADATA_ALLOW_PRIVATE`), no redirect, a size and a time limit registered as budgets (ADR-0291), a throttle per host. The document is cached for its HTTP lifetime, at most an hour, and read again on every approval.
- **Nothing durable is written for a request.** The client record — a third kind beside the administrator-entered client and the self-registered one (`Dynamic`) — is saved in the run-loop turn that saves the approved grant, never earlier. A stranger can make Atlas read a document; they cannot make it remember anything.
- **The consent screen names the host** (`wiki.example.org`) above the name the document claims, because the host is what TLS vouches for and the name is only what the document says.

A client an administrator entered by hand (ADR-0200) keeps working unchanged; self-registration stays off by default.

### 2. The system grant: the resource `…/system`

A fourth canonical resource beside the server, `/mcp` and `/tasks` (ADR-0510 §3): the server's origin followed by `/system`, with its protected-resource metadata at `/.well-known/oauth-protected-resource/system`.

- **Only a person holding `admin` approves it.** Anyone else is told on the consent screen that an administrator has to connect the system.
- **The system asks for capabilities** in the standard `scope` parameter. Only the narrow mintable scopes a system reads with are accepted — initially `diagrams` (ADR-0493), `tasklist` and `events` (ADR-0510); `full`, `worker` and every other scope are refused for this resource.
- **The approver chooses what it reaches.** The consent screen lists each requested capability with a box to untick and the reach it may be narrowed to: projects for `diagrams` (as minted today) and for `tasklist`, and projects for `events`, which then receives the **task facts of those projects only** and no order outcomes or grant events. A narrowed `events` grant therefore closes ADR-0510's second open point for the systems that use it. The reach is stored with the grant and enforced where the scope's reach is enforced today (`api/apitokenscope.go`); handlers still never read a scope (ADR-0493).
- **The grant carries its approver** (ADR-0196: a token carries a person). Their roles are snapshotted and maintained as for every grant: a disabled account revokes it, a role change rewrites it. When the approver stops being an administrator, the grant is revoked and the Console says so; another administrator connects the system again with one approval.
- **Ended or out of reach are told apart.** A system that keeps copies of tasks must not take a task that left its reach for one that ended. Reading one task by key with a system grant therefore answers a task that ended — completed or cancelled, while the history still holds it (ADR-0115) — as ended, and a task outside the grant's reach as not found, as it is today for anything the caller may not see.
- **One system grant per client.** A new approval replaces the previous one in the same run-loop turn and withdraws it.
- **Audited** as `system.connect`, `system.change` and `system.disconnect`, with the approver, the capabilities and the reach.

### 3. Pre-consent: the people follow without a consent screen

The consent screen of a system grant carries one more choice: **"Let people connect their accounts without asking each of them."** It applies to the person resources the system's request names — today `…/tasks` (ADR-0510 §3).

- When pre-consent stands, a person's authorization request from that client for `…/tasks` is approved **without the consent page** once the person is signed in, by password or single sign-on. The grant is still that person's own: their account, their roles, their tasks. Atlas still decides every claim and completion (ADR-0317).
- `prompt=none` is honoured for these requests: without a session, Atlas answers `login_required` instead of showing a page, so a system can try in the background and send the person through a sign-in only when needed.
- **The system learns what was approved** from `GET /api/v1/auth/me` read with its system grant: the approver, the capabilities with their reach, and whether pre-consent stands. It needs that to tell a task that left its reach from one that ended, and to know whether to send people through a silent connection.
- **A withdrawal sticks.** A person who withdraws such a grant is recorded as having declined that client; pre-consent no longer applies to them, and the consent screen asks them again if they connect themselves.
- Pre-consent ends with the system grant. A disconnect may also withdraw every person's grant of that client (§4).

### 4. Seeing and ending a connection

- **Console → Connected systems** (administrators): one row per system — its host and name, who approved it and when, its capabilities and reach, whether pre-consent stands, how many people are connected, when it last used Atlas. From there an administrator changes the reach (a new approval), turns pre-consent off, or disconnects: the system grant is withdrawn and, if chosen, the grants of every person for that client.
- **Console → Connected accounts** (everyone): the systems one is connected to, those pre-approved for one, and a withdrawal for each.
- A system may give its grants back itself by RFC 7009 (ADR-0510 §5).

### 5. The procedure is published

A page of the Console's documentation describes what a system implements: the document it publishes, the two authorization requests (the system grant and a person's grant), the resources and scopes, `prompt=none`, the token endpoint with `private_key_jwt`, revocation, and the errors it may receive. The wiki's implementation (its docs/adr/0152, in that repository) is the first and the reference.

### 6. Not decided here: the other direction

ADR-0498 lets a process write into a wiki through a Worker that holds a token of a technical wiki account, set up by hand. The same connection could carry that credential the other way — the connected system offering a credential for a technical account when the administrator approves, Atlas keeping it in its vault (ADR-0069). That needs the connected system to act as an authorization server for Atlas, or a credential exchange of its own, and is left to a record of its own.

### As built

Nothing yet. Suggested order, each step usable on its own:

1. Client ID metadata documents: fetching, the third client kind, `private_key_jwt` at the token endpoint, the host on the consent screen.
2. The resource `…/system` with capabilities and reach, the reach of `tasklist` and `events` by projects, an ended task told from one out of reach, what was approved in `auth/me`, and the Console page *Connected systems*.
3. Pre-consent, `prompt=none`, the declined marker, and *Connected accounts* listing pre-approved systems.
4. The published procedure.

### Consequences

- **Positive:** connecting a system is one approval in Atlas started from the other system; no secret exists outside the system that holds it; what a system reads is chosen and seen in one place; the `events` reach by projects narrows what a system reads to the task facts it needs; people connect without a consent screen yet keep a grant, and a withdrawal, of their own.
- **Negative / trade-offs accepted:** Atlas makes outbound requests on an unauthenticated route — bounded and throttled, but a new kind of reach into the network, which is why private addresses are off by default; a system grant ends with its approver's administrator role, so a departing administrator disconnects the systems they approved until another one approves again; pre-consent means a person's tasks reach a system before that person approved it.
- **Follow-ups / risks to watch:** the metadata document draft may change before it is final (open question 1); a document host that answers slowly holds an authorization request up to the time budget; clock skew between the systems affects `private_key_jwt` assertions, which are accepted within a small leeway.

## Pros and cons of the options

### Option 2: open self-registration

Strongest form: it exists, it is a finished standard, and every MCP client already speaks it; opening it removes the hand-made client today without writing a line.

- Good: no new code for the client part.
- Bad: it is the open write endpoint `oauthregister.go` declined to open by default, for every caller on the internet, to save one administrator one step; the registered client is "self-registered", which the consent screen has to present as unvetted — the opposite of what an administrator's connection is.
- Bad: it does nothing for the three tokens, which are most of the work.

### Option 3: a pairing code

Strongest form: one value copied once instead of five, no fetching of documents, no keys, buildable in days; a pairing code can be short-lived and single-use.

- Good: simple on both sides; works for systems that cannot publish a document.
- Bad: still a secret copied by a person, and the one that unlocks everything else; the other system has to store the credentials it fetches with it, which are again tokens without a person; nothing standard to implement against, so every system learns Atlas's own exchange.

### Option 4: the system acts for its people

Strongest form: nobody but the administrator does anything; people who never sign in to Atlas still have their tasks completed in their name; one credential is simpler to hold than one per person.

- Good: zero steps for people.
- Bad: Atlas would believe the system's word about who acts — ADR-0510 rejected exactly this as its option 4, because a wrong mapping or a compromised system completes any task as anyone, and "Atlas decides who works a task" would become "the system decides, Atlas records". The maintainer decided against it (decision 3).

### Option 5: Rich Authorization Requests

Strongest form: RFC 9396 is the standard way for a client to say precisely what it wants — which projects, which kinds of fact — and the approver sees exactly that.

- Good: the narrowing travels in a standard field; a system that knows what it needs asks for no more.
- Bad: the system rarely knows Atlas's projects before it is connected, so it would ask broadly and the approver would narrow anyway; Atlas's reach is chosen by the person who mints a credential (the API token form, ADR-0493), and the consent screen continues that rather than adding a second language for it. Can be added later for systems that do know.

### Option 6: nothing new

Strongest form: the five steps work, are built and tested, and a better guide costs an afternoon.

- Good: no new surface.
- Bad: the maintainer asked for fewer steps; five hand-copied values with no check that they arrived is where a connection fails without saying why.

## Links

- builds on ADR-0200 (Atlas as an OAuth resource and authorization server), ADR-0510 (tasks for a connected system, its `…/tasks` resource and RFC 7009 revocation), ADR-0196 (a token carries a person)
- extends ADR-0194 and ADR-0493 (scopes and their reach) with a reach by projects for `tasklist` and `events`
- relates to ADR-0291 (budgets), ADR-0069 (the vault), ADR-0210 and ADR-0399 (sign-in, single sign-on on the consent page), ADR-0475 (identity-aware execution), ADR-0498 (the wiki as a Worker Type; §6)
- the first system: wikiwikibangbang's record "A process engine is connected with one approval" (github.com/dbuchs7/wikiwikibangbang, docs/adr/0152)
- standards: RFC 6749 and RFC 7636 (code flow with PKCE), RFC 7523 (JWT client authentication), RFC 7009 (revocation), RFC 8414 and RFC 9728 (metadata), RFC 8707 (resource indicators), RFC 9396 (rich authorization requests; option 5), the OAuth Client ID Metadata Document draft
