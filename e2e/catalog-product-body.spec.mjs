// e2e for what the Console's product form actually posts
// (api/web/catalog-admin.js, productBody and variantLines).
//
// The gap these pin: the form rendered eight of a product's fields and the portal
// read twelve. There was no control for the orderable shapes, the search terms, the
// eligible groups or the ceiling on how long a right may last — so a portal that
// blocks an order until a shape is chosen, a search that reads the keywords, an
// order refused for an ineligible recipient and a right that expires were all
// driven by data no maintainer could see, let alone change. They were settable only
// over REST or MCP.
//
// A save is a full replace, which makes this two properties and not one: a control
// that is rendered must be able to CLEAR its field, and a field that is not
// rendered must survive the save UNTOUCHED. The form used to get the second half
// right by never writing any of them.
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

test("the four controls the form did not have reach the body", async ({ page }) => {
  const body = await build(page, {
    "t-de": "Notebook",
    keywords: "Laptop, mobiles Gerät , M365",
    variants: "klein = 13 Zoll\ngross = 15 Zoll",
    eligible: ["grp_dev"],
    maxDays: "90",
    state: "active",
  });
  expect(body.keywords, "trimmed, and the empty term dropped")
    .toEqual(["Laptop", "mobiles Gerät", "M365"]);
  expect(body.variants).toEqual([
    { id: "klein", texts: { de: "13 Zoll" } },
    { id: "gross", texts: { de: "15 Zoll" } },
  ]);
  expect(body.eligible).toEqual(["grp_dev"]);
  expect(body.maxDays).toBe(90);
});

test("a control that is rendered can empty its field", async ({ page }) => {
  // What the product carries before the save. Every one of the four is set, and the
  // form is submitted with all four controls empty.
  const stored = {
    keywords: ["Laptop"],
    variants: [{ id: "klein", texts: { de: "13 Zoll" } }],
    eligible: ["grp_dev", "grp_ops"],
    maxDays: 90,
  };
  const body = await build(page, {
    "t-de": "Notebook", keywords: "", variants: "", eligible: [], maxDays: "",
  }, stored);
  expect(body.keywords).toEqual([]);
  expect(body.variants).toEqual([]);
  expect(body.eligible).toEqual([]);
  // Zero is a statement here and not an absence: the right does not end. It is the
  // ordinary case, which is why an empty field reads as it and not as NaN.
  expect(body.maxDays).toBe(0);
});

test("the field the form does not render survives the save", async ({ page }) => {
  // The orderable window has no control, deliberately: nothing enforces it yet, and
  // a control promising a window that is never checked would be worse than none. So
  // it has to ride through untouched — the seed-and-overlay rule the body is built
  // on, and the reason a save cannot be assembled from the controls alone.
  const stored = {
    lifecycle: { from: 1735689600000000000, until: 1767225600000000000 },
    createdAt: 1700000000000000000,
    revision: 7,
  };
  const body = await build(page, { "t-de": "Notebook" }, stored);
  expect(body.lifecycle).toEqual(stored.lifecycle);
  expect(body.createdAt, "a replace would reset the creation date to today")
    .toBe(stored.createdAt);
  expect(body.revision, "the precondition that turns a colleague's edit into a refusal")
    .toBe(7);
});

test("a shape keeps the name it carries in a language this catalogue does not declare",
  async ({ page }) => {
    // The rule the product's own texts follow, one level down. A product is shared
    // between catalogues; this form renders one box per language THIS catalogue
    // declares, and a name in a language it does not declare belongs to a catalogue
    // that does. Rebuilding the variants from the textarea alone would delete it.
    const stored = { variants: [{ id: "gross", texts: { de: "15 Zoll", fr: "15 pouces" } }] };
    const body = await build(page, { variants: "gross = 15 Zoll" }, stored, ["de"]);
    expect(body.variants).toEqual([
      { id: "gross", texts: { de: "15 Zoll", fr: "15 pouces" } },
    ]);
  });

test("a name removed from a declared language is removed from the shape", async ({ page }) => {
  // The other direction of the same rule: emptying what is rendered clears it, or a
  // name could be added and never taken away.
  const stored = { variants: [{ id: "gross", texts: { de: "15 Zoll", en: "15 inch" } }] };
  const body = await build(page, { variants: "gross = de:15 Zoll" }, stored, ["de", "en"]);
  expect(body.variants).toEqual([{ id: "gross", texts: { de: "15 Zoll" } }]);
});

test("the shapes round-trip through the textarea", async ({ page }) => {
  // Rendered, read back, and rendered again: the property that makes editing one
  // line safe. A round trip that lost a name would lose it on the save of an
  // unrelated field, which is the failure nobody notices until an order shows an id
  // where a name belongs.
  for (const langs of [["de"], ["de", "en"]]) {
    const variants = langs.length === 1
      ? [{ id: "klein", texts: { de: "13 Zoll" } }, { id: "gross", texts: { de: "15 Zoll" } }]
      : [{ id: "klein", texts: { de: "13 Zoll", en: "13 inch" } },
        { id: "gross", texts: { de: "15 Zoll", en: "15 inch" } }];

    const text = await page.evaluate(([v, l]) => window.lines(v, l), [variants, langs]);
    const body = await build(page, { variants: text }, { variants }, langs);
    expect(body.variants, `round trip for ${langs.join("+")}`).toEqual(variants);

    const again = await page.evaluate(([v, l]) => window.lines(v, l), [body.variants, langs]);
    expect(again, `rendering is stable for ${langs.join("+")}`).toBe(text);
  }
});

test("a shape line with no name is kept as an id rather than swallowed", async ({ page }) => {
  // The rule parseTargets follows for a line with no colon: a line this form
  // swallowed would be a shape somebody believes they entered. The portal falls back
  // to the id, so the omission is visible instead of silent.
  const body = await build(page, { variants: "gross\nklein =" });
  expect(body.variants).toEqual([
    { id: "gross", texts: {} },
    { id: "klein", texts: {} },
  ]);
});

test("the ceiling reads a whole number of days and nothing else", async ({ page }) => {
  for (const [typed, want] of [["90", 90], ["", 0], ["0", 0], ["90.6", 90], ["-5", 0]]) {
    const body = await build(page, { maxDays: typed });
    expect(body.maxDays, `maxDays from ${JSON.stringify(typed)}`).toBe(want);
    expect(Number.isInteger(body.maxDays), "always a whole number").toBe(true);
  }
});

test("the eligibility picker and the id field it degrades to are both read",
  async ({ page }) => {
    const picked = await build(page, { eligible: ["grp_dev", "grp_ops"] });
    expect(picked.eligible).toEqual(["grp_dev", "grp_ops"]);

    // No directory: the real form falls back to a comma-separated id field, because a
    // picker with no options and no explanation reads as "there are no groups" — an
    // answer to a question it never asked.
    await page.evaluate(() => window.degrade());
    const typed = await build(page, { "eligible-raw": " grp_dev , , grp_ops " });
    expect(typed.eligible, "trimmed, and the empty entry dropped")
      .toEqual(["grp_dev", "grp_ops"]);
  });

test("the two headings and the price still reach the body", async ({ page }) => {
  // The fields the form already had, kept under test because the body they are
  // assembled in was moved out of the submit handler to be testable at all.
  const body = await build(page, {
    "t-de": "Notebook", category: " Arbeitsplatz ", productGroup: " Mobile Geräte ",
    price: " CHF 1'200.– ", multipleAllowed: true, state: "active",
    akind: "role", "aref-role": "grp_it",
  });
  expect(body.category).toBe("Arbeitsplatz");
  expect(body.productGroup).toBe("Mobile Geräte");
  expect(body.price).toBe("CHF 1'200.–");
  expect(body.multipleAllowed).toBe(true);
  expect(body.state).toBe("active");
  expect(body.approval).toEqual({ kind: "role", ref: "grp_it" });
});
