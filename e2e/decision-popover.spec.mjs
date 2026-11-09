// The hover card that shows a decision table's trace (api/web/dmn-trace.js, .dec-pop
// in app.css) — Operations → Decisions, the live view's panel, the instance replay.
//
// The card takes no pointer events: it is a glance, and nothing inside it can be
// scrolled to. So the one property that matters is that the whole table is inside
// the card and the card is inside the window. A wide table — a long list of literals
// in one cell, a long input expression — used to run out past the card's right edge
// over the page beneath it, and a tall one off the bottom of the window.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/decision-popover-harness.html");
  await page.waitForFunction(() => window.__ready === true);
  page._errors = errors;
});

test.afterEach(async ({ page }) => {
  expect(page._errors, "no uncaught page errors").toEqual([]);
});

// A table shaped like the one that was reported: a FIRST policy over four inputs, one
// rule whose condition is a list of thirteen reserved names, a long input expression,
// and two hits whose outputs are long strings.
const reserved = '"admin", "administrator", "postmaster", "hostmaster", "webmaster", "abuse", ' +
  '"noreply", "no-reply", "security", "sicherheit", "root", "support", "test"';
const out = ["atlas-test-58o4-tm-gl-hw-geschaeftsleitung", "atlas-test-58o4-TM-GL HW Geschaeftsleitung", true, "Namensregel erfüllt"];
const rule = (index, entries, matched) => ({
  index, matched,
  conditions: entries.map((entry) => ({ entry, matched: entry === "-" || entry === '"team"' })),
  outputs: matched ? out : null,
});
const wideTable = (rows = 16) => {
  const rules = [
    rule(0, ['"site"', "-", "-", "-"], false),
    rule(1, ["-", 'null, ""', "-", "-"], false),
    rule(2, ["-", reserved, "-", "-"], false),
    rule(3, ["-", "-", "> 30", "-"], false),
    rule(4, ['"team"', "-", "-", "-"], true),
  ];
  for (let i = rules.length; i < rows - 1; i++) rules.push(rule(i, [`"typ-${i}"`, "-", "-", "-"], false));
  rules.push(rule(rows - 1, ["-", "-", "-", "-"], true));
  return {
    hitPolicy: "F",
    inputs: [
      { expression: "objekttyp", value: "team" },
      { expression: "slug", value: "hw-geschaeftsleitung" },
      { expression: "string length(slug) + string length(bereich)", value: 22 },
      { expression: "praefix != null", value: true },
    ],
    rules,
  };
};

// show draws the table into the card and places it beside an anchor row, exactly as
// the three hover handlers do, then measures card, table and window.
const show = (page, table, anchor) => page.evaluate(({ table, anchor }) => {
  const pop = document.getElementById("dec-pop");
  pop.innerHTML = `<div class="pop-title">M365Namensregel</div>` + window.__renderTraceTable(table, 0);
  pop.hidden = false;
  window.__placeTracePop(pop, { ...anchor, right: anchor.left + 200, bottom: anchor.top + 20 });
  const r = (el) => { const b = el.getBoundingClientRect(); return { left: b.left, right: b.right, top: b.top, bottom: b.bottom }; };
  return { pop: r(pop), table: r(pop.querySelector(".mgrid")), vw: innerWidth, vh: innerHeight };
}, { table, anchor });

const expectContained = (m) => {
  // The table inside the card …
  expect(m.table.left).toBeGreaterThanOrEqual(m.pop.left);
  expect(m.table.right).toBeLessThanOrEqual(m.pop.right);
  expect(m.table.bottom).toBeLessThanOrEqual(m.pop.bottom);
  // … and the card inside the window.
  expect(m.pop.left).toBeGreaterThanOrEqual(0);
  expect(m.pop.right).toBeLessThanOrEqual(m.vw);
  expect(m.pop.top).toBeGreaterThanOrEqual(0);
  expect(m.pop.bottom).toBeLessThanOrEqual(m.vh);
};

for (const width of [1536, 800]) {
  test(`a wide decision table stays inside the card at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 });
    const m = await show(page, wideTable(), { left: 300, top: 860 });
    expectContained(m);
  });
}

test("a table too tall for either side of its row is still kept inside the window", async ({ page }) => {
  // The row sits in the middle: the card fits neither above nor below it, which is
  // where the old "else below" sent it off the bottom of the window.
  await page.setViewportSize({ width: 1200, height: 700 });
  const m = await show(page, wideTable(16), { left: 40, top: 330 });
  expectContained(m);
});

test("a card that fits above its row is still drawn above it", async ({ page }) => {
  // The common case must not move: a small table above the row it explains.
  await page.setViewportSize({ width: 1200, height: 900 });
  const small = wideTable(3);
  small.rules = small.rules.filter((r) => r.index !== 2);
  const m = await show(page, small, { left: 40, top: 700 });
  expect(m.pop.bottom).toBeLessThanOrEqual(700);
  expectContained(m);
});
