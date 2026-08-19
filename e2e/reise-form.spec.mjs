import { test, expect } from "@playwright/test";

// Verifies the two in-form ("Ebene A") behaviours of examples/reisebuchung's
// start form against the REAL vendored form-js bundle:
//   1. conditional field — Reisepass-Nr. is shown only for a Fernreise,
//   2. cascading select — the Ziel options depend on the chosen Reiseart.
// Both run purely client-side (form-js FEEL), with no Atlas engine involved.

async function openZielOptions(page) {
  // The Ziel select is the second select widget; click its display to open the
  // dropdown, then read the offered option labels.
  await page.locator(".fjs-form-field-select").nth(1).locator(".fjs-select-display").click();
  await page.locator(".fjs-dropdownlist-item").first().waitFor({ timeout: 4000 });
  return page.locator(".fjs-dropdownlist-item").allTextContents();
}

test("schema imports cleanly and renders all fields", async ({ page }) => {
  const pageErrors = [];
  page.on("pageerror", e => pageErrors.push(String(e)));
  await page.goto("/reise-form-harness.html");
  await page.waitForFunction(() => window.ready);

  expect(await page.evaluate(() => window.__err)).toBeNull();
  expect(pageErrors).toEqual([]);
  const s = await page.evaluate(() => window.readState());
  expect(s.hasReiseart && s.hasZiel && s.hasGeburtsdatum).toBe(true);
});

test("Fernreise: Reisepass field shown, Ziel cascades to far destinations", async ({ page }) => {
  await page.goto("/reise-form-harness.html");
  await page.waitForFunction(() => window.ready);
  await page.evaluate(() => window.render({ reiseart: "Fernreise" }));

  const s = await page.evaluate(() => window.readState());
  expect(s.hasReisepass).toBe(true); // conditional.hide: shown for Fernreise

  const opts = await openZielOptions(page);
  expect(opts).toEqual(["Thailand", "USA", "Japan"]);
});

test("Inland: Reisepass field hidden, Ziel cascades to domestic destinations", async ({ page }) => {
  await page.goto("/reise-form-harness.html");
  await page.waitForFunction(() => window.ready);
  await page.evaluate(() => window.render({ reiseart: "Inland" }));

  const s = await page.evaluate(() => window.readState());
  expect(s.hasReisepass).toBe(false); // conditional.hide: removed for non-Fernreise

  const opts = await openZielOptions(page);
  expect(opts).toEqual(["Sylt", "Bayern", "Harz"]);
});
