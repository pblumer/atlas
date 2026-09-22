// e2e for the two headings a maintainer types into one box each
// (api/web/catalog-admin.js, headingList and headingFrom through productBody).
//
// ADR-draft-translatable-catalogue-headings. The category and the product group
// used to be one string apiece, read
// the same in every language a catalogue offers — so a catalogue kept in four
// languages translated every product name and then filed them all under a German
// word. They are now a key, which is what the portal groups by, and a wording per
// language tag, which is what it shows.
//
// One box writes both, and the convention is positional: the wordings in the order
// the catalogue declares its languages, separated by semicolons. Positional means
// the box has to round-trip exactly — what it renders, saved unchanged, must store
// what it read — or a maintainer who opens a product and presses save loses
// wordings without touching them. That is the property these pin, and it is not
// one the Console can be clicked into showing.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/catalog-product-body-harness.html");
  await page.waitForFunction(() => window.__ready === true);
  page._errors = errors;
});

test.afterEach(async ({ page }) => {
  expect(page._errors, "no uncaught page errors").toEqual([]);
});

const build = (page, values, stored, langs) =>
  page.evaluate(([v, s, l]) => { window.set(v); return window.build(s, l); },
    [values, stored, langs]);

const box = (page, key, texts, langs) =>
  page.evaluate(([k, t, l]) => window.headingBox(k, t, l), [key, texts, langs]);

test("a semicolon list is read against the languages the catalogue declares",
  async ({ page }) => {
    const body = await build(page, {
      "t-de": "Notebook",
      category: "Arbeitsplatz; Poste de travail; Workplace; Postazione",
      productGroup: "Mobile Geräte; Appareils mobiles; Mobile devices; Dispositivi",
    }, {}, ["de", "fr", "en", "it"]);

    // The first is the key: what the portal groups by, and what a release already
    // published holds. It is deliberately not "the German one" — it is position
    // one of the catalogue's own language list.
    expect(body.category).toBe("Arbeitsplatz");
    expect(body.categoryTexts).toEqual({
      de: "Arbeitsplatz", fr: "Poste de travail",
      en: "Workplace", it: "Postazione",
    });
    expect(body.productGroup).toBe("Mobile Geräte");
    expect(body.productGroupTexts.it).toBe("Dispositivi");
  });

test("one wording is a heading that is not translated", async ({ page }) => {
  // The state of every product written before the field existed, and the one a
  // single-language catalogue wants. Storing {de: "Arbeitsplatz"} instead would
  // refuse the catalogue's own publish the day somebody adds a second language.
  const body = await build(page, { category: "Arbeitsplatz" }, {}, ["de", "fr"]);
  expect(body.category).toBe("Arbeitsplatz");
  expect(body.categoryTexts).toEqual({});
});

test("a trailing semicolon is not a half-translated heading", async ({ page }) => {
  // Publishing refuses a heading worded in German and not French, which is right.
  // A maintainer who typed a separator and stopped has not worded anything twice,
  // and reading the stray semicolon as a translation would refuse their publish
  // for a keystroke.
  const body = await build(page, { category: "Arbeitsplatz; " }, {}, ["de", "fr"]);
  expect(body.category).toBe("Arbeitsplatz");
  expect(body.categoryTexts).toEqual({});
});

test("the box round-trips: rendering and saving unchanged stores what it read",
  async ({ page }) => {
    const langs = ["de", "fr", "en"];
    const stored = {
      category: "Arbeitsplatz",
      categoryTexts: { de: "Arbeitsplatz", fr: "Poste de travail", en: "Workplace" },
      productGroup: "Mobile Geräte",
      productGroupTexts: { de: "Mobile Geräte", fr: "Appareils mobiles", en: "Mobile devices" },
    };
    const category = await box(page, stored.category, stored.categoryTexts, langs);
    const group = await box(page, stored.productGroup, stored.productGroupTexts, langs);
    expect(category).toBe("Arbeitsplatz; Poste de travail; Workplace");

    const body = await build(page, { category, productGroup: group }, stored, langs);
    expect(body.category).toBe(stored.category);
    expect(body.categoryTexts).toEqual(stored.categoryTexts);
    expect(body.productGroup).toBe(stored.productGroup);
    expect(body.productGroupTexts).toEqual(stored.productGroupTexts);
  });

test("an untranslated heading round-trips as the key alone", async ({ page }) => {
  // The other half of the round trip, and the one the installed base is in. A box
  // that rendered "Arbeitsplatz; ; " here would store a half-translated heading
  // the moment somebody pressed save on an untouched product.
  const langs = ["de", "fr"];
  const category = await box(page, "Arbeitsplatz", undefined, langs);
  expect(category).toBe("Arbeitsplatz");

  const body = await build(page, { category }, { category: "Arbeitsplatz" }, langs);
  expect(body.category).toBe("Arbeitsplatz");
  expect(body.categoryTexts).toEqual({});
});

test("a rendered control can clear the wordings it shows", async ({ page }) => {
  // A save is a full replace, so a box that shows four wordings has to be able to
  // take them back — the property the whole form is held to.
  const stored = {
    category: "Arbeitsplatz",
    categoryTexts: { de: "Arbeitsplatz", fr: "Poste de travail" },
  };
  const body = await build(page, { category: "Arbeitsplatz" }, stored, ["de", "fr"]);
  expect(body.categoryTexts).toEqual({});

  // And emptying the box takes the heading itself with them.
  const gone = await build(page, { category: "" }, stored, ["de", "fr"]);
  expect(gone.category).toBe("");
  expect(gone.categoryTexts).toEqual({});
});

test("a wording in a language this catalogue does not declare survives a save here",
  async ({ page }) => {
    // A product is referenced by catalogues rather than owned by one (ADR-0315),
    // so the Italian wording belongs to the catalogue next door. This form renders
    // the boxes THIS catalogue declares, and a save made here writes only those —
    // the rule the names and the descriptions above already follow.
    const stored = {
      category: "Arbeitsplatz",
      categoryTexts: { de: "Arbeitsplatz", fr: "Poste de travail", it: "Postazione" },
    };
    const body = await build(page,
      { category: "Arbeitsplatz; Poste de travail moderne" }, stored, ["de", "fr"]);
    expect(body.categoryTexts).toEqual({
      de: "Arbeitsplatz", fr: "Poste de travail moderne", it: "Postazione",
    });
  });
