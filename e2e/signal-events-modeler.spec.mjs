// End-to-end coverage for the catalogued events in the Modeler's signal picker
// (api/web/editor.js, ADR-0435 §5). Driven through the real vendored bpmn-js: an element
// that waits for a signal is offered the events atlas emits that a model may listen to,
// picking one declares or reuses a signal of that name, the chosen event says what it
// carries with its personal data named, and an atlas.* name that is no event, or that a
// model throws, is warned about.
import { test, expect } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
  await page.goto("/signal-events-modeler-harness.html");
  await page.waitForFunction(() => window.__ready === true, null, { timeout: 20000 });
});

async function mount(page) {
  await page.evaluate(() => window.__mount());
  await page.locator('[data-tab="implement"]').click();
}

// openSignal selects an element and expands its Signal group, wherever the panel puts
// it, so the picker is on screen.
async function openSignal(page, id) {
  await page.evaluate((el) => window.__select(el), id);
  const picker = page.locator("#f-sigref");
  await expect(picker).toHaveCount(1);
  if (!(await picker.isVisible())) await page.locator(".pgroup-head", { hasText: "Signal" }).first().click();
  await expect(picker).toBeVisible();
}

const eventGroup = (page) => page.locator("#f-sigref optgroup#f-sig-events");

test("a signal start is offered the events a model may listen to, and only those", async ({ page }) => {
  await mount(page);
  await openSignal(page, "Start_sig");
  await expect(eventGroup(page)).toHaveAttribute("label", "Events atlas emits");
  await expect(eventGroup(page).locator("option")).toHaveCount(2);
  const offered = await eventGroup(page).locator("option").evaluateAll((os) =>
    os.map((o) => ({ value: o.value, text: o.textContent })));
  // The order message drives atlas's own fulfilment and the outcome is a shape, so
  // neither is offered; the diagram already declares atlas.user.requested.
  expect(offered).toEqual([
    { value: "__event__:atlas.user.requested", text: "atlas.user.requested" },
    { value: "__event__:atlas.user.created", text: "atlas.user.created — new" },
  ]);
  expect(page.__errors).toEqual([]);
});

test("picking an event declares a signal of that name and says what the listener receives", async ({ page }) => {
  await mount(page);
  await openSignal(page, "Start_sig");
  await expect(eventGroup(page).locator("option")).toHaveCount(2);
  await page.locator("#f-sigref").selectOption("__event__:atlas.user.created");

  const xml = await page.evaluate(() => window.__xml());
  const sig = /<bpmn:signal id="([^"]+)" name="atlas\.user\.created"/.exec(xml);
  expect(sig).not.toBeNull();
  expect(xml).toMatch(new RegExp(`<bpmn:startEvent id="Start_sig"[\\s\\S]*?signalRef="${sig[1]}"`));
  await expect(page.locator("#f-sigref")).toHaveValue(sig[1]);
  await expect(page.locator("#f-sigevent [data-event]")).toHaveAttribute("data-event", "atlas.user.created");
  await expect(page.locator("#f-sigevent")).toContainText("A user account was created.");
  await expect(page.locator("#f-sigevent")).not.toContainText("personal data");
  expect(page.__errors).toEqual([]);
});

test("picking an event the diagram declares reuses its signal", async ({ page }) => {
  await mount(page);
  await openSignal(page, "Start_sig");
  await expect(eventGroup(page).locator("option")).toHaveCount(2);
  await page.locator("#f-sigref").selectOption("__event__:atlas.user.requested");

  const xml = await page.evaluate(() => window.__xml());
  expect(xml.match(/name="atlas\.user\.requested"/g)).toHaveLength(1);
  expect(xml).toMatch(/<bpmn:startEvent id="Start_sig"[\s\S]*?signalRef="Signal_req"/);
  // The note names the personal data the listener receives.
  await expect(page.locator("#f-sigevent")).toContainText("Somebody asked for a user account.");
  await expect(page.locator("#f-sigevent")).toContainText("The listener receives personal data: vorname, email.");
  await expect(page.locator('#f-sigevent a[href="/#/console/events"]')).toHaveCount(1);
  expect(page.__errors).toEqual([]);
});

test("a catch on an atlas.* name that is no event is warned about", async ({ page }) => {
  await mount(page);
  await openSignal(page, "Catch_stray");
  await expect(page.locator("#f-sigevent .hint.warn")).toContainText(
    "atlas emits no event named atlas.user.vanished; this element would wait for something that never comes.");
  expect(page.__errors).toEqual([]);
});

test("a throw is offered no events and is told the name is atlas's own", async ({ page }) => {
  await mount(page);
  await openSignal(page, "Throw_1");
  await page.waitForFunction(() => window.__catalogCalls > 0);
  await expect(page.locator("#f-sigevent .hint.warn")).toContainText(
    "atlas.user.requested is a name atlas emits: a model that throws it speaks for atlas.");
  await expect(eventGroup(page)).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("renaming the signal redraws the note for the new name", async ({ page }) => {
  await mount(page);
  await openSignal(page, "Catch_stray");
  await expect(page.locator("#f-sigevent .hint.warn")).toHaveCount(1);
  await page.locator("#f-signame").fill("atlas.user.created");
  await page.locator("#f-signame").blur();
  await expect(page.locator("#f-sigevent [data-event]")).toHaveAttribute("data-event", "atlas.user.created");
  await page.locator("#f-signame").fill("kunde.angelegt");
  await page.locator("#f-signame").blur();
  await expect(page.locator("#f-sigevent")).toBeEmpty();
  expect(page.__errors).toEqual([]);
});

test("the catalogue is asked for once, however many elements are opened", async ({ page }) => {
  await mount(page);
  await openSignal(page, "Start_sig");
  await expect(eventGroup(page).locator("option")).toHaveCount(2);
  await openSignal(page, "Catch_req");
  await expect(page.locator("#f-sigevent [data-event]")).toHaveCount(1);
  await openSignal(page, "Throw_1");
  await expect(page.locator("#f-sigevent .hint.warn")).toHaveCount(1);
  expect(await page.evaluate(() => window.__catalogCalls)).toBe(1);
  expect(page.__errors).toEqual([]);
});

test("a catalogue that cannot be read leaves the picker as it was", async ({ page }) => {
  await page.evaluate(() => { window.__failCatalog = true; });
  await mount(page);
  await openSignal(page, "Start_sig");
  await page.waitForFunction(() => window.__catalogCalls > 0);
  await expect(eventGroup(page)).toHaveCount(0);
  await expect(page.locator('#f-sigref option[value="__new__"]')).toHaveCount(1);
  await expect(page.locator('#f-sigref option[value="Signal_req"]')).toHaveCount(1);
  expect(page.__errors).toEqual([]);
});
