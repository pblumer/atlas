# ADR-DRAFT: A product manager maintains the catalogue over MCP

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-16
- **Deciders:** Atlas maintainers
- **Open question:** Whether a catalogue ever needs a delete. This record keeps the
  model's answer — a product is withdrawn, a catalogue is not removable at all — and
  that answer was written for records an order resolves through. It was not written
  for the twenty draft products an agent can now file in a minute and nobody can
  clear away. No installation has yet run an agent against a catalogue long enough
  to say whether that becomes the first thing somebody asks for.
- **Question checked:** 2026-09

## Context and problem statement

The self-service portal's catalogue ([ADR-0312](0312-portal-catalogue-order-inventory.md))
is maintained by a product manager ([ADR-0315](0315-portal-roles-and-responsibilities.md)):
they define products, assemble catalogues, decide what needs approval, and publish a
release. The whole of that surface exists over HTTP and is driven from the Console.

None of it was reachable over MCP. `mcp/tool_registry_drift_test.go` recorded the
reason against each route, and the reason was a date rather than a principle:

> portal catalogue surface still being built; a tool is a public contract

That was true when it was written. Since then the catalogue has gained ordering,
approvals, releases, an inventory, reconciliation, search, categories, prices,
configuration forms, eligibility and time-bounded entitlements. The shapes stopped
moving; the omission did not.

Meanwhile the work the omission blocks is exactly what an agent is good at. Deriving
a catalogue from an ArchiMate model, filling two hundred products' keywords, checking
which products are missing a deprovisioning binding before a publish — each is
mechanical, wide, and dull, which is the shape of work worth handing over.

Two things stood in the way of simply exposing the routes, and neither is about the
catalogue being unfinished.

**Saving a product is a full replace.** `POST /api/v1/catalog-products` stores the
record it is given. For the Console that is correct: the form renders every field
and posts every field, so what it overwrites is what is on the screen in front of
somebody. For a caller that changes one field of a record it read a minute ago, it
silently destroys every field it did not think to send — translations, variants,
keywords, eligibility, the deprovisioning binding — and destroys a concurrent
maintainer's edit with nothing to say it existed.

**"Delete" does not exist here, and the request for these tools asked for it.**
[ADR-0312](0312-portal-catalogue-order-inventory.md) gives an item three states and
no fourth: an order placed years ago and an entitlement still held both resolve
through the item, so an item is withdrawn rather than removed. A catalogue has no
delete at all.

## Decision drivers

- A tool is a public contract; exposing one pins the shape behind it.
- An agent has no screen, so what a form teaches by rendering, a tool description has
  to say.
- The likeliest agent defect on a replace-shaped write is destroying a field it never
  read, and it is silent.
- Whatever an agent may do, it may do as the person who called it and no more
  ([ADR-0196](0196-authenticated-mcp-transport.md)).
- A new mechanism a reader has to learn is a cost; the repository already has an
  optimistic-concurrency pattern.

## Considered options

1. **Keep the omission.** Catalogue maintenance stays a Console act.
2. **Expose the routes as they are.** Nine tools, replace semantics documented in
   their descriptions and nothing else.
3. **Expose the routes, and make the replace safe to hand to a caller that is not a
   form.**

## Decision outcome

Chosen option: **3 — nine tools, plus a stated precondition on the product write.**

Option 1 is no longer arguing what it was written to argue. The surface it protects
is built; keeping the omission would mean a maintainer's most mechanical work stays
manual for a reason that has expired.

Option 2 is the serious contender and fails on one specific thing. A description
saying "send the whole record back" is advice, and advice is what a caller follows
until it is under a deadline or halfway through a context window. The failure it
allows is not an error the caller sees — it is a product that quietly lost its
French name, discovered by whoever opens the portal in French. A surface handed to a
non-human caller should make that failure a refusal rather than a silence.

### The nine tools

One HTTP operation each, per [ADR-0016](0016-mcp-server-over-http-api.md). The
adapter interprets no catalogue, resolves no edge and decides no publish.

| Tool | Operation |
|---|---|
| `atlas_list_catalogs` | `GET /api/v1/catalogs` |
| `atlas_get_catalog` | `GET /api/v1/catalogs/{id}` |
| `atlas_create_catalog` | `POST /api/v1/catalogs` |
| `atlas_update_catalog` | `PATCH /api/v1/catalogs/{id}` |
| `atlas_list_catalog_products` | `GET /api/v1/catalog-products` |
| `atlas_save_catalog_product` | `POST /api/v1/catalog-products` |
| `atlas_publish_catalog` | `POST /api/v1/catalogs/{id}/releases` |
| `atlas_catalog_releases` | `GET /api/v1/catalogs/{id}/releases` |
| `atlas_import_catalog_archimate` | `POST /api/v1/catalogs/{id}/import` |

What stays omitted stays omitted for reasons that are about the act and not about
the calendar: a catalogue's theme and brand mark are an operator's choice, the
ordering routes are somebody's own, and the portal's view of "your" catalogue is
answered from a person's groups, which an agent has none of.

### The precondition: a revision, not a timestamp

An `Item` gains `revision`, and `HandleSaveItem` follows the rule
`api/capability/service.go` already uses, spelled the same way: a stated revision
that does not match the stored one is refused as a conflict, and every write
advances it. Zero means no precondition and replaces unconditionally, which is what
the Console sends — it builds its body from form fields and knows no revision, and
demanding one would be a breaking change to the surface that has no concurrency
problem.

The obvious alternative was to use `updatedAt`, which the record already carries, and
it does not work. It is Unix nanoseconds — around 1.8e18, past the 2^53 where a
float64 stops representing integers exactly. Every client that decodes JSON numbers
as doubles, which is most of them and every MCP client, hands back a value a few
hundred nanoseconds off and is told its own read was stale. This was found by writing
the test with a float64 round trip in it, and that round trip is kept in
`mcp/tools_catalog_test.go` so the mistake cannot be made again.

### Withdrawal is the delete, and it is not a tool

`state: "withdrawn"` on the ordinary save. No `atlas_delete_catalog_product` exists,
because there is nothing for it to call and inventing a route would mean deciding, in
an adapter, a question [ADR-0312](0312-portal-catalogue-order-inventory.md) already
answered. Every write tool's description says so in the place a caller looks — on the
`state` property — rather than leaving an agent to search for a tool that is not
there and improvise.

### Authorization is the caller's, and over stdio it stops short

The catalogue routes require `productmanager` plus editor or owner on the catalogue
itself, and the adapter holds no credential of its own
([ADR-0196](0196-authenticated-mcp-transport.md)). Over the HTTP transport the
caller's own credential is forwarded, so a signed-in product manager reaches exactly
their own catalogues and the tools need no new authority whatsoever.

Over stdio the adapter authenticates with an API token, and **no API token can carry
`productmanager` today**: minting is admin-only and `tokenRoles` gives an admin
minter `legacyRoles()`, which [ADR-0315](0315-portal-roles-and-responsibilities.md)
deliberately kept the new role out of so that no account is handed catalogue control
by an upgrade. So over stdio the read tools work and the write tools answer 403.

That is recorded here as the outcome rather than fixed, because fixing it means
widening what a machine credential can reach, and that is ADR-0194's decision to
revisit on its own evidence — not a side effect of adding an adapter.

### Consequences

- **Positive:** The catalogue's most mechanical maintenance is reachable by an agent,
  under the caller's own authority and object scope. The replace that made the surface
  risky to expose is now refusable. The Console is untouched.
- **Negative / trade-offs accepted:** Nine more public contracts. A product gains a
  field that travels into every release. The precondition is optional, so a caller
  that omits it gets the old behaviour — the guard is available, not enforced, and
  that is the price of not breaking the Console.
- **Follow-ups / risks to watch:** The stdio gap above. The open question on delete.
  And one defect this record found and did not fix: the Console's product form posts
  a body built from its own fields, which does not include `variants`, `lifecycle`,
  `keywords`, `eligible` or `maxDays` — so editing a product there clears all five,
  and resets `createdAt`. That is a Console bug on the same replace, it predates
  these tools, and it wants its own change.

## Pros and cons of the options

### Option 1 — keep the omission
- Good: no new public contract; no new field on a stored record.
- Bad: the argument for it has expired, and the work it blocks is the work an agent
  is best at.

### Option 2 — expose as-is
- Good: the smallest change; a pure adapter with nothing behind it.
- Bad: hands a replace-shaped write to a caller with no screen, where the failure is
  silent and lands on whoever reads the portal in the language that got dropped.

### Option 3 — expose, with a precondition
- Good: the failure becomes a refusal; follows the concurrency pattern already here.
- Bad: a field on `Item`, and a guard that is optional by necessity.

## Links

- relates to [ADR-0016](0016-mcp-server-over-http-api.md) — the adapter boundary
- relates to [ADR-0312](0312-portal-catalogue-order-inventory.md) — the catalogue model
- relates to [ADR-0315](0315-portal-roles-and-responsibilities.md) — who maintains it
- relates to [ADR-0196](0196-authenticated-mcp-transport.md) — whose credential a tool call carries
- relates to [ADR-0194](0194-api-tokens.md) — why a machine credential stops short
