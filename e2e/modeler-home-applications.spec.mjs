// The Modeler home is one table of applications (api/web/app.js viewModelerHome,
// ADR-0541): no list of deployed processes of its
// own, a table that folds under its heading like every list (ADR-0505), and a column
// that says what each application has deployed and links to where it is listed.
import { test, expect } from "@playwright/test";

const APPS = [
  { id: "app-1", name: "Rechnung", visibility: "private", myRole: "owner", updatedAt: 1790000000 },
  { id: "app-2", name: "Leer", visibility: "private", myRole: "owner", updatedAt: 1789000000 },
];
const PROCESSES = [
  // Two versions of one process: one process, and its newest version is paused.
  { key: 11, processId: "pruefen", name: "Prüfen", version: 2, deployedAt: 1790000000, active: false, projectId: "app-1" },
  { key: 10, processId: "pruefen", name: "Prüfen", version: 1, deployedAt: 1789000000, active: true, projectId: "app-1" },
  { key: 12, processId: "zahlen", name: "Zahlen", version: 1, deployedAt: 1790000000, active: true, projectId: "app-1" },
  // Filed under no application.
  { key: 13, processId: "lose", name: "Lose", version: 1, deployedAt: 1790000000, active: true },
];

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page._errors = errors;
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path === "/api/v1/applications") return route.fulfill({ json: APPS });
    if (path === "/api/v1/processes") return route.fulfill({ json: PROCESSES });
    return route.fulfill({ json: [] });
  });
  await page.goto("/index.html#/modeler");
  await expect(page.locator("#proj-rows tr")).toHaveCount(3);
});

test.afterEach(async ({ page }) => {
  expect(page._errors, "no uncaught page errors").toEqual([]);
});

const row = (page, name) => page.locator("#proj-rows tr", { hasText: name });

test("the home page lists applications and no deployed processes of its own", async ({ page }) => {
  await expect(page.locator("#view h1")).toHaveText("Modeler");
  await expect(page.locator("#sec-deployed")).toHaveCount(0);
  await expect(page.locator('#view a[href^="#/modeler/d/"]')).toHaveCount(0);
});

test("the application table folds under its heading and remembers it", async ({ page }) => {
  const toggle = page.locator('.section-toggle[data-section="home-applications"]');
  await expect(toggle).toHaveText("Anwendungen");
  await expect(page.locator("#home-applications .sec-head")).toContainText("2 Anwendungen");
  await toggle.click();
  await expect(page.locator("#sec-home-applications")).toBeHidden();
  await page.reload();
  await expect(page.locator("#sec-home-applications")).toBeHidden();
  await page.locator('.section-toggle[data-section="home-applications"]').click();
  await expect(page.locator("#sec-home-applications")).toBeVisible();
});

test("each application says how many processes it has deployed, and links to them", async ({ page }) => {
  const rechnung = row(page, "Rechnung");
  const link = rechnung.locator('td:nth-child(3) a[href="#/modeler/p/app-1/deployments"]');
  await expect(link).toHaveText("2 Prozesse");
  await expect(rechnung.locator(".pill.warn")).toHaveText("1 pausiert");
  await expect(row(page, "Leer").locator("td").nth(2)).toHaveText("—");

  // A process filed under no application makes "Not assigned" appear, once, with its
  // own way to the deployments.
  const loose = row(page, "Nicht zugeordnet");
  await expect(loose).toHaveCount(1);
  await expect(loose.locator('td:nth-child(3) a[href="#/modeler/p/ungrouped/deployments"]')).toHaveText("1 Prozess");
});

test("the link opens the application on its Deployments tab", async ({ page }) => {
  await page.route("**/api/v1/applications/app-1/deployments", (route) => route.fulfill({ json: {
    id: "app-1", name: "Rechnung", version: 2, processes: 2, running: 0, finished: 0,
    definitions: [
      { key: 11, processId: "pruefen", name: "Prüfen", version: 2, current: true, active: false, running: 0, finished: 0, deployedAt: 1790000000 },
      { key: 10, processId: "pruefen", name: "Prüfen", version: 1, current: false, active: true, running: 0, finished: 0, deployedAt: 1789000000 },
      { key: 12, processId: "zahlen", name: "Zahlen", version: 1, current: true, active: true, running: 0, finished: 0, deployedAt: 1790000000 },
    ],
  } }));
  await row(page, "Rechnung").locator('td:nth-child(3) a[href="#/modeler/p/app-1/deployments"]').click();
  await expect(page.locator('#pd-tabs button[data-pane="deployments"]')).toHaveClass(/\bactive\b/);
  await expect(page.locator("#pane-deployments")).toBeVisible();
  await expect(page.locator("#pd-processes tbody tr")).toHaveCount(2);
});
