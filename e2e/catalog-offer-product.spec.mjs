// End-to-end coverage for offering a product a catalogue does not carry yet
// (api/web/catalog-admin.js, the "Offer an existing product" button).
//
// The case behind it, reported from the running server: the button opened a
// window.prompt that printed the products this catalogue does not offer as lines
// of text and asked for an id back. Nothing in that list could be clicked — the
// entries are prompt body, not controls — so picking a product meant reading an id
// off the wall of lines and typing it exactly, and a typo was answered with "No
// product with that id" and nothing else. Past a handful of lines a browser
// truncates a prompt body, so the products that sort last were not in the list
// somebody was told to type from.
//
// So the test that matters is the pointer: open the picker, choose a product with
// the mouse, and see the catalogue ask the server to offer exactly that one.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/catalog-offer-product-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
});

const result = (page) => page.locator("#result");

test("every product the catalogue does not offer can be chosen with the pointer", async ({ page }) => {
  await page.locator('button[data-act="add-existing"]').click();

  const select = page.locator("#pick-opt");
  await expect(select).toBeVisible();
  // The four products the catalogue does not carry, and only those: the two it
  // already offers are not offerable again.
  await expect(select.locator("option")).toHaveCount(4);
  await expect(select.locator("option").first()).toHaveText(/Benutzerkonto für interne Mitarbeitende/);
  await expect(select.locator("option").last()).toHaveText(/Zusatzbildschirm/);

  // Chosen by name with the pointer, never by typing an id.
  await select.selectOption({ label: "Zusatzbildschirm — zusatzbildschirm" });
  await page.locator("[data-ok]").click();

  // The write is the whole item list plus the one picked, carrying the revision
  // the page was rendered at so a concurrent addition is refused rather than lost.
  await expect(result(page)).toHaveText(/"items":\["2027-benutzeraccount-intern","2027-interner-account","zusatzbildschirm"\]/);
  await expect(result(page)).toHaveText(/"revision":7/);
  expect(page.__errors).toEqual([]);
});

test("a product is named before it is identified", async ({ page }) => {
  // The label leads with the name a person reads in the catalogue and carries the
  // id behind it, because the id is what the prompt used to lead with — and an id
  // is the one part of a product nobody browsing the catalogue knows by heart.
  await page.locator('button[data-act="add-existing"]').click();
  await expect(page.locator("#pick-opt option").nth(1)).toHaveText("Laptop Entwickler — laptop-entwickler");
});

test("cancelling offers nothing", async ({ page }) => {
  await page.locator('button[data-act="add-existing"]').click();
  await page.locator("[data-cancel]").click();
  await expect(page.locator("#pick-opt")).toHaveCount(0);
  await expect(result(page)).toHaveText("—");

  // Escape is the same answer, and the dialog leaves nothing behind either way.
  await page.locator('button[data-act="add-existing"]').click();
  await page.keyboard.press("Escape");
  await expect(page.locator(".modal-ov")).toHaveCount(0);
  await expect(result(page)).toHaveText("—");
});
