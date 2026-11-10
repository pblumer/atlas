# ADR-0540: SharePoint Server is a Worker Type of its own

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-11-09
- **Deciders:** Atlas maintainers
- **Open question:** Whether the cmdlets and server object model behave as written here on a real SharePoint Server Subscription Edition farm: every step below comes from Microsoft's reference, which lists versions up to 2019 and does not name Subscription Edition, and no test has run against a farm.
- **Question checked:** 2026-11

## Context and problem statement

Atlas administers SharePoint Online through the `sharepoint` Worker Type (ADR-0141), which speaks Microsoft Graph about lists and files. On-premises customers need the same self-service for SharePoint Server (Subscription Edition): create a team site, set owners and members, delete and restore it, set its quota. A site collection is the unit there; there is no Microsoft 365 group. Exchange solved the same split as two targets of one Worker Type (ADR-0462). Should SharePoint Server be a target of `sharepoint`, or a Worker Type of its own?

## Decision drivers

- A model should say what it means, and a deploy should refuse what cannot run.
- The on-premises credential or account must not reach the engine.
- The cmdlets exist only on a server of the farm.
- Operations must survive at-least-once delivery.

## Considered options

1. A `target: server` in the `sharepoint` bundle, as Exchange has.
2. A separate Worker Type `sharepointserver`, modelled on Exchange's on-premises backend.
3. Generic PowerShell script tasks.

## Decision outcome

Chosen option: **"a separate Worker Type `sharepointserver`"**, because the two have no operation in common. Exchange's two targets offer the same operations through different cmdlets, which is what makes one type with two targets pay: one model runs against either. `sharepoint` is lists and files over Graph, `sharepointserver` is site lifecycle through farm PowerShell. One type would hold two disjoint tables and a job type that decides which by inspecting configuration.

The Worker runs on a server of the farm. Each job starts `powershell.exe` with a fixed program; the operation and values travel as JSON in the environment and are bound as parameters, so no authored value is parsed as PowerShell and a model cannot name a cmdlet. The operation table lives in `connector/sharepointserver/catalog`, which the compiler and the worker both read. A site is named by its leaf under the managed path, never by a URL, so a task cannot leave the configured web application.

### Consequences

- **Positive:** a model reads as the administrator thinks: create-site, update-group-members. A deploy refuses an unknown parameter or a malformed value. No credential reaches Atlas: the worker's account does the work. Repeated delivery is safe for every operation.
- **Negative / trade-offs accepted:** a process that serves Online and Server needs two models or two service tasks, unlike Exchange's single model. This is accepted because the operations differ anyway. Each job costs a PowerShell start and snap-in load. The Worker is never supervised by Atlas: the worker must be installed on the farm.
- **Follow-ups / risks to watch:** see the open question. `New-SPSite` creates no default groups, so `create-site` makes them itself from the server object model (since 2026-11-10; before, it called `CreateDefaultAssociatedGroups`, see As built); that and `EnsureUser` on AD groups are not cmdlets in Microsoft's cmdlet reference.

## Pros and cons of the options

### A target of `sharepoint`
- Good: one Worker Type name.
- Bad: two disjoint operation tables, a job type that depends on configuration, and a bundle that means different things.

### A separate Worker Type
- Good: table and compile checks match what can run; the on-premises constraints are explicit.
- Bad: one more kind to wire (compiler, worker, Console, handbook).

### Script tasks
- Good: nothing new to build.
- Bad: the model carries PowerShell, a connection and the account, and every check moves to run time.

## As built

- `connector/sharepointserver`, `compiler/sharepointserver_compile.go`, job type `io.atlas.sharepointserver` (index 39), Modeler rows in `api/web/sharepointserver-ops.js`.
- Operations: create-site, get-site, list-sites, delete-site (`Remove-SPSite -GradualDelete`), restore-site (`Get-SPDeletedSite`, `Restore-SPDeletedSite`), set-site-owners (`Set-SPSite`), update-site-admins, update-group-members (the default groups), set-site-quota (`Set-SPSite -MaxSize -WarningSize`).
- Refusal codes are in `connector/sharepointserver/codes.go`; the bootstrap sets them from checks it made itself, so they do not depend on the farm's language.
- A site's inventory is the farm itself: `create-site` writes `atlas.*` properties into the root web's property bag and `list-sites` reads them back, so no SharePoint Online list is needed.
- 2026-11-09: `connector/sharepointserver/manifest.json` declares each operation's idempotency and effect (ADR-0538), checked against the bootstrap. Repeated delivery repeats no effect, but a second `create-site` after a first that finished fails with `SHAREPOINT_SERVER_SITE_EXISTS` rather than succeeding ([ADR-0546](0546-new-built-in-worker-types-ship-a-manifest.md)).
- 2026-11-10, first run on a real farm (the M365 test environment, SharePoint Server SE 16.0.14326 with SQL Server 2025), and what it changed:
  - The web application uses claims: `New-SPSite` takes `DOMAIN\name`, but the site knows a user as `i:0#.w|domain\name`. `create-site` resolves owners with `EnsureUser` before it uses them.
  - The Worker's account is a shell admin (`Add-SPShellAdmin`), which reaches the content databases and no site: `EnsureUser` answered "Attempted to perform an unauthorized operation." It now holds a web application policy with a role of its own, *Atlas Worker* (Open, ViewPages, BrowseUserInfo, EnumeratePermissions, ManagePermissions, CreateGroups, ManageWeb): enough to resolve users and to make and fill groups, nothing to read or write content. The M365 test environment's `sharepoint-setup.ps1` (`Grant-RunbookPolicy`) sets it; another installation grants the same. Full Control was considered and not taken.
  - `CreateDefaultAssociatedGroups` also sets permissions on the site's system libraries and answered `E_ACCESSDENIED` under that role. `create-site` makes Owners (Full Control), Members (Edit) and Visitors (Read) itself, named from SharePoint's resources in the site's language with an English fallback, visible to everyone as a team site's default groups are, and associates them with the site.
  - In SharePoint a group's members are changed by its owner. The Worker's account owns the three groups it makes, which gives it no right to the site's content; a group of that name somebody else owns stops `create-site` with `SHAREPOINT_SERVER_GROUP_NOT_OWNED`.
  - A second `create-site` of the same position (the site carries its `atlas.position`) now answers with the site instead of `SHAREPOINT_SERVER_SITE_EXISTS`, which is what `check` in the manifest promises (ADR-0538); a finished site of another position, or of none, is still refused.
  - The bootstrap outgrew the command line: as `-EncodedCommand` it took 34,468 of the 32,767 characters Windows allows. It now travels in the child's environment (`ATLAS_SHAREPOINTSERVER_BOOTSTRAP`) and the command line carries a fixed starter that runs it; both are fixed text, and the request's values still arrive as data. `TestTheBootstrapFitsWhereItTravels` keeps both below the limit.
  - A failure the bootstrap did not name reports the bootstrap line it happened at.
- 2026-11-10: the team site product (`examples/m365-dienste/sharepoint-server/`) checks the address before it creates the site, as the SharePoint Online team site does. After the name rule, `get-site` on `regel.alias` writes `siteAlt` (null when no site is there), and the gateway `gw_frei` goes on to `create-site` only when `siteAlt` is null or its property `atlas.position` is this position's (`get value(siteAlt.Properties, "atlas.position")`, the value `create-site` writes); that second case is a redelivery, and answering it is `create-site`'s, which answers it with the site (above). Every other site takes the address: the refusal says «Die Adresse ist bereits vergeben.» The error boundaries on `create-site` stay for the race between the check and the creation.
