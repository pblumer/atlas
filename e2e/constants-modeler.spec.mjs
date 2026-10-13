// End-to-end coverage for process constants in the Modeler (api/web/editor.js,
// ADR-draft-process-constants): the values every instance starts with and nobody is asked
// for. The process panel's Implement tab edits them beside the start variables, the
// Variables panel offers them like any variable, and a model carrying them survives the
// Modeler's round trip.
//
// The assertions read the exported XML, because that is what a deploy sends. Without the
// moddle types (api/web/atlas-moddle.json) the round trip fails: bpmn-js drops an
// extension element it cannot parse, and a Save would strip every constant.
import { test, expect } from "@playwright/test";

test.use({ viewport: { width: 1600, height: 900 } });

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/constants-modeler-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
});

// openConstants mounts the editor on one of the harness's two models, switches to the
// Implement tab, shows the process itself (nothing selected) and opens its Constants group.
async function openConstants(page, kind) {
  await page.evaluate((k) => window.__mount(k), kind || "declared");
  await expect(page.locator("#p-body")).toBeVisible();
  await page.locator('[data-tab="implement"]').click();
  await page.evaluate(() => window.__deselect());
  const group = page.locator('.pgroup[data-group="Constants"]');
  await expect(group).toHaveCount(1);
  if (await group.evaluate((g) => g.classList.contains("collapsed"))) {
    await group.locator(".pgroup-head").click();
  }
  await expect(page.locator("#cv-add")).toBeVisible();
}

const constantsXML = async (page) => {
  const xml = await page.evaluate(() => window.__xml());
  return /<atlas:constants>[\s\S]*?<\/atlas:constants>/.exec(xml)?.[0] || "";
};

test("the Implement tab lists the process's constants, without a required mark", async ({ page }) => {
  await openConstants(page, "declared");
  const rows = page.locator("#cv-list .sv-row");
  await expect(rows).toHaveCount(2);
  await expect(rows.nth(0).locator(".sv-name")).toHaveValue("DomainSuffix");
  await expect(rows.nth(0).locator(".sv-default")).toHaveValue("contoso.ch");
  await expect(rows.nth(1).locator(".sv-name")).toHaveValue("MaxAgents");
  await expect(rows.nth(1).locator(".sv-type")).toHaveValue("number");
  await expect(rows.nth(1).locator(".sv-default")).toHaveValue("20");
  // Nobody supplies a constant, so there is nothing for "required" to mean.
  await expect(page.locator("#cv-list .sv-required")).toHaveCount(0);
  // The start variables are still their own list.
  await expect(page.locator("#sv-list .sv-row")).toHaveCount(1);
  expect(page.__errors).toEqual([]);
});

test("a model carrying constants survives the round trip untouched", async ({ page }) => {
  await openConstants(page, "declared");
  const xml = await constantsXML(page);
  expect(xml).toContain('<atlas:constant name="DomainSuffix" type="string" value="contoso.ch" />');
  expect(xml).toContain('<atlas:constant name="MaxAgents" type="number" value="20" />');
  expect(page.__errors).toEqual([]);
});

test("a constant added in the panel reaches the deployed XML, beside the start variables", async ({ page }) => {
  await openConstants(page, "plain");
  expect(await constantsXML(page)).toBe("");

  await page.locator("#cv-add").click();
  const row = page.locator("#cv-list .sv-row").last();
  await row.locator(".sv-name").fill("TenantId");
  await row.locator(".sv-default").fill("tenant-dev");
  await row.locator(".sv-default").blur();

  await expect.poll(() => constantsXML(page)).toContain('<atlas:constant name="TenantId" type="string" value="tenant-dev" />');
  const xml = await page.evaluate(() => window.__xml());
  expect(xml).toContain('<atlas:startVariable name="Name" type="string" required="true" />');

  // Removing the last constant removes the element rather than leaving an empty one.
  await row.locator(".sv-del").click();
  await expect.poll(() => constantsXML(page)).toBe("");
  expect(page.__errors).toEqual([]);
});

test("the Variables panel offers a constant like any variable", async ({ page }) => {
  await openConstants(page, "declared");
  await page.locator("#vars-toggle").click();
  const row = page.locator('#vars-list .var-row[data-var="DomainSuffix"]');
  await expect(row).toBeVisible();
  await expect(row.locator(".var-meta")).toContainText("constant");
  await expect(page.locator('#vars-list .var-row[data-var="MaxAgents"] .vtag')).toHaveText("number");
  expect(page.__errors).toEqual([]);
});
