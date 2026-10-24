# ADR-0462: A Microsoft Exchange Worker Type — mailbox administration as named operations, online and on-premises

- **Status:** Accepted (amended 2026-10-24: an identity policy, ADR-0475, is refused for an Exchange Worker while a model can compute its name)
- **Implementation:** Landed
- **Date:** 2026-10-17
- **Deciders:** Atlas maintainers
- **Open question:** whether Exchange Online's `InvokeCommand` endpoint behaves at a real
  tenant exactly as this record assumes. The request shape (`CmdletInput`, the
  `X-AnchorMailbox` routing hint, `@odata.nextLink` paging, the
  `#Exchange.GenericHashTable` marker for hashtable parameters, the `|Type|message` error
  text) is taken from the Exchange Online PowerShell module's behaviour as community
  implementations describe it, not from published documentation — Microsoft documents the
  module and not its transport. Two points in particular were not verified: whether the
  endpoint accepts an app-only token obtained with a **client secret** (Microsoft documents
  only certificates for app-only Exchange administration), and whether an explicit JSON
  `null` clears a property over REST as `$null` does in PowerShell. Somebody with a test
  tenant should run one operation of each class (a read, a listing, a hashtable change, a
  grant, `clear-forwarding`) and record what they saw here.
- **Question checked:** 2026-10

## Context and problem statement

Identity processes do not stop at the directory. A joiner needs a shared mailbox opened to
them and a distribution list they belong to; a mover changes who may send as a team
address; a leaver's mailbox is converted to a shared one, handed to a manager with full
access, given an automatic reply and a forward, and put on litigation hold. Atlas covers
the directory side — Active Directory (ADR-0166), Entra ID (ADR-0172) — and nothing of
Exchange. A process today reaches Exchange only through a PowerShell script task, which
puts a connection, a credential and every cmdlet's encoding into the diagram.

Exchange is administered through the cmdlets of the ExchangePowerShell module, in Exchange
Online and in Exchange Server alike. The request behind this record is a Worker that offers
"the important functions of the module for low-code": an author picks *grant full
access*, not `Add-MailboxPermission -AccessRights FullAccess -InheritanceType All`.

Two facts about Microsoft's interfaces frame every option:

- **Exchange Online** has three ways in. Exchange Online PowerShell (module
  `ExchangeOnlineManagement`, v3) is the supported, complete one; since v3 it no longer
  speaks WinRM but sends each cmdlet as a REST request to
  `https://outlook.office365.com/adminapi/beta/{organization}/InvokeCommand`. The
  *Exchange Online Admin API* has been in public preview since 2025-11-17, with
  general availability not announced as of this record; it has six endpoints, nearly all
  read-only (organization config, accepted domains, mailbox properties and send-on-behalf,
  folder permissions, group membership reads). Microsoft Graph covers mailbox *content*
  and a few settings, but not mailbox permissions, distribution lists or mailbox types.
- **Exchange Server** (2019, Subscription Edition) has no REST management surface at all.
  Remote PowerShell to the server's `Microsoft.Exchange` endpoint is the supported way in.

## Decision drivers

- **Name the intent, not the encoding** — the argument ADR-0166 and ADR-0172 made for AD
  and Entra applies unchanged: full access is two parameters with fixed values, an alias
  is a hashtable, and Send As is a different cmdlet online and on a server.
- **Both targets** — the person asking needs Exchange Online and Exchange Server, and a
  hybrid organization needs both from one process.
- **Worker-only** (ADR-0164/0168): an app with `Exchange.ManageAsApp` and an Exchange
  administrator role, or a service account in Organization Management, reads and changes
  every mailbox in the organization. The engine must not hold it.
- **Testable without a tenant**, and no new runtime on the worker host where avoidable.
- **Deploy-time checks** — a missing `User` on a grant is a deploy error, not an incident.

## Considered options

1. **The Exchange Online Admin API (preview).**
2. **Exchange Online's `InvokeCommand` REST endpoint, called from Go**, plus Remote
   PowerShell for Exchange Server.
3. **The PowerShell module on the worker host for both targets** — the worker starts pwsh,
   imports `ExchangeOnlineManagement` (or opens a session to the server) and runs the
   cmdlet.
4. **Nothing new: PowerShell script tasks** (ADR-0047).

## Decision outcome

Chosen option: **2 — a dedicated, worker-only `exchange` Worker Type whose operations are
rows of one table, run against Exchange Online over `InvokeCommand` and against Exchange
Server over Remote PowerShell.** The person who asked chose the REST transport for Exchange
Online and both targets in one step, knowing the transport is not a published API.

**Option 1 is rejected on coverage**, not on its preview status: none of grant full
access, Send As, distribution-list membership changes, mailbox creation or conversion,
forwarding or litigation hold is in it. It may become the right transport for the reads it
does cover once it is generally available; the backend seam below is where that would go.

**Option 3 is the strongest alternative**, and it is the supported one. What it costs is
PowerShell 7 plus the module plus a certificate on every worker host, a connection of
several seconds per job (or a long-lived session to supervise and re-open), and a protocol
that can only be tested with a fake interpreter. What option 2 buys is a Go client with a
cached token, no runtime on the host, and a protocol tested with `httptest`. The price is
stated, not hidden: the request shape is learned from the module, and a module release can
change it. That price is confined to one file (`connector/exchange/online.go`) behind a
`Backend` interface, so falling back to option 3 for Exchange Online is a second backend,
not a redesign.

**Option 4 remains right for a one-off**, and is what this Worker's read-only escape hatch
replaces for reads.

For **Exchange Server** options 2 and 3 coincide: Remote PowerShell is the only supported
way in, so that backend starts PowerShell for each call (`connector/exchange/onprem.go`).

### The shape

- **One operation table, read by the compiler and the worker.** Every other Worker Type
  keeps its rules twice — once in its package and once in the compiler, held together by a
  drift test — because the connector imports the compiler and not the other way round. The
  Exchange table is forty-five operations with their own parameter lists; a second copy
  would be the larger half of the code. So it lives in `connector/exchange/catalog`, a
  package that imports nothing of Atlas's, and both sides read the same rows. The cause of
  the duplication — the direction of the import — is removed instead of guarded.
- **An operation is one cmdlet**, with required and optional parameters under the
  cmdlets' own names (so Microsoft's cmdlet reference documents them), parameters the
  operation sets itself (refused if a model authors them), renames and hashtable placement
  where a target needs them, and a cmdlet per target where they differ (Send As:
  `Add-RecipientPermission` online, `Add-ADPermission` on a server). Update operations are
  open — `Set-Mailbox` takes over a hundred parameters — and every other operation refuses
  an undeclared parameter, so the encoding it adds cannot be overridden.
- **Areas:** mailboxes (read, list, create shared/room/equipment, update, convert, add and
  remove an address, remove, statistics), permissions (full access, Send As, send on
  behalf, folder permissions, and their listings), distribution groups and mail contacts
  (lifecycle and membership), and per-mailbox settings (automatic reply, forwarding,
  litigation hold, calendar processing, quotas).
- **A read-only escape hatch**, `run-get-cmdlet`: any `Get-` cmdlet, checked at deploy;
  the cmdlet is compiled structure, never FEEL, so whether a task reads or changes is
  visible in the model. A change goes through an operation that names it.
- **Parameters** are a JSON object whose string leaves may be `=FEEL`, compiled to one FEEL
  context at deploy (I5) — the template Entra's inline attributes use — or a variable
  holding the object. JSON keeps a switch a boolean, which PowerShell requires.
- **A parameter that evaluates to null is left out, not sent.** Exchange reads an explicit
  null as "clear this property"; a variable that is not set must not empty a mailbox's
  forwarding. Clearing is what operations like `clear-forwarding` are for.
- **Results:** a read writes one object or null, and fails when the identity matched
  several rather than continuing with whichever came first; a listing writes an array,
  bounded by `maxResults` (default 1000), asked of the cmdlet one past the cap via
  `ResultSize` where it takes one, and failed rather than truncated past it; a change
  writes nothing, except a create or a grant, which may keep what it made. Objects are
  flattened to their properties with complex values as text, which is what both backends
  can give.
- **Configuration** is one vault bundle per Worker (`connector/exchange.Bundle`): Online
  `{organization, clientId, certificate | clientSecret}`, optionally `tenantId`,
  `authorityUrl`, `anchorMailbox`; Server `{"target": "onprem", authentication, username,
  password}` with the Remote PowerShell URI as the Worker's endpoint. Fields of the other
  target are refused. The certificate grant (a signed client assertion) is new in
  `connector/oauth2` beside the client-secret grant, because it is mechanism Entra and
  SharePoint can use too.
- **The on-premises bootstrap** is a fixed PowerShell program passed as
  `-EncodedCommand`; the cmdlet and its parameters travel as JSON in the environment and
  are splatted, so no authored value is parsed as PowerShell. It imports only the one
  command from the session and runs it through the imported module, so a name the Exchange
  endpoint does not export (`Get-Content`) cannot resolve to a local command. It runs on
  Windows PowerShell 5.1 and PowerShell 7, adds `-Confirm:$false` to changes and
  `-ErrorAction Stop` to everything, and is handed the worker's environment without any
  `ATLAS_` variable.

### Consequences

- **Positive:** an Exchange administration step is a modelled, checked operation on both
  targets; a hybrid organization is two Workers and a FEEL Worker name; the engine holds no
  Exchange credential; Exchange Online needs nothing installed on the worker host.
- **Negative / trade-offs accepted:** the Exchange Online transport is unpublished (see the
  open question). Exchange Server needs PowerShell on the worker host, in practice a
  Windows domain member for Kerberos, and opens a session per call — seconds, not
  milliseconds — chosen over a long-lived session for independence between jobs. Delivery
  is at-least-once, and a replayed grant or membership add may meet Exchange's "already
  present" error; the worker does not match error texts to swallow it.
- **Follow-ups / risks to watch:** the open question above; the Admin API's general
  availability, which may make it the transport for the reads it covers; idempotent
  retries for grants and membership once the exact error identities are confirmed at a
  tenant.

## As built

- `connector/exchange/catalog` — the operation table (45 rows), `Build`, `CheckAuthored`.
- `connector/exchange` — `Resolve`/`Run`, `OnlineClient`, `OnPremClient` with its
  bootstrap, `Bundle`/`NewBackend`/`CheckBundle`.
- `connector/oauth2/certificate.go` — the certificate client-assertion grant.
- `compiler` — `io.atlas.exchange` at reserved index 33, `<atlas:exchangeConnector>`.
- `worker/exchangeconnector.go` — `ATLAS_EXCHANGE_CONNECTORS`,
  `ATLAS_EXCHANGE_<NAME>_CREDENTIALS`, `_URL`, and `ATLAS_EXCHANGE_POWERSHELL`.
- `api` — the managed, worker-only, supervised-by-default `exchange` kind, its supervision
  env, payload arm, Repository package `atlas.exchange-administration`, Modeler entry,
  setup doc and handbook card.
- The bootstrap is exercised in a real pwsh (`TestBootstrapInRealPowerShell`) with
  `New-PSSession` and `Import-PSSession` replaced by functions; WinRM, Kerberos and
  Exchange's own parameter binding are not reached by any test.

## Amendment (2026-10-24): no identity policy while a model can compute the name

An Exchange task may compute its Worker's name (`connector="=…"`), as an Entra task may.
[ADR-0475](0475-identity-aware-worker-execution.md) therefore refuses an identity policy for
an Exchange Worker until the name the engine resolved into the job is part of the decision,
for the reason ADR-0172's amendment of the same day gives. As built, the server governs
SharePoint Workers only.

## Links

- relates to ADR-0166 (Active Directory), ADR-0172 (Entra ID), ADR-0047 (script tasks),
  ADR-0164/0168 (worker-first), ADR-0314 (worker-evaluated expressions)
- Microsoft: Exchange Online PowerShell V3 module; app-only authentication for unattended
  scripts; Exchange Online Admin API overview (preview)
