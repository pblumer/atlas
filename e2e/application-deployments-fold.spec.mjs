// End-to-end coverage for the folding lists of an application's Deployments tab
// (ADR-0128): the deployed definitions, the deployment targets and the release
// history each fold away under their heading, which keeps the count while folded,
// and the browser remembers each choice for every application (sectionState).
import { test, expect } from "@playwright/test";

const APP = "app-1";
const SECTIONS = ["appdep-definitions", "appdep-targets", "appdep-releases"];

function installMock(page) {
  page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path === "/api/v1/applications") return route.fulfill({ json: [{ id: APP, name: "Rechnung", visibility: "private" }] });
    if (path === `/api/v1/applications/${APP}/deployments`) {
      return route.fulfill({ json: {
        name: "Rechnung", version: 3, processes: 1, running: 0, finished: 0,
        definitions: [
          { key: 11, processId: "proc_rechnung", name: "Rechnung prüfen", version: 3, current: true, active: true, running: 0, finished: 0, deployedAt: 1792000000 },
          { key: 10, processId: "proc_rechnung", name: "Rechnung prüfen", version: 2, current: false, active: true, running: 0, finished: 0, deployedAt: 1791000000 },
        ],
      } });
    }
    if (path === `/api/v1/applications/${APP}/releases`) {
      return route.fulfill({ json: [3, 2, 1].map((v) => ({ id: `rel-${v}`, version: v, publishedAt: 1790000000 + v, members: [], note: `Release ${v}` })) });
    }
    if (path === `/api/v1/applications/${APP}/targets`) {
      return route.fulfill({ json: [{ targetName: "PROD", baseUrl: "https://prod.example", bound: true, reachable: true, version: 2, running: 0, finished: 0 }] });
    }
    return route.fulfill({ json: [] });
  });
}

async function openDeployments(page) {
  await page.evaluate((id) => { location.hash = "#/modeler/p/" + id; }, APP);
  await page.locator('#pd-tabs button[data-pane="deployments"]').click();
  await expect(page.locator("#pd-releases")).toBeAttached();
}

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  installMock(page);
  await page.goto("/index.html");
  await page.waitForFunction(() => document.querySelector("#view")?.children.length > 0, null, { timeout: 15000 });
});

test("each list of the Deployments tab folds away, and stays folded", async ({ page }) => {
  await openDeployments(page);
  for (const id of SECTIONS) {
    const toggle = page.locator(`.section-toggle[data-section="${id}"]`);
    const body = page.locator(`#sec-${id}`);
    await expect(toggle).toHaveAttribute("aria-expanded", "true");
    await expect(body).toBeVisible();
    await toggle.click();
    await expect(toggle).toHaveAttribute("aria-expanded", "false");
    await expect(body).toBeHidden();
  }
  // A folded section still says what it holds.
  const heads = page.locator("#pd-deployments .sec-head");
  await expect(heads.nth(0)).toContainText("2 Definitionen");
  await expect(heads.nth(1)).toContainText("1 Ziel");
  await expect(heads.nth(2)).toContainText("3 Releases");

  await page.reload();
  await page.waitForFunction(() => document.querySelector("#view")?.children.length > 0, null, { timeout: 15000 });
  await openDeployments(page);
  for (const id of SECTIONS) {
    await expect(page.locator(`#sec-${id}`)).toBeHidden();
    await expect(page.locator(`.section-toggle[data-section="${id}"]`)).toHaveAttribute("aria-expanded", "false");
  }
  // The summary above the lists is not folded with them.
  await expect(page.locator("#pd-deployments .stats")).toBeVisible();

  await page.click('.section-toggle[data-section="appdep-releases"]');
  await expect(page.locator("#sec-appdep-releases")).toBeVisible();
  await expect(page.locator("#pd-releases")).toContainText("Release 3");
  expect(page.__errors).toEqual([]);
});

// The tab loads lazily, after the route enhanced the view's tables; folding must not
// cost the lists the filter row every other list in the Console has.
test("the folding lists keep their filter row", async ({ page }) => {
  await openDeployments(page);
  const releases = page.locator("#pd-releases table");
  await expect(releases.locator(".dt-filter").first()).toBeVisible();
  await releases.locator(".dt-filter").nth(3).fill("Release 2");
  await expect(releases.locator("tbody tr:not([hidden])")).toHaveCount(1);
  await expect(page.locator("#sec-appdep-definitions table .dt-filter").first()).toBeVisible();
  expect(page.__errors).toEqual([]);
});
