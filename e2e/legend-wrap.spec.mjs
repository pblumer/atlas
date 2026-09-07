// End-to-end coverage for the runtime legend at the width it is actually read at.
//
// The bar under an Operations diagram is shared with the Modeler's problem list, which
// is one fixed 30px row — fine for "3 problems ⌄", wrong for a legend of seven entries.
// On a narrow window every entry broke across two and three lines and the bar then cut
// them off at its own height: "completed here / and moved on" with the second half
// sliced by the border, which is what the legend looked like at 700px. A key that has to
// be guessed at is not a key.
//
// So the geometry is the behaviour here, and only a browser knows it: an entry must
// never break in the middle, the row wraps between entries instead, and the bar grows by
// whole rows to hold what it wraps. A wide window must keep the single row it always
// had — the fix is for the narrow case and must cost the normal one nothing.
import { test, expect } from "@playwright/test";

const open = async (page) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/event-gateway-overlay-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mountLive());
  await page.waitForSelector(".legend-toggle");
};

// One text line of the bar is 22px at most: the tallest thing on it is a count badge
// (20px) inside its switch (1px of padding either side). Anything taller has wrapped.
const ONE_LINE = 22;

test.describe("a narrow window", () => {
  test.use({ viewport: { width: 700, height: 560 } });

  test("keeps every legend entry whole, and inside the bar", async ({ page }) => {
    await open(page);
    const bar = page.locator(".problems");

    // Nothing is cut off: the bar is at least as tall as everything it holds. This reads
    // the scroll height rather than the entries, because half the legend is bare text
    // nodes — which is exactly what used to be sliced, and what a per-element check
    // would miss.
    const fit = await bar.evaluate((el) => ({ scroll: el.scrollHeight, client: el.clientHeight }));
    expect(fit.scroll).toBeLessThanOrEqual(fit.client);

    // And no entry is broken in the middle: each switch is one line, whole, within the
    // bar it sits in.
    const box = await bar.boundingBox();
    for (const kind of ["passed", "cancelled", "live"]) {
      const b = await page.locator(`.legend-toggle[data-badge="${kind}"]`).boundingBox();
      expect(b.height).toBeLessThanOrEqual(ONE_LINE);
      expect(b.y).toBeGreaterThanOrEqual(box.y);
      expect(b.y + b.height).toBeLessThanOrEqual(box.y + box.height);
    }

    // Which at this width it can only manage by wrapping between entries and taking a
    // second row for them.
    expect(box.height).toBeGreaterThan(30);
    expect(page.__errors).toEqual([]);
  });
});

test.describe("a wide window", () => {
  test.use({ viewport: { width: 1500, height: 560 } });

  test("still draws the legend as the single row it always was", async ({ page }) => {
    await open(page);
    const box = await page.locator(".problems").boundingBox();
    expect(box.height).toBe(30);

    // Every entry on that one row, at the same height — nothing has been pushed down.
    const tops = await page.$$eval(".problems .legend-toggle", (els) =>
      els.map((el) => Math.round(el.getBoundingClientRect().y)));
    expect(tops.length).toBe(3);
    expect(new Set(tops).size).toBe(1);
    expect(page.__errors).toEqual([]);
  });
});
