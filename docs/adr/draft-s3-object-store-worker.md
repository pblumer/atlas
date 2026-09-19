# ADR-DRAFT: An S3 Worker Type — a process puts a file down, finds it again, and hands it out

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-19
- **Deciders:** Atlas maintainers
- **Open question:** how a custom endpoint addresses its buckets. This record derives the
  addressing style from whether an endpoint is configured (AWS → virtual-host, custom →
  path-style), which covers AWS, MinIO, Ceph, Garage, R2 and Wasabi as they are deployed
  today. An S3 gateway that requires virtual-host addressing under its own domain has no
  way to say so, and nobody has yet reported one. A `pathStyle` field on the Worker would
  settle it, and is deliberately not added before the case exists.
- **Question checked:** 2026-09

## Context and problem statement

A process produces and consumes documents. An invoice arrives, a letter is generated, a
report is rendered, a scan is attached to a case. Atlas today has nowhere to put any of
them: a process variable is capped at 1 MiB ([`limits/limits.go`](../../limits/limits.go)),
and everything written into one is persisted in the event log for as long as the
installation keeps its history. There is no artifact store, and
[ADR-0299](0299-worker-type-admission-criteria.md) names that absence as one of the three
missing seam pieces that defer capabilities.

So the documents live somewhere else already — an object store, almost always something
speaking the S3 API — and the process cannot reach it. What operators do instead is one of
three things, and all three are worse than the thing this record proposes:

- **A script task shelling out to `aws s3 cp`.** It works, and it puts the installation's
  credentials in a script task's environment and its file handling in a shell one-liner
  nothing validates.
- **A REST task against a presigned URL somebody else minted.** It moves the problem to
  whoever mints the URL, on a schedule unrelated to the process that needs it.
- **The bytes in a variable.** Which is the 1 MiB ceiling, forever, in the log.

The question this record answers is therefore: **can a process put an object into an S3
bucket, find one again, and hand one to a person — without the bytes passing through the
engine and without the credential leaving the server?**

### What "öffnen" has to mean here

The request that prompted this record asked for a Worker that can *ablegen, suchen und
wieder öffnen* — put down, search, open again. The third verb is the one that decides the
design, because there are two readings and they are not the same feature:

1. **Open into the process.** The object's bytes become a process variable, so FEEL can
   read them. Bounded by the variable budget, which is 1 MiB, which is smaller than most
   of the documents this is for.
2. **Open for a person.** Somebody clicks a link in a user task, a mail or a Discord
   message and the document opens in their browser. Unbounded, because the bytes never
   come near Atlas.

Both are real. A 4 KB JSON manifest a later gateway branches on wants (1); a 40 MB signed
PDF an approver has to read wants (2). This record ships both and is explicit about which
is which, because a Worker Type that offered only (1) would be a Worker Type that cannot
open the documents people actually have.

## Decision drivers

- **The bytes must not become the engine's problem.** Anything that scales with document
  size has to stay outside the log, the variable budget and the run loop.
- **The credential must stay on the server.** A model names a Worker; the access key
  resolves out of the vault at build time and travels no further
  ([ADR-0069](0069-engine-internal-encrypted-secret-vault.md),
  [ADR-0168](0168-connector-work-on-a-worker.md)).
- **The five gates of [ADR-0299](0299-worker-type-admission-criteria.md) are answered
  here, not re-derived.** That record exists precisely so that a new kind states its case
  against a written rule.
- **S3 is a de-facto interface, not a vendor.** MinIO, Ceph RGW, Garage, Cloudflare R2,
  Wasabi, Hetzner and AWS all speak it. A kind that only worked against AWS would be worth
  a fraction of one that works against whatever the installation already runs.

## Considered options

1. **No Worker Type; document the script task.** Tell operators to shell out to the AWS
   CLI or `mc` from a PowerShell/Python task.
2. **No Worker Type; wait for an engine-side artifact store.** Build the seam piece
   ADR-0299 gate 3 names, then decide.
3. **An S3 Worker Type that moves bytes** — every operation reads or writes the object's
   content through a process variable.
4. **An S3 Worker Type that moves references, with a bounded byte path** (chosen): the
   object is addressed by bucket and key; metadata, listings and presigned URLs travel as
   variables; content travels only when the model asks for it and only under a cap.

## Decision outcome

Chosen: **option 4.** A new Worker Type `s3`, reserved job type `io.atlas.s3`, one package
at [`connector/s3`](../../connector/s3), one `managedConnectorKind` entry, eight
operations.

### The eight operations

| Operation | What a process does with it | Answers with |
|---|---|---|
| `put-object` | Put a document the process produced into a bucket | etag, versionId, key, bucket, size |
| `get-object` | Read a small object back into a variable | content + content type, size, etag |
| `head-object` | Ask whether it is there, how big, how old | `exists`, and the metadata when it is |
| `list-objects` | Find objects under a prefix — the "search" | keys, sizes, etags, dates; where to resume |
| `copy-object` | Archive or move one, server-side | etag, key, bucket |
| `delete-object` | Take it away | nothing |
| `presign-get` | **Hand a person a link that opens the document** | url, expiresAt |
| `presign-put` | Hand a person a link that uploads one | url, expiresAt |

The two presign operations are why this design works. They are **pure computation** — a
SigV4 query-string signature, no network call at all — and they produce a URL that opens
the object directly at the store. A 40 MB PDF reaches an approver without one byte of it
entering Atlas, and a scan reaches the bucket the same way, from the browser that has it.

`list-objects` is what the word *search* honestly means against S3: a prefix scan with an
optional delimiter, a start-after cursor and a cap. S3 has no query language, and naming
this operation `search` would promise one.

Its cursor is a **key**, not S3's opaque continuation token, and that is a deliberate
trade. The token is the more precise mechanism; it is also a value nobody can read, which
turns a paging loop in a model into a variable a reviewer has to take on faith. A
truncated page therefore answers with `nextStartAfter` — the greater of the last key and
the last common prefix, since with a delimiter either may end the page — and the loop is
visible in the diagram and resumable by hand.

### What bounds the byte path

`get-object` and `put-object` are the two operations where content crosses the seam, and
both are capped at [`MaxObjectBytes`](../../connector/s3/client.go), which *is*
`limits.Limits.Variable` — one mebibyte by default — rather than a number of the
connector's own. The destination of a read is one process variable, so a cap that could
differ from the variable's own would only move the failure one step later, and an
installation that raises the variable budget means to raise this with it. The cap
**refuses rather than truncates**, for the reason
[`entra.Request.MaxBytes`](../../connector/entra/client.go) gives: half a PDF is not a
smaller PDF, it is a file that passes every format check and is broken.

Content is authored and returned as text by default, or as base64 when the model says
`encoding="base64"` — which is what makes a binary round-trip through a variable possible
at all, and what a form's uploaded file already looks like.

### Where the credential lives

One bundle shape, in the vault, named by the Worker's `credentialsRef`:

```json
{ "accessKeyId": "AKIA…", "secretAccessKey": "…", "region": "eu-central-1", "sessionToken": "…" }
```

`sessionToken` is optional (STS). `region` is in the bundle rather than beside it because
SigV4 signs with it: a region that disagreed with the credential would produce a signature
the store rejects, and the two are worth keeping in one place where they are set together.
The Worker's `endpoint` is optional and is the only public half — blank means AWS at
`https://s3.<region>.amazonaws.com`, and anything else is the address of the store the
installation runs.

Addressing follows from that one field: **no endpoint → virtual-host** (`https://<bucket>.s3.<region>.amazonaws.com/<key>`,
which is what AWS supports for buckets created since 2020), **an endpoint → path-style**
(`<endpoint>/<bucket>/<key>`, which is what MinIO, Ceph, Garage, R2 and Wasabi serve). See
the *Open question* for the case this does not cover.

### The five gates of ADR-0299

**Gate 1 — its operations are steps a process takes.** Put the invoice down. Find last
month's. Give the approver the link. Archive it when the case closes. Each is a task with a
verb on it in a diagram. The surface is deliberately not the S3 API: there is no bucket
lifecycle, no versioning configuration, no ACL, no multipart upload, no replication — those
are administration of the store, not steps in a process, and the store's own console and
the REST Worker remain the way to reach them.

**Gate 2 — the generic path is blocked.** This is the *blocked* limb, and structurally so.
S3 authenticates with AWS Signature Version 4: the caller builds a canonical request,
hashes it, derives a signing key by chaining HMAC-SHA256 over the date, the region, the
service and the literal `aws4_request`, and signs the hash with it. The REST Worker's auth
surface is basic, bearer, an API key and OAuth2 client credentials
([ADR-0067](0067-service-task-connector-catalog.md),
[ADR-0152](0152-rest-connector-oauth2.md)). FEEL has no HMAC and no
SHA-256 and should not grow them for this. No model author can bridge it, and no amount of
authored headers gets there — the same shape of absence ADR-0235 found at Google's JWT
assertion.

**Gate 3 — the work fits a job.** Four answers:

1. *Inside a lease?* Every operation is one HTTP round trip bounded by the shared worker
   call budget ([ADR-0149](0149-bounded-connector-call-budget.md)); the two presigns make
   no call at all. The capped byte path is what keeps a transfer from being open-ended.
2. *Safe twice?* `put-object` to a key overwrites with the same bytes; `copy-object`,
   `delete-object` and the three reads are idempotent by definition; the presigns are
   pure. The one caveat is an author's: a key built from a non-deterministic expression
   makes a retry write a *second* object, which is true of any at-least-once write and is
   said in the panel where the key is authored.
3. *Does the result fit a variable?* Yes, and that is the design: metadata, a listing page
   and a URL are all small. Content is the exception and is capped at the variable budget.
4. *Is the result the whole answer?* Yes. There is no progress stream to read — the
   nearest thing, a large upload, is exactly what a presigned URL moves out of the job.

**Gate 4 — one credential shape with a server-side home.** Above. One bundle, resolved by
Worker name from the vault at build time, never in a BPMN file, an event or a variable.

**Gate 5 — it arrives complete.** The `managedConnectorKind` entry, the
[`releasedkinds.go`](../../api/releasedkinds.go) row, the Repository package
`atlas.s3-objects`, the ADR-0289 setup entry and handbook card, and this record. The
`packagesOwed` ratchet is unchanged at sixteen, because this kind ships its package.

### The objection this record has to answer

**A presigned URL is credential material, and gate 4 says credential material never enters
a variable. So `presign-get` must not exist.**

That is the strongest form of the objection and it deserves its strongest form. A
presigned URL is a bearer capability: whoever holds it can perform that one verb on that
one object, with no further authentication, until it expires. Writing one into a variable
puts it in the process's state and therefore in the event log, where it outlives the
expiry by however long the installation keeps history. A link pasted into a Discord channel
is readable by everyone in the channel. None of that is hypothetical.

What makes it right anyway is the difference between a *standing identity* and a *derived,
scoped, expiring capability*, which is the difference gate 4 is actually about. The access
key grants every verb on every object in reach of that policy, to whoever holds it, until
somebody rotates it — that is the thing that must never leave the server, and it does not.
A presigned URL grants one verb on one key until a stated moment, and it **is the artifact
the process asked for**: the whole point of the operation is that a person can open the
document. Refusing it would leave only the byte path, which means either a 1 MiB ceiling on
every document a process handles or 40 MB in the event log forever — worse on every axis
including this one.

So the capability is admitted and bounded instead. The default expiry is one hour and the
ceiling is S3's own seven days; the panel, the handbook and the package all say in plain
words that the URL is a time-limited key and belongs in a variable only for as long as
somebody needs to click it. The engine already carries mail bodies, Jira comments and
Discord message text in exactly the same place, under exactly the same rule.

## Consequences

- **Positive:** a process can finally handle documents at their real size. A generated
  letter goes into a bucket and an approver opens it from a user task; a scan is uploaded
  straight to the store by the browser that has it; a case's attachments are found again
  by prefix. None of it touches the log or the loop.
- **Positive:** it works against whatever object store the installation already runs, which
  for most is not AWS.
- **Positive:** ADR-0299's gate 3 named "an artifact store" as missing seam work. This does
  not build one for the *engine* — the variable budget and the absence of a blob column are
  untouched — but it gives *models* a place to put artifacts, which is the half that was
  actually blocking work.
- **Negative / trade-offs accepted:** a presigned URL in a variable is a bearer capability
  in the event log, bounded by its expiry and by nothing else. Argued above; it remains the
  sharpest edge of this kind.
- **Negative:** the byte path is capped at 1 MiB and refuses rather than truncates, so a
  model that reaches for `get-object` on a real document gets an error rather than a
  partial answer. That is the correct failure and it will still surprise people; the panel
  points at `presign-get` from the field where it happens.
- **Negative:** no multipart upload, so `put-object` cannot write a large object even if
  the cap were raised. `presign-put` is the answer, and it moves the upload to a client
  that can do it properly.
- **Negative:** a bucket whose name contains a dot cannot be reached on AWS, because
  virtual-host addressing puts it in the hostname and AWS's wildcard certificate covers
  one label. That is AWS's own long-standing limitation rather than anything introduced
  here, and it surfaces as a TLS error naming the host, which is at least diagnosable;
  the same bucket on a self-hosted store is addressed in the path and works.
- **Follow-ups / risks to watch:** virtual-host addressing under a custom domain (the open
  question), which is also what would give the dotted-bucket case an answer on AWS. Server-side encryption headers (SSE-KMS) are reachable through the metadata
  fields today but are not modelled as their own properties; if that turns out to be how
  installations actually use it, they deserve fields. A `list-objects` that pages itself
  across calls is deliberately not built — the cursor is authored, so the loop is visible
  in the diagram.

## Pros and cons of the options

### Option 1 — Document the script task
- Good: no new kind, no new code, and it works today for anybody who already has the CLI.
- Bad: the credential lands in a script task's environment, which is the arrangement
  ADR-0168 exists to end.
- Bad: nothing validates it. A typo in a bucket name is found by a token parking on a
  non-zero exit code with `aws`'s stderr attached.
- Bad: it needs a binary on the host, which an installation that offloads its script tasks
  to a container may not have.

### Option 2 — Wait for an engine-side artifact store
- Good: it is the more general answer, and ADR-0299 already names it.
- Good: it would also unblock OpenTofu's plan output and worker log streaming.
- Bad: it is a large record and a larger change — a new store, a retention policy, a
  garbage-collection story, an API surface, a backup story — and none of it is needed for a
  process to use the object store the installation already has.
- Bad: it answers the wrong half. The engine holding artifacts is not what an operator
  asked for; a process reaching *their* bucket is.

### Option 3 — An S3 Worker Type that moves bytes
- Good: the simplest model to explain — content in, content out.
- Good: no presigned URL ever enters a variable, so the objection above never arises.
- Bad: it caps every document a process can handle at the variable budget, which excludes
  most of the documents this is for.
- Bad: what it does carry, it carries into the event log permanently.
- Bad: it would make Atlas the transfer path for every byte, on a goroutine budget sized
  for API calls.

### Option 4 — References, with a bounded byte path (chosen)
- Good: documents are handled at their real size, because the bytes go directly between the
  store and whoever needs them.
- Good: the small case still works, and it is bounded by the same number the variable it
  lands in is bounded by.
- Good: it fits the job seam as it exists, with no new engine machinery.
- Bad: two ways to "open" an object is one concept more than a single operation would be,
  and an author has to pick the right one.
- Bad: the presigned URL's residency in the log is a real cost, accepted and bounded rather
  than avoided.

## Links

- admission gates: [ADR-0299](0299-worker-type-admission-criteria.md)
- what a Worker Type is: [ADR-0203](0203-worker-execution-model.md),
  [ADR-0067](0067-service-task-connector-catalog.md)
- gate 2's other worked cases: [ADR-0235](0235-google-sheets-worker.md) (blocked),
  [ADR-0258](0258-discord-worker.md) (lossy)
- the credential seam: [ADR-0069](0069-engine-internal-encrypted-secret-vault.md),
  [ADR-0070](0070-vault-on-by-default-with-generated-key.md),
  [ADR-0168](0168-connector-work-on-a-worker.md)
- the job seam: [ADR-0007](0007-job-worker-protocol.md),
  [ADR-0149](0149-bounded-connector-call-budget.md),
  [ADR-0061](0061-incident-model.md)
- where it runs: [ADR-0164](0164-no-in-process-service-tasks.md)
- what it owes on arrival: [ADR-0167](0167-released-connectors-ship-in-the-marketplace.md),
  [ADR-0289](0289-worker-type-setup-in-the-panel.md)
