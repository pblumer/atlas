// End-to-end coverage for the catalogued events in the Modeler's signal picker
// (api/web/editor.js, ADR-0435 §5). Driven through the real vendored bpmn-js: an element
// that waits for a signal is offered the events atlas emits that a model may listen to,
// picking one declares or reuses a signal of that name, the chosen event says what it
// carries with its personal data named, and an atlas.* name that is no event, or that a
// model throws, is warned about. The signals the deployed models throw are offered too,
// and a throw is told who receives its name (ADR-0447 §6).
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
  if (!(await picker.isVisible())) await page.locator('.pgroup[data-group*="Signal" i] > .pgroup-head').first().click();
  await expect(picker).toBeVisible();
}

const eventGroup = (page) => page.locator("#f-sigref optgroup#f-sig-events");

test("a signal start is offered the events a model may listen to, and only those", async ({ page }) => {
  await mount(page);
  await openSignal(page, "Start_sig");
  await expect(eventGroup(page)).toHaveAttribute("label", "Ereignisse, die atlas aussendet");
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
  await expect(page.locator("#f-sigevent")).not.toContainText("Personendaten");
  await expect(page.locator("#f-sigevent")).not.toContainText("Administrator");
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
  await expect(page.locator("#f-sigevent")).toContainText("Wer darauf hört, erhält Personendaten: vorname, email.");
  // The deploy rule that follows from it (ADR-0435 §6) is said where the event is chosen.
  await expect(page.locator("#f-sigevent")).toContainText("Nur ein Administrator darf ein Modell deployen, das darauf hört.");
  await expect(page.locator('#f-sigevent a[href="/#/console/events"]')).toHaveCount(1);
  expect(page.__errors).toEqual([]);
});

test("a catch on an atlas.* name that is no event is warned about", async ({ page }) => {
  await mount(page);
  await openSignal(page, "Catch_stray");
  await expect(page.locator("#f-sigevent .hint.warn")).toContainText(
    "atlas sendet kein Ereignis namens atlas.user.vanished aus; dieses Element würde auf etwas warten, das nie kommt.");
  expect(page.__errors).toEqual([]);
});

test("a throw is offered no events and is told the name is atlas's own", async ({ page }) => {
  await mount(page);
  await openSignal(page, "Throw_1");
  await page.waitForFunction(() => window.__catalogCalls > 0);
  await expect(page.locator("#f-sigevent .hint.warn")).toContainText(
    "atlas.user.requested ist ein Name, den atlas aussendet: Ein Modell, das ihn wirft, spricht für atlas.");
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

// The inventory of the signals the deployed models throw and receive (ADR-0447 §6): a
// receiving element is offered the names the newest versions throw, and a throwing one
// is told who receives its name.
const SIGNALS = {
  signals: [
    { name: "atlas.user.requested", points: [{ processId: "rogue", version: 1, newest: true, role: "throw" }], findings: [] },
    { name: "old.throw", points: [{ processId: "legacy", version: 1, newest: false, role: "throw" }], findings: [] },
    { name: "only.received", points: [{ processId: "waiter", version: 1, newest: true, role: "catch" }], findings: [] },
    { name: "order.cancelled", points: [
      { processId: "billing", version: 2, newest: true, role: "boundary", interrupting: true },
      { processId: "billing", version: 1, newest: false, role: "boundary", interrupting: true },
      { processId: "shop", version: 4, newest: true, role: "throw" },
      { processId: "stale", version: 1, newest: true, role: "catch", inactive: true },
    ], findings: [] },
  ],
};

const deployedGroup = (page) => page.locator("#f-sigref optgroup#f-sig-deployed");

test("a signal start is offered the signals deployed models throw", async ({ page }) => {
  await page.evaluate((s) => { window.__SIGNALS = s; }, SIGNALS);
  await mount(page);
  await openSignal(page, "Start_sig");
  await expect(deployedGroup(page)).toHaveAttribute("label", "Signale, die deployte Modelle werfen");
  // An atlas.* name, a name only an older version throws and a name nobody throws are
  // not offered.
  const offered = await deployedGroup(page).locator("option").evaluateAll((os) =>
    os.map((o) => ({ value: o.value, text: o.textContent })));
  expect(offered).toEqual([{ value: "__event__:order.cancelled", text: "order.cancelled — shop — new" }]);

  await page.locator("#f-sigref").selectOption("__event__:order.cancelled");
  const xml = await page.evaluate(() => window.__xml());
  const sig = /<bpmn:signal id="([^"]+)" name="order\.cancelled"/.exec(xml);
  expect(sig).not.toBeNull();
  expect(xml).toMatch(new RegExp(`<bpmn:startEvent id="Start_sig"[\\s\\S]*?signalRef="${sig[1]}"`));
  expect(page.__errors).toEqual([]);
});

test("a throw is told who receives its name, or that nobody does", async ({ page }) => {
  await page.evaluate((s) => { window.__SIGNALS = s; }, SIGNALS);
  await mount(page);
  await openSignal(page, "Throw_1");
  const line = page.locator("#f-sigreceivers");
  // An atlas.* name is atlas's: the note says so, and the line says nothing.
  await expect(page.locator("#f-sigevent .hint.warn")).toHaveCount(1);
  await expect(line).toBeEmpty();

  // The newest, active receivers count: billing's v2, not its v1, and not the inactive one.
  await page.locator("#f-signame").fill("order.cancelled");
  await page.locator("#f-signame").blur();
  await expect(line).toContainText("Empfangen von 1 deployten Prozess: billing.");
  await expect(line.locator('a[href="/#/console/events?name=order.cancelled&channel=signal"]')).toHaveCount(1);

  await page.locator("#f-signame").fill("nobody.hears");
  await page.locator("#f-signame").blur();
  await expect(line).toContainText("Kein deployter Prozess empfängt es");
  expect(page.__errors).toEqual([]);
});

test("a server without the inventory leaves the picker as it was", async ({ page }) => {
  await mount(page);
  await openSignal(page, "Start_sig");
  await expect(eventGroup(page).locator("option")).toHaveCount(2);
  await expect(deployedGroup(page)).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});
