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

function installMock(page) {
  page.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path === "/api/v1/applications") {
      return route.fulfill({ json: [{ id: "app-1", name: "Enterprise Architecture", myRole: "owner" }] });
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
  await expect(page.locator(".panorama-status")).toContainText("Read only");
  expect(writes).toEqual([]);
  expect(pageErrors).toEqual([]);
});
