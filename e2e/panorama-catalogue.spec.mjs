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
    { id: "application:a1", kind: "application", name: "Workplace", provenance: "derived", state: "unbound", severity: "unknown" },
    { id: "process:1", kind: "process", name: "Provision a phone", provenance: "derived", application: "application:a1", processId: "provision-phone", version: 1, state: "healthy", severity: "ok" },
    { id: "catalog:cat_1", kind: "catalog", name: "Mobile devices", provenance: "derived", state: "unbound", severity: "unknown" },
    { id: "product:package", kind: "product", name: "Phone package", provenance: "derived", catalog: "catalog:cat_1", state: "unbound", severity: "unknown" },
    { id: "product:phone", kind: "product", name: "Apple iPhone", provenance: "derived", catalog: "catalog:cat_1", state: "unbound", severity: "unknown" },
    { id: "product:case", kind: "product", name: "Protective case", provenance: "derived", catalog: "catalog:cat_1", state: "unbound", severity: "unknown" },
    { id: "unresolved:process:revoke-phone", kind: "unresolved", name: "revoke-phone", provenance: "derived", state: "unbound", severity: "unknown" },
  ],
  edges: [
    { from: "application:a1", to: "process:1", kind: "contains" },
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

function installMock(page, mesh = graph) {
  page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path === "/api/v1/panorama/mesh") return route.fulfill({ json: mesh });
    if (path === "/api/v1/panorama/notations") return route.fulfill({ json: notations });
    return route.fulfill({ json: [] });
  });
}

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  installMock(page);
});

test("the catalogue is drawn as its own family, not as more processes", async ({ page }) => {
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toBeVisible();

  await expect(page.locator(".mesh-node")).toHaveCount(7);
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
  await page.goto("/index.html#/panorama/starmap");
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
  await page.goto("/index.html#/panorama/starmap");

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

test("in ArchiMate's vocabulary the catalogue is named in ArchiMate's words", async ({ page }) => {
  await page.goto("/index.html#/panorama/starmap");
  await page.locator("#mesh-notation").selectOption("archimate-3.2");

  const legend = page.locator(".mesh-legend");
  await expect(legend).toContainText("Grouping");
  await expect(legend).toContainText("Product");

  // And drawn in ArchiMate's own outlines rather than kept in Atlas's cards: a
  // Grouping's tabbed corner and a Product's bar are silhouettes, so both leave the
  // rectangle family the derived picture draws them in.
  const tagOf = (id) => page.locator(`[data-node-id="${id}"] .mesh-body`)
    .evaluate((el) => el.tagName.toLowerCase());
  expect(await tagOf("catalog:cat_1")).toBe("polygon");
  expect(await tagOf("product:phone")).toBe("polygon");
  // The two relationships this landscape can finally name exactly rather than
  // approximately.
  await expect(legend).toContainText("Composition");
  await expect(legend).toContainText("Aggregation");
  expect(page.__errors).toEqual([]);
});
