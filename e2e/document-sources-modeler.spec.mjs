// End-to-end coverage for writing an LDAP search or a SQL query as a document
// (docs/planning/ad-search-as-document.md, S3), through the real bpmn-js modeler and
// Atlas properties panel: select, resultAs and documentName must survive the moddle
// round trip, and switching back to the variable form must drop the file name, which
// the compiler refuses without the form.
import { test, expect } from "@playwright/test";

// openGroup unfolds every panel section: a re-render after a mode change draws the
// sections afresh, so the one a field moved into may be folded again.
async function openGroup(page) {
  const folded = page.locator(".pgroup.collapsed .pgroup-head");
  while (await folded.count() > 0) await folded.first().click();
}

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/document-sources-modeler-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await page.locator('[data-tab="implement"]').click();
});

function taskXml(xml, id) {
  const start = xml.indexOf(`id="${id}"`);
  return xml.slice(start, xml.indexOf("</bpmn:serviceTask>", start));
}

test("an LDAP search selects its columns and writes them as a document", async ({ page }) => {
  await page.evaluate(() => window.__selectSearch());
  await openGroup(page);
  await openGroup(page);
  await page.locator("#f-st-resultAs").selectOption("document");
  await openGroup(page);
  await openGroup(page);
  await page.locator("#f-st-select").fill("uid,mail,memberOf");
  await page.locator("#f-st-select").blur();
  await page.locator("#f-st-documentName").fill("personen.csv");
  await page.locator("#f-st-documentName").blur();
  await expect(page.locator("#f-st-maxEntries")).toHaveAttribute("placeholder", "100000");
  await expect(page.locator(".pgroup[data-group*='Output' i]").first()).toContainText('split(row.memberOf, "[|]")');

  let task = taskXml(await page.evaluate(() => window.__xml()), "Activity_search");
  expect(task).toContain('select="uid,mail,memberOf"');
  expect(task).toContain('resultAs="document"');
  expect(task).toContain('documentName="personen.csv"');

  await page.locator("#f-st-resultAs").selectOption("");
  task = taskXml(await page.evaluate(() => window.__xml()), "Activity_search");
  expect(task).not.toContain('resultAs="document"');
  expect(task).not.toContain("documentName=");
  expect(task).toContain('select="uid,mail,memberOf"');
  expect(page.__errors).toEqual([]);
});

test("a SQL query writes its rows as a document, and another operation drops the form", async ({ page }) => {
  await page.evaluate(() => window.__selectQuery());
  await openGroup(page);
  await page.locator("#f-st-resultAs").selectOption("document");
  await openGroup(page);
  await page.locator("#f-st-documentName").fill("mitarbeitende.csv");
  await page.locator("#f-st-documentName").blur();
  await expect(page.locator("#f-st-maxRows")).toHaveAttribute("placeholder", "100000");

  let task = taskXml(await page.evaluate(() => window.__xml()), "Activity_query");
  expect(task).toContain('resultAs="document"');
  expect(task).toContain('documentName="mitarbeitende.csv"');

  await openGroup(page);
  await page.locator("#f-st-operation").selectOption("query-one");
  await expect(page.locator("#f-st-resultAs")).toHaveCount(0);
  task = taskXml(await page.evaluate(() => window.__xml()), "Activity_query");
  expect(task).not.toContain("resultAs=");
  expect(task).not.toContain("documentName=");
  expect(page.__errors).toEqual([]);
});
