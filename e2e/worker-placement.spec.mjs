// End-to-end coverage for the "where does this run" badge in the three Modeler panels
// that pick an implementation (api/web/editor.js): the worker picker, a script task's
// language, and a business rule task's decision binding.
//
// The worker picker's badge used to be a constant compiled into the browser, written
// when every kind but the plain job worker ran inside the engine. That stopped being true
// in two directions — kinds moved onto a worker by the server's own command line
// (ADR-0168), kinds born on a worker with no in-engine form (ADR-0173) — and the browser
// cannot know either. The other two panels said nothing at all, while authoring work that
// the same flag moves. All three now ask the server, and these tests hold badge and notice
// to that answer.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/worker-placement-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await page.locator('[data-tab="implement"]').click();
});

// openPicker selects a task and expands its Type group (property groups but General
// start collapsed), so the kind rows and their badges are on screen.
async function openPicker(page, id) {
  await page.evaluate((el) => window.__select(el), id);
  await page.locator(".pgroup-head", { hasText: "Worker type" }).click();
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
  await expect(notice).toContainText("does not run this itself");
  await expect(notice).toContainText("holds the configured Worker");
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

// A script task's language is enabled per language and offloaded as one kind, so two
// languages on the same server can differ — the panel says which, per task.
test("a script task says where its language runs, per language", async ({ page }) => {
  await page.evaluate(() => window.__select("Activity_pwsh"));
  // The badge and the notice sit with the field that chooses the language, so they are
  // on screen exactly when the choice is being made.
  await page.locator(".pgroup-head", { hasText: "Script" }).click();
  await expect(page.locator(".field .stkind-where")).toBeVisible();
  await expect(page.locator(".field .stkind-where")).toHaveText("on a worker");
  await expect(page.locator(".stkind-notice")).toBeVisible();
  await expect(page.locator(".stkind-notice")).toContainText("interpreter has to be installed");

  await page.evaluate(() => window.__select("Activity_py"));
  await expect(page.locator(".field .stkind-where")).toHaveText(/in.engine/);
  // The worker advice would be nonsense here: a script task cannot become a job
  // worker task, so what it is told is how Atlas normally runs scripts.
  await expect(page.locator(".stkind-notice")).toContainText("holds the loop with it");
  await expect(page.locator(".stkind-notice")).not.toContainText("should prefer");
  expect(page.__errors).toEqual([]);
});

test("a FEEL script says nothing about placement, because it creates no job", async ({ page }) => {
  await page.evaluate(() => window.__select("Activity_feel"));
  await expect(page.locator(".field .stkind-where")).toHaveCount(0);
  await expect(page.locator(".stkind-notice")).toHaveCount(0);
  // The option itself still says where FEEL runs; that one has always been true.
  await expect(page.locator("#f-scriptlang")).toHaveValue("feel");
  expect(page.__errors).toEqual([]);
});

test("a business rule task says where its decision is evaluated, per binding", async ({ page }) => {
  await page.evaluate(() => window.__select("Activity_rule"));
  await page.locator(".pgroup-head", { hasText: "Called decision" }).click();
  await expect(page.locator(".field .stkind-where")).toBeVisible();
  await expect(page.locator(".field .stkind-where")).toHaveText(/in.engine/);
  await expect(page.locator(".stkind-notice")).toBeVisible();
  await expect(page.locator(".stkind-notice")).toContainText("embedded DMN library");

  await page.evaluate(() => window.__select("Activity_rule_temis"));
  await expect(page.locator(".field .stkind-where")).toHaveText("on a worker");
  await expect(page.locator(".stkind-notice")).toContainText("endpoint and its credential");
  expect(page.__errors).toEqual([]);
});

test("the Evaluation options name the decision, not where it runs", async ({ page }) => {
  await page.evaluate(() => window.__select("Activity_rule"));
  // "In-engine (embedded DMN)" was a placement claim the select could not keep: the
  // binding is offloadable, so the badge answers that and the label names the decision.
  await expect(page.locator("#f-brt-mode")).not.toContainText("In-engine");
  await expect(page.locator("#f-brt-mode")).toContainText("Embedded DMN");
  expect(page.__errors).toEqual([]);
});

test("the fetch is still shared across all three panels", async ({ page }) => {
  await openPicker(page, "Activity_rest");
  await expect(badge(page, "rest")).toHaveText(/in.engine/);
  for (const id of ["Activity_pwsh", "Activity_rule", "Activity_rule_temis", "Activity_py"]) {
    await page.evaluate((el) => window.__select(el), id);
  }
  await expect(page.locator(".field .stkind-where")).toHaveText(/in.engine/);
  expect(await page.evaluate(() => window.__kindFetches)).toBe(1);
  expect(page.__errors).toEqual([]);
});
