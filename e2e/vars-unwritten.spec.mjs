// End-to-end coverage for the Modeler's warning about a variable the model reads and
// nothing in it writes (api/web/editor.js unwrittenReads), in the Problems bar and in
// the deploy panel.
//
// The compiler cannot give this warning: it knows a linked form by its id and nothing of
// the process a call activity calls, so every form field and every variable a child
// hands back would read as unwritten. The Modeler has both, from the Variables panel's
// own reading of the diagram, and says it beside the compiler's findings. It is a
// warning and never more: a value can come from outside the model — a message's payload,
// a start that is not declared — so the deploy goes ahead.
//
// The diagram is the Variables panel's (vars-scope-harness.html). One task reads
// `betrag`, which nothing writes. Everything else it reads is written somewhere the
// compiler does not see: the called process's `kindergebnis`, a multi-instance round's
// item, an input mapping's local, an agent tool's parameter and its toolCallId.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/vars-scope-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
});

const unwritten = (page) => page.locator("#prob-list .prob-row", {
  has: page.locator(".prob-rule", { hasText: /^variable\.unwritten$/ }),
});

test("a name read and written nowhere is a warning on the element that reads it", async ({ page }) => {
  await expect(page.locator("#problems-bar")).toHaveClass(/has-warnings/);
  await expect(unwritten(page)).toHaveCount(1);
  const row = unwritten(page).first();
  await expect(row).toHaveAttribute("data-el", "pruefen");
  await expect(row).toHaveClass(/prob-warning/);
  await expect(row.locator(".prob-msg")).toContainText("betrag");
  // Nothing the compiler cannot see is reported: the child's variables, the round's
  // item, the input mapping's local, the agent tool's parameter and call id.
  for (const n of ["kindergebnis", "pos", "limite", "suchbegriff", "toolCallId"]) {
    await expect(page.locator("#prob-list .prob-msg", { hasText: n }), n).toHaveCount(0);
  }
  expect(page.__errors).toEqual([]);
});

test("the warning goes once the name is no longer read", async ({ page }) => {
  await expect(unwritten(page)).toHaveCount(1);
  await page.evaluate(() => {
    const m = window.__atlasModeler;
    const el = m.get("elementRegistry").get("pruefen");
    const script = el.businessObject.extensionElements.values.find((v) => v.$type === "zeebe:Script");
    m.get("modeling").updateModdleProperties(el, script, { expression: "=kundennr > limite" });
  });
  await expect(unwritten(page)).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("the deploy panel names what is read and unwritten, and still deploys", async ({ page }) => {
  await expect(unwritten(page)).toHaveCount(1);
  await page.locator("#deploy").click();
  const note = page.locator("#deploy-unwritten");
  await expect(note).toBeVisible();
  await expect(note).toContainText("betrag");
  await expect(note).toContainText("Prüfen");
  await expect(page.locator("#deploy-only")).toBeEnabled();
  await expect(page.locator("#deploy-run")).toBeEnabled();
  expect(page.__errors).toEqual([]);
});

test("the agent tool's parameter and call id are its own variables", async ({ page }) => {
  await page.locator("#vars-toggle").click();
  await page.evaluate(() => window.__select("suchen"));
  await expect(page.locator('#vars-list .vars-group[data-cat="@local"] .var-row[data-var="suchbegriff"]')).toBeVisible();
  await expect(page.locator('#vars-list .vars-group[data-cat="@local"] .var-row[data-var="toolCallId"]')).toBeVisible();
  await expect(page.locator('#vars-list .var-row[data-unwritten="1"]')).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});
