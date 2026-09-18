import { test, expect } from "@playwright/test";

// The service catalogue on the starmap (#1022, second half).
//
// The derivation is proved as arithmetic in the Go suite. This drives the REAL view
// against a mocked payload, which is the only way to check the half that lives in the
// browser: that a catalogue and a product are drawn as their own family rather than
// falling back to the process square, that the key explains the lines the catalogue
// adds, and that going into one lands in the Catalogue rather than in Operations.

const graph = {
  nodes: [
    // The process a product binds — drawn here because a product named it, with the
    // state the engine has for it, which is the finding this picture exists for.
    { id: "process:1", kind: "process", name: "Provision a phone", provenance: "derived", processId: "provision-phone", version: 1, state: "healthy", severity: "ok" },
    { id: "catalog:cat_1", kind: "catalog", name: "Mobile devices", provenance: "derived", state: "unbound", severity: "unknown" },
    { id: "product:package", kind: "product", name: "Phone package", provenance: "derived", catalog: "catalog:cat_1", state: "unbound", severity: "unknown" },
    { id: "product:phone", kind: "product", name: "Apple iPhone", provenance: "derived", catalog: "catalog:cat_1", state: "unbound", severity: "unknown" },
    { id: "product:case", kind: "product", name: "Protective case", provenance: "derived", catalog: "catalog:cat_1", state: "unbound", severity: "unknown" },
    { id: "unresolved:process:revoke-phone", kind: "unresolved", name: "revoke-phone", provenance: "derived", state: "unbound", severity: "unknown" },
  ],
  edges: [
    { from: "catalog:cat_1", to: "product:package", kind: "offers" },
    { from: "catalog:cat_1", to: "product:phone", kind: "offers" },
    { from: "catalog:cat_1", to: "product:case", kind: "offers" },
    { from: "product:package", to: "product:phone", kind: "composition" },
    { from: "product:package", to: "product:case", kind: "aggregation" },
    { from: "product:phone", to: "process:1", kind: "uses" },
    { from: "product:phone", to: "unresolved:process:revoke-phone", kind: "uses" },
  ],
  restricted: 0,
  clustered: false,
};

// The mapping is the server's (ADR-0211 §8), so the mock serves the rows the server
// serves — a mock that invented its own would be testing a picture no server draws.
const notations = [
  {
    id: "atlas", label: "Atlas (derived)", short: "Atlas", projection: false, mappingVersion: 2,
    types: {}, relations: {},
    loss: ["Incompatibility between two products is not drawn."],
  },
  {
    id: "archimate-3.2", label: "ArchiMate 3.2", short: "ArchiMate",
    projection: true, mappingVersion: 2,
    types: {
      application: { name: "Application Component", type: "ApplicationComponent" },
      process: { name: "Application Process", type: "ApplicationProcess" },
      catalog: { name: "Grouping", type: "Grouping" },
      product: { name: "Product", type: "Product" },
    },
    relations: {
      contains: { name: "Assignment", type: "Assignment" },
      uses: { name: "Serving", type: "Serving", flip: true },
      offers: { name: "Aggregation", type: "Aggregation" },
      composition: { name: "Composition", type: "Composition" },
      aggregation: { name: "Aggregation", type: "Aggregation" },
    },
    loss: ["Precedence between two products is drawn and not exported."],
  },
];

// The server derives two pictures, so the mock serves two. A mock that answered the
// same graph either way would be testing a picker that changes nothing.
function installMock(page, { landscape = estate, products = graph } = {}) {
  page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (url.pathname === "/api/v1/panorama/mesh") {
      page.__asked.push(url.search);
      return route.fulfill({ json: url.searchParams.get("view") === "products" ? products : landscape });
    }
    if (url.pathname === "/api/v1/panorama/notations") return route.fulfill({ json: notations });
    return route.fulfill({ json: [] });
  });
}

// The landscape: the estate, with not one product on it.
const estate = {
  nodes: [
    { id: "application:a1", kind: "application", name: "Workplace", provenance: "derived", state: "unbound", severity: "unknown" },
    { id: "process:1", kind: "process", name: "Provision a phone", provenance: "derived", application: "application:a1", processId: "provision-phone", version: 1, state: "healthy", severity: "ok" },
    { id: "worker:w1", kind: "worker", name: "ops-mail", provenance: "derived", workerType: "mail", state: "healthy", severity: "ok" },
  ],
  edges: [
    { from: "application:a1", to: "process:1", kind: "contains" },
    { from: "process:1", to: "worker:w1", kind: "uses" },
  ],
  restricted: 0,
  clustered: false,
};

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  page.__asked = [];
  installMock(page);
});

// openProductMap picks the second subject, which re-asks the server rather than
// redrawing what is on screen.
async function openProductMap(page) {
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toBeVisible();
  await page.locator("#mesh-notation").selectOption("products");
  await expect(page.locator('[data-node-id="catalog:cat_1"]')).toBeVisible();
}

test("the catalogue is drawn as its own family, not as more processes", async ({ page }) => {
  await openProductMap(page);

  await expect(page.locator(".mesh-node")).toHaveCount(6);
  await expect(page.locator(".mesh-canvas")).toContainText("Mobile devices");
  await expect(page.locator(".mesh-canvas")).toContainText("Phone package");

  // Shape is the channel that survives a projector and a printout, so the offered
  // half has a silhouette of its own: rectangles on a canvas of polygons. A kind the
  // view did not know would fall back to the process square, which is exactly the
  // failure this pins.
  // Read as the drawn proportion rather than as the element name: a process is a
  // square and is drawn with the same SVG tag, so the tag alone would pass on the day
  // the catalogue fell back to it.
  const aspectOf = (id) => page.locator(`[data-node-id="${id}"] .mesh-body`)
    .evaluate((el) => {
      const box = el.getBBox();
      return box.width / box.height;
    });
  expect(await aspectOf("process:1")).toBeCloseTo(1, 1);
  expect(await aspectOf("catalog:cat_1")).toBeGreaterThan(1.5);
  expect(await aspectOf("product:phone")).toBeGreaterThan(1.1);
  expect(await aspectOf("product:phone")).toBeLessThan(1.5);

  // And a catalogue is never coloured as a finding: nothing observes one.
  await expect(page.locator('[data-node-id="catalog:cat_1"]')).not.toHaveClass(/mesh-critical|mesh-attention/);
  expect(page.__errors).toEqual([]);
});

test("the key explains the lines the catalogue adds", async ({ page }) => {
  await openProductMap(page);
  const legend = page.locator(".mesh-legend");

  // "Comes with it" and "offered beside it" are different promises to whoever
  // orders, so the key has to tell them apart in words. The line patterns group the
  // catalogue's edges; only the legend distinguishes them.
  await expect(legend).toContainText("always comes with the whole");
  await expect(legend).toContainText("offered beside the whole");
  await expect(legend).toContainText("Catalogue — what a group of people may order");
  expect(page.__errors).toEqual([]);
});

test("a product opens where it is maintained, not in Operations", async ({ page }) => {
  await openProductMap(page);

  await page.locator('[data-node-id="product:phone"]').click();
  await page.getByRole("link", { name: "Open in Catalogue" }).click();
  await expect(page).toHaveURL(/#\/catalog\/c\/cat_1$/);
  expect(page.__errors).toEqual([]);
});

test("what breaks if this service goes down counts the products that contain it", async ({ page }) => {
  // Asked of the traversal directly, through the harness the rest of the impact rules
  // are checked in: it is a walk over a graph, and a click sequence would be testing
  // the panel rather than the rule.
  await page.goto("/panorama-impact-harness.html");
  await expect(page.locator("#ready")).toHaveText("ready");
  const impact = (id) => page.evaluate(([g, i]) =>
    window.impactFrom(g, i, { direction: "dependents", depth: Infinity }), [graph, id]);

  // The package cannot be delivered without the phone in it, so the phone's blast
  // radius reaches the package — and through it, the catalogue does not come along:
  // a catalogue does not depend on what it offers, any more than an application
  // depends on the processes it holds.
  const phone = await impact("product:phone");
  expect(new Set(phone.nodes)).toEqual(new Set(["product:phone", "product:package"]));

  // The optional part is the half of the rule worth pinning. A package whose case is
  // unavailable is still a package: that is what "optional" means in the store, so
  // nothing breaks with it.
  const optional = await impact("product:case");
  expect(new Set(optional.nodes)).toEqual(new Set(["product:case"]));
});

test("the ArchiMate export follows the picture rather than the estate", async ({ page }) => {
  await openProductMap(page);

  // The vocabularies on this picker are views of the *landscape* — one answer at a
  // time — so the catalogue's own ArchiMate words (Grouping, Product, Composition,
  // Aggregation, proved in the Go suite) are reached through the export rather than by
  // reading the product map in another notation. What the export must not do is hand
  // back the estate to somebody looking at the products: two answers to one question,
  // with no way to tell which was theirs.
  const asked = [];
  await page.route("**/api/v1/panorama/mesh/archimate*", async (route) => {
    asked.push(new URL(route.request().url()).search);
    await route.fulfill({ status: 200, contentType: "application/xml", body: "<model/>" });
  });
  await page.locator("#mesh-export-archimate").click();
  await expect.poll(() => asked.length).toBeGreaterThan(0);
  expect(asked[0]).toBe("?view=products");
  expect(page.__errors).toEqual([]);
});

// The two subjects, and the control that switches between them (#1022, corrected).
//
// The first cut drew the catalogue onto the landscape. It made the landscape worse for
// everybody who does not maintain a catalogue, and — the part that is not taste — every
// product spends the size budget, so one large catalogue could collapse somebody else's
// estate to applications. They are two pictures now, on one picker called View.
test("the landscape has no products on it, and the picker asks the server for them", async ({ page }) => {
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toBeVisible();

  // The estate, drawn as itself.
  await expect(page.locator('[data-node-id="application:a1"]')).toBeVisible();
  await expect(page.locator('[data-node-id="catalog:cat_1"]')).toHaveCount(0);
  await expect(page.locator('[data-node-id="product:phone"]')).toHaveCount(0);

  // The control is called View, because only some of its entries are vocabularies.
  await expect(page.locator('label[for="mesh-notation"]')).toHaveText("View");

  await page.locator("#mesh-notation").selectOption("products");
  await expect(page.locator('[data-node-id="catalog:cat_1"]')).toBeVisible();
  // The other picture, and the estate's own nodes are gone with it.
  await expect(page.locator('[data-node-id="worker:w1"]')).toHaveCount(0);
  await expect(page.locator('[data-node-id="application:a1"]')).toHaveCount(0);

  // Asked of the server rather than filtered here: a picture filtered in the browser
  // would still have spent the size budget on its way over.
  expect(page.__asked).toContain("?view=products");
  expect(page.__errors).toEqual([]);
});

test("the drafts switch belongs to the landscape", async ({ page }) => {
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toBeVisible();
  await expect(page.locator("#mesh-drafts")).toBeEnabled();

  // A draft is a diagram nobody deployed, and the product map draws no diagrams — so
  // the switch is disabled there rather than silently doing nothing.
  await page.locator("#mesh-notation").selectOption("products");
  await expect(page.locator('[data-node-id="catalog:cat_1"]')).toBeVisible();
  await expect(page.locator("#mesh-drafts")).toBeDisabled();

  await page.locator("#mesh-notation").selectOption("atlas");
  await expect(page.locator('[data-node-id="application:a1"]')).toBeVisible();
  await expect(page.locator("#mesh-drafts")).toBeEnabled();
  expect(page.__errors).toEqual([]);
});
