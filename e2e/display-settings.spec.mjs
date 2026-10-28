// End-to-end coverage for the Display screen and the two formatters behind it
// (ADR-0459).
//
// The screen's whole claim is that the sample beside an option is what the console
// will actually print. So the tests assert the sample text itself — and assert it
// against the same formatters the badges use, rather than against a locale the
// browser happens to carry.
//
// The screen's own words come from the message catalogue (ADR-0267), whose default
// is German. That is asserted here too: a language picker on a screen that did not
// itself follow the choice would disprove itself on the first click.
import { test, expect } from "@playwright/test";

const NNBSP = " "; // the default thousands separator

const open = async (page, mode = "account", stored = null) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/display-settings-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(({ m, stored }) => {
    window.__reset();
    window.__mode = m;
    if (stored) window.__stored = { userId: "usr_1", region: "", timeZone: "", language: "", ...stored };
  }, { m: mode, stored });
  await page.evaluate(() => window.__render());
};

const sample = (page, id) => page.locator(`#ds-sample-${id}`);
const regionRow = (page, id) => page.locator(`.ds-option:has(input[value="${id}"]) .ds-option-sample`);

test("every region shows what it will print, side by side", async ({ page }) => {
  await open(page);

  // Four rows, four separators — the comparison somebody is actually making.
  expect(await regionRow(page, "international").textContent()).toContain(`25${NNBSP}864.5`);
  expect(await regionRow(page, "ch").textContent()).toContain("25'864.5");
  expect(await regionRow(page, "de").textContent()).toContain("25.864,5");
  expect(await regionRow(page, "us").textContent()).toContain("25,864.5");

  // And each writes the date its own way: the day first, the year first, or the
  // month first — the trap a bare "10/15" hides.
  expect(await regionRow(page, "international").textContent()).toContain("2026-10-15");
  expect(await regionRow(page, "ch").textContent()).toContain("15.10.2026");
  expect(await regionRow(page, "us").textContent()).toContain("10/15/2026");
  expect(page.__errors).toEqual([]);
});

test("choosing a region previews it without applying it", async ({ page }) => {
  await open(page);
  await page.locator('input[name="ds-region"][value="ch"]').check();

  expect(await sample(page, "count").textContent()).toBe("50'002");
  expect(await sample(page, "amount").textContent()).toBe("25'864.5");

  // Nothing was saved by looking: the preview is a preview until Save is pressed.
  expect(await page.evaluate(() => window.__puts.length)).toBe(0);
  expect(await page.evaluate(() => window.__stored.region)).toBe("");
});

test("the time zone decides which instant a timestamp is", async ({ page }) => {
  await open(page);
  // The sample instant is 15:18:29 UTC. On the Zurich clock that is 17:18:29 —
  // the two-hour error an operator would otherwise make by reading a server's
  // timestamp on their own laptop.
  await page.locator("#ds-zone").selectOption("UTC");
  expect(await sample(page, "when").textContent()).toBe("2026-10-15 15:18:29");
  expect(await sample(page, "zone").textContent()).toBe("UTC");

  await page.locator("#ds-zone").selectOption("Europe/Zurich");
  expect(await sample(page, "when").textContent()).toBe("2026-10-15 17:18:29");

  // And the region rows move with it, so a row is never showing a different clock
  // from the preview above it.
  expect(await regionRow(page, "ch").textContent()).toContain("15.10.2026 17:18:29");
});

test("saving stores the three fields for this account", async ({ page }) => {
  await open(page);
  await page.locator('input[name="ds-region"][value="de"]').check();
  await page.locator("#ds-zone").selectOption("Europe/Berlin");
  await page.locator("#ds-save").click();

  await expect(page.locator("#ds-status")).toHaveText("Gespeichert.");
  expect(await page.evaluate(() => window.__stored)).toMatchObject({
    region: "de", timeZone: "Europe/Berlin", language: "",
  });
  // And the console reads that way from now on, not only this screen.
  expect(await page.evaluate(() => window.__fmt.fmtCount(50002))).toBe("50.002");
  expect(await page.evaluate(() => window.__fmt.fmtDateTime(Date.UTC(2026, 9, 15, 15, 18, 29))))
    .toBe("15.10.2026 17:18:29");
});

test("back to defaults is the console nobody configured", async ({ page }) => {
  await open(page);
  await page.locator('input[name="ds-region"][value="us"]').check();
  await page.locator("#ds-zone").selectOption("America/New_York");
  await page.locator("#ds-reset").click();

  expect(await sample(page, "count").textContent()).toBe(`50${NNBSP}002`);
  expect(await sample(page, "when").textContent()).toBe("2026-10-15 15:18:29");
  await page.locator("#ds-save").click();
  await expect(page.locator("#ds-status")).toHaveText("Gespeichert.");
  expect(await page.evaluate(() => window.__stored)).toMatchObject({ region: "international", timeZone: "" });
});

// The appearance is the fourth choice (ADR-0507), and it obeys the screen's rule: a
// click shows a choice, Save applies it. The dark option says it is a preview.
test("the appearance is chosen here and applied on save, not on a click", async ({ page }) => {
  await open(page);
  const appearance = () => page.evaluate(() => document.documentElement.getAttribute("data-appearance"));
  await expect(page.locator('.ds-option:has(input[value="dark"]) .ds-option-name')).toHaveText("Dunkel (Vorschau)");
  // Nothing chosen reads as the light option, which is what it draws.
  await expect(page.locator('input[name="ds-appearance"][value="light"]')).toBeChecked();

  await page.locator('input[name="ds-appearance"][value="dark"]').check();
  expect(await appearance()).toBe("light");
  await page.locator("#ds-save").click();
  await expect(page.locator("#ds-status")).toHaveText("Gespeichert.");
  expect(await appearance()).toBe("dark");
  expect(await page.evaluate(() => window.__stored)).toMatchObject({ appearance: "dark" });

  // Back to the defaults clears it — the record says "nothing chosen", not "light".
  await page.locator("#ds-reset").click();
  await expect(page.locator('input[name="ds-appearance"][value="light"]')).toBeChecked();
  await page.locator("#ds-save").click();
  await expect(page.locator("#ds-status")).toHaveText("Gespeichert.");
  expect(await page.evaluate(() => window.__stored)).toMatchObject({ appearance: "" });
  expect(await appearance()).toBe("light");
  expect(page.__errors).toEqual([]);
});

test("saving another field leaves an unchosen appearance unchosen", async ({ page }) => {
  await open(page);
  await page.locator('input[name="ds-region"][value="ch"]').check();
  await page.locator("#ds-save").click();
  await expect(page.locator("#ds-status")).toHaveText("Gespeichert.");
  // "" is the default, and the default may change once dark is no longer a preview:
  // a save of the number format must not pin this person to light.
  expect(await page.evaluate(() => window.__stored)).toMatchObject({ region: "ch", appearance: "" });
  // The region rows repaint their samples on a change; the appearance rows have none.
  expect(page.__errors).toEqual([]);
});

test("with no sign-in the setting is this browser's, and says so", async ({ page }) => {
  await open(page, "anonymous");

  // Not an error state: an instance with authentication off has nobody to store a
  // preference for, and the screen says where it is kept instead of failing.
  await expect(page.locator("#ds-scope")).toContainText("bleibt in diesem Browser");

  await page.locator('input[name="ds-region"][value="ch"]').check();
  await page.locator("#ds-save").click();
  await expect(page.locator("#ds-status")).toHaveText("Gespeichert.");
  // Kept locally, and read by the formatters all the same.
  expect(await page.evaluate(() => window.__fmt.fmtCount(50002))).toBe("50'002");
  expect(await page.evaluate(() => window.__prefs().region)).toBe("ch");
  expect(page.__errors).toEqual([]);
});

test("an unreachable server leaves the console reading the way it did", async ({ page }) => {
  await open(page, "offline");
  // "Offline" is not "no account": saying the setting is this browser's here would
  // invite somebody to re-set a preference that is not lost at all.
  await expect(page.locator("#ds-scope")).toContainText("nicht erreichbar");
});

test("the screen speaks the language it is offering", async ({ page }) => {
  // The catalogue's default is German (ADR-0267), so that is what an account with
  // no choice gets — including this screen.
  await open(page);
  await expect(page.locator("h1")).toHaveText("Darstellung");
  await expect(page.locator('.ds-option:has(input[value="ch"]) .ds-option-name')).toHaveText("Schweiz");

  // And an account that chose English reads it in English, this screen included:
  // the setting is not a promise about other screens only.
  await open(page, "account", { language: "en" });
  await expect(page.locator("h1")).toHaveText("Display");
  await expect(page.locator('.ds-option:has(input[value="ch"]) .ds-option-name')).toHaveText("Switzerland");
  await expect(page.locator("#ds-save")).toHaveText("Save");
});

test("the formatters agree with the samples, at their edges", async ({ page }) => {
  await open(page);
  const fmt = (name, ...args) => page.evaluate(
    ({ name, args }) => window.__fmt[name](...args), { name, args });

  // Under a thousand nothing is inserted, in any region.
  expect(await fmt("fmtCount", 999, { region: "de" })).toBe("999");
  expect(await fmt("fmtCount", 1000, { region: "de" })).toBe("1.000");
  // A decimal mark is the region's too, not always a point.
  expect(await fmt("fmtCount", 1234.5, { region: "de" })).toBe("1.234,5");
  expect(await fmt("fmtCount", 1234.5, { region: "ch" })).toBe("1'234.5");
  // Midnight on a twelve-hour clock is 12 AM, not 00 AM — the quirk a locale
  // string would have decided for us.
  expect(await fmt("fmtTime", Date.UTC(2026, 9, 15, 0, 5, 0), { region: "us", timeZone: "UTC" }))
    .toBe("12:05:00 AM");
  expect(await fmt("fmtTime", Date.UTC(2026, 9, 15, 13, 5, 0), { region: "us", timeZone: "UTC" }))
    .toBe("1:05:00 PM");
  // A date with no time, and an instant that is not one at all.
  expect(await fmt("fmtDate", Date.UTC(2026, 9, 15, 15, 0, 0), { region: "ch", timeZone: "UTC" }))
    .toBe("15.10.2026");
  expect(await fmt("fmtDateTime", null)).toBe("");
  expect(await fmt("fmtDateTime", "not a date")).toBe("");
});
