// End-to-end coverage for the e-mail view (ADR-0489): the form
// component that shows a mail a mail task read. It runs on the real vendored form-js
// viewer (mail-view-harness.html) and in the real form editor (form-editor-harness.html).
// What it must hold: the HTML is rendered in a sandboxed frame that runs no script and
// loads no remote image until the reader asks; the envelope is shown as text; and the
// form never submits the mail back.
import { test, expect } from "@playwright/test";

test.use({ viewport: { width: 1100, height: 900 } });

async function mount(page, data, opts) {
  await page.evaluate(([d, o]) => window.__mount(d, o), [data, opts || {}]);
  await page.waitForFunction(() => window.__mounted === true);
}

test.describe("in a task form", () => {
  test.beforeEach(async ({ page }) => {
    page.__errors = [];
    page.on("pageerror", (e) => page.__errors.push(e.message));
    // Every request for the mail's remote images, so a test can tell whether the frame
    // fetched them.
    page.__remote = [];
    await page.route("https://img.example/**", (route) => {
      page.__remote.push(route.request().url());
      return route.fulfill({ status: 200, contentType: "image/png", body: Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII=", "base64") });
    });
    await page.goto("/mail-view-harness.html");
    await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  });

  test.afterEach(async ({ page }) => {
    expect(page.__errors).toEqual([]);
  });

  test("the envelope is shown as text", async ({ page }) => {
    await mount(page, { mail: await page.evaluate(() => window.__mail) });
    const view = page.locator(".atlas-mailview");
    await expect(view.locator(".fjs-form-field-label")).toHaveText("Eingegangene E-Mail");
    // A subject with angle brackets is text, never markup.
    await expect(view.locator(".atlas-mailview-subject")).toHaveText("Rechnung 42 <dringend>");
    const head = view.locator(".atlas-mailview-head dl");
    await expect(head).toContainText("Von");
    await expect(head).toContainText("Anna Muster <anna@example.com>");
    await expect(head).toContainText("support@example.com");
    await expect(head).toContainText("team@example.com");
    await expect(view.locator(".atlas-mailview-att")).toHaveText("Rechnung-42.pdf · 51 KB");
  });

  test("the HTML is sandboxed and its remote images stay blocked until asked for", async ({ page }) => {
    await mount(page, { mail: await page.evaluate(() => window.__mail) });
    const frame = page.locator("iframe.atlas-mailview-frame");
    await expect(frame).toHaveAttribute("sandbox", "allow-popups allow-popups-to-escape-sandbox");
    await expect(frame).toHaveAttribute("referrerpolicy", "no-referrer");
    await expect(page.frameLocator("iframe.atlas-mailview-frame").locator("#hello")).toHaveText("Guten Tag Support");
    const srcdoc = () => frame.evaluate((f) => f.srcdoc);
    expect(await srcdoc()).toContain("img-src data:;");
    // Give the frame every chance to fetch: it must not.
    await page.waitForTimeout(500);
    expect(page.__remote).toEqual([]);

    const banner = page.locator(".atlas-mailview-blocked");
    await expect(banner).toContainText("Externe Bilder sind blockiert");
    await banner.locator("button").click();
    await expect(banner).toHaveCount(0);
    expect(await srcdoc()).toContain("img-src data: https: http:");
    await expect.poll(() => page.__remote.some((u) => u.endsWith("/logo.png"))).toBe(true);
    await expect.poll(() => page.__remote.some((u) => u.endsWith("/bg.png"))).toBe(true);
  });

  test("a script in the mail does not run, even one the server missed", async ({ page }) => {
    const mail = await page.evaluate(() => window.__mail);
    mail.html = '<p id="p">x</p><script>parent.__pwned = 1; document.getElementById("p").textContent = "ran";</script>' +
      '<img src="x" onerror="parent.__pwned = 2">';
    await mount(page, { mail });
    await expect(page.frameLocator("iframe.atlas-mailview-frame").locator("#p")).toHaveText("x");
    await page.waitForTimeout(300);
    expect(await page.evaluate(() => window.__pwned)).toBeUndefined();
  });

  test("the text version is one click away", async ({ page }) => {
    await mount(page, { mail: await page.evaluate(() => window.__mail) });
    const tabs = page.locator(".atlas-mailview-tabs button");
    await expect(tabs).toHaveText(["HTML", "Text"]);
    await tabs.nth(1).click();
    await expect(page.locator(".atlas-mailview-text")).toHaveText("Guten Tag\nAnbei die Rechnung.");
    await expect(page.locator("iframe.atlas-mailview-frame")).toHaveCount(0);
    await tabs.nth(0).click();
    await expect(page.locator("iframe.atlas-mailview-frame")).toHaveCount(1);
  });

  test("completing the task never sends the mail back", async ({ page }) => {
    await mount(page, { mail: await page.evaluate(() => window.__mail), note: "" });
    const note = page.locator(".fjs-form-field-textfield input").first();
    await note.fill("geprüft");
    await note.press("Tab"); // form-js takes a text field's value on blur
    // The submitted data is the note and nothing else: no mail, no html.
    await expect.poll(() => page.evaluate(() => window.__submit())).toEqual({ note: "geprüft" });
  });

  test("what a mail without HTML, without content, or without a value shows", async ({ page }) => {
    await mount(page, { mail: { from: "a@example.com", subject: "Nur Text", body: "Hallo", bodyTruncated: true } });
    await expect(page.locator(".atlas-mailview-tabs")).toHaveCount(0);
    await expect(page.locator(".atlas-mailview-text")).toHaveText("Hallo");
    await expect(page.locator(".atlas-mailview-cut")).toHaveText("Der Text ist gekürzt.");

    await mount(page, { mail: { from: "a@example.com", subject: "Umschlag" } });
    await expect(page.locator(".atlas-mailview-empty")).toHaveText("Die E-Mail wurde ohne ihren Inhalt gelesen.");

    await mount(page, {});
    await expect(page.locator(".atlas-mailview-empty")).toHaveText("Keine E-Mail in «mail».");

    await mount(page, { mail: "Ein Text in einer Variable" });
    await expect(page.locator(".atlas-mailview-text")).toHaveText("Ein Text in einer Variable");
  });

  test("the view speaks the page's language", async ({ page }) => {
    await mount(page, { mail: await page.evaluate(() => window.__mail) }, { locale: "en" });
    await expect(page.locator(".atlas-mailview-head dl")).toContainText("From");
    await expect(page.locator(".atlas-mailview-blocked button")).toHaveText("Load images");
  });

  test("a mail view inside a group reads the group's path", async ({ page }) => {
    const mail = await page.evaluate(() => window.__mail);
    const schema = { type: "default", components: [
      { type: "group", id: "g", path: "anfrage", components: [{ type: "mailview", id: "m", key: "mail", label: "Mail" }] },
    ] };
    await mount(page, { anfrage: { mail } }, { schema });
    await expect(page.locator(".atlas-mailview-subject")).toHaveText("Rechnung 42 <dringend>");
  });
});

test.describe("in the form editor", () => {
  test.use({ viewport: { width: 1500, height: 1000 } });

  test("the view is in the palette and names its variable in the properties", async ({ page }) => {
    const errors = [];
    page.on("pageerror", (e) => errors.push(e.message));
    await page.goto("/form-editor-harness.html");
    await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
    await page.evaluate(() => window.__mountMailView());
    const canvasField = page.locator('.fjs-editor-container [data-id="Field_mail"]').first();
    await expect(canvasField).toBeVisible({ timeout: 25000 });
    await expect(page.locator('.fjs-palette-field[data-field-type="mailview"]')).toBeVisible();
    await expect(canvasField.locator(".atlas-mailview-empty")).toHaveText("Keine E-Mail in «mail».");

    await canvasField.click();
    const props = page.locator(".fjs-properties-panel");
    await expect(props).toHaveAttribute("data-field", "Field_mail");
    const general = props.locator('[data-group-id="group-general"]');
    const entries = general.locator(".bio-properties-panel-group-entries");
    if (!(await entries.evaluate((el) => el.classList.contains("open")))) {
      await general.locator(".bio-properties-panel-group-header").click();
    }
    await expect(props.locator("#bio-properties-panel-atlasMail-label")).toHaveValue("E-Mail");
    const key = props.locator("#bio-properties-panel-atlasMail-key");
    await expect(key).toHaveValue("mail");
    await key.fill("anfrage mail");
    await expect(props.locator('[data-entry-id="atlasMail-key"]')).toHaveClass(/has-error/);
    await key.fill("anfrage.mail");
    await key.press("Enter");
    await expect(canvasField.locator(".atlas-mailview-empty")).toHaveText("Keine E-Mail in «anfrage.mail».");

    await page.evaluate(() => window.__save());
    await expect.poll(() => page.evaluate(() => window.__saved && window.__saved.schema)).toBeTruthy();
    const saved = await page.evaluate(() => window.__saved.schema);
    const comp = (typeof saved === "string" ? JSON.parse(saved) : saved).components.find((c) => c.id === "Field_mail");
    expect(comp).toMatchObject({ type: "mailview", key: "anfrage.mail" });
    expect(errors).toEqual([]);
  });
});
