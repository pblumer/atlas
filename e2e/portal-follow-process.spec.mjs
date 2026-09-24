// e2e for the portal's "View the process" link (api/web/shop.js followProcess),
// against a fixture.
//
// What was reported: pressing the link on an order wrote "Wird abgefragt …" under
// it and nothing else happened, ever. The lookup was a search naming no process
// definition, which reads every instance on the server and every variable of each;
// on the installation it was reported from, that search did not come back, and the
// page had no bound on how long it would wait for it.
//
// The fixture reproduces exactly that server: a search that names no definition
// never answers, and one that names a definition answers at once. So the first test
// fails against the old lookup by hanging, which is what the reader saw.
//
// It loads the REAL shop.html with only the network replaced, as the basket test
// does, so what is exercised is the page as shipped.
import { test, expect } from "@playwright/test";

function installFixture() {
  window.__unmatched = [];
  window.__asked = [];
  const ORDER = {
    id: "ord_follow", orderer: "usr_1", recipient: "usr_1", status: "cancelled",
    createdAt: Date.UTC(2026, 8, 24) * 1e6,
    lines: [{ id: "l1", itemId: "konto", status: "cancelled" }],
  };
  const CAT = { id: "cat_f", rank: 1, languages: ["de", "en"],
    texts: { de: "Katalog", en: "Catalogue" }, items: [] };
  // Three versions of the fulfilment process and one process that is not it. The
  // instance the order is worked by lives under the OLDEST version, so a lookup that
  // asked only the newest would say "none found" about an instance that exists.
  const PROCESSES = [
    { key: 466, processId: "atlas-auftrag-erfuellung", version: 1 },
    { key: 480, processId: "atlas-auftrag-erfuellung", version: 2 },
    { key: 493, processId: "atlas-auftrag-erfuellung", version: 3 },
    { key: 517, processId: "proc_monitor_ausgabe", version: 2 },
  ];
  const scoped = {
    // The fulfilment instance — under v1 only. What each scenario finds is set per
    // test through window.__found.
    466: () => window.__found || [],
    480: () => [],
    493: () => [],
  };
  const ROUTES = {
    "/api/v1/auth/me": { user: { id: "usr_1", username: "anja", displayName: "Anja",
      roles: ["operator"] } },
    "/api/v1/shop/catalog": CAT,
    "/api/v1/catalogs/cat_f/releases": [],
    "/api/v1/orders": [ORDER],
    "/api/v1/shop/tasks": { tasks: [], orders: [], truncated: false },
    "/api/v1/inventory": { items: [] },
    "/api/v1/shop/favourites": { itemIds: [] },
    "/api/v1/principals": [{ id: "usr_1", name: "Anja" }],
    "/api/v1/processes": PROCESSES,
  };
  const json = (body) => Promise.resolve(new Response(JSON.stringify(body), {
    status: 200, headers: { "content-type": "application/json" },
  }));
  // A request that never answers still ends when it is aborted, as the browser's own
  // fetch does: that is the one way out a page has, and the fixture keeps it.
  const never = (init) => new Promise((_, reject) => {
    const signal = init && init.signal;
    if (signal) signal.addEventListener("abort",
      () => reject(new DOMException("aborted", "AbortError")));
  });
  window.fetch = (input, init) => {
    const url = new URL(String(input), window.location.origin);
    const path = url.pathname;
    if (path === "/api/v1/instances/search") {
      window.__asked.push(url.search);
      const def = url.searchParams.get("process");
      // The server it was reported against: a search that names no definition
      // never comes back.
      if (!def) return never(init);
      if (window.__searchHangs) return never(init);
      const items = scoped[def] ? scoped[def]() : [];
      return json({ items, total: items.length, totalExact: true, truncated: false });
    }
    if (Object.prototype.hasOwnProperty.call(ROUTES, path)) return json(ROUTES[path]);
    window.__unmatched.push(path);
    return Promise.resolve(new Response("not in this fixture", { status: 404 }));
  };
}

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.addInitScript(installFixture);
  // Where the link leads is the console, which is not under test here: it is
  // answered with an empty page, and the URL is what is asserted.
  await page.route("**/index.html*", (r) => r.fulfill({ contentType: "text/html",
    body: "<!doctype html><title>console</title>" }));
  page._errors = errors;
});

test.afterEach(async ({ page }) => {
  expect(page._errors, "no uncaught page errors").toEqual([]);
});

const open = async (page) => {
  await page.goto("/shop.html");
  await page.getByRole("button", { name: "My orders" }).click();
  await expect(page.getByText("ord_follow")).toBeVisible({ timeout: 10000 });
};

const link = (page) => page.getByRole("button", { name: "View the process" });

test("the link opens the fulfilment instance, even where no-scope search never answers",
  async ({ page }) => {
    await page.addInitScript(() => {
      window.__found = [{ key: 281475006712060, processDefKey: 466,
        processId: "atlas-auftrag-erfuellung", state: "completed" }];
    });
    await open(page);
    await link(page).click();
    await page.waitForURL(/\/index\.html#\/operations\/i\/281475006712060$/,
      { timeout: 5000 });
  });

test("every version is asked, and none of them without naming it", async ({ page }) => {
  await open(page);
  const asked = await (async () => {
    await link(page).click();
    await expect(page.locator(".follow-note")).toContainText("No running process instance");
    return page.evaluate(() => window.__asked);
  })();
  // Newest first, each by its definition key, and no search naming none — the
  // one that does not come back.
  expect(asked.map((q) => new URLSearchParams(q).get("process")))
    .toEqual(["493", "480", "466"]);
});

test("an instance that exists only in the archive is said, not followed", async ({ page }) => {
  await page.addInitScript(() => {
    window.__found = [{ key: 7, processDefKey: 466, processId: "atlas-auftrag-erfuellung",
      archived: true }];
  });
  await open(page);
  await link(page).click();
  await expect(page.locator(".follow-note")).toContainText("exported event log");
  expect(page.url()).toContain("/shop.html");
});

test("a search that never answers is said after a while, not waited on for ever",
  async ({ page }) => {
    await page.clock.install();
    await page.addInitScript(() => { window.__searchHangs = true; });
    await open(page);
    await link(page).click();
    await expect(page.locator(".follow-note")).toHaveText("Asking …");
    await page.clock.runFor(21000);
    await expect(page.locator(".follow-note")).toContainText("did not answer");
  });
