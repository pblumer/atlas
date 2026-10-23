# Identity-aware Worker execution: the worked example

The scenario of [ADR-0475](../../adr/0475-identity-aware-worker-execution.md)
§14, as files you can deploy: a person asks for a document in the HR library to be
deleted, a manager approves that exact deletion, and the SharePoint Worker deletes it as
the person who asked — through their own Microsoft account, with a token Atlas released
for that one permitted action. `go test ./api -run IdentityAwareSample` deploys the
model and asks the policies the questions below, so this page cannot drift from what
the code does.

| File | What it is |
|---|---|
| [`dokument-loeschen.bpmn`](dokument-loeschen.bpmn) | The process: delete, or — when the policy asks for approval — a user task and back |
| [`hr-documents.policy.json`](hr-documents.policy.json) | Members of `grp_documents` may delete in the HR site with one manager's approval and a sign-in at Microsoft in the last 90 days |
| [`sharepoint-roles.policy.json`](sharepoint-roles.policy.json) | The same question asked as a DMN decision table: which role may do which operation |

## What has to exist first

1. **Linking is configured** (`atlas serve --link-microsoft-tenant … --link-microsoft-client-id …
   --link-microsoft-client-secret … --link-microsoft-scopes "https://graph.microsoft.com/Sites.ReadWrite.All"`).
   The app registration needs the delegated permission, admin consent where the tenant
   requires it, and the redirect URI `https://<atlas>/auth/link/microsoft/callback`.
   Delegated `Sites.Selected` narrows what a released token can reach, at the cost of a
   per-site grant.
2. **Authentication is on** and the SharePoint Worker runs **supervised**: a delegated
   credential is released only to a Worker Instance Atlas started.
3. **A configured Worker `sp-prod`** of type SharePoint, with its identity policy set
   (Console → Administration → *Security*, or):

   ```http
   PUT /api/v1/configured-workers/{id}/identity
   {"mode": "delegated-per-job", "actAs": "initiator", "enforcement": "enforce"}
   ```

4. **Groups** `grp_documents` (the requesters) and `grp_managers` (the approvers).
5. **The policy published** (Console → Administration → *Security*, or):

   ```http
   PUT  /api/v1/security/policies/hr-documents/draft     (body: hr-documents.policy.json)
   POST /api/v1/security/policies/hr-documents/publish
   ```

6. **Each requester links their account** (account menu → *Connected accounts*, or
   `GET /auth/link/microsoft/start` while signed in) and **allows `sp-prod`** to act through it:

   ```http
   PUT /api/v1/connections/{connection}/workers
   {"workers": ["sp-prod"]}
   ```

## What happens

| Step | Who | What is recorded |
|---|---|---|
| Alice starts `dokument-loeschen` with `docPath` | Alice | `StartedBy = usr_alice` on the instance's activation event |
| The Worker leases the delete and asks `POST /jobs/{key}/authorize` | supervised Worker | `AuthorizationDecided`: Deny, `APPROVAL_REQUIRED`, policy `hr-documents` v1, executed for `usr_alice`, as `microsoft:<tid>:<oid>` |
| The server opens an approval and throws `ATLAS_APPROVAL_REQUIRED` | server, same batch | `ApprovalRecorded` (requested), bound to the deletion's digest |
| Alice tries to approve her own deletion | Alice | refused (403), nothing recorded |
| Bob, a manager, approves | Bob | `ApprovalRecorded` (decided, granted) |
| The user task completes; the delete runs again | — | a new job, a new lease |
| The Worker asks again | supervised Worker | `AuthorizationDecided`: Permit, approval used; then a Graph token for Alice's account |
| The Worker deletes with that token and completes | supervised Worker | `JobCompleted` |

Every row after the first is readable at `GET /api/v1/instances/{key}/authorizations`
and `GET /api/v1/instances/{key}/action-approvals`, and survives a restart: the
decisions are events in the log and replay without evaluating any policy.

## The negative cases

| Situation | Answer |
|---|---|
| Alice has no linked account, or did not allow `sp-prod` | Deny `NO_CONNECTION`; the job parks on an incident; nothing reaches Graph |
| Alice's account in Atlas is disabled | Deny `SUBJECT_DISABLED` |
| Alice revoked the connection | the revoked connection is no candidate: Deny `NO_CONNECTION` |
| Bob's approval is older than a day | Deny `APPROVAL_EXPIRED`; a new request is opened |
| Bob rejected | Deny `APPROVAL_REJECTED` |
| A variable `approved = true` | changes nothing: approvals are facts in the log |
| The deletion's path changes after approval | another digest: the approval does not count |
| The policy is broken or missing | Indeterminate or NotApplicable: refused |
| A Worker completes without asking | completion refused (409) |
| An external Worker Instance asks | 403: delegated credentials go to supervised instances only |
| Restart between decision and Graph call | the lease expires; the next lease is decided afresh |
| Restart after the Graph call, before completion | the next lease is decided again and the delete may be sent twice (at-least-once) |

## The two policies together

Published on its own, `hr-documents` gives the scenario above. Publishing
`sharepoint-roles` beside it shows what deny-overrides means: its table denies a
member's delete outright (`DELETE_NOT_ALLOWED`), and a Deny from any policy wins, so
only a *manager's* deletion reaches the approval step — and then needs another
manager, because the approver may never be the person the deletion runs for.

## What this does not show

Bob answers under **Approvals** in the account menu (or at the route above); the page
names the exact deletion, and Alice sees it there as asked on her behalf, with no answer to give.
Google, and every Worker Type but SharePoint, are not governed by this build. See the
record's §11 for what is enforced at the credential and what is cooperative.
