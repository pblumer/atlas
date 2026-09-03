// End-to-end coverage for the mail worker's HTML body in the Modeler
// (api/web/editor.js, ADR-0079 amended). The field is the one place an author writes
// markup, so it must behave like the code field it is — highlighted, F2-able into the
// Developer View (ADR-0145) — and it must write back to the attribute the compiler
// reads, with the markup intact.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/mail-html-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await page.locator('[data-tab="implement"]').click();
  await page.evaluate(() => window.__select("Activity_mail"));
  // Property groups start collapsed (all but General), so open the one the message
  // fields live in before touching them.
  await page.locator(".pgroup-head", { hasText: "Message" }).click();
});

// mailXML returns the exported XML of the mail task.
async function mailXML(page) {
  const xml = await page.evaluate(() => window.__xml());
  return /<bpmn:serviceTask id="Activity_mail"[\s\S]*?<\/bpmn:serviceTask>/.exec(xml)[0];
}

test("the HTML body is read from the model and highlighted as markup", async ({ page }) => {
  await expect(page.locator("#f-st-bodyHtml")).toHaveValue("<p>Ihre Bestellung ist <b>unterwegs</b>.</p>");
  // It is a code editor, not a plain textarea: tags and attributes are coloured.
  const editor = page.locator(".code-editor", { has: page.locator("#f-st-bodyHtml") });
  await expect(editor.locator(".tok-html-tag").first()).toHaveText("p");
  // The plain-text body stays its own field — the two are alternatives, not one.
  await expect(page.locator("#f-st-body")).toHaveValue("Ihre Bestellung ist unterwegs.");
  expect(page.__errors).toEqual([]);
});

test("editing the HTML body writes it back to the worker extension", async ({ page }) => {
  await page.locator("#f-st-bodyHtml").fill('<p>Hallo <b>Anna</b></p>');
  await page.locator("#f-st-bodyHtml").blur();

  const task = await mailXML(page);
  expect(task).toMatch(/<atlas:mailConnector[^>]*bodyHtml="&#60;p&#62;Hallo &#60;b&#62;Anna&#60;\/b&#62;&#60;\/p&#62;"|bodyHtml="&lt;p&gt;Hallo &lt;b&gt;Anna&lt;\/b&gt;&lt;\/p&gt;"/);
  // The other message fields are untouched by the edit.
  expect(task).toMatch(/body="Ihre Bestellung ist unterwegs."/);
  expect(task).toMatch(/to="ops@example.com"/);
  expect(page.__errors).toEqual([]);
});

test("clearing the HTML body leaves a text-only message", async ({ page }) => {
  await page.locator("#f-st-bodyHtml").fill("");
  await page.locator("#f-st-bodyHtml").blur();

  const task = await mailXML(page);
  expect(task).not.toMatch(/bodyHtml="[^"]+"/);
  expect(task).toMatch(/body="Ihre Bestellung ist unterwegs."/);
});

test("F2 opens the HTML body in the Developer View", async ({ page }) => {
  await page.locator("#f-st-bodyHtml").focus();
  await page.keyboard.press("F2");

  await expect(page.locator(".dev-modal")).toBeVisible();
  await expect(page.locator(".dev-badge")).toHaveText("HTML");
  await expect(page.locator(".dev-title")).toHaveText("HTML body");
  // The markup language brings its own examples, and no FEEL function reference.
  await expect(page.locator(".dev-tab[data-tab='snips']")).toBeVisible();
  await expect(page.locator(".dev-tab[data-tab='fns']")).toBeHidden();

  // Applying writes through the field, so the panel's save wiring stores it.
  await page.locator(".dev-ta").fill("<p>Aus dem Developer View</p>");
  await page.locator(".dev-apply").click();
  await expect(page.locator(".dev-modal")).toHaveCount(0);
  await expect(page.locator("#f-st-bodyHtml")).toHaveValue("<p>Aus dem Developer View</p>");

  const task = await mailXML(page);
  expect(task).toMatch(/bodyHtml="(&#60;|&lt;)p(&#62;|&gt;)Aus dem Developer View/);
  expect(page.__errors).toEqual([]);
});
