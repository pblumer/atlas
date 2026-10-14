// The state canvas: a class's lifecycle, drawn (ADR-0259).
//
// The third notation on the shared diagram-js bundle, and the one that answers BPMN's
// second opaque slot — a `<dataState name="approved">` resolves against a state
// declared here. What these tests hold is the notation itself: that a reader can tell
// where a lifecycle starts, where it ends, and which way a transition runs.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/state-canvas-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await expect(page.locator(".uml-state").first()).toBeVisible();
});

const state = (page, name) => page.locator(`.uml-state[data-name="${name}"]`);

test("every state is named, and the two that are special say so", async ({ page }) => {
  await expect(page.locator(".uml-state")).toHaveCount(3);
  // Named, all of them: UML's initial and final pseudostates carry no name, and a
  // state nothing can name is a state no process can write.
  await expect(state(page, "draft").locator(".uml-state-name")).toHaveText("draft");
  await expect(state(page, "shipped").locator(".uml-state-name")).toHaveText("shipped");

  // Where an instance is created: the filled disc and its arrow, and only there.
  await expect(page.locator(".uml-state-start")).toHaveCount(1);
  await expect(state(page, "draft").locator(".uml-state-start")).toHaveCount(1);
  // What nothing leaves: the second ring, and only there.
  await expect(page.locator(".uml-state-final")).toHaveCount(1);
  await expect(state(page, "shipped").locator(".uml-state-final")).toHaveCount(1);
  expect(page.__errors).toEqual([]);
});

test("a transition is directed, which is what tells it from an association", async ({ page }) => {
  const lines = page.locator(".uml-transition-line");
  await expect(lines).toHaveCount(2);
  // The arrowhead is the whole difference: a class association says two things are
  // related, a transition says which way the object moves.
  await expect(lines.first()).toHaveAttribute("marker-end", "url(#uml-state-arrow)");
  await expect(page.locator(".uml-transition-label")).toHaveText(["approve", "ship"]);
  expect(page.__errors).toEqual([]);
});

test("the rules the server serves are the rules the canvas draws by", async ({ page }) => {
  const can = await page.evaluate(() => {
    const c = window.__canvas;
    const rules = c.diagram.get("rules");
    const reg = c.diagram.get("elementRegistry");
    const s = (n) => reg.get(`state-${n}`);
    return {
      // A record revised without leaving its stage: allowed, and the server says so.
      selfLoop: rules.allowed("connection.create", { source: s("draft"), target: s("draft") }),
      // "Final" and "leaves" are the two saying opposite things.
      outOfFinal: rules.allowed("connection.create", { source: s("shipped"), target: s("draft") }),
      intoFinal: rules.allowed("connection.create", { source: s("approved"), target: s("shipped") }),
      // A state is as wide as its name makes it, so a dragged corner would lie.
      resize: rules.allowed("shape.resize", { shape: s("draft") }),
    };
  });
  expect(can.selfLoop).toBe(true);
  expect(can.outOfFinal).toBe(false);
  expect(can.intoFinal).toBe(true);
  expect(can.resize).toBe(false);
  expect(page.__errors).toEqual([]);
});

test("a state is picked off the drawing, and reports itself to the host", async ({ page }) => {
  await page.locator('.djs-element:has(.uml-state[data-name="approved"])').click();
  await expect.poll(() => page.evaluate(() => window.__selected?.name)).toBe("approved");
  expect(page.__errors).toEqual([]);
});

// Drawing a second lifecycle into a canvas that already holds one has to replace it:
// diagram-js's element registry is per diagram, not per root.
test("rendering a second lifecycle replaces the first", async ({ page }) => {
  await page.evaluate(() => window.__canvas.render({
    states: [{ name: "neu", initial: true, x: 40, y: 40 }], transitions: [],
  }, []));
  await expect(page.locator(".uml-state")).toHaveCount(1);
  await expect(page.locator(".uml-transition-line")).toHaveCount(0);
  await expect(state(page, "neu").locator(".uml-state-start")).toHaveCount(1);
  expect(page.__errors).toEqual([]);
});
