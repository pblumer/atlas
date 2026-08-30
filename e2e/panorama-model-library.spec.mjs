// Panorama P1 browser coverage: the real app shell exposes Panorama as an active
// app and renders the application-owned ArchiMate model library. The Go suite
// covers persistence and validation; this test pins the user-facing wiring.
import { test, expect } from "@playwright/test";

function installMock(page) {
  const models = [];
  const created = [];
  page.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (url.pathname.endsWith("/auth/me")) {
      return route.fulfill({ json: { authEnabled: false, user: null } });
    }
    if (url.pathname === "/api/v1/applications") {
      return route.fulfill({ json: [{ id: "app-1", name: "Enterprise Architecture", myRole: "owner" }] });
    }
    if (url.pathname === "/api/v1/panorama/models" && request.method() === "GET") {
      return route.fulfill({ json: models });
    }
    if (url.pathname === "/api/v1/panorama/models" && request.method() === "POST") {
      const payload = request.postDataJSON();
      created.push(payload);
      models.push({
        id: "0123456789abcdef0123456789abcdef",
        applicationId: payload.applicationId,
        name: payload.name,
        notation: payload.notation,
        revision: 1,
        createdAt: 1700000000,
        updatedAt: 1700000000,
      });
      return route.fulfill({ status: 201, json: models[0] });
    }
    return route.fulfill({ json: [] });
  });
  return { models, created };
}

test("Panorama is active and creates an application-owned blank ArchiMate model", async ({ page }) => {
  const state = installMock(page);
  const pageErrors = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));

  await page.goto("/index.html#/panorama");
  await expect(page.locator("#view h1")).toHaveText("Architecture models");
  await expect(page.locator("#topnav a", { hasText: "Models" })).toHaveAttribute("href", "#/panorama");
  await expect(page.locator("#view")).toContainText("No architecture models yet");

  await page.getByRole("button", { name: "Create new" }).click();
  page.once("dialog", (dialog) => dialog.accept("Application landscape"));
  await page.locator('button[data-act="new-panorama"]').click();

  await expect(page.locator("#view tbody")).toContainText("Application landscape");
  await expect(page.locator("#view tbody")).toContainText("Enterprise Architecture");
  expect(state.created).toHaveLength(1);
  expect(state.created[0].applicationId).toBe("app-1");
  expect(state.created[0].notation).toBe("archimate-3.2");
  expect(state.created[0].xml).toContain("http://www.opengroup.org/xsd/archimate/3.0/");
  expect(pageErrors).toEqual([]);
});
