// A handbook "Open in the Modeler" action saves a draft in one named samples
// application, not as Ungrouped. Both recipes and catalogue examples use the same
// path. These tests drive the real handbook page against a mocked API.
import { test, expect } from "@playwright/test";

function mockSamples(page, calls, { applications = [], drafts = [] } = {}) {
  page.route("**/api/v1/**", async (route) => {
    const req = route.request();
    const url = new URL(req.url());
    const path = url.pathname + url.search;
    calls.push({ method: req.method(), path, body: req.postData() });
    if (url.pathname === "/api/v1/layout")
      return route.fulfill({ status: 500, json: { error: "layout unavailable" } });
    if (path === "/api/v1/applications" && req.method() === "GET")
      return route.fulfill({ json: applications });
    if (path === "/api/v1/applications" && req.method() === "POST") {
      applications.push({ id: "samples-1", name: "atlas-samples", key: "atlas-samples" });
      return route.fulfill({ json: applications[applications.length - 1] });
    }
    if (path === "/api/v1/drafts" && req.method() === "GET")
      return route.fulfill({ json: drafts });
    if (path === "/api/v1/drafts?projectId=samples-1" && req.method() === "POST")
      return route.fulfill({ json: { processId: req.postData().match(/<process\b[^>]*\bid="([^"]+)"/)?.[1] || "draft" } });
    return route.fulfill({ status: 500, json: { error: "unexpected API request" } });
  });
}

const opened = (calls) => calls.filter((c) => c.method === "POST" && c.path.startsWith("/api/v1/drafts"));

test("opening a recipe creates atlas-samples and files the draft there", async ({ page }) => {
  const calls = [];
  mockSamples(page, calls);
  await page.goto("/handbuch.html#rezepte");
  await page.locator("#rz-script-list .rz-open").click();
  await expect.poll(() => page.url()).toContain("#/modeler/draft/recipe-script-list");

  expect(calls.filter((c) => c.path === "/api/v1/applications").map((c) => c.method))
    .toEqual(["GET", "POST"]);
  expect(opened(calls)).toHaveLength(1);
  expect(opened(calls)[0].path).toBe("/api/v1/drafts?projectId=samples-1");
  expect(opened(calls)[0].body).toContain('id="recipe-script-list"');
  expect(calls.some((c) => /\/deployments|\/publish/.test(c.path))).toBe(false);
});

test("opening an existing ungrouped recipe files it under the existing samples application", async ({ page }) => {
  const calls = [];
  mockSamples(page, calls, {
    applications: [{ id: "samples-1", name: "atlas-samples", key: "atlas-samples" }],
    drafts: [{ processId: "recipe-script-list", projectId: "" }],
  });
  await page.goto("/handbuch.html#rezepte");
  await page.locator("#rz-script-list .rz-open").click();
  await expect.poll(() => page.url()).toContain("#/modeler/draft/recipe-script-list");

  expect(calls.filter((c) => c.path === "/api/v1/applications").map((c) => c.method))
    .toEqual(["GET"]);
  expect(opened(calls).map((c) => c.path)).toEqual(["/api/v1/drafts?projectId=samples-1"]);
});

test("catalogue examples use the same samples application without installing or publishing", async ({ page }) => {
  const calls = [];
  mockSamples(page, calls, {
    applications: [{ id: "samples-1", name: "atlas-samples", key: "atlas-samples" }],
  });
  await page.goto("/handbuch.html#beispiele");
  await page.locator("#bsp-pruefung .bsp-open").waitFor({ timeout: 20000 });
  await page.locator("#bsp-pruefung .bsp-open").click();
  await expect.poll(() => page.url()).toContain("#/modeler/draft/");

  expect(opened(calls)).toHaveLength(1);
  expect(opened(calls)[0].path).toBe("/api/v1/drafts?projectId=samples-1");
  expect(calls.filter((c) => c.path === "/api/v1/applications").map((c) => c.method))
    .toEqual(["GET"]);
  expect(calls.some((c) => /\/publish|\/deployments/.test(c.path))).toBe(false);
});

test("an example already filed in another application is not silently moved", async ({ page }) => {
  const calls = [];
  mockSamples(page, calls, {
    applications: [{ id: "samples-1", name: "atlas-samples", key: "atlas-samples" }],
    drafts: [{ processId: "recipe-script-list", projectId: "customer-workflows" }],
  });
  await page.goto("/handbuch.html#rezepte");
  await page.locator("#rz-script-list .rz-open").click();
  await expect(page.locator("#rz-script-list .rz-status")).toHaveClass(/err/);
  await expect(page.locator("#rz-script-list .rz-status")).toContainText("Application");
  expect(opened(calls)).toHaveLength(0);
});
