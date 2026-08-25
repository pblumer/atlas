# ADR-DRAFT: Mock mode for the Active Directory connector

- **Status:** Proposed
- **Date:** 2026-08-25
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0166](0166-active-directory-connector.md) gave Atlas the Active Directory
lifecycle — create a user, a group or a contact, set a password, enable, disable, move,
delete, manage membership, and read a DirSync delta — and
[ADR-0168](0168-connector-work-on-a-worker.md) moved the work onto a worker. What is
still missing is the ability to *try any of it*.

The directory an identity process touches is production by definition. A joiner
creates an account somebody logs in with; a leaver disables one; a mover renames one.
There is no harmless place to point a first draft of that process at, and the two
things an author can do today are both unsatisfying:

- **Replace the task with a mockup** ([ADR-0120](0120-mockup-service-task.md)). The
  engine then simulates the *task*, which is exactly right while the integration is
  still an idea — and useless once it exists, because the AD configuration is gone from
  the element. What such a run proves is that the flow around the task works, never
  that the task does.
- **Point it at a test forest.** Real, faithful, and available to roughly nobody at the
  moment they are drafting a process. It is also the slowest possible way to discover
  that a password write needs an encrypted channel.

So a process author cannot answer "does my joiner work" without either an AD
administrator or a leap of faith, and the failures that wait for them are the dull,
knowable ones: an unencrypted password write, a replayed create, a DirSync base that is
not a naming context root.

## Decision drivers

- **The model must not change between a test run and a real one.** A model that behaves
  differently in the two is a model whose test proves nothing about production.
- **Faithful where it matters.** A mock that accepts what Active Directory refuses
  teaches an author to be wrong, and the lesson then arrives in production.
- **No credential is kept.** A stand-in for a directory that stores the passwords a
  process sets is a new place to leak them.
- **Visible.** A worker that simulates everything looks, from the engine's side,
  exactly like one that works: it completes every job.
- **Nothing new on the engine.** ADR-0164 moves this work *off* the run loop; a mockup
  facility that lived in the engine would pull it back.

## Considered options

1. **A `mockup` attribute on the AD task.** The model says it is a simulation.
2. **A `mock://` URL.** The directory is model data (ADR-0166), so simulation is a
   directory a model can point at.
3. **A worker-side switch.** `atlas worker --connector ad` with `ATLAS_AD_MOCK` set
   serves AD jobs against an in-memory directory.
4. **An `atlas mock-ad` LDAP server**, in the shape of `atlas mock-remedy`: a real
   listener the model dials.

## Decision outcome

Chosen: **option 3, a worker-side switch**.

The deciding argument is the first driver. Options 1 and 2 both put the fact "this is a
test" into the model, and a model carrying that fact is a model that will one day be
deployed with it still set — silently doing nothing to a production directory while
reporting success on every task. Option 3 leaves the model byte-identical: the same
URL, the same bind DN, the same operations. What differs is **which worker leases its
jobs**, which is already the thing ADR-0168 made configurable, and promoting a mockup
run to a real one is then a worker's environment rather than an edit and a redeploy.

Concretely, `ad.MockDirectory` implements the connector's existing `Dialer` and `Conn`
interfaces, so it drops in exactly where `GoDialer` sits and every other line of the
connector — `Resolve`, `Run`, `dispatch`, `runSync` — is the code that runs against a
real domain controller. The mock is the *only* difference, which is what makes a mockup
run evidence about the model rather than about a second implementation.

**It refuses what AD refuses.** A replayed create fails with "entry already exists"
(delivery is at-least-once, and that is the failure a create actually has); `unicodePwd`
may only be written over an encrypted channel and must carry AD's quoted UTF-16LE
encoding; a group member cannot be added twice or removed twice; a container with
children cannot be deleted; a simple bind naming a DN with no password behind it is
refused, which is what an unset `ATLAS_CONNECTOR_<REF>_TOKEN` looks like on the wire;
and DirSync is answered only at a naming context root. Each of those is a real first-run
failure, and each is cheaper to meet here.

**The password is checked and then dropped.** A set-password validates the encoding and
records that one was set (`pwdLastSet`), never the value, and the operation journal
redacts it. Validating the encoding is what a mock is for; holding a credential is not.

**The delta is real.** Every write stamps the entry with a change counter, a delete
leaves a tombstone carrying `isDeleted`, and the DirSync cookie *is* that counter — so a
reconciliation loop modelled as sync → handle → wait → sync converges against the mock
exactly as it does against a domain controller, including the `more` signal and the
`maxEntries` cap. A cookie this directory did not write is refused rather than treated
as "start over", for ADR-0166's reason: a full directory presented as a change set is
worse than an error.

**A seed file** (`ATLAS_AD_MOCK_SEED`) fills the directory from LDIF or DSML, read with
the directory-file connector's own parser (ADR-0171) so the format means one thing in
Atlas. A leaver process has nothing to disable in an empty forest.

**And it says so.** The worker logs a warning at startup — no directory is being
written — and one line per simulated operation, which the Workers console shows
(ADR-0157). That log is the only place a mock worker is distinguishable from a working
one, so it is not optional.

Option 4 was the closest call, and it is a genuinely better *test* of one thing: it
would exercise the go-ldap adapter, the half of the connector that mock mode skips. It
was rejected on cost and on aim. It means implementing an LDAP server — a binary
protocol with no `httptest` equivalent — to gain coverage of a layer that
`connector/ad`'s own test directory already covers against the real client, while the
question a process author has is about *their model*. It stays on the follow-up list
rather than being ruled out.

### Consequences

- **Positive:** an identity process is exercisable end to end with no directory, and
  with the model that will go to production; the failures an author would otherwise meet
  in production are met at drafting time; a demo of the AD connector needs a worker and
  an environment variable rather than a forest; the mock is a `Dialer`, so a caller
  embedding the worker can drive it from a test.
- **Negative / trade-offs accepted:** the go-ldap adapter is not exercised by a mockup
  run, so the wire encoding is proved by the package's own tests and not by the demo;
  the mock has no schema, no ACL and no password policy, so an add whose parent does not
  exist is accepted where AD would refuse it (demanding a seeded OU chain would cost
  every test a fixture and prove nothing); only the equality, presence and wildcard parts
  of an LDAP filter are applied and anything else is refused rather than ignored; the
  directory is memory, so a restart is an empty forest; and mock mode is a whole worker,
  not a task — a worker cannot serve one model for real and another as a simulation.
- **Follow-ups / risks to watch:** the in-process AD handler has no mock mode, on
  purpose (ADR-0164 wants this work on a worker), which means trying it out requires
  `--offload-connectors ad` and a worker — worth revisiting if that friction shows up in
  practice; an `atlas mock-ad` listener as in option 4, if the go-ldap path ever wants
  demonstrating; exposing the mock's directory over the worker's own surface, so the
  Workers view could show what a mockup run created rather than only the log.

## Pros and cons of the options

### Option 1 — a mockup attribute on the task
- Good: visible in the Modeler, per task, no worker configuration.
- Bad: the model carries "this is a test"; it will eventually be deployed that way, and
  a task that reports success while doing nothing is the worst failure mode available.

### Option 2 — a `mock://` URL
- Good: consistent with ADR-0166 (the directory is model data) and with pointing a
  Remedy connector at `atlas mock-remedy`.
- Bad: same defect as option 1 in a different field, and it splits the dialer decision
  per task, so a model is no longer portable between a real worker and a mock one.

### Option 3 — a worker-side switch (chosen)
- Good: the model is identical either way; the switch belongs to whoever runs the
  worker; every line of the connector but the transport is the production one.
- Bad: needs the kind offloaded and a worker started; simulation is per worker, not per
  task.

### Option 4 — an `atlas mock-ad` LDAP server
- Good: exercises the real go-ldap client; the model points at it explicitly.
- Bad: an LDAP server is a lot of protocol to own for a development aid, and it answers
  a question about the client rather than about the model.

## Links

- relates to ADR-0166 (Active Directory connector) — the operations mock mode performs
- relates to ADR-0168 (connector work on a worker) — the worker this switch belongs to
- relates to ADR-0164 (no in-process service tasks) — why the mock is worker-only
- relates to ADR-0120 (mockup service tasks) — the engine-side simulation this is not
- relates to ADR-0171 (directory-file connector) — the parser a seed file is read with
- relates to ADR-0157 (worker processes, supervision and console) — where the mock's log is read
