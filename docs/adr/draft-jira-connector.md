# ADR-DRAFT: Atlas Jira connector

- **Status:** Proposed
- **Date:** 2026-08-27
- **Deciders:** Atlas maintainers

## Context and problem statement

Atlas already reaches the two ITSM/collaboration systems its early users named — BMC
Remedy for incidents (ADR-0106) and SharePoint for list items (ADR-0141). Jira is the
third, and by volume the first: an issue tracker is where most organizations record the
work a process asks a human to do, and a workflow that provisions an account, imports a
file or evaluates a decision routinely has to open a ticket, comment on it, move it
through its workflow, and read back what happened.

Today a model can only reach Jira through the generic REST connector (ADR-0067). That
works — Jira's API is HTTP and JSON — but it makes every model author do the same four
things over and over, and get them wrong in the same four ways:

- **Author the URL.** `https://acme.atlassian.net/rest/api/2/issue` is knowledge about
  Jira, not about the process. Written into the model it also pins the *instance*, so a
  move from one Jira to another is an edit of every task that touches it.
- **Author the credential wiring.** Jira Cloud authenticates with HTTP Basic over
  `email:apiToken`; Jira Data Center with a bearer personal access token. A REST task can
  express both, and both then live as an authored auth block per task.
- **Author the body shape.** Creating an issue is
  `{"fields":{"project":{"key":…},"issuetype":{"name":…},"summary":…}}` — two of those
  four values wrapped in an object each. It is the kind of shape that is right or silently
  creates the wrong thing.
- **Author the operation.** Transitioning an issue is a POST of
  `{"transition":{"id":…}}` to a sub-resource; assigning is a PUT of
  `{"accountId":…}` to another. Nothing about "REST" tells an author these exist.

The question this record answers: does Jira get a first-class connector kind, and if so
which operations does it name?

## Decision drivers

- **A model should name the work, not the protocol.** The same argument ADR-0106 made for
  Remedy and ADR-0141 for SharePoint: the value a connector adds is at the model level.
- **No endpoint and no secret in a model** (ADR-0036/0041). The Jira base URL and the
  credential belong in the managed connector store and the vault, so moving from a test
  Jira to production is a Console edit, not a redeploy.
- **One kind, not one kind per operation.** Jira's operations share an instance, a
  credential and an error envelope; splitting them into reserved job types the way clio
  splits read/write/query would cost seven indices for one integration.
- **Cloud and Data Center from the same model.** The two differ in authentication and in
  the newest API version, not in what an issue is.
- **The engine must not grow a Jira dependency on the hot path** (I1/I2): a connector task
  compiles to a job, and an in-process worker performs the call after fsync (ADR-0007).

## Considered options

1. **Keep using the REST connector.** Document a recipe and ship nothing.
2. **One reserved job type per Jira operation**, like clio's write/query/read.
3. **One reserved job type with a modeled `operation`**, like the Entra connector
   (ADR-0172) and the SCIM/LDAP/AD connectors.

## Decision outcome

Chosen option: **"one reserved job type with a modeled operation"**, a managed connector
kind `jira` that runs in the engine and is offloadable.

- `<atlas:jiraConnector connector="…" operation="…" …/>` on a service task compiles to a
  connector task carrying the reserved `io.atlas.jira` job type (index 25).
- The seven operations are `create-issue`, `get-issue`, `update-issue`,
  `transition-issue`, `add-comment`, `assign-issue` and `search`. They cover the whole
  loop a process actually runs: open a ticket, read it, change it, move it, say something
  on it, hand it to somebody, and find the ones that match.
- What the operation needs is a table (`connector/jira.Ops`), mirrored by the compiler's
  own copy with a drift test between them, so a new operation is a row rather than three
  edits in three files that can disagree.
- The connector record holds the Jira base URL (`endpoint`) and a `credentialsRef` naming
  a vault bundle: `{"email":…,"apiToken":…}` for Cloud (HTTP Basic, which is how Atlassian
  documents an API token) or `{"token":…}` for a Data Center personal access token
  (bearer). Which of the two a bundle is, is decided by which fields it carries — there is
  no `method` to get wrong, because the fields already say it.
- The transport is Jira's **REST API v2** (`/rest/api/2`). v3 differs from it in exactly
  one thing that matters here: a description or comment body must be an Atlassian Document
  Format tree rather than a string. Making every model author ADF to write one sentence
  is the opposite of what this connector is for, and v2 is served by both Cloud and Data
  Center.
- Every authored value is literal-or-FEEL and evaluated over the variables the task sees,
  up its scope chain (ADR-0068), off the hot path — the same contract as every other
  connector's fields.
- Extra issue fields (`fields`) are name/value pairs, and a value's **FEEL kind decides
  its JSON shape**: a value that evaluates to an object or a list is sent as that object
  or list (so `labels`, `priority`, a custom field with an id all work), anything else as a
  string. The alternative — sniffing whether the resolved text parses as JSON — would make
  a summary that happens to start with `{` a different kind of field.
- `create-issue`, `get-issue`, `add-comment` and `search` return what Jira returned into
  the task's result variable; the three that Jira answers with `204 No Content`
  (`update-issue`, `transition-issue`, `assign-issue`) write nothing, which is what the
  Modeler's result-variable hint says per operation.

### Consequences

- **Positive:** a Jira task is four fields in the properties panel instead of a URL, an
  auth block and a hand-built JSON body. The instance and its credential move in the
  Console. A deploy catches a connector-name typo, because the kind's job type is in the
  managed registry (ADR-0158).
- **Positive:** the operation set is the whole loop, so a process can also *wait on* Jira
  by polling `search`/`get-issue`, rather than only firing tickets into it.
- **Negative / trade-offs accepted:** delivery is at-least-once, as for Remedy and
  SharePoint. A crash between "Jira created the issue" and "job completed" replays the
  create and can duplicate an issue. The job key rides along as an `X-Request-ID` header
  for a downstream de-duplicator; a real idempotency key is a follow-up.
- **Negative:** v2 means no ADF, so a rich-text description is not expressible. Wiki
  markup is, which is what a process-generated description actually needs.
- **Follow-ups / risks to watch:** an out-of-process worker for the kind (ADR-0164/0168)
  once the credential handover exists, as it does for mail and Remedy; webhook-driven
  *inbound* Jira events, which are a message-correlation question (ADR-0020) rather than a
  connector one; attachments, which need the multipart upload endpoint.

## Pros and cons of the options

### Option 1 — keep using the REST connector
- Good: nothing to build; the generic connector already reaches Jira.
- Bad: every model repeats the base URL, the auth block and the body shape, and each is a
  place to be wrong. Moving Jira instances edits every model.
- Bad: no deploy-time check of anything, because to Atlas it is an opaque URL.

### Option 2 — one reserved job type per operation
- Good: a worker subscribes to exactly the operations it serves.
- Bad: seven reserved indices for one integration, each one permanent (position is
  identity in `reservedJobTypes`). clio's three are the precedent, and three is already
  the outer edge of what that shape is worth.
- Bad: the operations share everything — instance, credential, error envelope — so the
  split buys separation nothing needs.

### Option 3 — one job type, modeled operation *(chosen)*
- Good: one reserved index; the operation table is the single description the compiler,
  the panel and the worker all read.
- Good: matches the shape the directory connectors already settled on (SCIM, LDAP, AD,
  Entra), so there is one way to read a multi-operation connector in this codebase.
- Bad: `--offload-connectors jira` moves all seven at once; per-operation placement is not
  expressible. No operator has asked for it, and the SQL and directory kinds accept the
  same.

## Links

- relates to [ADR-0007](0007-job-worker-protocol.md) — connector work runs as a job, after fsync
- relates to [ADR-0036](0036-clio-connector.md) and [ADR-0041](0041-connector-management-and-secret-store.md) — a model names a connector; the endpoint and secret are managed
- relates to [ADR-0067](0067-service-task-connector-catalog.md) — the generic connector this one specializes
- relates to [ADR-0106](0106-bmc-remedy-connector.md) and [ADR-0141](0141-sharepoint-connector.md) — the two managed application connectors this one follows
- relates to [ADR-0172](0172-entra-id-connector.md) — the operation-table shape reused here
- relates to [ADR-0149](0149-bounded-connector-call-budget.md) — the call budget a Jira request runs under
