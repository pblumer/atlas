// End-to-end coverage for mail templates in the Modeler (api/web/editor.js,
// dev-view.js, dev-lang.js; ADR-0487). With placeholders on, a mail
// task's subject and bodies are templates in the language form text views use: the
// fields colour the {{ }} tags, F2 opens them in the Developer View, and the preview
// renders them with sample variables through POST /api/v1/feel/template — a text
// body as text, an HTML body as a sandboxed page.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/mail-html-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await page.locator('[data-tab="implement"]').click();
});

async function openMessage(page, id) {
  await page.evaluate((el) => window.__select(el), id);
  await page.locator('.pgroup[data-group*="Message" i] > .pgroup-head').click();
}

async function taskXML(page, id) {
  const xml = await page.evaluate(() => window.__xml());
  return new RegExp(`<bpmn:serviceTask id="${id}"[\\s\\S]*?</bpmn:serviceTask>`).exec(xml)[0];
}

test("switching placeholders on writes the attribute and makes the bodies templates", async ({ page }) => {
  await openMessage(page, "Activity_mail");
  await expect(page.locator("#f-st-templating")).toHaveValue("");
  // Off, the body is a plain literal: no code editor, F2 inert until fx.
  await expect(page.locator(".code-editor", { has: page.locator("#f-st-body") })).toHaveCount(0);

  await page.locator("#f-st-templating").selectOption("true");
  await expect.poll(async () => /templating="true"/.test(await taskXML(page, "Activity_mail"))).toBe(true);

  // The panel re-rendered: the body is a template field now, and colours its tags.
  const body = page.locator("#f-st-body");
  await body.fill("Hallo {{kunde.name}}");
  await expect(page.locator(".code-editor", { has: body }).locator(".tok-tpl-tag").first()).toHaveText("{{");
  await expect(body).toHaveAttribute("data-devlang", "template");
  await expect(page.locator("#f-st-bodyHtml")).toHaveAttribute("data-devlang", "html-template");
  expect(page.__errors).toEqual([]);
});

test("F2 on a template body opens the template editor with a live preview", async ({ page }) => {
  await openMessage(page, "Activity_tpl");
  await page.locator("#f-st-body").focus();
  await page.keyboard.press("F2");

  await expect(page.locator(".dev-modal")).toBeVisible();
  await expect(page.locator(".dev-badge")).toHaveText("Template");
  // The preview is open from the start: reading the result is the point.
  await expect(page.locator(".dev-test-toggle")).toHaveText("Preview");
  await expect(page.locator(".dev-preview")).toBeVisible();
  // The template language's tags lead the reference, FEEL's functions follow.
  await expect(page.locator(".dev-tab[data-tab='fns']")).toBeVisible();

  await page.locator(".dev-run-vars").fill('{ "kunde": { "name": "Ada" } }');
  await expect(page.locator(".dev-preview-text")).toHaveText("Hallo Ada");
  await expect(page.locator(".dev-run-out")).toHaveText("✓ rendered");

  // Editing the template renders again, and a missing value is pointed out.
  await page.locator(".dev-ta").fill("Hallo {{kunde.name}}, {{kunde.firma}}");
  await expect(page.locator(".dev-preview-text")).toHaveText("Hallo Ada, ");
  await expect(page.locator(".dev-preview-problems")).toContainText("kunde.firma");
  await expect(page.locator(".dev-run-out")).toContainText("1 placeholder rendered as nothing");

  const last = await page.evaluate(() => window.__templateCalls.at(-1));
  expect(last.html).toBe(false);
  expect(last.variables).toEqual({ kunde: { name: "Ada" } });

  // Applying writes through the field.
  await page.locator(".dev-apply").click();
  await expect(page.locator("#f-st-body")).toHaveValue("Hallo {{kunde.name}}, {{kunde.firma}}");
  expect(await taskXML(page, "Activity_tpl")).toMatch(/body="Hallo \{\{kunde\.name\}\}, \{\{kunde\.firma\}\}"/);
  expect(page.__errors).toEqual([]);
});

test("an HTML template is previewed as a sandboxed page with its values escaped", async ({ page }) => {
  await openMessage(page, "Activity_tpl");
  await page.locator("#f-st-bodyHtml").focus();
  await page.keyboard.press("F2");

  await expect(page.locator(".dev-badge")).toHaveText("HTML template");
  const frame = page.locator("iframe.dev-preview-html");
  // No scripts and no same-origin: the markup cannot reach the Modeler.
  await expect(frame).toHaveAttribute("sandbox", "");
  await page.locator(".dev-run-vars").fill('{ "kunde": { "name": "<i>Ada</i>" } }');
  await expect.poll(() => frame.evaluate((f) => f.srcdoc)).toBe("<p>Hallo <b>&lt;i&gt;Ada&lt;/i&gt;</b></p>");
  await expect(page.frameLocator("iframe.dev-preview-html").locator("b")).toHaveText("<i>Ada</i>");

  const last = await page.evaluate(() => window.__templateCalls.at(-1));
  expect(last.html).toBe(true);
  expect(page.__errors).toEqual([]);
});

test("a template that does not compile says where, and keeps the last rendering", async ({ page }) => {
  await openMessage(page, "Activity_tpl");
  await page.locator("#f-st-body").focus();
  await page.keyboard.press("F2");
  await page.locator(".dev-run-vars").fill('{ "kunde": { "name": "Ada" } }');
  await expect(page.locator(".dev-preview-text")).toHaveText("Hallo Ada");

  await page.locator(".dev-ta").fill("{{#if kunde.vip}}Sehr geehrte");
  await expect(page.locator(".dev-run-out")).toHaveText("✗ line 1, column 1");
  await expect(page.locator(".dev-run-detail")).toContainText("never closed with {{/if}}");
  await expect(page.locator(".dev-preview-out")).toHaveClass(/stale/);
  await expect(page.locator(".dev-preview-text")).toHaveText("Hallo Ada");

  await page.locator(".dev-run-vars").fill("[1, 2]");
  await expect(page.locator(".dev-run-out")).toHaveText("Sample variables must be a JSON object.");
  expect(page.__errors).toEqual([]);
});

test("an HTML body without placeholders is its own preview", async ({ page }) => {
  await openMessage(page, "Activity_mail");
  await page.locator("#f-st-bodyHtml").focus();
  await page.keyboard.press("F2");
  await expect(page.locator(".dev-badge")).toHaveText("HTML");
  const frame = page.locator("iframe.dev-preview-html");
  await expect.poll(() => frame.evaluate((f) => f.srcdoc)).toBe("<p>Ihre Bestellung ist <b>unterwegs</b>.</p>");
  // Nothing to render on the server: the markup is the message.
  expect(await page.evaluate(() => window.__templateCalls.length)).toBe(0);
  expect(page.__errors).toEqual([]);
});

test("an operation that sends nothing drops the placeholders switch", async ({ page }) => {
  await openMessage(page, "Activity_tpl");
  await page.locator('.pgroup[data-group*="Mail worker" i] > .pgroup-head').click();
  await page.locator("#f-st-operation").selectOption("mark-read");
  await expect.poll(async () => /templating=/.test(await taskXML(page, "Activity_tpl"))).toBe(false);
  // A reply writes a text again, and offers the switch again.
  await page.locator("#f-st-operation").selectOption("reply");
  await expect(page.locator("#f-st-templating")).toBeVisible();
  expect(page.__errors).toEqual([]);
});

// A get may read the HTML body as well; a list may not (ADR-0489). The
// panel offers it where it is allowed and takes it back when a get becomes a list.
test("Read message offers the HTML body, and a list takes it back", async ({ page }) => {
  await openMessage(page, "Activity_mail");
  await page.locator('.pgroup[data-group*="Mail worker" i] > .pgroup-head').click();
  await page.locator("#f-st-operation").selectOption("get");
  await page.locator('.pgroup[data-group*="Mailbox" i] > .pgroup-head').click();
  const text = page.locator("#f-st-includeBody");
  await expect(text.locator("option")).toHaveText(["Nur Umschlag", "Mit Text", "Mit Text und HTML"]);
  await text.selectOption("html");
  await expect.poll(async () => /includeBody="html"/.test(await taskXML(page, "Activity_mail"))).toBe(true);

  await page.locator("#f-st-operation").selectOption("list");
  await expect.poll(async () => /includeBody="true"/.test(await taskXML(page, "Activity_mail"))).toBe(true);
  await expect(page.locator("#f-st-includeBody option")).toHaveText(["Nur Umschlag", "Mit Text"]);
  expect(page.__errors).toEqual([]);
});
