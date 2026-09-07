// End-to-end coverage for the Operations view's "click a task, see who is on it"
// filter (ADR-draft-instances-on-an-element).
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

// clickShape clicks a diagram element by its BPMN id.
const clickShape = (page, id) => page.locator(`[data-element-id="${id}"]`).click();

test("clicking a task lists the instances sitting on it", async ({ page }) => {
  await open(page);
  expect(await listedKeys(page)).toEqual(["1003", "1002", "1001"]);

  await clickShape(page, "eintritt");

  // The panel is the answer: two instances, both the ones waiting there.
  await expect(page.locator(".vp-inst")).toHaveCount(2);
  expect(await listedKeys(page)).toEqual(["1003", "1001"]);
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
  await expect(page.locator(".vp-inst")).toHaveCount(2);

  await clickShape(page, "austritt");

  await expect(page.locator(".vp-inst")).toHaveCount(1);
  expect(await listedKeys(page)).toEqual(["1002"]);
  await expect(page.locator(".vp-filter")).toContainText("Austritt");
  // Only one element is ever the filtered one.
  await expect(page.locator('[data-element-id="austritt"]')).toHaveClass(/atlas-filtered/);
  await expect(page.locator('[data-element-id="eintritt"]')).not.toHaveClass(/atlas-filtered/);
});

test("an element nothing is sitting on says so, rather than reading as an empty process", async ({ page }) => {
  await open(page);
  await clickShape(page, "end");

  await expect(page.locator(".vp-inst")).toHaveCount(0);
  await expect(page.locator(".var-panel")).toContainText("No instance is sitting on");
  // It is still a filter, and still says how to leave it.
  await expect(page.locator(".vp-filter")).toBeVisible();
});

test("clicking the process lists every instance again", async ({ page }) => {
  await open(page);
  await clickShape(page, "eintritt");
  await expect(page.locator(".vp-inst")).toHaveCount(2);

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
  await expect(page.locator(".vp-inst")).toHaveCount(2);

  await clickShape(page, "eintritt");

  await expect(page.locator(".vp-inst")).toHaveCount(3);
  await expect(page.locator(".vp-filter")).toHaveCount(0);
});

test("the chip's × leaves the filter", async ({ page }) => {
  await open(page);
  await clickShape(page, "austritt");
  await expect(page.locator(".vp-inst")).toHaveCount(1);

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
  await expect(page.locator(".vp-inst")).toHaveCount(1);

  const calls = await page.evaluate(() => window.__instanceCalls);
  expect(calls.some((u) => u.includes("element=austritt"))).toBe(true);
  // And it never asks for the finished half under a filter: a finished instance
  // holds no token, so that request's answer is empty by construction.
  expect(calls.filter((u) => u.includes("element=") && u.includes("state=finished"))).toEqual([]);
});
