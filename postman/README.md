# Atlas + Postman — onboarding kit

Everything a person with **Postman** and access to a running **Atlas** server needs
to go from zero to driving real workflows in about five minutes — and a documented,
runnable reference for the endpoints you use most.

| File | What it is |
|------|-----------|
| [`Atlas.postman_collection.json`](Atlas.postman_collection.json) | The collection: twelve folders, organised by resource and ordered so that the whole collection runs top to bottom. Every request documents the role it needs, its body and its answers, asserts the status and shape of the response, and carries saved example responses — including the common errors. |
| [`Atlas.postman_environment.json`](Atlas.postman_environment.json) | The `Atlas (local)` environment: `baseUrl`, `username`, and the secrets `password` and `apiToken` (empty). |
| [`order-approval.bpmn`](order-approval.bpmn) | The model the Golden Path deploys — a two-step human approval that parks on user tasks, so you can watch the whole lifecycle. |
| [`payment-wait.bpmn`](payment-wait.bpmn) | The model the Messages folder deploys — an instance that waits for a `payment-received` message correlated by its `orderId`. |

The whole collection runs green, top to bottom, against a fresh server —
`make postman-smoke` does exactly that — and its saved example responses were
recorded from such a run.

> **This collection is curated, not exhaustive.** It covers the endpoints a newcomer
> and an integrator need first. The complete `/api/v1` surface is described by the
> **OpenAPI** document at `/api/v1/openapi.json` and browsable in the built-in
> explorer at `/api/docs` — see [_OpenAPI and the API explorer_](#openapi-and-the-api-explorer).

---

## 1. Start a server

Atlas is a single self-contained binary. From the repository root:

```bash
ATLAS_ADMIN_PASSWORD='choose-a-password' \
  go run ./cmd/atlas serve --addr :8080 --data-dir ./atlas-data
```

- **A login is required by default** ([ADR-0195](../docs/adr/0195-auth-on-by-default.md)).
  On the first start with an empty data directory Atlas seeds one administrator:
  username `ATLAS_ADMIN_USERNAME` (default `admin`) and password
  `ATLAS_ADMIN_PASSWORD`. Leave the password unset and Atlas generates one and
  writes it to the startup log **once** (event `auth.admin_seeded`).
- `--auth=false` runs the server open, with a warning at startup. That is for
  laptops and demos only; the collection works against it too.

| URL | What |
|---|---|
| `http://localhost:8080/api/v1` | REST API |
| `http://localhost:8080/` | Web UI (Console, Modeler) |
| `http://localhost:8080/mcp` | MCP transport for AI agents |
| `http://localhost:8080/healthz`, `/readyz` | Liveness and readiness probes |
| `http://localhost:8080/api/docs` | API explorer (after login) |

Deployments and instances are durable, so they survive a restart.

## 2. Import into Postman

1. **Import** both JSON files (drag them onto Postman, or **Import → Files**).
2. Select the **`Atlas (local)`** environment (top right).
3. Set the environment's variables:
   - `baseUrl` — if your server is not on `http://localhost:8080`;
   - `username` — `admin` unless you chose another name;
   - `password` — as the **current value** only. It is typed *secret*, and a
     current value is neither synced to Postman's cloud nor exported with the
     environment.

## 3. Sign in

Run **Authentication → Check whether a login is required**, then **Log in**. The
server answers with an `atlas_session` cookie (HttpOnly, 12 hours); Postman's
cookie jar attaches it to every later request to the same host, so nothing else
needs configuring.

For machines — Newman, CI, scripts — use an **API token** instead: mint one with
**Authentication → Mint an API token** (admin) or in the Console, and put the secret
into the `apiToken` environment variable. The collection's pre-request script then
sends `Authorization: Bearer <token>` on every request, and *Log in* skips itself.
See [_Authentication_](#authentication) for the details.

## 4. Take the tour

Open **🚀 Golden Path (run top-to-bottom)** and send each request in order — or select
the folder and use the **Collection Runner**:

```
Deploy model → List processes → Start instance → List the instance's tasks
  → Complete the task → List instances → Read variables → Engine stats
```

You never copy an id: each step's **Tests** tab stores what it produced (`defKey`,
`instanceKey`, `taskKey`) in collection variables, and the next request uses them.
The same scripts assert the status and the shape of every answer, so a green run
means the server behaved.

What you will see:

- **Deploy** returns the definition `key`, `processId` (`order-approval`) and
  `version`. On a fresh server the key is not `1`: Atlas' own system processes are
  deployed first.
- **Start instance** runs the engine until idle; the model parks on *Review order*.
- **List the instance's tasks** uses `?processInstance=` so it finds exactly that
  instance's task, however busy the server is.
- **Complete the task** submits `{"variables": {...}}`; the instance moves on to
  *Approve order*.
- **Read variables** shows the start variables and the submitted form data, merged
  into one object — proof the data landed.

## 5. Beyond the tour

The folders are ordered so that the **whole collection runs in the Collection
Runner** without editing anything. Each folder's description says what it needs.

| Folder | What it shows |
|---|---|
| **Authentication** | Whether a login is required, *Log in* (cookie), *Who am I*, and minting, listing and revoking an API token. |
| **🚀 Golden Path** | The lifecycle tour above. |
| **Health & Info** | `/healthz`, `/readyz`, server info, engine stats, the OpenAPI document. |
| **FEEL Playground** | Validate and evaluate FEEL expressions with the engine's own FEEL — for authoring conditions before you deploy. |
| **User Tasks** | List, get, claim, release and complete — it finishes the Golden Path instance's second task. |
| **Messages** | Message correlation end to end: deploy `payment-wait`, start an instance that waits, publish the message, confirm the instance finished. |
| **Instances** | Start, list with filters, read and correct variables (admin), cancel. |
| **Incidents** | The unresolved-incident listing and its filters. |
| **Deployments & Processes** | Deploy, list, fetch XML, runtime overlays, delete. |
| **Applications, Drafts, Forms & DMN** | The Modeler's artifacts: create an application, save and file a draft, a form and a DMN reference, validate and deploy the application, and clean everything up again. |
| **MCP (AI agents)** | The JSON-RPC transport: `initialize`, `tools/list`, `tools/call`. |
| **Sign out (run last)** | *Log out*, last so a full run keeps its session. |

A full run deliberately **leaves two things behind** so you can look at them in the
Console afterwards: the Golden Path's `order-approval` deployment with its finished
and cancelled instances, and the `payment-wait` deployment with its finished
instance. Everything else it creates — a second `order-approval` version, the
sample application, its draft, form, DMN reference and deployed definition, and the
API token — it deletes again. Its sample ids (`postman-draft-sample`,
`postman-review-form`, a fresh `PM-<timestamp>` order id for the message) are
chosen so that a run cannot overwrite or correlate into somebody else's work. Still:
point it at a development or test server, not at production.

## 6. Run it from the command line

[Newman](https://www.npmjs.com/package/newman) runs the collection without Postman:

```bash
npx newman run postman/Atlas.postman_collection.json \
  -e postman/Atlas.postman_environment.json \
  --env-var baseUrl=http://localhost:8080 \
  --env-var username=admin \
  --env-var password="$ATLAS_ADMIN_PASSWORD"
```

or, with an API token instead of a password (the admin-only token requests then
skip themselves, and *Set instance variables* skips its assertions):

```bash
npx newman run postman/Atlas.postman_collection.json \
  -e postman/Atlas.postman_environment.json \
  --env-var baseUrl=http://localhost:8080 \
  --env-var apiToken="$ATLAS_API_TOKEN"
```

From a checkout, **`make postman-smoke`** ([`scripts/postman-smoke.sh`](../scripts/postman-smoke.sh))
builds Atlas from the tree, starts it on an empty data directory with a generated
admin password, runs the whole collection with Newman and stops the server again.
It needs Go and Node.js; Newman is fetched by `npx`.

## OpenAPI and the API explorer

Atlas serves an **OpenAPI 3.1** document at **`/api/v1/openapi.json`** and the
**Scalar** API explorer at **`/api/docs`** ([ADR-0043](../docs/adr/0043-openapi-spec-and-embedded-api-explorer.md)).
Both are generated from the same route table the server mounts, so they cannot
drift from what is served. Both require a login (ADR-0195), and both are off when
the server runs with `--docs=false`.

| | OpenAPI / API explorer | This collection |
|---|---|---|
| **Scope** | Every `/api/v1` endpoint | The endpoints you need first |
| **Best for** | Exact per-endpoint schemas, generating clients | A guided, runnable lifecycle you step through and reuse |
| **Chaining** | — | Ids flow from step to step (`{{defKey}}` → `{{instanceKey}}` → `{{taskKey}}`) |
| **Assertions** | — | Every request tests status and shape |
| **Examples** | — | Saved responses, including errors |
| **Automation** | Client generators | Collection Runner, Newman, `make postman-smoke` |

To get a generated collection of **every** endpoint, save the document as a file —
send **Health & Info → OpenAPI document** and use *Save response → Save to a file*,
or download it with curl and your session or token — and import that file
(*Import → Files*). Importing by link does not work, because the document is
behind the login. Use the generated collection as the exhaustive reference, and
this one as the onboarding path.

## Authentication

Every request accepts either credential:

| | Session cookie | API token |
|---|---|---|
| **For** | People | Machines: Newman, CI, workers, a stdio MCP adapter |
| **Obtain** | `POST /api/v1/auth/login` with `{"username", "password"}` | `POST /api/v1/api-tokens` (admin) or the Console; the secret is shown **once** |
| **Send** | Cookie `atlas_session` (Postman's cookie jar does it) | Header `Authorization: Bearer <token>` (the collection does it when `apiToken` is set) |
| **Lifetime** | 12 hours | `expiresInDays`, or never (`0`) |
| **Roles** | The user's roles | The minter's roles **without admin** |

**Reachable without a credential:** `POST /api/v1/auth/login`, `GET /api/v1/info`,
`GET /api/v1/auth/providers`, the login screen's settings
(`/api/v1/settings/theme`, `/logo`, `/registration`), `/healthz` and `/readyz`.
Everything else answers `401 {"error":"authentication required"}` with a
`WWW-Authenticate: Bearer` header that points at the server's OAuth
protected-resource metadata ([ADR-0200](../docs/adr/0200-mcp-oauth-resource-server.md)).
`curl -u user:pass` (HTTP Basic) is not a credential Atlas reads.

**Roles.** The request descriptions name the role each request needs: *user*
(task inbox), *operator* (instances, messages, incidents), *modeler* (deploy,
drafts, forms, FEEL playground) or *admin* (API tokens, setting instance
variables). Signed in but without the role, a request answers **403**.

**Gotchas**

- **Still `401` after Log in?** Open the **Cookies** manager (under the address
  bar) and check that `atlas_session` is listed for your host. If *Log in* itself
  answered `401 {"error":"invalid credentials"}`, check `username` and the
  *current* value of `password`.
- **Five wrong passwords** for one account throttle further attempts for that
  account until its budget refills (up to 15 minutes).
- **The Postman Vault** works too: replace `{{password}}` in *Log in*'s body with
  `{{vault:password}}`. Newman has no Vault; it treats `vault:password` as an
  ordinary variable name, so a command-line run then needs
  `--env-var 'vault:password=…'`.
- **Behind an authenticating reverse proxy** that wants HTTP Basic? Set the
  collection's **Authorization** tab to *Basic Auth*; Atlas' own login still
  applies behind it.

## Conventions

- **Deploy** and **draft** bodies are raw **BPMN XML** (`Content-Type: application/xml`).
  The body is the whole model; a collaboration deploys one definition per pool.
- Everything else is **JSON**. Variables use `{"variables": {name: value}}` —
  scalars, objects and arrays bind into FEEL as values, contexts and lists, and
  numbers keep their exact decimal text.
- **Keys** are engine-assigned `uint64` numbers. Instance and task keys are large
  because they encode the partition in their high bits.
- **Capped listings** — `/tasks`, `/instances`, `/incidents`, `/audit`,
  `/approvals` — answer a page `{items, total, totalExact, truncated, nextCursor}`,
  never a bare array. Read the rows from `items`; `total` is exact only when
  `totalExact` is true; pass `nextCursor` as `?before=` for the next page.
- **Errors** answer `{"error": "…"}` with a 4xx or 5xx status.
- **Applications** replaced projects (ADR-0128). `/api/v1/projects` still answers
  as a deprecated alias; use `/api/v1/applications`. Artifacts still name their
  application in a field called `projectId`.

## curl equivalent

```bash
BASE=http://localhost:8080
JAR=$(mktemp)

# Sign in; the cookie lands in $JAR
curl -s -c "$JAR" -X POST $BASE/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d "{\"username\":\"admin\",\"password\":\"$ATLAS_ADMIN_PASSWORD\"}"

# Deploy
KEY=$(curl -s -b "$JAR" -X POST $BASE/api/v1/deployments \
  -H 'Content-Type: application/xml' \
  --data-binary @postman/order-approval.bpmn | python3 -c 'import sys,json;print(json.load(sys.stdin)["key"])')

# Start an instance
INSTANCE=$(curl -s -b "$JAR" -X POST $BASE/api/v1/processes/$KEY/instances \
  -H 'Content-Type: application/json' \
  -d '{"variables":{"orderId":"A-1001","amount":4200}}' | python3 -c 'import sys,json;print(json.load(sys.stdin)["instanceKey"])')

# Work the task. The listing answers {items, total, totalExact, truncated, nextCursor} —
# the rows are under "items", because a bare array cannot say it is only a page.
TASK=$(curl -s -b "$JAR" "$BASE/api/v1/tasks?processInstance=$INSTANCE" | python3 -c 'import sys,json;print(json.load(sys.stdin)["items"][0]["key"])')
curl -s -b "$JAR" -X POST $BASE/api/v1/tasks/$TASK/complete \
  -H 'Content-Type: application/json' \
  -d '{"variables":{"approved":true,"score":7}}'

# See the result
curl -s -b "$JAR" $BASE/api/v1/instances/$INSTANCE/variables
```

With an API token, replace `-c "$JAR"` / `-b "$JAR"` by
`-H "Authorization: Bearer $ATLAS_API_TOKEN"` and skip the login.

## Maintaining the collection

The collection is checked in three ways:

- **`go test ./api`** (part of the mandatory sweep) reads the collection without a
  server: every request must resolve to a route the server mounts and not to a
  deprecated alias; every folder and request must have a description, every
  request a `pm.test` assertion and a saved example; the models deployed inline
  must be byte-identical to the `.bpmn` files here; and capped listings must be read
  through `items` ([`api/postman_internal_test.go`](../api/postman_internal_test.go),
  [`api/pagecount_internal_test.go`](../api/pagecount_internal_test.go)).
- **`go test ./examples`** compiles both sample models and checks their extension
  namespaces, like every model Atlas ships.
- **`make postman-smoke`** runs the whole collection against a live server.

When you add or change a request: write its description (role, body, answers),
give it a `pm.test` for its status and shape, run it once and save the response as
an example (*Save Response → Save as example*), and run `make postman-smoke`.
Edit the `.bpmn` files and the deploy bodies together.

## Troubleshooting

- **Connection refused** — the server is not running, or `baseUrl` points
  elsewhere. `GET {{baseUrl}}/healthz` must answer `ok`.
- **`401 authentication required`** — not signed in: run *Log in*, or set
  `apiToken`. The Postman console prints a hint on every 401.
- **`403`** — signed in, but without the role the request needs (see its
  description). An API token never carries the admin role.
- **`404` on a `{{defKey}}` / `{{instanceKey}}` request** — the variable is empty or
  stale: run the request that sets it first (the Golden Path, or the folder's
  first request). An empty variable leaves a gap in the path, such as
  `/processes//instances`.
- **Empty task list** — the instance has already finished, or the listing is
  scoped to another instance. Start a fresh one.
- **A published message changed nothing** — messages are not buffered: one that no
  instance waits for at that moment is accepted with 200 and dropped. Start the
  instance first, and check the correlation key.
- **`/mcp` answers 406** — the `Accept` header must allow both `application/json`
  and `text/event-stream` (the MCP requests set it).
