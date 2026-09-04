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

test.describe("an instance the archive answers for", () => {
  // The row the exported event log hands back for an instance history retention
  // removed from this server. It carries no element instances, because the archive
  // knows of no live tokens.
  const purged = {
    key: 900001, processDefKey: 7, processId: "identitaet", version: 1,
    elementInstances: 0, state: "completed", createdAt: 1_722_000_000_000_000_000,
    completedAt: 1_722_000_900_000_000_000,
    variables: [{ name: "identityId", value: "MT-1998", kind: "string" }],
  };

  const searchArchive = async (page, archive, q) => {
    await open(page);
    await page.evaluate((a) => { window.__archive = a; }, archive);
    await searchBox(page).fill(q);
    await searchBox(page).press("Enter");
  };

  test("is found, and is visibly not a live one", async ({ page }) => {
    await searchArchive(page, { state: "available", rows: [purged] }, "identityId=MT-1998");

    await expect(cards(page)).toHaveCount(1);
    const card = cards(page).first();
    await expect(card).toContainText("900001");
    await expect(card.locator(".pill", { hasText: "archived" })).toBeVisible();
  });

  test("offers nothing that would act on an instance that is gone", async ({ page }) => {
    await searchArchive(page, { state: "available", rows: [purged] }, "identityId=MT-1998");

    // Replay reads the instance's events from this server's store. For an instance
    // the store no longer has, the link would lead nowhere — so it is not offered.
    await expect(cards(page).first().locator(".replay-link")).toHaveCount(0);
  });

  test("keeps its marking after the poll rebuilds the panel", async ({ page }) => {
    await searchArchive(page, { state: "available", rows: [purged] }, "identityId=MT-1998");
    await expect(cards(page).first().locator(".pill", { hasText: "archived" })).toBeVisible();

    // The panel rebuilds every 1.5s. A signature that ignored archived-ness would
    // keep the first render and, worse, could redraw the row as an ordinary one.
    await page.waitForTimeout(2000);
    await expect(cards(page)).toHaveCount(1);
    await expect(cards(page).first().locator(".pill", { hasText: "archived" })).toBeVisible();
  });

  // The picker is the list an operator chooses from before ever seeing a row, so the
  // marking has to reach it too. This runs one query across the moment an instance
  // stops being live and starts being archived — the same key, the same state, the
  // same query — and holds that the option changes with it.
  test("the picker stops calling a purged instance live", async ({ page }) => {
    await open(page);
    const q = "identityId=MT-1998";
    // A finished instance the local store still has, matched by a variable.
    const key = await page.evaluate((needle) => {
      const row = window.__rows().find((r) => r.state !== "active");
      row.variables = [{ name: "identityId", value: needle, kind: "string" }];
      return row.key;
    }, "MT-1998");
    await searchBox(page).fill(q);
    await searchBox(page).press("Enter");
    const picker = page.locator("#instance-sel");
    await expect(picker).toContainText(String(key));
    await expect(picker).not.toContainText("archived");

    // Retention removes it; the archive answers the same query with the same key and
    // the same state.
    await page.evaluate(([k, needle]) => {
      window.__purge(k);
      window.__archive = { state: "available", rows: [{
        key: k, processDefKey: 7, processId: "identitaet", version: 1,
        elementInstances: 0, state: "completed",
        variables: [{ name: "identityId", value: needle, kind: "string" }],
      }] };
    }, [key, "MT-1998"]);
    await searchBox(page).fill(q);
    await searchBox(page).press("Enter");
    await expect(picker).toContainText("archived");
  });

  test("says why nothing was found when there is no archive to search", async ({ page }) => {
    await searchArchive(page, { state: "notConfigured", rows: [] }, "identityId=NOBODY");

    await expect(cards(page)).toHaveCount(0);
    const panel = page.locator("#var-panel");
    await expect(panel).toContainText("No instance of this version matches");
    // "Nothing matched" and "nothing was looked in" are different facts. An operator
    // told only the first stops looking for an instance that does exist.
    await expect(panel.locator(".vp-archive-note")).toContainText("no event log is exported");
  });

  test("separates a store that declined from one that could not be reached", async ({ page }) => {
    await searchArchive(page, { state: "refused", rows: [] }, "identityId=NOBODY");
    await expect(page.locator("#var-panel .vp-archive-note")).toContainText("declined");

    await page.evaluate(() => { window.__archive = { state: "unreachable", rows: [] }; });
    await searchBox(page).fill("identityId=NOBODY2");
    await searchBox(page).press("Enter");
    await expect(page.locator("#var-panel .vp-archive-note")).toContainText("could not be reached");
  });
});
