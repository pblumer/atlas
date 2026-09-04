// End-to-end coverage for the call-activity Process ID picker + "create new
// process" in the Modeler Implement panel (api/web/editor.js, ADR-0076). Driven
// through the real vendored bpmn-js against a mock `api`: selecting the call
// activity on the Implement tab surfaces a datalist of existing callees (deployed
// processes and drafts), and "＋ Create new process" saves the caller, scaffolds a
// starter draft keyed by the process id, and navigates to it.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/call-activity-modeler-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  // Show the Implement tab, then select the call activity so its panel renders.
  await page.locator('[data-tab="implement"]').click();
  await page.evaluate(() => window.__selectCa());
  // Property groups start collapsed on open except General, so expand "Called process"
  // to reveal its Process ID picker before the tests drive it.
  await page.locator(".pgroup-head", { hasText: "Called process" }).click();
});

test("the Process ID field offers deployed processes and drafts as suggestions", async ({ page }) => {
  await expect(page.locator("#f-call-processid")).toBeVisible();
  // The datalist is populated asynchronously from /processes and /drafts.
  const options = page.locator("#f-call-proc-list option");
  await expect(options).toHaveCount(3);
  const values = await options.evaluateAll((els) => els.map((e) => e.value));
  expect(values).toContain("pruefe-auftrag");
  expect(values).toContain("child");
  expect(values).toContain("entwurf-x");
  expect(page.__errors).toEqual([]);
});

test("create new process saves the caller, scaffolds the child, and navigates", async ({ page }) => {
  await page.locator("#f-call-processid").fill("neuer-prozess");
  await expect(page.locator("#f-call-newproc")).toBeVisible();

  await page.evaluate(() => { window.__calls.length = 0; }); // observe only the create flow
  await page.locator("#f-call-newproc").click();

  // It navigates to the new draft (same window — a hash change).
  await expect.poll(() => page.evaluate(() => location.hash)).toBe("#/modeler/draft/neuer-prozess");

  const posts = await page.evaluate(() =>
    window.__calls.filter((c) => c.method === "POST" && /\/drafts/.test(c.url)));
  // Two POSTs: the caller (persisted before navigating) and the child scaffold.
  expect(posts.length).toBe(2);
  // The child scaffold carries the entered process id as its bpmn:process id.
  const childPost = posts.find((c) => /id="neuer-prozess"/.test(c.body || ""));
  expect(childPost, "a scaffold draft for neuer-prozess was POSTed").toBeTruthy();
  expect(childPost.body).toContain("bpmn:startEvent");

  // The caller now references the child in its zeebe:calledElement.
  const callerPost = posts.find((c) => /calledElement[^>]*processId="neuer-prozess"/.test(c.body || ""));
  expect(callerPost, "the caller was saved pointing at neuer-prozess").toBeTruthy();
});

// --- Drilling into the called process (ADR-0076) ---------------------------------
// The call activity's "+" is the way into the process it calls: hovering the shape
// says so, double-clicking that marker opens it, and the panel's Open button is the
// same door for a pointer nowhere near the shape. Everything below drives the real
// vendored bpmn-js — the marker is located by the `data-marker` attribute bpmn-js's
// own renderer puts on it, so the hit test and the drawing cannot drift apart
// silently.

// markerCenter is the screen position of the "+" bpmn-js drew on a shape.
async function markerCenter(page, elementId) {
  const box = await page
    .locator(`[data-element-id="${elementId}"] path[data-marker="sub-process"]`)
    .boundingBox();
  if (!box) throw new Error(`no sub-process marker on ${elementId}`);
  return { x: box.x + box.width / 2, y: box.y + box.height / 2 };
}

// showCalledProcessPanel re-does what beforeEach does, for a test that remounts.
async function showCalledProcessPanel(page) {
  await page.locator('[data-tab="implement"]').click();
  await page.evaluate(() => window.__selectCa());
  await page.locator(".pgroup-head", { hasText: "Called process" }).click();
}

test("hovering a call activity names what its + opens", async ({ page }) => {
  await page.locator("#f-call-processid").fill("pruefe-auftrag");
  await page.locator("#f-call-processid").blur(); // committing the field writes the model
  const shape = await page.locator('[data-element-id="Activity_ca"] .djs-hit').first().boundingBox();
  await page.mouse.move(shape.x + shape.width * 0.3, shape.y + shape.height * 0.3);

  const tip = page.locator(".ca-drill-tip");
  await expect(tip).toBeVisible();
  await expect(tip).toContainText("pruefe-auftrag");
  await expect(tip).toContainText("Double-click");
  await expect(page.locator(".ca-drill-ring")).toBeVisible();

  // The cue must never be in the way of the gesture it advertises.
  for (const sel of [".ca-drill-tip", ".ca-drill-ring"]) {
    const events = await page.locator(sel).evaluate((el) => getComputedStyle(el).pointerEvents);
    expect(events, `${sel} must not take pointer events`).toBe("none");
  }

  // Off the shape again, the cue goes.
  await page.mouse.move(shape.x - 60, shape.y - 40);
  await expect(page.locator(".ca-drill-tip")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("an untouched diagram opens the callee without saving or asking", async ({ page }) => {
  // Activity_ca2 already names a deployed callee, so this is the plain case: nothing
  // has been edited, so there is nothing to save and nothing to warn about — the
  // gesture is a navigation, not a transaction.
  const asked = [];
  page.on("dialog", (d) => { asked.push(d.message()); d.dismiss(); });
  await page.evaluate(() => { window.__calls.length = 0; });

  const at = await markerCenter(page, "Activity_ca2");
  await page.mouse.dblclick(at.x, at.y);

  await expect.poll(() => page.evaluate(() => location.hash)).toBe("#/modeler/d/92");
  expect(asked, "an unedited diagram must not be asked about").toEqual([]);
  const writes = await page.evaluate(() => window.__calls.filter((c) => c.method !== "GET"));
  expect(writes, "nothing was edited, so nothing is written").toEqual([]);
  expect(page.__errors).toEqual([]);
});

test("double-clicking the + opens the callee's draft", async ({ page }) => {
  page.on("dialog", (d) => d.accept()); // the unsaved-edits guard (see below)
  await page.locator("#f-call-processid").fill("entwurf-x");

  const at = await markerCenter(page, "Activity_ca");
  await page.mouse.dblclick(at.x, at.y);

  // A draft holds that id, so the draft is what opens — that is where the work is.
  await expect.poll(() => page.evaluate(() => location.hash)).toBe("#/modeler/draft/entwurf-x");
  expect(page.__errors).toEqual([]);
});

test("double-clicking the + opens the newest deployed version when no draft holds the id", async ({ page }) => {
  page.on("dialog", (d) => d.accept());
  await page.locator("#f-call-processid").fill("pruefe-auftrag");

  const at = await markerCenter(page, "Activity_ca");
  await page.mouse.dblclick(at.x, at.y);

  // v2 (key 92), not the v1 that is also deployed.
  await expect.poll(() => page.evaluate(() => location.hash)).toBe("#/modeler/d/92");
  expect(page.__errors).toEqual([]);
});

test("the panel's Open button is the same door", async ({ page }) => {
  page.on("dialog", (d) => d.accept());
  await page.locator("#f-call-processid").fill("entwurf-x");

  const open = page.locator("#f-call-open");
  await expect(open).toBeVisible();
  await open.click();

  await expect.poll(() => page.evaluate(() => location.hash)).toBe("#/modeler/draft/entwurf-x");
  expect(page.__errors).toEqual([]);
});

test("double-clicking the shape itself still renames it", async ({ page }) => {
  await page.locator("#f-call-processid").fill("entwurf-x");
  const shape = await page.locator('[data-element-id="Activity_ca"] .djs-hit').first().boundingBox();
  await page.mouse.dblclick(shape.x + shape.width * 0.3, shape.y + shape.height * 0.3);

  // The body is the rename gesture bpmn-js has always had; only the marker drills in.
  await expect(page.locator(".djs-direct-editing-parent")).toBeVisible();
  expect(await page.evaluate(() => location.hash)).toBe("");
  expect(page.__errors).toEqual([]);
});

test("a deployment's unsaved edits are not discarded without a confirmation", async ({ page }) => {
  const asked = [];
  page.on("dialog", (d) => { asked.push(d.message()); d.dismiss(); });
  // Typing the callee id is itself an edit, and this session opened a deployed
  // definition — there is no draft to put it in, so leaving has to ask.
  await page.locator("#f-call-processid").fill("entwurf-x");
  await page.locator("#f-call-open").click();

  await expect.poll(() => asked.length).toBe(1);
  expect(asked[0]).toContain("unsaved changes");
  expect(await page.evaluate(() => location.hash)).toBe(""); // refused: we stayed
  expect(page.__errors).toEqual([]);
});

test("a draft caller is saved before it leaves for the callee", async ({ page }) => {
  await page.evaluate(() => window.__mountDraft());
  await showCalledProcessPanel(page);
  await page.locator("#f-call-processid").fill("entwurf-x");
  await page.evaluate(() => { window.__calls.length = 0; }); // observe only the drill-in

  const at = await markerCenter(page, "Activity_ca");
  await page.mouse.dblclick(at.x, at.y);

  await expect.poll(() => page.evaluate(() => location.hash)).toBe("#/modeler/draft/entwurf-x");
  // The caller went with it: a session that addresses a draft saves rather than asks.
  const posts = await page.evaluate(() =>
    window.__calls.filter((c) => c.method === "POST" && /\/drafts/.test(c.url)));
  expect(posts.length).toBe(1);
  expect(posts[0].url).toContain("from=caller");
  expect(posts[0].body).toMatch(/calledElement[^>]*processId="entwurf-x"/);
  expect(page.__errors).toEqual([]);
});
