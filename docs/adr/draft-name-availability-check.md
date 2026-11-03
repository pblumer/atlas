# ADR-DRAFT: A name field checks live whether a name is still free

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-11-03
- **Deciders:** Atlas maintainers
- **Open question:** four provider facts the implementation rests on and could not
  check against a live tenant. (1) Whether Microsoft Graph answers
  `GET /directory/deletedItems/microsoft.graph.group?$filter=mailNickname eq '…'` as a
  default (strongly consistent) query; its documentation of deleted items lists `$filter`
  without naming the properties and operators it supports there. If a tenant refuses it,
  the lookup has to be sent as an advanced query (`ConsistencyLevel: eventual` plus
  `$count=true`), which the Entra client already supports. (2) Whether a site that was
  deleted *without* a Microsoft 365 group — a communication site in the tenant's site
  recycle bin — still answers 404 to `GET /sites/{host}:/sites/{name}` while it keeps its
  URL; if so, the check reports such a name as free and only provisioning learns
  otherwise. (3) Whether the quota a SharePoint site's default drive reports
  (`/sites/{id}/drive?$select=quota`) is the site's storage quota as the SharePoint admin
  centre shows it. (4) Whether Exchange Online evaluates
  `EmailAddresses -like 'smtp:<alias>@*'` in a `Get-Recipient` filter in every tenant;
  Microsoft lists `EmailAddresses` among the filterable properties with `-like`, but the
  Worker Type is tested against a recorded endpoint only.
- **Question checked:** 2026-11

## Context and problem statement

An order form often asks for a name that becomes an identifier somewhere else: the URL of
a SharePoint site (`/sites/dev-huda`), the alias of a Microsoft 365 group, the address of
a shared mailbox. Such a name is unique in the target system. Today a person learns that
the name is taken only after ordering — the provisioning process refuses the position
with "already exists", the order is rejected, and the person orders again with another
name. Portals that people know (the one this request came from shows "URL already
exists. Please choose another one." under the field while it is typed) answer that
question in the form.

Atlas already has two ways a form asks the server something live:

- A **directory field** (ADR-0473) searches a remote directory through an ephemeral
  worker query. It is bound to a *task*: the route is
  `POST /api/v1/tasks/{key}/form-data/{field}/query`, the caller is authorised against
  the task, and a cursor is sealed to it. An order form in the shop has no task yet.
  It is also a *search* that returns records; a name check needs one bit.
- A **decision field** (ADR-0511) takes its options from a DMN decision, through a route
  bound to the stored form, `POST /api/v1/forms/{id}/fields/{field}/options`, which the
  shop already calls. A decision knows nothing about a tenant, so it cannot say whether a
  name is taken.

The question this record answers: **how can an order form tell a person, while they
type, whether a name is still free in Entra ID and SharePoint — without the engine
holding a Microsoft credential, without anything per keystroke becoming durable, and
without turning the form into an oracle that walks a tenant's names?**

## Decision drivers

- **The Worker's credential stays in the Worker** (ADR-0172, ADR-0473). The engine builds
  no Graph client for this.
- **Nothing per keystroke is durable** (invariants I2, I6; ADR-0473). Only the order is.
- **The server decides what a form may ask.** Which Workers, which resources, which
  SharePoint host — from the stored form, checked at save, never from the request.
- **One bit leaves.** The answer is "free" or "taken", never what holds the name.
- **The check is a courtesy, not the rule.** A name can be taken between the check and
  the provisioning; the process that creates the object asks again and owns the refusal.
- **It works in the shop**, where the form is filled before any task exists.

## Considered options

1. **Keep it in provisioning only**: the process checks and refuses the position.
2. **A durable job per check**: a service task in a tiny process the form starts.
3. **A form-bound availability field over ADR-0473's worker query**: a block on a
   textfield names the sources; the route bound to the stored form asks each source with
   a *value lookup* and folds the answers.
4. **The engine asks Graph itself** with the vault credential of a Console-managed
   Worker.
5. **A release-bound route**: the same check, but authorised against the shop release and
   the caller's catalogue rather than against the form.

## Decision outcome

Chosen: **option 3**, with the gates below.

### Why not the others, at their strongest

**Option 1** is what happens anyway and stays: the provisioning process is the only
place that can know. Its weakness is only the person's experience — an order, an
approval and a rejection for a name the form could have refused in a second. Option 3
does not replace it; it spares the round trip in the common case.

**Option 2** reuses the job path, the most tested part of Atlas. It would write an
instance, a job and its completion per keystroke, keep every typed name in the log and
in every backup, and need a process deployed beside every form. ADR-0473 refused this for
a search, and a name check is the same shape with less to return.

**Option 4** is the cheapest and would also work for Workers configured only in the
Console. It breaks the property ADR-0172 was written to establish, that the engine never
builds a client for an identity that can change a directory, and it would not reach a
Worker configured on an external host.

**Option 5** has the tightest audience: only somebody who can order the product could
ask. It needs the release and the catalogue to answer what the form already answers —
which field, which sources — and it would not serve the same field in a task form, where
an operator corrects a rejected name. The audience option 3 admits instead (any signed-in
person who may read the form) learns at most whether a group alias or a site name exists,
which every member of a Microsoft 365 tenant can already see in the address book and the
SharePoint site directory, and only at the rate limit below. If a deployment considers
names confidential, the Worker's editors keep it closed to forms.

### What a form says

A `textfield` carries one versioned block; form-js keeps a member it does not know, so
the form stays a form-js document and renders as a plain text field anywhere else:

```json
{
  "type": "textfield",
  "key": "url",
  "label": "URL",
  "validate": { "required": true, "pattern": "^[a-z0-9][a-z0-9_-]{2,63}$" },
  "atlasAvailability": {
    "version": 1,
    "minLength": 3,
    "checks": [
      { "workerType": "entra", "worker": "ucc_entra", "resource": "groups" },
      { "workerType": "entra", "worker": "ucc_entra", "resource": "deletedGroups" },
      { "workerType": "sharepoint", "worker": "ucc_sharepoint", "resource": "sites",
        "host": "contoso.sharepoint.com", "managedPath": "sites" }
    ]
  }
}
```

The sources are a table in the server (`api/formavailability`), each a view of the
Worker Type's own rules:

| Source | Worker Type asks | Taken when |
|---|---|---|
| `entra/groups` | `GET /groups?$filter=mailNickname eq '…'&$top=1` | a group has the alias |
| `entra/deletedGroups` | the same on `/directory/deletedItems/microsoft.graph.group` | a deleted group still holds it (30 days) |
| `exchange/recipients` | `Get-Recipient -Filter "(Alias -eq '…') -or (EmailAddresses -like 'smtp:…@*')" -ResultSize 1` | a mailbox, list, contact or group holds the alias or an address with that local part |
| `sharepoint/sites` | `get-site` on `https://<host>/<managedPath>/<name>` | Graph answers the site |

The save refuses a block this server cannot serve: an unknown source, a missing Worker
name, a SharePoint check without a valid host, a managed path other than `sites` or
`teams`, a source named twice, more than four sources, a block on anything but a
textfield, a newer version.

### What a request says, and what it gets

`POST /api/v1/forms/{id}/fields/{field}/availability` with `{"value": "…"}` and nothing
else (unknown members are refused). The server lower-cases and trims the value — an alias
and a site URL are case-insensitive — refuses one shorter than `minLength` or of a shape a
source cannot hold, checks every named Worker (configured, of the right type, enabled,
and open to forms by its editors' `formLookups` switch, ADR-0473), and puts one value
lookup per source to the broker side by side. The answer is
`{"value": "dev-huda", "available": false}`: free only when every source found nothing,
and an error when any source could not answer — a name one source could not check is not
known to be free.

The value lookup is ADR-0473's `lookup.Query` with `Value` set; the SharePoint source
carries its host and managed path as the query's two equality filters, which the
SharePoint Worker Type allowlists (`connector/sharepoint/lookup.go`) and turns into
`get-site`. A Worker acting as a person (a brokered, delegated identity, ADR-0475) does
not answer: a lookup has no job to authorise a person's token for.

### Gates

- **Signed-in only.** With authentication on, a request without a person is refused;
  a public form carrying the field still submits, it only loses the early warning.
- **Per-person rate limit**: 10 in a burst, 2 a second sustained — the field debounces
  (400 ms), so a person typing is untouched and a script cannot walk a tenant's names.
- **The Worker's own switch.** `formLookups` now opens a Worker to both kinds of form
  question; the Console offers it for every Worker Type a directory field or an
  availability check can ask (Entra, SharePoint).
- **Nothing is written.** The query lives in the broker's memory for the request's
  lifetime, as in ADR-0473. The processor, the log and recovery never see it.

### In the shop

The basket mounts each product's order form; a form with availability fields gets a
watcher (`attachAvailabilityChecks` in `formviewer.js`) that listens to form-js's
`changed` event, debounces, asks the route, and writes one line per field *under the
form* — "URL: «dev-huda» ist bereits vergeben." Under the form and not inside it, because
form-js owns its fields' DOM and redraws it. A name known to be taken stops the order
with a message; a check that could not run does not.

### Consequences

- **Positive:** a person learns at once that a name is taken; the provisioning
  process's refusal becomes the rare case. One mechanism serves every Worker Type that
  adds a value lookup.
- **Negative / trade-offs accepted:** a form now names a SharePoint host, which an
  installation has to adapt; a check is only as fresh as the moment it ran; two people can
  both be told "free".
- **Follow-ups:** the form editor has no panel for the block yet (it is authored as
  JSON, and kept by the editor); task forms and the amend dialog of an order line do not
  mount the watcher yet. Exchange recipients were first named here as a follow-up and
  became a source in the same change: a Microsoft 365 group takes the address
  `<alias>@<domain>`, which a shared mailbox or a distribution list made by hand may
  already hold, so a name Entra reports free could still fail at provisioning.

## Links

- builds on [ADR-0473](0473-form-remote-data-sources.md) (ephemeral worker query, the
  `formLookups` switch) and [ADR-0511](0511-form-options-from-a-decision.md)'s form-bound route
- uses the Entra lookup resources of [ADR-0172](0172-entra-id-connector.md) (amended
  2026-11-03) and SharePoint's `get-site` of [ADR-0141](0141-sharepoint-connector.md)
  (amended 2026-11-03)
- honours invariants I2, I3 and I6: nothing here reaches the processor or the log
