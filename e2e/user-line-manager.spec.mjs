// An account's line manager, as an administrator sets it in Console → Organization
// (ADR-0470; userForm in app.js).
//
// A `superior` approval goes to the orderer's line manager as the account records it,
// so an installation without a directory mirror keeps the relation here. The form
// offers every other account, sends the field only when it changed — a save that
// re-sent an unchanged manager whose account has since gone would fail a rename — and
// shows a manager the directory reports without letting it be overwritten. Drives the
// real app shell against a mock of the users endpoints, like user-presence.spec.mjs.
import { test, expect } from "@playwright/test";

const ROSTER = [
  { id: "usr_1", username: "root", displayName: "Root", roles: ["admin"], disabled: false },
  { id: "usr_2", username: "mia", displayName: "Mia Keller", roles: ["user"], disabled: false },
  { id: "usr_3", username: "olli", displayName: "Olli Weber", roles: ["user"], disabled: false, managerId: "usr_2" },
  {
    id: "usr_4", username: "eva", displayName: "Eva Entra", roles: ["user"], disabled: false, source: "entra",
    managerId: "usr_2", managerDirectoryId: "6f1c0d2e-0000-4000-8000-000000000001",
  },
];

function installMock(page) {
  const state = { patches: [] };
  page.route("**/api/v1/**", async (route) => {
    const req = route.request();
    const path = new URL(req.url()).pathname;
    if (path.endsWith("/auth/me")) {
      return route.fulfill({ json: { authEnabled: true, user: { id: "usr_1", username: "root", roles: ["admin"] } } });
    }
    if (req.method() === "PATCH" && path.includes("/users/")) {
      state.patches.push({ id: path.split("/").pop(), body: req.postDataJSON() });
      return route.fulfill({ json: {} });
    }
    if (path.endsWith("/users")) return route.fulfill({ json: ROSTER });
    if (path.endsWith("/users/presence")) return route.fulfill({ json: [] });
    return route.fulfill({ json: [] });
  });
  return state;
}

async function editUser(page, id) {
  await page.goto("/index.html");
  await page.waitForFunction(
    () => document.querySelector("#view") && document.querySelector("#view").children.length > 0,
    null,
    { timeout: 15000 },
  );
  await page.evaluate(() => { location.hash = "#/console/org"; });
  await expect(page.locator("#user-rows tr")).toHaveCount(ROSTER.length);
  await page.locator(`#user-rows tr[data-id="${id}"] button[data-act="edit"]`).click();
  await expect(page.locator(".user-form")).toBeVisible();
}

const select = (page) => page.locator('.user-form select[name="managerId"]');

test("the form offers every other account as line manager, with the current one chosen", async ({ page }) => {
  installMock(page);
  await editUser(page, "usr_3");
  await expect(select(page)).toHaveValue("usr_2");
  const offered = await select(page).locator("option").evaluateAll((os) => os.map((o) => o.value));
  // Nobody is their own line manager, and "none" is a choice.
  expect(offered).toEqual(["", "usr_1", "usr_2", "usr_4"]);
});

test("a changed line manager is sent, and an unchanged one is not", async ({ page }) => {
  const state = installMock(page);
  await editUser(page, "usr_3");
  await page.locator('.user-form input[name="displayName"]').fill("Olli W.");
  await page.locator('.user-form button[type="submit"]').click();
  await expect.poll(() => state.patches.length).toBe(1);
  expect(state.patches[0].body).not.toHaveProperty("managerId");

  await page.locator('#user-rows tr[data-id="usr_3"] button[data-act="edit"]').click();
  await select(page).selectOption("usr_1");
  await page.locator('.user-form button[type="submit"]').click();
  await expect.poll(() => state.patches.length).toBe(2);
  expect(state.patches[1]).toEqual(expect.objectContaining({ id: "usr_3" }));
  expect(state.patches[1].body.managerId).toBe("usr_1");
});

test("clearing the line manager sends an empty id", async ({ page }) => {
  const state = installMock(page);
  await editUser(page, "usr_3");
  await select(page).selectOption("");
  await page.locator('.user-form button[type="submit"]').click();
  await expect.poll(() => state.patches.length).toBe(1);
  expect(state.patches[0].body.managerId).toBe("");
});

test("a line manager the directory reports is shown and not offered for editing", async ({ page }) => {
  const state = installMock(page);
  await editUser(page, "usr_4");
  await expect(select(page)).toBeDisabled();
  await expect(select(page)).toHaveValue("usr_2");
  await expect(page.locator(".user-form .manager-note")).toContainText("directory");
  await page.locator('.user-form button[type="submit"]').click();
  await expect.poll(() => state.patches.length).toBe(1);
  expect(state.patches[0].body).not.toHaveProperty("managerId");
});
