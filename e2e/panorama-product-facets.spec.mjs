import { test, expect } from "@playwright/test";

// Narrowing the starmap by what the catalogue says an offering is.
//
// The element-type filter beside this one answers "which kinds of thing do I want
// to see". It cannot answer "show me only what is actually orderable", because
// that is not a kind — it is a property of one kind, and the search cannot answer
// it either: a product's state is not a word in its name.
//
// Two facets, because the catalogue keeps two facts about an offering that nothing
// else on the picture has. What state it is in, and whether ordering it stops for
// an approver.
//
// These drive the real view against a mocked payload: the server sends the two
// facts on the product node and the page decides what to draw with them.

const products = {
  nodes: [
    { id: "catalog:cat_1", kind: "catalog", name: "Mobile devices", provenance: "derived", state: "unbound", severity: "unknown" },
    // One product per combination worth telling apart. "superior" and "role" are
    // two different routes to the same answer for this control — both stop for a
    // human — which is the reading the picture makes and the store does not.
    { id: "product:phone", kind: "product", name: "Apple iPhone", provenance: "derived", catalog: "catalog:cat_1", productState: "active", approvalKind: "superior", state: "unbound", severity: "unknown" },
    { id: "product:case", kind: "product", name: "Phone case", provenance: "derived", catalog: "catalog:cat_1", productState: "active", approvalKind: "none", state: "unbound", severity: "unknown" },
    { id: "product:pager", kind: "product", name: "Pager", provenance: "derived", catalog: "catalog:cat_1", productState: "withdrawn", approvalKind: "none", state: "unbound", severity: "unknown" },
    { id: "product:watch", kind: "product", name: "Smart watch", provenance: "derived", catalog: "catalog:cat_1", productState: "draft", approvalKind: "role", state: "unbound", severity: "unknown" },
    // Bound by the phone alone, which is what makes it the orphan case below.
    { id: "process:1", kind: "process", name: "Provision a phone", provenance: "derived", processId: "provision-phone", version: 1, state: "healthy", severity: "ok" },
  ],
  edges: [
    { from: "catalog:cat_1", to: "product:phone", kind: "offers" },
    { from: "catalog:cat_1", to: "product:case", kind: "offers" },
    { from: "catalog:cat_1", to: "product:pager", kind: "offers" },
    { from: "catalog:cat_1", to: "product:watch", kind: "offers" },
    { from: "product:phone", to: "process:1", kind: "uses" },
  ],
  restricted: 0,
  clustered: false,
};

// A landscape has no products at all, which is the case that must carry no control.
const estate = {
  nodes: [
    { id: "application:a1", kind: "application", name: "Workplace", provenance: "derived", state: "unbound", severity: "unknown" },
    { id: "process:1", kind: "process", name: "Provision a phone", provenance: "derived", application: "application:a1", processId: "provision-phone", version: 1, state: "healthy", severity: "ok" },
  ],
  edges: [{ from: "application:a1", to: "process:1", kind: "contains" }],
  restricted: 0,
  clustered: false,
};

const notations = [
  {
    id: "atlas", label: "Atlas (derived)", short: "Atlas", projection: false, mappingVersion: 2,
    types: {}, relations: {}, loss: [],
  },
];

let payload = products;

test.beforeEach(async ({ page }) => {
  payload = products;
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.route("**/api/v1/**", (route) => {
    const url = new URL(route.request().url());
    if (url.pathname.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (url.pathname === "/api/v1/panorama/mesh") {
      return route.fulfill({ json: url.searchParams.get("view") === "products" ? payload : estate });
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

const state = (page, value) => page.locator(`#mesh-states input[data-state="${value}"]`);
const approval = (page, value) => page.locator(`#mesh-approvals input[data-approval="${value}"]`);

test("every state and both approval answers are offered, and every one is on", async ({ page }) => {
  await openProductMap(page);

  // One box per value the delivered picture actually holds, and none for a value it
  // does not: a control offering to hide something that is not there does nothing
  // but invite a click that changes no picture.
  await expect(page.locator("#mesh-states input[type=checkbox]")).toHaveCount(3);
  await expect(page.locator("#mesh-approvals input[type=checkbox]")).toHaveCount(2);
  for (const value of ["active", "withdrawn", "draft"]) {
    await expect(state(page, value)).toBeChecked();
  }
  for (const value of ["required", "none"]) {
    await expect(approval(page, value)).toBeChecked();
  }
  // Everything the server sent is drawn until somebody says otherwise: a new
  // control must not change the picture anybody had before it existed.
  await expect(page.locator(".mesh-node")).toHaveCount(6);
});

test("switching a state off takes those products off, with their lines", async ({ page }) => {
  await openProductMap(page);
  await state(page, "withdrawn").uncheck();

  await expect(page.locator('[data-node-id="product:pager"]')).toHaveCount(0);
  await expect(page.locator(".mesh-node")).toHaveCount(5);
  // And the line to it, because a line to nothing is not a claim about the estate.
  // Named by its two ends rather than counted: this canvas draws edges the payload
  // does not list, so a total would be a guess about the drawing and this is the
  // fact the cut is responsible for.
  await expect(page.locator('.mesh-edge[data-to="product:pager"]')).toHaveCount(0);
  await expect(page.locator('.mesh-edge[data-to="product:case"]')).toHaveCount(1);
});

test("a facet belonging to products never removes anything else", async ({ page }) => {
  // The property this control turns on, and the sharp edge is the approval one
  // rather than the state one.
  //
  // A catalogue carries no approval rule, and "carries no approval rule" is exactly
  // how a product without one reads. So a filter that forgot to ask what kind of
  // node it was looking at would answer "ordered without approval" for the
  // catalogue, for the process, and for every other thing on the picture — and
  // unticking that one box would empty the canvas. Unticking the states alone would
  // not show it: nothing but a product carries a state at all, so the state check
  // never fires on them and the bug hides.
  await openProductMap(page);
  await approval(page, "none").uncheck();

  await expect(page.locator('[data-node-id="catalog:cat_1"]')).toBeVisible();
  await expect(page.locator('[data-node-id="process:1"]')).toBeVisible();

  // And now the states too, so nothing on the picture is a product any more.
  for (const value of ["active", "withdrawn", "draft"]) {
    await state(page, value).uncheck();
  }
  await expect(page.locator(".mesh-node")).toHaveCount(2);
  await expect(page.locator('[data-node-id="catalog:cat_1"]')).toBeVisible();
  // The process stays although the only product that bound it is gone. That is the
  // same thing switching the whole Product type off already does, and it is the
  // honest answer: a deployed process is part of the estate in its own right, not
  // an appendage of whatever offers it.
  await expect(page.locator('[data-node-id="process:1"]')).toBeVisible();
});

test("switching an approval answer off takes those products off", async ({ page }) => {
  await openProductMap(page);
  await approval(page, "none").uncheck();

  // The two that stop for a human survive; the two that do not are gone. A kind
  // this build has never seen would count as stopping for a human too, which is
  // why the reading is "anything that is not none" rather than a list.
  await expect(page.locator('[data-node-id="product:phone"]')).toBeVisible();
  await expect(page.locator('[data-node-id="product:watch"]')).toBeVisible();
  await expect(page.locator('[data-node-id="product:case"]')).toHaveCount(0);
  await expect(page.locator('[data-node-id="product:pager"]')).toHaveCount(0);
});

test("the two facets narrow together rather than fighting", async ({ page }) => {
  // Each one removes what it names, so a product needs to survive both. Only the
  // phone is active *and* stops for an approver.
  await openProductMap(page);
  await state(page, "withdrawn").uncheck();
  await state(page, "draft").uncheck();
  await approval(page, "none").uncheck();

  await expect(page.locator('[data-node-id="product:phone"]')).toBeVisible();
  for (const id of ["product:case", "product:pager", "product:watch"]) {
    await expect(page.locator(`[data-node-id="${id}"]`)).toHaveCount(0);
  }
});

test("nothing is reached through a product that is switched off", async ({ page }) => {
  // The trap this exists for, and the reason the cut runs *before* the drilldown
  // rather than after it. A drilldown reaches outwards for context; cut afterwards
  // it would reach *through* a hidden product and leave what it found stranded.
  //
  // Concretely: the process sits two hops from the catalogue, behind the phone.
  // With the phone's state switched off, going into the catalogue two deep must not
  // put the process on screen — that would be the picture drawing a chain through a
  // link the reader cannot see.
  await openProductMap(page);
  await state(page, "active").uncheck();

  await page.locator('[data-node-id="catalog:cat_1"] .mesh-body').click();
  await page.locator("#mesh-drill-in").click();

  await expect(page.locator('[data-node-id="catalog:cat_1"]')).toBeVisible();
  await expect(page.locator('[data-node-id="product:phone"]')).toHaveCount(0);
  await expect(page.locator('[data-node-id="process:1"]')).toHaveCount(0);
});

test("a state that is switched off keeps its box, or it could never come back", async ({ page }) => {
  // The list is built from what the server delivered, not from what survives the
  // filter. Built from the survivors, unticking the last withdrawn product would
  // remove its own box and the picture could not be widened again from any control
  // on screen.
  await openProductMap(page);
  await state(page, "withdrawn").uncheck();

  await expect(state(page, "withdrawn")).toBeVisible();
  await expect(state(page, "withdrawn")).not.toBeChecked();

  await state(page, "withdrawn").check();
  await expect(page.locator(".mesh-node")).toHaveCount(6);
});

test("the count describes the picture, not the payload", async ({ page }) => {
  await openProductMap(page);
  await expect(page.locator("#mesh-count")).toContainText("6 node(s)");

  await state(page, "draft").uncheck();
  await expect(page.locator("#mesh-count")).toContainText("5 node(s)");
  await expect(page.locator("#mesh-count")).not.toContainText("6 node(s)");
});

test("emptying the canvas by facet says so, and does not blame the type filter", async ({ page }) => {
  // Three empty canvases need three different remedies. Sending a reader to the
  // element-type boxes when it was the state boxes that closed the picture is the
  // same defect as sending them to a search they never typed.
  await openProductMap(page);
  await page.locator('#mesh-kinds input[data-kind="catalog"]').uncheck();
  await page.locator('#mesh-kinds input[data-kind="process"]').uncheck();
  for (const value of ["active", "withdrawn", "draft"]) {
    await state(page, value).uncheck();
  }

  await expect(page.locator(".mesh-node")).toHaveCount(0);
  const empty = page.locator(".mesh-empty-filter");
  await expect(empty).toContainText("Every product is filtered out");
  await expect(empty).not.toContainText("Nothing matches");
  await expect(empty).not.toContainText("Every element type");
});

test("a saved view carries which products were on", async ({ page }) => {
  await openProductMap(page);
  await state(page, "withdrawn").uncheck();
  await approval(page, "none").uncheck();

  await page.locator("#mesh-view-name").fill("Approved and live");
  await page.locator("#mesh-view-save button[type=submit]").click();

  await state(page, "withdrawn").check();
  await approval(page, "none").check();
  await expect(page.locator(".mesh-node")).toHaveCount(6);

  await page.locator("#mesh-view-list button", { hasText: "Approved and live" }).first().click();
  await expect(state(page, "withdrawn")).not.toBeChecked();
  await expect(approval(page, "none")).not.toBeChecked();
  await expect(page.locator('[data-node-id="product:phone"]')).toBeVisible();
  await expect(page.locator('[data-node-id="product:case"]')).toHaveCount(0);
});

test("a state this build has no wording for still gets a box", async ({ page }) => {
  // A catalogue state from a store that has moved on. The element-type filter may
  // reasonably not offer a kind it cannot draw; a state it does not recognise is a
  // value that is plainly on the picture, and leaving it boxless would make it the
  // one thing a reader cannot put down.
  payload = {
    ...products,
    nodes: products.nodes.map((n) =>
      n.id === "product:pager" ? { ...n, productState: "archived" } : n),
  };
  await openProductMap(page);

  const odd = state(page, "archived");
  await expect(odd).toBeChecked();
  await odd.uncheck();
  await expect(page.locator('[data-node-id="product:pager"]')).toHaveCount(0);
});

test("a product the server says nothing about stays drawn", async ({ page }) => {
  // A payload from a build before these facets existed. The polarity is what makes
  // this work: the sets hold what is switched *off*, so a product with no state has
  // nothing switched off against it and is drawn — the picture such a payload
  // always produced.
  payload = {
    ...products,
    nodes: products.nodes.map((n) =>
      n.id === "product:pager" ? { ...n, productState: undefined } : n),
  };
  await openProductMap(page);

  await expect(page.locator("#mesh-states input[type=checkbox]")).toHaveCount(2);
  await state(page, "active").uncheck();
  await state(page, "draft").uncheck();
  await expect(page.locator('[data-node-id="product:pager"]')).toBeVisible();
});

test("a picture with no products carries no control for one", async ({ page }) => {
  // The landscape has no offerings, so it has no state and no approver, and a
  // heading over an empty space is a control that reads as broken.
  await page.goto("/index.html#/panorama/starmap");
  await expect(page.locator(".mesh-canvas")).toBeVisible();
  await expect(page.locator("#mesh-states")).toBeHidden();
  await expect(page.locator("#mesh-states-head")).toBeHidden();
  await expect(page.locator("#mesh-approvals")).toBeHidden();
  await expect(page.locator("#mesh-approvals-head")).toBeHidden();
  // The element-type boxes are unaffected: they are about this picture, which has
  // kinds on it.
  await expect(page.locator("#mesh-kinds input[type=checkbox]")).toHaveCount(2);
});

test("a facet with only one value offers no choice", async ({ page }) => {
  // Every product active is the ordinary state of a tidy catalogue, and one box
  // that can only empty the picture is a control that can only do damage.
  payload = {
    ...products,
    nodes: products.nodes.map((n) =>
      n.kind === "product" ? { ...n, productState: "active" } : n),
  };
  await openProductMap(page);

  await expect(page.locator("#mesh-states")).toBeHidden();
  await expect(page.locator("#mesh-states-head")).toBeHidden();
  // Approval still varies here, so that one stays.
  await expect(page.locator("#mesh-approvals input[type=checkbox]")).toHaveCount(2);
});

// The half of this that the canvas cannot show.
//
// A renderer needs two points to draw a line, so an edge pointing at a node that
// was just filtered away never reaches the screen whether the cut removed it or
// not. But the graph is not only drawn: it is counted, drilled into, searched and
// exported, and every one of those walks the edge list. An edge to nothing left in
// it is a claim that a product is still attached to something — invisible on the
// canvas and wrong everywhere else.
test.describe("the narrowing as a function over the graph", () => {
  test.beforeEach(async ({ page }) => {
    await page.goto("/panorama-facets-harness.html");
    await expect(page.locator("#ready")).toHaveText("ready");
  });

  test("an edge whose product is gone goes with it", async ({ page }) => {
    const out = await page.evaluate(() => {
      const graph = {
        nodes: [
          { id: "catalog:c", kind: "catalog" },
          { id: "product:live", kind: "product", productState: "active", approvalKind: "none" },
          { id: "product:old", kind: "product", productState: "withdrawn", approvalKind: "none" },
        ],
        edges: [
          { from: "catalog:c", to: "product:live", kind: "offers" },
          { from: "catalog:c", to: "product:old", kind: "offers" },
          { from: "product:live", to: "product:old", kind: "composition" },
        ],
      };
      const cut = window.facets.withoutProducts(graph, new Set(["withdrawn"]), new Set());
      return {
        nodes: cut.nodes.map((n) => n.id),
        edges: cut.edges.map((e) => `${e.from}->${e.to}`),
        // The ordinary case must not cost a copy of the landscape on every repaint.
        untouched: window.facets.withoutProducts(graph, new Set(), new Set()) === graph,
      };
    });

    expect(out.nodes).toEqual(["catalog:c", "product:live"]);
    // Both edges that ended on the withdrawn product are gone, including the one
    // between two products where only the far end was filtered.
    expect(out.edges).toEqual(["catalog:c->product:live"]);
    expect(out.untouched).toBe(true);
  });

  test("the buckets are read off the nodes, not guessed", async ({ page }) => {
    const out = await page.evaluate(() => {
      const graph = {
        nodes: [
          { id: "catalog:c", kind: "catalog" },
          { id: "process:p", kind: "process" },
          { id: "product:a", kind: "product", productState: "active", approvalKind: "none" },
          // A rule kind this build has never heard of. It still stops an order for
          // somebody, so it belongs in the same bucket as "superior".
          { id: "product:b", kind: "product", productState: "active", approvalKind: "four-eyes-board" },
          { id: "product:c", kind: "product", productState: "retired-2019" },
        ],
        edges: [],
      };
      return {
        states: window.facets.statesPresent(graph),
        approvals: window.facets.approvalsPresent(graph),
        // Only the product with no rule at all and the one that names "none".
        withoutRule: window.facets
          .withoutProducts(graph, new Set(), new Set(["required"]))
          .nodes.map((n) => n.id),
        // A stored view carrying an empty state. No box can produce one —
        // statesPresent never offers a state no product has — so the only way it
        // arrives is out of a saved view somebody or something wrote by hand. It
        // must not match the products the server sent no state for: the sets hold
        // what is switched *off*, and "the server told us nothing" is not an
        // opinion against drawing something.
        blankStored: window.facets
          .withoutProducts(
            { nodes: [{ id: "product:quiet", kind: "product" }], edges: [] },
            new Set([""]), new Set(),
          ).nodes.map((n) => n.id),
      };
    });

    // Known states first in the catalogue's own order, then whatever else is
    // actually on the picture — so nothing on it is boxless.
    expect(out.states).toEqual(["active", "retired-2019"]);
    // A catalogue and a process contribute nothing: neither is an offering.
    expect(out.approvals).toEqual(["required", "none"]);
    expect(out.withoutRule).toEqual(["catalog:c", "process:p", "product:a", "product:c"]);
    expect(out.blankStored).toEqual(["product:quiet"]);
  });
});
