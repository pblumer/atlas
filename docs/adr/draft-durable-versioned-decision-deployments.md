# ADR-DRAFT: Durable, versioned decision deployments — and `latest` resolved when a process is deployed

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-13
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0014](0014-dmn-business-rule-tasks-via-temis.md) made a DMN model a *piece of
a BPMN deployment*: `deployModel` resolves the models a process's business rule
tasks need, snapshots their XML into the process's `persistedDeployment`, and
registers them in the `dmn.Registry` under the **process-definition key**. There
is no such thing as a deployed decision — only a process that happens to carry
one.

[ADR-0063](0063-dmn-decision-binding.md) then honored `zeebe:calledDecision`'s
`bindingType`. `deployment` evaluates the snapshot the process was deployed with.
`latest` — the default, matching Camunda — evaluates *the newest deployed model
that provides the decision*, resolved **at task activation**: the registry keeps a
`latest map[decisionId]*Definitions` that every `Deploy` overwrites, and the worker
calls `EvaluateLatest` each time a token reaches the task.

Three concrete problems follow, and they compound.

1. **A decision cannot be published on its own.** An application
   ([ADR-0128](0128-process-applications.md)) whose only artifact is
   `eligibility.dmn` publishes *successfully* and deploys **nothing**: the bundle
   deploy iterates BPMN drafts and collects the DMN models those drafts reference,
   so with no draft there is no loop iteration, no registry entry, and no durable
   runtime record. The release manifest it mints is empty. After a restart there is
   still nothing — the decision was never a runtime artifact, only a design-time
   reference to a file in the model folder.

2. **`latest` makes an already-deployed process change behavior.** Publishing a
   newer version of a decision silently re-points every `latest`-bound task in
   every process already running against it. That is what Camunda's default means,
   but in Atlas it is worse than a surprise: it is a **replay hazard**. The engine's
   contract is that events are facts and a replay re-decides nothing (I6). A
   decision version selected on the worker at activation time is a routing decision
   made *outside* the log, from mutable in-memory state, and the answer depends on
   what has been deployed since.

3. **A BPMN deployment quietly owns the `latest` pointer for a decision id.**
   Because `Registry.register` updates `latest` for the models bundled with a
   process, deploying a *process* is what moves the pointer. There is no notion of
   a decision version that exists independently of some process's deployment, so
   "the newest version of the decision" and "the newest process that happened to
   carry it" are the same thing.

The question: **how does a decision become a durable, versioned Atlas deployment
artifact in its own right — publishable from an application with no BPMN in it —
and what does `latest` mean once it is one?**

## Decision drivers

- **A process's behavior must not change because something else was deployed.** A
  deployed definition is reproducible; whatever a `latest` reference resolved to
  has to be frozen with the process that made the reference.
- **Nothing may be re-decided on the runtime path or on replay** (I5/I6). All
  name-and-version resolution that can happen at deploy time must happen there.
- **Persist source, not compiled objects.** The durable record must be DMN XML plus
  stable metadata; the temis registry is rebuilt from it. Compiled temis structures
  are an internal representation with no compatibility contract — persisting them
  would pin Atlas's on-disk format to a dependency's internals.
- **Durable before visible** (I2). A decision must not be evaluable, listed, or
  claimed by a release manifest before its record is on disk.
- **Reuse what exists.** Atlas already has a sidecar store discipline, a global
  definition key space (`Server.nextKey`), a release manifest, and an
  all-or-nothing bundle publish. A second, parallel mechanism for any of those
  would be the wrong answer.
- **No silent semantic change for deployments already on disk.** An installation
  upgrading into this must keep the behavior its running definitions were deployed
  under, whatever we decide the new one is.

## Considered options

1. **Leave the runtime lookup, add durability only.** Store decisions durably and
   keep resolving `latest` at task activation.
2. **Resolve `latest` when the process is deployed and pin the exact decision
   deployment (chosen).** A decision becomes its own durable, versioned deployment
   artifact; a BPMN deployment freezes each `latest` reference to a concrete
   decision-deployment key at deploy time.
3. **Drop `latest` and make every binding `deployment`.** Only ever evaluate the
   model snapshotted with the process.
4. **Model decision deployments as engine events.** Put decision deployment in the
   event log next to process state.

## Decision outcome

Chosen option: **"Resolve `latest` when the process is deployed and pin the exact
decision deployment" (option 2).**

### A decision deployment is a runtime artifact

Publishing an application deploys its DMN artifacts as **decision deployments**,
alongside — and independently of — its BPMN drafts. A decision deployment is a
durable record in a new `decisions/` sidecar store, the same shape and discipline
as the `deployments/` store ADR-0019 established:

```
key            uint64   a key from the one global definition key space (Server.nextKey)
applicationId  string   the application it was published from (the on-disk projectId)
artifactId     string   the DMN reference artifact it was published from
modelRef       string   the resolver handle the model was read from
resourceName   string   "<modelRef>.dmn" — what the release manifest names
modelName      string   the DMN <definitions name>
decisions      [ ... ]  one entry per decision the model provides: id, name, version
checksum       string   sha256 of the XML, hex
deployedAt     int64    unix seconds
deployedBy     string   the account that published it
xml            string   the validated DMN 1.x source
```

What is **not** in it: compiled temis objects, FEEL programs, or any other internal
structure. The XML and the metadata are the record; `dmn.Registry` is rebuilt from
the XML at startup by compiling it again, off the processor and before the loop
serves traffic — the same thing `loadDeployments` already does for a bundled model.

An application with no BPMN draft at all publishes its decisions and mints a
release that references them. That is the acceptance criterion this record exists
for.

**Versions are counted per decision id**, not per model file: `eligibility v1, v2,
…` is the lineage an author reasons about, and it is intrinsic to the model's
content rather than to the handle the file happens to sit under. A model providing
two decisions advances both counters and stores a version per decision. Like a BPMN
deploy (ADR-0019), a publish always mints a new version; Atlas does not
content-dedupe a redeploy, and doing it for decisions alone would be a second rule
to learn.

### `latest` is a deployment-time selector

`latest` stays a legal `bindingType` and stays the default. What changes is **when
it is evaluated**:

```
process deployment
  └─ businessRuleTask, calledDecision decisionId="eligibility" bindingType="latest"
       └─ resolve NOW, once
            └─ store the concrete decision-deployment key in the deployment record
```

The rule, applied per local `latest`-bound task at deploy time:

1. if a **decision deployment** provides that decision id, pin its key — the newest
   such deployment;
2. otherwise pin **this process deployment's own key**, i.e. the model snapshotted
   with it.

Rule 2 is what keeps the change honest for a process whose decision has never been
published on its own: `latest` and `deployment` then resolve to the same model,
which is precisely the equivalence ADR-0063 already noted for a decision deployed
once. It also means a raw `POST /api/v1/deployments` with a bundled model behaves
as it did.

Because a publish deploys the application's decisions **before** its drafts, a
process published together with a decision pins to that decision's fresh
deployment, not to its own bundled copy.

`deployment` binding is unchanged: the model snapshotted with this process, under
this process's key. The two bindings now differ in *which lineage* they follow, not
in *when* they decide:

| binding | resolves to | when |
|---|---|---|
| `deployment` | the model bundled into this process deployment | deploy time (unchanged) |
| `latest` | the newest decision deployment of that decision id, else this process's own bundle | **deploy time** (was: task activation) |

At runtime the worker evaluates `registry.Evaluate(pinnedKey, decisionId, …)` and
makes no version choice at all. Replay makes none either: there is nothing left to
decide.

### The registry separates the two lineages

`dmn.Registry` keeps compiled models under a deployment key, and that key space is
now shared by process deployments and decision deployments — which is why one map
serves both and `Evaluate(key, decisionId, …)` needs no new shape. What it gains is
a **separate index of decision deployments**: `latestDecision map[decisionId]key`,
updated **only** by `DeployDecision`/`ReloadDecision`. Registering a model bundled
with a process no longer makes that process the newest version of a decision.

The pre-existing "newest model registered, of either kind" pointer stays, under its
own name, for exactly one purpose: the definitions deployed before this record,
described next.

### Deployments written before this keep ADR-0063 semantics

A deployment record now carries a **binding policy marker**:

- `bindingPolicy: "pinned"` plus `decisionBindings: [{decisionId, key}, …]` on
  every deployment written from here on;
- **absent** on every record written before, which is read as
  `legacy-runtime-latest`.

A legacy record's `latest`-bound tasks keep calling `EvaluateLatest` at activation,
against the "newest registered model of either kind" pointer — bit-for-bit the
ADR-0063 behavior, including the fact that a newly published decision deployment
moves it. Nothing on disk changes meaning, and no migration runs.

The alternative — pinning legacy records at load time — was rejected: it does not
preserve the old behavior (a process would jump to the newest version at each
restart and freeze there), so it is neither the old semantics nor the new one.

A legacy record is not upgraded in place. Redeploying the process is what moves it
to `pinned`, which is a deliberate act with a visible result.

### Publish stays all-or-nothing, in the order the invariants require

Inside one run-loop turn, publish does: every draft's message claim first (a
refusal must not leave decisions deployed), then **persist every decision record**,
then register them with the registry, then deploy the drafts. Persisting before
registering is I2 for this path: a decision that is not on disk is never evaluable,
and a failed publish leaves nothing that the next restart would not also produce.
The release manifest is minted only after the whole bundle registered, so it can
never claim a runtime artifact that does not exist.

The residual non-atomicity is the one ADR-0034 already documents and did not
introduce: a persist failure *between* two drafts of a bundle leaves the earlier
ones deployed. Decisions are now inside that same boundary rather than outside it,
which is strictly better than before, where they were not deployed at all.

### The release manifest names decisions

`releaseMember` gains `kind: "decision"` alongside `kind: "process"`, with `ref`
the decision id, `artifactVer` its version, `key` the decision deployment, and a
new optional `artifact` naming the resource (`eligibility.dmn`). Existing release
records are unchanged and unaffected — they simply carry no decision members.

### Consequences

- **Positive:** a decision is a deployable, versioned, durable Atlas artifact; an
  application can consist of decisions alone; a deployed process's decision version
  is frozen with it and survives restart exactly; the runtime and replay make no
  version choice, so I5/I6 hold where they did not; the Temis registry is rebuilt
  from source, so no compiled structure is ever persisted; publishing a decision no
  longer requires a BPMN process to carry it.
- **Negative / trade-offs accepted:** Atlas's `latest` is no longer Camunda's
  `latest` — an author who wants a running process to follow a new decision version
  must redeploy the process, and the Modeler's binding help has to say so. Two
  indexes exist in the registry where one did, the second one purely to hold
  pre-existing deployments still. Every publish mints a decision version, so a
  repeatedly published application accumulates records (the same growth profile a
  repeatedly deployed BPMN draft already has).
- **Follow-ups / risks to watch:** `versionTag` binding (pin to a named version) is
  now buildable and still deferred; a retention/cleanup story for superseded
  decision deployments is not written; **promoting a release to a remote target
  still carries only its processes** (ADR-0129's bundle has no decision artifact
  shape), so a decision-only application reaches a target with nothing to deploy —
  the manifest names its decisions, the transport does not move them yet; and the
  legacy runtime-latest path is code that exists only for old records and should be
  removable once no supported installation carries one.

## Pros and cons of the options

### Option 1 — durability only, keep the runtime lookup
- Good: smallest change; stays byte-compatible with Camunda's `latest`.
- Bad: leaves the actual defect. A deployed process still changes behavior when
  somebody publishes a decision, and the version is still chosen off-log at
  activation, which is the replay hazard #915 is about.

### Option 2 — pin at deploy time (chosen)
- Good: a deployment is reproducible; the runtime and replay decide nothing; the
  work lands at deploy time where invariant I5 says it belongs; a decision gets a
  first-class lineage independent of any process.
- Bad: diverges from Camunda's default semantics; needs a compatibility marker for
  records already on disk.

### Option 3 — drop `latest`
- Good: simplest possible runtime; one lineage.
- Bad: breaks authored models that state `bindingType="latest"` (the moddle's
  default, so effectively most of them), and throws away a genuinely useful
  authoring intent: "when I deploy this process, take the decision as it stands".

### Option 4 — decision deployments as engine events
- Good: one source of truth; recovery comes for free with the log.
- Bad: contradicts ADR-0128's whole stance that applications and their publishing
  are a design-time layer *below* the HTTP API and absent from the log; would put
  DMN XML in the WAL; would make a deployment a partition-scoped fact when it is a
  server-wide one. The sidecar-plus-rebuild discipline ADR-0019 set for process
  definitions already solves exactly this problem and is what this follows.

## Links

- builds on [ADR-0014](0014-dmn-business-rule-tasks-via-temis.md) — DMN business
  rule tasks via temis, and the deploy-time snapshot
- supersedes the `latest` semantics of
  [ADR-0063](0063-dmn-decision-binding.md); its `deployment` semantics and its
  `bindingType` authoring surface stand
- relates to [ADR-0019](0019-durable-deployments.md) — the sidecar store and
  reload discipline this follows
- relates to [ADR-0062](0062-embedded-dmn-editor.md) — the embedded dmn-js editor
  that authors the models this deploys
- relates to [ADR-0128](0128-process-applications.md) — the application as the
  publish boundary, and the release manifest extended here
