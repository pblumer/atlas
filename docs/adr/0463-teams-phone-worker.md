# ADR-0463: A Microsoft Teams Phone Worker Type for voice apps and their resource accounts

- **Status:** Accepted
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
