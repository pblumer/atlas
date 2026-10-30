// End-to-end coverage for folding the cards of Operations › Workers (ADR-0505): each
// card is headed by its <h2>, so it folds under it like every topic of the Console, and
// the browser remembers each one. A card that was empty when the page opened — no
// breaker held yet — folds too once a Refresh fills it.
import { test, expect } from "@playwright/test";

const CARDS = ["wk-collisions", "wk-workers", "wk-gaps", "wk-supervised"];

function installMock(page) {
  let calls = 0;
  page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path.endsWith("/api/v1/workers")) {
      calls++;
      return route.fulfill({
        json: {
          types: [{ type: "io.atlas.rest", index: 3, builtIn: true, leasable: true, parked: 0, inFlight: 0, incidents: 0, processes: [] }],
          workers: [{ worker: "w-1", types: { "io.atlas.rest": 1 }, connectors: [], leased: 0, pulled: 1, completed: 1, failed: 0, lastSeen: Date.now() * 1e6 }],
          jobTypeCollisions: [{ name: "billing", index: 7, nowMeans: "io.atlas.mail.send" }],
          unservedConnectors: [{ name: "ucc_entra", jobType: "io.atlas.entra", processes: [{ processDefKey: 1, processId: "p", name: "Lizenz", version: 8 }] }],
          supervised: [{ id: "ad", kinds: ["ad"], state: "running", pid: 93, starts: 1, log: [] }],
          // Nothing held on the first answer; a target held by the time of the Refresh.
          breakers: calls > 1 ? [{ jobType: "io.atlas.mail.send", connector: "smtp", state: "open", trippedAt: 0, probeAt: 0, reason: "refused" }] : [],
        },
      });
    }
    return route.fulfill({ json: [] });
  });
}

async function openWorkers(page) {
  await page.evaluate(() => { location.hash = "#/operations/workers"; });
  await expect(page.locator("#wk-supervised h2")).toBeVisible();
}

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  installMock(page);
  await page.goto("/index.html");
  await page.waitForFunction(() => document.querySelector("#view")?.children.length > 0, null, { timeout: 15000 });
});

test("every card of Operations › Workers folds under its heading, and stays folded", async ({ page }) => {
  await openWorkers(page);
  for (const id of CARDS) {
    const card = page.locator(`#${id}`);
    const toggle = card.locator("[data-fold-toggle]");
    await expect(toggle).toHaveAttribute("aria-expanded", "true");
    await expect(card.locator("table")).toBeVisible();
    await toggle.click();
    await expect(toggle).toHaveAttribute("aria-expanded", "false");
    await expect(card.locator("table")).toBeHidden();
    // The heading row stays, with what it says about the card's contents.
    await expect(card.locator(".wk-head")).toBeVisible();
  }

  await page.reload();
  await page.waitForFunction(() => document.querySelector("#view")?.children.length > 0, null, { timeout: 15000 });
  await openWorkers(page);
  for (const id of CARDS) {
    await expect(page.locator(`#${id}`)).toHaveAttribute("data-folded", "");
    await expect(page.locator(`#${id} table`)).toBeHidden();
  }
  expect(page.__errors).toEqual([]);
});

test("a card that a Refresh fills folds like the others", async ({ page }) => {
  await openWorkers(page);
  await expect(page.locator("#wk-breakers")).toBeHidden();
  await page.click("#refresh");
  const held = page.locator("#wk-breakers");
  await expect(held).toContainText("Held back");
  await held.locator("[data-fold-toggle]").click();
  await expect(held.locator("table")).toBeHidden();
  // A further Refresh redraws the card and keeps the fold.
  await page.click("#refresh");
  await expect(held.locator("[data-fold-toggle]")).toHaveAttribute("aria-expanded", "false");
  await expect(held.locator("table")).toBeHidden();
  expect(page.__errors).toEqual([]);
});
