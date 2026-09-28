// End-to-end coverage for the business rule task's binding field (api/web/editor.js,
// ADR-0423). Driven through the real
// vendored bpmn-js and the real properties panel.
//
// The field used to offer two bindings and describe one of them wrongly: "Latest —
// newest deployed version" froze the version when the process was deployed. It now
// offers the newest version when the task runs, a deployed version the author picks,
// and the model deployed with the process — and says which is which.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/decision-binding-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await page.locator('[data-tab="implement"]').click();
});

async function openBinding(page, id) {
  await page.evaluate((el) => window.__select(el), id);
  await page.locator(".pgroup-head", { hasText: "Called decision" }).click();
  await expect(page.locator("#f-brt-binding")).toBeVisible();
}

test("the binding field offers latest, a chosen version and the deployment, and says what each does", async ({ page }) => {
  await openBinding(page, "Activity_rule");
  const options = await page.locator("#f-brt-binding option").allTextContents();
  expect(options).toEqual([
    "Latest — newest version when the task runs",
    "Version — a deployed version you choose",
    "Deployment — the model deployed with this process",
  ]);
  await expect(page.locator(".pgroup", { hasText: "Called decision" })).toContainText("each time the task runs");
  await expect(page.locator("#f-brt-version-wrap")).toBeHidden();
  expect(page.__errors).toEqual([]);
});

test("choosing a version lists the deployed ones and writes atlas:version", async ({ page }) => {
  await openBinding(page, "Activity_rule");
  await page.locator("#f-brt-binding").selectOption("version");
  await expect(page.locator("#f-brt-version-wrap")).toBeVisible();
  await expect(page.locator("#f-brt-version option")).toHaveText([
    "— choose a deployed version —",
    "v2 — current",
    "v1",
  ]);
  await page.locator("#f-brt-version").selectOption("1");
  await expect.poll(() => page.evaluate(() => window.__calledDecision("Activity_rule")))
    .toContain('atlas:version="1"');
  const cd = await page.evaluate(() => window.__calledDecision("Activity_rule"));
  // Beside it the bindingType a Camunda engine reads stays latest — the default,
  // which the modeler writes as no attribute at all.
  expect(cd).not.toMatch(/bindingType="(deployment|versionTag)"/);

  // Back to latest removes the version rather than leaving it to be read.
  await page.locator("#f-brt-binding").selectOption("latest");
  await expect.poll(() => page.evaluate(() => window.__calledDecision("Activity_rule")))
    .not.toContain("atlas:version");
  expect(page.__errors).toEqual([]);
});

test("a binding the panel does not offer is shown and kept, not rewritten to latest", async ({ page }) => {
  await openBinding(page, "Activity_tag");
  await expect(page.locator("#f-brt-binding")).toHaveValue("versionTag");
  await expect(page.locator("#f-brt-binding option:checked")).toHaveText("versionTag — not supported, the deploy refuses it");
  // Any save of the called decision used to write bindingType="latest" back.
  await page.locator("#f-resultvar").fill("tarif2");
  await page.locator("#f-resultvar").dispatchEvent("change");
  await expect.poll(() => page.evaluate(() => window.__calledDecision("Activity_tag")))
    .toContain('resultVariable="tarif2"');
  const cd = await page.evaluate(() => window.__calledDecision("Activity_tag"));
  expect(cd).toContain('bindingType="versionTag"');
  expect(page.__errors).toEqual([]);
});

test("a deployment whose latest was frozen when it was deployed says so beside its name", async ({ page }) => {
  // The binding field reads what a deploy from here will do; this deployment does
  // something else, and Deploy on this bar is the remedy.
  const mark = page.locator(".editor-bar .crumbs .frozen-mark");
  await expect(mark).toBeVisible();
  await expect(mark).toHaveText("Decision frozen · newer deployed");
  await expect(mark).toHaveAttribute("title", /eligibility runs v1, the newest is v2/);
  expect(page.__errors).toEqual([]);
});
