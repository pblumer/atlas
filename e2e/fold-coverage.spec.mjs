// A guard for the pages nobody has written yet (ADR-0505): every page of the Console's
// menu is opened, and what it draws is held to the rules of folding.
//
//   - A page with two or more section headings (<h2>) folds each of them: each carries
//     the caret (fold.js, sectionHead). A page with one may leave it unfolded.
//   - A card is headed by its <h2>, not by a bold word or an <h3>: those fold nothing.
//     A card that holds a form may be headed by an <h3> (the form's heading, ADR-0505).
//   - A <details> the Console draws is marked fold-sub, so it shows the topics' caret.
//
// The pages are the menu's own (`route: "#/…"` in app.js), read when the test runs, so
// a page added to the menu is guarded without anybody adding it here. Each is opened
// with every answer empty: what a page draws only around data is held by its own spec
// (section-folds.spec.mjs and the like), and a <details> inside a list by the source
// guard in api/foldsub_internal_test.go. A heading that must not fold says so with
// class no-fold on itself or on what holds it; a page's title is its <h1>, or on a page
// with none, its first heading.
import { test, expect } from "@playwright/test";
import { readFileSync } from "node:fs";

const APP = readFileSync(new URL("../api/web/app.js", import.meta.url), "utf8");
const ROUTES = [...new Set([...APP.matchAll(/route: "(#\/[^"]+)"/g)].map((m) => m[1]))];

// Pages that cannot be held to the rules with empty answers, and why. Each is a
// canvas, not a column of topics, so it has nothing these rules are about.
const NOT_TOPICS = new Map([
  ["#/panorama/starmap", "a canvas with a side panel; it draws nothing without a model"],
]);

// examine reads what the page in view draws against the rules above. It runs in the
// page, so it uses nothing from this module.
function examine() {
  const view = document.getElementById("view");
  const shown = (el) => !!(el.offsetWidth || el.offsetHeight || el.getClientRects().length);
  const text = (el) => el.textContent.replace(/\s+/g, " ").trim().slice(0, 60);
  const exempt = (el) => !!el.closest(".modal, .no-fold");
  const out = { broken: /Something went wrong/.test(view.textContent), problems: [] };

  // The page's title is its <h1>; a page without one is titled by its first heading.
  const headings = [...view.querySelectorAll("h2")].filter((h) => shown(h) && !exempt(h));
  const sections = view.querySelector("h1") ? headings : headings.slice(1);
  if (sections.length >= 2) {
    for (const h of sections) {
      if (!h.querySelector(".section-toggle")) out.problems.push(`the section "${text(h)}" does not fold`);
    }
  }

  // A card whose heading is a bold word or an <h3> is a section fold.js cannot see.
  for (const card of view.querySelectorAll(".card")) {
    if (exempt(card) || !shown(card) || card.children.length < 2) continue;
    const first = card.firstElementChild;
    const lead = /^(H3|H4|B|STRONG)$/.test(first.tagName) ? first
      : first.firstElementChild && /^(H3|H4|B|STRONG)$/.test(first.firstElementChild.tagName)
        ? first.firstElementChild : null;
    if (!lead) continue;
    if (lead.tagName !== "B" && lead.tagName !== "STRONG" && card.querySelector("form, input, select, textarea")) continue;
    out.problems.push(`the card "${text(lead)}" is headed by <${lead.tagName.toLowerCase()}>, not by its <h2>`);
  }

  // A fold inside a topic draws the topics' caret; a library's own (form-js) is not ours.
  for (const d of view.querySelectorAll("details:not(.fold-sub)")) {
    if (exempt(d) || d.closest(".fjs-container, .fjs-form")) continue;
    out.problems.push(`a <details> ("${text(d.querySelector("summary") || d)}") is not marked fold-sub`);
  }
  return out;
}

test("the menu is read, so a new page is guarded without being listed here", () => {
  expect(ROUTES.length).toBeGreaterThan(30);
  expect(ROUTES).toContain("#/operations/workers");
});

test("every page of the menu folds its sections the same way", async ({ page }) => {
  test.setTimeout(240000);
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/auth/me")) return route.fulfill({ json: { authEnabled: false, user: null } });
    return route.fulfill({ json: [] });
  });
  await page.goto("/index.html");
  await page.waitForFunction(() => document.querySelector("#view")?.children.length > 0, null, { timeout: 15000 });

  const findings = [];
  let examined = 0;
  for (const hash of ROUTES) {
    if (NOT_TOPICS.has(hash)) continue;
    // What the last page drew is marked, so the next is read only once it has replaced
    // it: read too early, the last page would pass for this one.
    await page.evaluate(() => {
      for (const c of document.getElementById("view").children) c.setAttribute("data-guard-stale", "");
    });
    await page.evaluate((h) => { location.hash = h; }, hash);
    await page.waitForFunction(() => !document.querySelector("#view > [data-guard-stale]"), null, { timeout: 5000 })
      .catch(() => { /* a page that keeps its element is read as it stands */ });
    // The route folds the view once the page has drawn, and a page may fill a card
    // after its first paint; a finding is only one if it is still there once the page
    // has had time to settle, so a slow machine does not read a page mid-paint.
    await page.waitForTimeout(200);
    let seen = await page.evaluate(examine);
    for (const until = Date.now() + 5000; (seen.broken || seen.problems.length) && Date.now() < until;) {
      await page.waitForTimeout(250);
      seen = await page.evaluate(examine);
    }
    examined++;
    // A page that fails on empty answers would pass by drawing nothing; it is a finding
    // too, because then nothing here looked at it.
    if (seen.broken) findings.push(`${hash}: the page did not draw with empty answers, so it was not examined`);
    for (const p of seen.problems) findings.push(`${hash}: ${p}`);
  }
  expect(examined).toBeGreaterThan(30);
  expect(findings).toEqual([]);
  expect(errors).toEqual([]);
});
