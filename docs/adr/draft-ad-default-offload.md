# ADR-DRAFT: Active Directory runs on a worker by default

- **Status:** Proposed
- **Date:** 2026-08-25
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0164](0164-no-in-process-service-tasks.md) settled the direction — a side-effecting
service task belongs on a worker — and [ADR-0168](0168-connector-work-on-a-worker.md)
made it the *default* for the kinds a supervised worker can actually serve: csv,
script and webscrape because they need no credential, mail because the engine writes
its connector's configuration into the child's environment at spawn.

Active Directory was left out, and the reason was recorded rather than assumed.
[ADR-0166](0166-active-directory-connector.md)'s second amendment offloaded the kind
but kept it opt-in, because AD is the first kind whose secret is a **vault-backed
per-task reference**: a model authors `bindSecret="AD_BIND"`, and the engine resolves
that name against its encrypted vault (ADR-0069/0070) or its environment. A supervised
worker inherits the environment and has no vault, so defaulting AD would have moved
every vault-backed directory task to a worker holding nothing to bind with.

That left AD in an odd place. It is, of all the kinds, the one whose work most plainly
does not belong on the engine's single-writer loop: a dial, a bind, a modify and a
close against a domain controller somebody else operates, on a network Atlas may not
sit in. Yet it was the one credential-bearing kind an operator had to move by hand — so
in practice a directory write kept running in the engine, which is the arrangement
ADR-0164 exists to end.

The question is therefore not *whether* AD should run on a worker — that was decided —
but what has to be true for it to run there **without an operator doing anything**.

## Decision drivers

- **A directory write must not sit on the loop.** It is a network round trip to
  somebody else's server, and the engine has one writer per partition (I3).
- **No running installation may break.** An upgrade that silently turns every AD task
  into an incident is not a default; it is an outage with a changelog entry.
- **A credential belongs where it is used** (ADR-0168), and an AD bind password with
  write access to the directory is worth less to an attacker in a worker than in the
  engine — ADR-0166's own argument for offloading the kind at all.
- **A default must never produce a worker that cannot serve what it was given.** This
  is already a stated property of the default set, guarded by a test.
- **Nothing may travel on a job payload.** ADR-0168's line stays where it is: an
  `ad.Job` carries the *reference*, never the value.

## Considered options

1. **Leave AD opt-in.** Keep `--offload-connectors ad` as the only way.
2. **Default it, environment only.** Vault-backed references stop resolving; operators
   move those secrets into the environment by hand before upgrading.
3. **Default it, and hand the child the references the deployed models name.** The
   engine renders exactly those, resolved through the same vault-or-environment
   resolver it uses itself.
4. **Default it, and hand the child the whole vault.** Every secret, one worker.
5. **Let the worker ask the server for a secret per job.** ADR-0168's option 1,
   revisited for this one kind.

## Decision outcome

Chosen: **option 3 — default it, and hand over the references the deployed models
name**.

`superviseEnv` gains an AD half beside its mail one. At every spawn and every refresh
the engine walks the deployed compiled processes, collects the bind-password
references their AD connector tasks author, resolves each through
`resolveConnectorSecret` — the vault first, the environment second, exactly as the
in-process handler did — and renders one `ATLAS_CONNECTOR_<REF>_TOKEN` per reference
into the AD worker's environment. `ad` then joins `DefaultOffloadedKinds()`.

**Why the deployed models decide.** Every other provisioned kind is named by a
connector record an operator created, so the engine knows what to hand over by reading
its own store. AD has no record to read: the reference is model data. What the engine
does have is the compiled processes, which is precisely the list of references that
can ever be asked for. Rendering that list is the narrowest set that still works — the
worker gets the passwords for the directories the deployed models actually bind to,
and nothing else in the vault. That is why option 4 is rejected: it would hand one
worker every secret in the installation to solve a problem that has an exact answer.

**Why not per-job (option 5).** ADR-0168 weighed and rejected it: it adds a network
hop to every lease and keeps the engine the thing worth attacking for someone else's
credentials. Nothing about AD changes that reasoning.

**Why not option 2.** It breaks installations that did the recommended thing. The
vault is on by default (ADR-0070) and the Console writes secrets into it, so
"vault-backed" is the *normal* case, not an exotic one. A default whose upgrade note
reads "first move your secrets, or every joiner fails" is option 1 with extra steps.

**What follows from the mechanism**, and is worth stating because each is a real
consequence rather than an implementation detail:

- **Only the AD worker gets them.** Provisioning is per kind, so the script worker —
  which runs model-authored code and inherits its whole environment — is never handed
  a directory service account. There is a test for exactly that, because it is the
  failure that would matter most.
- **A deploy can restart the AD worker.** A model naming a reference the worker is not
  holding yet triggers a refresh, and refresh restarts a child only when its rendered
  environment actually changed. So a first AD deploy cycles the worker once; an
  ordinary redeploy costs nothing. A job in flight during that restart returns through
  its lease, which is what the lease is for.
- **A reference nothing answers to is left out**, not handed over empty. An empty
  variable reads as a configured blank password; leaving it out gives the worker's own
  error instead, which names the variable an operator must set.
- **Two references that fold to one variable** (`ad-bind` and `AD.BIND`) cannot both be
  handed over, so the second is skipped and logged — the same collision two mail
  connectors can have, resolved the same way.
- **An external AD worker is unaffected.** It is handed nothing, and its operator sets
  the same variables by hand, as before. Only a *supervised* child — this process's own
  child, same host, same user — is provisioned, which is the boundary ADR-0168 drew.

### Consequences

- **Positive:** a directory write leaves the engine's loop for every installation, not
  only for those that opted in; a vault-backed deployment keeps working across the
  upgrade with nothing to do; a supervised AD worker needs no configuration at all,
  which keeps ADR-0011's one-command story; and because the child inherits the
  server's environment, `ATLAS_AD_MOCK=1` on the server puts its AD worker into mock
  mode (ADR-0181) — a whole identity process is then
  exercisable with one variable and no flags.
- **Negative / trade-offs accepted:** the supervised AD worker holds the bind passwords
  of every deployed AD model, decrypted, in its process environment — readable by
  anyone who can read that user's `/proc`, and not encrypted at rest the way the vault
  is. That is a real widening over "the engine holds them in a vault", accepted for the
  same reason ADR-0168 accepted it for mail: it is the same host and the same user, and
  the alternative is a credential that crosses a network on every lease. The set handed
  over now changes as models are deployed, so deployment and worker lifecycle are
  coupled where they were not before. And a model deployed against a reference nothing
  resolves still fails its jobs — the same failure as before, now on a worker.
- **Follow-ups / risks to watch:** LDAP, SOAP and SCIM author secret references too, so
  a general "which secrets does this process reference" walker will be worth extracting
  when the second kind needs it; the Workers view cannot yet show *which* AD references
  a worker holds, so ADR-0168's "configured nowhere" answer has no AD half; and an
  installation that wants the old arrangement has `--in-process-connectors`, which is
  the escape hatch this default must keep honouring.

## Pros and cons of the options

### Option 1 — leave AD opt-in
- Good: nothing changes; no new provisioning path.
- Bad: the kind whose work least belongs on the loop is the one kind that stays there
  unless an operator intervenes.

### Option 2 — default it, environment only
- Good: no engine machinery at all.
- Bad: breaks the normal, recommended configuration on upgrade; the failure is a
  per-task incident, discovered by whoever is being onboarded that morning.

### Option 3 — default it, hand over the referenced secrets (chosen)
- Good: works for vault and environment alike, with no operator action; hands over the
  exact set that can be asked for and no more.
- Bad: the engine now derives a worker's configuration from deployed *models*, which
  couples deployment to the worker lifecycle; decrypted passwords sit in a child's
  environment.

### Option 4 — hand over the whole vault
- Good: trivially complete; no walking of compiled processes.
- Bad: one worker holding every secret in the installation, most of which it can never
  need.

### Option 5 — the worker asks the server per job
- Good: no secret at rest in the worker at all.
- Bad: a network hop per lease, and the engine stays the place worth attacking —
  already weighed and rejected in ADR-0168.

## Links

- amends ADR-0166 (Active Directory connector) — its second amendment kept the kind opt-in
- builds on ADR-0168 (connector work on a worker) — the provisioning boundary this uses
- realizes ADR-0164 (no in-process service tasks) — for the kind that had stayed behind
- relates to ADR-0157 (worker processes, supervision and console) — the supervised child
- relates to ADR-0069/0070 (the encrypted vault) — the store a worker cannot read
- relates to ADR-0181 (Active Directory connector mock mode) — the worker this default now starts for you
