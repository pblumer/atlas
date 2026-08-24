// End-to-end coverage for structured values in the replay's Variables tab
// (api/web/editor.js, ADR-0048): a list and one of its elements are told apart at a
// glance, and either can be opened where it stands.
//
// The case behind it: a loop hands one element of `customers` to each round as
// `customer`. In a table cell both values truncate to the same thirty characters — the
// only difference, the opening bracket, sits at the edge — so an operator read the
// element as the whole list and concluded the loop was binding the wrong thing.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/variables-json-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await page.locator('#history-list .ops-hrow[data-eik="1001"]').click();
  await page.locator("#rp-tabs button[data-tab='variables']").click();
  await expect(page.locator("#tab-variables .vt tbody tr").first()).toBeVisible();
});

const cell = (page, name) => page.locator(`#tab-variables .vt tbody tr`, { has: page.locator(`td.c-name`, { hasText: name }) });

test("a list and an object are told apart without opening either", async ({ page }) => {
  const rows = page.locator("#tab-variables .vt .v-open .v-sum");
  await expect(rows).toHaveCount(2);
  // The brackets carry the shape, so the two summaries differ at their first character
  // rather than thirty characters into a truncated preview.
  await expect(rows.filter({ hasText: "items" })).toHaveText("[3 items]");
  await expect(rows.filter({ hasText: "fields" })).toHaveText("{3 fields}");
});

test("a structure opens where it stands, and can still be opened in a window", async ({ page }) => {
  const btn = page.locator("#tab-variables .v-open", { hasText: "fields" });
  await expect(btn).toHaveAttribute("aria-expanded", "false");
  await btn.click();

  const struct = page.locator("#tab-variables .v-struct:not([hidden]) .vj-body");
  await expect(struct).toHaveCount(1);
  await expect(btn).toHaveAttribute("aria-expanded", "true");
  // Pretty-printed, so the structure is the thing that is read — every field on its own
  // line, and the element's own values rather than the list's.
  const text = await struct.innerText();
  expect(text).toContain(`"Nachname": "Blumer"`);
  expect(text).toContain(`"Umsatz": 5000`);
  expect(text.split("\n").length).toBeGreaterThan(3);

  // The window is still one step further in.
  await page.locator("#tab-variables .v-struct:not([hidden]) .v-big").click();
  await expect(page.locator("#var-modal-ov")).toBeVisible();
  await expect(page.locator("#var-modal-title")).toHaveText("customer");
  await page.locator("#var-modal-x").click();
  await expect(page.locator("#var-modal-ov")).toBeHidden();

  // Clicking again closes it.
  await btn.click();
  await expect(page.locator("#tab-variables .v-struct:not([hidden])")).toHaveCount(0);

  expect(page.__errors, "page errors").toEqual([]);
});

test("an opened structure survives the view re-rendering under it", async ({ page }) => {
  await page.locator("#tab-variables .v-open", { hasText: "items" }).click();
  await expect(page.locator("#tab-variables .v-struct:not([hidden])")).toHaveCount(1);

  // The rows are rewritten on every poll and every filter keystroke; an expansion that
  // lived only in the click would collapse under the reader.
  await page.locator("#tab-variables #v-filter").fill("cust");
  await expect(page.locator("#tab-variables .v-struct:not([hidden])")).toHaveCount(1);
  await page.locator("#tab-variables #v-filter").fill("");
  await expect(page.locator("#tab-variables .v-struct:not([hidden])")).toHaveCount(1);
});
