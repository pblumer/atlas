// End-to-end coverage for Deploy & run on a process that starts with a form
// (api/web/editor.js, ADR-0028).
//
// A process whose start event links a form already says what it starts with, in a form
// somebody laid out. Deploy & run ignored it and offered a free-form JSON textarea
// instead — so the author had to retype, as JSON, the values the form was there to
// collect, with no labels, no required marks and no validation. The panel now names the
// form and Deploy & run opens it, Send and Cancel.
//
// The order is the point: the form is asked *before* anything is deployed, so Cancel
// leaves the server exactly as it was. "Deploy & run" is one action; backing out of it
// should not leave a deployed version behind.
import { test, expect } from "@playwright/test";

// The editor's toolbar runs the width of the window and Deploy is its last button, so a
// narrower viewport puts it off screen — the modeler is a wide-screen surface.
test.use({ viewport: { width: 1600, height: 900 } });

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/deploy-start-form-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await page.locator("#deploy").click();
  await expect(page.locator("#deploy-panel")).toBeVisible();
});

const writes = (page) => page.evaluate(() => window.__writes);

test("the deploy panel names the start form instead of asking for JSON", async ({ page }) => {
  const body = page.locator("#deploy-body");
  await expect(body).toContainText("Account-Bestellung");
  await expect(body).toContainText("Deploy & run");
  // The JSON textarea is what it replaces: entering these values by hand, untyped and
  // unlabelled, when a designed form for exactly them exists.
  await expect(body.locator(".sv-json")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("Deploy & run opens the form in a modal with Send and Cancel", async ({ page }) => {
  await page.locator("#deploy-run").click();
  const modal = page.locator(".startform-modal");
  await expect(modal).toBeVisible();
  await expect(modal.locator(".modal-head h2")).toHaveText("Start values");
  // The real form, rendered by the same viewer the Tasks app uses — its labels, its
  // required marks, its field types.
  await expect(modal.locator(".startform-host")).toContainText("Vorname");
  await expect(modal.locator(".startform-host")).toContainText("Eilig");
  await expect(modal.locator("[data-sf-send]")).toBeEnabled();
  await expect(modal.locator("[data-sf-cancel]")).toBeEnabled();
  expect(page.__errors).toEqual([]);
});

test("Cancel deploys nothing at all", async ({ page }) => {
  await page.locator("#deploy-run").click();
  await expect(page.locator(".startform-modal")).toBeVisible();
  await page.locator("[data-sf-cancel]").click();
  await expect(page.locator(".startform-modal")).toHaveCount(0);
  // Not "deployed but not started" — nothing. The form was asked first for this reason.
  expect(await writes(page)).toEqual([]);
  expect(page.__errors).toEqual([]);
});

test("Escape is the same as Cancel", async ({ page }) => {
  await page.locator("#deploy-run").click();
  await expect(page.locator(".startform-modal")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.locator(".startform-modal")).toHaveCount(0);
  expect(await writes(page)).toEqual([]);
  expect(page.__errors).toEqual([]);
});

test("a required field left empty refuses the send, and deploys nothing", async ({ page }) => {
  await page.locator("#deploy-run").click();
  await expect(page.locator(".startform-modal")).toBeVisible();
  await page.locator("[data-sf-send]").click();

  await expect(page.locator("#sf-err")).toContainText("fix the highlighted fields");
  await expect(page.locator(".startform-modal")).toBeVisible(); // still open, values kept
  expect(await writes(page)).toEqual([]);
  expect(page.__errors).toEqual([]);
});

test("Send deploys and starts the instance with what the form collected", async ({ page }) => {
  await page.locator("#deploy-run").click();
  const modal = page.locator(".startform-modal");
  await expect(modal).toBeVisible();
  await modal.locator(".startform-host input[type='text']").first().fill("Anna");
  await modal.locator(".startform-host input[type='text']").nth(1).fill("Meier");
  await modal.locator(".startform-host input[type='checkbox']").first().check();
  await page.locator("[data-sf-send]").click();

  await expect(modal).toHaveCount(0);
  await expect(page.locator(".deploy-success-msg")).toContainText("started an instance");

  const w = await writes(page);
  expect(w.map((x) => x.method + " " + x.url.replace(/\?.*$/, ""))).toEqual([
    "POST /api/v1/deployments",
    "POST /api/v1/processes/11/instances",
  ]);
  // The form's data, verbatim, as the instance's start variables.
  expect(w[1].body.variables).toMatchObject({ vorname: "Anna", nachname: "Meier", eilig: true });
  expect(page.__errors).toEqual([]);
});

test("Deploy only never opens the form", async ({ page }) => {
  // Nothing is being started, so there are no start values to collect — and an unrelated
  // required field should not stand between an author and a plain deploy.
  await page.locator("#deploy-only").click();
  await expect(page.locator(".startform-modal")).toHaveCount(0);
  await expect(page.locator(".deploy-success-msg")).toContainText("Deployed proc-sf v1");
  const w = await writes(page);
  expect(w).toHaveLength(1);
  expect(w[0].method).toBe("POST");
  expect(w[0].url).toContain("/deployments");
  expect(page.__errors).toEqual([]);
});
