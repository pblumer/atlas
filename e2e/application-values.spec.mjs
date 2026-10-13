// End-to-end coverage for an application's values on this installation (api/web/app.js,
// ADR-0453): the Values tab on the application page
// lists what the deployed processes' constants bound to the application require, with
// what this installation answers for each, and edits the values held here.
//
// DEV, INT and PROD each hold their own; the page is where an operator sees that a value
// a process needs is missing here before a start is refused for it.
//
// Drives the REAL app (index.html → app.js) against a mocked /api/v1.
import { test, expect } from "@playwright/test";

const APP = { id: "app-1", name: "UCC-Sammelanschlüsse" };

// valuesAfter is what the server answers once the values are set.
const VIEW = (values) => ({
  applicationId: APP.id,
  values,
  required: [
    { name: "Language", type: "string", processes: ["ucc-create"], fallback: true,
      state: values.some((v) => v.name === "Language") ? "set" : "fallback" },
    { name: "TenantId", type: "string", processes: ["ucc-create", "ucc-delete"], fallback: false,
      state: values.some((v) => v.name === "TenantId") ? "set" : "missing" },
  ],
});

function installMock(page) {
  const puts = [];
  let held = [{ name: "DomainSuffix", type: "string", value: "contoso.ch" }];
  page.route("**/api/v1/**", async (route) => {
    const req = route.request();
    const url = new URL(req.url());
    const path = url.pathname;
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path === "/api/v1/applications" || path === "/api/v1/projects") return route.fulfill({ json: [APP] });
    if (path === `/api/v1/applications/${APP.id}/values`) {
      if (req.method() === "PUT") {
        const body = JSON.parse(req.postData() || "{}");
        puts.push(body);
        held = body.values;
      }
      return route.fulfill({ json: VIEW(held) });
    }
    if (path === `/api/v1/applications/${APP.id}/deployments`) {
      return route.fulfill({ json: { id: APP.id, name: APP.name, processes: 0, definitions: [] } });
    }
    return route.fulfill({ json: [] });
  });
  return puts;
}

async function openValues(page) {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto(`/index.html#/modeler/p/${APP.id}`);
  await page.locator('#pd-tabs button[data-pane="values"]').click({ timeout: 15000 });
  await expect(page.locator("#av-table")).toBeVisible();
}

test("the Values tab says what the processes require and what this installation holds", async ({ page }) => {
  installMock(page);
  await openValues(page);

  const required = page.locator("#av-required tbody tr");
  await expect(required).toHaveCount(2);
  await expect(page.locator('#av-required tr[data-req="TenantId"]')).toContainText("missing");
  await expect(page.locator('#av-required tr[data-req="TenantId"]')).toContainText("ucc-create, ucc-delete");
  await expect(page.locator('#av-required tr[data-req="Language"]')).toContainText("fallback");

  // The value held here, then a row for each required name it does not hold yet — so
  // setting one is typing a value, not retyping its name.
  const names = await page.locator("#av-rows .av-name").evaluateAll((els) => els.map((e) => e.value));
  expect(names).toEqual(["DomainSuffix", "Language", "TenantId"]);
  await expect(page.locator("#av-rows .av-value").first()).toHaveValue("contoso.ch");
  expect(page.__errors).toEqual([]);
});

test("Save sends the values that have one, and the page reads back the new state", async ({ page }) => {
  const puts = installMock(page);
  await openValues(page);

  await page.locator("#av-rows tr").nth(2).locator(".av-value").fill("tenant-int");
  await page.locator("#av-add").click();
  const added = page.locator("#av-rows tr").last();
  await added.locator(".av-name").fill("MaxAgents");
  await added.locator(".av-type").selectOption("number");
  await added.locator(".av-value").fill("20");
  await page.locator("#av-save").click();

  await expect.poll(() => puts.length).toBe(1);
  // Language has no value typed, so it is left out rather than saved as an empty string.
  expect(puts[0].values).toEqual([
    { name: "DomainSuffix", type: "string", value: "contoso.ch" },
    { name: "TenantId", type: "string", value: "tenant-int" },
    { name: "MaxAgents", type: "number", value: "20" },
  ]);
  await expect(page.locator('#av-required tr[data-req="TenantId"]')).toContainText("set");
  expect(page.__errors).toEqual([]);
});
