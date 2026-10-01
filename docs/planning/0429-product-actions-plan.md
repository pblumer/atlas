# Implementation plan — Product actions (ADR-0429)

Companion to [ADR-0429](../adr/0429-product-actions-are-commands-with-published-outcomes.md).
The ADR fixes the decision and its shape, including what was decided at acceptance (§10);
this document is the engineering plan — slices, files, types, routes and the test
obligations of each. It is a living plan, edited as slices land.

**Ground rules**

- Slices land in the order of ADR-0429 §10, decision 10: **A → B → C → D → E**. Each is
  one pull request that leaves `main` releasable.
- **Nothing is migrated.** A product that carries `operations` keeps working and is read
  as actions; an order line keeps what it froze (ADR-0312, ADR-0427).
- **Test-first** (ADR-0018). Every rule gets a test that fails without it; every UI change
  gets an e2e spec against the real asset, as the Modeler and catalogue editor already
  have.
- Engine changes (slices C and E) honour the six invariants: a new fact is an event folded
  by the one `applyToState`, generated values are frozen into it, nothing is answered
  before it is durable, resolution happens at publish and at the API boundary.
- Definition of done per slice: `go build ./...`, `go test -race -timeout=45m ./...`,
  `go vet ./...` green, `gofmt -l .` empty, the e2e suite green, `make whats-new` re-run
  when `CHANGELOG.md` changed.

Already landed under this record: the conditional-start refusal (§9, #1173) and the
Worker events in the Modeler's message picker (§6, #1176).

---

## Slice A — Actions on the product — ✅ landed

Built as planned below. ADR-0429 §1 carries the as-built note; the editor's `form` and
`outcomes` controls wait for slices B and C, and a save carries both through untouched.

**Goal:** a product declares an open list of actions with closed effects; everything that
reads the old operation map reads actions instead; the editor maintains the list. No new
route, no runtime behaviour beyond the refusals below.

### Model (`api/catalog/action.go`, new)

```go
type Action struct {
    Key      string            `json:"key"`                // [a-z0-9-]{1,64}, unique per product
    Message  string            `json:"message"`            // unique per product
    Effect   string            `json:"effect"`             // provision | deprovision | change | service
    Triggers []string          `json:"triggers,omitempty"` // customer | operator | system
    Labels   map[string]string `json:"labels,omitempty"`   // language → button text
    Form     string            `json:"form,omitempty"`     // an Atlas form id (ADR-0358)
    Outcomes map[string]string `json:"outcomes,omitempty"` // completed|rejected|failed → event type
}
```

- `Item.Actions []Action` beside `Operations`; `order.Line.Actions` frozen at placement.
- `Item.ActionList()` returns `Actions`, or — for an item that still carries `operations`
  — the actions they mean: `provision` (no triggers), `deprovision` (customer, operator),
  `change` (customer, operator). One reading for every consumer.
- `BindingFor(op)` is rebuilt on `ActionList()`: `provision` and `deprovision` resolve by
  effect, any other name by key. Every existing caller keeps its signature.

### Publish rules (`checkBindings`, `LifecycleProblems`)

- An item carries `operations` **or** `actions`, never both; actions need a lifecycle
  process (the two-process form has no message to address).
- Exactly one action of effect `provision`, keyed `provision`, with no triggers — the order
  starts it. Exactly one of effect `deprovision`, keyed `deprovision`, with triggers among
  `customer` and `operator` — the inventory's own sweeps are not declared (ADR-0429 §1).
  The two keys are reserved for the two effects, so trigger ids and recorded instances keep
  the names they have today.
- `change` and `service` actions name at least one trigger from the closed set.
- Keys match `[a-z0-9-]{1,64}` and are unique; messages are non-blank and unique within the
  product; effects, triggers and outcome kinds come from their closed sets; a form id is not
  whitespace; an outcome's event type is not blank.
- Against the newest version of the process: in the `per-operation` form every action's
  message is a root message start; in the `per-position` form `provision` is a start,
  `deprovision` is a correlated catch and a start, and every `change` or `service` action is
  a correlated catch. This generalises today's rule for `change`.
- **Interpretation recorded here:** §1's "a message name is owned by one action of one
  product" is read as unique *within* a product. Products that share one lifecycle process
  share its names, as ADR-0425 lets them do today; every refusal treats a name as
  catalogue-owned if any product owns it.

### Who may not use a product's names

- **Inbound watches** (`api/inbound.go`): creating a watch, renaming one, or enabling one
  is refused with 409 when a product's action owns the name — a Worker's event must not
  drive a lifecycle around the inventory (ADR-0425 §8). The server today has no check on
  this path at all.
- **The reverse:** publishing a catalogue refuses an action whose message an inbound watch
  already publishes, through a lookup the server implements beside `EntryPoints`.
- `catalogOwnerOfEntry` / `catalogOwnerOfDelivered` (`api/triggerroute.go`) read
  `ActionList()`; the second now covers every non-provision action of a per-position
  product, not only `change` and `deprovision`.

### Order (`api/order`)

- `linesFor` freezes `Actions` beside `Operations`; `Line.BindingFor` passes them on;
  `Rebind` copies them (ADR-0427). `StillStarts`, `OldBindings` and the delete guard are
  process-level and unchanged.

### Translations (`api/catalog/translationgaps.go`)

- A `customer` or `operator` action without a label in a declared language is a reported
  gap, never a refusal (ADR-0429 §10, decision 7; ADR-0414).

### Surfaces

- **Editor** (`api/web/catalog-admin.js`): the three start-event boxes become an action
  list — key, message, effect, triggers, a label per catalogue language. Choosing a
  lifecycle process on a product with no actions pre-fills `provision` and `deprovision`
  with `<product>.provision` / `<product>.deprovision`; those two rows keep their key and
  effect and cannot be removed. A product that still carries `operations` opens as the
  actions they mean and is saved as actions. `form` and `outcomes` are carried through a
  save untouched until slices B and C give them controls. `publishNeeds` asks for the two
  messages.
- **MCP** (`mcp/tools_catalog.go`): `actions` in the save tool's schema, `operations`
  described as the legacy form.
- **OpenAPI** (`api/openapi.go`): the catalogue product schema gains `lifecycleProcess`,
  `lifecycleForm`, `actions` and the legacy `operations` — none of the lifecycle fields is
  declared there today.

### Tests

- `api/catalog`: action validation (every rule above, one failing case each), legacy
  reading, `BindingFor` parity between `operations` and the equivalent `actions`, freeze
  deep-copies `Actions` (the reflection guard in `freeze_test.go` demands it), per-operation
  and per-position publish rules against a stub shape.
- `api/order`: freezing and rebinding carry `Actions`.
- `api`: watch create, rename and enable refused for a catalogue-owned name; publish refused
  for a watched name; `catalogOwnerOf*` over actions; the existing lifecycle and
  per-position HTTP tests pass unchanged against `operations`, and once more with `actions`.
- Guards: `catalogproductform_internal_test.go` (`actions` has a control),
  `catalogeditorcolumn_internal_test.go` (`publishNeeds`), `catalogitemprops_internal_test.go`
  (MCP schema).
- e2e: the action list — pre-fill, add, remove, the two fixed rows, legacy `operations`
  opening as actions, and the body a save sends.

---

## Follow-up to slice A — the name-correlated publish — ✅ landed

Found while landing slice A and reproduced: `POST /api/v1/messages` with a per-operation
product's message — `laptop.provision` — starts the lifecycle process outside the order,
because `handlePublishMessage` refuses only what a per-position product *delivers*
(`catalogOwnerOfDelivered`). It predates ADR-0429 and contradicts ADR-0425 §8. The fix is the
watch's door on this route: refuse any name `catalogOwnerOfName` finds, with the same 409.
One small pull request of its own, before slice B makes the action act the sanctioned path.

## Slice B — The action act, availability and the portal — ✅ landed

Built as planned below, with these decisions recorded in ADR-0429 §2's as-built note:
the provision and the return keep their own routes and are refused by the act; the
return follows the deprovision action's triggers (the recipient may give back); the act
refuses caller variables under the names it seeds; a directed delivery to a catch that
lost an event-based gateway's race now answers "not waiting" (it answered "delivered").
**MCP (decided 2026-10-01):** `atlas_order_line_actions` reads the availability and
`atlas_ask_order_line_action` asks for an `operator` or `system` action; the act's optional
`trigger` field is what the server checks, so a customer's action cannot be asked by an
agent.
**Not in this slice:** the Console's surface for operator actions, because the Console has
no order view to put it in — operators use the route (and, for reading, the MCP tool
`atlas_order_line_actions`) until one exists.

- `POST /api/v1/orders/{id}/lines/{item}/actions/{action}` (`commandId` required) and
  `GET …/actions` (availability, read off the loop), per ADR-0429 §2; `/change` stays as the
  action keyed `change`.
- **Who:** `customer` = orderer, recipient (§10, decision 4) or an operator; `operator` =
  `RoleOperator`.
- Portal (`shop.js`): a button per available customer action on a held position, with its
  label and its form; Console: operator actions on a position.
- Action forms rendered like the configuration form (ADR-0358).

## Slice C — The outcome fact and the shop send task

Cut into three pull requests, because each stands on its own and the first is an engine
change the other two build on:

- **C1 — the outcome fact and the REST route — ✅ landed.** `VTActionOutcome`, idempotent
  per order, position and command id; written in one command with the grant or revocation
  it accompanies; `commandId` seeded by every act and recorded on the line; the outcome
  route and the read route (HTTP and MCP). See ADR-0429 §3's as-built note.
- **C2 — the shop send task, mode `outcome` — ✅ landed.** A send-task kind `shop` compiled
  to the engine-only job type `io.atlas.shop`. Its handler runs off the loop, so it does
  not write the outcome itself: it hands it back on the job's completion, and the engine
  appends it in the command that completes the job, as a decision's history is. Publish
  check: every change or service action's `completed` is reported by such a task, and no
  task answers an action the product does not declare. See ADR-0429 §4's as-built note.
- **C3 — mode `command`** with the product's allow-list, limited to `operator`/`system`
  actions (§10, decision 1).

- `VTActionOutcome` / `IntentActionCompleted` (engine), written beside a grant or
  revocation in the same batch; outcome route for REST reporters.
- Send-task kind `shop`, mode `outcome`, compiled to an engine-only job type added to
  `engineOnlyJobTypes`; publish check that every action's `completed` outcome is reported.
- Mode `command` with the product's allow-list of process applications, limited to
  `operator` / `system` actions (§10, decision 1).

## Slice D — The modeler

- `sourceKind` on `GET /api/v1/message-sources`: `product-action` and `process` beside
  `inbound-watch`; the picker groups by source.
- Shop badges beside the envelope on shop receive and send tasks (§6).

## Slice E — The feed

- Feed column family folded from outcomes and entitlement events, pruned by an explicit
  event (30 days default, §10 decision 8); `GET /api/v1/events?after=` with CloudEvents 1.0
  envelopes; 410 for an expired cursor; push via a Worker prepared, not built.

## Not in any slice yet

- Triggering an operator action for every position of a product (§10, decision 3).
- A suspended entitlement state (§10, decision 2).
- The run-loop measurement (§10, decision 9) — before a per-position pilot is widened.
