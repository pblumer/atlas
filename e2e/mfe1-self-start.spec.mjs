// MFE1 / ADR-0516: a task user starts a process an administrator granted them, from
// the Start view on a phone, a tablet or a desktop. The server is mocked here; what
// it decides (who may start what, which values the form accepts) is held by the Go
// tests in api/. This spec holds what the person sees: that Start is offered only
// when something is granted, that they never meet a key, a version or a grant, that
// one tap starts one process, and that every way a start can end is said in words —
// the unknown outcome of a broken connection included.
import { test, expect } from "@playwright/test";

const FORM = {
  id: "inspection-start",
  schema: {
    type: "default",
    components: [
      { type: "textfield", key: "site", label: "Site", validate: { required: true } },
      { type: "textfield", key: "asset", label: "Asset" },
    ],
  },
};

const WITH_FORM = {
  key: 41, processId: "safety-inspection", name: "Safety inspection", version: 3,
  applicationId: "app-field", applicationName: "Field Operations",
  startForm: { available: true, formId: "inspection-start" },
};
const NO_FORM = {
  key: 42, processId: "site-visit", name: "Site visit", version: 1,
  applicationId: "app-field", applicationName: "Field Operations",
  startForm: { available: false },
};

// mock answers the API as a server would for one signed-in person. startable is the
// list GET /me/startable-processes answers (a function, so a test can change it while
// the view is open); start answers the self-service POST and records each call.
function mock(page, { roles = ["user"], startable = () => [WITH_FORM, NO_FORM], start } = {}) {
  const calls = { starts: [], operatorStarts: 0 };
  page.route("**/api/v1/**", async (route) => {
    const req = route.request();
    const path = new URL(req.url()).pathname;
    if (path.endsWith("/auth/me")) return route.fulfill({ json: {
      authEnabled: true, user: { id: "usr_inspector", username: "inspector", roles },
    } });
    if (path.endsWith("/api/v1/me/startable-processes")) {
      const items = startable();
      return route.fulfill({ json: { items, total: items.length, totalExact: true, truncated: false } });
    }
    if (path.endsWith("/api/v1/forms/inspection-start")) return route.fulfill({ json: FORM });
    const m = path.match(/\/api\/v1\/me\/processes\/(\d+)\/instances$/);
    if (m && req.method() === "POST") {
      calls.starts.push({ key: Number(m[1]), body: req.postDataJSON() });
      if (start) return start(route, calls.starts.length);
      return route.fulfill({ json: { definitionKey: Number(m[1]), instanceKey: 281474976710657, stats: {} } });
    }
    if (/\/api\/v1\/processes\/\d+\/instances$/.test(path)) { calls.operatorStarts++; return route.fulfill({ json: {} }); }
    if (path.endsWith("/api/v1/tasks")) return route.fulfill({ json: { items: [], total: 0, totalExact: true, truncated: false } });
    if (path.endsWith("/api/v1/processes")) return route.fulfill({ json: [{ ...WITH_FORM, projectId: "app-field", startFormId: "inspection-start", executable: true }] });
    if (path.endsWith("/api/v1/me/start-favourites")) return route.fulfill({ json: { processIds: [] } });
    return route.fulfill({ json: [] });
  });
  return calls;
}

const noHorizontalOverflow = (page) => page.evaluate(() =>
  document.documentElement.scrollWidth - document.documentElement.clientWidth);

for (const width of [320, 390, 768]) {
  test(`MFE1 self-start ${width}px: a granted process starts from its form and says where it stands`, async ({ page }) => {
    const calls = mock(page);
    await page.setViewportSize({ width, height: 800 });
    await page.goto("/index.html?lang=en#/tasks/start");
    const item = page.locator('#selfstart-list .tasks-item[data-key="41"]');
    await expect(item).toBeVisible({ timeout: 15000 });
    // A person picks a process by its name and application — no key, version or grant.
    await expect(page.locator("#selfstart-list")).toContainText("Field Operations");
    await expect(page.locator("#selfstart-list")).not.toContainText("41");
    await expect(page.locator("#selfstart-list")).not.toContainText("v3");
    expect(await noHorizontalOverflow(page)).toBeLessThanOrEqual(1);

    await item.click();
    const form = page.locator("#selfstart-form");
    await expect(form.getByLabel("Site")).toBeVisible();
    await form.getByLabel("Site").fill("Workshop 12");
    const go = page.locator("#selfstart-go");
    const box = await go.boundingBox();
    expect(box.height).toBeGreaterThanOrEqual(44);
    await go.click();
    await expect(page.locator("#selfstart-status")).toContainText("“Safety inspection” has started. Reference: 281474976710657");
    expect(calls.starts).toEqual([{ key: 41, body: { variables: { site: "Workshop 12", asset: "" } } }]);
    expect(calls.operatorStarts).toBe(0);
    await expect(page.locator("#selfstart-status a[href='#/tasks']")).toBeVisible();
    expect(await noHorizontalOverflow(page)).toBeLessThanOrEqual(1);
  });
}

test("MFE1 self-start: Start is offered in the navigation only when something is granted", async ({ page }) => {
  mock(page);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/index.html?lang=en#/tasks");
  await expect(page.locator('.mfe-entry a[href="#/tasks/start"]')).toBeVisible({ timeout: 15000 });
  await expect(page.locator('.mfe-entry a')).toHaveText(["My tasks", "Start process"]);
});

test("MFE1 self-start: nothing granted shows no Start and an empty view that says why", async ({ page }) => {
  mock(page, { startable: () => [] });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/index.html?lang=en#/tasks");
  await expect(page.locator("#task-list, .tasks-list").first()).toBeVisible({ timeout: 15000 });
  await expect(page.locator('a[href="#/tasks/start"]')).toHaveCount(0);
  await page.goto("/index.html?lang=en#/tasks/start");
  await expect(page.locator("#selfstart-empty")).toContainText("No process has been granted to you");
});

test("MFE1 self-start: a required field is asked for before anything is sent", async ({ page }) => {
  const calls = mock(page);
  await page.goto("/index.html?lang=en#/tasks/start");
  await page.locator('#selfstart-list .tasks-item[data-key="41"]').click();
  await expect(page.locator("#selfstart-form").getByLabel("Site")).toBeVisible({ timeout: 15000 });
  await page.locator("#selfstart-go").click();
  await expect(page.locator("#selfstart-status")).toContainText("Site");
  expect(calls.starts).toHaveLength(0);
});

test("MFE1 self-start: the server's refusal of a value keeps the form and its data", async ({ page }) => {
  mock(page, { start: (route) => route.fulfill({ status: 400, json: { error: "the start form refuses these values: site must be at most 20 characters" } }) });
  await page.goto("/index.html?lang=en#/tasks/start");
  await page.locator('#selfstart-list .tasks-item[data-key="41"]').click();
  const site = page.locator("#selfstart-form").getByLabel("Site");
  await site.fill("A very long site name indeed");
  await page.locator("#selfstart-go").click();
  await expect(page.locator("#selfstart-status")).toContainText("site must be at most 20 characters");
  await expect(site).toHaveValue("A very long site name indeed");
  await expect(page.locator("#selfstart-go")).toBeEnabled();
});

test("MFE1 self-start: a grant revoked while the form is open is said, and nothing is retried", async ({ page }) => {
  let granted = true;
  const calls = mock(page, {
    startable: () => (granted ? [WITH_FORM, NO_FORM] : [NO_FORM]),
    start: (route) => route.fulfill({ status: 403, json: { error: "you may not start this process" } }),
  });
  await page.goto("/index.html?lang=en#/tasks/start");
  await page.locator('#selfstart-list .tasks-item[data-key="41"]').click();
  await page.locator("#selfstart-form").getByLabel("Site").fill("Workshop 12");
  granted = false;
  await page.locator("#selfstart-go").click();
  await expect(page.locator("#selfstart-status")).toContainText("You may no longer start this process. Nothing was started.");
  await expect(page.locator("#selfstart-go")).toBeDisabled();
  expect(calls.starts).toHaveLength(1);
  await page.locator('#selfstart-status [data-act="reload"]').click();
  await expect(page.locator('#selfstart-list .tasks-item[data-key="41"]')).toHaveCount(0);
  await expect(page.locator('#selfstart-list .tasks-item[data-key="42"]')).toBeVisible();
});

test("MFE1 self-start: a version replaced in the meantime asks for a reload", async ({ page }) => {
  mock(page, { start: (route) => route.fulfill({ status: 409, json: { error: "replaced" } }) });
  await page.goto("/index.html?lang=en#/tasks/start");
  await page.locator('#selfstart-list .tasks-item[data-key="42"]').click();
  await expect(page.locator("#selfstart-form")).toContainText("This process starts without any input.");
  await page.locator("#selfstart-go").click();
  await expect(page.locator("#selfstart-status")).toContainText("Nothing was started; reload the list.");
});

test("MFE1 self-start: an expired session is said, and nothing claims a start", async ({ page }) => {
  mock(page, { start: (route) => route.fulfill({ status: 401, json: { error: "authentication required" } }) });
  await page.goto("/index.html?lang=en#/tasks/start");
  await page.locator('#selfstart-list .tasks-item[data-key="42"]').click();
  await page.locator("#selfstart-go").click();
  await expect(page.locator("#selfstart-status")).toContainText("Your session has ended. Sign in again; nothing was started.");
  await expect(page.locator('#selfstart-status [data-act="login"]')).toBeVisible();
});

test("MFE1 self-start: a double tap sends one start", async ({ page }) => {
  let release;
  const held = new Promise((r) => { release = r; });
  const calls = mock(page, { start: async (route) => { await held; return route.fulfill({ json: { definitionKey: 42, instanceKey: 7 } }); } });
  await page.goto("/index.html?lang=en#/tasks/start");
  await page.locator('#selfstart-list .tasks-item[data-key="42"]').click();
  const go = page.locator("#selfstart-go");
  await go.click();
  await go.click({ force: true }).catch(() => {});
  await go.dispatchEvent("click");
  await expect(page.locator("#selfstart-status")).toContainText("Starting the process…");
  await expect(go).toBeDisabled();
  release();
  await expect(page.locator("#selfstart-status")).toContainText("has started. Reference: 7");
  expect(calls.starts).toHaveLength(1);
});

test("MFE1 self-start: a broken connection leaves the outcome unknown and says so", async ({ page }) => {
  const calls = mock(page, { start: (route) => route.abort("connectionreset") });
  await page.goto("/index.html?lang=en#/tasks/start");
  await page.locator('#selfstart-list .tasks-item[data-key="42"]').click();
  await page.locator("#selfstart-go").click();
  await expect(page.locator("#selfstart-status")).toContainText("It is unclear whether the process was started");
  await expect(page.locator("#selfstart-go")).toBeDisabled();
  expect(calls.starts).toHaveLength(1);
  // Starting again is the person's decision, made after they were told to look first.
  await page.locator('#selfstart-status [data-act="retry"]').click();
  await expect(page.locator("#selfstart-go")).toBeEnabled();
});

test("MFE1 self-start: an operator keeps the operator Start view and its route", async ({ page }) => {
  const calls = mock(page, { roles: ["operator", "user"] });
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.goto("/index.html?lang=en#/tasks/start");
  await expect(page.locator("#start-list .tasks-item")).toContainText("Safety inspection", { timeout: 15000 });
  await expect(page.locator("#selfstart-list")).toHaveCount(0);
  await expect(page.locator('#topnav a[href="#/tasks/start"]')).toBeVisible();
  expect(calls.starts).toHaveLength(0);
});

test("MFE1 self-start desktop: the top bar offers Start to a task user with a grant", async ({ page }) => {
  mock(page);
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.goto("/index.html?lang=en#/tasks/start");
  await expect(page.locator('#selfstart-list .tasks-item[data-key="41"]')).toBeVisible({ timeout: 15000 });
  await expect(page.locator('#topnav a[href="#/tasks/start"]')).toBeVisible();
  // The keyboard reaches and opens a process.
  await page.locator('#selfstart-list .tasks-item[data-key="42"]').focus();
  await page.keyboard.press("Enter");
  await expect(page.locator("#selfstart-detail h1")).toHaveText("Site visit");
});
