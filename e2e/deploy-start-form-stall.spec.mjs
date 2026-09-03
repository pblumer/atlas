// A start form that never arrives (api/web/formviewer.js, api/web/editor.js).
//
// Deploy & run opens the process's start form in a modal (ADR-0028). The modal put up
// "Loading form…" and then waited on two things — the vendored form-js viewer and the
// form definition — with no deadline on either. A request that hangs rather than fails
// is not rare in the wild (a stalled asset, a proxy that holds the connection, a server
// that stops answering), and it left that placeholder standing for the rest of the
// session: no error, no way to retry, and a Send that stayed disabled beside it.
//
// A stall is a failure somebody can act on, so it is reported as one, it names which
// half did not arrive, and it offers the retry — which costs nothing, because whatever
// did arrive in the meantime is in the browser's cache.
import { test, expect } from "@playwright/test";

test.use({ viewport: { width: 1600, height: 900 } });

const VIEWER = "**/vendor/form-js/form-viewer.js";

test.beforeEach(async ({ page }) => {
  // The load deadline is 20s (FORM_LOAD_TIMEOUT_MS); these tests wait it out.
  test.setTimeout(120000);
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
});

const writes = (page) => page.evaluate(() => window.__writes);

// open mounts the editor and gets as far as the open start-form modal.
async function openModal(page, query = "") {
  await page.goto("/deploy-start-form-harness.html" + query);
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await page.locator("#deploy").click();
  await expect(page.locator("#deploy-panel")).toBeVisible();
  await page.locator("#deploy-run").click();
  const modal = page.locator(".startform-modal");
  await expect(modal).toBeVisible();
  return modal;
}

test("a definition that never arrives is reported, and deploys nothing", async ({ page }) => {
  const modal = await openModal(page, "?stall=form");
  await expect(modal.locator("#sf-host")).toContainText("Loading form"); // still waiting

  await expect(modal.locator("#sf-host .err")).toContainText("The form definition did not arrive",
    { timeout: 40000 });
  // Send would start an instance with nothing in it, so it stays out — but the way out
  // that still works is named, and the modal is still closable.
  await expect(modal.locator("[data-sf-send]")).toBeDisabled();
  await expect(modal.locator("#sf-host")).toContainText("Deploy only still works");
  await expect(modal.locator("[data-sf-retry]")).toBeVisible();
  // Nothing was deployed: the form is asked before anything reaches the server.
  expect(await writes(page)).toEqual([]);
  expect(page.__errors).toEqual([]);
});

test("Try again renders the form once it does arrive", async ({ page }) => {
  const modal = await openModal(page, "?stall=form");
  await expect(modal.locator("#sf-host .err")).toBeVisible({ timeout: 40000 });

  await page.evaluate(() => window.__releaseForm());
  await modal.locator("[data-sf-retry]").click();

  await expect(modal.locator(".startform-host")).toContainText("Vorname", { timeout: 30000 });
  await expect(modal.locator("[data-sf-send]")).toBeEnabled();
  expect(page.__errors).toEqual([]);
});

test("a viewer bundle that stalls is reported, and the failure is not remembered", async ({ page }) => {
  // Hold the vendored bundle: the request is neither answered nor refused.
  const held = [];
  await page.route(VIEWER, (route) => { held.push(route); });
  const modal = await openModal(page);
  await expect(modal.locator("#sf-host .err")).toContainText("The form viewer did not arrive",
    { timeout: 40000 });

  // The memoized loader must have forgotten the failed import — otherwise every later
  // form in this tab fails with it, and a page reload is the only way back.
  await page.unroute(VIEWER);
  await Promise.all(held.map((r) => r.abort().catch(() => {})));
  await modal.locator("[data-sf-retry]").click();

  await expect(modal.locator(".startform-host")).toContainText("Vorname", { timeout: 30000 });
  await expect(modal.locator("[data-sf-send]")).toBeEnabled();
  expect(await writes(page)).toEqual([]);
});

test("cancelling while it loads still deploys nothing, and the late arrival is dropped", async ({ page }) => {
  const modal = await openModal(page, "?stall=form");
  await modal.locator("[data-sf-cancel]").click();
  await expect(page.locator(".startform-modal")).toHaveCount(0);

  // The definition lands after the modal is gone: rendering it now would build a live
  // form, with its timers and listeners, into a container already detached.
  await page.evaluate(() => window.__releaseForm());
  await page.waitForTimeout(1000);
  expect(await page.locator(".fjs-container").count()).toBe(0);
  expect(await writes(page)).toEqual([]);
  expect(page.__errors).toEqual([]);
});
