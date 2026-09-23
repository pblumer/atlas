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

// openWith reloads the harness against a catalogue kept in the given languages.
const openWith = async (page, langs) => {
  await page.goto(`/portal-language-harness.html?langs=${encodeURIComponent(langs.join(","))}`);
  await page.waitForSelector(".cascade, .empty", { timeout: 10000 });
};

const langButtons = (page) => page.locator(".langs button").allTextContents();

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

test("a catalogue declared with regions is read by a reader without one",
  async ({ page }) => {
    // The catalogue says de-DE and en-EN; this page's locale is de or en, because
    // its own words live in a message catalogue keyed by the language alone. A
    // lookup for the whole tag finds neither, falls through to the first value the
    // product has, and shows one word in both languages — which is the defect that
    // started all of this, reached down a different road.
    await switchTo(page, "DE");
    let shown = await texts(page);
    expect(shown, "the German name under de-DE").toContain("Dockingstation");
    expect(shown, "and not the English one beside it").not.toContain("Docking cradle");

    await switchTo(page, "EN");
    shown = await texts(page);
    expect(shown, "the English name under en-EN").toContain("Docking cradle");
    expect(shown, "and not the German one").not.toContain("Dockingstation");
  });

test("a regional heading switches with the rest", async ({ page }) => {
  // The heading wordings are keyed the same way and reached through the same
  // function, so they are the cheapest place for the correction to be incomplete.
  //
  // A heading no other product in the fixture carries, because a word already on
  // the screen from somewhere else would make this pass without proving anything
  // — which is what it did when it was first written.
  await switchTo(page, "EN");
  let shown = await texts(page);
  expect(shown, "the heading under en-EN").toContain("Accessories");
  expect(shown, "and not the German one beside it").not.toContain("Zubehör");

  await switchTo(page, "DE");
  shown = await texts(page);
  expect(shown, "the heading under de-DE").toContain("Zubehör");
  expect(shown, "and not the English one").not.toContain("Accessories");
});

test("the switch offers the languages the catalogue is kept in", async ({ page }) => {
  // Not the two this page happens to be translated into. A catalogue kept only in
  // German showed an EN button that turned the furniture English and left every
  // product name German — a half-translated screen offered by the page itself.
  await openWith(page, ["de"]);
  expect(await langButtons(page),
    "a catalogue kept in one language needs no switch").toEqual([]);

  await openWith(page, ["de", "en"]);
  expect(await langButtons(page)).toEqual(["DE", "EN"]);
});

test("a language this page cannot render is not offered", async ({ page }) => {
  // ADR-0313: every string the portal renders exists in every locale it offers.
  // The page speaks German, English, French and Italian; a catalogue kept in
  // Romansh would get no RM button, because the furniture has no Romansh and the
  // button would promise a page that does not exist.
  await openWith(page, ["de-DE", "en-EN", "rm-CH"]);
  const shown = await langButtons(page);
  expect(shown, "the two it can render, as the catalogue spells them")
    .toEqual(["DE-DE", "EN-EN"]);
  expect(shown.join(" "), "and no Romansh").not.toContain("RM");
});

test("all four languages the page speaks are offered and render", async ({ page }) => {
  await openWith(page, ["de", "fr", "it", "en"]);
  expect(await langButtons(page)).toEqual(["DE", "FR", "IT", "EN"]);

  // Each one renders its own furniture rather than the key or the German. The
  // navigation is the cheapest thing to check and it is on every screen.
  for (const [button, word] of [["FR", "Mes prestations"], ["IT", "Le mie prestazioni"],
    ["DE", "Meine Leistungen"], ["EN", "My services"]]) {
    await page.getByRole("button", { name: button, exact: true }).click();
    await page.waitForTimeout(50);
    const shown = await page.locator("#app").innerText();
    expect(shown, `the ${button} furniture`).toContain(word);
    expect(shown, `no untranslated key under ${button}`)
      .not.toMatch(/\b(nav|portal|basket|find)\.[a-z]/);
  }
});

test("the chosen language settles onto one the catalogue is kept in",
  async ({ page }) => {
    // The choice is made before the catalogue is read, so it is a language and not
    // yet one of this catalogue's tags. A reader on English meeting a catalogue
    // kept in en-EN should be reading it, not falling through to whatever came
    // first — and the switch has to show it as chosen.
    await openWith(page, ["de-DE", "en-EN"]);
    await page.getByRole("button", { name: "EN-EN", exact: true }).click();
    await page.waitForTimeout(50);

    expect(await page.locator(".langs button.on").innerText()).toBe("EN-EN");
    expect(await page.locator("#app").innerText()).toContain("Docking cradle");
  });

test("the page's own words stay readable in a regional locale", async ({ page }) => {
  // A locale of de-DE has no message catalogue of its own; the German one is what
  // it renders. Read the wrong way this shows raw keys like `nav.catalog` across
  // the whole page, which is why it is worth a test of its own.
  await openWith(page, ["de-DE", "en-EN"]);
  // The language is stated rather than assumed: this harness runs in a browser
  // whose own list is English, so the choice settles on en-EN and a test that
  // expected German would be testing the settling, not the rendering.
  await page.getByRole("button", { name: "DE-DE", exact: true }).click();
  await page.waitForTimeout(50);

  const shown = await page.locator("#app").innerText();
  expect(shown, "no untranslated key is rendered").not.toMatch(/\b(nav|portal|basket)\.[a-z]/);
  expect(shown, "the German furniture under a de-DE locale").toContain("Meine Aufträge");
});
