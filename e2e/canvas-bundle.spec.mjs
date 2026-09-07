// One bundle, both canvases (api/web/vendor/canvas/, ADR-0237's named follow-up).
//
// The ArchiMate canvas and the UML class canvas were vendored as a bundle each, with
// a copy of diagram-js inside both. They now share one file. What that has to buy is
// exactly two things, and neither is visible from reading the source: both canvases
// still draw, and a page that opens the second one does not fetch the library again.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  page.__requests = [];
  page.on("request", (r) => page.__requests.push(r.url()));
  await page.goto("/canvas-bundle-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
});

const bundleRequests = (page) => page.__requests.filter((u) => u.includes("/vendor/canvas/atlas-canvas.js"));
const scripts = (page) => page.locator('script[src="vendor/canvas/atlas-canvas.js"]');
const styles = (page) => page.locator('link[href="vendor/canvas/diagram-js.css"]');

test("both canvases draw, and the file that carries them is fetched once", async ({ page }) => {
  await page.evaluate(() => window.__mountArchiMate());
  await expect(page.locator("#archimate .djs-element")).toHaveCount(1);
  expect(bundleRequests(page)).toHaveLength(1);

  // The second view asks in its own right, knowing nothing about the first.
  await page.evaluate(() => window.__mountUml());
  await expect(page.locator("#uml .uml-class")).toHaveCount(1);
  expect(bundleRequests(page)).toHaveLength(1);
  await expect(scripts(page)).toHaveCount(1);
  await expect(styles(page)).toHaveCount(1);
  expect(page.__errors).toEqual([]);
});

// Asked for at the same moment, not one after the other: two views mounting in the
// same tick have no global to find yet, so what stops the second from fetching it
// again is the load in flight, not the result.
test("two views asking at once share the one load", async ({ page }) => {
  await page.evaluate(() => Promise.all([window.__mountArchiMate(), window.__mountUml()]));
  await expect(page.locator("#archimate .djs-element")).toHaveCount(1);
  await expect(page.locator("#uml .uml-class")).toHaveCount(1);
  await expect(scripts(page)).toHaveCount(1);
  expect(page.__errors).toEqual([]);
});

// A load that failed must not be remembered as one still in flight, or every later
// view waits forever on a promise that will never settle — and the retry must not
// leave a second stylesheet link behind for the file that is already linked.
test("a failed load can be tried again", async ({ page }) => {
  await page.route("**/vendor/canvas/atlas-canvas.js", (route) => route.abort());
  const failure = await page.evaluate(() =>
    window.__mountUml().then(() => "", (e) => e.message));
  expect(failure).toContain("Could not load");

  await page.unroute("**/vendor/canvas/atlas-canvas.js");
  await page.evaluate(() => window.__mountUml());
  await expect(page.locator("#uml .uml-class")).toHaveCount(1);
  await expect(styles(page)).toHaveCount(1);
});

// The namespaces are the API the two views address the bundle by, and flattening
// them would put `Viewer` and `ClassCanvas` side by side as if they were two kinds
// of the same thing.
test("each canvas is reached under its own name", async ({ page }) => {
  await page.evaluate(() => window.__mountUml());
  const shape = await page.evaluate(() => ({
    archimate: Object.keys(window.AtlasCanvas.archimate).sort(),
    uml: Object.keys(window.AtlasCanvas.uml).sort(),
  }));
  expect(shape.archimate).toContain("Viewer");
  expect(shape.archimate).toContain("parseOpenExchange");
  expect(shape.uml).toContain("ClassCanvas");
  expect(shape.uml).not.toContain("Viewer");
  expect(page.__errors).toEqual([]);
});
