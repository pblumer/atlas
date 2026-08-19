// End-to-end coverage for incidents on the diagram (ADR-0150): the Operations live
// view and the single-instance replay must show a *stuck* token as stuck — marked on
// the element, listed beside the diagram, and resolvable without leaving the view.
// Driven through the real vendored bpmn-js and the real editor.js mounts.
import { test, expect } from "@playwright/test";

const open = async (page) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/incidents-ops-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
};

// resolveVia clicks a Resolve button, grants `retries` in the dialog, and confirms.
const resolveVia = async (page, button, retries) => {
  await button.click();
  const modal = page.locator(".modal-ov");
  await expect(modal).toBeVisible();
  await modal.locator("#inc-retries").fill(String(retries));
  await modal.locator("[data-inc-go]").click();
  await expect(modal).toHaveCount(0);
};

test.describe("live view", () => {
  test("marks the stuck element, counts the incidents, and lists them", async ({ page }) => {
    await open(page);
    await page.evaluate(() => window.__mountLive());

    // The element the tokens are parked on is outlined and badged — the token markers
    // alone said nothing about the instances being blocked.
    const stuck = page.locator('#canvas g[data-element-id="Task_pay"]');
    await expect(stuck).toHaveClass(/atlas-incident/);
    const badge = page.locator(".incident-badge");
    await expect(badge).toHaveText(/2 incidents/);
    await expect(badge).toHaveAttribute("title", /payment gateway refused: 503/);
    // Only the stuck element: the start event ran cleanly.
    await expect(page.locator('#canvas g[data-element-id="Start_1"]')).not.toHaveClass(/atlas-incident/);

    // The toolbar answers "is anything in this version stuck?" without a click.
    await expect(page.locator("#inc-pill")).toBeVisible();
    await expect(page.locator("#inc-count")).toHaveText("2");

    // Both instances' incidents are listed, each linking to that instance's replay.
    const cards = page.locator("#var-panel .inc-card");
    await expect(cards).toHaveCount(2);
    await expect(cards.first()).toContainText("Zahlung auslösen"); // the element's label, not its id
    await expect(cards.first()).toContainText("payment gateway refused: 503");
    await expect(cards.first().locator(".inc-inst")).toHaveAttribute(
      "href", `#/operations/i/${await page.evaluate(() => window.__STUCK)}`);
    // …and each instance row in the overview carries its own count.
    await expect(page.locator("#var-panel .inc-chip")).toHaveCount(2);
    expect(page.__errors).toEqual([]);
  });

  test("the badge narrows the panel to that element, and back", async ({ page }) => {
    await open(page);
    await page.evaluate(() => window.__mountLive());
    await expect(page.locator("#var-panel .inc-card")).toHaveCount(2);

    await page.locator(".incident-badge").click();
    await expect(page.locator("#var-panel .vp-inc-head")).toContainText("Zahlung auslösen");
    // Both incidents sit on the same element, so narrowing keeps both — and offers
    // the way back to everything in view.
    await expect(page.locator("#var-panel .inc-back")).toBeVisible();
    await page.locator("#var-panel .inc-back").click();
    await expect(page.locator("#var-panel .vp-inc-head")).toContainText("2 incidents");
    expect(page.__errors).toEqual([]);
  });

  test("resolves an incident in place, and the diagram catches up", async ({ page }) => {
    await open(page);
    await page.evaluate(() => window.__mountLive());
    await expect(page.locator("#var-panel .inc-card")).toHaveCount(2);

    await resolveVia(page, page.locator("#var-panel .inc-card [data-resolve-inc]").first(), 3);

    // The retry budget the operator typed reached the API…
    expect(await page.evaluate(() => window.__resolved)).toEqual([{ key: "1001", retries: 3 }]);
    // …and the view re-polled: one incident left, on the same element.
    await expect(page.locator("#var-panel .inc-card")).toHaveCount(1);
    await expect(page.locator("#inc-count")).toHaveText("1");
    // A single incident on the element drops the count from the badge.
    await expect(page.locator(".incident-badge")).toHaveText("⚠ incident");
    expect(page.__errors).toEqual([]);
  });

  test("a single selected instance sees only its own incident", async ({ page }) => {
    await open(page);
    await page.evaluate(() => window.__mountLive(window.__STUCK));

    await expect(page.locator("#inc-count")).toHaveText("1");
    await expect(page.locator("#var-panel .inc-card")).toHaveCount(1);
    // Scoped to one instance the card drops the instance link — the panel is already
    // about that instance.
    await expect(page.locator("#var-panel .inc-card .inc-inst")).toHaveCount(0);
    expect(page.__errors).toEqual([]);
  });
});

test.describe("instance replay", () => {
  test("flags the stuck step and resolves it from the Details panel", async ({ page }) => {
    await open(page);
    await page.evaluate(() => window.__mountReplay());
    await expect(page.locator("#history-list .ops-hrow").first()).toBeVisible();

    // The header says the instance is stuck, beside its state.
    await expect(page.locator("#m-inc-wrap")).toBeVisible();
    await expect(page.locator("#m-inc-n")).toHaveText("1");
    // The history row for the parked element instance is flagged, the earlier one isn't.
    await expect(page.locator('#history-list .ops-hrow[data-eik="1001"]')).toHaveClass(/inc/);
    await expect(page.locator('#history-list .ops-hrow[data-eik="1000"]')).not.toHaveClass(/inc/);
    // And the element keeps its incident outline wherever the playhead sits.
    await expect(page.locator('#canvas g[data-element-id="Task_pay"]')).toHaveClass(/atlas-incident/);
    await page.locator("#step-back").click();
    await expect(page.locator('#canvas g[data-element-id="Task_pay"]')).toHaveClass(/atlas-incident/);

    // Clicking the badge selects the stuck element and opens its Details panel.
    await page.locator(".incident-badge").click();
    const details = page.locator("#tab-details");
    await expect(details.locator(".ops-inc")).toContainText("This element is stuck");
    await expect(details.locator(".inc-card-msg")).toHaveText("payment gateway refused: 503");

    await resolveVia(page, details.locator("[data-resolve-inc]"), 1);
    expect(await page.evaluate(() => window.__resolved)).toEqual([{ key: "1001", retries: 1 }]);
    await expect(page.locator("#m-inc-wrap")).toBeHidden();
    await expect(page.locator(".incident-badge")).toHaveCount(0);
    await expect(page.locator('#canvas g[data-element-id="Task_pay"]')).not.toHaveClass(/atlas-incident/);
    expect(page.__errors).toEqual([]);
  });

  test("with nothing selected the panel still reaches the instance's incidents", async ({ page }) => {
    await open(page);
    await page.evaluate(() => window.__mountReplay());
    await expect(page.locator("#tab-details .ops-inc")).toContainText("1 incident");
    await expect(page.locator("#tab-details .inc-card-name")).toHaveText("Zahlung auslösen");
    expect(page.__errors).toEqual([]);
  });
});
