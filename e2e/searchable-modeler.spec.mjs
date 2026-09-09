// End-to-end coverage for the process's "Searchable variables" declaration in the
// Modeler panel (api/web/editor.js, ADR-0244). A process states what it wants to be
// found by — atlas:searchable="identityId,item" — and the engine maintains a value
// index for exactly those names. The declaration shipped as an attribute with no
// moddle property and no field, so it could only be authored by editing the XML
// outside the Modeler, and opening such a model in the Modeler risked dropping it.
//
// The assertions are about the exported XML, because that is what a deploy sends.
// Without the moddle property (api/web/atlas-moddle.json) every test here fails except
// the round trip: the raw attribute survives in moddle's $attrs, but the panel can
// neither read nor write it.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/searchable-modeler-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
});

// mount boots the editor on one of the harness's two models and opens the process
// panel — nothing selected is what shows the process itself, and its "Process" group
// starts collapsed (only "General" is open by default, pgroup.js).
async function mount(page, kind) {
  await page.evaluate((k) => window.__mount(k), kind || "declared");
  await expect(page.locator("#p-body")).toBeVisible();
  await page.evaluate(() => window.__deselect());
  await page.locator('.pgroup[data-group="Process"] .pgroup-head').click();
  await expect(page.locator("#f-psearch")).toBeVisible();
}

// processTag returns the exported <bpmn:process …> start tag, so an assertion sees
// the attribute on the process rather than anywhere in the document.
async function processTag(page) {
  const xml = await page.evaluate(() => window.__xml());
  return /<bpmn:process\b[^>]*>/.exec(xml)?.[0] || "";
}

// declare types a declaration into the field and commits it (the panel saves on change).
async function declare(page, value) {
  await page.locator("#f-psearch").fill(value);
  await page.locator("#f-psearch").blur();
}

test("the panel shows the declaration the model carries", async ({ page }) => {
  await mount(page, "declared");
  await expect(page.locator("#f-psearch")).toHaveValue("identityId,item");

  // …and a process that declares nothing shows an empty field, not the other model's.
  await mount(page, "plain");
  await expect(page.locator("#f-psearch")).toHaveValue("");
  expect(page.__errors).toEqual([]);
});

test("a declaration typed in the panel reaches the deployed XML", async ({ page }) => {
  await mount(page, "plain");
  await declare(page, "identityId, item");

  const tag = await processTag(page);
  expect(tag).toContain('atlas:searchable="identityId, item"');
  // No warning: the spacing is the compiler's to forgive.
  expect(await page.evaluate(() => window.__toasts)).toEqual([]);
  expect(page.__errors).toEqual([]);
});

test("clearing the field removes the attribute rather than writing an empty one", async ({ page }) => {
  await mount(page, "declared");
  await declare(page, "");

  expect(await processTag(page)).not.toContain("atlas:searchable");
  expect(page.__errors).toEqual([]);
});

// The round trip, which is the one thing that already worked: moddle keeps an
// attribute it has no property for in $attrs and writes it back, so a hand-authored
// declaration was never lost — it was only invisible. Now that the panel reads and
// rewrites the value as a real property, this guards the regression that would come
// with that: a panel that normalizes, empties or duplicates a declaration nobody
// touched.
test("a declaration the author never touches survives the round trip", async ({ page }) => {
  await mount(page, "declared");

  expect(await processTag(page)).toContain('atlas:searchable="identityId,item"');
  expect(page.__errors).toEqual([]);
});

// The panel mirrors the compiler's check (a nameless entry, a repeated name) so the
// mistake surfaces while authoring instead of as a refused deploy — but it stores what
// was typed, the way the TTL fields do, rather than dropping the author's value.
test("a declaration the compiler would refuse warns, and is still stored", async ({ page }) => {
  await mount(page, "plain");

  await declare(page, "identityId,,item");
  let toasts = await page.evaluate(() => window.__toasts);
  expect(toasts.at(-1).kind).toBe("err");
  expect(toasts.at(-1).msg).toContain("no name");
  expect(await processTag(page)).toContain('atlas:searchable="identityId,,item"');

  await declare(page, "identityId, identityId");
  toasts = await page.evaluate(() => window.__toasts);
  expect(toasts.at(-1).kind).toBe("err");
  expect(toasts.at(-1).msg).toContain("twice");

  // A valid declaration is accepted silently.
  const before = (await page.evaluate(() => window.__toasts)).length;
  await declare(page, "identityId, item");
  expect(await page.evaluate(() => window.__toasts)).toHaveLength(before);
  expect(page.__errors).toEqual([]);
});
