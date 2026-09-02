// End-to-end coverage for the replay's Data tab (api/web/editor.js,
// ADR-draft-process-information-model): the data objects an instance carries, and —
// the part no variable view can answer — which element on the diagram put each value
// there.
//
// The case behind it: a data object is the one thing on a BPMN diagram that has a life
// rather than only a current value. Atlas has recorded every transition since data
// objects became first class; showing only the latest would throw away the reason it
// records them, and showing "who wrote it" as a snapshot diff would credit both
// branches of a fork with both writes.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/data-objects-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await page.locator("#rp-tabs button[data-tab='data']").click();
  await expect(page.locator("#tab-data .do-table tbody tr").first()).toBeVisible();
});

const row = (page, name) =>
  page.locator("#tab-data .do-table tbody tr.do-row", { has: page.locator(".do-name", { hasText: name }) });

test("every data object the instance carries is listed with what it is and where it stands", async ({ page }) => {
  await expect(page.locator("#tab-data .do-row")).toHaveCount(3);
  await expect(page.locator("#tab-data .vp-count")).toHaveText("3 objects");

  const order = row(page, "order");
  // The declared class is what BPMN's itemSubjectRef points at — the type slot that,
  // until this record, resolved to nothing.
  await expect(order.locator(".do-class")).toHaveText("Order");
  await expect(order.locator(".do-state")).toHaveText("freigegeben");
  // A structure is summarized, not dumped — the same reading the Variables tab uses.
  await expect(order.locator(".c-val")).toHaveText("{2 fields}");
  await expect(order.locator(".do-by")).toHaveText("Freigeben");

  // An object nobody has written says so, and still shows what it was declared to be:
  // a declared collection is a fact about the model, not about the value.
  const positionen = row(page, "positionen");
  await expect(positionen.locator(".c-val")).toHaveText("unset");
  await expect(positionen.locator(".do-coll")).toHaveText("list");
  await expect(positionen.locator(".do-by")).toHaveText("seeded");
});

test("a row opens into the object's state trail, naming the element behind each write", async ({ page }) => {
  const order = row(page, "order");
  const toggle = order.locator(".do-toggle");
  await expect(toggle).toHaveAttribute("aria-expanded", "false");
  await toggle.click();
  await expect(toggle).toHaveAttribute("aria-expanded", "true");

  const trail = page.locator("#tab-data .do-trail-table tbody tr");
  await expect(trail).toHaveCount(3);
  // The sentence the trail tells: created empty, given an id by Erfassen, released by
  // Freigeben. Each write names the element that made *that* write, not the last one.
  await expect(trail.nth(0).locator(".do-t-state")).toHaveText("erfasst");
  await expect(trail.nth(0).locator(".do-t-by")).toHaveText("seeded");
  await expect(trail.nth(1).locator(".do-t-by")).toHaveText("Erfassen");
  await expect(trail.nth(1).locator(".do-t-val")).toHaveText("{1 field}");
  await expect(trail.nth(2).locator(".do-t-by")).toHaveText("Freigeben");
  await expect(trail.nth(2).locator(".do-t-val")).toHaveText("{2 fields}");

  // Closing it again leaves the list as it was.
  await toggle.click();
  await expect(page.locator("#tab-data .do-trail-table")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("a write the log cannot attribute says unknown rather than borrowing a name", async ({ page }) => {
  const alt = row(page, "altbestand");
  // The row summarizes its most recent write, which names nobody — and it must not
  // inherit "Freigeben" from the object listed next to it.
  await expect(alt.locator(".do-by")).toHaveText("unknown");
  await expect(alt.locator(".do-class")).toHaveText("untyped");

  await alt.locator(".do-toggle").click();
  const trail = page.locator("#tab-data .do-trail-table tbody tr");
  await expect(trail).toHaveCount(2);
  // The first entry is the seeding: nobody wrote it, and that is the true answer.
  await expect(trail.nth(0).locator(".do-t-by")).toHaveText("seeded");
  // A later entry that names nobody is a gap, not a seed, and reads differently.
  await expect(trail.nth(1).locator(".do-t-by")).toHaveText("unknown");
  expect(page.__errors).toEqual([]);
});
