// The line an operator reads after restoring a portable backup
// (api/web/restore-report.js,
// ADR-draft-a-portable-backup-does-not-overwrite-another-installations-identity).
//
// A restore that quietly declines to take a deployed definition is only honest if it
// says which one and why, so the sentence is what these assert — not that a function
// returned something truthy.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/restore-report-harness.html");
  await page.waitForFunction(() => window.__ready === true);
  page._errors = errors;
});

test.afterEach(async ({ page }) => {
  expect(page._errors, "no uncaught page errors").toEqual([]);
});

const summary = (page, report) => page.evaluate((r) => window.__summary(r), report);

test("a restore that took everything reads exactly as it always did", async ({ page }) => {
  // Nothing held back must add nothing: an operator whose restore was complete should
  // not have to read a sentence about a case that did not happen.
  const text = await summary(page, { restored: 7, skipped: 0, collisions: [], restartRequired: true });
  expect(text).toBe("Restored 7 file(s). Restart the server to activate restored deployments.");
});

test("a held-back definition is named, with both sides of the clash", async ({ page }) => {
  const text = await summary(page, {
    restored: 5,
    skipped: 1,
    restartRequired: true,
    collisions: [{ kind: "process", key: 1, here: "beta v1", incoming: "alpha v1" }],
  });
  // The key, so it can be looked up; both names, so it is clear which is this
  // installation's and which came out of the file.
  expect(text).toContain("key 1");
  expect(text).toContain("here: beta v1");
  expect(text).toContain("archive: alpha v1");
  // And why, because "not restored" without a reason reads as a failure rather than
  // as the deliberate refusal it is.
  expect(text).toContain("belongs to the installation that issued it");
  expect(text).toContain("fresh instance");
});

test("several clashes are all named rather than counted", async ({ page }) => {
  const text = await summary(page, {
    restored: 2,
    skipped: 3,
    restartRequired: true,
    collisions: [
      { kind: "process", key: 1, here: "beta v1", incoming: "alpha v1" },
      { kind: "process", key: 2, here: "orders v3", incoming: "orders v1" },
      { kind: "decision", key: 4, here: "discount v1", incoming: "eligibility v2" },
    ],
  });
  expect(text).toContain("3 deployed definition(s) were NOT restored");
  for (const key of ["key 1", "key 2", "key 4"]) expect(text).toContain(key);
  expect(text).toContain("eligibility v2");
});

test("a response with no collisions field is treated as a clean restore", async ({ page }) => {
  // The full-snapshot endpoint answers without one, and an older server would too.
  const text = await summary(page, { restored: 3, restartRequired: false });
  expect(text).toBe("Restored 3 file(s).");
  expect(await page.evaluate(() => window.__skipNote(null))).toBe("");
  expect(await page.evaluate(() => window.__skipNote({}))).toBe("");
});
