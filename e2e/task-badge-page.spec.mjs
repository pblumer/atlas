// End-to-end coverage for what the live diagram says about waiting user tasks.
//
// Reported from a running server: "Ersatzgerät beschaffen" carried a 📋 500 badge while
// 1 275 tokens sat on it, and completing tasks in the Tasks app left the 500 exactly
// where it was. Nothing was stuck — 500 is the cap on one page of GET /api/v1/tasks
// (api/handlers.go maxTaskListDefault), and the badge was counting the rows it had been
// handed. A number that is the size of its own page cannot move until the queue drains
// below the cap, and reads as a total the whole time it cannot.
//
// So the open-task link no longer carries a count at all: the green token badge on the
// same shape is that number, exact at any scale, and it is the one an operator watches
// go down. What the link still decides — form or inbox, and whether to appear — now
// comes from the same runtime counters rather than from the page.
import { test, expect } from "@playwright/test";

const NNBSP = "\u202F"; // NARROW NO-BREAK SPACE — the thousands separator the badges use

const open = async (page) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/task-badge-page-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mountLive());
};

const link = (page, id) =>
  page.locator(`.djs-overlays[data-container-id="${id}"] .task-open`);
const badges = (page, id) =>
  page.locator(`.djs-overlays[data-container-id="${id}"] .token-badge`);

test("the open-task link never prints the size of its page", async ({ page }) => {
  await open(page);

  // 1 275 tasks are waiting and the page carries 400 of them. Neither number may appear
  // on the link: one is wrong and the other is the page's, not the element's.
  const l = link(page, "ut_ersatz");
  await expect(l).toHaveCount(1);
  expect(await l.textContent()).not.toMatch(/\d/);
  await expect(l).toHaveAttribute("href", "#/tasks");
  await expect(l).toHaveAttribute("title", "Open the waiting user tasks in the inbox");

  // The count is on the green badge, from the runtime counters — the whole 1 275, not
  // the 400 the page held.
  const two = badges(page, "ut_ersatz");
  await expect(two).toHaveCount(2); // 15 completed here and moved on, 1 275 alive
  expect(await two.nth(1).textContent()).toBe(`1${NNBSP}275`);
  expect(page.__errors).toEqual([]);
});

test("the count on the shape follows the tasks being completed", async ({ page }) => {
  await open(page);
  await expect(badges(page, "ut_ersatz").nth(1)).toHaveText(`1${NNBSP}275`);

  // Work the queue down. The page the mock hands back is unchanged — under the real cap
  // it would be, for the next 775 completions — so anything reading it would sit still.
  await page.evaluate(() => window.__setErsatzTokens(1272));
  await expect(badges(page, "ut_ersatz").nth(1)).toHaveText(`1${NNBSP}272`);

  // Drained: the link goes with the last token, rather than lingering because a stale
  // page still lists rows for the element.
  await page.evaluate(() => window.__setErsatzTokens(0));
  await expect(link(page, "ut_ersatz")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("a task the page could not carry still gets its link", async ({ page }) => {
  await open(page);

  // ut_flood has 40 people waiting on it and not one row on the page — the cap is
  // applied across every definition before the list is filtered to this one, so a flood
  // elsewhere pushes a plainly-waiting task off it. The badge is drawn from the tokens,
  // so it is there.
  const l = link(page, "ut_flood");
  await expect(l).toHaveCount(1);
  await expect(l).toHaveAttribute("href", "#/tasks");
  expect(await badges(page, "ut_flood").nth(1).textContent()).toBe("40");
  expect(page.__errors).toEqual([]);
});

test("one waiting task still lands straight on its form, and none gets no link", async ({ page }) => {
  await open(page);

  // The single-task case is what the deep link exists for, and it is the one thing the
  // page is still read for: it alone names which task to open.
  await expect(link(page, "ut_single")).toHaveAttribute("href", "#/tasks/t/7001");
  await expect(link(page, "ut_single")).toHaveAttribute("title", "Open the waiting user task's form");

  // A user task that only has history carries no link: there is nothing open to open.
  await expect(link(page, "ut_done")).toHaveCount(0);
  await expect(badges(page, "ut_done")).toHaveCount(1); // 12 completed here and moved on
  expect(page.__errors).toEqual([]);
});

test("isolating an instance asks for that instance's tasks, not the global page", async ({ page }) => {
  await open(page);
  await expect(link(page, "ut_flood")).toHaveAttribute("href", "#/tasks");

  // Pick the one running instance. Its tasks are resolved through the instance's own
  // element index, which the page cap does not apply to.
  const sel = page.locator("#instance-sel");
  await expect(sel.locator("option")).toHaveCount(2);
  await sel.selectOption("281474983999455");

  await expect
    .poll(() => page.evaluate(() => window.__seen.some((u) => u.startsWith("/api/v1/tasks?processInstance="))))
    .toBe(true);

  // And the payoff: this instance's ut_flood task is one click away, though the global
  // page never carried a row for that element.
  await expect(link(page, "ut_flood")).toHaveAttribute("href", "#/tasks/t/6001");
  await expect(link(page, "ut_single")).toHaveAttribute("href", "#/tasks/t/7001");
  expect(page.__errors).toEqual([]);
});
