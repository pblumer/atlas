// e2e for the value-or-expression (fx) toggle's locked '=' prefix (api/web/editor.js
// attachExpressionToggle → api/web/code-editor.js). A FEEL fx field stores an
// expression '=' prefixed (the compiler keys on it), but the '=' is not part of what
// the FEEL parser accepts. This proves the marker is shown dimmed and read-only, the
// validator sees the bare expression (no more spurious "unexpected '='"), and the
// marker can't be typed away — only the fx button switches back to a literal.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/feel-fx-harness.html");
  await page.waitForFunction(() => window.__ready === true);
  page._errors = errors;
});

test.afterEach(async ({ page }) => {
  expect(page._errors, "no uncaught page errors").toEqual([]);
});

test("expression mode shows a dimmed '=' prefix and validates the bare expression", async ({
  page,
}) => {
  // The '=' is rendered as its own dimmed span; the stored value is untouched.
  await expect(page.locator(".ce-locked")).toHaveText("=");
  expect(await page.evaluate(() => window.__el.value)).toBe("=email");

  // The reported bug: the validator used to be handed "=email" and flagged it. It now
  // receives the bare "email", and the field is not marked invalid.
  await page.waitForFunction(() => window.__validateCalls.length > 0);
  expect(await page.evaluate(() => window.__validateCalls.at(-1))).toBe("email");
  expect(await page.evaluate(() => window.__invalid())).toBe(false);
});

test("the '=' marker cannot be deleted with the keyboard", async ({ page }) => {
  await page.locator("#fld").focus();

  // Backspace with the caret right after '=' must not remove the marker.
  await page.evaluate(() => window.__el.setSelectionRange(1, 1));
  await page.keyboard.press("Backspace");
  expect(await page.evaluate(() => window.__el.value)).toBe("=email");

  // Select-all + Delete is blocked too — the whole edit reaches the locked run.
  await page.evaluate(() => window.__el.setSelectionRange(0, window.__el.value.length));
  await page.keyboard.press("Delete");
  expect(await page.evaluate(() => window.__el.value)).toBe("=email");

  // Editing after the marker still works: the expression, not the '=', changes.
  await page.evaluate(() => window.__el.setSelectionRange(window.__el.value.length, window.__el.value.length));
  await page.keyboard.type("2");
  expect(await page.evaluate(() => window.__el.value)).toBe("=email2");
});

test("the fx toggle switches back to a plain literal", async ({ page }) => {
  await page.locator(".fx-toggle").click();
  expect(await page.evaluate(() => window.__el.value)).toBe("email");
  await expect(page.locator(".ce-locked")).toHaveCount(0);
});
