# ADR-0202: Atlas holds the AD mockup's starting entries

- **Status:** Proposed
- **Date:** 2026-08-27
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0181](0181-ad-connector-mock-mode.md) gave the Active Directory connector a mock
directory, and [ADR-0193](0193-ad-mock-in-the-console.md) moved its switch from a start-up
environment variable into the Console, so an operator can flip it while trying a
process out. Both records left one field behind: the *seed* — the accounts and groups a
mock directory starts with, because a joiner creates its own account and needs none,
while a leaver or a mover has nothing to disable or rename in an empty forest.

The seed stayed what it had been when the whole feature was an environment variable: a
**path**, `ATLAS_AD_MOCK_SEED=/srv/atlas/forest.ldif`, rendered straight through into
the Console as a text field. That is wrong in three separate ways, and in practice it
was worse than wrong.

**The Console is org-wide; a path belongs to one machine.** An operator typing into a
browser cannot see, complete, or verify a filename on the host that happens to run the
AD worker. Nothing about the field says which machine it means. A relative path —
`./forest.ldif`, which is what the placeholder invited — resolves against the *child
process's working directory*, which is not something anybody can predict from a Console,
and not something Atlas documents because it is an implementation detail of how the
supervisor spawns a child.

**The field implied a multiplicity that does not exist.** There is exactly one mock
directory per worker, seeded from exactly one source. A free-text file field looks like
a choice among several forests. It is not; there is nothing to choose.

**And a typo in it was fatal.** `adDialerFromEnv` refused to build a dialer when the
seed could not be read, `BuiltinConnectors` propagated that, and the worker exited 1.
The supervisor restarts a child that exits, so the worker entered a restart loop: the
Workers view showed a **failed** AD worker with several hundred starts and one log line
repeated every thirty seconds. An *optional* field made every AD task in the instance
unservable, indefinitely, and the only thing that said so was a log an operator had to
go looking for.

## Decision drivers

- What an operator sets in the Console must be something the Console can actually
  validate — not a promise about a filesystem it cannot reach.
- A mockup is a thing you try. Its setup must not require placing files on a server.
- No optional field may be able to take a worker down. Degrading has to be possible and
  has to be chosen wherever it is safe.
- A worker still has to be configurable by environment alone: ADR-0157 forbids a private
  channel between engine and supervised child, so whatever the Console does must end in
  the same variables an external worker reads.

## Considered options

1. **Keep the path, validate it better.** The Console could refuse a relative path and
   check for the file at save time.
2. **Atlas stores the seed content and manages the file.** The operator uploads or
   pastes LDIF/DSML; Atlas writes it beside its other settings and hands the worker a
   path of its own making.
3. **Atlas stores the content and streams it to the worker over the API.** No file at
   all; the worker fetches its seed when it starts.

## Decision outcome

Chosen option: **Atlas stores the content and owns the file (option 2).**

- `adMockSetting.Seed` holds the LDIF or DSML **text**. `SeedName` keeps the uploaded
  file's name for display only and never reaches the worker.
- The format is decided by **looking at the content**, not at a file extension — there
  is no longer a name to read it off, and an operator pastes as often as they upload.
  DSML is XML, so the first non-whitespace character is `<`; anything else is LDIF,
  which is the right default because it is what a directory exports.
- **The Console parses the seed at save time** and refuses one it cannot read, naming
  the format it tried. The refusal reaches the one person who can fix it, at the moment
  they are looking at what they wrote. It reports the entry count back, so a save is
  visibly a save of something.
- `saveADMock` writes the seed to `<settings>/admock-seed-<digest>.<format>` and removes
  every earlier one. **The filename carries a digest of the content**, and that is not
  about caching: `supervisor.refresh` restarts a child only when its *rendered
  environment* differs, so a fixed filename would render an unchanged
  `ATLAS_AD_MOCK_SEED` and leave a running worker serving the directory it started with
  after an operator replaced the seed. Naming the file by its content makes the variable
  change exactly when the content does — and makes an unchanged save correctly restart
  nothing.
- **The worker degrades.** A seed it cannot read or parse now logs
  `ad_mock.seed_unusable` and starts an **empty** directory instead of refusing to
  start.
- The Console offers a small **example directory** — an OU, two accounts, one group — in
  one click. "What starting entries?" is a question most people cannot answer the first
  time they try a process; the honest answer is "whatever your process expects to find",
  which is no help at all when the process is the thing you are testing.

### Why degrading is right here, and only here

Atlas refuses rather than degrades nearly everywhere: a Remedy connector missing half
its bundle is left out, a mail connector whose name collides is not handed over,
`ATLAS_AD_MOCK=maybe` is an error rather than a "no". The rule those share is that the
degraded behaviour would touch something real with a configuration nobody chose.

A mock directory touches nothing real, so the calculation inverts. An empty directory
costs a joiner nothing — it creates its own account. It costs a leaver exactly one
failed job with "no such object", which surfaces as an incident **against the task that
needed the account**: a signal pointing at the missing seed, at the moment somebody is
watching a run. Refusing costs every AD task in the instance, for as long as nobody
reads the worker log.

One case keeps its refusal: a seed named while the mock switch is **off**. There is no
safe degradation there. Reading the file into a directory no job reaches is pointless,
and dialling the *production* directory because somebody who obviously wanted a mockup
got one variable wrong is precisely the outcome the switch exists to prevent. The
Console can no longer produce that combination at all; a hand-set environment on an
external worker still can, and there an operator is at a terminal reading the error.

### A panic found on the way

Writing the degradation test surfaced an unrelated defect in the connector itself, fixed
here because it is one branch away: `dispatch` wrote the default `objectClass` into
`j.Attributes`, and a `create-user` / `create-group` / `create-contact` whose
`entryVariable` resolved to nothing left that map **nil**. The result was a panic —
`assignment to entry in nil map` — reached as easily against a real domain controller as
against a mock, by nothing more exotic than a misspelled variable name. A create with no
attributes is now refused, saying so, which also prevents the other bad outcome: an
account created in a real directory carrying an objectClass and no name.

### Consequences

- **Positive:** No path is typed anywhere. What an operator picks is what Atlas stores,
  validated while they watch, with an entry count as the receipt. Replacing a seed
  actually restarts the worker. An unreadable seed costs one incident instead of an
  outage. A first mockup run needs one click rather than a file on a server.
- **Negative / trade-offs accepted:** The seed now lives in Atlas's settings directory
  and is captured by design-time backups — appropriate for invented directory data,
  and the reason the content is admin-only on read even though the switch's state is
  not. A very large forest travels through a JSON body and a settings file; that is the
  wrong shape for a seed of that size, and the answer there is a smaller seed, not a
  bigger field. `ATLAS_AD_MOCK_SEED` remains a path for external workers, so the two
  paths through the feature are no longer symmetric: the supervised one is managed, the
  external one is not.
- **Follow-ups / risks to watch:** Option 3 (the worker fetching its seed) stays open if
  external workers ever want the same convenience — it needs a scoped endpoint rather
  than a private channel, which is a bigger decision than this one.

## Pros and cons of the options

### Keep the path, validate it better
- Good: smallest change; nothing new is stored.
- Bad: the Console cannot check a path on another host, so validation is a guess that
  passes at save time and fails at spawn; the field still asks for something an operator
  cannot see; the multiplicity it implies still does not exist.

### Atlas stores the content and owns the file
- Good: validated where it is written; nothing to place on a server; one obvious source;
  the content-addressed name makes replacement reach a running worker.
- Bad: seeds are limited by what fits comfortably in a settings file and a request body;
  supervised and external workers are configured differently.

### Atlas stores the content and the worker fetches it
- Good: one mechanism for supervised and external workers alike; no file on disk.
- Bad: a new authenticated endpoint and a new startup dependency — a worker that cannot
  reach the engine at start now has a second reason to fail; more machinery than the
  problem needs today.

## Links

- amends [ADR-0181](0181-ad-connector-mock-mode.md) (mock mode) and
  [ADR-0193](0193-ad-mock-in-the-console.md) (the switch in the Console)
- constrained by [ADR-0157](0157-worker-processes-supervision-and-console.md): no private channel between the
  engine and a supervised child — the Console's decision has to end in the same
  environment variables an external worker reads
- uses the sidecar write discipline of [ADR-0019](0019-durable-deployments.md)
- parses through [ADR-0171](0171-directory-file-connector.md)'s reader, so LDIF and DSML
  mean one thing in Atlas rather than one thing per package
