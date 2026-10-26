// e2e for readable element ids (api/web/elementids.js, ADR-0474).
//
// A new BPMN element is named <type>_<yyyymmdd>_<researcher> — activity_20261023_heisenberg
// — instead of bpmn-js's Activity_0jmndkk, and a new diagram's process the same way
// instead of Process_<timestamp+random>. The shorter id gives up the near-certainty a
// random one had, so these tests are mostly about duplicates: none within a diagram,
// however many elements are drawn, none with an id the imported diagram already holds,
// and no process id the installation already holds — as a draft, as a deployment, or
// only in the eyes of the server's availability check.
import { test, expect } from "@playwright/test";

// The author's local day the tests draw on; the date segment must read 20261023.
const DAY = new Date(2026, 9, 23, 10, 30, 0);
const STEM = "_20261023_";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.clock.setFixedTime(DAY);
  await page.goto("/element-ids-harness.html");
  await page.waitForFunction(() => window.__ready === true);
  page._errors = errors;
});

test.afterEach(async ({ page }) => {
  expect(page._errors, "no uncaught page errors").toEqual([]);
});

// The pool itself: a name a person can say, the same in every tool, and no name twice.
test("the researcher pool is lowercase ASCII without duplicates", async ({ page }) => {
  const names = await page.evaluate(() => [...window.__lib.RESEARCHERS]);
  expect(names.length).toBeGreaterThan(100);
  expect(new Set(names).size).toBe(names.length);
  for (const n of names) expect(n).toMatch(/^[a-z]+$/);
});

test("the type and date segments", async ({ page }) => {
  const got = await page.evaluate(() => ({
    activity: window.__lib.idType("Activity_"),
    dataObject: window.__lib.idType("DataObjectReference_"),
    empty: window.__lib.idType(""),
    date: window.__lib.idDate(new Date(2026, 0, 5, 23, 59)),
  }));
  expect(got).toEqual({ activity: "activity", dataObject: "dataobjectreference", empty: "element", date: "20260105" });
});

// With every name taken for a type on a day, a counter follows the name: the supply
// never runs out and never repeats.
test("an exhausted pool continues with a counter, never with a duplicate", async ({ page }) => {
  const r = await page.evaluate(() => {
    const lib = window.__lib;
    const taken = new Set();
    const out = [];
    for (let i = 0; i < lib.RESEARCHERS.length * 2 + 5; i++) {
      const id = lib.readableId("activity", { taken: (c) => taken.has(c), now: () => new Date(2026, 9, 23) });
      taken.add(id);
      out.push(id);
    }
    return { out, size: taken.size, n: lib.RESEARCHERS.length };
  });
  expect(r.size).toBe(r.out.length);
  for (const id of r.out.slice(0, r.n)) expect(id).toMatch(/^activity_20261023_[a-z]+$/);
  for (const id of r.out.slice(r.n, 2 * r.n)) expect(id).toMatch(/^activity_20261023_[a-z]+2$/);
  for (const id of r.out.slice(2 * r.n)) expect(id).toMatch(/^activity_20261023_[a-z]+3$/);
});

test("a new diagram's process and start event read <type>_<date>_<researcher>", async ({ page }) => {
  await page.evaluate(() => window.__mount());
  const pids = await page.evaluate(() => window.__processId());
  expect(pids).toHaveLength(1);
  expect(pids[0]).toMatch(new RegExp(`^process${STEM}[a-z]+\\d*$`));
  const all = await page.evaluate(() => window.__ids());
  expect(all.some((id) => new RegExp(`^event${STEM}[a-z]+\\d*$`).test(id))).toBe(true);
  // The breadcrumb names the diagram by its process id until it has a name.
  await expect(page.locator(".crumb-current")).toHaveText(pids[0]);
  // The process id was put to the server's availability check before it was used.
  const calls = await page.evaluate(() => window.__calls);
  expect(calls).toContain(`GET /api/v1/drafts/${pids[0]}/availability`);
});

test("drawn tasks, events, gateways and flows get readable ids, never twice", async ({ page }) => {
  await page.evaluate(() => window.__mount());
  const tasks = await page.evaluate(() => window.__draw("bpmn:Task", 400));
  for (const id of tasks) expect(id).toMatch(new RegExp(`^activity${STEM}[a-z]+\\d*$`));
  // More tasks than researchers, so the counter must have stepped in.
  expect(tasks.some((id) => /\d$/.test(id))).toBe(true);
  const gw = await page.evaluate(() => window.__draw("bpmn:ExclusiveGateway", 1));
  expect(gw[0]).toMatch(new RegExp(`^gateway${STEM}[a-z]+\\d*$`));
  const ev = await page.evaluate(() => window.__draw("bpmn:EndEvent", 1));
  expect(ev[0]).toMatch(new RegExp(`^event${STEM}[a-z]+\\d*$`));
  const flow = await page.evaluate(([a, b]) => window.__connect(a, b), [tasks[0], tasks[1]]);
  expect(flow).toMatch(new RegExp(`^flow${STEM}[a-z]+\\d*$`));
  const msg = await page.evaluate(() => window.__create("bpmn:Message"));
  expect(msg).toMatch(new RegExp(`^message${STEM}[a-z]+\\d*$`));

  const all = await page.evaluate(() => window.__ids());
  expect(new Set(all).size).toBe(all.length);
  // The diagram interchange keeps bpmn-js's own scheme: nobody reads it.
  const di = await page.evaluate(() => window.__nextPrefixed("BPMNShape_"));
  expect(di).toMatch(/^BPMNShape_/);
});

// An id an imported diagram already holds is claimed on import and never minted again:
// with all but one researcher taken for activities on the day, a new task gets that one.
test("an id the opened diagram already holds is never handed out", async ({ page }) => {
  const names = await page.evaluate(() => [...window.__lib.RESEARCHERS]);
  const free = names[names.length - 1];
  const tasks = names.slice(0, -1).map((n, i) =>
    `<bpmn:task id="activity${STEM}${n}" name="T${i}"/>`).join("\n    ");
  const xml = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI" xmlns:dc="http://www.omg.org/spec/DD/20100524/DC" id="Definitions_x" targetNamespace="http://atlas/bpmn">
  <bpmn:process id="existing" isExecutable="true">
    ${tasks}
  </bpmn:process>
  <bpmndi:BPMNDiagram id="D"><bpmndi:BPMNPlane id="P" bpmnElement="existing"/></bpmndi:BPMNDiagram>
</bpmn:definitions>`;
  await page.evaluate((x) => window.__mount({ draftXml: x }), xml);
  const first = await page.evaluate(() => window.__draw("bpmn:Task", 1));
  expect(first[0]).toBe(`activity${STEM}${free}`);
  const second = await page.evaluate(() => window.__draw("bpmn:Task", 1));
  expect(second[0]).toMatch(new RegExp(`^activity${STEM}[a-z]+2$`));
});

// A process id is the installation-wide identity deployments are grouped by: one held by
// a draft or a deployment is not chosen, and neither is one the availability check
// reports taken (a draft the author cannot see).
test("a process id held elsewhere on the installation is not chosen", async ({ page }) => {
  const names = await page.evaluate(() => [...window.__lib.RESEARCHERS]);
  const [viaCheck, free, ...rest] = names;
  const half = Math.floor(rest.length / 2);
  const drafts = rest.slice(0, half).map((n) => ({ processId: `process${STEM}${n}`, name: n }));
  const processes = rest.slice(half).map((n, i) => ({ key: i + 1, processId: `process${STEM}${n}`, name: n }));
  await page.evaluate((w) => window.__mount(w), {
    drafts, processes, unavailable: [`process${STEM}${viaCheck}`],
  });
  const pids = await page.evaluate(() => window.__processId());
  expect(pids).toEqual([`process${STEM}${free}`]);

  // A pool added later is a second process: it avoids the same ids, and the main one.
  const pool = await page.evaluate(() => {
    window.__draw("bpmn:Participant", 1);
    return window.__processId();
  });
  for (const id of pool) expect(id).toMatch(new RegExp(`^process${STEM}[a-z]+\\d*$`));
  expect(new Set(pool).size).toBe(pool.length);
  const held = new Set([...drafts, ...processes].map((d) => d.processId));
  for (const id of pool) expect(held.has(id)).toBe(false);
});
