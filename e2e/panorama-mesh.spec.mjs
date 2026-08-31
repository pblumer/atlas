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

function installMock(page, mesh = graph) {
  page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path === "/api/v1/panorama/mesh") return route.fulfill({ json: mesh });
    return route.fulfill({ json: [] });
  });
}

test("renders the derived landscape and drills into a process", async ({ page }) => {
  installMock(page);
  const pageErrors = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));

  await page.goto("/index.html#/panorama/landscape");

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
  await page.goto("/index.html#/panorama/landscape");

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
  await page.goto("/index.html#/panorama/landscape");

  await expect(page.locator(".mesh-note").first()).toContainText("size budget");
  await expect(page.locator(".mesh-canvas")).toContainText("42");
});

test("an empty landscape explains itself rather than showing a blank page", async ({ page }) => {
  installMock(page, { nodes: [], edges: [], restricted: 0, clustered: false });
  await page.goto("/index.html#/panorama/landscape");

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
  await page.goto("/index.html#/panorama/landscape");
  await expect(page.locator(".mesh-canvas")).toBeVisible({ timeout: 30000 });
  await expect(page.locator(".mesh-node")).toHaveCount(400);
  expect(Date.now() - started).toBeLessThan(15000);
});

// Search is what makes a few hundred nodes navigable at all — without it the view
// is a picture rather than a tool. It matches on name, kind and BPMN process id,
// and it reports how much it hid: a filtered mesh looks exactly like a small one.
test("filters the landscape and says how much it is hiding", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/landscape");
  await expect(page.locator(".mesh-node")).toHaveCount(7);

  await page.getByLabel("Filter the landscape").fill("invoice");
  await expect(page.locator(".mesh-node")).toHaveCount(1);
  await expect(page.locator("#mesh-count")).toContainText("1 of 7");
  await expect(page.locator(".mesh-canvas")).toContainText("Invoice");

  // Filtering by kind is the other half: "what does this instance talk to".
  await page.getByLabel("Filter the landscape").fill("worker");
  await expect(page.locator(".mesh-worker")).toHaveCount(1);
  await expect(page.locator(".mesh-canvas")).toContainText("ops-mail");

  await page.getByLabel("Filter the landscape").fill("nothing-matches-this");
  await expect(page.locator(".mesh-empty-filter")).toContainText("Nothing matches");

  await page.getByLabel("Filter the landscape").fill("");
  await expect(page.locator(".mesh-node")).toHaveCount(7);
  await expect(page.locator("#mesh-count")).toContainText("7 node(s)");
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
  await page.goto("/index.html#/panorama/landscape");

  await expect(page.locator(".mesh-unresolved title")).toContainText("worker");
  await expect(page.locator(".mesh-unresolved title")).toContainText("park");
});

// Impact analysis in the view (ADR-0211 §6). The traversal itself is covered case by
// case in panorama-impact.spec.mjs; these cover what the viewer actually gets.
test("selecting a node shows its blast radius and dims the rest", async ({ page }) => {
  installMock(page);
  await page.goto("/index.html#/panorama/landscape");

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
  await page.locator("#mesh-depth").selectOption("1");
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
  await page.goto("/index.html#/panorama/landscape");

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
  await page.goto("/index.html#/panorama/landscape");

  await page.locator('[data-node-id="worker:c1"]').click();
  await expect(page.locator(".mesh-impact-count")).toBeVisible();

  await page.getByLabel("Filter the landscape").fill("invoice");
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
  await page.goto("/index.html#/panorama/landscape");

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
  await page.goto("/index.html#/panorama/landscape");

  const legend = page.locator(".mesh-legend");
  await expect(legend).toContainText("nothing on this");
  await expect(legend).not.toContainText("neither matched nor missing");
});
