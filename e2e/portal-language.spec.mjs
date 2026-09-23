// e2e for the portal's language switch (api/web/portal.js, against a fixture).
//
// What was reported from a live installation: "switching the language does not
// translate the products". It was true, and the cause was two screens away — the
// catalogue had been saved with the single language tag `de; en`, so every name
// was stored under a key no locale ever looks up and the page fell through to the
// first value it had, for every product, in both languages.
//
// The fallback itself was never broken, and reading the source says so. What
// reading cannot say is whether the SWITCH re-renders through it: that is a
// property of how the page is wired, and wiring is what fails. So this drives the
// real page and reads the DOM.
//
// The fixture carries the three cases that matter now that a half-translated
// product can be published at all: one product written in both languages, one
// only in German, one only in English.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/portal-language-harness.html");
  await page.waitForSelector(".cascade, .empty", { timeout: 10000 });
  page._errors = errors;
});

test.afterEach(async ({ page }) => {
  expect(page._errors, "no uncaught page errors").toEqual([]);
  // A route the page reads and the fixture does not serve would make every
  // assertion above pass against a page that never loaded its catalogue.
  const missed = await page.evaluate(() => window.__unmatched);
  expect(missed, "every route the page reads is served by the fixture").toEqual([]);
});

const switchTo = async (page, lang) => {
  await page.getByRole("button", { name: lang, exact: true }).click();
  await page.waitForTimeout(50);
};

const texts = (page) => page.locator("#app").innerText();

test("the switch translates the products, not only the page's own words",
  async ({ page }) => {
    await switchTo(page, "DE");
    let shown = await texts(page);
    expect(shown, "the German name").toContain("Notebook");
    expect(shown, "and not the English one beside it").not.toContain("Laptop");

    await switchTo(page, "EN");
    shown = await texts(page);
    expect(shown, "the English name").toContain("Laptop");
    expect(shown, "and not the German one").not.toContain("Notebook");
  });

test("a product written in only one language is shown in that language",
  async ({ page }) => {
    // The case the publish rule used to make impossible. A reader of the other
    // language sees the name that exists rather than a blank or an id — which is
    // the whole argument for letting it be published.
    await switchTo(page, "EN");
    let shown = await texts(page);
    expect(shown, "the German-only product, read in English").toContain("Bildschirm");
    expect(shown, "and never its id").not.toContain("de-only");

    await switchTo(page, "DE");
    shown = await texts(page);
    expect(shown, "the English-only product, read in German").toContain("Docking station");
    expect(shown, "and never its id").not.toContain("en-only");
  });

test("the two heading columns switch with everything else", async ({ page }) => {
  await switchTo(page, "DE");
  let shown = await texts(page);
  expect(shown).toContain("Arbeitsplatz");

  await switchTo(page, "EN");
  shown = await texts(page);
  expect(shown, "the heading in the reader's language").toContain("Workplace");
  expect(shown, "and not the key it is grouped by").not.toContain("Arbeitsplatz");
});

test("the catalogue's own name switches too", async ({ page }) => {
  await switchTo(page, "DE");
  expect(await texts(page)).toContain("Produktekatalog");
  await switchTo(page, "EN");
  expect(await texts(page)).toContain("Product catalogue");
});

test("what a product IS switches with what it is called", async ({ page }) => {
  // The description is a second map on the same record and reached by its own
  // function, which once had a rule of its own and showed an English reader
  // nothing at all. It is opened from the product, so the product is opened first.
  await switchTo(page, "EN");
  // The "i" beside the product, which is what opens the panel. Clicking the name
  // selects the product in the cascade and draws the column to its right.
  const row = page.locator(".cell", { hasText: "Laptop" }).first();
  await row.locator("button.info").click();
  await page.waitForTimeout(50);
  expect(await texts(page)).toContain("A mobile device.");

  await switchTo(page, "DE");
  expect(await texts(page)).toContain("Ein mobiles Gerät.");
});

test("the choice survives a reload", async ({ page }) => {
  // It is kept in this browser rather than on the account, which the page's own
  // comment calls the one place it knowingly falls short of its record. Worth
  // pinning anyway: a switch that does not survive the next page load is a switch
  // somebody presses every morning.
  await switchTo(page, "DE");
  await page.reload();
  await page.waitForSelector(".cascade, .empty");
  expect(await texts(page)).toContain("Notebook");
});
