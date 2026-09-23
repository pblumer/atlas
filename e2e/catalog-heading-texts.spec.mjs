// e2e for what a product says per language, and how the Console writes it
// (api/web/catalog-admin.js, headingBoxes and headingFrom through productBody).
//
// ADR-0412, as amended. A catalogue declares the languages it is kept in, and
// every text on a product is a map keyed by those tags. The form draws that list:
// one box per declared language, side by side, each labelled with its tag.
//
// The two headings are the interesting half, because each is TWO things on the
// record — the key everything groups by, and a wording per language that is shown
// — and one row of boxes writes both. The first box that has anything in it is the
// key. That has to round-trip exactly: what the row renders, saved unchanged, must
// store what it read, or a maintainer who opens a product and presses save loses
// wordings without touching them.
//
// It replaces a convention that packed the wordings into one box separated by
// semicolons. That convention was compact and it was a trap: the position-to-
// language mapping was invisible, and the separator leaked one screen up — a live
// catalogue was saved with the single language tag `de; en`, after which its
// portal's language switch did nothing at all, for every product, silently.
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

const boxes = (page, key, texts, langs) =>
  page.evaluate(([k, t, l]) => window.headingBoxes(k, t, l), [key, texts, langs]);

test("every declared language gets its own box, and all of them are read",
  async ({ page }) => {
    const body = await build(page, {
      "t-de": "Notebook", "t-fr": "Portable", "t-en": "Laptop", "t-it": "Portatile",
      "cat-de": "Arbeitsplatz", "cat-fr": "Poste de travail",
      "cat-en": "Workplace", "cat-it": "Postazione",
      "grp-de": "Mobile Geräte", "grp-fr": "Appareils mobiles",
      "grp-en": "Mobile devices", "grp-it": "Dispositivi",
    }, {}, ["de", "fr", "en", "it"]);

    expect(body.texts).toEqual({
      de: "Notebook", fr: "Portable", en: "Laptop", it: "Portatile",
    });
    // The key is the first box that has anything in it, in the order the
    // catalogue declares its languages — not "the German one".
    expect(body.category).toBe("Arbeitsplatz");
    expect(body.categoryTexts).toEqual({
      de: "Arbeitsplatz", fr: "Poste de travail",
      en: "Workplace", it: "Postazione",
    });
    expect(body.productGroup).toBe("Mobile Geräte");
    expect(body.productGroupTexts.it).toBe("Dispositivi");
  });

test("a box for a language the catalogue does not declare is never read",
  async ({ page }) => {
    // The harness carries four boxes and the catalogue declares two. A form that
    // read the boxes it happens to find rather than the languages it was given
    // would write texts under tags this catalogue knows nothing about.
    const body = await build(page, {
      "t-de": "Notebook", "t-en": "Laptop", "t-it": "Portatile",
      "cat-de": "Arbeitsplatz", "cat-it": "Postazione",
    }, {}, ["de", "en"]);

    expect(body.texts).toEqual({ de: "Notebook", en: "Laptop" });
    expect(body.categoryTexts).toEqual({});
    expect(body.category).toBe("Arbeitsplatz");
  });

test("one wording is a heading that is not translated", async ({ page }) => {
  // The state of every product written before the wordings existed, and the one a
  // single-language catalogue wants. Storing {de: "Arbeitsplatz"} instead would
  // put the whole installed base on the list of things still to translate.
  const body = await build(page, { "cat-de": "Arbeitsplatz" }, {}, ["de", "en"]);
  expect(body.category).toBe("Arbeitsplatz");
  expect(body.categoryTexts).toEqual({});
});

test("filling only the second box still yields a heading", async ({ page }) => {
  // The key is the first box with anything in it and not the first box. Read the
  // other way, this would store a wording with no key to group by — which
  // publishing refuses, for a reason the maintainer could not have seen coming.
  const body = await build(page, { "cat-en": "Workplace" }, {}, ["de", "en"]);
  expect(body.category).toBe("Workplace");
  expect(body.categoryTexts).toEqual({});
});

test("the row round-trips: rendering and saving unchanged stores what it read",
  async ({ page }) => {
    const langs = ["de", "fr", "en"];
    const stored = {
      category: "Arbeitsplatz",
      categoryTexts: { de: "Arbeitsplatz", fr: "Poste de travail", en: "Workplace" },
      productGroup: "Mobile Geräte",
      productGroupTexts: { de: "Mobile Geräte", fr: "Appareils mobiles", en: "Mobile devices" },
    };
    const cat = await boxes(page, stored.category, stored.categoryTexts, langs);
    expect(cat).toEqual(stored.categoryTexts);

    const grp = await boxes(page, stored.productGroup, stored.productGroupTexts, langs);
    const body = await build(page, {
      "cat-de": cat.de, "cat-fr": cat.fr, "cat-en": cat.en,
      "grp-de": grp.de, "grp-fr": grp.fr, "grp-en": grp.en,
    }, stored, langs);

    expect(body.category).toBe(stored.category);
    expect(body.categoryTexts).toEqual(stored.categoryTexts);
    expect(body.productGroup).toBe(stored.productGroup);
    expect(body.productGroupTexts).toEqual(stored.productGroupTexts);
  });

test("an untranslated heading round-trips as the key in the first box",
  async ({ page }) => {
    // The other half of the round trip, and the one the installed base is in. A
    // row that rendered the key into every box would store a fully translated
    // heading the moment somebody pressed save on an untouched product — four
    // identical wordings, and a product that has left the list of things to do.
    const langs = ["de", "en"];
    const cat = await boxes(page, "Arbeitsplatz", undefined, langs);
    expect(cat).toEqual({ de: "Arbeitsplatz" });

    const body = await build(page, { "cat-de": cat.de || "", "cat-en": "" },
      { category: "Arbeitsplatz" }, langs);
    expect(body.category).toBe("Arbeitsplatz");
    expect(body.categoryTexts).toEqual({});
  });

test("a rendered control can clear the wordings it shows", async ({ page }) => {
  // A save is a full replace, so a row that shows four wordings has to be able to
  // take them back — the property the whole form is held to.
  const stored = {
    category: "Arbeitsplatz",
    categoryTexts: { de: "Arbeitsplatz", fr: "Poste de travail" },
  };
  const body = await build(page, { "cat-de": "Arbeitsplatz", "cat-fr": "" },
    stored, ["de", "fr"]);
  expect(body.categoryTexts).toEqual({});

  const gone = await build(page, { "cat-de": "", "cat-fr": "" }, stored, ["de", "fr"]);
  expect(gone.category).toBe("");
  expect(gone.categoryTexts).toEqual({});
});

test("a wording in a language this catalogue does not declare survives a save here",
  async ({ page }) => {
    // A product is referenced by catalogues rather than owned by one (ADR-0315),
    // so the Italian wording belongs to the catalogue next door. This form draws
    // the boxes THIS catalogue declares, and a save made here writes only those —
    // the rule the names and the descriptions above already follow.
    const stored = {
      category: "Arbeitsplatz",
      categoryTexts: { de: "Arbeitsplatz", fr: "Poste de travail", it: "Postazione" },
    };
    const body = await build(page,
      { "cat-de": "Arbeitsplatz", "cat-fr": "Poste de travail moderne" },
      stored, ["de", "fr"]);
    expect(body.categoryTexts).toEqual({
      de: "Arbeitsplatz", fr: "Poste de travail moderne", it: "Postazione",
    });
  });
