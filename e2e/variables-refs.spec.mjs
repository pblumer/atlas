// End-to-end coverage for references in the replay's Variables tab (api/web/editor.js,
// api/web/varrefs.js): a value that is a person's, a process's or a product's id is
// named beside it, explained on hover, and opens what it names where the reader may go.
//
// A process carries people as principal ids and never as names (ADR-0314), so the
// history holds nothing an erasure would have to reach. The tab printed those ids as
// stored, and `orderer = usr_3b0e…` had to be looked up somewhere else. The name is read
// when the tab is drawn, which is the moment ADR-0314 names for it, and the value itself
// is left exactly as it was: it is still what the row shows first and what it copies.
import { test, expect } from "@playwright/test";

const ORDERER = "usr_3b0ed4783d940eac82171edf";
const GONE = "usr_0123456789abcdef01234567";

const open = async (page, opts) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/variables-refs-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate((o) => window.__mount(o), opts || {});
  await page.locator('#history-list .ops-hrow[data-eik="1001"]').click();
  await page.locator("#rp-tabs button[data-tab='variables']").click();
  await expect(page.locator("#tab-variables .vt tbody tr").first()).toBeVisible();
};

const row = (page, name) =>
  page.locator("#tab-variables .vt tbody tr", { has: page.locator("td.c-name", { hasText: new RegExp(`^${name}`) }) });
const ref = (page, name) => row(page, name).locator(".c-ref");

test("a person's id is named, and the id stays what the row shows and copies", async ({ page }) => {
  await open(page);
  await expect(ref(page, "orderer")).toContainText("Patrick Blumer");
  // The stored value comes first and is unchanged: the name is beside it, not instead.
  await expect(row(page, "orderer").locator(".c-val")).toHaveText(ORDERER);
  await expect(row(page, "orderer").locator(".vcopy")).toHaveAttribute("data-copy", ORDERER);
  // There is nowhere a person is opened from here, so a person is named, not linked.
  await expect(ref(page, "orderer")).not.toHaveAttribute("href", /.*/);
  expect(page.__errors).toEqual([]);
});

test("hovering a reference says what it is and that the name is read now", async ({ page }) => {
  await open(page);
  const pop = page.locator("#ref-pop");
  await expect(pop).toBeHidden();
  await ref(page, "orderer").hover();
  await expect(pop).toBeVisible();
  await expect(pop).toContainText("Patrick Blumer");
  await expect(pop).toContainText("Person");
  await expect(pop).toContainText(ORDERER);
  // A replay shows a past step; the name is today's. Saying so keeps a renamed account
  // from reading as what the process saw.
  await expect(pop).toContainText(/now/i);
  await page.mouse.move(0, 0);
  await expect(pop).toBeHidden();
  expect(page.__errors).toEqual([]);
});

test("a reference explains itself to the keyboard as well", async ({ page }) => {
  await open(page);
  await ref(page, "orderer").focus();
  await expect(page.locator("#ref-pop")).toBeVisible();
  await expect(page.locator("#ref-pop")).toContainText("Patrick Blumer");
  // Tab moves on to the row's copy button, which explains nothing.
  await page.keyboard.press("Tab");
  await expect(page.locator("#ref-pop")).toBeHidden();
  expect(page.__errors).toEqual([]);
});

test("an id no account answers to says so instead of guessing", async ({ page }) => {
  await open(page);
  const chip = ref(page, "recipient");
  await expect(chip).toHaveClass(/missing/);
  await expect(chip).toContainText(/no account/i);
  await chip.hover();
  // Deleted and disabled read alike here — the directory lists neither — so the
  // popover names both rather than picking one.
  await expect(page.locator("#ref-pop")).toContainText(/deleted/i);
  await expect(page.locator("#ref-pop")).toContainText(/disabled/i);
  await expect(page.locator("#ref-pop")).toContainText(GONE);
  expect(page.__errors).toEqual([]);
});

test("a group's id names the group", async ({ page }) => {
  await open(page);
  await expect(ref(page, "approvalRef")).toContainText("IT-Leitung");
  await ref(page, "approvalRef").hover();
  await expect(page.locator("#ref-pop")).toContainText("Group");
  expect(page.__errors).toEqual([]);
});

test("a group nobody lists was deleted, and is not called disabled", async ({ page }) => {
  await open(page);
  const chip = ref(page, "formerTeam");
  await expect(chip).toHaveClass(/missing/);
  await expect(chip).toContainText(/no group/i);
  await chip.hover();
  // A group has no disabled state, so the card names the one thing that happened.
  await expect(page.locator("#ref-pop")).toContainText(/deleted/i);
  await expect(page.locator("#ref-pop")).not.toContainText(/disabled/i);
  expect(page.__errors).toEqual([]);
});

test("a process id opens the newest deployed version's live view", async ({ page }) => {
  await open(page);
  const chip = ref(page, "provisionProcess");
  await expect(chip).toContainText("M365 Funktionspostfach");
  await expect(chip).toHaveAttribute("href", "#/operations/p/22");
  await chip.hover();
  await expect(page.locator("#ref-pop")).toContainText("Process");
  await expect(page.locator("#ref-pop")).toContainText(/version 2/i);
  expect(page.__errors).toEqual([]);
});

test("a product and a position name the product and its variant, and lead to its catalogue", async ({ page }) => {
  await open(page);
  const item = ref(page, "itemId");
  await expect(item).toContainText("Shared mailbox");
  await expect(item).toHaveAttribute("href", "#/catalog/c/m365");
  const pos = ref(page, "positionId");
  await expect(pos).toContainText("Shared mailbox");
  await expect(pos).toContainText("Large");
  await pos.hover();
  await expect(page.locator("#ref-pop")).toContainText("Product");
  await expect(page.locator("#ref-pop")).toContainText("m365");
  expect(page.__errors).toEqual([]);
});

test("without the role a reference is named but not linked", async ({ page }) => {
  await open(page, { roles: ["user"] });
  await expect(ref(page, "provisionProcess")).toContainText("M365 Funktionspostfach");
  await expect(ref(page, "provisionProcess")).not.toHaveAttribute("href", /.*/);
  await expect(ref(page, "itemId")).toContainText("Shared mailbox");
  await expect(ref(page, "itemId")).not.toHaveAttribute("href", /.*/);
  expect(page.__errors).toEqual([]);
});

test("only a whole value is a reference", async ({ page }) => {
  await open(page);
  await expect(ref(page, "orderer")).toHaveCount(1); // the directories have answered
  // A filter that contains an id, a sentence naming the prefix, a word nothing is
  // called, and a number: none of them names anything.
  for (const name of ["filter", "note", "status", "count"]) {
    await expect(ref(page, name)).toHaveCount(0);
  }
  expect(page.__errors).toEqual([]);
});

test("the directories are read once, not on every redraw", async ({ page }) => {
  await open(page);
  await expect(ref(page, "itemId")).toHaveCount(1);
  // The rows are rewritten on every filter keystroke, every element and every poll.
  await page.locator("#tab-variables #v-filter").fill("item");
  await page.locator("#tab-variables #v-filter").fill("");
  await page.locator('#history-list .ops-hrow[data-eik="1002"]').click();
  await expect(ref(page, "orderer")).toContainText("Patrick Blumer");
  const calls = await page.evaluate(() => window.__calls);
  expect(calls["/api/v1/principals"]).toBe(1);
  expect(calls["/api/v1/processes"]).toBe(1);
  expect(calls["/api/v1/catalog-products"]).toBe(1);
  expect(page.__errors).toEqual([]);
});

test("a directory that cannot be read leaves its ids as they were", async ({ page }) => {
  await open(page, { failPrincipals: true });
  await expect(ref(page, "provisionProcess")).toHaveCount(1); // the others still answer
  // Not knowing is not the same as "no account": nothing is claimed about either id.
  await expect(ref(page, "orderer")).toHaveCount(0);
  await expect(ref(page, "recipient")).toHaveCount(0);
  await expect(row(page, "orderer").locator(".c-val")).toHaveText(ORDERER);
  expect(page.__errors).toEqual([]);
});

test("the column filter finds a row by the name its value resolves to", async ({ page }) => {
  await open(page);
  await expect(ref(page, "orderer")).toContainText("Patrick Blumer");
  const valueFilter = page.locator("#tab-variables .vt .dt-filter-row th").nth(1).locator("input");
  await valueFilter.fill("blumer");
  await expect(row(page, "orderer")).toBeVisible();
  await expect(row(page, "itemId")).toBeHidden();
  expect(page.__errors).toEqual([]);
});
