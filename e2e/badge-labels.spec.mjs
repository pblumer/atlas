// End-to-end coverage for where the Operations views put their runtime badges
// (ADR-0252).
//
// The badges used to sit in the four *inner* corners of a shape, which is where the
// words are: a task's caption is drawn inside its box, and an event's is centred
// underneath, far wider than the 36px circle. On an ordinary model that produced
// three collisions — the decision badge covering 69px of a 74px line of a task name,
// and the token count sitting on the captions of both the start event and the
// gateway. Nobody notices while modelling, because none of it is drawn until the
// process runs.
//
// A layout rule is only as good as the geometry it produces, and only a browser
// knows that geometry: bpmn-js wraps the caption itself, in the font the app ships.
// So this measures rather than inspects the source — every badge against every
// caption, and every badge against every other badge.
import { test, expect } from "@playwright/test";

// The badge kinds the live view draws, all of them present in the harness at once.
const KINDS = ["tokens", "incident", "open-task", "decision"];

// rects reads the rendered geometry the assertions are about: the captions bpmn-js
// laid out, the badges the view overlaid, and the shapes both hang off.
async function rects(page) {
  return page.evaluate(() => {
    const box = (el) => {
      const b = el.getBoundingClientRect();
      return { x: b.x, y: b.y, w: b.width, h: b.height, right: b.right, bottom: b.bottom };
    };
    const labels = [...document.querySelectorAll("text.djs-label")].map((el) => ({
      text: el.textContent.trim(),
      owner: (el.closest("g.djs-element") || {}).dataset.elementId,
      ...box(el),
    }));
    const shapes = {};
    for (const el of document.querySelectorAll("g.djs-shape")) shapes[el.dataset.elementId] = box(el);
    const badges = [...document.querySelectorAll(".djs-overlay")].map((el) => ({
      kind: (el.className.match(/djs-overlay-([\w-]+)/) || [])[1] || "?",
      // The overlay container is the anchor; the badge itself is what is drawn.
      ...box(el.firstElementChild || el),
    }));
    return { labels, shapes, badges };
  });
}

// overlap returns the intersection of two rectangles, or null when they are clear
// of each other. A shared edge is not an overlap.
function overlap(a, b) {
  const x = Math.min(a.right, b.right) - Math.max(a.x, b.x);
  const y = Math.min(a.bottom, b.bottom) - Math.max(a.y, b.y);
  return x > 0.5 && y > 0.5 ? { x: Math.round(x), y: Math.round(y) } : null;
}

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/badge-labels-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  // An instance is selected, not "all": the decision badge is drawn for the instance
  // whose decisions the panel is showing, so this is the state that has every kind.
  await page.evaluate(() => window.__mountLive(window.__INST));
  await expect(page.locator("#canvas .djs-overlay")).not.toHaveCount(0);
});

test.afterEach(async ({ page }) => {
  expect(page.__errors, "the live view must not throw").toEqual([]);
});

test("every badge kind is drawn, so the rest of this file is measuring something", async ({ page }) => {
  const { badges } = await rects(page);
  for (const kind of KINDS) {
    expect(badges.filter((b) => b.kind === kind).length, `no ${kind} badge on the diagram`).toBeGreaterThan(0);
  }
});

test("no badge lands on a caption", async ({ page }) => {
  const { labels, badges } = await rects(page);
  expect(labels.length, "the model's names should be rendered").toBeGreaterThan(3);

  const hits = [];
  for (const b of badges) {
    for (const l of labels) {
      const o = overlap(b, l);
      if (o) hits.push(`${b.kind} covers ${o.x}×${o.y}px of "${l.text}" (${l.owner})`);
    }
  }
  expect(hits, "a badge is drawn over a name").toEqual([]);
});

test("no badge lands on another badge", async ({ page }) => {
  const { badges } = await rects(page);
  const hits = [];
  for (let i = 0; i < badges.length; i++) {
    for (let j = i + 1; j < badges.length; j++) {
      const o = overlap(badges[i], badges[j]);
      if (o) hits.push(`${badges[i].kind} overlaps ${badges[j].kind} by ${o.x}×${o.y}px`);
    }
  }
  expect(hits, "two badges are drawn on top of each other").toEqual([]);
});

test("a badge hangs outside its shape, never in it", async ({ page }) => {
  // The structural rule rather than this diagram's luck: whatever the caption turns
  // out to be, a badge that is not over the box cannot be over a caption drawn in it.
  const { shapes, badges } = await rects(page);
  const boxes = Object.entries(shapes).filter(([id]) => !id.endsWith("_label"));
  const hits = [];
  for (const b of badges) {
    for (const [id, s] of boxes) {
      const o = overlap(b, s);
      if (o) hits.push(`${b.kind} sits ${o.x}×${o.y}px inside ${id}`);
    }
  }
  expect(hits, "a badge is drawn over the body of a shape").toEqual([]);
});

test("nothing is drawn under a shape whose caption is under it", async ({ page }) => {
  // An event or a gateway carries its name below itself, as a label element of its
  // own. That band belongs to the caption whatever its length, so both of the
  // bottom-corner badges move above the shape there — which is why the start event's
  // token count and the gateway's are above them, and the task's are below.
  const { shapes, badges } = await rects(page);
  for (const id of ["Start_1", "Gate_1"]) {
    const shape = shapes[id];
    const below = badges.filter((b) => b.y >= shape.bottom - 0.5 && b.x < shape.right && b.right > shape.x);
    expect(below.map((b) => b.kind), `${id} carries its caption below it; nothing else may go there`).toEqual([]);
  }
  // And the counterpart: a task's caption is inside it, so the space under it is free
  // and the bottom badges really do use it.
  const task = shapes.Task_pay;
  const under = badges.filter((b) => b.y >= task.bottom - 0.5 && b.x < task.right && b.right > task.x);
  expect(under.map((b) => b.kind).sort(), "a task's bottom corners are usable").toEqual(["incident", "tokens"]);
});

test("a badge stays a badge: one line, about as wide as it is tall", async ({ page }) => {
  // What made the old badges collide with everything was their width — a pill
  // spelling out "⚠ 2 incidents" is 90px, most of a task and three times an event.
  // They carry a glyph and at most a count now, so a badge that grows back into a
  // sentence fails here rather than in production.
  const { badges } = await rects(page);
  for (const b of badges) {
    expect(b.h, `${b.kind} is taller than one line`).toBeLessThanOrEqual(26);
    expect(b.w, `${b.kind} is too wide to sit beside a shape`).toBeLessThanOrEqual(48);
  }
});

test("the compact badges still say what they are", async ({ page }) => {
  // The words moved into the tooltip and the accessible name; they did not vanish.
  await expect(page.locator(".incident-badge")).toHaveAttribute("title", /1 incident — die Freigabe/);
  await expect(page.locator(".incident-badge")).toHaveAttribute("aria-label", "1 incident");
  await expect(page.locator(".task-open")).toHaveAttribute("title", /Open the waiting user task/);
  await expect(page.locator(".decision-badge")).toHaveAttribute("aria-label", "Inspect this decision");
});

test("the live view says when a diagram was adjusted after it was deployed", async ({ page }) => {
  // The other half of the same complaint (ADR-0251): an
  // operator may move what a badge used to cover, in place, on the deployment. The
  // picture then differs from the deployed artefact, and this view is where somebody
  // would otherwise compare it against a printed copy and conclude it was redeployed.
  const note = page.locator(".live-layout-note");
  await expect(note).toBeVisible();
  await expect(note).toContainText("layout adjusted");
  await expect(note).toHaveAttribute("title", /the process, its version and its instances are the deployed ones/);
  await expect(note).toHaveAttribute("title", /adjusted by u-patrick/);
});
