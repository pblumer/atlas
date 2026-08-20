// A table that outgrows its card must scroll inside it, never draw past it (ADR-0163).
// The regression this guards is visual and silent: adding one column or one button to
// any console table pushes its min-content width past the card, and the table is then
// laid out beyond the card's right edge — border and header rule stopping short, cells
// hanging in the page. No unit test sees that; this does.
import { test, expect } from "@playwright/test";

const open = async (page) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/table-overflow-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
};

test("an over-wide table scrolls inside its card instead of spilling out of it", async ({ page }) => {
  await open(page);

  const box = await page.evaluate(() => {
    const card = document.getElementById("wide");
    const host = document.getElementById("host");
    return {
      cardRight: card.getBoundingClientRect().right,
      hostRight: host.getBoundingClientRect().right,
      cardWidth: card.clientWidth,
      scrollWidth: card.scrollWidth,
      overflowX: getComputedStyle(card).overflowX,
    };
  });

  // The table really is wider than the card — otherwise this proves nothing.
  expect(box.scrollWidth).toBeGreaterThan(box.cardWidth);
  // The card is the scroll container, so the excess is reachable by scrolling and
  // clipped everywhere else. (The table's own bounding rect stays as wide as it was
  // laid out — geometry is not clipping — so asserting on it would measure nothing.)
  expect(box.overflowX).toBe("auto");
  // The card itself stays inside the box it was given.
  expect(box.cardRight).toBeLessThanOrEqual(box.hostRight + 1);
  expect(page.__errors).toEqual([]);
});

test("the page itself never scrolls sideways because of it", async ({ page }) => {
  await open(page);
  const doc = await page.evaluate(() => ({
    scrollWidth: document.documentElement.scrollWidth,
    clientWidth: document.documentElement.clientWidth,
  }));
  // The card absorbs the overflow, so nothing leaks out to the document — the symptom
  // an operator actually sees is the whole page acquiring a horizontal scrollbar.
  expect(doc.scrollWidth).toBeLessThanOrEqual(doc.clientWidth);
});
