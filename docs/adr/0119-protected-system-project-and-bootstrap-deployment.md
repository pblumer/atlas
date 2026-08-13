# ADR-0119: A protected system project and bootstrap-deployed platform processes

- **Status:** Accepted
- **Date:** 2026-08-13
- **Deciders:** Atlas maintainers

## Context and problem statement

Atlas is beginning to **model its own operations as Atlas processes** — user
onboarding, access review, offboarding — the same way `examples/onboarding`
onboards people with a running Atlas instance (dogfooding). The first of these,
`proc_benutzer_aufnahme` (user intake: request → admin creates account →
credentials mailed), was authored and deployed by hand through the MCP authoring
tools.

Deploying it by hand exposes two gaps:

1. **It lives in an ordinary, unprotected project.** The intake process currently
   sits in a project owned by the MCP service principal (`system:mcp`, ADR-0049),
   with `private` visibility. That project is a normal ADR-0034 container under
   the ADR-0071 sharing model: any owner/admin can rename it, delete it, delete
   the deployed definition, or deploy a different diagram over the same process
   id. A process that is supposed to be **part of the platform** should not be
   editable or deletable as if it were tenant content. There is no project kind
   that is platform-owned and change-protected.

2. **It does not come with the install.** A platform process is only useful if it
   is *there* on a fresh instance. Today nothing deploys it at startup. Atlas
   already **embeds assets into the single binary** (`//go:embed`: the conformance
   models, the marketplace catalog of ADR-0081), but there is no **bootstrap step**
   that deploys embedded *processes* (with their forms) when the server starts —
   and doing it naïvely would re-deploy a new version on every restart (the
   "Entscheidungstest v1…v18" effect visible in the current instance).

The question this ADR answers: **where do Atlas's own processes live, how do they
reach a running instance, and how are they protected from being treated as
ordinary editable content — without breaking the engine invariants or the
user-management boundary (ADR-0044/0049)?**

This is deliberately *not* a proposal to move synchronous admin CRUD (the Console
→ Organization → Users screen) into processes. That boundary is drawn explicitly
below.

## Decision drivers

- **Ships with the install.** A platform process must exist on a fresh instance
  with no manual authoring step — from the binary, no external files, no network
  (ADR-0011, the ADR-0081 embed precedent).
- **Protected, not tenant-owned.** Platform processes must not be renameable,
  deletable, or overwritable through the ordinary project/deployment surface.
- **Idempotent across restarts and upgrades.** Starting the server twice must not
  create a second version; shipping a *changed* process in a new Atlas build
  must deploy exactly one new version, with running old-version instances left
  untouched (BPMN versioning already gives us that).
- **Respect the invariants.** A bootstrap deploy is still a deploy: it goes
  through the run-loop owner and the durable deployment store (I3 single-writer,
  I2 durable-before-visible; ADR-0019). No new persistence path, no engine change.
- **Respect the user-management boundary.** The privileged step of these
  processes touches admin-gated user management (ADR-0044) and must not smuggle
  an admin capability into the automation identity (ADR-0049). The protected
  system project is precisely the trusted, non-editable home in which a *future*
  sanctioned automated write-path could be opened deliberately.
- **One system of record.** Whatever front door is used (native console or a
  process), user changes land in the one user store; no divergent second copy.

## Considered options

For **where system processes live**:
1. An ordinary project owned by a human or `system:mcp` (status quo).
2. A **reserved, protected "system" project** owned by a dedicated `system`
   principal, distinct from `system:mcp`.

For **how they reach an instance**:
1. Manual/operator deploy (status quo).
2. **Embed the models + forms in the binary and deploy them at startup**, guarded
   by an idempotency key (per-process content checksum / bundle version).

For **protection semantics**:
1. Fully read-only to everyone, including admins (cannot even start instances —
   too strong; the point is to *run* them).
2. **Platform-managed**: visible to all, startable by the appropriate role, but
   its definitions and the project itself are **not** renameable, deletable, or
   overwritable through the API; content changes only via the bootstrap path.

For **the write-path stance on user changes** (the native-console question):
1. Processes become the *only* sanctioned path; the native create/disable/delete
   is demoted to break-glass.
2. **Coexistence**: the native console stays the direct/break-glass admin surface;
   processes are the governed front door for requested/approved change; both
   write the same store.

## Decision outcome

**Introduce a reserved, protected system project, owned by a dedicated `system`
principal, whose processes and forms are embedded in the binary and
bootstrap-deployed at startup, idempotently by content checksum; keep the native
Users console as the direct/break-glass surface and let governed processes be the
front door, both writing the one user store.**

Concretely (proposed shape; a follow-up implements it test-first):

- **Reserved `system` owner.** A new reserved principal id (e.g. `system`),
  separate from `system:mcp`. `system:mcp` stays intentionally non-admin
  (ADR-0049); the bootstrap deployer is its own identity with exactly the
  authority to seed the system project and nothing else.
- **A `protected` project.** The `project` record gains a `Protected bool`
  (omitempty, so existing records are unaffected — the ADR-0044/0071 additive
  pattern). `effectiveRole`/the project + deployment handlers refuse rename,
  delete, membership change, draft-overwrite, and `delete_process` for a
  protected project and its definitions, for **every** caller. It remains
  readable and its processes startable per role.
- **Embedded bundle.** The system processes and their forms are embedded via
  `//go:embed` as a versioned bundle (each process carries a checksum). No
  external files; nothing over the network.
- **Idempotent bootstrap deploy at startup.** On start, on the run-loop owner
  (never beside it), the server ensures the system project exists and, for each
  embedded process, deploys it **only if** absent or if the embedded checksum
  differs from what is deployed. Same binary, same content → zero new versions on
  restart. New binary with a changed process → exactly one new version; old-
  version instances keep running.
- **Coexistence with the native console.** The Console → Organization → Users
  screen (ADR-0044) remains the **direct, synchronous, break-glass** admin
  surface. Governed lifecycle **processes** (intake, access review, offboarding)
  are the standard front door for requested and approved change. Both ultimately
  write the same user store; the privileged mutation stays a human admin action
  (or, later and deliberately, a sanctioned connector — see below).

### Relationship to the native Users console (direct CRUD vs. governed process)

The native Users screen is **direct manipulation of records**: click *Disable* →
it happens now, the table refreshes. Wrapping that in a BPMN instance would add
ceremony (start → claim → complete), remove the immediate feedback, and still
have to call the same admin-gated API — no gain. So **synchronous admin CRUD
stays a native screen; it is not reimplemented as a process.**

What *does* belong on processes is the **governed lifecycle around** those
records — work that has real process nature (steps, multiple people, approval,
side effects, audit, deadlines):

> **Rule of thumb.** A state transition with duration / participants / approval /
> side effects → a process. Immediate direct manipulation of one record → a
> native screen.

The risk to manage is a **split write path**: if some changes route through a
governed process (approval + audit + mail) and others bypass it via the console,
the process's guarantees leak. Coexistence is therefore an explicit stance, not
an accident: the console is break-glass, the process is the front door, and if an
instance needs *all* non-emergency user changes governed, that is a deliberate
follow-up (demote the console's direct create to break-glass only). The protected
system project is where that policy — and any future sanctioned automated
write-path that opens the ADR-0044/0049 boundary — would live, because it is
platform-owned and non-editable, not an arbitrary hand-editable tenant project.

### Consequences

- **Positive:** Atlas can ship operating processes that are present on every
  install, safe from accidental edit/delete, and upgraded cleanly with the
  binary. The system project is a clear, trusted home for "Atlas runs itself."
  No engine, WAL, processor, or `applyToState` change — a bootstrap deploy reuses
  the existing durable-deployment path. The additive `Protected` field needs no
  data migration.
- **Negative / trade-offs accepted:**
  - A new reserved principal and a protection check on several project/deployment
    handlers — a real surface to get right and test (including "a protected
    project cannot be deleted even by an admin").
  - Startup does a little more work (checksum compare + possibly a deploy); it
    must stay off the hot path and behind the run-loop owner.
  - Idempotency now depends on a stable content checksum; a careless change to
    embedding/formatting could churn versions. Covered by a test asserting "boot
    twice → one version".
- **Follow-ups / risks to watch:** the sanctioned automated write-path for user
  changes (a `restConnector` on `/api/users` with a vault-resolved admin
  credential, ADR-0041/0067) as its **own** decision that opens ADR-0044/0049 for
  the system project only; access-review and offboarding processes as further
  system-project residents; a Console view that surfaces which deployed
  definitions are platform-protected; whether non-admins may *start* specific
  system processes (e.g. an access *request*) via role, not just admins.

## Pros and cons of the options

### Ordinary project vs. protected system project
- **Protected system project (chosen)** — Good: platform content can't be edited
  or deleted as if it were tenant work; a clear owner and boundary. Bad: a new
  reserved principal and protection checks to implement and test.
- **Ordinary project** — Good: nothing new to build. Bad: platform processes are
  one careless *Delete* away from gone; no distinction from user content.

### Manual deploy vs. embedded bootstrap
- **Embedded bootstrap (chosen)** — Good: present on every fresh install, no
  external files, upgrades with the binary; reuses the ADR-0081 embed precedent
  and the ADR-0019 deployment store. Bad: startup logic + a durable idempotency
  key to get right.
- **Manual deploy** — Good: zero startup logic. Bad: a fresh instance has no
  platform processes until someone remembers to author them; not "ships with the
  install".

### Fully read-only vs. platform-managed protection
- **Platform-managed (chosen)** — Good: the processes can actually *run* while
  their definitions stay immutable to the API. Bad: "protected" is a specific set
  of refused operations, not a single flag on a lock — more to specify.
- **Fully read-only** — Good: simplest mental model. Bad: too strong — you could
  not start the very processes the feature exists to run.

### Process-only write path vs. coexistence
- **Coexistence (chosen)** — Good: keeps the fast, synchronous admin console for
  direct/break-glass work while offering a governed front door; one store. Bad:
  the split-write-path risk must be managed as policy.
- **Process-only** — Good: every change is governed and audited. Bad: turns an
  immediate operator action into a multi-step instance; heavy, and a poor fit for
  break-glass.

## Links

- relates to ADR-0034 (projects as artifact containers), ADR-0071 (project
  sharing scopes), ADR-0044 (user management & the auth boundary), ADR-0049
  (the MCP service principal is intentionally non-admin), ADR-0081 (embedded,
  bundled catalog precedent), ADR-0019 (durable deployment sidecar store),
  ADR-0021 (diagram drafts), ADR-0011 (single-binary distribution with embedded
  assets)
- builds on the engine invariants I2 (durable before visible) and I3
  (single-writer) — a bootstrap deploy is an ordinary deploy on the run-loop owner
