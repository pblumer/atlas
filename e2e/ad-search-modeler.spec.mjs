// End-to-end coverage for authoring an AD search written as a document
// (docs/planning/ad-search-as-document.md, S2). It drives the real bpmn-js modeler
// and Atlas properties panel: resultAs and documentName must survive the moddle round
// trip, the file name must appear only in the document form, and switching back to the
// variable form must drop it — the compiler refuses a documentName without the form.
import { test, expect } from "@playwright/test";

async function openGroup(page, title) {
  const group = page.locator(`.pgroup[data-group*="${title}" i]`);
  if (await group.count() === 0) return;
  if (await group.evaluate((el) => el.classList.contains("collapsed"))) {
    await group.locator(".pgroup-head").click();
  }
}

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/ad-search-modeler-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await page.locator('[data-tab="implement"]').click();
  await page.evaluate(() => window.__selectSearch());
  await openGroup(page, "Operation");
  await openGroup(page, "Output");
});

test("the document form writes resultAs and the file name, and the variable form drops the name", async ({ page }) => {
  const resultAs = page.locator("#f-st-resultAs");
  await expect(resultAs).toHaveValue("");
  await expect(page.locator("#f-st-documentName")).toHaveCount(0);

  await resultAs.selectOption("document");
  await openGroup(page, "Output");
  const name = page.locator("#f-st-documentName");
  await expect(name).toBeVisible();
  await expect(name).toHaveAttribute("placeholder", "ad-search.csv");
  // The hints follow the form: the file's layout and the FEEL test of a multi-valued
  // cell, the columns being required, the cap's own default.
  const output = page.locator(".pgroup[data-group*='Output' i]");
  await expect(output).toContainText('split(row.memberOf, "[|]")');
  await expect(page.locator("#f-st-maxEntries")).toHaveAttribute("placeholder", "100000");

  await name.fill("benutzer.csv");
  await name.blur();
  let xml = await page.evaluate(() => window.__xml());
  expect(xml).toContain('resultAs="document"');
  expect(xml).toContain('documentName="benutzer.csv"');

  await page.locator("#f-st-resultAs").selectOption("");
  await expect(page.locator("#f-st-documentName")).toHaveCount(0);
  xml = await page.evaluate(() => window.__xml());
  // The panel writes an emptied select as resultAs="", which the compiler reads as the
  // variable form; the name must be gone, because it is refused without the form.
  expect(xml).not.toContain('resultAs="document"');
  expect(xml).not.toContain("documentName=");
  expect(xml).toContain('select="sAMAccountName,mail,memberOf"');
  expect(page.__errors).toEqual([]);
});

test("another operation drops the document form", async ({ page }) => {
  await page.locator("#f-st-resultAs").selectOption("document");
  await openGroup(page, "Output");
  await page.locator("#f-st-documentName").fill("benutzer.csv");
  await page.locator("#f-st-documentName").blur();

  await page.locator("#f-st-operation").selectOption("sync");
  await expect(page.locator("#f-st-resultAs")).toHaveCount(0);
  const xml = await page.evaluate(() => window.__xml());
  expect(xml).not.toContain("resultAs=");
  expect(xml).not.toContain("documentName=");
  expect(page.__errors).toEqual([]);
});
