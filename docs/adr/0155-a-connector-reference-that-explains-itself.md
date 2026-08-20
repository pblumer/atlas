# ADR-0155: A connector reference that explains itself — and an incident you can actually resolve

- **Status:** Accepted
- **Date:** 2026-08-20
- **Deciders:** Atlas maintainers

## Context and problem statement

An operator reported a parked mail task carrying this message:

```
mail: no connector registered as "Patrick Blumer"
```

The connector was configured. It was sitting in the connector list, under exactly that
name. The message was not describing what had happened.

It came from a worker doing a map lookup:

```go
name := cp.Intern(detail.Connector)
client, ok := reg.Client(name)
if !ok { return fmt.Errorf("mail: no connector registered as %q", name) }
```

and the map is filled by a rebuild that skips whatever it cannot build:

```go
if !c.Enabled || c.Kind != connectorKindMail { continue }
client, err := mail.NewProviderClient(...)
if err != nil { continue }   // the reason is discarded here
clients[c.Name] = client
```

Skipping is right — a connector that cannot be built must not send wrongly, so its
tasks park (ADR-0093/0141). Discarding the reason is not. `NewProviderClient` produces
an exact one for every failure ("no credential (set credentialsRef to a JSON auth
bundle in the vault)", "credential is not valid JSON", an endpoint that names no host,
an unknown provider) and every one of them reached the operator as *the connector does
not exist*. Four different situations — never configured, disabled, configured under
another kind, configured and broken — arrived as one sentence, and the only one it
literally described was the one that had not happened.

Three further gaps came out of the same report:

- **Nothing checks a connector reference until a token parks on it.** A model naming a
  connector that does not exist deploys happily; the mistake surfaces in production, at
  the first instance, as the message above.
- **An incident can be read but not fixed.** Resolving grants a retry budget — and a
  retry repeats exactly what failed. The variables are usually what has to change
  first, and `POST /instances/{key}/variables` (ADR-0098) has existed for that all
  along, but *no UI anywhere calls it*. An operator with a browser could see the cause
  and not act on it.
- **An operator correction is invisible.** That endpoint audits every write with its
  actor, and `GET /instances/{key}/timeline` has been returning that actor per variable
  change since ADR-0098 — the replay never rendered it. A value could change under an
  instance with nothing on screen saying a human did it.

## Decision drivers

- **A message an operator reads must describe what happened.** "It does not exist"
  about a thing that exists is worse than no message: it sends someone looking in the
  wrong place.
- **Say it at the earliest moment somebody is looking.** Deploy time beats the first
  parked token; the connector list beats the incident.
- **Don't refuse what is legitimate.** Deploying a model before its connectors are
  provisioned is normal, and so is deploying to an environment that will provision them
  later. This warns; it does not block.
- **Fix, don't just report.** Surfacing a cause without an action is where this stops
  being useful.
- **Nothing new in the durable record.** All of it is read-side and presentation: no
  new value type, event, or recovery path.

## Considered options

1. **Improve the message text.** Reword the worker's error to hedge ("no *usable*
   connector"). Cheapest, and still cannot say which of the four cases it is.
2. **Carry the reason in the registry, check references at deploy, and make the
   incident actionable.** The reason travels from where it is known (the rebuild) to
   where it is read (the incident, the connector list); the reference is checked when
   the model is deployed; and the incident offers the correction the retry needs.
3. **Key connectors by a stable id instead of the display name.** Removes the rename
   trap underneath all of this — and changes a deployed model's meaning, so it is a
   migration, not a fix.

## Decision outcome

Chosen option: **2**, in four parts.

**The registry remembers why a connector is missing.** The five connector kinds had
five byte-identical registries; they now share one generic
[`connector/clientreg`](../../connector/clientreg/registry.go), which holds the clients
*and* a `name → reason` map for the configured connectors it could not build. Each
`build*Clients` records instead of discarding: `the connector is disabled`, `no
endpoint is configured`, the provider's own error, or — for a name that exists under a
different kind — `it is configured as a "clio" connector, not "mail"`. `Registry.Unresolved`
formats the two answers a worker can now give, and every worker returns it:

```
mail: connector "Patrick Blumer" is configured but not usable: the connector is disabled
mail: no connector registered as "Nobody"      ← unchanged, and now *only* for this case
```

The same reason appears on the connector row in Organization › Connectors, so it is
visible before a token parks rather than only after.

**A deploy checks the references it can.** `CompiledProcess.ConnectorRefs` enumerates
every connector reference in a model (connector tasks and business rule tasks alike,
each with the reserved job type that says which *kind* it needs); the deploy resolves
each against the connector store and returns what will not resolve as `warnings` on the
response, which the Modeler shows beside the success. The deploy still succeeds — see
the drivers — but "this will not run as written" is now said while the author is still
in the room.

**The incident offers the correction, not only the retry.** Every incident row — live
view, replay, incidents table — gains **✎ Fix variables…** beside the resolve. It opens
the instance's current variables, writes them through the existing audited operator
override, and optionally resolves in the same step ("Save & retry"), or leaves the
incident standing ("Save only"). The variables are edited as JSON rather than as a form:
the endpoint sets and overwrites exactly the keys it is given, and a value can be an
object or an array, which no flat field grid represents honestly. Invalid JSON is
refused in the dialog rather than server-side with the edit already lost.

**The replay names who did it.** A variable an operator set by hand carries a `✎ actor`
chip beside its name in the replay's Variables tab. The data was already in the
response; this makes an operator correction reviewable instead of an unexplained jump
in the values — which is the condition for the previous paragraph being acceptable at
all.

### Consequences

- **Positive:** the four situations behind a parked connector task are now four
  different sentences, each naming what to go and fix. A mistyped or renamed connector
  is caught at deploy. An operator who can see a cause can now act on it without
  leaving the incident, and what they did is visible in the replay. Five duplicated
  registries became one.
- **Negative / trade-offs accepted:** the deploy response grows a field, and the
  warning is advisory — a model with warnings still deploys, so it is possible to
  ignore it. The variable editor is raw JSON, which is honest about the value space but
  asks more of the operator than a form would. Changing variables stays admin-only
  (ADR-0098), so a non-admin sees the button and is told they cannot use it. The
  registry now holds one string per unusable connector — bounded by the number of
  configured connectors, which is small.
- **Follow-ups / risks to watch:** the rename trap is untouched (option 3) — renaming a
  connector still silently breaks the deployed models referencing it, and the deploy
  warning only catches it for models deployed *after* the rename; a warning at rename
  time, or a stable id, is the real fix. The same check would be worth having in the
  Modeler's Problems panel (ADR-0026), which validates a model without deploying it.

## Pros and cons of the options

### Improve the message text
- Good: one line, no new mechanism.
- Bad: the worker does not know which case it is in, so no wording can be accurate. It
  would replace a confidently wrong sentence with a vague one.

### Carry the reason, check at deploy, make the incident actionable
- Good: each fact is reported by the layer that actually knows it — the rebuild knows
  why a client could not be built, the deploy knows what the model asks for, the
  incident knows what is stuck. Removes a five-way duplication on the way past.
- Bad: touches five connector packages, the deploy response and three UI surfaces; the
  breadth is the cost of the reason having been dropped in one place and needed in
  several.

### Key connectors by a stable id
- Good: removes the rename trap at the root.
- Bad: a deployed model's connector attribute *is* the name today; changing that
  re-points every existing model and needs a migration and its own ADR. Worth doing,
  not worth conflating with making the current failure legible.

## Links

- fixes the diagnosis path for ADR-0061 incidents raised by ADR-0093/0141 connector
  workers
- builds on [ADR-0150](0150-preview-mail-provider-and-visible-incidents.md) and
  [ADR-0151](0151-incidents-beyond-the-live-diagram.md) (incidents on the diagram, in
  the replay and in the lists) — this makes the message they display worth displaying
- puts the operator override of ADR-0098 (set variables on a running instance, audited
  with the actor) in front of an operator for the first time
- relates to ADR-0036/0041 (a model refers to a connector by name only, never carrying
  an endpoint or a secret)
