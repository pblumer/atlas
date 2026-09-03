// End-to-end coverage for the shape of the Worker Type picker's rows (api/web/editor.js).
//
// The picker lists nineteen Worker Types. Each used to be a two-line card — name, then
// its description underneath — which put a single choice across four screens of
// scrolling, and every second name ended in the word "Connector", which separates
// nothing while pushing apart the words that do (and, since ADR-0203, names the wrong
// concept). A row is now one line: the name, which no longer carries that suffix, the
// placement badge parked at the right edge, and the description moved to the row's
// tooltip. The Worker Type an author has actually chosen still spells its description
// out, because that is the one being read rather than scanned.
//
// These tests hold that shape, and hold the two things it must not cost: the full name
// is still what search matches on, and the badge is still there, on its own line.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/worker-placement-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await page.locator('[data-tab="implement"]').click();
  await page.evaluate(() => window.__select("Activity_rest"));
  await page.locator(".pgroup-head", { hasText: "Worker type" }).click();
});

const rows = (page) => page.locator(".stkind-row");

test("no row repeats the word every kind in the list shares", async ({ page }) => {
  const names = await rows(page).locator("b").allInnerTexts();
  expect(names.length).toBeGreaterThan(10);
  expect(names.filter((n) => /\bConnector$/.test(n))).toEqual([]);
  // What is left is the part that tells the kinds apart, not a truncation of it.
  expect(names).toContain("REST Outbound");
  expect(names).toContain("Microsoft SQL Server");
});

test("a kind is one line: the description is the row's tooltip", async ({ page }) => {
  // Every row carries its description where it costs no height...
  for (const title of await rows(page).evaluateAll((rs) => rs.map((r) => r.getAttribute("title")))) {
    expect((title || "").length).toBeGreaterThan(5);
  }
  // ...and only the chosen kind spends a second line on it.
  await expect(page.locator(".stkind-desc")).toHaveCount(1);
  await expect(page.locator(".stkind-row-on .stkind-desc")).toHaveText(/REST/i);
});

test("choosing another kind moves the spelled-out description with it", async ({ page }) => {
  await page.locator(".stkind-row[data-kind='mail']").click();
  await page.locator(".pgroup-head", { hasText: "Worker type" }).click();
  await expect(page.locator(".stkind-row-on")).toHaveAttribute("data-kind", "mail");
  await expect(page.locator(".stkind-desc")).toHaveCount(1);
  await expect(page.locator(".stkind-row[data-kind='rest'] .stkind-desc")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("the badge sits beside the name, not inside it", async ({ page }) => {
  // As a sibling it can be pushed to the row's right edge, which is what lets the eye run
  // down a column of badges instead of hunting for each one after a name of unpredictable
  // length. Inside the name it would wrap with the text and lose that column.
  const row = page.locator(".stkind-row[data-kind='mail']");
  await expect(row.locator(".stkind-name .stkind-where")).toHaveCount(0);
  await expect(row.locator("> .stkind-where")).toHaveText("on a worker");
  // And it stays on one line: "on a worker" broken in two would cost the row the line it
  // just saved.
  const lines = await row.locator(".stkind-where").evaluate((b) => b.getClientRects().length);
  expect(lines).toBe(1);
});

test("searching still finds a kind by the word the label no longer shows", async ({ page }) => {
  const filter = page.locator("#f-stkind-filter");
  await filter.fill("connector");
  await expect(page.locator(".stkind-row[data-kind='rest']")).toBeVisible();
  const shown = await page.locator(".stkind-row:not([hidden])").count();
  expect(shown).toBeGreaterThan(5);

  // The description is still searched too, though no row prints it any more.
  await filter.fill("rest api");
  await expect(page.locator(".stkind-row[data-kind='rest']")).toBeVisible();
  await expect(page.locator(".stkind-row[data-kind='mail']")).toBeHidden();
  expect(page.__errors).toEqual([]);
});
