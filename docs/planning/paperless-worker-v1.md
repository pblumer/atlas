# Paperless-ngx Worker v1 (experimental)

This is the first executable, **external-only** metadata slice of [ADR-0479](../adr/0479-paperless-ngx-worker.md). It does not yet have a first-class Modeler palette entry, managed Worker record, Repository package or reserved job type. It uses the existing public job-worker protocol and a *dynamic*, explicitly named job type, so the processor and WAL need no new code. The reserved job type for a later first-class Worker will be separate; never rename `paperless.v1` into a reserved `io.atlas.*` string behind existing deployments.

## Scope

Implemented operations:
- `search-documents` (max 25 per page, title and other selected metadata; the backend searches full text but the worker does not return it);
- `get-document` (one metadata projection);
- `present-document` (authenticated Paperless UI URL; it does not grant viewing permissions);
- `list-correspondents` (one page of IDs and names);
- `add-correspondent` (ensure by name, with a preflight lookup, conflict reconciliation and optional provider matching rule).

**Not in v1:** `upload-document`, `download-document`, `get-upload-status`, `list-tags`, metadata updates, opt-in OCR text, fully policy-governed delegated identity, first-class Modeler setup. No PDF, binary content or raw OCR is returned by these operations. An operator may still deliberately put sensitive data into arbitrary BPMN variables; this worker does not change what ordinary variable events persist.

## Configure the external Worker Instance

Give the Paperless service user only the rights needed by its operations. Configure the worker instance (on a trusted machine) using its environment, never model-authored credentials:

```bash
export ATLAS_PAPERLESS_CONNECTORS=archive
export ATLAS_PAPERLESS_ARCHIVE_URL=https://paperless.example.com
export ATLAS_PAPERLESS_ARCHIVE_TOKEN='token-from-paperless-profile'
atlas worker --server https://atlas.example.com --token "$ATLAS_TOKEN" --connector paperless
```

The underscore-normalized Worker name `archive` selects `ATLAS_PAPERLESS_ARCHIVE_*`. You can configure multiple comma-separated names. TLS validation is on by default. Only for a trusted internal HTTP deployment, set `ATLAS_PAPERLESS_ARCHIVE_ALLOW_HTTP=true` explicitly. Redirects are not followed and token-bearing responses are never logged by this package. An external worker's process environment remains sensitive: do not grant shell access to untrusted users.

## Model a BPMN Service Task

Use the existing generic service task extension, not an `atlas:paperlessConnector` that does not exist yet:

```xml
<bpmn:serviceTask id="FindDocument" name="Find Paperless document">
  <bpmn:extensionElements>
    <zeebe:taskDefinition type="paperless.v1" retries="3" />
  </bpmn:extensionElements>
</bpmn:serviceTask>
```

Provide the input variable `paperless` in the activity scope (prefer an input mapping) or instance scope. It must be a JSON object, never a file or bearer URL:

```json
{
  "paperless": {
    "worker": "archive",
    "operation": "search-documents",
    "query": "Invoice 2026",
    "page": 1,
    "resultVariable": "documents"
  }
}
```

The output is written into the named variable `documents` if provided. Leaving `resultVariable` empty discards the provider result entirely instead of retaining document metadata in the WAL. Only expected metadata is projected; provider `content`, OCR highlights, notes, and raw error bodies are not included.

A correspondent-creation task uses the same job type and input variable:

```json
{
  "paperless": {
    "worker": "archive",
    "operation": "add-correspondent",
    "name": "Muster AG",
    "matchingAlgorithm": 0,
    "resultVariable": "correspondent"
  }
}
```

`matchingAlgorithm` follows Paperless integer enum 0=None, 1=Any, 2=All, 3=Exact, 4=Regex, 5=Fuzzy, 6=Automatic. The default 0 avoids surprise automatic classification. Optional `match` and `isInsensitive` configure a new correspondent. Existing correspondents are never silently rewritten. A duplicate name caused by a concurrent creation is looked up again after a 400/409 response; this mitigates at-least-once calls, but does not provide a cross-owner exactly-once transaction. A transport error after successful remote creation can still raise an incident; retrying will first look up the existing name.

The `present-document` operation requires a `documentId`, verifies it through the API and returns an ordinary URL under `/documents/{id}/details`. The recipient must sign into Paperless-ngx and be entitled to view the document. A Worker service token is **never** embedded in the URL.

## Safety and current limitations

- Response JSON is bounded by the central `limits.Payload` budget (8 MiB by default); an unusually large OCR-bearing provider payload is rejected rather than truncated or written to WAL.
- Document searches return at most 25 projected rows per page; correspondents are scanned over at most 50 pages of 100 before refusing a create when uniqueness cannot be established.
- Errors return HTTP status and a sanitized description, not a provider response body.
- API version 10 is requested explicitly. Confirm compatibility against your Paperless-ngx installation.
- The v1 input `paperless` is an ordinary BPMN variable and therefore durable; it must contain **metadata only**.
- Security is based on the configured service account and Paperless object ACL. Per-execution policy is a separate partially implemented Atlas capability (ADR-0475).
- The metadata-only client and worker are covered by mock HTTP tests, **not yet** tested against a live Paperless server.

This is an integration stepping stone, not the completion of ADR-0479's first-class Worker Type. Complete the compiler extension, Console/Modeler configuration, vault-based supervised Worker setup, Repository package, handbook and end-to-end recovery tests before calling the type released.
