// End-to-end coverage for "Generate this form" (api/web/formgen-dialog.js,
// ADR-draft-ai-form-generation).
//
// The dialog is where the two halves of the feature meet: what the author types, and
// what the process already says about itself. The server's half is tested in
// api/formgen; what only a browser can show is that the request carries the process and
// the step somebody picked, that a refusal from the model leaves the brief on screen
// instead of closing over it, and that the affordance is absent — not broken — where no
// AI Worker is configured.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/formgen-dialog-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
});

const result = (page) => page.locator("#result");
const sent = (page) => page.locator("#sent");

test("a brief alone generates a form", async ({ page }) => {
  await page.locator("#open").click();
  await page.locator("#fg-brief").fill("Ein Antrag auf Sonderurlaub mit Grund und Zeitraum.");
  await page.locator("[data-ok]").click();

  await expect(sent(page)).toHaveText(/"description":"Ein Antrag auf Sonderurlaub/);
  // The form's identity travels with the request: the editor is holding this form
  // under an id a user task may already bind.
  await expect(sent(page)).toHaveText(/"formId":"urlaub-pruefen"/);
  await expect(result(page)).toHaveText(/"key":"grund"/);
  expect(page.__errors).toEqual([]);
});

test("the process and the step it is for reach the request", async ({ page }) => {
  await page.locator("#open").click();

  // Drafts and deployed processes in one picker, and a process that is both appears
  // once — the server reads the draft in that case.
  const proc = page.locator("#fg-process");
  await expect(proc.locator("option")).toHaveCount(4); // none + 2 drafts + 1 deployment
  await expect(page.locator("#fg-step-field")).toBeHidden();

  await proc.selectOption("urlaubsantrag");
  const step = page.locator("#fg-step");
  await expect(page.locator("#fg-step-field")).toBeVisible();
  // The user tasks of the model, plus the start-form entry that is the default.
  await expect(step.locator("option")).toHaveCount(3);
  await expect(step.locator("option").first()).toHaveText(/Start form/);

  await step.selectOption("Task_Pruefen");
  await page.locator("#fg-brief").fill("Die Führungskraft entscheidet.");
  await page.locator("[data-ok]").click();

  await expect(sent(page)).toHaveText(/"processId":"urlaubsantrag"/);
  await expect(sent(page)).toHaveText(/"elementId":"Task_Pruefen"/);
});

test("naming no process is allowed, and a request with nothing in it is not", async ({ page }) => {
  await page.locator("#open").click();
  await page.locator("[data-ok]").click();

  // Nothing was sent and the dialog is still open, with the reason on it.
  await expect(sent(page)).toHaveText("—");
  await expect(page.locator("#fg-err")).toContainText(/Say what the form is for/);
  await expect(page.locator("#fg-brief")).toBeVisible();

  // Picking a process is a complete request on its own: "the form that starts this".
  await page.locator("#fg-process").selectOption("reisekosten");
  await page.locator("[data-ok]").click();
  await expect(sent(page)).toHaveText(/"processId":"reisekosten"/);
});

test("refining sends the form that is open, and turning it off does not", async ({ page }) => {
  await page.locator("#open").click();
  await page.locator("#fg-brief").fill("Füge ein Feld für den Zeitraum hinzu.");
  await expect(page.locator("#fg-refine")).toBeChecked();
  await page.locator("[data-ok]").click();
  await expect(sent(page)).toHaveText(/"schema":\{.*"key":"alt"/);

  await page.locator("#open").click();
  await page.locator("#fg-brief").fill("Ganz von vorne.");
  await page.locator("#fg-refine").uncheck();
  await page.locator("[data-ok]").click();
  await expect(sent(page)).not.toHaveText(/"schema"/);
});

test("with two Workers the author picks one, with one there is nothing to pick", async ({ page }) => {
  await page.locator("#open").click();
  // One Worker: no picker, but the dialog still says who is going to write the form.
  await expect(page.locator("#fg-worker")).toHaveCount(0);
  await expect(page.locator("#fg-worker-note")).toContainText("haus");
  await page.keyboard.press("Escape");

  await page.locator("#open-many").click();
  await page.locator("#fg-worker").selectOption("lokal");
  await page.locator("#fg-brief").fill("Ein Formular.");
  await page.locator("[data-ok]").click();
  await expect(sent(page)).toHaveText(/"worker":"lokal"/);
  await expect(result(page)).toHaveText(/"worker":"lokal"/);
});

test("a refusal from the model stays in the dialog, with the brief still in it", async ({ page }) => {
  await page.locator("#open-fails").click();
  await page.locator("#fg-brief").fill("Etwas, das das Modell nicht schreiben will.");
  await page.locator("[data-ok]").click();

  // The reason the model gave, verbatim: the author decides whether to rephrase or
  // simply try again, and can do either without retyping.
  await expect(page.locator("#fg-err")).toContainText(/no JSON object/);
  await expect(page.locator("#fg-brief")).toHaveValue(/nicht schreiben will/);
  await expect(page.locator("[data-ok]")).toHaveText("Generate");
  await expect(page.locator("[data-ok]")).toBeEnabled();
  // Nothing was resolved: a failed generation is not an answer.
  await expect(result(page)).toHaveText("—");
});

test("Escape and Cancel resolve to nothing", async ({ page }) => {
  await page.locator("#open").click();
  await page.keyboard.press("Escape");
  await expect(result(page)).toHaveText("cancelled");

  await page.locator("#open").click();
  await page.locator("[data-cancel]").click();
  await expect(result(page)).toHaveText("cancelled");
});

test("the capability probe decides whether the button exists at all", async ({ page }) => {
  // Configured: the Workers come back and the editor shows its button.
  expect(await page.evaluate(() => window.__workers)).toEqual([{ name: "haus", model: "claude-opus-5" }]);
  // A server that does not know the route — or has no AI Worker — answers with
  // nothing, and the editor leaves the button hidden rather than offering a failure.
  expect(await page.evaluate(() => window.__noWorkers)).toEqual([]);
});
