# ADR-0510: Tasks for a connected system — facts on the feed, a scope to list them, a grant to work them, and one's own groups

- **Status:** Proposed
- **Implementation:** Partial
- **Date:** 2026-10-28
- **Deciders:** requested 2026-10-28 by the maintainer, who wants wikiwikibangbang (github.com/dbuchs7/wikiwikibangbang) to show Atlas's user tasks as tasks of the wiki — on its boards, in tables in its pages — and to complete them there with their forms. The maintainer decided four things this record rests on: Atlas alone decides whether a person may work a task; each person connects their wiki account to Atlas once, by OAuth, with the mail address only a suggestion; groups serve only to decide who sees a task in the wiki, never rights in the wiki; and the coupling must work whatever the people and groups come from (a directory, Atlas itself, local accounts, any identity provider). Asked for the lifecycle facts and the groups in `auth/me` to be built with the rest. On 2026-10-29, building the wiki's side of the grant surfaced three gaps, and the maintainer asked for all three to be closed as follow-ups of §3 (§5). Proposed for the maintainers' acceptance.
- **Open question:** two points. (1) **A submission is checked against its form only in the browser.** `POST /api/v1/tasks/{key}/complete` checks who completes (ADR-0317) and a directory field's value (ADR-0473), not whether a required field is filled or a value fits the form. The Tasks app checks the rest with form-js; a second surface that submits forms — the wiki, with its own copy of form-js and without Atlas's form modules — checks it with its own. If Atlas is to decide *what* is accepted as it decides *who*, the completion has to check the submission against the form on the server; that is a record of its own, and whether it must land before a second surface goes live is the maintainers' call. (2) **An events token reads every fact of the feed.** The follower of task facts needs the engine's facts only; an `events` token without a reach also reads every order outcome and every grant. A reach that names kinds of fact rather than catalogues would narrow it; it is not built here.
- **Question checked:** 2026-10

## Context and problem statement

A wiki that keeps its own tasks on boards and in tables wants Atlas's user tasks among them: a task the engine opens appears as a task of the wiki with the labels its administrator chose; a page lists the tasks a filter selects; a card moved to *done* completes the task in Atlas, showing the task's form first where the task has one. The wiki's side is its own decision record ([wikiwikibangbang, docs/adr/0151](https://github.com/dbuchs7/wikiwikibangbang/blob/main/docs/adr/0151-user-tasks-of-an-engine-become-linked-tasks.md)). What it needs from Atlas, and what Atlas had on 2026-10-28:

- **To learn that a task opened, changed hands or ended.** Nothing told it. The event catalogue (ADR-0435) and the feed (ADR-0429 §5) carried orders, rights and incidents, no task. `GET /api/v1/tasks` lists open tasks newest first with a backward cursor only, and a task that ended answers 404 whether it was completed or cancelled. A follower could only read every open task again and compare.
- **To list the open tasks with a credential that can do nothing else.** The only mintable scope that reaches `/api/v1/tasks` is `full`: an administrator's `full` token carries `modeler`, `operator` and `user`, so it can also complete, cancel, deploy and read variables.
- **To work a task as the person who works it.** Completing records the caller as `completedBy` and is refused to a caller who does not hold the task (ADR-0317). A machine credential completing for people would record the machine and would replace Atlas's check with the wiki's. A person's OAuth grant (ADR-0200) records the person and keeps the check — but a grant for the server reaches everything the person can do, and a grant for `/mcp` reaches only the transport.
- **To know which groups a person is in.** A task is offered to candidate groups, named by group id or by name (ADR-0318, ADR-0421). `GET /api/v1/auth/me` answered the account without its groups, and `GET /api/v1/groups` is an administrator's.

The question: **what is the smallest surface through which a connected system follows the tasks, lists them, and lets each person work their own — with Atlas deciding every time?**

## Decision drivers

- **Atlas decides.** Who sees a task and who may work it is Atlas's answer, given to the person's own credential; nothing here lets another system answer it instead.
- **A credential reaches what its holder needs and nothing more** (ADR-0194). The reach of every scope stays readable in `api/apitokenscope.go` alone; handlers never read a scope (ADR-0493).
- **The feed carries facts, not content.** It names people by id only (ADR-0314); a receiver that needs more asks Atlas as the person it acts for, as it does for an incident's message (ADR-0435).
- **The fold stays a function of the log** (I4, I6): a row is written by `applyToState` from the record and the state the same pipeline built, live and on replay alike.
- **Whatever the source of people and groups.** A directory mirror (ADR-0332), groups made in the Console (ADR-0180) and single sign-on (ADR-0210) all end in the same group store; the answer reads that store.

## Considered options

1. **Four pieces: task facts on the feed, a `tasklist` scope, a `tasks` resource for OAuth grants, and one's own groups in `auth/me`.**
2. **Webhooks per task**: a listener on user tasks, configured in the model or the Console, posting to an address.
3. **A forward cursor on the task list** (`GET /api/v1/tasks?after=`) instead of facts.
4. **Completion on behalf of a named person** by a trusted system's token.
5. **Nothing new**: a `full` token and a grant for the whole server.

## Decision outcome

Chosen option: **1**, because each piece answers one of the four needs with a mechanism Atlas already trusts — the feed, a scope's allowlist, a grant's resource, the group store — and none of them lets anything but Atlas decide.

### 1. Task facts on the feed

Five types, each a fact of the engine (`Kind: platform`, source `…/engine`), folded by `applyToState` from the job records of the user-task type (`compiler.UserTaskJobTypeIndex`):

| Type | Folded from |
|---|---|
| `atlas.task.created` | `IntentJobCreated` of a job that was not open: a job re-created after an incident is not a new task |
| `atlas.task.assigned` | `IntentJobAssigned` with an assignee: a claim, or a claim for somebody |
| `atlas.task.unassigned` | `IntentJobAssigned` without one: given back |
| `atlas.task.completed` | `IntentJobCompleted` |
| `atlas.task.cancelled` | `IntentJobCanceled`: an interrupting boundary event, a cancelled or terminated instance |

- **The row** is keyed by the log position of the record, like every feed row, and holds the job, its key, the element it waits on and the definition its instance ran, read from the element instance through the same batch — the element instance is gone by the time a receiver reads a completed task, so the fold keeps what the envelope needs.
- **The payload:** `taskKey`, `processInstanceKey`, `elementInstanceKey`, `elementIndex` and `assigned` always; `createdAt` where the job recorded it, `dueDate` for a task with a due date, `candidateGroups` as the task holds them (comma-separated, by id or name, the model's value for a job written before it carried its own), and, while the definition is deployed, `processDefKey`, `processId`, `version`, `elementId` and `name`. The subject is `tasks/{taskKey}`.
- **Not the assignee's name.** A model writes an assignee as a username or an expression's value, not a principal id, and the feed names people by id only (ADR-0314). `assigned` says whether somebody holds the task; who, the receiver asks `GET /api/v1/tasks/{key}` with a credential that may see it. Nor the form, the variables, the content, the documentation or who completed it: `completedBy` is written as a variable after the job record, where the fold of that record cannot see it.
- **Who reads them:** the platform rule of incidents — the `feedreader` role or an `events` token without a reach; a token narrowed to catalogues never receives them. Push subscriptions carry them as they carry incidents.
- **Volume:** two to four rows per user task, kept for the feed's retention.

### 2. The `tasklist` scope

A mintable scope reaching `GET /api/v1/tasks` and `GET /api/v1/tasks/{key}` and nothing else. It carries its minter's roles, as `diagrams` does, so what it lists is what those roles see: an administrator's token, holding `operator`, lists every open task. It cannot claim, complete, read a form, a variable or an instance. It is what a system that keeps a copy of the open tasks holds to compare its copy with Atlas — the facts say what changed, the list says what is, after a restart or a gap longer than the feed's retention.

The list answers `?content=1` to it as to anyone whose roles allow: the short values of the fields each task's form asks for. The allowlist matches routes, not queries, and a handler does not read a scope; a list without content for this scope would be a route of its own, as ADR-0493's counts are. Not built: the follower asks for no content, and a leak of this token reads those short values of the open tasks it sees.

### 3. A grant for tasks: the resource `…/tasks` and the scope `tasks`

A third canonical resource for OAuth authorization, beside the server and `/mcp`: the server's origin followed by `/tasks`. Its protected-resource metadata is served at `/.well-known/oauth-protected-resource/tasks`, and a grant for it carries the scope `tasks`, which is not mintable — it follows from the resource the person approved, as `mcp` does. It reaches:

- `GET /api/v1/auth/me` — who the person is, with their groups;
- `GET /api/v1/tasks`, `GET /api/v1/tasks/{key}` — their tasks, as Atlas shows them to them;
- `POST /api/v1/tasks/{key}/claim`, `…/unclaim`, `…/complete` — working them, refused where Atlas refuses the person;
- `POST /api/v1/tasks/{key}/form-data/{field}/query` — a directory field's lookups while filling the form (ADR-0473);
- `GET /api/v1/forms/{id}` — the form a task names;
- `GET /api/v1/instances/{key}/variables` — the values a form opens with, scoped as for the person (ADR-0275).

The consent page says what is asked: to see one's tasks, take them on, give them back and complete them with their forms, as oneself, and nothing else. The token is the person: their roles and groups, so an operator's grant reads on these routes what an operator reads. Disabling or deleting the account revokes its grants (ADR-0200), and the connected system finds its next renewal refused.

### 4. One's own groups in `auth/me`

`GET /api/v1/auth/me` adds `groups` to the user: `[{"id","name"}]`, every group the group store holds the caller as a member of at the moment of the request, by id and name because a task names its candidate groups by either. Only the caller's own; nothing about the other members. With the person's `tasks` grant a connected system learns which groups that person is in, however the groups came about.

### 5. Follow-ups the first client needed: single sign-on on the consent page, giving a grant back, a throttle of its own

Building the wiki's side of §3 — each wiki user connects their Atlas account once, with the authorization code flow and PKCE, for `…/tasks` — met three gaps in the grant ADR-0200 built. Each is closed here, as a follow-up of the connected-system grant rather than a decision of its own.

- **A person who signs in only through single sign-on reaches the consent page.** `GET /oauth/authorize` offered a password form and nothing else, and a federated login (ADR-0210) could end on the Console or the portal only (ADR-0399), so such a person could never come back to the request they were asked to approve. The consent page now offers each identity provider the Console's sign-in offers (`GET /api/v1/auth/providers`), and starts it with its own request as the page to come back to. The callback admits exactly the path `/oauth/authorize` as a return target with a query: compared as a string, refused with a backslash, a fragment or a control character, bounded to 2 KiB, and its query parsed and written back with `url.Values.Encode`, so nothing but that path on this origin can come out. What the query says is not the login's to decide: the page re-checks every parameter as it always has, shows a fatal error without following it, and grants nothing until the person presses Allow.
- **A client can give a grant back** (RFC 7009). `POST /oauth/revoke` is public like `/oauth/token`, authenticates the client the same way (`client_secret_post`), and takes `token` with an optional `token_type_hint` that is only a hint — the token's prefix says which kind it is. A refresh or access token of a grant **of that client** withdraws the whole grant, through the same run-loop turn and audit event as `DELETE /api/v1/oauth-grants/{id}`; the access token's expiry is not consulted, because an expired one still names a grant whose refresh token works. Every other token — unknown, rotated away, already withdrawn, another client's — is answered 200 alike, so presenting a token teaches nothing about it (§2.2); a bad client is 401 `invalid_client`, a missing token 400 `invalid_request`, and a store that could not delete 503, which tells the client to assume the grant stands and try again. The metadata advertises `revocation_endpoint` and `revocation_endpoint_auth_methods_supported: ["client_secret_post"]`. Without it a wiki whose user disconnects destroys its copy of the refresh token and leaves the grant standing, which ADR-0200 says a grant must not become.
- **The token and revocation endpoints throttle on a bucket of their own.** They drew on the public forms' bucket, twenty at once and then one a second per address — right for a person at a form, wrong for a server renewing all its people's tokens from one address, all at once after its own restart, and shared, so form traffic through the same proxy refused those renewals too. The new bucket holds 300 at once and earns back 10 a second per address: a restart of a client with three hundred connected people in one go, a thousand in seventy seconds, and no gain at that rate for anybody guessing a 256-bit secret. It is two budgets, `TokenEndpointBurst` and `TokenEndpointRate` (`ATLAS_LIMIT_TOKEN_ENDPOINT_BURST`, `ATLAS_LIMIT_TOKEN_ENDPOINT_RATE`), and a spent bucket still answers 429 `temporarily_unavailable`.

### As built

- `state/feed.go`: the kinds `FeedTaskCreated` to `FeedTaskCancelled` and `PutFeedTask`, a row holding the definition, the job's key and its element before the job.
- `engine/apply.go`: `userTaskFeedKind` and `putTaskFeedRow`, in the job arm of `applyToState`.
- `eventcatalog/catalog.go`: the five entries (`taskEntry`), with the handbook and `docs/runtime-contract.md` regenerated from them; `api/taskfeed.go`: the envelope.
- `api/apitokenscope.go`: `tasklist` (mintable) and `tasks` (from the resource); `api/oauthserver.go`, `api/oauthgrantstore.go`, `api/server.go`: the resource `…/tasks` and its metadata; the consent page and the AI-access page name what such a grant reaches; the token page offers `tasklist`.
- `api/users.go`: `meUser`, who am I with one's own groups.
- Tested by `state` (`TestATaskRowKeepsItsJobElementAndDefinition`), `engine` (`TestAUserTasksLifeLeavesOnTheFeed`, with its replay, and the cancelled and service-task cases), `api` (`TestAUserTasksLifeLeavesOnTheFeedAsFactsOfTheEngine`, `TestTheFeedCarriesNoSecret`, `TestTasklistScopeListsTasksAndNothingElse`, `TestATasksGrantWorksThePersonsTasksAsThemAndNothingElse`, `TestAuthMeAnswersOnesOwnGroups`).
- §5, single sign-on on the consent page: `api/oidclogin.go` (`oidcConsentReturn`, `oidcFailedReturn`), `api/web/oauth-consent.html` (`offerProviders`, `consentReturn`). Tested by `TestTheConsentPageIsAReturnTargetWithItsQueryReencoded`, `TestOnlyTheConsentPathCarriesAQuery`, `TestAFederatedLoginReturnsToTheConsentRequest`, `TestTheConsentPageOffersTheProvidersAndComesBack`.
- §5, revocation: `api/oauthserver.go` (`handleRevoke`, the metadata), `api/oauthclients.go` (`withdrawGrant`, which `DELETE /api/v1/oauth-grants/{id}` now goes through too), `api/oauthgrantstore.go` (`grantTokenHash`, `holdsTokenHash`), the route in `api/server.go` and the public allowlist, and taken while the disk is below the reserve (`api/storageguard.go`), since it gives space back. Tested by `TestRevokingEitherTokenEndsTheWholeGrant`, `TestAClientCannotGiveBackAnotherClientsGrant`, `TestRevokingANonTokenAnswersOK`, `TestRevocationJudgesTheClientOutLoud`, `TestTheMetadataAdvertisesRevocation`, `TestAnExpiredAccessTokenStillGivesItsGrantBack`, and the broken-store and malformed-form cases of `TestOAuthSurvivesABrokenDataDirectory` and `TestApproveAndTokenRefuseMalformedInput`.
- §5, the throttle: `limits/limits.go` (`TokenEndpointBurst`, `TokenEndpointRate`), `Server.tokenRate` built after the options in `api/server.go`. Tested by `TestTheTokenEndpointsDoNotShareThePublicFormsThrottle`, `TestTheTokenThrottleTakesARestartingClientsBurst`, `TestTheTokenThrottleIsConfigured`.

### Consequences

- **Positive:**
  - A connected system learns of a task within a push round, lists the open tasks with a credential that can only read them, and works each task as the person who works it; Atlas's checks and records stay exactly as they are.
  - The feed gains the facts a task inbox elsewhere needs, without a second delivery mechanism.
  - A person's groups become knowable to the person, which also serves any other client acting for them.
  - Whoever signs in through single sign-on can approve a connected system, a connected system can give back what it was given when its user disconnects, and a restart of such a system renews its people's tokens without being throttled like a form.
- **Negative / trade-offs accepted:**
  - The feed holds two to four more rows per user task for its retention.
  - `assigned` is a flag, not a name; a receiver that shows who holds a task asks for it.
  - A grant for `…/tasks` reaches the variables route as the person: an operator's grant reads every instance's variables there, as the operator could.
  - The `tasklist` scope reads `?content=1`.
  - The consent page is the one return target of a federated login that carries a query. It is re-encoded and bounded, and what it says stays the client's choice, checked as before.
  - One address may renew three hundred grants at once; a stranger at that address gets the same budget, against secrets it cannot guess.
- **Follow-ups / risks to watch:**
  - The open questions: a server-side check of a submission against its form; a reach of the feed by kind of fact.
  - `completedBy` on `atlas.task.completed`, if a receiver needs it, folded from the variable rather than the job record.

## Pros and cons of the options

### Option 2: webhooks per task

Strongest form: a receiver is told at once, without following a feed; a model could choose per task where to report it, and a receiver needs no credential at all.

- Good: no polling and no cursor on the receiver's side.
- Bad: a second delivery mechanism beside the feed's push (ADR-0433), with its own retries, signing and failures; addresses in models are configuration a deployment carries everywhere; a model that forgets the listener leaves its tasks unannounced, which is the gap this record closes for every model at once.

### Option 3: a forward cursor on the task list

Strongest form: no new state, no new types — the list already exists, and `after=` would let a follower page forward through what opened since it last looked.

- Good: small, and it answers "what opened".
- Bad: it does not answer "what ended" or "what changed hands": a completed task leaves the list without a trace, and telling completion from cancellation is exactly what a copy needs to close its own card correctly. Job keys are not opening order across partitions or re-creations.

### Option 4: completion on behalf of a named person

Strongest form: no grant per person, no token store at the connected system; one trusted credential and a name, and Atlas checks the named person's rights.

- Good: simplest for the connected system, and Atlas still checks the person's rights.
- Bad: Atlas would decide on an identity another system asserts and it cannot verify; a leak of that one credential acts as anyone. It is ADR-0475's territory, decided there for Workers, not here.

### Option 5: nothing new

Strongest form: it works today — a `full` token lists everything, a grant for the server works every task, and nothing in Atlas changes.

- Good: no change.
- Bad: the follower holds a credential that can deploy and cancel; the grant reaches everything the person can do; nothing says what changed, so the follower reads every open task again on every round; and the groups stay unknown, so who sees a task elsewhere is a guess.

## Links

- extends ADR-0435 (the catalogue of the events Atlas emits) and ADR-0429 §5 (the feed)
- extends ADR-0194 (token scopes) and ADR-0200 (OAuth grants); §5 adds RFC 7009 revocation to ADR-0200's authorization server, a third return page to ADR-0210's federated login (after ADR-0399's portal), and two budgets to ADR-0291's registry beside ADR-0197's throttles
- follows ADR-0493 (a narrow scope for a connected wiki)
- relates to ADR-0042 (claim and unclaim), ADR-0180 and ADR-0185 (groups), ADR-0275 (instance variables by object), ADR-0314 (people by id), ADR-0317 and ADR-0421 (who may work and see a task), ADR-0318 (assignment frozen at activation), ADR-0473 (directory fields), ADR-0475 (identity-aware execution), ADR-0498 (the wiki as a Worker Type)
- the consuming side: wikiwikibangbang's decision record on linked tasks (docs/adr/0151 in that repository)
