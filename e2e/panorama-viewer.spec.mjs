import { test, expect } from "@playwright/test";

const modelId = "11111111111111111111111111111111";

const xml = `<?xml version="1.0" encoding="UTF-8"?>
<model xmlns="http://www.opengroup.org/xsd/archimate/3.0/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" identifier="model-landscape">
  <name xml:lang="en">Order landscape</name>
  <elements>
    <element identifier="cap-orders" xsi:type="Capability"><name xml:lang="en">Fulfil orders</name></element>
    <element identifier="app-orders" xsi:type="ApplicationComponent"><name xml:lang="en">Order Service</name></element>
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

function installMock(page, { role = "owner", onLayout } = {}) {
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
