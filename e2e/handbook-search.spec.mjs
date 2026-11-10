// The handbooks' search field (api/web/handbook-search.js, in handbuch.html and
// shop-handbuch.html). Ctrl+F already finds words on these pages; this field is for the
// readers who do not know it, so what has to hold is what such a reader relies on:
//
//   - a word anywhere in the visible language is found, also one folded away in a
//     <details>, and choosing it opens the fold and brings the passage on screen;
//   - only the language on screen is searched — a hit in the hidden half would scroll
//     to something nobody can see;
//   - the keyboard alone is enough ("/", arrows, Enter, Escape);
//   - accents and ß do not have to be typed;
//   - on a phone the field fits under the bar without pushing its controls off it.
import { test, expect } from "@playwright/test";

async function openHandbook(page, lang, url = "/handbuch.html") {
  await page.goto(url);
  await page.click("#lang-" + lang);
  await expect(page.locator(".hsearch input")).toBeVisible();
}

const search = (page) => page.locator(".hsearch input");
const results = (page) => page.locator(".hsearch .hs-item");

test("a word inside a folded block is found, and choosing it opens the fold", async ({ page }) => {
  await openHandbook(page, "en");
  await search(page).fill("appears three times in the same expression");
  await expect(results(page).first()).toBeVisible();
  await expect(results(page).first().locator(".hs-text")).toContainText("It appears three times in the same expression");
  await expect(results(page).first().locator("mark", { hasText: "appears" })).toHaveCount(1);

  // The passage lives in a "Make it yours" fold that starts closed.
  const fold = page.locator("details.bsp-more", { hasText: "It appears three times in the same expression" });
  await expect(fold).not.toHaveAttribute("open", "");
  await results(page).first().click();
  await expect(fold).toHaveAttribute("open", "");
  await expect(fold.locator("li", { hasText: "It appears three times" })).toBeInViewport();
  await expect(page.locator(".hsearch .hs-panel")).toBeHidden();
});

test("only the language on screen is searched", async ({ page }) => {
  await openHandbook(page, "en");
  // German-only wording of the same passage as above.
  await search(page).fill("steht dreimal im selben Ausdruck");
  await expect(page.locator(".hsearch .hs-meta")).toHaveText("No matches.");
  await expect(results(page)).toHaveCount(0);

  // Switching the language re-reads the page and runs the open search again.
  await page.click("#lang-de");
  await search(page).focus();
  await expect(results(page).first()).toBeVisible();
  await expect(page.locator(".hsearch .hs-meta")).toHaveText(/Treffer/);
  await expect(search(page)).toHaveAttribute("placeholder", "Suchen …");
});

test("the keyboard alone finds and opens a passage", async ({ page }) => {
  await openHandbook(page, "en");
  await page.locator("main").click({ position: { x: 5, y: 5 } });
  await page.keyboard.press("/");
  await expect(search(page)).toBeFocused();
  await page.keyboard.type("glossary");
  await expect(results(page).first()).toBeVisible();
  await expect(results(page).first()).toHaveAttribute("aria-selected", "true");
  await page.keyboard.press("ArrowDown");
  await expect(results(page).nth(1)).toHaveAttribute("aria-selected", "true");
  await page.keyboard.press("ArrowUp");
  await page.keyboard.press("Enter");
  await expect(page.locator(".hsearch .hs-panel")).toBeHidden();
  // The best hit for a chapter name is that chapter's heading.
  await expect(page.locator('#glossar h2[data-l="en"]')).toBeInViewport();

  // The field kept the focus through the jump; a click brings the list back.
  await search(page).click();
  await expect(page.locator(".hsearch .hs-panel")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.locator(".hsearch .hs-panel")).toBeHidden();
  await page.keyboard.press("Escape");
  await expect(search(page)).toHaveValue("");
});

test("Enter right after typing goes to the best hit of what was typed", async ({ page }) => {
  await openHandbook(page, "en");
  await search(page).click();
  // No wait between the last key and Enter: the list has not caught up yet.
  await page.keyboard.type("glossary", { delay: 0 });
  await page.keyboard.press("Enter");
  await expect(page.locator(".hsearch .hs-panel")).toBeHidden();
  await expect(page.locator('#glossar h2[data-l="en"]')).toBeInViewport();
});

test("accents and ß need not be typed", async ({ page }) => {
  await openHandbook(page, "de");
  await search(page).fill("prufung");
  await expect(results(page).first()).toBeVisible();
  await expect(results(page).first().locator("mark").first()).toHaveText(/prüfung/i);
});

test("one character is not a search", async ({ page }) => {
  await openHandbook(page, "en");
  await search(page).fill("a");
  await expect(page.locator(".hsearch .hs-meta")).toHaveText("Type at least two characters.");
  await expect(results(page)).toHaveCount(0);
});

test("a link can carry the search", async ({ page }) => {
  await page.goto("/handbuch.html?q=incident");
  await expect(search(page)).toHaveValue("incident");
  await expect(results(page).first()).toBeVisible();
});

test("on a phone the field takes its own row and the bar keeps its other controls", async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 700 });
  await openHandbook(page, "de");
  const field = await search(page).boundingBox();
  const toggle = await page.locator(".langtoggle").boundingBox();
  const brand = await page.locator(".topbar .brand").boundingBox();
  // The language toggle stays on the brand's row; the field sits below both.
  expect(Math.abs(toggle.y - brand.y)).toBeLessThan(brand.height);
  expect(field.y).toBeGreaterThan(toggle.y + toggle.height - 1);
  for (const el of await page.locator(".topbar > *:visible").all()) {
    const b = await el.boundingBox();
    expect(b.x + b.width).toBeLessThanOrEqual(375);
  }
  await search(page).fill("timer");
  await expect(results(page).first()).toBeVisible();
  const panel = await page.locator(".hsearch .hs-panel").boundingBox();
  expect(panel.x).toBeGreaterThanOrEqual(0);
  expect(panel.x + panel.width).toBeLessThanOrEqual(375);
});

test("the shop handbook has the same search", async ({ page }) => {
  await openHandbook(page, "de", "/shop-handbuch.html");
  await search(page).fill("katalog");
  await expect(results(page).first()).toBeVisible();
  await results(page).first().click();
  await expect(page.locator(".hsearch .hs-panel")).toBeHidden();
});
