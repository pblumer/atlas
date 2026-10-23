# ADR-DRAFT: Paperless-ngx Worker with reference-only document handling

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-10-23
- **Deciders:** Atlas maintainers
- **Open question:** How a supervised Paperless Worker receives an authorized, replayable reference to an externally stored input file (initially S3) without copying another Worker's secret or persisting a bearer URL. The current Worker protocol has no durable binary/artifact channel. The transfer mechanism must be settled before `upload-document` is implemented.
- **Question checked:** 2026-10

## Context and problem statement

A BPMN process should be able to file a document in Paperless-ngx, find it later and present it to a person. Paperless-ngx provides a token-authenticated REST API, searchable document metadata, uploads, processing tasks, tags, correspondents, document types and object-level permissions.

In Atlas, assigning a document to a process variable persists its content through variable events into the WAL. Even an apparently harmless Paperless API read can return the complete OCR text as a `content` property, and search responses can contain matching excerpts. Passing a raw API response to the existing Worker completion path would persist that text unintentionally. The 1 MiB variable budget does not solve this privacy or retention problem: it merely limits its size.

Atlas already chose references rather than bulk bytes for the S3 Worker in ADR-0442. This proposal extends that principle to a document-management application, with separate handling for metadata, presentation, ingestion and explicit text extraction. It does **not** turn the WAL into a document repository, and it does not introduce an engine-local blob store as a local workaround.

The question is: how can the Paperless-ngx Worker handle normal documents, including large ones, while only explicitly requested document data becomes durable process state, and while respecting retries, object permissions and worker identity?

### Provider facts checked 2026-10

- `POST /api/documents/post_document/` takes a multipart `document` field plus optional title, date, correspondent, document type, storage path, tags and custom fields. It returns a processing task UUID, not a committed document ID. `GET /api/tasks/?task_id=...` is the follow-up.
- `GET /api/documents/` supports pagination, `text`, `title_search`, `query` and other filters. Its response may include `content` (OCR text) and `__search_hit__.highlights`.
- `GET /api/documents/{id}/` returns metadata and OCR text. `GET /api/documents/{id}/download/` and `preview/` return binary content; a version can be selected.
- Paperless supports object-level owner/view/change permissions. An API service identity is not interchangeable with the person viewing the document.
- Paperless may accept duplicate content and flag it rather than reject it. An idempotency key for document posting is not documented; do not assume exactly-once ingestion.

Sources: https://docs.paperless-ngx.com/api/ and https://docs.paperless-ngx.com/usage/ (reviewed 2026-10-23).

## Decision drivers

1. The default must persist **no binary document bytes and no OCR document content** in Atlas's WAL, including through errors, job results, variable mappings and operator history.
2. A process needs durable IDs and status facts to recover; volatile, non-repeatable upload tickets alone cannot represent a business document.
3. Uploads and downloads must not run on the processor's single-writer loop and must follow durable-before-visible.
4. A Paperless identity must be configured on the Worker through the vault; a model must not contain credentials, signed URLs or authorization headers.
5. Presentation to a human should ordinarily use that human's Paperless session and Paperless authorization, not a public bearer link.
6. External writes are at-least-once. An acknowledged upload task and a fully ingested document are different states.
7. The Worker Type must satisfy ADR-0299's admission criteria and ship with its package, setup instructions and tests, not just a catalog entry.

## Considered options

1. **Generic REST Worker only.** Useful for quick prototypes, but the REST job body is JSON-shaped, not multipart streaming. Reading Paperless's raw JSON into variables may retain OCR text inadvertently, and every model would repeat the filtering, polling and permission assumptions.
2. **Document bytes in base64 variables.** Would make upload easy, but deliberately stores potentially sensitive binary data in the event log; normal documents also exceed the variable limit.
3. **Engine-owned persistent blob store.** A reusable eventual capability, but requires new storage, retention, authorization, backup and recovery contracts. Not a prerequisite for metadata operations or presentation.
4. **Reference-first Paperless Worker (proposed).** A dedicated `paperless` Worker Type whose results are allowlisted metadata, and whose binary transfers, when available, stream directly between trusted external source, Worker Instance and Paperless. No arbitrary URLs or binary payloads traverse the job/variable/WAL boundary.

## Decision outcome

Propose option **4**, in staged vertical slices. The Worker Type is `paperless`; the exact reserved job type, extension schema, and UI catalog entry are assigned when implementing, following current Atlas registry patterns. This is a proposal, **not** a claim that the Worker exists.

### Operation contract

| Operation | Effect | Durable result (allowlist only) |
|---|---|---|
| `get-document` | Read a known ID; optionally a version | `id`, `title`, `created`, `modified`, `tags`, `correspondent`, `documentType`, `storagePath`, `filename`, `version` where present |
| `search-documents` | Search titles, full text or configured filters; bounded page size | `documents[]` of the same safe projection, `count`, cursor/page marker |
| `present-document` | Construct a canonical, authenticated Paperless UI link for an ID | `id`, `url` (without token or signature); Paperless checks the viewing user's right |
| `list-tags` | Look up tags for authoring/selection | Bounded IDs and labels |
| `list-correspondents` | Look up correspondents | Bounded IDs and labels |
| `update-document` | Change expressly supplied metadata only | ID and safe updated metadata |
| `upload-document` | Stream a trusted external file into `post_document` without process-variable bytes | `uploadTaskId`, `state=accepted`; **not** `documentId` |
| `get-upload-status` | Resolve an accepted Paperless consumption task | `uploadTaskId`, `state`, `documentId` on success, controlled error code |

`download-document` is deliberately **not** an operation that returns a base64 file. The authenticated UI link is the default presentation path. A future approved direct-download or document-copy operation must stream to a target outside engine variables and needs its own transport and authorization contract.

The Worker MUST construct these results itself. It must never return unfiltered Paperless JSON via `map[string]any`, and tests must deliberately inject sensitive `content`, `__search_hit__.highlights`, note bodies, signed URLs and raw error responses to prove they are absent. Even a small OCR result is omitted by default.

A later opt-in `read-text` operation MAY put extracted text into a named process variable under `Limits.Variable`, only when explicitly authored in the model and visibly marked as retained in WAL/history. This is a separate operation and not an `includeContent` flag silently inherited by common reads. Binary file content is never a result variable in this design.

### Reference-only upload boundary

An upload needs **durable, replayable source coordinates**: for example a trusted S3 source Worker name plus bucket, key, optional immutable version or ETag, filename and content type. Such a tuple is metadata, not a signed URL or file bytes. The Paperless Worker Instance obtains *its own explicitly authorized access* to that source using its configured runtime identity, reads the object as a bounded stream, then posts multipart content to Paperless with `io.Pipe` or equivalent. No bytes are sent to the Atlas Engine or written to process variables.

This does **not** authorize a Paperless Worker to steal an S3 Worker's credential: the Worker supervisor or a delegated source adapter must enforce a separately configured grant for the Paperless Worker to read that source. The current code does not establish that grant or an artifact transfer seam. Implementing this path without first deciding that authorization contract is **blocked**; do not replace it with a freely modelled URL, access key, filesystem path, or undocumented credential borrowing.

A trusted direct browser-to-Paperless upload is another possible later integration, but needs an explicit Paperless authorization and callback/correlation design; no client JavaScript-only workaround may be described as an engine feature.

### User-facing presentation

`present-document` emits an **ordinary authenticated URL** under the configured Paperless base URL, never a share token, a service token, or an API download signed URL. The receiving user must be able to authenticate to Paperless and must independently have view permission there. The Worker **does not** grant access by presenting a reference.

If an Atlas user lacks Paperless access, return a useful authorization/setup error, or later add a purpose-bound Atlas proxy with its own policy and non-WAL streaming design. Paperless share links are public bearer capabilities; they require a distinct explicit operation and security review, not a default option.

### Identity, metadata and data minimization

- Configure Paperless endpoint and API token on an operator-managed Worker using vault-held secrets; support TLS validation, optional trusted local mock endpoint for development, and conservative timeouts.
- A service token acts with its Paperless user's privileges; it does **not** prove that the human completing an Atlas task is authorized to see every returned document. Enforce the Paperless viewer's access at presentation. Broader per-execution policy is addressed by ADR-0475, whose implementation is partial; do not claim that boundary exists here.
- Authenticate provider calls server-side and never place tokens in BPMN, job payloads, error text or result variables. Do not expose raw response bodies or log Authorization headers.
- Metadata can itself be sensitive: titles, filenames, tags and search terms are allowed **only when the model explicitly maps a result variable**. A result absent a result variable is not persisted as an extra side channel.
- Control result count, response size and output mappings; never load an entire search result unbounded. Reject redirected downloads outside the configured allowed source and deny arbitrary SSRF-capable URLs.

### Asynchronous ingestion and duplicate safety

`upload-document` reports acceptance with `uploadTaskId` only. A separate `get-upload-status` step determines when Paperless created the final `documentId`. The model should use a BPMN timer/retry loop with bounded backoff rather than hold a lease during OCR.

An accepted upload can be repeated if the Worker crashes after Paperless receives the multipart body but before Atlas durably records the completion. Paperless does not document a native atomic idempotency key. Therefore **exactly-once is not claimed**.

The implementation must first research and test reconciliation using source checksum and a stable Atlas business reference (potentially a Paperless custom field), including the failure window before the document is searchable. Preflight search alone is not atomic. An installation's duplicate-policy setting is not a substitute for a worker idempotency guarantee. Until this is settled, duplicate creation is an explicit operational risk with a reconciliation path, and tests must simulate the crash window.

### Milestone and implementation slices

This belongs to **Milestone 1 — Core BPMN / Worker Types**, with a dependency on identity-aware execution (ADR-0475) for fully governed delegated access and an unresolved external-binary transfer seam for uploads.

1. **Slice A — read/present safely.** Add the Worker Type, compiler detail, registry, configuration/secret handling, Worker Instance execution, setup and handbook, package/release registry, metadata projection, search, get, present, unit and mock integration tests. Default outputs omit binary and OCR content. Deliver a usable search → human review process.
2. **Slice B — metadata changes and async status.** Update safe metadata, list vocabularies, query consumption status; test permissions, bounded paging and state transitions.
3. **Slice C — external source → Paperless.** Decide source authorization first (in a separate ADR if it changes the existing worker boundary); implement streaming multipart upload, task UUID polling, idempotency/reconciliation, crash/retry tests and a complete document filing example.
4. **Slice D — opt-in text and guarded external presentation**, only if justified by a business process; otherwise leave both out.

For a Mock Mode, implement a stateful `httptest` Paperless API double (token auth, paged search, metadata, multipart upload, pending/success/failure task transitions, duplicate/timeout cases). A mock must verify what bytes leave the Worker and what bytes **never** enter job results or variable events.

### Required tests and acceptance

- TDD: failing tests for every operation, malformed source, unauthorized response, HTTP failures, timeouts, pagination and permission denied.
- Leak regression: a canary PDF and OCR string must not occur in any job-completion variable, WAL segment, replay snapshot, output mapping, incident detail or mock report after read/search/upload/present by default; explicitly allowed metadata may occur.
- Recovery: run the workflow, fsync durable reference/status events, restart/replay the WAL, compare live and replay states; repeat across accepted/pending/succeeded/failed ingestion.
- Retry: inject failures before upload, after remote 200 but before Atlas completion, and after status transition; assert documented dedup/reconciliation behavior without an unsupported exactly-once claim.
- Large file: multi-megabyte upload keeps engine WAL size independent of binary size; stream with bounded worker memory and no variable-limit bypass.
- Authorization: denied Paperless principal or non-allowlisted source fails closed; tokens/share links never appear in output or logs.
- Complete gate 5: `api/connectorkinds.go`, `api/releasedkinds.go`, Worker package, setup documentation and bilingual handbook, required catalog/package, changelog and roadmap.
- Definition of done remains `go build ./...`, `go test ./...`, `go test -race -timeout=45m ./...`, `go vet ./...` and an empty `gofmt -l .`.

### Consequences

- **Positive:** Useful document lookups and human review without binary/OCR content in the WAL by default. The job protocol and engine invariants remain unchanged for the first slice.
- **Positive:** The separate ingestion status makes the business process see the difference between "accepted" and "archived".
- **Negative / trade-offs accepted:** Human preview needs Paperless access; a durable reference is not a portable file. Paperless may disappear or revoke access while Atlas still remembers the ID.
- **Negative / trade-offs accepted:** Safe source-to-Paperless uploads and exactly-once semantics are not pretend-solutions; the existing protocol leaves a real integration/authorization dependency.
- **Follow-ups / risks to watch:** delegated credentials and policy enforcement, source connector permissions, retention/deletion policies, callback-based upload completion, audited share links and non-binary text opt-in.

## Pros and cons of the options

### Option 1 — Generic REST Worker only
- Good: no new Worker Type.
- Bad: no normal multipart file streaming; raw responses can leak OCR text; each model must reproduce polling and provider semantics.

### Option 2 — Base64 process variables
- Good: simplest primitive to route a few kilobytes.
- Bad: puts documents into WAL, backups and replay; normal file sizes exceed the variable limit.

### Option 3 — Atlas artifact/blob store
- Good: general cross-worker binary transfer abstraction.
- Bad: a new storage product, lifecycle and access-control surface before the first Paperless use case.

### Option 4 — Reference-first Paperless Worker
- Good: bounded metadata in WAL; direct provider access, no engine bytes; understandable human review.
- Bad: requires an authorized external binary source for upload and explicit eventual-consistency/duplicate handling.

## Links

- [ADR-0442 S3 Worker](0442-s3-object-store-worker.md) — references, variable budget, link trade-offs
- [ADR-0299 Worker Type admission criteria](0299-worker-type-admission-criteria.md) — all five gates
- [ADR-0168 Connector work on a Worker](0168-connector-work-on-a-worker.md) — off-process credentials and job resolution
- [ADR-0174 Connector payloads are the input mapping](0174-connector-payloads-are-the-input-mapping.md) — output/payload scope discipline
- [ADR-0475 Identity-aware Worker execution](0475-identity-aware-worker-execution.md) — proposed per-action identity and policy boundary
- [Atlas invariants](../architecture/invariants.md) — hot path, fsync, single writer, deterministic replay
