// Planned against built — the difference reading
// (ADR-0310).
//
// The list will be read as work, so what these tests hold is mostly about restraint:
// that the two directions are never blended, that "planned" is not presented as a
// defect, and that what the comparison never looked at is said where it lists — because
// a reader who does not know what was excluded cannot tell a short list from a clean
// bill.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/model-difference-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
});

const mounted = async (page, which = "__mount") => {
  await page.evaluate((fn) => window[fn](), which);
  await expect(page.locator(".md-root")).toBeVisible();
};

test("the two directions are two columns, never one list", async ({ page }) => {
  await mounted(page);
  const planned = page.locator(".md-col.planned");
  const built = page.locator(".md-col:not(.planned)");
  await expect(planned).toHaveCount(1);
  await expect(built).toHaveCount(1);

  // A reader acts on them differently, so they are counted apart.
  await expect(planned.locator(".md-count")).toHaveText("4");
  await expect(built.locator(".md-count")).toHaveText("1");
  // And nothing from one side appears on the other.
  await expect(built).not.toContainText("cancelled");
  await expect(planned).not.toContainText("rabatt");
  expect(page.__errors).toEqual([]);
});

test("the backlog is not presented as a defect", async ({ page }) => {
  await mounted(page);
  const planned = page.locator(".md-col.planned");
  // The whole reframing ADR-0301 turns on: modelling ahead of implementation is normal
  // practice, and a screen that called it drift would make people stop doing it.
  await expect(planned).toContainText("backlog");
  await expect(planned).toContainText("not yet implemented");
  await expect(planned).toContainText("not");
  await expect(planned.locator(".md-meaning")).toContainText("defect");
});

test("findings are grouped by class, because a class is one subject", async ({ page }) => {
  await mounted(page);
  const groups = page.locator(".md-col.planned .md-class");
  await expect(groups).toHaveCount(2); // Order and Invoice
  const order = groups.filter({ hasText: "Order" }).first();
  await expect(order.locator(".md-rows li")).toHaveCount(3);
  // A transition names its ends, because that is what it says — an id is documentation.
  await expect(order).toContainText("approved → received");
});

test("what the comparison never looked at is said where it lists", async ({ page }) => {
  await mounted(page);
  const excluded = page.locator(".md-excluded");
  await expect(excluded).toBeVisible();
  await expect(excluded).toContainText("business key");
  // And it says *why* that matters, rather than only listing them.
  await expect(excluded).toContainText("not a clean bill");
  expect(page.__errors).toEqual([]);
});

test("a model that matches its processes says so on both sides", async ({ page }) => {
  await mounted(page, "__mountAgreed");
  await expect(page.locator(".md-col.planned")).toContainText("the processes build");
  await expect(page.locator(".md-col:not(.planned)")).toContainText("the model describes");
  // The exclusions stay, and that is the point: agreement on what was compared is not
  // agreement on everything.
  await expect(page.locator(".md-excluded")).toBeVisible();
});

test("an application that plans nothing is not handed a backlog", async ({ page }) => {
  await mounted(page, "__mountUnmodelled");
  await expect(page.locator("#md-body")).toContainText("Nothing is planned yet");
  // No columns at all: a wall of findings that are only the absence of a document
  // somebody has not started is the worst possible first impression.
  await expect(page.locator(".md-cols")).toHaveCount(0);
  await expect(page.locator(".md-excluded")).toHaveCount(0);
  // And it offers the way that actually helps: read what the processes already carry.
  await expect(page.locator("#md-body a[href*='/data/derived/']")).toHaveCount(1);
  expect(page.__errors).toEqual([]);
});
