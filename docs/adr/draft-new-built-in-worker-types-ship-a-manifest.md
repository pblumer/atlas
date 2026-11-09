# ADR-DRAFT: Every new built-in Worker Type ships a manifest that declares its operations' idempotency and effect

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-11-09
- **Deciders:** pblumer (maintainer; accepted rule A on 2026-11-09, SharePoint Server first)

## Context and problem statement

[ADR-0538](0538-operations-declare-idempotency-and-effect.md) makes every operation of a
Worker Type declare what a second delivery of one of its jobs does (`idempotency`,
`effect`), and the manifest parser refuses a manifest without them. But only installed
artifacts carry a manifest ([ADR-0535](0535-worker-types-as-distributed-artifacts.md)
step 3). The built-in Worker Types carry none, so the declaration is required of third
parties and of nobody in this repository — and new built-in types keep arriving that
way: SharePoint Server ([ADR-0540](0540-sharepoint-server-worker.md)) landed on
2026-11-09 with a statement in prose ("repeated delivery is safe for every operation")
and no declaration a tool can read.

Migrating every built-in type onto the artifact path (ADR-0535 steps 4 and 8) is the
long answer; it touches stored configuration and needs the generic server half first.
The question here is narrower: what a *new* built-in type must bring, starting now.

## Decision drivers

- The duplicate behaviour of an operation is a declared fact for atlas's own Worker
  Types as much as for anyone else's.
- The rule must be checkable by a test, or it erodes as ADR-0167's package mandate did.
- No migration of existing installations is implied.

## Considered options

1. **Rule A:** every new built-in Worker Type ships a manifest with the declaration; the
   existing ones are listed and owe theirs.
2. Rule A plus a date after which new types are only artifacts.
3. No rule until the migration of ADR-0535 step 4.

## Decision outcome

Chosen: **option 1.** A date (option 2) is not decided: an artifact can today be
installed only with `--allow-unsigned-worker-types`, which is for development, so "only
artifacts" before ADR-0535 step 5 would mean a new type no production server can run.
It is a follow-up, to be reviewed once a first-party artifact is installable in
production.

### The rule

- A connector kind in `api/releasedkinds.go` names its manifest in the row's
  `Manifest` field: an `atlas.io/v1alpha1` Worker Type manifest with runtime
  `bundled`, the row's job type among `spec.jobTypes`, and every operation the Worker
  runs in `spec.operations` with `idempotency` and `effect`.
- The kinds released before this record are listed by name in `kindsWithoutManifest`.
  The list may only shrink; a kind not on it must have a manifest. Names rather than a
  count, so one kind gaining a manifest cannot pay for another arriving without one.
- Script-task kinds (`classScriptTask`) and engine job types are outside the rule: the
  first run the model's own code, whose duplicate behaviour is the author's; the second
  are not Worker Types.
- A Worker Type's own package tests that its manifest names exactly the operations it
  runs, so the declaration cannot drift from the operation table.

### SharePoint Server, the first

`connector/sharepointserver/manifest.json`, checked against the Worker's PowerShell
program rather than against ADR-0540's sentence:

| Operation | Declared | Why |
|---|---|---|
| `get-site`, `list-sites` | natural / reversible | reads |
| `set-site-owners`, `set-site-quota` | natural / reversible | set a value |
| `update-site-admins`, `update-group-members` | natural / reversible | add or remove an account; already as asked changes nothing |
| `delete-site` | check / reversible | a site already in the recycle bin succeeds |
| `restore-site` | check / reversible | a site already back succeeds |
| `create-site` | check / reversible | an existing, finished site is not created twice |

One finding the table does not show: a second delivery of `create-site` after a first
one that finished (the worker died before it reported) does not succeed — it fails with
`SHAREPOINT_SERVER_SITE_EXISTS`, because the Worker cannot tell a site it created from a
site somebody else made at that address. The effect is not repeated, so `check` is
honest; but a process that catches that code may take its "already exists" path for a
site it created itself. ADR-0540's "repeated delivery is safe for every operation" holds
for the effect, not for the outcome a process sees. Settling it — for example by
stamping the job's `idempotencyKey` into the site's property bag and treating a match as
success — changes the Worker's behaviour and is left to a decision of its own.

### Consequences

- **Positive:** atlas's own new Worker Types are held to the declaration third parties
  are; the 31 that owe one are named in the source with a test on the list.
- **Negative / trade-offs accepted:** for built-in types nothing reads the declaration
  yet — ADR-0538's warnings are not built — so until then it is documentation a test
  keeps honest; a manifest is one more file per type to maintain beside its catalog.
- **Follow-ups:** manifests for the 31 listed kinds; the date of option 2 once a
  first-party artifact is installable in production; the `create-site` outcome above.

## Links

- applies [ADR-0538](0538-operations-declare-idempotency-and-effect.md) to built-in Worker Types
- relates to [ADR-0535](0535-worker-types-as-distributed-artifacts.md) steps 4 and 8, [ADR-0540](0540-sharepoint-server-worker.md), [ADR-0167](0167-released-connectors-ship-in-the-marketplace.md)
