// The replay Data tab's third reading: the state machine a class declares, with this
// instance's own life drawn on it (ADR-0259 §4).
//
// It adds no fact — the trail was already on disk with attribution, and the machine
// was already in the information model. What these tests hold is that the two are
// read on top of each other rather than side by side: which states this datum has
// been through, where it is now, what moved it along each edge, and — the half worth
// the whole feature — what it did that the model does not account for.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/lifecycle-overlay-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await page.locator("#rp-tabs button[data-tab='data']").click();
  await expect(page.locator("#tab-data .do-table tbody tr").first()).toBeVisible();
  await page.locator('#tab-data [data-dview="lifecycle"]').click();
  // The canvas bundle is fetched on demand, so the first wait is for the drawing
  // rather than for the markup around it.
  await expect(page.locator(".uml-state").first()).toBeVisible();
});

const state = (page, name) => page.locator(`.uml-state[data-name="${name}"]`);

test("the whole declared machine is drawn, not only the parts this instance used", async ({ page }) => {
  // A picture of only what happened answers a different question. The way out this
  // order never took is part of what its class says an order may do.
  await expect(page.locator(".uml-state")).toHaveCount(4);
  await expect(page.locator(".uml-transition")).toHaveCount(3);
  await expect(state(page, "storniert")).toHaveCount(1);
  expect(page.__errors).toEqual([]);
});

test("where the datum has been and where it is now are two different marks", async ({ page }) => {
  for (const been of ["erfasst", "freigegeben", "versendet"]) {
    await expect(state(page, been)).toHaveClass(/visited/);
  }
  // Never reached, so neither — and the state it is in now is the one question
  // somebody opens this to answer, so it is marked apart from merely having been.
  await expect(state(page, "storniert")).not.toHaveClass(/visited/);
  await expect(state(page, "versendet")).toHaveClass(/current/);
  await expect(state(page, "erfasst")).not.toHaveClass(/current/);
  // And said in words above the drawing too, for a reader who is not reading colour.
  await expect(page.locator(".lc-bar .do-state")).toHaveText("versendet");
});

test("an edge the datum travelled names the element that moved it", async ({ page }) => {
  // The one thing no class diagram can say, and the reason to draw this rather than
  // read the trail as a list.
  const taken = page.locator(".uml-transition.taken");
  await expect(taken).toHaveCount(2);
  await expect(page.locator('.uml-transition[data-id="t1"] .uml-transition-by')).toHaveText("Freigeben_1");
  await expect(page.locator('.uml-transition[data-id="t2"] .uml-transition-by')).toHaveText("Versenden_1");
  // The transition nothing took carries no attribution, because there is none.
  await expect(page.locator('.uml-transition[data-id="t3"]')).not.toHaveClass(/taken/);
  await expect(page.locator('.uml-transition[data-id="t3"] .uml-transition-by')).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("a move the model does not allow is drawn as what happened, and said in words", async ({ page }) => {
  // The run-time twin of the data.illegal-transition deploy check: this is what a
  // lifecycle drawn *after* the processes that write it looks like from the outside.
  await page.locator("#lc-pick").selectOption("retoure");
  await expect(page.locator(".lc-of")).toContainText("retoure");

  // Drawn, because what happened is the point of an overlay — and dashed and apart,
  // because it must never read as something the model says.
  const stray = page.locator(".uml-transition.undeclared");
  await expect(stray).toHaveCount(1);
  await expect(stray.locator(".uml-transition-by")).toHaveText("Freigeben_1");
  // The two declared transitions are still there, and neither is marked taken: an
  // undeclared move must not borrow a declared edge to be drawn on. (The stray edge
  // itself is both — it *was* taken, it is just not declared — and the dashed danger
  // stroke is defined after the accent one so it is the one that shows.)
  await expect(page.locator('.uml-transition[data-id="r1"]')).not.toHaveClass(/taken/);
  await expect(page.locator('.uml-transition[data-id="r2"]')).not.toHaveClass(/taken/);
  await expect(page.locator(".uml-transition")).toHaveCount(3);

  await expect(page.locator(".og-notes")).toContainText("declares no transition for");
  expect(page.__errors).toEqual([]);
});

test("a state the class never declared is named rather than quietly left off", async ({ page }) => {
  await page.locator("#lc-pick").selectOption("retoure");
  // It is not on the drawing, because the drawing is the declared machine — so the
  // note is the only place it can be said, and saying it is the whole point: this is
  // what `[aproved]` typed once looks like.
  await expect(page.locator('.uml-state[data-name="abgeschlosen"]')).toHaveCount(0);
  await expect(page.locator(".og-notes")).toContainText("abgeschlosen");
  await expect(page.locator(".og-notes")).toContainText("does not");
  // The bar still says where the datum actually is, which is not a state of the machine.
  await expect(page.locator(".lc-bar .do-state")).toHaveText("abgeschlosen");
});

test("only an object whose class declares a lifecycle is offered", async ({ page }) => {
  // `notiz` carries no declared class at all, so there is nothing to read it against.
  const options = await page.locator("#lc-pick option").evaluateAll((els) => els.map((e) => e.value));
  expect(options).toEqual(["order", "retoure"]);
});

test("the reading is fetched once and survives the tab being re-rendered", async ({ page }) => {
  // This tab re-renders on every selected element and on every live poll. Refetching
  // — or redrawing — each time would throw away the reader's zoom and pan, which are
  // two of the things moving this onto diagram-js was for.
  const before = await page.evaluate(() => window.__lifecycleCalls);
  expect(before).toBe(1);
  await page.locator("#rp-history .ops-hrow").first().click();
  await expect(page.locator(".uml-state").first()).toBeVisible();
  expect(await page.evaluate(() => window.__lifecycleCalls)).toBe(1);
  expect(page.__errors).toEqual([]);
});

test("switching between the tab's readings leaves only the one asked for", async ({ page }) => {
  // Three readings share one body, so a switch has to replace it rather than add to
  // it. (That each switch also *destroys* the diagram-js instance behind the drawing
  // it replaced is not visible from here — the markup is gone either way — so it is
  // held by the code that mirrors destroyObjectCanvas, not by this test.)
  await page.locator('#tab-data [data-dview="diagram"]').click();
  await expect(page.locator(".og-node").first()).toBeVisible();
  await expect(page.locator(".uml-state")).toHaveCount(0);

  await page.locator('#tab-data [data-dview="lifecycle"]').click();
  await expect(page.locator(".uml-state").first()).toBeVisible();
  await expect(page.locator(".og-node")).toHaveCount(0);

  await page.locator('#tab-data [data-dview="list"]').click();
  await expect(page.locator("#tab-data .do-table")).toBeVisible();
  await expect(page.locator(".uml-state")).toHaveCount(0);
  await expect(page.locator(".og-node")).toHaveCount(0);

  // And coming back is drawn from the reading already in hand, not fetched again.
  await page.locator('#tab-data [data-dview="lifecycle"]').click();
  await expect(page.locator(".uml-state").first()).toBeVisible();
  expect(await page.evaluate(() => window.__lifecycleCalls)).toBe(1);
  expect(page.__errors).toEqual([]);
});
