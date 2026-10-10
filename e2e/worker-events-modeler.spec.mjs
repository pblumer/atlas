// End-to-end coverage for the Worker events in the Modeler's message picker
// (api/web/editor.js, ADR-0429 §6). Driven through the real vendored bpmn-js: an element
// that waits for a message is offered the names the server's inbound watches publish,
// picking one declares a message of that name, and an element that throws a message is
// offered none — so a Worker's event is chosen, not retyped one character differently.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/worker-events-modeler-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => window.__mount());
  // The message picker is part of how an element is implemented, not how it is drawn.
  await page.locator('[data-tab="implement"]').click();
});

// openMessage selects an element and expands its Message group, wherever the panel puts
// it, so the picker is on screen.
async function openMessage(page, id) {
  await page.evaluate((el) => window.__select(el), id);
  const picker = page.locator("#f-msgref");
  await expect(picker).toHaveCount(1);
  if (!(await picker.isVisible())) await page.locator(".pgroup-head", { hasText: "Message" }).first().click();
  await expect(picker).toBeVisible();
}

const workerGroup = (page) => page.locator("#f-msgref optgroup[data-worker-events]");

test("a message start is offered the Worker events, one per name, naming who publishes it", async ({ page }) => {
  await openMessage(page, "Start_msg");
  await expect(workerGroup(page)).toHaveAttribute("label", "Events from Workers");

  const offered = await workerGroup(page).locator("option").evaluateAll((os) =>
    os.map((o) => ({ value: o.value, text: o.textContent })));
  // order.placed is declared by the diagram already, so it is not offered a second time.
  expect(offered.map((o) => o.value)).toEqual([
    "__source__:employee.created",
    "__source__:jira.ticket.created",
  ]);
  // Two watches publish one name: one choice, both workers named.
  expect(offered[1].text).toBe("jira.ticket.created — ServiceDesk (jira), Projects (jira)");
  // Only disabled watches publish this one: kept, and marked.
  expect(offered[0].text).toBe("employee.created — HR (googlesheets) — off");
  expect(page.__errors).toEqual([]);
});

test("picking a Worker event declares a message of that name and links it", async ({ page }) => {
  await openMessage(page, "Start_msg");
  await expect(workerGroup(page)).toHaveCount(1);
  await page.locator("#f-msgref").selectOption("__source__:jira.ticket.created");

  await expect(page.locator("#f-msgname")).toHaveValue("jira.ticket.created");
  await expect(page.locator("#f-msgsources")).toContainText("Published by 2 inbound event watches");

  const xml = await page.evaluate(() => window.__xml());
  const msg = /<bpmn:message id="([^"]+)" name="jira\.ticket\.created"/.exec(xml);
  expect(msg).not.toBeNull();
  expect(xml).toMatch(new RegExp(`<bpmn:startEvent id="Start_msg"[\\s\\S]*?messageRef="${msg[1]}"`));
  // Re-rendered on the new message, the picker now lists the name among the diagram's
  // own messages and no longer among the Worker events.
  await expect(page.locator("#f-msgref")).toHaveValue(msg[1]);
  await expect(workerGroup(page).locator('option[value="__source__:jira.ticket.created"]')).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("the name field suggests every Worker event, declared ones included", async ({ page }) => {
  await openMessage(page, "Catch_1");
  await expect(page.locator("#f-msgname")).toHaveAttribute("list", "f-msgname-sources");
  await expect(page.locator("#f-msgname-sources option")).toHaveCount(3);
  const values = await page.locator("#f-msgname-sources option").evaluateAll((os) => os.map((o) => o.value));
  expect(values).toEqual(["employee.created", "jira.ticket.created", "order.placed"]);
  expect(page.__errors).toEqual([]);
});

test("an element that throws a message is offered no Worker events", async ({ page }) => {
  await openMessage(page, "Throw_1");
  // Give the listing time to arrive before asserting it changed nothing.
  await page.waitForFunction(() => window.__sourceCalls > 0);
  await expect(workerGroup(page)).toHaveCount(0);
  await expect(page.locator("#f-msgname")).not.toHaveAttribute("list", /.+/);
  expect(page.__errors).toEqual([]);
});

test("one render asks the server once for the listing", async ({ page }) => {
  await openMessage(page, "Catch_1");
  await expect(page.locator("#f-msgname-sources option")).toHaveCount(3);
  const before = await page.evaluate(() => window.__sourceCalls);
  // A rename re-reads the line under the field from the same listing.
  await page.locator("#f-msgname").fill("jira.ticket.created");
  await page.locator("#f-msgname").blur();
  await expect(page.locator("#f-msgsources")).toContainText("Published by 2 inbound event watches");
  expect(await page.evaluate(() => window.__sourceCalls)).toBe(before);
  expect(page.__errors).toEqual([]);
});

test("a listing that fails leaves the picker as it was", async ({ page }) => {
  await page.evaluate(() => { window.__failSources = true; });
  await openMessage(page, "Start_msg");
  await page.waitForFunction(() => window.__sourceCalls > 0);
  await expect(workerGroup(page)).toHaveCount(0);
  // The picker still offers the diagram's messages and a new one.
  await expect(page.locator('#f-msgref option[value="__new__"]')).toHaveCount(1);
  await expect(page.locator('#f-msgref option[value="Message_order"]')).toHaveCount(1);
  expect(page.__errors).toEqual([]);
});
