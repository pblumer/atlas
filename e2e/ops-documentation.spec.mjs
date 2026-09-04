// End-to-end coverage for element documentation in the Operations instance replay
// (api/web/editor.js mountInstanceReplay, ADR-0025). The replay already imports the
// model to draw it, so the Details tab reads each element's <bpmn:documentation> straight
// off the rendered diagram — no extra request, and it works for every element, including
// one this instance never reached. Driven through the real vendored bpmn-js.
//
// The prose is rendered as Markdown (ADR-draft-documentation-is-markdown) by the same
// module the Tasks app uses; markdown.spec.mjs covers the renderer, including that its
// output cannot script the page. What is checked here is that this panel renders rather
// than escapes, and that plain prose written before Markdown still reads as written.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/ops-documentation-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await expect(page.locator("#history-list .ops-hrow").first()).toBeVisible();
});

// selectStep picks an element through its Instance History row — the same
// selectElement path as a diagram click, without the replay chrome overlapping the
// small canvas.
const selectStep = (page, eik) => page.locator(`#history-list .ops-hrow[data-eik="${eik}"]`).click();

test("an executed element shows what the modeler documented about it", async ({ page }) => {
  await selectStep(page, "1002"); // Task_check

  const details = page.locator("#tab-details");
  await expect(details).toContainText("Task_check");
  const doc = details.locator(".ops-doc");
  await expect(doc.locator("h4")).toHaveText("Documentation");
  // Plain prose from before the field understood Markdown, unchanged: two paragraphs,
  // the author's break between them kept by the renderer rather than by `white-space`.
  await expect(doc.locator(".md p")).toHaveCount(2);
  await expect(doc.locator(".md p").first()).toHaveText("Vergleiche die Angaben mit den Anlagen.");
  await expect(doc.locator(".md p").nth(1)).toHaveText("Bei fehlenden Unterlagen ablehnen.");
  expect(page.__errors).toEqual([]);
});

test("an undocumented element shows no block, not the previous element's text", async ({ page }) => {
  await selectStep(page, "1002"); // documented
  await expect(page.locator("#tab-details .ops-doc")).toBeVisible();

  await selectStep(page, "1001"); // Gateway_1 carries no documentation
  await expect(page.locator("#tab-details")).toContainText("Gateway_1");
  await expect(page.locator("#tab-details .ops-doc")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("with nothing selected the panel describes the process itself", async ({ page }) => {
  const doc = page.locator("#tab-details .ops-doc");
  await expect(doc).toBeVisible();
  await expect(doc.locator("p")).toContainText("Bearbeitet Anträge von der Prüfung bis zur Auszahlung.");
  expect(page.__errors).toEqual([]);
});

test("an element this instance never reached still explains itself", async ({ page }) => {
  // Task_escalate is the branch that was not taken: it has no history row, so the
  // diagram is the only way in. Selecting it used to fall back to the process panel.
  await page.locator('#canvas g[data-element-id="Task_escalate"]').click();

  const details = page.locator("#tab-details");
  await expect(details).toContainText("Eskalieren");
  await expect(details).toContainText("Not reached in this instance");
  await expect(details.locator(".ops-doc p")).toContainText("an die Fachaufsicht abgeben");
  expect(page.__errors).toEqual([]);
});

test("a marked-up documentation reads as structure, not as markers", async ({ page }) => {
  // Task_escalate is the untaken branch and the one written as Markdown: the operator
  // asking "what would this have done?" gets the modeler's own list and emphasis.
  await page.locator('#canvas g[data-element-id="Task_escalate"]').click();

  const doc = page.locator("#tab-details .ops-doc");
  await expect(doc.locator(".md strong")).toHaveText("Fachaufsicht");
  await expect(doc.locator(".md ul li")).toHaveCount(2);
  await expect(doc.locator(".md code")).toHaveText("7 Tage");

  // The panel's own "DOCUMENTATION" label is the app talking; a heading inside the prose
  // is the modeler talking, and the two must not look alike.
  const label = await doc.locator("> h4").evaluate((el) => getComputedStyle(el).textTransform);
  const heading = await doc.locator(".md h2").evaluate((el) => getComputedStyle(el).textTransform);
  expect(label).toBe("uppercase");
  expect(heading, "the modeler's heading is not the panel's label").toBe("none");

  const shown = await doc.locator(".md").innerText();
  expect(shown).not.toContain("**");
  expect(shown).not.toContain("`");
  expect(page.__errors).toEqual([]);
});
