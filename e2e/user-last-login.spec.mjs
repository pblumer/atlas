// The day each account last signed in, from the browser (ADR-0477;
// the Users card in app.js, viewConsoleOrg).
//
// What only a real DOM proves: the column turns the stored UTC day into an age a
// person reads at a glance, counts that age in UTC so it is right in every time zone
// a reader may have chosen, says plainly when there is nothing recorded, and sorts
// so that the accounts nobody uses come first. Drives the REAL app shell against a
// routed mock of the roster, like user-presence.spec.mjs, with the browser in Zurich
// and the clock fixed half an hour after local midnight — the one half hour of the
// day in which local and UTC calendars disagree.
import { test, expect } from "@playwright/test";

test.use({ timezoneId: "Europe/Zurich" });

// 00:30 on 24 October in Zurich, which is still 23 October in UTC.
const NOW = new Date("2026-10-23T22:30:00Z");

const ROSTER = [
  { id: "usr_1", username: "root", roles: ["admin"], lastLoginDate: "2026-10-23",
    presence: { userId: "usr_1", state: "online", sessions: 1 } },
  { id: "usr_2", username: "mia", roles: ["modeler"], lastLoginDate: "2026-10-22",
    presence: { userId: "usr_2", state: "offline" } },
  { id: "usr_3", username: "olli", roles: ["user"], lastLoginDate: "2026-03-02",
    presence: { userId: "usr_3", state: "offline" } },
  { id: "usr_4", username: "testuser", roles: ["user"],
    presence: { userId: "usr_4", state: "offline" } },
];

function installMock(page, { authEnabled = true } = {}) {
  page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/me")) {
      return route.fulfill({ json: { authEnabled, user: { id: "usr_1", username: "root", roles: ["admin"] } } });
    }
    if (path.endsWith("/auth/presence")) return route.fulfill({ json: { ok: true } });
    if (path.endsWith("/users/presence")) return route.fulfill({ json: [] });
    if (path.endsWith("/users")) return route.fulfill({ json: ROSTER });
    return route.fulfill({ json: [] });
  });
}

async function gotoOrg(page) {
  await page.goto("/index.html");
  await page.waitForFunction(
    () => document.querySelector("#view") && document.querySelector("#view").children.length > 0,
    null,
    { timeout: 15000 },
  );
  await page.evaluate(() => { location.hash = "#/console/org"; });
  await expect(page.locator("#user-rows tr")).toHaveCount(ROSTER.length);
}

// The last-login cell is the one right after Status.
const cellOf = (page, id) => page.locator(`#user-rows tr[data-id="${id}"] td`).nth(4);

test("the column says how long ago, counted in UTC days", async ({ page }) => {
  await page.clock.setFixedTime(NOW);
  installMock(page);
  await gotoOrg(page);

  await expect(page.locator("#view table[data-dt-key='users'] thead")).toContainText("Last login");
  // Local time is already the 24th; the stored day is the UTC 23rd, and it is still
  // the 23rd in UTC — so this is today, not yesterday.
  await expect(cellOf(page, "usr_1")).toContainText("today");
  await expect(cellOf(page, "usr_2")).toContainText("yesterday");
  await expect(cellOf(page, "usr_3")).toContainText("235 days ago");
  // The day itself is shown too, and the title says what it is and is not.
  await expect(cellOf(page, "usr_3")).toContainText("2026-03-02");
  await expect(cellOf(page, "usr_3")).toHaveAttribute("title", /UTC calendar day/);

  // Nothing recorded is said as such, never as a made-up date.
  await expect(cellOf(page, "usr_4")).toHaveText("—");
  await expect(cellOf(page, "usr_4")).toHaveAttribute("title", /No login recorded/);
});

test("sorting by it puts the accounts nobody uses first", async ({ page }) => {
  await page.clock.setFixedTime(NOW);
  installMock(page);
  await gotoOrg(page);

  await page.locator("#view table[data-dt-key='users'] thead th", { hasText: "Last login" }).click();
  const order = await page.locator("#user-rows tr").evaluateAll((rows) => rows.map((r) => r.dataset.id));
  expect(order).toEqual(["usr_4", "usr_3", "usr_2", "usr_1"]);
});

test("without enforced login there are no logins, and no column", async ({ page }) => {
  await page.clock.setFixedTime(NOW);
  installMock(page, { authEnabled: false });
  await gotoOrg(page);

  await expect(page.locator("#view table[data-dt-key='users'] thead")).not.toContainText("Last login");
});
