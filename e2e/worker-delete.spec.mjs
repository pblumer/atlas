// End-to-end coverage for what a worker's deployed use means on the operator page
// (ADR-0163): the count the row carries, the list behind it, and deleting one the
// models still reference. The server refuses that delete and answers with the
// processes in the way; this proves the operator surface does something useful with
// the refusal instead of showing a bare 409 — it asks again, with the list, and only
// then forces.
import { test, expect } from "@playwright/test";

const open = async (page) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/worker-delete-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
};

// answer drives the window.confirm chain: one entry per dialog, in order. Playwright
// dismisses dialogs by default, so an un-answered confirm reads as "no".
const answer = (page, decisions) => {
  const queue = [...decisions];
  page.on("dialog", async (d) => {
    const yes = queue.shift();
    page.__dialogs = (page.__dialogs || []).concat(d.message());
    await (yes ? d.accept() : d.dismiss());
  });
};

test("the row counts what resolves through a worker, and the instances running on them", async ({ page }) => {
  await open(page);
  // The row itself is the count. Two definitions of two processes agree, so it says
  // one number; the running total is what makes this a decision rather than a click.
  await expect(page.locator("#usage-referenced")).toContainText("Used by 2 deployed processes");
  await expect(page.locator("#usage-referenced")).toContainText("2 running instances");

  // Definitions and processes are different numbers, and a worker eleven deployed
  // definitions of seven processes reference has to say both rather than overstate
  // the blast radius as eleven processes.
  await expect(page.locator("#usage-many")).toContainText("Used by 7 processes");
  await expect(page.locator("#usage-many")).toContainText("11 deployed versions");
  await expect(page.locator("#usage-many")).toContainText("1 running instance");

  // A worker nothing references says so plainly rather than showing an empty line —
  // and offers nothing to open.
  await expect(page.locator("#usage-orphan")).toContainText("Referenced by no deployed process");
  await expect(page.locator("#usage-orphan button")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("the count opens the list it stands for", async ({ page }) => {
  await open(page);
  await page.locator("#usage-referenced [data-usage]").click();
  const dialog = page.locator(".usage-modal");
  await expect(dialog).toContainText("Used by · Patrick Blumer");
  await expect(dialog).toContainText("Zahlung");
  await expect(dialog).toContainText("Mahnung");
  // Every version links to its own Operations page and names the elements whose tasks
  // resolve through the worker — what the row used to spell out, where there is room.
  await expect(dialog.locator('a[href="#/operations/p/7"]')).toContainText("v3");
  await expect(dialog.locator('a[href="#/operations/p/7"]')).toContainText("Task_pay");
  await expect(dialog.locator('a[href="#/operations/p/9"]')).toContainText("Task_remind, Task_escalate");
  // The one process something is running on says so on its own row.
  await expect(dialog.locator('a[href="#/operations/p/7"]')).toContainText("2 running");
  // And it says what deleting the worker would do to them.
  await expect(dialog).toContainText("no worker registered as Patrick Blumer");

  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("the list groups a redeployed process instead of repeating its name", async ({ page }) => {
  await open(page);
  await page.locator("#usage-many [data-usage]").click();
  const dialog = page.locator(".usage-modal");
  // Seven processes, seven groups — the five deployed versions of one of them are rows
  // inside its group rather than five entries that read as five different processes.
  await expect(dialog.locator(".usage-group")).toHaveCount(7);
  const redeployed = dialog.locator(".usage-group").filter({ hasText: "Info Mail versenden" });
  await expect(redeployed.locator(".usage-ver")).toHaveCount(5);
  // Newest first: the version an operator is looking for is the one at the top.
  await expect(redeployed.locator(".usage-ver").first()).toContainText("v5");
  await expect(redeployed.locator(".usage-ver").first()).toContainText("1 running");

  // Past a handful of processes the dialog is a list to search, not one to scan.
  const filter = dialog.locator("[data-usage-filter]");
  await filter.fill("offboard");
  await expect(dialog.locator(".usage-group:visible")).toHaveCount(1);
  await expect(dialog.locator(".usage-group:visible")).toContainText("Benutzer offboarden");
  await filter.fill("nothing matches this");
  await expect(dialog.locator(".usage-group:visible")).toHaveCount(0);
  await expect(dialog.locator("[data-usage-none]")).toBeVisible();

  await dialog.locator("[data-usage-done]").click();
  await expect(dialog).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("a worker nothing references deletes on one confirm", async ({ page }) => {
  await open(page);
  answer(page, [true]);
  await page.locator("#del-orphan").click();

  await expect.poll(() => page.evaluate(() => window.__result)).toBe(true);
  // One plain DELETE, no force: nothing was in the way, so nothing was overridden.
  expect(await page.evaluate(() => window.__deletes)).toEqual(["/api/v1/connectors/c2"]);
  expect(page.__errors).toEqual([]);
});

test("a refused delete asks again with the processes in hand, then forces", async ({ page }) => {
  await open(page);
  answer(page, [true, true]);
  await page.locator("#del-referenced").click();

  await expect.poll(() => page.evaluate(() => window.__result)).toBe(true);
  // The plain delete was tried first and refused; only then the forced one.
  expect(await page.evaluate(() => window.__deletes)).toEqual([
    "/api/v1/connectors/c1",
    "/api/v1/connectors/c1?force=true",
  ]);
  // The second question is the one that carries the answer: which processes, how many
  // running, and what actually happens to their tasks.
  const second = page.__dialogs[1];
  expect(second).toContain("Zahlung v3");
  expect(second).toContain("(2 running)");
  expect(second).toContain("Mahnung v1");
  expect(second).toContain("no worker registered");
  expect(page.__errors).toEqual([]);
});

test("declining the second question leaves the worker alone", async ({ page }) => {
  await open(page);
  answer(page, [true, false]);
  await page.locator("#del-referenced").click();

  await expect.poll(() => page.evaluate(() => window.__result)).toBe(false);
  // The refused attempt stands; nothing was forced.
  expect(await page.evaluate(() => window.__deletes)).toEqual(["/api/v1/connectors/c1"]);
  expect(page.__errors).toEqual([]);
});

test("declining the first question issues nothing at all", async ({ page }) => {
  await open(page);
  answer(page, [false]);
  await page.locator("#del-referenced").click();

  await expect.poll(() => page.evaluate(() => window.__result)).toBe(false);
  expect(await page.evaluate(() => window.__deletes)).toEqual([]);
  expect(page.__errors).toEqual([]);
});
