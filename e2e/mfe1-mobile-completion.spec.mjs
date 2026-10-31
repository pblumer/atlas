// MFE1 / ADR-0514: completing a field-work task should leave a clear mobile
// receipt only after the existing server completion command has succeeded.
// The real Tasks view and vendored form-js run against mocked HTTP responses.
import { test, expect } from "@playwright/test";

const TASK = {
  key: 501, processInstanceKey: 9001, elementInstanceKey: 9501, processDefKey: 1,
  processId: "mobile-safety-inspection", elementId: "safety-check",
  name: "Safety inspection", formId: "mfe-safety-check", priority: 50,
};

const FORM = { schema: {
  type: "default", id: "mfe-safety-check", components: [
    { type: "textfield", key: "location", label: "Inspection location", validate: { required: true } },
    { type: "select", key: "result", label: "Safety result", values: [
      { label: "Safe", value: "safe" }, { label: "Hazard found", value: "hazard" },
    ], validate: { required: true } },
    { type: "textarea", key: "notes", label: "Observations" },
  ],
} };

function setup(page, handler, task = TASK) {
  let completed = false;
  const requests = [];
  page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path.endsWith("/api/v1/tasks")) return route.fulfill({ json: {
      items: completed ? [] : [task], total: completed ? 0 : 1, totalExact: true,
      truncated: false, nextCursor: null,
    } });
    if (path.endsWith("/api/v1/forms/mfe-safety-check")) return route.fulfill({ json: FORM });
    if (path === "/api/v1/tasks/501/complete") {
      requests.push(route.request().postDataJSON());
      return handler(route, () => { completed = true; });
    }
    if (path.endsWith("/variables")) return route.fulfill({ json: {} });
    return route.fulfill({ json: [] });
  });
  return requests;
}

async function openInspection(page, width = 390) {
  await page.setViewportSize({ width, height: 844 });
  await page.goto("/index.html?lang=en#/tasks");
  await expect(page.locator('.tasks-item[data-key="501"]')).toBeVisible({ timeout: 15000 });
  await page.locator('.tasks-item[data-key="501"]').click();
  await expect(page.locator("#task-form .fjs-form")).toBeVisible({ timeout: 15000 });
}

async function completeValidInspection(page) {
  const form = page.locator("#task-form");
  await form.getByLabel("Inspection location").fill("Workshop 12");
  const select = form.locator(".fjs-form-field-select");
  await select.locator(".fjs-select-display").click();
  await select.locator(".fjs-dropdownlist-item", { hasText: "Safe" }).click();
  await page.locator("#task-complete").click();
}

test("MFE1 phone inspection: receipt appears only after confirmed completion", async ({ page }) => {
  const requests = setup(page, (route, markComplete) => {
    markComplete();
    return route.fulfill({ json: { key: TASK.key } });
  });
  await openInspection(page, 320);
  await expect(page.locator(".mfe-completion")).toBeHidden();
  await completeValidInspection(page);

  await expect(page.locator(".mfe-completion")).toBeVisible();
  await expect(page.locator(".mfe-completion")).toContainText("Safety inspection");
  await expect(page.locator(".mfe-completion")).toContainText("completed");
  await expect(page.locator("#task-detail")).toBeHidden();
  await expect(page.locator(".tasks-list-pane")).toBeVisible();
  expect(requests).toHaveLength(1);
  expect(requests[0].variables).toMatchObject({ location: "Workshop 12", result: "safe" });
  expect(await page.evaluate(() =>
    document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBeLessThanOrEqual(1);

  await page.locator(".mfe-completion button").click();
  await expect(page.locator(".mfe-completion")).toBeHidden();
});

test("MFE1 phone inspection: invalid form never sends completion or shows receipt", async ({ page }) => {
  const requests = setup(page, (route, markComplete) => {
    markComplete();
    return route.fulfill({ json: { key: TASK.key } });
  });
  await openInspection(page);
  await page.locator("#task-form").getByLabel("Inspection location").fill("Workshop 12");
  await page.locator("#task-complete").click();
  await expect(page.locator("#toast")).toContainText("Safety result");
  expect(requests).toHaveLength(0);
  await expect(page.locator(".mfe-completion")).toBeHidden();
  await expect(page.locator("#task-form").getByLabel("Inspection location")).toHaveValue("Workshop 12");
});

test("MFE1 phone inspection: pending completion cannot show a receipt or send twice", async ({ page }) => {
  let release;
  const gate = new Promise((resolve) => { release = resolve; });
  const requests = setup(page, async (route, markComplete) => {
    await gate;
    markComplete();
    await route.fulfill({ json: { key: TASK.key } });
  });
  await openInspection(page);
  await completeValidInspection(page);
  await expect.poll(() => requests.length).toBe(1);
  await expect(page.locator("#task-complete")).toBeDisabled();
  await expect(page.locator(".mfe-completion")).toBeHidden();
  await page.keyboard.press("Control+Enter"); // keyboard completion cannot duplicate the pending POST
  expect(requests).toHaveLength(1);

  release();
  await expect(page.locator(".mfe-completion")).toBeVisible();
  expect(requests).toHaveLength(1);
});

test("MFE1 phone inspection: denied completion is not shown as successful", async ({ page }) => {
  const requests = setup(page, (route) => route.fulfill({
    status: 403, json: { error: "Forbidden" },
  }));
  await openInspection(page);
  await completeValidInspection(page);
  await expect(page.locator("#toast")).toContainText("Complete failed");
  await expect(page.locator(".mfe-completion")).toBeHidden();
  await expect(page.locator("#task-detail")).toBeVisible();
  await expect(page.locator("#task-form").getByLabel("Inspection location")).toHaveValue("Workshop 12");
  expect(requests).toHaveLength(1);
});

test("MFE1 desktop inspection: completion keeps normal desktop behaviour", async ({ page }) => {
  const requests = setup(page, (route, markComplete) => {
    markComplete();
    return route.fulfill({ json: { key: TASK.key } });
  });
  await openInspection(page, 1280);
  await completeValidInspection(page);
  await expect(page.locator("#toast")).toContainText("Task completed");
  await expect(page.locator(".mfe-completion")).toBeHidden();
  expect(requests).toHaveLength(1);
});


test("MFE1 Android tablet landscape: inspection form stays readable across rotation", async ({ page }) => {
  // Regression for #1473: at tablet widths the three-pane grid previously
  // left the form a few pixels wide and wrapped this title one letter per line.
  const inspection = { ...TASK, name: "Sicherheitskontrolle vor Ort" };
  setup(page, (route) => route.fulfill({ json: { key: TASK.key } }), inspection);
  await openInspection(page, 1024);
  const title = page.locator(".tasks-detail-head h1");
  await expect(title).toHaveText(inspection.name);

  for (const width of [900, 1024, 1180]) {
    await page.setViewportSize({ width, height: 768 });
    await expect(page.locator(".tasks-list-pane")).toBeVisible();
    await expect(page.locator(".tasks-folders")).toBeVisible();
    await expect(page.locator("#task-form .fjs-form")).toBeVisible();
    await expect(page.locator("#tasks-col-resizer")).toBeHidden();
    await expect(page.locator("#task-back")).toBeHidden();

    const geometry = await page.evaluate(() => {
      const box = (selector) => document.querySelector(selector).getBoundingClientRect();
      const grid = box(".tasks");
      const folders = box(".tasks-folders");
      const list = box(".tasks-list-pane");
      const detail = box("#task-detail");
      const form = box("#task-form .fjs-form");
      const heading = box(".tasks-detail-head h1");
      const fontSize = parseFloat(getComputedStyle(document.querySelector(".tasks-detail-head h1")).fontSize);
      return {
        gridWidth: grid.width, folderWidth: folders.width,
        folderBottom: folders.bottom, listTop: list.top,
        listRight: list.right, listWidth: list.width,
        detailLeft: detail.left, detailWidth: detail.width,
        formWidth: form.width, titleWidth: heading.width,
        titleLines: heading.height / fontSize,
        overflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
      };
    });
    expect(geometry.folderWidth).toBeGreaterThanOrEqual(geometry.gridWidth - 4);
    expect(geometry.folderBottom).toBeLessThanOrEqual(geometry.listTop + 2);
    expect(geometry.listRight).toBeLessThanOrEqual(geometry.detailLeft + 2);
    expect(geometry.listWidth).toBeGreaterThanOrEqual(240);
    expect(geometry.detailWidth).toBeGreaterThanOrEqual(400);
    expect(geometry.formWidth).toBeGreaterThanOrEqual(320);
    expect(geometry.titleWidth).toBeGreaterThanOrEqual(150);
    expect(geometry.titleLines).toBeLessThan(4);
    expect(geometry.overflow).toBeLessThanOrEqual(1);
  }

  // Orientation changes must preserve both the selected task and form input.
  const location = page.locator("#task-form").getByLabel("Inspection location");
  await location.fill("Workshop 12");
  await page.setViewportSize({ width: 768, height: 1024 });
  await expect(page.locator(".tasks-list-pane")).toBeHidden();
  await expect(page.locator("#task-form .fjs-form")).toBeVisible();
  await expect(location).toHaveValue("Workshop 12");

  await page.setViewportSize({ width: 1024, height: 768 });
  await expect(page.locator(".tasks-list-pane")).toBeVisible();
  await expect(page.locator("#task-form .fjs-form")).toBeVisible();
  await expect(location).toHaveValue("Workshop 12");
  expect(await page.evaluate(() =>
    document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBeLessThanOrEqual(1);
});
