// End-to-end coverage for the replay's Data tab (api/web/editor.js,
// ADR-0230): the data objects an instance carries, and —
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

// The Data tab's second reading: the same instance as an object diagram
// (ADR-0230, slice 4). UML draws types and instances as
// two diagrams, and that split is why a class diagram was the right notation for
// Atlas at all — it falls on the design-time/run-time line the engine already has.
test.describe("the object diagram", () => {
  test.beforeEach(async ({ page }) => {
    await page.locator('#tab-data [data-dview="diagram"]').click();
    await expect(page.locator(".og-svg")).toBeVisible();
  });

  test("each object is drawn with its class, its members and its business key", async ({ page }) => {
    await expect(page.locator(".og-node")).toHaveCount(4);
    const order = page.locator(".og-node", { has: page.locator("text=order : Order") });
    // UML underlines an object's name, which is the notation's own way of saying
    // "this is an instance, not a type" — the mark that tells the two apart.
    await expect(order.locator(".og-label")).toHaveText("order : Order");
    // The key is marked, because it is what makes this object *this* order and what
    // another object's reference has to match to become a line.
    await expect(order.locator(".og-attr-name.key")).toHaveText("⚿ id");
    // A member the class declares and the value does not carry says so, rather than
    // reading as an empty string.
    await expect(order.locator(".og-absent")).toHaveText("not set");
    await expect(order.locator(".og-state")).toHaveText("[freigegeben]");
  });

  test("the two kinds of line are told apart", async ({ page }) => {
    // Containment: the part lives inside the whole's value, so the line is a fact
    // read off that value and carries the composition diamond.
    const composition = page.locator(".og-line.composition");
    await expect(composition).toHaveCount(1);
    await expect(composition).toHaveAttribute("marker-start", "url(#og-diamond)");
    // A reference is an inference from two values agreeing on a business key, and
    // is drawn without one.
    const association = page.locator(".og-line.association");
    await expect(association).toHaveCount(1);
    await expect(association).not.toHaveAttribute("marker-start", /.*/);
    await expect(page.locator(".og-line-label")).toHaveText(["lines", "customer"]);
  });

  test("a reference this instance cannot satisfy is stated, and points at where to look", async ({ page }) => {
    // Not a fault: it is the edge of what one instance can see, and exactly the
    // boundary a data store removes.
    await expect(page.locator(".og-notes")).toContainText("order.agent");
    await expect(page.locator(".og-notes")).toContainText("C-99");
    await expect(page.locator(".og-notes")).toContainText("another instance or in a data store");
    // And the one place the picture admits its own edge is the one place it says
    // where to look: the data index answers exactly this, across every instance.
    const find = page.locator(".og-notes a");
    await expect(find).toHaveText("Find it →");
    await expect(find).toHaveAttribute("href",
      "#/data/instances?class=Customer&key=C-99&history=true");
  });

  test("the reading is remembered, and switching back shows the list", async ({ page }) => {
    await expect(page.locator('#tab-data [data-dview="diagram"]')).toHaveClass(/active/);
    await page.locator('#tab-data [data-dview="list"]').click();
    await expect(page.locator(".do-table")).toBeVisible();
    await expect(page.locator(".og-svg")).toHaveCount(0);
    // The preference is about how a person reads data, not about this instance.
    const stored = await page.evaluate(() => localStorage.getItem("atlas.replay.datadiagram"));
    expect(stored).toBe("0");
    expect(page.__errors).toEqual([]);
  });
});
