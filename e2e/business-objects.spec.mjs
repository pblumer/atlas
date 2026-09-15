// The class catalogue, and where one class is used
// (ADR-0338).
//
// Two claims are worth holding in a browser rather than in Go. The first is that the
// list is an ordinary list — the shared sort/filter table (ADR-0286),
// not a bespoke one — because that is the whole reason it reads like every other list
// in the console. The second is that a class no process touches is not shown as unused:
// an enumeration's uses are in the model, and a page that only counted processes would
// quietly recommend deleting the most shared element of the vocabulary.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/business-objects-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
});

const mounted = async (page, which = "__mount") => {
  await page.evaluate((fn) => window[fn](), which);
  await expect(page.locator(".bo-root")).toBeVisible();
};

test("the list is the shared table, so it sorts and filters like every other list", async ({ page }) => {
  await mounted(page);
  const table = page.locator("table[data-dt-key='info-classes']");
  await expect(table).toBeVisible();
  // enhanceTable marks what it has taken over, and gives the list its own search.
  await expect(table).toHaveAttribute("data-dt-enhanced", "1");
  await expect(table.locator("tr.dt-filter-row input.dt-filter").first()).toBeVisible();
  await expect(table.locator("tbody tr")).toHaveCount(3);

  // Filtering the name column leaves the rows that match and nothing else.
  await table.locator("tr.dt-filter-row input.dt-filter").first().fill("order");
  await expect(table.locator("tbody tr:visible")).toHaveCount(2);
  await table.locator("tr.dt-filter-row input.dt-filter").first().fill("");
  await expect(table.locator("tbody tr:visible")).toHaveCount(3);
  expect(page.__errors).toEqual([]);
});

test("the list spans applications, which is what a per-model view cannot do", async ({ page }) => {
  await mounted(page);
  const rows = page.locator("table[data-dt-key='info-classes'] tbody tr");
  await expect(rows.filter({ hasText: "Sales" })).toHaveCount(2);
  await expect(rows.filter({ hasText: "Fulfilment" })).toHaveCount(1);
  // And every kind of class is in it: an enumeration nobody declares is still part of
  // the vocabulary somebody maintains.
  for (const kind of ["Business object", "Value type", "Enumeration"]) {
    await expect(rows.filter({ hasText: kind })).toHaveCount(1);
  }
});

test("a class used by nothing deployed says so, and one used only by the model says that instead", async ({ page }) => {
  await mounted(page);
  const rows = page.locator("table[data-dt-key='info-classes'] tbody tr");
  await expect(rows.filter({ hasText: "OrderStatus" })).toContainText("in the model");
  await expect(rows.filter({ hasText: "OrderStatus" })).not.toContainText("used by nothing");
  await expect(rows.filter({ hasText: "Address" })).toContainText("used by nothing deployed");
  // The scope of that claim is on the page, not left to be assumed: a draft is not read.
  await expect(page.locator(".bo-scope")).toContainText("draft");
});

test("nothing modelled yet is an invitation, not an empty table", async ({ page }) => {
  await mounted(page, "__mountEmpty");
  await expect(page.locator("#bo-body")).toContainText("Nothing is modelled yet");
  await expect(page.locator("table")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("one class says where it is used and how", async ({ page }) => {
  await mounted(page, "__mountOrder");
  const uses = page.locator("table[data-dt-key='info-class-uses'] tbody tr");
  await expect(uses).toHaveCount(5);

  // Each kind of use, located precisely enough to act on.
  await expect(uses.filter({ hasText: "declares" })).toContainText("received");
  const read = uses.filter({ hasText: "reads" });
  await expect(read).toContainText("approve");
  await expect(read).toContainText("orderUnderReview");
  const memberWrite = uses.filter({ hasText: "capture" });
  await expect(memberWrite).toContainText("writes");
  await expect(memberWrite).toContainText("total");
  await expect(uses.filter({ hasText: "Ref_archive" })).toContainText("Order archive");
  // A write that carries no value only moves the state, and says that rather than
  // claiming it wrote the whole object.
  await expect(uses.filter({ hasText: "approved" })).toContainText("state only");

  // And the process is a link to it, not a name to go and look up.
  await expect(uses.first().locator("a[href='#/operations/p/7']")).toHaveCount(1);
  expect(page.__errors).toEqual([]);
});

test("what processes touch is counted against what the class declares", async ({ page }) => {
  await mounted(page, "__mountOrder");
  const touched = page.locator(".bo-touched");
  // The gap is the point: one member written of three, two states reached of three.
  await expect(touched).toContainText("of 3");
  await expect(touched).toContainText("total");
  await expect(touched).toContainText("approved");
  await expect(touched).toContainText("Order archive");
  // The summary states the shape before the rows do.
  await expect(page.locator(".bo-summary .stat").first()).toContainText("1");
});

test("the model's own uses are listed beside the processes'", async ({ page }) => {
  await mounted(page, "__mountOrder");
  const model = page.locator("table[data-dt-key='info-class-model-uses'] tbody tr");
  await expect(model).toHaveCount(2);
  await expect(model.filter({ hasText: "Customer" })).toContainText("association");
  // A class named in a use is a link to its own page: "where used" is a graph to walk.
  await expect(model.filter({ hasText: "Customer" }).locator("a[href*='/data/objects/']")).toHaveCount(1);
  await expect(model.filter({ hasText: "Order archive" })).toContainText("kept in");
});

test("an enumeration is not unused: its uses are in the model", async ({ page }) => {
  await mounted(page, "__mountStatus");
  // No process table at all — and the sentence that says why that is normal.
  await expect(page.locator("table[data-dt-key='info-class-uses']")).toHaveCount(0);
  await expect(page.locator(".bo-sec").filter({ hasText: "Used in processes" }))
    .toContainText("No deployed process declares a data object of this class");
  const model = page.locator("table[data-dt-key='info-class-model-uses'] tbody tr");
  await expect(model).toHaveCount(2);
  await expect(model.filter({ hasText: "status" })).toContainText("typed as");
  await expect(model.filter({ hasText: "states of" })).toHaveCount(1);
  // Its members are its literals, which is what an enumeration has instead of attributes.
  await expect(page.locator(".bo-sec").filter({ hasText: "What it is" })).toContainText("Literal");
  expect(page.__errors).toEqual([]);
});

test("the lifecycle is readable without opening the diagram", async ({ page }) => {
  await mounted(page, "__mountOrder");
  const life = page.locator(".bo-life");
  await expect(life).toContainText("states from «OrderStatus»");
  await expect(life.locator(".bo-state.initial")).toHaveText("received");
  await expect(life.locator(".bo-state.final")).toHaveText("shipped");
  await expect(life.locator(".bo-moves li")).toHaveCount(2);
});
