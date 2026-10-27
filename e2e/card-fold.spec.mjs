// Every topic of a Console page folds away under its heading, the same way on every
// page, and the browser remembers the choice (fold.js, ADR-0505).
//
// The harness drives fold.js over cards shaped like the pages' own; the last tests
// boot the real Console and look at a page nobody wrote any folding for, because
// "every page" is only true if the router gives it to pages that never asked.
import { test, expect } from "@playwright/test";

const open = async (page, query = "") => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/card-fold-harness.html" + query);
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
};

const toggle = (page, card) => page.locator(`#${card} [data-fold-toggle]`);
const settle = (page) => page.evaluate(() => new Promise((r) => setTimeout(r, 0)));

test("every topic gets the same toggle in its heading, and nothing else does", async ({ page }) => {
  await open(page);
  for (const card of ["c-plain", "c-actions", "c-link"]) {
    await expect(toggle(page, card)).toHaveCount(1);
    await expect(toggle(page, card)).toHaveClass(/section-toggle/);
    await expect(toggle(page, card)).toHaveAttribute("aria-expanded", "true");
  }
  // The heading's words are the button, so the whole title is what one clicks.
  await expect(toggle(page, "c-plain")).toHaveText("Readiness");
  // A page title, a form, a card that opted out and a heading with nothing under it
  // are not topics.
  for (const card of ["c-title", "c-form", "c-optout", "c-alone"]) {
    await expect(page.locator(`#${card} .section-toggle`)).toHaveCount(0);
  }
  // A section that folds itself keeps its own toggle and gets no second one.
  await expect(page.locator("#c-section .section-toggle")).toHaveCount(1);
  await expect(toggle(page, "c-section")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("a folded topic hides its body and keeps its heading row, actions included", async ({ page }) => {
  await open(page);
  await toggle(page, "c-plain").click();
  await expect(toggle(page, "c-plain")).toHaveAttribute("aria-expanded", "false");
  await expect(page.locator("#c-plain-body")).toBeHidden();
  await expect(page.locator("#c-plain h2")).toBeVisible();

  await toggle(page, "c-actions").click();
  await expect(page.locator("#c-actions p")).toBeHidden();
  await expect(page.locator("#c-actions-new")).toBeVisible();
  // The card has no padding of its own; folded, its heading row pads its own bottom,
  // so the title does not sit on the card's edge.
  await expect(page.locator("#c-actions")).toHaveClass(/fold-flush/);
  await expect(page.locator("#c-plain")).not.toHaveClass(/fold-flush/);
  const pad = await page.locator("#c-actions > .fold-head").evaluate((el) => getComputedStyle(el).paddingBottom);
  expect(pad).toBe("16px");

  await toggle(page, "c-plain").click();
  await expect(page.locator("#c-plain-body")).toBeVisible();
  await expect(toggle(page, "c-plain")).toHaveAttribute("aria-expanded", "true");
  expect(page.__errors).toEqual([]);
});

test("the browser remembers each topic, per page", async ({ page }) => {
  await open(page);
  await toggle(page, "c-plain").click();
  expect(await page.evaluate(() => localStorage.getItem("atlas.sec.harness/c-plain"))).toBe("0");

  await page.reload();
  await page.waitForFunction(() => window.__ready === true);
  await expect(page.locator("#c-plain-body")).toBeHidden();
  await expect(toggle(page, "c-plain")).toHaveAttribute("aria-expanded", "false");
  // Only the topic that was folded.
  await expect(page.locator("#c-actions p")).toBeVisible();

  // Another page's topic of the same name is its own.
  await open(page, "?page=elsewhere");
  await expect(page.locator("#c-plain-body")).toBeVisible();
  expect(page.__errors).toEqual([]);
});

test("a control in a folded heading opens the topic before it acts", async ({ page }) => {
  await open(page);
  await toggle(page, "c-actions").click();
  await expect(page.locator("#c-actions p")).toBeHidden();
  await page.click("#c-actions-new");
  await expect(page.locator("#c-actions-form")).toBeVisible();
  await expect(toggle(page, "c-actions")).toHaveAttribute("aria-expanded", "true");
  expect(page.__errors).toEqual([]);
});

test("a topic redrawn in place keeps its toggle and its state", async ({ page }) => {
  await open(page);
  await toggle(page, "c-plain").click();
  // A page that saves and redraws a card writes its contents anew.
  await page.evaluate(() => {
    document.getElementById("c-plain").innerHTML = `<h2>Readiness</h2><p>redrawn</p><div id="c-plain-new">new body</div>`;
  });
  await settle(page);
  await expect(toggle(page, "c-plain")).toHaveCount(1);
  await expect(toggle(page, "c-plain")).toHaveAttribute("aria-expanded", "false");
  await expect(page.locator("#c-plain-new")).toBeHidden();

  // Redrawn into something that is no topic, nothing stays hidden behind a heading
  // that is no longer there.
  await page.evaluate(() => {
    document.getElementById("c-plain").innerHTML = `<p id="c-plain-loading">Loading…</p>`;
  });
  await settle(page);
  await expect(page.locator("#c-plain-loading")).toBeVisible();
  expect(page.__errors).toEqual([]);
});

test("a heading that carries a link keeps it, with the caret beside the words", async ({ page }) => {
  await open(page);
  const btn = toggle(page, "c-link");
  await expect(btn).toHaveAttribute("aria-label", "Linked elsewhere");
  await expect(page.locator("#c-link [data-fold-toggle] a")).toHaveCount(0);
  await expect(page.locator("#c-link-a")).toBeVisible();
  await btn.click();
  await expect(page.locator("#c-link-body")).toBeHidden();
  expect(page.__errors).toEqual([]);
});

test("a page is the route's first two segments", async ({ page }) => {
  await open(page);
  const pages = await page.evaluate(() => [
    "#/console/security", "#/console/security?x=1", "#/modeler/p/app-1", "#/modeler/p/app-2",
    "#/console", "", "#/",
  ].map((h) => window.__fold.pageOf(h)));
  expect(pages).toEqual(["console/security", "console/security", "modeler/p", "modeler/p", "console", "console", "console"]);
});

// The real Console: the Security page was written without a word about folding.

function installSecurityMock(page) {
  page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    if (path === "/api/v1/security/readiness") {
      return route.fulfill({ json: { workers: [{ worker: "clio-prod", kind: "clio", governed: false, governable: false, credentialKind: "worker", credentialRef: "clio_token" }] } });
    }
    if (path === "/api/v1/security/linking/microsoft") return route.fulfill({ json: { source: "none", available: true, active: false } });
    return route.fulfill({ json: [] });
  });
}

async function bootConsole(page, hash) {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  installSecurityMock(page);
  await page.goto("/index.html" + hash);
  await page.waitForFunction(() => document.querySelector("#security .card h2"), null, { timeout: 15000 });
}

test("the Console's pages fold their topics without asking, and remember them", async ({ page }) => {
  await bootConsole(page, "#/console/security");
  const topics = page.locator("#security > .card > :first-child [data-fold-toggle], #security > .card > h2 > [data-fold-toggle]");
  await expect(topics).toHaveCount(5);
  // The page's title is not a topic.
  await expect(page.locator("#security h1 [data-fold-toggle]")).toHaveCount(0);

  const readiness = page.locator("#security .card").filter({ has: page.locator("#sec-readiness") });
  await expect(readiness.locator("#sec-readiness table")).toBeVisible();
  await readiness.locator("[data-fold-toggle]").click();
  await expect(readiness.locator("#sec-readiness")).toBeHidden();

  await page.reload();
  await page.waitForFunction(() => document.querySelector("#security [data-fold-toggle]"), null, { timeout: 15000 });
  await expect(readiness.locator("#sec-readiness")).toBeHidden();
  await expect(readiness.locator("[data-fold-toggle]")).toHaveAttribute("aria-expanded", "false");
  // Folding one topic folded no other.
  await expect(page.locator("#sec-policies table")).toBeVisible();
  expect(page.__errors).toEqual([]);
});
