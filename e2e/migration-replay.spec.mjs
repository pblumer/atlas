// End-to-end coverage for the migration boundary on the Operations instance-replay
// view (api/web/editor.js mountInstanceReplay, ADR-0162).
//
// A migrated instance's history spans two versions of its process. The replay renders
// the version the instance is on *now*, so the steps that ran before the migration name
// elements that may not be on that diagram at all — and without the boundary in the
// list, an operator reading the history has no way to know why. These tests are what
// says the boundary is there, reads as a boundary rather than as another step, and
// explains itself when selected.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/migration-replay-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await page.waitForSelector("#history-list .ops-hrow");
});

test("the migration sits in the history between the steps it separates", async ({ page }) => {
  const rows = page.locator("#history-list .ops-hrow[data-i]:not(.root)");
  // start, legacy_review, the boundary, review, done.
  await expect(rows).toHaveCount(5);

  const mig = page.locator("#history-list .ops-hrow.mig");
  await expect(mig).toHaveCount(1);
  // Named by where it went, not by an element — it has none.
  await expect(mig).toContainText("Migrated");
  await expect(mig).toContainText("v1");
  await expect(mig).toContainText("v2");

  // Its position in the list is the point it happened at: after the v1 steps, before
  // the v2 ones. Ordering is the whole affordance — a boundary in the wrong place
  // explains the wrong steps.
  const order = await page.evaluate(() =>
    [...document.querySelectorAll("#history-list .ops-hrow[data-i]:not(.root)")].map((r) =>
      r.classList.contains("mig") ? "«migration»" : r.querySelector(".ops-hname").textContent.trim()));
  expect(order).toEqual(["Start", "legacy_review", "«migration»", "Review", "Done"]);

  expect(page.__errors, "page errors during replay").toEqual([]);
});

test("a step that ran on the old version keeps naming its own element", async ({ page }) => {
  // "legacy_review" is not in the v2 diagram, so bpmn-js can tell the test nothing about
  // it — the row falls back to the raw element id, which is the honest answer and the
  // one the backend resolved through v1. What must NOT happen is it being renamed to
  // whichever v2 element now holds that index.
  const rows = page.locator("#history-list .ops-hrow[data-i]:not(.root)");
  await expect(rows.nth(1)).toContainText("legacy_review");
  await expect(rows.nth(1)).not.toContainText("Triage");

  // And "Triage" — a v2 element this instance never reached — appears nowhere in the
  // history at all.
  await expect(page.locator("#history-list")).not.toContainText("Triage");

  expect(page.__errors, "page errors during replay").toEqual([]);
});

test("selecting the migration explains what changed, and by whom", async ({ page }) => {
  await page.locator("#history-list .ops-hrow.mig").click();

  const details = page.locator("#tab-details");
  await expect(details).toContainText("Migrated to another version");
  await expect(details).toContainText("Version 1");
  await expect(details).toContainText("Version 2");
  await expect(details).toContainText("hotfix"); // the target's version tag
  await expect(details).toContainText("dana");
  await expect(details).toContainText("the tax rate was wrong");
  // The consequence, spelled out: the steps above ran on a different model.
  await expect(details.locator(".ops-mig")).toContainText("above");

  // The row reads as selected, like any other history selection.
  await expect(page.locator("#history-list .ops-hrow.mig")).toHaveClass(/sel/);

  expect(page.__errors, "page errors during replay").toEqual([]);
});

test("selecting an element after the migration clears the boundary selection", async ({ page }) => {
  await page.locator("#history-list .ops-hrow.mig").click();
  await expect(page.locator("#tab-details")).toContainText("Migrated to another version");

  // Back to an ordinary step: the details must switch to the element, not keep showing
  // the boundary. The two selections are different kinds of thing and only one can be
  // active.
  await page.locator('#history-list .ops-hrow[data-eik="1002"]').click();
  const details = page.locator("#tab-details");
  await expect(details).toContainText("Element ID");
  await expect(details).not.toContainText("Migrated to another version");
  await expect(page.locator("#history-list .ops-hrow.mig")).not.toHaveClass(/sel/);

  expect(page.__errors, "page errors during replay").toEqual([]);
});
