import { test, expect } from "@playwright/test";

// FALL A — one intake form carries ALL the dependency in-form (no process
// branching): cascading Ziel, Fernreise-only fields, and minor-only fields
// driven by a FEEL date comparison in conditional.hide. Verified against the
// real vendored form-js bundle.

async function openZielOptions(page) {
  await page.locator(".fjs-form-field-select").nth(1).locator(".fjs-select-display").click();
  await page.locator(".fjs-dropdownlist-item").first().waitFor({ timeout: 4000 });
  return page.locator(".fjs-dropdownlist-item").allTextContents();
}

test("schema imports cleanly (guards empty geburtsdatum)", async ({ page }) => {
  const pageErrors = [];
  page.on("pageerror", e => pageErrors.push(String(e)));
  await page.goto("/reise-antrag-harness.html");
  await page.waitForFunction(() => window.ready);
  expect(await page.evaluate(() => window.__err)).toBeNull();
  expect(pageErrors).toEqual([]);
  // With no data, minor/Fernreise fields are hidden and nothing throws.
  const s = await page.evaluate(() => window.readState());
  expect(s.hasReiseart && s.hasZiel && s.hasGeburtsdatum && s.hasBuchung).toBe(true);
  expect(s.hasReisepass || s.hasVertreter || s.hasEinverstaendnis).toBe(false);
});

test("Fernreise + minor: far Ziel, Fernreise fields, guardian fields", async ({ page }) => {
  await page.goto("/reise-antrag-harness.html");
  await page.waitForFunction(() => window.ready);
  await page.evaluate(() => window.render({ reiseart: "Fernreise", geburtsdatum: "2010-05-01" }));
  const s = await page.evaluate(() => window.readState());
  expect(s.hasReisepass).toBe(true);
  expect(s.hasStaat).toBe(true);
  expect(s.hasVertreter).toBe(true);
  expect(s.hasEinverstaendnis).toBe(true);
  expect(await openZielOptions(page)).toEqual(["Thailand", "USA", "Japan"]);
});

test("Inland + adult: domestic Ziel, no extra fields", async ({ page }) => {
  await page.goto("/reise-antrag-harness.html");
  await page.waitForFunction(() => window.ready);
  await page.evaluate(() => window.render({ reiseart: "Inland", geburtsdatum: "1990-03-15" }));
  const s = await page.evaluate(() => window.readState());
  expect(s.hasReisepass).toBe(false);
  expect(s.hasStaat).toBe(false);
  expect(s.hasVertreter).toBe(false);
  expect(s.hasEinverstaendnis).toBe(false);
  expect(await openZielOptions(page)).toEqual(["Sylt", "Bayern", "Harz"]);
});

test("EU + exactly-18 adult boundary: guardian fields hidden", async ({ page }) => {
  await page.goto("/reise-antrag-harness.html");
  await page.waitForFunction(() => window.ready);
  // born 2008-08-08 → 18 on the 2026-08-08 reference date → adult (hidden)
  await page.evaluate(() => window.render({ reiseart: "EU", geburtsdatum: "2008-08-08" }));
  const s = await page.evaluate(() => window.readState());
  expect(s.hasVertreter).toBe(false);
  expect(s.hasEinverstaendnis).toBe(false);
  expect(await openZielOptions(page)).toEqual(["Spanien", "Frankreich", "Italien"]);
});
