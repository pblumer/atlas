# Implementation plan — Process applications (ADR-0127)

Companion to [ADR-0127](../adr/0127-process-applications.md). The ADR fixes the
*decision and shape*; this document is the *engineering plan* — file-by-file
changes, new types, endpoints, and the test obligations per phase. It is a living
plan (unlike the immutable ADR) and may be edited as slices land.

**Ground rules carried from ADR-0127 and AGENTS.md**

- Everything here is **design-time, below the HTTP API**. No change to `engine/`,
  `wal/`, `state/`, `compiler/`, `applyToState`, or the six invariants.
- **Test-first** (ADR-0018). New endpoints, the release/version logic, and the
  target/git side effects each get tests before the code. Recovery test where a
  new sidecar store is reloaded on startup. Keep repo-wide coverage ≥ 95%.
- **No forced migration.** On disk stays `projects/` + `projectId`. New stores are
  additive.
- Definition of done per slice: `go build ./...`, `go test -race ./...`,
  `go vet ./...` green and `gofmt -l .` empty.

---

## Phase 1 — Reframe (no behaviour change) — ✅ shipped

**Goal:** the concept is "application" everywhere a user or API client sees it;
bundle deploy is the headline Publish path. Zero behavioural change to storage or
the engine.

**Delivered.** API, MCP tools, and the UI all speak "process application"; the
pre-rename surface stays registered as deprecated aliases for one release. What
landed, against the plan below:

- `/api/v1/applications*` registered on the same handlers as their `/projects*`
  twins; the eight `/projects*` routes marked `deprecated` in the OpenAPI doc
  (new `deprecated` field on `apiOp`).
- MCP gained `atlas_create_application`, `atlas_list_applications`,
  `atlas_delete_application`, `atlas_deploy_application`; the four
  `atlas_*_project` tools keep working with deprecation notices in their
  descriptions. Both MCP guards (drift + contract) stay green.
- UI relabelled (home "Applications", 📦, "Not assigned", "Publish" as the
  app-level action) **and** moved onto the canonical `/api/v1/applications`
  paths; the per-diagram Deploy in `editor.js` is now a secondary button.
- Storage untouched: still `projects/` on disk and `projectId` on artifacts.

**Deliberately deferred out of Phase 1:** renaming the Go identifiers
(`project`, `projectView`, `handleCreateProject`, `deployProject`, …) and the
`data-dt-key`/route slugs (`#/modeler/p/…`). They are internal names with no
user-visible surface; a mechanical rename is its own low-risk slice, best done
when Phase 2 touches these files anyway.

### Backend (`api/`)

- **`api/openapi.go`** — register `/api/v1/applications*` routes that resolve to
  the *same handlers* as the existing `/api/v1/projects*` routes (create, list,
  get, patch, delete, members, validate, deploy). Keep the `/projects*` routes
  registered and mark them `deprecated: true` in the OpenAPI metadata.
- **`api/projects.go`, `api/projectdeploy.go`, `api/scopes.go`** — no logic change;
  add doc comments noting the application framing. Optionally introduce
  `application` type aliases (`type application = project`) to let new code read in
  the new vocabulary without duplicating structs.
- **`api/systemproject.go`** — rename user-facing name string to "Atlas System"
  application framing only; reserved id `system` and `protectedGuard` unchanged.
- Response `projectView` gains no new fields in this phase; the UI reads it as an
  application.

### MCP (`mcp/`)

- **`mcp/tools_authoring.go`, `mcp/tools.go`** — add `atlas_list_applications`,
  `atlas_create_application`, `atlas_deploy_application`,
  `atlas_delete_application` as aliases delegating to the existing project tool
  bodies. Keep the `*_project` tools, mark deprecated in their descriptions.

### Frontend (`api/web/app.js`, `app.css`)

- Rename the Modeler home to **"Applikationen"**; the project row/table becomes the
  application list. Ungrouped bucket relabelled **"Nicht zugewiesen"**.
- Project detail → **application detail**; breadcrumb "Applikationen › Name".
- Make the primary **"Applikation publizieren"** button call the bundle deploy
  (`deployProject`, to be aliased `deployApplication`); demote per-diagram deploy
  in the editor to a secondary action (keep it working).
- App icon `📦` for applications (replacing `📁`), preserving the existing card /
  chip / pill styling (see the published mock).

### Tests

- `api/applications_alias_test.go` — each `/applications*` route returns the same
  result as its `/projects*` twin; deprecated routes still work.
- MCP alias tool test mirrors the above.
- Mechanical relabels in `app.js` are covered by the existing e2e Design-view
  suite; add an assertion that the home renders "Applikationen".

---

## Phase 2 — Application releases

**Goal:** publishing mints a versioned, listable release; the app shows its local
live version and running instances.

### New sidecar store

- **`api/releasestore.go`** — `releaseStore`, one JSON file per release under
  `<data-dir>/releases/`, atomic-write + dir fsync + `loadAll` on startup, owned by
  the run-loop goroutine (mirror `deploystore.go`/`projectstore.go` exactly).

```go
type applicationRelease struct {
    ID            string          `json:"id"`
    ApplicationID string          `json:"applicationId"` // == projectId on disk
    Version       int32           `json:"version"`       // per-application counter
    PublishedAt   int64           `json:"publishedAt"`
    PublishedBy   string          `json:"publishedBy,omitempty"`
    Note          string          `json:"note,omitempty"`
    Members       []releaseMember `json:"members"`       // artifact keys + versions
}
type releaseMember struct {
    Kind        string `json:"kind"`       // "process" | "decision" | "form"
    Ref         string `json:"ref"`        // processId / modelRef / formId
    ArtifactVer int32  `json:"artifactVer"`// per-process version (ADR-0019), etc.
    Key         uint64 `json:"key,omitempty"`
}
```

### Backend

- **`api/projectdeploy.go`** — on a successful bundle deploy, compute
  `version = maxReleaseVersion(appID) + 1`, snapshot the deployed members, and
  `releases.save(...)`. Version counter derived from `loadAll` at startup
  (`s.appVersions[appID]`), same pattern as `s.versions[pid]`.
- New endpoints (`api/releases.go`, wired in `openapi.go`):
  - `GET  /api/v1/applications/{id}/releases` — release history.
  - `POST /api/v1/applications/{id}/publish` — body `{ note }`; the headline
    publish (bundle deploy + release). `POST .../deploy` kept as the lower-level
    alias.
  - `GET  /api/v1/applications/{id}/deployments` — local live view: for each member
    process, current deployed version + running/finished counts, assembled from the
    existing `/processes` + instances-summary data (ADR-0080/0083 counters), scoped
    to this application.

### Frontend

- **Deployments tab** inside application detail (see mock screen 2): local target
  card (version + instances), release history table.
- **Publish dialog** (mock screen 3): shows `v(n) → v(n+1)`, changelog note,
  triggers `POST .../publish`.

### Tests

- `api/releasestore_test.go` — save/loadAll ordering, atomic write.
- `api/release_recovery_test.go` — publish, simulate restart, `loadAll`, assert
  `appVersions` and release history reconstructed (recovery test, first-class).
- `api/releases_test.go` — publish increments version; failed bundle deploy mints
  **no** release (all-or-nothing preserved); deployments view aggregates correctly.

---

## Phase 3 — Remote deployment targets

**Goal:** deploy an application to another Atlas server; see per-target live state.
**Note:** ADR-0127 flags this as a candidate for its own ADR (remote auth,
transport, partial-failure). Land the ADR before/with this slice.

### New sidecar store

- **`api/targetstore.go`** — `deploymentTarget` under `<data-dir>/targets/`.

```go
type deploymentTarget struct {
    ID          string `json:"id"`
    Name        string `json:"name"`        // "Produktion", "Test"
    BaseURL     string `json:"baseUrl"`      // remote Atlas
    CredentialRef string `json:"credentialRef"` // vault handle (ADR-0069/0070)
    Kind        string `json:"kind"`         // "prod" | "test" | "local"
    CreatedAt   int64  `json:"createdAt"`
}
```

### Backend

- **`api/targets.go`** — CRUD for targets (`/api/v1/targets`), credential stored via
  the vault, never returned in responses.
- **`api/remotedeploy.go`** — `POST /api/v1/applications/{id}/publish` gains an
  optional `targetId`. When set, the local server calls the **remote's existing**
  `POST /api/v1/deployments` (or its bundle publish) with each member's XML/DMN,
  authenticating with the vault credential. Runs as a post-API side effect (an
  outbound HTTP call like any connector), off the hot path. Records the release
  with the target on it.
- **`api/targets_status.go`** — `GET /api/v1/applications/{id}/deployments` extended
  to query each target's `GET /processes` + instances summary and fold live
  version + running counts per target (the "deployed apps inside the application"
  view).

### Frontend

- Deployments tab lists **targets** (Produktion / Test / this server) with live
  version + instance counts and an "Öffnen" / "Hierhin publizieren" action (mock
  screen 2, Deployments pane). Publish dialog gains the target picker (mock screen
  3).

### Tests

- Target store save/loadAll + recovery.
- `api/remotedeploy_test.go` — against an `httptest` stand-in remote: bundle posts
  to the remote's deploy API; credential resolved from vault, never logged; a
  remote 4xx/5xx surfaces as a failed publish with a clear reason; **partial
  failure** across multiple targets is reported per target (decision to record in
  the Phase-3 ADR).

---

## Phase 4 — Git source binding

**Goal:** bind an application to a git repo; import/sync/commit the same sidecar
JSON the backup archive serializes (ADR-0107). **Candidate for its own ADR**
(transport, conflicts, credentials).

### Backend

- **`api/gitbinding.go`** — bind info on the application (repo URL, branch,
  credential vault handle). Persisted on the project sidecar (additive optional
  fields) or a small `<data-dir>/git-bindings/` store.
- **`api/gitsync.go`** —
  - **Import:** clone/pull to a scratch working copy, read the application's
    artifact files (the ADR-0107 allowlisted shapes: drafts, dmnrefs, forms), write
    them into the sidecar stores tagged with this application.
  - **Commit/push:** serialize the application's current artifacts (reuse the
    ADR-0107 export serialization) into the working copy, commit, push.
  - Pure design-time server side effect; no engine, no hot path. Use a CGO-free git
    approach (e.g. `go-git`) to preserve the single-binary/no-CGO constraint
    (ADR-0010) — confirm in the Phase-4 ADR.
- Endpoints: `POST /api/v1/applications/{id}/git/bind`, `.../git/import`,
  `.../git/commit`, `GET .../git/status`.

### Frontend

- **Quelle & Git tab** (mock screen 2, Quelle pane): repo/branch/last-sync/status
  card + import/commit/unbind actions. Application list shows a git source column.

### Tests

- `api/gitsync_test.go` — against a local bare repo fixture: bind → import
  populates artifacts; local edit → commit/push writes them back; round-trip
  (export→import) is stable; credential from vault, never logged; conflict handling
  per the Phase-4 ADR.

---

## Sequencing & risk

| Phase | Ships | New network surface | Own ADR? | Rough size |
|-------|-------|---------------------|----------|-----------|
| 1 Reframe | rename + headline publish | none | no | S |
| 2 Releases | app version + history + local deployments view | none | no | M |
| 3 Remote targets | deploy to other servers, per-target status | outbound HTTP | **yes** | L |
| 4 Git binding | import/sync/commit | outbound git | **yes** | L |

- Phases 1–2 are self-contained and low-risk (no new network, no new trust
  boundary) — land them first; they already deliver the "application that publishes
  as a versioned unit" story.
- Phases 3–4 each add outbound network, credentials, and failure modes. Gate each
  behind its own ADR covering auth, partial-failure, and (for git) conflicts and
  the CGO-free transport choice.
- Backward-compat guard across all phases: the deprecated `/api/v1/projects*`
  routes and `atlas_*_project` tools keep passing their existing tests until a
  future release removes them (track that removal as a separate task so the alias
  doesn't ossify — ADR-0127 follow-up).
