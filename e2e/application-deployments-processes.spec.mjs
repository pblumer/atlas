// An application's Deployments tab lists one row per deployed process, with the older
// versions folded under it, and carries what is done to a process: open it, see its
// instances, pause or resume its automatic starts, delete it
// (api/web/app.js renderAppDeployments, ADR-draft-the-application-holds-its-deployments).
// "Not assigned" has the same tab, built from the process listing.
import { test, expect } from "@playwright/test";

const APP = "app-1";
let definitions;
let calls;

function install(page) {
  page.route("**/api/v1/**", async (route) => {
    const req = route.request();
    const url = new URL(req.url());
    const path = url.pathname;
    calls.push(`${req.method()} ${path}${url.search}`);
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path === "/api/v1/applications") return route.fulfill({ json: [{ id: APP, name: "Rechnung", visibility: "private", myRole: "owner" }] });
    if (path === `/api/v1/applications/${APP}/deployments`) {
      return route.fulfill({ json: { id: APP, name: "Rechnung", version: 3, processes: 2, running: 5, finished: 7, definitions } });
    }
    if (req.method() === "PUT" && /\/processes\/\d+\/active$/.test(path)) {
      const key = Number(path.split("/")[4]);
      const active = JSON.parse(req.postData()).active;
      for (const d of definitions) if (d.key === key) d.active = active;
      return route.fulfill({ json: { key, active } });
    }
    if (req.method() === "DELETE" && /\/processes\/\d+$/.test(path)) {
      const key = Number(path.split("/")[4]);
      definitions = definitions.filter((d) => d.key !== key);
      return route.fulfill({ status: 204, body: "" });
    }
    if (path === "/api/v1/processes") {
      return route.fulfill({ json: [
        { key: 40, processId: "lose", name: "Lose", version: 2, deployedAt: 1792000000, active: true },
        { key: 39, processId: "lose", name: "Lose", version: 1, deployedAt: 1791000000, active: true },
        { key: 41, processId: "drin", name: "Drin", version: 1, deployedAt: 1792000000, active: true, projectId: APP },
      ] });
    }
    if (path === "/api/v1/instances/summary") {
      return route.fulfill({ json: [
        { processDefKey: 40, processId: "lose", active: 1, completed: 2 },
        { processDefKey: 39, processId: "lose", active: 3, completed: 0 },
      ] });
    }
    return route.fulfill({ json: [] });
  });
}

test.beforeEach(async ({ page }) => {
  definitions = [
    { key: 11, processId: "pruefen", name: "Prüfen", version: 3, current: true, active: true, running: 1, finished: 2, deployedAt: 1792000000 },
    { key: 10, processId: "pruefen", name: "Prüfen", version: 2, current: false, active: true, running: 4, finished: 5, deployedAt: 1791000000 },
    { key: 9, processId: "pruefen", name: "Prüfen", version: 1, current: false, active: false, running: 0, finished: 0, deployedAt: 1790000000 },
    { key: 12, processId: "zahlen", name: "Zahlen", version: 1, current: true, active: true, running: 0, finished: 0, deployedAt: 1792000000 },
  ];
  calls = [];
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page._errors = errors;
  install(page);
});

test.afterEach(async ({ page }) => {
  expect(page._errors, "no uncaught page errors").toEqual([]);
});

const row = (page, pid) => page.locator(`#pd-processes tr[data-process="${pid}"]`);
const menu = async (page, pid, label) => {
  await row(page, pid).locator(".dropdown-toggle").click();
  // An item reads its icon and its label; matched at the end, "Aktivieren" is not
  // "Deaktivieren".
  await row(page, pid).locator(".dropdown-menu button", { hasText: new RegExp(label + "$") }).click();
};

test("one row per process, its older versions folded under it", async ({ page }) => {
  await page.goto(`/index.html#/modeler/p/${APP}/deployments`);
  await expect(page.locator("#pd-processes tbody tr")).toHaveCount(2);
  await expect(page.locator("#pd-deployments .sec-head").first()).toContainText("2 Prozesse");

  const pruefen = row(page, "pruefen");
  await expect(pruefen.locator(".chip")).toHaveText("v3");
  // The process holds the instances of all its versions.
  await expect(pruefen.locator("td").nth(3)).toHaveText("5");
  await expect(pruefen.locator("td").nth(4)).toHaveText("7");

  const older = pruefen.locator("details.fold-sub.dep-versions");
  await expect(older.locator("summary")).toHaveText("2 ältere Versionen");
  await expect(older.locator("li").first()).toBeHidden();
  await older.locator("summary").click();
  await expect(older.locator("li")).toHaveCount(2);
  await expect(older.locator('li a[href="#/operations/p/10"]')).toHaveText("v2");
  await expect(older.locator("li").nth(1).locator(".pill.warn")).toHaveText("pausiert");
  await expect(row(page, "zahlen").locator("details")).toHaveCount(0);
});

test("a process is paused, resumed and deleted from its row", async ({ page }) => {
  await page.goto(`/index.html#/modeler/p/${APP}/deployments`);
  await expect(row(page, "pruefen")).toBeVisible();

  await menu(page, "pruefen", "Deaktivieren");
  await expect.poll(() => calls.includes("PUT /api/v1/processes/11/active")).toBe(true);
  await expect(row(page, "pruefen").locator("td").nth(2)).toContainText("pausiert");

  await menu(page, "pruefen", "Aktivieren");
  await expect(row(page, "pruefen").locator("td").nth(2)).not.toContainText("pausiert");

  page.on("dialog", (d) => d.accept());
  await menu(page, "zahlen", "Löschen");
  await expect(row(page, "zahlen")).toHaveCount(0);
  expect(calls).toContain("DELETE /api/v1/processes/12");
});

test("a row opens the process in the Modeler and its instances in Operations", async ({ page }) => {
  await page.goto(`/index.html#/modeler/p/${APP}/deployments`);
  const items = row(page, "pruefen").locator(".dropdown-menu");
  await expect(items.locator('a[href="#/modeler/d/11"]')).toHaveCount(1);
  await expect(items.locator('a[href="#/operations/p/11"]')).toHaveCount(1);
});

test("Not assigned lists the processes filed under no application", async ({ page }) => {
  await page.goto("/index.html#/modeler/p/ungrouped/deployments");
  await expect(page.locator("#pd-processes tbody tr")).toHaveCount(1);
  const lose = row(page, "lose");
  await expect(lose.locator(".chip")).toHaveText("v2");
  await expect(lose.locator("td").nth(3)).toHaveText("4");
  await expect(lose.locator("td").nth(4)).toHaveText("2");
  // No releases and no constants: not an application.
  await expect(page.locator("#pd-releases")).toHaveCount(0);
  await expect(page.locator('#pd-tabs button[data-pane="constants"]')).toHaveCount(0);
});
