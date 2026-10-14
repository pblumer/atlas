// e2e for the actions on a held position in the shop (api/web/shop.js, ADR-0429 §2),
// against a fixture.
//
// A product says what can be asked of what somebody holds. Under a held position the
// shop draws a button per action the server says this reader may ask for, labelled
// in the reader's language and greyed out with the reason when the position does not
// take it now. An action with a form opens it under the position; one without asks
// first. Either sends a command id, so a retry is the same request.
//
// It loads the REAL shop.html with only the network replaced.
import { test, expect } from "@playwright/test";

// A form-js schema for the storage extension: one number the process needs.
const EXTEND_FORM = {
  type: "default", schemaVersion: 4, id: "extend-form",
  components: [{ type: "number", key: "quotaGB", label: "Quota in GB", validate: { required: true } }],
};

function installFixture(form) {
  window.__unmatched = [];
  window.__asked = [];
  const ORDER = {
    id: "ord_own", orderer: "usr_1", recipient: "usr_1", releaseId: "rel_t",
    createdAt: Date.UTC(2026, 9, 1, 10) * 1e6,
    lines: [
      { itemId: "mailbox", status: "done", lifecycleProcess: "mbx-strand", lifecycleForm: "per-position",
        actions: [
          { key: "provision", message: "mbx.provision", effect: "provision" },
          { key: "deprovision", message: "mbx.deprovision", effect: "deprovision", triggers: ["customer", "operator"] },
        ] },
      // A right only an operator gives back: the reader is not offered its return.
      { itemId: "token", status: "done",
        actions: [
          { key: "provision", message: "tok.provision", effect: "provision" },
          { key: "deprovision", message: "tok.deprovision", effect: "deprovision", triggers: ["operator"] },
        ] },
    ],
  };
  const ACTIONS = {
    position: "mailbox",
    actions: [
      { key: "storage-extend", effect: "change", triggers: ["customer"], form: "extend-form",
        labels: { de: "Speicher erweitern", en: "Extend storage" }, available: true },
      { key: "password-reset", effect: "service", triggers: ["customer"],
        labels: { de: "Passwort zurücksetzen", en: "Reset password" }, available: true },
      { key: "archive", effect: "service", triggers: ["customer"], labels: { en: "Archive" },
        available: false, why: "the position's process does not take this action now" },
      // A product that still carries the operation map: its change has no label.
      { key: "change", effect: "change", triggers: ["customer", "operator"], available: true },
    ],
  };
  const CAT = { id: "cat_t", rank: 1, languages: ["de", "en"],
    texts: { de: "Katalog", en: "Catalogue" }, items: [] };
  const ROUTES = {
    "/api/v1/auth/me": { authEnabled: true, user: { id: "usr_1", username: "anja", displayName: "Anja", roles: [] } },
    "/api/v1/shop/catalog": CAT,
    "/api/v1/catalogs/cat_t/releases": [],
    "/api/v1/orders": [ORDER],
    "/api/v1/shop/tasks": { tasks: [], orders: [], truncated: false },
    "/api/v1/inventory": { items: [] },
    "/api/v1/shop/favourites": { itemIds: [] },
    "/api/v1/principals": [{ id: "usr_1", name: "Anja" }],
    "/api/v1/forms/extend-form": { id: "extend-form", schema: form },
    "/api/v1/orders/ord_own/lines/mailbox/actions": ACTIONS,
  };
  const json = (body) => Promise.resolve(new Response(JSON.stringify(body), {
    status: 200, headers: { "content-type": "application/json" },
  }));
  const realFetch = window.fetch.bind(window);
  window.fetch = (input, init) => {
    const url = new URL(String(input), window.location.origin);
    const path = url.pathname;
    if (!path.startsWith("/api/")) return realFetch(input, init);
    const ask = path.match(/^\/api\/v1\/orders\/ord_own\/lines\/mailbox\/actions\/([a-z0-9-]+)$/);
    if (ask && init && init.method === "POST") {
      window.__asked.push({ action: ask[1], body: JSON.parse(init.body) });
      return json({ action: ask[1], instanceKey: 77, process: "mbx-strand" });
    }
    if (Object.prototype.hasOwnProperty.call(ROUTES, path)) return json(ROUTES[path]);
    window.__unmatched.push(path);
    return Promise.resolve(new Response("not in this fixture", { status: 404 }));
  };
}

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.addInitScript(installFixture, EXTEND_FORM);
  await page.goto("/shop.html");
  await page.getByRole("button", { name: "My orders" }).click();
  await expect(page.getByText("ord_own")).toBeVisible({ timeout: 10000 });
  page._errors = errors;
});

test.afterEach(async ({ page }) => {
  expect(page._errors, "no uncaught page errors").toEqual([]);
});

const mailbox = (page) => page.locator(".lines > li", { hasText: "mailbox" });
const asked = (page) => page.evaluate(() => window.__asked);

test("a held position offers its actions in the reader's language", async ({ page }) => {
  const row = mailbox(page);
  await expect(row.getByRole("button", { name: "Extend storage" })).toBeEnabled();
  await expect(row.getByRole("button", { name: "Reset password" })).toBeEnabled();
  // Not now, and said why — the process decides, and the button says what it said.
  const archive = row.getByRole("button", { name: "Archive" });
  await expect(archive).toBeDisabled();
  await expect(archive).toHaveAttribute("title", /does not take this action now/);
  // An unlabelled change is named in the reader's language, not by its key.
  await expect(row.getByRole("button", { name: "Change", exact: true })).toBeEnabled();
});

test("an action without a form asks first, then sends a command id", async ({ page }) => {
  const prompts = [];
  page.on("dialog", (d) => { prompts.push(d.message()); d.accept(); });
  await mailbox(page).getByRole("button", { name: "Reset password" }).click();
  await expect.poll(() => asked(page)).toHaveLength(1);
  const [sent] = await asked(page);
  expect(prompts).toEqual(["Really ask for this: Reset password"]);
  expect(sent.action).toBe("password-reset");
  expect(sent.body.commandId).toBeTruthy();
  expect(sent.body.variables).toEqual({});
  // The position stays held either way; the page says the action was asked for.
  await expect(mailbox(page)).toContainText("Requested: Reset password");
});

test("an action with a form opens it under the position and sends its answers", async ({ page }) => {
  await mailbox(page).getByRole("button", { name: "Extend storage" }).click();
  const quota = page.getByLabel("Quota in GB");
  await expect(quota).toBeVisible({ timeout: 10000 });
  await quota.fill("100");
  await page.getByRole("button", { name: "Send" }).click();
  await expect.poll(() => asked(page)).toHaveLength(1);
  const [sent] = await asked(page);
  expect(sent.action).toBe("storage-extend");
  expect(sent.body.commandId).toBeTruthy();
  expect(sent.body.variables).toEqual({ quotaGB: 100 });
});

test("a form left empty is not sent", async ({ page }) => {
  await mailbox(page).getByRole("button", { name: "Extend storage" }).click();
  await expect(page.getByLabel("Quota in GB")).toBeVisible({ timeout: 10000 });
  await page.getByRole("button", { name: "Send" }).click();
  await expect(page.locator(".cfg .error")).toBeVisible();
  expect(await asked(page)).toEqual([]);
});

test("a return only an operator gives is not offered to the holder", async ({ page }) => {
  await expect(mailbox(page).getByRole("button", { name: "Return" })).toHaveCount(1);
  const token = page.locator(".lines > li", { hasText: "token" });
  await expect(token).toBeVisible();
  await expect(token.getByRole("button", { name: "Return" })).toHaveCount(0);
});
