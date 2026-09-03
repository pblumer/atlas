// End-to-end coverage for finding an instance in the live view when the version
// holds far more instances than a panel can show. The panel used to fetch every
// instance of the version and render one card each; at a few hundred thousand that
// is a scan of the store per poll and a DOM node per instance. It must instead ask
// for one page per half, say what it is showing out of what exists, walk the cursor
// on demand, and reach anything off the page through the search box — a point read
// for a bare instance key, a scoped variable search otherwise.
import { test, expect } from "@playwright/test";

const PAGE = 50;      // the panel's page size, per half
const ACTIVE = 120;   // live instances the harness holds — more than two pages
const FINISHED = 30;  // finished ones — fewer than a page, so that half is exhausted at once
const FIRST = PAGE + FINISHED; // what the first render lists: one active page + all history

const open = async (page) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/live-instance-search-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mountLive());
  await expect(page.locator("#var-panel .vp-inst").first()).toBeVisible();
};

const cards = (page) => page.locator("#var-panel .vp-inst");
const searchBox = (page) => page.locator("#var-panel .vp-search-q");

test.describe("the live instance panel", () => {
  test("asks for a page per half, not for every instance", async ({ page }) => {
    await open(page);

    const listCalls = await page.evaluate(() =>
      window.__requests.filter((u) => u.startsWith("/api/v1/instances?")));
    expect(listCalls.length).toBeGreaterThan(0);
    for (const url of listCalls) {
      expect(url).toContain("process=");
      expect(url).toMatch(/state=(active|finished)/);
      expect(url).toContain(`limit=${PAGE}`);
    }
    // Both halves are asked for, so history is reachable without a second view.
    expect(listCalls.some((u) => u.includes("state=active"))).toBe(true);
    expect(listCalls.some((u) => u.includes("state=finished"))).toBe(true);
    expect(page.__errors).toEqual([]);
  });

  test("shows one page and says what it was drawn from", async ({ page }) => {
    await open(page);

    // One page of the live half plus the whole (shorter) history — not the 150 the
    // version holds.
    await expect(cards(page)).toHaveCount(FIRST);
    const total = ACTIVE + FINISHED;
    await expect(page.locator("#var-panel .vp-title").first())
      .toHaveText(new RegExp(`all instances \\(${FIRST} of ${total}\\)`));
    // The picker agrees, so the count is the same wherever an operator reads it.
    await expect(page.locator("#instance-sel option").first())
      .toHaveText(new RegExp(`${FIRST} of ${total}`));
  });

  test("load more walks the cursor into older instances without losing the page", async ({ page }) => {
    await open(page);
    const first = await cards(page).first().locator("b").innerText();

    // Only the live half has more left, so exactly one more active page arrives.
    await page.locator("#var-panel [data-load-more]").click();
    await expect(cards(page)).toHaveCount(FIRST + PAGE);

    // The rows already in front of the operator stayed put.
    await expect(cards(page).first().locator("b")).toHaveText(first);
    // The second page really was asked for with a cursor.
    const paged = await page.evaluate(() =>
      window.__requests.filter((u) => u.includes("before=")));
    expect(paged.length).toBeGreaterThan(0);
    expect(page.__errors).toEqual([]);
  });

  test("a loaded page survives the poll instead of snapping back", async ({ page }) => {
    await open(page);
    await page.locator("#var-panel [data-load-more]").click();
    await expect(cards(page)).toHaveCount(FIRST + PAGE);

    // The 1.5s poll re-reads the listing. It must re-read what is *shown* — so the
    // rows stay live — not just the first page, which would throw away what the
    // operator deliberately loaded.
    await page.waitForTimeout(2000);
    await expect(cards(page)).toHaveCount(FIRST + PAGE);
    expect(page.__errors).toEqual([]);
  });

  test("an instance key off the page is found by the search box", async ({ page }) => {
    await open(page);
    // The oldest instance of the version — deliberately far outside the first page.
    const key = await page.evaluate(() => String(900000));

    await searchBox(page).fill(key);
    await searchBox(page).press("Enter");

    await expect(cards(page)).toHaveCount(1);
    await expect(cards(page).first().locator("b")).toHaveText(key);
    await expect(page.locator("#var-panel .vp-title").first()).toContainText("search");
    // Scoped to this version, so the server reads this version's index.
    const searches = await page.evaluate(() =>
      window.__requests.filter((u) => u.startsWith("/api/v1/instances/search")));
    expect(searches.length).toBe(1);
    expect(searches[0]).toContain("process=");
    expect(searches[0]).toContain(`q=${key}`);
  });

  test("a variable search narrows the panel, and clearing restores the page", async ({ page }) => {
    await open(page);

    await searchBox(page).fill("identityId=MT-1000");
    await searchBox(page).press("Enter");
    await expect(cards(page)).toHaveCount(1);
    await expect(cards(page).first()).toContainText("MT-1000");

    await page.locator("#var-panel [data-search-clear]").click();
    await expect(cards(page)).toHaveCount(FIRST);
    await expect(searchBox(page)).toHaveValue("");
    expect(page.__errors).toEqual([]);
  });

  test("a search that matches nothing says so rather than emptying the panel silently", async ({ page }) => {
    await open(page);

    await searchBox(page).fill("nothing-matches-this");
    await searchBox(page).press("Enter");

    await expect(cards(page)).toHaveCount(0);
    await expect(page.locator("#var-panel")).toContainText("No instance of this version matches");
    // The box keeps the query, so the operator can correct it rather than retype it.
    await expect(searchBox(page)).toHaveValue("nothing-matches-this");
  });

  test("a typed query survives the poll's rebuild", async ({ page }) => {
    await open(page);

    await searchBox(page).fill("identityId=MT-10");
    // Force the rebuild the 1.5s poll would do, without waiting for it.
    await page.evaluate(() => document.querySelector("#refresh")?.click());
    await page.waitForTimeout(1800);

    await expect(searchBox(page)).toHaveValue("identityId=MT-10");
    expect(page.__errors).toEqual([]);
  });
});
