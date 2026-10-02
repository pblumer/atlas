// End-to-end coverage for the FEEL assistant (api/web/feel-assistant.js,
// ADR-draft-feel-assistant).
//
// The server's half — the prompt, the engine's check, the correction rounds — is tested
// in api/feelgen. What only a browser can show is the other half: that the shortcut and
// the sparks open it from wherever the author is, that the conversation reaches the
// request whole, that a checked proposal lands in the editor with its verdict beside it,
// that "Apply" writes into the field it was opened from through that field's own events,
// and that the history and the favourites are still there after it was closed.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/feel-assistant-harness.html");
  await page.evaluate(() => { localStorage.clear(); sessionStorage.clear(); });
  await page.goto("/feel-assistant-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
});

const assistant = (page) => page.locator(".fa-modal");
const sent = (page) => page.locator("#sent");

async function openWithShortcut(page) {
  await page.locator("body").click({ position: { x: 5, y: 5 } });
  await page.keyboard.press("Control+Shift+E");
  await expect(assistant(page)).toBeVisible();
}

test("the shortcut opens the assistant from anywhere and closes it again", async ({ page }) => {
  await openWithShortcut(page);
  await expect(page.locator(".fa-title")).toHaveText("FEEL-Assistent");
  // Opened from no field: nothing to apply to, so no Apply button.
  await expect(page.locator("[data-apply]")).toHaveCount(0);
  await page.keyboard.press("Control+Shift+E");
  await expect(assistant(page)).toHaveCount(0);

  await openWithShortcut(page);
  await page.keyboard.press("Escape");
  await expect(assistant(page)).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("a message writes a checked expression into the editor", async ({ page }) => {
  await openWithShortcut(page);
  await expect(page.locator("[data-worker]")).toContainText("openrouter");
  await page.locator("[data-ask]").fill("10 % Rabatt, wenn total über 1000 liegt");
  await page.locator("[data-ask]").press("Control+Enter");

  // What the request carried: the conversation, the editor and no target.
  await expect(sent(page)).toContainText('"path":"/api/v1/feel/generate"');
  await expect(sent(page)).toContainText('"messages":[{"role":"user","content":"10 % Rabatt, wenn total über 1000 liegt"}]');

  // The proposal is in the editor, its example in the test pane, its verdict in the log.
  await expect(page.locator("[data-expr]")).toHaveValue("if total > 1000 then total * 0.1 else 0");
  await expect(page.locator("[data-vars]")).toHaveValue(/"total": 1500/);
  await expect(page.locator(".fa-check.ok")).toContainText("Geprüft: 150 (number) · wie erwartet");
  await expect(page.locator(".fa-meta")).toContainText("nach 2 Versuchen");
  // …and in the history, under the words that asked for it.
  await expect(page.locator(".fa-pick-name").first()).toHaveText("10 % Rabatt, wenn total über 1000 liegt");

  // The next message carries the whole conversation, the model's turn as the reply it
  // came with, and what the editor holds now — including a hand edit.
  await page.locator("[data-expr]").fill("if total > 1000 then total * 0.15 else 0");
  await page.locator("[data-ask]").fill("Nur für Gold-Kunden");
  await page.locator("[data-send]").click();
  await expect(sent(page)).toContainText('"role":"assistant","content":"{\\"expression\\":\\"if total > 1000 then total * 0.1 else 0\\"');
  await expect(sent(page)).toContainText('"expression":"if total > 1000 then total * 0.15 else 0"');
  await expect(sent(page)).toContainText('"variables":{"total":1500}');
  expect(page.__errors).toEqual([]);
});

test("a failed request leaves the message where it was typed", async ({ page }) => {
  await openWithShortcut(page);
  await page.evaluate(() => { window.__generate = "fails"; });
  await page.locator("[data-ask]").fill("Summe der offenen Positionen");
  await page.locator("[data-send]").click();
  await expect(page.locator("[data-status]")).toContainText("429");
  await expect(page.locator("[data-ask]")).toHaveValue("Summe der offenen Positionen");
  await expect(page.locator(".fa-msg.user")).toHaveCount(0);
});

test("invalid test variables are refused before anything is sent", async ({ page }) => {
  await openWithShortcut(page);
  await page.locator("[data-vars]").fill("{ total: 1 }");
  await page.locator("[data-ask]").fill("x");
  await page.locator("[data-send]").click();
  await expect(page.locator("[data-status]")).toContainText("kein gültiges JSON-Objekt");
  await expect(sent(page)).toHaveText("—");

  await page.locator("[data-expr]").fill("total * 2");
  await page.locator("[data-run]").click();
  await expect(page.locator("[data-out]")).toContainText("kein gültiges JSON-Objekt");
});

test("run evaluates the editor against the test variables", async ({ page }) => {
  await openWithShortcut(page);
  await page.locator("[data-expr]").fill("total * 2");
  await page.locator("[data-vars]").fill('{ "total": 21 }');
  await page.locator("[data-run]").click();
  await expect(page.locator("[data-out]")).toHaveText("→ 42 (number)");
  await expect(sent(page)).toContainText('"variables":{"total":21}');

  await page.locator("[data-expr]").fill("fail");
  await page.locator("[data-run]").click();
  await expect(page.locator("[data-out]")).toHaveText("boom");
});

test("copy, history and favourites outlive the assistant and the page", async ({ page }) => {
  await openWithShortcut(page);
  await page.locator("[data-expr]").fill("sum(positions.amount)");
  await page.locator("[data-copy]").click();
  await expect(page.locator("#copied")).toHaveText("sum(positions.amount)");
  await expect(page.locator("#toast")).toHaveText("Expression kopiert");
  await expect(page.locator("[data-tab=history]")).toHaveText("Verlauf (1)");

  // ☆ saves it as a favourite; it can be renamed.
  await page.locator("[data-star]").click();
  await expect(page.locator("[data-star]")).toHaveText("★");
  await page.locator("[data-tab=favourites]").click();
  await expect(page.locator("[data-tab=favourites]")).toHaveText("Favoriten (1)");
  await page.locator("[data-rename]").click();
  await page.locator(".fa-rename").fill("Summe aller Beträge");
  await page.locator(".fa-rename").press("Enter");
  await expect(page.locator(".fa-pick-name")).toHaveText("Summe aller Beträge");

  // Closed, reloaded, reopened: the favourite is still there, and picking it loads it.
  await page.keyboard.press("Escape");
  await page.reload();
  await page.waitForFunction(() => window.__ready === true);
  await openWithShortcut(page);
  await page.locator("[data-expr]").fill("");
  await page.locator("[data-tab=favourites]").click();
  await page.locator("[data-pick]").click();
  await expect(page.locator("[data-expr]")).toHaveValue("sum(positions.amount)");
  await expect(page.locator("[data-star]")).toHaveText("★");

  // ★ again removes it.
  await page.locator("[data-star]").click();
  await expect(page.locator("[data-tab=favourites]")).toHaveText("Favoriten (0)");
});

test("the conversation survives closing and reopening", async ({ page }) => {
  await openWithShortcut(page);
  await page.locator("[data-ask]").fill("Rabatt");
  await page.locator("[data-send]").click();
  await expect(page.locator(".fa-msg.ai")).toHaveCount(1);
  await page.keyboard.press("Escape");
  await openWithShortcut(page);
  await expect(page.locator(".fa-msg.user")).toHaveText("Rabatt");
  await expect(page.locator(".fa-msg.ai")).toHaveCount(1);

  await page.locator("[data-new]").click();
  await expect(page.locator(".fa-msg")).toHaveCount(0);
});

test("the mini spark on a FEEL field opens the assistant on that field, and Apply writes it back", async ({ page }) => {
  const spark = page.locator("#cond").locator("xpath=ancestor::div[contains(@class,'code-editor')]").locator(".feel-ai-open");
  await expect(spark).toBeVisible();
  await spark.dispatchEvent("mousedown");
  await expect(assistant(page)).toBeVisible();
  await expect(page.locator("[data-target]")).toHaveText("Für: Condition");
  // The field's own expression is what the assistant starts from.
  await expect(page.locator("[data-expr]")).toHaveValue("amount > 10");

  await page.locator("[data-ask]").fill("Rabatt");
  await page.locator("[data-send]").click();
  await expect(sent(page)).toContainText('"target":"Feld „Condition“ eines BPMN-Elements im Modeler"');
  await expect(page.locator("[data-expr]")).toHaveValue("if total > 1000 then total * 0.1 else 0");

  await page.locator("[data-apply]").click();
  await expect(assistant(page)).toHaveCount(0);
  await expect(page.locator("#cond")).toHaveValue("if total > 1000 then total * 0.1 else 0");
  // The panel's save wiring hears it as it hears a keystroke.
  await expect(page.locator("#changes")).toContainText("cond=if total > 1000 then total * 0.1 else 0");
  await expect(page.locator("#toast")).toHaveText("In „Condition“ übernommen");
});

test("an fx field keeps its '=' marker", async ({ page }) => {
  await page.locator("#fx").focus();
  await page.keyboard.press("Control+Shift+E");
  await expect(page.locator("[data-target]")).toHaveText("Für: Assignee");
  await expect(page.locator("[data-expr]")).toHaveValue("kunde.name");
  await page.locator("[data-expr]").fill("upper case(kunde.name)");
  await page.locator("[data-apply]").click();
  await expect(page.locator("#fx")).toHaveValue("= upper case(kunde.name)");
});

test("a dmn-js output cell gets a spark when focused, and Apply types into it", async ({ page }) => {
  await page.locator("#dmn-out").click();
  const spark = page.locator(".feel-ai-float");
  await expect(spark).toBeVisible();
  await spark.dispatchEvent("mousedown");
  await expect(page.locator("[data-target]")).toHaveText("Für: Ausgabezelle (DMN)");
  await expect(page.locator("[data-expr]")).toHaveValue('"alt"');
  await page.locator("[data-expr]").fill('"neu"');
  await page.locator("[data-apply]").click();
  await expect(page.locator("#dmn-out")).toHaveText('"neu"');
  // dmn-js reads its cells on input; the cell must have heard one.
  await expect(page.locator("#changes")).toContainText('dmn-out=\\"neu\\"');
});

test("a decision table's input cell is not written to", async ({ page }) => {
  await page.locator("#dmn-in").click();
  // No spark: the cell takes a unary test, not an expression.
  await expect(page.locator(".feel-ai-float")).toBeHidden();
  await page.keyboard.press("Control+Shift+E");
  await expect(page.locator("[data-target]")).toHaveText("Für: Eingabezelle (DMN)");
  await expect(page.locator("[data-apply]")).toBeDisabled();
  await expect(page.locator("[data-apply-note]")).toContainText("Unary Test");
});

test("without an AI Worker the chat is absent and the rest still works", async ({ page }) => {
  await page.goto("/feel-assistant-harness.html?workers=none");
  await page.waitForFunction(() => window.__ready === true);
  await openWithShortcut(page);
  await expect(page.locator(".fa-intro")).toContainText("Kein KI-Worker eingerichtet");
  await expect(page.locator("[data-ask]")).toBeDisabled();
  await page.locator("[data-expr]").fill("1 + 1");
  await page.locator("[data-run]").click();
  await expect(page.locator("[data-out]")).toHaveText("→ 42 (number)");
});

test("the top-bar button opens it and remembers the field that had the focus", async ({ page }) => {
  const btn = page.locator("#feel-assistant-btn");
  await expect(btn).toHaveAttribute("title", /FEEL-Assistent öffnen/);
  await page.locator("#cond").focus();
  await btn.click();
  await expect(page.locator("[data-target]")).toHaveText("Für: Condition");
});
