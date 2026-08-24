// The Console panel that binds one repair form per connector kind
// (api/web/repairforms.js, ADR-draft-repair-forms-without-authoring).
//
// This is the surface that makes the feature worth having without per-task authoring:
// binding a form to `mail` once gives every mail task in every model the same guidance.
// So what these tests hold onto is that the panel shows what the *server* holds — the
// kinds it reports, the binding it stored, and a binding whose form was deleted named as
// stale rather than quietly shown as unset.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/repair-forms-console-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
});

test("one row per kind the server reports, showing the stored binding", async ({ page }) => {
  await page.evaluate(() => window.__mount());
  const selects = page.locator("select[data-repair-kind]");
  await expect(selects).toHaveCount(3);

  // The kinds are the server's, in the server's order — a list hardcoded in the browser
  // would omit whichever integration was added last.
  const kinds = await page.evaluate(() =>
    [...document.querySelectorAll("select[data-repair-kind]")].map((s) => s.dataset.repairKind));
  expect(kinds).toEqual(["mail", "rest", "sharepoint"]);

  // What is bound is selected; what is not shows the empty option, not the first form.
  await expect(page.locator('select[data-repair-kind="mail"]')).toHaveValue("mail-repair");
  await expect(page.locator('select[data-repair-kind="rest"]')).toHaveValue("");

  // Rendering the panel writes nothing.
  expect(await page.evaluate(() => window.__puts)).toEqual([]);
  expect(page.__errors, "page errors").toEqual([]);
});

test("picking a form saves the whole table, not just the row that changed", async ({ page }) => {
  await page.evaluate(() => window.__mount());
  await page.locator('select[data-repair-kind="rest"]').selectOption("rest-repair");

  await expect.poll(() => page.evaluate(() => window.__puts.length)).toBe(1);
  const put = await page.evaluate(() => window.__puts[0]);
  // Both bindings go up together. A per-row write would let two changes made in quick
  // succession interleave into a table that holds neither operator's intent.
  expect(put.byKind).toEqual({ mail: "mail-repair", rest: "rest-repair" });

  const t = await page.evaluate(() => window.__toast);
  expect(t.msg).toContain("rest");
  expect(t.kind).toBe("ok");
  expect(page.__errors, "page errors").toEqual([]);
});

test("clearing a kind drops it from the table", async ({ page }) => {
  await page.evaluate(() => window.__mount());
  await page.locator('select[data-repair-kind="mail"]').selectOption("");

  await expect.poll(() => page.evaluate(() => window.__puts.length)).toBe(1);
  const put = await page.evaluate(() => window.__puts[0]);
  // Absent, not empty-string: the stored table is the set of kinds that have a form, so
  // an unbound kind should not survive as a binding to nothing.
  expect(put.byKind).toEqual({});
  expect(await page.evaluate(() => window.__stored())).toEqual({});

  const t = await page.evaluate(() => window.__toast);
  expect(t.msg).toContain("cleared");
  expect(page.__errors, "page errors").toEqual([]);
});

test("a refused save says why and puts the panel back to what the server holds", async ({ page }) => {
  await page.evaluate(() => window.__mount());
  await page.evaluate(() => { window.__failNextPut = true; });
  await page.locator('select[data-repair-kind="rest"]').selectOption("rest-repair");

  // The 403 is the common case here — binding org-wide guidance is an admin act — so it
  // is named as that rather than shown as a raw status.
  await expect.poll(() => page.evaluate(() => (window.__toast || {}).msg || "")).toContain("admin");
  expect(await page.evaluate(() => window.__toast.kind)).toBe("warn");

  // And the picker is re-read, so it stops showing a choice the server never took. A
  // panel left displaying the refused value is a panel that lies about what operators
  // will be shown.
  await expect.poll(() => page.locator('select[data-repair-kind="rest"]').inputValue()).toBe("");
  expect(await page.evaluate(() => window.__stored())).toEqual({ mail: "mail-repair" });
  expect(page.__errors, "page errors").toEqual([]);
});

test("a binding whose form was deleted is named as missing, not shown as unset", async ({ page }) => {
  await page.evaluate(() => { window.__setStored({ mail: "deleted-form" }); });
  await page.evaluate(() => window.__mount());

  const mail = page.locator('select[data-repair-kind="mail"]');
  await expect(mail).toHaveValue("deleted-form");
  await expect(mail).toContainText("missing");
  // Falling back to "— none —" would read as "nobody bound one", and the person who did
  // would never learn their form is gone.
  await expect(page.locator("#repair-forms-body")).toContainText("falls back to the raw variable editor");
  expect(page.__errors, "page errors").toEqual([]);
});
