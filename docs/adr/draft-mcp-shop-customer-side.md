# ADR-DRAFT: The shop's customer side is offered over MCP, as the connected person, unless the server switches it off

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-11-10
- **Deciders:** Atlas maintainers

## Context and problem statement

Until now the MCP surface left out everything a person does in the shop. ADR-0428
kept the change of a held position off it "for the reason a return is not", and
ADR-0429 §2, amended on 2026-10-01, let an agent ask a held position only for an
operator's or a system's action and never for a customer's: `atlas_ask_order_line_action`
refused `trigger: customer` in the adapter, and the drift registry
(`mcp/tool_registry_drift_test.go`) omitted placing, reading, withdrawing, correcting,
returning and changing orders, the shop's catalogues, its tasks and the approver's
own approvals — most of them with the reason that "an agent is not a who", "an agent is
nobody's orderer" or that the ordering surface was still being built.

Those reasons assumed the agent acts as a server identity. Since ADR-0196 the adapter
holds no credential of its own: over `/mcp` it forwards the caller's credential
verbatim, and since ADR-0200 that caller is a person who signed in and approved the
connection with OAuth; over stdio it is the person whose API token `atlas mcp --token`
was started with. A tool call is then the person's own request, decided by the same
handler under the same rules as the portal's, and the order or the decision it makes
records that person.

What the maintainers now need is a **repeatable, agent-driven acceptance run of the
catalogue**: an agent that holds one MCP connection per test person — an employee who
orders, a manager who approves, an administration that fulfils — and walks the same
paths through the shop every time. With the shop off the MCP surface, that run needs a
browser per person.

The question: should the shop's customer side be an MCP surface, and if it is, how
does an installation that does not want agents in its shop say so?

## Decision drivers

- Repeatable end-to-end tests of a catalogue, driven by an agent, with one connection
  per test person.
- An agent must add no privilege: each tool is one HTTP operation (ADR-0016), and the
  handler decides as it decides for the portal.
- Consent and attribution: the person connects their own MCP session, so the agent
  acts with their consent and under their name, and the audit trail names them.
- An installation must be able to keep the shop a browser's, and that line must be
  held by the server, not by an adapter it does not control.

## Considered options

1. Keep the exclusion (ADR-0428, ADR-0429 §2 as amended on 2026-10-01).
2. Offer the shop over MCP unconditionally.
3. Offer it by default, with a server setting that refuses it to requests that come
   through MCP (opt-out).
4. Offer it only when a server setting switches it on (opt-in).

## Decision outcome

Chosen option: **3, offered by default, with an opt-out the server enforces**. The
reversal of ADR-0428's and ADR-0429 §2's exclusion is the maintainers' decision of
2026-11-10.

### What is offered

The tools, each one route (`mcp/tools_shop.go`):

| Tool | Route |
|------|-------|
| `atlas_shop_catalogs`, `atlas_shop_catalog` | `GET /api/v1/shop/catalogs`, `GET /api/v1/shop/catalog` |
| `atlas_shop_form_query` | `POST /api/v1/shop/releases/{release}/items/{item}/form-data/{field}/query` |
| `atlas_place_order` | `POST /api/v1/orders` |
| `atlas_list_orders`, `atlas_get_order` | `GET /api/v1/orders`, `GET /api/v1/orders/{id}` |
| `atlas_order_ready_lines` | `GET /api/v1/orders/{id}/next` (operator) |
| `atlas_shop_tasks` | `GET /api/v1/shop/tasks` |
| `atlas_order_line_progress` | `GET /api/v1/shop/orders/{id}/lines/{position}/progress` |
| `atlas_cancel_order`, `atlas_cancel_order_line` | `POST …/cancel`, `POST …/lines/{item}/cancel` |
| `atlas_amend_order_line` | `POST …/lines/{item}/details` |
| `atlas_return_order_line` | `POST …/lines/{item}/return` |
| `atlas_change_order_line` | `POST …/lines/{item}/change` |
| `atlas_list_approvals`, `atlas_decide_approvals` | `GET /api/v1/approvals`, `POST /api/v1/approvals/decide` |

and `atlas_ask_order_line_action` takes `trigger: customer` beside `operator` and
`system`. `atlas_catalog_releases` gained `limit`, so the newest release — the one an
order names — is one call.

Three order routes stay off the surface, because none of them is a person's own act:
`POST /api/v1/orders/{id}/lines/{item}` reports a provisioning outcome on a target
system's behalf; `POST …/lines/{item}/start` is the fulfilment and approval models'
call; and `POST …/lines/{item}/decision` records a decision under whatever name its
body gives — it is the approval process's record. An agent approves as the person it
acts for through `atlas_decide_approvals` or the approval task (`atlas_complete_task`),
which record the caller.

### The switch

`--mcp-shop` (`ATLAS_MCP_SHOP`, read as strictly as `ATLAS_CATALOGUE_PURGE`), default
`true`, after the precedent of `--catalogue-purge` (ADR-0469). `--mcp-shop=false`
(`api.WithoutMCPShop`) refuses the routes above, and the action act with
`trigger: customer` or no trigger, to a request that came through MCP — 403, with a
sentence naming `--mcp-shop=false (ATLAS_MCP_SHOP=false)` — and serves the portal as
before. An operator's and the system's actions stay available to agents either way.
`GET /api/v1/info` answers `mcpShop`, so an agent can tell the person before a call is
refused. The tools stay listed either way and say in their descriptions that a server
may have switched them off.

### How the server tells an MCP request

Two signals, either of which makes a request an MCP request (`api/mcpshop.go`,
`Server.viaMCP`):

- **The transport's stamp.** Every request entering `/mcp` is stamped with
  `X-Atlas-Via-MCP` carrying this server's internal token (ADR-0196's
  `mcpTransport`), and the adapter forwards it on the API calls its tools make. It is
  verified and cannot be forged by a caller. It exists only while authentication is on,
  because there is no internal token without it.
- **The adapter's declaration.** Every request the `mcp` package's `Client` makes
  carries `X-Atlas-MCP-Adapter` — the in-process adapter behind `/mcp` and a stdio
  adapter (`atlas mcp`) alike. It covers the server without authentication and the
  remote stdio adapter, which is a separate process holding an ordinary API token and
  whose requests nothing else distinguishes.

**The limit.** The declaration is honest, not proven. A client that does not send it —
a third-party MCP server wrapped around the HTTP API, or an `atlas mcp` binary older
than this record — is not refused by the switch. The server cannot do better for a
remote adapter: its request is, byte for byte, the request the same token holder could
make directly. That is acceptable for what the switch decides. It is not a boundary
against the holder of the credential, who may use the shop in the portal with exactly
the same rights; it decides whether agents Atlas ships drive the shop on this
installation. An installation that must keep a credential out of the shop altogether
keeps that credential's roles and catalogue audiences out of it — the authorization
that holds for the portal holds here.

### Consequences

- **Positive:** a catalogue can be tested end to end by an agent, repeatably, with one
  connection per person; the agent can do in the shop only what that person can, and
  every order, withdrawal, correction, return, change and decision names that person.
- **Positive:** no handler changed its authorization; the only server change is a
  refusal, and only when an operator asked for it.
- **Negative / trade-offs accepted:** ordering, returning and changing reach target
  systems, and an agent that misreads a basket orders the wrong thing in the person's
  name. The descriptions say so, say that placing an order has no idempotency key, and
  tell the agent to read its orders before trying again.
- **Negative / trade-offs accepted:** the opt-out is held by a declaration for a remote
  stdio adapter, with the limit above.
- **Follow-ups / risks to watch:** the inventory (`GET /api/v1/inventory`) stays off the
  surface for the reason ADR-0346 gives — its administrator's form reads somebody
  else's access; a test run reads what an order granted from the order. If acceptance
  runs need it, the person's own form could be a tool under the same switch.

## Pros and cons of the options

### Option 1 — keep the exclusion
- Good: nothing an agent does in the shop can surprise anybody.
- Bad: a catalogue cannot be tested end to end without a browser per person, and the
  reasons for the exclusion no longer describe how an agent connects.

### Option 2 — offer it unconditionally
- Good: simplest.
- Bad: an installation whose shop must stay a browser's cannot say so.

### Option 3 — opt-out held by the server (chosen)
- Good: works on a development and test installation with nothing to set, and a
  production installation can switch it off; the server, not the adapter, refuses.
- Bad: the refusal of a remote adapter rests on its declaration.

### Option 4 — opt-in
- Good: nothing changes for an installation that did not ask.
- Bad: every test installation has to remember a flag, and the precedent the
  maintainers chose for a development-first capability is the opt-out
  (`--catalogue-purge`, `--instance-purge`).

## Links

- reverses the MCP exclusion of ADR-0428 ("Landed in a second step", change) and of
  ADR-0429 §2 as amended on 2026-10-01
- relates to ADR-0016 (each tool is one HTTP operation), ADR-0196 (the adapter holds no
  credential), ADR-0200 (OAuth for remote MCP clients), ADR-0469 (the opt-out precedent),
  ADR-0473 (order-form directory fields), ADR-0362 (deciding a request), ADR-0416 (the
  shop's tasks)
