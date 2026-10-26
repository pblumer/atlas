// End-to-end coverage for the fx switch on a timer's schedule field (api/web/editor.js
// timerFieldsHTML → attachExpressionToggle). The engine has evaluated a timer whose
// timeDuration, timeDate or timeCycle begins with '=' as FEEL since ADR-0055/0056, but
// the Modeler offered only a plain text field, so the one way to reach it was to know
// the '=' convention. Driven through the real vendored bpmn-js: a literal opens as a
// literal and an '=' value as an expression, the switch flips between the two, and the
// exported XML — what a deploy sends — carries exactly what the field shows.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/timer-fx-modeler-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await page.locator('[data-tab="implement"]').click();
});

test.afterEach(async ({ page }) => {
  expect(page.__errors, "no uncaught page errors").toEqual([]);
});

// openTimer selects an element and expands its Timer group, wherever the panel puts it,
// so the schedule field is on screen.
async function openTimer(page, id) {
  await page.evaluate((el) => window.__select(el), id);
  const field = page.locator("#f-timerval");
  await expect(field).toHaveCount(1);
  if (!(await field.isVisible())) await page.locator('.pgroup[data-group*="Timer" i] > .pgroup-head').first().click();
  await expect(field).toBeVisible();
}

// The fx switch sits in the schedule field's label, like every other fx field's.
const fxToggle = (page) => page.locator("label.field", { has: page.locator("#f-timerval") }).locator(".fx-toggle");

// eventXML is the exported XML of one event, so an assertion cannot match a neighbour's.
async function eventXML(page, id) {
  const xml = await page.evaluate(() => window.__xml());
  const m = new RegExp(`<bpmn:(\\w+) id="${id}"[\\s\\S]*?</bpmn:\\1>`).exec(xml);
  expect(m, `${id} in the exported XML`).not.toBeNull();
  return m[0];
}

// typeAtEnd puts the caret after the field's text and types there — after the locked
// '=' when the field holds an expression.
async function typeAtEnd(page, text) {
  const field = page.locator("#f-timerval");
  await field.focus();
  await field.evaluate((el) => el.setSelectionRange(el.value.length, el.value.length));
  await page.keyboard.type(text);
  await field.blur();
}

test("a literal duration opens as a literal, and fx makes it an expression", async ({ page }) => {
  await openTimer(page, "Catch_lit");
  await expect(page.locator("#f-timerval")).toHaveValue("PT30M");
  await expect(fxToggle(page)).toHaveCount(1);
  await expect(fxToggle(page)).not.toHaveClass(/active/);

  await fxToggle(page).click();
  await expect(fxToggle(page)).toHaveClass(/active/);
  await expect(page.locator("#f-timerval")).toHaveValue("= PT30M");
  await expect(page.locator("label.field", { has: page.locator("#f-timerval") }).locator(".ce-locked")).toHaveText("=");
  // An '=' literal is still the same fixed schedule (ADR-0055); what matters here is that
  // the switch wrote the expression form the compiler keys on.
  expect(await eventXML(page, "Catch_lit")).toMatch(/<bpmn:timeDuration[^>]*>= PT30M<\/bpmn:timeDuration>/);

  // Replace the literal with a variable the instance carries.
  await page.locator("#f-timerval").focus();
  await page.locator("#f-timerval").evaluate((el) => el.setSelectionRange(el.value.length, el.value.length));
  for (let i = 0; i < "PT30M".length; i++) await page.keyboard.press("Backspace");
  await typeAtEnd(page, "wartezeit");
  await expect(page.locator("#f-timerval")).toHaveValue("= wartezeit");
  const xml = await eventXML(page, "Catch_lit");
  expect(xml).toMatch(/<bpmn:timeDuration[^>]*>= wartezeit<\/bpmn:timeDuration>/);
  expect(xml).not.toContain("timeDate");
  // The engine is asked about the bare expression, not its '=' storage form.
  await page.waitForFunction(() => window.__validateCalls.includes("wartezeit"));
});

test("an expression opens as one, and fx turns it back into a literal", async ({ page }) => {
  await openTimer(page, "Catch_feel");
  await expect(page.locator("#f-timerval")).toHaveValue("= wartezeit");
  await expect(fxToggle(page)).toHaveClass(/active/);

  // Opening the event changed nothing.
  expect(await eventXML(page, "Catch_feel")).toMatch(/<bpmn:timeDuration[^>]*>= wartezeit<\/bpmn:timeDuration>/);

  await fxToggle(page).click();
  await expect(fxToggle(page)).not.toHaveClass(/active/);
  await expect(page.locator("#f-timerval")).toHaveValue("wartezeit");
  expect(await eventXML(page, "Catch_feel")).toMatch(/<bpmn:timeDuration[^>]*>wartezeit<\/bpmn:timeDuration>/);
});

test("a date can be an expression too, written into timeDate", async ({ page }) => {
  await openTimer(page, "Catch_lit");
  await page.locator("#f-timerkind").selectOption("date");
  // The panel re-renders for the new kind; the switch comes with it.
  await expect(page.locator("#f-timerkind")).toHaveValue("date");
  await expect(page.locator("#f-timerval")).toHaveValue("");
  await expect(fxToggle(page)).not.toHaveClass(/active/);

  await fxToggle(page).click();
  await expect(fxToggle(page)).toHaveClass(/active/);
  await typeAtEnd(page, " termin");
  await expect(page.locator("#f-timerval")).toHaveValue("= termin");
  const xml = await eventXML(page, "Catch_lit");
  expect(xml).toMatch(/<bpmn:timeDate[^>]*>= termin<\/bpmn:timeDate>/);
  expect(xml).not.toContain("timeDuration");
});

test("the panel says how to write an expression and what it must yield", async ({ page }) => {
  await openTimer(page, "Catch_lit");
  const panel = page.locator("label.field", { has: page.locator("#f-timerval") }).locator("xpath=following-sibling::p[1]");
  await expect(panel).toContainText("fx");
  await expect(panel).toContainText("evaluated when the timer is armed");
});

test("a boundary timer and a timer start offer the switch as well", async ({ page }) => {
  await openTimer(page, "Boundary_t");
  await expect(page.locator("#f-timerval")).toHaveValue("PT1H");
  await expect(fxToggle(page)).toHaveCount(1);

  await openTimer(page, "Start_timer");
  await expect(page.locator("#f-timerkind")).toHaveValue("date");
  await expect(page.locator("#f-timerval")).toHaveValue("2026-08-01T09:00:00Z");
  await expect(fxToggle(page)).toHaveCount(1);
  // A timer start has no instance yet, so its expression may read no variables; the
  // panel says so where the expression is written rather than leaving it to the deploy.
  const note = page.locator("label.field", { has: page.locator("#f-timerval") }).locator("xpath=following-sibling::p[1]");
  await expect(note).toContainText("no instance");
});
