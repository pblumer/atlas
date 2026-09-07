// A model that no application owns (ADR-draft-shared-information-models).
//
// The information model was scoped to one process application, which solves inside an
// application exactly the problem BPMN has across processes — and reproduces it one
// level up: two applications that both handle a customer end up with two unrelated
// Customer classes and no statement anywhere that they are the same customer. A model
// with no application is the answer: every application resolves against it, so the
// attributes are typed once and the business key survives the boundary.
//
// These drive the real Console, because the part that can go wrong is the part the
// server never sees: the library is the *absence* of an application, and a UI that
// sends a value standing for absence is a UI that puts a sentinel in the store.
import { test, expect } from "@playwright/test";

const APP = { id: "app-1", name: "Sales", myRole: "owner", protected: false };

const model = (over) => ({
  id: "m1", name: "Sales data", applicationId: "app-1", revision: 3,
  classes: 2, associations: 1, stores: 0, updatedAt: 1_700_000_000, ...over,
});

// A library model is one whose applicationId the server omits entirely — which is
// what the field means, and what the UI has to read.
const LIBRARY = { id: "m2", name: "Shared vocabulary", revision: 1, classes: 1, associations: 0, stores: 0, updatedAt: 1_700_000_100 };

function stub(page, { models, user = null, authEnabled = false, posted = [] } = {}) {
  page.route("**/api/v1/**", (route) => route.fulfill({ json: [] }));
  page.route("**/api/v1/auth/me", (route) => route.fulfill({ json: { authEnabled, user } }));
  page.route("**/api/v1/applications**", (route) => route.fulfill({ json: [APP] }));
  page.route("**/api/v1/infomodel/models**", (route) => {
    if (route.request().method() === "POST") {
      posted.push(route.request().postDataJSON());
      return route.fulfill({ json: { id: "new-1", name: "x" } });
    }
    return route.fulfill({ json: models });
  });
  return posted;
}

test("a model with no application reads as the library, not as a missing one", async ({ page }) => {
  stub(page, { models: [model(), LIBRARY] });
  await page.goto("/index.html#/data");
  await expect(page.locator("table[data-dt-key='info-models'] tbody tr")).toHaveCount(2);

  const library = page.locator("tr", { hasText: "Shared vocabulary" });
  await expect(library).toContainText("Library");
  // The failure this guards against: an absent applicationId read as an application
  // that could not be found.
  await expect(library).not.toContainText("Missing application");
  await expect(page.locator("tr", { hasText: "Sales data" })).toContainText("Sales");
});

test("creating into the library sends no application at all", async ({ page }) => {
  const posted = stub(page, { models: [] });
  await page.goto("/index.html#/data");
  await page.locator('[data-act="new-im"]').click();

  const select = page.locator("#pick-opt");
  await expect(select).toBeVisible();
  // The library is offered first, because it is the answer to "shared by more than
  // one application" and that is the question this screen exists for.
  await expect(select.locator("option").first()).toHaveText(/Library/);
  await expect(page.locator(".modal .muted")).toContainText("no application");

  await select.selectOption({ index: 0 });
  await page.locator("[data-ok]").click();
  await expect.poll(() => posted.length).toBe(1);
  expect(posted[0].name).toBeTruthy();
  expect("applicationId" in posted[0]).toBe(false);
});

test("creating into an application still names it", async ({ page }) => {
  const posted = stub(page, { models: [] });
  await page.goto("/index.html#/data");
  await page.locator('[data-act="new-im"]').click();
  await page.locator("#pick-opt").selectOption("app-1");
  await page.locator("[data-ok]").click();
  await expect.poll(() => posted.length).toBe(1);
  expect(posted[0].applicationId).toBe("app-1");
});

// Deleting a library model reaches every application on the server, so it asks for
// more than editing one does. Editing it does not: a shared vocabulary only one
// person can maintain is not one a team can rely on.
test("a modeler may edit a library model but not delete it", async ({ page }) => {
  stub(page, { models: [LIBRARY], authEnabled: true, user: { username: "sven", roles: ["modeler"] } });
  await page.goto("/index.html#/data");
  const row = page.locator("tr", { hasText: "Shared vocabulary" });
  await row.locator(".icon-btn").click();
  await expect(page.locator('[data-act="rename-im"]')).toBeVisible();
  await expect(page.locator('[data-act="delete-im"]')).toHaveCount(0);
});

test("an administrator may delete one", async ({ page }) => {
  stub(page, { models: [LIBRARY], authEnabled: true, user: { username: "root", roles: ["admin"] } });
  await page.goto("/index.html#/data");
  const row = page.locator("tr", { hasText: "Shared vocabulary" });
  await row.locator(".icon-btn").click();
  await expect(page.locator('[data-act="delete-im"]')).toBeVisible();
});
