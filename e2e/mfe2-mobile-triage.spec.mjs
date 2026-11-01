// MFE2 / ADR-0514: real mobile incident triage UI against mocked HTTP,
// separate from both MFE3 human alarm acknowledgement and desktop remediation.
import { test, expect } from "@playwright/test";

const GROUPS = [
  { processDefKey: 6, processId: "payment", version: 1, elementId: "Task_pay",
    elementIndex: 2, type: "job", count: 18, oldestRaisedAt: 1, newestRaisedAt: 2,
    message: "payment gateway unavailable", messageVaries: true },
  { processDefKey: 7, processId: "shipping", version: 2, elementId: "Timer_wait",
    elementIndex: 3, type: "timer", count: 2, oldestRaisedAt: 3, newestRaisedAt: 4,
    message: "schedule unavailable" },
];

function mockAPI(page, { roles = ["operator", "user"], summaryStatus = 200,
  detailStatus = 200, groups = GROUPS, truncated = false, ungrouped = 0 } = {}) {
  const calls = [];
  page.route("**/api/v1/**", async (route) => {
    const req = route.request(), u = new URL(req.url()), path = u.pathname;
    if (path.endsWith("/auth/me")) return route.fulfill({
      json: { authEnabled: true, user: { id: "mfe2", username: "operator", roles } },
    });
    if (path === "/api/v1/stats") return route.fulfill({ json: { unresolvedIncidents: 20 } });
    if (path === "/api/v1/incidents/summary") {
      calls.push({ path, method: req.method() });
      if (summaryStatus !== 200) return route.fulfill({ status: summaryStatus,
        json: { error: "internal SECRET-incident-label" } });
      return route.fulfill({ json: { total: 20, groups, groupsTruncated: truncated, ungrouped } });
    }
    if (path === "/api/v1/incidents") {
      calls.push({ path, method: req.method(), query: u.searchParams.toString() });
      if (detailStatus !== 200) return route.fulfill({ status: detailStatus,
        json: { error: "internal SECRET-incident-label" } });
      return route.fulfill({ json: {
        items: [{ processDefKey: 6, elementId: "Task_pay", type: "job",
          processInstanceKey: "900001", raisedAt: 2, message: "<img src=x onerror=alert(1)>" }],
        total: 18, totalExact: false, truncated: true,
      } });
    }
    if (req.method() !== "GET") calls.push({ path, method: req.method() });
    return route.fulfill({ json: [] });
  });
  return calls;
}

async function openMobile(page, width = 390) {
  await page.setViewportSize({ width, height: 844 });
  await page.goto("/index.html?lang=en#/operations/incidents");
  await expect(page.locator("#mfe2-triage")).toBeVisible({ timeout: 15000 });
}

test("MFE2: operator sees bounded grouped causes without any incident row download", async ({ page }) => {
  const calls = mockAPI(page);
  await openMobile(page);
  await expect(page.locator("#mfe2-causes button")).toHaveCount(2);
  await expect(page.locator("#mfe2-causes button").first()).toContainText("payment");
  await expect(page.locator("#mfe2-causes button").first()).toContainText("18");
  await expect(page.locator("#mfe2-details")).toContainText("Select a cause");
  expect(calls.filter((c) => c.path === "/api/v1/incidents")).toHaveLength(0);
  expect(calls.filter((c) => c.method !== "GET")).toHaveLength(0);
});

test("MFE2: selecting one cause reads only a bounded scoped sample and escapes server text", async ({ page }) => {
  const calls = mockAPI(page);
  await openMobile(page);
  await page.locator("#mfe2-causes button").first().click();
  await expect(page.locator("#mfe2-details")).toContainText("payment gateway unavailable");
  await expect(page.locator("#mfe2-samples")).toContainText("<img src=x onerror=alert(1)>");
  await expect(page.locator("#mfe2-samples img")).toHaveCount(0);
  const list = calls.filter((c) => c.path === "/api/v1/incidents");
  expect(list).toHaveLength(1);
  const args = new URLSearchParams(list[0].query);
  expect(args.get("limit")).toBe("10");
  expect(args.get("process")).toBe("6");
  expect(args.get("element")).toBe("Task_pay");
  expect(args.get("type")).toBe("job");
  expect(calls.filter((c) => c.method !== "GET")).toHaveLength(0);
});

test("MFE2: truncated summary shows an honest warning, empty summary stays readable", async ({ page }) => {
  mockAPI(page, { truncated: true, ungrouped: 9 });
  await openMobile(page, 320);
  await expect(page.locator("#mfe2-truncation")).toContainText("9");
  await expect(page.locator("#mfe2-causes button")).toHaveCount(2);
  expect(await page.evaluate(() =>
    document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBeLessThanOrEqual(1);
});

test("MFE2: a denied summary never exposes incident data or privileged commands", async ({ page }) => {
  const calls = mockAPI(page, { summaryStatus: 403 });
  await openMobile(page);
  await expect(page.locator("#mfe2-status")).toContainText("access");
  await expect(page.locator("#mfe2-causes button")).toHaveCount(0);
  await expect(page.locator("#mfe2-triage")).not.toContainText("SECRET-incident-label");
  expect(calls.filter((c) => c.path === "/api/v1/incidents")).toHaveLength(0);
});

test("MFE2: revoked detail access clears previously visible incident context", async ({ page }) => {
  mockAPI(page, { detailStatus: 401 });
  await openMobile(page);
  await expect(page.locator("#mfe2-causes button")).toHaveCount(2);
  await page.locator("#mfe2-causes button").first().click();
  await expect(page.locator("#mfe2-status")).toContainText("access");
  await expect(page.locator("#mfe2-causes button")).toHaveCount(0);
  await expect(page.locator("#mfe2-details")).not.toContainText("payment gateway unavailable");
});

test("MFE2: task-only user cannot reach incidents with a forged URL", async ({ page }) => {
  const calls = mockAPI(page, { roles: ["user"] });
  await openMobile(page);
  await expect(page.locator("#mfe2-status")).toContainText("access");
  await expect(page.locator("#mfe2-causes button")).toHaveCount(0);
  expect(calls.filter((c) => c.path.startsWith("/api/v1/incidents"))).toHaveLength(0);
});

test("MFE2: phone and portrait tablet remain accessible; desktop Operations remains unchanged", async ({ page }) => {
  mockAPI(page);
  for (const width of [320, 390, 768]) {
    await openMobile(page, width);
    const first = page.locator("#mfe2-causes button").first();
    await expect(first).toBeVisible();
    await first.focus();
    await expect(first).toBeFocused();
    await page.keyboard.press("Enter");
    await expect(page.locator("#mfe2-details")).toContainText("Task_pay");
    expect(await page.evaluate(() =>
      document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBeLessThanOrEqual(1);
  }
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.reload();
  await expect(page.locator("#causes")).toBeVisible({ timeout: 15000 });
  await expect(page.locator("#mfe2-triage")).toHaveCount(0);
});
