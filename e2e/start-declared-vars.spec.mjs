// End-to-end coverage for a start form *and* declared start variables on one process
// (api/web/startvars.js, editor.js, app.js; ADR-0028).
//
// A process says what it starts with in two places: the form linked on its start event,
// and the start variables declared on the process (atlas:startForm). Every surface that
// opened the form used to drop the declaration without a word, so a declared value the
// form never asked for was simply missing from the instance — and a script task that
// read it got null. The surfaces now ask, beneath the form, for the declared variables
// the form does not bind, with the declaration's types, defaults and required marks, and
// start the instance with both halves.
//
// Covered on all three surfaces that start through the form: the Modeler's Deploy & run,
// the Live view's Start instance, and the Tasks app's Start.
import { test, expect } from "@playwright/test";

// The editor's toolbar runs the width of the window and Deploy is its last button, so a
// narrower viewport puts it off screen — the modeler is a wide-screen surface.
test.use({ viewport: { width: 1600, height: 900 } });

// What the form does not bind, in declaration order: Name is a form field, address is
// bound by the form's `address.street`, and Hidden is on the form only disabled — which
// form-js never submits, so it is still the declaration's to ask.
const EXTRAS = ["ApplicationId", "TenantId", "Retries", "Options", "Hidden"];

// Both halves, as the instance must start with them: the form's data and the declared
// values, defaults included.
const STARTED_WITH = {
  Name: "Anna",
  address: { street: "Bahnhofstrasse 1" },
  ApplicationId: "app-1",
  TenantId: "tenant-42",
  Retries: 3,
  Options: { dry: true },
  Hidden: "from-declaration",
};

async function openHarness(page, query = "") {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/start-declared-vars-harness.html" + query);
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
}

const writes = (page) => page.evaluate(() => window.__writes);

// fillFormHalf fills the form's two submittable fields inside host.
async function fillFormHalf(host) {
  const inputs = host.locator("input[type='text']");
  await inputs.nth(0).fill("Anna");
  await inputs.nth(1).fill("Bahnhofstrasse 1");
}

test.describe("Deploy & run", () => {
  test.beforeEach(async ({ page }) => {
    await openHarness(page);
    await page.evaluate(() => window.__mountEditor());
    await page.locator("#deploy").click();
    await expect(page.locator("#deploy-panel")).toBeVisible();
  });

  test("asks, beneath the form, for the declared variables the form does not bind", async ({ page }) => {
    await expect(page.locator("#deploy-body")).toContainText("auf dem Prozess deklarierten");
    await page.locator("#deploy-run").click();
    const modal = page.locator(".startform-modal");
    await expect(modal.locator(".startform-host")).toContainText("Strasse");
    const extra = modal.locator(".sf-extra");
    await expect(extra).toBeVisible();
    expect(await extra.locator(".dv-field").evaluateAll((els) => els.map((e) => e.dataset.name))).toEqual(EXTRAS);
    // The declaration's defaults are prefilled, and its required mark is shown.
    await expect(extra.locator('[data-name="TenantId"]')).toHaveValue("tenant-42");
    await expect(extra.locator('[data-name="Retries"]')).toHaveValue("3");
    await expect(extra.locator('[data-name="ApplicationId"][data-required="1"]')).toHaveCount(1);
    expect(page.__errors).toEqual([]);
  });

  test("a required declared variable left empty refuses the send, and deploys nothing", async ({ page }) => {
    await page.locator("#deploy-run").click();
    const modal = page.locator(".startform-modal");
    await expect(modal.locator(".sf-extra")).toBeVisible();
    await fillFormHalf(modal.locator(".startform-host"));
    await page.locator("[data-sf-send]").click();

    await expect(page.locator("#sf-err")).toContainText("Pflichtfeld: ApplicationId");
    await expect(modal).toBeVisible(); // still open, values kept
    expect(await writes(page)).toEqual([]);
    expect(page.__errors).toEqual([]);
  });

  test("Send starts the instance with the form's data and the declared values", async ({ page }) => {
    await page.locator("#deploy-run").click();
    const modal = page.locator(".startform-modal");
    await expect(modal.locator(".sf-extra")).toBeVisible();
    await fillFormHalf(modal.locator(".startform-host"));
    await modal.locator('.sf-extra [data-name="ApplicationId"]').fill("app-1");
    await page.locator("[data-sf-send]").click();

    await expect(modal).toHaveCount(0);
    const w = await writes(page);
    expect(w.map((x) => x.method + " " + x.url.replace(/\?.*$/, ""))).toEqual([
      "POST /api/v1/deployments",
      "POST /api/v1/processes/11/instances",
    ]);
    expect(w[1].body.variables).toEqual(STARTED_WITH);
    expect(page.__errors).toEqual([]);
  });
});

test("without a declaration the modal is the form alone", async ({ page }) => {
  await openHarness(page, "?declared=0");
  await page.evaluate(() => window.__mountEditor());
  await page.locator("#deploy").click();
  await expect(page.locator("#deploy-body")).not.toContainText("auf dem Prozess deklarierten");
  await page.locator("#deploy-run").click();
  const modal = page.locator(".startform-modal");
  await expect(modal.locator(".startform-host")).toContainText("Strasse");
  await expect(modal.locator("[data-sf-send]")).toBeEnabled();
  await expect(modal.locator(".sf-extra")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("the Live view's Start instance opens the form, with the declared variables beneath it", async ({ page }) => {
  await openHarness(page);
  await page.evaluate(() => window.__mountLive());
  await page.locator("#start").click();
  const modal = page.locator(".startform-modal");
  await expect(modal.locator(".sf-extra")).toBeVisible();
  // The modal replaces the typed panel, which asked for the declaration alone and
  // dropped the form's fields — the same gap the other way round.
  await expect(page.locator("#start-panel")).toBeHidden();
  expect(await modal.locator(".sf-extra .dv-field").evaluateAll((els) => els.map((e) => e.dataset.name))).toEqual(EXTRAS);

  await fillFormHalf(modal.locator(".startform-host"));
  await modal.locator('.sf-extra [data-name="ApplicationId"]').fill("app-1");
  await page.locator("[data-sf-send]").click();
  await expect(modal).toHaveCount(0);

  // A start, never a deploy: the definition is already live.
  const w = await writes(page);
  expect(w.map((x) => x.method + " " + x.url)).toEqual(["POST /api/v1/processes/11/instances"]);
  expect(w[0].body.variables).toEqual(STARTED_WITH);
  expect(page.__errors).toEqual([]);
});

// The Tasks app reads the declaration from the process listing, which carries it beside
// startFormId, so it needs no BPMN of its own.
test.describe("Tasks app Start", () => {
  const FORM = {
    id: "collective",
    name: "Sammelanschluss (Collective Line)",
    schema: {
      type: "default",
      components: [
        { type: "textfield", key: "Name", label: "Name", validate: { required: true } },
        { type: "textfield", key: "address.street", label: "Strasse" },
        { type: "textfield", key: "Hidden", label: "Hidden", disabled: true },
      ],
    },
  };
  const PROCESSES = [{
    key: 11, processId: "proc-ucc", name: "Sammelanschluss", version: 1, executable: true,
    startFormId: "collective",
    startVariables: [
      { name: "ApplicationId", type: "string", required: true },
      { name: "TenantId", type: "string", default: "tenant-42" },
      { name: "Retries", type: "number", default: "3" },
      { name: "Options", type: "json", default: '{"dry":true}' },
      { name: "Name", type: "string", required: true },
      { name: "address", type: "json" },
      { name: "Hidden", type: "string", default: "from-declaration" },
    ],
  }];

  let posted;
  test.beforeEach(async ({ page }) => {
    posted = [];
    const errors = [];
    page.on("pageerror", (e) => errors.push(e.message));
    page.__errors = errors;
    await page.route("**/api/v1/**", async (route) => {
      const req = route.request();
      const path = new URL(req.url()).pathname;
      if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
      if (req.method() === "POST" && path === "/api/v1/processes/11/instances") {
        posted.push(JSON.parse(req.postData() || "{}"));
        return route.fulfill({ json: { key: 99 } });
      }
      if (path === "/api/v1/processes") return route.fulfill({ json: PROCESSES });
      if (path === "/api/v1/forms/collective") return route.fulfill({ json: FORM });
      if (path === "/api/v1/tasks" || path === "/api/v1/instances") {
        return route.fulfill({ json: { items: [], total: 0, totalExact: true, truncated: false } });
      }
      return route.fulfill({ json: [] });
    });
    await page.goto("/index.html#/tasks/start");
    await page.locator('#start-list .tasks-item[data-key="11"]').click({ timeout: 15000 });
    await expect(page.locator("#start-form")).toContainText("Strasse");
  });

  test("asks beneath the form for the declared variables it does not bind", async ({ page }) => {
    const extra = page.locator("#start-extra .sf-extra");
    await expect(extra).toBeVisible();
    expect(await extra.locator(".dv-field").evaluateAll((els) => els.map((e) => e.dataset.name))).toEqual(EXTRAS);
    await expect(extra.locator('[data-name="TenantId"]')).toHaveValue("tenant-42");
    expect(page.__errors).toEqual([]);
  });

  test("a required declared variable left empty stops the start", async ({ page }) => {
    await fillFormHalf(page.locator("#start-form"));
    await page.locator("#start-go").click();
    await expect(page.locator("#toast")).toContainText("Pflichtfeld: ApplicationId");
    expect(posted).toEqual([]);
    expect(page.__errors).toEqual([]);
  });

  test("Start process sends the form's data and the declared values", async ({ page }) => {
    await fillFormHalf(page.locator("#start-form"));
    await page.locator('#start-extra [data-name="ApplicationId"]').fill("app-1");
    await page.locator("#start-go").click();
    await expect.poll(() => posted.length).toBe(1);
    expect(posted[0].variables).toEqual(STARTED_WITH);
    expect(page.__errors).toEqual([]);
  });
});
