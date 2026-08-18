// e2e for the Developer View (api/web/dev-view.js, ADR-0144) — the modal a
// code-bearing field opens with F2. What matters here is that the shortcut finds the
// right field, that every pane writes at the caret of the *modal's* editor rather
// than the field behind it, and that leaving the modal cannot silently lose or
// silently commit an edit.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/dev-view-harness.html");
  await page.waitForFunction(() => window.__ready === true);
  page._errors = errors;
});

test.afterEach(async ({ page }) => {
  expect(page._errors, "no uncaught page errors").toEqual([]);
});

// openFeel focuses the FEEL field and presses F2 — the gesture the whole feature is.
async function openFeel(page) {
  await page.locator("#feel-fld").focus();
  await page.keyboard.press("F2");
  await expect(page.locator(".dev-modal")).toBeVisible();
}

test("F2 opens the focused field in the modal, with its language and title", async ({ page }) => {
  await openFeel(page);
  await expect(page.locator(".dev-badge")).toHaveText("FEEL");
  await expect(page.locator(".dev-title")).toHaveText("Expression (FEEL)");
  // The modal edits a copy of the field's value.
  expect(await page.evaluate(() => document.querySelector(".dev-ta").value)).toBe("amount");
  await expect(page.locator(".dev-foot .dev-pos")).toHaveText("Ln 1, Col 7");
});

test("F2 does nothing in a field that holds no code", async ({ page }) => {
  await page.evaluate(() => {
    const el = document.createElement("textarea");
    el.id = "plain";
    document.body.appendChild(el);
    el.focus();
  });
  await page.keyboard.press("F2");
  await expect(page.locator(".dev-modal")).toHaveCount(0);
});

test("the variables pane groups by scope and inserts at the caret", async ({ page }) => {
  await openFeel(page);
  await expect(page.locator(".dev-pane-vars .dev-group h4")).toHaveText([
    "Input", "Output", "Process scope", "Form fields", "Data objects",
  ]);

  // Caret to the start, then insert the process variable: it lands there, not at the
  // end, and not in the field behind the modal.
  await page.evaluate(() => {
    const ta = document.querySelector(".dev-ta");
    ta.focus();
    ta.setSelectionRange(0, 0);
  });
  await page.locator(".dev-pane-vars .dev-item", { hasText: "orderId" }).click();
  expect(await page.evaluate(() => document.querySelector(".dev-ta").value)).toBe("orderIdamount");
  expect(await page.evaluate(() => document.querySelector("#feel-fld").value)).toBe("amount");

  // The filter narrows the pane to one row.
  await page.locator(".dev-filter").fill("email");
  await expect(page.locator(".dev-pane-vars .dev-item")).toHaveCount(1);
  await expect(page.locator(".dev-pane-vars .dev-item-name")).toHaveText("email");
});

test("clicking a function opens its help page without touching the code", async ({ page }) => {
  await openFeel(page);
  await page.locator(".dev-tab[data-tab='fns']").click();
  await page.locator(".dev-filter").fill("upper case");
  await page.locator(".dev-pane-fns .dev-fn", { hasText: "upper case" }).first().click();

  // Browsing the reference must not edit what the author is writing.
  expect(await page.evaluate(() => document.querySelector(".dev-ta").value)).toBe("amount");
  await expect(page.locator(".dev-pane-help")).toBeVisible();
  await expect(page.locator(".dev-help-sig")).toHaveText("upper case(string)");
  await expect(page.locator(".dev-help-ex")).toContainText("ATLAS");

  // The help page inserts: the call, with the caret between the parentheses…
  await page.locator(".dev-pane-help .dev-ins-fn").click();
  expect(await page.evaluate(() => document.querySelector(".dev-ta").value)).toBe("amountupper case()");
  expect(await page.evaluate(() => document.querySelector(".dev-ta").selectionStart)).toBe("amountupper case(".length);
  // …or the worked example.
  await page.locator(".dev-pane-help .dev-ins-ex").click();
  expect(await page.evaluate(() => document.querySelector(".dev-ta").value)).toContain('upper case("atlas")');
});

test("the '+' beside a function row inserts it in one click", async ({ page }) => {
  await openFeel(page);
  await page.locator(".dev-tab[data-tab='fns']").click();
  await page.locator(".dev-filter").fill("string length");
  await page.locator(".dev-pane-fns .dev-row", { hasText: "string length" }).locator(".dev-quick").click();
  expect(await page.evaluate(() => document.querySelector(".dev-ta").value)).toBe("amountstring length()");
  // Still on the catalogue — inserting is not a navigation.
  await expect(page.locator(".dev-pane-fns")).toBeVisible();
});

test("an example snippet is inserted whole", async ({ page }) => {
  await openFeel(page);
  await page.evaluate(() => {
    const ta = document.querySelector(".dev-ta");
    ta.value = "";
    ta.dispatchEvent(new Event("input", { bubbles: true }));
  });
  await page.locator(".dev-tab[data-tab='snips']").click();
  await page.locator(".dev-snip", { hasText: "Conditional" }).locator(".dev-ins-snip").click();
  expect(await page.evaluate(() => document.querySelector(".dev-ta").value))
    .toBe('if amount > 1000 then "manager" else "team"');
});

test("Apply writes the value back and fires the panel's change event", async ({ page }) => {
  await openFeel(page);
  await page.locator(".dev-ta").focus();
  await page.keyboard.type(" * 2");
  await page.locator(".dev-apply").click();

  await expect(page.locator(".dev-modal")).toHaveCount(0);
  expect(await page.evaluate(() => document.querySelector("#feel-fld").value)).toBe("amount * 2");
  expect(await page.evaluate(() => window.__changes)).toEqual([{ id: "feel-fld", value: "amount * 2" }]);
  // Focus returns to the field it came from.
  expect(await page.evaluate(() => document.activeElement.id)).toBe("feel-fld");
});

test("F2 inside the modal applies and hands the work back to the field", async ({ page }) => {
  await openFeel(page);
  await page.locator(".dev-ta").focus();
  await page.keyboard.type(" + 1");
  await page.keyboard.press("F2");
  await expect(page.locator(".dev-modal")).toHaveCount(0);
  expect(await page.evaluate(() => document.querySelector("#feel-fld").value)).toBe("amount + 1");
});

test("Escape with unsaved changes asks first, and discards on the second press", async ({ page }) => {
  await openFeel(page);
  await page.locator(".dev-ta").focus();
  await page.keyboard.type(" * 2");

  await page.keyboard.press("Escape");
  await expect(page.locator(".dev-modal")).toBeVisible();
  await expect(page.locator(".dev-confirm")).toBeVisible();

  // "Keep editing" puts you back in the code with the edit intact.
  await page.locator(".dev-keep").click();
  await expect(page.locator(".dev-confirm")).toBeHidden();
  expect(await page.evaluate(() => document.querySelector(".dev-ta").value)).toBe("amount * 2");

  await page.keyboard.press("Escape");
  await page.keyboard.press("Escape");
  await expect(page.locator(".dev-modal")).toHaveCount(0);
  // The field kept its original value and nothing was saved.
  expect(await page.evaluate(() => document.querySelector("#feel-fld").value)).toBe("amount");
  expect(await page.evaluate(() => window.__changes)).toEqual([]);
});

test("Escape closes straight away when nothing was changed", async ({ page }) => {
  await openFeel(page);
  await page.keyboard.press("Escape");
  await expect(page.locator(".dev-modal")).toHaveCount(0);
  expect(await page.evaluate(() => window.__changes)).toEqual([]);
});

test("the Test panel evaluates the expression through the server round trip", async ({ page }) => {
  await openFeel(page);
  await page.locator(".dev-test-toggle").click();
  await page.locator(".dev-run-vars").fill('{ "amount": 21 }');
  await page.locator(".dev-run-btn").click();
  await expect(page.locator(".dev-run-out")).toHaveClass(/ok/);
  await expect(page.locator(".dev-run-detail")).toContainText("42");
  await expect(page.locator(".dev-run-detail")).toContainText("number");
  expect(await page.evaluate(() => window.__evaluateCalls.at(-1))).toEqual({
    expression: "amount",
    vars: { amount: 21 },
  });
});

test("live validation marks a broken expression", async ({ page }) => {
  await openFeel(page);
  await page.locator(".dev-ta").focus();
  await page.keyboard.type(" boom");
  await expect(page.locator(".dev-editor .code-editor.invalid")).toBeVisible();
  await expect(page.locator(".dev-editor .feel-status")).toHaveText("1:1: boom");
});

test("a documentation field opens as Markdown, with no function reference", async ({ page }) => {
  await page.locator("#doc-fld").focus();
  await page.keyboard.press("F2");
  await expect(page.locator(".dev-badge")).toHaveText("Markdown");
  await expect(page.locator(".dev-title")).toHaveText("Documentation");
  await expect(page.locator(".dev-tab[data-tab='fns']")).toBeHidden();
  // No Test panel either — there is nothing to run.
  await expect(page.locator(".dev-test-toggle")).toHaveCount(0);
  // Markdown structure is highlighted.
  await expect(page.locator(".dev-editor .tok-md-head")).toHaveText("# Titel");
});

test("a JSON field can be formatted from the modal", async ({ page }) => {
  await page.locator("#json-fld").focus();
  await page.keyboard.press("F2");
  await expect(page.locator(".dev-badge")).toHaveText("JSON");
  await page.locator(".dev-fmt").click();
  expect(await page.evaluate(() => document.querySelector(".dev-ta").value)).toBe('{\n  "a": 1\n}');
  await page.locator(".dev-apply").click();
  expect(await page.evaluate(() => document.querySelector("#json-fld").value)).toBe('{\n  "a": 1\n}');
});

test("variables carry the value they hold in a real instance", async ({ page }) => {
  await openFeel(page);

  // The strip says where the values came from.
  await expect(page.locator(".dev-sample-strip")).toContainText("instance 4711");
  await expect(page.locator(".dev-sample-strip")).toContainText("active");

  // A number, a string (quoted so an empty one is visible) and a flattened JSON.
  const row = (name) => page.locator(".dev-pane-vars .dev-item", { hasText: name });
  await expect(row("amount").locator(".dev-item-value")).toHaveText("= 1250.5");
  await expect(row("orderId").locator(".dev-item-value")).toHaveText('= "A-2026-0042"');
  await expect(row("email").locator(".dev-item-value")).toContainText("anna@example.com");
  // A variable the instance does not carry simply has no value line.
  await expect(row("gross").locator(".dev-item-value")).toHaveCount(0);

  // Clicking still inserts the *name* — the value is context, not the payload.
  await row("orderId").click();
  expect(await page.evaluate(() => document.querySelector(".dev-ta").value)).toBe("amountorderId");
});

test("the Test panel is prefilled from the instance, and can be refilled", async ({ page }) => {
  await openFeel(page);
  await page.locator(".dev-test-toggle").click();

  // Typed values, not the server's string form.
  const prefilled = await page.evaluate(() => document.querySelector(".dev-run-vars").value);
  expect(JSON.parse(prefilled)).toEqual({
    amount: 1250.5,
    orderId: "A-2026-0042",
    email: { to: "anna@example.com", cc: [] },
  });

  // What the author typed is never overwritten behind their back…
  await page.locator(".dev-run-vars").fill('{ "amount": 1 }');
  await page.locator(".dev-sample-reload").click();
  await expect(page.locator(".dev-sample-strip")).toContainText("instance 4711");
  expect(await page.evaluate(() => document.querySelector(".dev-run-vars").value)).toBe('{ "amount": 1 }');
  // …until they ask for it.
  await page.locator(".dev-run-fill").click();
  expect(JSON.parse(await page.evaluate(() => document.querySelector(".dev-run-vars").value)).amount).toBe(1250.5);
});

test("a process that has never run says so instead of failing", async ({ page }) => {
  await page.evaluate(() => { window.__samples = null; });
  await openFeel(page);
  await expect(page.locator(".dev-sample-strip")).toContainText("No instance to read values from yet");
  await expect(page.locator(".dev-pane-vars .dev-item-value")).toHaveCount(0);
  // Reload asks again — and this time an instance exists.
  await page.evaluate(() => {
    window.__samples = { instance: { key: 9, state: "completed" }, values: { amount: { value: "7", kind: "number" } } };
  });
  await page.locator(".dev-sample-reload").click();
  await expect(page.locator(".dev-sample-strip")).toContainText("instance 9");
  expect(await page.evaluate(() => window.__sampleCalls.map((c) => c.force))).toEqual([false, true]);
});

test("the side panel folds away and the choice is remembered", async ({ page }) => {
  await openFeel(page);
  await expect(page.locator(".dev-pane-vars")).toBeVisible();

  await page.locator(".dev-side-toggle").click();
  await expect(page.locator(".dev-side")).toHaveClass(/collapsed/);
  await expect(page.locator(".dev-pane-vars")).toBeHidden();
  // The tabs stay reachable as a rail.
  await expect(page.locator(".dev-tab[data-tab='vars']")).toBeVisible();

  // Reopening the view honours the choice…
  await page.keyboard.press("Escape");
  await openFeel(page);
  await expect(page.locator(".dev-side")).toHaveClass(/collapsed/);

  // …and picking a tab from the rail expands onto it.
  await page.locator(".dev-tab[data-tab='snips']").click();
  await expect(page.locator(".dev-side")).not.toHaveClass(/collapsed/);
  await expect(page.locator(".dev-pane-snips")).toBeVisible();
});

test("the inline '</>' button opens the same view", async ({ page }) => {
  await page.locator("#feel-fld").hover();
  await page.locator(".code-editor .dev-open").click();
  await expect(page.locator(".dev-modal")).toBeVisible();
  await expect(page.locator(".dev-badge")).toHaveText("FEEL");
});
