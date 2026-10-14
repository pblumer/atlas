// End-to-end coverage for checking a worker from the operator page
// (api/web/workerdialog.js, testWorkerFlow).
//
// The check has two modes and they are not degrees of the same thing: a probe
// connects, authenticates and stops, and a send puts a real message in a real
// person's inbox. The server tells them apart by whether `to` is empty
// (api/connectors.go). That was asked as a window.prompt saying "leave empty to
// only check the connection", so the harmless mode was expressed by typing nothing
// into the box that means "send mail to this address" — and a prompt is refusable:
// suppressed by the browser it returns null, which the caller read as Cancel and
// the operator read as nothing happening at all.
//
// So the tests here are about what reaches the server for each mode, and about the
// address never being sent half-typed.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/worker-test-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
});

const posted = (page) => page.locator("#posted");

test("the harmless mode is the one that is offered first", async ({ page }) => {
  await page.locator("#check-mail").click();
  // Opening the dialog must not preselect the mode that mails somebody.
  await expect(page.locator("#wt-mode")).toHaveValue("probe");
  await expect(page.locator("#wt-to-field")).toBeHidden();
  await expect(page.locator("[data-wt-go]")).toHaveText("Check");

  await page.locator("[data-wt-go]").click();
  // An empty recipient is what makes this a probe, and it still has to be sent.
  await expect(posted(page)).toHaveText(/"to":""/);
  await expect(posted(page)).toHaveText(/"name":"gmail_auth"/);
  expect(page.__errors).toEqual([]);
});

test("sending asks for the address, and sends exactly it", async ({ page }) => {
  await page.locator("#check-mail").click();
  await page.locator("#wt-mode").selectOption("send");
  // The field appears with the mode, and the button says what it is about to do.
  await expect(page.locator("#wt-to-field")).toBeVisible();
  await expect(page.locator("[data-wt-go]")).toHaveText("Send");

  await page.locator("#wt-to").fill("  simone@example.org  ");
  await page.locator("[data-wt-go]").click();
  await expect(posted(page)).toHaveText(/"to":"simone@example.org"/);
  await expect(page.locator("#result")).toHaveText("true");
});

test("an address that is not one is not sent", async ({ page }) => {
  await page.locator("#check-mail").click();
  await page.locator("#wt-mode").selectOption("send");
  await page.locator("#wt-to").fill("simone");
  await page.locator("[data-wt-go]").click();

  // Still open, nothing posted: the provider would answer this in its own error
  // vocabulary, which an operator reads as "the worker is broken".
  await expect(page.locator("#wt-mode")).toBeVisible();
  await expect(posted(page)).toHaveText("—");
});

test("cancelling checks nothing", async ({ page }) => {
  await page.locator("#check-mail").click();
  await page.locator("[data-wt-cancel]").click();
  await expect(page.locator(".modal-ov")).toHaveCount(0);
  await expect(posted(page)).toHaveText("—");
  await expect(page.locator("#result")).toHaveText("false");

  await page.locator("#check-mail").click();
  await page.keyboard.press("Escape");
  await expect(page.locator(".modal-ov")).toHaveCount(0);
  await expect(posted(page)).toHaveText("—");
});

test("a worker that cannot be checked by sending is not asked about it", async ({ page }) => {
  // Only mail has a second half; a database check dials and stops, because the
  // equivalent of "send one to see" would be running a statement.
  await page.locator("#check-sql").click();
  await expect(page.locator(".modal-ov")).toHaveCount(0);
  await expect(posted(page)).toHaveText(/"kind":"postgres"/);
  await expect(posted(page)).toHaveText(/"to":""/);
});
