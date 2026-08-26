// End-to-end regression tests for the hash router's re-entrancy guard
// (api/web/app.js). route() is async and re-fires on every hashchange, so two
// navigations can be in flight at once. A view handler that renders — or installs
// a poll timer, or claims window.__atlasCleanup — only AFTER an await must bail
// when a newer navigation has landed, or its late write clobbers the newer view /
// leaks a timer. app.js bumps a module-level navGen on every route() and each
// guarded handler re-checks it (the `superseded(gen)` calls).
//
// These drive the REAL app (index.html → app.js) against a mocked /api/v1 surface,
// so they exercise the actual router, not a stand-in. The mock stalls a specific
// endpoint so we can navigate away while its handler is still awaiting.
import { test, expect } from "@playwright/test";

const wait = (ms) => new Promise((r) => setTimeout(r, ms));

// installMock intercepts every /api/v1 call. `delays` maps a path suffix to a stall
// in ms; `counts` (returned) tallies requests per suffix so a test can prove a poll
// did or didn't keep firing. Auth is answered as single-user so the app never gates.
function installMock(page, delays = {}) {
  const counts = {};
  page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/me")) {
      return route.fulfill({ json: { authEnabled: false, user: null } });
    }
    const suffix = Object.keys(delays).find((s) => path.endsWith(s));
    if (suffix) {
      counts[suffix] = (counts[suffix] || 0) + 1;
      if (delays[suffix]) await wait(delays[suffix]);
    }
    // Benign default: an empty array satisfies the console's list endpoints; object
    // endpoints tolerate it, and route()'s try/catch absorbs any that don't.
    if (path.endsWith("/logs")) return route.fulfill({ json: { lines: ["boot"] } });
    return route.fulfill({ json: [] });
  });
  return counts;
}

// bootApp loads the real shell and waits until the first route() has rendered.
async function bootApp(page) {
  await page.goto("/index.html");
  await page.waitForFunction(
    () => document.querySelector("#view") && document.querySelector("#view").children.length > 0,
    null,
    { timeout: 15000 },
  );
}

test("a superseded logs view does not throw an error card over the new view", async ({ page }) => {
  // viewConsoleLogs finishes its setup AFTER its first GET /logs resolves, touching
  // the live DOM (document.getElementById("log-refresh")). Leave while that fetch is
  // in flight and, unguarded, that lookup returns null and throws — route()'s catch
  // then paints a "Something went wrong" card over whatever the user navigated to.
  const pageErrors = [];
  page.on("pageerror", (e) => pageErrors.push(e.message));
  const counts = installMock(page, { "/logs": 600 });
  await bootApp(page);

  await page.evaluate(() => { location.hash = "#/console/logs"; });
  await wait(120); // let viewConsoleLogs fire its first GET /logs
  expect(counts["/logs"]).toBe(1);

  await page.evaluate(() => { location.hash = "#/console"; }); // leave while it's in flight

  // Past the stalled response (600ms): the guard should have made the logs handler
  // return cleanly, so no error card clobbers the view and no timer keeps polling.
  await wait(3200);
  await expect(page.locator("#view")).not.toContainText("Something went wrong");
  await expect(page.locator("#view")).not.toContainText("Server logs"); // stale view didn't survive either
  expect(counts["/logs"], "logs poll kept running after navigating away").toBe(1);
  expect(pageErrors).toEqual([]);
});

test("a superseded view does not clobber the view the user navigated to", async ({ page }) => {
  // The org view awaits its user roster before writing view.innerHTML. Navigate
  // org→logs while that roster is stalled; the org handler must not paint over the
  // logs view when its fetch finally resolves.
  installMock(page, { "/users": 700 });
  await bootApp(page);

  await page.evaluate(() => { location.hash = "#/console/org"; });
  await wait(120); // org fires GET /users (stalled 700ms)
  await page.evaluate(() => { location.hash = "#/console/logs"; }); // move to logs

  // Let the logs view render, then let org's stalled /users resolve and (if buggy)
  // clobber. The surviving view must be logs.
  await wait(1400);
  await expect(page.locator("#view")).toContainText("Server logs");
  await expect(page.locator("#view")).not.toContainText("Organization");
});
