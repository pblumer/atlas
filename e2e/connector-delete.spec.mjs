// End-to-end coverage for deleting a connector deployed models still reference
// (ADR-0163). The server refuses that delete and answers with the processes in the
// way; this proves the operator surface does something useful with the refusal
// instead of showing a bare 409 — it asks again, with the list, and only then forces.
import { test, expect } from "@playwright/test";

const open = async (page) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/connector-delete-harness.html");
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

test("names the processes that resolve through a connector, and the instances running on them", async ({ page }) => {
  await open(page);
  const used = page.locator("#usage-referenced");
  await expect(used).toContainText("Zahlung v3");
  await expect(used).toContainText("Mahnung v1");
  // The running count is the number that makes this a decision rather than a click.
  await expect(used).toContainText("2 running instances");
  // Each link goes to the process it names, and carries its elements for the hover.
  await expect(used.locator('a[href="#/operations/p/7"]')).toHaveAttribute("title", "Task_pay");
  await expect(used.locator('a[href="#/operations/p/9"]')).toHaveAttribute("title", "Task_remind, Task_escalate");

  // A connector nothing references says so plainly rather than showing an empty line.
  await expect(page.locator("#usage-orphan")).toContainText("Referenced by no deployed process");
  expect(page.__errors).toEqual([]);
});

test("a connector nothing references deletes on one confirm", async ({ page }) => {
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
  expect(second).toContain("no connector registered");
  expect(page.__errors).toEqual([]);
});

test("declining the second question leaves the connector alone", async ({ page }) => {
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
