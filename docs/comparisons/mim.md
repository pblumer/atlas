# Microsoft Identity Manager and Atlas

This document compares [Microsoft Identity Manager](https://learn.microsoft.com/en-us/microsoft-identity-manager/microsoft-identity-manager-2016)
(MIM, formerly FIM/ILM) with Atlas, and records **which connectors Atlas still
needs** to cover MIM's connector surface.

It exists because ADR-0154 and ADR-0166 both cite a "connector wishlist" that was
never checked in. This is that list, derived from MIM's own
[supported connectors](https://learn.microsoft.com/en-us/microsoft-identity-manager/supported-management-agents)
page rather than from memory.

> [!IMPORTANT]
> Atlas is in early development. In this document, **implemented** means present in
> the current Atlas codebase and tests. **Partial** means the kind exists but does
> not cover the MIM connector's scope. **Missing** means there is no such kind.

## The category difference (read this first)

MIM and Atlas are not the same kind of product, and the connector table below is
misleading without this paragraph.

**A MIM connector is a synchronization management agent.** It participates in
MIM's sync model: a full or delta *import* stages objects into a per-connector
*connector space*, *join/projection* rules link them to a *metaverse* object,
declarative *attribute flows* (with precedence) move values in and out, and an
*export* run writes changes back. The connector is a data-source adapter driven by
run profiles on a schedule; nobody authors the individual operations.

**An Atlas connector is an operation on a service task.** A `TypeConnectorTask`
compiles to a job carrying a reserved job-type index, and a worker performs exactly
the one operation the model authored, off the hot path and after fsync
(ADR-0007/0067/0168). There is no connector space, no metaverse, no join, no
declarative attribute flow, and no run profile.

So the honest summary is:

| | MIM | Atlas |
|---|---|---|
| Primary category | Identity synchronization and lifecycle suite | Durable BPMN 2.x workflow engine |
| Connector role | Sync management agent (import/export, delta) | Per-operation service-task worker |
| Identity data model | Metaverse + connector space, join/projection | Process variables; no identity store |
| Attribute movement | Declarative flows with precedence | Explicit in the model |
| Scheduling | Run profiles | BPMN timer start events (cycles supported) |
| Workflow model | WF/XOML declarative workflows, MPRs, Sets | BPMN 2.x |
| Extensibility | ECMA 2.0 / rules extensions (.NET) | Out-of-process job workers (ADR-0164/0168) |
| Migration path | — | `mimimport` converts MIM XOML workflows to BPMN |

**Replacing MIM connector-for-connector is therefore not sufficient**, and it is not
the only gap: an Atlas process must model the reconciliation logic MIM performs
declaratively. Whether Atlas should grow a sync/reconciliation layer at all is an
architecture decision that needs its own ADR — it is deliberately out of scope here.

## Connector coverage

MIM's supported-connector list, mapped to Atlas as of this writing.

### Directory

| MIM connector | Atlas | Status |
|---|---|---|
| Active Directory Domain Services | `ad` (ADR-0166, amended) | **Implemented** for the lifecycle — create-user, create-group, update-attributes, set-password, enable/disable, move/rename, delete, add/remove-group-member. Plus a **DirSync delta read** for reconciliation. Runs on a worker (`--offload-connectors ad`). Search stays with `ldap` by design. Remaining: simple bind over TLS only (no Kerberos/NTLM), and no incremental-values flag. |
| *(no MIM counterpart — the cloud directory)* | `entra` (ADR-0171) | **Implemented** — the same lifecycle against Entra ID over Graph. |
| Active Directory Lightweight Directory Services (ADLDS) | `ldap` (ADR-0154) | **Implemented** — plain LDAP, no AD-specific encoding needed. |
| Active Directory Global Address List (GAL) | a **process**, not a connector — [`examples/galsync.bpmn`](../../examples/galsync.bpmn) | **Implemented as a model.** GALSync is a policy ("show forest A's mailboxes in forest B's address book"), not a protocol: the DirSync delta, the contact create/update/delete and the loop are all connectors already. `ad create-contact` was the one piece missing. |
| Generic LDAP Connector | `ldap` (ADR-0154, amended) | **Implemented** — search / add / modify / add-values / delete-values / delete / modify-password, paged and entry-capped searches, and a client-certificate (SASL EXTERNAL) bind. Connections are pooled. Remaining: a delta/sync cookie. |
| IBM Directory Server | `ldap` | **Implemented** via the generic connector. |
| Novell eDirectory | `ldap` | **Implemented** via the generic connector. |
| Oracle (previously Sun and Netscape) Directory Servers | `ldap` | **Implemented** via the generic connector. |

### Database

| MIM connector | Atlas | Status |
|---|---|---|
| Microsoft SQL Server | `mssql` (ADR-0170) | **Implemented** — query / query-one / execute, bound parameters, named binding. |
| Generic SQL Connector | `mssql`, `mariadb`, `postgres` (ADR-0170) | **Partial** — three products rather than one generic kind, deliberately: placeholder syntax is per-product, so the product is part of the model. |
| Oracle Database | — | **Missing** — servable (`sijms/go-ora` is pure Go), left as a follow-up; adding it is a row in the product table, a job type, and a blank import. |
| IBM DB2 Universal Database | — | **Missing, and staying that way** — IBM's driver is a CGO wrapper and ADR-0010 forbids CGO. Reach DB2 through a worker of your own. |

Atlas also has a **MariaDB / MySQL** connector, which MIM has no counterpart for.

These are the first connector kinds with no in-process handler at all: SQL runs only
on a worker, so a database credential never enters the engine (ADR-0164/0170).

### Cloud and web services

| MIM connector | Atlas | Status |
|---|---|---|
| Microsoft Graph Connector | `entra` (ADR-0171) | **Partial** — the directory surface: create / read / update / delete a user, enable, disable, and group membership. Other Graph areas (licences, directory roles, administrative units) go through the `rest` connector. |
| Microsoft Azure Active Directory Connector | `entra` (ADR-0171) | Out of support in MIM itself; superseded by Graph, which is what this connector speaks. |
| Connector for Web Services | `soap` (ADR-0165) | **Implemented** |
| SharePoint Services Connector UPA | `sharepoint` (ADR-0141) | **Partial** — Graph list items, not the SharePoint User Profile Application. |
| Connector for Lotus Domino | — | **Missing** — legacy; recommend not building. |

### Files

| MIM connector | Atlas | Status |
|---|---|---|
| Generic CSV Connector / Delimited text file | `csv` (ADR-0139, amended) | **Implemented** — read and write. |
| Fixed-Width text file | `csv` format `fixed-width` | **Implemented** — read and write, columns authored as `name:width`. |
| Attribute-Value Pair text file | `csv` format `avp` | **Implemented** — read and write. |
| LDAP Data Interchange Format (LDIF) | `ldif` (ADR-0172) | **Implemented** — read and write. Change records are read as entries, not applied. |
| Directory Services Mark-up Language (DSML) | `ldif` format `dsml` | **Implemented** — read and write, DSML v1. |

### Platform

| MIM connector | Atlas | Status |
|---|---|---|
| Windows PowerShell Connector | `script` (ADR-0047) | **Implemented** — plus Python and JavaScript. |
| Extensible Connectivity 2.0 (ECMA2) | out-of-process job workers (ADR-0164/0168) | **Equivalent, different shape** — Atlas's extensibility seam is a job worker in any language, not a .NET MA assembly. |
| FIM Service | Atlas itself | n/a — and `mimimport` converts MIM's XOML workflow definitions into Atlas BPMN. |

## What Atlas has that MIM does not

`rest` (ADR-0067/0152), `scim` (ADR-0153), `mail` (ADR-0079/0093), `remedy`
(ADR-0106), `webscrape` (ADR-0118), `clio` (ADR-0036), `temis` (ADR-0050), DMN
decisions, and the sanctioned user-provisioning connector (ADR-0123). SCIM in
particular is the modern replacement for several MIM target-system MAs.

## The remaining work, in priority order

### Wave 1 — blocks a MIM replacement

1. ~~**Generic SQL connector.**~~ **Done** ([ADR-0170](../adr/0170-generic-sql-connector.md))
   as three product connectors — `mssql`, `mariadb`, `postgres` — worker-only, with
   the DSN held in the worker's environment and the statement literal by
   construction. Oracle is the remaining database follow-up.
2. ~~**Microsoft Graph / Entra ID connector.**~~ **Done**
   ([ADR-0171](../adr/0171-entra-id-connector.md)) as `entra`, worker-only, with the
   tenant's app credential held only on the worker. The OAuth2 client-credentials
   plumbing was lifted into `connector/oauth2` rather than written a third time.
3. ~~**Complete the AD connector.**~~ **Done** (ADR-0166, amended 2026-08-21):
   update-attributes, move/rename, delete and create-group close the
   joiner/mover/leaver gap, and the connector now runs on a worker (ADR-0168) with the
   bind password read from the worker's own environment. Kerberos/NTLM bind and a
   DirSync delta read remain.

### Wave 2 — depth on what exists

4. ~~**LDAP hardening.**~~ **Done.** Paged results, per-value modify, an mTLS bind and
   connection pooling landed in ADR-0154 (amended 2026-08-21). The delta read landed
   as a `sync` operation on the **AD** connector (ADR-0166, third amendment), because
   it is not one feature but two vendor protocols — DirSync for AD, RFC 4533
   elsewhere — and a generic connector guessing which server it is talking to is the
   wrong shape. RFC 4533 content sync for non-AD directories remains unbuilt.
5. ~~**File connector family.**~~ **Done.** Fixed-width, AVP and a write direction
   joined the text-file connector (ADR-0139, amended); LDIF and DSML became their own
   `ldif` connector (ADR-0172), because they carry directory entries rather than table
   rows and produce the shape a live directory read produces.
6. ~~**GALSync.**~~ **Done as a process** ([`examples/galsync.bpmn`](../../examples/galsync.bpmn)),
   plus `ad create-contact` for the one directory primitive it needed. It is worth
   saying why it is not a connector: GALSync has no wire protocol of its own — it is a
   rule about which objects in one forest should appear in another's address book, and
   a rule expressed as sequence flow is one an operator can read and change. Building
   it in Go would have put business policy in the engine, which is the thing a BPMN
   engine exists to avoid.

### Deliberately not building

Lotus Domino, native DB2/Oracle MAs beyond the generic SQL connector, and the
SharePoint UPA connector. Legacy or vanishing surface; revisit only on a concrete
customer need.

### Beyond connectors

MIM ships more than management agents: password change notification (PCNS) and
password sync, self-service password reset, Privileged Access Management, BHOLD,
and Certificate Management. None have an Atlas counterpart, and none are connector
work.
