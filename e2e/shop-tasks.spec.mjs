// e2e for the open tasks under an order's positions in the shop (api/web/shop.js,
// ADR-draft-the-shop-shows-an-orders-open-tasks), against a fixture.
//
// What was asked for: an order's row says "Wartet" and not on whom. Under each
// position the shop now shows its open tasks and whom each waits for, and whoever
// holds one answers it there, on the task's own form.
//
// The fixture is two orders. Alice's own: a VPN waiting for Bob's approval, which
// she sees and may not answer. And one somebody else placed, in which the reader
// holds the approval: listed because of that, marked as such, and answerable.
//
// It loads the REAL shop.html with only the network replaced.
import { test, expect } from "@playwright/test";
import { readFileSync } from "node:fs";

// The approval form Atlas ships, read from the source it is seeded from — the test
// is about that form's heading saying for whom, so a stand-in would prove nothing.
const FORM = JSON.parse(readFileSync(
  new URL("../api/systemprocesses/form-genehmigung.json", import.meta.url), "utf8"));

function installFixture(form) {
  window.__unmatched = [];
  window.__completed = [];
  const line = (itemId, status) => ({ itemId, status });
  const OWN = {
    id: "ord_own", orderer: "usr_1", recipient: "usr_1", releaseId: "rel_t",
    createdAt: Date.UTC(2026, 8, 24, 10) * 1e6,
    lines: [line("vpn", "pending"), line("laptop", "done")],
  };
  const HELD = {
    id: "ord_held", orderer: "usr_2", recipient: "usr_2", releaseId: "rel_t",
    createdAt: Date.UTC(2026, 8, 24, 9) * 1e6,
    lines: [line("vpn", "pending")],
  };
  const task = (key, orderId, holder, mayWork) => ({
    key, orderId, positionId: "vpn", processInstanceKey: key + 1000, elementInstanceKey: key + 2000,
    processId: "atlas-genehmigung-fix", name: "Genehmigen", formId: "genehmigung",
    approval: true, holder, mayWork,
  });
  const TASKS = {
    tasks: [
      task(11, "ord_own", { kind: "fixed", name: "Bob Muster" }, false),
      task(22, "ord_held", { kind: "superior", name: "Anja" }, true),
    ],
    orders: [HELD],
    truncated: false,
  };
  const CAT = { id: "cat_t", rank: 1, languages: ["de", "en"],
    texts: { de: "Katalog", en: "Catalogue" }, items: [] };
  const ROUTES = {
    "/api/v1/auth/me": { user: { id: "usr_1", username: "anja", displayName: "Anja", roles: [] } },
    "/api/v1/shop/catalog": CAT,
    "/api/v1/catalogs/cat_t/releases": [],
    "/api/v1/orders": [OWN],
    "/api/v1/shop/tasks": TASKS,
    "/api/v1/inventory": { items: [] },
    "/api/v1/shop/favourites": { itemIds: [] },
    "/api/v1/principals": [{ id: "usr_1", name: "Anja" }, { id: "usr_2", name: "Carla" }],
    "/api/v1/forms/genehmigung": { id: "genehmigung", schema: form },
    "/api/v1/instances/2022/variables": {},
  };
  const json = (body) => Promise.resolve(new Response(JSON.stringify(body), {
    status: 200, headers: { "content-type": "application/json" },
  }));
  const realFetch = window.fetch.bind(window);
  window.fetch = (input, init) => {
    const url = new URL(String(input), window.location.origin);
    const path = url.pathname;
    // The form runtime and its stylesheets are the page's own static files.
    if (!path.startsWith("/api/")) return realFetch(input, init);
    const done = path.match(/^\/api\/v1\/tasks\/(\d+)\/complete$/);
    if (done && init && init.method === "POST") {
      window.__completed.push({ key: Number(done[1]), body: JSON.parse(init.body) });
      TASKS.tasks = TASKS.tasks.filter((x) => x.key !== Number(done[1]));
      return json({ taskKey: Number(done[1]) });
    }
    if (Object.prototype.hasOwnProperty.call(ROUTES, path)) return json(ROUTES[path]);
    window.__unmatched.push(path);
    return Promise.resolve(new Response("not in this fixture", { status: 404 }));
  };
}

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.addInitScript(installFixture, FORM);
  await page.goto("/shop.html");
  await page.getByRole("button", { name: "My orders" }).click();
  await expect(page.getByText("ord_own")).toBeVisible({ timeout: 10000 });
  page._errors = errors;
});

test.afterEach(async ({ page }) => {
  expect(page._errors, "no uncaught page errors").toEqual([]);
});

const taskRow = (page, key) => page.locator(`li[data-task="${key}"]`);

test("a position says whom it waits for", async ({ page }) => {
  const row = taskRow(page, 11);
  await expect(row).toContainText("Genehmigen");
  await expect(row).toContainText("Approval by Bob Muster");
  // Under its own position, not somewhere in the row: the laptop has no task.
  const vpn = page.locator("tr", { hasText: "ord_own" }).locator(".lines > li", { hasText: "vpn" });
  await expect(vpn.locator('li[data-task="11"]')).toHaveCount(1);
});

test("the orderer is not offered their own approval to answer", async ({ page }) => {
  await expect(taskRow(page, 11).getByRole("button")).toHaveCount(0);
});

test("an order the reader holds a task in is listed, and said to be so", async ({ page }) => {
  const held = page.locator("tr", { hasText: "ord_held" });
  await expect(held).toContainText("for you to handle");
  await expect(taskRow(page, 22)).toContainText("Approval by the line manager Anja");
  // Nothing on it that belongs to whoever placed it.
  await expect(held.getByRole("button", { name: /withdraw|give back/i })).toHaveCount(0);
});

test("the holder answers the task on its own form, in the row", async ({ page }) => {
  await taskRow(page, 22).getByRole("button", { name: "Work on it" }).click();
  const panel = taskRow(page, 22).locator(".cfg");
  await expect(panel.getByLabel("Genehmigen")).toBeVisible({ timeout: 10000 });
  // For whom, by name — the order's recipient is usr_2, and "Bestellt für usr_2"
  // asks the approver to know a key.
  await expect(panel).toContainText("Bestellt für Carla");
  await expect(panel).not.toContainText("usr_2");
  await panel.getByLabel("Genehmigen").check();
  await panel.getByLabel("Begründung").fill("passt");
  await panel.getByRole("button", { name: "Complete" }).click();

  await expect.poll(() => page.evaluate(() => window.__completed)).toEqual([
    { key: 22, body: { variables: { genehmigt: true, begruendung: "passt" } } },
  ]);
  // And the page read the orders again: the answered task is gone from the row.
  await expect(taskRow(page, 22)).toHaveCount(0);
});
