// End-to-end coverage for the connector picker's "where does this run" badge
// (api/web/editor.js). The badge used to be a constant compiled into the browser,
// written when every kind but the plain job worker ran inside the engine. That stopped
// being true in two directions — kinds moved onto a worker by the server's own command
// line (ADR-0168), kinds born on a worker with no in-engine form (ADR-0173) — and the
// browser cannot know either. So the picker asks the server, and these tests hold the
// badge and its notice to that answer instead of to a constant.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/connector-placement-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await page.locator('[data-tab="implement"]').click();
});

// openPicker selects a task and expands its Type group (property groups but General
// start collapsed), so the kind rows and their badges are on screen.
async function openPicker(page, id) {
  await page.evaluate((el) => window.__select(el), id);
  await page.locator(".pgroup-head", { hasText: "Type" }).click();
}

const badge = (page, kind) => page.locator(`.stkind-row[data-kind='${kind}'] .stkind-where`);

test("each kind's badge says where this server runs it", async ({ page }) => {
  await openPicker(page, "Activity_rest");

  // Run here, and movable: the amber badge, because the engine's loop waits on the call.
  await expect(badge(page, "rest")).toHaveText(/in.engine/);
  await expect(badge(page, "rest")).toHaveClass(/stkind-where-engine/);

  // Offloaded on this server. The old constant called this one in-engine too.
  await expect(badge(page, "mail")).toHaveText("on a worker");
  await expect(badge(page, "mail")).toHaveClass(/stkind-where-worker/);

  // Born on a worker: no configuration could bring it into the engine, and the badge
  // says something different from "was moved out".
  await expect(badge(page, "mssql")).toHaveText("worker only");
  await expect(badge(page, "mssql")).toHaveClass(/stkind-where-worker/);

  // Runs here and has no out-of-process form; still in-engine, different advice below.
  await expect(badge(page, "userconnector")).toHaveText(/in.engine/);

  expect(page.__errors).toEqual([]);
});

test("a kind the server says nothing about carries no badge", async ({ page }) => {
  await openPicker(page, "Activity_rest");
  // The plain job worker: being out of process is what the kind IS, and the mockup
  // creates no job at all. Silence beats a badge that could be wrong.
  await expect(badge(page, "worker")).toHaveCount(0);
  await expect(badge(page, "mockup")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("an in-engine kind is told to prefer a job worker", async ({ page }) => {
  await page.evaluate(() => window.__select("Activity_rest"));
  const notice = page.locator(".stkind-notice");
  await expect(notice).toContainText("in its own process");
  await expect(notice).toContainText("Job worker");
  await expect(notice).not.toHaveClass(/stkind-notice-worker/);
  expect(page.__errors).toEqual([]);
});

test("an offloaded kind says its jobs are leased by a worker, and where its credential lives", async ({ page }) => {
  await page.evaluate(() => window.__select("Activity_mail"));
  const notice = page.locator(".stkind-notice");
  await expect(notice).toHaveClass(/stkind-notice-worker/);
  await expect(notice).toContainText("does not run this kind");
  await expect(notice).toContainText("connector configuration");
  // The advice for an in-engine kind would be nonsense here: it is already on a worker.
  await expect(notice).not.toContainText("should prefer");
  expect(page.__errors).toEqual([]);
});

test("a kind born on a worker says it has no in-engine form", async ({ page }) => {
  await page.evaluate(() => window.__select("Activity_sql"));
  await expect(page.locator(".stkind-notice")).toContainText("no in-engine form");
  expect(page.__errors).toEqual([]);
});

test("an engine-only kind is not told to move to a worker it cannot have", async ({ page }) => {
  await page.evaluate(() => window.__select("Activity_login"));
  const notice = page.locator(".stkind-notice");
  await expect(notice).toContainText("in its own process");
  await expect(notice).toContainText("nothing to move onto a worker");
  await expect(notice).not.toContainText("should prefer");
  expect(page.__errors).toEqual([]);
});

test("the placements are fetched once for the page, not per panel", async ({ page }) => {
  await openPicker(page, "Activity_rest");
  await expect(badge(page, "rest")).toHaveText(/in.engine/);
  for (const id of ["Activity_mail", "Activity_sql", "Activity_worker", "Activity_rest"]) {
    await page.evaluate((el) => window.__select(el), id);
  }
  await expect(badge(page, "mail")).toHaveText("on a worker");
  expect(await page.evaluate(() => window.__kindFetches)).toBe(1);
  expect(page.__errors).toEqual([]);
});
