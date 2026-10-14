// Renaming a catalogue, and changing the languages it is offered in.
//
// Both go through one form — `.cat-meta` on the catalogue detail screen — and both
// were broken in a way no Go test could see and no layout test would notice: the
// handler threw before it ever reached the API.
//
// `catalog-admin.js` has a `list` helper at the top that splits a comma-separated
// field into trimmed entries, and the save calls it for the languages. Inside the
// same function, a DOM element had been bound as `const list = view.querySelector(
// ".product-list")` — shadowing the helper for the whole of it. So the save called
// an HTMLElement and threw `list is not a function`, which the submit handler
// caught and showed as a toast. From the outside that reads as the server refusing
// the rename, which is the wrong place to look entirely.
//
// These drive the real detail view against the harness's mock, and check the two
// halves that failure had: what the page sends, and that it says nothing went wrong.
import { test, expect } from "@playwright/test";

async function open(page) {
  await page.setViewportSize({ width: 1600, height: 800 });
  await page.goto("/catalog-editor-harness.html");
  await page.waitForFunction(() => window.__ready === true);
  await page.evaluate(() => window.__mount());
  await expect(page.locator(".cat-meta")).toBeVisible();
  // The mount itself reads a handful of routes; only what happens after matters.
  await page.evaluate(() => { window.__sent.length = 0; window.__toasts.length = 0; });
}

const patched = (page) => page.evaluate(() =>
  window.__sent.filter((c) => String(c.method).toUpperCase() === "PATCH"));

test("renaming a catalogue reaches the server", async ({ page }) => {
  await open(page);

  await page.fill('.cat-meta [name="t-de"]', "Arbeitsplatz");
  await page.locator('.cat-meta button[type=submit]').click();

  await expect.poll(async () => (await patched(page)).length).toBe(1);
  const [call] = await patched(page);
  expect(call.url).toContain("/api/v1/catalogs/c1");
  expect(call.body.texts.de).toBe("Arbeitsplatz");
  // The other language is untouched rather than dropped: the form renders a box per
  // declared language and a save is a patch of what it rendered.
  expect(call.body.texts.en).toBe("Catalogue");
});

test("the languages are sent as a list, trimmed", async ({ page }) => {
  // The field this broke on. It is one comma-separated box and the record wants an
  // array, so the save has to split it — which is the call that was hitting a DOM
  // element instead of the helper.
  await open(page);

  await page.fill('.cat-meta [name="languages"]', "de, fr ,it");
  await page.locator('.cat-meta button[type=submit]').click();

  await expect.poll(async () => (await patched(page)).length).toBe(1);
  const [call] = await patched(page);
  expect(call.body.languages).toEqual(["de", "fr", "it"]);
});

test("a save that works says nothing went wrong", async ({ page }) => {
  // The other half, and the one that made this look like a server problem. The
  // handler catches what it throws and toasts the message, so a page that cannot
  // run reports itself as a refusal. No error toast is the assertion; the specific
  // wording of the old one is checked too, because that is what was reported.
  await open(page);

  await page.fill('.cat-meta [name="t-de"]', "Arbeitsplatz");
  await page.locator('.cat-meta button[type=submit]').click();

  await expect.poll(async () => (await patched(page)).length).toBe(1);
  const toasts = await page.evaluate(() => window.__toasts);
  const errors = toasts.filter((t) => t.kind === "err");
  expect(errors, `the save reported: ${JSON.stringify(errors)}`).toEqual([]);
  expect(JSON.stringify(toasts)).not.toContain("is not a function");
});
