// The DMN view's requirements graph is a diagram, so it can be zoomed
// (api/web/app.js viewDmnViewer, ADR-0062 for the model it draws).
//
// The graph is the one diagram Atlas draws itself rather than handing to
// diagram-js, and it used to be a fixed picture: the frame scrolled, nothing more,
// so the only way closer was the browser's page zoom scaling the whole console.
// This drives the real shell to the real route and asserts the shared control from
// api/web/diagram-zoom.js is actually wired there — the module's own tests cover
// how it behaves, this covers that the view has it.
import { test, expect } from "@playwright/test";

// GRAPH is the shape /dmnrefs/{id}/graph returns: decisions and input data with
// authored DMNDI bounds, plus the requirements between them.
const GRAPH = {
  valid: true, resolved: true, modelName: "Einstufung",
  nodes: [
    { id: "stufe", name: "Stufe", kind: "decision", x: 40, y: 20, width: 160, height: 60 },
    { id: "alter", name: "Alter", kind: "input", x: 40, y: 160, width: 160, height: 50 },
  ],
  edges: [{ from: "alter", to: "stufe" }],
};

async function openDmnView(page) {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (/\/dmnrefs\/[^/]+\/graph$/.test(path)) return route.fulfill({ json: GRAPH });
    if (path.endsWith("/dmnrefs")) {
      return route.fulfill({ json: [{ id: "ref1", name: "Einstufung", modelRef: "dmn/alter", projectId: "" }] });
    }
    return route.fulfill({ json: [] });
  });
  await page.goto("/index.html#/modeler/dmn/ref1");
  await page.waitForFunction(() => !!document.getElementById("dmn-canvas"), null, { timeout: 20000 });
}

test("the decision requirements graph carries the shared zoom control", async ({ page }) => {
  await openDmnView(page);

  const svg = page.locator("#dmn-canvas svg");
  await expect(svg).toHaveCount(1);
  await expect(page.locator(".dzoom button[aria-label='Zoom in']")).toBeVisible();
  await expect(page.locator(".dzoom button[aria-label='Zoom out']")).toBeVisible();
  await expect(page.locator(".dzoom button[aria-label='Fit the diagram']")).toBeVisible();

  const before = (await svg.boundingBox()).width;
  await page.locator(".dzoom button[aria-label='Zoom in']").click();
  expect((await svg.boundingBox()).width).toBeGreaterThan(before);

  expect(page.__errors).toEqual([]);
});
