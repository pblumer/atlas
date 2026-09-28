// End-to-end coverage for the Operations view's "click a task, see who is on it"
// filter (ADR-0261).
//
// The complaint behind it: a diagram badged "25205 here" beside an instance panel
// listing the newest fifty of fifty thousand, and no way to get from the first to
// the second. The token count says how many are waiting; the operator's next
// question is invariably *which*, and answering it meant a variable search for
// something they would have to know already.
//
// So the diagram is the query. Clicking an element narrows the panel to the
// instances whose token is sitting on it, clicking another switches to that one,
// and clicking the process — the canvas around the shapes — puts every instance
// back. These tests drive the real editor.js against a mock API that answers
// ?element= the way the server does.
import { test, expect } from "@playwright/test";

const open = async (page) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/instance-element-filter-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mountLive());
  await expect(page.locator(".vp-inst")).toHaveCount(3);
};

// listedKeys reads the instance keys the panel is showing, in order.
const listedKeys = (page) =>
  page.locator(".vp-inst .vp-inst-head b").allTextContents();

// A filtered panel is one section per switched-on legend count; `live` is the one
// that answers "who is sitting here" (ADR-draft-instances-that-left-an-element).
const section = (page, kind) => page.locator(`.vp-sect[data-kind="${kind}"]`);
const sectionRows = (page, kind) => section(page, kind).locator(".vp-inst");
const sectionKeys = (page, kind) =>
  sectionRows(page, kind).locator(".vp-inst-head b").allTextContents();
const toggle = (page, kind) => page.locator(`.legend-toggle[data-badge="${kind}"]`);

// clickShape clicks a diagram element by its BPMN id.
const clickShape = (page, id) => page.locator(`[data-element-id="${id}"]`).click();

test("clicking a task lists the instances sitting on it", async ({ page }) => {
  await open(page);
  expect(await listedKeys(page)).toEqual(["1003", "1002", "1001"]);

  await clickShape(page, "eintritt");

  // The panel is the answer: two instances, both the ones waiting there.
  await expect(sectionRows(page, "live")).toHaveCount(2);
  expect(await sectionKeys(page, "live")).toEqual(["1003", "1001"]);
  // And it says what it is narrowed to, in the element's own words, with the way out.
  const chip = page.locator(".vp-filter");
  await expect(chip).toBeVisible();
  await expect(chip).toContainText("Eintritt verbuchen");
  await expect(page.locator(".vp-title")).toContainText("at Eintritt verbuchen");
  // The shape the operator clicked is outlined, so the chip and the diagram are
  // visibly about the same element.
  await expect(page.locator('[data-element-id="eintritt"]')).toHaveClass(/atlas-filtered/);
  // The picker above the diagram agrees.
  await expect(page.locator("#instance-sel option").first()).toContainText("At Eintritt verbuchen");

  expect(page.__errors, "no page errors").toEqual([]);
});

test("clicking another task switches the filter to it", async ({ page }) => {
  await open(page);
  await clickShape(page, "eintritt");
  await expect(sectionRows(page, "live")).toHaveCount(2);

  await clickShape(page, "austritt");

  await expect(sectionRows(page, "live")).toHaveCount(1);
  expect(await sectionKeys(page, "live")).toEqual(["1002"]);
  await expect(page.locator(".vp-filter")).toContainText("Austritt");
  // Only one element is ever the filtered one.
  await expect(page.locator('[data-element-id="austritt"]')).toHaveClass(/atlas-filtered/);
  await expect(page.locator('[data-element-id="eintritt"]')).not.toHaveClass(/atlas-filtered/);
});

test("an element nothing is sitting on says so, rather than reading as an empty process", async ({ page }) => {
  await open(page);
  await clickShape(page, "end");

  await expect(sectionRows(page, "live")).toHaveCount(0);
  await expect(section(page, "live")).toContainText("No instance is sitting on");
  // It is still a filter, and still says how to leave it.
  await expect(page.locator(".vp-filter")).toBeVisible();
});

test("clicking the process lists every instance again", async ({ page }) => {
  await open(page);
  await clickShape(page, "eintritt");
  await expect(sectionRows(page, "live")).toHaveCount(2);

  // The canvas around the shapes is the process: a click that lands on no element.
  const box = await page.locator("#canvas").boundingBox();
  await page.mouse.click(box.x + box.width - 30, box.y + box.height - 30);

  await expect(page.locator(".vp-inst")).toHaveCount(3);
  expect(await listedKeys(page)).toEqual(["1003", "1002", "1001"]);
  await expect(page.locator(".vp-filter")).toHaveCount(0);
  await expect(page.locator('[data-element-id="eintritt"]')).not.toHaveClass(/atlas-filtered/);
});

test("clicking the same task again clears the filter", async ({ page }) => {
  await open(page);
  await clickShape(page, "eintritt");
  await expect(sectionRows(page, "live")).toHaveCount(2);

  await clickShape(page, "eintritt");

  await expect(page.locator(".vp-inst")).toHaveCount(3);
  await expect(page.locator(".vp-filter")).toHaveCount(0);
});

test("the chip's × leaves the filter", async ({ page }) => {
  await open(page);
  await clickShape(page, "austritt");
  await expect(sectionRows(page, "live")).toHaveCount(1);

  await page.locator(".vp-filter [data-filter-clear]").click();

  await expect(page.locator(".vp-inst")).toHaveCount(3);
  await expect(page.locator(".vp-filter")).toHaveCount(0);
});

test("the filter is a question put to the server, not a sieve over one page", async ({ page }) => {
  // This is the property that makes the feature usable at the scale it was asked
  // for. The panel lists one page of a version that may hold hundreds of thousands
  // of instances, so an element's instances cannot be found by filtering the rows
  // already in the browser — most of them are not there. The view must ask.
  await open(page);
  await page.evaluate(() => { window.__instanceCalls.length = 0; });
  await clickShape(page, "austritt");
  await expect(sectionRows(page, "live")).toHaveCount(1);

  const calls = await page.evaluate(() => window.__instanceCalls);
  expect(calls.some((u) => u.includes("element=austritt"))).toBe(true);
  // And it never asks for the finished half under a filter: a finished instance
  // holds no token, so that request's answer is empty by construction.
  expect(calls.filter((u) => u.includes("element=") && u.includes("state=finished"))).toEqual([]);
  // The history is asked of the element's own indexes too, never sieved out of a page:
  // one request per section, and without a half — those indexes are not split in two.
  const history = calls.filter((u) => u.includes("element=austritt") && u.includes("&at="));
  expect(history.some((u) => u.includes("at=passed"))).toBe(true);
  expect(history.some((u) => u.includes("at=cancelled"))).toBe(true);
  expect(history.filter((u) => u.includes("state="))).toEqual([]);
});

// The history half of the click (ADR-draft-instances-that-left-an-element). The shape
// in the screenshot that prompted it carried a single gray count of two million and a
// panel saying nobody was sitting on it: the number led nowhere. Now the panel lists
// the instances behind every count the legend has switched on.
test("a click lists the instances behind every count: sitting here, cancelled here, completed here", async ({ page }) => {
  await open(page);
  await clickShape(page, "eintritt");

  // One section per count, in a fixed order: who is here now first, so a long history
  // never buries the instances an operator most often clicks to find.
  await expect(page.locator(".vp-sect")).toHaveCount(3);
  expect(await page.locator(".vp-sect").evaluateAll((els) => els.map((e) => e.dataset.kind)))
    .toEqual(["live", "cancelled", "passed"]);
  expect(await sectionKeys(page, "live")).toEqual(["1003", "1001"]);
  expect(await sectionKeys(page, "cancelled")).toEqual(["980"]);
  expect(await sectionKeys(page, "passed")).toEqual(["1002", "990"]);
  // A history row is whatever the instance is now, and says so.
  await expect(section(page, "cancelled").locator(".vp-inst").first()).toContainText("terminated");
  await expect(section(page, "passed")).toContainText("Completed here and moved on");

  // The picker offers each instance once, however many sections it is in.
  const options = await page.locator("#instance-sel option").allTextContents();
  expect(options.filter((o) => o.startsWith("1002 "))).toHaveLength(1);

  // An element nobody was cancelled at says so in its own section, and only there.
  await clickShape(page, "austritt");
  await expect(section(page, "cancelled")).toContainText("No instance on this server was cancelled at");
  expect(await sectionKeys(page, "passed")).toEqual(["990"]);
  expect(await sectionKeys(page, "live")).toEqual(["1002"]);

  expect(page.__errors, "no page errors").toEqual([]);
});

test("the legend's switches choose which sections a click lists", async ({ page }) => {
  await open(page);
  await clickShape(page, "eintritt");
  await expect(page.locator(".vp-sect")).toHaveCount(3);

  // Switching the gray count off takes its section away at once — no poll to wait for —
  // and the view stops asking for it.
  await toggle(page, "passed").click();
  await expect(section(page, "passed")).toHaveCount(0);
  await expect(section(page, "live")).toHaveCount(1);
  await expect(section(page, "cancelled")).toHaveCount(1);
  await page.evaluate(() => { window.__instanceCalls.length = 0; });
  await page.waitForFunction(() => window.__instanceCalls.length > 0);
  await page.waitForTimeout(1800); // one more poll
  const calls = await page.evaluate(() => window.__instanceCalls);
  expect(calls.filter((u) => u.includes("at=passed"))).toEqual([]);

  // The green one too: the history alone, which is what "which way did this go" wants.
  await toggle(page, "live").click();
  await expect(section(page, "live")).toHaveCount(0);
  expect(await sectionKeys(page, "cancelled")).toEqual(["980"]);

  // All three off is a panel that says why it lists nothing, not an empty process.
  await toggle(page, "cancelled").click();
  await expect(page.locator(".vp-sect")).toHaveCount(0);
  await expect(page.locator(".vp-sect-none")).toContainText("switched off");

  // And back on, the section returns.
  await toggle(page, "passed").click();
  expect(await sectionKeys(page, "passed")).toEqual(["1002", "990"]);

  expect(page.__errors, "no page errors").toEqual([]);
});

test("a switch works on the list even where the browser does not keep it", async ({ page }) => {
  // A browser that does not keep site data accepts a write and forgets it. The switch
  // still takes its count off the diagram there, so the list has to follow the diagram
  // and not a preference that was never written down.
  await page.addInitScript(() => {
    Storage.prototype.setItem = function () { /* not kept */ };
  });
  await open(page);
  await clickShape(page, "eintritt");
  await expect(section(page, "passed")).toHaveCount(1);

  await toggle(page, "passed").click();
  await expect(section(page, "passed")).toHaveCount(0);
  await expect(section(page, "live")).toHaveCount(1);

  expect(page.__errors, "no page errors").toEqual([]);
});

test("each section pages on its own cursor", async ({ page }) => {
  await open(page);
  await clickShape(page, "start");

  // Five instances went through the start event, answered two to a page.
  await expect(sectionRows(page, "passed")).toHaveCount(2);
  await expect(section(page, "passed").locator(".vp-sect-n")).toHaveText("(2+)");
  // Nobody sits on a start event, and that section has nothing to load.
  await expect(section(page, "live").locator("[data-load-more]")).toHaveCount(0);

  await page.evaluate(() => { window.__instanceCalls.length = 0; });
  await section(page, "passed").locator("[data-load-more]").click();
  await expect(sectionRows(page, "passed")).toHaveCount(4);
  await section(page, "passed").locator("[data-load-more]").click();
  await expect(sectionRows(page, "passed")).toHaveCount(5);
  expect(await sectionKeys(page, "passed")).toEqual(["1003", "1002", "1001", "990", "980"]);
  await expect(section(page, "passed").locator("[data-load-more]")).toHaveCount(0);

  // The cursor was the history section's own: the live listing was never deepened.
  const calls = await page.evaluate(() => window.__instanceCalls);
  expect(calls.some((u) => u.includes("at=passed") && u.includes("before=1002"))).toBe(true);
  expect(calls.filter((u) => !u.includes("&at=") && u.includes("before="))).toEqual([]);

  expect(page.__errors, "no page errors").toEqual([]);
});
