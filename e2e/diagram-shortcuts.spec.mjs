// Favourite and recent diagrams in the Console (ADR-draft-diagram-shortcuts).
//
// The server decides what a mark resolves to; what the browser owes is the rest:
// the star is offered only to somebody signed in, pressing it sends one diagram and
// adopts the whole list the server answers, the menu opens on what is known and does
// not close when a star inside it is pressed, and opening a canvas reports a visit by
// the id the route names — by deployment key for a deployed version.
import { test, expect } from "@playwright/test";

const ADA = { username: "ada", roles: ["admin"] };

// stubAPI answers as a signed-in administrator with one application holding one
// information model, and keeps the shortcut record in the page's closure so a mark
// round-trips. Catch-all first: Playwright matches the most recently registered route.
function stubAPI(page, { user = ADA } = {}) {
  const state = { favourites: [], recent: [], posts: [], writes: [] };
  const im = { id: "a1b2", applicationId: "app1", name: "Kundenmodell", classes: 2, associations: 1, revision: 1, updatedAt: 1 };
  const resolved = (kind, id) => kind === "infomodel" && id === im.id
    ? { kind, id, available: true, name: im.name, href: `#/data/m/${id}` }
    : { kind, id, available: false };
  page.route("**/api/v1/**", (route) => route.fulfill({ json: [] }));
  page.route("**/api/v1/auth/me", (route) => route.fulfill({ json: { authEnabled: true, user } }));
  page.route("**/api/v1/projects", (route) => route.fulfill({ json: [{ id: "app1", name: "Vertrieb", myRole: "owner" }] }));
  page.route("**/api/v1/infomodel/models", (route) => route.fulfill({ json: [im] }));
  page.route("**/api/v1/diagram-shortcuts", (route) => route.fulfill({ json: { favourites: state.favourites, recent: state.recent } }));
  page.route("**/api/v1/diagram-shortcuts/favourites/**", (route) => {
    const req = route.request();
    const [kind, id] = new URL(req.url()).pathname.split("/").slice(-2).map(decodeURIComponent);
    state.writes.push(`${req.method()} ${kind}/${id}`);
    state.favourites = state.favourites.filter((f) => !(f.kind === kind && f.id === id));
    if (req.method() === "PUT") state.favourites.push(resolved(kind, id));
    return route.fulfill({ json: { favourites: state.favourites, recent: state.recent } });
  });
  page.route("**/api/v1/diagram-shortcuts/recent", (route) => {
    state.posts.push(route.request().postDataJSON());
    return route.fulfill({ status: 204, body: "" });
  });
  return state;
}

test("a star in the Data list marks the model, and the menu lists it without closing on a star", async ({ page }) => {
  const state = stubAPI(page);
  await page.goto("/index.html#/data");

  const star = page.locator('.dg-star[data-dg-kind="infomodel"][data-dg-id="a1b2"]');
  await expect(star).toHaveAttribute("aria-pressed", "false");
  await star.click();
  await expect(star).toHaveAttribute("aria-pressed", "true");
  expect(state.writes).toEqual(["PUT infomodel/a1b2"]);

  await page.click("#shortcuts-btn");
  const menu = page.locator("#shortcuts-menu");
  await expect(menu).toBeVisible();
  await expect(menu.locator('a[href="#/data/m/a1b2"]')).toContainText("Kundenmodell");

  // Clearing from inside the menu keeps it open and clears the row's star too.
  await menu.locator(".dg-star").first().click();
  await expect(menu).toBeVisible();
  await expect(menu.locator('a[href="#/data/m/a1b2"]')).toHaveCount(0);
  await expect(star).toHaveAttribute("aria-pressed", "false");
  expect(state.writes).toEqual(["PUT infomodel/a1b2", "DELETE infomodel/a1b2"]);
});

test("opening a canvas reports a visit by the id its route names", async ({ page }) => {
  const state = stubAPI(page);
  await page.goto("/index.html#/data/m/a1b2");
  await expect.poll(() => state.posts).toContainEqual({ kind: "infomodel", id: "a1b2" });

  await page.goto("/index.html#/modeler/d/42");
  await expect.poll(() => state.posts).toContainEqual({ kind: "process", deploymentKey: 42 });

  // A route that opens nothing stored is not a visit.
  const before = state.posts.length;
  await page.goto("/index.html#/modeler/dmn/new");
  await page.waitForTimeout(300);
  expect(state.posts.length).toBe(before);
});

test("with nobody to own a mark there is no star and no menu", async ({ page }) => {
  page.route("**/api/v1/**", (route) => route.fulfill({ json: [] }));
  page.route("**/api/v1/auth/me", (route) => route.fulfill({ json: { authEnabled: false, user: null } }));
  page.route("**/api/v1/infomodel/models", (route) =>
    route.fulfill({ json: [{ id: "a1b2", applicationId: "app1", name: "Kundenmodell", classes: 0, associations: 0, revision: 1 }] }));
  await page.goto("/index.html#/data");
  await expect(page.locator("#view")).toContainText("Kundenmodell");
  await expect(page.locator(".dg-star")).toHaveCount(0);
  await expect(page.locator("#shortcuts-dropdown")).toBeHidden();
});

test("every diagram row of an application's list carries its star", async ({ page }) => {
  stubAPI(page);
  page.route("**/api/v1/drafts", (route) => route.fulfill({ json: [{ processId: "order", name: "Bestellung", savedAt: 1 }] }));
  page.route("**/api/v1/dmnrefs", (route) => route.fulfill({ json: [{ id: "discount", name: "Rabatt", modelRef: "discount", createdAt: 1 }] }));
  page.route("**/api/v1/dmn-drafts", (route) => route.fulfill({ json: [{ id: "dd-1", name: "Neu", savedAt: 1 }] }));
  await page.goto("/index.html#/modeler/p/ungrouped");

  for (const [kind, id] of [["process", "order"], ["decision", "discount"], ["decision", "dd-1"]]) {
    await expect(page.locator(`.dg-star[data-dg-kind="${kind}"][data-dg-id="${id}"]`)).toHaveCount(1);
  }
  // Each row still links to the canvas it opens.
  await expect(page.locator('a[href="#/modeler/dmn/discount"]').first()).toBeVisible();
  await expect(page.locator('a[href="#/modeler/dmn/d/dd-1"]').first()).toBeVisible();
});
