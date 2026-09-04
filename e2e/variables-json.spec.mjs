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
  // The window is the shared dialog now (api/web/dialog.js): built when it opens and
  // gone when it closes, rather than a hidden div in the view being shown in place.
  const window_ = page.locator(".modal-ov .var-modal");
  await expect(window_).toBeVisible();
  await expect(window_.locator(".modal-head h2")).toHaveText("customer");
  await window_.locator(".modal-head [aria-label='Close']").click();
  await expect(page.locator(".modal-ov .var-modal")).toHaveCount(0);

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

test("a structure comes closed, and an expansion does not follow the reader to another element", async ({ page }) => {
  // Everything starts closed: the table is the thing to read, and a wall of JSON in a
  // panel this size hides the very rows it belongs to.
  await expect(page.locator("#tab-variables .v-struct:not([hidden])")).toHaveCount(0);

  await page.locator("#tab-variables .v-open", { hasText: "items" }).click();
  await expect(page.locator("#tab-variables .v-struct:not([hidden])")).toHaveCount(1);

  // The end event holds a variable of the same name. What was opened on the script task
  // belongs to *that* set — carried over, it would read as "these come open by default".
  await page.locator('#history-list .ops-hrow[data-eik="1002"]').click();
  await expect(page.locator("#tab-variables .v-open")).toHaveCount(1);
  await expect(page.locator("#tab-variables .v-struct:not([hidden])")).toHaveCount(0);

  // And back again: still closed, not remembered from before.
  await page.locator('#history-list .ops-hrow[data-eik="1001"]').click();
  await expect(page.locator("#tab-variables .v-struct:not([hidden])")).toHaveCount(0);

  expect(page.__errors, "page errors").toEqual([]);
});

// The toolbar's one structure control. A control that only appears once something is
// open is not there when it is first looked for — which is how it was reported: "Collapse
// all is not visible", from a tab where everything was correctly closed.
test("one toolbar control opens every structure and closes them again", async ({ page }) => {
  const toggle = page.locator("#tab-variables #v-struct-toggle");
  // Visible with nothing open, because opening is what a reader wants first.
  await expect(toggle).toBeVisible();
  await expect(toggle).toHaveText("▸ Expand all");

  await toggle.click();
  await expect(page.locator("#tab-variables .v-struct:not([hidden])")).toHaveCount(2);
  await expect(toggle).toHaveText("▾ Collapse all");
  for (const a of await page.locator("#tab-variables .v-open").all()) {
    await expect(a).toHaveAttribute("aria-expanded", "true");
  }

  // And back: one click closes both, because an open structure can push the row it
  // belongs to off the screen.
  await toggle.click();
  await expect(page.locator("#tab-variables .v-struct:not([hidden])")).toHaveCount(0);
  await expect(toggle).toHaveText("▸ Expand all");
  await expect(page.locator("#tab-variables .v-open").first()).toHaveAttribute("aria-expanded", "false");
});

// Opening one row by hand is enough to make the control the way back out.
test("the control flips to Collapse all as soon as a row is opened by hand", async ({ page }) => {
  const toggle = page.locator("#tab-variables #v-struct-toggle");
  await page.locator("#tab-variables .v-open", { hasText: "items" }).click();
  await expect(toggle).toHaveText("▾ Collapse all");

  await toggle.click();
  await expect(page.locator("#tab-variables .v-struct:not([hidden])")).toHaveCount(0);
  await expect(toggle).toHaveText("▸ Expand all");
});

// "All" means what the table is showing. A filter that leaves one structure on screen must
// not open the ones it hid — they would be waiting, open, when the filter is cleared.
test("expand all follows the name filter", async ({ page }) => {
  await page.locator("#tab-variables #v-filter").fill("customers");
  await expect(page.locator("#tab-variables .vt tbody tr.v-struct")).toHaveCount(1);
  await page.locator("#tab-variables #v-struct-toggle").click();
  await expect(page.locator("#tab-variables .v-struct:not([hidden])")).toHaveCount(1);

  await page.locator("#tab-variables #v-filter").fill("");
  await expect(page.locator("#tab-variables .vt tbody tr.v-struct")).toHaveCount(2);
  await expect(page.locator("#tab-variables .v-struct:not([hidden])")).toHaveCount(1);
});

// The Variables table is enhanced by the shared sort/filter helper (table.js), like every
// table in a view. The helper drove each row's `hidden` to say what its filter matched —
// including the expansion rows, which are not data — so every structure was forced open on
// arrival and again on every rewrite, and the toolbar control, which reports the set of
// openings the *reader* made, could not say the truth about it. It reproduced only with both modules
// present, which is why the harness now composes them the way app.js does.
test("the sort/filter helper does not force the expansions open", async ({ page }) => {
  await expect(page.locator("#tab-variables .vt tbody tr.v-struct")).toHaveCount(2);
  await expect(page.locator("#tab-variables .v-struct:not([hidden])")).toHaveCount(0);
  await expect(page.locator("#tab-variables #v-struct-toggle")).toHaveText("▸ Expand all");

  // An opening the reader made survives the helper re-running over the rows.
  await page.locator("#tab-variables .v-open", { hasText: "items" }).click();
  await expect(page.locator("#tab-variables .v-struct:not([hidden])")).toHaveCount(1);
  await page.locator("#tab-variables .vt tbody").evaluate((b) => b.appendChild(document.createComment("x")));
  await page.waitForTimeout(50);
  await expect(page.locator("#tab-variables .v-struct:not([hidden])")).toHaveCount(1);
  await expect(page.locator("#tab-variables #v-struct-toggle")).toHaveText("▾ Collapse all");
});

// Sorting reorders the data rows; an expansion belongs to the row above it and has to go
// where that row goes, or it ends up explaining a stranger.
test("an expansion follows its variable when the table is sorted", async ({ page }) => {
  await page.locator("#tab-variables .v-open", { hasText: "items" }).click();
  const owner = await page.locator("#tab-variables .v-struct:not([hidden])").evaluate(
    (s) => s.previousElementSibling.querySelector("td.c-name").textContent.trim());

  await page.locator("#tab-variables .vt thead th.dt-sortable").first().click();
  await page.waitForTimeout(50);
  await page.locator("#tab-variables .vt thead th.dt-sortable").first().click(); // reverse it
  await page.waitForTimeout(50);

  const stillOwner = await page.locator("#tab-variables .v-struct:not([hidden])").evaluate(
    (s) => s.previousElementSibling.querySelector("td.c-name").textContent.trim());
  expect(stillOwner, "the expansion sits under the variable it belongs to").toBe(owner);
});
