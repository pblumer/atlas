// End-to-end coverage for the form editor's properties panel (api/web/app.css).
//
// form-js marks a properties group whose entries are all unset with the class `empty`
// — its own state flag, on `bio-properties-panel-group-header`. app.css carried a bare
// `.empty` for our "nothing here yet" placeholders: centred text, 34px of padding all
// round. It reached straight into the vendored panel, so in the Form editor (and the
// DMN one, which uses the same widget) every unset group became a 68px block with its
// title pushed inward and truncated — "Custom properties" showed as "Custom p" — while
// the groups that happened to have something set stayed 27px. Six rows, two shapes, for
// no reason the reader could see.
//
// The rule is now held off that panel rather than overridden inside it, so the vendor's
// own styling stands. These tests hold the outcome: every group header reads the same.
import { test, expect } from "@playwright/test";

test.use({ viewport: { width: 1500, height: 900 } });

test.beforeEach(async ({ page }) => {
  page.__errors = [];
  page.on("pageerror", (e) => page.__errors.push(e.message));
  await page.goto("/form-editor-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mountReal());
  // The Playground loads form-js lazily; wait for the field, then select it so the
  // properties panel has something to describe.
  const field = page.locator(".fjs-form-field-textfield").first();
  await expect(field).toBeVisible({ timeout: 25000 });
  await field.click();
  await expect(page.locator(".bio-properties-panel-group-header").first()).toBeVisible();
});

const headers = (page) => page.locator(".bio-properties-panel-group-header");

test("an unset group is styled like a set one, not padded out to twice the height", async ({ page }) => {
  const boxes = await headers(page).evaluateAll((hs) =>
    hs.map((h) => ({
      title: (h.querySelector(".bio-properties-panel-group-header-title") || {}).textContent || "",
      height: Math.round(h.getBoundingClientRect().height),
      padding: getComputedStyle(h).padding,
      empty: h.classList.contains("empty"),
    })));

  // The panel has both kinds — otherwise this test proves nothing.
  expect(boxes.some((b) => b.empty)).toBe(true);
  expect(boxes.some((b) => !b.empty)).toBe(true);

  // One height for all of them, and none of them carrying the placeholder's padding.
  expect(new Set(boxes.map((b) => b.height)).size).toBe(1);
  for (const b of boxes) expect(b.padding).toBe("0px");
  expect(page.__errors).toEqual([]);
});

test("every group title starts at the same place and none is truncated", async ({ page }) => {
  const titles = await page.locator(".bio-properties-panel-group-header-title").evaluateAll((ts) =>
    ts.map((t) => ({
      text: t.textContent || "",
      left: Math.round(t.getBoundingClientRect().left),
      // scrollWidth beyond clientWidth is what the ellipsis is hiding.
      clipped: t.scrollWidth > t.clientWidth + 1,
      align: getComputedStyle(t).textAlign,
    })));

  expect(titles.length).toBeGreaterThan(2);
  expect(new Set(titles.map((t) => t.left)).size).toBe(1); // one column, not two
  for (const t of titles) {
    expect(t.align).not.toBe("center"); // the placeholder centred them
    expect(t.clipped, `"${t.text}" is truncated`).toBe(false);
  }
  // The longest of them is the one that used to lose its second word.
  expect(titles.map((t) => t.text)).toContain("Custom properties");
  expect(page.__errors).toEqual([]);
});

test("our own placeholder still looks like a placeholder", async ({ page }) => {
  // The fix narrows a rule that is still doing its job everywhere else; a `.empty`
  // outside the vendored panel must keep the centred, padded placeholder styling.
  const style = await page.evaluate(() => {
    const p = document.createElement("p");
    p.className = "empty";
    p.textContent = "Nothing here yet.";
    document.body.appendChild(p);
    const cs = getComputedStyle(p);
    const out = { padding: cs.padding, align: cs.textAlign };
    p.remove();
    return out;
  });
  expect(style.padding).toBe("34px");
  expect(style.align).toBe("center");
  expect(page.__errors).toEqual([]);
});
