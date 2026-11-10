# ADR-0547: Refuse the sequence-flow shapes BPMN forbids, and locate element-local compile errors

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-11-09
- **Deciders:** Patrick Blumer (programme owner, BPMN conformance)
- **Open question:** how many *stored production* deployments contain one of the four shapes.
  The repository's own corpus (259 `.bpmn` files, scanned 2026-11-09) contains none, but
  that says nothing about models authored on running servers. The decision does not depend
  on the number — reload stays tolerant either way — but the release note's wording and
  whether a grace period is worth a release does. Also open: the exact OMG BPMN 2.0.2
  clause wording for each prohibition, which the CON0 per-clause review has not yet
  confirmed.
- **Question checked:** 2026-11

## Context and problem statement

The BPMN conformance programme's CON2 slice pinned atlas's deploy-time diagnostics in
`conformance/normative_diagnostics_test.go`. Two kinds of gap came out of it.

**Four shapes the specification forbids are deployed without an error.** Each is
characterised by an executable test in `TestCON2KnownAcceptedSpecViolations`:

1. **A start event that is the target of a sequence flow.** BPMN allows a start event no
   incoming sequence flow. Atlas deploys it; executed, the flow re-enters the start event,
   and the model in the test loops until the per-token step budget raises an incident.
2. **An end event that is the source of a sequence flow.** BPMN allows an end event no
   outgoing sequence flow. Atlas deploys it and **never takes the flow** — what the author
   drew after the end event silently does not run.
3. **A gateway `default` that names a flow which is not one of the gateway's outgoing
   flows** (in the test, the flow *into* the gateway). The attribute is effectively ignored;
   the gateway parks with an incident when no condition holds, and the modeler reports only
   the generic `gateway.missing-default` warning, which does not say what is wrong.
4. **A gateway `default` that names another gateway's flow.** This is the serious one.
   `markDefault` in `compiler/scope_compile.go` resolves the id to a flow index and calls
   `Builder.SetFlowDefault` without checking the flow's source. The *other* gateway then
   treats that flow as its default: it routes there **although the flow's own condition is
   false**, and its `gateway.missing-default` warning disappears. In the test, a gateway
   whose two conditions are both false — which must not route at all — reaches an end event.
   That is a silent mis-route, not a refusal or an incident.

Registry row FLOW-14 ("model validation") moved from `implemented` to `partial` on this
evidence.

**Element-local compile errors carry no location.** Ten faults the compiler does refuse —
duplicate id, unknown error/escalation/signal/message reference, link throw without a
catch, unparsable condition, dangling flow, boundary on an unknown host, loop without a
bound — come out of `compiler.ValidateModel` as one `Problem` with rule `compile` and an
**empty `Element`**; the id is only in the message text (`TestCON2InvalidModelsHaveCoarseDiagnostics`).
The Problems panel (ADR-0026) cannot select the element, and a client cannot group by it.
Graph-wide (stage 5) problems do carry the element. There are 372 `fmt.Errorf("compiler: …")`
sites in `compiler/`; about 128 of them (`parse.go`, `scope_compile.go`, `builder.go`)
are BPMN-structural, the rest Worker Type configuration.

What already exists and constrains the answer:

- **Reload is tolerant of rules added later.** ADR-0177 made the deploy gate a gate on
  *deploying*, not on *loading*: `compiler.ReloadNamed` returns a stage-5
  `ValidationError`'s process together with its problems, and `api.restoreDeployment` logs
  `deployment.reloaded_with_problems` per deployment and serves on. ADR-0393 generalised
  this to any rule the compiler gains later, whatever stage it is implemented in, after a
  build-stage rule took a production server into a crash loop. Its open question is exactly
  the risk here: the next rule must land on the right side of that line.
- **The compiled process is the same on both paths** (I5). Validation decides whether a
  model may be deployed; it never changes what a deployed model does.

The question: should atlas refuse these four shapes at deploy, how, and without changing
what a definition that is already stored does on its next restart? And should element-local
errors carry their element?

## Decision drivers

- **An upgrade must not be an outage** (ADR-0177, ADR-0393). A stored definition with one
  of these shapes must still load.
- **The definition that comes back must be the one that ran** (ADR-0393). A reload must not
  "repair" a stored model — not even shape 4, whose current behaviour is wrong — because
  its running instances were started under that behaviour.
- **A silent mis-route is worse than a refusal.** A deploy refusal is seen by the author who
  can fix it; a mis-route is seen by nobody until the data is wrong.
- **The modeler and the deploy must agree.** `ValidateModel` and `Parse` share stage 5, so a
  rule there is shown in the Problems panel and enforced at deploy by one implementation.
- **Compile, don't interpret** (I5): all checks at deploy time, nothing on the hot path,
  no change to `applyToState`, the WAL or any persisted format.
- **Diagnostics are an API.** Rule slugs are stable identifiers (validation.go); changing
  a slug that clients group by is a breaking change, adding an element field is not.

## Considered options

For the four shapes:

1. **Leave and document.** Keep the characterisation tests and FLOW-14 `partial`.
2. **Stage-5 errors now.** Three new graph-wide rules, refused at deploy, reported on
   reload through the existing ADR-0177 path.
3. **Warning for one release, then error.** Ship the same rules at warning severity, promote
   them in the next release.
4. **Repair at compile.** Drop the outgoing flow of an end event, ignore a foreign default,
   without refusing anything.
5. **Refuse at compile stage 3**, inside `markDefault` and the event registration, as plain
   errors.

For element locations:

- **A. A typed compile error** carrying the element id, unwrapped by `ValidateModel`.
- **B. Parse the id out of the message.**
- **C. Leave it.**

## Decision outcome

Chosen: **option 2 for the shapes, option A for locations**, delivered as two separate
vertical PRs.

### Part 1 — three stage-5 rules

| Rule slug | Element | Fires when |
| --- | --- | --- |
| `start.incoming-flow` | the start event | any start event (process, subprocess or event subprocess level, every trigger type) is the target of a sequence flow |
| `end.outgoing-flow` | the end event | any end event (every result type) is the source of a sequence flow |
| `gateway.default-not-outgoing` | the declaring gateway | an exclusive or inclusive gateway's `default` names a flow whose source is not that gateway — covering shapes 3 and 4 |

All three are `SeverityError`, raised in `compiler.Validate` so they arrive as a
`ValidationError` with a process. That one placement gives each rule the ADR-0177/0393
split for free: `Parse`, `ParseAll`, `ParseNamed` and `ValidateModel` refuse; `ReloadNamed`
returns the process and the problem, and `restoreDeployment` logs it under
`deployment.reloaded_with_problems`.

Shape 4 needs one piece of compile-time metadata that stage 5 does not have today: which
gateway declared a flow as its default. `markDefault` records the declaring gateway's
element index beside the flow (deploy-time data on the `CompiledProcess`, never read by the
engine) and **keeps calling `SetFlowDefault` exactly as now**. The compiled routing is
therefore unchanged, so a reloaded definition with a leaked default mis-routes on the next
restart exactly as it did before it — the warning in the log is what points at it. Fixing
the routing of an already stored definition would change the behaviour of its running
instances and is explicitly not part of this decision; the author fixes the model and
redeploys.

The modeler needs no change: it renders server problems generically, and the new slugs are
anchored to an element like every other stage-5 rule.

**Why not option 3.** Its strongest form: operators get a release in which every affected
model is visible without any deploy being refused, so no author is surprised. But the only
thing option 2 refuses is a *new deploy* of an affected model, and that refusal names the
element and the rule; the stored definitions keep running, and the first start of the new
build lists every affected deployment in the log — the same visibility option 3 offers, at
the same moment. A warning release would also leave shape 4 mis-routing *newly deployed*
models for a further release. Option 3 remains the fallback if the open question turns up
a large number of stored models whose authors need warning before the refusal.

**Why not option 4.** It changes what a stored definition does on reload, the one thing
ADR-0393 rules out, and it still leaves the author's model wrong without telling them.

**Why not option 5.** A plain compile-stage error has no process to bring back, so it is
fatal on reload — the crash loop of ADR-0393, recreated on purpose.

### Part 2 — element locations

Introduce `compiler.ElementError{Element string; Err error}` whose `Error()` is the
wrapped message unchanged and which unwraps to it. The BPMN-structural sites (the roughly
128 in `parse.go`, `scope_compile.go` and `builder.go`, including the ten pinned faults)
return it; `ValidateModel` takes the element with `errors.As` and keeps the rule `compile`.
Worker Type configuration sites follow in later PRs as they are touched.

- The **message text is unchanged**, so a deploy's error response and every existing test
  on it read the same.
- The **rule slug is unchanged**, so a client grouping by rule sees the same groups; it
  gains an element where it had an empty string.
- **Reload is unchanged**: these faults leave no process to bring back and stay fatal, as
  ADR-0177 drew the line.

Option B is rejected because the message is deliberately not a stable API; option C leaves
the Problems panel unable to select ten common faults.

### Consequences

- **Positive:** the four shapes are refused where the author is watching, with an element
  and a rule; shape 4 stops producing new mis-routing deployments. FLOW-14 can be
  re-assessed by the CON0 reviewer.
- **Positive:** element-local faults become selectable in the Problems panel without any
  change to deploy responses.
- **Negative / trade-offs accepted:** a stored definition with shape 4 keeps mis-routing
  until it is redeployed; the reload log is the only signal. That is the ADR-0393 asymmetry,
  accepted for the same reason.
- **Negative:** an author who redeploys a model containing one of the shapes is refused
  where yesterday they were not. The release note must say so and name the three slugs.
- **Follow-ups / risks to watch:** the CON0 review may find further shapes of the same kind
  (for example a link catch with an incoming flow); each is added as a stage-5 rule by the
  same pattern, not by widening these three. A rule that cannot be stage 5 must use the gate
  value of ADR-0393, never a plain compile error.

### Delivery and acceptance

1. **PR 1 — rules.** The three rules in `compiler/validation.go`, the declaring-gateway
   metadata in `markDefault`; compiler unit tests per rule; the four
   `TestCON2KnownAcceptedSpecViolations` cases move to
   `TestCON2InvalidModelsHaveLocatedDiagnostics`; an `api` reload test that stores a
   deployment with each shape, restarts, and asserts the definition loads, the problem is
   logged, and shape 4 still routes as before. CHANGELOG under *Changed*.
2. **PR 2 — locations.** `ElementError` and the structural sites; the tripwire in
   `TestCON2InvalidModelsHaveCoarseDiagnostics` flips to asserting the element for the
   cases it covers.
3. Both: `go build ./...`, `go test -race -timeout=45m ./...`, `go vet ./...`, `gofmt -l .`,
   the Windows vet line, `python3 scripts/conformance_registry.py --check` with FLOW-14
   updated.

**Invariants.** No change to the processor, `applyToState`, the WAL, the state store or any
persisted format (I1–I4, I6); all work is compile-time (I5). The new metadata lives on the
immutable `CompiledProcess` and is read only by `Validate`.

**Scale-out readiness.** Compatible. Deploy validation is a stateless, per-request
compile-time check with no partition state, routing or cross-partition interaction; a
future multi-node deploy path runs the same compiler. Standalone mode is unchanged.

## Pros and cons of the options

### Option 1 — leave and document
- Good: no change, no risk to any author.
- Bad: shape 4 keeps producing silent mis-routes on new deploys; FLOW-14 stays `partial`.

### Option 2 — stage-5 errors now
- Good: one placement covers deploy, modeler and reload; refusals are located; reload
  tolerance is inherited, not re-implemented.
- Bad: needs one piece of new compile-time metadata for shape 4; redeploying an affected
  model is refused without a grace release.

### Option 3 — warning first
- Good: a release of visibility before any refusal.
- Bad: one more release of new mis-routing deployments; the reload log already gives the
  same visibility under option 2.

### Option 4 — repair at compile
- Good: nothing is refused.
- Bad: changes stored definitions' behaviour on restart (ADR-0393), and hides the model
  error from its author.

### Option 5 — refuse at compile stage 3
- Good: smallest diff.
- Bad: fatal on reload; recreates the ADR-0393 outage.

## Links

- relates to ADR-0026 (Problems panel), ADR-0177 (reload skips the deploy gate),
  ADR-0273 (gateway routing incident), ADR-0393 (a rule added later is a gate on deploy)
- evidence: `conformance/normative_diagnostics_test.go`; programme plan
  `docs/planning/bpmn-conformance-program.md` (CON6)
