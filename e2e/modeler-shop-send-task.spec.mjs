// End-to-end coverage for the shop send task in the Modeler (api/web/editor.js, ADR-0429
// §4). Driven through the real vendored bpmn-js: a send task given the Shop kind carries
// <atlas:shopTask mode="outcome" action="…" outcome="…"/> and nothing else — no message,
// no task definition, no Worker extension — because that element is the whole contract the
// compiler parses. Choosing another kind takes it off again, and an action key no
// catalogue could declare is flagged where it is typed rather than at deploy.
import { test, expect } from "@playwright/test";

async function mount(page, which) {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/modeler-shop-send-task-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
  await page.evaluate((w) => window.__mount(w), which);
  // What a send task does is how it is implemented, not how it is drawn.
  await page.locator('[data-tab="implement"]').click();
}

// openGroup expands a property group by its exact title unless it is open already; groups
// but General start collapsed, and a re-render keeps what the author chose. The title is
// matched whole, so "Type" is not "Worker type" and "Message" is not "Message name".
async function openGroup(page, title, probe) {
  await expect(page.locator(probe)).toHaveCount(1);
  if (!(await page.locator(probe).isVisible())) {
    await page.locator(".pgroup-head .pgroup-title").filter({ hasText: new RegExp(`^${title}$`) }).click();
  }
  await expect(page.locator(probe)).toBeVisible();
}

// chooseKind selects a send task and picks a kind in its Type picker.
async function chooseKind(page, id, kind) {
  await page.evaluate((el) => window.__select(el), id);
  await openGroup(page, "Type", "#f-stkind-list");
  await page.locator(`.stkind-row[data-kind='${kind}']`).click();
}

// openShop expands the Shop group of the selected send task.
const openShop = (page) => openGroup(page, "Shop", "#f-shop-action");

// sendTaskXML returns the exported XML of one send task.
async function sendTaskXML(page, id) {
  const xml = await page.evaluate(() => window.__xml());
  return new RegExp(`<bpmn:sendTask id="${id}"[\\s\\S]*?</bpmn:sendTask>`).exec(xml)[0];
}

// extensionsOf is what a task's <extensionElements> holds, whitespace between elements
// removed, or null when it has none.
function extensionsOf(task) {
  const m = /<bpmn:extensionElements>([\s\S]*?)<\/bpmn:extensionElements>/.exec(task);
  return m ? m[1].replace(/>\s+</g, "><").trim() : null;
}

test("choosing Shop on a message send task writes the shop task and takes the message off", async ({ page }) => {
  await mount(page);
  await chooseKind(page, "Send_msg", "shop");
  await openShop(page);
  await page.locator("#f-shop-action").fill("password-reset");
  await page.locator("#f-shop-action").blur();
  await page.locator("#f-shop-outcome").selectOption("rejected");

  const task = await sendTaskXML(page, "Send_msg");
  expect(extensionsOf(task)).toBe('<atlas:shopTask mode="outcome" action="password-reset" outcome="rejected" />');
  expect(task).not.toContain("messageRef");
  expect(task).not.toContain("operationRef");
  expect(page.__errors).toEqual([]);
});

test("choosing Shop on a job-worker send task drops its task definition", async ({ page }) => {
  await mount(page);
  await chooseKind(page, "Send_worker", "shop");
  await openShop(page);
  // The outcome starts on the first choice, and the model says what the panel shows.
  await expect(page.locator("#f-shop-outcome")).toHaveValue("completed");
  await page.locator("#f-shop-action").fill("storage-extend");
  await page.locator("#f-shop-action").blur();

  const task = await sendTaskXML(page, "Send_worker");
  expect(extensionsOf(task)).toBe('<atlas:shopTask mode="outcome" action="storage-extend" outcome="completed" />');
  expect(task).not.toContain("taskDefinition");
  expect(task).not.toContain("retries");
  expect(page.__errors).toEqual([]);
});

test("a send task drawn fresh takes the Shop kind in a model that declared no atlas namespace", async ({ page }) => {
  await mount(page, "bare");
  const id = await page.evaluate(() => window.__drawSendTask());
  await chooseKind(page, id, "shop");
  await openShop(page);
  await page.locator("#f-shop-action").fill("password-reset");
  await page.locator("#f-shop-action").blur();

  const xml = await page.evaluate(() => window.__xml());
  expect(xml).toContain('xmlns:atlas="http://atlas/schema/1.0"');
  const task = await sendTaskXML(page, id);
  expect(extensionsOf(task)).toBe('<atlas:shopTask mode="outcome" action="password-reset" outcome="completed" />');
  expect(task).not.toContain("messageRef");
  expect(page.__errors).toEqual([]);
});

test("a shop task read from the model shows what it declares, and offers one mode", async ({ page }) => {
  await mount(page);
  await page.evaluate((el) => window.__select(el), "Send_shop");
  await openGroup(page, "Type", "#f-stkind-list");
  const chosen = page.locator(".stkind-row-on");
  await expect(chosen).toHaveAttribute("data-kind", "shop");
  await expect(chosen).toContainText("Shop");
  await expect(chosen).toContainText("Reports how a product action ended (ADR-0429).");

  await openShop(page);
  const modes = await page.locator("#f-shop-mode option").evaluateAll((os) => os.map((o) => o.value));
  expect(modes).toEqual(["outcome"]);
  await expect(page.locator("#f-shop-action")).toHaveValue("storage-extend");
  await expect(page.locator("#f-shop-outcome")).toHaveValue("failed");
  const outcomes = await page.locator("#f-shop-outcome option").evaluateAll((os) => os.map((o) => o.value));
  expect(outcomes).toEqual(["completed", "rejected", "failed"]);
  await expect(page.locator("#f-shop-action-err")).toBeHidden();
  // An outcome is reported once for the command it answers, so the task offers no loop.
  await expect(page.locator("#f-mi-mode")).toHaveCount(0);

  // Opening the task changed nothing: the round trip keeps the declaration as it was.
  const task = await sendTaskXML(page, "Send_shop");
  expect(extensionsOf(task)).toBe('<atlas:shopTask mode="outcome" action="storage-extend" outcome="failed" />');
  expect(page.__errors).toEqual([]);
});

test("switching back to the message kind takes the shop task off", async ({ page }) => {
  await mount(page);
  await chooseKind(page, "Send_shop", "message");

  const task = await sendTaskXML(page, "Send_shop");
  expect(task).not.toContain("shopTask");
  expect(task).not.toContain("taskDefinition");
  // The message picker is back, waiting for a message to be chosen.
  await openGroup(page, "Message", "#f-msgref");
  await expect(page.locator("#f-shop-action")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("switching to a Worker Type takes the shop task off", async ({ page }) => {
  await mount(page);
  await chooseKind(page, "Send_shop", "rest");

  const task = await sendTaskXML(page, "Send_shop");
  expect(task).not.toContain("shopTask");
  expect(task).toContain("<atlas:restConnector");
  expect(page.__errors).toEqual([]);
});

test("an action key no catalogue could declare shows the validation hint", async ({ page }) => {
  await mount(page);
  await page.evaluate((el) => window.__select(el), "Send_shop");
  await openShop(page);
  const action = page.locator("#f-shop-action");
  const hint = page.locator("#f-shop-action-err");

  // Said as it is typed, before anything is saved.
  await action.fill("Password Reset");
  await expect(hint).toBeVisible();
  await expect(hint).toContainText("lower-case letters, digits and dashes");
  await action.fill("password-reset");
  await expect(hint).toBeHidden();
  await action.fill("x".repeat(65));
  await expect(hint).toBeVisible();

  // What was typed is kept, not silently replaced, and the hint stays with it when the
  // task is opened again.
  await action.fill("passwort_zurücksetzen");
  await action.blur();
  expect(await sendTaskXML(page, "Send_shop")).toContain('action="passwort_zurücksetzen"');
  await page.evaluate((el) => window.__select(el), "Send_msg");
  await page.evaluate((el) => window.__select(el), "Send_shop");
  await openShop(page);
  await expect(page.locator("#f-shop-action-err")).toBeVisible();

  await page.locator("#f-shop-action").fill("password-reset");
  await page.locator("#f-shop-action").blur();
  await expect(page.locator("#f-shop-action-err")).toBeHidden();
  expect(await sendTaskXML(page, "Send_shop")).toContain('action="password-reset"');
  expect(page.__errors).toEqual([]);
});
