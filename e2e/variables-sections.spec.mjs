// End-to-end coverage for the replay's Variables tab with an element selected
// (api/web/editor.js buildVarSections): what the element was handed (Input), what it
// wrote (Output), and what it could merely reach (Environment), the last grouped by the
// scope that holds a value and folded away until the reader opens it.
//
// The case behind it: one round of a multi-instance task showed its loop counter, its
// item, the body's output collection and every process variable as one list, with the
// same "BEWERTEN" chip on a round's local and on the body's collection — and an
// Input/Output switch that meant "before/after", where the diagram's card beside it
// meant "mapped in/written" by the same two words.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/variables-sections-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
});

const tab = (page) => page.locator("#tab-variables");
const rowOf = (page, name) =>
  tab(page).locator(".vt tbody tr", { has: page.locator("td.c-name", { hasText: new RegExp(`^${name}`) }) });

async function selectRound(page, eik) {
  // A loop's rounds sit under its activity in the history, folded until it is opened.
  const row = page.locator(`#history-list .ops-hrow[data-eik="${eik}"]`);
  if (!(await row.isVisible())) {
    const toggle = page.locator("#history-list .ops-mi-toggle").first();
    if ((await toggle.getAttribute("aria-expanded")) !== "true") await toggle.click();
  }
  await row.click();
  await page.locator("#rp-tabs button[data-tab='variables']").click();
  await expect(tab(page).locator(".v-sec-in")).toBeVisible();
}

test("a round's variables are sorted into what it was handed, what it wrote and what it could reach", async ({ page }) => {
  await selectRound(page, "1003");

  // The three sections, in that order.
  const heads = tab(page).locator(".vt tbody tr.v-ghead");
  await expect(heads).toHaveCount(3);
  await expect(heads.nth(0)).toHaveClass(/v-sec-in/);
  await expect(heads.nth(1)).toHaveClass(/v-sec-out/);
  await expect(heads.nth(2)).toHaveClass(/v-sec-env/);

  // Input is the round's own scope: its counter and its item, nothing of the process.
  const order = await tab(page).locator(".vt tbody tr").evaluateAll((rows) =>
    rows.map((r) => r.classList.contains("v-ghead") ? `#${[...r.classList].find((c) => c.startsWith("v-sec-"))}`
      : (r.querySelector("td.c-name") || {}).textContent?.trim().split(/\s/)[0] || ""));
  const inSec = order.slice(order.indexOf("#v-sec-in") + 1, order.indexOf("#v-sec-out")).filter(Boolean);
  expect(inSec).toEqual(["loopCounter", "pos"]);

  // Output is what the round itself wrote: a new value says so, a rewritten one says
  // what it was.
  // The round's answer lives in its own scope until the body collects it; it is still
  // what the round produced.
  await expect(rowOf(page, "bewertung").locator(".c-new")).toBeVisible();
  await expect(rowOf(page, "szenario").locator(".v-was")).toContainText("manuell");
  await expect(rowOf(page, "szenario").locator(".c-val")).toHaveText("geprueft");

  // The environment comes folded: its rows exist and are not shown, its count is.
  await expect(tab(page).locator(".v-env-toggle")).toHaveAttribute("aria-expanded", "false");
  await expect(rowOf(page, "positionen")).toBeHidden();
  await expect(tab(page).locator(".v-sec-env .v-gcount")).toHaveText("6");
  expect(page.__errors, "page errors").toEqual([]);
});

test("the environment groups what is in reach by scope, innermost first, and leaves out what is not", async ({ page }) => {
  await selectRound(page, "1003");
  await tab(page).locator(".v-env-toggle").click();
  await expect(rowOf(page, "positionen")).toBeVisible();

  // Body, enclosing subprocess, process, constants: the order a name is looked up in.
  const groups = tab(page).locator(".vt tbody tr.v-gsub");
  await expect(groups).toHaveCount(4);
  await expect(groups.nth(0)).toContainText("Position bewerten");
  await expect(groups.nth(1)).toContainText("Prüfung");
  const under = async (i) => tab(page).locator(".vt tbody tr").evaluateAll((rows, idx) => {
    const subs = rows.filter((r) => r.classList.contains("v-gsub"));
    const out = [];
    for (let r = subs[idx].nextElementSibling; r && !r.hasAttribute("data-dt-group"); r = r.nextElementSibling) {
      const n = r.querySelector("td.c-name");
      if (n && !r.classList.contains("v-struct")) out.push(n.textContent.trim().split(/\s/)[0]);
    }
    return out;
  }, i);
  expect(await under(0)).toEqual(["bewertungen"]);
  expect(await under(1)).toEqual(["limite"]);
  expect(await under(2)).toEqual(["notiz", "positionen"]);
  expect(await under(3)).toEqual(["MAX_POSITIONEN", "server"]);

  // A subprocess running beside the task shares the snapshot and is out of its reach.
  await expect(rowOf(page, "fremd")).toHaveCount(0);
  // What only appeared while the round ran says so. On an instance recorded before each
  // round's element was kept, the body's collection reads unfilled until the loop is
  // done, and says so rather than read as rounds that produced nothing.
  await expect(rowOf(page, "notiz").locator(".c-late")).toBeVisible();
  await expect(rowOf(page, "bewertungen").locator(".v-coll")).toHaveCount(1);
  // A constant says whose it is.
  await expect(rowOf(page, "MAX_POSITIONEN").locator(".c-const")).toBeVisible();
  await expect(rowOf(page, "server").locator(".c-const")).toBeVisible();

  // The reader's choice to open it holds for the next element they pick.
  await selectRound(page, "1004");
  await expect(tab(page).locator(".v-env-toggle")).toHaveAttribute("aria-expanded", "true");
  expect(page.__errors, "page errors").toEqual([]);
});

test("a round still running says it has no output yet", async ({ page }) => {
  await selectRound(page, "1004");
  const note = tab(page).locator(".vt tbody tr.v-sec-out + tr.v-gnote");
  await expect(note).toBeVisible();
  await expect(tab(page).locator(".v-was:not(.v-coll)")).toHaveCount(0);
  expect(page.__errors, "page errors").toEqual([]);
});

test("a name the filter finds in the folded environment is shown", async ({ page }) => {
  await selectRound(page, "1003");
  await expect(rowOf(page, "positionen")).toBeHidden();
  await tab(page).locator("#v-filter").fill("positionen");
  await expect(rowOf(page, "positionen")).toBeVisible();
  // ...and only that: the sections it emptied say so rather than vanish.
  await expect(rowOf(page, "loopCounter")).toHaveCount(0);
  await tab(page).locator("#v-filter").fill("");
  await expect(rowOf(page, "positionen")).toBeHidden();
  expect(page.__errors, "page errors").toEqual([]);
});

test("sorting the table keeps every row in its own section", async ({ page }) => {
  await selectRound(page, "1003");
  await tab(page).locator(".v-env-toggle").click();
  const name = tab(page).locator(".vt thead th.dt-sortable").first();
  await name.click();
  await name.click(); // descending
  const sectionOf = await tab(page).locator(".vt tbody tr").evaluateAll((rows) => {
    let sec = "";
    const out = {};
    for (const r of rows) {
      const head = [...r.classList].find((c) => c.startsWith("v-sec-"));
      if (head) { sec = head; continue; }
      const n = r.querySelector("td.c-name");
      if (n && !r.hasAttribute("data-dt-group") && !r.classList.contains("v-struct")) out[n.textContent.trim().split(/\s/)[0]] = sec;
    }
    return out;
  });
  expect(sectionOf.loopCounter).toBe("v-sec-in");
  expect(sectionOf.pos).toBe("v-sec-in");
  expect(sectionOf.bewertung).toBe("v-sec-out");
  expect(sectionOf.szenario).toBe("v-sec-out");
  expect(sectionOf.positionen).toBe("v-sec-env");
  expect(sectionOf.server).toBe("v-sec-env");
  expect(page.__errors, "page errors").toEqual([]);
});

test("with nothing selected the instance's variables stay one list, each scope named by its kind", async ({ page }) => {
  await page.locator("#rp-tabs button[data-tab='variables']").click();
  // Walk the replay to the frame where the first round has started; nothing is selected.
  for (let i = 0; i < 5; i++) await page.locator("#step-fwd").click();
  await expect(rowOf(page, "bewertungen")).toBeVisible();
  await expect(tab(page).locator(".vt tbody tr.v-ghead")).toHaveCount(0);
  // The body's collection names its scope's kind beside the element id the round
  // shares — the id alone was what made a round's local and the body's look alike.
  const chip = rowOf(page, "bewertungen").locator(".c-scope");
  await expect(chip).toContainText("Bewerten_1");
  await expect(chip).not.toHaveText("Bewerten_1");
  await expect(rowOf(page, "limite").locator(".c-scope")).not.toHaveText(await chip.innerText());
  expect(page.__errors, "page errors").toEqual([]);
});

// Where each round's element is on record, the body's collection fills round by round:
// the round that answered shows the list with its answer at completion, the next round
// starts from it, and nothing explains an unfilled list away.
test("a recorded collection fills round by round and needs no explanation", async ({ page }) => {
  await page.goto("/variables-sections-harness.html?recorded=1");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  await selectRound(page, "1003");
  await tab(page).locator(".v-env-toggle").click();
  await expect(rowOf(page, "bewertungen").locator(".v-was")).toContainText('["gut",null]');
  await expect(rowOf(page, "bewertungen").locator(".v-coll")).toHaveCount(0);
  await selectRound(page, "1004");
  await expect(rowOf(page, "bewertungen").locator(".v-open .peek")).toContainText('"gut"');
  await expect(rowOf(page, "bewertungen").locator(".v-coll")).toHaveCount(0);
  expect(page.__errors, "page errors").toEqual([]);
});
