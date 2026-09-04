// End-to-end coverage for how the runtime views print a large count.
//
// The report behind it came from a production diagram: badges reading "25864", "50002",
// "23436" sitting next to each other over the shapes. Every number is correct and none
// of them can be read — a six-digit run has to be counted digit by digit, and two of
// them side by side cannot be compared at all, which is the only reason the counts are
// on the diagram rather than in a table.
//
// So the counts are grouped in threes, with a NARROW NO-BREAK SPACE rather than a
// locale's own separator: "25.864" is twenty-five thousand to one reader and twenty-five
// point eight to the next, and the badge has no room to say which it meant. The space is
// no-break so a badge stays one line at any count.
import { test, expect } from "@playwright/test";

const NNBSP = "\u202F"; // NARROW NO-BREAK SPACE — the separator, spelled out

const open = async (page) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/badge-numbers-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mountLive());
};

const badges = (page, id) =>
  page.locator(`.djs-overlays[data-container-id="${id}"] .token-badge`);

test("groups the thousands in every count on a shape", async ({ page }) => {
  await open(page);

  // Past → present, left to right: completed here, cancelled here, alive here now.
  // Asserted on the raw textContent, because the separator itself is the point and a
  // whitespace-normalizing matcher would accept a plain space just as happily.
  const three = badges(page, "register");
  await expect(three).toHaveCount(3);
  expect(await three.nth(0).textContent()).toBe(`25${NNBSP}864`);
  expect(await three.nth(1).textContent()).toBe(`2${NNBSP}428`);
  expect(await three.nth(2).textContent()).toBe(`50${NNBSP}002`);

  // The tooltip is the same number in a sentence, so it is grouped too — a reader who
  // hovers to check what a badge means must not have to re-read the digits there.
  await expect(three.nth(2)).toHaveAttribute("title", new RegExp(`50${NNBSP}002 live token`));

  // Six digits group twice.
  expect(await badges(page, "start").first().textContent()).toBe(`78${NNBSP}294`);
  expect(page.__errors).toEqual([]);
});

test("leaves a count below a thousand exactly as it was", async ({ page }) => {
  await open(page);

  // Nothing to group, so nothing is inserted: a separator on "999" would be noise, and
  // a badge is small enough that noise costs a digit's worth of room.
  expect(await badges(page, "end").first().textContent()).toBe("999");
  expect(page.__errors).toEqual([]);
});

test("groups the counts in the header pills as well", async ({ page }) => {
  await open(page);

  // The pills are the same numbers the badges show, on the same screen. Grouping the
  // diagram and leaving the header in digit runs would only move the problem.
  await expect(page.locator("#inst-count")).toHaveText(`50${NNBSP}002`);
  await expect(page.locator("#token-count")).toHaveText(`78${NNBSP}294`);
  expect(page.__errors).toEqual([]);
});

test("groups from the right, keeps the sign, and passes a non-number through", async ({ page }) => {
  await open(page);
  const fmt = (n) => page.evaluate((n) => window.__fmtCount(n), n);

  expect(await fmt(0)).toBe("0");
  expect(await fmt(999)).toBe("999");
  expect(await fmt(1000)).toBe(`1${NNBSP}000`);
  expect(await fmt(50002)).toBe(`50${NNBSP}002`);
  expect(await fmt(1234567)).toBe(`1${NNBSP}234${NNBSP}567`);
  // Grouping runs from the right, so the leading group is whatever is left over.
  expect(await fmt(12345)).toBe(`12${NNBSP}345`);
  // A negative keeps its sign outside the groups (a difference, never a count).
  expect(await fmt(-25864)).toBe(`-25${NNBSP}864`);
  // Numeric strings are the same numbers; anything that is not a number at all is
  // shown as it came rather than turned into "NaN" on a badge.
  expect(await fmt("50002")).toBe(`50${NNBSP}002`);
  expect(await fmt("—")).toBe("—");
  expect(await fmt(null)).toBe("");
  expect(page.__errors).toEqual([]);
});
