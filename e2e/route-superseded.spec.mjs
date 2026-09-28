import { test, expect } from "@playwright/test";

// Access review and Reconciliation are loaded on demand, and the router hands each a
// closure that asks whether a later navigation has superseded it. Both routes once
// read a generation they never took, so the view's first question after its first
// await threw and the page showed "gen is not defined" instead of the view.

function installMock(page) {
  page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path.endsWith("/api/v1/recertification")) return route.fulfill({ json: [{ id: "rc_1", name: "Q3 review" }] });
    if (path.endsWith("/api/v1/recertification/rc_1")) {
      return route.fulfill({ json: { counts: { rows: 0 }, rows: [] } });
    }
    return route.fulfill({ json: [] });
  });
}

for (const [hash, heading] of [
  ["#/tasks/recertification", "Access review"],
  ["#/operations/reconciliation", null],
]) {
  test(`${hash} opens without an error`, async ({ page }) => {
    const errors = [];
    page.on("pageerror", (e) => errors.push(e.message));
    installMock(page);
    await page.goto(`/index.html${hash}`);
    if (heading) await expect(page.getByRole("heading", { name: heading, level: 1 })).toBeVisible({ timeout: 15000 });
    else await expect(page.locator("main h1").first()).toBeVisible({ timeout: 15000 });
    await page.waitForTimeout(500);
    await expect(page.getByText("Something went wrong")).toHaveCount(0);
    await expect(page.getByText("gen is not defined")).toHaveCount(0);
    expect(errors).toEqual([]);
  });
}
