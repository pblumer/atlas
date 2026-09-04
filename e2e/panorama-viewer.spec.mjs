import { test, expect } from "@playwright/test";

const modelId = "11111111111111111111111111111111";

const xml = `<?xml version="1.0" encoding="UTF-8"?>
<model xmlns="http://www.opengroup.org/xsd/archimate/3.0/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" identifier="model-landscape">
  <name xml:lang="en">Order landscape</name>
  <elements>
    <element identifier="cap-orders" xsi:type="Capability"><name xml:lang="en">Fulfil orders</name></element>
    <element identifier="app-orders" xsi:type="ApplicationComponent"><name xml:lang="en">Order Service</name>
      <documentation xml:lang="en">Owns the **order lifecycle**.

## Boundaries

- takes orders from the shop
- never prices them
</documentation></element>
    <element identifier="svc-orders" xsi:type="ApplicationService"><name xml:lang="en">Order API</name></element>
    <element identifier="node-prod" xsi:type="Node"><name xml:lang="en">Production node</name></element>
  </elements>
  <relationships>
    <relationship identifier="rel-realize" source="app-orders" target="svc-orders" xsi:type="Realization"><name xml:lang="en">offers</name></relationship>
  </relationships>
  <views><diagrams>
    <view identifier="view-app" xsi:type="Diagram"><name xml:lang="en">Application cooperation</name>
      <node identifier="n-app" elementRef="app-orders" xsi:type="Element" x="80" y="100" w="190" h="80"/>
      <node identifier="n-svc" elementRef="svc-orders" xsi:type="Element" x="380" y="105" w="170" h="70"/>
      <connection identifier="c-realize" relationshipRef="rel-realize" xsi:type="Relationship" source="n-app" target="n-svc"/>
    </view>
    <view identifier="view-tech" xsi:type="Diagram"><name xml:lang="en">Technology deployment</name>
      <node identifier="n-node" elementRef="node-prod" xsi:type="Element" x="100" y="80" w="200" h="90"/>
    </view>
  </diagrams></views>
</model>`;

// subset is what the server says may be authored. The palette and the connect menu
// are both built from it, so this fixture is what decides what the canvas offers.
const subset = {
  version: 1,
  elements: [
    { type: "ApplicationComponent", label: "Application component", layer: "application", aspect: "active" },
    { type: "ApplicationService", label: "Application service", layer: "application", aspect: "behavior" },
    { type: "Node", label: "Node", layer: "technology", aspect: "active" },
  ],
  relationships: [
    { type: "Realization", label: "Realization", rule: "Something concrete makes something more abstract real." },
    { type: "Association", label: "Association", rule: "Related in a way the others do not capture." },
  ],
  matrix: {
    "ApplicationComponent>ApplicationService": ["Realization", "Association"],
    "ApplicationComponent>Node": ["Association"],
  },
  limits: [
    { limit: "an authoring subset, not all of ArchiMate 3.2", reason: "Atlas authors the listed types and refuses the rest." },
  ],
};

function installMock(page, { role = "owner", onLayout, onAdd, onConnect,
  served = subset } = {}) {
  page.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path === "/api/v1/applications") {
      return route.fulfill({ json: [{ id: "app-1", name: "Enterprise Architecture", myRole: role }] });
    }
    if (path === "/api/v1/panorama/models") {
      return route.fulfill({ json: [{ id: modelId, applicationId: "app-1", name: "Order landscape", notation: "archimate-3.2", revision: 4, updatedAt: 1700000000 }] });
    }
    if (path === `/api/v1/panorama/models/${modelId}`) {
      return route.fulfill({ json: { id: modelId, applicationId: "app-1", name: "Order landscape", notation: "archimate-3.2", revision: 4, updatedAt: 1700000000 } });
    }
    if (path === `/api/v1/panorama/models/${modelId}/xml`) {
      return route.fulfill({ contentType: "application/xml", body: xml });
    }
    if (path === "/api/v1/panorama/subset") {
      if (!served) return route.fulfill({ status: 500, json: { error: "no subset" } });
      return route.fulfill({ json: served });
    }
    if (path === `/api/v1/panorama/models/${modelId}/elements`) {
      onAdd?.(JSON.parse(request.postData() || "{}"));
      return route.fulfill({ json: { id: modelId, revision: 5, createdId: "n-new" } });
    }
    if (path === `/api/v1/panorama/models/${modelId}/relationships`) {
      onConnect?.(JSON.parse(request.postData() || "{}"));
      return route.fulfill({ json: { id: modelId, revision: 5, createdId: "rel-new" } });
    }
    if (path === `/api/v1/panorama/models/${modelId}/layout`) {
      onLayout?.(JSON.parse(request.postData() || "{}"));
      return route.fulfill({ json: { id: modelId, revision: 5 } });
    }
    return route.fulfill({ json: [] });
  });
}

test("opens Open Exchange views in the read-only ArchiMate canvas", async ({ page }) => {
  installMock(page);
  const writes = [];
  page.on("request", (request) => {
    if (request.url().includes("/panorama/") && request.method() !== "GET") writes.push(request.method());
  });
  const pageErrors = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));

  await page.goto("/index.html#/panorama");
  await page.getByRole("link", { name: "Order landscape" }).click();

  await expect(page).toHaveURL(new RegExp(`#\/panorama\/models\/${modelId}$`));
  await expect(page.locator(".panorama-editor .crumb-current")).toHaveText("Order landscape");
  await expect(page.locator(".panorama-view-tab.active")).toHaveText("Application cooperation");
  await expect(page.locator('.djs-element[data-element-id="n-app"]')).toBeVisible();
  await expect(page.locator('.djs-element[data-element-id="n-svc"]')).toBeVisible();
  await expect(page.locator(".panorama-canvas")).toContainText("Order Service");
  await expect(page.locator(".panorama-canvas")).toContainText("Order API");

  await page.locator('.djs-element[data-element-id="n-app"]').click();
  await expect(page.locator(".panorama-properties")).toContainText("Application Component");
  await expect(page.locator(".panorama-properties")).toContainText("app-orders");

  await page.getByRole("tab", { name: "Technology deployment" }).click();
  await expect(page.locator('.djs-element[data-element-id="n-node"]')).toBeVisible();
  await expect(page.locator(".panorama-canvas")).toContainText("Production node");
  // Opening a model writes nothing. An editable canvas is still a canvas nobody has
  // touched, so it says it has no unsaved changes rather than claiming an edit.
  await expect(page.locator(".panorama-status")).toContainText("No unsaved changes");
  expect(writes).toEqual([]);
  expect(pageErrors).toEqual([]);
});

// A reader's canvas carries no authoring at all — not a disabled toolbar, but a
// diagram-js with no modeling behaviour loaded (see the Viewer's `editable`
// option). The badge says so, because a surface that looks editable and refuses
// every edit is worse than one that never offered.
test("a viewer gets a read-only canvas", async ({ page }) => {
  installMock(page, { role: "viewer" });
  await page.goto(`/index.html#/panorama/models/${modelId}`);

  await expect(page.locator(".panorama-status")).toContainText("Read only");
  await expect(page.locator('[data-tool="save"]')).toHaveCount(1);
  await expect(page.locator('[data-tool="save"]')).toBeDisabled();
  await expect(page.locator('[data-tool="undo"]')).toBeDisabled();
});

// Moving a shape and saving it (ADR-0189 §2, P2a).
//
// The save sends the shapes that moved, never the document: a browser's
// XMLSerializer normalises, and §2 requires that nothing outside the edit changes.
// So the request body is the assertion that matters here.
test("moves a shape and saves what moved, not the document", async ({ page }) => {
  const saves = [];
  installMock(page, { onLayout: (body) => saves.push(body) });
  await page.goto(`/index.html#/panorama/models/${modelId}`);

  const shape = page.locator('.djs-element[data-element-id="n-app"]');
  await expect(shape).toBeVisible();
  const before = await shape.boundingBox();

  // Nothing has moved, so there is nothing to save.
  await expect(page.locator('[data-tool="save"]')).toBeDisabled();

  await page.mouse.move(before.x + before.width / 2, before.y + before.height / 2);
  await page.mouse.down();
  await page.mouse.move(before.x + before.width / 2 + 120, before.y + before.height / 2 + 60, { steps: 12 });
  await page.mouse.up();

  // The toolbar says there is unsaved work, and says how much.
  await expect(page.locator(".panorama-status")).toContainText("1 shape moved");
  await expect(page.locator('[data-tool="save"]')).toBeEnabled();
  await expect(page.locator('[data-tool="undo"]')).toBeEnabled();

  await page.locator('[data-tool="save"]').click();
  await expect.poll(() => saves.length).toBe(1);

  // What went over the wire is a list of moved shapes and the revision they were
  // read at — not XML.
  expect(saves[0].expectedRevision).toBe(4);
  expect(saves[0].changes).toHaveLength(1);
  expect(saves[0].changes[0].nodeId).toBe("n-app");
  expect(saves[0].changes[0].w).toBeGreaterThan(0);
  expect(saves[0].changes[0].h).toBeGreaterThan(0);
  expect(JSON.stringify(saves[0])).not.toContain("<model");
});

// Undo returns the canvas to the document, and the toolbar stops offering a save —
// because dragging a box away and back is not a change, and saving it would bump a
// revision that moved nothing and conflict every other open editor for it.
test("undo puts a moved shape back and there is then nothing to save", async ({ page }) => {
  const saves = [];
  installMock(page, { onLayout: (body) => saves.push(body) });
  await page.goto(`/index.html#/panorama/models/${modelId}`);

  const shape = page.locator('.djs-element[data-element-id="n-app"]');
  const before = await shape.boundingBox();
  await page.mouse.move(before.x + before.width / 2, before.y + before.height / 2);
  await page.mouse.down();
  await page.mouse.move(before.x + before.width / 2 + 90, before.y + before.height / 2, { steps: 10 });
  await page.mouse.up();
  await expect(page.locator(".panorama-status")).toContainText("1 shape moved");

  await page.locator('[data-tool="undo"]').click();
  await expect(page.locator(".panorama-status")).toContainText("No unsaved changes");
  await expect(page.locator('[data-tool="save"]')).toBeDisabled();
  await expect(page.locator('[data-tool="redo"]')).toBeEnabled();
  expect(saves).toEqual([]);
});

// A viewer's canvas does not move under the pointer. The modeling modules are not
// loaded at all for a reader, so this is not a refused drag — there is nothing to
// refuse.
test("a viewer cannot drag a shape", async ({ page }) => {
  installMock(page, { role: "viewer" });
  await page.goto(`/index.html#/panorama/models/${modelId}`);

  const shape = page.locator('.djs-element[data-element-id="n-app"]');
  // The element's own transform, not its screen box: dragging the canvas *pans* it
  // for a reader, which moves the box on screen without moving the shape in the
  // diagram. Panning is navigation and is meant to work here.
  const placed = () => shape.getAttribute("transform");
  const before = await placed();
  const box = await shape.boundingBox();

  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await page.mouse.down();
  await page.mouse.move(box.x + box.width / 2 + 120, box.y + box.height / 2, { steps: 10 });
  await page.mouse.up();

  expect(await placed()).toBe(before);
  await expect(page.locator(".panorama-status")).toContainText("Read only");
  await expect(page.locator('[data-tool="save"]')).toBeDisabled();
});

// The palette is built from the subset the server serves (ADR-0189 §2, P2b), not
// from a list in the browser. A palette offering a type the server will not write
// is a promise the server breaks, and one list is the only way that cannot happen.
test("the palette offers what the server says it can author, and says what it cannot", async ({ page }) => {
  const added = [];
  installMock(page, { onAdd: (body) => added.push(body) });
  await page.goto(`/index.html#/panorama/models/${modelId}`);

  const palette = page.locator(".panorama-palette");
  await expect(palette).toContainText("Application component");
  await expect(palette).toContainText("Node");
  // Grouped by ArchiMate layer, which is how somebody looks for one.
  await expect(palette).toContainText("application");
  await expect(palette).toContainText("technology");
  // Nothing outside the served subset is offered.
  await expect(palette).not.toContainText("Business process");

  // The record forbids claiming complete ArchiMate 3.2 authoring, and a palette is
  // the one thing somebody reads without reading anything else.
  await expect(palette.locator(".panorama-subset-limits")).toContainText("not all of ArchiMate 3.2");

  page.once("dialog", (dialog) => dialog.accept("Billing API"));
  await palette.locator('[data-add-type="ApplicationService"]').click();

  await expect.poll(() => added.length).toBe(1);
  expect(added[0]).toMatchObject({
    type: "ApplicationService", name: "Billing API", expectedRevision: 4, viewId: "view-app",
  });
  // Sent as what to create, never as a document.
  expect(JSON.stringify(added[0])).not.toContain("<model");
});

// A palette that could not be loaded offers nothing rather than guessing, because
// a guess is exactly what the server may refuse.
test("a palette that could not be loaded offers nothing", async ({ page }) => {
  installMock(page, { served: null });
  const pageErrors = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));

  await page.goto(`/index.html#/panorama/models/${modelId}`);
  await expect(page.locator(".panorama-palette")).toContainText("could not be loaded");
  await expect(page.locator("[data-add-type]")).toHaveCount(0);
  // And the model still opens: the palette is additive.
  await expect(page.locator(".panorama-canvas .djs-element").first()).toBeVisible();
  expect(pageErrors).toEqual([]);
});

// The connect menu offers only relationships ArchiMate permits between the two
// elements, read from the same matrix the server enforces with. Somebody authoring
// a model should meet the notation's rules by seeing what is offered, not by being
// refused after the fact.
test("the connect menu offers only what the matrix permits", async ({ page }) => {
  const drawn = [];
  installMock(page, { onConnect: (body) => drawn.push(body) });
  await page.goto(`/index.html#/panorama/models/${modelId}`);
  await page.locator('.djs-element[data-element-id="n-app"]').click();

  const panel = page.locator(".panorama-properties");
  await expect(panel).toContainText("Connect");
  // app-orders is an ApplicationComponent and svc-orders an ApplicationService, so
  // the matrix permits Realization and Association and nothing else.
  const options = panel.locator('[data-connect-to="svc-orders"] option');
  await expect(options).toHaveCount(2);
  await expect(options.nth(0)).toHaveText("Realization");
  await expect(panel).toContainText("Only relationships ArchiMate permits");

  await panel.locator('[data-connect-to="svc-orders"]').selectOption("Realization");
  await panel.locator('[data-connect="svc-orders"]').click();

  await expect.poll(() => drawn.length).toBe(1);
  expect(drawn[0]).toMatchObject({
    type: "Realization", source: "app-orders", target: "svc-orders", expectedRevision: 4,
  });
});

// A pair the subset says nothing may be drawn between is shown saying so, rather
// than left out — the absence is a statement about the notation, and hiding it
// would read as a gap in the page.
test("a pair with nothing permitted says so instead of vanishing", async ({ page }) => {
  installMock(page, {
    served: { ...subset, matrix: { "ApplicationComponent>ApplicationService": [] } },
  });
  await page.goto(`/index.html#/panorama/models/${modelId}`);
  await page.locator('.djs-element[data-element-id="n-app"]').click();

  const panel = page.locator(".panorama-properties");
  await expect(panel).toContainText("nothing may be drawn");
  await expect(panel.locator("[data-connect]")).toHaveCount(0);
});

// A reader gets no palette and no connect menu. Not disabled ones — none: a
// surface that looks like it authors and refuses every attempt is worse than one
// that never offered.
test("a viewer is offered no authoring at all", async ({ page }) => {
  installMock(page, { role: "viewer" });
  await page.goto(`/index.html#/panorama/models/${modelId}`);
  await page.locator('.djs-element[data-element-id="n-app"]').click();

  await expect(page.locator("[data-add-type]")).toHaveCount(0);
  await expect(page.locator("[data-connect]")).toHaveCount(0);
  await expect(page.locator(".panorama-properties")).not.toContainText("Connect");
});

// An element's documentation comes out of a foreign modelling tool and is shown to
// whoever opens the landscape, so it is rendered by the shared Markdown module rather
// than escaped into one paragraph (ADR-0250). The renderer's
// own guarantees — including that this text cannot script the console — are covered in
// markdown.spec.mjs; what is checked here is that the panel renders it and that the
// section's label still reads as the app's, not the author's.
test("an element's documentation is rendered as prose, not as markers", async ({ page }) => {
  installMock(page);
  const pageErrors = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));

  await page.goto(`/index.html#/panorama/models/${modelId}`);
  await page.locator('.djs-element[data-element-id="n-app"]').click();

  const props = page.locator(".panorama-properties");
  const doc = props.locator("section.psec", { has: page.locator(".md") });
  await expect(doc.locator(".md strong")).toHaveText("order lifecycle");
  await expect(doc.locator(".md ul li")).toHaveCount(2);
  await expect(doc.locator(".md ul li").nth(1)).toHaveText("never prices them");

  const label = await doc.locator("> h3").evaluate((el) => getComputedStyle(el).textTransform);
  const heading = await doc.locator(".md h2").evaluate((el) => getComputedStyle(el).textTransform);
  expect(label).toBe("uppercase");
  expect(heading, "the author's heading is not the section's label").toBe("none");

  expect(await doc.locator(".md").innerText()).not.toContain("**");
  expect(pageErrors).toEqual([]);
});
