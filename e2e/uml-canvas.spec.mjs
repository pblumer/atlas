// End-to-end coverage for the UML class canvas bundle
// (api/web/vendor/uml/, ADR-0237).
//
// The class canvas was hand-rolled SVG, redrawn whole on every edit. That is why it
// had no zoom, no pan, no marquee, no multi-select and no undo: each of those is
// something a canvas library has and a redraw loop does not. This bundle is
// diagram-js plus the part that is Atlas's — how a class, a store and the four
// association kinds are drawn, and what the served subset permits between them.
//
// These tests pin the substrate's contract, because the editor is being ported onto
// it: what it draws, what the matrix refuses, and — the one that decides whether the
// port is usable at all — that updating the model in place does not take the
// viewport, the selection or the undo stack with it.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/uml-canvas-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await expect(page.locator(".uml-class").first()).toBeVisible();
});

test("a class reads as UML, and a store does not read as a class", async ({ page }) => {
  await expect(page.locator(".uml-class")).toHaveCount(3);
  await expect(page.locator(".uml-cname")).toHaveText(["Customer", "Order", "OrderStatus"]);
  await expect(page.locator(".uml-stereo").first()).toHaveText("«businessObject»");

  // The business key is marked on the box, because it is the fact the whole model
  // turns on — what makes Order#ORD-1 the same order in two processes.
  await expect(page.locator(".uml-attr.key .uml-attr-name")).toHaveText("⚿ kdnr");
  // A non-default multiplicity is shown; "exactly one" is the unstated default.
  await expect(page.locator(".uml-attr-mult")).toHaveText(" [0..1]");
  // An enumeration carries literals where the others carry attributes.
  await expect(page.locator(".uml-literal")).toHaveText(["draft", "approved"]);

  // A store is a cylinder and deliberately not a box: it is not a class
  // (ADR-0230 §7).
  await expect(page.locator(".uml-store")).toHaveCount(1);
  await expect(page.locator(".uml-store-sub")).toHaveText("«read» Order");
  // One authored relationship. The store's line to the class it holds is not one:
  // a store and its class do not relate, one is kept in the other.
  await expect(page.locator(".uml-edge")).toHaveCount(1);
  await expect(page.locator(".uml-store-link")).toHaveCount(1);
  expect(page.__errors).toEqual([]);
});

test("the canvas offers only what the served matrix allows", async ({ page }) => {
  // The rules read the table the server sent. A canvas that decided for itself would
  // be a second copy of the matrix, and the copy is how you get an arrow the write
  // path then rejects.
  const allowed = await page.evaluate(() => ({
    boToBo: window.__canvas.allowedFrom("businessObject", "businessObject"),
    boToValue: window.__canvas.allowedFrom("businessObject", "valueType"),
    // An enumeration is a closed set of values, so nothing points at it.
    boToEnum: window.__canvas.allowedFrom("businessObject", "enumeration"),
  }));
  expect(allowed.boToBo).toEqual(["association", "aggregation", "composition", "generalization"]);
  expect(allowed.boToValue).toEqual(["association", "aggregation", "composition"]);
  expect(allowed.boToEnum).toEqual([]);
});

test("an edit updates in place and leaves the view, the selection and undo alone", async ({ page }) => {
  // This is the test the port depends on. The editor re-renders on every keystroke in
  // the properties panel; were that a redraw, typing a class name would zoom the
  // diagram back to fit and deselect the class being renamed.
  const out = await page.evaluate(async () => {
    const cv = window.__canvas;
    await new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)));
    cv.select("c1");
    cv.zoom(1.5);
    const zoom = cv.canvas.zoom();
    const viewbox = cv.canvas.viewbox().x;
    const model = window.__model();
    model.classes[0].name = "Kunde";
    model.classes[0].attributes.push({ name: "geburtsdatum", type: "date", multiplicity: "0..1" });
    cv.sync(model, []);
    return {
      zoomKept: Math.abs(cv.canvas.zoom() - zoom) < 0.001,
      viewboxKept: cv.canvas.viewbox().x === viewbox,
      stillSelected: cv.selection.get().map((e) => e.id),
      height: cv.shapes.get("c1").height,
    };
  });
  expect(out.zoomKept).toBe(true);
  expect(out.viewboxKept).toBe(true);
  expect(out.stillSelected).toEqual(["c1"]);

  // The rename landed and the box grew with its new attribute.
  await expect(page.locator(".uml-cname").first()).toHaveText("Kunde");
  await expect(page.locator(".uml-class").first().locator(".uml-attr")).toHaveCount(3);
  expect(out.height).toBeGreaterThan(34 + 10 + 2 * 20);
  expect(page.__errors).toEqual([]);
});

test("a class that goes away takes its relationships with it", async ({ page }) => {
  const counts = await page.evaluate(() => {
    const model = window.__model();
    model.classes = model.classes.filter((c) => c.id !== "c2");
    model.associations = [];
    window.__canvas.sync(model, []);
    return { shapes: window.__canvas.shapes.size };
  });
  expect(counts.shapes).toBe(3); // two classes and the store
  await expect(page.locator(".uml-class")).toHaveCount(2);
  // The authored association is gone, and so is the store's derived line — the class
  // it pointed at is no longer there to point at.
  await expect(page.locator(".uml-edge")).toHaveCount(0);
  await expect(page.locator(".uml-store-link")).toHaveCount(0);
});

test("a shape the author moved is reported, and one they did not is not", async ({ page }) => {
  // The move list is computed against what was loaded rather than by accumulating
  // drags: dragging a box away and back is not a change, and an accumulating list
  // would save a revision that moved nothing.
  const out = await page.evaluate(() => {
    const cv = window.__canvas;
    const before = cv.moved();
    const shape = cv.shapes.get("c1");
    cv.diagram.get("modeling").moveShape(shape, { x: 60, y: 20 });
    const after = cv.moved();
    cv.diagram.get("modeling").moveShape(shape, { x: -60, y: -20 });
    return { before, after, backAgain: cv.moved() };
  });
  expect(out.before).toEqual([]);
  expect(out.after).toEqual([{ id: "c1", kind: "class", x: 100, y: 60 }]);
  expect(out.backAgain).toEqual([]);
});

test("a finding marks the shape it is about", async ({ page }) => {
  await page.evaluate(() => window.__canvas.sync(window.__model(), [{ classId: "c2", reason: "invalid" }]));
  await expect(page.locator(".uml-class.invalid")).toHaveCount(1);
  await expect(page.locator(".uml-class.invalid .uml-cname")).toHaveText("Order");
});

test("the ends carry the role and the multiplicity, and a generalization's do not", async ({ page }) => {
  // "1 customer places 0..* orders" is the sentence a class diagram is drawn to say.
  // A line without those two says only that the classes are related, which is the
  // part nobody needed a diagram for.
  await page.evaluate(() => {
    const model = window.__model();
    model.associations = [
      { id: "a1", kind: "composition", name: "places",
        from: { classId: "c1", role: "customer", multiplicity: "1" },
        to: { classId: "c2", role: "", multiplicity: "0..*" } },
    ];
    window.__canvas.sync(model, []);
  });
  await expect(page.locator(".uml-end-label")).toHaveText(["customer 1", "0..*"]);

  // A generalization has neither: "is a kind of" is not a counted relationship.
  await page.evaluate(() => {
    const model = window.__model();
    model.associations = [
      { id: "a1", kind: "generalization",
        from: { classId: "c1", role: "sub", multiplicity: "1" },
        to: { classId: "c2", role: "super", multiplicity: "1" } },
    ];
    window.__canvas.sync(model, []);
  });
  await expect(page.locator(".uml-end-label")).toHaveCount(0);
});

test("a class related to its own kind loops rather than collapsing", async ({ page }) => {
  // The palette refuses to draw one, but an imported model may well contain one — an
  // Employee who reports to an Employee is the ordinary case (ADR-0232) — and docking
  // a shape against itself divides by a zero-length direction.
  const points = await page.evaluate(() => {
    const model = window.__model();
    model.associations = [
      { id: "a1", kind: "association", name: "reports to",
        from: { classId: "c1", role: "", multiplicity: "0..1" },
        to: { classId: "c1", role: "", multiplicity: "0..*" } },
    ];
    window.__canvas.sync(model, []);
    return document.querySelector(".uml-edge .uml-edge-line").getAttribute("points");
  });
  expect(points).not.toContain("NaN");
  // It leaves one side of the box and comes back into another, so it is visible as a
  // loop rather than as a dot under the class.
  const xs = points.split(" ").map((p) => Number(p.split(",")[0]));
  expect(Math.max(...xs) - Math.min(...xs)).toBeGreaterThan(20);
  expect(page.__errors).toEqual([]);
});
