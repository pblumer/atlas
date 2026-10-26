// The Users card fits the content column, from the browser (the Users card in app.js,
// viewConsoleOrg).
//
// What only a real DOM proves: with a roster as it really looks — an id, an address
// that cannot wrap, four roles, a last login and a presence pill on one row — the
// table is no wider than its card at the console's own column width, so the card does
// not scroll it and nothing is cut off at its edge. It did both while the row carried
// four buttons side by side: they alone set 342px of a 1156px minimum in a 1074px card,
// and Delete was drawn past the edge on a screen with room to spare beside the column.
// The actions that went into the row's ⋯ menu must still do what the buttons did.
// Drives the REAL app shell against a routed mock of the roster, like
// user-last-login.spec.mjs.
import { test, expect } from "@playwright/test";

// Wider than the content column, so the card gets the column's full 1120px: the width
// the overflow was seen at, and the one every admin screen is laid out for.
test.use({ viewport: { width: 1280, height: 800 } });

const ROSTER = [
  { id: "usr_9a41c07e5d2b8f36e1047c2a", username: "root", displayName: "Root Admin",
    email: "root@example.org", roles: ["admin", "user", "modeler", "operator"],
    lastLoginDate: "2026-10-23", presence: { userId: "usr_9a41c07e5d2b8f36e1047c2a", state: "online", sessions: 1 } },
  { id: "usr_5f2d8e14b7a09c63d18e4b70", username: "valentina.rossi", displayName: "Valentina Rossi-Meier",
    email: "valentina.rossi-meier@example.admin.ch", roles: ["admin", "user", "modeler", "operator"],
    presence: { userId: "usr_5f2d8e14b7a09c63d18e4b70", state: "offline" } },
  { id: "usr_c3e70b9a2d5f4e18a6b29d04", username: "konstantin.lehmann", displayName: "Konstantin Lehmann",
    email: "konstantin.lehmann@example.admin.ch", roles: ["modeler", "user"], disabled: false,
    presence: { userId: "usr_c3e70b9a2d5f4e18a6b29d04", state: "offline" } },
];
const ME = ROSTER[0].id;
const OTHER = ROSTER[2];

// installMock answers as an admin-enforcing instance and records every write the page
// sends to an account, so a test can see which request a menu entry made. The app
// shell's presence beacon writes too, and is not one of them.
function installMock(page) {
  const writes = [];
  page.route("**/api/v1/**", async (route) => {
    const req = route.request();
    const path = new URL(req.url()).pathname;
    if (req.method() !== "GET") {
      if (path.startsWith("/api/v1/users/")) {
        writes.push({ method: req.method(), path, body: req.postData() ? req.postDataJSON() : null });
      }
      return route.fulfill({ json: {} });
    }
    if (path.endsWith("/auth/me")) {
      return route.fulfill({ json: { authEnabled: true, user: { id: ME, username: "root", roles: ["admin"] } } });
    }
    if (path.endsWith("/users")) return route.fulfill({ json: ROSTER });
    return route.fulfill({ json: [] });
  });
  return writes;
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

const rowOf = (page, id) => page.locator(`#user-rows tr[data-id="${id}"]`);

// openMenu opens a row's ⋯ menu and returns it.
async function openMenu(page, id) {
  await rowOf(page, id).locator(".dropdown-toggle").click();
  const menu = rowOf(page, id).locator(".dropdown-menu");
  await expect(menu).toBeVisible();
  return menu;
}

test("the roster fits its card at the column's width, with nothing cut off", async ({ page }) => {
  installMock(page);
  await gotoOrg(page);

  const box = await page.evaluate(() => {
    const table = document.querySelector("table[data-dt-key='users']");
    const card = table.parentElement;
    const cardRight = card.getBoundingClientRect().right;
    // The right edge of the last control on every row: where Delete used to hang.
    const lastRight = Math.max(...[...document.querySelectorAll("#user-rows tr")].map((r) =>
      r.cells[r.cells.length - 1].lastElementChild.getBoundingClientRect().right));
    return { client: card.clientWidth, scroll: card.scrollWidth, cardRight, lastRight };
  });
  // The column really is the limit here — otherwise this proves nothing.
  expect(box.client).toBeLessThanOrEqual(1080);
  expect(box.scroll).toBeLessThanOrEqual(box.client);
  expect(box.lastRight).toBeLessThanOrEqual(box.cardRight);
});

test("Edit stays one click on the row; the rest are in its menu", async ({ page }) => {
  installMock(page);
  await gotoOrg(page);

  const row = rowOf(page, OTHER.id);
  await expect(row.locator("td.row-actions > button[data-act='edit']")).toBeVisible();
  const menu = await openMenu(page, OTHER.id);
  // Each entry opens with its icon, so the label is matched at the end.
  await expect(menu.locator("button[data-act]")).toHaveText([/Set password…$/, /Disable account$/, /Delete…$/]);
  await expect(menu.locator("button[data-act='delete']")).toHaveClass(/danger/);
});

test("the menu's entries do what the row's buttons did", async ({ page }) => {
  const writes = installMock(page);
  await gotoOrg(page);
  const user = `/api/v1/users/${OTHER.id}`;

  // Disable takes effect at once, as the button did.
  await (await openMenu(page, OTHER.id)).locator("button[data-act='toggle']").click();
  await expect.poll(() => writes.length).toBe(1);
  expect(writes[0]).toEqual({ method: "PATCH", path: user, body: { disabled: true } });
  await expect(page.locator(".dropdown-menu:not([hidden])")).toHaveCount(0);

  // Password asks for the new one first.
  page.once("dialog", (d) => d.accept("correct-horse-battery"));
  await (await openMenu(page, OTHER.id)).locator("button[data-act='password']").click();
  await expect.poll(() => writes.length).toBe(2);
  expect(writes[1]).toEqual({ method: "POST", path: `${user}/password`, body: { password: "correct-horse-battery" } });

  // Delete asks before it deletes, and a refusal deletes nothing.
  page.once("dialog", (d) => d.dismiss());
  await (await openMenu(page, OTHER.id)).locator("button[data-act='delete']").click();
  page.once("dialog", (d) => d.accept());
  await (await openMenu(page, OTHER.id)).locator("button[data-act='delete']").click();
  await expect.poll(() => writes.length).toBe(3);
  expect(writes[2]).toEqual({ method: "DELETE", path: user, body: null });
});
