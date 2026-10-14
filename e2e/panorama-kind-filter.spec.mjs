import { test, expect } from "@playwright/test";

// Switching an element type off on the starmap.
//
// The Product Map draws four kinds at once — catalogues, products, the processes
// those products bind, and a placeholder where nothing is deployed — and a reader
// who came to look at one of them has no way to put the other three down. The
// search narrows by *name*, which is the wrong question: "show me the catalogues"
// is not a string anybody can type.
//
// These drive the real view against a mocked payload, because the whole of this
// lives in the browser: the server keeps sending the same picture and the page
// decides what of it to draw.

const products = {
  nodes: [
    { id: "process:1", kind: "process", name: "Provision a phone", provenance: "derived", processId: "provision-phone", version: 1, state: "healthy", severity: "ok" },
    { id: "catalog:cat_1", kind: "catalog", name: "Mobile devices", provenance: "derived", state: "unbound", severity: "unknown" },
    { id: "product:package", kind: "product", name: "Phone package", provenance: "derived", catalog: "catalog:cat_1", state: "unbound", severity: "unknown" },
    { id: "product:phone", kind: "product", name: "Apple iPhone", provenance: "derived", catalog: "catalog:cat_1", state: "unbound", severity: "unknown" },
    { id: "unresolved:process:revoke-phone", kind: "unresolved", name: "revoke-phone", provenance: "derived", state: "unbound", severity: "unknown" },
  ],
  edges: [
    { from: "catalog:cat_1", to: "product:package", kind: "offers" },
    { from: "catalog:cat_1", to: "product:phone", kind: "offers" },
    { from: "product:package", to: "product:phone", kind: "composition" },
    { from: "product:phone", to: "process:1", kind: "uses" },
    { from: "product:phone", to: "unresolved:process:revoke-phone", kind: "uses" },
  ],
  restricted: 0,
  clustered: false,
};

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

const notations = [
  {
    id: "atlas", label: "Atlas (derived)", short: "Atlas", projection: false, mappingVersion: 2,
    types: {}, relations: {}, loss: [],
  },
  {
    id: "archimate-3.2", label: "ArchiMate 3.2", short: "ArchiMate",
    projection: true, mappingVersion: 2,
    types: {
      application: { name: "Application Component", type: "ApplicationComponent" },
      process: { name: "Application Process", type: "ApplicationProcess" },
      worker: { name: "Technology Service", type: "TechnologyService" },
    },
    relations: {
      contains: { name: "Assignment", type: "Assignment" },
      uses: { name: "Serving", type: "Serving", flip: true },
    },
    loss: [],
  },
];

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.route("**/api/v1/**", (route) => {
    const url = new URL(route.request().url());
    if (url.pathname.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (url.pathname === "/api/v1/panorama/mesh") {
      return route.fulfill({ json: url.searchParams.get("view") === "products" ? products : estate });
    }
    if (url.pathname === "/api/v1/panorama/notations") return route.fulfill({ json: notations });
    return route.fulfill({ json: [] });
  });
});

test.afterEach(async ({ page }) => {
  expect(page.__errors).toEqual([]);
});

async function openProductMap(page) {
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toBeVisible();
  await page.locator("#mesh-notation").selectOption("products");
  await expect(page.locator('[data-node-id="catalog:cat_1"]')).toBeVisible();
}

const box = (page, kind) => page.locator(`#mesh-kinds input[data-kind="${kind}"]`);

test("every element type on the picture is offered, and every one is on", async ({ page }) => {
  await openProductMap(page);

  // One box per kind the server sent, and no box for a kind it did not: a control
  // offering to hide something that is not there is a control that does nothing.
  await expect(page.locator("#mesh-kinds input[type=checkbox]")).toHaveCount(4);
  for (const kind of ["catalog", "product", "process", "unresolved"]) {
    await expect(box(page, kind)).toBeChecked();
  }
  await expect(page.locator("#mesh-kinds")).not.toContainText("Worker");
  await expect(page.locator(".mesh-node")).toHaveCount(5);
});

test("switching a type off takes it off the picture, with its lines", async ({ page }) => {
  await openProductMap(page);
  await box(page, "product").uncheck();

  await expect(page.locator(".mesh-node")).toHaveCount(3);
  await expect(page.locator('[data-node-id="product:phone"]')).toHaveCount(0);
  await expect(page.locator('[data-node-id="catalog:cat_1"]')).toBeVisible();
  await expect(page.locator('[data-node-id="process:1"]')).toBeVisible();

  // An edge whose other end is gone is not a line anybody can read. Both of the
  // catalogue's `offers` and both of the phone's are drawn between a survivor and
  // something no longer here, so the only edge left is none.
  await expect(page.locator(".mesh-edge")).toHaveCount(0);
});

test("nothing is reached through a type that is switched off", async ({ page }) => {
  // The trap this exists for, and the reason the type cut runs *before* the search
  // and the drilldown rather than after them. Both of those reach outwards for
  // context; cut afterwards, they would reach *through* a hidden kind and leave
  // whatever they found on the canvas.
  //
  // Concretely: the catalogue's only neighbours are its products. Going into it two
  // hops deep with products switched off must reach nothing — the process and the
  // unresolved marker sit behind a product, and putting them on screen would be the
  // picture drawing a dependency chain through a link the reader cannot see.
  await openProductMap(page);
  await box(page, "product").uncheck();

  await page.locator('[data-node-id="catalog:cat_1"] .mesh-body').click();
  await page.locator("#mesh-drill-in").click();

  await expect(page.locator('[data-node-id="catalog:cat_1"]')).toBeVisible();
  await expect(page.locator(".mesh-node")).toHaveCount(1);
  for (const id of ["product:phone", "product:package", "process:1", "unresolved:process:revoke-phone"]) {
    await expect(page.locator(`[data-node-id="${id}"]`)).toHaveCount(0);
  }
});

test("a search keeps its context, minus the types that are off", async ({ page }) => {
  await openProductMap(page);
  await box(page, "product").uncheck();

  await page.locator("#mesh-search").fill("Mobile devices");
  await expect(page.locator('[data-node-id="catalog:cat_1"]')).toBeVisible();
  await expect(page.locator('[data-node-id="product:phone"]')).toHaveCount(0);
  await expect(page.locator('[data-node-id="product:package"]')).toHaveCount(0);
});

test("a type that is switched off keeps its box, or it could never come back", async ({ page }) => {
  // The list is built from what the server delivered, not from what survives the
  // filter. Built from the survivors, unticking a kind would remove its own box and
  // the picture could not be widened again from any control on screen.
  await openProductMap(page);
  await box(page, "product").uncheck();

  await expect(box(page, "product")).toBeVisible();
  await expect(box(page, "product")).not.toBeChecked();

  await box(page, "product").check();
  await expect(page.locator(".mesh-node")).toHaveCount(5);
});

test("the count describes the picture, not the payload", async ({ page }) => {
  await openProductMap(page);
  await expect(page.locator("#mesh-count")).toContainText("5 node(s)");

  await box(page, "product").uncheck();
  // Three nodes and no edge, said as such. The old total would have the header
  // reporting five over a canvas holding three.
  await expect(page.locator("#mesh-count")).toContainText("3 node(s)");
  await expect(page.locator("#mesh-count")).not.toContainText("5 node(s)");
});

test("the key stops explaining what is no longer drawn", async ({ page }) => {
  await openProductMap(page);
  await expect(page.locator("#mesh-legend-slot")).toContainText("Product");

  await box(page, "product").uncheck();
  await expect(page.locator("#mesh-legend-slot")).not.toContainText("Product");
  await expect(page.locator("#mesh-legend-slot")).toContainText("Catalogue");
});

test("switching everything off says so, rather than reading as a search that missed", async ({ page }) => {
  await openProductMap(page);
  for (const kind of ["catalog", "product", "process", "unresolved"]) {
    await box(page, kind).uncheck();
  }
  await expect(page.locator(".mesh-node")).toHaveCount(0);
  // "Nothing matches that" would send a reader to the search box, which is not
  // where the picture went.
  const empty = page.locator(".mesh-empty-filter");
  await expect(empty).toContainText("element type");
  await expect(empty).not.toContainText("Nothing matches");
});

test("a saved view carries which types were on", async ({ page }) => {
  await openProductMap(page);
  await box(page, "product").uncheck();

  await page.locator("#mesh-view-name").fill("Catalogues only");
  await page.locator("#mesh-view-save button[type=submit]").click();

  // Put them back, then reopen: a view is the whole question somebody saved, and
  // one that reopens with everything on answers a different one.
  await box(page, "product").check();
  await expect(page.locator(".mesh-node")).toHaveCount(5);

  await page.locator("#mesh-view-list button", { hasText: "Catalogues only" }).first().click();
  await expect(page.locator(".mesh-node")).toHaveCount(3);
  await expect(box(page, "product")).not.toBeChecked();
});

test("the boxes are named in whatever vocabulary the picture is read in", async ({ page }) => {
  // The same rule the key follows: a reader on a projection should not have to
  // translate the control back into Atlas's own words to use it.
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toBeVisible();
  await expect(page.locator("#mesh-kinds")).toContainText("Worker");

  await page.locator("#mesh-notation").selectOption("archimate-3.2");
  await expect(page.locator("#mesh-kinds")).toContainText("Technology Service");
});

test("the landscape has the same control", async ({ page }) => {
  // It is the same question on both subjects — "which of the things on this picture
  // do I want to see" — so it is the same control, listing whatever that picture
  // holds.
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toBeVisible();
  await expect(page.locator("#mesh-kinds input[type=checkbox]")).toHaveCount(3);

  await box(page, "worker").uncheck();
  await expect(page.locator('[data-node-id="worker:w1"]')).toHaveCount(0);
  await expect(page.locator('[data-node-id="process:1"]')).toBeVisible();
});
