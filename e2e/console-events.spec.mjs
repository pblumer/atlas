// End-to-end coverage for the Console's Events page (ADR-0435 §7). Drives the REAL
// page against a mocked /api/v1: everybody with modeler or above reads the catalogue's
// entries; who listens now is a second, administrator-only route, so a modeler's page
// has no "listening now" column at all, while an administrator's counts the listeners,
// names them per event with the personal data each receives, and warns about models
// that wait for an atlas.* name atlas never emits. The page's second section lists the
// installation's own signals and messages with their findings (ADR-0447, ADR-0446).
// The Go suite decides what the routes answer; this checks the wiring.
import { test, expect } from "@playwright/test";

const field = (name, personal, always = true) => ({ name, type: "string", always, data: personal ? "personal" : "not-personal", meaning: { en: `the ${name}`, de: name } });

const CATALOG = {
  entries: [
    { type: "atlas.order.placed", kind: "domain", channels: ["message"], since: "0.7.0", stability: "stable",
      meaning: { en: "An order was placed.", de: "Eine Bestellung wurde aufgegeben." },
      moment: { places: [{ process: "atlas-auftrag-erfuellung", element: "Start" }], producer: "the shop's order route" },
      payload: [field("orderId", false)], neverSecret: "TestTheOrderMessagesCarryNoSecret",
      access: { message: "Atlas's own fulfilment process" } },
    { type: "atlas.entitlement.granted", kind: "platform", channels: ["feed"], since: "Unreleased", stability: "stable",
      serviceCatalogue: true,
      meaning: { en: "A right was granted.", de: "Ein Recht wurde erteilt." },
      moment: { producer: "the entitlement store" },
      payload: [field("principal", false), field("until", false, false)], neverSecret: "TestTheFeedCarriesNoSecret",
      access: { feed: "feedreader, or an events token" } },
    { type: "<message>.<outcome>", shaped: true, kind: "domain", channels: ["feed"], since: "Unreleased", stability: "stable",
      serviceCatalogue: true,
      meaning: { en: "An action ended.", de: "Eine Aktion endete." }, moment: { producer: "the outcome route" },
      payload: [], neverSecret: "TestTheFeedCarriesNoSecret", access: { feed: "feedreader, or an events token" } },
    { type: "atlas.user.requested", kind: "domain", channels: ["signal"], since: "Unreleased", stability: "experimental",
      listenable: true,
      meaning: { en: "Somebody asked for a <user> account.", de: "Jemand hat ein Benutzerkonto beantragt." },
      moment: { places: [{ process: "proc_benutzer_aufnahme", element: "beantragt_melden" }], producer: "the intake process" },
      payload: [field("atlasInstance", false), field("vorname", true), field("email", true)],
      neverSecret: "TestSystemIntakeAnnouncesTheRequestAsASignal", access: { signal: "a deployed model with a signal start or catch" } },
    { type: "atlas.incident.raised", kind: "platform", channels: ["feed"], since: "Unreleased", stability: "experimental",
      meaning: { en: "A token parked on an element.", de: "Ein Token blieb stehen." },
      moment: { producer: "the incident record" },
      payload: [field("elementInstanceKey", false)], neverSecret: "TestTheFeedCarriesNoSecret",
      access: { feed: "feedreader, or an events token without a reach" } },
  ],
};

const LISTENERS = (catalogueWithheld) => ({
  processes: [
    { type: "atlas.order.placed", channel: "message", processId: "atlas-auftrag-erfuellung", processName: "Auftrag erfüllen",
      version: 3, definitionKey: 11, system: true, element: "Start", role: "start", catalogued: true, personal: [] },
    { type: "atlas.user.requested", channel: "signal", processId: "hr-notify", processName: "HR benachrichtigen",
      version: 2, definitionKey: 12, projectId: "p-hr", element: "Start_req", role: "start", catalogued: true,
      personal: ["vorname", "email"] },
    { type: "atlas.user.vanished", channel: "signal", processId: "hr-wait", processName: "Wartet ewig",
      version: 1, definitionKey: 13, projectId: "p-hr", element: "Catch_gone", role: "catch", catalogued: false },
  ],
  feed: [
    { subscriptionId: "fs-1", workerId: "w-billing", workerName: "billing", reach: [], enabled: true },
    { subscriptionId: "fs-2", workerId: "w-hr", workerName: "hr-cmdb", reach: ["cat-hr"], enabled: true },
  ],
  feedTypes: ["atlas.entitlement.granted", "<message>.<outcome>", "atlas.incident.raised"],
  catalogueWithheld,
});

// The installation's own signals and messages (ADR-0447, ADR-0446), as the two
// inventory routes answer them.
const SIGNALS = {
  signals: [
    { name: "order.cancelled", findings: [{ rule: "signal.crosses-projects", message: "Thrown in project p-sales and received in project p-billing. An interrupting receiver there (billing v2) cancels the work it guards." }],
      points: [
        { processId: "billing", processName: "Billing", version: 2, newest: true, definitionKey: 21, projectId: "p-billing", elementId: "Stop", role: "boundary", interrupting: true },
        { processId: "billing", processName: "Billing", version: 1, newest: false, definitionKey: 20, projectId: "p-billing", elementId: "Stop", role: "boundary", interrupting: true },
        { processId: "shop", processName: "Shop", version: 4, newest: true, definitionKey: 30, projectId: "p-sales", elementId: "Cancel", role: "throw" },
        { processId: "legacy", processName: "Legacy", version: 1, newest: false, definitionKey: 10, elementId: "Wait", role: "catch" },
      ] },
    { name: "typo.sent", findings: [{ rule: "signal.unreceived", message: "Thrown by shop v4, and no newest, active definition receives it." }],
      points: [{ processId: "shop", processName: "Shop", version: 4, newest: true, definitionKey: 30, projectId: "p-sales", elementId: "Typo", role: "throw" }] },
    { name: "spaced ", findings: [{ rule: "signal.unreceived", message: "Thrown by shop v4, and no newest, active definition receives it." }],
      points: [{ processId: "shop", version: 4, newest: true, definitionKey: 30, elementId: "Say", role: "throw" }] },
    { name: "quiet.one", findings: [],
      points: [
        { processId: "a", version: 1, newest: true, definitionKey: 40, elementId: "T", role: "throw" },
        { processId: "b", version: 1, newest: true, definitionKey: 41, elementId: "C", role: "catch" },
      ] },
  ],
};
const MESSAGES = {
  messages: [
    { name: "invoice.ready", findings: [],
      sources: [
        { sourceKind: "inbound-watch", messageName: "invoice.ready", connectorName: "Ledger", kind: "clio", description: "stream invoices", enabled: true },
        { sourceKind: "process", messageName: "invoice.ready", processId: "billing", processName: "Billing", version: 2, newest: true, definitionKey: 21, projectId: "p-billing", elementId: "Go", element: "start", role: "start", enabled: true },
      ] },
    { name: "lost.msg", findings: [{ rule: "message.unreceived", message: "Sent by shop v4, and no newest, active definition waits for it." }],
      sources: [
        { sourceKind: "process-throw", messageName: "lost.msg", processId: "shop", processName: "Shop", version: 4, newest: true, definitionKey: 30, projectId: "p-sales", elementId: "Lost", element: "throw", role: "throw", enabled: true },
      ] },
  ],
};

// A message whose name has hyphens in it, sent by a product action whose product name is
// a paragraph: beside that column the name used to break over two lines at its hyphens.
const LONG_MESSAGES = {
  messages: [
    { name: "laptop-apple-hw.deprovision", findings: [],
      sources: [
        { sourceKind: "product-action", messageName: "laptop-apple-hw.deprovision", action: "deprovision", productId: "laptop",
          productName: "Hardware MacBook Pro 14 Zoll: Apple M5 (10-Core CPU, 10-Core GPU), 24 GB gemeinsamer Arbeitsspeicher, 512 GB SSD, 14,2 Zoll Liquid Retina XDR (3024 x 1964)",
          effect: "deprovision", enabled: true },
        { sourceKind: "process", messageName: "laptop-apple-hw.deprovision", processId: "proc_laptop_apple_hw_strang", version: 1, newest: true,
          definitionKey: 50, elementId: "Go", element: "start", role: "start", enabled: true },
      ] },
  ],
};

// The page is German unless English was chosen (ADR-0267). These tests check the
// wiring in English, the one remembered choice installMock makes for them; the tests
// of the page's language pass lang: null and read it as a viewer who chose nothing.
function installMock(page, { admin, catalogueWithheld = false, own = false, messages = MESSAGES, lang = "en" }) {
  if (lang) page.addInitScript((l) => { try { localStorage.setItem("atlas.locale", l); } catch { /* none */ } }, lang);
  const calls = [];
  page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    calls.push(path);
    if (path.endsWith("/auth/me")) {
      return route.fulfill({ json: { authEnabled: true, user: { id: "u1", username: "mo", roles: [admin ? "admin" : "modeler"] } } });
    }
    if (own && path.endsWith("/signals")) return route.fulfill({ json: SIGNALS });
    if (own && path.endsWith("/message-names")) return route.fulfill({ json: messages });
    if (path.endsWith("/event-catalog")) return route.fulfill({ json: CATALOG });
    if (path.endsWith("/event-catalog/listeners")) {
      return admin
        ? route.fulfill({ json: LISTENERS(catalogueWithheld) })
        : route.fulfill({ status: 403, json: { error: "forbidden: requires role admin" } });
    }
    return route.fulfill({ json: [] });
  });
  return calls;
}

async function openEvents(page) {
  await page.goto("/index.html#/console/events");
  await expect(page.locator("#ev-rows tr[data-i]").first()).toBeVisible({ timeout: 15000 });
}

test.beforeEach(async ({ page }) => {
  const errors = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.__errors = errors;
});

test("a modeler reads every entry and has no listening-now column", async ({ page }) => {
  const calls = installMock(page, { admin: false });
  await openEvents(page);
  await expect(page.locator("#ev-rows tr[data-i]")).toHaveCount(5);
  await expect(page.locator("#ev-head th")).toHaveCount(6);
  await expect(page.locator("#ev-head")).not.toContainText("Listening now");
  await expect(page.locator(".ev-count")).toHaveCount(0);
  // The shape is marked as one, and an experimental entry says so.
  await expect(page.locator('#ev-rows tr[data-type="<message>.<outcome>"]')).toContainText("shape");
  await expect(page.locator('#ev-rows tr[data-type="atlas.user.requested"] .pill.warn')).toHaveText("experimental");
  // The listeners route was asked and refused; that refusal is the answer.
  expect(calls).toContain("/api/v1/event-catalog/listeners");
  await expect(page.locator(".ev-stray")).toHaveCount(0);

  await page.locator('#ev-rows tr[data-type="atlas.user.requested"]').click();
  const detail = page.locator(".ev-detail");
  await expect(detail).toContainText("Somebody asked for a <user> account.");
  // Where it is thrown: each place, process and element.
  await expect(detail).toContainText("proc_benutzer_aufnahme at beantragt_melden — the intake process");
  await expect(detail.locator("tr", { hasText: "vorname" }).locator(".pill.warn")).toHaveText("personal");
  await expect(detail.locator("tr", { hasText: "atlasInstance" }).locator(".pill")).toHaveCount(0);
  await expect(detail).toContainText("TestSystemIntakeAnnouncesTheRequestAsASignal");
  await expect(detail.locator(".ev-listeners")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("the Console's navigation offers the page to a modeler", async ({ page }) => {
  installMock(page, { admin: false });
  await openEvents(page);
  await expect(page.locator('a[href="#/console/events"]').first()).toBeAttached();
  expect(page.__errors).toEqual([]);
});

test("an administrator sees who listens, per event, with the personal data each receives", async ({ page }) => {
  installMock(page, { admin: true });
  await openEvents(page);
  await expect(page.locator("#ev-head th")).toHaveCount(7);
  await expect(page.locator("#ev-head")).toContainText("Listening now");
  const count = (type) => page.locator(`#ev-rows tr[data-type="${type}"] .ev-count`);
  await expect(count("atlas.order.placed")).toHaveText("1");
  await expect(count("atlas.user.requested")).toHaveText("1");
  // A subscription receives every catalogue type its reach allows; a fact of the
  // engine reaches only the subscription narrowed to no catalogue.
  await expect(count("atlas.entitlement.granted")).toHaveText("2");
  await expect(count("<message>.<outcome>")).toHaveText("2");
  await expect(count("atlas.incident.raised")).toHaveText("1");

  await page.locator('#ev-rows tr[data-type="atlas.user.requested"]').focus();
  await page.keyboard.press("Enter");
  const listeners = page.locator(".ev-detail .ev-listeners");
  await expect(listeners).toContainText("HR benachrichtigen");
  await expect(listeners).toContainText("v2");
  await expect(listeners).toContainText("p-hr");
  await expect(listeners).toContainText("Start_req (start)");
  await expect(listeners).toContainText("vorname, email");
  await expect(page.locator('#ev-rows tr[data-type="atlas.user.requested"]')).toHaveClass(/sel/);

  await page.keyboard.press("Escape");
  await page.locator('#ev-rows tr[data-type="atlas.order.placed"]').click();
  await expect(page.locator(".ev-detail .ev-listeners .pill", { hasText: "system" })).toHaveCount(1);
  expect(page.__errors).toEqual([]);
});

test("an administrator is warned about a model waiting for a name atlas never emits", async ({ page }) => {
  installMock(page, { admin: true });
  await openEvents(page);
  const stray = page.locator(".ev-stray");
  await expect(stray).toContainText("Waiting for names atlas never emits.");
  await expect(stray).toContainText("atlas.user.vanished");
  await expect(stray).toContainText("Wartet ewig v1");
  expect(page.__errors).toEqual([]);
});

test("a feed entry lists the subscriptions it reaches", async ({ page }) => {
  installMock(page, { admin: true });
  await openEvents(page);
  await page.locator('#ev-rows tr[data-type="atlas.entitlement.granted"]').click();
  let listeners = page.locator(".ev-detail .ev-listeners");
  await expect(listeners).toContainText("No deployed model listens to this name.");
  await expect(listeners).toContainText("billing");
  await expect(listeners).toContainText("the whole feed");
  await expect(listeners).toContainText("hr-cmdb");
  await expect(page.locator(".ev-detail")).toContainText("Part of the service catalogue");

  await page.keyboard.press("Escape");
  await page.locator('#ev-rows tr[data-type="atlas.incident.raised"]').click();
  listeners = page.locator(".ev-detail .ev-listeners");
  await expect(listeners).toContainText("billing");
  await expect(listeners).not.toContainText("hr-cmdb");
  await expect(page.locator(".ev-detail")).not.toContainText("Part of the service catalogue");
  expect(page.__errors).toEqual([]);
});

test("with the shop switched off the catalogue's events are withheld and the engine's still flow", async ({ page }) => {
  installMock(page, { admin: true, catalogueWithheld: true });
  await openEvents(page);
  const count = (type) => page.locator(`#ev-rows tr[data-type="${type}"] .ev-count`);
  await expect(count("atlas.entitlement.granted")).toHaveText("0");
  await expect(count("atlas.incident.raised")).toHaveText("1");
  await page.locator('#ev-rows tr[data-type="atlas.entitlement.granted"]').click();
  await expect(page.locator(".ev-detail .ev-withheld")).toContainText("the feed passes over this event");
  await page.keyboard.press("Escape");
  await page.locator('#ev-rows tr[data-type="atlas.incident.raised"]').click();
  await expect(page.locator(".ev-detail .ev-withheld")).toHaveCount(0);
  await expect(page.locator(".ev-detail .ev-listeners")).toContainText("billing");
  expect(page.__errors).toEqual([]);
});

test("the installation's own signals and messages are listed beside the catalogue, with their findings", async ({ page }) => {
  installMock(page, { admin: false, own: true });
  await openEvents(page);
  const rows = page.locator("#own-rows tr[data-own]");
  await expect(rows).toHaveCount(6);
  // The catalogue is the first list and stays as it was: an own name is never in it.
  await expect(page.locator("#ev-rows tr[data-i]")).toHaveCount(5);
  await expect(page.locator('#ev-rows tr[data-type="order.cancelled"]')).toHaveCount(0);

  const row = (name) => page.locator(`#own-rows tr[data-name="${name}"]`);
  await expect(row("order.cancelled")).toContainText("crosses projects");
  await expect(row("typo.sent")).toContainText("nobody receives it");
  await expect(row("lost.msg")).toContainText("nobody waits for it");
  // A process only an older version of which receives is named with that version; one
  // whose newest version does, by its name alone.
  await expect(row("order.cancelled")).toContainText("billing, legacy (v1)");
  await expect(row("invoice.ready")).toContainText("watch of Ledger");

  await page.locator("#own-channel").selectOption("message");
  await expect(rows).toHaveCount(2);
  await page.locator("#own-findings").check();
  await expect(rows).toHaveCount(1);
  await expect(row("lost.msg")).toHaveCount(1);
  await page.locator("#own-channel").selectOption("");
  await expect(rows).toHaveCount(4);
  await page.locator("#own-findings").uncheck();
  // A name with a space at its end is another name, and the row says so.
  await expect(page.locator('#own-rows tr[data-name="spaced "]')).toContainText('"spaced "');
  await expect(page.locator('#own-rows tr[data-name="spaced "] .pill.warn', { hasText: "space" })).toHaveCount(1);

  await row("order.cancelled").click();
  const detail = page.locator(".own-detail");
  await expect(detail).toContainText("signal.crosses-projects");
  await expect(detail).toContainText("An interrupting receiver there (billing v2) cancels the work it guards.");
  await expect(detail).toContainText("boundary, interrupting");
  await expect(detail.locator('a[href="#/operations/p/21"]')).toHaveCount(1);
  await expect(detail).toContainText("Only a model throws a signal");
  // What was sent under the name is the event log's, filtered to it.
  await expect(detail.locator('a.own-log-link')).toHaveAttribute("href", "#/operations/event-log?name=order.cancelled&channel=signal");
  await expect(row("order.cancelled")).toHaveClass(/sel/);

  await page.keyboard.press("Escape");
  await row("invoice.ready").focus();
  await page.keyboard.press("Enter");
  await expect(detail).toContainText("Inbound watch of worker Ledger");
  await expect(detail).toContainText("never that nothing sends it");
  expect(page.__errors).toEqual([]);
});

test("a link from the Modeler opens the name it names", async ({ page }) => {
  installMock(page, { admin: false, own: true });
  await page.goto("/index.html#/console/events?name=lost.msg&channel=message");
  await expect(page.locator(".own-detail")).toContainText("lost.msg");
  await expect(page.locator(".own-detail")).toContainText("message.unreceived");
  await expect(page.locator("#own-channel")).toHaveValue("message");
  expect(page.__errors).toEqual([]);
});

test("a server without the inventories says so in the second section and keeps the first", async ({ page }) => {
  installMock(page, { admin: false });
  await openEvents(page);
  await expect(page.locator("#own-rows")).toContainText("No deployed model throws, sends or receives a signal or a message of its own.");
  await expect(page.locator("#ev-rows tr[data-i]")).toHaveCount(5);
  expect(page.__errors).toEqual([]);
});

test("a detail opens as a dialog over the list, and Escape returns to the row it was opened from", async ({ page }) => {
  installMock(page, { admin: false, own: true });
  await openEvents(page);
  // The last section's rows sit below the fold of the page: the detail must open where
  // the person is looking, not further down still.
  const row = page.locator('#own-rows tr[data-name="lost.msg"]');
  await row.click();
  const dialog = page.getByRole("dialog", { name: "lost.msg" });
  await expect(dialog).toBeVisible();
  await expect(dialog).toBeInViewport();
  await expect(dialog).toContainText("message.unreceived");
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(row).toBeFocused();
  // The row stays marked as the one last opened.
  await expect(row).toHaveClass(/sel/);
  // The space bar opens it as Enter does, and its release does not close it again.
  await page.keyboard.press(" ");
  await expect(page.getByRole("dialog", { name: "lost.msg" })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(row).toBeFocused();

  // The close button closes it, and so does a click beside it.
  const entry = page.locator('#ev-rows tr[data-type="atlas.order.placed"]');
  await entry.click();
  await expect(page.getByRole("dialog", { name: "atlas.order.placed" })).toBeVisible();
  await page.getByRole("dialog").getByRole("button", { name: "Close", exact: true }).first().click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await entry.click();
  await expect(page.getByRole("dialog")).toHaveCount(1);
  await page.mouse.click(4, 4);
  await expect(page.getByRole("dialog")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("leaving the page closes the dialog it opened", async ({ page }) => {
  installMock(page, { admin: false, own: true });
  await openEvents(page);
  await page.locator('#own-rows tr[data-name="lost.msg"]').click();
  await expect(page.getByRole("dialog")).toHaveCount(1);
  // The dialog lives beside the page, not in it, so a navigation that re-renders the
  // page would otherwise leave it standing over whatever comes next.
  await page.evaluate(() => { location.hash = "#/console/events?channel=signal"; });
  await expect(page.locator("#own-channel")).toHaveValue("signal");
  await expect(page.getByRole("dialog")).toHaveCount(0);
  expect(page.__errors).toEqual([]);
});

test("each section folds away, says what it holds while folded, and stays folded on the next visit", async ({ page }) => {
  installMock(page, { admin: true, own: true });
  await openEvents(page);
  const catalogue = page.locator("details#ev-section");
  const own = page.locator("details#own-section");
  const isOpen = (d) => d.evaluate((el) => el.open);
  expect(await isOpen(catalogue)).toBe(true);
  expect(await isOpen(own)).toBe(true);
  // What must survive the fold is what the section warns about, so the summary says it.
  await expect(catalogue.locator("summary")).toContainText("5 events");
  await expect(catalogue.locator("summary")).toContainText("1 receiver waits for a name atlas never emits");
  await expect(own.locator("summary")).toContainText("6 names");
  await expect(own.locator("summary")).toContainText("4 with a finding");

  await own.locator("summary").click();
  await expect(page.locator("#own-rows")).toBeHidden();
  await catalogue.locator("summary").click();
  await expect(page.locator("#ev-rows")).toBeHidden();
  // The <details> toggle event persists the new state asynchronously.
  // A reload before that event has run tests a race, not persistence.
  await expect.poll(() => page.evaluate(() => localStorage.getItem("atlas.events.own"))).toBe("0");
  await expect.poll(() => page.evaluate(() => localStorage.getItem("atlas.events.catalog"))).toBe("0");

  await page.reload();
  await expect(page.locator("details#own-section summary")).toContainText("6 names");
  expect(await isOpen(page.locator("details#ev-section"))).toBe(false);
  expect(await isOpen(page.locator("details#own-section"))).toBe(false);

  await page.locator("details#ev-section summary").click();
  await expect.poll(() => page.evaluate(() => localStorage.getItem("atlas.events.catalog"))).toBe("1");
  await page.reload();
  await expect(page.locator("details#ev-section")).toHaveAttribute("open", "");
  await expect(page.locator("#ev-rows tr[data-i]").first()).toBeVisible();
  expect(await isOpen(page.locator("details#own-section"))).toBe(false);
  expect(page.__errors).toEqual([]);
});

test("a link from the Modeler unfolds the section it names", async ({ page }) => {
  installMock(page, { admin: false, own: true });
  await page.addInitScript(() => { try { localStorage.setItem("atlas.events.own", "0"); } catch { /* none */ } });
  await page.goto("/index.html#/console/events?name=lost.msg&channel=message");
  await expect(page.getByRole("dialog", { name: "lost.msg" })).toBeVisible();
  expect(await page.locator("details#own-section").evaluate((el) => el.open)).toBe(true);
  expect(page.__errors).toEqual([]);
});

test("a name stays on one line however long the column beside it", async ({ page }) => {
  installMock(page, { admin: false, own: true, messages: LONG_MESSAGES });
  await openEvents(page);
  // An inline element has one client rect per line it is laid out on.
  const lines = (loc) => loc.evaluate((el) => el.getClientRects().length);
  await expect(page.locator('#own-rows tr[data-name="laptop-apple-hw.deprovision"]')).toBeVisible();
  expect(await lines(page.locator('#own-rows tr[data-name="laptop-apple-hw.deprovision"] td:first-child code'))).toBe(1);
  expect(await lines(page.locator('#ev-rows tr[data-type="atlas.entitlement.granted"] td:first-child code'))).toBe(1);
  expect(page.__errors).toEqual([]);
});

test("an event's meaning reads in the Console's language: German by default, English when chosen", async ({ page }) => {
  installMock(page, { admin: false, lang: null });
  await openEvents(page);
  const meaning = page.locator('#ev-rows tr[data-type="atlas.order.placed"] td').nth(3);
  await expect(meaning).toHaveText("Eine Bestellung wurde aufgegeben.");
  await expect(meaning.locator('[lang="de"]')).toHaveCount(1);
  await page.locator('#ev-rows tr[data-type="atlas.user.requested"]').click();
  const detail = page.locator(".ev-detail");
  await expect(detail).toContainText("Jemand hat ein Benutzerkonto beantragt.");
  await expect(detail).not.toContainText("Somebody asked for a <user> account.");

  await page.evaluate(() => localStorage.setItem("atlas.locale", "en"));
  await page.reload();
  await expect(page.locator("#ev-rows tr[data-i]").first()).toBeVisible({ timeout: 15000 });
  await expect(meaning).toHaveText("An order was placed.");
  await expect(meaning.locator('[lang="en"]')).toHaveCount(1);
  await page.locator('#ev-rows tr[data-type="atlas.user.requested"]').click();
  await expect(detail).toContainText("Somebody asked for a <user> account.");
  // A payload field's meaning follows the same choice.
  await expect(detail.locator("tr", { hasText: "atlasInstance" })).toContainText("the atlasInstance");
  expect(page.__errors).toEqual([]);
});

test("the page reads in German for a viewer who chose no language", async ({ page }) => {
  installMock(page, { admin: true, own: true, lang: null });
  await openEvents(page);
  await expect(page.locator("h1")).toHaveText("Ereignisse");
  await expect(page.locator("details#ev-section summary h2")).toHaveText("Ereignisse, die atlas aussendet");
  await expect(page.locator("details#own-section summary h2")).toHaveText("Signale und Nachrichten dieser Installation");
  await expect(page.locator("#ev-head th")).toHaveText(["Ereignis", "Art", "Kanäle", "Bedeutung", "Seit", "Stabilität", "Zuhörende"]);
  await expect(page.locator('#own-rows').locator("xpath=ancestor::table").locator("thead tr").first().locator("th"))
    .toHaveText(["Name", "Kanal", "Gesendet oder geworfen von", "Empfangen von", "Befunde"]);
  // The words for the server's ids: a kind, a stability, a shape, a finding.
  const entry = (type) => page.locator(`#ev-rows tr[data-type="${type}"]`);
  await expect(entry("atlas.order.placed")).toContainText("fachlich");
  await expect(entry("atlas.incident.raised")).toContainText("Plattform");
  await expect(entry("atlas.user.requested").locator(".pill.warn")).toHaveText("experimentell");
  await expect(entry("atlas.entitlement.granted")).toContainText("noch nicht veröffentlicht");
  await expect(entry("<message>.<outcome>")).toContainText("Namensmuster");
  await expect(page.locator('#own-rows tr[data-name="typo.sent"]')).toContainText("wird nicht empfangen");
  await expect(page.locator('#own-rows tr[data-name="invoice.ready"]')).toContainText("Watch von Ledger");
  await expect(page.locator("details#ev-section summary")).toContainText("5 Ereignisse");
  await expect(page.locator("details#ev-section summary")).toContainText("1 Empfänger wartet auf einen Namen, den atlas nie aussendet");
  await expect(page.locator("details#own-section summary")).toContainText("6 Namen");
  await expect(page.locator("details#own-section summary")).toContainText("4 mit Befund");
  await expect(page.locator("#own-channel option")).toHaveText(["Alle", "Signale", "Nachrichten"]);
  // The page says which language it is in, inside a Console whose document is English.
  await expect(page.locator("#ev-section").locator("xpath=ancestor::*[@lang][1]")).toHaveAttribute("lang", "de");

  await entry("atlas.user.requested").click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toHaveAttribute("lang", "de");
  await expect(dialog).toContainText("Nutzdaten");
  await expect(dialog).toContainText("Wer es empfangen darf");
  await expect(dialog).toContainText("Wer gerade zuhört");
  await expect(dialog).toContainText("Empfangene Personendaten");
  await expect(dialog.locator("tr", { hasText: "vorname" }).locator(".pill.warn")).toHaveText("Personendaten");
  // What the server writes in English stays English, and says so.
  await expect(dialog.locator('[lang="en"]', { hasText: "the intake process" })).toHaveCount(1);
  await expect(dialog.getByRole("button", { name: "Schliessen", exact: true })).toHaveCount(2);
  await page.keyboard.press("Escape");

  await page.locator('#own-rows tr[data-name="order.cancelled"]').click();
  await expect(dialog).toContainText("Geworfen von");
  await expect(dialog).toContainText("Empfangen von");
  await expect(dialog).toContainText("Boundary-Event, unterbrechend");
  await expect(dialog).toContainText("Nur ein Modell wirft ein Signal");
  await expect(dialog.locator("a.own-log-link")).toHaveText("Was unter diesem Namen gesendet wurde: das Event log");
  await page.keyboard.press("Escape");

  // No message the page asks for is missing: a missing one renders as its key.
  expect(await page.locator("main").innerText()).not.toMatch(/\b(events|common)\.[a-z]/);
  expect(page.__errors).toEqual([]);
});
