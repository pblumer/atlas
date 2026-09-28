// End-to-end coverage for the collaboration view's runtime overlay
// (api/web/editor.js mountCollaboration, ADR-0038), driven through the real vendored
// bpmn-js against a mock `api`.
//
// This was the third runtime view and the last one still drawing the picture ADR-0249
// retired: two states per shape and no numbers at all, so an event-based gateway waiting
// on the other pool's reply read as N concurrent waits — every armed branch green, the
// gateway not — and which event had actually won could not be read anywhere. It now
// draws what the live view and the replay draw, from the same helpers: the race counted
// once on the gateway, the armed branches outlined, and each shape's history split into
// completed (gray) and cancelled (amber) (issue #802).
import { test, expect } from "@playwright/test";

const open = async (page, mount = "__mountRace") => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/collaboration-overlay-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate((m) => window[m](), mount);
};

const badges = (page, id, cls = "") =>
  page.locator(`.djs-overlays[data-container-id="${id}"] .token-badge${cls}`);
const shape = (page, id) => page.locator(`#canvas g[data-element-id="${id}"]`);
const toggle = (page, kind) => page.locator(`.legend-toggle[data-badge="${kind}"]`);

// poll asks the view for a fresh runtime and waits until it has been read.
const poll = async (page) => {
  const before = await page.evaluate(() => window.__polls);
  await page.locator("#refresh").click();
  await page.waitForFunction((n) => window.__polls > n, before);
};

test("counts the race on the gateway, not once per armed branch", async ({ page }) => {
  await open(page);

  // The gateway holds the wait: two customers are still racing, one race is decided.
  await expect(shape(page, "gw")).toHaveClass(/atlas-active/);
  await expect(badges(page, "gw")).toHaveText(["1", "2"]);
  await expect(badges(page, "gw").nth(1)).toHaveAttribute("title", /2 live token/);

  // The branches are armed, not counted: neither repeats the gateway's green number.
  for (const id of ["reply", "timeout"]) {
    await expect(shape(page, id)).toHaveClass(/atlas-armed/);
    await expect(shape(page, id)).not.toHaveClass(/atlas-active/);
    await expect(badges(page, id).filter({ hasText: /^2$/ })).toHaveCount(0);
  }
  expect(page.__errors).toEqual([]);
});

test("tells the branch that won from the branch that was cancelled", async ({ page }) => {
  await open(page);

  // Identical visits, identical live tokens — the one difference is that the reply
  // completed on `reply` and the timer was cancelled on `timeout`.
  await expect(badges(page, "reply")).toHaveText(["1"]);
  await expect(badges(page, "reply").first()).toHaveAttribute("title", /completed here and moved on/);
  await expect(badges(page, "reply", ".cancelled")).toHaveCount(0);

  await expect(badges(page, "timeout")).toHaveText(["1"]);
  await expect(badges(page, "timeout", ".cancelled")).toHaveText("1");
  await expect(badges(page, "timeout", ".cancelled")).toHaveAttribute("title", /cancelled here/);

  await expect(badges(page, "end_reply")).toHaveText(["1"]);
  await expect(shape(page, "end_reply")).toHaveClass(/atlas-visited/);
  expect(page.__errors).toEqual([]);
});

test("counts every pool, not only the one holding the race", async ({ page }) => {
  await open(page);

  // The provider's side is on the same diagram and in the same response: two requests
  // are being worked on, one was answered and moved on.
  await expect(shape(page, "a_work")).toHaveClass(/atlas-active/);
  await expect(badges(page, "a_work")).toHaveText(["1", "2"]);
  await expect(badges(page, "a_start")).toHaveText(["3"]);
  expect(page.__errors).toEqual([]);
});

test("says once, in the legend, what an armed branch is and what each count means", async ({ page }) => {
  await open(page);

  const armed = page.locator("#legend-armed");
  await expect(armed).toBeVisible();
  await expect(armed).toContainText("armed branch of an event gateway");
  await expect(armed).toHaveAttribute("title", /counted once — on the gateway/);

  // The words are the live view's. "Passed through" is the one that had to go: it named
  // completed and cancelled as one fact, which is the misreading the split exists to end.
  const legend = page.locator(".problems");
  await expect(legend).toContainText("completed here and moved on");
  await expect(legend).toContainText("cancelled here");
  await expect(legend).toContainText("tokens here now");
  await expect(legend).toContainText("message flow");
  await expect(legend).not.toContainText("passed through");
  expect(page.__errors).toEqual([]);
});

test("leaves the armed entry out of a collaboration with no deferred choice", async ({ page }) => {
  await open(page, "__mountPlain");

  await expect(shape(page, "pa_task")).toHaveClass(/atlas-active/);
  await expect(badges(page, "pa_task")).toHaveText(["1"]);
  await expect(page.locator("#legend-armed")).toBeHidden();
  await expect(page.locator(".problems")).toContainText("cancelled here");
  expect(page.__errors).toEqual([]);
});

test("a poll rebuilds the counts instead of piling new ones on top", async ({ page }) => {
  await open(page);
  await expect(badges(page, "gw")).toHaveText(["1", "2"]);
  // This view draws its type icons once and never clears its overlays, so the counts
  // are the one thing a poll has to take down before it puts them back.
  const overlaysBefore = await page.locator(".djs-overlay").count();

  // A second reply arrives: one race left, and the reply branch has now won twice.
  await page.evaluate(() => {
    window.__runtime = {
      ...window.__runtime,
      elements: window.__runtime.elements.map((e) =>
        e.elementId === "reply" ? { ...e, tokens: 1 }
          : e.elementId === "timeout" ? { ...e, tokens: 1, terminated: 2 }
          : e.elementId === "end_reply" ? { ...e, visits: 2 }
          : e),
    };
  });
  await poll(page);

  await expect(badges(page, "gw")).toHaveText(["2", "1"]);
  await expect(badges(page, "timeout", ".cancelled")).toHaveText("2");
  for (const id of ["gw", "reply", "timeout", "a_work"]) {
    await expect(page.locator(`.djs-overlays[data-container-id="${id}"] .token-badges`)).toHaveCount(1);
  }
  // Nothing stacked, and nothing static taken down with the counts.
  expect(await page.locator(".djs-overlay").count()).toBe(overlaysBefore);
  expect(page.__errors).toEqual([]);
});

test("the count switches are the live view's own, and survive a poll", async ({ page }) => {
  await open(page);
  await expect(badges(page, "gw", ".history")).toHaveText("1");

  await toggle(page, "passed").click();
  await expect(badges(page, "gw", ".history")).toBeHidden();
  await expect(badges(page, "timeout", ".cancelled")).toBeVisible();

  // Which counts a person reads is about how they read a diagram, not about which view
  // it is in — so it is the same remembered switch the live view throws.
  expect(await page.evaluate(() => localStorage.getItem("atlas.live.badge.passed"))).toBe("0");

  // The poll rebuilds every count from scratch; a count switched off stays off.
  await poll(page);
  await expect(badges(page, "gw", ".history")).toBeHidden();
  await expect(toggle(page, "passed")).toHaveAttribute("aria-pressed", "false");
  expect(page.__errors).toEqual([]);
});
