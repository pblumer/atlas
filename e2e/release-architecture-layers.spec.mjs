// End-to-end coverage for the layers of a release's architecture sketch
// (ADR-0495, slice A): Applications → Deployments →
// Release history → Architecture offers one checkbox per layer the release records,
// and the picture and both downloads follow what is ticked.
import { test, expect } from "@playwright/test";

const APP = "app-1";
const BASE = `/api/v1/applications/${APP}/releases/2/architecture`;

function installMock(page, { recorded = ["application", "technology"] } = {}) {
  const svgRequests = [];
  page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname;
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path === "/api/v1/applications") return route.fulfill({ json: [{ id: APP, name: "Rechnung", visibility: "private" }] });
    if (path === `/api/v1/applications/${APP}/deployments`) {
      return route.fulfill({ json: { name: "Rechnung", version: 2, processes: 1, running: 0, finished: 0, definitions: [] } });
    }
    if (path === `/api/v1/applications/${APP}/releases`) {
      return route.fulfill({ json: [{ id: "rel-2", version: 2, publishedAt: 1792000000, members: [], note: "" }] });
    }
    if (path === BASE) {
      return route.fulfill({ json: { version: 2, layers: { recorded, shown: recorded }, architecture: {} } });
    }
    if (path === BASE + ".svg") {
      const layers = url.searchParams.get("layers") || "";
      svgRequests.push(layers);
      return route.fulfill({
        contentType: "image/svg+xml",
        body: `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><title>${layers}</title></svg>`,
      });
    }
    return route.fulfill({ json: [] });
  });
  return svgRequests;
}

async function openArchitecture(page) {
  await page.goto("/index.html");
  await page.waitForFunction(() => document.querySelector("#view")?.children.length > 0, null, { timeout: 15000 });
  await page.evaluate((id) => { location.hash = "#/modeler/p/" + id; }, APP);
  await page.locator('#pd-tabs button[data-pane="deployments"]').click();
  await page.locator("button[data-architecture]").click();
  await expect(page.locator('.modal[aria-label="Architecture of v2"]')).toBeVisible();
}

test("each recorded layer is a checkbox, and the picture and the downloads follow it", async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  const svgRequests = installMock(page);
  await openArchitecture(page);

  const boxes = page.locator("[data-layers] input[type=checkbox]");
  await expect(boxes).toHaveCount(2);
  await expect(boxes.nth(0)).toBeChecked();
  await expect(boxes.nth(1)).toBeChecked();
  await expect.poll(() => svgRequests).toEqual(["application,technology"]);
  await expect(page.locator("[data-svg]")).toHaveAttribute("href", `${BASE}.svg?layers=application%2Ctechnology`);

  await page.locator('[data-layers] input[value="technology"]').uncheck();
  await expect.poll(() => svgRequests.at(-1)).toBe("application");
  await expect(page.locator("[data-xml]")).toHaveAttribute("href", `${BASE}.xml?layers=application`);
  await expect(page.locator("[data-svg]")).toHaveAttribute("href", `${BASE}.svg?layers=application`);
  expect(errors).toEqual([]);
});

test("the last ticked layer cannot be switched off", async ({ page }) => {
  const svgRequests = installMock(page);
  await openArchitecture(page);
  await page.locator('[data-layers] input[value="application"]').uncheck();
  await expect.poll(() => svgRequests.at(-1)).toBe("technology");

  // A click rather than uncheck(): uncheck() insists the box ends up empty, and
  // keeping it ticked is the behaviour under test.
  const technology = page.locator('[data-layers] input[value="technology"]');
  await technology.click();
  await expect(technology).toBeChecked();
  await expect(page.locator("#toast")).toContainText("at least one layer");
  expect(svgRequests.at(-1)).toBe("technology");
});

test("a release that records one layer offers no choice", async ({ page }) => {
  const svgRequests = installMock(page, { recorded: ["application"] });
  await openArchitecture(page);
  await expect(page.locator("[data-layers]")).toHaveCount(0);
  await expect.poll(() => svgRequests).toEqual(["application"]);
});
