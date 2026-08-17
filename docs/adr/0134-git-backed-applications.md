# ADR-0134: Git-backed applications — a repository as an application's source of truth

- **Status:** Proposed
- **Date:** 2026-08-17
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0128](0128-process-applications.md) made the process application the unit of
bundling and versioning, and named four phases. Phases 1–3 shipped: the rename,
application releases, and — via [ADR-0129](0129-remote-deployment-targets.md) —
promoting a release to another Atlas server. This is Phase 4, the one still open:
**binding an application to a git repository**, so its artifacts can be read in
from git and written back.

Atlas has no git integration today. The only `git` in the tree is
`api/buildinfo.go`, which records the running binary's own commit for display. The
ways design-time work leaves an instance are the ADR-0107 backup tar, the ADR-0109
whole-instance snapshot, and now an ADR-0129 promotion — none of which is a
source-control workflow.

What a git binding is *for* is worth stating precisely, because it is easy to
confuse with Phase 3:

- **Promotion (ADR-0129) moves what is deployed.** A release travels to a server so
  it runs there. The unit is a frozen release; the direction is outward; the
  content is compiled-and-deployed bytes.
- **A git binding moves the source.** Drafts, forms, and decision references are
  what an author edits; git gives them history, review, branching, and a life
  outside this one server's disk. The unit is the working set; the direction is
  both ways; the content is what a human reads and edits.

Conflating the two would produce a repository full of deployment records nobody can
review, or a promotion mechanism that ships uncommitted drafts.

The question: **what does an application's repository contain, how does Atlas move
work in and out of it without becoming a merge tool, and how is an application
identified when its repository is cloned into a server that has never seen it?**

## Decision drivers

- **Stay a design-time concern, off the hot path.** Like ADR-0034/0128/0129, this
  lives below the HTTP API. Git I/O is an outbound side effect: resolve state on
  the run loop, do the I/O off it, record results back on it. Nothing enters the
  event log; nothing affects recovery (ADR-0002).
- **Keep the single binary (ADR-0010/0011).** No CGO, and no assumption that a
  `git` executable exists next to the server.
- **A repository is for humans.** Its whole value is diffs, review, and blame. A
  format that produces one-line JSON blobs with escaped XML inside has git's costs
  and none of its benefits.
- **Atlas must not pretend to merge BPMN.** Two people editing one diagram is a
  real problem, and a plausible-looking automatic merge of XML is worse than an
  honest refusal.
- **Credentials by reference, never by value** — the rule ADR-0041 set and ADR-0129
  followed for deployment targets.
- **Identity must survive a clone.** ADR-0129 deliberately left the portable
  application key to this ADR, because git is what actually needs it.

## Considered options

### A. What the repository contains

1. **A curated source layout** — real `.bpmn`, `.dmn`, and form files in named
   directories, plus a small manifest.
2. **The sidecar JSON as it sits on disk** — the ADR-0107 serialization, committed
   verbatim.
3. **One archive file per commit** — the backup tar, versioned.

### B. How git is spoken

1. **A pure-Go implementation (`go-git`)** linked into the binary.
2. **Shell out to a `git` executable.**
3. **No git protocol at all** — fetch a tarball over HTTPS from the forge's API.

### C. How divergence is handled

1. **Fast-forward or refuse.** Atlas never merges; a diverged branch is reported and
   resolved by a human, in a real git client.
2. **Automatic merge**, with textual conflict markers on failure.
3. **Last-writer-wins**, in whichever direction the operator triggered.

### D. Identity across clones

1. **A portable application key in the manifest.** Same key means the same
   application, on every server; the random per-server id stays local.
2. **Match by name**, as the ADR-0129 bundle import does.
3. **Keep the publisher's id** and require every server to adopt it.

## Decision outcome (proposed)

**A → 1** (curated source layout), **B → 1** (`go-git`), **C → 1** (fast-forward or
refuse), **D → 1** (portable key in the manifest).

### The repository is a source tree, not a database dump

```
atlas.json                      # manifest: key, name, format version
processes/mitarbeiter-onboarding.bpmn
processes/it-ausstattung.bpmn
decisions/freigabestufe.dmn
forms/neuer-mitarbeiter.form.json
```

Each artifact is its own file, in its own native format. A BPMN diagram is a
`.bpmn` file that opens in any modeler; a change to a gateway condition is a diff a
reviewer can read; two people touching different processes do not touch the same
file at all.

**This deliberately contradicts [ADR-0107](0107-backup-and-restore.md) option C,
which rejected a structured export because "the on-disk files already *are* the
serialization".** That reasoning was right for a backup and does not carry here,
because the two features want opposite things. A backup wants an exact, opaque,
complete restore of one instance's state — inventing a second schema there buys
nothing and risks drift. A repository wants the *content*, legible and editable by
tools that are not Atlas. The sidecar JSON, with its escaped XML payload and its
per-store bookkeeping fields, is unreadable as a diff and unmergeable in practice
(option A2); a per-commit archive (A3) is worse still, since git cannot diff it at
all and every commit rewrites the whole blob.

The cost is accepted and real: a second serialization to maintain, and a mapping
between it and the sidecar records that can silently drop a field when a store
grows one. Two mitigations are part of the decision — the manifest carries a
**format version**, and the round trip (export → import → export) must be
byte-stable, which is a property a test can assert rather than a hope.

Only **source** artifacts travel: drafts, forms, and decision references.
Deployments and releases do not. They are records of what happened on one server,
not source an author edits, and putting them under review would make every publish
a commit somebody has to approve.

### go-git, and what it costs

`go-git` is pure Go, so the binary stays CGO-free (ADR-0010) and self-contained
(ADR-0011). Shelling out to `git` (B2) would make the server's behaviour depend on
whether an executable happens to be installed and which version it is — a poor
property for a single-binary product, and a new process-execution surface. Fetching
a tarball from a forge API (B3) is simplest and works for import, but it is
forge-specific, cannot push, and gives up history entirely.

The honest costs of B1: a large dependency tree; `go-git` does not implement every
corner of the protocol the reference client does; and performance on very large
repositories is markedly worse. All three are acceptable for repositories holding a
handful of diagrams, and all three would need revisiting if that assumption breaks.

### Atlas never merges

A sync either fast-forwards or is refused with a clear statement of what diverged.
Atlas does not attempt a three-way merge of BPMN XML, and does not write conflict
markers into files it will later have to parse.

This is a deliberate limit, not an oversight. A textual merge of two BPMN diagrams
can produce a file that parses, deploys, and is silently wrong — the worst possible
failure for a workflow engine. Making the human resolve it in a real git client,
with a real diff, is the honest answer. Option C3 (last-writer-wins) is rejected for
the same reason with more force: it destroys work without telling anyone.

The consequence, accepted: an operator whose branch diverged has to leave the Atlas
UI to fix it. A later slice may offer per-file "keep mine / take theirs", which is a
choice a human can actually make correctly; automatic content merging is not on the
roadmap.

### The portable application key

The manifest carries a **key** — a stable, human-meaningful slug (`onboarding`) that
is the application's identity *across servers*. Cloning a repository into a server
that has never seen the application creates it with that key; importing into a
server that already has it updates in place. The random per-server id introduced by
ADR-0034 stays exactly as it is, local and unchanged, so nothing migrates.

Matching by name (D2) is what ADR-0129's bundle import does, and it is adequate
there because a promotion is an explicit act between two servers an operator has
paired. It is not adequate here: a repository is cloned by people, names get
translated and corrected, and "Onboarding" versus "Mitarbeiter-Onboarding" would
silently fork one application into two. Adopting the publisher's random id (D3)
would make one server's implementation detail into a global identifier, and gives no
answer for a repository created from scratch.

Keys are unique per server. An import whose key collides with a *different*
application is refused rather than merged — the same "say what happened, do not
guess" posture as everywhere else in this chain.

**This also settles the question ADR-0129 deferred.** With a portable key, a future
slice may address an application on a deployment target by key instead of the
learned binding record; the binding stays valid and becomes an optimisation rather
than the mechanism.

### Consequences

- **Positive:** an application's source gets history, review, and branching; its
  diagrams are editable by tools that are not Atlas; a repository becomes a portable
  definition of an application that any server can adopt; the identity question left
  open by ADR-0129 is answered; nothing in the engine, the log, or recovery is
  touched.
- **Negative / trade-offs accepted:** a second serialization and the drift risk it
  carries; a large new dependency; divergence is a human's problem, so an operator
  can be blocked in a way the UI cannot resolve; git credentials are one more secret
  class to manage; and an application can now be edited from two places at once,
  which is precisely why the merge posture above is strict.
- **Follow-ups / risks to watch:** whether per-file "keep mine / take theirs" is
  worth building; how a rename of an artifact is represented (a delete plus an add
  loses history); whether the format version needs a migration path before the
  first release; and whether the key should eventually replace ADR-0129's binding
  record outright.

## Open questions

1. **Does a commit happen automatically on save, or explicitly?** Auto-commit gives
   a complete history but floods the log with keystroke-level noise; explicit commit
   matches how people use git but lets work sit uncommitted on the server.
2. **One repository per application, or one repository with many applications?** The
   ADR assumes one-to-one, which is what makes the manifest simple. A monorepo of
   applications is a plausible ask and would change the manifest's shape.
3. **Should a git-backed application be read-only in the Modeler on a receiving
   server?** If a repository is the source of truth, local edits on a production
   server are the thing that creates divergence in the first place.

## Links

- implements Phase 4 of [ADR-0128](0128-process-applications.md) (process
  applications)
- relates to [ADR-0129](0129-remote-deployment-targets.md) (promotion moves what is
  deployed; this moves the source — and this ADR settles the portable key ADR-0129
  deferred)
- departs from [ADR-0107](0107-backup-and-restore.md) option C (a structured export
  was rejected for backup; the reasoning does not carry to a repository, and the
  section above says why)
- relates to [ADR-0034](0034-projects-and-artifacts.md) (the drafts, forms, and DMN
  references that travel, and the local id that stays local)
- relates to [ADR-0010](0010-go-and-no-cgo.md) and
  [ADR-0011](0011-single-binary-distribution-and-web-ui.md) (why go-git rather than
  a `git` executable)
- relates to [ADR-0041](0041-connector-management-and-secret-store.md) and
  [ADR-0069](0069-engine-internal-encrypted-secret-vault.md) (where the repository
  credential lives)
- relates to [ADR-0002](0002-single-writer-partition-model.md) (why git I/O runs off
  the run loop)
