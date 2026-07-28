# Atlas examples

Runnable BPMN models that double as showcases of what the Atlas engine can do
and as deterministic test scenarios. Every request/response below was verified
against a live Atlas server (`0.1.0-dev`).

| File | What it is |
|------|-----------|
| [`order-fulfillment.bpmn`](order-fulfillment.bpmn) | A self-completing order-fulfillment process that exercises inline scripts and all three gateway kinds, and drives itself to an end event with **no external workers attached**. |
| [`cart-total.bpmn`](cart-total.bpmn) | A shopping-cart checkout that computes an order total (subtotal → rebate → VAT → shipping) entirely in inline FEEL, and routes on the computed sum. Self-completing. |
| [`entra-create-account.bpmn`](entra-create-account.bpmn) | A PowerShell `jobScript` task that creates an EntraID account — the *worker-backed* counterpart: its token parks on the script job until a PowerShell script worker runs it. |

> Looking for a model that parks on human tasks so you can watch the task
> lifecycle? See [`../postman/order-approval.bpmn`](../postman/order-approval.bpmn)
> and the [Postman onboarding kit](../postman/README.md). The two examples are
> complementary: that one shows the *human-in-the-loop* side, this one shows the
> *fully automatic* side.

---

## `order-fulfillment.bpmn` — the self-completing showcase

A small order-to-fulfillment flow, modelled as a realistic happy path with
data-based branching and concurrency:

```
Order received
   → Register order        (seed case data)
   → Score risk            (compute from prior data)
   → Order value?  ──ᴇxᴄʟᴜsɪᴠᴇ──┐
        ≥ 1000               otherwise
          │                     └→ Auto-approve small order → (Order fast-tracked)
   ┌──ᴘᴀʀᴀʟʟᴇʟ (fork)──┐
   Reserve stock   Prepare invoice
   └────ᴘᴀʀᴀʟʟᴇʟ (join)────┘        ← waits for BOTH
   → Which notifications? ──ɪɴᴄʟᴜsɪᴠᴇ (fork)──┐
        priority        EU region       otherwise
        Priority        EU compliance    Standard
        handling        check            notification
   └────────ɪɴᴄʟᴜsɪᴠᴇ (join)────────┘   ← waits for the branches that opened
   → Order fulfilled
```

### Why it is interesting

The whole model runs to completion the moment you start an instance, because:

- **Every activity is an inline FEEL script task** (`<script expression="…"
  resultVariable="…"/>`). Atlas evaluates these *inside the engine* — unlike a
  `serviceTask`, a `<jobScript>`, or a `businessRuleTask`, an inline script needs
  no external worker, so the token never parks. (A `serviceTask` with no worker
  attached would sit active forever — correct engine behavior, but not what you
  want in a smoke test.)
- **The process seeds its own data.** `atlas_create_instance` / `POST
  …/instances` here is called with no input variables, so the first script task
  writes the case data (`order`) as a FEEL context, and every downstream decision
  reads from it. That makes each run **deterministic** — the same path every
  time — which is exactly what you want from a regression scenario.

### Engine capabilities it demonstrates

| Capability | Where in the model |
|-----------|--------------------|
| Inline FEEL script task (in-engine, no worker) | every `scriptTask` |
| Variables written by one step, read by the next | `score_risk` reads `order.amount`/`order.items` |
| FEEL context literals + member access | `register_order` writes `{amount, region, priority, items}` |
| FEEL arithmetic | `score_risk` (`= order.amount / 100 + order.items`) |
| Data-based **exclusive** gateway + default flow | `value_gw` (`= order.amount > 1000`, default `flow_express`) |
| **Parallel** split & synchronizing join | `fanout` / `fanin` |
| **Inclusive** split (multi-branch) & synchronizing join | `notify_split` / `notify_join` |
| Default-flow suppression when a condition matches | `flow_notify_standard` is *not* taken |
| Multiple end events | `order_fulfilled`, `order_fast_tracked` |

### Modelling conventions (Camunda best practices)

The model follows the naming and structure guidance from the Camunda
[BPMN modelling best practices](https://docs.camunda.io/docs/components/best-practices/modeling/):

- **Tasks** are named *verb + object* — *Register order*, *Reserve stock*.
- **Gateways** are named as a **question** — *Order value?*, *Which notifications?* —
  and their **outgoing flows are labelled with the answers** (`≥ 1000`,
  `otherwise`, `priority`, `EU region`).
- **Events** are named *object + past participle* — *Order received*,
  *Order fulfilled*.
- **Splitting and joining gateways are paired** (`fanout`/`fanin`,
  `notify_split`/`notify_join`); routing is never done by branching a task's
  outgoing flows directly.
- A single blank start event; explicit, differently-named end states.

> No `<bpmndi:BPMNDiagram>` (visual layout) is checked in, matching the
> convention of the other sample model in this repo. Atlas **auto-generates**
> diagram layout on deploy, so `atlas_get_process_xml` (or the bpmn.io viewer at
> `http://localhost:8080/`) returns a fully rendered diagram anyway.

---

## Run it

### Via the Atlas MCP server (AI agents)

```
atlas_deploy          { xml: <contents of order-fulfillment.bpmn> }   → returns a definition `key`
atlas_create_instance { key: <that key> }                             → runs to idle (here: completed)
atlas_process_runtime { key: <that key> }                             → per-element visit counts
```

### Via curl

```bash
BASE=http://localhost:8080

KEY=$(curl -s -X POST $BASE/api/v1/deployments \
  -H 'Content-Type: application/xml' \
  --data-binary @order-fulfillment.bpmn | python3 -c 'import sys,json;print(json.load(sys.stdin)["key"])')

curl -s -X POST $BASE/api/v1/processes/$KEY/instances -H 'Content-Type: application/json' -d '{}'
curl -s "$BASE/api/v1/processes/$KEY/runtime"
```

## Expected result — use it as a test

Because the run is deterministic, the per-element **visit counts** are a precise
assertion target. After one instance the definition should report
`instances: 0, tokens: 0` (the instance completed) with:

| Element | Type | Visits |
|---------|------|:------:|
| `order_received` | StartEvent | 1 |
| `register_order` | ScriptTask | 1 |
| `score_risk` | ScriptTask | 1 |
| `value_gw` | ExclusiveGateway | 1 |
| `fanout` | ParallelGateway | 1 |
| `reserve_stock` | ScriptTask | 1 |
| `prepare_invoice` | ScriptTask | 1 |
| `fanin` | ParallelGateway | **2** (one visit per incoming branch; join fires once) |
| `notify_split` | InclusiveGateway | 1 |
| `notify_priority` | ScriptTask | 1 |
| `notify_eu` | ScriptTask | 1 |
| `notify_standard` | ScriptTask | **0** (default suppressed — a condition matched) |
| `notify_join` | InclusiveGateway | **2** (one visit per opened branch) |
| `order_fulfilled` | EndEvent | 1 |
| `auto_approve` / `order_fast_tracked` | ScriptTask / EndEvent | 0 (express path not taken) |

Those three highlighted numbers are the interesting invariants:

- `fanin = 2` proves the **parallel join synchronised** both branches.
- `notify_join = 2` proves the **inclusive join waited for exactly the branches
  that opened** (two), not all three and not just one.
- `notify_standard = 0` proves the **inclusive default was correctly suppressed**
  because at least one condition held.

To exercise the *other* exclusive branch, change the seeded amount in
`register_order` to a value `≤ 1000`; the instance then takes
`Auto-approve small order → Order fast-tracked` instead.

---

## `cart-total.bpmn` — shopping-cart sum calculation

A checkout that turns a cart of line items into a payable order total, computed
step by step in inline FEEL — the engine does real money arithmetic, no worker
involved:

```
Warenkorb abgeschickt
   → Warenkorb laden          positions = [ {BPMN-Buch, 24.90, ×1},
                                            {Kaffeetasse, 9.95, ×2},
                                            {Aufkleber-Set, 4.50, ×3} ]
   → Artikelanzahl zaehlen    itemCount = sum(for p in positions return p.qty)
   → Zwischensumme berechnen  subtotal  = decimal(sum(for p in positions
                                                      return p.price * p.qty), 2)
   → Zwischensumme ≥ 50 €? ──┐
        ja                  nein
        Rabatt & Gratis-     Standard-
        versand              versand
        {discount: 10%,      {discount: 0,
         shipping: 0}         shipping: 4.90}
   └──────────┬──────────┘
   → Nettobetrag    net   = decimal(subtotal − rebate.discount, 2)
   → MwSt (19%)     tax   = decimal(net * 0.19, 2)
   → Gesamtsumme    total = decimal(net + tax + rebate.shipping, 2)
   → Summe > 100 €? ── ja → Freigabe erforderlich
                      nein → Bestellung bestaetigt
```

### The calculation

`sum(for p in positions return p.price * p.qty)` is the cart sum; `decimal(x, 2)`
rounds to cents (FEEL uses exact decimals, so the figures are reproducible). For
the seeded cart:

| Step | FEEL | Value |
|------|------|------:|
| Zwischensumme | `decimal(sum(p.price·p.qty), 2)` | **58.30 €** |
| Rabatt 10 % (≥ 50 €) | `decimal(subtotal · 0.10, 2)` | − 5.83 € |
| Nettobetrag | `subtotal − discount` | 52.47 € |
| MwSt 19 % | `decimal(net · 0.19, 2)` | + 9.97 € |
| Versand (frei ≥ 50 €) | — | 0.00 € |
| **Gesamtsumme** | `decimal(net + tax + shipping, 2)` | **62.44 €** |

Both gateways branch on the *computed* sum, so the visited end event alone proves
the arithmetic: `subtotal ≥ 50` takes the rebate branch, and `total ≤ 100` reaches
`Bestellung bestaetigt`. Expected visits after one instance
(`instances: 0, tokens: 0`):

| Element | Type | Visits |
|---------|------|:------:|
| `cart_submitted` | StartEvent | 1 |
| `load_cart` · `count_items` · `subtotal` | ScriptTask | 1 each |
| `threshold_gw` | ExclusiveGateway | 1 |
| `apply_rebate` | ScriptTask | 1 |
| `standard_shipping` | ScriptTask | **0** (subtotal ≥ 50 → rebate branch) |
| `rebate_join` | ExclusiveGateway | 1 |
| `net` · `tax` · `grand_total` | ScriptTask | 1 each |
| `approval_gw` | ExclusiveGateway | 1 |
| `confirmed` | EndEvent | 1 |
| `needs_approval` | EndEvent | **0** (total ≤ 100 → no approval) |

To land on the *other* branches, edit the seeded `positions` in `load_cart`: a cart
under 50 € takes `Standardversand` (with 4.90 € shipping), and one whose total
exceeds 100 € reaches `Freigabe erforderlich`.

## Clean up

The instance completes, so it does not block anything, but to remove the
definition from the engine and disk:

```
atlas_delete_process { key: <that key> }     # or: curl -X DELETE $BASE/api/v1/processes/$KEY
```
