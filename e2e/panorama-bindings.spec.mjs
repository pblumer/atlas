import { test, expect } from "@playwright/test";

// Atlas bindings in the Panorama model viewer (ADR-0189 §4). The document stores an
// opaque id; every name on screen comes from the server, so the panel can never show
// a stale copy of one.

const modelId = "22222222222222222222222222222222";

const xml = `<?xml version="1.0" encoding="UTF-8"?>
<model xmlns="http://www.opengroup.org/xsd/archimate/3.0/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" identifier="model-bound">
  <name xml:lang="en">Bound landscape</name>
  <elements>
    <element identifier="app-orders" xsi:type="ApplicationComponent"><name xml:lang="en">Order Service</name></element>
    <element identifier="bp-fulfil" xsi:type="BusinessProcess"><name xml:lang="en">Fulfil order</name></element>
  </elements>
  <views><diagrams>
    <view identifier="view-app" xsi:type="Diagram"><name xml:lang="en">Application cooperation</name>
      <node identifier="n-app" elementRef="app-orders" xsi:type="Element" x="80" y="100" w="190" h="80"/>
      <node identifier="n-bp" elementRef="bp-fulfil" xsi:type="Element" x="380" y="105" w="170" h="70"/>
    </view>
  </diagrams></views>
</model>`;

// resolution is what the server says the document's bindings mean. One resolved, one
// forbidden, one missing — the three answers the panel has to tell apart.
const resolution = {
  contractVersion: 1,
  unresolved: 2,
  problems: [],
  bindings: [
    {
      elementId: "app-orders", elementType: "ApplicationComponent", key: "atlas.applicationId",
      values: [
        { value: "proj-abc", status: "resolved", name: "Billing" },
        { value: "proj-hidden", status: "forbidden" },
        { value: "proj-gone", status: "missing" },
      ],
    },
  ],
};

function installMock(page, { role = "owner", onPut } = {}) {
  page.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path === "/api/v1/applications") {
      return route.fulfill({ json: [{ id: "app-1", name: "Enterprise Architecture", myRole: role }] });
    }
    if (path === `/api/v1/panorama/models/${modelId}`) {
      return route.fulfill({ json: { id: modelId, applicationId: "app-1", name: "Bound landscape", notation: "archimate-3.2", revision: 3 } });
    }
    if (path === `/api/v1/panorama/models/${modelId}/xml`) {
      return route.fulfill({ contentType: "application/xml", body: xml });
    }
    if (path === `/api/v1/panorama/models/${modelId}/bindings/candidates`) {
      const key = new URL(request.url()).searchParams.get("key");
      if (key === "atlas.runtimeId") return route.fulfill({ json: { key, supported: false, candidates: [] } });
      return route.fulfill({ json: { key, supported: true, candidates: [
        { id: "proj-abc", name: "Billing" }, { id: "proj-new", name: "Collections" },
      ] } });
    }
    if (path === `/api/v1/panorama/models/${modelId}/bindings`) {
      if (request.method() === "PUT") {
        onPut?.(JSON.parse(request.postData() || "{}"));
        return route.fulfill({ json: { id: modelId, revision: 4 } });
      }
      return route.fulfill({ json: resolution });
    }
    return route.fulfill({ json: [] });
  });
}

test("shows resolved names and keeps unresolved bindings visible", async ({ page }) => {
  installMock(page);
  const pageErrors = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));

  await page.goto(`/index.html#/panorama/models/${modelId}`);
  await page.locator('.djs-element[data-element-id="n-app"]').click();

  const panel = page.locator(".panorama-properties");
  await expect(panel).toContainText("Atlas bindings");
  // The resolved one shows the server's name beside the id the document stores.
  await expect(panel).toContainText("Billing");
  await expect(panel).toContainText("proj-abc");
  // A broken binding is shown, not hidden: hiding it would make the model look
  // correct. Each unresolved kind says which it is, because they are fixed in
  // different places.
  await expect(panel).toContainText("outside your access");
  await expect(panel).toContainText("no longer on this server");
  // A forbidden resource's name is exactly what the scope withholds.
  await expect(panel).not.toContainText("HR Confidential");

  expect(pageErrors).toEqual([]);
});

test("only keys valid for the element type are offered", async ({ page }) => {
  installMock(page);
  await page.goto(`/index.html#/panorama/models/${modelId}`);

  await page.locator('.djs-element[data-element-id="n-bp"]').click();
  const panel = page.locator(".panorama-properties");
  // A business process binds a BPMN process id and nothing else; offering an
  // application id here would invite an error the server would then refuse.
  await expect(panel).toContainText("BPMN process");
  await expect(panel).not.toContainText("Process application");
  await expect(panel).toContainText("Not bound");
});

test("binds a resource through the picker and sends the expected revision", async ({ page }) => {
  const puts = [];
  installMock(page, { onPut: (body) => puts.push(body) });
  await page.goto(`/index.html#/panorama/models/${modelId}`);

  await page.locator('.djs-element[data-element-id="n-bp"]').click();
  await page.getByRole("button", { name: "Bind" }).click();

  // The picker lists only what the server offered — an opaque id is never typed.
  await expect(page.locator(".panorama-pick")).toContainText("Collections");
  await page.locator('.panorama-pick input[value="proj-new"]').check();
  await page.getByRole("button", { name: "Save binding" }).click();

  await expect.poll(() => puts.length).toBe(1);
  expect(puts[0]).toMatchObject({
    expectedRevision: 3, elementId: "bp-fulfil", key: "atlas.processId", values: ["proj-new"],
  });
});

test("a viewer is not offered the edit control", async ({ page }) => {
  installMock(page, { role: "viewer" });
  await page.goto(`/index.html#/panorama/models/${modelId}`);

  await page.locator('.djs-element[data-element-id="n-app"]').click();
  await expect(page.locator(".panorama-properties")).toContainText("Billing");
  await expect(page.getByRole("button", { name: "Change" })).toHaveCount(0);
});
