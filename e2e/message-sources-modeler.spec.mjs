// End-to-end coverage for the Modeler's message picker grouped by source (api/web/editor.js,
// ADR-0429 §6). GET /api/v1/message-sources answers three kinds of row — inbound watches,
// product actions, and the deployed processes waiting for a message — and the picker
// offers each where it belongs: an element that waits for a message is offered the Worker
// events and the product actions, an element that throws one the processes waiting for it
// and never a product action, whose message is the order's to send. A row without a
// sourceKind is an inbound watch, as an older server means it, and only watches count as
// Worker events. A newer server adds a fourth kind, a deployed process sending the name,
// and lists every version (ADR-0446): a waiting element is offered what the newest
// versions send, and a sending one is told who waits for it. Driven through the real
// vendored bpmn-js against a mock `api`.
import { test, expect } from "@playwright/test";

// mount opens the Modeler on the shared diagram filed under processId and shows the
// Implement tab, where the message picker lives. mailbox-lifecycle is the process the
// Mailbox product binds; any other id is one no product binds.
async function mount(page, processId) {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/message-sources-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate((pid) => window.__mount(pid), processId);
  await page.locator('[data-tab="implement"]').click();
}

// openMessage selects an element and expands its Message group, wherever the panel puts
// it, so the picker is on screen.
async function openMessage(page, id) {
  await page.evaluate((el) => window.__select(el), id);
  const picker = page.locator("#f-msgref");
  await expect(picker).toHaveCount(1);
  if (!(await picker.isVisible())) {
    await page.locator(".pgroup-head .pgroup-title").filter({ hasText: /^Message$/ }).click();
  }
  await expect(picker).toBeVisible();
}

const group = (page, data) => page.locator(`#f-msgref optgroup[data-${data}]`);

// optionsOf reads a group's options as value and visible text.
const optionsOf = (page, data) => group(page, data).locator("option").evaluateAll((os) =>
  os.map((o) => ({ value: o.value, text: o.textContent, disabled: o.disabled })));

// groupLabels lists the picker's groups in the order the author sees them.
const groupLabels = (page) => page.locator("#f-msgref optgroup").evaluateAll((gs) => gs.map((g) => g.label));

test("an element that waits for a message is offered the Worker events, then the product actions", async ({ page }) => {
  await mount(page, "onboarding");
  await openMessage(page, "Catch_new");
  await expect(group(page, "product-actions")).toHaveCount(1);
  expect(await groupLabels(page)).toEqual(["Events from Workers", "Product actions"]);

  // One choice per action's message: the product, the action key and its effect. The one
  // whose message the diagram already declares is not offered a second time.
  expect(await optionsOf(page, "product-actions")).toEqual([
    { value: "__source__:mailbox.password.reset", text: "mailbox.password.reset — Mailbox · password-reset (service)", disabled: false },
    { value: "__source__:mailbox.provision", text: "mailbox.provision — Mailbox · provision (provision)", disabled: false },
  ]);
  // Both groups sit before "New message", which stays the last choice.
  const last = await page.locator("#f-msgref option").last().getAttribute("value");
  expect(last).toBe("__new__");
  await expect(group(page, "waiting-processes")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("a row without a sourceKind is an inbound watch, and only watches are Worker events", async ({ page }) => {
  await mount(page, "onboarding");
  await openMessage(page, "Catch_new");
  await expect(group(page, "worker-events")).toHaveCount(1);
  // jira.ticket.created and employee.created come without a sourceKind; order.placed is a
  // watch too, but the diagram declares it. No product action and no process is here.
  expect(await optionsOf(page, "worker-events")).toEqual([
    { value: "__source__:employee.created", text: "employee.created — HR (googlesheets) — off", disabled: false },
    { value: "__source__:jira.ticket.created", text: "jira.ticket.created — ServiceDesk (jira)", disabled: false },
  ]);

  // The line under the field counts watches alone.
  await page.locator("#f-msgref").selectOption("__source__:jira.ticket.created");
  await expect(page.locator("#f-msgname")).toHaveValue("jira.ticket.created");
  await expect(page.locator("#f-msgsources")).toContainText("Published by 1 inbound event watch:");
  await expect(page.locator("#f-msgsources")).toContainText("ServiceDesk");
  await expect(page.locator("#f-msgsources")).not.toContainText("product action");
  expect(page.__errors).toEqual([]);
});

test("in a process a product binds, every Worker event is marked as not for a product action", async ({ page }) => {
  await mount(page, "mailbox-lifecycle");
  await openMessage(page, "Catch_new");
  await expect(group(page, "worker-events")).toHaveCount(1);
  const workers = await optionsOf(page, "worker-events");
  expect(workers.map((o) => o.text)).toEqual([
    "employee.created — HR (googlesheets) — off — not for a product action",
    "jira.ticket.created — ServiceDesk (jira) — not for a product action",
  ]);
  // A warning, not a lock: the choice is still there and still declares the message.
  expect(workers.every((o) => !o.disabled)).toBe(true);
  // The product actions are not marked: they are what such a process is for.
  const actions = await optionsOf(page, "product-actions");
  expect(actions.map((o) => o.text)).toEqual([
    "mailbox.password.reset — Mailbox · password-reset (service)",
    "mailbox.provision — Mailbox · provision (provision)",
  ]);

  await page.locator("#f-msgref").selectOption("__source__:jira.ticket.created");
  await expect(page.locator("#f-msgname")).toHaveValue("jira.ticket.created");
  expect(page.__errors).toEqual([]);
});

test("an element that throws is offered the processes waiting for it, and no product action", async ({ page }) => {
  await mount(page, "onboarding");
  for (const id of ["Throw_new", "Send_msg"]) {
    await openMessage(page, id);
    await expect(group(page, "waiting-processes")).toHaveCount(1);
    expect(await groupLabels(page)).toEqual(["Processes waiting for it"]);
    // invoice.paid is waited for by two processes: one choice, both named, each with where
    // it waits. mailbox.done and mailbox.storage.extend are declared, so not offered again.
    expect(await optionsOf(page, "waiting-processes")).toEqual([
      { value: "__source__:invoice.paid", text: "invoice.paid — billing · catch, dunning · start", disabled: false },
    ]);
    await expect(group(page, "product-actions")).toHaveCount(0);
    await expect(group(page, "worker-events")).toHaveCount(0);
    const last = await page.locator("#f-msgref option").last().getAttribute("value");
    expect(last).toBe("__new__");
  }
  expect(page.__errors).toEqual([]);
});

test("picking a waiting process declares its message on the throw", async ({ page }) => {
  await mount(page, "onboarding");
  await openMessage(page, "Throw_new");
  await expect(group(page, "waiting-processes")).toHaveCount(1);
  await page.locator("#f-msgref").selectOption("__source__:invoice.paid");
  await expect(page.locator("#f-msgname")).toHaveValue("invoice.paid");

  // Free text stays allowed, and the field suggests every name a deployed process waits
  // for — declared ones included — and nothing a product action owns.
  await expect(page.locator("#f-msgname")).toHaveAttribute("list", "f-msgname-sources");
  await expect(page.locator("#f-msgname-sources option")).toHaveCount(3);
  const values = await page.locator("#f-msgname-sources option").evaluateAll((os) => os.map((o) => o.value));
  expect(values).toEqual(["invoice.paid", "mailbox.done", "mailbox.storage.extend"]);

  const xml = await page.evaluate(() => window.__xml());
  const msg = /<bpmn:message id="([^"]+)" name="invoice\.paid"/.exec(xml);
  expect(msg).not.toBeNull();
  expect(xml).toMatch(new RegExp(`<bpmn:intermediateThrowEvent id="Throw_new"[\\s\\S]*?messageRef="${msg[1]}"`));
  await expect(page.locator("#f-msgsources")).toContainText("Waited for by deployed processes billing (catch), dunning (start).");
  expect(page.__errors).toEqual([]);
});

test("picking a product action declares its message, and the field suggests the actions too", async ({ page }) => {
  await mount(page, "mailbox-lifecycle");
  await openMessage(page, "Catch_new");
  await expect(group(page, "product-actions")).toHaveCount(1);
  await page.locator("#f-msgref").selectOption("__source__:mailbox.password.reset");
  await expect(page.locator("#f-msgname")).toHaveValue("mailbox.password.reset");

  // The name field — there once a message is chosen — suggests the Worker events, then the
  // product actions, declared names included.
  await expect(page.locator("#f-msgname")).toHaveAttribute("list", "f-msgname-sources");
  await expect(page.locator("#f-msgname-sources option")).toHaveCount(6);
  const values = await page.locator("#f-msgname-sources option").evaluateAll((os) => os.map((o) => o.value));
  expect(values).toEqual([
    "employee.created", "jira.ticket.created", "order.placed",
    "mailbox.password.reset", "mailbox.provision", "mailbox.storage.extend",
  ]);

  const xml = await page.evaluate(() => window.__xml());
  const msg = /<bpmn:message id="([^"]+)" name="mailbox\.password\.reset"/.exec(xml);
  expect(msg).not.toBeNull();
  expect(xml).toMatch(new RegExp(`<bpmn:intermediateCatchEvent id="Catch_new"[\\s\\S]*?messageRef="${msg[1]}"`));
  await expect(page.locator("#f-msgsources")).toContainText("Owned by product action password-reset of Mailbox (service)");
  // Declared now, so it is in the diagram's own list and no longer among the actions.
  await expect(group(page, "product-actions").locator('option[value="__source__:mailbox.password.reset"]')).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("the hint names every source of the name: the watches, the product action, the waiting processes", async ({ page }) => {
  await mount(page, "mailbox-lifecycle");
  const hint = page.locator("#f-msgsources");

  // A product's message: the action that owns it and the process waiting for it. No watch
  // publishes it, and none may, so the line does not suggest adding one.
  await openMessage(page, "Receive_extend");
  await expect(hint).toContainText("Owned by product action storage-extend of Mailbox (change)");
  await expect(hint).toContainText("Waited for by deployed process mailbox-lifecycle (catch).");
  await expect(hint).not.toContainText("inbound event watch");

  // A watch's message: what it said before, and nothing about products.
  await openMessage(page, "Receive_plain");
  await expect(hint).toContainText("Published by 1 inbound event watch:");
  await expect(hint).toContainText("Orders");
  await expect(hint).not.toContainText("product action");
  await expect(hint).not.toContainText("Waited for");

  // A thrown message nothing publishes but a process waits for: the old line, then who
  // waits for it.
  await openMessage(page, "Send_msg");
  await expect(hint).toContainText("No inbound event watch on this server publishes mailbox.done.");
  await expect(hint).toContainText("Waited for by deployed process archive (catch).");
  expect(page.__errors).toEqual([]);
});

test("a listing that fails leaves the throw's picker and name field as they were", async ({ page }) => {
  await page.goto("/message-sources-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate(() => { window.__failSources = true; });
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.evaluate(() => window.__mount("onboarding"));
  await page.locator('[data-tab="implement"]').click();
  await openMessage(page, "Throw_new");
  await page.waitForFunction(() => window.__sourceCalls > 0);
  await expect(group(page, "waiting-processes")).toHaveCount(0);
  await expect(page.locator('#f-msgref option[value="__new__"]')).toHaveCount(1);
  await expect(page.locator('#f-msgref option[value="Message_done"]')).toHaveCount(1);
  expect(errors).toEqual([]);
});

// A newer server lists a deployed process's own sends and every deployed version
// (ADR-0446 §1). The picker reads the newest version alone, offers a waiting element
// the names models send, and a sending element is told whether anybody waits.
const NEWER = [
  { sourceKind: "process-throw", messageName: "invoice.sent", enabled: true, processId: "billing", elementId: "Send_inv", element: "throw", role: "throw", version: 3, newest: true },
  { sourceKind: "process-throw", messageName: "invoice.sent", enabled: true, processId: "billing", elementId: "Send_inv", element: "throw", role: "throw", version: 2 },
  { sourceKind: "process-throw", messageName: "only.old", enabled: true, processId: "legacy", elementId: "Send_old", element: "throw", role: "throw", version: 1 },
  { sourceKind: "process", messageName: "invoice.paid", enabled: true, processId: "legacy", elementId: "Catch_old", element: "catch", role: "catch", version: 1 },
];

async function mountNewer(page, processId) {
  await page.goto("/message-sources-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate((rows) => { window.__extraSources = rows; }, NEWER);
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.evaluate((pid) => window.__mount(pid), processId);
  await page.locator('[data-tab="implement"]').click();
}

test("a waiting element is offered the names deployed models send, newest versions only", async ({ page }) => {
  await mountNewer(page, "onboarding");
  await openMessage(page, "Catch_new");
  await expect(group(page, "sending-processes")).toHaveCount(1);
  expect(await groupLabels(page)).toEqual(["Events from Workers", "Product actions", "Messages deployed models send"]);
  // only.old is sent by an older version alone, so nobody sends it now.
  expect(await optionsOf(page, "sending-processes")).toEqual([
    { value: "__source__:invoice.sent", text: "invoice.sent — billing · throw", disabled: false },
  ]);

  // A name a deployed model sends no longer reads as unfed.
  await page.locator("#f-msgref").selectOption("__source__:invoice.sent");
  await expect(page.locator("#f-msgname")).toHaveValue("invoice.sent");
  await expect(page.locator("#f-msgsources")).toContainText("Sent by deployed process billing.");
  await expect(page.locator("#f-msgsources")).not.toContainText("No inbound event watch");
  expect(page.__errors).toEqual([]);
});

test("a sending element is told who waits for its message, or that nobody does", async ({ page }) => {
  await mountNewer(page, "onboarding");
  // An older version's catch is not offered: who waits now is the newest version.
  await openMessage(page, "Throw_new");
  expect(await optionsOf(page, "waiting-processes")).toEqual([
    { value: "__source__:invoice.paid", text: "invoice.paid — billing · catch, dunning · start", disabled: false },
  ]);

  await mount(page, "mailbox-lifecycle");
  await openMessage(page, "Send_msg");
  const hint = page.locator("#f-msgsources");
  await expect(hint).toContainText("Waited for by deployed process archive (catch).");
  await expect(hint.locator('a[href="/#/console/events?name=mailbox.done&channel=message"]')).toHaveCount(1);

  await page.locator("#f-msgname").fill("nobody.waits");
  await page.locator("#f-msgname").blur();
  await expect(hint).toContainText("No deployed process waits for nobody.waits: what this element sends is lost.");
  await expect(hint.locator('a[href="/#/console/events?name=nobody.waits&channel=message"]')).toHaveCount(1);
  expect(page.__errors).toEqual([]);
});
