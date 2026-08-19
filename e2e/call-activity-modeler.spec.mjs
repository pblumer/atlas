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
