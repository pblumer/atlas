import { test, expect } from "@playwright/test";

// The derived landscape mesh (ADR-0211). These tests drive the real view against a
// mocked mesh payload, so they cover what a browser actually renders: the graph, the
// drilldown link, and — the part that matters most — that a filtered or collapsed
// picture says so in words rather than merely looking smaller.

const graph = {
  nodes: [
    { id: "application:a1", kind: "application", name: "Billing", provenance: "derived" },
    { id: "process:1", kind: "process", name: "Invoice", provenance: "derived", application: "application:a1", processId: "invoice", version: 2 },
    { id: "process:2", kind: "process", name: "Dunning", provenance: "derived", application: "application:a1", processId: "dunning", version: 1 },
    { id: "restricted:1", kind: "restricted", provenance: "derived" },
    { id: "unresolved:process:archive", kind: "unresolved", name: "archive", provenance: "derived" },
    { id: "worker:c1", kind: "worker", name: "ops-mail", provenance: "derived", workerType: "mail" },
    { id: "decision:credit", kind: "decision", name: "Credit score", provenance: "derived" },
  ],
  edges: [
    { from: "application:a1", to: "process:1", kind: "contains" },
    { from: "application:a1", to: "process:2", kind: "contains" },
    { from: "process:1", to: "process:2", kind: "calls" },
    { from: "process:1", to: "restricted:1", kind: "calls" },
    { from: "process:2", to: "unresolved:process:archive", kind: "calls" },
    { from: "process:1", to: "worker:c1", kind: "uses" },
    { from: "process:2", to: "decision:credit", kind: "uses" },
  ],
  restricted: 1,
  clustered: false,
};

// The notation mapping is served by the server (ADR-0211 §8), so the mock has to
// serve it too: it is the same table the ArchiMate export writes from, and a mock
// that invented its own would be testing a picture no server produces.
const notations = [
  { id: "atlas", label: "Atlas (derived)", short: "Atlas", projection: false, mappingVersion: 1, types: {}, loss: [] },
  {
    id: "archimate-3.2", label: "ArchiMate 3.2", short: "ArchiMate",
    projection: true, mappingVersion: 1,
    types: {
      application: { name: "Application Component", type: "ApplicationComponent" },
      process: { name: "Application Process", type: "ApplicationProcess" },
      worker: { name: "Application Service", type: "ApplicationService" },
      decision: { name: "Application Function", type: "ApplicationFunction" },
      target: { name: "Node", type: "Node" },
    },
    loss: [
      "Nothing here was modelled.",
      "Relationships are derived from two facts. ArchiMate tells serving from triggering from assignment.",
      "Restricted placeholders have no ArchiMate element.",
    ],
  },
  {
    id: "c4-projection", label: "C4 (projection)", short: "C4",
    projection: true, mappingVersion: 1,
    types: {
      application: { name: "Container" }, process: { name: "Component" },
      worker: { name: "Component" }, decision: { name: "Component" },
      target: { name: "Deployment Node" },
    },
    loss: [
      "C4 separates its levels onto different diagrams.",
      "External systems are absent. Atlas holds no model of what is behind a worker.",
    ],
  },
];

function installMock(page, mesh = graph) {
  page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path === "/api/v1/panorama/mesh") return route.fulfill({ json: mesh });
    if (path === "/api/v1/panorama/notations") return route.fulfill({ json: notations });
    return route.fulfill({ json: [] });
  });
}

// The drafts switch is the one control that re-asks the server, so a test about it
// needs a mock that can tell the two questions apart. Same landscape either way, plus
// one saved-but-undeployed diagram when it is asked for — which is exactly what the
// server does with ?drafts=1.
const withDrafts = {
  ...graph,
  nodes: [...graph.nodes,
    { id: "draft:refund", kind: "draft", name: "Refund", provenance: "derived", application: "application:a1", processId: "refund", state: "unbound", severity: "unknown" }],
  edges: [...graph.edges, { from: "application:a1", to: "draft:refund", kind: "contains" }],
};

// deployed is what the server answers when nothing is deployed at all: the landscape
// is empty until the drafts are asked for. It is the cold start a new instance has,
// where every diagram anybody has drawn is still a draft.
function installDraftMock(page, { deployed = graph, drafted = withDrafts, failDrafts = false } = {}) {
  page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (url.pathname === "/api/v1/panorama/mesh") {
      if (url.searchParams.get("drafts") !== "1") return route.fulfill({ json: deployed });
      if (failDrafts) return route.fulfill({ status: 500, json: { error: "no" } });
      return route.fulfill({ json: drafted });
    }
    if (url.pathname === "/api/v1/panorama/notations") return route.fulfill({ json: notations });
    return route.fulfill({ json: [] });
  });
}

// The depth control is a number field plus an "all" switch, so setting it is two
// possible gestures rather than one option to pick. One helper, so every test says
// what it wants rather than how the control is built.
// Going into a node. A double-click does it for every kind whose inside is on this
// canvas; for a process the inside is its Operations view, so the drilldown there is
// the header's "→" — the control the gesture was given when the drilldown became a
// path. One helper, so a test about the drilldown says "go into this" rather than
// picking a gesture per kind.
async function drillInto(page, id) {
  await page.locator(`[data-node-id="${id}"] .mesh-body`).click();
  await page.locator("#mesh-drill-in").click();
}

async function setDepth(page, hops) {
  if (hops === "all") {
    await page.locator("#mesh-depth-any").check();
    return;
  }
  await page.locator("#mesh-depth-any").uncheck();
  await page.locator("#mesh-depth").fill(String(hops));
}

test("renders the derived landscape and drills into a process", async ({ page }) => {
  installMock(page);
  const pageErrors = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));

  await page.goto("/index.html#/panorama/starmap");

  // Every node is drawn, and the two placeholder kinds are visually distinct from
  // real ones rather than merged into them.
  await expect(page.locator(".mesh-canvas")).toBeVisible();
  await expect(page.locator(".mesh-node")).toHaveCount(7);
  await expect(page.locator(".mesh-restricted")).toHaveCount(1);
  await expect(page.locator(".mesh-unresolved")).toHaveCount(1);
  await expect(page.locator(".mesh-edges line")).toHaveCount(7);
  await expect(page.locator(".mesh-canvas")).toContainText("Billing");
  await expect(page.locator(".mesh-canvas")).toContainText("Invoice");

  // Nothing here was drawn by a person, and the view says so — that is the
  // provenance rule, and in this slice everything is derived.
  await expect(page.locator(".mesh-meta")).toContainText("derived");

  // Panorama owns the landscape altitude and links into the existing Operations
  // view for a process, rather than rendering a second BPMN canvas. Since impact
  // analysis landed, a node click selects rather than navigates — a node cannot do
  // both — so the drilldown lives in the selection panel.
  await page.locator('[data-node-id="process:1"]').click();
  await page.getByRole("link", { name: "Open in Operations" }).click();
  await expect(page).toHaveURL(/#\/operations\/p\/1$/);

  expect(pageErrors).toEqual([]);
});

test("says in words that the picture is filtered", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/starmap");

  // The count is the point: a viewer must be able to tell a complete landscape
  // from one their access has cut down, without inferring it from an absence.
  await expect(page.locator(".mesh-note").first()).toContainText("1");
  await expect(page.locator(".mesh-note").first()).toContainText("access");

  // A restricted placeholder must not carry the hidden resource's identity.
  await expect(page.locator(".mesh-restricted")).not.toContainText("Dunning");
  const label = await page.locator(".mesh-restricted .mesh-label").textContent();
  expect(label?.trim() ?? "").toBe("");
});

test("repeats the server's collapse instead of hiding it", async ({ page }) => {
  installMock(page, {
    nodes: [{ id: "application:a1", kind: "application", name: "Billing", provenance: "derived", children: 42 }],
    edges: [],
    restricted: 0,
    clustered: true,
  });
  await page.goto("/index.html#/panorama/starmap");

  await expect(page.locator(".mesh-note").first()).toContainText("size budget");
  await expect(page.locator(".mesh-canvas")).toContainText("42");
});

test("an empty landscape explains itself rather than showing a blank page", async ({ page }) => {
  installMock(page, { nodes: [], edges: [], restricted: 0, clustered: false });
  await page.goto("/index.html#/panorama/starmap");

  await expect(page.locator(".card.empty")).toContainText("derived");
  await expect(page.locator(".card.empty")).toContainText("nothing to model first");
});

// The size budget is an acceptance criterion, not a hope (ADR-0211 §7): the mesh
// must actually paint at the size the server is willing to send. The ceiling here is
// deliberately loose — measured cost at this size is around a second, and CI runners
// are slower than a developer's machine. It is here to catch a change that makes the
// layout worse than quadratic, which would blow straight through it, not to police
// a few hundred milliseconds.
test("stays inside its size budget", async ({ page }) => {
  const nodes = [], edges = [];
  for (let a = 0; a < 20; a++) {
    nodes.push({ id: `application:a${a}`, kind: "application", name: `App ${a}`, provenance: "derived" });
    for (let p = 0; p < 19; p++) {
      const id = `process:${a}_${p}`;
      nodes.push({ id, kind: "process", name: `P${a}-${p}`, provenance: "derived", application: `application:a${a}`, processId: `p${a}_${p}`, version: 1 });
      edges.push({ from: `application:a${a}`, to: id, kind: "contains" });
      if (p > 0) edges.push({ from: `process:${a}_${p - 1}`, to: id, kind: "calls" });
    }
  }
  expect(nodes.length).toBe(400); // the budget in api/panoramamesh.go

  installMock(page, { nodes, edges, restricted: 0, clustered: false });
  const started = Date.now();
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toBeVisible({ timeout: 30000 });
  await expect(page.locator(".mesh-node")).toHaveCount(400);
  // The ranking walks from every node, so it is inside this measurement rather than
  // beside it: if O(N·E) at the budget were a problem, this is where it would show.
  await expect(page.locator(".mesh-rank-go").first()).toBeVisible();
  expect(Date.now() - started).toBeLessThan(15000);
});

// Search is what makes a few hundred nodes navigable at all — without it the view
// is a picture rather than a tool. It matches on name, kind and BPMN process id,
// and it reports how much it hid: a filtered mesh looks exactly like a small one.
test("filters the landscape and says how much it is hiding", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-node")).toHaveCount(7);

  await page.getByLabel("Filter the starmap").fill("invoice");
  // One match, and the header says so — separately from the context around it.
  await expect(page.locator(".mesh-node:not(.mesh-context)")).toHaveCount(1);
  await expect(page.locator("#mesh-count")).toContainText("1 of 7 node(s) match");
  await expect(page.locator(".mesh-canvas")).toContainText("Invoice");

  // Filtering by kind is the other half: "what does this instance talk to".
  await page.getByLabel("Filter the starmap").fill("worker");
  await expect(page.locator(".mesh-worker:not(.mesh-context)")).toHaveCount(1);
  await expect(page.locator(".mesh-canvas")).toContainText("ops-mail");

  await page.getByLabel("Filter the starmap").fill("nothing-matches-this");
  await expect(page.locator(".mesh-empty-filter")).toContainText("Nothing matches");

  await page.getByLabel("Filter the starmap").fill("");
  await expect(page.locator(".mesh-node")).toHaveCount(7);
  await expect(page.locator("#mesh-count")).toContainText("7 node(s)");
});

// A match on its own is a circle in an empty field: it answers "does this exist" and
// nothing else, when the question somebody types a name to ask is nearly always "and
// what is it attached to". So the filter keeps one hop around every match — and says
// which of what is on screen is a result and which is only there to explain it.
test("a filtered node keeps the things it is attached to", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/starmap");

  await page.getByLabel("Filter the starmap").fill("invoice");

  // Invoice calls Dunning and a restricted node, uses the mail worker, and sits in
  // Billing. All of them are on screen; none of them matched "invoice".
  const match = page.locator('[data-node-id="process:1"]');
  await expect(match).toHaveCount(1);
  await expect(match).not.toHaveClass(/mesh-context/);
  for (const id of ["process:2", "worker:c1", "application:a1", "restricted:1"]) {
    await expect(page.locator(`[data-node-id="${id}"]`)).toHaveClass(/mesh-context/);
  }
  // And it stops at one hop: the decision only Dunning uses is two away.
  await expect(page.locator('[data-node-id="decision:credit"]')).toHaveCount(0);

  // The context is drawn as context rather than as a result — a search that
  // presented non-matches as matches would be a worse answer than an empty field.
  await expect(page.locator("#mesh-count")).toContainText("1 of 7 node(s) match");
  await expect(page.locator("#mesh-count")).toContainText("4 shown for context");
  const faded = await page.locator('[data-node-id="worker:c1"]').evaluate(
    (el) => Number(getComputedStyle(el).opacity));
  expect(faded).toBeGreaterThan(0);
  expect(faded).toBeLessThan(1);

  // The edges between what is left are drawn, so the attachment is visible and not
  // merely implied by two circles being on the same screen.
  const joined = await page.evaluate(() => [...document.querySelectorAll(".mesh-edge")]
    .some((e) => e.dataset.from === "process:1" && e.dataset.to === "worker:c1"));
  expect(joined).toBe(true);
});

// An unresolved node must say what kind of thing is missing: a missing deployment
// and a missing worker are fixed in different places.
test("an unresolved dependency names what kind of thing is missing", async ({ page }) => {
  installMock(page, {
    nodes: [
      { id: "process:1", kind: "process", name: "Notifier", provenance: "derived", processId: "notifier", version: 1 },
      { id: "unresolved:worker:ops-mail", kind: "unresolved", name: "ops-mail", provenance: "derived" },
    ],
    edges: [{ from: "process:1", to: "unresolved:worker:ops-mail", kind: "uses" }],
    restricted: 0,
    clustered: false,
  });
  await page.goto("/index.html#/panorama/starmap");

  await expect(page.locator(".mesh-unresolved title")).toContainText("worker");
  await expect(page.locator(".mesh-unresolved title")).toContainText("park");
});

// Impact analysis in the view (ADR-0211 §6). The traversal itself is covered case by
// case in panorama-impact.spec.mjs; these cover what the viewer actually gets.
test("selecting a node shows its blast radius and dims the rest", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/starmap");

  await expect(page.locator(".mesh-panel-empty")).toContainText("Nothing selected");

  // Dunning uses the credit decision, and Invoice calls Dunning. So at two hops the
  // decision's blast radius is both processes; at one hop, only Dunning.
  await page.locator('[data-node-id="decision:credit"]').click();
  await expect(page.locator(".mesh-impact-count")).toContainText("2");
  await expect(page.locator('[data-node-id="process:2"]')).toHaveClass(/mesh-in-impact/);
  await expect(page.locator('[data-node-id="process:1"]')).toHaveClass(/mesh-in-impact/);
  // Context stays on screen, dimmed rather than removed, so the impact set reads as
  // part of the landscape instead of as the whole of it. The application is dimmed
  // even though it contains both: containment is not a dependency.
  await expect(page.locator('[data-node-id="application:a1"]')).toHaveClass(/mesh-dimmed/);

  // Depth is the chosen depth: one hop stops at the direct dependent.
  await setDepth(page, "1");
  await expect(page.locator(".mesh-impact-count")).toContainText("1");
  await expect(page.locator('[data-node-id="process:1"]')).toHaveClass(/mesh-dimmed/);

  // Clicking the selection again clears it.
  await page.locator('[data-node-id="decision:credit"]').click();
  await expect(page.locator(".mesh-panel-empty")).toBeVisible();
});

// The honesty rule, carried from the picture into the analysis over it: a walk that
// stopped at a permission boundary must not present its count as a total.
test("an impact answer that hits a restricted node says it is a lower bound", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/starmap");

  // The controls are addressed by id rather than by label: a node's aria-label is a
  // descriptive sentence, and getByLabel matches by substring — "Show" collides with
  // "…its identity is not shown" on a restricted node.
  await page.locator('[data-node-id="process:1"]').click();
  await page.locator("#mesh-direction").selectOption("dependencies");

  await expect(page.locator(".mesh-truncated")).toContainText("Incomplete");
  await expect(page.locator(".mesh-truncated")).toContainText("lower bound");
});

// A selection the filter removes cannot stay selected: the panel would describe a
// node that is no longer on screen.
test("filtering away the selection clears it", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/starmap");

  await page.locator('[data-node-id="worker:c1"]').click();
  await expect(page.locator(".mesh-impact-count")).toBeVisible();

  // Nothing matching the worker, and it is not within a hop of what does.
  await page.getByLabel("Filter the starmap").fill("dunning");
  await expect(page.locator(".mesh-panel-empty")).toBeVisible();
});

// The model overlay (ADR-0211 §11, P2.5b): the mesh compared against the
// architecture models, in both directions.
const overlaidGraph = {
  nodes: [
    { id: "application:a1", kind: "application", name: "Billing", provenance: "both",
      modelElementId: "app-orders", modelElementType: "ApplicationComponent", modelName: "Order Service" },
    { id: "process:1", kind: "process", name: "Invoice", provenance: "derived", processId: "invoice", version: 1 },
    { id: "modeled:application:a-ghost", kind: "application", name: "Reporting", provenance: "modeled",
      modelElementId: "app-ghost", modelElementType: "ApplicationComponent", modelName: "Reporting" },
  ],
  edges: [{ from: "application:a1", to: "process:1", kind: "contains" }],
  restricted: 0, clustered: false, modeled: 1, unmodeled: 1, outOfScope: 2,
};

test("shows what is modeled, what is not, and what could not be compared", async ({ page }) => {
  installMock(page, overlaidGraph);
  await page.goto("/index.html#/panorama/starmap");

  // Provenance is on the node itself, not only in the legend.
  await expect(page.locator('[data-node-id="application:a1"]')).toHaveClass(/mesh-prov-both/);
  await expect(page.locator('[data-node-id="process:1"]')).toHaveClass(/mesh-prov-derived/);
  await expect(page.locator('[data-node-id="modeled:application:a-ghost"]')).toHaveClass(/mesh-prov-modeled/);

  const legend = page.locator(".mesh-legend");
  // Drift the drawing alone could not show.
  await expect(legend).toContainText("declared by a");
  await expect(legend).toContainText("not present here");
  // What exists and nobody wrote down.
  await expect(legend).toContainText("no");
  // Bindings at an altitude this picture does not draw are counted, not dropped —
  // calling them missing would invent drift that is not there.
  await expect(legend).toContainText("neither matched nor missing");

  // A node whose Atlas name and modeled name differ says both.
  await expect(page.locator('[data-node-id="application:a1"] title')).toContainText("Order Service");
});

// Without an overlay the legend must not imply the landscape was checked: "0
// unmodeled" would be a claim about a comparison nobody made.
test("says nothing about drift when no model was compared", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/starmap");

  const legend = page.locator(".mesh-legend");
  await expect(legend).toContainText("nothing on this");
  await expect(legend).not.toContainText("neither matched nor missing");
});

// Severity on the mesh (ADR-0211 §4). A landscape with one process parked behind an
// incident, one worker that cannot serve work at all, and one node nothing observes
// — the three answers that must stay distinguishable from each other.
const statusGraph = {
  nodes: [
    { id: "application:a1", kind: "application", name: "Billing", provenance: "derived",
      state: "not-ready", severity: "critical", reason: "This worker cannot serve work: it is disabled.",
      severityFrom: "process:2" },
    { id: "process:1", kind: "process", name: "Invoice", provenance: "derived",
      application: "application:a1", processId: "invoice", version: 1,
      state: "degraded", severity: "attention", incidents: 3,
      reason: "3 token(s) are parked behind an unresolved incident.",
      sites: [
        { elementId: "charge-card", elementType: "ServiceTask", count: 2,
          message: "POST https://payments.example/charge: 502 Bad Gateway" },
        { elementId: "notify-customer", elementType: "SendTask", count: 1,
          message: "no Worker Instance holds the mail worker" },
      ] },
    { id: "process:2", kind: "process", name: "Dunning", provenance: "derived",
      application: "application:a1", processId: "dunning", version: 1,
      state: "not-ready", severity: "critical", reason: "This worker cannot serve work: it is disabled." },
    { id: "decision:credit", kind: "decision", name: "Credit score", provenance: "derived",
      state: "unbound", severity: "unknown" },
  ],
  edges: [
    { from: "application:a1", to: "process:1", kind: "contains" },
    { from: "application:a1", to: "process:2", kind: "contains" },
  ],
  restricted: 0, clustered: false,
  status: {
    ok: 0, attention: 1, critical: 2, unknown: 1,
    unavailable: [
      { state: "unreachable", reason: "This view contacts no source outside the engine." },
      { state: "stale", reason: "Every fact here is read from this server's own state." },
    ],
  },
};

test("marks severity on the node itself, not only in the legend", async ({ page }) => {
  installMock(page, statusGraph);
  await page.goto("/index.html#/panorama/starmap");

  await expect(page.locator('[data-node-id="process:2"]')).toHaveClass(/mesh-sev-critical/);
  await expect(page.locator('[data-node-id="process:1"]')).toHaveClass(/mesh-sev-attention/);
  await expect(page.locator('[data-node-id="decision:credit"]')).toHaveClass(/mesh-sev-unknown/);

  // Colour is never the only channel: each class that is not "nothing to report"
  // carries a glyph, so the picture is readable without colour perception.
  await expect(page.locator('[data-node-id="process:2"] .mesh-badge-glyph')).toHaveText("!");
  await expect(page.locator('[data-node-id="process:1"] .mesh-badge-glyph')).toHaveText("•");

  // The state and the reason travel with the node. The three classes are a reading
  // aid for a zoomed-out picture, never a replacement for the state underneath.
  await expect(page.locator('[data-node-id="process:1"] title')).toContainText("degraded");
  await expect(page.locator('[data-node-id="process:1"] title')).toContainText("3 token(s)");
});

// The rule that keeps a red parent actionable: it says which descendant made it red.
// An unattributed finding at the top tells an operator that something is wrong
// somewhere, which is not something anybody can act on.
test("an inherited severity names the descendant it came from", async ({ page }) => {
  installMock(page, statusGraph);
  await page.goto("/index.html#/panorama/starmap");

  await expect(page.locator('[data-node-id="application:a1"] title')).toContainText("inherited from process:2");

  await page.locator('[data-node-id="application:a1"]').click();
  const panel = page.locator(".mesh-panel");
  await expect(panel).toContainText("Critical");
  await expect(panel).toContainText("not ready");
  await expect(panel).toContainText("inherited from process:2");
});

// What the picture cannot see is stated beside what it can. Without this an instance
// nothing observes renders as uniformly well, and a green view with no way to go red
// is worse than no view at all.
test("says which observation states it cannot produce", async ({ page }) => {
  installMock(page, statusGraph);
  await page.goto("/index.html#/panorama/starmap");

  const legend = page.locator(".mesh-legend");
  await expect(legend).toContainText("Not watched here");
  await expect(legend).toContainText("unreachable");
  await expect(legend).toContainText("stale");
});

// Severity is a search axis, not only a colour: typing "critical" is how an operator
// gets from a few hundred nodes to the handful that are broken.
test("filters the landscape by severity", async ({ page }) => {
  installMock(page, statusGraph);
  await page.goto("/index.html#/panorama/starmap");

  await page.locator("#mesh-search").fill("critical");
  await expect(page.locator(".mesh-node:not(.mesh-context)")).toHaveCount(2);
  await expect(page.locator('[data-node-id="process:1"]:not(.mesh-context)')).toHaveCount(0);
});

// The opening picture is the whole landscape, and it stays zoomable from there.
// Both halves matter: a view that opened zoomed in would hide the mesh, and one that
// could not zoom would be unreadable the moment the graph got interesting.
test("opens fitted to the content and zooms from there", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/starmap");

  const canvas = page.locator(".mesh-canvas");
  const fitted = await canvas.getAttribute("viewBox");
  const [viewX, viewY, fitW, fitH] = fitted.split(" ").map(Number);

  // The frame the view opens on is the drawing surface itself, so its aspect ratio
  // matches the element's — which is what leaves no letterboxed band around the
  // graph. Anything else and preserveAspectRatio pads one axis with empty space.
  const box = await canvas.boundingBox();
  expect(fitW / fitH).toBeCloseTo(box.width / box.height, 1);

  // The bottom-right corner is not drawable: the zoom controls float over it, and a
  // node underneath them cannot be clicked or dragged. The fit holds it clear by
  // giving up either that corner's width or its height — whichever costs the picture
  // less — so the content is centred in the frame minus one of the two.
  const chrome = await page.locator(".mesh-zoom").boundingBox();
  const perUnit = fitW / box.width;
  const reserveW = (chrome.width + 22) * perUnit;
  const reserveH = (chrome.height + 22) * (fitH / box.height);

  // The settled nodes reach the frame: the content fills the space it was given
  // rather than floating in a small disc in the middle of it.
  const spread = await page.evaluate(() => {
    const at = [...document.querySelectorAll(".mesh-node")]
      .map((g) => /translate\(([-\d.]+),([-\d.]+)\)/.exec(g.getAttribute("transform")));
    const xs = at.map((m) => Number(m[1])), ys = at.map((m) => Number(m[2]));
    return {
      x: Math.max(...xs) - Math.min(...xs),
      y: Math.max(...ys) - Math.min(...ys),
      left: Math.min(...xs), top: Math.min(...ys),
    };
  });
  // The fit scales both axes by one factor, so the content reaches the padding on
  // the axis that ran out first. Requiring both would mean stretching the axes
  // independently, which fills the last pixel by misreporting distance — and
  // distance is the only thing a force layout is trying to say.
  const reachesX = spread.x > fitW - reserveW - 200;
  const reachesY = spread.y > fitH - reserveH - 200;
  expect(reachesX || reachesY).toBe(true);
  // And it is centred in the space it can be reached in, so the leftover is a margin
  // rather than a blank half. Which of the two regions that is depends on the shape
  // of the content, so either reading may hold — but one of them must.
  const centredInWidth = Math.abs((spread.left - viewX) - (fitW - reserveW - spread.x) / 2) < 12;
  const centredInHeight = Math.abs((spread.top - viewY) - (fitH - reserveH - spread.y) / 2) < 12;
  expect(centredInWidth || centredInHeight).toBe(true);

  await page.locator("#mesh-zoom-in").click();
  const zoomed = Number((await canvas.getAttribute("viewBox")).split(" ")[2]);
  expect(zoomed).toBeLessThan(fitW);

  await page.locator("#mesh-zoom-fit").click();
  expect(await canvas.getAttribute("viewBox")).toBe(fitted);
});

// Panning at any magnification, the fitted frame included. It used to be refused
// there — everything was on screen, so a drag could only push the picture into the
// empty space the fit exists to remove — and that stopped being the whole truth when
// a node became draggable anywhere: there is somewhere to pan *to*, and a canvas that
// only moves when zoomed in is a canvas whose rules a reader has to discover.
test("pans at any magnification, without also selecting", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/starmap");

  const canvas = page.locator(".mesh-canvas");
  const fitted = await canvas.getAttribute("viewBox");
  // Measured per drag rather than once: a repaint can change the legend's height and
  // move the canvas under a coordinate captured earlier, which would silently drag
  // somewhere else and report the pan as broken.
  const drag = async () => {
    const box = await canvas.boundingBox();
    await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
    await page.mouse.down();
    await page.mouse.move(box.x + box.width / 2 - 120, box.y + box.height / 2, { steps: 6 });
    await page.mouse.up();
  };

  await drag();
  const pannedAtFit = await canvas.getAttribute("viewBox");
  expect(pannedAtFit).not.toBe(fitted);
  // A translation, not a rescale — the same rule as when zoomed in.
  expect(pannedAtFit.split(" ").slice(2)).toEqual(fitted.split(" ").slice(2));
  // And Fit is the way back, which is what makes an unbounded canvas navigable
  // rather than a place to get lost in.
  await page.locator("#mesh-zoom-fit").click();
  expect(await canvas.getAttribute("viewBox")).toBe(fitted);

  await page.locator("#mesh-zoom-in").click();
  const zoomed = await canvas.getAttribute("viewBox");
  await drag();
  const panned = await canvas.getAttribute("viewBox");
  expect(panned).not.toBe(zoomed);
  // Only the origin moved: a pan is a translation, never a rescale.
  expect(panned.split(" ").slice(2)).toEqual(zoomed.split(" ").slice(2));

  // And the drag did not also select whatever it started on — panning and selecting
  // share the surface, and a pan that changed the answer beside the picture would
  // make the picture impossible to move.
  await expect(page.locator(".mesh-panel-empty")).toBeVisible();
});

// meshOf builds a landscape of n processes under one application, for the label
// policy — which is a function of how crowded the picture is.
function meshOf(processes) {
  const graph = {
    nodes: [{ id: "application:a1", kind: "application", name: "Billing", provenance: "derived" }],
    edges: [], restricted: 0, clustered: false,
  };
  for (let i = 0; i < processes; i++) {
    graph.nodes.push({
      id: `process:${i}`, kind: "process", name: `Process ${i}`, provenance: "derived",
      application: "application:a1", processId: `p${i}`, version: 1,
    });
    graph.edges.push({ from: "application:a1", to: `process:${i}`, kind: "contains" });
  }
  return graph;
}

// Zoomed out over a crowded landscape a name would be a smear sitting on top of
// the structure the picture is carrying, so it is not painted. That is a statement
// about the magnification, not about the node: the name is in the DOM, it is the
// node's accessible label, and hovering or zooming brings it back.
test("a crowded landscape holds its names until they can be read", async ({ page }) => {
  installMock(page, meshOf(100));
  await page.goto("/index.html#/panorama/starmap");

  // Every node wants its name; the canvas decides which are legible right now.
  await expect(page.locator('[data-node-id="process:0"]')).toHaveClass(/mesh-named/);
  await expect(page.locator(".mesh-canvas")).not.toHaveClass(/mesh-names-all/);
  const ink = page.locator('[data-node-id="process:0"] .mesh-label-ink');
  await expect(ink).toHaveCSS("opacity", "0");

  // The name is in the DOM either way: a screen reader must not depend on a
  // pointer, or on a zoom level, to reach it.
  await expect(page.locator('[data-node-id="process:0"] .mesh-label')).toHaveText("Process 0");
  await expect(page.locator('[data-node-id="process:0"]'))
    .toHaveAttribute("aria-label", /Process 0/);

  // Hovering reveals it, without re-rendering the graph.
  await page.locator('[data-node-id="process:0"]').hover();
  await expect(ink).toHaveCSS("opacity", "1");
});

// And zooming in reveals them all — which is the whole bargain of laying the graph
// out in a world larger than the window. Nothing is re-rendered to do it: the
// canvas is told what magnification it is at and the stylesheet does the rest.
test("zooming in brings the names out", async ({ page }) => {
  installMock(page, meshOf(100));
  await page.goto("/index.html#/panorama/starmap");

  const canvas = page.locator(".mesh-canvas");
  await expect(canvas).not.toHaveClass(/mesh-names-all/);
  const before = await canvas.getAttribute("viewBox");

  for (let i = 0; i < 6; i++) await page.locator("#mesh-zoom-in").click();

  await expect(canvas).toHaveClass(/mesh-names-all/);
  await expect(page.locator('[data-node-id="process:0"] .mesh-label-ink')).toHaveCSS("opacity", "1");
  // The graph did not move under the zoom: same nodes, same layout, new window.
  expect(await canvas.getAttribute("viewBox")).not.toBe(before);
  await expect(page.locator(".mesh-node")).toHaveCount(101);
});

// A small landscape needs no zoom: its world is the window, so every name is
// already large enough to read and hiding any would be pure loss.
test("a small landscape keeps every name on screen", async ({ page }) => {
  installMock(page, meshOf(4));
  await page.goto("/index.html#/panorama/starmap");

  await expect(page.locator(".mesh-node")).toHaveCount(5);
  await expect(page.locator(".mesh-canvas")).toHaveClass(/mesh-names-all/);
  for (const id of ["application:a1", "process:0", "process:3"]) {
    await expect(page.locator(`[data-node-id="${id}"] .mesh-label-ink`)).toHaveCSS("opacity", "1");
  }
});

// Two things bring a name back in a crowded graph without hovering and without
// zooming: selecting the node, and filtering down to it. Both are how somebody
// actually looks for one. The graph has to be large enough that its names are
// genuinely held back, or this asserts nothing.
test("selecting or filtering brings a name back", async ({ page }) => {
  installMock(page, meshOf(100));
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).not.toHaveClass(/mesh-names-all/);

  await page.locator('[data-node-id="process:7"]').click();
  await expect(page.locator('[data-node-id="process:7"] .mesh-label-ink')).toHaveCSS("opacity", "1");

  await page.locator("#mesh-search").fill("Process 12");
  await expect(page.locator(".mesh-node:not(.mesh-context)")).toHaveCount(1);
  await expect(page.locator('[data-node-id="process:12"] .mesh-label-ink')).toHaveCSS("opacity", "1");
});

// Size carries rank as well as kind: at a few hundred nodes the eye sorts by size
// before it reads anything, so an application has to be unmistakably the largest.
// mesh-body is the node's own outline: a group can also hold a provenance ring and a
// severity badge, and either would answer with the wrong radius.
//
// data-r rather than the `r` attribute, because a node's outline is a square, a
// hexagon or a triangle as often as it is a circle — and because data-r is the
// radius the *layout* reserved, which is what every claim about size and spacing on
// this view is actually about. Reading `r` would answer null for every shape that is
// not a circle, and `+null` is 0: the overlap tests below would have gone on passing
// while measuring nothing.
const radiusOf = async (page, id) => Number(
  await page.locator(`[data-node-id="${id}"] .mesh-body`).getAttribute("data-r"));

test("kinds are told apart by size, not only by colour", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/starmap");

  const application = await radiusOf(page, "application:a1");
  const process = await radiusOf(page, "process:1");
  const worker = await radiusOf(page, "worker:c1");
  expect(application).toBeGreaterThan(process);
  expect(process).toBeGreaterThan(worker);
});

// Size says two things at once — what kind of thing this is, and how much of the
// landscape hangs off it. The second is what makes a hub findable in a picture with
// four hundred circles in it, where the eye sorts by size before it reads anything.
// (That the two never overwrite each other is arithmetic, checked exhaustively in
// panorama-size.spec.mjs; this is the claim that the drawing actually uses it.)
test("a well-connected node is drawn larger than a lonely one of its kind", async ({ page }) => {
  installMock(page, {
    nodes: [
      { id: "process:hub", kind: "process", name: "Hub", provenance: "derived", processId: "hub", version: 1 },
      { id: "process:leaf", kind: "process", name: "Leaf", provenance: "derived", processId: "leaf", version: 1 },
      ...Array.from({ length: 6 }, (_, i) => ({
        id: `worker:w${i}`, kind: "worker", name: `w${i}`, provenance: "derived", workerType: "mail",
      })),
    ],
    edges: Array.from({ length: 6 }, (_, i) => (
      { from: "process:hub", to: `worker:w${i}`, kind: "uses" })),
    restricted: 0, clustered: false,
  });
  await page.goto("/index.html#/panorama/starmap");

  const hub = await radiusOf(page, "process:hub");
  const leaf = await radiusOf(page, "process:leaf");
  expect(hub).toBeGreaterThan(leaf);
});

// The separation pass exists because repulsion alone is a soft force a spring can
// overpower, and two circles sitting on top of each other is the one arrangement
// that makes the picture unreadable rather than merely tight.
test("nodes do not overlap each other", async ({ page }) => {
  installMock(page, meshOf(30));
  await page.goto("/index.html#/panorama/starmap");

  const overlaps = await page.evaluate(() => {
    const at = [...document.querySelectorAll(".mesh-node")].map((g) => {
      const m = /translate\(([-\d.]+),([-\d.]+)\)/.exec(g.getAttribute("transform"));
      const circle = g.querySelector(".mesh-body");
      return { x: +m[1], y: +m[2], r: Number(circle.getAttribute("data-r")) };
    });
    let worst = 0;
    for (let i = 0; i < at.length; i++) {
      for (let j = i + 1; j < at.length; j++) {
        const gap = Math.hypot(at[i].x - at[j].x, at[i].y - at[j].y) - at[i].r - at[j].r;
        worst = Math.min(worst, gap);
      }
    }
    return worst;
  });
  // Negative means two circles intersect. A little slack for the fit's rescaling.
  expect(overlaps).toBeGreaterThan(-2);
});

// Which bubble is connected to which, answered by pointing at one. Impact analysis
// already answers the bigger question — what breaks if this goes down — but it
// needs a click and walks the whole chain. This is the question asked dozens of
// times while reading a landscape: what does *this* touch?
test("pointing at a node shows what it is connected to", async ({ page }) => {
  installMock(page, graph);
  await page.goto("/index.html#/panorama/starmap");
  const canvas = page.locator(".mesh-canvas");
  await expect(canvas).not.toHaveClass(/mesh-relating/);

  await page.locator('[data-node-id="process:1"]').hover();
  await expect(canvas).toHaveClass(/mesh-relating/);

  // The node itself, its neighbours, and the edges between them are lifted.
  await expect(page.locator('[data-node-id="process:1"]')).toHaveClass(/mesh-relating-self/);
  const related = page.locator(".mesh-node.mesh-related");
  expect(await related.count()).toBeGreaterThan(0);
  await expect(page.locator(".mesh-edge.mesh-related-edge").first()).toBeVisible();

  // Every edge lifted actually touches the node — the highlight is the graph's own
  // adjacency, not a guess from where things happen to sit on screen.
  const honest = await page.evaluate(() => [...document.querySelectorAll(".mesh-related-edge")]
    .every((e) => e.dataset.from === "process:1" || e.dataset.to === "process:1"));
  expect(honest).toBe(true);

  // A related node's name comes out with it: knowing something is connected is not
  // much use without knowing what it is.
  await expect(related.first().locator(".mesh-label-ink")).toHaveCSS("opacity", "1");

  // And it is marked in colour, not only by being left un-faded: a ring in the
  // accent says "these are the ones" where merely staying visible says "these are
  // still here". The ring is outside the circle rather than a recolouring of it,
  // because the body's own stroke is carrying the severity.
  const ring = async (id) => page.locator(`[data-node-id="${id}"] .mesh-halo`)
    .evaluate((el) => ({
      opacity: Number(getComputedStyle(el).opacity),
      width: parseFloat(getComputedStyle(el).strokeWidth),
    }));
  const neighbour = await related.first().getAttribute("data-node-id");
  const lit = await ring(neighbour);
  expect(lit.opacity).toBeGreaterThan(0.5);
  expect(lit.width).toBeGreaterThan(1);
  // The node being asked about is never in doubt: its ring is the stronger one.
  expect((await ring("process:1")).width).toBeGreaterThan(lit.width);
  // And an unrelated node has no ring at all.
  expect((await ring("decision:credit")).opacity).toBe(0);
  // The severity stroke on a related node survives being highlighted.
  await expect(page.locator(`[data-node-id="${neighbour}"] .mesh-body`)).toBeVisible();

  // Unrelated ones fall back rather than disappearing: the question is "what does
  // this touch", not "what if the rest were gone".
  const dimmed = await page.evaluate(() => {
    const el = [...document.querySelectorAll(".mesh-node")]
      .find((n) => !n.classList.contains("mesh-related") && !n.classList.contains("mesh-relating-self"));
    return el ? getComputedStyle(el).opacity : null;
  });
  expect(Number(dimmed)).toBeGreaterThan(0);
  expect(Number(dimmed)).toBeLessThan(1);
});

// The layout is what the whole picture rests on: a graph compressed into the
// viewport puts its circles through each other, and no amount of colour or labelling
// recovers from that. The world grows with the content instead, so the guarantee
// holds at every size rather than only at the small ones.
test("no two nodes overlap, however many there are", async ({ page }) => {
  for (const size of [12, 60]) {
    installMock(page, meshOf(size));
    await page.goto("/index.html#/panorama/starmap");
    await page.locator(".mesh-node").first().waitFor();

    const worst = await page.evaluate(() => {
      const at = [...document.querySelectorAll(".mesh-node")].map((g) => {
        const m = /translate\(([-\d.]+),([-\d.]+)\)/.exec(g.getAttribute("transform"));
        return { x: +m[1], y: +m[2], r: +g.querySelector(".mesh-body").getAttribute("data-r") };
      });
      let gap = Infinity;
      for (let i = 0; i < at.length; i++) for (let j = i + 1; j < at.length; j++) {
        gap = Math.min(gap, Math.hypot(at[i].x - at[j].x, at[i].y - at[j].y) - at[i].r - at[j].r);
      }
      return gap;
    });
    // Not merely non-negative: there is room for a name between any two of them.
    expect(worst).toBeGreaterThan(30);
  }
});

// Picking a bubble up and putting it somewhere.
//
// The layout answers "where does this graph want to sit", which is the right first
// answer and never the last one: the person reading it knows things the simulation
// does not, and until now had no way to say so. These check the three things that
// make the gesture worth having — the node goes where it is dropped, the graph
// rearranges itself around it, and what was placed by hand survives the next thing
// that repaints the picture.

// screenAt reads a node's rendered centre in page coordinates, which is what a drag
// is expressed in — the transform is in world units and the two differ by whatever
// the current viewBox is.
async function screenAt(page, id) {
  const box = await page.locator(`[data-node-id="${id}"] .mesh-body`).boundingBox();
  return { x: box.x + box.width / 2, y: box.y + box.height / 2 };
}

// worldAt reads the settled coordinates the layout is actually working in.
async function worldAt(page, id) {
  return page.evaluate((nodeId) => {
    const g = document.querySelector(`[data-node-id="${nodeId}"]`);
    const m = /translate\(([-\d.]+),([-\d.]+)\)/.exec(g.getAttribute("transform"));
    return { x: +m[1], y: +m[2] };
  }, id);
}

// dragBy picks a node up and puts it down dx/dy away. hover() rather than a raw
// mouse.move, because it scrolls the node into view first: a press at coordinates
// below the fold is a gesture the browser never sees, and the test would be
// reporting on the scroll position rather than on the drag.
async function dragBy(page, id, dx, dy) {
  const target = page.locator(`[data-node-id="${id}"] .mesh-body`);
  await target.hover();
  const box = await target.boundingBox();
  const stage = await page.locator("#mesh-surface").boundingBox();
  const view = page.viewportSize();
  const from = { x: box.x + box.width / 2, y: box.y + box.height / 2 };
  // Into the part of the stage that is actually on screen. hover() may have scrolled
  // the stage so that its top is above the viewport, and a drop at a negative
  // coordinate is a gesture the browser never sees.
  const within = (value, low, high) => Math.min(Math.max(value, low), high);
  const to = {
    x: within(from.x + dx, Math.max(stage.x, 0) + 40,
      Math.min(stage.x + stage.width, view.width) - 40),
    y: within(from.y + dy, Math.max(stage.y, 0) + 40,
      Math.min(stage.y + stage.height, view.height) - 40),
  };
  await page.mouse.down();
  await page.mouse.move(to.x, to.y, { steps: 12 });
  await page.mouse.up();
  return { from, to };
}

test("a node goes where it is dropped, and the graph settles around it", async ({ page }) => {
  installMock(page, meshOf(12));
  await page.goto("/index.html#/panorama/starmap");
  await page.locator(".mesh-node").first().waitFor();

  const others = await page.evaluate(() => Object.fromEntries(
    [...document.querySelectorAll(".mesh-node")].map((g) => [g.dataset.nodeId, g.getAttribute("transform")])));

  const { to } = await dragBy(page, "process:3", 120, -70);
  const landed = await screenAt(page, "process:3");
  // Where it was put, not where the layout wanted it: within a couple of pixels of
  // the pointer, which is all the rounding in the transform allows for.
  expect(Math.abs(landed.x - to.x)).toBeLessThan(6);
  expect(Math.abs(landed.y - to.y)).toBeLessThan(6);

  // And it says so. A node that is not where the layout put it is a fact about the
  // picture; hiding it would make the arrangement look like the simulation's answer.
  await expect(page.locator('[data-node-id="process:3"]')).toHaveClass(/mesh-pinned/);

  // The rest of the landscape moved out of the way rather than staying put and
  // being overlapped — which is the difference between dragging a node and dragging
  // a sticker across a picture of one.
  const moved = await page.evaluate((before) => [...document.querySelectorAll(".mesh-node")]
    .filter((g) => g.dataset.nodeId !== "process:3" && g.getAttribute("transform") !== before[g.dataset.nodeId])
    .length, others);
  expect(moved).toBeGreaterThan(0);

  // Still no two circles through each other, which is the guarantee the whole
  // picture rests on and the one a drag could most easily break.
  const worst = await page.evaluate(() => {
    const at = [...document.querySelectorAll(".mesh-node")].map((g) => {
      const m = /translate\(([-\d.]+),([-\d.]+)\)/.exec(g.getAttribute("transform"));
      return { x: +m[1], y: +m[2], r: +g.querySelector(".mesh-body").getAttribute("data-r") };
    });
    let gap = Infinity;
    for (let i = 0; i < at.length; i++) for (let j = i + 1; j < at.length; j++) {
      gap = Math.min(gap, Math.hypot(at[i].x - at[j].x, at[i].y - at[j].y) - at[i].r - at[j].r);
    }
    return gap;
  });
  expect(worst).toBeGreaterThan(0);

  // Dragging is not selecting. A gesture that also selected whatever it started on
  // would make the picture impossible to arrange without changing the answer beside it.
  await expect(page.locator(".mesh-panel-empty")).toBeVisible();
});

// A drag moves what it is joined to, and leaves the rest of the landscape exactly
// where the reader last saw it.
//
// This is the property, not a nicety. Resuming the layout's own physics from the
// picture on screen looks reasonable and is not: the picture has been fitted since
// it was simulated, so it sits nowhere near the simulation's equilibrium, and
// restarting it there reorganises the whole landscape the moment a node is touched.
// The reader loses the arrangement they were reading in order to move one node in it.
test("a drag moves the neighbourhood, not the landscape", async ({ page }) => {
  installMock(page, meshOf(12));
  await page.goto("/index.html#/panorama/starmap");
  await page.locator(".mesh-node").first().waitFor();

  const positions = () => page.evaluate(() => Object.fromEntries(
    [...document.querySelectorAll(".mesh-node")].map((g) => {
      const m = /translate\(([-\d.]+),([-\d.]+)\)/.exec(g.getAttribute("transform"));
      return [g.dataset.nodeId, { x: +m[1], y: +m[2] }];
    })));

  const before = await positions();
  await dragBy(page, "process:3", 130, -60);
  const after = await positions();
  const moved = (id) => Math.hypot(after[id].x - before[id].x, after[id].y - before[id].y);

  // The node went where it was put, and the application it sits in came after it.
  expect(moved("process:3")).toBeGreaterThan(60);
  expect(moved("application:a1")).toBeGreaterThan(5);

  // Everything else is untouched, except whatever was actually in the way. In this
  // landscape the processes are joined to their application and to nothing else, so
  // there is nothing to pull them along — only a collision could move one.
  const others = Object.keys(before)
    .filter((id) => id !== "process:3" && id !== "application:a1");
  const still = others.filter((id) => moved(id) < 1);
  expect(still.length).toBeGreaterThanOrEqual(others.length - 2);
});

test("what you placed by hand survives a repaint", async ({ page }) => {
  installMock(page, meshOf(12));
  await page.goto("/index.html#/panorama/starmap");
  await page.locator(".mesh-node").first().waitFor();

  await dragBy(page, "process:5", -110, 90);
  const dropped = await worldAt(page, "process:5");

  // Anything that re-lays-out the graph. A depth change is the cheapest of them and
  // has nothing to do with where things sit, which is the point: an arrangement that
  // only survived until the next unrelated click would not be worth making.
  await setDepth(page, "1");
  await expect(page.locator('[data-node-id="process:5"]')).toHaveClass(/mesh-pinned/);
  const after = await worldAt(page, "process:5");
  expect(Math.hypot(after.x - dropped.x, after.y - dropped.y)).toBeLessThan(1);

  // A filter is a temporary question, and asking one must not cost the arrangement.
  await page.fill("#mesh-search", "Process 5");
  await expect(page.locator(".mesh-node:not(.mesh-context)")).toHaveCount(1);
  await page.fill("#mesh-search", "");
  await expect(page.locator(".mesh-node")).toHaveCount(13);
  await expect(page.locator('[data-node-id="process:5"]')).toHaveClass(/mesh-pinned/);
});

test("release puts everything back where the layout wants it", async ({ page }) => {
  installMock(page, meshOf(12));
  await page.goto("/index.html#/panorama/starmap");
  await page.locator(".mesh-node").first().waitFor();

  const release = page.locator("#mesh-release");
  await expect(release).toBeDisabled();
  const original = await worldAt(page, "process:7");

  await dragBy(page, "process:7", 140, 60);
  await expect(release).toBeEnabled();

  await release.click();
  await expect(release).toBeDisabled();
  await expect(page.locator('[data-node-id="process:7"]')).not.toHaveClass(/mesh-pinned/);
  // Back where it started: the layout is deterministic, so releasing everything
  // reproduces the picture the graph opened with rather than some third arrangement.
  const back = await worldAt(page, "process:7");
  expect(Math.hypot(back.x - original.x, back.y - original.y)).toBeLessThan(1);
});

// Releasing one node lives in the panel beside the node it is about. It used to be
// a double-click — which is a thing you have to be told, and which the drilldown
// now wants — so it became a button on the thing itself, which is a thing you can
// see.
test("one node can be released without disturbing the arrangement", async ({ page }) => {
  installMock(page, meshOf(12));
  await page.goto("/index.html#/panorama/starmap");
  await page.locator(".mesh-node").first().waitFor();

  await dragBy(page, "process:2", 100, 40);
  await expect(page.locator('[data-node-id="process:2"]')).toHaveClass(/mesh-pinned/);
  await dragBy(page, "process:9", -90, -60);
  await expect(page.locator('[data-node-id="process:9"]')).toHaveClass(/mesh-pinned/);
  const kept = await worldAt(page, "process:9");

  // Nothing selected: the panel offers no release, because there is no node it
  // would be about.
  await expect(page.locator(".mesh-unpin")).toHaveCount(0);

  await page.locator('[data-node-id="process:2"]').click();
  await page.locator(".mesh-unpin").click();
  await expect(page.locator('[data-node-id="process:2"]')).not.toHaveClass(/mesh-pinned/);

  // The other one is untouched — both its pin and the place it was put.
  await expect(page.locator('[data-node-id="process:9"]')).toHaveClass(/mesh-pinned/);
  await expect(page.locator("#mesh-release")).toBeEnabled();
  const still = await worldAt(page, "process:9");
  expect(Math.hypot(still.x - kept.x, still.y - kept.y)).toBeLessThan(1);
});

// Arranging without a mouse. Everything this view *says* is already reachable from
// the keyboard — focusing a node lifts its neighbours, Enter selects it — so this is
// a convenience rather than information. But a convenience only some people can have
// is not one.
test("a focused node can be moved with the arrow keys", async ({ page }) => {
  installMock(page, meshOf(12));
  await page.goto("/index.html#/panorama/starmap");
  await page.locator(".mesh-node").first().waitFor();

  const node = page.locator('[data-node-id="process:4"]');
  await node.focus();
  const before = await worldAt(page, "process:4");

  await page.keyboard.press("ArrowRight");
  const stepped = await worldAt(page, "process:4");
  expect(stepped.x).toBeGreaterThan(before.x);
  expect(Math.abs(stepped.y - before.y)).toBeLessThan(1);
  await expect(node).toHaveClass(/mesh-pinned/);

  // Shift is the coarse step: crossing a large landscape a pixel at a time is not a
  // keyboard equivalent of a drag.
  const fine = stepped.x - before.x;
  await page.keyboard.press("Shift+ArrowRight");
  const coarse = (await worldAt(page, "process:4")).x - stepped.x;
  expect(coarse).toBeGreaterThan(fine * 2);

  // And it is the same arrangement a drag makes, so Release puts it back.
  await page.locator("#mesh-release").click();
  await expect(node).not.toHaveClass(/mesh-pinned/);
});

// Saved views. The complaint they answer: watching one node means filtering down to
// it and zooming in, and a reload puts you back at the whole landscape with all of
// it to do again.
test("a saved view brings the whole setup back", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/starmap");
  await page.locator(".mesh-node").first().waitFor();
  await expect(page.locator(".mesh-view-empty")).toBeVisible();

  // Set the landscape up: filter to one node, watch it, zoom in, place it by hand.
  await page.fill("#mesh-search", "invoice");
  await expect(page.locator("#mesh-count")).toContainText("1 of 7");
  await page.locator('[data-node-id="process:1"]').click();
  await expect(page.locator(".mesh-impact-count")).toBeVisible();
  await page.locator("#mesh-zoom-in").click();
  await page.locator("#mesh-zoom-in").click();
  await dragBy(page, "process:1", 60, 40);
  const framed = await page.locator(".mesh-canvas").getAttribute("viewBox");

  await page.fill("#mesh-view-name", "Billing watch");
  await page.locator("#mesh-view-save button").click();
  await expect(page.locator("#mesh-view-note")).toContainText("Saved “Billing watch”");
  await expect(page.locator(".mesh-view-open")).toHaveText("Billing watch");

  // Everything back to where a reload leaves it.
  await page.locator("#mesh-release").click();
  await page.fill("#mesh-search", "");
  await page.locator("#mesh-zoom-fit").click();
  await expect(page.locator(".mesh-node")).toHaveCount(7);

  await page.locator(".mesh-view-open").click();
  await expect(page.locator("#mesh-count")).toContainText("1 of 7");
  await expect(page.locator(".mesh-impact-count")).toBeVisible();
  await expect(page.locator('[data-node-id="process:1"]')).toHaveClass(/mesh-pinned/);
  // The same magnification, framed on the node it was watching.
  const reopened = await page.locator(".mesh-canvas").getAttribute("viewBox");
  expect(Number(reopened.split(" ")[2])).toBeCloseTo(Number(framed.split(" ")[2]), 0);
});

// A view is a way of looking, not a snapshot of what was there: the landscape is
// derived and changes as things are deployed. So a view that was watching something
// which has since gone must say so rather than describing a node that is not on
// screen.
test("a view whose node is gone opens and says so", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/starmap");

  await page.locator('[data-node-id="worker:c1"]').click();
  await page.fill("#mesh-view-name", "Mail worker");
  await page.locator("#mesh-view-save button").click();

  // The same view, against a landscape that no longer has that worker.
  const smaller = { ...graph, nodes: graph.nodes.filter((n) => n.id !== "worker:c1"),
    edges: graph.edges.filter((e) => e.from !== "worker:c1" && e.to !== "worker:c1") };
  installMock(page, smaller);
  await page.reload();
  await page.locator(".mesh-node").first().waitFor();

  // It survived the reload — that is the whole point of saving it.
  await expect(page.locator(".mesh-view-open")).toHaveText("Mail worker");
  await page.locator(".mesh-view-open").click();
  await expect(page.locator("#mesh-view-note")).toContainText("no longer in this starmap");
  await expect(page.locator(".mesh-panel-empty")).toBeVisible();
});

test("views can be renamed over and forgotten", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/starmap");
  await page.locator(".mesh-node").first().waitFor();

  await page.fill("#mesh-search", "invoice");
  await page.fill("#mesh-view-name", "Billing watch");
  await page.locator("#mesh-view-save button").click();

  // Saving the same name again updates it in place rather than making a second entry.
  await page.fill("#mesh-search", "dunning");
  await page.fill("#mesh-view-name", "billing watch");
  await page.locator("#mesh-view-save button").click();
  await expect(page.locator("#mesh-view-note")).toContainText("Updated");
  await expect(page.locator(".mesh-view-open")).toHaveCount(1);

  await page.fill("#mesh-search", "");
  await page.locator(".mesh-view-open").click();
  await expect(page.locator("#mesh-search")).toHaveValue("dunning");

  // An empty name is refused with a sentence, not by doing nothing.
  await page.fill("#mesh-view-name", "   ");
  await page.locator("#mesh-view-save button").click();
  await expect(page.locator("#mesh-view-note")).toContainText("name");
  await expect(page.locator(".mesh-view-open")).toHaveCount(1);

  await page.locator(".mesh-view-drop").click();
  await expect(page.locator(".mesh-view-empty")).toBeVisible();
  await page.reload();
  await expect(page.locator(".mesh-view-empty")).toBeVisible();
});

// The findings list. The picture already marks which nodes have something wrong with
// them, and on four hundred circles that is not the same as being able to read them:
// finding three red dots means hunting, and hunting is what somebody does instead of
// noticing.
test("lists the findings beside the picture, worst first", async ({ page }) => {
  installMock(page, statusGraph);
  await page.goto("/index.html#/panorama/starmap");

  const findings = page.locator(".mesh-finding-go");
  await expect(findings).toHaveCount(3);

  // Worst first, and within a class the one with more incidents behind it.
  const names = await page.locator(".mesh-finding-name").allTextContents();
  expect(names.slice(0, 2).sort()).toEqual(["Billing", "Dunning"]);
  expect(names[2]).toBe("Invoice");

  // Nothing well or unwatched is in the list: it is the findings, not the inventory.
  expect(names).not.toContain("Credit score");

  // The count is carried where there is one to carry. An incident belongs to a
  // token and only a process has tokens, so a node without a count is one that
  // cannot have one — never one reported as having none.
  await expect(page.locator('[data-finding="process:1"]')).toContainText("3 incident(s)");
  await expect(page.locator('[data-finding="process:1"]')).toContainText("degraded");
  await expect(page.locator('[data-finding="decision:credit"]')).toHaveCount(0);
  await expect(page.locator(".mesh-findings-head")).toContainText("3 node(s), 3 incident(s)");

  // The sentence behind the finding is there: the class says what kind of trouble,
  // only the reason says what to do about it.
  await expect(page.locator('[data-finding="process:2"]'))
    .toContainText("This worker cannot serve work");
});

// Going *to* a finding is the whole reason the list is worth having.
test("clicking a finding selects it and puts it on screen", async ({ page }) => {
  installMock(page, statusGraph);
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).not.toHaveClass(/mesh-zoomed/);

  await page.locator('[data-finding="process:1"]').click();

  // Selected, so the panel above it explains the finding.
  await expect(page.locator(".mesh-panel-head")).toContainText("Invoice");
  await expect(page.locator('[data-node-id="process:1"]')).toHaveClass(/mesh-in-impact/);

  // And framed on it, rather than left somewhere in the landscape.
  await expect(page.locator(".mesh-canvas")).toHaveClass(/mesh-zoomed/);
  const centred = await page.evaluate(() => {
    const svg = document.querySelector(".mesh-canvas");
    const [x, y, w, h] = svg.getAttribute("viewBox").split(" ").map(Number);
    const g = document.querySelector('[data-node-id="process:1"]');
    const m = /translate\(([-\d.]+),([-\d.]+)\)/.exec(g.getAttribute("transform"));
    return { dx: Math.abs(x + w / 2 - +m[1]), dy: Math.abs(y + h / 2 - +m[2]) };
  });
  expect(centred.dx).toBeLessThan(1);
  expect(centred.dy).toBeLessThan(1);
});

// An empty findings list is not a claim that everything is well: most nodes in a
// young landscape are unobserved, and a status view that cannot go red is worse than
// no status view.
test("an empty findings list does not claim everything is fine", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/starmap");

  await expect(page.locator(".mesh-finding-go")).toHaveCount(0);
  await expect(page.locator(".mesh-findings")).toContainText("not the same as everything being well");
});

// The heartbeat. Motion is the one channel left once colour, size, shape and a glyph
// are all carrying something — and it is the channel the eye finds without being
// pointed at it, which is what a view somebody glances at needs.
test("nodes with a finding beat, and the worse ones beat slower", async ({ page }) => {
  installMock(page, statusGraph);
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toHaveClass(/mesh-beating/);

  const beat = (id) => page.locator(`[data-node-id="${id}"] .mesh-beat`).evaluate((el) => {
    const style = getComputedStyle(el);
    return { name: style.animationName, seconds: parseFloat(style.animationDuration), stroke: style.stroke };
  });

  // The worse the state, the *less* pulse: a degraded process is still working and
  // beats quickly and twice; one that cannot do work beats once, slowly and heavily.
  const attention = await beat("process:1");
  const critical = await beat("process:2");
  expect(attention.name).toBe("mesh-beat-quick");
  expect(critical.name).toBe("mesh-beat-slow");
  expect(critical.seconds).toBeGreaterThan(attention.seconds * 2);

  // Each keeps its own severity colour rather than both going red: "it is broken"
  // and "something inside it went wrong" are the two findings this view exists to
  // tell apart.
  expect(critical.stroke).not.toBe(attention.stroke);

  // Nothing well or unwatched beats at all — a landscape that pulsed everywhere
  // would be saying nothing, loudly.
  await expect(page.locator('[data-node-id="decision:credit"] .mesh-beat')).toHaveCount(0);
});

// A landscape where three things are wrong should draw the eye to those three. One
// where two hundred are wrong is a picture of an outage, and two hundred animations
// say less than a still frame while costing far more to paint.
test("past its budget the beat stops rather than swamping the picture", async ({ page }) => {
  const many = {
    nodes: Array.from({ length: 90 }, (_, i) => ({
      id: `process:${i}`, kind: "process", name: `Process ${i}`, provenance: "derived",
      processId: `p${i}`, version: 1, state: "degraded", severity: "attention",
      incidents: 1, reason: "1 token(s) are parked behind an unresolved incident.",
    })),
    edges: [], restricted: 0, clustered: false,
  };
  installMock(page, many);
  await page.goto("/index.html#/panorama/starmap");

  // The rings are still there, and still marking the findings — they have simply
  // stopped competing for attention they no longer need to win.
  await expect(page.locator(".mesh-canvas")).not.toHaveClass(/mesh-beating/);
  const ring = await page.locator('[data-node-id="process:1"] .mesh-beat')
    .evaluate((el) => ({ name: getComputedStyle(el).animationName, opacity: Number(getComputedStyle(el).opacity) }));
  expect(ring.name).toBe("none");
  expect(ring.opacity).toBeGreaterThan(0);
});

// A filtered picture gets a filtered list — and then has to say so. "Findings" over a
// landscape showing one node in four would otherwise read as *the* findings, which is
// a claim about the three that are not there.
test("a filtered findings list says it is filtered", async ({ page }) => {
  installMock(page, statusGraph);
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-findings-head")).toContainText("3 node(s)");
  await expect(page.locator(".mesh-findings-head")).not.toContainText("filtered");

  await page.fill("#mesh-search", "dunning");
  await expect(page.locator(".mesh-findings-head")).toContainText("in the filtered starmap");
});

// Drilling into a node: the landscape reduced to it and what it touches.
//
// The complaint it answers is the one every large graph has: you find the thing you
// came for and it is still sitting in four hundred circles of everything else.
test("going into a node reduces the landscape to it and what it touches", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-node")).toHaveCount(7);
  await expect(page.locator("#mesh-drill-trail")).toBeHidden();

  await drillInto(page, "process:1");

  // Invoice, and what it touches at the depth already on screen (2 hops): its
  // application, both processes, the restricted placeholder, the mail worker, and
  // the decision Dunning uses. Not the unresolved archive dependency's siblings.
  await expect(page.locator("#mesh-drill-trail")).toBeVisible();
  // Where you are, and the way back out — the whole starmap is the first station.
  await expect(page.locator(".mesh-crumb-here")).toHaveText("Invoice");
  await expect(page.locator('.mesh-crumb[data-crumb="-1"]')).toHaveText("All");
  await expect(page.locator('[data-node-id="process:1"]')).toHaveCount(1);
  await expect(page.locator('[data-node-id="worker:c1"]')).toHaveCount(1);

  // The node it went into is the subject; everything else is there to explain it,
  // and is drawn as such — the same language a filtered picture uses.
  await expect(page.locator('[data-node-id="process:1"]')).not.toHaveClass(/mesh-context/);
  await expect(page.locator('[data-node-id="worker:c1"]')).toHaveClass(/mesh-context/);

  // And it is selected, so the panel explains it without a second click.
  await expect(page.locator(".mesh-panel-head")).toContainText("Invoice");
  await expect(page.locator("#mesh-count")).toContainText("hop(s)");
});

// The depth control decides the reach, so "just this" and "this and its whole chain"
// are the same gesture at two settings.
test("the drilldown reaches as far as the depth says", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/starmap");

  await setDepth(page, "1");
  await drillInto(page, "process:1");
  const oneHop = await page.locator(".mesh-node").count();
  // Invoice's own neighbours only: the decision that only Dunning uses is two away.
  await expect(page.locator('[data-node-id="decision:credit"]')).toHaveCount(0);

  await setDepth(page, "2");
  await expect(page.locator('[data-node-id="decision:credit"]')).toHaveCount(1);
  expect(await page.locator(".mesh-node").count()).toBeGreaterThan(oneHop);
});

// A drilldown is a place you are standing, so there has to be a way back — and
// coming back must not mean finding the node again.
test("leaving a drilldown restores the landscape with the node still marked", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/starmap");

  await page.locator('[data-node-id="worker:c1"] .mesh-body').dblclick();
  await expect(page.locator("#mesh-drill-trail")).toBeVisible();

  await page.locator('.mesh-crumb[data-crumb="-1"]').click();
  await expect(page.locator("#mesh-drill-trail")).toBeHidden();
  await expect(page.locator(".mesh-node")).toHaveCount(7);
  await expect(page.locator(".mesh-panel-head")).toContainText("ops-mail");

  // Escape is the other way out, the one it is everywhere else.
  await drillInto(page, "process:2");
  await expect(page.locator("#mesh-drill-trail")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.locator("#mesh-drill-trail")).toBeHidden();
  await expect(page.locator(".mesh-node")).toHaveCount(7);
});

// The search box and the drilldown ask the same kind of question, so only one is
// ever in force. Two narrowings compounding invisibly is how a picture ends up
// showing something nobody asked for and nobody can undo.
test("a search leaves the drilldown rather than compounding with it", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/starmap");

  await page.fill("#mesh-search", "invoice");
  await expect(page.locator("#mesh-count")).toContainText("match");

  // Drilling in clears the box, so the header is never describing one narrowing
  // while the picture shows another.
  await drillInto(page, "process:1");
  await expect(page.locator("#mesh-search")).toHaveValue("");
  await expect(page.locator("#mesh-count")).toContainText("hop(s)");

  // And typing goes back to asking about the whole landscape.
  await page.fill("#mesh-search", "dunning");
  await expect(page.locator("#mesh-drill-trail")).toBeHidden();
  await expect(page.locator("#mesh-count")).toContainText("match");
});

// Where exactly the work is parked. "Three tokens are parked" says there is a
// problem; naming the task and quoting what it said says where to go — which is the
// difference between a status view somebody glances at and one they act on.
test("a finding says which task the work is parked on", async ({ page }) => {
  installMock(page, statusGraph);
  await page.goto("/index.html#/panorama/starmap");

  const finding = page.locator(".mesh-findings");
  await expect(finding).toContainText("charge-card");
  await expect(finding).toContainText("ServiceTask");
  await expect(finding).toContainText("502 Bad Gateway");

  // Worst first, and a repeated element is one entry with a count rather than one
  // entry each.
  const rows = await page.locator(".mesh-findings .mesh-sites li").allTextContents();
  expect(rows[0]).toContain("charge-card");
  expect(rows[0]).toContain("2×");
  expect(rows[1]).toContain("notify-customer");
  // One incident on an element is not decorated with "1×" — a count that is always
  // there stops being read.
  expect(rows[1]).not.toContain("1×");

  // The selected node repeats it beside the picture, so acting on a finding does not
  // mean reading it in one panel and selecting it in another.
  await page.locator('[data-node-id="process:1"]').click();
  await expect(page.locator(".mesh-finding")).toContainText("charge-card");
});

// A node with a finding and no sites is the ordinary case for everything that is not
// a process: an incident belongs to a token, and only a process has tokens.
test("a finding with nowhere to point does not invent a place", async ({ page }) => {
  installMock(page, statusGraph);
  await page.goto("/index.html#/panorama/starmap");

  await page.locator('[data-node-id="process:2"]').click();
  await expect(page.locator(".mesh-finding")).toContainText("This worker cannot serve work");
  await expect(page.locator(".mesh-finding .mesh-sites")).toHaveCount(0);
});

// Attention has to look like a finding, not like a hint.
//
// It used to be drawn as a pale wash inside a thin ochre outline while critical was
// a solid disc — so the two classes differed in *form* as well as in colour, and the
// form was doing most of the work. A tinted outline beside a filled badge does not
// read as "less urgent", it reads as "not really a finding".
test("an attention finding is drawn as one, not as a hint", async ({ page }) => {
  installMock(page, statusGraph);
  await page.goto("/index.html#/panorama/starmap");

  const badge = (id) => page.locator(`[data-node-id="${id}"] .mesh-badge-dot`)
    .evaluate((el) => {
      const style = getComputedStyle(el);
      return { fill: style.fill, stroke: style.stroke };
    });

  // Both classes are a filled badge in their own colour — the fill is the colour,
  // not a wash of it, so neither is a ring around nothing.
  const attention = await badge("process:1");
  const critical = await badge("process:2");
  expect(attention.fill).toBe(attention.stroke);
  expect(critical.fill).toBe(critical.stroke);
  // And they are still two different findings: "it is broken" and "something inside
  // it went wrong" are what this view exists to tell apart.
  expect(attention.fill).not.toBe(critical.fill);

  // The glyph on each is legible against the badge it sits on, which is why they do
  // not share one: white on the red, near-black on the amber.
  const ink = (id) => page.locator(`[data-node-id="${id}"] .mesh-badge-glyph`)
    .evaluate((el) => getComputedStyle(el).fill);
  expect(await ink("process:1")).not.toBe(await ink("process:2"));

  // Colour is never the only channel (ADR-0211 §4): each class keeps its own glyph,
  // so the picture is readable without separating red from amber.
  const glyphs = await page.evaluate(() => ({
    attention: document.querySelector('[data-node-id="process:1"] .mesh-badge-glyph').textContent,
    critical: document.querySelector('[data-node-id="process:2"] .mesh-badge-glyph').textContent,
  }));
  expect(glyphs.attention).not.toBe(glyphs.critical);

  // Weight is the third channel: a node with a finding wears a heavier ring than one
  // without, which survives being small, being zoomed out, and being looked at by
  // somebody who does not separate the two hues at all.
  const ring = (id) => page.locator(`[data-node-id="${id}"] .mesh-body`)
    .evaluate((el) => parseFloat(getComputedStyle(el).strokeWidth));
  const quiet = await ring("decision:credit");
  expect(await ring("process:1")).toBeGreaterThan(quiet);
  expect(await ring("process:2")).toBeGreaterThan(quiet);
});

// Kinds are told apart by shape, which is the channel that survives what colour and
// size do not: a printout, a projector, and a reader who does not separate the hues.
test("each kind is drawn with its own outline", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/starmap");

  const outline = (id) => page.locator(`[data-node-id="${id}"] .mesh-body`)
    .evaluate((el) => el.tagName.toLowerCase());

  // A rect for the process, polygons for the rest, a circle for the application.
  expect(await outline("application:a1")).toBe("circle");
  expect(await outline("process:1")).toBe("rect");
  expect(await outline("worker:c1")).toBe("polygon");
  expect(await outline("decision:credit")).toBe("polygon");

  // The polygons are told apart by their corner count: three for the decision, six
  // for the worker, four for the placeholder that stands for an unknown kind.
  const corners = (id) => page.locator(`[data-node-id="${id}"] .mesh-body`)
    .evaluate((el) => el.getAttribute("points").trim().split(/\s+/).length);
  expect(await corners("decision:credit")).toBe(3);
  expect(await corners("worker:c1")).toBe(6);
  expect(await corners("restricted:1")).toBe(4);

  // An unresolved process is drawn in the silhouette of the deployment that should
  // have been there — the dashes say it is missing, the shape says what is.
  expect(await outline("unresolved:process:archive")).toBe("rect");
  await expect(page.locator('[data-node-id="unresolved:process:archive"] .mesh-body'))
    .toHaveAttribute("stroke-dasharray", "4 3");
});

// The legend is drawn by the same function the nodes are, so it cannot come to
// disagree with the picture it explains.
test("the legend shows the shapes it is explaining", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/starmap");
  await page.locator(".mesh-swatch").first().waitFor();

  const shapes = await page.evaluate(() => [...document.querySelectorAll(".mesh-swatch svg")]
    .map((svg) => {
      const body = svg.querySelector(".mesh-body");
      return body ? body.tagName.toLowerCase() : null;
    })
    .filter(Boolean));

  // One swatch per kind on screen, and between them every outline the canvas uses.
  expect(shapes).toContain("circle");
  expect(shapes).toContain("rect");
  expect(shapes.filter((tag) => tag === "polygon").length).toBeGreaterThan(1);
});

// A peer that stopped answering, on the landscape.
//
// Until now this view contacted nothing, so it declared *unreachable* and *stale*
// unproducible — which was true and useless: an operator scanning the landscape for
// trouble could not see that a whole server had gone away. Deployment targets are
// drawn now, and they are the only nodes here whose state comes from outside this
// process.
const peeredGraph = {
  nodes: [
    { id: "application:a1", kind: "application", name: "Billing", provenance: "derived",
      state: "healthy", severity: "ok", reason: "No work is parked." },
    { id: "target:t1", kind: "target", name: "Production", provenance: "derived",
      state: "unreachable", severity: "attention",
      reason: "This peer could not be reached." },
    { id: "target:t2", kind: "target", name: "Staging", provenance: "derived",
      state: "stale", severity: "attention",
      reason: "Last answered 240s ago and the refresh failed, so this is history rather than status." },
    { id: "target:t3", kind: "target", name: "Sandbox", provenance: "derived",
      state: "healthy", severity: "ok", reason: "This peer answered and identified itself." },
  ],
  edges: [],
  restricted: 0, clustered: false,
  status: { ok: 2, attention: 2, critical: 0, unknown: 0, unavailable: [] },
};

test("a peer that stopped answering is on the landscape", async ({ page }) => {
  installMock(page, peeredGraph);
  await page.goto("/index.html#/panorama/starmap");

  // Its own outline, so it is told from everything else without reading a word.
  const outline = (id) => page.locator(`[data-node-id="${id}"] .mesh-body`)
    .evaluate((el) => ({
      tag: el.tagName.toLowerCase(),
      corners: el.getAttribute("points")?.trim().split(/\s+/).length ?? 0,
    }));
  expect(await outline("target:t1")).toEqual({ tag: "polygon", corners: 5 });

  // Both failures are *attention*, never critical: "I could not reach it" and "it is
  // broken" are different findings, and a view that painted them alike loses its
  // credibility on the first network fault.
  await expect(page.locator('[data-node-id="target:t1"]')).toHaveClass(/mesh-sev-attention/);
  await expect(page.locator('[data-node-id="target:t2"]')).toHaveClass(/mesh-sev-attention/);
  await expect(page.locator('[data-node-id="target:t3"]')).toHaveClass(/mesh-sev-ok/);

  // And they beat, so a server going away is noticed rather than looked for.
  await expect(page.locator(".mesh-canvas")).toHaveClass(/mesh-beating/);
  await expect(page.locator('[data-node-id="target:t1"] .mesh-beat')).toHaveCount(1);
  await expect(page.locator('[data-node-id="target:t3"] .mesh-beat')).toHaveCount(0);

  // The findings list tells the two apart in words, which is the half a colour
  // cannot carry: nothing is known, versus something is known and may be wrong.
  const findings = page.locator(".mesh-findings");
  await expect(findings).toContainText("unreachable");
  await expect(findings).toContainText("stale");
  await expect(findings).toContainText("history rather than status");

  // Never the peer's address. That is this operator's map of where their
  // infrastructure lives, and a landscape is opened by anybody with modeler access.
  const drawn = await page.locator("#mesh-root").innerHTML();
  for (const leak of ["http://", "https://", ".test", "credential"]) {
    expect(drawn.includes(leak), leak).toBe(false);
  }
});

// The declaration is a property of the response, not of the build. A payload that
// went on saying "unreachable cannot happen here" beside a target reporting exactly
// that would be a contract nobody could rely on again.
test("a landscape with a peer stops claiming it cannot see", async ({ page }) => {
  installMock(page, peeredGraph);
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-legend")).toBeVisible();
  await expect(page.locator(".mesh-legend")).not.toContainText("Not watched here");

  // And the other way round: a landscape with no peer says so, and says what would
  // change it rather than only that it cannot.
  installMock(page, {
    ...peeredGraph,
    nodes: peeredGraph.nodes.filter((n) => n.kind !== "target"),
    status: {
      ok: 1, attention: 0, critical: 0, unknown: 0,
      unavailable: [
        { state: "unreachable", reason: "No deployment target is drawn here. Configure a deployment target and this landscape reports it." },
        { state: "stale", reason: "Only a peer's answer holds a freshness contract: configure a deployment target and this landscape reports it." },
      ],
    },
  });
  await page.reload();
  await expect(page.locator(".mesh-legend")).toContainText("Not watched here");
  await expect(page.locator(".mesh-legend")).toContainText("deployment target");
});

// Exporting the landscape (ADR-0211 §10). The file is the deliverable here, so the
// test takes the download and reads it: it has to be the whole landscape, and it
// has to carry the provenance the app shows beside the picture and a file cannot.
test("exports the landscape as a stamped, self-contained SVG", async ({ page }) => {
  installMock(page, {
    ...graph,
    observedAt: 1_700_000_000,
    status: {
      ok: 6, attention: 0, critical: 0, unknown: 1,
      unavailable: [{ state: "stale", reason: "No deployment target is configured." }],
    },
  });
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toBeVisible();

  // Zoomed in first, on purpose: a file cropped to the reader's window would drop
  // nodes and say nothing about it.
  await page.locator("#mesh-zoom-in").click();
  await page.locator("#mesh-zoom-in").click();
  await expect(page.locator(".mesh-canvas")).toHaveClass(/mesh-zoomed/);

  const [download] = await Promise.all([
    page.waitForEvent("download"),
    page.locator("#mesh-export-svg").click(),
  ]);
  expect(download.suggestedFilename()).toMatch(/^atlas-starmap-\d{8}-\d{4}\.svg$/);

  const stream = await download.createReadStream();
  const svg = await new Promise((resolve, reject) => {
    let out = "";
    stream.on("data", (chunk) => (out += chunk));
    stream.on("end", () => resolve(out));
    stream.on("error", reject);
  });

  // Every node in the payload, not the handful that were still in the window.
  expect([...svg.matchAll(/class="mesh-node/g)]).toHaveLength(graph.nodes.length);
  expect(svg).toContain("Billing");
  // The canvas itself is unzoomed and names everything. Read off the element's own
  // class list rather than off the file as a whole: the harvested stylesheet quotes
  // both class names in the rules it carries, so a substring search would find them
  // in a picture that is neither.
  const canvasClasses = svg.match(/class="([^"]*mesh-canvas[^"]*)"/)[1];
  expect(canvasClasses).not.toContain("mesh-zoomed");
  expect(canvasClasses).toContain("mesh-names-all");

  // The provenance §10 requires rendered into the artifact.
  expect(svg).toContain("Atlas starmap — the whole starmap");
  expect(svg).toContain("Observed 20");
  expect(svg).toContain("Source ");
  expect(svg).toContain("7 node(s) drawn");
  // Including what the picture cannot show: the placeholder, and the state this
  // build cannot produce at all. On screen these sit in the legend; the file has no
  // legend beside it, so they travel inside it.
  expect(svg).toContain("hidden by your access");
  expect(svg).toContain("Not watched here");
  expect(svg).toContain("No deployment target is configured.");
  // And what the two line styles mean. A file that travels has no legend beside it,
  // so a reader opening it next quarter has nowhere else to find out.
  expect(svg).toContain("Solid line");
  expect(svg).toContain("Dashed line");
});

// A filtered export is a real landscape and not *the* landscape, and the only place
// a later reader can learn that is the file itself.
test("an export of a filtered landscape says it is filtered", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toBeVisible();

  await page.locator("#mesh-search").fill("invoice");
  await expect(page.locator("#mesh-count")).toContainText("match");

  const [download] = await Promise.all([
    page.waitForEvent("download"),
    page.locator("#mesh-export-svg").click(),
  ]);
  const stream = await download.createReadStream();
  const svg = await new Promise((resolve) => {
    let out = "";
    stream.on("data", (chunk) => (out += chunk));
    stream.on("end", () => resolve(out));
  });

  expect(svg).toContain("filtered by “invoice”");
  expect(svg).toMatch(/\d of 7 node\(s\) drawn/);
  // This payload carries no observation time, and the file says that rather than
  // dating itself from the browser that saved it.
  expect(svg).toContain("Observation time not reported by this server");
});

// Impact analysis beyond the count (ADR-0211 §6). A landscape where one worker is
// what four processes reach, so the radius has a shape worth reporting.
const radiusGraph = {
  nodes: [
    { id: "process:1", kind: "process", name: "Invoice", provenance: "derived", processId: "invoice", version: 1, severity: "ok", state: "healthy" },
    { id: "process:2", kind: "process", name: "Dunning", provenance: "derived", processId: "dunning", version: 1, severity: "critical", state: "degraded", reason: "4 token(s) are parked.", incidents: 4 },
    { id: "process:3", kind: "process", name: "Reminder", provenance: "derived", processId: "reminder", version: 1, severity: "ok", state: "healthy" },
    { id: "process:4", kind: "process", name: "Signup", provenance: "derived", processId: "signup", version: 1, severity: "attention", state: "degraded", reason: "1 token is parked.", incidents: 1 },
    { id: "worker:mail", kind: "worker", name: "ops-mail", provenance: "derived", workerType: "mail", severity: "ok", state: "healthy" },
    { id: "decision:credit", kind: "decision", name: "Credit score", provenance: "derived", severity: "ok", state: "healthy" },
  ],
  edges: [
    { from: "process:1", to: "process:2", kind: "calls" },
    { from: "process:1", to: "worker:mail", kind: "uses" },
    { from: "process:2", to: "worker:mail", kind: "uses" },
    { from: "process:3", to: "worker:mail", kind: "uses" },
    { from: "process:4", to: "worker:mail", kind: "uses" },
    { from: "process:2", to: "decision:credit", kind: "uses" },
  ],
  restricted: 0,
  clustered: false,
  status: { ok: 4, attention: 1, critical: 1, unknown: 0, unavailable: [] },
};

// "Twelve depend on this" is a different sentence depending on how many of the
// twelve are already burning, and the panel now says which.
test("the impact answer says how bad the radius is and names it", async ({ page }) => {
  installMock(page, radiusGraph);
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toBeVisible();
  await setDepth(page, "all");
  await page.locator('[data-node-id="worker:mail"]').click();

  const panel = page.locator(".mesh-panel");
  await expect(panel.locator(".mesh-impact-count")).toContainText("4");
  // The mix, with the class named beside its colour.
  await expect(panel.locator(".mesh-impact-chip.mesh-sev-critical")).toContainText("1 critical");
  await expect(panel.locator(".mesh-impact-chip.mesh-sev-attention")).toContainText("1 attention");
  await expect(panel.locator(".mesh-impact-chip.mesh-sev-ok")).toContainText("2 ok");
  // Read for triage and never as cause.
  await expect(panel).toContainText("not what this node caused");

  // And the nodes themselves, worst first, so the three that matter do not have to
  // be hunted for among the highlighted circles.
  const named = panel.locator(".mesh-impact-go .mesh-impact-who");
  await expect(named).toHaveText(["Dunning", "Signup", "Invoice", "Reminder"]);

  // Clicking one goes there, exactly as a finding does.
  await panel.locator(".mesh-impact-go", { hasText: "Dunning" }).click();
  await expect(page.locator(".mesh-panel-head")).toContainText("Dunning");
});

// Direct and transitive are different facts, and the panel keeps them apart: the
// direct dependents are the ones whose owners get a call.
test("the panel tells direct dependents from the ones further out", async ({ page }) => {
  installMock(page, radiusGraph);
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toBeVisible();
  await setDepth(page, "all");
  await page.locator('[data-node-id="decision:credit"]').click();

  const panel = page.locator(".mesh-panel");
  await expect(panel).toContainText("1 directly");
  await expect(panel).toContainText("1 further out");
  await expect(panel.locator(".mesh-impact-go", { hasText: "Dunning" })).toContainText("directly");
  await expect(panel.locator(".mesh-impact-go", { hasText: "Invoice" })).toContainText("further out");
});

// The question a reader arrives with, which until now needed them to already
// suspect the right node: which of these would hurt most.
test("the landscape ranks its blast radii with nothing selected", async ({ page }) => {
  installMock(page, radiusGraph);
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toBeVisible();
  await setDepth(page, "all");

  const rank = page.locator(".mesh-rank");
  await expect(rank.locator(".mesh-rank-head")).toContainText("Biggest blast radius");
  await expect(rank.locator(".mesh-rank-who")).toHaveText([
    "ops-mail", "Credit score", "Dunning",
  ]);
  await expect(rank.locator(".mesh-rank-go").first()).toContainText("4");
  await expect(rank.locator(".mesh-rank-go").first()).toContainText("4 direct");

  // It follows the controls rather than fixing its own question, so it and the
  // panel are never two differently measured numbers on one page.
  await page.selectOption("#mesh-direction", "dependencies");
  await expect(rank.locator(".mesh-rank-head")).toContainText("Most dependent");
  await expect(rank.locator(".mesh-rank-who").first()).toHaveText("Invoice");

  // Clicking a row goes to the node, like every other list in this column.
  await rank.locator(".mesh-rank-go").first().click();
  await expect(page.locator(".mesh-panel-head")).toContainText("Invoice");
});

// A landscape whose processes call nothing has no radius to rank, and says that as
// a fact about its edges rather than as reassurance.
test("a landscape with no dependencies says there is nothing to rank", async ({ page }) => {
  installMock(page, {
    nodes: [
      { id: "application:a1", kind: "application", name: "Billing", provenance: "derived", severity: "ok", state: "healthy" },
      { id: "process:1", kind: "process", name: "Invoice", provenance: "derived", processId: "invoice", version: 1, severity: "ok", state: "healthy" },
    ],
    edges: [{ from: "application:a1", to: "process:1", kind: "contains" }],
    restricted: 0, clustered: false,
    status: { ok: 2, attention: 0, critical: 0, unknown: 0, unavailable: [] },
  });
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toBeVisible();

  const rank = page.locator(".mesh-rank");
  await expect(rank).toContainText("no radius to rank");
  // And why the one edge on screen did not count.
  await expect(rank).toContainText("Containment is not counted");
});

// Fit after arranging by hand (ADR-0211 §7). The layout skips its own fit while
// anything is pinned — fitting rescales every position and would drag the pins off
// the spots they were dropped on — so from the first drag onward the content and
// the world are two different boxes. Framing the world then shows the picture in a
// corner of a mostly empty sheet, which is what "Fit" exists to prevent.
test("Fit frames the arrangement, not the empty sheet around it", async ({ page }) => {
  installMock(page, meshOf(12));
  await page.goto("/index.html#/panorama/starmap");
  await page.locator(".mesh-node").first().waitFor();

  // Pin one node, which is all it takes for the layout to stop fitting.
  await dragBy(page, "process:3", 90, -60);
  await page.locator("#mesh-zoom-fit").click();

  const framing = await page.evaluate(() => {
    const svg = document.querySelector(".mesh-canvas");
    const v = svg.viewBox.baseVal;
    const at = [...svg.querySelectorAll(".mesh-node")].map((g) => {
      const m = /translate\(([-\d.]+),([-\d.]+)\)/.exec(g.getAttribute("transform"));
      return { x: +m[1], y: +m[2], r: +g.querySelector(".mesh-body").getAttribute("data-r") };
    });
    const left = Math.min(...at.map((n) => n.x - n.r)), right = Math.max(...at.map((n) => n.x + n.r));
    const top = Math.min(...at.map((n) => n.y - n.r)), bottom = Math.max(...at.map((n) => n.y + n.r));
    return { v: { x: v.x, y: v.y, w: v.width, h: v.height }, left, right, top, bottom };
  });

  // Everything drawn is inside the frame.
  expect(framing.left).toBeGreaterThanOrEqual(framing.v.x);
  expect(framing.top).toBeGreaterThanOrEqual(framing.v.y);
  expect(framing.right).toBeLessThanOrEqual(framing.v.x + framing.v.w);
  expect(framing.bottom).toBeLessThanOrEqual(framing.v.y + framing.v.h);

  // And it fills it: the content reaches most of the frame on at least one axis
  // rather than sitting in a corner of it. A frame twice the content's size on both
  // axes is the failure this test is about.
  const fillX = (framing.right - framing.left) / framing.v.w;
  const fillY = (framing.bottom - framing.top) / framing.v.h;
  expect(Math.max(fillX, fillY)).toBeGreaterThan(0.6);
});

// A node under the zoom controls takes no pointer: the panel is in front of the
// canvas, so the press lands on chrome and the node can be neither selected nor
// dragged. The fit holds that corner clear, which matters most in a filtered or
// drilled picture — few enough nodes that the fit puts one of them there, and even
// odds it is the one being reached for.
test("a node in the chrome's corner can still be picked up", async ({ page }) => {
  installMock(page);
  await page.setViewportSize({ width: 1280, height: 1000 });
  await page.goto("/index.html#/panorama/starmap");
  await page.locator(".mesh-canvas").waitFor();
  await page.locator("#mesh-search").fill("invoice");
  await expect(page.locator("#mesh-count")).toContainText("match");

  // Whichever node the fit put closest to the controls is the one at risk.
  const chrome = await page.locator(".mesh-zoom").boundingBox();
  const corner = { x: chrome.x + chrome.width / 2, y: chrome.y + chrome.height / 2 };
  const nearest = await page.evaluate((c) => {
    let best = null;
    for (const g of document.querySelectorAll(".mesh-node")) {
      const box = g.querySelector(".mesh-body").getBoundingClientRect();
      const d = Math.hypot(box.x + box.width / 2 - c.x, box.y + box.height / 2 - c.y);
      if (!best || d < best.d) best = { id: g.dataset.nodeId, d };
    }
    return best;
  }, corner);
  expect(nearest).toBeTruthy();

  // Every drawn node answers a hit test on its own body — none is behind the panel.
  const covered = await page.evaluate(() => [...document.querySelectorAll(".mesh-node")]
    .filter((g) => {
      const box = g.querySelector(".mesh-body").getBoundingClientRect();
      const hit = document.elementFromPoint(box.x + box.width / 2, box.y + box.height / 2);
      return hit?.closest?.("[data-node-id]")?.dataset?.nodeId !== g.dataset.nodeId;
    })
    .map((g) => g.dataset.nodeId));
  expect(covered).toEqual([]);

  // And the one nearest the chrome really does drag, neighbours making room for it.
  const before = await screenAt(page, nearest.id);
  await dragBy(page, nearest.id, -140, -90);
  const after = await screenAt(page, nearest.id);
  expect(Math.hypot(after.x - before.x, after.y - before.y)).toBeGreaterThan(60);
  await expect(page.locator(`[data-node-id="${nearest.id}"]`)).toHaveClass(/mesh-pinned/);
});

// Notation projections (ADR-0211 §8). The landscape may be spoken in ArchiMate's or
// C4's vocabulary. §8 allows exactly one thing here — a read-only projection with an
// explicit versioned mapping and reported loss — and forbids the thing it would
// otherwise become, which is a renderer toggle pretending to be a second notation.
test("the landscape can be read in ArchiMate's vocabulary, loss and all", async ({ page }) => {
  installMock(page, radiusGraph);
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toBeVisible();

  await page.selectOption("#mesh-notation", "archimate-3.2");

  // Every node says what the notation calls it, which is ArchiMate's corner icon
  // written out — an icon at this zoom is a smudge.
  const canvas = page.locator(".mesh-canvas");
  await expect(canvas).toContainText("[Application Process]");
  await expect(canvas).toContainText("[Application Service]");
  await expect(canvas).toContainText("[Application Function]");

  // Structure is a rectangle and behaviour a rounded one, which is ArchiMate's own
  // convention — and both are still inscribed in the circle the layout reserved.
  const worker = page.locator('[data-node-id="worker:mail"] .mesh-body');
  expect(await worker.evaluate((el) => el.tagName.toLowerCase())).toBe("rect");

  // It says it is a projection, names the mapping's version, and lists what it
  // drops. A picture in somebody else's vocabulary that does not name the vocabulary
  // reads as a model of it.
  const projection = page.locator(".mesh-projection");
  await expect(projection).toContainText("Projected into ArchiMate 3.2");
  await expect(projection).toContainText("mapping v1");
  await expect(projection).toContainText("read-only");
  await expect(projection).toContainText("Nothing on this landscape was modelled");
  await expect(projection.locator(".mesh-loss li")).not.toHaveCount(0);
  await expect(projection).toContainText("ArchiMate tells serving from triggering");

  // The legend keeps Atlas's own word beside the notation's, or a reader could not
  // get from the picture back to the thing it is about.
  await expect(page.locator(".mesh-legend")).toContainText("Application Process — Process");

  // And switching back is switching back: no annotations, Atlas's own shapes.
  await page.selectOption("#mesh-notation", "atlas");
  await expect(canvas).not.toContainText("[Application Process]");
  await expect(page.locator(".mesh-projection")).toHaveCount(0);
});

// C4 tells its types apart by the annotation rather than by silhouette, and the
// projection is faithful to that rather than inventing shapes C4 does not have.
test("C4 draws boxes and says which kind of box each one is", async ({ page }) => {
  installMock(page, radiusGraph);
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toBeVisible();
  await page.selectOption("#mesh-notation", "c4-projection");

  const canvas = page.locator(".mesh-canvas");
  await expect(canvas).toContainText("[Component]");
  await expect(page.locator(".mesh-projection")).toContainText("C4 separates its levels");
  await expect(page.locator(".mesh-projection")).toContainText("External systems are absent");
});

// A kind the notation has no word for keeps its own shape and is named as loss.
// Inventing an element for it would be the silent drop §8's theme ban exists to
// prevent — and a restricted placeholder is a finding about the picture rather than
// a piece of architecture, so no notation should have a word for it.
test("a kind the notation cannot express keeps its own shape", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toBeVisible();

  const shapeOf = (id) => page.locator(`[data-node-id="${id}"] .mesh-body`)
    .evaluate((el) => el.tagName.toLowerCase());
  const before = await shapeOf("restricted:1");

  await page.selectOption("#mesh-notation", "archimate-3.2");
  expect(await shapeOf("restricted:1")).toBe(before);
  await expect(page.locator(".mesh-projection")).toContainText("no ArchiMate element");
  // The process beside it did change, so this is a node the projection left alone
  // rather than a projection that did nothing.
  expect(await shapeOf("process:1")).toBe("rect");
});

// An exported projection has to carry that it is one. A C4-looking file that does
// not say it was projected from Atlas's own resources is the misrepresentation §8
// forbids, and the file is where nobody can ask.
test("an exported projection says so, and says what it drops", async ({ page }) => {
  installMock(page, { ...radiusGraph, observedAt: 1_700_000_000 });
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toBeVisible();
  await page.selectOption("#mesh-notation", "c4-projection");

  const [download] = await Promise.all([
    page.waitForEvent("download"),
    page.locator("#mesh-export-svg").click(),
  ]);
  const stream = await download.createReadStream();
  const svg = await new Promise((resolve) => {
    let out = "";
    stream.on("data", (chunk) => (out += chunk));
    stream.on("end", () => resolve(out));
  });

  expect(svg).toContain("Atlas starmap · C4");
  expect(svg).toContain("Projected into C4 (projection) (mapping v1)");
  expect(svg).toContain("nothing here was modelled");
  expect(svg).toContain("External systems are absent");
  // The key in the file speaks the projection too, or it explains a picture that is
  // no longer the one beside it — and keeps Atlas's own word next to C4's, so a
  // reader can get from the file back to the thing it is about.
  expect(svg).toContain("Component — Process");
});

// A saved view is the whole question somebody saved, and the vocabulary is part of
// the question: reopening a C4 landscape as an Atlas one answers a different one.
test("a saved view remembers the notation it was read in", async ({ page }) => {
  installMock(page, radiusGraph);
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toBeVisible();

  await page.selectOption("#mesh-notation", "archimate-3.2");
  await page.locator("#mesh-view-name").fill("As architecture");
  await page.locator("#mesh-view-save button").click();
  await expect(page.locator(".mesh-view-list")).toContainText("As architecture");

  await page.selectOption("#mesh-notation", "atlas");
  await expect(page.locator(".mesh-canvas")).not.toContainText("[Application Process]");

  await page.locator(".mesh-view-open").first().click();
  await expect(page.locator("#mesh-notation")).toHaveValue("archimate-3.2");
  await expect(page.locator(".mesh-canvas")).toContainText("[Application Process]");
});

// The landscape as a file another tool can open (ADR-0211 §8). The document is
// generated by the server from the same landscape the picture is drawn from, so the
// button hands the browser a plain same-origin navigation rather than building a
// second answer here.
test("the landscape can be downloaded as an ArchiMate model", async ({ page }) => {
  installMock(page, radiusGraph);
  // The one route the mock must not swallow: this is the file itself, and what is in
  // it is the server's answer rather than the picture's.
  await page.route("**/api/v1/panorama/mesh/archimate", (route) => route.fulfill({
    contentType: "application/xml",
    headers: { "content-disposition": 'attachment; filename="atlas-starmap-20260903.xml"' },
    body: '<?xml version="1.0"?><model xmlns="http://www.opengroup.org/xsd/archimate/3.0/" identifier="id-atlas-starmap"><name>Atlas starmap</name></model>',
  }));
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toBeVisible();

  const [download] = await Promise.all([
    page.waitForEvent("download"),
    page.locator("#mesh-export-archimate").click(),
  ]);
  expect(download.suggestedFilename()).toMatch(/^atlas-starmap.*\.xml$/);
});

// The picker offers what the server says it can draw, after the two ways of drawing
// that are the browser's own. A *vocabulary* the browser invented would be one the
// exported document knows nothing about; a weighting it invented is a rendering
// decision the server has no opinion on (ADR-0211 §8).
test("the notations on offer are the ones the server serves", async ({ page }) => {
  installMock(page, radiusGraph);
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toBeVisible();

  const offered = await page.locator("#mesh-notation option").evaluateAll(
    (options) => options.map((o) => o.value));
  expect(offered).toEqual([
    "atlas", "instances", "incidents", "incident-age", "archimate-3.2", "c4-projection",
  ]);
});

// And a landscape whose mapping cannot be read still draws. The projection is
// additive: losing it costs the vocabulary, never the picture.
test("a landscape draws even when the notations cannot be read", async ({ page }) => {
  page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path === "/api/v1/panorama/mesh") return route.fulfill({ json: graph });
    if (path === "/api/v1/panorama/notations") return route.fulfill({ status: 500, json: { error: "no" } });
    return route.fulfill({ json: [] });
  });
  await page.goto("/index.html#/panorama/starmap");

  await expect(page.locator(".mesh-canvas")).toBeVisible();
  await expect(page.locator(".mesh-node")).toHaveCount(graph.nodes.length);
  // The vocabulary that cannot be wrong about which vocabulary it is in, and the
  // weightings that never needed the server to name anything: they are drawn from
  // tallies already in the mesh payload.
  const offered = await page.locator("#mesh-notation option").evaluateAll(
    (options) => options.map((o) => o.value));
  expect(offered).toEqual(["atlas", "instances", "incidents", "incident-age"]);
});

// Going into a node, as a control rather than only as a gesture. A double-click is
// something you have to be told about, and the drilldown is what this view is for
// once the landscape is larger than a screenful.
test("the arrow goes into the selected node, and says when there is nowhere to go", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toBeVisible();

  const arrow = page.locator("#mesh-drill-in");
  // Nothing selected is nothing to go into. Dimmed rather than hidden, so a reader
  // can learn the control is there before they need it.
  await expect(arrow).toBeDisabled();
  await expect(arrow).toBeVisible();

  await page.locator('[data-node-id="process:1"] .mesh-body').click();
  await expect(arrow).toBeEnabled();
  await arrow.click();

  // The same place the double-click goes: the node and what it touches, with the
  // chip that says where you are standing.
  await expect(page.locator("#mesh-drill-trail")).toBeVisible();
  await expect(page.locator(".mesh-crumb-here")).toHaveText("Invoice");
  // A drilldown counts what is within the reach on screen rather than what matched a
  // term, and the count says so — which is the sentence that moved to its own line.
  await expect(page.locator("#mesh-count")).toContainText("hop(s)");
  // And it is not a place you can go into again.
  await expect(arrow).toBeDisabled();
});

// The header is a row of controls; how much of the landscape is on screen is a
// reading of the picture rather than a control on it. It used to sit in the row as
// the one item with no width of its own, so flexbox gave every control the size it
// asked for and squeezed the sentence into a column one word wide — and squeezed the
// search box into an empty stub with no visible purpose at all.
test("the header is one row of controls, with the count on its own line", async ({ page }) => {
  installMock(page);
  await page.setViewportSize({ width: 1400, height: 900 });
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toBeVisible();

  const count = page.locator(".mesh-subhead #mesh-count");
  await expect(count).toContainText("node(s)");

  const head = await page.locator(".mesh-head").boundingBox();
  const sub = await page.locator(".mesh-subhead").boundingBox();
  // Below, not beside.
  expect(sub.y).toBeGreaterThanOrEqual(head.y + head.height - 1);
  // Right-aligned: the text ends where the row does rather than starting where it does.
  const text = await count.boundingBox();
  expect(sub.x + sub.width - (text.x + text.width)).toBeLessThan(4);
  expect(text.x - sub.x).toBeGreaterThan(40);

  // And the controls are on one line: every one of them shares the header's row.
  const boxes = await page.locator(
    ".mesh-head h1, .mesh-search, #mesh-drill-in, .mesh-notation-pick, .mesh-export"
  ).evaluateAll((els) => els.map((el) => el.getBoundingClientRect().top));
  expect(Math.max(...boxes) - Math.min(...boxes)).toBeLessThan(20);

  // The search box is a control somebody can use, not the few-pixel stub it became
  // once the header filled up.
  const box = await page.locator(".mesh-search").boundingBox();
  expect(box.width).toBeGreaterThan(150);
});

// The two line styles on the canvas (ADR-0211 §4 — a channel that carries meaning
// has to be named somewhere a reader can find it). Shape and colour were both in the
// key; the dashes were not, so an edge could be seen to be different without any way
// of finding out from what.
test("the key says what each line style means, and only for the ones drawn", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toBeVisible();

  const rules = page.locator(".mesh-rules");
  await expect(rules).toContainText("Solid line — calls");
  await expect(rules).toContainText("Dashed line — uses");
  await expect(rules).toContainText("Dotted line — belongs to");

  // Drawn by the rules that drew the canvas, not by a copy of them: each swatch
  // carries the edge's own class, and what a reader sees is the computed stroke.
  await expect(rules.locator("svg line")).toHaveCount(3);
  const dashes = await rules.locator("svg line").evaluateAll(
    (els) => els.map((el) => getComputedStyle(el).strokeDasharray));
  // Three distinct strokes, and the canvas draws each kind with the same one.
  expect(new Set(dashes).size).toBe(3);
  const canvas = await page.locator(".mesh-edges line").evaluateAll((els) => {
    const seen = {};
    for (const el of els) {
      const kind = [...el.classList].find((c) => c.startsWith("mesh-edge-"));
      seen[kind] = getComputedStyle(el).strokeDasharray;
    }
    return seen;
  });
  expect(canvas["mesh-edge-calls"]).toBe(dashes[0]);
  expect(canvas["mesh-edge-uses"]).toBe(dashes[1]);
  expect(canvas["mesh-edge-contains"]).toBe(dashes[2]);

  // And only what is on screen. A landscape of applications alone has no containment
  // and no dependency drawn between them, so none of the three is claimed.
  await installMock(page, {
    nodes: [
      { id: "application:a1", kind: "application", name: "Billing", provenance: "derived" },
      { id: "application:a2", kind: "application", name: "Ledger", provenance: "derived" },
    ],
    edges: [], restricted: 0, clustered: false,
  });
  await page.reload();
  await expect(page.locator(".mesh-canvas")).toBeVisible();
  await expect(page.locator(".mesh-rules")).toHaveCount(0);
});

// A landscape of a size somebody would actually open. The small fixture above says
// what the view does; this one says whether it can be read.
function estate(apps = 6, procsPer = 4) {
  const nodes = [], edges = [];
  for (let a = 0; a < apps; a++) {
    const app = `application:a${a}`;
    nodes.push({ id: app, kind: "application", name: `App ${a}`, provenance: "derived" });
    for (let p = 0; p < procsPer; p++) {
      const pid = `process:${a}-${p}`;
      nodes.push({
        id: pid, kind: "process", name: `Process ${a}-${p}`, provenance: "derived",
        application: app, processId: `p${a}${p}`, version: 1,
      });
      edges.push({ from: app, to: pid, kind: "contains" });
      if (p > 0) edges.push({ from: `process:${a}-${p - 1}`, to: pid, kind: "calls" });
      if (p % 2 === 0) {
        const w = `worker:w${a}-${p}`;
        nodes.push({ id: w, kind: "worker", name: `worker-${a}-${p}`, provenance: "derived", workerType: "mail" });
        edges.push({ from: pid, to: w, kind: "uses" });
      }
    }
  }
  return { nodes, edges, restricted: 0, clustered: false };
}

// The landscape is the one view where the centred reading column costs more than it
// gives: everything on the canvas is sized by fitting a world into it, so width the
// column withholds comes off every node, every name and every gap at once.
test("the landscape gets the whole window rather than the reading column", async ({ page }) => {
  installMock(page, estate());
  await page.setViewportSize({ width: 1400, height: 900 });
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toBeVisible();

  const canvas = await page.locator(".mesh-canvas").boundingBox();
  // Wider than the whole content column used to be, never mind the canvas inside it.
  expect(canvas.width).toBeGreaterThan(1000);

  // And only here. Leaving the landscape puts the column back, or every other view
  // in the app would have been widened by a Panorama change.
  await page.goto("/index.html#/panorama/models");
  await expect(page.locator("main")).toBeVisible();
  const main = await page.locator("main").boundingBox();
  expect(main.width).toBeLessThanOrEqual(1121);
});

// What "too close together" actually was, measured: how much clear space a node has
// to its nearest neighbour, in the pixels a reader sees. The layout is seeded, so
// this is one number rather than a distribution — the same graph settles the same way
// on every load.
test("a landscape of forty nodes keeps room between them", async ({ page }) => {
  installMock(page, estate());
  await page.setViewportSize({ width: 1400, height: 900 });
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toBeVisible();
  await expect(page.locator(".mesh-node")).toHaveCount(42);

  const gaps = await page.evaluate(() => {
    const nodes = [...document.querySelectorAll(".mesh-node")].map((g) => {
      const box = g.querySelector(".mesh-body").getBoundingClientRect();
      return { x: box.x + box.width / 2, y: box.y + box.height / 2, r: Math.max(box.width, box.height) / 2 };
    });
    // Edge to edge, not centre to centre: a hub is drawn larger than a leaf, and the
    // space a reader sees is what is left between the outlines.
    return nodes.map((a, i) => Math.min(...nodes
      .filter((_, j) => j !== i)
      .map((b) => Math.hypot(a.x - b.x, a.y - b.y) - a.r - b.r))).sort((p, q) => p - q);
  });

  // No node is crowded: the tightest pair in the picture still has more clear space
  // between them than the larger of the two is wide.
  expect(gaps[0]).toBeGreaterThan(30);
  // And the picture as a whole is airy rather than merely non-overlapping. This is
  // the number the change moved: 56px between the average nearest pair in the
  // centred column, 72px now, with the nodes drawn the same size as before.
  expect(gaps[Math.floor(gaps.length / 2)]).toBeGreaterThan(66);
});

// An exported landscape is a picture with a provenance stamp under it, and the two
// have to stay apart. They did not: the harvested stylesheet carries `.mesh-canvas`,
// which is 100% of the surface the canvas fills in the page, and a CSS property beats
// the width and height the export puts on the clone — so the picture was laid out
// against the whole file, letterboxed inside it, and hung its lowest names across the
// stamp while leaving a dead strip along the top.
//
// Checked by rendering the file, because that is the only place the bug exists: every
// coordinate in it was right, and what went wrong was the box they were resolved in.
test("an exported landscape stays inside its own band", async ({ page }) => {
  installMock(page, estate());
  await page.setViewportSize({ width: 1600, height: 950 });
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toBeVisible();

  const [download] = await Promise.all([
    page.waitForEvent("download"),
    page.locator("#mesh-export-svg").click(),
  ]);
  const stream = await download.createReadStream();
  const svg = await new Promise((resolve, reject) => {
    let out = "";
    stream.on("data", (chunk) => (out += chunk));
    stream.on("end", () => resolve(out));
    stream.on("error", reject);
  });

  // Served as its own document rather than injected into this one: an <svg> inside a
  // page inherits the page's box, which is the very thing under test.
  await page.route("**/exported-landscape.svg", (route) =>
    route.fulfill({ contentType: "image/svg+xml", body: svg }));
  await page.goto("/exported-landscape.svg");

  const laid = await page.evaluate(() => {
    const canvas = document.querySelector(".mesh-canvas");
    // The rule the stamp is written under, drawn across the full width at the
    // picture's bottom edge.
    const rule = [...document.querySelectorAll("line")]
      .find((l) => l.getAttribute("x1") === "0").getBoundingClientRect().top;
    const nodes = [...document.querySelectorAll(".mesh-node")]
      .map((g) => g.getBoundingClientRect());
    return {
      rule,
      band: canvas.getBoundingClientRect().height,
      lowest: Math.max(...nodes.map((b) => b.bottom)),
      highest: Math.min(...nodes.map((b) => b.top)),
    };
  });

  // Nothing the picture draws reaches the stamp.
  expect(laid.lowest).toBeLessThan(laid.rule);
  // And the picture is not sitting in the middle of a taller box: what it draws fills
  // the band it was given, give or take the margin a name needs. Without this the
  // overlap comes back the moment the picture is centred in something else.
  expect(laid.lowest - laid.highest).toBeGreaterThan(laid.rule * 0.9);
});

// Where the key sits. It is a reference — consulted while looking at the picture,
// not read on the way to it — so it belongs under the canvas rather than between the
// controls and the thing they control.
test("the key sits under the picture, and the zoom controls stay on it", async ({ page }) => {
  installMock(page, estate());
  await page.setViewportSize({ width: 1600, height: 950 });
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toBeVisible();

  const box = async (sel) => await page.locator(sel).boundingBox();
  const canvas = await box(".mesh-surface");
  const legend = await box(".mesh-legend");

  // Under it, and in its column: the key explains the picture, so it stays the
  // picture's width rather than dropping below whichever column is taller.
  expect(legend.y).toBeGreaterThanOrEqual(canvas.y + canvas.height - 1);
  expect(Math.abs(legend.width - canvas.width)).toBeLessThan(4);
  expect(Math.abs(legend.x - canvas.x)).toBeLessThan(4);

  // And the zoom panel still floats in the canvas's own corner. It is positioned
  // against an ancestor, so giving that ancestor the key as well sinks the buttons to
  // the bottom of the key — off the picture they act on.
  const zoom = await box(".mesh-zoom");
  expect(zoom.y).toBeGreaterThan(canvas.y);
  expect(zoom.y + zoom.height).toBeLessThanOrEqual(canvas.y + canvas.height);
  expect(zoom.x + zoom.width).toBeLessThanOrEqual(canvas.x + canvas.width);
  // Still wired to the picture rather than merely present.
  await expect(page.locator("#mesh-zoom-in")).toBeVisible();
  await page.locator("#mesh-zoom-in").click();
  await expect(page.locator(".mesh-canvas")).toHaveClass(/mesh-zoomed/);
});

// A maintenance window: several nodes taken down together (ADR-0211 §6 asked of a
// set). The landscape is built so the overlap is a fact rather than a guess — Shared
// calls both P1 and P2, so it stops either way and can only stop once.
const windowMesh = {
  nodes: [
    { id: "process:p1", kind: "process", name: "P1", provenance: "derived", processId: "p1", version: 1 },
    { id: "process:p2", kind: "process", name: "P2", provenance: "derived", processId: "p2", version: 1 },
    { id: "process:onlyA", kind: "process", name: "OnlyA", provenance: "derived", processId: "onlya", version: 1 },
    { id: "process:onlyB", kind: "process", name: "OnlyB", provenance: "derived", processId: "onlyb", version: 1 },
    { id: "process:shared", kind: "process", name: "Shared", provenance: "derived", processId: "shared", version: 1 },
  ],
  edges: [
    { from: "process:shared", to: "process:p1", kind: "calls" },
    { from: "process:shared", to: "process:p2", kind: "calls" },
    { from: "process:onlyA", to: "process:p1", kind: "calls" },
    { from: "process:onlyB", to: "process:p2", kind: "calls" },
  ],
  restricted: 0,
  clustered: false,
};

async function openWindow(page) {
  installMock(page, windowMesh);
  await page.setViewportSize({ width: 1400, height: 900 });
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toBeVisible();
  await page.locator('[data-node-id="process:p1"] .mesh-body').click();
  await page.locator('[data-node-id="process:p2"] .mesh-body').click({ modifiers: ["Control"] });
  await expect(page.locator(".mesh-panel")).toContainText("Maintenance window");
}

test("a window says what the evening costs, not what each change costs", async ({ page }) => {
  await openWindow(page);
  const panel = page.locator(".mesh-panel");
  await expect(panel).toContainText("2 node(s) going down together");

  // The union. Three things stop — Shared, OnlyA, OnlyB — and Shared stops once.
  await expect(panel.locator(".mesh-impact-count")).toContainText("3");
  // And the comparison, which is the whole reason to plan a window rather than two
  // changes: 4 apart, 3 together, because one node is in both radii.
  await expect(panel).toContainText("One at a time these come to");
  const compare = await panel.locator(".mesh-note", { hasText: "One at a time" }).textContent();
  expect(compare.replace(/\s+/g, " ")).toContain("come to 4");
  expect(compare.replace(/\s+/g, " ")).toContain("together 3");
  expect(compare.replace(/\s+/g, " ")).toContain("1 node(s) sit in more than one radius");

  // Each member carries what it costs on its own, which is what says whether one of
  // them is driving the window.
  await expect(panel.locator(".mesh-window-item")).toHaveCount(2);
  await expect(panel.locator(".mesh-window-item").first()).toContainText("2 on its own");

  // The picture says which nodes are the ones going down, as against the ones that
  // go with them.
  await expect(page.locator(".mesh-picked")).toHaveCount(2);
  await expect(page.locator('[data-node-id="process:shared"]')).toHaveClass(/mesh-in-impact/);
  await expect(page.locator('[data-node-id="process:shared"]')).not.toHaveClass(/mesh-picked/);
});

test("a window comes apart the way it was built", async ({ page }) => {
  await openWindow(page);

  // From the panel, without a modifier: a set assembled by clicking has to be
  // unbuildable by clicking.
  await page.locator('[data-window-drop="process:p2"]').click();
  await expect(page.locator(".mesh-panel")).not.toContainText("Maintenance window");
  await expect(page.locator(".mesh-panel-head")).toContainText("P1");
  await expect(page.locator(".mesh-picked")).toHaveCount(1);

  // And with one, back on the canvas.
  await page.locator('[data-node-id="process:p2"] .mesh-body').click({ modifiers: ["Control"] });
  await expect(page.locator(".mesh-panel")).toContainText("Maintenance window");
  await page.locator("[data-window-clear]").click();
  await expect(page.locator(".mesh-panel")).toContainText("Nothing selected");
  await expect(page.locator(".mesh-picked")).toHaveCount(0);
});

// A window is rings on two nodes and nothing else. On screen the panel says what they
// are; a file has no panel, and §10 is the rule that it has to carry what it is.
test("an exported window says which nodes it is about", async ({ page }) => {
  await openWindow(page);
  const [download] = await Promise.all([
    page.waitForEvent("download"),
    page.locator("#mesh-export-svg").click(),
  ]);
  const stream = await download.createReadStream();
  const svg = await new Promise((resolve, reject) => {
    let out = "";
    stream.on("data", (chunk) => (out += chunk));
    stream.on("end", () => resolve(out));
    stream.on("error", reject);
  });
  expect(svg).toContain("Maintenance window");
  expect(svg).toContain("P1, P2");
  expect(svg).toContain("going down");
  // The rings survive into the file: they are what the sentence is about.
  expect(svg.match(/mesh-picked/g)?.length).toBeGreaterThanOrEqual(2);
});

// The view is called Starmap. The name is what a reader looks for in the nav and on
// the page, and it has to be the same word in both.
test("the view is called Starmap, in the nav and on the page", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toBeVisible();
  await expect(page.locator("#mesh-root h1")).toHaveText("Starmap");
  await expect(page.locator("#topnav a.active")).toHaveText("Starmap");
});

// Renaming a view renames its URL, and every bookmark, saved link and pasted URL
// from before the rename is still a link to it. Rewritten rather than served under
// two names — the same thing the Workers page does with its pre-ADR-0203 spelling.
test("the view's previous URL still lands on it", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/landscape");
  await expect(page.locator(".mesh-canvas")).toBeVisible();
  // On the new URL, so a reader who bookmarks from here bookmarks the current name.
  expect(new URL(page.url()).hash).toBe("#/panorama/starmap");
  await expect(page.locator("#mesh-root h1")).toHaveText("Starmap");
});

// How much is running, on the picture (ADR-0083's summary columns, on the Starmap).
// Off by default and asked for by name — a structural picture that always carried it
// would be a status board with arrows — and asked for in the notation picker, because
// what it changes is how the landscape is drawn: the nodes are sized by load instead
// of by connectivity, and the numbers come with them (ADR-0211 §8).
const runningMesh = {
  nodes: [
    { id: "application:a1", kind: "application", name: "Billing", provenance: "derived" },
    { id: "process:1", kind: "process", name: "Invoice", provenance: "derived", application: "application:a1", processId: "invoice", version: 1, runtime: { running: 12, finished: 431, lastActivity: 0 } },
    { id: "process:2", kind: "process", name: "Dunning", provenance: "derived", application: "application:a1", processId: "dunning", version: 1, runtime: { running: 0, finished: 7 } },
    { id: "worker:c1", kind: "worker", name: "ops-mail", provenance: "derived", workerType: "mail" },
  ],
  edges: [
    { from: "application:a1", to: "process:1", kind: "contains" },
    { from: "application:a1", to: "process:2", kind: "contains" },
    { from: "process:1", to: "worker:c1", kind: "uses" },
  ],
  restricted: 0,
  clustered: false,
};

test("the count on the canvas is asked for, and only where there is one", async ({ page }) => {
  installMock(page, runningMesh);
  await page.setViewportSize({ width: 1400, height: 900 });
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toBeVisible();

  // Off by default: the structural picture is what this view is for.
  await expect(page.locator(".mesh-runs")).toHaveCount(0);

  await page.selectOption("#mesh-notation", "instances");
  // On the busy process, and on nothing else. An idle one carries no number — on a
  // landscape of four hundred, "0 running" four hundred times hides the eleven
  // numbers somebody turned this on to find.
  await expect(page.locator(".mesh-runs")).toHaveCount(1);
  await expect(page.locator('[data-node-id="process:1"] .mesh-runs')).toHaveText("12 running");
  // And the legend says what the absence means, so it is not read as "not measured".
  await expect(page.locator(".mesh-legend")).toContainText("carries no number");

  await page.selectOption("#mesh-notation", "atlas");
  await expect(page.locator(".mesh-runs")).toHaveCount(0);
});

// The weighting itself: size stops being structure and becomes load. This is the
// whole of what the entry is for, so it is checked on the drawn radii rather than
// only on the arithmetic beside them.
test("on the instance weighting a node is sized by what is running on it", async ({ page }) => {
  installMock(page, runningMesh);
  await page.setViewportSize({ width: 1400, height: 900 });
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toBeVisible();

  // data-r is the radius the layout reserved, which is the number every other part
  // of the picture reads back — so it is the honest thing to assert against.
  const radius = (id) => page.locator(`[data-node-id="${id}"] .mesh-body`)
    .evaluate((el) => Number(el.getAttribute("data-r")));

  // Structurally, an application is the largest thing on the picture and a worker
  // among the smallest. That is the reading this weighting deliberately replaces.
  expect(await radius("application:a1")).toBeGreaterThan(await radius("process:1"));

  await page.selectOption("#mesh-notation", "instances");

  const busy = await radius("process:1");
  const idle = await radius("process:2");
  const worker = await radius("worker:c1");
  const app = await radius("application:a1");

  // The only node running anything is the largest, ahead of the application that
  // holds it: on this picture size is load, and an application's load sits on the
  // processes inside it.
  expect(busy).toBeGreaterThan(app);
  expect(busy).toBeGreaterThan(idle * 1.5);
  // And nothing has vanished. Everything with nothing running sits on one floor,
  // which is a node somebody can see and click rather than a gap.
  expect(idle).toBe(worker);
  expect(idle).toBe(app);
  expect(idle).toBeGreaterThan(8);

  // The key says what size means here, and names the reference the areas are
  // measured against — an area with no unit is a decoration.
  await expect(page.locator(".mesh-legend")).toContainText("Size is load here, not structure");
  await expect(page.locator(".mesh-legend")).toContainText("12");

  // Switching back is switching back: the structural reading returns intact.
  await page.selectOption("#mesh-notation", "atlas");
  expect(await radius("application:a1")).toBeGreaterThan(await radius("process:1"));
});

// The other question the same channel can answer, and the one somebody opening a
// landscape in the morning actually has: not where the work is, but where it is
// stuck. The severity badges already say *which* nodes have a finding; what they
// cannot say is how much is parked behind each, and a process holding four hundred
// stuck tokens wears the same badge as one holding a single retry.
const parkedMesh = {
  nodes: [
    { id: "application:a1", kind: "application", name: "Billing", provenance: "derived" },
    { id: "process:1", kind: "process", name: "Invoice", provenance: "derived", application: "application:a1", processId: "invoice", version: 1, severity: "critical", state: "degraded", incidents: 41, runtime: { running: 3, finished: 10 } },
    { id: "process:2", kind: "process", name: "Dunning", provenance: "derived", application: "application:a1", processId: "dunning", version: 1, severity: "attention", state: "degraded", incidents: 2, runtime: { running: 120, finished: 7 } },
    { id: "worker:c1", kind: "worker", name: "ops-mail", provenance: "derived", workerType: "mail" },
  ],
  edges: [
    { from: "application:a1", to: "process:1", kind: "contains" },
    { from: "application:a1", to: "process:2", kind: "contains" },
    // Dunning calls Invoice, so the worst-parked process is also one something else
    // needs — which is what makes the reach beside its count worth printing.
    { from: "process:2", to: "process:1", kind: "calls" },
    { from: "process:1", to: "worker:c1", kind: "uses" },
  ],
  restricted: 0,
  clustered: false,
};

test("on the incident weighting a node is sized by what is parked on it", async ({ page }) => {
  installMock(page, parkedMesh);
  await page.setViewportSize({ width: 1400, height: 900 });
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toBeVisible();

  const radius = (id) => page.locator(`[data-node-id="${id}"] .mesh-body`)
    .evaluate((el) => Number(el.getAttribute("data-r")));

  await page.selectOption("#mesh-notation", "incidents");

  // The badly parked process is the largest thing on the picture — ahead of the one
  // that is busier but healthy, which is the whole distinction the badges cannot draw.
  const parked = await radius("process:1");
  const nearlyFine = await radius("process:2");
  expect(parked).toBeGreaterThan(nearlyFine * 1.5);
  expect(nearlyFine).toBeGreaterThan(await radius("worker:c1"));

  // The numbers under the names are incidents now, not instances: one question at a
  // time, and the canvas says which.
  await expect(page.locator('[data-node-id="process:1"] .mesh-runs'))
    .toHaveText("41 incident(s)");
  await expect(page.locator('[data-node-id="process:2"] .mesh-runs'))
    .toHaveText("2 incident(s)");
  await expect(page.locator(".mesh-runs")).toHaveCount(2);
  await expect(page.locator(".mesh-legend")).toContainText("Size is trouble here, not structure");

  // And the two weightings are alternatives rather than layers. On the instance
  // picture the busier-but-healthy process is the larger one, which is the opposite
  // ranking — the same nodes, a different question.
  await page.selectOption("#mesh-notation", "instances");
  expect(await radius("process:2")).toBeGreaterThan(await radius("process:1"));
  await expect(page.locator('[data-node-id="process:2"] .mesh-runs')).toHaveText("120 running");
  await expect(page.locator(".mesh-legend")).toContainText("Size is load here, not structure");
});

// A healthy estate is the case a trouble picture has to get right: flat is the
// answer, not a missing one, and the key says so rather than leaving a reader to
// wonder whether anything was measured at all.
test("an estate with nothing parked draws flat, and says that is the finding", async ({ page }) => {
  installMock(page, runningMesh);
  await page.setViewportSize({ width: 1400, height: 900 });
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toBeVisible();

  await page.selectOption("#mesh-notation", "incidents");

  const radii = await page.locator(".mesh-node .mesh-body").evaluateAll(
    (els) => els.map((el) => Number(el.getAttribute("data-r"))));
  expect(new Set(radii).size).toBe(1);
  expect(radii[0]).toBeGreaterThan(8);
  await expect(page.locator(".mesh-runs")).toHaveCount(0);
  await expect(page.locator(".mesh-legend")).toContainText("nothing on this landscape is parked at all");
});

// How long, not how much — and the two rank the same estate the opposite way round,
// which is the whole reason this weighting exists. Four hundred incidents from the
// last five minutes is a worker that has just fallen over and drains itself once
// somebody restarts it; three standing since Friday is a process nobody is coming
// back to, and a count puts that one last.
//
// The timestamps are minted at test time rather than fixed at module load: an age is
// measured against the clock at render time, and a value frozen at import drifts into
// a different bucket behind a long suite — a flake in the test rather than a fault in
// the view.
function agedMesh() {
  const now = Date.now();
  const ago = (ms) => (now - ms) * 1e6;
  return {
    nodes: [
      { id: "application:a1", kind: "application", name: "Billing", provenance: "derived" },
      // The loud one: hundreds parked, all of it in the last few minutes.
      { id: "process:1", kind: "process", name: "Invoice", provenance: "derived", application: "application:a1", processId: "invoice", version: 1, severity: "critical", state: "degraded", incidents: 400, oldestIncident: ago(4 * 60_000) },
      // The quiet one: two tokens, standing since the middle of last week.
      { id: "process:2", kind: "process", name: "Dunning", provenance: "derived", application: "application:a1", processId: "dunning", version: 1, severity: "attention", state: "degraded", incidents: 2, oldestIncident: ago(5 * 86_400_000) },
      // Parked, and holding only incidents this engine never dated. It carries a
      // count and no age, which must read as "not known" rather than as 1970.
      { id: "process:3", kind: "process", name: "Chase", provenance: "derived", application: "application:a1", processId: "chase", version: 1, severity: "attention", state: "degraded", incidents: 7 },
      { id: "worker:c1", kind: "worker", name: "ops-mail", provenance: "derived", workerType: "mail" },
    ],
    edges: [
      { from: "application:a1", to: "process:1", kind: "contains" },
      { from: "application:a1", to: "process:2", kind: "contains" },
      { from: "application:a1", to: "process:3", kind: "contains" },
      { from: "process:1", to: "worker:c1", kind: "uses" },
    ],
    restricted: 0,
    clustered: false,
  };
}

test("the age weighting ranks the estate by how long, not by how much", async ({ page }) => {
  installMock(page, agedMesh());
  await page.setViewportSize({ width: 1400, height: 900 });
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toBeVisible();

  const radius = (id) => page.locator(`[data-node-id="${id}"] .mesh-body`)
    .evaluate((el) => Number(el.getAttribute("data-r")));

  // By count, the four-hundred-incident process dominates.
  await page.selectOption("#mesh-notation", "incidents");
  expect(await radius("process:1")).toBeGreaterThan(await radius("process:2"));
  await expect(page.locator(".mesh-rank-who").first()).toHaveText("Invoice");

  // By age, the two tokens standing since last week do — the opposite ordering of the
  // same estate, and the one that decides what somebody actually does next.
  await page.selectOption("#mesh-notation", "incident-age");
  expect(await radius("process:2")).toBeGreaterThan(await radius("process:1"));
  await expect(page.locator('[data-node-id="process:2"] .mesh-runs')).toHaveText("stuck 5 d");
  await expect(page.locator('[data-node-id="process:1"] .mesh-runs')).toHaveText("stuck 4 min");

  // A node the engine never dated carries no number and sits at the floor: "not
  // known" is a different fact from "raised at the epoch", and drawing it as the
  // oldest trouble on the estate would be the picture inventing one.
  await expect(page.locator('[data-node-id="process:3"] .mesh-runs')).toHaveCount(0);
  expect(await radius("process:3")).toBe(await radius("worker:c1"));

  await expect(page.locator(".mesh-legend")).toContainText("Size is age here, not structure");
  await expect(page.locator(".mesh-rank-head")).toContainText("Stuck longest");
  await expect(page.locator(".mesh-rank-who").first()).toHaveText("Dunning");
  await expect(page.locator(".mesh-rank-go").first()).toContainText("stuck 5 d");
});

// The exact age, in the panel, for whichever node is selected — the number a circle
// cannot give, beside the count it belongs to. Stated whatever the picture is drawn
// with, like the instance tally beside it.
test("the panel dates the oldest token a process is holding", async ({ page }) => {
  installMock(page, agedMesh());
  await page.setViewportSize({ width: 1400, height: 900 });
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toBeVisible();

  await page.locator('[data-node-id="process:2"] .mesh-body').click();
  await expect(page.locator(".mesh-panel .mesh-parked-since")).toContainText("5 d ago");

  // And says nothing where there is nothing to date, rather than a date it does not
  // have. The count is still on the node; only the age is missing.
  await page.locator('[data-node-id="process:3"] .mesh-body').click();
  await expect(page.locator(".mesh-panel .mesh-parked-since")).toHaveCount(0);
  await expect(page.locator(".mesh-panel .mesh-finding")).toBeVisible();
});

// The column beside the picture ranks by whatever the picture is sized by. Two
// orderings on one screen — the largest circle and the first row being different
// nodes — is a contradiction nothing on screen resolves.
test("the ranking beside the picture follows the weighting", async ({ page }) => {
  installMock(page, parkedMesh);
  await page.setViewportSize({ width: 1400, height: 900 });
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toBeVisible();

  const rank = page.locator(".mesh-rank");
  // With no weighting it is the blast ranking it has always been.
  await expect(rank.locator(".mesh-rank-head")).toContainText("Biggest blast radius");

  await page.selectOption("#mesh-notation", "incidents");
  await expect(rank.locator(".mesh-rank-head")).toContainText("Most parked");
  await expect(rank.locator(".mesh-rank-who")).toHaveText(["Invoice", "Dunning"]);
  // The exact number, which two areas cannot give — and the reach beside it, which is
  // what turns a count into a priority.
  await expect(rank.locator(".mesh-rank-go").first()).toContainText("41 incident(s)");
  await expect(rank.locator(".mesh-rank-go").first()).toContainText("1 node(s)");

  // The other weighting is the other ordering, and it is the reverse one here: the
  // busiest process is not the worst one, which is the whole reason there are two.
  await page.selectOption("#mesh-notation", "instances");
  await expect(rank.locator(".mesh-rank-head")).toContainText("Busiest");
  await expect(rank.locator(".mesh-rank-who")).toHaveText(["Dunning", "Invoice"]);
  await expect(rank.locator(".mesh-rank-go").first()).toContainText("120 running");

  // A row is still the way to the node, like every other list in this column.
  await rank.locator(".mesh-rank-go").first().click();
  await expect(page.locator(".mesh-panel-head")).toContainText("Dunning");

  // And leaving the weighting puts the blast ranking back.
  await page.selectOption("#mesh-notation", "atlas");
  await expect(rank.locator(".mesh-rank-head")).toContainText("Biggest blast radius");
});

// An estate with nothing parked has nothing to rank, and that is the finding rather
// than an empty column — the same argument the flat canvas makes.
test("a ranking with nothing to count says so as an answer", async ({ page }) => {
  installMock(page, runningMesh);
  await page.setViewportSize({ width: 1400, height: 900 });
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toBeVisible();

  await page.selectOption("#mesh-notation", "incidents");
  const rank = page.locator(".mesh-rank");
  await expect(rank.locator(".mesh-rank-head")).toContainText("Most parked");
  await expect(rank).toContainText("nothing to rank");
  await expect(rank.locator(".mesh-rank-go")).toHaveCount(0);
});

// The weightings exclude the projections and each other rather than combining, and
// that is the point of putting them in the list: size is one channel, so a picture
// cannot mean connectivity and load at once.
test("choosing a projection gives up the instance weighting", async ({ page }) => {
  installMock(page, runningMesh);
  await page.setViewportSize({ width: 1400, height: 900 });
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toBeVisible();

  await page.selectOption("#mesh-notation", "instances");
  await expect(page.locator(".mesh-runs")).toHaveCount(1);
  // No vocabulary is claimed: it is Atlas's own landscape, sized differently.
  await expect(page.locator(".mesh-type")).toHaveCount(0);
  await expect(page.locator(".mesh-projection")).toHaveCount(0);

  await page.selectOption("#mesh-notation", "archimate-3.2");
  await expect(page.locator(".mesh-runs")).toHaveCount(0);
  await expect(page.locator(".mesh-projection")).toBeVisible();
});

// A number a real server produces: the counts on this view come from the same engine
// counters the Operations badges carry, and there they reach five and six digits. The
// thousands are grouped with a narrow no-break space, on the canvas and in the panel
// alike — an unbroken "50002" has to be read digit by digit, and a lifetime total is
// the number most likely to be long.
test("a large count is grouped in thousands, on the canvas and in the panel", async ({ page }) => {
  installMock(page, {
    ...runningMesh,
    nodes: runningMesh.nodes.map((n) => n.id !== "process:1" ? n
      : { ...n, runtime: { running: 50002, finished: 1284431, lastActivity: 0 } }),
  });
  await page.setViewportSize({ width: 1400, height: 900 });
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toBeVisible();

  await page.selectOption("#mesh-notation", "instances");
  const runs = page.locator('[data-node-id="process:1"] .mesh-runs');
  // textContent rather than a whitespace-normalizing matcher: the separator is the
  // point, and normalization would accept a plain space just as happily.
  expect(await runs.textContent()).toBe("50\u202F002 running");

  await page.locator('[data-node-id="process:1"] .mesh-body').click();
  const panel = page.locator(".mesh-panel .mesh-runtime");
  expect(await panel.locator(".mesh-runtime-now").textContent()).toBe("50\u202F002 running");
  await expect(panel).toContainText("1\u202F284\u202F431 finished");
});

// The panel states the tally for whichever node is selected, whether or not the
// canvas is carrying counts — including the zero the canvas has no room to give.
test("the panel says what a process is running, the zero included", async ({ page }) => {
  // The timestamp is minted here rather than where the fixture is declared: "4 min
  // ago" is measured against the clock at render time, and a value fixed at module
  // load drifts into "9 min ago" behind a long suite — which is a flake in the test
  // rather than a fault in the view.
  installMock(page, {
    ...runningMesh,
    nodes: runningMesh.nodes.map((n) => n.id !== "process:1" ? n
      : { ...n, runtime: { ...n.runtime, lastActivity: Date.now() * 1e6 - 240e9 } }),
  });
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toBeVisible();

  await page.locator('[data-node-id="process:1"] .mesh-body').click();
  const panel = page.locator(".mesh-panel .mesh-runtime");
  await expect(panel).toContainText("12 running");
  await expect(panel).toContainText("431 finished");
  await expect(panel).toContainText("last activity 4 min ago");

  await page.locator('[data-node-id="process:2"] .mesh-body').click();
  const idle = page.locator(".mesh-panel .mesh-runtime");
  await expect(idle).toContainText("0 running");
  await expect(idle).toContainText("7 finished");
  // A lifetime total with no timestamp is a definition this build has no activity
  // clock for. It says nothing about when, rather than "never started" — which
  // beside seven finished instances is a contradiction the reader has to resolve.
  await expect(idle).not.toContainText("never started");

  // A worker has no instances at all — not zero of them — so it says nothing.
  await page.locator('[data-node-id="worker:c1"] .mesh-body').click();
  await expect(page.locator(".mesh-panel .mesh-runtime")).toHaveCount(0);
});

// The canvas has no edges. A node goes where the hand puts it — the world is a budget
// for the layout to settle in, not a fence around the arrangement — and the fit
// follows, so nothing dragged out of sight is lost.
test("a node can be dragged past the edge of the world, and Fit brings it back", async ({ page }) => {
  installMock(page);
  await page.setViewportSize({ width: 1400, height: 900 });
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toBeVisible();

  const node = page.locator('[data-node-id="process:1"]');
  const before = await node.evaluate((g) => g.getAttribute("transform"));

  // Straight up and well past the top: the direction the clamp used to refuse.
  const box = await node.boundingBox();
  const canvas = await page.locator(".mesh-canvas").boundingBox();
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await page.mouse.down();
  await page.mouse.move(box.x + box.width / 2, canvas.y - 260, { steps: 10 });
  await page.mouse.up();

  const after = await node.evaluate((g) => g.getAttribute("transform"));
  expect(after).not.toBe(before);
  // Above the world's own top edge, which is where the clamp used to stop it.
  const y = Number(after.match(/-?[\d.]+/g)[1]);
  expect(y).toBeLessThan(0);

  // And it is still findable: Fit frames the arrangement as it now is, so the node
  // that was dragged out of the frame is inside the next one.
  await page.locator("#mesh-zoom-fit").click();
  const view = (await page.locator(".mesh-canvas").getAttribute("viewBox")).split(" ").map(Number);
  expect(y).toBeGreaterThan(view[1]);
  expect(y).toBeLessThan(view[1] + view[3]);
});

// Going into a node is repeatable: each one becomes the new centre, and the path is
// kept so a reader can follow a dependency as far as it goes and still get back to
// where they were two nodes ago.
test("going into a node is repeatable, and the path is the way back", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toBeVisible();

  await page.locator('[data-node-id="application:a1"] .mesh-body').dblclick();
  await expect(page.locator(".mesh-crumb-here")).toHaveText("Billing");

  // From inside Billing, into one of its processes: the picture recentres and the
  // station is appended rather than replacing the one before it.
  await drillInto(page, "process:1");
  await expect(page.locator(".mesh-crumb-here")).toHaveText("Invoice");
  await expect(page.locator("#mesh-drill-trail .mesh-crumb")).toHaveText(["All", "Billing", "Invoice"]);

  // And on, from there. The depth is what bounds each picture; the trail is history.
  await page.locator('[data-node-id="worker:c1"] .mesh-body').dblclick();
  await expect(page.locator("#mesh-drill-trail .mesh-crumb"))
    .toHaveText(["All", "Billing", "Invoice", "ops-mail"]);

  // Escape steps back one station rather than throwing the walk away.
  await page.keyboard.press("Escape");
  await expect(page.locator(".mesh-crumb-here")).toHaveText("Invoice");

  // And any earlier station is one press away — following four deep and wanting the
  // second one back is the ordinary case, not the exotic one.
  await page.locator('.mesh-crumb[data-crumb="0"]').click();
  await expect(page.locator(".mesh-crumb-here")).toHaveText("Billing");
  await expect(page.locator("#mesh-drill-trail .mesh-crumb")).toHaveText(["All", "Billing"]);

  await page.locator('.mesh-crumb[data-crumb="-1"]').click();
  await expect(page.locator("#mesh-drill-trail")).toBeHidden();
  await expect(page.locator(".mesh-node")).toHaveCount(7);
});

// A path that visited a node already on it truncates back to that node rather than
// listing it twice: a trail that can contain the same station at two depths is a
// trail nobody can read, and "back to where I was" would then be ambiguous.
test("stepping into a node already on the path returns to it", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toBeVisible();

  await page.locator('[data-node-id="application:a1"] .mesh-body').dblclick();
  await drillInto(page, "process:1");
  await expect(page.locator("#mesh-drill-trail .mesh-crumb")).toHaveText(["All", "Billing", "Invoice"]);

  await page.locator('[data-node-id="application:a1"] .mesh-body').dblclick();
  await expect(page.locator("#mesh-drill-trail .mesh-crumb")).toHaveText(["All", "Billing"]);
  await expect(page.locator(".mesh-crumb-here")).toHaveText("Billing");
});

// An exported picture cropped to one node, with no account of how that node was
// arrived at, is a narrowing the reader cannot check (ADR-0211 §10).
test("an export of a path says which way it came", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toBeVisible();

  await page.locator('[data-node-id="application:a1"] .mesh-body').dblclick();
  await drillInto(page, "process:1");

  const [download] = await Promise.all([
    page.waitForEvent("download"),
    page.locator("#mesh-export-svg").click(),
  ]);
  const stream = await download.createReadStream();
  const svg = await new Promise((resolve, reject) => {
    let out = "";
    stream.on("data", (chunk) => (out += chunk));
    stream.on("end", () => resolve(out));
    stream.on("error", reject);
  });
  expect(svg).toContain("drilled into Invoice");
  expect(svg).toContain("via Billing");
});

// Depth is a number somebody types, or the whole graph. It was a three-option list —
// 1 hop, 2 hops, all — which answers the question at exactly two depths and refuses
// every other one: a reader asking "and one further?" had nothing to press.
test("depth is a number to type, and 'all' for however far it goes", async ({ page }) => {
  installMock(page, radiusGraph);
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toBeVisible();

  // Three, which the old control could not be asked for.
  await page.locator('[data-node-id="worker:mail"] .mesh-body').click();
  await setDepth(page, "3");
  await expect(page.locator(".mesh-impact-count")).toContainText("within 3 hop(s)");

  // "all" makes the field inert rather than hiding it: the number the reader had is
  // still there when they come back to it.
  await page.locator("#mesh-depth-any").check();
  await expect(page.locator("#mesh-depth")).toBeDisabled();
  await expect(page.locator("#mesh-depth")).toHaveValue("3");
  await expect(page.locator(".mesh-impact-count")).toContainText("within any hop(s)");

  await page.locator("#mesh-depth-any").uncheck();
  await expect(page.locator("#mesh-depth")).toBeEnabled();
  await expect(page.locator(".mesh-impact-count")).toContainText("within 3 hop(s)");
});

// A field somebody is mid-edit in holds anything, and a walk of zero hops is a
// picture of one node with nothing drawn around it — a broken answer rather than a
// narrow one. So it is read through a floor, and put right when the field is left.
test("a depth that is not a depth falls back to one hop", async ({ page }) => {
  await page.goto("/panorama-impact-harness.html");
  await expect(page.locator("#ready")).toHaveText("ready");
  const read = await page.evaluate(() =>
    ["", "0", "-4", "abc", "2.7", "500", "all"].map((v) => window.depthOf(v)));
  expect(read).toEqual([1, 1, 1, 1, 2, 99, Infinity]);
});

// A picture laid out for a box the surface never had.
//
// The first paint happens as soon as the markup is in the document, and there are
// ordinary reasons the surface has no box at that moment — a tab opened in the
// background does no layout at all. The measurement then falls back to its floor,
// the world takes that floor's nearly-square aspect, and the finished picture is
// letterboxed into a column of a wide canvas. Nothing was wrong with the graph; it
// was laid out for a box it never had — and it stayed wrong because nothing measured
// again, so changing the notation, or anything else that repaints, "fixed" it.
test("a picture laid out before the canvas had a box corrects itself", async ({ page }) => {
  installMock(page, radiusGraph);
  await page.setViewportSize({ width: 1400, height: 900 });
  // No box at all when the first layout runs, which is what a background tab hands
  // the view. Removed once the picture is up, as switching to that tab would.
  await page.addInitScript(() => {
    const style = document.createElement("style");
    style.id = "no-box";
    style.textContent = ".mesh-surface{display:none!important}";
    document.addEventListener("DOMContentLoaded", () => document.head.append(style));
  });
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toHaveCount(1);
  await page.evaluate(() => document.getElementById("no-box")?.remove());

  // The viewBox has the canvas's own shape, so there is nothing to letterbox and the
  // picture fills the surface it was given.
  await expect(async () => {
    const shape = await page.evaluate(() => {
      const s = document.querySelector(".mesh-surface").getBoundingClientRect();
      const [, , w, h] = document.querySelector(".mesh-canvas")
        .getAttribute("viewBox").split(" ").map(Number);
      return { canvas: s.width / s.height, view: w / h };
    });
    expect(shape.view).toBeCloseTo(shape.canvas, 1);
  }).toPass({ timeout: 4000 });

  // And the nodes are spread across it rather than standing in a column: the widest
  // gap between two of them is most of the canvas.
  const spread = await page.evaluate(() => {
    const xs = [...document.querySelectorAll(".mesh-node")]
      .map((g) => g.getBoundingClientRect().x);
    const box = document.querySelector(".mesh-surface").getBoundingClientRect();
    return (Math.max(...xs) - Math.min(...xs)) / box.width;
  });
  expect(spread).toBeGreaterThan(0.5);
});

// A canvas whose box keeps moving.
//
// The observer that fixed the first-paint case only ever asked for a *re-layout*,
// and a re-layout is expensive, so it is debounced — every further resize pushed it
// back again. A page that keeps nudging the surface while it settles (a panel
// filling in, a scrollbar making up its mind, a window being dragged) therefore held
// the picture at the shape it was first drawn for, for as long as the nudging went
// on: the viewBox kept an aspect ratio the canvas no longer had, preserveAspectRatio
// letterboxed the difference, and the whole drawing shrank into the middle of the
// surface — which is what "the nodes are too close together" looks like.
//
// Framing is not expensive, so it no longer waits for the layout: the view is put
// right on the spot and the re-settling follows.
test("a canvas still being nudged is framed for the box it has, not the one it had", async ({ page }) => {
  installMock(page, radiusGraph);
  await page.setViewportSize({ width: 1400, height: 900 });
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toHaveCount(1);

  // Something on the page keeps resizing the canvas — never for long enough to let a
  // debounced re-layout run. Deliberately not awaited: the assertion below is about
  // what the reader sees *while* this is going on.
  await page.evaluate(() => {
    const style = document.createElement("style");
    document.head.append(style);
    let flip = 0;
    const id = setInterval(() => {
      style.textContent = `.mesh-surface{height:${300 + (flip ^= 1)}px!important}`;
    }, 40);
    setTimeout(() => clearInterval(id), 3000);
  });

  const shape = () => page.evaluate(() => {
    const s = document.querySelector(".mesh-surface").getBoundingClientRect();
    const [, , w, h] = document.querySelector(".mesh-canvas")
      .getAttribute("viewBox").split(" ").map(Number);
    return { canvas: s.width / s.height, view: w / h };
  });
  // Let the nudging get going, so the canvas is demonstrably no longer the shape the
  // picture was first drawn for — otherwise the first sample would agree by having
  // been taken too early.
  await expect.poll(async () => (await shape()).canvas, { timeout: 2000 })
    .toBeGreaterThan(3);
  // Then every sample, while the nudging carries on, has the picture framed for the
  // canvas it is in. Sampled rather than awaited once: the claim is that the view is
  // right *throughout*, not that it eventually catches up when the page stops moving.
  for (let i = 0; i < 5; i++) {
    const now = await shape();
    expect(now.view).toBeCloseTo(now.canvas, 1);
    await page.waitForTimeout(120);
  }
});

// The two channels a finding is drawn in are strokes, and a stroke in world units is
// a stroke that disappears as the estate grows — the opening view scales the world
// down to fit, so the red outline measured 3.3 device pixels at twelve nodes and
// 0.73 at three hundred and twenty, and the heartbeat ring 2.4 and 0.52. Both are
// still in the DOM at that size and neither is visible, which is the worst of both:
// the view reports that it is showing you the problem.
test("the severity outline and the heartbeat keep their weight on a large starmap", async ({ page }) => {
  const many = { nodes: [], edges: [], restricted: 0, clustered: false };
  for (let i = 1; i <= 120; i++) {
    const bad = i % 11 === 0;
    many.nodes.push({
      id: `process:${i}`, kind: "process", name: `Process ${i}`, provenance: "derived",
      processId: `p${i}`, version: 1,
      severity: bad ? "critical" : "ok", state: bad ? "not-ready" : "healthy",
      reason: bad ? "3 token(s) are parked." : "", incidents: bad ? 3 : 0,
    });
    if (i > 1) many.edges.push({ from: `process:${i - 1}`, to: `process:${i}`, kind: "calls" });
  }
  installMock(page, many);
  await page.setViewportSize({ width: 1400, height: 900 });
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toHaveCount(1);

  const drawn = await page.evaluate(() => {
    const svg = document.querySelector(".mesh-canvas");
    const [, , w] = svg.getAttribute("viewBox").split(" ").map(Number);
    const bad = document.querySelector(".mesh-sev-critical");
    const beat = bad.querySelector(".mesh-beat");
    const body = bad.querySelector(".mesh-body");
    const stroke = (el) => parseFloat(getComputedStyle(el).strokeWidth);
    return {
      // Well under one screen pixel per world unit: this is the case the strokes
      // used to vanish in, so the test is measuring the situation it is about.
      scale: document.querySelector(".mesh-surface").getBoundingClientRect().width / w,
      beating: svg.classList.contains("mesh-beating"),
      beatColour: getComputedStyle(beat).stroke,
      bodyColour: getComputedStyle(body).stroke,
      danger: getComputedStyle(document.documentElement).getPropertyValue("--danger").trim(),
      beatAnimation: getComputedStyle(beat).animationName,
      // Drawn in screen pixels rather than in world units, so the weight is the
      // weight the stylesheet asked for however far out the view is.
      beatPx: getComputedStyle(beat).vectorEffect === "non-scaling-stroke" ? stroke(beat) : null,
      bodyPx: getComputedStyle(body).vectorEffect === "non-scaling-stroke" ? stroke(body) : null,
    };
  });

  expect(drawn.scale).toBeLessThan(0.5);
  // Still red, still beating — the two things the finding is drawn with.
  expect(drawn.bodyColour).toBe(drawn.beatColour);
  expect(drawn.beatAnimation).toBe("mesh-beat-slow");
  expect(drawn.beating).toBe(true);
  // And both are drawn at the weight they are specified at, not at that weight times
  // however far the landscape had to shrink to fit.
  expect(drawn.bodyPx).toBeCloseTo(3.5, 2);
  expect(drawn.beatPx).toBeCloseTo(2.5, 2);
});

// The shape the graph settles into, against the shape it is being fitted to.
//
// The fit scales both axes by one factor — stretching them independently would fill
// the frame by misreporting distance — so whatever shape the settle lands on is the
// shape that gets framed, and the difference from the canvas's own shape is left over
// as a band of empty canvas along one edge. The centring pull is anisotropic to stop
// that happening, and it was applying the frame's aspect ratio to each axis in
// opposite directions, which is that ratio *squared* between them. Measured on a
// 36-node estate at 1400x900: the graph settled at 2.8:1 against a canvas of 1.7:1,
// filled 93% of the width and 55% of the height, and crowded the nodes into that half
// while a third of the canvas stayed blank.
//
// So the pull is aimed rather than assumed — the shape it reaches depends on the
// graph as much as on the frame — and the remaining steps run under the correction.
test("the graph settles into the shape of its canvas, not into a band across it", async ({ page }) => {
  // An estate shaped like a real one, which is mostly *not* joined up: a derived
  // starmap of thirty-six nodes carries seventeen edges, so most of it floats free
  // and is placed by the repulsion and the centring pull alone. That is the case the
  // pull's shape decides on its own, with no springs to argue with it — and it is
  // the common case, because an estate is not a graph somebody drew.
  const estate = { nodes: [{ id: "application:a1", kind: "application", name: "Atlas System", provenance: "derived" }], edges: [], restricted: 0, clustered: false };
  for (let i = 1; i <= 29; i++) {
    estate.nodes.push({ id: `process:${i}`, kind: "process", name: `Process number ${i}`, provenance: "derived", processId: `p${i}`, version: 1 });
  }
  for (const i of [1, 2, 3]) estate.edges.push({ from: "application:a1", to: `process:${i}`, kind: "contains" });
  estate.edges.push({ from: "process:4", to: "process:5", kind: "calls" });
  for (let u = 1; u <= 6; u++) {
    estate.nodes.push({ id: `unresolved:svc-${u}`, kind: "unresolved", name: `svc-${u}`, provenance: "derived" });
    for (let k = 0; k < 2; k++) {
      estate.edges.push({ from: `process:${u * 3 + k}`, to: `unresolved:svc-${u}`, kind: "uses" });
    }
  }
  installMock(page, estate);

  for (const [w, h] of [[1400, 900], [1000, 700]]) {
    await page.setViewportSize({ width: w, height: h });
    await page.goto("/index.html#/panorama/starmap");
    await expect(page.locator(".mesh-canvas")).toHaveCount(1);
    await page.waitForTimeout(300);
    const fill = await page.evaluate(() => {
      const s = document.querySelector(".mesh-surface").getBoundingClientRect();
      const [, , vw] = document.querySelector(".mesh-canvas").getAttribute("viewBox").split(" ").map(Number);
      const scale = s.width / vw;
      const at = [...document.querySelectorAll(".mesh-node")].map((g) => {
        const m = /translate\(([-\d.]+),([-\d.]+)\)/.exec(g.getAttribute("transform"));
        return { x: +m[1], y: +m[2] };
      });
      const xs = at.map((a) => a.x), ys = at.map((a) => a.y);
      return {
        x: (Math.max(...xs) - Math.min(...xs)) * scale / s.width,
        y: (Math.max(...ys) - Math.min(...ys)) * scale / s.height,
      };
    });
    // Both axes, not "whichever ran out first". A picture that reaches the sides and
    // stops halfway down the canvas is the one this is about.
    expect(fill.x, `width at ${w}x${h}`).toBeGreaterThan(0.65);
    expect(fill.y, `height at ${w}x${h}`).toBeGreaterThan(0.65);
  }
});

// Names that used to be written on top of each other.
//
// The alternative was to make the layout keep them apart — personal space as wide as
// a name — and the numbers rule it out: names run to 250 world units against radii of
// 6 to 18, so the world would grow about tenfold in area, and since the opening view
// fits the whole world onto the canvas, every node and every name would be drawn that
// much smaller. That answers "the names overlap" with "the names are too small to
// read". So the graph stays where it settled and the names move instead.
test("no two names are written on top of each other", async ({ page }) => {
  const crowd = { nodes: [], edges: [], restricted: 0, clustered: false };
  const long = ["Benutzerverwaltung: Benutzer aufnehmen", "Benutzerverwaltung: Benutzer offboarden",
    "Order-to-Cash (Warenkorb bis Zahlung)", "Konten-Rezertifizierung (Entra ID)",
    "Hypothekarzinsen erfassen (Migros Bank)", "GALSync (cross-forest address list)"];
  for (let i = 1; i <= 24; i++) {
    crowd.nodes.push({ id: `process:${i}`, kind: "process", name: `${long[i % long.length]} ${i}`,
      provenance: "derived", processId: `p${i}`, version: 1 });
    if (i > 1) crowd.edges.push({ from: `process:${i - 1}`, to: `process:${i}`, kind: "calls" });
  }
  installMock(page, crowd);
  await page.setViewportSize({ width: 1400, height: 900 });
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toHaveCount(1);
  await page.waitForTimeout(300);

  const read = await page.evaluate(() => {
    const painted = [...document.querySelectorAll(".mesh-node")].filter((g) => {
      const ink = g.querySelector(".mesh-label-ink");
      return ink && ink.textContent.trim() && Number(getComputedStyle(ink).opacity) > 0.5;
    });
    const boxes = painted.map((g) => g.querySelector(".mesh-caption").getBoundingClientRect());
    let clashes = 0;
    for (let i = 0; i < boxes.length; i++) {
      for (let j = i + 1; j < boxes.length; j++) {
        const a = boxes[i], b = boxes[j];
        if (a.left < b.right && b.left < a.right && a.top < b.bottom && b.top < a.bottom) clashes++;
      }
    }
    return { painted: painted.length, clashes };
  });

  expect(read.clashes).toBe(0);
  // And moving them apart is what happened, not hiding them: nearly every name is
  // still written. A pass that fixed the overlap by dropping names would satisfy the
  // line above and be a worse picture than the one it replaced.
  expect(read.painted).toBeGreaterThan(crowd.nodes.length * 0.75);
});

// A name there was no room for is not a name that is gone.
test("a name there was no room for comes back when you point at its node", async ({ page }) => {
  // Names long enough, on nodes enough of them, that four places around a node are
  // not always four free places.
  const crowd = { nodes: [], edges: [], restricted: 0, clustered: false };
  for (let i = 1; i <= 60; i++) {
    crowd.nodes.push({ id: `process:${i}`, kind: "process",
      name: `Benutzerverwaltung: Benutzer aufnehmen, prüfen und freigeben (Stufe ${i})`,
      provenance: "derived", processId: `p${i}`, version: 1 });
    if (i > 1) crowd.edges.push({ from: `process:${i - 1}`, to: `process:${i}`, kind: "uses" });
  }
  installMock(page, crowd);
  await page.setViewportSize({ width: 1400, height: 900 });
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toHaveCount(1);
  await page.waitForTimeout(300);

  const crowdedOut = page.locator(".mesh-node.mesh-crowded").first();
  await expect(crowdedOut).toBeAttached();
  expect(await crowdedOut.locator(".mesh-label-ink")
    .evaluate((el) => Number(getComputedStyle(el).opacity))).toBeLessThan(0.5);

  await crowdedOut.hover({ force: true });
  await expect.poll(async () => crowdedOut.locator(".mesh-label-ink")
    .evaluate((el) => Number(getComputedStyle(el).opacity))).toBeGreaterThan(0.9);
});

// Contrast, as a number rather than as an opinion.
//
// The two structural colours had drifted under what a canvas needs: WCAG 2.1 asks 3:1
// of a graphical object against what it is drawn on, and against the white canvas the
// edges were 2.13 — below the floor, on the thinnest mark in the picture — and the
// process outline 3.21, the bare minimum, on the most numerous node there is. Both
// were also drawn in world units, so they thinned out as the estate grew: 2.2 units
// came to 1.4 device pixels at 36 nodes and 0.64 at 160.
//
// Held here as a floor, not as an exact value: a palette may be retuned, and this
// says only that the retuning cannot take the picture back under the line.
test("the structure of the picture clears the contrast floor a canvas needs", async ({ page }) => {
  installMock(page, radiusGraph);
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toHaveCount(1);

  const seen = await page.evaluate(() => {
    const lin = (c) => (c /= 255) <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
    const lum = (rgb) => {
      const [r, g, b] = rgb.match(/\d+(\.\d+)?/g).map(Number);
      return 0.2126 * lin(r) + 0.7152 * lin(g) + 0.0722 * lin(b);
    };
    const ratio = (a, b) => {
      const [x, y] = [lum(a), lum(b)];
      return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05);
    };
    const canvas = document.querySelector(".mesh-canvas");
    const paper = getComputedStyle(canvas).backgroundColor;
    const body = document.querySelector(".mesh-process .mesh-body");
    const edge = document.querySelector(".mesh-edge");
    return {
      outline: ratio(getComputedStyle(body).stroke, paper),
      edge: ratio(getComputedStyle(edge).stroke, paper),
      // Drawn in screen pixels, so the weight holds however far out the view is.
      outlineScales: getComputedStyle(body).vectorEffect,
      edgeScales: getComputedStyle(edge).vectorEffect,
    };
  });

  expect(seen.edge).toBeGreaterThanOrEqual(3);
  // More than the floor for the outline: it is what a process *is* on this canvas,
  // the fill being a tint and the shape a rounded square.
  expect(seen.outline).toBeGreaterThan(4);
  expect(seen.outlineScales).toBe("non-scaling-stroke");
  expect(seen.edgeScales).toBe("non-scaling-stroke");
});

// Double-clicking a process opens it in Operations.
//
// One gesture with one meaning — go inside the thing this node stands for — and where
// the inside is depends on the kind. Panorama owns the landscape and application
// altitudes and links into the process one rather than reimplementing it (§5), so a
// process's inside is not on this canvas at all: it is the live view, with its
// instances and its tokens.
test("double-clicking a process opens it in Operations", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toBeVisible();

  await page.locator('[data-node-id="process:1"] .mesh-body').dblclick();
  await expect(page).toHaveURL(/#\/operations\/p\/1$/);
});

// And the link it duplicates stays where it was. A gesture you have to be told about
// is one most readers never find, so the panel goes on saying it in words.
test("the panel still says where a process opens, in words", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/starmap");

  await page.locator('[data-node-id="process:1"] .mesh-body').click();
  await expect(page.getByRole("link", { name: "Open in Operations" })).toBeVisible();
  await page.getByRole("link", { name: "Open in Operations" }).click();
  await expect(page).toHaveURL(/#\/operations\/p\/1$/);
});

// Every other kind keeps the gesture it had: there is no elsewhere to open a worker
// in, so going into one still means this node becomes the centre of the picture.
test("double-clicking anything else still goes into it on the canvas", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/starmap");

  await page.locator('[data-node-id="worker:c1"] .mesh-body').dblclick();
  await expect(page.locator("#mesh-drill-trail")).toBeVisible();
  await expect(page.locator(".mesh-crumb-here")).toHaveText("ops-mail");
  await expect(page).toHaveURL(/#\/panorama\/starmap$/);
});

// A process can still be drilled into on the landscape — the gesture moved, the
// capability did not. The "→" is the discoverable half of the pair.
test("a process can still be made the centre of the picture, from the header", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/starmap");

  await drillInto(page, "process:1");
  await expect(page.locator("#mesh-drill-trail")).toBeVisible();
  await expect(page.locator(".mesh-crumb-here")).toHaveText("Invoice");
  await expect(page).toHaveURL(/#\/panorama\/starmap$/);
});

// A decision's inside is its evaluation history — the same question one altitude
// down: what has this actually done, and with what.
test("double-clicking a decision opens its evaluations in Operations", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toBeVisible();

  await page.locator('[data-node-id="decision:credit"] .mesh-body').dblclick();
  await expect(page).toHaveURL(/#\/operations\/decisions\/credit$/);
});

// And it is said in words too, for the same reason the process link is: a gesture you
// have to be told about is one most readers never find.
test("the panel says where a decision opens, and the link goes there", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/starmap");

  await page.locator('[data-node-id="decision:credit"] .mesh-body').click();
  await page.getByRole("link", { name: "Open in Operations" }).click();
  await expect(page).toHaveURL(/#\/operations\/decisions\/credit$/);
});

// The two placeholder kinds are the case this must not get wrong (§3). A decision the
// caller may not see is a restricted node and one nothing provides is an unresolved
// node — neither is a decision, and neither has a page. Offering a link to one would
// be an absence reading as a fact.
test("a placeholder is not offered a page it does not have", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/starmap");

  for (const id of ["restricted:1", "unresolved:process:archive"]) {
    await page.locator(`[data-node-id="${id}"] .mesh-body`).click();
    await expect(page.getByRole("link", { name: "Open in Operations" })).toHaveCount(0);
    await page.locator(`[data-node-id="${id}"] .mesh-body`).dblclick();
    // Going into it on the canvas is what it does instead.
    await expect(page).toHaveURL(/#\/panorama\/starmap$/);
    await expect(page.locator("#mesh-drill-trail")).toBeVisible();
    await page.keyboard.press("Escape");
  }
});

// Drafts (ADR-0211 §7 amendment). The picture's subject is what this server runs, so
// a saved-but-undeployed diagram is off by default and switched on by name — and the
// switch has to actually re-ask the server, because the drafts are not in the payload
// until they are wanted.
test("the starmap leaves drafts out until they are asked for", async ({ page }) => {
  installDraftMock(page);
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toBeVisible();

  await expect(page.locator("#mesh-drafts")).not.toBeChecked();
  await expect(page.locator('[data-node-id="draft:refund"]')).toHaveCount(0);

  await page.locator("#mesh-drafts").check();
  await expect(page.locator('[data-node-id="draft:refund"]')).toHaveCount(1);

  // And off again, so the switch is a switch rather than a one-way door.
  await page.locator("#mesh-drafts").uncheck();
  await expect(page.locator('[data-node-id="draft:refund"]')).toHaveCount(0);
});

// Told apart by more than colour. The draft keeps the process square — it is a
// process — and carries the dashed outline the placeholder kinds use, which is the
// channel that survives a printout and a reader who does not separate the hues.
test("a draft is drawn as a process that is not running", async ({ page }) => {
  installDraftMock(page);
  await page.goto("/index.html#/panorama/starmap");
  await page.locator("#mesh-drafts").check();

  const draft = page.locator('[data-node-id="draft:refund"] .mesh-body');
  const process = page.locator('[data-node-id="process:1"] .mesh-body');
  await expect(draft).toHaveCount(1);

  const [draftShape, processShape] = await Promise.all([
    draft.evaluate((n) => n.tagName.toLowerCase()),
    process.evaluate((n) => n.tagName.toLowerCase()),
  ]);
  expect(draftShape).toBe(processShape);

  const [draftDash, processDash] = await Promise.all([
    draft.evaluate((n) => n.getAttribute("stroke-dasharray") || ""),
    process.evaluate((n) => n.getAttribute("stroke-dasharray") || ""),
  ]);
  expect(draftDash).not.toBe("");
  expect(processDash).toBe("");

  // And the fills differ in *brightness*, not only in hue. The first version of this
  // colour was a warm tone at the same luminance as the process fill — a contrast
  // ratio of 1.00 between them — so on a projector, in a print, and to a reader who
  // does not separate those hues the two were identical and the dash carried the whole
  // distinction. A ratio is the measurement that catches that; "the fills differ" does
  // not. It stays far below a finding, which is the other half of the rule: the amber
  // status badge is 3.59 against the canvas (ADR-0211 §4).
  const [draftFill, processFill] = await Promise.all([
    draft.evaluate((n) => getComputedStyle(n).fill),
    process.evaluate((n) => getComputedStyle(n).fill),
  ]);
  expect(draftFill).not.toBe(processFill);
  const contrast = (a, b) => {
    const lum = (css) => {
      const [r, g, b2] = css.match(/[\d.]+/g).slice(0, 3).map((v) => Number(v) / 255);
      const f = (c) => (c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4);
      return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b2);
    };
    const [x, y] = [lum(a), lum(b)].sort((m, n) => n - m);
    return (x + 0.05) / (y + 0.05);
  };
  expect(contrast(draftFill, processFill)).toBeGreaterThan(1.05);
  expect(contrast(draftFill, "rgb(255, 255, 255)")).toBeLessThan(1.5);

  // The tooltip says the same thing in words, and never claims a version: a draft has
  // none, and "v undefined" would be the picture contradicting itself.
  const title = await page.locator('[data-node-id="draft:refund"] title').textContent();
  expect(title).toContain("not deployed");
  expect(title).not.toContain("undefined");
});

// The legend explains what the picture contains and nothing else, so the draft row
// appears with the drafts and goes away with them.
test("the legend gains a draft row only while drafts are on", async ({ page }) => {
  installDraftMock(page);
  await page.goto("/index.html#/panorama/starmap");

  await expect(page.locator(".mesh-legend")).not.toContainText("Draft");
  await page.locator("#mesh-drafts").check();
  await expect(page.locator(".mesh-legend")).toContainText("Draft — saved, not deployed");
  await page.locator("#mesh-drafts").uncheck();
  await expect(page.locator(".mesh-legend")).not.toContainText("Draft");
});

// A draft's inside is the Modeler, not Operations: it has never run, so there is
// nothing at the operational altitude to link into, and offering the link anyway
// would be an absence reading as a fact (ADR-0211 §3).
test("double-clicking a draft opens it in the Modeler", async ({ page }) => {
  installDraftMock(page);
  await page.goto("/index.html#/panorama/starmap");
  await page.locator("#mesh-drafts").check();

  await page.locator('[data-node-id="draft:refund"] .mesh-body').click();
  await expect(page.getByRole("link", { name: "Open in Modeler" })).toBeVisible();

  await page.locator('[data-node-id="draft:refund"] .mesh-body').dblclick();
  await expect(page).toHaveURL(/#\/modeler\/draft\/refund$/);
});

// An instance where nothing is deployed is one where everything is a draft. The empty
// picture used to be the end of the road — its only switch was on the page it refused
// to draw — so the view asks once before saying the landscape is empty.
test("a landscape that is all drafts is drawn rather than declared empty", async ({ page }) => {
  installDraftMock(page, { deployed: { nodes: [], edges: [], restricted: 0, clustered: false } });
  await page.goto("/index.html#/panorama/starmap");

  await expect(page.locator(".mesh-canvas")).toBeVisible();
  await expect(page.locator("#mesh-drafts")).toBeChecked();
  await expect(page.locator('[data-node-id="draft:refund"]')).toHaveCount(1);
});

// A failed fetch must not leave the control claiming something the picture does not
// show. The switch goes back, because nothing changed.
test("a drafts fetch that fails puts the switch back", async ({ page }) => {
  installDraftMock(page, { failDrafts: true });
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toBeVisible();

  await page.locator("#mesh-drafts").check();
  await expect(page.locator("#mesh-drafts")).not.toBeChecked();
  await expect(page.locator('[data-node-id="process:1"]')).toHaveCount(1);
});

// A saved view carries the drafts switch the way it carries the instance counts. It
// is the one setting whose restoration costs a fetch, and skipping it would open a
// named view showing half the picture it was named for.
test("a saved view comes back with its drafts", async ({ page }) => {
  installDraftMock(page);
  await page.goto("/index.html#/panorama/starmap");
  await page.locator(".mesh-node").first().waitFor();

  await page.locator("#mesh-drafts").check();
  await expect(page.locator('[data-node-id="draft:refund"]')).toHaveCount(1);
  await page.fill("#mesh-view-name", "Everything drawn");
  await page.locator("#mesh-view-save button").click();
  await expect(page.locator(".mesh-view-open")).toHaveText("Everything drawn");
  await expect(page.locator(".mesh-view-open")).toHaveAttribute("title", /with drafts/);

  // Back to the deployed-only picture, then open the view again.
  await page.locator("#mesh-drafts").uncheck();
  await expect(page.locator('[data-node-id="draft:refund"]')).toHaveCount(0);

  await page.locator(".mesh-view-open").click();
  await expect(page.locator("#mesh-drafts")).toBeChecked();
  await expect(page.locator('[data-node-id="draft:refund"]')).toHaveCount(1);
});

// A node with no edge, and the picture it used to be dragged out of shape by.
//
// The centring pull is anisotropic so the graph takes the shape of the frame, and
// that shape was decided for a node the springs are also holding. A node with no edge
// has no springs: the pull is all that keeps it near the picture, against a repulsion
// that falls off as 1/d². Measured here at 1400x900, the balance put two of the ten
// unattached processes hard against the left and right edges of an otherwise centred
// picture, with everything else squeezed into the middle of it.
//
// The fill test above cannot see this and never could: an outlier makes the bounding
// box *wider*, so a picture "filled" by two stragglers scores better than a good one.
// This measures the gap instead — every node's distance to its nearest neighbour,
// against the median of them — which is the thing a reader actually notices.
test("nothing is left stranded at the edge of the picture", async ({ page }) => {
  // Four applications with their processes, and ten deployed processes filed under
  // nothing at all. That last part is not contrived: a process deployed through the
  // API, or before its application existed, belongs to no application and is drawn
  // with no edge of any kind.
  const estate = { nodes: [], edges: [], restricted: 0, clustered: false };
  for (let a = 1; a <= 4; a++) {
    estate.nodes.push({ id: `application:a${a}`, kind: "application", name: `App ${a}`, provenance: "derived" });
    for (let p = 1; p <= 5; p++) {
      const id = `process:${a}_${p}`;
      estate.nodes.push({ id, kind: "process", name: `Proc ${a}.${p}`, provenance: "derived", processId: `p${a}_${p}`, version: 1 });
      estate.edges.push({ from: `application:a${a}`, to: id, kind: "contains" });
    }
  }
  for (let i = 1; i <= 10; i++) {
    estate.nodes.push({ id: `process:free${i}`, kind: "process", name: `Frei ${i}`, provenance: "derived", processId: `f${i}`, version: 1 });
  }
  installMock(page, estate);
  await page.setViewportSize({ width: 1400, height: 900 });
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toHaveCount(1);
  await page.waitForTimeout(600);

  const spread = await page.evaluate(() => {
    const at = [...document.querySelectorAll(".mesh-node")].map((el) => {
      const t = /translate\(([-\d.]+),([-\d.]+)\)/.exec(el.getAttribute("transform"));
      return { id: el.getAttribute("data-node-id"), x: +t[1], y: +t[2] };
    });
    const nearest = at.map((a) => Math.min(...at.filter((b) => b !== a)
      .map((b) => Math.hypot(a.x - b.x, a.y - b.y))));
    const sorted = [...nearest].sort((x, y) => x - y);
    const median = sorted[Math.floor(sorted.length / 2)];
    return {
      ratio: sorted[sorted.length - 1] / median,
      stranded: at.filter((_, i) => nearest[i] > median * 2.5).map((a) => a.id),
    };
  });

  // Two and a half times the median is already a visible hole in the picture; the
  // defect measured 3.1 with two nodes past it, and the corrected layout measures 1.1.
  expect(spread.stranded, "nodes with no neighbour near them").toEqual([]);
  expect(spread.ratio).toBeLessThan(2);
});
