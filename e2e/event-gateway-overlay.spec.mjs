// End-to-end coverage for how the live overlay draws a deferred choice and a cancelled
// token (ADR-0110, ADR-draft-overlay-cancelled-tokens). Both are about the same failure:
// a number on the diagram that an operator reads as one thing while it means another.
//
// An event-based gateway arms every branch at once, so the engine parks a token on each
// of them and none on the gateway. Drawn one-for-one that says two things that are not
// true — that the race is two waits, and (because both branches always carry the same
// count) that both events arrived equally often. The overlay therefore counts the race
// once, on the gateway, marks the branches armed, and separates the tokens that were
// cancelled on a branch from the ones that completed there.
import { test, expect } from "@playwright/test";

const open = async (page) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/event-gateway-overlay-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mountLive());
};

const badges = (page, id, cls = "") =>
  page.locator(`.djs-overlays[data-container-id="${id}"] .token-badge${cls}`);
const shape = (page, id) => page.locator(`#canvas g[data-element-id="${id}"]`);

test("counts the race on the gateway, not once per armed branch", async ({ page }) => {
  await open(page);

  // The gateway holds the wait: two instances are still racing, one race is decided.
  await expect(shape(page, "gw")).toHaveClass(/atlas-active/);
  await expect(badges(page, "gw")).toHaveText(["1", "2"]);
  await expect(badges(page, "gw").nth(1)).toHaveAttribute("title", /2 live token/);

  // The branches are armed, not counted: no green number is repeated on either of them.
  for (const id of ["reply", "timeout"]) {
    await expect(shape(page, id)).toHaveClass(/atlas-armed/);
    const armed = badges(page, id, ".armed");
    await expect(armed).toHaveText("armed");
    await expect(armed).toHaveAttribute("title", /nächstes Ereignis/);
  }
  expect(page.__errors).toEqual([]);
});

test("tells the branch that won from the branch that was cancelled", async ({ page }) => {
  await open(page);

  // Identical visits, identical live tokens — the only difference between the two
  // branches is that one token completed on `reply` and one was cancelled on `timeout`.
  // Gray then armed on the winner; nothing gray, an amber cancelled count on the loser.
  await expect(badges(page, "reply")).toHaveText(["1", "armed"]);
  await expect(badges(page, "reply").first()).toHaveAttribute("title", /completed here and moved on/);
  await expect(badges(page, "reply", ".cancelled")).toHaveCount(0);

  await expect(badges(page, "timeout")).toHaveText(["1", "armed"]);
  const cancelled = badges(page, "timeout", ".cancelled");
  await expect(cancelled).toHaveText("1");
  await expect(cancelled).toHaveAttribute("title", /cancelled here/);

  // A plain element is unaffected: the end event the winner reached keeps its history.
  await expect(badges(page, "end_reply")).toHaveText(["1"]);
  await expect(shape(page, "end_reply")).toHaveClass(/atlas-visited/);
  expect(page.__errors).toEqual([]);
});
