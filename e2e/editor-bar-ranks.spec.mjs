// The rank rules ADR-0229 worked out for the Modeler's bar, held to across every
// editor bar in Atlas (ADR-draft-shared-ui-primitives).
//
// The record asked for a builder that emits the bar. Measured against the four other
// bars, that is not what is missing: the Modeler's had seven equal buttons mixing
// four kinds of act, and the form editor, the class canvas and the Panorama viewer
// have one or two acts each. There is no rank there to get wrong, and a builder would
// be placing a single button for three of them.
//
// What is worth holding is the reasoning rather than the markup — an act that cannot
// be taken back does not look like one that can, state and command are different
// shapes, and a bar does not grow back into a row of equals. That is what these
// assert, on the rendered DOM rather than on the source, so a bar is measured as a
// reader meets it.
import { test, expect } from "@playwright/test";

// FILLED is the accent-filled button: `btn` with no ghost/neutral modifier. ADR-0229
// reserves it for the one act that leaves the browser and cannot be undone.
const FILLED = "button.btn:not(.ghost):not(.neutral):not(.icon-btn)";
// Direct acts are the bar's own buttons, not the ones inside its overflow menu.
const DIRECT = ":scope > button.btn, :scope > .dropdown > button, :scope > * > button.btn";

async function checkBar(page, selector) {
  const bar = page.locator(selector);
  await expect(bar).toHaveCount(1);

  // At most one filled act. Two filled buttons side by side say "these are equally
  // consequential", which is the thing the flat row said and got wrong.
  const filled = await bar.locator(FILLED).count();
  expect(filled, `${selector}: more than one filled act`).toBeLessThanOrEqual(1);

  // Every control that holds a state says so, in the vocabulary for what it is: a
  // toggle is pressed or not (aria-pressed), a tab is selected or not
  // (aria-selected). The Modeler shipped two toggles where only one carried
  // aria-pressed, and its tabs said "active" in a class and nothing else — so which
  // view you were in reached sighted readers alone.
  const unannounced = await bar.evaluate((el) => {
    const out = [];
    for (const b of el.querySelectorAll("button")) {
      const isTab = b.getAttribute("role") === "tab" || !!b.dataset.tab || !!b.dataset.ftab;
      const attr = isTab ? "aria-selected" : "aria-pressed";
      const holdsState = isTab || b.classList.contains("toggle") || b.classList.contains("active");
      if (holdsState && !b.hasAttribute(attr)) out.push(`${b.id || b.textContent.trim()} (${attr})`);
    }
    return out;
  });
  expect(unannounced, `${selector}: controls that hold a state without saying so`).toEqual([]);

  // A tab belongs to a tablist, or a screen reader meets three unrelated buttons.
  const tabs = await bar.locator('button[role="tab"]').count();
  if (tabs > 0) {
    expect(await bar.locator('[role="tablist"]').count(),
      `${selector}: tabs outside a tablist`).toBeGreaterThan(0);
  }

  // A bar that has grown past a handful of direct acts is the row ADR-0229 broke up;
  // beyond that, the rest belongs behind one menu.
  const direct = await bar.evaluate((el, sel) => el.querySelectorAll(sel).length, DIRECT);
  if (direct > 4) {
    expect(await bar.locator(".dropdown-menu").count(),
      `${selector}: ${direct} direct acts and no overflow menu`).toBeGreaterThan(0);
  }
}

test("the Modeler's bar keeps the ranks the record decided", async ({ page }) => {
  await page.goto("/editor-bar-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await checkBar(page, ".editor-bar");
});

test("the form editor's bar holds to them too", async ({ page }) => {
  await page.goto("/form-editor-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await checkBar(page, ".editor-bar");
});

test("the class canvas's bar holds to them too", async ({ page }) => {
  await page.goto("/infomodel-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  // The class canvas sits in a card rather than full-bleed, so its bar is styled for
  // that context and carries its own class. The ranks are the same question.
  await checkBar(page, ".im-bar");
});
