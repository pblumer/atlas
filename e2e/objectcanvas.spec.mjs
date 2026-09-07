// The object canvas's own contract (ADR-0237), below the replay's Data tab.
//
// The Data tab builds a fresh canvas for every graph, so it never exercises the one
// thing a public render() has to survive: being called twice. diagram-js's element
// registry is per diagram and not per root, so a shape or a connection left in it
// makes the next render fail with "element already exists" — and the failure is a
// thrown error in a requestAnimationFrame, which is exactly the kind that reaches a
// user as a blank panel and reaches a test as nothing at all.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/objectcanvas-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
});

test("drawing a second graph into a canvas that already holds one replaces it", async ({ page }) => {
  await page.evaluate(() => window.__draw("a"));
  await expect(page.locator(".og-node")).toHaveCount(2);
  await expect(page.locator(".og-line.composition")).toHaveCount(1);

  // The same canvas, a different graph: the first one's shapes and its line both have
  // to be gone from the registry, not merely off the root.
  await page.evaluate(() => window.__draw("b"));
  await expect(page.locator(".og-node")).toHaveCount(1);
  await expect(page.locator(".og-line")).toHaveCount(0);
  await expect(page.locator(".og-label")).toHaveText("customer : Customer");
  expect(page.__errors).toEqual([]);
});

test("redrawing the same graph is not an error either", async ({ page }) => {
  await page.evaluate(() => window.__draw("a"));
  await expect(page.locator(".og-node")).toHaveCount(2);
  await page.evaluate(() => window.__draw("a"));
  await expect(page.locator(".og-node")).toHaveCount(2);
  expect(page.__errors).toEqual([]);
});
