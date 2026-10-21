# ADR-0463: A Microsoft Teams Phone Worker Type for voice apps and their resource accounts

- **Status:** Accepted (amended 2026-10-19: `ensure-call-queue` brings a queue it finds to the agents a model names — the directly assigned ones, in order under serial routing — and answers `agentsChanged`; agents not named are left as they are, a value that resolved to nothing is refused, and an empty list never empties a queue that has agents. Amended 2026-10-20: the queue's authorized users and routing settings are reconciled as its agents are — what a model names is set, what it leaves out is kept — so the compiler no longer writes a setting's default into the model; the answer lists what it set in `changed`; and six refusals carry `TEAMSPHONE_…` codes, thrown as BPMN errors when the process catches them; and `ensure-phone-number` / `remove-phone-number` give a user or a resource account a phone number and take it away again; and `remove-hotline` takes a whole hotline away in one task. Amended 2026-10-21: `list-free-phone-numbers` lists the inventory's numbers nobody holds, for the type and the kind of holder a model names)
- **Implementation:** Landed
- **Date:** 2026-10-17
- **Deciders:** Atlas maintainers
- **Open question:** whether this kind belongs in the tree or in an external package under
  [ADR-0207](0207-worker-type-packaging.md) / [ADR-0208](0208-worker-type-packages.md).
  It is built in the tree because no third-party package has been through that contract
  yet ([ADR-0299](0299-worker-type-admission-criteria.md), *Open question*), and moving it
  out later is a packaging change, not a model change.
- **Question checked:** 2026-10

## Context and problem statement

A Teams Phone *voice app* — an auto attendant or a call queue — answers calls through a
**resource account**: a disabled Entra user created for the purpose and associated with
the voice app. Provisioning a reachable queue is therefore four steps in a fixed order:
two resource accounts, the call queue with its agents, and an auto attendant that hands
its calls to the queue, each associated with its account.

Microsoft documents those steps only as cmdlets of the `MicrosoftTeams` PowerShell module
(`New-CsOnlineApplicationInstance`, `New-CsCallQueue`, `New-CsAutoAttendant`,
`New-CsOnlineApplicationInstanceAssociation`). Microsoft Graph reads call records but has
no endpoint that creates, changes or removes a voice app or its resource account; a search
in 2026-10 found only the cmdlet guides.

So a process that provisions one today does it in PowerShell script tasks
([ADR-0047](0047-polyglot-script-tasks-via-job-workers.md)). An installation that did
exactly that ran into every problem the script path leaves to each model:

- **The connection is re-authored per script.** Every task imports the module, finds a
  certificate, and signs in. A proxy bypass copied from an older environment made the
  sign-in hang until the script timeout; the certificate sat in the worker host's store,
  where *every* script on that host could use the app — a tenant-wide Teams
  administrator.
- **Idempotency is re-authored per script, and is subtle.** Teams shows a new resource
  account only after a synchronization, while Entra already holds its user principal
  name. A retried "create the account" therefore fails with *An object with the same
  userPrincipalName already exists*, and only a model that also asks Entra recovers.
- **Nothing is checked at deploy.** A misspelled parameter or a wrong shape of the agent
  list is found when a token parks on an incident.

The question: **how does a process provision and remove Teams voice apps and their
resource accounts without authoring PowerShell, while the work itself can only be done
by the `MicrosoftTeams` module?**

## Decision drivers

- **Named operations, validated at deploy**, as the Entra and AD Worker Types give a
  directory ([ADR-0172](0172-entra-id-connector.md), [ADR-0166](0166-active-directory-connector.md)).
- **The credential lives server-side** and travels to no model, variable or event
  ([ADR-0069](0069-engine-internal-encrypted-secret-vault.md), [ADR-0168](0168-connector-work-on-a-worker.md)).
- **At-least-once must be safe.** A job may run twice; an "ensure" that creates a second
  queue on its second run is worse than one that fails.
- **Generic, not one organization's conventions.** Naming rules, an employee-id scheme or
  a holiday calendar are a model's business.
- **Testable without a tenant**, and usable by a model before one exists.

## Considered options

1. **Keep the script tasks** and publish the working scripts as an example.
2. **A native Go worker against the endpoints the module calls.** They are undocumented
   and change with the module.
3. **A Worker Type that runs fixed, shipped PowerShell** for a closed set of operations
   (chosen).

## Decision outcome

Chosen: **option 3 — a worker-only Worker Type, `teamsphone`, whose operations are fixed
PowerShell shipped inside Atlas and run by `pwsh` on the worker host.**

### The five gates of ADR-0299

| Gate | Answer |
|---|---|
| 1. Operations are steps a process takes | `resolve-users`, `ensure-resource-account`, `ensure-call-queue`, `ensure-auto-attendant`, `remove-auto-attendant`, `remove-call-queue`, `remove-resource-account` — each a task with a verb on it; the remove operations are what a leaver path needs. |
| 2. The generic path is blocked or lossy | **Blocked**: there is no supported REST API, so the REST Worker reaches nothing. The script-task path is **lossy** on top: every model re-authors sign-in, idempotency and the Teams/Entra lag, and nothing validates it at deploy. |
| 3. The work fits a job | Each operation is one bounded `pwsh` run: importing the module took about 5 s on a customer host, signing in a few more, a cmdlet seconds. A run is killed at `ATLAS_TEAMSPHONE_TIMEOUT` (default 4 minutes, under the 5-minute default lease). Every operation is safe to repeat (below). Results are a handful of ids. Waiting for a licence is not an operation: it is a timer loop in the model around an Entra `get-user` with `select="assignedLicenses"`. |
| 4. One credential shape, server-side | `{tenantId, clientId}` with **either** `certificate` (certificate and unencrypted key as one PEM text, from the vault) **or** `certificateThumbprint` (a certificate already in the worker host's `CurrentUser\My` or `LocalMachine\My`). Two shapes, told apart by which field is present — the tolerable case. No client secret: the module's application sign-in is certificate-based. |
| 5. Arrives complete | Kind entry, release-registry row, Repository package, setup entry, handbook card and a mock mode, in this change. |

### Operations

All values are literal-or-FEEL unless marked *static* (validated at deploy).

| Operation | Needs | Returns |
|---|---|---|
| `resolve-users` | `users` (a list, or comma-separated text) | `{ids, notFound}` — Teams identities of the users found, and the entries that were not |
| `ensure-resource-account` | `userPrincipalName`, `displayName`, `accountType` *static* (`call-queue` \| `auto-attendant`) | `{objectId, userPrincipalName, created, source}` |
| `ensure-call-queue` | `name`, `resourceAccountId`; optional `agents` (at most 20), `authorizedUsers`, `languageId`, and *static* `routingMethod` (`attendant` default, `serial`, `round-robin`, `longest-idle`), `presenceBasedRouting`, `conferenceMode`, `allowOptOut` (each `true` by default) | `{id, name, created}` |
| `ensure-auto-attendant` | `name`, `resourceAccountId`, `transferTarget` (the queue's resource account id), `languageId`, `timeZone`; optional `authorizedUsers`, `holidays` (a list of `{name, start, end}` ISO dates on which calls are disconnected; `end` is the day after the last holiday and defaults to the day after `start`) | `{id, name, created}` |
| `remove-auto-attendant`, `remove-call-queue` | `resourceAccountId`; optional `name` | `{removed, id}` |
| `remove-resource-account` | `resourceAccountId` | `{removed, objectId}` |

The result variable is required on every operation but the removes, whose answer a
leaver path may discard. `languageId` and `timeZone` are required on an auto attendant
because Teams refuses one without them, and any default would be one organization's.
Lists are a FEEL list or one text separated by commas, semicolons or line breaks; blanks
and repeats are dropped. An empty `users` list answers `{ids: [], notFound: []}` without
starting PowerShell.

### What makes a second run safe

- **ensure-resource-account** looks the account up in Teams, then in Entra (through
  Graph, with the same certificate) before it creates one, and looks again if the create
  is refused as a duplicate. An account Entra holds but Teams does not show yet is adopted
  — unless it is an *enabled* user with another display name, which is refused, because
  resource accounts are disabled by definition and an enabled one is somebody's account.
- **ensure-call-queue / ensure-auto-attendant** first ask which voice app the resource
  account is already associated with and return that one; then look for one of the
  name; and only then create and associate. A failure between create and associate is
  the one window left, and a repeat closes it by finding the voice app by name.
- **ensure-call-queue / ensure-auto-attendant** wait for Teams to show the resource
  account — polling, up to two minutes — and refuse, before creating anything, one it
  still does not show. The wait is in the job because the built-in worker retries a failed
  job at once: retries alone would be spent in seconds against a delay of minutes. The
  lag therefore cannot leave a voice app without its account.
- **remove-auto-attendant / remove-call-queue** remove the association, then the voice
  app. A run that fails between the two leaves a voice app no account points at; the
  optional `name` finds it again on the retry.
- **remove-resource-account** removes the association through Teams and deletes the
  account through Graph: a resource account is an Entra user, and the module has no
  cmdlet that deletes one.
- **remove-\*** answers `removed: false` for something already gone.
- A user that `resolve-users` cannot look up, for whatever reason, is reported in
  `notFound` rather than failing the task; the reason is written to the worker's log.

### Running PowerShell from a Go worker

- **One `pwsh` per job**, started with `-NoProfile -NonInteractive` and a short fixed
  bootstrap. The operation script and the request travel on **stdin** as one JSON
  document, so neither the certificate nor the script text appears in argv or in the
  child's environment. The child's environment is an allowlist, as a script task's is.
- **One line is the result.** The script answers with one JSON envelope,
  `{"ok":true,"result":…}` or `{"ok":false,"error":…}`, as the last line of stdout, escaped
  to ASCII so the host's console code page cannot corrupt a display name; the worker reads
  the last line that decodes as one, so a module writing to the output stream costs
  nothing. Logs go to stderr, which is quoted when a run fails — the contract
  [ADR-0458](0458-powershell-param-block.md) gave script tasks. The request travels
  ASCII-escaped too, read as UTF-8 on the PowerShell side.
- **Normalized once, in Go.** The worker turns the resolved values into the shapes an
  operation takes — lists, holidays, booleans — and refuses what an operation cannot use
  before any PowerShell starts. The mock and the real tenant therefore refuse the same
  requests with the same words.
- **Graph without the Graph modules.** The Entra lookup and the account deletion use a
  Graph token the script obtains itself: a client assertion (RFC 7523) signed with the
  same certificate, which is what the Graph modules would do and what spares the worker
  host from installing them.
- **No persistent session.** A warm session would save the import and sign-in per job,
  but it carries state between jobs — a token that expired, a module that half-failed —
  and a stuck call would block every job queued behind it. One process per job is the
  shape that cannot leak a fault into the next. It is a named follow-up, not a refusal.
- **The proxy is the host's.** The script sets none; the module's and Graph's HTTP
  clients use the system proxy of the account the worker runs as.
- **A mock mode.** `ATLAS_TEAMSPHONE_MOCK=1` serves every operation from an in-memory
  tenant inside the worker, with the same refusals and the same idempotency, so a model
  runs end to end before a tenant exists; `ATLAS_TEAMSPHONE_MOCK_USERS` names the users
  `resolve-users` knows (every user when unset). It is the AD mock's idea
  ([ADR-0181](0181-ad-connector-mock-mode.md)) for a system that has no directory to
  read.

### Consequences

- **Positive:** a model provisions a reachable queue with four tasks and no PowerShell;
  the certificate leaves the worker host's shared store for the vault, so a script task
  can no longer use the app; the Teams/Entra lag and the retry hazards are handled once.
- **Positive:** a leaver path exists — the remove operations — where today a process
  ends with the voice app still in the tenant.
- **Negative / trade-offs accepted:** the worker host needs `pwsh` 7 and the
  `MicrosoftTeams` module; Atlas now owns PowerShell that follows a module Microsoft
  releases often. Each job pays the import and sign-in, roughly ten seconds. The app
  needs the Teams administrator role and the Graph application permission
  `User.ReadWrite.All` (finding an account Teams does not show yet, and deleting one).
- **Negative:** the shipped script is tested end to end against a stand-in module and a
  stand-in Microsoft, never against a tenant in this repository's tests; a cmdlet whose
  behaviour Microsoft changes is found by the first installation that runs it. Only the
  global cloud is addressed.
- **Negative:** the voice-app surface is deliberately small: transfer-to-queue as the
  default call flow, holidays that disconnect, no menus, no phone numbers, no voice
  routing. A model that needs more keeps a script task for that step.
- **Follow-ups / risks to watch:** a warm `pwsh` session per tenant; phone-number
  assignment once Microsoft documents a stable path; whether this kind should move to an
  external package (the open question).

### Amendment (2026-10-19): ensure-call-queue brings an existing queue to the agents it names

As landed, `ensure-call-queue` found an existing queue — through its account's
association or by its name — and returned it as it was. A process run again after a
change of staff therefore answered `created: false` and completed while the queue kept the
old agents. Nothing failed, so nothing showed it: the one failure in this design nobody
sees. A model that names the agents of an *ensure* means them as the queue's agents, and
for agents the operation did not do what its name says.

**Decision.** When a model names `agents`, a queue the operation finds is brought to that
list with `Set-CsCallQueue -Users`, on both paths, and the answer gains `agentsChanged`.

- **Only the agents named directly** (the queue's `Users`) are compared and set. Agents a
  distribution list or a Teams channel brings stay Teams' to keep, as they were before.
- **Ids compare without regard to case; order counts only under serial routing.** There
  the order *is* the routing, so a reordered list is a change; under every other routing it
  is not. The routing compared under is the one the model asks for.
- **Not named means "leave them".** The worker passes `agents` on only when the model
  named it (`Op.Reconciled`), so an existing queue keeps agents an operator maintains in
  the Teams admin center unless the model claims them.
- **A value that resolved to nothing is refused**, for a new queue as well: the worker
  cannot know before PowerShell starts whether the queue exists, and a variable nobody set
  is not "no agents". Until now it created a queue without agents.
- **An empty list never empties a queue that has agents; it is refused.** The strongest
  case for applying it is that an explicit empty list is the model's statement. It loses
  to what produces such a list in practice: `resolve-users` reports every lookup it could
  not make as `notFound`, so an outage turns a staffed hotline's list into an empty one,
  and silently emptying the queue is the most damaging outcome this operation could have.
  Emptying a queue remains `remove-call-queue` or a script task. The same mechanism removes
  a single agent who was only briefly not found; that is not refused, because it cannot be
  told from a leaver, so the handbook tells a model to check `notFound` first.
- **On the name path the agents are set before the association.** A refused list leaves
  the queue as unattached as it found it, and a failure after the change is met by a retry
  that finds the agents already right.

**Considered and not chosen:** an opt-in setting that would have left deployed models
unchanged. It would also have kept the silent outcome for every model whose author does
not know the switch, and naming the agents of an *ensure* already says what is wanted.
The consequence is accepted: a deployed model that names agents now overwrites agents
changed in the admin center since; such a model leaves `agents` empty.

**Not reconciled:** routing, presence-based routing, conference mode, opt-out, authorized
users and language still apply only when a queue is created. A model that changes those
on an existing queue still needs a script task; that asymmetry is a named follow-up.

**Assumption.** Microsoft documents `-Users` as a parameter but not the properties of the
object `Get-CsCallQueue` returns; the script reads the directly assigned agents from its
`Users` property, as the stand-in module models it. Were the property missing, a named
non-empty list would be set again on every run — converging, but answering
`agentsChanged: true` each time — and an empty one would never be refused. Like the rest
of the script, this is found by the first installation that runs it against a tenant.

### Amendment (2026-10-20): routing and authorized users reconciled; refusals a process can catch

The 2026-10-19 amendment reconciled a queue's agents and named the rest as a follow-up:
routing, presence-based routing, conference mode, opt-out and authorized users still
applied only when a queue was created. And every refusal — an address that belongs to a
person, an account of the other kind, a list that would be emptied — was retried and
then parked on an incident. An author could draw an error boundary on the task, and
nothing ever reached it; ADR-0461 had closed that gap for script tasks only.

**Decision — reconciliation.**

- `authorizedUsers`, `routingMethod`, `presenceBasedRouting`, `conferenceMode` and
  `allowOptOut` join `agents` in `Op.Reconciled`. A queue the operation finds is brought
  to every one of them the model names, in one `Set-CsCallQueue`.
- **What the model leaves out is kept.** For the lists that was already absence. For the
  settings it was not: the compiler wrote each setting's default into the compiled task, so
  a setting the author never chose read exactly like one chosen. The compiler now leaves an
  unauthored setting out; `Prepare` applies the default for a queue the request creates and
  lists in `Request.Reconcile` only what the model named. The strongest case for enforcing
  defaults is that a model then describes the whole queue. It loses to what it would do to
  every model deployed so far: none of them could say "keep", so each would reset routing an
  operator changed in the Teams admin center. The Modeler and the Repository template gain
  an explicit choice for each default (*Attendant*, *On*, *Yes*) beside *Keep*.
- **The agents' order** counts under the routing the queue has once the run is done: the
  routing the model names, or the queue's own when it names none.
- **Authorized users are never emptied either**, refused under the same code as agents: a
  Resolve users that found nobody reads the same for both lists, and emptying either stays a
  task for the admin center.
- **The answer gains `changed`**, the sorted names the run set. `agentsChanged`, which
  0.16.0 shipped, stays as whether `agents` is among them.
- `languageId` and everything on an auto attendant are still applied only at creation.

**Decision — codes.** A refusal a process may have a path for is a `teamsphone.Refusal`
under one of six codes; the shipped PowerShell reports the code beside the error, and the
mock refuses with the same code:

| Code | Raised by | When |
|---|---|---|
| `TEAMSPHONE_UPN_TAKEN` | ensure-resource-account | the address belongs to an enabled user account |
| `TEAMSPHONE_ACCOUNT_TYPE_MISMATCH` | ensure-resource-account, ensure-call-queue, ensure-auto-attendant | the account was made for the other kind of voice app |
| `TEAMSPHONE_ACCOUNT_IN_USE` | ensure-call-queue, ensure-auto-attendant, remove-call-queue, remove-auto-attendant | the account is associated with the other kind of voice app |
| `TEAMSPHONE_ACCOUNT_NOT_VISIBLE` | ensure-call-queue, ensure-auto-attendant | Teams still does not show the account after the wait |
| `TEAMSPHONE_WOULD_EMPTY_LIST` | ensure-call-queue | an empty list would take every agent or authorized user away |
| `TEAMSPHONE_TOO_MANY_AGENTS` | ensure-call-queue | more than 20 agents |

ADR-0461's rule decides when a code is a BPMN error: when the task's process catches it.
The codes the process catches travel with the job (`Job.ErrorCodes`, from
`CompiledProcess.CaughtErrorCodes`), and `Run` throws a refusal under one of them as
`job.BPMNError`; the instance takes the error path at once, spending no retry. Any other
refusal stays a failure and keeps its retries, so a process that catches nothing behaves
exactly as before. The worker accepts a code from the script only if it is one of the six;
a malformed value, an unset required variable, a failed sign-in or a missing module have no
code, because the model or the worker host is what has to change.

The mock now refuses a remove of the other kind of voice app (`TEAMSPHONE_ACCOUNT_IN_USE`)
where it answered `removed: false`; the shipped PowerShell always refused it.

**Consequences.**

- A deployed model whose task names a setting now sets it on an existing queue. That
  includes models built from the Repository template before 1.2.0, which wrote
  `routingMethod="attendant"` by default; such a model chooses *Keep* to leave the queue's
  routing alone.
- A job resolved before a server upgrades carries the compiled defaults, which read as
  named; once, on an existing queue, it sets them. A job lives for minutes, so this is a
  window at upgrade, not a state.
- **Assumption, as for `Users`:** the script reads `RoutingMethod`,
  `PresenceBasedRouting`, `ConferenceMode`, `AllowOptOut` and `AuthorizedUsers` from the
  object `Get-CsCallQueue` returns, which Microsoft documents as parameters but not as
  properties. Were one missing, a named value would be set again on every run and listed in
  `changed` every time; the queue would still end up right.
- The handbook gains a reference card for every operation (`#ref-teamsphone`), linked from
  the panel beside the setup card; a test holds it to the operation table and the codes.

### Amendment (2026-10-20): phone numbers for users and resource accounts

The record left phone numbers out and named them a follow-up "once Microsoft documents a
stable path". That path exists: `Set-CsPhoneNumberAssignment`,
`Remove-CsPhoneNumberAssignment` and `Get-CsPhoneNumberAssignment` are documented module
cmdlets for users and resource accounts alike, with the number's type and its holder in
what the lookup answers. Without them a queue the worker provisions is reachable from
Teams but not from the telephone network, and the commonest Teams Phone process — giving
a joiner a direct line — stayed a script task.

**Decision.** Two operations, for a person and a resource account alike, because Teams
makes no difference between the two assignments:

| Operation | Needs | Returns |
|---|---|---|
| `ensure-phone-number` | `identity` (UPN or object id), `phoneNumber`, `numberType` *static* (`direct-routing` \| `calling-plan` \| `operator-connect`, no default); optional `locationId` | `{phoneNumber, identity, assigned, previousNumber}` |
| `remove-phone-number` | `identity`; optional `phoneNumber` (without it, every number the identity holds) | `{removed, phoneNumbers}` |

- **Safe to repeat.** The holder's numbers are read first (`-AssignedPstnTargetId`
  takes the UPN or the object id); a holder that has the number is answered
  `assigned: false`.
- **Replace, not refuse.** A holder with another primary number has it removed first and
  reported as `previousNumber`: what the model names is the holder's number, as with a
  queue's agents. Removing first is a choice made for want of knowledge — Microsoft does not
  say whether an assignment replaces a primary number — and a retry after a failure between
  the two finds the holder without a number and simply assigns. Private and alternate lines
  are left alone.
- **Never taken from somebody else.** A number another holder has is refused
  (`TEAMSPHONE_NUMBER_IN_USE`); a Calling Plan or Operator Connect number the tenant does not
  have is refused (`TEAMSPHONE_NUMBER_NOT_FOUND`). A Direct Routing number is the operator's
  own carrier's and is in no inventory until assigned, so its absence refuses nothing.
- **The number type is static**, like `accountType`: the three ways a tenant reaches the
  telephone network decide what Teams checks, and a guess would hand Teams a number under
  the wrong one.
- **A number is read as people write one.** Spaces, dashes, dots, slashes and brackets
  and a `tel:` prefix are dropped; the result must be digits with an optional `+` and an
  optional `;ext=`. A `(0)` after a country code is refused: kept, it dials another number;
  dropped, it guesses.
- **The licence is the model's to wait for**, as the record already says for a resource
  account's: a holder without a Teams Phone licence and a usage location fails the
  assignment as any other fault does, because the module's words for it are not known to
  this record and a code built on guessed words would be a promise not kept.
- The mock keeps the same rules; `ATLAS_TEAMSPHONE_MOCK_NUMBERS` names its Calling Plan
  and Operator Connect inventory, every number when unset.

- **The leaver path is one task.** `remove-resource-account` releases the account's numbers
  first, then removes the association, then deletes the account, and answers the released
  numbers as `phoneNumbers`. A number left on a deleted account is one nobody can give out
  again until Teams notices, and a model that had to remember the order would sooner or
  later not. The account is looked up in Entra first, so a repeat answers
  `removed: false` before Teams is asked anything about an account that is gone. Voice
  apps are not removed with their account: a queue or an attendant is the model's to take
  away, by its own operation, because removing one is not implied by removing an account.

**Consequences and what is not known.** Microsoft's list of cmdlets an app-only sign-in may
not run does not name `Set-CsPhoneNumberAssignment` (checked 2026-10; the lookup and the
removal were not checked). An absence from that list is not an affirmation, so the first
installation confirms all three. The lookup of a number the tenant lacks is assumed to answer nothing rather
than fail, as a filter does. A person's numbers are not released by anything but
`remove-phone-number`: the worker deletes no user account.

### Amendment (2026-10-20): a hotline removed in one task

Taking a hotline down took five tasks in an order a model had to know: the auto attendant,
the queue, then each account — and, since the phone-number amendment, each account's
numbers before it. A model that got the order wrong left a caller ringing into a queue
that was gone, or a number on a deleted account.

**Decision.** `remove-hotline` takes `resourceAccountIds` (the hotline's accounts, a list)
and optionally `names` (the voice apps'), and answers
`{removed, attendants, queues, accounts, phoneNumbers}`:

- **Auto attendants first, then queues, then accounts.** An attendant hands its calls to
  a queue's account; removed first, it stops taking calls before the queue goes. Each
  account then releases its numbers and is deleted, as `remove-resource-account` does.
- **Refused before anything changes** when a voice app the accounts carry also answers for
  an account the request does not name (`TEAMSPHONE_ACCOUNT_IN_USE`): that voice app is
  another hotline's too. The check reads the voice app's `ApplicationInstances`, which
  the orphan lookup of the single removes already relies on.
- **Safe to repeat.** Each step is the single operation's own, and `names` finds a voice
  app an earlier run left without its account, as the single removes' `name` does.
- The single removes stay, for taking down part of a hotline.

A required list that resolves to nothing is refused, for every operation: read as empty,
it would make a leaver path report success having removed nothing. That also refuses a
`resolve-users` whose `users` variable nobody set, which until now answered with no ids.

### Amendment (2026-10-21): the free numbers to choose from

`ensure-phone-number` gives out the number a model names, and a model had no way to
learn which numbers were free: a person picked one from a numbering plan kept elsewhere,
and a number somebody already held showed up only as `TEAMSPHONE_NUMBER_IN_USE` at
assignment.

**Decision.** `list-free-phone-numbers` reads the tenant's inventory with
`Get-CsPhoneNumberAssignment` and answers `{numbers, entries, count, truncated}`:

- **Free means what Teams will give out:** unassigned (`-PstnAssignmentStatus
  Unassigned`), activated (`-ActivationState Activated`), of the type named, carrying the
  capability of the holder named, and not blocked for assignment. Teams lists a blocked
  number as unassigned and then refuses to give it out, and has no filter for the block,
  so the script drops a number whose `AssignmentBlockedState` is anything but
  `NotBlocked` or empty.
- **Both settings are required.** `numberType` is the type `ensure-phone-number` will
  assign under, fixed for the same reason. `assignableTo` (`user` | `resource-account`)
  maps to the capability `UserAssignment` or `VoiceApplicationAssignment`: a number
  listed for the wrong kind of holder is one Teams then refuses.
- **A page, not the inventory.** `max` (1–500, 100 when unset) bounds the answer;
  `numberPrefix`, read like a number, narrows it to a range. One more than `max` is asked
  for, so `truncated` says whether Teams had more; with blocked numbers dropped, a page
  that says `truncated` may hold fewer than `max`. Teams answers 500 at a time and sorts
  by number; the script sorts again, ordinally, as the mock does.
- **Nothing is reserved.** The answer is a choice, not a claim: a number listed now may be
  given to somebody else before `ensure-phone-number` runs, which then refuses it with
  `TEAMSPHONE_NUMBER_IN_USE` — the path a model already has.
- **Direct Routing numbers are listed only once uploaded.** Microsoft's inventory holds a
  Direct Routing number after `New-CsOnlineDirectRoutingTelephoneNumberUploadOrder` has
  uploaded it; one that never was is the carrier's alone, and no Teams query finds it
  free. A tenant that does not upload them keeps its numbering plan elsewhere.
- **The mock** lists the numbers `ATLAS_TEAMSPHONE_MOCK_NUMBERS` names that nobody holds,
  for every type and holder, since its inventory knows neither; with none named it has
  every number and lists none.

What Microsoft does not state, and this amendment assumes: that an uploaded Direct
Routing number without `AcquiredCapability` carries the user and voice-app capabilities,
as every Direct Routing number in the cmdlet's examples does. If it carries none, the
capability filter lists none of them.

## Pros and cons of the options

### Option 1 — script tasks plus an example
- Good: no engine change; anything the module can do is reachable.
- Bad: every model carries the sign-in, the idempotency and the lag handling; nothing is
  validated at deploy; the certificate stays usable by every script on the host.

### Option 2 — native Go against undocumented endpoints
- Good: no PowerShell on the worker host.
- Bad: the endpoints are undocumented and change with the module; a silent break would
  surface as a provisioning failure in production.

### Option 3 — fixed PowerShell behind named operations (chosen)
- Good: uses the only supported path; the operations are validated and idempotent once,
  for every model; the credential is server-side.
- Bad: a PowerShell runtime dependency and module churn for Atlas to follow.

## Links

- relates to [ADR-0299](0299-worker-type-admission-criteria.md) (the five gates)
- relates to [ADR-0047](0047-polyglot-script-tasks-via-job-workers.md) and
  [ADR-0458](0458-powershell-param-block.md) (how Atlas runs PowerShell)
- relates to [ADR-0172](0172-entra-id-connector.md) (licences and the `select` a licence
  check reads) and [ADR-0181](0181-ad-connector-mock-mode.md) (the mock mode's model)
- relates to [ADR-0168](0168-connector-work-on-a-worker.md) (resolve on the engine, run on
  the worker)
