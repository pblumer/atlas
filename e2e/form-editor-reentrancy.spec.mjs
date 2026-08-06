// End-to-end regression test for the form editor's re-entrancy guard
// (api/web/form-editor.js). Like the BPMN editor, mountFormEditor builds a heavy
// widget (the form-js Playground, plus timers) AFTER awaiting the form definition
// and the form-js bundle. Without a generation guard, a mount a newer navigation
// had already superseded would resume and instantiate that Playground into a
// detached container, leaking it. mountFormEditor now captures a generation token
// after cleanup() and bails after each await once cleanup() (the navigation
// teardown) has bumped it.
//
// The harness supersedes the mount during its stalled form-definition load, so the
// guard fires before form-js is ever imported — the assertion needs no form-js.
import { test, expect } from "@playwright/test";

const wait = (ms) => new Promise((r) => setTimeout(r, ms));

test.beforeEach(async ({ page }) => {
  page.__errors = [];
  page.on("pageerror", (e) => page.__errors.push(e.message));
  await page.goto("/form-editor-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
});

test("a superseded form-editor mount bails before building the playground", async ({ page }) => {
  // Start a mount whose GET /forms/form-x is stalled, then tear it down (as
  // navigating away would) before it resolves.
  await page.evaluate(() => { window.__p = window.__mount(400); });
  await wait(120); // mount is parked on the stalled form-definition load
  await page.evaluate(() => window.__cleanup()); // supersede — bumps the generation
  await page.evaluate(() => window.__p); // let the mount resume and bail

  // Guarded, the mount returned right after the form load without touching the
  // container, so the initial "Loading form editor…" shell is still there — the
  // Playground was never built. Unguarded, the container would have been cleared and
  // replaced (or shown a load error).
  await expect(page.locator("#form-playground")).toContainText("Loading form editor");
  expect(page.__errors, "page errors during superseded mount").toEqual([]);
});
