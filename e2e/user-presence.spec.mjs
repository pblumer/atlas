// Who is signed in, from the browser (ADR-0228; the Users card in
// app.js, viewConsoleOrg, and the beacon in the app shell).
//
// Two halves only a real DOM proves. The page renders a presence column from what
// the roster carries and keeps it current without reloading the page — an
// administrator half-way through a user form must not have it wiped every thirty
// seconds — and the tab itself reports, with the activity flag set only by a real
// pointer or key and never by the app's own polling. Drives the REAL app shell
// against a mock of the two endpoints, like sso-mapping.spec.mjs, and drives the
// two intervals with Playwright's clock rather than with test-only hooks in the
// app: the timing is part of what is being tested.
import { test, expect } from "@playwright/test";

const ROSTER = [
  {
    id: "usr_1", username: "root", displayName: "Root", roles: ["admin"], disabled: false,
    presence: { userId: "usr_1", state: "online", sessions: 1 },
  },
  {
    id: "usr_2", username: "mia", displayName: "Mia Keller", roles: ["modeler"], disabled: false,
    presence: { userId: "usr_2", state: "idle", sessions: 1, lastActiveAt: 1 },
  },
  {
    id: "usr_3", username: "olli", displayName: "Olli Weber", roles: ["user"], disabled: false,
    presence: { userId: "usr_3", state: "offline" },
  },
];

// installMock answers as an admin-enforcing instance. The returned handle records
// every beacon the page posted, and `live` is what the presence refresh answers
// with, so a test can change who is here and watch the table follow.
function installMock(page, { authEnabled = true } = {}) {
  const state = { beacons: [], live: [{ userId: "usr_2", state: "online", sessions: 1 }] };
  page.route("**/api/v1/**", async (route) => {
    const req = route.request();
    const path = new URL(req.url()).pathname;
    if (path.endsWith("/auth/me")) {
      return route.fulfill({ json: { authEnabled, user: { id: "usr_1", username: "root", roles: ["admin"] } } });
    }
    if (path.endsWith("/auth/presence")) {
      state.beacons.push(req.postDataJSON());
      return route.fulfill({ json: { ok: true } });
    }
    if (path.endsWith("/users/presence")) return route.fulfill({ json: state.live });
    if (path.endsWith("/users")) return route.fulfill({ json: ROSTER });
    if (path.endsWith("/auth/providers")) return route.fulfill({ json: [] });
    return route.fulfill({ json: [] });
  });
  return state;
}

async function gotoOrg(page) {
  await page.goto("/index.html");
  await page.waitForFunction(
    () => document.querySelector("#view") && document.querySelector("#view").children.length > 0,
    null,
    { timeout: 15000 },
  );
  await page.evaluate(() => { location.hash = "#/console/org"; });
  await expect(page.locator("#user-rows tr")).toHaveCount(3);
}

const pillOf = (page, id) => page.locator(`#user-rows tr[data-id="${id}"] td.presence-cell .pill`);

test("the roster's own presence is what the column shows", async ({ page }) => {
  installMock(page);
  await gotoOrg(page);

  await expect(page.locator("#view table[data-dt-key='users'] thead")).toContainText("Presence");
  await expect(pillOf(page, "usr_1")).toHaveText("online");
  await expect(pillOf(page, "usr_2")).toHaveText("idle");
  await expect(pillOf(page, "usr_3")).toHaveText("offline");
  // Offline is the quiet pill, not a warning: a user list is mostly offline.
  await expect(pillOf(page, "usr_3")).toHaveClass(/off/);
  await expect(pillOf(page, "usr_3")).toHaveAttribute("title", /Not signed in/);
  await expect(pillOf(page, "usr_2")).toHaveAttribute("title", /nothing done since/);
});

test("presence repaints itself without touching the rest of the page", async ({ page }) => {
  await page.clock.install();
  const server = installMock(page);
  await gotoOrg(page);

  // An administrator is half-way through creating a user…
  await page.click("#new-user");
  await expect(page.locator("#user-form-slot .user-form")).toBeVisible();
  await page.fill("#user-form-slot input[name='username']", "half-typed");

  // …while the world changes underneath: Mia starts working, everyone else leaves.
  server.live = [{ userId: "usr_2", state: "online", sessions: 2 }];
  await page.clock.runFor(30_000);

  await expect(pillOf(page, "usr_2")).toHaveText("online");
  await expect(pillOf(page, "usr_1")).toHaveText("offline");
  // The form is still there, with what was typed into it.
  await expect(page.locator("#user-form-slot input[name='username']")).toHaveValue("half-typed");
});

test("the tab reports itself, and claims activity only for a real action", async ({ page }) => {
  await page.clock.install();
  const server = installMock(page);
  await gotoOrg(page);

  // Opening the app is an act, so the first beacon claims it.
  await expect.poll(() => server.beacons.length).toBeGreaterThan(0);
  expect(server.beacons[0]).toEqual({ active: true });

  // A minute in which the page polled on its own but nobody touched anything: the
  // beacon says so. This is what separates "signed in" from "at the keyboard".
  server.beacons.length = 0;
  await page.clock.runFor(60_000);
  await expect.poll(() => server.beacons.length).toBeGreaterThan(0);
  expect(server.beacons.at(-1)).toEqual({ active: false });

  // A keystroke, and the next beacon carries it.
  server.beacons.length = 0;
  await page.keyboard.press("a");
  await page.clock.runFor(60_000);
  await expect.poll(() => server.beacons.length).toBeGreaterThan(0);
  expect(server.beacons.at(-1)).toEqual({ active: true });
});

test("with login off there is no session to be present in, and no column", async ({ page }) => {
  await page.clock.install();
  const server = installMock(page, { authEnabled: false });
  await gotoOrg(page);

  await expect(page.locator("#view table[data-dt-key='users'] thead")).not.toContainText("Presence");
  await expect(page.locator("#user-rows td.presence-cell")).toHaveCount(0);
  await page.clock.runFor(120_000);
  expect(server.beacons).toHaveLength(0);
});
