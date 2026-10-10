// End-to-end coverage for the subprocess's Wizard section and badge in the Modeler
// (api/web/editor.js, ADR-0449). atlas:wizard marks an embedded subprocess as one sitting
// of one person, walked screen by screen. Driven through the real vendored bpmn-js: the
// panel reads the mark a model carries, writing it reaches the exported XML, and the badge
// on the shape follows the property. Without the SubProcessMeta property in
// api/web/atlas-moddle.json the panel can neither read nor write the attribute.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/wizard-modeler-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await page.locator('[data-tab="implement"]').click();
});

// openWizard selects a subprocess and expands its Wizard group (groups but General start
// collapsed), so the field is on screen.
async function openWizard(page, id) {
  await page.evaluate((el) => window.__select(el), id);
  await page.locator(".pgroup-head", { hasText: "Wizard" }).click();
  await expect(page.locator("#f-wizard")).toBeVisible();
}

// subTag returns the exported start tag of one subprocess, so an assertion sees the
// attribute on that element and nowhere else.
async function subTag(page, id) {
  const xml = await page.evaluate(() => window.__xml());
  return new RegExp(`<bpmn:subProcess id="${id}"[^>]*>`).exec(xml)?.[0] || "";
}

const badge = (page, id) => page.locator(`.djs-overlays[data-container-id="${id}"] .wizard-badge`);

test("the panel reads the mark a subprocess carries", async ({ page }) => {
  await openWizard(page, "Sub_wizard");
  await expect(page.locator("#f-wizard")).toHaveValue("internal");

  await page.evaluate(() => window.__select("Sub_public"));
  await expect(page.locator("#f-wizard")).toHaveValue("public");

  await page.evaluate(() => window.__select("Sub_plain"));
  await expect(page.locator("#f-wizard")).toHaveValue("");
  expect(page.__errors).toEqual([]);
});

test("choosing a kind writes atlas:wizard, and No removes it", async ({ page }) => {
  await openWizard(page, "Sub_plain");
  await page.locator("#f-wizard").selectOption("public");
  expect(await subTag(page, "Sub_plain")).toContain('atlas:wizard="public"');
  await expect(badge(page, "Sub_plain")).toHaveCount(1);

  await page.locator("#f-wizard").selectOption("");
  expect(await subTag(page, "Sub_plain")).not.toContain("atlas:wizard");
  await expect(badge(page, "Sub_plain")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

// A value the panel does not know is shown as it stands and left alone: replacing it with
// "No" on sight would change the model under an author who only looked.
test("a value atlas does not know is shown and kept", async ({ page }) => {
  await openWizard(page, "Sub_odd");
  await expect(page.locator("#f-wizard")).toHaveValue("yes");
  expect(await subTag(page, "Sub_odd")).toContain('atlas:wizard="yes"');
  await expect(badge(page, "Sub_odd")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("an event subprocess is not offered the mark", async ({ page }) => {
  await page.evaluate(() => window.__select("Sub_event"));
  await expect(page.locator(".pgroup-head", { hasText: "Wizard" })).toHaveCount(0);
  await expect(page.locator("#f-wizard")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

// The badge is drawn where the implementation badges are: the Implement tab, never the
// Design view, which shows plain BPMN only.
test("the badge marks the wizards on the Implement tab only", async ({ page }) => {
  await expect(badge(page, "Sub_wizard")).toHaveCount(1);
  await expect(badge(page, "Sub_public")).toHaveCount(1);
  await expect(badge(page, "Sub_plain")).toHaveCount(0);
  await expect(badge(page, "Sub_event")).toHaveCount(0);
  await expect(badge(page, "Sub_public")).toHaveAttribute("title", /anonymous/);

  await page.locator('[data-tab="design"]').click();
  await expect(page.locator(".wizard-badge")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("a mark nobody touches survives the round trip", async ({ page }) => {
  expect(await subTag(page, "Sub_wizard")).toContain('atlas:wizard="internal"');
  expect(await subTag(page, "Sub_public")).toContain('atlas:wizard="public"');
  expect(await subTag(page, "Sub_plain")).not.toContain("atlas:wizard");
  expect(page.__errors).toEqual([]);
});
