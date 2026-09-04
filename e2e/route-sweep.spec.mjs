// Every route the shell serves says where it is (api/web/app.js).
//
// The shell answers "where am I" from five hand-maintained lists — the dispatcher,
// TOPNAV plus setChrome's active match, routeTitle, the help mapping and fullBleed —
// and nothing fails when one of them is missed. Several were: fifteen detail routes
// marked no navigation entry, and eight routes left the browser tab reading a bare
// "Atlas". Those are the symptoms ADR-draft-every-route-says-where-it-is is about.
//
// This is the cheap half of that record: rather than restructure the router so the
// lists cannot disagree, walk every route and assert what a reader should get. The
// list below is a sixth list, and deliberately so — a test is allowed to state the
// expectation independently, which is what makes it able to disagree with the code.
import { test, expect } from "@playwright/test";

// Every route the dispatcher serves, with a stand-in for each id it takes. Add a
// route here when you add one to route(); that is the point of the file.
const ROUTES = [
  "#/console", "#/console/engine", "#/console/logs", "#/console/backup",
  "#/console/org", "#/console/workers", "#/console/ai-access", "#/console/audit",
  "#/modeler", "#/modeler/repository", "#/modeler/new", "#/modeler/p/app-1",
  "#/modeler/form/new", "#/modeler/d/7", "#/modeler/draft/proc-1", "#/modeler/dmn/ref-1",
  "#/tasks", "#/tasks/start",
  "#/operations", "#/operations/incidents", "#/operations/workers", "#/operations/outbox",
  "#/operations/ad-mock", "#/operations/sql-mock", "#/operations/decisions",
  "#/operations/call-activities", "#/operations/p/7", "#/operations/i/9",
  "#/panorama", "#/panorama/landscape",
  "#/data", "#/data/instances",
];

// The app a route belongs to, and whether that app shows a secondary navigation at
// all. Console/Modeler/Operations/Tasks/Panorama/Data all do.
const appOf = (route) => route.split("/")[1] || "console";

async function boot(page) {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path.endsWith("/logs")) return route.fulfill({ json: { lines: ["boot"] } });
    // A benign default: the console's list endpoints take an empty array, and route()
    // absorbs the rest. This test is about the chrome, not about what a view renders.
    return route.fulfill({ json: [] });
  });
  await page.goto("/index.html#/console");
  await page.waitForFunction(() => document.querySelector("#topnav") !== null, null, { timeout: 20000 });
}

// go navigates and waits for the shell to have processed the hash change.
async function go(page, route) {
  await page.evaluate((r) => { location.hash = r; }, route);
  await page.waitForFunction((r) => location.hash === r, route, { timeout: 10000 });
  await page.waitForTimeout(120); // route() is async; the chrome is set before the view
}

test("every route names itself in the browser tab", async ({ page }) => {
  await boot(page);
  const bare = [];
  for (const route of ROUTES) {
    await go(page, route);
    const title = await page.title();
    // "Atlas" alone is the fallback for a route routeTitle has no rule for. A tab
    // that says only the product name is a bookmark nobody can tell apart.
    if (title === "Atlas" || title === "") bare.push(`${route} → ${JSON.stringify(title)}`);
  }
  expect(bare, "routes with no page title").toEqual([]);
  expect(page.__errors).toEqual([]);
});

test("every route marks where it sits in the navigation", async ({ page }) => {
  await boot(page);
  const unmarked = [];
  for (const route of ROUTES) {
    await go(page, route);
    const marked = await page.locator("#topnav a.active").count();
    const offered = await page.locator("#topnav a").count();
    // A route whose app shows a secondary navigation must mark one of its entries:
    // a detail page that marks none leaves the reader inside a section the bar
    // declines to name.
    if (offered > 0 && marked !== 1) unmarked.push(`${route} (${marked} of ${offered} marked)`);
  }
  expect(unmarked, "routes that mark no navigation entry").toEqual([]);
  expect(page.__errors).toEqual([]);
});
