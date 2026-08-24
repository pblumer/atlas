# ADR-DRAFT: Reload skips the deploy-time validation gate

- **Status:** Proposed
- **Date:** 2026-08-24
- **Deciders:** Atlas maintainers

## Context and problem statement

Deployments are durable (ADR-0019): each one is a JSON record under
`<data-dir>/deployments/<key>.json` holding the model XML, and `api.loadDeployments`
recompiles every record at startup before the loop serves traffic. That reload
runs the *whole* compile pipeline, stage 5 included — the graph-wide validation
that refuses a deploy (`compiler.Validate`, ADR-0026/Milestone 1) — and treats any
failure as fatal. ADR-0019 named that consequence and accepted it: "a definition
whose XML no longer compiles (e.g. after a compiler change) would fail startup
load — treated as a fatal, actionable error rather than silently dropped".

Validation rules, however, keep being added — that is the point of Milestone 1,
and each new rule catches a modeling mistake earlier. The rules apply to models
already on disk, and there the same fatality reads very differently. A server
running for months on a model deployed long ago is upgraded; a rule added in the
meantime refuses that model; the server exits during startup, the supervisor
restarts it, and it exits again. Every *other* definition and every running
instance is unreachable behind the one record that will not pass a gate that did
not exist when it was deployed.

This is not hypothetical. `variable.dotted-target` (a script task writing its
result to `customers.gesamtumsatz`) took down a running deployment on the next
upgrade, in a crash loop whose log named the deployment but offered no way back
in: the API that could delete or replace the definition only exists once the
server is up. The remedies available to the operator were to clear the data
directory — discarding every definition and all instance state — or to edit the
model XML inside the JSON record by hand.

The DMN models snapshotted into the same record (ADR-0014/0034) reload the same
way and fail the same way: `dmn.Registry.Deploy` refuses a model whose
diagnostics carry an error, and `loadDeployments` treats that as fatal too. A
decision that stops compiling under a newer temis is therefore the identical
outage, arriving through a dependency bump rather than an Atlas rule.

The question: what should the reload path do with a stored definition — or a
stored decision model — that today's checks would refuse?

## Decision drivers

- **Compile, don't interpret (I5).** Validation is stated, in the compiler
  itself, as a deploy-time gate: "a model that compiled and ran before runs
  identically; validation only decides, at deploy, whether it is allowed to."
  The compiled process is the same object either way.
- **An upgrade must not be an outage.** Adding a rule that helps authors must not
  be able to take down a server whose models nobody touched.
- **Availability of everything else.** One refused definition should not make the
  other definitions, and every running instance, unreachable.
- **Drift must stay visible.** A model that no longer satisfies the rules is
  still a model someone has to fix — silence would just move the surprise to the
  next deploy.
- **A gate still has to gate.** The rule must keep refusing the model at deploy,
  where the author is watching and can act on it.

## Considered options

1. **Keep the reload gated (status quo).** A stored model that fails today's
   validation is fatal; recover by clearing the data directory or hand-editing
   the record.
2. **Quarantine the record.** Keep the deployment listed but unregistered, log
   it, and let the server start without it.
3. **Reload without the deploy gate.** Compile the stored model, skip stage 5's
   refusal, register the definition, and report what today's rules say about it —
   and the same for a bundled DMN model's diagnostics.

## Decision outcome

Chosen option: **"Reload without the deploy gate" (option 3)**, because it
matches what validation already claims to be. A definition in the deployment
store passed the gate of the day it was deployed; its instances have been running
under it since. Re-applying today's rules at every restart does not make those
instances safer — it only decides, long after the fact, that a model that has
been executing correctly may no longer be *loaded*.

`compiler.ReloadNamed` is the reload path's entry point: it compiles the named
process and, when only stage 5 refused it, returns the compiled process together
with the Problems the gate raised (`ValidationError` now carries the process it
refused — the model is fully compiled by the time the gate runs).
`loadDeployments` registers the definition and logs one warning per record,
`event=deployment.reloaded_with_problems`, naming the deployment and the rules,
so the drift is visible in the place operators already watch. The deploy path
(`Parse` / `ParseAll` for a model being deployed, `ParseNamed` for one named
process out of it) is unchanged and still refuses the model with the rule named.

`dmn.Registry.Reload` is the same split for the decision models bundled with a
deployment: it registers the model and returns its error diagnostics rendered for
display instead of refusing. What makes that safe is temis's own contract —
malformed XML is a hard error, but a per-decision problem leaves the rest of the
model compiled and the affected decision "present but not executable". So a
decision that stopped compiling fails when something evaluates it, as a job error
on a worker, which is a failure the engine already has an answer for; every other
decision in the model keeps answering. The diagnostics are returned rendered
rather than structured because temis documents their messages as human-readable
and explicitly not a stable API: they are for an operator to read, not for code to
branch on. `Deploy` keeps the gate.

The line is drawn, on both paths, at "is there something to bring back": a record
that yields no compiled process at all (it does not decode, names no such process,
holds an expression that will not compile), or a DMN model temis cannot parse,
stays a fatal, actionable error as ADR-0019 decided. Nothing can run it, so
starting without it would only move the failure to the first instance that tried
to advance. That error now names the record's path, since acting on it means going
to that file.

### Consequences

- **Positive:** Adding a validation rule — or bumping temis — can no longer brick a
  running server. A definition that was accepted keeps running exactly as it did,
  and its instances keep advancing; the decisions that still compile keep
  answering. An operator upgrading Atlas gets a warning naming the model to fix
  instead of a crash loop with no way in. The gate keeps its full force at deploy.
- **Negative / trade-offs accepted:** A model that today's rules call an error can
  be *running* on a current server — the rule catches it at its next deploy, not
  before. So the invariant is "everything running passed the gate of its own day",
  not "everything running passes today's gate". A rule authored as a *runtime*
  safety check would therefore not be enforced by this path; validation is
  documented as a deploy-time gate, and a check that has to hold at runtime
  belongs in the engine, not in stage 5. On the DMN side the trade is sharper: a
  decision that no longer compiles is registered rather than refused, so the model
  it belongs to is only *partly* executable — the failure surfaces on the instance
  that evaluates it, as an incident, instead of at startup. That is the right place
  for it (one broken decision should not stop unrelated instances), but it does
  move the discovery from boot to first use.
- **Follow-ups / risks to watch:** The warning is the only surface today. Carrying
  the stale Problems into the deployment listing, so the Problems panel (ADR-0026)
  can flag a deployed definition that would no longer deploy, is the natural next
  step, and the DMN diagnostics belong beside them. When deployment becomes
  event-sourced (ADR-0019's Milestone 4 successor), the split applies there too:
  the accepted deploy is the gate, the replay is not.

## Pros and cons of the options

### Option 1 — Keep the reload gated
- Good: one rule, applied everywhere; a stored model always satisfies the current
  compiler.
- Bad: every new validation rule is a potential outage on upgrade; recovery means
  discarding all state or hand-editing a JSON record; the failure takes down
  definitions and instances that have nothing to do with the offending model.

### Option 2 — Quarantine the record
- Good: the server starts; the offending record is visible and recoverable.
- Bad: the definition is gone from the processor, so its running instances have no
  model to advance against — the engine resolves a definition by key and would
  fault on the next timer, job, or API call for one of them. It answers "the
  server must start" while leaving the instances that made the definition matter
  in a worse state than the crash loop did.

### Option 3 — Reload without the deploy gate (chosen)
- Good: the definition is the one that was deployed and ran; instances advance
  unchanged; no new state to model; the gate keeps working at deploy; the drift is
  reported.
- Bad: a running definition may not satisfy today's rules, so "deployed" and
  "currently valid" are no longer the same statement.

## Links

- relates to ADR-0019 (durable deployments) — refines its fatal-on-reload
  follow-up
- relates to ADR-0026 (Problems panel) and ROADMAP Milestone 1 (validation rules)
- relates to ADR-0014 (DMN integration) and ADR-0063 (latest-bound decisions)
- relates to ADR-0004 (deterministic compilation), invariant I5
