# Atlas + Postman — onboarding kit

Everything a person with **Postman** and access to a running **Atlas** server needs
to go from zero to driving real workflows in about five minutes.

This folder contains:

| File | What it is |
|------|-----------|
| [`Atlas.postman_collection.json`](Atlas.postman_collection.json) | The full API collection (49 requests, every `/api/v1` endpoint + `/healthz` + `/mcp`), organized by resource, with auto-chaining variables and test assertions. |
| [`Atlas.postman_environment.json`](Atlas.postman_environment.json) | The `Atlas (local)` environment. One variable: `baseUrl` → `http://localhost:8080`. |
| [`order-approval.bpmn`](order-approval.bpmn) | The sample model the Golden Path deploys — a two-step human approval that parks on user tasks, so you can watch the whole lifecycle. |

Every request in the collection was verified against a live Atlas server, so what
you import matches what the server actually returns.

> **Coming alongside this: a built-in Swagger UI / OpenAPI spec** (in progress on a
> separate branch). The two are complementary, not competing — see
> [_Swagger UI / OpenAPI_](#relationship-to-swagger-ui--openapi) below.

---

## 1. Start a server

Atlas is a single self-contained binary. From the repo root:

```bash
go run ./cmd/atlas serve --addr :8080 --data-dir ./atlas-data
```

- REST API: `http://localhost:8080/api/v1`
- Web UI (bpmn.io viewer/editor): `http://localhost:8080/`
- MCP (for AI agents): `http://localhost:8080/mcp`
- Health: `http://localhost:8080/healthz`

Deployments and instances are durable (an on-disk sidecar under `--data-dir`), so
they survive a restart.

## 2. Import into Postman

1. **Import** both JSON files (drag them onto Postman, or **Import → Files**).
2. Top-right environment selector → choose **`Atlas (local)`**.
3. If your server isn't on `localhost:8080`, edit the environment's `baseUrl`.

## 3. Take the tour

Open the **🚀 Golden Path (run top-to-bottom)** folder and hit **Send** on each
request in order — or select the folder and use the **Collection Runner**. It walks:

```
Deploy model → List processes → Start instance → List tasks
   → Complete task → List instances → Engine stats
```

You don't copy any ids around: each step's **Tests** tab captures the ids it
produced (`defKey`, `taskKey`, …) into collection variables, and the next request
references them as `{{defKey}}`, `{{taskKey}}`. The requests also assert status
codes and shapes, so a green run means the server behaved.

### What you'll see

- **Deploy** returns the assigned definition `key` (e.g. `1`), `processId`
  (`order-approval`), and `version`.
- **Start instance** runs the engine to idle; the model parks on the first user
  task, so `activeProcessInstances` becomes `1`.
- **List tasks** shows the open *Review order* task with its `candidateGroups`.
- **Complete task** submits `{"variables": {...}}`; the instance advances to
  *Approve order*.
- **List instances** shows the instance still active on the second task, carrying
  both the start variables and the ones you submitted — proof the data landed.

## 4. Beyond the tour

The reference folders cover the complete surface, grouped by resource:

- **Health & Info** — `/healthz`, `/api/v1/info`, `/api/v1/stats`
- **FEEL Playground** — validate/evaluate FEEL expressions with the same engine
  deployment uses (great for authoring gateway conditions before deploying)
- **Deployments & Processes** — deploy, list, fetch XML, live runtime overlay,
  collaboration runtime, delete
- **Instances** — start, list (with variables), cancel
- **User Tasks** — list, claim, unclaim, complete
- **Messages** — correlate a message into waiting instances
- **Projects, Drafts, Forms & DMN** — the Modeler's artifacts, exposed for
  automation
- **MCP** — poke the JSON-RPC transport (`initialize`, `tools/list`,
  `tools/call`) the AI-agent connector uses

## Relationship to Swagger UI / OpenAPI

Atlas is also getting a built-in **Swagger UI** backed by an **OpenAPI** spec
(built on a separate branch). They serve different moments, and Postman works well
with both:

| | Swagger UI / OpenAPI | This Postman collection |
|---|---|---|
| **Best for** | Browsing the surface, exact per-endpoint schemas, try-it-in-the-page | A guided, runnable lifecycle you step through and reuse |
| **Golden Path** | — | Auto-chains ids across steps (`{{defKey}}` → `{{taskKey}}`), so a whole workflow runs top-to-bottom |
| **Assertions** | — | Each request tests status/shape, so a green run means the server behaved |
| **Automation** | Generate clients from the spec | Collection Runner / Newman in CI |

When the OpenAPI spec is available, you can also **import it straight into
Postman** (*Import → Link* the spec URL, e.g. `.../openapi.json`) to get an
always-in-sync, generated collection. Use that generated collection as the
exhaustive schema reference, and keep this hand-curated one as the opinionated
onboarding path — the golden-path chaining and tests are the parts a raw
spec-to-Postman import can't give you.

Once the spec lands, this kit will link to it here so newcomers can pick whichever
entry point suits them.

## Authentication

The Atlas server is **unauthenticated by design** — it expects to sit behind a
reverse proxy that adds auth (the `/mcp` endpoint especially). Out of the box no
credentials are needed. If your deployment fronts it with auth, add the token or
header once under the **collection's Authorization tab** and it applies to every
request.

## Conventions worth knowing

- **Deploy** bodies are raw **BPMN XML** (`Content-Type: application/xml`). The
  body is the whole model; a collaboration deploys one definition per pool.
- Everything else is **JSON**. Process/task variables use the shape
  `{"variables": {name: value}}` — scalars, objects, and arrays are all accepted
  and bind into FEEL as values, contexts, and lists. Numbers keep their exact
  decimal text.
- **Keys** are engine-assigned `uint64`s returned on deploy/list. Instance and job
  (task) keys are large numbers (they encode the partition in their high bits) —
  that's expected.

## curl equivalent (if you want the shell version)

```bash
BASE=http://localhost:8080

# Deploy
KEY=$(curl -s -X POST $BASE/api/v1/deployments \
  -H 'Content-Type: application/xml' \
  --data-binary @order-approval.bpmn | python3 -c 'import sys,json;print(json.load(sys.stdin)["key"])')

# Start an instance
curl -s -X POST $BASE/api/v1/processes/$KEY/instances \
  -H 'Content-Type: application/json' \
  -d '{"variables":{"orderId":"A-1001","amount":4200}}'

# Work the task
TASK=$(curl -s $BASE/api/v1/tasks | python3 -c 'import sys,json;print(json.load(sys.stdin)[0]["key"])')
curl -s -X POST $BASE/api/v1/tasks/$TASK/complete \
  -H 'Content-Type: application/json' \
  -d '{"variables":{"approved":true,"score":7}}'

# See the result
curl -s $BASE/api/v1/instances
```

## Troubleshooting

- **Connection refused** — the server isn't running, or `baseUrl` points elsewhere.
  Start it (step 1) and check `GET /healthz` returns `ok`.
- **404 on `{{defKey}}` requests** — run **Deploy** first (or the Golden Path) so
  `defKey` is populated; a variable that never got set sends the literal
  `{{defKey}}`.
- **Empty task list** — the instance already finished, or you completed both tasks.
  Start a fresh instance.
- **`/mcp` returns 406** — the `Accept` header must allow both `application/json`
  and `text/event-stream` (the MCP requests already set this).
