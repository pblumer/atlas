// Every palette entry carries a mark, and the armed one is lit.
//
// The marks are miniatures of the shapes they produce, drawn as inline SVG in the
// stylesheet rather than vendored: OMG fixes the shapes on the diagram and says
// nothing about a toolbar, so there is no official set to take.
//
// What is worth a test is not how they look — it is that every entry has one. The
// palette is built from the *served* subset, so a stereotype or a relationship added
// on the server arrives here with no rule to draw it, and the failure is a button
// that renders as an empty square. That is the drift this pins.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/infomodel-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await expect(page.locator(".uml-class").first()).toBeVisible();
});

// What the entry's ::before actually paints. A mark arrives either as a background
// image (a classifier, painted in the colour of its kind) or as a mask (a mode, a
// stencil that takes the palette's colour) — and an entry with neither is the defect.
const marks = (page) => page.evaluate(() =>
  [...document.querySelectorAll(".djs-palette .uml-pi")].map((el) => {
    const s = getComputedStyle(el, "::before");
    return {
      id: [...el.classList].find((c) => c.startsWith("uml-pi-"))?.slice(7) || "",
      image: s.backgroundImage,
      mask: s.maskImage || s.webkitMaskImage,
      colour: s.backgroundColor,
    };
  }));

test("every entry the palette offers is drawn", async ({ page }) => {
  const found = await marks(page);
  // The harness serves the whole subset, so this is every entry there is.
  expect(found.length).toBeGreaterThanOrEqual(9);
  for (const m of found) {
    const painted = m.image && m.image !== "none";
    const stencilled = m.mask && m.mask !== "none";
    expect(painted || stencilled,
      `the ${m.id} entry paints nothing — it renders as an empty button`).toBe(true);
  }
  expect(page.__errors).toEqual([]);
});

test("a classifier is painted and a relationship is a stencil", async ({ page }) => {
  const by = Object.fromEntries((await marks(page)).map((m) => [m.id, m]));

  // A classifier is a button with no state, so its kind can live in the colour.
  for (const id of ["businessObject", "valueType", "enumeration", "store"]) {
    expect(by[id].image, `${id} should be painted`).not.toBe("none");
  }
  // A relationship is a mode: one is armed while the next two clicks draw that line,
  // and the armed entry is recoloured to say so — which a baked colour cannot do.
  for (const id of ["association", "aggregation", "composition", "generalization"]) {
    expect(by[id].mask, `${id} should be a stencil`).not.toBe("none");
  }
});

test("the mark lights on hover, again when armed, and goes back when neither", async ({ page }) => {
  const composition = page.locator('.djs-palette [data-action="composition"]');
  const inkOf = () => composition.evaluate((el) => getComputedStyle(el, "::before").backgroundColor);
  // Reading the resting colour means reading it with the pointer elsewhere: hovering
  // lights the mark too, so a read taken under the cursor is a read of the hover.
  const away = () => page.mouse.move(400, 400);

  await away();
  const resting = await inkOf();
  await composition.hover();
  const hovered = await inkOf();
  expect(hovered).not.toBe(resting);

  await composition.click();
  await expect(composition).toHaveClass(/active/);
  // The whole reason the modes are stencils rather than painted: a mode nobody can
  // see is a trap, because the next two clicks do something the reader did not ask
  // for. Armed, the entry fills with the accent and the mark reverses out of it.
  const armed = await inkOf();
  expect(armed).not.toBe(hovered);
  expect(armed).not.toBe(resting);

  await composition.click();
  await expect(composition).not.toHaveClass(/active/);
  await away();
  expect(await inkOf()).toBe(resting);
  expect(page.__errors).toEqual([]);
});
